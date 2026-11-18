package events

import (
	"encoding/json"
	"fmt"

	"resgata-example-crm/internal/resgata"
	"resgata-example-crm/internal/store"
)

var statusRank = map[string]int{"queued": 0, "sent": 1, "delivered": 2, "read": 3}

func Apply(s *store.Store, event resgata.Event) (string, error) {
	switch event.Type {
	case resgata.EventMessageReceived:
		return messageReceived(s, event)
	case resgata.EventMessageSent, resgata.EventMessageDelivered, resgata.EventMessageRead, resgata.EventMessageFailed:
		return messageStatus(s, event)
	case resgata.EventContactOptedOut:
		return optedOut(s, event)
	case resgata.EventConnectionRevoked:
		return "connection revoked", s.RevokeConnection()
	case resgata.EventConnectionCreated, resgata.EventNumberConnected, resgata.EventNumberDisconnected, resgata.EventPing:
		return "recorded", nil
	default:
		return "unknown event type recorded", nil
	}
}

func messageReceived(s *store.Store, event resgata.Event) (string, error) {
	var data resgata.MessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return "", err
	}
	phone := store.E164(data.Message.From)
	lead, found, err := s.LeadByPhone(phone)
	if err != nil {
		return "", err
	}
	note := ""
	if !found {
		lead, err = createLeadFromInbound(s, phone, data.Message.FromName)
		if err != nil {
			return "", err
		}
		note = " (new lead)"
	}
	_, err = s.AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindMessageIn, Body: data.Message.Text, ExternalID: data.Message.ID})
	return "reply added to " + lead.Nome + note, err
}

func createLeadFromInbound(s *store.Store, phone, name string) (store.Lead, error) {
	flows, err := s.Flows()
	if err != nil || len(flows) == 0 {
		return store.Lead{}, fmt.Errorf("no flow to receive new leads: %v", err)
	}
	units, err := s.Units()
	if err != nil || len(units) == 0 {
		return store.Lead{}, fmt.Errorf("no unit to receive new leads: %v", err)
	}
	initial := flows[0].Steps[0]
	for _, st := range flows[0].Steps {
		if st.Inicial {
			initial = st
			break
		}
	}
	if name == "" {
		name = phone
	}
	id, err := s.CreateLead(store.LeadInput{Nome: name, Celular: phone, Estagio: "lead", UnitID: units[0].ID, StepID: initial.ID})
	if err != nil {
		return store.Lead{}, err
	}
	lead, _, err := s.Lead(id)
	return lead, err
}

func messageStatus(s *store.Store, event resgata.Event) (string, error) {
	var data resgata.MessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return "", err
	}
	status := data.Message.Status
	if status == "" {
		status = event.Type[len("message."):]
	}
	code := data.ErrorCode()

	entry, found, err := s.MessageByExternalID(data.Message.ID)
	if err != nil {
		return "", err
	}
	if !found {
		return addUnknownOutbound(s, data, status, code)
	}
	if !advances(entry.Status, status) {
		return fmt.Sprintf("kept %s (late %s event)", entry.Status, status), nil
	}
	return "message " + status, s.UpdateMessageStatus(entry.ID, status, code)
}

func addUnknownOutbound(s *store.Store, data resgata.MessageData, status, code string) (string, error) {
	lead, found, err := s.LeadByPhone(store.E164(data.Message.To))
	if err != nil || !found {
		return "message of an unknown contact ignored", err
	}
	_, err = s.AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindMessageOut, Body: data.Message.Text, Status: status, ErrorCode: code, ExternalID: data.Message.ID})
	return "message sent outside the CRM added to " + lead.Nome, err
}

func advances(current, next string) bool {
	if current == "failed" {
		return false
	}
	if next == "failed" {
		return true
	}
	return statusRank[next] > statusRank[current]
}

func optedOut(s *store.Store, event resgata.Event) (string, error) {
	var data resgata.OptOutData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return "", err
	}
	lead, found, err := s.LeadByPhone(store.E164(data.Contact.Phone))
	if err != nil {
		return "", err
	}
	if !found {
		return "opt-out of an unknown contact ignored", nil
	}
	if lead.OptOut {
		return "already opted out", nil
	}
	if err := s.SetOptOut(lead.ID, true); err != nil {
		return "", err
	}
	body := "Pediu para parar de receber mensagens."
	if data.MatchedPhrase != "" {
		body = fmt.Sprintf("Pediu para parar de receber mensagens (frase: \"%s\").", data.MatchedPhrase)
	}
	_, err = s.AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindOptOut, Body: body})
	return "opt-out recorded for " + lead.Nome, err
}
