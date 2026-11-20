package portal

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	clockSkew   = 5 * time.Second
	maxLifetime = 5 * time.Minute
	maxNameLen  = 80
)

var (
	ErrMalformed  = errors.New("token malformado")
	ErrSignature  = errors.New("a assinatura do token não confere")
	ErrExpired    = errors.New("token expirado")
	ErrNotYet     = errors.New("token emitido no futuro")
	ErrLifetime   = errors.New("validade do token longa demais")
	ErrIncomplete = errors.New("token sem identificador do desenvolvedor ou do token")
)

type Claims struct {
	Subject   string `json:"sub"`
	Name      string `json:"name"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	ID        string `json:"jti"`
}

func (c Claims) Expiry() time.Time {
	return time.Unix(c.ExpiresAt, 0)
}

func (c Claims) DisplayName() string {
	var cleaned strings.Builder
	for _, r := range c.Name {
		if r >= 0x20 && r != 0x7f {
			cleaned.WriteRune(r)
		}
	}
	name := strings.TrimSpace(cleaned.String())
	runes := []rune(name)
	if len(runes) > maxNameLen {
		name = string(runes[:maxNameLen])
	}
	return name
}

func (c Claims) FirstName() string {
	fields := strings.Fields(c.DisplayName())
	if len(fields) == 0 {
		return "dev"
	}
	return fields[0]
}

func Sign(secret string, claims Claims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + signature(secret, encoded), nil
}

func Verify(secret, token string, now time.Time) (Claims, error) {
	encoded, sig, found := strings.Cut(token, ".")
	if !found || encoded == "" || sig == "" || strings.Contains(sig, ".") {
		return Claims{}, ErrMalformed
	}
	given, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	expected := mac(secret, encoded)
	if subtle.ConstantTimeCompare(given, expected) != 1 {
		return Claims{}, ErrSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var claims Claims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return Claims{}, ErrMalformed
	}
	if claims.Subject == "" || claims.ID == "" {
		return Claims{}, ErrIncomplete
	}
	if now.After(claims.Expiry().Add(clockSkew)) {
		return Claims{}, ErrExpired
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(clockSkew)) {
		return Claims{}, ErrNotYet
	}
	if claims.Expiry().Sub(time.Unix(claims.IssuedAt, 0)) > maxLifetime {
		return Claims{}, ErrLifetime
	}
	return claims, nil
}

func mac(secret, message string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return h.Sum(nil)
}

func signature(secret, message string) string {
	return base64.RawURLEncoding.EncodeToString(mac(secret, message))
}

type ReplayGuard struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	swept time.Time
}

func NewReplayGuard() *ReplayGuard {
	return &ReplayGuard{seen: map[string]time.Time{}}
}

func (g *ReplayGuard) Use(id string, until, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Sub(g.swept) > time.Minute {
		for key, expires := range g.seen {
			if now.After(expires) {
				delete(g.seen, key)
			}
		}
		g.swept = now
	}
	if _, used := g.seen[id]; used {
		return false
	}
	g.seen[id] = until.Add(clockSkew)
	return true
}
