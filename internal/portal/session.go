package portal

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const (
	sessionContext = "funil-demo/session-cookie/v1"
	workspaceLabel = "funil-demo/workspace/v1:"
	WorkspaceIDLen = 16
)

func WorkspaceID(subject string) string {
	sum := sha256.Sum256([]byte(workspaceLabel + subject))
	return hex.EncodeToString(sum[:])[:WorkspaceIDLen]
}

func ValidWorkspaceID(id string) bool {
	if len(id) != WorkspaceIDLen {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

type Sessions struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

func NewSessions(encryptionKey []byte, ttl time.Duration) (*Sessions, error) {
	key, err := hkdf.Key(sha256.New, encryptionKey, nil, sessionContext, 32)
	if err != nil {
		return nil, err
	}
	return &Sessions{key: key, ttl: ttl, now: time.Now}, nil
}

func (s *Sessions) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Sessions) TTL() time.Duration {
	return s.ttl
}

func (s *Sessions) Issue(workspaceID string) (string, time.Time) {
	expires := s.now().Add(s.ttl)
	payload := workspaceID + "." + strconv.FormatInt(expires.Unix(), 10)
	return payload + "." + s.sign(payload), expires
}

func (s *Sessions) Parse(value string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || !ValidWorkspaceID(parts[0]) {
		return "", false
	}
	given, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", false
	}
	expected, _ := base64.RawURLEncoding.DecodeString(s.sign(parts[0] + "." + parts[1]))
	if subtle.ConstantTimeCompare(given, expected) != 1 {
		return "", false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !s.now().Before(time.Unix(expires, 0)) {
		return "", false
	}
	return parts[0], true
}

func (s *Sessions) sign(payload string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
