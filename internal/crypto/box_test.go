package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestRoundTrip(t *testing.T) {
	sample := "valor-" + hex.EncodeToString(newKey(t)[:8])
	box, err := NewBox(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal(sample)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, sample) {
		t.Fatal("ciphertext leaks plaintext")
	}
	plain, err := box.Open(sealed)
	if err != nil || plain != sample {
		t.Fatalf("open = %q, %v", plain, err)
	}
}

func TestSealIsRandomised(t *testing.T) {
	box, _ := NewBox(newKey(t))
	a, _ := box.Seal("x")
	b, _ := box.Seal("x")
	if a == b {
		t.Fatal("two seals of the same value must differ")
	}
}

func TestWrongKeyAndTamperingFail(t *testing.T) {
	first, _ := NewBox(newKey(t))
	second, _ := NewBox(newKey(t))
	sealed, _ := first.Seal("x")
	if _, err := second.Open(sealed); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := first.Open(sealed[:len(sealed)-4] + "AAAA"); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}

func TestKeyLength(t *testing.T) {
	if _, err := NewBox([]byte("short")); err == nil {
		t.Fatal("short key must be rejected")
	}
}

func TestHashIsStable(t *testing.T) {
	if Hash("a") != Hash("a") || Hash("a") == Hash("b") {
		t.Fatal("hash must be deterministic and distinct")
	}
}
