package web

import (
	"net/http"
	"strings"

	"resgata-example-crm/internal/store"
)

type keysData struct {
	Keys      []store.APIKey
	Units     []store.Unit
	NewKey    string
	NewName   string
	BaseURL   string
	Example   string
	RateLimit int
}

func (s *Server) keysData() (keysData, error) {
	keys, err := s.store.APIKeys()
	if err != nil {
		return keysData{}, err
	}
	units, err := s.store.Units()
	if err != nil {
		return keysData{}, err
	}
	return keysData{Keys: keys, Units: units, BaseURL: s.cfg.PublicURL + "/api/v1", RateLimit: s.cfg.RateLimit}, nil
}

func (s *Server) keys(w http.ResponseWriter, r *http.Request) {
	data, err := s.keysData()
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.render(w, r, "keys", "Chaves da API", "keys", data)
}

func (s *Server) keyCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	unitID := r.FormValue("unidade")
	name := strings.TrimSpace(r.FormValue("nome"))
	if name == "" {
		name = "Integração Resgata Fácil"
	}
	if _, ok, _ := s.store.Unit(unitID); !ok {
		s.redirect(w, r, "/chaves", "error", "Escolha uma unidade para a chave.")
		return
	}
	secret, err := s.store.CreateAPIKey(unitID, name)
	if err != nil {
		s.serverError(w, err)
		return
	}
	data, err := s.keysData()
	if err != nil {
		s.serverError(w, err)
		return
	}
	data.NewKey = secret
	data.NewName = name
	s.render(w, r, "keys", "Chaves da API", "keys", data)
}

func (s *Server) keyRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAPIKey(r.PathValue("id")); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/chaves", "success", "Chave revogada. Quem a usava passa a receber 401.")
}
