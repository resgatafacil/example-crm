package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

func TestCampaignRunsToTheEndWithEveryOutcome(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	templateID := e.approvedTemplateID()

	res := e.do(http.MethodPost, "/campanhas", url.Values{"modelo": {templateID}, "etapa": {"st_novo"}, "nome": {"Novos leads"}, "var_1": {"nome"}}, nil)
	res.Body.Close()
	location := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(location, "/campanhas/cmp_") {
		t.Fatalf("status %d location %q", res.StatusCode, location)
	}

	eventually(t, "the campaign to finish", func() bool {
		_, done := e.campaignWhere(func(c store.Campaign) bool { return c.Status == store.CampaignFinished })
		return done
	})
	campaign, _ := e.campaignWhere(func(c store.Campaign) bool { return c.Name == "Novos leads" })
	if campaign.Progress.Total != 6 || campaign.Progress.Sent != 5 || campaign.Progress.Failed != 1 || campaign.Progress.Read != 5 {
		t.Fatalf("progress = %+v", campaign.Progress)
	}
	if !e.lead("+5500000000030").OptOut {
		t.Fatal("the opt-out of the campaign must reach the lead")
	}
	failed := e.lead("+5500000000003")
	if status, code := e.outboundStatus(failed.ID); status != "failed" || code != "131049" {
		t.Fatalf("failure = %s %s", status, code)
	}
	eventually(t, "the reply on the timeline", func() bool {
		entries, _ := e.store.Timeline(e.lead("+5500000000031").ID)
		for _, entry := range entries {
			if entry.Kind == store.KindMessageIn {
				return true
			}
		}
		return false
	})

	page := readBody(e.do(http.MethodGet, location, nil, nil))
	for _, want := range []string{"Novos leads", "Concluída", "Mariana Souza", "131049"} {
		if !strings.Contains(page, want) {
			t.Errorf("campaign page misses %q", want)
		}
	}
	for _, kind := range []string{"campaign.started", "campaign.finished", "message.sent", "message.read", "message.failed"} {
		events, _ := e.store.Events(kind, 5)
		if len(events) == 0 {
			t.Errorf("no %s event was recorded", kind)
		}
	}
}

func TestCampaignCanBePausedAndResumed(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	res := e.do(http.MethodPost, "/leads", url.Values{"nome": {"Lead lento"}, "celular": {"5500000000020"}, "estagio": {"lead"}, "unidade": {"un_centro"}, "etapa": {"st_novo"}}, nil)
	res.Body.Close()
	e.sendTo("+5500000000020")
	campaign, _ := e.campaignWhere(func(store.Campaign) bool { return true })

	res = e.do(http.MethodPost, "/campanhas/"+campaign.ID+"/pausar", url.Values{}, nil)
	res.Body.Close()
	paused, _, _ := e.store.Campaign(campaign.ID)
	if paused.Status != store.CampaignPaused || paused.PauseReason != "MANUAL_USER" {
		t.Fatalf("campaign = %+v", paused)
	}
	page := readBody(e.do(http.MethodGet, "/campanhas/"+campaign.ID, nil, nil))
	if !strings.Contains(page, "Pausada") || !strings.Contains(page, "Retomar") {
		t.Fatal("a paused campaign must offer to resume")
	}

	time.Sleep(700 * time.Millisecond)
	if still, _, _ := e.store.Campaign(campaign.ID); still.Progress.Sent != 0 {
		t.Fatal("a paused campaign kept sending")
	}
	res = e.do(http.MethodPost, "/campanhas/"+campaign.ID+"/pausar", url.Values{}, nil)
	res.Body.Close()
	if flashMessage(res) == "" {
		t.Fatal("pausing twice must explain why it failed")
	}

	res = e.do(http.MethodPost, "/campanhas/"+campaign.ID+"/retomar", url.Values{}, nil)
	res.Body.Close()
	eventually(t, "the resumed campaign to finish", func() bool {
		done, _, _ := e.store.Campaign(campaign.ID)
		return done.Status == store.CampaignFinished && done.Progress.Read == 1
	})
}

func flashMessage(res *http.Response) string {
	for _, cookie := range res.Cookies() {
		if cookie.Name == flashCookie {
			return cookie.Value
		}
	}
	return ""
}

func TestCampaignFormExplainsWhatIsWrong(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	approved := e.approvedTemplateID()

	res := e.do(http.MethodPost, "/campanhas", url.Values{"modelo": {approved}, "etapa": {"st_novo"}, "var_1": {"texto"}, "txt_1": {""}}, nil)
	body := readBody(res)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Escreva o texto fixo da variável {{1}}") {
		t.Fatalf("fixed text must be required: %d", res.StatusCode)
	}

	res = e.do(http.MethodPost, "/campanhas", url.Values{"modelo": {"tpl_naoexiste"}, "etapa": {"st_novo"}}, nil)
	body = readBody(res)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Modelo não encontrado") {
		t.Fatalf("an unknown template must be refused: %d", res.StatusCode)
	}
}

func TestTemplatesPageCreatesAndTracksApproval(t *testing.T) {
	e := newEnv(t, "")
	e.connect()

	res := e.createTemplate("promo_da_semana", "Oi {{1}}, confira {{2}}!", "Maria")
	body := readBody(res)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "um exemplo por linha") {
		t.Fatalf("missing examples must be explained: %d", res.StatusCode)
	}

	res = e.createTemplate("Promo da Semana", "Oi {{1}}, confira {{2}}!", "Maria\nnossa oferta")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d", res.StatusCode)
	}
	if page := readBody(e.do(http.MethodGet, "/modelos", nil, nil)); !strings.Contains(page, "promo_da_semana") {
		t.Fatal("the name must be normalized by the API")
	}
	eventually(t, "approval", func() bool {
		return strings.Contains(readBody(e.do(http.MethodGet, "/modelos/lista", nil, nil)), "Aprovado")
	})
	eventually(t, "template.approved", func() bool {
		events, _ := e.store.Events("template.approved", 5)
		return len(events) == 1
	})

	res = e.createTemplate("teste_reject", "Texto recusado", "")
	res.Body.Close()
	eventually(t, "rejection", func() bool {
		list := readBody(e.do(http.MethodGet, "/modelos/lista", nil, nil))
		return strings.Contains(list, "Recusado") && strings.Contains(list, "INVALID_FORMAT")
	})
}

func TestReadinessChecklistAndMetaLink(t *testing.T) {
	e := newEnv(t, "")
	e.connectWith("&meta=pending")

	page := readBody(e.do(http.MethodGet, "/integracao", nil, nil))
	for _, want := range []string{"Pronto para disparar?", "WhatsApp Business vinculado à Meta", "Vincular com a Meta", "Simular falha", "Ao menos um modelo aprovado", "Não verificado", "Ainda faltam passos obrigatórios"} {
		if !strings.Contains(page, want) {
			t.Errorf("checklist misses %q", want)
		}
	}

	res := e.createTemplate("antes_da_meta", "Oi!", "")
	body := readBody(res)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Vincule o WhatsApp Business à Meta") {
		t.Fatalf("a template needs the Meta link first: %d", res.StatusCode)
	}

	followMeta := func(query string) string {
		res := e.do(http.MethodGet, "/resgata/meta"+query, nil, nil)
		link := res.Header.Get("Location")
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || !strings.Contains(link, "/oauth/sandbox/meta-link") {
			t.Fatalf("meta link redirect %d %q", res.StatusCode, link)
		}
		res = e.do(http.MethodGet, link, nil, nil)
		back := res.Header.Get("Location")
		res.Body.Close()
		return back
	}

	back := followMeta("?simular=falha")
	if !strings.HasSuffix(back, "/integracao?meta=failed") {
		t.Fatalf("failed link must come back with the result, got %q", back)
	}
	if page := readBody(e.do(http.MethodGet, back, nil, nil)); !strings.Contains(page, "não foi concluída") || !strings.Contains(page, "Vincular com a Meta") {
		t.Fatal("a failed link must be explained and offered again")
	}

	back = followMeta("")
	if !strings.HasSuffix(back, "/integracao?meta=linked") {
		t.Fatalf("got %q", back)
	}
	page = readBody(e.do(http.MethodGet, back, nil, nil))
	if !strings.Contains(page, "Meta vinculada") || strings.Contains(page, "Simular falha") {
		t.Fatal("a linked account must stop offering the link")
	}
	if strings.Contains(page, "Tudo pronto") {
		t.Fatal("without an approved template the connection is not ready yet")
	}

	e.approvedTemplateID()
	if page := readBody(e.do(http.MethodGet, "/integracao", nil, nil)); !strings.Contains(page, "Tudo pronto") {
		t.Fatal("with Meta linked and a template approved the connection is ready")
	}
}

func TestIndividualSendsAreGone(t *testing.T) {
	e := newEnv(t, "")
	e.connect()
	lead := e.lead("+5519991230001")

	res := e.do(http.MethodPost, "/leads/"+lead.ID+"/mensagem", url.Values{"texto": {"oi"}}, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("the individual send route must be gone, got %d", res.StatusCode)
	}
	page := readBody(e.do(http.MethodGet, "/leads/"+lead.ID, nil, nil))
	if strings.Contains(page, `name="texto"`) && strings.Contains(page, "/mensagem") {
		t.Fatal("the lead page must not offer an individual send")
	}
	if !strings.Contains(page, "/campanhas/nova?lead="+lead.ID) {
		t.Fatal("the lead page must offer a campaign for this lead")
	}

	conn, _, _ := e.store.Connection()
	for _, scope := range strings.Fields(conn.Scope) {
		if strings.HasPrefix(scope, "messages:") {
			t.Fatalf("the connection still holds the retired scope %s", scope)
		}
	}
	for _, want := range []string{"templates:write", "audiences:write", "campaigns:write"} {
		if !strings.Contains(conn.Scope, want) {
			t.Errorf("the connection misses the scope %s", want)
		}
	}

	req, _ := http.NewRequest(http.MethodPost, e.cfg.APIURL+"/messages", strings.NewReader(`{"to":"5511999990001","text":"oi"}`))
	req.Header.Set("Authorization", "Bearer anything")
	answer, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	answer.Body.Close()
	if answer.StatusCode != http.StatusNotFound {
		t.Fatalf("the mock must answer 404 like the real API, got %d", answer.StatusCode)
	}
}
