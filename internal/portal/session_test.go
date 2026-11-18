package portal

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func newSessions(t *testing.T, ttl time.Duration) (*Sessions, *time.Time) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	s, err := NewSessions(key, ttl)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	return s, &now
}

func TestWorkspaceIDIsStableAndHex(t *testing.T) {
	a, b := WorkspaceID("user-1"), WorkspaceID("user-1")
	if a != b || !ValidWorkspaceID(a) || a == WorkspaceID("user-2") {
		t.Fatalf("unexpected ids %q %q", a, WorkspaceID("user-2"))
	}
	if ValidWorkspaceID("../etc/passwd") || ValidWorkspaceID(strings.ToUpper(a)) || ValidWorkspaceID(a+"0") {
		t.Fatal("only 16 lowercase hex characters are valid ids")
	}
}

func TestSessionRoundTripAndExpiry(t *testing.T) {
	s, now := newSessions(t, time.Hour)
	id := WorkspaceID("user-1")
	value, expires := s.Issue(id)
	if expires != now.Add(time.Hour) {
		t.Fatalf("unexpected expiry %v", expires)
	}
	if got, ok := s.Parse(value); !ok || got != id {
		t.Fatalf("parse = %q %v", got, ok)
	}
	*now = now.Add(time.Hour + time.Second)
	if _, ok := s.Parse(value); ok {
		t.Fatal("an expired session must not parse")
	}
}

func TestSessionRejectsTampering(t *testing.T) {
	s, _ := newSessions(t, time.Hour)
	other, _ := newSessions(t, time.Hour)
	id, victim := WorkspaceID("user-1"), WorkspaceID("user-2")
	value, _ := s.Issue(id)
	parts := strings.Split(value, ".")

	cases := map[string]string{
		"other workspace":  victim + "." + parts[1] + "." + parts[2],
		"longer expiry":    parts[0] + ".9999999999." + parts[2],
		"bad signature":    parts[0] + "." + parts[1] + ".AAAA",
		"missing part":     parts[0] + "." + parts[1],
		"empty":            "",
		"signed elsewhere": func() string { v, _ := other.Issue(id); return v }(),
	}
	for name, forged := range cases {
		if _, ok := s.Parse(forged); ok {
			t.Errorf("%s: forged cookie accepted", name)
		}
	}
}
