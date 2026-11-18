package web

import (
	"net/http"
	"strings"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

type connectionData struct {
	Conn        store.Connection
	Found       bool
	State       string
	Live        resgata.Connection
	Setup       resgata.Setup
	HasSetup    bool
	Numbers     []resgata.Number
	Error       string
	LastEvent   store.Event
	HasEvent    bool
	WebhookURL  string
	RedirectURI string
	Scopes      []string
	Sandbox     bool
	HasAppKey   bool
}

func (s *Server) connection(w http.ResponseWriter, r *http.Request) {
	conn, found, err := s.store.Connection()
	if err != nil {
		s.serverError(w, err)
		return
	}
	data := connectionData{
		Conn: conn, Found: found, State: "none", WebhookURL: s.cfg.WebhookURL(), RedirectURI: s.cfg.RedirectURI(),
		Scopes: resgata.DefaultScopes, Sandbox: s.cfg.Sandbox(), HasAppKey: s.cfg.AppKey != "",
	}
	if found {
		data.State = conn.State
	}
	data.LastEvent, data.HasEvent, _ = s.store.LastValidEvent()

	if _, token, connected := s.token(); connected {
		s.loadLive(r, token, &data)
	}
	s.render(w, r, "connection", "Integração com o Resgata Fácil", "connection", data)
}

func (s *Server) loadLive(r *http.Request, token string, data *connectionData) {
	ctx, cancel := s.apiContext(r)
	defer cancel()

	live, err := s.client.Connection(ctx, token)
	if resgata.IsStatus(err, http.StatusUnauthorized) {
		_ = s.store.RevokeConnection()
		data.State = store.StateRevoked
		return
	}
	if err != nil {
		data.Error = s.describeAPIError(err)
		return
	}
	data.Live = live
	if live.Status == "revoked" {
		_ = s.store.RevokeConnection()
		data.State = store.StateRevoked
		return
	}

	if setup, err := s.client.Setup(ctx, token); err == nil {
		data.Setup = setup
		data.HasSetup = true
	} else {
		data.Error = s.describeAPIError(err)
	}
	if numbers, err := s.client.Numbers(ctx, token); err == nil {
		data.Numbers = numbers
	} else if data.Error == "" {
		data.Error = s.describeAPIError(err)
	}
}

type eventRow struct {
	Event   store.Event
	Payload string
}

type eventsData struct {
	Rows     []eventRow
	Types    []string
	Selected string
	Total    int
}

func (s *Server) loadEvents(r *http.Request) (eventsData, error) {
	selected := strings.TrimSpace(r.URL.Query().Get("tipo"))
	rows, err := s.store.Events(selected, 100)
	if err != nil {
		return eventsData{}, err
	}
	types, _ := s.store.EventTypes()
	data := eventsData{Types: types, Selected: selected, Total: len(rows)}
	for _, e := range rows {
		data.Rows = append(data.Rows, eventRow{Event: e, Payload: prettyJSON(e.Payload)})
	}
	return data, nil
}

func (s *Server) eventsPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.loadEvents(r)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.render(w, r, "events", "Eventos recebidos", "events", data)
}

func (s *Server) eventsFragment(w http.ResponseWriter, r *http.Request) {
	data, err := s.loadEvents(r)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.fragmentOf(w, "events", "eventsList", data)
}

func (s *Server) fragmentOf(w http.ResponseWriter, page, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.pages[page].ExecuteTemplate(w, name, data); err != nil {
		s.serverError(w, err)
	}
}
