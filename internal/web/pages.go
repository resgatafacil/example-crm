package web

import (
	"net/http"
	"strings"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

type checklistItem struct {
	Title string
	Hint  string
	Done  bool
	Href  string
	Label string
}

type dashboardData struct {
	Stats      store.Stats
	Checklist  []checklistItem
	Done       int
	Recent     []store.Event
	WebhookURL string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := s.st(r).Stats()
	if err != nil {
		s.serverError(w, err)
		return
	}
	conn, _, _ := s.st(r).Connection()
	validEvents, _ := s.st(r).CountValidEvents()
	sent, _ := s.st(r).HasCampaign()
	recent, _ := s.st(r).Events("", 6)

	checklist := []checklistItem{
		{Title: "Criar o aplicativo no portal de desenvolvedores", Hint: "Cadastre o redirect URI " + s.cfg.RedirectURI() + " e copie o client_id.", Done: s.cfg.ClientID != ""},
		{Title: "Configurar as variáveis de ambiente", Hint: "Chaves e segredos ficam só no .env, nunca no código. Validadas ao iniciar.", Done: true},
		{Title: "Registrar o endpoint de webhook", Hint: "Cadastre " + s.cfg.WebhookURL() + " no portal e envie um evento de teste.", Done: validEvents > 0, Href: "/eventos", Label: "Ver eventos"},
		{Title: "Conectar ao Resgata Fácil", Hint: "Autorização OAuth com PKCE: o cliente aprova e o CRM recebe um token.", Done: conn.State == store.StateConnected, Href: "/integracao", Label: "Abrir integração"},
		{Title: "Enviar uma campanha de teste", Hint: "Escolha um modelo aprovado e os leads de uma etapa do funil; acompanhe os status chegando por webhook.", Done: sent, Href: "/campanhas/nova", Label: "Criar campanha"},
	}
	done := 0
	for _, item := range checklist {
		if item.Done {
			done++
		}
	}
	s.render(w, r, "dashboard", "Início", "dashboard", dashboardData{Stats: stats, Checklist: checklist, Done: done, Recent: recent, WebhookURL: s.cfg.WebhookURL()})
}

type boardColumn struct {
	Step  store.Step
	Leads []store.Lead
	Prev  string
	Next  string
}

type boardData struct {
	Flows   []store.Flow
	Current store.Flow
	Columns []boardColumn
	Total   int
}

func (s *Server) board(w http.ResponseWriter, r *http.Request) {
	flows, err := s.st(r).Flows()
	if err != nil || len(flows) == 0 {
		s.serverError(w, err)
		return
	}
	current := flows[0]
	for _, f := range flows {
		if f.ID == r.URL.Query().Get("fluxo") {
			current = f
		}
	}
	page, err := s.st(r).LeadsPage(store.LeadFilter{FlowID: current.ID, Limit: 500})
	if err != nil {
		s.serverError(w, err)
		return
	}
	columns := make([]boardColumn, len(current.Steps))
	for i, st := range current.Steps {
		columns[i].Step = st
		if i > 0 {
			columns[i].Prev = current.Steps[i-1].ID
		}
		if i < len(current.Steps)-1 {
			columns[i].Next = current.Steps[i+1].ID
		}
		for _, l := range page.Leads {
			if l.StepID == st.ID {
				columns[i].Leads = append(columns[i].Leads, l)
			}
		}
	}
	s.render(w, r, "board", "Funil", "board", boardData{Flows: flows, Current: current, Columns: columns, Total: len(page.Leads)})
}

type leadsData struct {
	Leads    []store.Lead
	Units    []store.Unit
	Query    string
	Unit     string
	Stage    string
	Filtered bool
}

func (s *Server) leads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.LeadFilter{Search: strings.TrimSpace(q.Get("q")), UnitID: q.Get("unidade"), Stage: q.Get("estagio"), Limit: 200}
	page, err := s.st(r).LeadsPage(filter)
	if err != nil {
		s.serverError(w, err)
		return
	}
	units, _ := s.st(r).Units()
	s.render(w, r, "leads", "Leads", "leads", leadsData{
		Leads: page.Leads, Units: units, Query: filter.Search, Unit: filter.UnitID, Stage: filter.Stage,
		Filtered: filter.Search != "" || filter.UnitID != "" || filter.Stage != "",
	})
}
