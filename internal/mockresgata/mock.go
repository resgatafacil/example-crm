package mockresgata

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
)

type Options struct {
	ClientID      string
	WebhookURL    string
	WebhookSecret string
	Speed         time.Duration
}

type Mock struct {
	mu      sync.Mutex
	opts    Options
	codes   map[string]authCode
	conn    *connection
	numbers []*number
	msgs    map[string]*message
	nextNum int
	Sent    []string
	client  *http.Client
}

type authCode struct {
	challenge   string
	redirectURI string
	externalRef string
}

type connection struct {
	ID          string
	Token       string
	ExternalRef string
	Status      string
}

type number struct {
	ID     int
	Name   string
	Phone  string
	Status string
}

type message struct {
	ID     string
	Number int
	Phone  string
	Text   string
	Status string
	Code   string
}

func New(opts Options) *Mock {
	if opts.Speed == 0 {
		opts.Speed = time.Second
	}
	return &Mock{opts: opts, codes: map[string]authCode{}, msgs: map[string]*message{}, client: &http.Client{Timeout: 5 * time.Second}}
}

func (m *Mock) SetWebhook(target, secret string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opts.WebhookURL = target
	m.opts.WebhookSecret = secret
}

func randomID(prefix string, n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return prefix + hex.EncodeToString(raw)
}

func (m *Mock) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/sandbox/authorize", m.authorize)
	mux.HandleFunc("POST /oauth/token", m.token)
	mux.HandleFunc("POST /connections", m.createConnection)
	mux.HandleFunc("POST /connections/{id}/tokens", m.issueToken)
	mux.Handle("GET /connection", m.auth(m.getConnection))
	mux.Handle("DELETE /connection", m.auth(m.disconnect))
	mux.Handle("GET /connection/setup", m.auth(m.setup))
	mux.Handle("GET /numbers", m.auth(m.listNumbers))
	mux.Handle("POST /messages", m.auth(m.sendMessage))
	mux.Handle("GET /messages/{id}", m.auth(m.getMessage))
	mux.Handle("POST /sandbox/events", m.auth(m.fireEvent))
	mux.Handle("POST /sandbox/inbound-messages", m.auth(m.inbound))
	mux.Handle("POST /sandbox/opt-outs", m.auth(m.optOut))
	mux.Handle("POST /sandbox/numbers", m.auth(m.createNumber))
	mux.Handle("PATCH /sandbox/numbers/{id}", m.auth(m.patchNumber))
	mux.Handle("POST /sandbox/reset", m.auth(m.reset))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func apiError(w http.ResponseWriter, status int, kind, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": kind, "message": message}})
}

func (m *Mock) bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (m *Mock) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		ok := m.conn != nil && m.conn.Status == "active" && subtle.ConstantTimeCompare([]byte(m.bearer(r)), []byte(m.conn.Token)) == 1
		m.mu.Unlock()
		if !ok {
			apiError(w, http.StatusUnauthorized, "authentication_error", "Credencial ausente, inválida ou revogada.")
			return
		}
		next(w, r)
	})
}

func (m *Mock) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	if q.Get("client_id") != m.opts.ClientID || redirect == "" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "Pedido de autorização inválido.")
		return
	}
	target, err := url.Parse(redirect)
	if err != nil {
		apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "redirect_uri inválido.")
		return
	}
	values := target.Query()
	values.Set("state", q.Get("state"))
	if simulate := q.Get("simulate"); simulate != "" {
		values.Set("error", simulate)
	} else {
		code := randomID("code_", 12)
		m.mu.Lock()
		m.codes[code] = authCode{challenge: q.Get("code_challenge"), redirectURI: redirect, externalRef: q.Get("external_ref")}
		m.mu.Unlock()
		values.Set("code", code)
	}
	target.RawQuery = values.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (m *Mock) token(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)

	m.mu.Lock()
	code, found := m.codes[body["code"]]
	delete(m.codes, body["code"])
	valid := found && body["grant_type"] == "authorization_code" && body["client_id"] == m.opts.ClientID &&
		body["redirect_uri"] == code.redirectURI && resgata.Challenge(body["code_verifier"]) == code.challenge
	if !valid {
		m.mu.Unlock()
		apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "Código inválido, expirado ou verificador PKCE incorreto.")
		return
	}
	ref := code.externalRef
	if ref == "" {
		ref = randomID("ref_", 4)
	}
	m.conn = &connection{ID: randomID("conn_", 8), Token: randomID("mock_conn_", 24), ExternalRef: ref, Status: "active"}
	m.resetNumbersLocked()
	out := map[string]any{"access_token": m.conn.Token, "token_type": "Bearer", "scope": "numbers:read messages:send messages:read contacts:read", "connection_id": m.conn.ID, "external_ref": m.conn.ExternalRef}
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, out)
	m.emit("connection.created", map[string]any{"external_ref": ref, "scopes": []string{"numbers:read", "messages:send"}})
}

func (m *Mock) createConnection(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil && m.conn.Status == "active" && m.conn.ExternalRef == body["external_ref"] {
		apiError(w, http.StatusConflict, "external_ref_in_use", "Já existe uma conexão de sandbox com este external_ref.")
		return
	}
	m.conn = &connection{ID: randomID("conn_", 8), ExternalRef: body["external_ref"], Status: "active", Token: randomID("mock_conn_", 24)}
	m.resetNumbersLocked()
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": m.conn.ID, "object": "connection", "environment": "sandbox", "external_ref": m.conn.ExternalRef, "status": "active"}})
}

func (m *Mock) issueToken(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn == nil || m.conn.ID != r.PathValue("id") {
		apiError(w, http.StatusNotFound, "not_found", "Conexão não encontrada.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"secret": m.conn.Token}})
}

func (m *Mock) resetNumbersLocked() {
	m.numbers = []*number{{ID: 1, Name: "Número de teste", Phone: "5511900000001", Status: "connected"}}
	m.nextNum = 2
	m.msgs = map[string]*message{}
}

func (m *Mock) getConnection(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": m.conn.ID, "object": "connection", "environment": "sandbox", "external_ref": m.conn.ExternalRef, "status": "active"}})
}

func (m *Mock) disconnect(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	ref := m.conn.ExternalRef
	m.mu.Unlock()
	m.emit("connection.revoked", map[string]any{"external_ref": ref})
	m.mu.Lock()
	m.conn.Status = "revoked"
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"object": "connection", "status": "revoked"})
}

func (m *Mock) setup(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	connected := 0
	for _, n := range m.numbers {
		if n.Status == "connected" {
			connected++
		}
	}
	status := func(ok bool) string {
		if ok {
			return "complete"
		}
		return "incomplete"
	}
	steps := []map[string]any{
		{"id": "workspace", "title": "Conta Resgata Fácil", "status": "complete", "required": true, "action_url": nil},
		{"id": "number", "title": "Adicionar um número", "status": status(len(m.numbers) > 0), "required": true, "action_url": "https://app.resgatafacil.com/numeros"},
		{"id": "channel", "title": "Conectar um canal de envio", "status": status(connected > 0), "required": true, "action_url": "https://app.resgatafacil.com/numeros"},
		{"id": "meta_official", "title": "Vincular a Meta oficial (coexistência)", "status": status(connected > 0), "required": false, "action_url": "https://app.resgatafacil.com/meta-official"},
	}
	ready := len(m.numbers) > 0 && connected > 0
	writeJSON(w, http.StatusOK, map[string]any{"object": "connection_setup", "connection": m.conn.ID, "ready": ready, "steps": steps})
}

func (m *Mock) numberJSON(n *number) map[string]any {
	return map[string]any{"id": n.ID, "object": "number", "name": n.Name, "phone": n.Phone, "channel": "sandbox", "connected": n.Status == "connected", "status": n.Status}
}

func (m *Mock) listNumbers(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]any, 0, len(m.numbers))
	for _, n := range m.numbers {
		out = append(out, m.numberJSON(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (m *Mock) findNumber(id int) *number {
	for _, n := range m.numbers {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m *Mock) sendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To       string `json:"to"`
		Text     string `json:"text"`
		NumberID int    `json:"number_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	phone := digits(body.To)
	if phone == "" || strings.TrimSpace(body.Text) == "" {
		apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "Informe to (celular com DDD) e text.")
		return
	}

	m.mu.Lock()
	num := m.findNumber(body.NumberID)
	if body.NumberID == 0 && len(m.numbers) > 0 {
		num = m.numbers[0]
	}
	if num == nil {
		m.mu.Unlock()
		apiError(w, http.StatusNotFound, "not_found", "Número do sandbox não encontrado.")
		return
	}
	if num.Status != "connected" {
		m.mu.Unlock()
		apiError(w, http.StatusUnprocessableEntity, "number_not_connected", "O número do sandbox não está conectado.")
		return
	}
	msg := &message{ID: randomID("msg_", 8), Number: num.ID, Phone: phone, Text: body.Text, Status: "queued"}
	m.msgs[msg.ID] = msg
	m.mu.Unlock()

	writeJSON(w, http.StatusAccepted, map[string]any{"data": m.messageJSON(msg)})
	m.runScenario(msg)
}

func (m *Mock) messageJSON(msg *message) map[string]any {
	var code any
	if msg.Code != "" {
		code, _ = strconv.Atoi(msg.Code)
	}
	return map[string]any{"id": msg.ID, "object": "message", "direction": "outbound", "number_id": msg.Number, "phone": msg.Phone, "text": msg.Text, "status": msg.Status, "error_code": code, "created_at": time.Now().Unix()}
}

func (m *Mock) getMessage(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.msgs[r.PathValue("id")]
	if !ok {
		apiError(w, http.StatusNotFound, "not_found", "Mensagem não encontrada.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": m.messageJSON(msg)})
}

func (m *Mock) runScenario(msg *message) {
	step := m.opts.Speed
	switch msg.Phone {
	case "5500000000003":
		m.later(0, func() { m.advance(msg, "failed", "131049") })
	case "5500000000012":
	case "5500000000010":
		m.later(0, func() { m.advance(msg, "sent", "") })
	default:
		m.later(0, func() { m.advance(msg, "sent", "") })
		m.later(step, func() { m.advance(msg, "delivered", "") })
		m.later(2*step, func() { m.advance(msg, "read", "") })
		switch msg.Phone {
		case "5500000000030":
			m.later(3*step, func() {
				m.emit("contact.opted_out", map[string]any{"contact": map[string]any{"phone": msg.Phone}, "matched_phrase": "parar"})
			})
		case "5500000000031":
			m.later(3*step, func() { m.receive(msg.Phone, "Recebi sua mensagem!") })
		}
	}
}

func (m *Mock) later(d time.Duration, fn func()) {
	if d == 0 {
		go fn()
		return
	}
	time.AfterFunc(d, fn)
}

func (m *Mock) advance(msg *message, status, code string) {
	m.mu.Lock()
	msg.Status, msg.Code = status, code
	data := map[string]any{"message": map[string]any{"id": msg.ID, "channel": "sandbox", "to": msg.Phone, "status": status, "number_id": msg.Number}}
	m.mu.Unlock()
	if status == "failed" {
		data["error"] = map[string]any{"code": mustInt(code), "message": "Mensagem não entregue."}
	}
	m.emit("message."+status, data)
}

func mustInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (m *Mock) receive(phone, text string) {
	m.emit("message.received", map[string]any{"message": map[string]any{"id": randomID("in_", 6), "channel": "sandbox", "from": phone, "from_name": "", "type": "text", "text": text}})
}

func (m *Mock) fireEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type  string `json:"type"`
		Phone string `json:"phone"`
		Text  string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	known := false
	for _, t := range resgata.EventTypes {
		known = known || t == body.Type
	}
	if !known {
		apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "Tipo de evento desconhecido.")
		return
	}
	phone := digits(body.Phone)
	if phone == "" {
		phone = "5511999990000"
	}
	text := body.Text
	if text == "" {
		text = "Mensagem de teste do sandbox"
	}
	var data any = map[string]any{"test": true}
	switch {
	case body.Type == "message.received":
		data = map[string]any{"message": map[string]any{"id": randomID("in_", 6), "channel": "sandbox", "from": phone, "text": text}}
	case strings.HasPrefix(body.Type, "message."):
		status := strings.TrimPrefix(body.Type, "message.")
		payload := map[string]any{"message": map[string]any{"id": randomID("msg_", 6), "channel": "sandbox", "to": phone, "status": status, "number_id": 1}}
		if status == "failed" {
			payload["error"] = map[string]any{"code": 131026, "message": "Mensagem não entregável."}
		}
		data = payload
	case body.Type == "contact.opted_out":
		data = map[string]any{"contact": map[string]any{"phone": phone}, "matched_phrase": "parar"}
	case strings.HasPrefix(body.Type, "number."):
		data = map[string]any{"number": map[string]any{"id": 1, "name": "Número de teste", "phone": "5511900000001", "status": strings.TrimPrefix(body.Type, "number.")}, "source": "sandbox_event"}
	}
	m.emit(body.Type, data)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": "evt_mock", "type": body.Type})
}

func (m *Mock) inbound(w http.ResponseWriter, r *http.Request) {
	var body struct{ From, Text string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.receive(digits(body.From), body.Text)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (m *Mock) optOut(w http.ResponseWriter, r *http.Request) {
	var body struct{ Phone string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.emit("contact.opted_out", map[string]any{"contact": map[string]any{"phone": digits(body.Phone)}, "matched_phrase": "parar"})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (m *Mock) createNumber(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Status string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Status == "" {
		body.Status = "connected"
	}
	m.mu.Lock()
	n := &number{ID: m.nextNum, Name: body.Name, Phone: "55119" + strconv.Itoa(10000000+m.nextNum), Status: body.Status}
	m.nextNum++
	m.numbers = append(m.numbers, n)
	out := m.numberJSON(n)
	m.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (m *Mock) patchNumber(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	var body struct{ Status string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	n := m.findNumber(id)
	if n == nil {
		m.mu.Unlock()
		apiError(w, http.StatusNotFound, "not_found", "Número do sandbox não encontrado.")
		return
	}
	changed := n.Status != body.Status
	n.Status = body.Status
	out := m.numberJSON(n)
	m.mu.Unlock()
	if changed {
		kind := "number.disconnected"
		if body.Status == "connected" {
			kind = "number.connected"
		}
		m.emit(kind, map[string]any{"number": map[string]any{"id": n.ID, "name": n.Name, "phone": n.Phone, "status": body.Status}, "source": "sandbox_" + body.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (m *Mock) reset(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	m.resetNumbersLocked()
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Mock) emit(eventType string, data any) {
	m.mu.Lock()
	target, secret, ref, connID := m.opts.WebhookURL, m.opts.WebhookSecret, "", ""
	if m.conn != nil {
		ref, connID = m.conn.ExternalRef, m.conn.ID
	}
	m.mu.Unlock()
	if target == "" {
		return
	}

	id := randomID("evt_", 8)
	body, _ := json.Marshal(map[string]any{
		"id": id, "object": "event", "type": eventType, "api_version": "2026-11-01", "created": time.Now().Unix(), "environment": "sandbox",
		"connection": map[string]any{"id": connID, "external_ref": ref}, "data": data,
	})
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(resgata.SignatureHeader, resgata.SignatureHeaderValue(secret, time.Now().Unix(), body))
	req.Header.Set(resgata.EventIDHeader, id)
	req.Header.Set(resgata.DeliveryHeader, randomID("dlv_", 6))
	req.Header.Set(resgata.AttemptHeader, "1")
	res, err := m.client.Do(req)
	if err == nil {
		res.Body.Close()
	}
	m.mu.Lock()
	m.Sent = append(m.Sent, eventType)
	m.mu.Unlock()
}
