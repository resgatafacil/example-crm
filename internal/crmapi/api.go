package crmapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

type Resolver interface {
	StoreForKey(secret string) (*store.Store, bool)
}

type fixed struct{ st *store.Store }

func (f fixed) StoreForKey(string) (*store.Store, bool) {
	return f.st, true
}

func Fixed(st *store.Store) Resolver {
	return fixed{st: st}
}

type API struct {
	resolver Resolver
	limiter  *Limiter
}

func New(resolver Resolver, limiter *Limiter) *API {
	return &API{resolver: resolver, limiter: limiter}
}

func (a *API) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/unidades", a.authenticated(a.units))
	mux.Handle("GET /api/v1/fluxos", a.authenticated(a.flows))
	mux.Handle("GET /api/v1/leads", a.authenticated(a.leads))
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "Recurso não encontrado.", nil)
	})
}

type field struct {
	Campo  string `json:"campo"`
	Motivo string `json:"motivo"`
}

type authedHandler func(w http.ResponseWriter, r *http.Request, st *store.Store, unit store.Unit)

func (a *API) authenticated(next authedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearer(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "Chave ausente ou inválida.", nil)
			return
		}
		st, resolved := a.resolver.StoreForKey(secret)
		if !resolved {
			writeError(w, http.StatusUnauthorized, "Chave ausente ou inválida.", nil)
			return
		}
		key, found, err := st.AuthenticateKey(secret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Erro interno.", nil)
			return
		}
		if !found {
			writeError(w, http.StatusUnauthorized, "Chave ausente ou inválida.", nil)
			return
		}

		decision := a.limiter.Allow(key.ID)
		w.Header().Set("RateLimit-Limit", strconv.Itoa(decision.Limit))
		w.Header().Set("RateLimit-Remaining", strconv.Itoa(decision.Remaining))
		w.Header().Set("RateLimit-Reset", strconv.Itoa(ceilSeconds(decision.ResetAfter)))
		if !decision.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(ceilSeconds(decision.ResetAfter)))
			writeError(w, http.StatusTooManyRequests, "Limite de requisições atingido.", nil)
			return
		}

		unit, found, err := st.Unit(key.UnitID)
		if err != nil || !found {
			writeError(w, http.StatusInternalServerError, "Erro interno.", nil)
			return
		}
		if !unit.Ativa {
			writeError(w, http.StatusForbidden, "A unidade desta chave está com o acesso suspenso.", nil)
			return
		}
		next(w, r, st, unit)
	})
}

func bearer(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	secret, found := strings.CutPrefix(header, "Bearer ")
	secret = strings.TrimSpace(secret)
	return secret, found && strings.HasPrefix(secret, store.KeyPrefix)
}

func ceilSeconds(d time.Duration) int {
	seconds := int((d + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (a *API) units(w http.ResponseWriter, _ *http.Request, _ *store.Store, unit store.Unit) {
	writeJSON(w, http.StatusOK, map[string]any{"dados": []any{unitJSON(unit)}})
}

func (a *API) flows(w http.ResponseWriter, _ *http.Request, st *store.Store, _ store.Unit) {
	flows, err := st.Flows()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erro interno.", nil)
		return
	}
	out := make([]any, 0, len(flows))
	for _, f := range flows {
		steps := make([]any, 0, len(f.Steps))
		for _, step := range f.Steps {
			steps = append(steps, map[string]any{"id": step.ID, "nome": step.Nome, "ordem": step.Ordem, "inicial": step.Inicial})
		}
		out = append(out, map[string]any{"id": f.ID, "nome": f.Nome, "ativo": f.Ativo, "etapas": steps})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dados": out})
}

const (
	defaultLimit = 50
	maxLimit     = 500
)

func (a *API) leads(w http.ResponseWriter, r *http.Request, st *store.Store, unit store.Unit) {
	query := r.URL.Query()
	filter := store.LeadFilter{
		UnitID: unit.ID,
		FlowID: query.Get("fluxo_id"),
		StepID: query.Get("etapa_id"),
		Stage:  query.Get("estagio"),
		Cursor: query.Get("cursor"),
		Limit:  defaultLimit,
	}

	var invalid []field
	if requested := query.Get("organizacao_id"); requested != "" && requested != unit.ID {
		writeError(w, http.StatusForbidden, "Esta chave não tem acesso à unidade pedida.", nil)
		return
	}
	if filter.Stage != "" && !validStage(filter.Stage) {
		invalid = append(invalid, field{"estagio", "deve ser lead, avaliado ou cliente"})
	}
	if raw := query.Get("limite"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxLimit {
			invalid = append(invalid, field{"limite", "deve ser um inteiro entre 1 e " + strconv.Itoa(maxLimit)})
		} else {
			filter.Limit = n
		}
	}
	if len(invalid) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "Parâmetros inválidos.", invalid)
		return
	}

	page, err := st.LeadsPage(filter)
	if err == store.ErrBadCursor {
		writeError(w, http.StatusUnprocessableEntity, "Parâmetros inválidos.", []field{{"cursor", "cursor inválido ou expirado"}})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erro interno.", nil)
		return
	}

	data := make([]any, 0, len(page.Leads))
	for _, l := range page.Leads {
		data = append(data, leadJSON(l))
	}
	var next any
	if page.HasMore {
		next = page.NextCursor
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dados": data,
		"paginacao": map[string]any{
			"quantidade":     len(data),
			"tem_mais":       page.HasMore,
			"proximo_cursor": next,
		},
	})
}

func validStage(stage string) bool {
	for _, s := range store.Stages {
		if s == stage {
			return true
		}
	}
	return false
}

func unitJSON(u store.Unit) map[string]any {
	return map[string]any{"id": u.ID, "nome": u.Nome, "cidade": u.Cidade, "estado": u.Estado, "ativa": u.Ativa}
}

func leadJSON(l store.Lead) map[string]any {
	var email any
	if l.Email != "" {
		email = l.Email
	}
	return map[string]any{
		"id":          l.ID,
		"nome":        l.Nome,
		"celular":     l.Celular,
		"email":       email,
		"estagio":     l.Estagio,
		"opt_out":     l.OptOut,
		"unidade":     map[string]any{"id": l.UnitID, "nome": l.UnitName},
		"fluxo":       map[string]any{"id": l.FlowID, "nome": l.FlowName},
		"etapa":       map[string]any{"id": l.StepID, "nome": l.StepName},
		"criado_em":   l.CriadoEm.Format(time.RFC3339),
		"alterado_em": l.AlteradoEm.Format(time.RFC3339),
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string, fields []field) {
	body := map[string]any{"mensagem": message}
	if fields != nil {
		body["campos"] = fields
	}
	writeJSON(w, status, map[string]any{"erro": body})
}
