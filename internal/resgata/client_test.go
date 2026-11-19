package resgata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientUnwrapsDataAndSendsBearer(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/numbers":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": 1, "name": "Principal", "status": "connected", "connected": true}}})
		case "/connection/setup":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "connection_setup", "ready": false, "steps": []map[string]any{{"id": "number", "title": "Número", "status": "incomplete", "required": true, "action_url": "https://x/numeros"}}})
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, nil)

	numbers, err := client.Numbers(context.Background(), "tok")
	if err != nil || len(numbers) != 1 || numbers[0].Status != "connected" || gotAuth != "Bearer tok" {
		t.Fatalf("numbers: %+v %v auth=%q", numbers, err, gotAuth)
	}
	setup, err := client.Setup(context.Background(), "tok")
	if err != nil || setup.Ready || len(setup.Steps) != 1 || setup.Steps[0].ActionURL == nil {
		t.Fatalf("setup: %+v %v", setup, err)
	}
}

func TestClientParsesErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"type": "invalid_request_error", "message": "O modelo tem campos inválidos.",
			"fields": map[string]any{"body.text": []string{"O texto da mensagem é obrigatório."}},
		}})
	}))
	defer server.Close()

	_, err := NewClient(server.URL, nil).CreateTemplate(context.Background(), "tok", TemplateInput{Name: "x"})
	var apiErr *APIError
	if !IsStatus(err, 422) || !IsType(err, "invalid_request_error") || !errors.As(err, &apiErr) || len(apiErr.Fields["body.text"]) != 1 {
		t.Fatalf("err = %v", err)
	}
}

func TestRetiredMessagesEndpointIsNotInTheClient(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "cmp_1", "status": "running", "progress": map[string]any{"total": 2}}})
	}))
	defer server.Close()
	client := NewClient(server.URL, nil)
	ctx := context.Background()

	if _, err := client.CreateCampaign(ctx, "tok", CampaignInput{Name: "n", TemplateID: "tpl_1", AudienceID: "aud_1", Variables: map[string]string{"1": "@nome"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PauseCampaign(ctx, "tok", "cmp_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResumeCampaign(ctx, "tok", "cmp_1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /campaigns", "POST /campaigns/cmp_1/pause", "POST /campaigns/cmp_1/resume"}
	for i, path := range want {
		if i >= len(paths) || paths[i] != path {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

func TestCampaignAndTemplateDecoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
				"id": "tpl_1", "name": "promo", "category": "MARKETING", "status": "approved", "rejection_reason": nil,
				"components": []map[string]any{{"type": "BODY", "text": "Oi {{1}}!"}}, "variables": map[string]any{"count": 1, "placeholders": []int{1}},
			}}, "meta": map[string]any{"next_cursor": nil}})
		case "/audiences":
			var body struct {
				Contacts []AudienceContact `json:"contacts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Contacts) != 1 || body.Contacts[0].Variables[0] != "Dentista" {
				t.Errorf("audience body = %+v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "aud_1", "contacts": map[string]any{"received": 1, "accepted": 1, "rejected": 0}}})
		case "/connection/meta-link":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"url": "https://meta.example/link"}})
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, nil)
	ctx := context.Background()

	templates, err := client.Templates(ctx, "tok", "")
	if err != nil || len(templates) != 1 || !templates[0].Usable() || templates[0].Body() != "Oi {{1}}!" || templates[0].Variables.Count != 1 {
		t.Fatalf("templates = %+v %v", templates, err)
	}
	audience, err := client.CreateAudience(ctx, "tok", "Leads", []AudienceContact{{Phone: "5511999990001", Name: "Maria", Variables: []string{"Dentista"}}})
	if err != nil || audience.ID != "aud_1" || audience.Contacts.Accepted != 1 {
		t.Fatalf("audience = %+v %v", audience, err)
	}
	link, err := client.StartMetaLink(ctx, "tok", "https://crm/integracao", "")
	if err != nil || link != "https://meta.example/link" {
		t.Fatalf("link = %q %v", link, err)
	}
}

func TestPlaceholders(t *testing.T) {
	got := Placeholders("Oi {{2}}, {{ 1 }} e {{2}} de novo")
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("placeholders = %v", got)
	}
	if Placeholders("sem variáveis") != nil {
		t.Fatal("no placeholders expected")
	}
}

func TestExchangeCodeSendsPKCEVerifier(t *testing.T) {
	var body map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "connection_id": "conn_1", "scope": "numbers:read", "external_ref": "ref"})
	}))
	defer server.Close()

	token, err := NewClient(server.URL, nil).ExchangeCode(context.Background(), "app_x", "code1", "verifier1", "https://crm/cb")
	if err != nil || token.AccessToken != "tok" || token.ConnectionID != "conn_1" {
		t.Fatalf("%+v %v", token, err)
	}
	if body["grant_type"] != "authorization_code" || body["code_verifier"] != "verifier1" || body["redirect_uri"] != "https://crm/cb" || body["client_id"] != "app_x" {
		t.Fatalf("unexpected body %v", body)
	}
}

func TestFailureReasons(t *testing.T) {
	for _, code := range []string{"131026", "131047", "131048", "131049", "131056"} {
		if FailureReason(code) == FailureReason("") {
			t.Errorf("%s has no specific reason", code)
		}
	}
}

func TestMessageDataErrorCodeAcceptsNumbersAndStrings(t *testing.T) {
	var asNumber, asString MessageData
	_ = json.Unmarshal([]byte(`{"error":{"code":131049}}`), &asNumber)
	_ = json.Unmarshal([]byte(`{"error":{"code":"131049"}}`), &asString)
	if asNumber.ErrorCode() != "131049" || asString.ErrorCode() != "131049" {
		t.Fatalf("%q %q", asNumber.ErrorCode(), asString.ErrorCode())
	}
}
