package resgata

import (
	"context"
	"encoding/json"
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
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "not_available", "message": "indisponível"}})
	}))
	defer server.Close()

	_, err := NewClient(server.URL, nil).SendMessage(context.Background(), "tok", "5511999990000", "oi", 0)
	if !IsStatus(err, 501) || !IsType(err, "not_available") {
		t.Fatalf("err = %v", err)
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
