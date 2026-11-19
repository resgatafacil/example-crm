package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/events"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

const (
	maxCampaignLeads = 500
	maxSyncedOnList  = 10
)

type variableSource struct {
	Key   string
	Label string
}

var variableSources = []variableSource{
	{"nome", "Nome do lead"},
	{"email", "E-mail do lead"},
	{"estagio", "Estágio do lead"},
	{"unidade", "Unidade do lead"},
	{"etapa", "Etapa do funil"},
	{"texto", "Texto fixo"},
}

func leadValue(lead store.Lead, source string) string {
	var value string
	switch source {
	case "nome":
		value = lead.Nome
	case "email":
		value = lead.Email
	case "estagio":
		value = stageLabels[lead.Estagio]
	case "unidade":
		value = lead.UnitName
	case "etapa":
		value = lead.StepName
	}
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

type variableMapping struct {
	N      int
	Source string
	Text   string
}

type campaignParams struct {
	TemplateID string
	StepID     string
	LeadID     string
	Name       string
	Sources    map[int]string
	Texts      map[int]string
}

func readCampaignParams(r *http.Request) campaignParams {
	_ = r.ParseForm()
	p := campaignParams{
		TemplateID: strings.TrimSpace(r.FormValue("modelo")),
		StepID:     r.FormValue("etapa"),
		LeadID:     r.FormValue("lead"),
		Name:       strings.TrimSpace(r.FormValue("nome")),
		Sources:    map[int]string{},
		Texts:      map[int]string{},
	}
	for key, values := range r.Form {
		if len(values) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(key, "var_"):
			if n, err := strconv.Atoi(strings.TrimPrefix(key, "var_")); err == nil {
				p.Sources[n] = values[0]
			}
		case strings.HasPrefix(key, "txt_"):
			if n, err := strconv.Atoi(strings.TrimPrefix(key, "txt_")); err == nil {
				p.Texts[n] = strings.TrimSpace(values[0])
			}
		}
	}
	return p
}

func validSource(key string) bool {
	for _, source := range variableSources {
		if source.Key == key {
			return true
		}
	}
	return false
}

func (p campaignParams) mappings(placeholders []int) []variableMapping {
	out := make([]variableMapping, 0, len(placeholders))
	for _, n := range placeholders {
		source := p.Sources[n]
		if !validSource(source) {
			source = "texto"
			if n == 1 {
				source = "nome"
			}
		}
		out = append(out, variableMapping{N: n, Source: source, Text: p.Texts[n]})
	}
	return out
}

type campaignNewData struct {
	Connected  bool
	Error      string
	Templates  []resgata.Template
	Template   *resgata.Template
	Mappings   []variableMapping
	Sources    []variableSource
	Flows      []store.Flow
	Params     campaignParams
	Recipients []store.Lead
	Excluded   int
	Truncated  bool
}

func (s *Server) campaignNewData(r *http.Request, token string, connected bool, params campaignParams) campaignNewData {
	data := campaignNewData{Connected: connected, Sources: variableSources, Params: params}
	data.Flows, _ = s.st(r).Flows()
	if !connected {
		return data
	}

	ctx, cancel := s.apiContext(r)
	defer cancel()
	templates, err := s.client.Templates(ctx, token, "")
	if err != nil {
		data.Error = s.describeAPIError(err)
	}
	for i, t := range templates {
		if !t.Usable() {
			continue
		}
		data.Templates = append(data.Templates, t)
		if t.ID == params.TemplateID {
			data.Template = &templates[i]
		}
	}
	if data.Template != nil {
		data.Mappings = params.mappings(resgata.Placeholders(data.Template.Body()))
	}
	data.Recipients, data.Excluded, data.Truncated = s.campaignRecipients(r, params)
	return data
}

func (s *Server) campaignRecipients(r *http.Request, params campaignParams) ([]store.Lead, int, bool) {
	var leads []store.Lead
	truncated := false
	if params.LeadID != "" {
		if lead, found, _ := s.st(r).Lead(params.LeadID); found {
			leads = []store.Lead{lead}
		}
	} else {
		page, err := s.st(r).LeadsPage(store.LeadFilter{StepID: params.StepID, Limit: maxCampaignLeads})
		if err == nil {
			leads, truncated = page.Leads, page.HasMore
		}
	}
	var sendable []store.Lead
	for _, lead := range leads {
		if !lead.OptOut {
			sendable = append(sendable, lead)
		}
	}
	return sendable, len(leads) - len(sendable), truncated
}

func (s *Server) campaignNewPage(w http.ResponseWriter, r *http.Request) {
	_, token, connected := s.token(r)
	data := s.campaignNewData(r, token, connected, readCampaignParams(r))
	s.render(w, r, "campaign_new", "Enviar campanha", "campaigns", data)
}

func (s *Server) campaignFormError(w http.ResponseWriter, r *http.Request, token string, params campaignParams, message string) {
	data := s.campaignNewData(r, token, true, params)
	data.Error = message
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.render(w, r, "campaign_new", "Enviar campanha", "campaigns", data)
}

type audiencePlan struct {
	Variables map[string]string
	Contacts  []resgata.AudienceContact
}

func buildAudience(leads []store.Lead, mappings []variableMapping) (audiencePlan, string) {
	plan := audiencePlan{Variables: map[string]string{}}
	var columns []string
	for _, m := range mappings {
		key := strconv.Itoa(m.N)
		switch m.Source {
		case "nome":
			plan.Variables[key] = "@nome"
		case "texto":
			if m.Text == "" {
				return plan, fmt.Sprintf("Escreva o texto fixo da variável {{%d}}.", m.N)
			}
			plan.Variables[key] = m.Text
		default:
			columns = append(columns, m.Source)
			plan.Variables[key] = "@variavel_" + strconv.Itoa(len(columns))
		}
	}
	for _, lead := range leads {
		contact := resgata.AudienceContact{Phone: store.Digits(lead.Celular), Name: lead.Nome}
		for _, column := range columns {
			contact.Variables = append(contact.Variables, leadValue(lead, column))
		}
		plan.Contacts = append(plan.Contacts, contact)
	}
	return plan, ""
}

func (s *Server) campaignCreate(w http.ResponseWriter, r *http.Request) {
	_, token, connected := s.token(r)
	if !connected {
		s.redirect(w, r, "/integracao", "error", "Conecte o CRM ao Resgata Fácil antes de criar campanhas.")
		return
	}
	params := readCampaignParams(r)

	ctx, cancel := s.apiContext(r)
	defer cancel()
	template, err := s.client.Template(ctx, token, params.TemplateID)
	if err != nil {
		s.campaignFormError(w, r, token, params, s.describeAPIError(err))
		return
	}
	if !template.Usable() {
		s.campaignFormError(w, r, token, params, apiErrorMessages["template_not_approved"])
		return
	}
	recipients, _, _ := s.campaignRecipients(r, params)
	if len(recipients) == 0 {
		s.campaignFormError(w, r, token, params, "Nenhum lead elegível: escolha outra etapa do funil. Leads com opt-out ficam de fora.")
		return
	}
	plan, problem := buildAudience(recipients, params.mappings(resgata.Placeholders(template.Body())))
	if problem != "" {
		s.campaignFormError(w, r, token, params, problem)
		return
	}

	name := params.Name
	if name == "" {
		name = "Campanha " + template.Name
	}
	audience, err := s.client.CreateAudience(ctx, token, name, plan.Contacts)
	if err != nil {
		s.campaignFormError(w, r, token, params, s.describeAPIError(err))
		return
	}
	created, err := s.client.CreateCampaign(ctx, token, resgata.CampaignInput{Name: name, TemplateID: template.ID, AudienceID: audience.ID, Variables: plan.Variables})
	if err != nil {
		s.campaignFormError(w, r, token, params, s.describeAPIError(err))
		return
	}

	local := store.Campaign{
		ID: created.ID, Name: created.Name, TemplateID: template.ID, TemplateName: template.Name, AudienceID: audience.ID,
		Status: created.Status, PauseReason: created.PauseText(), LeadCount: audience.Contacts.Accepted,
		Progress: store.Progress{Total: max(created.Progress.Total, audience.Contacts.Accepted)},
	}
	if existing, found, _ := s.st(r).Campaign(created.ID); found {
		local = existing
	}
	if err := s.st(r).SaveCampaign(local); err != nil {
		s.serverError(w, err)
		return
	}
	if _, err := events.ApplyCampaign(s.st(r), created); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/campanhas/"+created.ID, "success", campaignCreatedMessage(audience))
}

func campaignCreatedMessage(a resgata.Audience) string {
	message := fmt.Sprintf("Campanha criada para %d contato(s). O progresso chega por webhook.", a.Contacts.Accepted)
	if a.Contacts.Rejected > 0 {
		message += fmt.Sprintf(" %d lead(s) ficaram de fora porque o Resgata Fácil recusou o contato.", a.Contacts.Rejected)
	}
	return message
}

type campaignsData struct {
	Campaigns []store.Campaign
	Connected bool
}

func (s *Server) syncCampaign(r *http.Request, token string, c store.Campaign) store.Campaign {
	ctx, cancel := s.apiContext(r)
	defer cancel()
	remote, err := s.client.Campaign(ctx, token, c.ID)
	if err != nil {
		return c
	}
	if applied, err := events.ApplyCampaign(s.st(r), remote); err != nil || !applied {
		return c
	}
	if updated, found, _ := s.st(r).Campaign(c.ID); found {
		return updated
	}
	return c
}

func (s *Server) campaignsPage(w http.ResponseWriter, r *http.Request) {
	_, token, connected := s.token(r)
	list, err := s.st(r).Campaigns(100)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if connected {
		synced := 0
		for i, c := range list {
			if synced == maxSyncedOnList {
				break
			}
			if !c.Terminal() {
				list[i] = s.syncCampaign(r, token, c)
				synced++
			}
		}
	}
	s.render(w, r, "campaigns", "Campanhas", "campaigns", campaignsData{Campaigns: list, Connected: connected})
}

type campaignData struct {
	Campaign store.Campaign
	Messages []store.CampaignMessage
}

func (s *Server) loadCampaign(w http.ResponseWriter, r *http.Request) (store.Campaign, bool) {
	c, found, err := s.st(r).Campaign(r.PathValue("id"))
	if err != nil {
		s.serverError(w, err)
		return c, false
	}
	if !found {
		s.notFound(w, r)
		return c, false
	}
	return c, true
}

func (s *Server) campaignPage(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadCampaign(w, r)
	if !ok {
		return
	}
	if _, token, connected := s.token(r); connected && !c.Terminal() {
		c = s.syncCampaign(r, token, c)
	}
	data := campaignData{Campaign: c}
	data.Messages, _ = s.st(r).CampaignMessages(c.ID)
	s.render(w, r, "campaign", c.Name, "campaigns", data)
}

func (s *Server) campaignFragment(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadCampaign(w, r)
	if !ok {
		return
	}
	data := campaignData{Campaign: c}
	data.Messages, _ = s.st(r).CampaignMessages(c.ID)
	s.fragmentOf(w, "campaign", "campaignProgress", data)
}

func (s *Server) campaignAction(w http.ResponseWriter, r *http.Request, success string, call func(token, id string) (resgata.Campaign, error)) {
	c, ok := s.loadCampaign(w, r)
	if !ok {
		return
	}
	back := "/campanhas/" + c.ID
	_, token, connected := s.token(r)
	if !connected {
		s.redirect(w, r, "/integracao", "error", "Conecte o CRM ao Resgata Fácil antes de mudar uma campanha.")
		return
	}
	remote, err := call(token, c.ID)
	if err != nil {
		s.redirect(w, r, back, "error", s.describeAPIError(err))
		return
	}
	if _, err := events.ApplyCampaign(s.st(r), remote); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, back, "success", success)
}

func (s *Server) campaignPause(w http.ResponseWriter, r *http.Request) {
	s.campaignAction(w, r, "Campanha pausada. Os envios param até você retomar.", func(token, id string) (resgata.Campaign, error) {
		ctx, cancel := s.apiContext(r)
		defer cancel()
		return s.client.PauseCampaign(ctx, token, id)
	})
}

func (s *Server) campaignResume(w http.ResponseWriter, r *http.Request) {
	s.campaignAction(w, r, "Campanha retomada.", func(token, id string) (resgata.Campaign, error) {
		ctx, cancel := s.apiContext(r)
		defer cancel()
		return s.client.ResumeCampaign(ctx, token, id)
	})
}
