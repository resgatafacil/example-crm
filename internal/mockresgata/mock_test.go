package mockresgata

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type harness struct {
	t      *testing.T
	server *httptest.Server
	token  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	mock := New(Options{ClientID: "app_test", Speed: 10 * time.Millisecond})
	server := httptest.NewServer(mock.Handler())
	t.Cleanup(server.Close)
	h := &harness{t: t, server: server}

	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	h.decode(h.call(http.MethodPost, "/connections", map[string]any{"external_ref": "t"}, ""), &created)
	var issued struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	h.decode(h.call(http.MethodPost, "/connections/"+created.Data.ID+"/tokens", nil, ""), &issued)
	h.token = issued.Data.Secret
	return h
}

func (h *harness) call(method, path string, body any, token string) *http.Response {
	h.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, h.server.URL+path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func (h *harness) api(method, path string, body any) *http.Response {
	h.t.Helper()
	return h.call(method, path, body, h.token)
}

func (h *harness) decode(res *http.Response, out any) {
	h.t.Helper()
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) errorType(res *http.Response) (int, string) {
	h.t.Helper()
	var out struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	status := res.StatusCode
	h.decode(res, &out)
	return status, out.Error.Type
}

type dataEnvelope struct {
	Data       map[string]any `json:"data"`
	Duplicated bool           `json:"duplicated"`
}

func (h *harness) approvedTemplate(name string) string {
	h.t.Helper()
	var out dataEnvelope
	h.decode(h.api(http.MethodPost, "/templates", map[string]any{
		"name": name, "category": "MARKETING", "language": "pt_BR", "body": map[string]any{"text": "Oi {{1}}!", "examples": []string{"Ana"}},
	}), &out)
	id, _ := out.Data["id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got dataEnvelope
		h.decode(h.api(http.MethodGet, "/templates/"+id, nil), &got)
		if got.Data["status"] == "approved" {
			return id
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("template %s was never approved", name)
	return ""
}

func (h *harness) audience(phones ...string) string {
	h.t.Helper()
	contacts := make([]map[string]any, 0, len(phones))
	for _, phone := range phones {
		contacts = append(contacts, map[string]any{"phone": phone, "name": "Contato"})
	}
	var out dataEnvelope
	h.decode(h.api(http.MethodPost, "/audiences", map[string]any{"name": "Teste", "contacts": contacts}), &out)
	id, _ := out.Data["id"].(string)
	if id == "" {
		h.t.Fatalf("audience not created: %v", out)
	}
	return id
}

func TestRetiredMessagesAndUnknownRoutesAnswerJSON404(t *testing.T) {
	h := newHarness(t)
	for _, c := range [][2]string{{http.MethodPost, "/messages"}, {http.MethodGet, "/messages/msg_1"}, {http.MethodGet, "/nada"}} {
		status, kind := h.errorType(h.api(c[0], c[1], map[string]any{"to": "5511999990001", "text": "oi"}))
		if status != http.StatusNotFound || kind != "not_found" {
			t.Errorf("%s %s: %d %s", c[0], c[1], status, kind)
		}
	}
}

func TestTemplateValidationApprovalAndRejection(t *testing.T) {
	h := newHarness(t)

	res := h.api(http.MethodPost, "/templates", map[string]any{"name": "", "category": "X", "body": map[string]any{"text": ""}})
	var failed struct {
		Error struct {
			Fields map[string][]string `json:"fields"`
		} `json:"error"`
	}
	status := res.StatusCode
	h.decode(res, &failed)
	if status != http.StatusUnprocessableEntity || failed.Error.Fields["name"] == nil || failed.Error.Fields["category"] == nil || failed.Error.Fields["body.text"] == nil {
		t.Fatalf("validation: %d %v", status, failed)
	}

	res = h.api(http.MethodPost, "/templates", map[string]any{"name": "x", "category": "MARKETING", "body": map[string]any{"text": "Oi {{1}} e {{2}}", "examples": []string{"a"}}})
	status = res.StatusCode
	h.decode(res, &failed)
	if status != http.StatusUnprocessableEntity || failed.Error.Fields["body.examples"] == nil {
		t.Fatalf("examples must match the variables: %d %v", status, failed)
	}

	var created dataEnvelope
	res = h.api(http.MethodPost, "/templates", map[string]any{"name": "Promoção da Semana", "category": "marketing", "body": map[string]any{"text": "Sem variáveis"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", res.StatusCode)
	}
	h.decode(res, &created)
	if created.Data["name"] != "promocao_da_semana" || created.Data["status"] != "pending" {
		t.Fatalf("name must be normalized and start pending: %v", created.Data)
	}

	var again dataEnvelope
	res = h.api(http.MethodPost, "/templates", map[string]any{"name": "promocao da semana", "category": "MARKETING", "body": map[string]any{"text": "Sem variáveis"}})
	status = res.StatusCode
	h.decode(res, &again)
	if status != http.StatusOK || !again.Duplicated || again.Data["id"] != created.Data["id"] {
		t.Fatalf("a repeated template must come back as a duplicate: %d %v", status, again)
	}

	h.decode(h.api(http.MethodPost, "/templates", map[string]any{"name": "teste_reject", "category": "UTILITY", "body": map[string]any{"text": "x"}}), &created)
	id := created.Data["id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got dataEnvelope
		h.decode(h.api(http.MethodGet, "/templates/"+id, nil), &got)
		if got.Data["status"] == "rejected" {
			if got.Data["rejection_reason"] != "INVALID_FORMAT" {
				t.Fatalf("reason = %v", got.Data["rejection_reason"])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the _reject template was never rejected")
}

func TestAudienceRejectionsAndLimits(t *testing.T) {
	h := newHarness(t)
	var out dataEnvelope
	h.decode(h.api(http.MethodPost, "/audiences", map[string]any{"name": "x", "contacts": []map[string]any{
		{"phone": "(11) 98888-0001"}, {"phone": "11988880001"}, {"phone": "123"}, {"phone": "5500000000011"},
	}}), &out)
	contacts := out.Data["contacts"].(map[string]any)
	rejections := out.Data["rejections"].(map[string]any)
	if contacts["accepted"] != float64(2) || contacts["rejected"] != float64(2) || rejections["invalid"] != float64(1) || rejections["duplicated"] != float64(1) {
		t.Fatalf("audience = %v", out.Data)
	}

	status, kind := h.errorType(h.api(http.MethodPost, "/audiences", map[string]any{"contacts": []map[string]any{{"phone": "1"}}}))
	if status != http.StatusUnprocessableEntity || kind != "no_valid_contacts" {
		t.Fatalf("no valid contact: %d %s", status, kind)
	}
	many := make([]map[string]any, maxMockAudience+1)
	for i := range many {
		many[i] = map[string]any{"phone": "11988880001"}
	}
	if status, kind := h.errorType(h.api(http.MethodPost, "/audiences", map[string]any{"contacts": many})); status != http.StatusUnprocessableEntity || kind != "invalid_request_error" {
		t.Fatalf("limit: %d %s", status, kind)
	}
}

func TestCampaignGuardsAndDuplicates(t *testing.T) {
	h := newHarness(t)
	templateID := h.approvedTemplate("com_variavel")
	audienceID := h.audience("5500000000012")

	cases := []struct {
		name   string
		body   map[string]any
		status int
		kind   string
	}{
		{"missing ids", map[string]any{}, 422, "invalid_request_error"},
		{"unknown template", map[string]any{"template_id": "tpl_x", "audience_id": audienceID}, 404, "not_found"},
		{"unknown audience", map[string]any{"template_id": templateID, "audience_id": "aud_x"}, 404, "not_found"},
		{"missing variable", map[string]any{"template_id": templateID, "audience_id": audienceID}, 422, "invalid_request_error"},
	}
	for _, c := range cases {
		if status, kind := h.errorType(h.api(http.MethodPost, "/campaigns", c.body)); status != c.status || kind != c.kind {
			t.Errorf("%s: %d %s", c.name, status, kind)
		}
	}

	var pending dataEnvelope
	h.decode(h.api(http.MethodPost, "/templates", map[string]any{"name": "ainda_pending", "category": "MARKETING", "body": map[string]any{"text": "oi"}}), &pending)
	if status, kind := h.errorType(h.api(http.MethodPost, "/campaigns", map[string]any{"template_id": pending.Data["id"], "audience_id": audienceID})); status != 409 || kind != "template_not_approved" {
		t.Errorf("unapproved template: %d %s", status, kind)
	}

	body := map[string]any{"name": "Uma", "template_id": templateID, "audience_id": audienceID, "variables": map[string]string{"1": "@nome"}}
	var first, second dataEnvelope
	res := h.api(http.MethodPost, "/campaigns", body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", res.StatusCode)
	}
	h.decode(res, &first)
	res = h.api(http.MethodPost, "/campaigns", body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("duplicate status %d", res.StatusCode)
	}
	h.decode(res, &second)
	if !second.Duplicated || second.Data["id"] != first.Data["id"] || !strings.HasPrefix(first.Data["id"].(string), "cmp_") {
		t.Fatalf("duplicate = %v", second)
	}
}

func TestCampaignProgressPauseAndResume(t *testing.T) {
	h := newHarness(t)
	templateID := h.approvedTemplate("fluxo")
	audienceID := h.audience("5511988880001", "5500000000000", "5500000000011")

	var created dataEnvelope
	h.decode(h.api(http.MethodPost, "/campaigns", map[string]any{"template_id": templateID, "audience_id": audienceID, "variables": map[string]string{"1": "@nome"}}), &created)
	id := created.Data["id"].(string)

	if status, kind := h.errorType(h.api(http.MethodPost, "/campaigns/"+id+"/resume", nil)); status != 409 || kind != "campaign_not_paused" {
		t.Fatalf("resume a running campaign: %d %s", status, kind)
	}

	var finished dataEnvelope
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		h.decode(h.api(http.MethodGet, "/campaigns/"+id, nil), &finished)
		if finished.Data["status"] == "finished" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	progress := finished.Data["progress"].(map[string]any)
	if finished.Data["status"] != "finished" || progress["total"] != float64(3) || progress["sent"] != float64(2) || progress["failed"] != float64(1) || progress["delivered"] != float64(2) || progress["read"] != float64(1) || progress["percent"] != float64(100) {
		t.Fatalf("campaign = %v", finished.Data)
	}
	if status, kind := h.errorType(h.api(http.MethodPost, "/campaigns/"+id+"/pause", nil)); status != 409 || kind != "campaign_not_active" {
		t.Fatalf("pause a finished campaign: %d %s", status, kind)
	}

	var list struct {
		Data []map[string]any `json:"data"`
	}
	h.decode(h.api(http.MethodGet, "/campaigns", nil), &list)
	if len(list.Data) != 1 || list.Data[0]["id"] != id {
		t.Fatalf("list = %v", list.Data)
	}
}

func TestQueuedNumbersKeepTheCampaignRunning(t *testing.T) {
	h := newHarness(t)
	templateID := h.approvedTemplate("fila")
	audienceID := h.audience("5500000000012", "5511988880001")
	var created dataEnvelope
	h.decode(h.api(http.MethodPost, "/campaigns", map[string]any{"template_id": templateID, "audience_id": audienceID, "variables": map[string]string{"1": "@nome"}}), &created)
	time.Sleep(300 * time.Millisecond)
	var got dataEnvelope
	h.decode(h.api(http.MethodGet, "/campaigns/"+created.Data["id"].(string), nil), &got)
	if got.Data["status"] != "running" {
		t.Fatalf("a message stuck in the queue keeps the campaign open, got %v", got.Data["status"])
	}
}

func TestSetupFollowsMetaAndTemplates(t *testing.T) {
	h := newHarness(t)
	var before struct {
		Data struct {
			Ready bool             `json:"ready"`
			Steps []map[string]any `json:"steps"`
		} `json:"data"`
		Ready bool             `json:"ready"`
		Steps []map[string]any `json:"steps"`
	}
	h.decode(h.api(http.MethodGet, "/connection/setup", nil), &before)
	ids := map[string]string{}
	for _, step := range before.Steps {
		ids[step["id"].(string)] = step["status"].(string)
	}
	if before.Ready || ids["meta_linked"] != "complete" || ids["template_available"] != "incomplete" || len(ids) != 12 {
		t.Fatalf("setup = %v", ids)
	}

	h.approvedTemplate("pronta")
	h.decode(h.api(http.MethodGet, "/connection/setup", nil), &before)
	if !before.Ready {
		t.Fatal("meta linked plus an approved template must be ready")
	}
}
