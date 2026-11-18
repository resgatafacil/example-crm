package portal

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func randomSecret(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func claimsAt(now time.Time) Claims {
	return Claims{Subject: "user-1", Name: "Maria Souza", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), ID: "jti-1"}
}

func TestVerifyAcceptsAValidToken(t *testing.T) {
	secret, now := randomSecret(t), time.Unix(1_800_000_000, 0)
	token, err := Sign(secret, claimsAt(now))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify(secret, token, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" || claims.FirstName() != "Maria" || claims.ID != "jti-1" {
		t.Fatalf("unexpected claims %+v", claims)
	}
}

func TestVerifyMatchesTheDocumentedFormat(t *testing.T) {
	secret, now := randomSecret(t), time.Unix(1_800_000_000, 0)
	token, _ := Sign(secret, claimsAt(now))
	parts := strings.Split(token, ".")
	if len(parts) != 2 || strings.ContainsAny(token, "=+/") {
		t.Fatalf("token must be two raw base64url parts, got %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !strings.Contains(string(payload), `"sub":"user-1"`) {
		t.Fatalf("payload part must be base64url JSON, got %q", payload)
	}
	if string(mac(secret, parts[0])) != mustDecode(t, parts[1]) {
		t.Fatal("signature must be HMAC-SHA256 over the base64url payload string")
	}
}

func mustDecode(t *testing.T, s string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestVerifyRejectsBadTokens(t *testing.T) {
	secret, now := randomSecret(t), time.Unix(1_800_000_000, 0)
	good, _ := Sign(secret, claimsAt(now))
	parts := strings.Split(good, ".")
	otherPayload, _ := Sign(secret, Claims{Subject: "someone-else", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), ID: "x"})
	expired, _ := Sign(secret, Claims{Subject: "u", IssuedAt: now.Add(-2 * time.Minute).Unix(), ExpiresAt: now.Add(-time.Minute).Unix(), ID: "j"})
	future, _ := Sign(secret, Claims{Subject: "u", IssuedAt: now.Add(time.Hour).Unix(), ExpiresAt: now.Add(time.Hour + time.Minute).Unix(), ID: "j"})
	long, _ := Sign(secret, Claims{Subject: "u", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(), ID: "j"})
	noSubject, _ := Sign(secret, Claims{IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), ID: "j"})
	wrongSecret, _ := Sign(randomSecret(t), claimsAt(now))
	notJSON := base64.RawURLEncoding.EncodeToString([]byte("not json"))

	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"empty", "", ErrMalformed},
		{"no dot", parts[0], ErrMalformed},
		{"three parts", good + ".extra", ErrMalformed},
		{"bad base64 signature", parts[0] + ".%%%", ErrMalformed},
		{"tampered signature", parts[0] + "." + parts[1][:len(parts[1])-2] + "AA", ErrSignature},
		{"payload swapped", strings.Split(otherPayload, ".")[0] + "." + parts[1], ErrSignature},
		{"wrong secret", wrongSecret, ErrSignature},
		{"payload not json", notJSON + "." + signature(secret, notJSON), ErrMalformed},
		{"expired", expired, ErrExpired},
		{"issued in the future", future, ErrNotYet},
		{"lifetime too long", long, ErrLifetime},
		{"missing subject", noSubject, ErrIncomplete},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Verify(secret, c.token, now); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestVerifyToleratesSmallClockSkew(t *testing.T) {
	secret, now := randomSecret(t), time.Unix(1_800_000_000, 0)
	token, _ := Sign(secret, claimsAt(now))
	if _, err := Verify(secret, token, now.Add(time.Minute+3*time.Second)); err != nil {
		t.Fatalf("3s after expiry must be tolerated: %v", err)
	}
	if _, err := Verify(secret, token, now.Add(time.Minute+10*time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("10s after expiry must be rejected, got %v", err)
	}
	if _, err := Verify(secret, token, now.Add(-3*time.Second)); err != nil {
		t.Fatalf("a clock 3s behind must be tolerated: %v", err)
	}
}

func TestReplayGuardAllowsEachIDOnce(t *testing.T) {
	guard, now := NewReplayGuard(), time.Unix(1_800_000_000, 0)
	until := now.Add(time.Minute)
	if !guard.Use("a", until, now) {
		t.Fatal("first use must pass")
	}
	if guard.Use("a", until, now.Add(time.Second)) {
		t.Fatal("second use must be refused")
	}
	if !guard.Use("b", until, now) {
		t.Fatal("another id must pass")
	}
	if !guard.Use("a", until, now.Add(10*time.Minute)) {
		t.Fatal("ids are forgotten after they could no longer verify")
	}
}

func TestDisplayNameIsCleaned(t *testing.T) {
	c := Claims{Name: "  Ana\x00 \x07Paula  " + strings.Repeat("x", 200)}
	if strings.ContainsAny(c.DisplayName(), "\x00\x07") || len([]rune(c.DisplayName())) > 80 {
		t.Fatalf("display name not cleaned: %q", c.DisplayName())
	}
	if c.FirstName() != "Ana" {
		t.Fatalf("first name = %q", c.FirstName())
	}
	if (Claims{}).FirstName() != "dev" {
		t.Fatal("empty names fall back to a neutral word")
	}
}
