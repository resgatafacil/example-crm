package web

import (
	"net/http"
	"strconv"
	"strings"

	"resgata-example-crm/internal/resgata"
	"resgata-example-crm/internal/store"
)

type magicNumber struct {
	Phone   string
	Outcome string
}

var magicNumbers = []magicNumber{
	{"5500000000003", "Falha 131049: a Meta não entrega para manter a saúde do ecossistema"},
	{"5500000000004", "Falha 131056: limite de envios para o mesmo contato"},
	{"5500000000011", "Entregue, mas nunca lida"},
	{"5500000000012", "Fica na fila e nunca é enviada"},
	{"5500000000020", "Lenta: enviada em 30 s, entregue em 60 s, lida em 120 s"},
	{"5500000000030", "Lida; 7 s depois chega contact.opted_out"},
	{"5500000000031", "Lida; 8 s depois chega uma resposta automática"},
}

type sandboxData struct {
	Leads       []store.Lead
	Numbers     []resgata.Number
	Connected   bool
	Error       string
	Magic       []magicNumber
	WebhookURL  string
	RedirectURI string
}

func (s *Server) sandbox(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Sandbox() {
		s.redirect(w, r, "/integracao", "error", "As ferramentas de sandbox só aparecem quando RESGATA_ENVIRONMENT=sandbox.")
		return
	}
	page, _ := s.store.LeadsPage(store.LeadFilter{Limit: 200})
	data := sandboxData{Leads: page.Leads, Magic: magicNumbers, WebhookURL: s.cfg.WebhookURL(), RedirectURI: s.cfg.RedirectURI()}
	if _, token, connected := s.token(); connected {
		data.Connected = true
		ctx, cancel := s.apiContext(r)
		defer cancel()
		numbers, err := s.client.Numbers(ctx, token)
		if err != nil {
			data.Error = s.describeAPIError(err)
		}
		data.Numbers = numbers
	}
	s.render(w, r, "sandbox", "Ferramentas do sandbox", "sandbox", data)
}

func (s *Server) sandboxCall(w http.ResponseWriter, r *http.Request, success string, call func(token, phone string, r *http.Request) error) {
	if !s.cfg.Sandbox() {
		s.redirect(w, r, "/integracao", "error", "As ferramentas de sandbox só existem com RESGATA_ENVIRONMENT=sandbox.")
		return
	}
	_, token, connected := s.token()
	if !connected {
		s.redirect(w, r, "/integracao", "error", "Conecte o CRM ao Resgata Fácil antes de usar o sandbox.")
		return
	}
	_ = r.ParseForm()
	phone := strings.TrimSpace(r.FormValue("telefone"))
	if leadID := r.FormValue("lead"); leadID != "" {
		lead, found, err := s.store.Lead(leadID)
		if err != nil || !found {
			s.redirect(w, r, "/sandbox", "error", "Lead não encontrado.")
			return
		}
		phone = lead.Celular
	}
	if phone != "" {
		phone = store.Digits(phone)
	}
	if err := call(token, phone, r); err != nil {
		s.redirect(w, r, "/sandbox", "error", s.describeAPIError(err))
		return
	}
	s.redirect(w, r, "/sandbox", "success", success)
}

func (s *Server) sandboxEvent(w http.ResponseWriter, r *http.Request) {
	s.sandboxCall(w, r, "Evento disparado. Ele chega ao webhook em instantes; veja em Eventos.", func(token, phone string, r *http.Request) error {
		numberID, _ := strconv.Atoi(r.FormValue("numero"))
		ctx, cancel := s.apiContext(r)
		defer cancel()
		return s.client.FireSandboxEvent(ctx, token, resgata.SandboxEventRequest{
			Type: r.FormValue("tipo"), Phone: phone, Text: strings.TrimSpace(r.FormValue("texto")), NumberID: numberID,
		})
	})
}

func (s *Server) sandboxInbound(w http.ResponseWriter, r *http.Request) {
	s.sandboxCall(w, r, "Resposta simulada. Abra o lead para ver a mensagem na linha do tempo.", func(token, phone string, r *http.Request) error {
		text := strings.TrimSpace(r.FormValue("texto"))
		if text == "" {
			text = "Oi, quero saber mais."
		}
		ctx, cancel := s.apiContext(r)
		defer cancel()
		return s.client.SandboxInbound(ctx, token, phone, text)
	})
}

func (s *Server) sandboxOptOut(w http.ResponseWriter, r *http.Request) {
	s.sandboxCall(w, r, "Opt-out simulado. O lead deve aparecer como bloqueado em instantes.", func(token, phone string, r *http.Request) error {
		ctx, cancel := s.apiContext(r)
		defer cancel()
		return s.client.SandboxOptOut(ctx, token, phone)
	})
}

func (s *Server) sandboxCreateNumber(w http.ResponseWriter, r *http.Request) {
	s.sandboxCall(w, r, "Número criado no sandbox.", func(token, _ string, r *http.Request) error {
		name := strings.TrimSpace(r.FormValue("nome"))
		ctx, cancel := s.apiContext(r)
		defer cancel()
		_, err := s.client.CreateSandboxNumber(ctx, token, name, r.FormValue("status"))
		return err
	})
}

func (s *Server) sandboxNumberStatus(w http.ResponseWriter, r *http.Request) {
	s.sandboxCall(w, r, "Estado do número alterado. O evento number.* foi disparado.", func(token, _ string, r *http.Request) error {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			return err
		}
		ctx, cancel := s.apiContext(r)
		defer cancel()
		return s.client.SetSandboxNumberStatus(ctx, token, id, r.FormValue("status"))
	})
}

func (s *Server) sandboxReset(w http.ResponseWriter, r *http.Request) {
	s.sandboxCall(w, r, "Sandbox limpo: mensagens, números e eventos foram apagados e um número conectado foi recriado.", func(token, _ string, r *http.Request) error {
		ctx, cancel := s.apiContext(r)
		defer cancel()
		return s.client.ResetSandbox(ctx, token)
	})
}
