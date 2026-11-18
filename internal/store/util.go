package store

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

func randomHex(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

var nonDigits = regexp.MustCompile(`\D`)

func Digits(phone string) string {
	return nonDigits.ReplaceAllString(phone, "")
}

func E164(phone string) string {
	digits := Digits(phone)
	if digits == "" {
		return ""
	}
	if len(digits) <= 11 {
		digits = "55" + digits
	}
	return "+" + digits
}
