package resgata

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
)

var DefaultScopes = []string{"numbers:read", "messages:send", "messages:read", "contacts:read"}

func randomURLSafe(bytes int) string {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func NewVerifier() string {
	return randomURLSafe(32)
}

func NewState() string {
	return randomURLSafe(24)
}

func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type AuthorizeParams struct {
	ClientID    string
	RedirectURI string
	State       string
	Verifier    string
	ExternalRef string
	Scopes      []string
}

func AuthorizeURL(base string, p AuthorizeParams) string {
	query := url.Values{}
	query.Set("client_id", p.ClientID)
	query.Set("redirect_uri", p.RedirectURI)
	query.Set("state", p.State)
	query.Set("code_challenge", Challenge(p.Verifier))
	query.Set("code_challenge_method", "S256")
	query.Set("scope", strings.Join(p.Scopes, " "))
	if p.ExternalRef != "" {
		query.Set("external_ref", p.ExternalRef)
	}
	return base + "?" + query.Encode()
}

func AuthorizeEndpoint(appURL, apiURL string, sandbox bool) string {
	if sandbox {
		return apiURL + "/oauth/sandbox/authorize"
	}
	return appURL + "/autorizar"
}

var oauthErrors = map[string]string{
	"access_denied":         "A autorização foi negada no Resgata Fácil. Nada foi conectado.",
	"external_ref_in_use":   "Este identificador já está ligado a outro Workspace neste aplicativo. Desconecte a ligação anterior ou use outro identificador.",
	"invalid_request_error": "O Resgata Fácil recusou o pedido de autorização. Confira o client_id e o redirect_uri cadastrados no portal.",
}

func OAuthErrorMessage(code string) string {
	if message, ok := oauthErrors[code]; ok {
		return message
	}
	return "O Resgata Fácil devolveu o erro \"" + code + "\" ao autorizar."
}
