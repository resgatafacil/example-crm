package web

import (
	"net/http"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

const oauthStateMaxAge = 10 * time.Minute

func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	state := resgata.NewState()
	verifier := resgata.NewVerifier()
	if err := s.st(r).SaveOAuthState(state, verifier); err != nil {
		s.serverError(w, err)
		return
	}
	target := resgata.AuthorizeURL(resgata.AuthorizeEndpoint(s.cfg.AppURL, s.cfg.APIURL, s.cfg.Sandbox()), resgata.AuthorizeParams{
		ClientID:    s.cfg.ClientID,
		RedirectURI: s.cfg.RedirectURI(),
		State:       state,
		Verifier:    verifier,
		ExternalRef: s.externalRef(r),
		Scopes:      resgata.DefaultScopes,
	})
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	verifier, ok, err := s.st(r).ConsumeOAuthState(query.Get("state"), oauthStateMaxAge)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !ok {
		s.redirect(w, r, "/integracao", "error", "O pedido de conexão expirou ou não é válido. Comece de novo pelo botão Conectar.")
		return
	}
	if code := query.Get("error"); code != "" {
		s.redirect(w, r, "/integracao", "error", resgata.OAuthErrorMessage(code))
		return
	}
	code := query.Get("code")
	if code == "" {
		s.redirect(w, r, "/integracao", "error", "O Resgata Fácil não devolveu o código de autorização.")
		return
	}

	ctx, cancel := s.apiContext(r)
	defer cancel()
	result, err := s.client.ExchangeCode(ctx, s.cfg.ClientID, code, verifier, s.cfg.RedirectURI())
	if err != nil {
		s.redirect(w, r, "/integracao", "error", s.describeAPIError(err))
		return
	}
	if err := s.saveToken(r, result); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/integracao", "success", "Conectado ao Resgata Fácil. Agora complete os passos abaixo.")
}

func (s *Server) saveToken(r *http.Request, result resgata.Token) error {
	sealed, err := s.box.Seal(result.AccessToken)
	if err != nil {
		return err
	}
	return s.st(r).SaveConnection(store.Connection{
		ConnectionID:   result.ConnectionID,
		TokenEncrypted: sealed,
		Scope:          result.Scope,
		ExternalRef:    result.ExternalRef,
		State:          store.StateConnected,
	})
}

func (s *Server) connectWithAppKey(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Sandbox() || s.cfg.AppKey == "" {
		s.redirect(w, r, "/integracao", "error", "O atalho com a chave do aplicativo só existe no sandbox e precisa de RESGATA_APP_KEY.")
		return
	}
	ctx, cancel := s.apiContext(r)
	defer cancel()

	conn, err := s.client.CreateSandboxConnection(ctx, s.cfg.AppKey, s.externalRef(r))
	if resgata.IsType(err, "external_ref_in_use") {
		s.redirect(w, r, "/integracao", "error", "Já existe uma conexão de sandbox para esta demo. Use Conectar (OAuth) para reaproveitá-la.")
		return
	}
	if err != nil {
		s.redirect(w, r, "/integracao", "error", s.describeAPIError(err))
		return
	}
	secret, err := s.client.IssueConnectionToken(ctx, s.cfg.AppKey, conn.ID)
	if err != nil {
		s.redirect(w, r, "/integracao", "error", s.describeAPIError(err))
		return
	}
	if err := s.saveToken(r, resgata.Token{AccessToken: secret, ConnectionID: conn.ID, ExternalRef: conn.ExternalRef}); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/integracao", "success", "Conexão de sandbox criada com a chave do aplicativo.")
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	_, token, ok := s.token(r)
	if ok {
		ctx, cancel := s.apiContext(r)
		defer cancel()
		if err := s.client.Disconnect(ctx, token); err != nil && !resgata.IsStatus(err, http.StatusUnauthorized) {
			s.redirect(w, r, "/integracao", "error", s.describeAPIError(err))
			return
		}
	}
	if err := s.st(r).RevokeConnection(); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/integracao", "success", "Desconectado. O token foi apagado deste CRM e revogado no Resgata Fácil.")
}
