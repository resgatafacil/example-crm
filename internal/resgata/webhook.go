package resgata

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	SignatureHeader = "X-Resgata-Signature"
	EventIDHeader   = "X-Resgata-Event-Id"
	DeliveryHeader  = "X-Resgata-Delivery-Id"
	AttemptHeader   = "X-Resgata-Delivery-Attempt"

	SignatureTolerance = 300 * time.Second
)

var (
	ErrNoSignature = errors.New("assinatura ausente")
	ErrMalformed   = errors.New("assinatura em formato inválido")
	ErrExpired     = errors.New("assinatura fora da janela de 5 minutos")
	ErrMismatch    = errors.New("assinatura não confere com o segredo")
)

func Sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func SignatureHeaderValue(secret string, timestamp int64, body []byte) string {
	return "t=" + strconv.FormatInt(timestamp, 10) + ",v1=" + Sign(secret, timestamp, body)
}

func ParseSignature(header string) (int64, []string, error) {
	if strings.TrimSpace(header) == "" {
		return 0, nil, ErrNoSignature
	}
	var timestamp int64
	var signatures []string
	for _, part := range strings.Split(header, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			return 0, nil, ErrMalformed
		}
		switch name {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return 0, nil, ErrMalformed
			}
			timestamp = parsed
		case "v1":
			signatures = append(signatures, value)
		}
	}
	if timestamp == 0 || len(signatures) == 0 {
		return 0, nil, ErrMalformed
	}
	return timestamp, signatures, nil
}

func Verify(secret, header string, body []byte, now time.Time) error {
	timestamp, signatures, err := ParseSignature(header)
	if err != nil {
		return err
	}

	age := now.Sub(time.Unix(timestamp, 0))
	if age < 0 {
		age = -age
	}
	if age > SignatureTolerance {
		return ErrExpired
	}

	expected := []byte(Sign(secret, timestamp, body))
	matched := false
	for _, candidate := range signatures {
		if hmac.Equal(expected, []byte(candidate)) {
			matched = true
		}
	}
	if !matched {
		return ErrMismatch
	}
	return nil
}
