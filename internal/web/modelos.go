package web

import (
	"net/http"
	"strings"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
)

type templateForm struct {
	Name     string
	Category string
	Body     string
	Examples string
	Footer   string
}

type templatesData struct {
	Connected bool
	Templates []resgata.Template
	Pending   bool
	Error     string
	Form      templateForm
	Errors    map[string]string
}

func defaultTemplateForm() templateForm {
	return templateForm{Category: "MARKETING", Body: "Oi {{1}}, temos uma novidade para você!", Examples: "Maria"}
}

func (s *Server) loadTemplates(r *http.Request, data *templatesData) {
	_, token, connected := s.token(r)
	data.Connected = connected
	if !connected {
		return
	}
	ctx, cancel := s.apiContext(r)
	defer cancel()
	templates, err := s.client.Templates(ctx, token, "")
	if err != nil {
		data.Error = s.describeAPIError(err)
		return
	}
	data.Templates = templates
	for _, t := range templates {
		data.Pending = data.Pending || t.Status == "pending"
	}
}

func (s *Server) templatesPage(w http.ResponseWriter, r *http.Request) {
	data := templatesData{Form: defaultTemplateForm(), Errors: map[string]string{}}
	s.loadTemplates(r, &data)
	s.render(w, r, "templates", "Modelos de mensagem", "templates", data)
}

func (s *Server) templatesFragment(w http.ResponseWriter, r *http.Request) {
	data := templatesData{}
	s.loadTemplates(r, &data)
	s.fragmentOf(w, "templates", "templatesList", data)
}

func splitLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (s *Server) readTemplateForm(r *http.Request) (templateForm, resgata.TemplateInput, map[string]string) {
	_ = r.ParseForm()
	form := templateForm{
		Name:     strings.TrimSpace(r.FormValue("nome")),
		Category: r.FormValue("categoria"),
		Body:     strings.TrimSpace(r.FormValue("corpo")),
		Examples: r.FormValue("exemplos"),
		Footer:   strings.TrimSpace(r.FormValue("rodape")),
	}
	errs := map[string]string{}
	if form.Name == "" {
		errs["name"] = "Dê um nome ao modelo."
	}
	if form.Category != "MARKETING" && form.Category != "UTILITY" {
		errs["category"] = "Escolha marketing ou utilidade."
	}
	switch {
	case form.Body == "":
		errs["body"] = "Escreva o texto da mensagem."
	case len([]rune(form.Body)) > 1024:
		errs["body"] = "O texto pode ter até 1024 caracteres."
	}
	examples := splitLines(form.Examples)
	if want := len(resgata.Placeholders(form.Body)); form.Body != "" && want != len(examples) {
		errs["examples"] = "Informe um exemplo por linha para cada variável do texto ({{1}}, {{2}}…)."
	}
	if len([]rune(form.Footer)) > 60 {
		errs["footer"] = "O rodapé pode ter até 60 caracteres."
	}
	return form, resgata.TemplateInput{Name: form.Name, Category: form.Category, Language: "pt_BR", BodyText: form.Body, BodyExamples: examples, Footer: form.Footer}, errs
}

var templateFieldKeys = map[string]string{
	"name": "name", "category": "category", "body.text": "body", "body.examples": "examples", "footer": "footer",
}

func (s *Server) templateCreate(w http.ResponseWriter, r *http.Request) {
	_, token, connected := s.token(r)
	if !connected {
		s.redirect(w, r, "/integracao", "error", "Conecte o CRM ao Resgata Fácil antes de criar modelos.")
		return
	}
	form, input, errs := s.readTemplateForm(r)
	data := templatesData{Form: form, Errors: errs}
	if len(errs) == 0 {
		ctx, cancel := s.apiContext(r)
		_, err := s.client.CreateTemplate(ctx, token, input)
		cancel()
		if err == nil {
			s.redirect(w, r, "/modelos", "success", "Modelo enviado para a aprovação da Meta. O status muda sozinho nesta tela.")
			return
		}
		var apiErr *resgata.APIError
		if asAPIError(err, &apiErr) && len(apiErr.Fields) > 0 {
			for field, messages := range apiErr.Fields {
				key, known := templateFieldKeys[field]
				if !known {
					key = "body"
				}
				errs[key] = strings.Join(messages, " ")
			}
		} else {
			data.Error = s.describeAPIError(err)
		}
	}
	s.loadTemplates(r, &data)
	if data.Error == "" && len(errs) == 0 {
		data.Error = "Não foi possível criar o modelo."
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.render(w, r, "templates", "Modelos de mensagem", "templates", data)
}
