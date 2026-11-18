package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/config"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/crmapi"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/crypto"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/portal"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/workspace"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static
var staticFiles embed.FS

const (
	flashCookie  = "flash"
	apiTimeout   = 15 * time.Second
	maxBodyBytes = 1 << 20
)

type Backend struct {
	Local      *store.Store
	Workspaces *workspace.Manager
}

type Server struct {
	cfg      config.Config
	local    *store.Store
	ws       *workspace.Manager
	sessions *portal.Sessions
	replay   *portal.ReplayGuard
	box      *crypto.Box
	client   *resgata.Client
	pages    map[string]*template.Template
	loc      *time.Location
	now      func() time.Time
	limiter  *crmapi.Limiter
}

func New(cfg config.Config, backend Backend, box *crypto.Box, client *resgata.Client) (*Server, error) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.UTC
	}
	s := &Server{cfg: cfg, local: backend.Local, ws: backend.Workspaces, box: box, client: client, loc: loc, now: time.Now}
	if cfg.PortalMode() {
		if backend.Workspaces == nil {
			return nil, errors.New("portal mode needs a workspace manager")
		}
		s.sessions, err = portal.NewSessions(cfg.EncryptionKey, cfg.SessionTTL)
		if err != nil {
			return nil, err
		}
		s.replay = portal.NewReplayGuard()
	} else if backend.Local == nil {
		return nil, errors.New("local mode needs a store")
	}
	s.limiter = crmapi.NewLimiter(cfg.RateLimit, time.Minute)
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) parseTemplates() error {
	base, err := template.New("base").Funcs(s.funcs()).ParseFS(templateFiles, "templates/layout.html", "templates/partials.html")
	if err != nil {
		return err
	}
	pages := []string{"dashboard", "board", "leads", "lead_form", "lead", "connection", "events", "sandbox", "keys", "notfound"}
	s.pages = map[string]*template.Template{}
	for _, name := range pages {
		clone, err := base.Clone()
		if err != nil {
			return err
		}
		if _, err := clone.ParseFS(templateFiles, "templates/"+name+".html"); err != nil {
			return err
		}
		s.pages[name] = clone
	}
	setup, err := template.New("setup").Funcs(s.funcs()).ParseFS(templateFiles, "templates/setup.html")
	if err != nil {
		return err
	}
	s.pages["setup"] = setup
	gate, err := template.New("gate").Funcs(s.funcs()).ParseFS(templateFiles, "templates/gate.html")
	if err != nil {
		return err
	}
	s.pages["gate"] = gate
	return nil
}

func (s *Server) Handler() http.Handler {
	public := http.NewServeMux()
	public.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	public.Handle("GET /static/", s.staticHandler())
	public.HandleFunc("POST /webhooks/resgata", s.webhook)
	crmapi.New(s, s.limiter).Register(public)
	if s.cfg.PortalMode() {
		public.HandleFunc("GET /portal-login", s.portalLogin)
		public.Handle("POST /sair", sameOrigin(http.HandlerFunc(s.portalLogout)))
	}

	admin := http.NewServeMux()
	s.registerAdmin(admin)
	public.Handle("/", s.adminAuth(sameOrigin(admin)))

	return securityHeaders(public)
}

func (s *Server) registerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /funil", s.board)
	mux.HandleFunc("GET /leads", s.leads)
	mux.HandleFunc("GET /leads/novo", s.leadForm)
	mux.HandleFunc("POST /leads", s.leadCreate)
	mux.HandleFunc("GET /leads/{id}", s.lead)
	mux.HandleFunc("GET /leads/{id}/editar", s.leadForm)
	mux.HandleFunc("POST /leads/{id}", s.leadUpdate)
	mux.HandleFunc("POST /leads/{id}/mover", s.leadMove)
	mux.HandleFunc("POST /leads/{id}/nota", s.leadNote)
	mux.HandleFunc("POST /leads/{id}/mensagem", s.leadMessage)
	mux.HandleFunc("GET /leads/{id}/linha-do-tempo", s.leadTimeline)

	mux.HandleFunc("GET /integracao", s.connection)
	mux.HandleFunc("GET /resgata/conectar", s.oauthStart)
	mux.HandleFunc("GET /resgata/callback", s.oauthCallback)
	mux.HandleFunc("POST /resgata/atalho", s.connectWithAppKey)
	mux.HandleFunc("POST /resgata/desconectar", s.disconnect)

	mux.HandleFunc("GET /eventos", s.eventsPage)
	mux.HandleFunc("GET /eventos/lista", s.eventsFragment)

	mux.HandleFunc("GET /sandbox", s.sandbox)
	mux.HandleFunc("POST /sandbox/evento", s.sandboxEvent)
	mux.HandleFunc("POST /sandbox/resposta", s.sandboxInbound)
	mux.HandleFunc("POST /sandbox/optout", s.sandboxOptOut)
	mux.HandleFunc("POST /sandbox/numero", s.sandboxCreateNumber)
	mux.HandleFunc("POST /sandbox/numero/{id}", s.sandboxNumberStatus)
	mux.HandleFunc("POST /sandbox/limpar", s.sandboxReset)

	mux.HandleFunc("GET /chaves", s.keys)
	mux.HandleFunc("POST /chaves", s.keyCreate)
	mux.HandleFunc("POST /chaves/{id}/revogar", s.keyRevoke)

	if s.cfg.PortalMode() {
		mux.HandleFunc("POST /reiniciar", s.workspaceReset)
	}

	mux.HandleFunc("/", s.notFound)
}

func SetupHandler(problems []config.Problem, suggestedKey string) http.Handler {
	pages := template.Must(template.New("setup").ParseFS(templateFiles, "templates/setup.html"))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", staticOnly())
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		var buf bytes.Buffer
		if err := pages.ExecuteTemplate(&buf, "setup", map[string]any{"Problems": problems, "SuggestedKey": suggestedKey}); err != nil {
			log.Printf("setup page: %v", err)
			return
		}
		_, _ = buf.WriteTo(w)
	})
	return securityHeaders(mux)
}

func staticOnly() http.Handler {
	sub, _ := fs.Sub(staticFiles, "static")
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}

func (s *Server) staticHandler() http.Handler {
	inner := staticOnly()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		inner.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' https://fonts.googleapis.com; style-src-attr 'unsafe-inline'; font-src https://fonts.gstatic.com; img-src 'self' data:; script-src 'self'; frame-ancestors 'none'; form-action 'self' https:")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminAuth(next http.Handler) http.Handler {
	if s.cfg.PortalMode() {
		return s.portalGate(next)
	}
	if s.cfg.AdminPassword == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.AdminPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Funil Demo", charset="UTF-8"`)
			http.Error(w, "Autenticação necessária.", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "Origem não permitida.", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || parsed.Host != r.Host {
					http.Error(w, "Origem não permitida.", http.StatusForbidden)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

type flash struct {
	Kind    string
	Message string
}

func (s *Server) setFlash(w http.ResponseWriter, kind, message string) {
	value := base64.RawURLEncoding.EncodeToString([]byte(kind + "|" + message))
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30})
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) *flash {
	cookie, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1})
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil
	}
	kind, message, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil
	}
	return &flash{Kind: kind, Message: message}
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to, kind, message string) {
	if message != "" {
		s.setFlash(w, kind, message)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

type view struct {
	Title   string
	Active  string
	Flash   *flash
	Config  config.Config
	Resgata resgataState
	Portal  *portalView
	Data    any
}

type resgataState struct {
	Connected bool
	Revoked   bool
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name, title, active string, data any) {
	conn, _, _ := s.st(r).Connection()
	v := view{
		Title: title, Active: active, Flash: s.takeFlash(w, r), Config: s.cfg, Data: data, Portal: s.portalViewFor(r),
		Resgata: resgataState{Connected: conn.State == store.StateConnected, Revoked: conn.State == store.StateRevoked},
	}
	var buf bytes.Buffer
	if err := s.pages[name].ExecuteTemplate(&buf, "layout", v); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "Erro ao montar a página.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}

func (s *Server) fragment(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.pages["lead"].ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("fragment %s: %v", name, err)
		http.Error(w, "Erro ao montar o trecho.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "Algo deu errado. Tente de novo.", http.StatusInternalServerError)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "notfound", "Página não encontrada", "", nil)
}

type token struct {
	Value string
	OK    bool
}

func (s *Server) token(r *http.Request) (store.Connection, string, bool) {
	conn, found, err := s.st(r).Connection()
	if err != nil || !found || conn.State != store.StateConnected || conn.TokenEncrypted == "" {
		return conn, "", false
	}
	plain, err := s.box.Open(conn.TokenEncrypted)
	if err != nil {
		log.Printf("cannot decrypt the stored token: %v", err)
		return conn, "", false
	}
	return conn, plain, true
}

func (s *Server) apiContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), apiTimeout)
}

func (s *Server) describeAPIError(err error) string {
	var apiErr *resgata.APIError
	if asAPIError(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return "O Resgata Fácil recusou o token desta conexão. Reconecte para gerar um novo."
		case http.StatusNotImplemented:
			return "O envio de mensagens em produção ainda não está disponível pela API do Resgata Fácil. Use o sandbox para ver o fluxo completo."
		case http.StatusTooManyRequests:
			return "O Resgata Fácil limitou as requisições. Espere um minuto e tente de novo."
		}
		if apiErr.Message != "" {
			return fmt.Sprintf("O Resgata Fácil respondeu: %s", apiErr.Message)
		}
	}
	return "Não foi possível falar com o Resgata Fácil agora. Tente de novo em instantes."
}
