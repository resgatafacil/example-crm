package web

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/config"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/crypto"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/mockresgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

type env struct {
	t      *testing.T
	app    *httptest.Server
	mock   *mockresgata.Mock
	store  *store.Store
	secret string
	cfg    config.Config
	http   *http.Client
}

func randomString(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return "t" + string(bytes.Map(func(r rune) rune { return 'a' + r%26 }, raw))
}

func newEnv(t *testing.T, adminPassword string) *env {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Seed(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	secret := randomString(t)
	clientID := "app_" + randomString(t)
	mock := mockresgata.New(mockresgata.Options{ClientID: clientID, WebhookSecret: secret, Speed: 15 * time.Millisecond})
	mockServer := httptest.NewServer(mock.Handler())
	t.Cleanup(mockServer.Close)

	var handler http.Handler
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(app.Close)

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cfg := config.Config{
		PublicURL: app.URL, APIURL: mockServer.URL, AppURL: mockServer.URL, ClientID: clientID, WebhookSecret: secret,
		Environment: config.EnvSandbox, RateLimit: 100, AdminPassword: adminPassword, EncryptionKey: key,
	}
	box, err := crypto.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, Backend{Local: st}, box, resgata.NewClient(cfg.APIURL, nil))
	if err != nil {
		t.Fatal(err)
	}
	handler = server.Handler()
	mock.SetWebhook(app.URL+"/webhooks/resgata", secret)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &env{t: t, app: app, mock: mock, store: st, secret: secret, cfg: cfg, http: client}
}

func (e *env) do(method, path string, form url.Values, headers map[string]string) *http.Response {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = e.app.URL + path
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.http.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func readBody(res *http.Response) string {
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return string(raw)
}

func (e *env) connect() {
	e.t.Helper()
	e.connectWith("")
}

func (e *env) connectWith(extraQuery string) {
	e.t.Helper()
	res := e.do(http.MethodGet, "/resgata/conectar", nil, nil)
	authorize := res.Header.Get("Location") + extraQuery
	res.Body.Close()
	if !strings.Contains(authorize, "/oauth/sandbox/authorize") || !strings.Contains(authorize, "code_challenge_method=S256") {
		e.t.Fatalf("unexpected authorize redirect %q", authorize)
	}
	res = e.do(http.MethodGet, authorize, nil, nil)
	callback := res.Header.Get("Location")
	res.Body.Close()
	res = e.do(http.MethodGet, callback, nil, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("callback status %d", res.StatusCode)
	}
	conn, found, _ := e.store.Connection()
	if !found || conn.State != store.StateConnected {
		e.t.Fatalf("not connected: %+v", conn)
	}
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *env) lead(phone string) store.Lead {
	e.t.Helper()
	lead, ok, _ := e.store.LeadByPhone(phone)
	if !ok {
		e.t.Fatalf("lead %s not found", phone)
	}
	return lead
}

var templateIDPattern = regexp.MustCompile(`value="(tpl_[a-z0-9]+)"`)

func (e *env) createTemplate(name, body, examples string) *http.Response {
	e.t.Helper()
	return e.do(http.MethodPost, "/modelos", url.Values{"nome": {name}, "categoria": {"MARKETING"}, "corpo": {body}, "exemplos": {examples}}, nil)
}

func (e *env) approvedTemplateID() string {
	e.t.Helper()
	res := e.createTemplate("boas_vindas", "Oi {{1}}, tudo bem?", "Maria")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("template status %d", res.StatusCode)
	}
	var id string
	eventually(e.t, "an approved template", func() bool {
		match := templateIDPattern.FindStringSubmatch(readBody(e.do(http.MethodGet, "/campanhas/nova", nil, nil)))
		if match == nil {
			return false
		}
		id = match[1]
		return true
	})
	return id
}

func (e *env) campaignTo(phone string) (store.Lead, *http.Response) {
	e.t.Helper()
	lead := e.lead(phone)
	res := e.do(http.MethodPost, "/campanhas", url.Values{"modelo": {e.approvedTemplateID()}, "lead": {lead.ID}, "nome": {"Teste"}, "var_1": {"nome"}}, nil)
	return lead, res
}

func (e *env) sendTo(phone string) store.Lead {
	e.t.Helper()
	lead, res := e.campaignTo(phone)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/campanhas/cmp_") {
		e.t.Fatalf("campaign status %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
	return lead
}

func (e *env) campaignWhere(check func(store.Campaign) bool) (store.Campaign, bool) {
	list, _ := e.store.Campaigns(10)
	for _, c := range list {
		if check(c) {
			return c, true
		}
	}
	return store.Campaign{}, false
}

func (e *env) outboundStatus(leadID string) (string, string) {
	entries, _ := e.store.Timeline(leadID)
	for _, entry := range entries {
		if entry.Kind == store.KindMessageOut {
			return entry.Status, entry.ErrorCode
		}
	}
	return "", ""
}

func TestOAuthSendAndWebhookRoundTrip(t *testing.T) {
	e := newEnv(t, "")
	e.connect()

	conn, _, _ := e.store.Connection()
	if conn.TokenEncrypted == "" || strings.HasPrefix(conn.TokenEncrypted, "mock_conn_") {
		t.Fatal("the connection token must be stored encrypted")
	}

	lead := e.sendTo("+5519991230001")
	eventually(t, "the message to be read", func() bool {
		status, _ := e.outboundStatus(lead.ID)
		return status == "read"
	})
	eventually(t, "connection.created to be recorded", func() bool {
		events, _ := e.store.Events("connection.created", 10)
		return len(events) == 1 && events[0].SignatureOK
	})
}

func TestFailedMessageShowsCode(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	lead := e.sendTo("+5500000000003")
	eventually(t, "the failure", func() bool {
		status, code := e.outboundStatus(lead.ID)
		return status == "failed" && code == "131049"
	})
	page := readBody(e.do(http.MethodGet, "/leads/"+lead.ID, nil, nil))
	if !strings.Contains(page, "131049") || !strings.Contains(page, "ecossistema") {
		t.Fatal("the lead page must explain the failure code")
	}
}

func TestOptOutKeepsTheLeadOutOfNextCampaigns(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	lead := e.sendTo("+5500000000030")
	eventually(t, "opt-out", func() bool { return e.lead("+5500000000030").OptOut })

	before, _ := e.store.Timeline(lead.ID)
	_, res := e.campaignTo("+5500000000030")
	body := readBody(res)
	after, _ := e.store.Timeline(lead.ID)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Nenhum lead elegível") {
		t.Fatalf("a lead that opted out must not be eligible, got %d", res.StatusCode)
	}
	if len(after) != len(before) {
		t.Fatal("a message was recorded for a lead that opted out")
	}
}

func TestAutoReplyArrivesOnTheTimeline(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	lead := e.sendTo("+5500000000031")
	eventually(t, "the reply", func() bool {
		entries, _ := e.store.Timeline(lead.ID)
		for _, entry := range entries {
			if entry.Kind == store.KindMessageIn && entry.Body == "Recebi sua mensagem!" {
				return true
			}
		}
		return false
	})
}

func (e *env) postWebhook(body []byte, eventID string, signedAt time.Time, secret string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.app.URL+"/webhooks/resgata", bytes.NewReader(body))
	req.Header.Set(resgata.SignatureHeader, resgata.SignatureHeaderValue(secret, signedAt.Unix(), body))
	req.Header.Set(resgata.EventIDHeader, eventID)
	res, err := e.http.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.StatusCode, readBody(res)
}

func TestWebhookSecurityAndDedupe(t *testing.T) {
	e := newEnv(t, "")
	body, _ := json.Marshal(map[string]any{
		"id": "evt_fixed", "type": "contact.opted_out", "created": time.Now().Unix(),
		"data": map[string]any{"contact": map[string]any{"phone": "+5519991230005"}, "matched_phrase": "parar"},
	})

	status, _ := e.postWebhook(body, "evt_fixed", time.Now(), randomString(t))
	if status != http.StatusUnauthorized || e.lead("+5519991230005").OptOut {
		t.Fatal("a wrong signature must be rejected without side effects")
	}
	status, _ = e.postWebhook(body, "evt_fixed", time.Now().Add(-10*time.Minute), e.secret)
	if status != http.StatusUnauthorized {
		t.Fatalf("a replayed old signature must be rejected, got %d", status)
	}
	rejected, _ := e.store.Events("rejected", 10)
	if len(rejected) != 2 {
		t.Fatalf("rejected deliveries must be recorded for the events page, got %d", len(rejected))
	}

	status, _ = e.postWebhook(body, "evt_fixed", time.Now(), e.secret)
	if status != http.StatusOK || !e.lead("+5519991230005").OptOut {
		t.Fatalf("a valid delivery must be applied: %d", status)
	}
	status, answer := e.postWebhook(body, "evt_fixed", time.Now(), e.secret)
	if status != http.StatusOK || !strings.Contains(answer, "duplicate") {
		t.Fatalf("a repeated event id must be acknowledged as a duplicate: %d %s", status, answer)
	}
	entries, _ := e.store.Timeline(e.lead("+5519991230005").ID)
	optOuts := 0
	for _, entry := range entries {
		if entry.Kind == store.KindOptOut {
			optOuts++
		}
	}
	if optOuts != 1 {
		t.Fatalf("the duplicate was processed twice (%d opt-out entries)", optOuts)
	}
}

func TestInvalidJSONWithValidSignatureIsRejected(t *testing.T) {
	e := newEnv(t, "")
	status, _ := e.postWebhook([]byte("not json"), "evt_bad_json", time.Now(), e.secret)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d", status)
	}
}

func TestDisconnectAndRevokedWebhook(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	res := e.do(http.MethodPost, "/resgata/desconectar", url.Values{}, nil)
	res.Body.Close()
	conn, _, _ := e.store.Connection()
	if conn.State != store.StateRevoked || conn.TokenEncrypted != "" {
		t.Fatalf("not revoked: %+v", conn)
	}
	page := readBody(e.do(http.MethodGet, "/integracao", nil, nil))
	if !strings.Contains(page, "Conectar de novo") {
		t.Fatal("a revoked connection must offer to reconnect")
	}
}

func TestOAuthErrorsAreFriendly(t *testing.T) {
	e := newEnv(t, "")
	res := e.do(http.MethodGet, "/resgata/conectar", nil, nil)
	authorize := res.Header.Get("Location") + "&simulate=access_denied"
	res.Body.Close()
	res = e.do(http.MethodGet, authorize, nil, nil)
	callback := res.Header.Get("Location")
	res.Body.Close()
	res = e.do(http.MethodGet, callback, nil, nil)
	cookies := res.Cookies()
	res.Body.Close()
	if len(cookies) == 0 {
		t.Fatal("expected a flash message")
	}
	if _, found, _ := e.store.Connection(); found {
		t.Fatal("a denied authorization must not create a connection")
	}
}

func TestOAuthStateMismatchIsRefused(t *testing.T) {
	e := newEnv(t, "")
	res := e.do(http.MethodGet, "/resgata/callback?code=abc&state=forged", nil, nil)
	res.Body.Close()
	if _, found, _ := e.store.Connection(); found || res.StatusCode != http.StatusSeeOther {
		t.Fatal("a forged state must be refused")
	}
}

func TestPagesRender(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	lead := e.lead("+5519991230001")
	for _, path := range []string{"/", "/funil", "/funil?fluxo=fl_reativacao", "/leads", "/leads?q=mariana", "/leads/novo", "/leads/" + lead.ID, "/leads/" + lead.ID + "/editar", "/leads/" + lead.ID + "/linha-do-tempo", "/integracao", "/eventos", "/eventos/lista", "/sandbox", "/chaves", "/modelos", "/modelos/lista", "/campanhas", "/campanhas/nova", "/campanhas/nova?lead=" + lead.ID} {
		res := e.do(http.MethodGet, path, nil, nil)
		body := readBody(res)
		if res.StatusCode != http.StatusOK || len(body) < 50 {
			t.Errorf("%s: status %d", path, res.StatusCode)
		}
	}
	res := e.do(http.MethodGet, "/nada-aqui", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown page: %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestLeadFormValidation(t *testing.T) {
	e := newEnv(t, "")
	res := e.do(http.MethodPost, "/leads", url.Values{"nome": {""}, "celular": {"12"}, "estagio": {"lead"}, "unidade": {"un_centro"}, "etapa": {"st_novo"}}, nil)
	body := readBody(res)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Informe o nome") || !strings.Contains(body, "Informe o celular") {
		t.Fatalf("validation errors expected, got %d", res.StatusCode)
	}
	res = e.do(http.MethodPost, "/leads", url.Values{"nome": {"Nova Pessoa"}, "celular": {"(19) 99888-7766"}, "estagio": {"lead"}, "unidade": {"un_centro"}, "etapa": {"st_novo"}}, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || e.lead("+5519998887766").Nome != "Nova Pessoa" {
		t.Fatal("a valid lead must be created with an E.164 number")
	}
}

func TestAdminPasswordProtectsOnlyTheAdminUI(t *testing.T) {
	password := randomString(t)
	e := newEnv(t, password)
	if res := e.do(http.MethodGet, "/", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin UI must ask for a password: %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, e.app.URL+"/", nil)
	req.SetBasicAuth("admin", password)
	res, _ := e.http.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("correct password must work: %d", res.StatusCode)
	}
	if res := e.do(http.MethodGet, "/healthz", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatal("healthz must stay open")
	}
	if res := e.do(http.MethodGet, "/api/v1/unidades", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatal("the CRM API uses its own bearer keys")
	}
	status, _ := e.postWebhook([]byte("{}"), "x", time.Now(), randomString(t))
	if status != http.StatusUnauthorized {
		t.Fatal("the webhook endpoint is authenticated by signature, not by the admin password")
	}
}

func TestCrossSitePostsAreRefused(t *testing.T) {
	e := newEnv(t, "")
	res := e.do(http.MethodPost, "/leads/novo", url.Values{}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
	res = e.do(http.MethodPost, "/chaves", url.Values{"unidade": {"un_centro"}}, map[string]string{"Origin": "https://evil.example"})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("origin check: %d", res.StatusCode)
	}
}

func TestCRMAPIKeyFlowThroughTheFullServer(t *testing.T) {
	e := newEnv(t, "")
	page := readBody(e.do(http.MethodPost, "/chaves", url.Values{"unidade": {"un_centro"}, "nome": {"parceiro"}}, nil))
	start := strings.Index(page, "crm_")
	if start < 0 {
		t.Fatal("the new key must be shown once")
	}
	secret := page[start : start+4+48]

	req, _ := http.NewRequest(http.MethodGet, e.app.URL+"/api/v1/leads?limite=2", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	res, _ := e.http.Do(req)
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	res.Body.Close()
	if res.StatusCode != 200 || len(body["dados"].([]any)) != 2 || body["paginacao"].(map[string]any)["tem_mais"] != true {
		t.Fatalf("unexpected API answer %d %v", res.StatusCode, body)
	}
	if again := readBody(e.do(http.MethodGet, "/chaves", nil, nil)); strings.Contains(again, secret) {
		t.Fatal("the key must never be shown again")
	}
}

func TestSetupScreenListsExactlyWhatIsMissing(t *testing.T) {
	handler := SetupHandler([]config.Problem{{Name: "RESGATA_CLIENT_ID", Hint: "dica um"}, {Name: "APP_ENCRYPTION_KEY", Hint: "dica dois"}}, "chave-sugerida")
	server := httptest.NewServer(handler)
	defer server.Close()
	res, _ := http.Get(server.URL + "/qualquer")
	body := readBody(res)
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "RESGATA_CLIENT_ID") || !strings.Contains(body, "APP_ENCRYPTION_KEY") || !strings.Contains(body, "chave-sugerida") {
		t.Fatalf("setup screen incomplete (status %d)", res.StatusCode)
	}
	if strings.Contains(body, "RESGATA_WEBHOOK_SECRET") {
		t.Fatal("only the missing variables may be listed")
	}
}
