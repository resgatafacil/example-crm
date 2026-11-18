package web

import (
	"net/http"
	"strings"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

type leadFormData struct {
	Lead   store.LeadInput
	ID     string
	Units  []store.Unit
	Flows  []store.Flow
	Errors map[string]string
}

func (s *Server) leadForm(w http.ResponseWriter, r *http.Request) {
	data := leadFormData{Errors: map[string]string{}, Lead: store.LeadInput{Estagio: "lead"}}
	data.Units, _ = s.st(r).Units()
	data.Flows, _ = s.st(r).Flows()

	title := "Novo lead"
	if id := r.PathValue("id"); id != "" {
		lead, found, err := s.st(r).Lead(id)
		if err != nil {
			s.serverError(w, err)
			return
		}
		if !found {
			s.notFound(w, r)
			return
		}
		data.ID = id
		data.Lead = store.LeadInput{Nome: lead.Nome, Celular: lead.Celular, Email: lead.Email, Estagio: lead.Estagio, UnitID: lead.UnitID, StepID: lead.StepID}
		title = "Editar lead"
	} else if len(data.Flows) > 0 {
		data.Lead.StepID = data.Flows[0].Steps[0].ID
	}
	if len(data.Units) > 0 && data.Lead.UnitID == "" {
		data.Lead.UnitID = data.Units[0].ID
	}
	s.render(w, r, "lead_form", title, "leads", data)
}

func (s *Server) readLeadForm(r *http.Request) (store.LeadInput, map[string]string) {
	_ = r.ParseForm()
	in := store.LeadInput{
		Nome:    strings.TrimSpace(r.FormValue("nome")),
		Celular: strings.TrimSpace(r.FormValue("celular")),
		Email:   strings.TrimSpace(r.FormValue("email")),
		Estagio: r.FormValue("estagio"),
		UnitID:  r.FormValue("unidade"),
		StepID:  r.FormValue("etapa"),
	}
	errs := map[string]string{}
	if in.Nome == "" {
		errs["nome"] = "Informe o nome."
	}
	if n := len(store.Digits(in.Celular)); n < 10 || n > 13 {
		errs["celular"] = "Informe o celular com DDD, por exemplo (19) 99123-4567."
	}
	if in.Email != "" && !strings.Contains(in.Email, "@") {
		errs["email"] = "Esse e-mail não parece válido."
	}
	if !validStage(in.Estagio) {
		errs["estagio"] = "Escolha um estágio."
	}
	if _, ok, _ := s.st(r).Unit(in.UnitID); !ok {
		errs["unidade"] = "Escolha uma unidade."
	}
	if _, ok, _ := s.st(r).Step(in.StepID); !ok {
		errs["etapa"] = "Escolha uma etapa."
	}
	return in, errs
}

func validStage(stage string) bool {
	for _, st := range store.Stages {
		if st == stage {
			return true
		}
	}
	return false
}

func (s *Server) leadCreate(w http.ResponseWriter, r *http.Request) {
	in, errs := s.readLeadForm(r)
	if len(errs) > 0 {
		s.renderLeadFormErrors(w, r, "", in, errs, "Novo lead")
		return
	}
	id, err := s.st(r).CreateLead(in)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/leads/"+id, "success", "Lead criado.")
}

func (s *Server) leadUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, errs := s.readLeadForm(r)
	if len(errs) > 0 {
		s.renderLeadFormErrors(w, r, id, in, errs, "Editar lead")
		return
	}
	if err := s.st(r).UpdateLead(id, in); err != nil {
		s.serverError(w, err)
		return
	}
	if err := s.st(r).MoveLead(id, in.StepID); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/leads/"+id, "success", "Lead atualizado.")
}

func (s *Server) renderLeadFormErrors(w http.ResponseWriter, r *http.Request, id string, in store.LeadInput, errs map[string]string, title string) {
	data := leadFormData{Lead: in, ID: id, Errors: errs}
	data.Units, _ = s.st(r).Units()
	data.Flows, _ = s.st(r).Flows()
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.render(w, r, "lead_form", title, "leads", data)
}

type leadData struct {
	Lead       store.Lead
	Timeline   []store.Entry
	Flows      []store.Flow
	Numbers    []resgata.Number
	NumbersErr string
	Connected  bool
	Pending    bool
	Sandbox    bool
}

func (s *Server) loadLead(w http.ResponseWriter, r *http.Request) (store.Lead, bool) {
	lead, found, err := s.st(r).Lead(r.PathValue("id"))
	if err != nil {
		s.serverError(w, err)
		return lead, false
	}
	if !found {
		s.notFound(w, r)
		return lead, false
	}
	return lead, true
}

func (s *Server) lead(w http.ResponseWriter, r *http.Request) {
	lead, ok := s.loadLead(w, r)
	if !ok {
		return
	}
	data := leadData{Lead: lead, Sandbox: s.cfg.Sandbox()}
	data.Timeline, _ = s.st(r).Timeline(lead.ID)
	data.Flows, _ = s.st(r).Flows()
	data.Pending, _ = s.st(r).HasPendingMessages(lead.ID)

	if _, token, connected := s.token(r); connected {
		data.Connected = true
		ctx, cancel := s.apiContext(r)
		defer cancel()
		numbers, err := s.client.Numbers(ctx, token)
		if err != nil {
			data.NumbersErr = s.describeAPIError(err)
		}
		data.Numbers = numbers
	}
	s.render(w, r, "lead", lead.Nome, "leads", data)
}

func (s *Server) leadTimeline(w http.ResponseWriter, r *http.Request) {
	lead, ok := s.loadLead(w, r)
	if !ok {
		return
	}
	data := leadData{Lead: lead}
	data.Timeline, _ = s.st(r).Timeline(lead.ID)
	data.Pending, _ = s.st(r).HasPendingMessages(lead.ID)
	s.fragment(w, "timeline", data)
}

func (s *Server) leadMove(w http.ResponseWriter, r *http.Request) {
	lead, ok := s.loadLead(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	stepID := r.FormValue("etapa")
	step, found, err := s.st(r).Step(stepID)
	if err != nil || !found {
		s.redirect(w, r, "/funil", "error", "Etapa não encontrada.")
		return
	}
	if err := s.st(r).MoveLead(lead.ID, stepID); err != nil {
		s.serverError(w, err)
		return
	}
	if _, err := s.st(r).AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindStep, Body: "Movido para " + step.Nome + "."}); err != nil {
		s.serverError(w, err)
		return
	}
	back := r.FormValue("voltar")
	if back != "board" {
		back = "/leads/" + lead.ID
	} else {
		back = "/funil?fluxo=" + step.FlowID
	}
	s.redirect(w, r, back, "success", lead.Nome+" agora está em "+step.Nome+".")
}

func (s *Server) leadNote(w http.ResponseWriter, r *http.Request) {
	lead, ok := s.loadLead(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	text := strings.TrimSpace(r.FormValue("texto"))
	if text == "" {
		s.redirect(w, r, "/leads/"+lead.ID, "error", "Escreva a nota antes de salvar.")
		return
	}
	if _, err := s.st(r).AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindNote, Body: text}); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/leads/"+lead.ID, "success", "Nota salva.")
}

func (s *Server) leadMessage(w http.ResponseWriter, r *http.Request) {
	lead, ok := s.loadLead(w, r)
	if !ok {
		return
	}
	back := "/leads/" + lead.ID
	_ = r.ParseForm()
	text := strings.TrimSpace(r.FormValue("texto"))

	switch {
	case lead.OptOut:
		s.redirect(w, r, back, "error", "Este lead pediu para não receber mensagens. O envio foi bloqueado.")
		return
	case text == "" || len(text) > 1000:
		s.redirect(w, r, back, "error", "Escreva a mensagem (até 1000 caracteres).")
		return
	}
	_, token, connected := s.token(r)
	if !connected {
		s.redirect(w, r, back, "error", "Conecte o CRM ao Resgata Fácil antes de enviar mensagens.")
		return
	}

	numberID := 0
	if raw := r.FormValue("numero"); raw != "" {
		for _, c := range raw {
			if c < '0' || c > '9' {
				s.redirect(w, r, back, "error", "Número inválido.")
				return
			}
			numberID = numberID*10 + int(c-'0')
		}
	}

	ctx, cancel := s.apiContext(r)
	defer cancel()
	message, err := s.client.SendMessage(ctx, token, store.Digits(lead.Celular), text, numberID)
	if err != nil {
		s.redirect(w, r, back, "error", s.describeAPIError(err))
		return
	}
	status := message.Status
	if status == "" {
		status = "queued"
	}
	if _, err := s.st(r).AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindMessageOut, Body: text, Status: status, ExternalID: message.ID}); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, back, "success", "Mensagem enviada ao Resgata Fácil. Os status chegam por webhook.")
}
