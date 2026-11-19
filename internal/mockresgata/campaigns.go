package mockresgata

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
)

const (
	metaLinked  = "linked"
	metaPending = "pending"
	metaFailed  = "failed"

	maxMockAudience = 200
	duplicateWindow = 10 * time.Minute
)

type template struct {
	ID        string
	Name      string
	Category  string
	Language  string
	Status    string
	Reason    string
	Body      string
	Footer    string
	CreatedAt int64
	UpdatedAt int64
}

type audienceContact struct {
	Phone     string
	Name      string
	Variables []string
}

type audience struct {
	ID       string
	Name     string
	Contacts []audienceContact
}

type campaignMessage struct {
	ID    string
	Phone string
}

type campaign struct {
	ID          string
	Name        string
	TemplateID  string
	AudienceID  string
	Status      string
	PauseReason string
	Total       int
	Sent        int
	Delivered   int
	Read        int
	Failed      int
	CreatedAt   int64
	FinishedAt  *int64
	Messages    []*campaignMessage
	remaining   int
	stuck       int
}

func (m *Mock) setupStepsLocked(c *connection) ([]map[string]any, bool) {
	linked := c.Meta == metaLinked
	hasApproved := false
	for _, t := range c.templates {
		hasApproved = hasApproved || t.Status == "approved"
	}
	step := func(id, title string, required bool, status, detail, action string) map[string]any {
		out := map[string]any{"id": id, "title": title, "status": status, "required": required, "detail": nil, "action_url": nil}
		if detail != "" {
			out["detail"] = detail
		}
		if action != "" && status != "complete" {
			out["action_url"] = action
		}
		return out
	}
	dependsOnMeta := func(id, title string, required bool) map[string]any {
		if linked {
			return step(id, title, required, "complete", "", "")
		}
		return step(id, title, required, "unknown", "Depende da vinculação com a Meta.", "")
	}
	metaStatus, metaDetail := "complete", ""
	switch c.Meta {
	case metaPending:
		metaStatus, metaDetail = "incomplete", "A vinculação com a Meta ainda não foi concluída."
	case metaFailed:
		metaStatus, metaDetail = "incomplete", "A vinculação com a Meta falhou. Tente de novo."
	}
	templateStatus, templateDetail := "complete", ""
	if !hasApproved {
		templateStatus, templateDetail = "incomplete", "Crie um modelo e espere a aprovação da Meta."
	}
	steps := []map[string]any{
		step("workspace", "Conta Resgata Fácil", true, "complete", "", ""),
		step("meta_linked", "WhatsApp Business vinculado à Meta", true, metaStatus, metaDetail, "https://app.resgatafacil.com/meta-official"),
		dependsOnMeta("business_verified", "Empresa verificada na Meta", false),
		dependsOnMeta("account_review", "Conta do WhatsApp Business aprovada", false),
		dependsOnMeta("display_name", "Nome de exibição aprovado", false),
		dependsOnMeta("phone_status", "Número ativo na Meta", false),
		dependsOnMeta("payment_method", "Forma de pagamento na Meta", false),
		dependsOnMeta("messaging_restriction", "Sem restrições de envio", false),
		dependsOnMeta("webhook_subscribed", "Webhook da Meta assinado", false),
		dependsOnMeta("business_profile", "Perfil comercial preenchido", false),
		step("template_available", "Ao menos um modelo aprovado", true, templateStatus, templateDetail, "https://app.resgatafacil.com/modelos"),
		dependsOnMeta("messaging_tier", "Limite de envio da Meta", false),
	}
	return steps, linked && hasApproved
}

func originOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (m *Mock) startMetaLink(w http.ResponseWriter, r *http.Request, c *connection) {
	var body struct {
		ReturnURL string `json:"return_url"`
		Simulate  string `json:"simulate"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	target, err := url.Parse(body.ReturnURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "return_url precisa ser um endereço http(s) válido.")
		return
	}
	result := metaLinked
	if body.Simulate == metaFailed {
		result = metaFailed
	}
	token := randomID("ml_", 12)
	m.mu.Lock()
	m.links[token] = metaLink{conn: c, returnURL: body.ReturnURL, result: result}
	m.mu.Unlock()
	link := originOf(r) + "/oauth/sandbox/meta-link?token=" + url.QueryEscape(token)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"url": link, "expires_at": time.Now().Add(30 * time.Minute).Unix()}})
}

func (m *Mock) finishMetaLink(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	m.mu.Lock()
	link, ok := m.links[token]
	delete(m.links, token)
	if ok {
		link.conn.Meta = link.result
	}
	m.mu.Unlock()
	if !ok {
		apiError(w, http.StatusNotFound, "not_found", "Link de vinculação inválido ou já usado.")
		return
	}
	target, err := url.Parse(link.returnURL)
	if err != nil {
		apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "return_url inválido.")
		return
	}
	values := target.Query()
	values.Set("meta", link.result)
	target.RawQuery = values.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

var accentFolds = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c",
)

func normalizeTemplateName(name string) string {
	var b strings.Builder
	for _, r := range accentFolds.Replace(strings.ToLower(strings.TrimSpace(name))) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteRune('_')
		}
	}
	return b.String()
}

func templateJSON(t *template) map[string]any {
	components := []map[string]any{{"type": "BODY", "text": t.Body}}
	if t.Footer != "" {
		components = append(components, map[string]any{"type": "FOOTER", "text": t.Footer})
	}
	placeholders := resgata.Placeholders(t.Body)
	if placeholders == nil {
		placeholders = []int{}
	}
	var reason any
	if t.Reason != "" {
		reason = t.Reason
	}
	return map[string]any{
		"id": t.ID, "object": "template", "name": t.Name, "category": t.Category, "language": t.Language, "status": t.Status,
		"rejection_reason": reason, "components": components,
		"variables":  map[string]any{"count": len(placeholders), "placeholders": placeholders},
		"created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
	}
}

func fieldsError(w http.ResponseWriter, message string, fields map[string][]string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": message, "fields": fields}})
}

func (m *Mock) createTemplate(w http.ResponseWriter, r *http.Request, c *connection) {
	var body struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Language string `json:"language"`
		Body     struct {
			Text     string   `json:"text"`
			Examples []string `json:"examples"`
		} `json:"body"`
		Footer string `json:"footer"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	fields := map[string][]string{}
	name := normalizeTemplateName(body.Name)
	if name == "" {
		fields["name"] = []string{"Informe o nome do modelo."}
	}
	category := strings.ToUpper(body.Category)
	if category != "MARKETING" && category != "UTILITY" && category != "AUTHENTICATION" {
		fields["category"] = []string{"Use MARKETING, UTILITY ou AUTHENTICATION."}
	}
	text := strings.TrimSpace(body.Body.Text)
	switch {
	case text == "":
		fields["body.text"] = []string{"O texto da mensagem é obrigatório."}
	case len([]rune(text)) > 1024:
		fields["body.text"] = []string{"O texto pode ter no máximo 1024 caracteres."}
	case len(resgata.Placeholders(text)) != len(body.Body.Examples):
		fields["body.examples"] = []string{"Informe um exemplo para cada variável do texto."}
	}
	if len([]rune(body.Footer)) > 60 {
		fields["footer"] = []string{"O rodapé pode ter no máximo 60 caracteres."}
	}
	if len(fields) > 0 {
		fieldsError(w, "O modelo tem campos inválidos.", fields)
		return
	}
	language := body.Language
	if language == "" {
		language = "pt_BR"
	}

	m.mu.Lock()
	if c.Meta != metaLinked {
		m.mu.Unlock()
		apiError(w, http.StatusConflict, "meta_not_ready", "Vincule o WhatsApp Business à Meta antes de criar modelos.")
		return
	}
	for _, existing := range c.templates {
		if existing.Name == name && existing.Language == language {
			out := templateJSON(existing)
			m.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"data": out, "duplicated": true})
			return
		}
	}
	now := time.Now().Unix()
	t := &template{ID: randomID("tpl_", 10), Name: name, Category: category, Language: language, Status: "pending", Body: text, Footer: body.Footer, CreatedAt: now, UpdatedAt: now}
	c.templates = append(c.templates, t)
	out := templateJSON(t)
	m.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
	m.reviewTemplate(c, t)
}

func (m *Mock) reviewTemplate(c *connection, t *template) {
	if strings.Contains(t.Name, "_pending") {
		return
	}
	status, reason := "approved", ""
	if strings.Contains(t.Name, "_reject") {
		status, reason = "rejected", "INVALID_FORMAT"
	}
	m.later(2*m.opts.Speed, func() {
		m.mu.Lock()
		t.Status, t.Reason, t.UpdatedAt = status, reason, time.Now().Unix()
		payload := map[string]any{"id": t.ID, "name": t.Name, "category": t.Category, "language": t.Language, "status": status, "rejection_reason": nil}
		if reason != "" {
			payload["rejection_reason"] = reason
		}
		m.mu.Unlock()
		m.emit(c, "template."+status, map[string]any{"template": payload, "previous_status": "pending"})
	})
}

func (m *Mock) listTemplates(w http.ResponseWriter, r *http.Request, c *connection) {
	status := r.URL.Query().Get("status")
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]any, 0, len(c.templates))
	for i := len(c.templates) - 1; i >= 0; i-- {
		if status == "" || c.templates[i].Status == status {
			out = append(out, templateJSON(c.templates[i]))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "meta": map[string]any{"next_cursor": nil}})
}

func (m *Mock) getTemplate(w http.ResponseWriter, r *http.Request, c *connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := c.findTemplate(r.PathValue("id")); t != nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": templateJSON(t)})
		return
	}
	apiError(w, http.StatusNotFound, "not_found", "Modelo não encontrado.")
}

func (c *connection) findTemplate(id string) *template {
	for _, t := range c.templates {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func normalizePhone(raw string) string {
	d := digits(raw)
	switch {
	case len(d) == 10 || len(d) == 11:
		return "55" + d
	case strings.HasPrefix(d, "55") && (len(d) == 12 || len(d) == 13):
		return d
	}
	return ""
}

func (m *Mock) createAudience(w http.ResponseWriter, r *http.Request, c *connection) {
	var body struct {
		Name     string `json:"name"`
		Contacts []struct {
			Phone     string   `json:"phone"`
			Name      string   `json:"name"`
			Variables []string `json:"variables"`
		} `json:"contacts"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.Contacts) == 0 {
		fieldsError(w, "O público tem campos inválidos.", map[string][]string{"contacts": {"Envie contacts como uma lista com pelo menos um contato."}})
		return
	}
	if len(body.Contacts) > maxMockAudience {
		fieldsError(w, "O público tem campos inválidos.", map[string][]string{"contacts": {"O sandbox aceita no máximo " + strconv.Itoa(maxMockAudience) + " contatos por requisição."}})
		return
	}

	rejections := map[string]int{}
	samples := []map[string]any{}
	seen := map[string]bool{}
	a := &audience{ID: randomID("aud_", 10), Name: body.Name}
	for i, contact := range body.Contacts {
		phone := normalizePhone(contact.Phone)
		reason := ""
		switch {
		case phone == "":
			reason = "invalid"
		case seen[phone]:
			reason = "duplicated"
		}
		if reason != "" {
			rejections[reason]++
			samples = append(samples, map[string]any{"index": i, "phone": contact.Phone, "reason": reason})
			continue
		}
		seen[phone] = true
		a.Contacts = append(a.Contacts, audienceContact{Phone: phone, Name: contact.Name, Variables: contact.Variables})
	}
	if len(a.Contacts) == 0 {
		apiError(w, http.StatusUnprocessableEntity, "no_valid_contacts", "Nenhum contato válido foi enviado.")
		return
	}
	m.mu.Lock()
	c.audiences[a.ID] = a
	m.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{
		"id": a.ID, "object": "audience", "name": a.Name,
		"contacts":   map[string]any{"received": len(body.Contacts), "accepted": len(a.Contacts), "rejected": len(body.Contacts) - len(a.Contacts)},
		"rejections": rejections, "rejected_samples": samples, "created_at": time.Now().Unix(),
	}})
}

func (m *Mock) campaignJSONLocked(cp *campaign) map[string]any {
	pending := max(0, cp.Total-cp.Sent-cp.Failed)
	percent := 0
	if cp.Total > 0 {
		percent = min(100, (cp.Sent+cp.Failed)*100/cp.Total)
	}
	var reason any
	if cp.PauseReason != "" {
		reason = cp.PauseReason
	}
	var finished any
	if cp.FinishedAt != nil {
		finished = *cp.FinishedAt
	}
	return map[string]any{
		"id": cp.ID, "object": "campaign", "name": cp.Name, "status": cp.Status, "pause_reason": reason,
		"template_id": cp.TemplateID, "audience_id": cp.AudienceID, "scheduled_at": nil, "send_until": nil,
		"progress": map[string]any{
			"total": cp.Total, "sent": cp.Sent, "delivered": cp.Delivered, "read": cp.Read, "failed": cp.Failed,
			"skipped": 0, "pending": pending, "percent": percent,
		},
		"created_at": cp.CreatedAt, "finished_at": finished,
	}
}

func sampleCampaign(kind string) map[string]any {
	status := map[string]string{"started": "running", "paused": "paused", "finished": "finished", "failed": "failed"}[kind]
	if status == "" {
		status = "running"
	}
	return map[string]any{
		"id": "cmp_sample", "object": "campaign", "name": "Campanha de teste", "status": status, "pause_reason": nil,
		"template_id": "tpl_sample", "audience_id": "aud_sample",
		"progress":   map[string]any{"total": 2, "sent": 2, "delivered": 1, "read": 0, "failed": 0, "skipped": 0, "pending": 0, "percent": 100},
		"created_at": time.Now().Unix(), "finished_at": nil,
	}
}

func (c *connection) findCampaign(id string) *campaign {
	for _, cp := range c.campaigns {
		if cp.ID == id {
			return cp
		}
	}
	return nil
}

func (m *Mock) createCampaign(w http.ResponseWriter, r *http.Request, c *connection) {
	var body struct {
		Name       string            `json:"name"`
		TemplateID string            `json:"template_id"`
		AudienceID string            `json:"audience_id"`
		Variables  map[string]string `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	fields := map[string][]string{}
	if body.TemplateID == "" {
		fields["template_id"] = []string{"Informe o template_id do modelo aprovado."}
	}
	if body.AudienceID == "" {
		fields["audience_id"] = []string{"Informe o audience_id do público."}
	}
	if len(fields) > 0 {
		fieldsError(w, "A campanha tem campos inválidos.", fields)
		return
	}

	m.mu.Lock()
	if c.Meta != metaLinked {
		m.mu.Unlock()
		apiError(w, http.StatusConflict, "meta_not_ready", "Vincule o WhatsApp Business à Meta antes de criar campanhas.")
		return
	}
	t := c.findTemplate(body.TemplateID)
	a := c.audiences[body.AudienceID]
	switch {
	case t == nil:
		m.mu.Unlock()
		apiError(w, http.StatusNotFound, "not_found", "Modelo não encontrado.")
		return
	case a == nil:
		m.mu.Unlock()
		apiError(w, http.StatusNotFound, "not_found", "Público não encontrado.")
		return
	case t.Status != "approved":
		m.mu.Unlock()
		apiError(w, http.StatusConflict, "template_not_approved", "O modelo ainda não foi aprovado pela Meta.")
		return
	}
	for _, n := range resgata.Placeholders(t.Body) {
		if strings.TrimSpace(body.Variables[strconv.Itoa(n)]) == "" {
			m.mu.Unlock()
			apiError(w, http.StatusUnprocessableEntity, "invalid_request_error", "Informe o valor da variável {{"+strconv.Itoa(n)+"}}.")
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = t.Name
	}
	now := time.Now()
	for _, existing := range c.campaigns {
		if existing.TemplateID == t.ID && existing.AudienceID == a.ID && existing.Name == name && now.Sub(time.Unix(existing.CreatedAt, 0)) < duplicateWindow {
			out := m.campaignJSONLocked(existing)
			m.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"data": out, "duplicated": true})
			return
		}
	}
	cp := &campaign{ID: randomID("cmp_", 10), Name: name, TemplateID: t.ID, AudienceID: a.ID, Status: "running", Total: len(a.Contacts), CreatedAt: now.Unix()}
	for _, contact := range a.Contacts {
		cp.Messages = append(cp.Messages, &campaignMessage{ID: randomID("wamid_", 10), Phone: contact.Phone})
	}
	c.campaigns = append(c.campaigns, cp)
	out := m.campaignJSONLocked(cp)
	m.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
	m.startCampaign(c, cp)
}

func (m *Mock) listCampaigns(w http.ResponseWriter, _ *http.Request, c *connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ordered := append([]*campaign(nil), c.campaigns...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CreatedAt > ordered[j].CreatedAt })
	out := make([]any, 0, len(ordered))
	for _, cp := range ordered {
		out = append(out, m.campaignJSONLocked(cp))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "meta": map[string]any{"next_cursor": nil}})
}

func (m *Mock) getCampaign(w http.ResponseWriter, r *http.Request, c *connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := c.findCampaign(r.PathValue("id"))
	if cp == nil {
		apiError(w, http.StatusNotFound, "not_found", "Campanha não encontrada.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": m.campaignJSONLocked(cp)})
}

func (m *Mock) pauseCampaign(w http.ResponseWriter, r *http.Request, c *connection) {
	m.mu.Lock()
	cp := c.findCampaign(r.PathValue("id"))
	if cp == nil {
		m.mu.Unlock()
		apiError(w, http.StatusNotFound, "not_found", "Campanha não encontrada.")
		return
	}
	if cp.Status != "running" && cp.Status != "pending" && cp.Status != "scheduled" {
		m.mu.Unlock()
		apiError(w, http.StatusConflict, "campaign_not_active", "A campanha não está em andamento.")
		return
	}
	cp.Status, cp.PauseReason = "paused", "MANUAL_USER"
	out := m.campaignJSONLocked(cp)
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
	m.emit(c, "campaign.paused", map[string]any{"campaign": out})
}

func (m *Mock) resumeCampaign(w http.ResponseWriter, r *http.Request, c *connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := c.findCampaign(r.PathValue("id"))
	if cp == nil {
		apiError(w, http.StatusNotFound, "not_found", "Campanha não encontrada.")
		return
	}
	if cp.Status != "paused" {
		apiError(w, http.StatusConflict, "campaign_not_paused", "A campanha não está pausada.")
		return
	}
	cp.Status, cp.PauseReason = "running", ""
	writeJSON(w, http.StatusOK, map[string]any{"data": m.campaignJSONLocked(cp)})
}

type scenarioStep struct {
	After  time.Duration
	Kind   string
	Code   string
	Silent bool
}

var failureCodes = map[string]string{
	"5500000000000": "131026",
	"5500000000001": "131047",
	"5500000000002": "131048",
	"5500000000003": "131049",
	"5500000000004": "131056",
}

func scenarioFor(phone string, step time.Duration) []scenarioStep {
	if code, failing := failureCodes[phone]; failing {
		return []scenarioStep{{After: 0, Kind: "failed", Code: code}}
	}
	switch phone {
	case "5500000000010":
		return []scenarioStep{{After: 0, Kind: "sent"}}
	case "5500000000011":
		return []scenarioStep{{After: 0, Kind: "sent"}, {After: step, Kind: "delivered"}}
	case "5500000000012":
		return nil
	case "5500000000020":
		return []scenarioStep{{After: 30 * step, Kind: "sent"}, {After: 60 * step, Kind: "delivered"}, {After: 120 * step, Kind: "read"}}
	}
	steps := []scenarioStep{{After: 0, Kind: "sent"}, {After: step, Kind: "delivered"}, {After: 2 * step, Kind: "read"}}
	switch phone {
	case "5500000000030":
		steps = append(steps, scenarioStep{After: 3 * step, Kind: "opt_out"})
	case "5500000000031":
		steps = append(steps, scenarioStep{After: 3 * step, Kind: "reply"})
	}
	return steps
}

func (m *Mock) startCampaign(c *connection, cp *campaign) {
	m.mu.Lock()
	type plan struct {
		msg   *campaignMessage
		steps []scenarioStep
	}
	var plans []plan
	for _, msg := range cp.Messages {
		steps := scenarioFor(msg.Phone, m.opts.Speed)
		if len(steps) == 0 {
			cp.stuck++
		}
		cp.remaining += len(steps)
		plans = append(plans, plan{msg: msg, steps: steps})
	}
	started := m.campaignJSONLocked(cp)
	m.mu.Unlock()

	m.emit(c, "campaign.started", map[string]any{"campaign": started})
	for _, p := range plans {
		for _, st := range p.steps {
			m.later(st.After, func() { m.runCampaignStep(c, cp, p.msg, st) })
		}
	}
}

func (m *Mock) runCampaignStep(c *connection, cp *campaign, msg *campaignMessage, st scenarioStep) {
	m.mu.Lock()
	if cp.Status == "paused" {
		m.mu.Unlock()
		m.later(m.opts.Speed, func() { m.runCampaignStep(c, cp, msg, st) })
		return
	}
	switch st.Kind {
	case "sent":
		cp.Sent++
	case "delivered":
		cp.Delivered++
	case "read":
		cp.Read++
	case "failed":
		cp.Failed++
	}
	m.mu.Unlock()

	switch st.Kind {
	case "opt_out":
		m.emit(c, "contact.opted_out", map[string]any{"contact": map[string]any{"phone": msg.Phone}, "matched_phrase": "parar", "campaign_id": cp.ID})
	case "reply":
		m.emit(c, "message.received", map[string]any{"message": map[string]any{"id": randomID("in_", 6), "channel": "sandbox", "from": msg.Phone, "from_name": "", "type": "text", "text": "Recebi sua mensagem!", "campaign_id": cp.ID}})
	default:
		data := map[string]any{"message": map[string]any{"id": msg.ID, "channel": "sandbox", "to": msg.Phone, "status": st.Kind, "number_id": 1, "campaign_id": cp.ID}}
		if st.Kind == "failed" {
			data["error"] = map[string]any{"code": mustInt(st.Code), "message": "Mensagem não entregue."}
		}
		m.emit(c, "message."+st.Kind, data)
	}
	m.finishStep(c, cp)
}

func (m *Mock) finishStep(c *connection, cp *campaign) {
	m.mu.Lock()
	cp.remaining--
	done := cp.remaining == 0 && cp.stuck == 0 && cp.Status != "finished" && cp.Status != "failed"
	var event string
	var payload map[string]any
	if done {
		event = "campaign.finished"
		cp.Status = "finished"
		if cp.Sent == 0 && cp.Failed > 0 {
			event, cp.Status = "campaign.failed", "failed"
		}
		finished := time.Now().Unix()
		cp.FinishedAt = &finished
		payload = m.campaignJSONLocked(cp)
	}
	m.mu.Unlock()
	if done {
		m.emit(c, event, map[string]any{"campaign": payload})
	}
}
