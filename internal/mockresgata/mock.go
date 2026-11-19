package mockresgata

import (
	"bytes"
	"crypto/rand"
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
	mu     sync.Mutex
	opts   Options
	codes  map[string]authCode
	conns  map[string]*connection
	links  map[string]metaLink
	Sent   []string
	client *http.Client
}

type authCode struct {
	challenge   string
	redirectURI string
	externalRef string
	meta        string
}

type metaLink struct {
	conn      *connection
	returnURL string
	result    string
}

type connection struct {
	ID          string
	Token       string
	ExternalRef string
	Status      string
	Meta        string
	numbers     []*number
	nextNum     int
	templates   []*template
	audiences   map[string]*audience
	campaigns   []*campaign
}

type connHandler func(w http.ResponseWriter, r *http.Request, c *connection)

type number struct {
	ID     int
	Name   string
	Phone  string
	Status string
}

func New(opts Options) *Mock {
	if opts.Speed == 0 {
		opts.Speed = time.Second
	}
	return &Mock{opts: opts, codes: map[string]authCode{}, conns: map[string]*connection{}, links: map[string]metaLink{}, client: &http.Client{Timeout: 5 * time.Second}}
}

func (m *Mock) SentTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.Sent...)
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
	mux.Handle("POST /connection/meta-link", m.auth(m.startMetaLink))
	mux.HandleFunc("GET /oauth/sandbox/meta-link", m.finishMetaLink)
	mux.Handle("GET /templates", m.auth(m.listTemplates))
	mux.Handle("POST /templates", m.auth(m.createTemplate))
	mux.Handle("GET /templates/{id}", m.auth(m.getTemplate))
	mux.Handle("POST /audiences", m.auth(m.createAudience))
	mux.Handle("GET /campaigns", m.auth(m.listCampaigns))
	mux.Handle("POST /campaigns", m.auth(m.createCampaign))
	mux.Handle("GET /campaigns/{id}", m.auth(m.getCampaign))
	mux.Handle("POST /campaigns/{id}/pause", m.auth(m.pauseCampaign))
	mux.Handle("POST /campaigns/{id}/resume", m.auth(m.resumeCampaign))
	mux.Handle("POST /sandbox/events", m.auth(m.fireEvent))
	mux.Handle("POST /sandbox/inbound-messages", m.auth(m.inbound))
	mux.Handle("POST /sandbox/opt-outs", m.auth(m.optOut))
	mux.Handle("POST /sandbox/numbers", m.auth(m.createNumber))
	mux.Handle("PATCH /sandbox/numbers/{id}", m.auth(m.patchNumber))
	mux.Handle("POST /sandbox/reset", m.auth(m.reset))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, http.StatusNotFound, "not_found", "Rota não encontrada.")
	})
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

func (m *Mock) auth(next connHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		c := m.conns[m.bearer(r)]
		if c != nil && c.Status != "active" {
			c = nil
		}
		m.mu.Unlock()
		if c == nil {
			apiError(w, http.StatusUnauthorized, "authentication_error", "Credencial ausente, inválida ou revogada.")
			return
		}
		next(w, r, c)
	})
}

func (m *Mock) newConnectionLocked(ref string) *connection {
	for token, existing := range m.conns {
		if existing.ExternalRef == ref && existing.Status == "active" {
			delete(m.conns, token)
		}
	}
	c := &connection{ID: randomID("conn_", 8), Token: randomID("mock_conn_", 24), ExternalRef: ref, Status: "active", Meta: metaLinked}
	c.resetLocked()
	m.conns[c.Token] = c
	return c
}

func (c *connection) resetLocked() {
	c.numbers = []*number{{ID: 1, Name: "Número de teste", Phone: "5511900000001", Status: "connected"}}
	c.nextNum = 2
	c.templates = nil
	c.audiences = map[string]*audience{}
	c.campaigns = nil
}

func (c *connection) findNumber(id int) *number {
	for _, n := range c.numbers {
		if n.ID == id {
			return n
		}
	}
	return nil
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
		meta := q.Get("meta")
		if meta == "" {
			meta = metaLinked
		}
		m.codes[code] = authCode{challenge: q.Get("code_challenge"), redirectURI: redirect, externalRef: q.Get("external_ref"), meta: meta}
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
	c := m.newConnectionLocked(ref)
	c.Meta = code.meta
	scopes := strings.Join(resgata.DefaultScopes, " ")
	out := map[string]any{"access_token": c.Token, "token_type": "Bearer", "scope": scopes, "connection_id": c.ID, "external_ref": c.ExternalRef}
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, out)
	m.emit(c, "connection.created", map[string]any{"external_ref": ref, "scopes": resgata.DefaultScopes})
}

func (m *Mock) createConnection(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.conns {
		if existing.Status == "active" && existing.ExternalRef == body["external_ref"] {
			apiError(w, http.StatusConflict, "external_ref_in_use", "Já existe uma conexão de sandbox com este external_ref.")
			return
		}
	}
	c := m.newConnectionLocked(body["external_ref"])
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": c.ID, "object": "connection", "environment": "sandbox", "external_ref": c.ExternalRef, "status": "active"}})
}

func (m *Mock) issueToken(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		if c.ID == r.PathValue("id") {
			writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"secret": c.Token}})
			return
		}
	}
	apiError(w, http.StatusNotFound, "not_found", "Conexão não encontrada.")
}

func (m *Mock) getConnection(w http.ResponseWriter, _ *http.Request, c *connection) {
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": c.ID, "object": "connection", "environment": "sandbox", "external_ref": c.ExternalRef, "status": "active"}})
}

func (m *Mock) disconnect(w http.ResponseWriter, _ *http.Request, c *connection) {
	m.emit(c, "connection.revoked", map[string]any{"external_ref": c.ExternalRef})
	m.mu.Lock()
	c.Status = "revoked"
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"object": "connection", "status": "revoked"})
}

func (m *Mock) setup(w http.ResponseWriter, _ *http.Request, c *connection) {
	m.mu.Lock()
	steps, ready := m.setupStepsLocked(c)
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"object": "connection_setup", "connection": c.ID, "ready": ready, "steps": steps})
}

func (m *Mock) numberJSON(n *number) map[string]any {
	return map[string]any{"id": n.ID, "object": "number", "name": n.Name, "phone": n.Phone, "channel": "sandbox", "connected": n.Status == "connected", "status": n.Status}
}

func (m *Mock) listNumbers(w http.ResponseWriter, _ *http.Request, c *connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]any, 0, len(c.numbers))
	for _, n := range c.numbers {
		out = append(out, m.numberJSON(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
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

func (m *Mock) later(d time.Duration, fn func()) {
	if d == 0 {
		go fn()
		return
	}
	time.AfterFunc(d, fn)
}

func mustInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (m *Mock) receive(c *connection, phone, text string) {
	m.emit(c, "message.received", map[string]any{"message": map[string]any{"id": randomID("in_", 6), "channel": "sandbox", "from": phone, "from_name": "", "type": "text", "text": text}})
}

func (m *Mock) fireEvent(w http.ResponseWriter, r *http.Request, c *connection) {
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
	case strings.HasPrefix(body.Type, "campaign."):
		data = map[string]any{"campaign": sampleCampaign(strings.TrimPrefix(body.Type, "campaign."))}
	case strings.HasPrefix(body.Type, "template."):
		status := map[string]string{"approved": "approved", "rejected": "rejected"}[strings.TrimPrefix(body.Type, "template.")]
		if status == "" {
			status = "paused"
		}
		data = map[string]any{"template": map[string]any{"id": "tpl_sample", "name": "modelo_de_teste", "category": "MARKETING", "language": "pt_BR", "status": status, "rejection_reason": nil}, "previous_status": "pending"}
	case strings.HasPrefix(body.Type, "number."):
		data = map[string]any{"number": map[string]any{"id": 1, "name": "Número de teste", "phone": "5511900000001", "status": strings.TrimPrefix(body.Type, "number.")}, "source": "sandbox_event"}
	}
	m.emit(c, body.Type, data)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": "evt_mock", "type": body.Type})
}

func (m *Mock) inbound(w http.ResponseWriter, r *http.Request, c *connection) {
	var body struct{ From, Text string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.receive(c, digits(body.From), body.Text)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (m *Mock) optOut(w http.ResponseWriter, r *http.Request, c *connection) {
	var body struct{ Phone string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.emit(c, "contact.opted_out", map[string]any{"contact": map[string]any{"phone": digits(body.Phone)}, "matched_phrase": "parar"})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (m *Mock) createNumber(w http.ResponseWriter, r *http.Request, c *connection) {
	var body struct{ Name, Status string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Status == "" {
		body.Status = "connected"
	}
	m.mu.Lock()
	n := &number{ID: c.nextNum, Name: body.Name, Phone: "55119" + strconv.Itoa(10000000+c.nextNum), Status: body.Status}
	c.nextNum++
	c.numbers = append(c.numbers, n)
	out := m.numberJSON(n)
	m.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (m *Mock) patchNumber(w http.ResponseWriter, r *http.Request, c *connection) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	var body struct{ Status string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	n := c.findNumber(id)
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
		m.emit(c, kind, map[string]any{"number": map[string]any{"id": n.ID, "name": n.Name, "phone": n.Phone, "status": body.Status}, "source": "sandbox_" + body.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (m *Mock) reset(w http.ResponseWriter, _ *http.Request, c *connection) {
	m.mu.Lock()
	c.resetLocked()
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Mock) emit(c *connection, eventType string, data any) {
	m.mu.Lock()
	target, secret, ref, connID := m.opts.WebhookURL, m.opts.WebhookSecret, c.ExternalRef, c.ID
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
