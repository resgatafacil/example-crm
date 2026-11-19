package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/config"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/crypto"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/mockresgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/portal"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/workspace"
)

type portalEnv struct {
	t            *testing.T
	app          *httptest.Server
	mock         *mockresgata.Mock
	server       *Server
	ws           *workspace.Manager
	cfg          config.Config
	portalSecret string
	webhook      string
}

func newPortalEnv(t *testing.T, maxWorkspaces int) *portalEnv {
	t.Helper()
	webhookSecret := randomString(t)
	clientID := "app_" + randomString(t)
	mock := mockresgata.New(mockresgata.Options{ClientID: clientID, WebhookSecret: webhookSecret, Speed: 15 * time.Millisecond})
	mockServer := httptest.NewServer(mock.Handler())
	t.Cleanup(mockServer.Close)

	manager, err := workspace.NewManager(t.TempDir(), maxWorkspaces)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)

	var handler http.Handler
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(app.Close)

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	portalSecret := make([]byte, 24)
	_, _ = rand.Read(portalSecret)
	cfg := config.Config{
		PublicURL: app.URL, APIURL: mockServer.URL, AppURL: mockServer.URL, ClientID: clientID, WebhookSecret: webhookSecret,
		Environment: config.EnvSandbox, RateLimit: 100, EncryptionKey: key,
		PortalSecret: hex.EncodeToString(portalSecret), PortalURL: "https://portal.example.test",
		WorkspaceTTL: 24 * time.Hour, SessionTTL: 8 * time.Hour, MaxWorkspaces: maxWorkspaces,
	}
	box, err := crypto.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, Backend{Workspaces: manager}, box, resgata.NewClient(cfg.APIURL, nil))
	if err != nil {
		t.Fatal(err)
	}
	handler = server.Handler()
	mock.SetWebhook(app.URL+"/webhooks/resgata", webhookSecret)
	return &portalEnv{t: t, app: app, mock: mock, server: server, ws: manager, cfg: cfg, portalSecret: cfg.PortalSecret, webhook: webhookSecret}
}

func (p *portalEnv) token(sub, name string) string {
	p.t.Helper()
	return p.tokenAt(sub, name, time.Now(), time.Minute, p.portalSecret)
}

func (p *portalEnv) tokenAt(sub, name string, issued time.Time, lifetime time.Duration, secret string) string {
	p.t.Helper()
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	token, err := portal.Sign(secret, portal.Claims{
		Subject: sub, Name: name, IssuedAt: issued.Unix(), ExpiresAt: issued.Add(lifetime).Unix(), ID: hex.EncodeToString(raw),
	})
	if err != nil {
		p.t.Fatal(err)
	}
	return token
}

type developer struct {
	p      *portalEnv
	client *http.Client
	id     string
}

func (p *portalEnv) visitor() *developer {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &developer{p: p, client: client}
}

func (p *portalEnv) login(sub, name string) *developer {
	p.t.Helper()
	d := p.visitor()
	res := d.get("/portal-login?token=" + url.QueryEscape(p.token(sub, name)))
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		p.t.Fatalf("login of %s failed: %d %s", sub, res.StatusCode, res.Header.Get("Location"))
	}
	d.id = portal.WorkspaceID(sub)
	return d
}

func (d *developer) do(method, path string, form url.Values, headers map[string]string) *http.Response {
	d.p.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = d.p.app.URL + path
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		d.p.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := d.client.Do(req)
	if err != nil {
		d.p.t.Fatal(err)
	}
	return res
}

func (d *developer) get(path string) *http.Response {
	d.p.t.Helper()
	return d.do(http.MethodGet, path, nil, nil)
}

func (d *developer) page(path string) string {
	d.p.t.Helper()
	return readBody(d.get(path))
}

func (d *developer) store() *store.Store {
	d.p.t.Helper()
	st, _, found, err := d.p.ws.Get(d.id)
	if err != nil || !found {
		d.p.t.Fatalf("workspace %s not found: %v", d.id, err)
	}
	return st
}

func (d *developer) connect() {
	d.p.t.Helper()
	res := d.get("/resgata/conectar")
	authorize := res.Header.Get("Location")
	res.Body.Close()
	res = d.get(authorize)
	callback := res.Header.Get("Location")
	res.Body.Close()
	res = d.get(callback)
	res.Body.Close()
	conn, found, _ := d.store().Connection()
	if !found || conn.State != store.StateConnected {
		d.p.t.Fatalf("workspace %s did not connect: %+v", d.id, conn)
	}
	if conn.ExternalRef != d.id {
		d.p.t.Fatalf("external_ref must be the workspace id, got %q", conn.ExternalRef)
	}
}

func (d *developer) lead(phone string) store.Lead {
	d.p.t.Helper()
	lead, ok, _ := d.store().LeadByPhone(phone)
	if !ok {
		d.p.t.Fatalf("lead %s not found for %s", phone, d.id)
	}
	return lead
}

var portalTemplateID = regexp.MustCompile(`value="(tpl_[a-z0-9]+)"`)

func (d *developer) approvedTemplateID() string {
	d.p.t.Helper()
	res := d.do(http.MethodPost, "/modelos", url.Values{"nome": {"boas_vindas"}, "categoria": {"MARKETING"}, "corpo": {"Oi {{1}}, tudo bem?"}, "exemplos": {"Maria"}}, nil)
	res.Body.Close()
	var id string
	eventually(d.p.t, "an approved template", func() bool {
		match := portalTemplateID.FindStringSubmatch(readBody(d.get("/campanhas/nova")))
		if match == nil {
			return false
		}
		id = match[1]
		return true
	})
	return id
}

func (d *developer) sendTo(phone string) store.Lead {
	d.p.t.Helper()
	lead := d.lead(phone)
	res := d.do(http.MethodPost, "/campanhas", url.Values{"modelo": {d.approvedTemplateID()}, "lead": {lead.ID}, "nome": {"Teste"}, "var_1": {"nome"}}, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		d.p.t.Fatalf("campaign status %d", res.StatusCode)
	}
	return lead
}

func (d *developer) timeline(leadID string) []store.Entry {
	entries, _ := d.store().Timeline(leadID)
	return entries
}

func hasEntry(entries []store.Entry, kind, body string) bool {
	for _, e := range entries {
		if e.Kind == kind && e.Body == body {
			return true
		}
	}
	return false
}

func TestGateRequiresThePortalAndKeepsPublicRoutesOpen(t *testing.T) {
	p := newPortalEnv(t, 5)
	visitor := p.visitor()

	res := visitor.get("/")
	page := readBody(res)
	if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("status %d, www-authenticate %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if !strings.Contains(page, "Entre pelo portal de desenvolvedores") || !strings.Contains(page, `href="https://portal.example.test"`) {
		t.Fatal("the gate page must explain the portal and link to it")
	}
	for _, path := range []string{"/leads", "/integracao", "/eventos", "/chaves", "/sandbox", "/funil"} {
		res := visitor.get(path)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s answered %d without a session", path, res.StatusCode)
		}
	}

	for path, want := range map[string]int{"/healthz": 200, "/static/app.css": 200, "/api/v1/unidades": 401} {
		res := visitor.get(path)
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s answered %d, want %d", path, res.StatusCode, want)
		}
	}
	res = visitor.do(http.MethodPost, "/webhooks/resgata", nil, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the webhook must answer for itself, got %d", res.StatusCode)
	}
}

func TestLoginOpensAPrivateWorkspace(t *testing.T) {
	p := newPortalEnv(t, 5)
	d := p.visitor()
	res := d.get("/portal-login?token=" + url.QueryEscape(p.token("user-1", "Maria Souza")))
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("unexpected session cookie: %+v", cookie)
	}
	if parts := strings.Split(cookie.Value, "."); len(parts) != 3 || parts[0] != portal.WorkspaceID("user-1") {
		t.Fatalf("cookie must be workspaceId.exp.sig, got %q", cookie.Value)
	}
	if !cookie.Expires.After(time.Now().Add(7*time.Hour)) || !cookie.Expires.Before(time.Now().Add(9*time.Hour)) {
		t.Fatalf("session should last SESSION_TTL_HOURS, expires %v", cookie.Expires)
	}

	page := d.page("/")
	if !strings.Contains(page, "Olá, Maria") || !strings.Contains(page, "24 h sem uso") || !strings.Contains(page, "só sua") {
		t.Fatal("the header must greet the developer and explain the private workspace")
	}
	if strings.Contains(page, "Soluções") {
		t.Fatal("unexpected content")
	}
}

func TestLoginRefusesBadTokens(t *testing.T) {
	p := newPortalEnv(t, 5)
	now := time.Now()
	good := p.token("user-1", "Maria")
	cases := map[string]struct {
		token string
		want  string
	}{
		"expired":      {p.tokenAt("user-1", "Maria", now.Add(-10*time.Minute), time.Minute, p.portalSecret), "não vale mais"},
		"wrong secret": {p.tokenAt("user-1", "Maria", now, time.Minute, "another-secret-that-is-long-enough"), "Não foi possível validar"},
		"garbage":      {"not-a-token", "Não foi possível validar"},
		"missing":      {"", "Não foi possível validar"},
		"tampered":     {good[:len(good)-3] + "AAA", "Não foi possível validar"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := p.visitor()
			res := d.get("/portal-login?token=" + url.QueryEscape(c.token))
			page := readBody(res)
			if res.StatusCode != http.StatusUnauthorized || !strings.Contains(page, c.want) {
				t.Fatalf("status %d, page lacks %q", res.StatusCode, c.want)
			}
			if len(res.Cookies()) != 0 {
				t.Fatal("a refused login must not set a cookie")
			}
		})
	}
	if n, _ := p.ws.Count(); n != 0 {
		t.Fatalf("refused logins created %d workspaces", n)
	}
}

func TestLoginTokensWorkOnlyOnce(t *testing.T) {
	p := newPortalEnv(t, 5)
	token := p.token("user-1", "Maria")
	first := p.visitor().get("/portal-login?token=" + url.QueryEscape(token))
	first.Body.Close()
	second := p.visitor().get("/portal-login?token=" + url.QueryEscape(token))
	page := readBody(second)
	if first.StatusCode != http.StatusSeeOther || second.StatusCode != http.StatusUnauthorized || !strings.Contains(page, "não vale mais") {
		t.Fatalf("first=%d second=%d", first.StatusCode, second.StatusCode)
	}
}

func TestForgedOrExpiredSessionsAreSentToTheGate(t *testing.T) {
	p := newPortalEnv(t, 5)
	d := p.login("user-1", "Maria")
	if res := d.get("/leads"); res.StatusCode != http.StatusOK {
		t.Fatalf("a valid session must work, got %d", res.StatusCode)
	}

	u, _ := url.Parse(p.app.URL)
	var valid string
	for _, c := range d.client.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			valid = c.Value
		}
	}
	parts := strings.Split(valid, ".")
	victim := portal.WorkspaceID("user-2")
	p.login("user-2", "Outra Pessoa")
	forgeries := map[string]string{
		"other workspace": victim + "." + parts[1] + "." + parts[2],
		"longer expiry":   parts[0] + ".9999999999." + parts[2],
		"bad signature":   parts[0] + "." + parts[1] + ".AAAA",
		"junk":            "junk",
	}
	for name, value := range forgeries {
		t.Run(name, func(t *testing.T) {
			attacker := p.visitor()
			attacker.client.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: value, Path: "/"}})
			res := attacker.get("/leads")
			res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("forged cookie accepted: %d", res.StatusCode)
			}
		})
	}

	p.server.sessions.SetClock(func() time.Time { return time.Now().Add(9 * time.Hour) })
	res := d.get("/leads")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an expired session must send the visitor back to the gate, got %d", res.StatusCode)
	}
}

func TestWorkspacesAreIsolatedFromEachOther(t *testing.T) {
	p := newPortalEnv(t, 5)
	a := p.login("user-a", "Ana Lima")
	b := p.login("user-b", "Beto Dias")

	units, _ := a.store().Units()
	flows, _ := a.store().Flows()
	if _, err := a.store().CreateLead(store.LeadInput{Nome: "Cliente exclusivo da Ana", Celular: "+5511977770001", Estagio: "lead", UnitID: units[0].ID, StepID: flows[0].Steps[0].ID}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.page("/leads"), "Cliente exclusivo da Ana") {
		t.Fatal("the lead must show for its owner")
	}
	if strings.Contains(b.page("/leads"), "Cliente exclusivo da Ana") || strings.Contains(b.page("/funil"), "Cliente exclusivo da Ana") {
		t.Fatal("a lead leaked into another developer's workspace")
	}
	if res := b.get("/leads/" + a.lead("+5511977770001").ID); res.StatusCode != http.StatusNotFound {
		t.Fatalf("opening another workspace's lead answered %d", res.StatusCode)
	}

	unitID := units[0].ID
	res := a.do(http.MethodPost, "/chaves", url.Values{"unidade": {unitID}, "nome": {"integração"}}, nil)
	body := readBody(res)
	secret := keyFromPage(t, body)
	if !strings.HasPrefix(secret, "crm_"+a.id+"_") {
		t.Fatalf("keys must carry their workspace id, got %q", secret)
	}

	api := func(secret string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, p.app.URL+"/api/v1/leads?limite=500", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, readBody(res)
	}
	status, listing := api(secret)
	if status != http.StatusOK || !strings.Contains(listing, "Cliente exclusivo da Ana") {
		t.Fatalf("a key must read its own workspace: %d", status)
	}
	if status, _ := api("crm_" + b.id + "_" + secret[len("crm_"+a.id+"_"):]); status != http.StatusUnauthorized {
		t.Fatalf("the same key material under another workspace id must fail, got %d", status)
	}
	if status, _ := api("crm_" + strings.Repeat("0", 16) + "_deadbeef"); status != http.StatusUnauthorized {
		t.Fatalf("a key for an unknown workspace must fail, got %d", status)
	}
	if status, _ := api("crm_deadbeef"); status != http.StatusUnauthorized {
		t.Fatalf("an unscoped key must fail in portal mode, got %d", status)
	}
	if keys, _ := b.store().APIKeys(); len(keys) != 0 {
		t.Fatal("a key leaked into another workspace")
	}
}

func keyFromPage(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, "crm_")
	if start < 0 {
		t.Fatal("the new key was not shown")
	}
	end := start
	for end < len(page) && (page[end] == '_' || page[end] >= '0' && page[end] <= '9' || page[end] >= 'a' && page[end] <= 'z') {
		end++
	}
	return page[start:end]
}

func (p *portalEnv) postWebhook(event map[string]any, secret string) (int, string) {
	p.t.Helper()
	body, _ := json.Marshal(event)
	req, _ := http.NewRequest(http.MethodPost, p.app.URL+"/webhooks/resgata", strings.NewReader(string(body)))
	req.Header.Set(resgata.SignatureHeader, resgata.SignatureHeaderValue(secret, time.Now().Unix(), body))
	req.Header.Set(resgata.EventIDHeader, event["id"].(string))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	return res.StatusCode, readBody(res)
}

func inbound(id, ref, phone, text string) map[string]any {
	return map[string]any{
		"id": id, "type": "message.received", "created": time.Now().Unix(),
		"connection": map[string]any{"id": "conn_x", "external_ref": ref},
		"data":       map[string]any{"message": map[string]any{"id": "in_" + id, "from": phone, "text": text}},
	}
}

func TestWebhooksAreRoutedByExternalRef(t *testing.T) {
	p := newPortalEnv(t, 5)
	a := p.login("user-a", "Ana")
	b := p.login("user-b", "Beto")

	status, _ := p.postWebhook(inbound("evt_a1", a.id, "+5511966660001", "Quero agendar"), p.webhook)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	lead, found, _ := a.store().LeadByPhone("+5511966660001")
	if !found || !hasEntry(a.timeline(lead.ID), store.KindMessageIn, "Quero agendar") {
		t.Fatal("the reply must reach the workspace named by external_ref")
	}
	if _, found, _ := b.store().LeadByPhone("+5511966660001"); found {
		t.Fatal("the reply leaked into another workspace")
	}
	if events, _ := b.store().Events("", 10); len(events) != 0 {
		t.Fatalf("another workspace recorded %d events", len(events))
	}
	if events, _ := a.store().Events("message.received", 10); len(events) != 1 || !events[0].SignatureOK {
		t.Fatalf("the owner must record the event once: %+v", events)
	}

	status, body := p.postWebhook(inbound("evt_a1", a.id, "+5511966660001", "Quero agendar"), p.webhook)
	if status != http.StatusOK || !strings.Contains(body, "duplicate") {
		t.Fatalf("dedupe must stay per workspace: %d %s", status, body)
	}
	status, _ = p.postWebhook(inbound("evt_a1", b.id, "+5511966660002", "Mesmo id, outro workspace"), p.webhook)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if _, found, _ := b.store().LeadByPhone("+5511966660002"); !found {
		t.Fatal("the same event id in another workspace is not a duplicate")
	}
}

func TestWebhooksForUnknownWorkspacesAreAcknowledgedAndDropped(t *testing.T) {
	p := newPortalEnv(t, 5)
	a := p.login("user-a", "Ana")
	for name, ref := range map[string]string{"unknown id": strings.Repeat("a", 16), "empty": "", "not an id": "../../etc"} {
		status, body := p.postWebhook(inbound("evt_"+name, ref, "+5511955550001", "oi"), p.webhook)
		if status != http.StatusOK || !strings.Contains(body, "ignored") {
			t.Errorf("%s: %d %s (Resgata must not retry events we cannot route)", name, status, body)
		}
	}
	if _, found, _ := a.store().LeadByPhone("+5511955550001"); found {
		t.Fatal("an unroutable event was applied")
	}
	if events, _ := a.store().Events("", 10); len(events) != 0 {
		t.Fatal("an unroutable event was stored")
	}
	status, _ := p.postWebhook(inbound("evt_bad", a.id, "+5511955550002", "oi"), "wrong-secret-wrong-secret")
	if status != http.StatusUnauthorized {
		t.Fatalf("a bad signature must still be refused, got %d", status)
	}
	if events, _ := a.store().Events("", 10); len(events) != 0 {
		t.Fatal("a rejected webhook must not be stored in any workspace in portal mode")
	}
}

func TestTwoDevelopersRunTheFullFlowSideBySide(t *testing.T) {
	p := newPortalEnv(t, 5)
	a := p.login("user-a", "Ana")
	b := p.login("user-b", "Beto")
	a.connect()
	b.connect()

	leadA := a.sendTo("+5500000000031")
	eventually(t, "Ana's reply and read status", func() bool {
		entries := a.timeline(leadA.ID)
		return hasEntry(entries, store.KindMessageIn, "Recebi sua mensagem!") && statusOf(entries) == "read"
	})
	leadB := b.lead("+5500000000031")
	if messageCount(b.timeline(leadB.ID)) != 0 {
		t.Fatal("Beto's workspace received messages from Ana's conversation")
	}

	b.sendTo("+5500000000003")
	eventually(t, "Beto's failure", func() bool { return statusOf(b.timeline(b.lead("+5500000000003").ID)) == "failed" })
	if messageCount(a.timeline(a.lead("+5500000000003").ID)) != 0 {
		t.Fatal("Ana's workspace received Beto's failure")
	}
}

func messageCount(entries []store.Entry) int {
	n := 0
	for _, e := range entries {
		if e.Kind == store.KindMessageIn || e.Kind == store.KindMessageOut {
			n++
		}
	}
	return n
}

func statusOf(entries []store.Entry) string {
	for _, e := range entries {
		if e.Kind == store.KindMessageOut {
			return e.Status
		}
	}
	return ""
}

func TestLogoutClearsTheSessionAndPointsBackToThePortal(t *testing.T) {
	p := newPortalEnv(t, 5)
	d := p.login("user-1", "Maria")
	res := d.do(http.MethodPost, "/sair", url.Values{}, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "https://portal.example.test" {
		t.Fatalf("logout: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	cleared := false
	for _, c := range res.Cookies() {
		cleared = cleared || (c.Name == sessionCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("the session cookie was not cleared")
	}
	if res := d.get("/"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout the gate must show, got %d", res.StatusCode)
	}
	if _, _, found, _ := p.ws.Get(portal.WorkspaceID("user-1")); !found {
		t.Fatal("logging out must keep the workspace for the next visit")
	}
	res = d.do(http.MethodPost, "/sair", url.Values{}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site logout must be refused, got %d", res.StatusCode)
	}
}

func TestResetRestoresTheSeedAndDisconnectsFromResgata(t *testing.T) {
	p := newPortalEnv(t, 5)
	d := p.login("user-1", "Maria")
	d.connect()
	seeded := len(leadsOf(t, d.store()))
	units, _ := d.store().Units()
	flows, _ := d.store().Flows()
	if _, err := d.store().CreateLead(store.LeadInput{Nome: "Temporário", Celular: "+5511944440001", Estagio: "lead", UnitID: units[0].ID, StepID: flows[0].Steps[0].ID}); err != nil {
		t.Fatal(err)
	}

	res := d.do(http.MethodPost, "/reiniciar", url.Values{}, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset status %d", res.StatusCode)
	}
	if got := len(leadsOf(t, d.store())); got != seeded {
		t.Fatalf("leads after reset = %d, want %d", got, seeded)
	}
	if _, found, _ := d.store().Connection(); found {
		t.Fatal("the connection record must be gone after a reset")
	}
	eventually(t, "Resgata to be told the connection ended", func() bool { return contains(p.mock.SentTypes(), "connection.revoked") })
	time.Sleep(100 * time.Millisecond)
	if events, _ := d.store().Events("connection.revoked", 10); len(events) != 0 {
		t.Fatal("the revoked event of the old connection must not pollute the fresh workspace")
	}
	if res := d.get("/"); res.StatusCode != http.StatusOK {
		t.Fatalf("the session must survive a reset, got %d", res.StatusCode)
	}
}

func leadsOf(t *testing.T, st *store.Store) []store.Lead {
	t.Helper()
	page, err := st.LeadsPage(store.LeadFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	return page.Leads
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestPurgeRemovesIdleWorkspacesAndDisconnectsThem(t *testing.T) {
	p := newPortalEnv(t, 5)
	idle := p.login("user-idle", "Parada")
	active := p.login("user-active", "Ativa")
	idle.connect()
	idlePath := idle.id

	future := time.Now().Add(25 * time.Hour)
	p.ws.SetClock(func() time.Time { return future })
	p.server.now = func() time.Time { return future }
	if err := p.ws.Touch(active.id); err != nil {
		t.Fatal(err)
	}

	purged, err := p.server.PurgeIdle(t.Context())
	if err != nil || purged != 1 {
		t.Fatalf("purged %d, err %v", purged, err)
	}
	if _, _, found, _ := p.ws.Get(idlePath); found {
		t.Fatal("the idle workspace is still registered")
	}
	if _, _, found, _ := p.ws.Get(active.id); !found {
		t.Fatal("the active workspace must be kept")
	}
	if contains(p.mock.SentTypes(), "connection.revoked") == false {
		t.Fatal("the idle workspace must be disconnected from Resgata before it is deleted")
	}
	if matches := p.ws.Files(idlePath); len(matches) != 0 {
		t.Fatalf("files left behind: %v", matches)
	}

	p.server.now = time.Now
	res := idle.get("/")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a purged workspace must send its owner back to the gate, got %d", res.StatusCode)
	}
}

func TestPurgeSurvivesAnUnreachableResgata(t *testing.T) {
	p := newPortalEnv(t, 5)
	d := p.login("user-1", "Maria")
	d.connect()
	p.server.client = resgata.NewClient("http://127.0.0.1:1", nil)

	future := time.Now().Add(25 * time.Hour)
	p.server.now = func() time.Time { return future }
	purged, err := p.server.PurgeIdle(t.Context())
	if err != nil || purged != 1 {
		t.Fatalf("a failing disconnect must not block the purge: purged=%d err=%v", purged, err)
	}
}

func TestFullDemoShowsAFriendlyPage(t *testing.T) {
	p := newPortalEnv(t, 1)
	p.login("user-1", "Primeira")

	res := p.visitor().get("/portal-login?token=" + url.QueryEscape(p.token("user-2", "Segunda")))
	page := readBody(res)
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(page, "demonstração está cheia") {
		t.Fatalf("status %d", res.StatusCode)
	}
	if len(res.Cookies()) != 0 {
		t.Fatal("no session for a refused visitor")
	}
	again := p.visitor().get("/portal-login?token=" + url.QueryEscape(p.token("user-1", "Primeira")))
	again.Body.Close()
	if again.StatusCode != http.StatusSeeOther {
		t.Fatalf("a developer who already has a workspace must get back in, got %d", again.StatusCode)
	}
}

func TestLocalModeIsUnchanged(t *testing.T) {
	e := newEnv(t, "")
	res := e.do(http.MethodGet, "/portal-login?token=x", nil, nil)
	body := readBody(res)
	if res.StatusCode != http.StatusNotFound || strings.Contains(body, "portal de desenvolvedores") {
		t.Fatalf("local mode must not expose the portal login: %d", res.StatusCode)
	}
	page := readBody(e.do(http.MethodGet, "/", nil, nil))
	if strings.Contains(page, "Olá,") || strings.Contains(page, "workspace-bar") {
		t.Fatal("local mode must not show the workspace bar")
	}
	res = e.do(http.MethodPost, "/reiniciar", url.Values{}, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("reset must not exist in local mode, got %d", res.StatusCode)
	}
	unscoped, err := e.store.CreateAPIKey(firstUnitID(t, e.store), "local")
	if err != nil || strings.Count(unscoped, "_") != 1 {
		t.Fatalf("local keys stay unscoped: %q %v", unscoped, err)
	}
}

func firstUnitID(t *testing.T, st *store.Store) string {
	t.Helper()
	units, err := st.Units()
	if err != nil || len(units) == 0 {
		t.Fatal("no units")
	}
	return units[0].ID
}

func TestPortalModeNeedsAWorkspaceManager(t *testing.T) {
	cfg := config.Config{PortalSecret: strings.Repeat("s", 32), EncryptionKey: make([]byte, 32), RateLimit: 1}
	box, _ := crypto.NewBox(cfg.EncryptionKey)
	if _, err := New(cfg, Backend{}, box, resgata.NewClient("http://example.test", nil)); err == nil {
		t.Fatal("portal mode without workspaces must be refused")
	}
	cfg.PortalSecret = ""
	if _, err := New(cfg, Backend{}, box, resgata.NewClient("http://example.test", nil)); err == nil {
		t.Fatal("local mode without a store must be refused")
	}
}
