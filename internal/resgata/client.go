package resgata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

type APIError struct {
	Status  int
	Type    string
	Message string
	Fields  map[string][]string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("resgata: %d %s: %s", e.Status, e.Type, e.Message)
}

func IsStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

func IsType(err error, errorType string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Type == errorType
}

func (c *Client) do(ctx context.Context, method, path, credential string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return err
	}

	if res.StatusCode >= 300 {
		return parseError(res.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(unwrap(raw), out)
}

func parseError(status int, raw []byte) error {
	var envelope struct {
		Error struct {
			Type    string              `json:"type"`
			Message string              `json:"message"`
			Fields  map[string][]string `json:"fields"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)
	apiErr := &APIError{Status: status, Type: envelope.Error.Type, Message: envelope.Error.Message, Fields: envelope.Error.Fields}
	if apiErr.Type == "" {
		apiErr.Type = "http_error"
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(status)
	}
	return apiErr
}

func unwrap(raw []byte) []byte {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Data) > 0 && envelope.Data[0] != 'n' {
		return envelope.Data
	}
	return raw
}

type Connection struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	ExternalRef string `json:"external_ref"`
	Status      string `json:"status"`
}

type SetupStep struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`
	Required  bool    `json:"required"`
	Detail    *string `json:"detail"`
	ActionURL *string `json:"action_url"`
}

func (s SetupStep) Complete() bool { return s.Status == "complete" }

type Setup struct {
	Ready bool        `json:"ready"`
	Steps []SetupStep `json:"steps"`
}

type Number struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Channel   string `json:"channel"`
	Connected bool   `json:"connected"`
	Status    string `json:"status"`
}

type Token struct {
	AccessToken  string `json:"access_token"`
	Scope        string `json:"scope"`
	ConnectionID string `json:"connection_id"`
	ExternalRef  string `json:"external_ref"`
}

func (c *Client) ExchangeCode(ctx context.Context, clientID, code, verifier, redirectURI string) (Token, error) {
	var token Token
	err := c.do(ctx, http.MethodPost, "/oauth/token", "", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  redirectURI,
	}, &token)
	return token, err
}

func (c *Client) Connection(ctx context.Context, token string) (Connection, error) {
	var out Connection
	err := c.do(ctx, http.MethodGet, "/connection", token, nil, &out)
	return out, err
}

func (c *Client) Setup(ctx context.Context, token string) (Setup, error) {
	var out Setup
	err := c.do(ctx, http.MethodGet, "/connection/setup", token, nil, &out)
	return out, err
}

func (c *Client) Disconnect(ctx context.Context, token string) error {
	return c.do(ctx, http.MethodDelete, "/connection", token, nil, nil)
}

func (c *Client) Numbers(ctx context.Context, token string) ([]Number, error) {
	var out []Number
	err := c.do(ctx, http.MethodGet, "/numbers", token, nil, &out)
	return out, err
}

func (c *Client) CreateSandboxConnection(ctx context.Context, appKey, externalRef string) (Connection, error) {
	var out Connection
	err := c.do(ctx, http.MethodPost, "/connections", appKey, map[string]string{"external_ref": externalRef}, &out)
	return out, err
}

func (c *Client) IssueConnectionToken(ctx context.Context, appKey, connectionID string) (string, error) {
	var out struct {
		Secret string `json:"secret"`
	}
	err := c.do(ctx, http.MethodPost, "/connections/"+url.PathEscape(connectionID)+"/tokens", appKey, nil, &out)
	return out.Secret, err
}

type SandboxEventRequest struct {
	Type     string `json:"type"`
	Phone    string `json:"phone,omitempty"`
	Text     string `json:"text,omitempty"`
	NumberID int    `json:"number_id,omitempty"`
}

func (c *Client) FireSandboxEvent(ctx context.Context, token string, req SandboxEventRequest) error {
	return c.do(ctx, http.MethodPost, "/sandbox/events", token, req, nil)
}

func (c *Client) SandboxInbound(ctx context.Context, token, from, text string) error {
	return c.do(ctx, http.MethodPost, "/sandbox/inbound-messages", token, map[string]string{"from": from, "text": text}, nil)
}

func (c *Client) SandboxOptOut(ctx context.Context, token, phone string) error {
	return c.do(ctx, http.MethodPost, "/sandbox/opt-outs", token, map[string]string{"phone": phone}, nil)
}

func (c *Client) CreateSandboxNumber(ctx context.Context, token, name, status string) (Number, error) {
	var out Number
	err := c.do(ctx, http.MethodPost, "/sandbox/numbers", token, map[string]string{"name": name, "status": status}, &out)
	return out, err
}

func (c *Client) SetSandboxNumberStatus(ctx context.Context, token string, id int, status string) error {
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("/sandbox/numbers/%d", id), token, map[string]string{"status": status}, nil)
}

func (c *Client) ResetSandbox(ctx context.Context, token string) error {
	return c.do(ctx, http.MethodPost, "/sandbox/reset", token, nil, nil)
}
