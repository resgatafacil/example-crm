package events

import (
	"encoding/json"
	"fmt"
	"sync"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

var statusRank = map[string]int{"queued": 0, "sent": 1, "delivered": 2, "read": 3}

var messageMu sync.Mutex

func Apply(s *store.Store, event resgata.Event) (string, error) {
	switch event.Type {
	case resgata.EventMessageReceived:
		return messageReceived(s, event)
	case resgata.EventMessageSent, resgata.EventMessageDelivered, resgata.EventMessageRead, resgata.EventMessageFailed:
		return messageStatus(s, event)
	case resgata.EventContactOptedOut:
		return optedOut(s, event)
	case resgata.EventCampaignStarted, resgata.EventCampaignPaused, resgata.EventCampaignFinished, resgata.EventCampaignFailed:
		return campaignChanged(s, event)
	case resgata.EventTemplateApproved, resgata.EventTemplateRejected, resgata.EventTemplateChanged:
		return templateChanged(event)
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
	messageMu.Lock()
	defer messageMu.Unlock()
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
	if err := s.UpdateMessageStatus(entry.ID, status, code); err != nil {
		return "", err
	}
	return "message " + status, refreshCampaign(s, entry.CampaignID)
}

func refreshCampaign(s *store.Store, campaignID string) error {
	if campaignID == "" {
		return nil
	}
	return s.RefreshCampaignProgress(campaignID)
}

func addUnknownOutbound(s *store.Store, data resgata.MessageData, status, code string) (string, error) {
	lead, found, err := s.LeadByPhone(store.E164(data.Message.To))
	if err != nil || !found {
		return "message of an unknown contact ignored", err
	}
	body := data.Message.Text
	campaignID := data.Message.CampaignID
	if campaign, known, err := s.Campaign(campaignID); err == nil && known {
		body = fmt.Sprintf("Campanha “%s” · modelo %s", campaign.Name, campaign.TemplateName)
	} else {
		campaignID = ""
	}
	if _, err = s.AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindMessageOut, Body: body, Status: status, ErrorCode: code, ExternalID: data.Message.ID, CampaignID: campaignID}); err != nil {
		return "", err
	}
	if campaignID != "" {
		return "campaign message added to " + lead.Nome, refreshCampaign(s, campaignID)
	}
	return "message sent outside the CRM added to " + lead.Nome, nil
}

func campaignChanged(s *store.Store, event resgata.Event) (string, error) {
	var data resgata.CampaignEventData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return "", err
	}
	applied, err := ApplyCampaign(s, data.Campaign)
	if err != nil {
		return "", err
	}
	if !applied {
		return "campaign of another source ignored", nil
	}
	return "campaign " + data.Campaign.Name + " is " + data.Campaign.Status, nil
}

func ApplyCampaign(s *store.Store, remote resgata.Campaign) (bool, error) {
	local, found, err := s.Campaign(remote.ID)
	if err != nil || !found {
		return false, err
	}
	progress := store.Progress{
		Total: remote.Progress.Total, Sent: remote.Progress.Sent, Delivered: remote.Progress.Delivered,
		Read: remote.Progress.Read, Failed: remote.Progress.Failed, Skipped: remote.Progress.Skipped,
	}
	if progress.Total == 0 {
		progress.Total = local.Progress.Total
	}
	status, pauseReason := remote.Status, remote.PauseText()
	if local.Terminal() && !(store.Campaign{Status: remote.Status}).Terminal() {
		status, pauseReason = local.Status, local.PauseReason
	}
	if err := s.SetCampaignRemote(local.ID, status, pauseReason, progress); err != nil {
		return false, err
	}
	return true, s.RefreshCampaignProgress(local.ID)
}

func templateChanged(event resgata.Event) (string, error) {
	var data resgata.TemplateEventData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return "", err
	}
	return "template " + data.Template.Name + " is " + data.Template.Status, nil
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
