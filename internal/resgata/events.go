package resgata

import (
	"encoding/json"
	"strconv"
)

const (
	EventConnectionCreated  = "connection.created"
	EventConnectionRevoked  = "connection.revoked"
	EventNumberConnected    = "number.connected"
	EventNumberDisconnected = "number.disconnected"
	EventMessageReceived    = "message.received"
	EventMessageSent        = "message.sent"
	EventMessageDelivered   = "message.delivered"
	EventMessageRead        = "message.read"
	EventMessageFailed      = "message.failed"
	EventContactOptedOut    = "contact.opted_out"
	EventPing               = "ping"
)

var EventTypes = []string{
	EventConnectionCreated, EventConnectionRevoked,
	EventNumberConnected, EventNumberDisconnected,
	EventMessageReceived, EventMessageSent, EventMessageDelivered, EventMessageRead, EventMessageFailed,
	EventContactOptedOut, EventPing,
}

type Event struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	APIVersion  string          `json:"api_version"`
	Created     int64           `json:"created"`
	Environment string          `json:"environment"`
	Connection  EventConnection `json:"connection"`
	Data        json.RawMessage `json:"data"`
}

type EventConnection struct {
	ID          string `json:"id"`
	ExternalRef string `json:"external_ref"`
}

type MessageData struct {
	Message struct {
		ID       string `json:"id"`
		Channel  string `json:"channel"`
		From     string `json:"from"`
		FromName string `json:"from_name"`
		To       string `json:"to"`
		Text     string `json:"text"`
		Status   string `json:"status"`
		NumberID int    `json:"number_id"`
	} `json:"message"`
	Error struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (m MessageData) ErrorCode() string {
	switch code := m.Error.Code.(type) {
	case string:
		return code
	case float64:
		return strconv.Itoa(int(code))
	default:
		return ""
	}
}

type OptOutData struct {
	Contact struct {
		Phone string `json:"phone"`
	} `json:"contact"`
	MatchedPhrase string `json:"matched_phrase"`
}

var failureReasons = map[string]string{
	"131026": "Mensagem não entregável: o número não tem WhatsApp ou está inacessível.",
	"131047": "Fora da janela de 24 horas: o contato precisa responder antes ou é preciso usar um modelo aprovado.",
	"131048": "Limite de spam atingido: a Meta limitou os envios deste número por excesso de denúncias ou bloqueios.",
	"131049": "A Meta não entregou para manter a saúde do ecossistema: o contato recebeu mensagens demais de empresas.",
	"131056": "Limite de envios para este mesmo contato atingido: espere um pouco antes de tentar de novo.",
}

func FailureReason(code string) string {
	if reason, ok := failureReasons[code]; ok {
		return reason
	}
	if code == "" {
		return "A mensagem falhou."
	}
	return "A mensagem falhou (código " + code + ")."
}
