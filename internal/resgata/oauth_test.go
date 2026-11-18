package resgata

import (
	"net/url"
	"strings"
	"testing"
)

func TestChallengeMatchesRFC7636Vector(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := Challenge(verifier); got != want {
		t.Fatalf("challenge = %s", got)
	}
}

func TestVerifierAndStateAreUniqueAndLongEnough(t *testing.T) {
	a, b := NewVerifier(), NewVerifier()
	if a == b || len(a) < 43 || len(a) > 128 {
		t.Fatalf("verifier length %d, unique %v", len(a), a != b)
	}
	if strings.ContainsAny(a, "+/=") {
		t.Fatal("verifier must be url-safe base64 without padding")
	}
	if NewState() == NewState() {
		t.Fatal("state must be random")
	}
}

func TestAuthorizeURLCarriesPKCEAndScopes(t *testing.T) {
	verifier := NewVerifier()
	raw := AuthorizeURL("https://resgata.example/autorizar", AuthorizeParams{
		ClientID: "app_x", RedirectURI: "https://crm.example/resgata/callback", State: "st",
		Verifier: verifier, ExternalRef: "ref-1", Scopes: DefaultScopes,
	})
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("code_challenge") != Challenge(verifier) || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("pkce params wrong: %v", q)
	}
	if q.Get("code_verifier") != "" || strings.Contains(raw, verifier) {
		t.Fatal("the verifier must never be sent to the authorize endpoint")
	}
	if q.Get("scope") != "numbers:read messages:send messages:read contacts:read" || q.Get("external_ref") != "ref-1" || q.Get("state") != "st" {
		t.Fatalf("unexpected params: %v", q)
	}
}

func TestAuthorizeEndpoint(t *testing.T) {
	if got := AuthorizeEndpoint("https://app", "https://api/v1", true); got != "https://api/v1/oauth/sandbox/authorize" {
		t.Fatal(got)
	}
	if got := AuthorizeEndpoint("https://app", "https://api/v1", false); got != "https://app/autorizar" {
		t.Fatal(got)
	}
}

func TestOAuthErrorMessages(t *testing.T) {
	if !strings.Contains(OAuthErrorMessage("access_denied"), "negada") || !strings.Contains(OAuthErrorMessage("weird"), "weird") {
		t.Fatal("unexpected messages")
	}
}
