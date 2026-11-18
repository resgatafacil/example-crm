package resgata

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testSecret = randomSecret()

func randomSecret() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

var testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func header(t time.Time, body string, extraV1 ...string) string {
	h := "t=" + strconv.FormatInt(t.Unix(), 10)
	for _, v := range extraV1 {
		h += ",v1=" + v
	}
	return h
}

func TestVerifyAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	value := SignatureHeaderValue(testSecret, testNow.Unix(), body)
	if err := Verify(testSecret, value, body, testNow); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsTamperedBodyAndWrongSecret(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	value := SignatureHeaderValue(testSecret, testNow.Unix(), body)
	if err := Verify(testSecret, value, []byte(`{"id":"evt_2"}`), testNow); err != ErrMismatch {
		t.Fatalf("tampered body: %v", err)
	}
	if err := Verify(randomSecret(), value, body, testNow); err != ErrMismatch {
		t.Fatalf("wrong secret: %v", err)
	}
}

func TestVerifyToleranceWindow(t *testing.T) {
	body := []byte("{}")
	cases := map[string]struct {
		offset time.Duration
		want   error
	}{
		"just inside past":   {-299 * time.Second, nil},
		"exactly at limit":   {-300 * time.Second, nil},
		"too old":            {-301 * time.Second, ErrExpired},
		"just inside future": {299 * time.Second, nil},
		"too far in future":  {301 * time.Second, ErrExpired},
	}
	for name, c := range cases {
		signedAt := testNow.Add(c.offset)
		value := SignatureHeaderValue(testSecret, signedAt.Unix(), body)
		if err := Verify(testSecret, value, body, testNow); err != c.want {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

func TestVerifyAcceptsAnyOfSeveralV1Values(t *testing.T) {
	body := []byte(`{"id":"evt_rotated"}`)
	good := Sign(testSecret, testNow.Unix(), body)
	old := Sign(randomSecret(), testNow.Unix(), body)

	for name, value := range map[string]string{
		"good first": header(testNow, "", good, old),
		"good last":  header(testNow, "", old, good),
	} {
		if err := Verify(testSecret, value, body, testNow); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := Verify(testSecret, header(testNow, "", old, old), body, testNow); err != ErrMismatch {
		t.Fatalf("no matching v1: %v", err)
	}
}

func TestVerifyMalformedHeaders(t *testing.T) {
	body := []byte("{}")
	cases := map[string]error{
		"":             ErrNoSignature,
		"   ":          ErrNoSignature,
		"garbage":      ErrMalformed,
		"t=abc,v1=aa":  ErrMalformed,
		"t=1767225600": ErrMalformed,
		"v1=aa":        ErrMalformed,
		"t=0,v1=aa":    ErrMalformed,
	}
	for value, want := range cases {
		if err := Verify(testSecret, value, body, testNow); err != want {
			t.Errorf("%q: got %v, want %v", value, err, want)
		}
	}
}

func TestSignatureIsOverTimestampDotBody(t *testing.T) {
	body := []byte("corpo")
	if Sign(testSecret, 1, body) == Sign(testSecret, 2, body) {
		t.Fatal("timestamp must be part of the signed payload")
	}
	if !strings.HasPrefix(SignatureHeaderValue(testSecret, 7, body), "t=7,v1=") {
		t.Fatal("unexpected header format")
	}
}

func TestParseSignatureKeepsEveryV1(t *testing.T) {
	ts, sigs, err := ParseSignature("t=10, v1=aa, v1=bb")
	if err != nil || ts != 10 || len(sigs) != 2 || sigs[1] != "bb" {
		t.Fatalf("%d %v %v", ts, sigs, err)
	}
}
