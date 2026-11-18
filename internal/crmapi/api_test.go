package crmapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

type fixture struct {
	t       *testing.T
	store   *store.Store
	server  *httptest.Server
	limiter *Limiter
	key     string
}

func newFixture(t *testing.T, limit int) *fixture {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	key, err := s.CreateAPIKey("un_centro", "teste")
	if err != nil {
		t.Fatal(err)
	}
	limiter := NewLimiter(limit, time.Minute)
	mux := http.NewServeMux()
	New(Fixed(s), limiter).Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &fixture{t: t, store: s, server: server, limiter: limiter, key: key}
}

func (f *fixture) get(path, key string) (*http.Response, map[string]any) {
	f.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.server.URL+path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res, body
}

func TestAuthentication(t *testing.T) {
	f := newFixture(t, 100)
	for name, key := range map[string]string{"missing": "", "unknown": "crm_nope", "wrong prefix": "rf_test_x"} {
		res, body := f.get("/api/v1/unidades", key)
		if res.StatusCode != http.StatusUnauthorized || body["erro"] == nil {
			t.Errorf("%s: status %d body %v", name, res.StatusCode, body)
		}
	}
	res, body := f.get("/api/v1/unidades", f.key)
	dados := body["dados"].([]any)
	if res.StatusCode != 200 || len(dados) != 1 || dados[0].(map[string]any)["id"] != "un_centro" {
		t.Fatalf("a unit key must only see its own unit: %d %v", res.StatusCode, body)
	}
}

func TestSuspendedUnitIsForbidden(t *testing.T) {
	f := newFixture(t, 100)
	key, _ := f.store.CreateAPIKey("un_norte", "suspensa")
	res, _ := f.get("/api/v1/leads", key)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestOtherUnitIsForbidden(t *testing.T) {
	f := newFixture(t, 100)
	res, _ := f.get("/api/v1/leads?organizacao_id=un_cambui", f.key)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
	res, _ = f.get("/api/v1/leads?organizacao_id=un_centro", f.key)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("own unit must be accepted: %d", res.StatusCode)
	}
}

func TestFlowsIncludeOrderedSteps(t *testing.T) {
	f := newFixture(t, 100)
	_, body := f.get("/api/v1/fluxos", f.key)
	flows := body["dados"].([]any)
	steps := flows[0].(map[string]any)["etapas"].([]any)
	if len(flows) != 2 || len(steps) != 4 || steps[0].(map[string]any)["inicial"] != true || steps[3].(map[string]any)["ordem"].(float64) != 4 {
		t.Fatalf("unexpected flows: %v", body)
	}
}

func TestLeadShape(t *testing.T) {
	f := newFixture(t, 100)
	_, body := f.get("/api/v1/leads?limite=1", f.key)
	lead := body["dados"].([]any)[0].(map[string]any)
	for _, key := range []string{"id", "nome", "celular", "email", "estagio", "opt_out", "unidade", "fluxo", "etapa", "criado_em", "alterado_em"} {
		if _, ok := lead[key]; !ok {
			t.Errorf("lead is missing %q", key)
		}
	}
	if _, err := time.Parse(time.RFC3339, lead["criado_em"].(string)); err != nil {
		t.Errorf("criado_em is not ISO 8601: %v", err)
	}
	if lead["unidade"].(map[string]any)["nome"] == "" {
		t.Error("unidade needs id and nome")
	}
}

func TestCursorPaginationEnvelope(t *testing.T) {
	f := newFixture(t, 100)
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		path := "/api/v1/leads?limite=4"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		_, body := f.get(path, f.key)
		pagination := body["paginacao"].(map[string]any)
		for _, l := range body["dados"].([]any) {
			id := l.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("lead %s repeated across pages", id)
			}
			seen[id] = true
		}
		if int(pagination["quantidade"].(float64)) != len(body["dados"].([]any)) {
			t.Fatal("quantidade must match the page")
		}
		pages++
		if pagination["tem_mais"] != true {
			if pagination["proximo_cursor"] != nil {
				t.Fatal("proximo_cursor must be null on the last page")
			}
			break
		}
		cursor = pagination["proximo_cursor"].(string)
	}
	want, _ := f.store.Stats()
	unit := 0
	page, _ := f.store.LeadsPage(store.LeadFilter{UnitID: "un_centro", Limit: 500})
	unit = len(page.Leads)
	if len(seen) != unit || pages < 2 || want.Leads < unit {
		t.Fatalf("saw %d leads in %d pages, unit has %d", len(seen), pages, unit)
	}
}

func TestFilters(t *testing.T) {
	f := newFixture(t, 100)
	cases := map[string]int{
		"estagio=cliente":                      1,
		"fluxo_id=fl_reativacao":               2,
		"etapa_id=st_agendado":                 1,
		"fluxo_id=fl_agendamento&estagio=lead": 5,
	}
	for query, want := range cases {
		_, body := f.get("/api/v1/leads?"+query, f.key)
		if got := len(body["dados"].([]any)); got != want {
			t.Errorf("%s: %d leads, want %d", query, got, want)
		}
	}
}

func TestOptOutIsExposed(t *testing.T) {
	f := newFixture(t, 100)
	lead, _, _ := f.store.LeadByPhone("+5519991230001")
	_ = f.store.SetOptOut(lead.ID, true)
	_, body := f.get("/api/v1/leads?limite=200", f.key)
	for _, l := range body["dados"].([]any) {
		m := l.(map[string]any)
		if m["id"] == lead.ID && m["opt_out"] != true {
			t.Fatal("opt_out must reflect webhook updates")
		}
	}
}

func TestValidationErrorsListFields(t *testing.T) {
	f := newFixture(t, 100)
	res, body := f.get("/api/v1/leads?estagio=vip&limite=0&cursor=zzz", f.key)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", res.StatusCode)
	}
	fields := body["erro"].(map[string]any)["campos"].([]any)
	names := map[string]bool{}
	for _, item := range fields {
		names[item.(map[string]any)["campo"].(string)] = true
	}
	if !names["estagio"] || !names["limite"] {
		t.Fatalf("fields = %v", fields)
	}
	res, body = f.get("/api/v1/leads?cursor=zzz", f.key)
	campos := body["erro"].(map[string]any)["campos"].([]any)
	if res.StatusCode != 422 || campos[0].(map[string]any)["campo"] != "cursor" {
		t.Fatalf("bad cursor: %d %v", res.StatusCode, body)
	}
}

func TestRateLimit(t *testing.T) {
	f := newFixture(t, 3)
	for i := 0; i < 3; i++ {
		res, _ := f.get("/api/v1/unidades", f.key)
		if res.StatusCode != 200 {
			t.Fatalf("request %d: %d", i, res.StatusCode)
		}
		if got := res.Header.Get("RateLimit-Remaining"); got != []string{"2", "1", "0"}[i] {
			t.Fatalf("request %d: remaining %q", i, got)
		}
	}
	res, body := f.get("/api/v1/unidades", f.key)
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" || body["erro"] == nil {
		t.Fatalf("expected 429 with Retry-After: %d %v", res.StatusCode, res.Header)
	}
	if res.Header.Get("RateLimit-Reset") == "" {
		t.Fatal("RateLimit-Reset missing")
	}
}

func TestLimiterWindowResets(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewLimiter(1, time.Minute)
	l.now = func() time.Time { return now }
	if !l.Allow("k").Allowed || l.Allow("k").Allowed {
		t.Fatal("second call in the window must be refused")
	}
	if !l.Allow("other").Allowed {
		t.Fatal("limits are per key")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("k").Allowed {
		t.Fatal("a new window must allow again")
	}
}

func TestUnknownRoute(t *testing.T) {
	f := newFixture(t, 100)
	res, _ := f.get("/api/v1/nada", f.key)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestLimitAcceptsTheResgataPageSize(t *testing.T) {
	f := newFixture(t, 100)
	res, _ := f.get("/api/v1/leads?limite=500", f.key)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("limite=500 is what the Resgata backend asks for: %d", res.StatusCode)
	}
	res, _ = f.get("/api/v1/leads?limite=501", f.key)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("limite above the cap must be refused: %d", res.StatusCode)
	}
}
