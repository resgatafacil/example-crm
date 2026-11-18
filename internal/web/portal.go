package web

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/portal"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/workspace"
)

const (
	sessionCookie     = "funil_session"
	localExternalRef  = "funil-demo"
	storeContextLabel = "workspace"
)

type ctxKey string

type workspaceSession struct {
	Info  workspace.Info
	Store *store.Store
}

type portalView struct {
	Name     string
	First    string
	Initial  string
	TTLHours int
}

type gateKind string

const (
	gateSignedOut gateKind = "signed_out"
	gateExpired   gateKind = "expired"
	gateInvalid   gateKind = "invalid"
	gateFull      gateKind = "full"
)

type gateView struct {
	Kind      gateKind
	Title     string
	Message   string
	PortalURL string
	TTLHours  int
}

func (s *Server) st(r *http.Request) *store.Store {
	if sess, ok := r.Context().Value(ctxKey(storeContextLabel)).(workspaceSession); ok {
		return sess.Store
	}
	return s.local
}

func (s *Server) externalRef(r *http.Request) string {
	if sess, ok := r.Context().Value(ctxKey(storeContextLabel)).(workspaceSession); ok {
		return sess.Info.ID
	}
	return localExternalRef
}

func (s *Server) portalViewFor(r *http.Request) *portalView {
	sess, ok := r.Context().Value(ctxKey(storeContextLabel)).(workspaceSession)
	if !ok {
		return nil
	}
	claims := portal.Claims{Name: sess.Info.Name}
	first := claims.FirstName()
	return &portalView{Name: claims.DisplayName(), First: first, Initial: string([]rune(first)[:1]), TTLHours: int(s.cfg.WorkspaceTTL.Hours())}
}

func (s *Server) StoreForKey(secret string) (*store.Store, bool) {
	if !s.cfg.PortalMode() {
		return s.local, true
	}
	rest, found := strings.CutPrefix(secret, store.KeyPrefix)
	if !found || len(rest) <= portal.WorkspaceIDLen || rest[portal.WorkspaceIDLen] != '_' {
		return nil, false
	}
	return s.workspaceStore(rest[:portal.WorkspaceIDLen])
}

func (s *Server) workspaceStore(id string) (*store.Store, bool) {
	if s.ws == nil || !portal.ValidWorkspaceID(id) {
		return nil, false
	}
	st, _, found, err := s.ws.Get(id)
	if err != nil {
		log.Printf("opening workspace %s: %v", id, err)
		return nil, false
	}
	if found {
		_ = s.ws.Touch(id)
	}
	return st, found
}

func (s *Server) storeForEvent(event resgata.Event) (*store.Store, bool) {
	if !s.cfg.PortalMode() {
		return s.local, true
	}
	return s.workspaceStore(event.Connection.ExternalRef)
}

func (s *Server) portalGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.sessionFrom(r)
		if !ok {
			s.renderGate(w, r, gateSignedOut)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey(storeContextLabel), sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) sessionFrom(r *http.Request) (workspaceSession, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return workspaceSession{}, false
	}
	id, ok := s.sessions.Parse(cookie.Value)
	if !ok {
		return workspaceSession{}, false
	}
	st, info, found, err := s.ws.Get(id)
	if err != nil {
		log.Printf("loading workspace %s: %v", id, err)
		return workspaceSession{}, false
	}
	if !found {
		return workspaceSession{}, false
	}
	_ = s.ws.Touch(id)
	return workspaceSession{Info: info, Store: st}, true
}

func (s *Server) setSession(w http.ResponseWriter, id string) {
	value, expires := s.sessions.Issue(id)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", Expires: expires, HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) portalLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims, err := portal.Verify(s.cfg.PortalSecret, r.URL.Query().Get("token"), s.now())
	switch {
	case errors.Is(err, portal.ErrExpired):
		s.renderGate(w, r, gateExpired)
		return
	case err != nil:
		log.Printf("portal login refused: %v", err)
		s.renderGate(w, r, gateInvalid)
		return
	}
	if !s.replay.Use(claims.ID, claims.Expiry(), s.now()) {
		s.renderGate(w, r, gateExpired)
		return
	}

	id := portal.WorkspaceID(claims.Subject)
	if _, _, err := s.ws.Ensure(id, claims.DisplayName()); err != nil {
		if errors.Is(err, workspace.ErrFull) {
			s.renderGate(w, r, gateFull)
			return
		}
		s.serverError(w, err)
		return
	}
	s.setSession(w, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) portalLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	http.Redirect(w, r, s.cfg.PortalURL, http.StatusSeeOther)
}

func (s *Server) workspaceReset(w http.ResponseWriter, r *http.Request) {
	sess := r.Context().Value(ctxKey(storeContextLabel)).(workspaceSession)
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	s.disconnectWorkspace(ctx, sess.Store)
	if _, err := s.ws.Reset(sess.Info.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.redirect(w, r, "/", "success", "Sua área de trabalho foi reiniciada com os dados de exemplo.")
}

func (s *Server) disconnectWorkspace(ctx context.Context, st *store.Store) {
	conn, found, err := st.Connection()
	if err != nil || !found || conn.State != store.StateConnected || conn.TokenEncrypted == "" {
		return
	}
	token, err := s.box.Open(conn.TokenEncrypted)
	if err != nil {
		log.Printf("cannot decrypt a workspace token while cleaning up: %v", err)
		return
	}
	if err := s.client.Disconnect(ctx, token); err != nil && !resgata.IsStatus(err, http.StatusUnauthorized) {
		log.Printf("disconnecting a workspace from Resgata: %v", err)
	}
}

func (s *Server) PurgeIdle(ctx context.Context) (int, error) {
	idle, err := s.ws.Idle(s.now().Add(-s.cfg.WorkspaceTTL))
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, info := range idle {
		if ctx.Err() != nil {
			return purged, ctx.Err()
		}
		if st, _, found, err := s.ws.Get(info.ID); err == nil && found {
			call, cancel := context.WithTimeout(ctx, apiTimeout)
			s.disconnectWorkspace(call, st)
			cancel()
		}
		if err := s.ws.Delete(info.ID); err != nil {
			log.Printf("purging workspace %s: %v", info.ID, err)
			continue
		}
		purged++
	}
	return purged, nil
}

func (s *Server) RunPurger(ctx context.Context, every time.Duration) {
	if s.ws == nil {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purged, err := s.PurgeIdle(ctx)
			if err != nil {
				log.Printf("purging idle workspaces: %v", err)
			}
			if purged > 0 {
				log.Printf("purged %d idle workspace(s)", purged)
			}
		}
	}
}

func (s *Server) renderGate(w http.ResponseWriter, r *http.Request, kind gateKind) {
	view := gateView{Kind: kind, PortalURL: s.cfg.PortalURL, TTLHours: int(s.cfg.WorkspaceTTL.Hours())}
	status := http.StatusUnauthorized
	switch kind {
	case gateExpired:
		view.Title = "Esse link de acesso não vale mais"
		view.Message = "Cada link do portal funciona uma vez e por poucos segundos. Volte ao portal e clique em Abrir exemplo para entrar de novo."
	case gateInvalid:
		view.Title = "Não foi possível validar o acesso"
		view.Message = "O link está incompleto ou não foi gerado pelo portal de desenvolvedores. Volte ao portal e abra o exemplo por lá."
	case gateFull:
		status = http.StatusServiceUnavailable
		view.Title = "A demonstração está cheia agora"
		view.Message = "Todas as áreas de trabalho estão em uso. Elas são liberadas depois de algumas horas sem atividade; tente de novo mais tarde."
	default:
		view.Title = "Entre pelo portal de desenvolvedores"
		view.Message = "Esta demonstração é aberta só para quem tem acesso ao portal de desenvolvedores do Resgata Fácil. Entre lá e use o link Exemplo: você ganha uma área de trabalho só sua."
	}

	var buf bytes.Buffer
	if err := s.pages["gate"].ExecuteTemplate(&buf, "gate", view); err != nil {
		log.Printf("render gate: %v", err)
		http.Error(w, "Acesso restrito.", status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
