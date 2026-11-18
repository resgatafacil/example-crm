package web

import (
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
	"unicode"

	"resgata-example-crm/internal/resgata"
	"resgata-example-crm/internal/store"
)

func asAPIError(err error, target **resgata.APIError) bool {
	return errors.As(err, target)
}

var stageLabels = map[string]string{"lead": "Lead", "avaliado": "Avaliado", "cliente": "Cliente"}

var messageStatusLabels = map[string]string{
	"queued": "Na fila", "sent": "Enviada", "delivered": "Entregue", "read": "Lida", "failed": "Falhou",
}

var numberStatusLabels = map[string]string{
	"connected": "Conectado", "disconnected": "Desconectado", "restricted": "Restrito", "banned": "Banido", "qr_pending": "Aguardando QR code",
}

var numberStatusClasses = map[string]string{
	"connected": "ok", "disconnected": "muted", "restricted": "warn", "banned": "err", "qr_pending": "info",
}

var messageStatusClasses = map[string]string{
	"queued": "muted", "sent": "info", "delivered": "ok", "read": "ok", "failed": "err",
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"icon":          icon,
		"datetime":      s.datetime,
		"ago":           s.ago,
		"stageLabel":    func(v string) string { return orDefault(stageLabels, v) },
		"msgLabel":      func(v string) string { return orDefault(messageStatusLabels, v) },
		"msgClass":      func(v string) string { return orDefault(messageStatusClasses, v) },
		"numLabel":      func(v string) string { return orDefault(numberStatusLabels, v) },
		"numClass":      func(v string) string { return orDefault(numberStatusClasses, v) },
		"initials":      initials,
		"phone":         formatPhone,
		"failure":       resgata.FailureReason,
		"pretty":        prettyJSON,
		"safeURL":       safeURL,
		"stages":        func() []string { return store.Stages },
		"eventClass":    eventClass,
		"hasPrefix":     strings.HasPrefix,
		"stringsFields": strings.Fields,
		"deref":         func(t *time.Time) time.Time { return *t },
		"join":          strings.Join,
		"add":           func(a, b int) int { return a + b },
		"sub":           func(a, b int) int { return a - b },
		"numberStates":  func() []string { return []string{"connected", "disconnected", "restricted", "banned", "qr_pending"} },
		"eventTypes":    func() []string { return resgata.EventTypes },
	}
}

func orDefault(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return key
}

func (s *Server) datetime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(s.loc).Format("02/01/2006 15:04")
}

func (s *Server) ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := s.now().Sub(t)
	switch {
	case d < 45*time.Second:
		return "agora mesmo"
	case d < 90*time.Minute:
		return fmt.Sprintf("há %d min", max(1, int(d.Minutes()+0.5)))
	case d < 36*time.Hour:
		return fmt.Sprintf("há %d h", int(d.Hours()+0.5))
	default:
		return fmt.Sprintf("há %d dias", int(d.Hours()/24))
	}
}

func initials(name string) string {
	var out []rune
	for _, word := range strings.Fields(name) {
		first := []rune(word)[0]
		if unicode.IsLetter(first) {
			out = append(out, unicode.ToUpper(first))
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

func formatPhone(e164 string) string {
	digits := store.Digits(e164)
	if strings.HasPrefix(digits, "55") && len(digits) == 13 {
		return fmt.Sprintf("+55 %s %s-%s", digits[2:4], digits[4:9], digits[9:])
	}
	if strings.HasPrefix(digits, "55") && len(digits) == 12 {
		return fmt.Sprintf("+55 %s %s-%s", digits[2:4], digits[4:8], digits[8:])
	}
	if digits == "" {
		return "—"
	}
	return "+" + digits
}

func safeURL(raw *string) string {
	if raw == nil {
		return ""
	}
	parsed, err := url.Parse(*raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return parsed.String()
}

func eventClass(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "message.failed"):
		return "err"
	case strings.HasPrefix(eventType, "message."):
		return "info"
	case strings.HasPrefix(eventType, "contact."):
		return "warn"
	case strings.HasPrefix(eventType, "number."):
		return "violet"
	case strings.HasPrefix(eventType, "connection."):
		return "ok"
	default:
		return "muted"
	}
}
