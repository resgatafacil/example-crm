# API da Plataforma Resgata Fácil (guia de integração para parceiros)

Versão `2026-11-01`. URL base `https://app.resgatafacil.com/api/platform/v1`. JSON na entrada e na saída, datas e horas em segundos Unix (exceto `scheduled_at` e `send_until` das campanhas, que usam ISO 8601).

## Conceitos

- **Aplicativo (Application)**: o seu produto (por exemplo, o CRMSOLUT). É criado no portal do desenvolvedor, com o domínio, as URIs de redirecionamento do OAuth, as chaves de API e os endpoints de webhook.
- **Conexão (Connection)**: um usuário seu ligado a um Workspace da Resgata. Toda chamada de API e todo webhook trazem a conexão, então você identifica o usuário certo por `connection.id` ou pelo seu próprio `external_ref`.
- **Ambiente**: `live` (números de WhatsApp reais) ou `sandbox` (serviços simulados, nada sai dos nossos servidores). A credencial define o ambiente: `rf_live_*` ou `rf_test_*`.
- **Credenciais**
  - Chave do aplicativo `rf_<env>_app_…`: age em nome do aplicativo inteiro (listar conexões, emitir tokens, criar conexões de sandbox).
  - Token de conexão `rf_<env>_conn_…`: age em nome da conexão de um usuário. Envie como `Authorization: Bearer <credencial>`.

Chaves e tokens são exibidos uma única vez e guardados com hash. Para trocar, crie uma nova credencial e revogue a antiga.

## Conectando um usuário (OAuth, live)

Código de autorização com PKCE (S256 é obrigatório).

1. Cadastre no portal as URIs de redirecionamento exatas do seu aplicativo (`https://…`, ou `http://localhost…` em desenvolvimento).
2. Envie o navegador do usuário para `https://app.resgatafacil.com/autorizar` com:
   `client_id` (o id do seu aplicativo, `app_…`), `redirect_uri`, `state`, `code_challenge`, `code_challenge_method=S256`, `scope` (separados por espaço) e `external_ref` (o seu id para esse usuário, opcional, mas recomendado).
3. O usuário entra na conta (se precisar), **conecta a conta da Meta (WhatsApp Business) pelo Facebook** quando o aplicativo exigir (veja abaixo), vê o seu nome e as permissões e aprova. Só a pessoa root do Workspace ou um administrador pode aprovar. Redirecionamos para `redirect_uri?code=…&state=…`, ou para `?error=access_denied|external_ref_in_use&state=…`.
4. No seu servidor, faça `POST /oauth/token` com `grant_type=authorization_code`, `code`, `code_verifier`, `redirect_uri` e `client_id`. Resposta: `access_token` (o token de conexão), `token_type` (`Bearer`), `scope`, `connection_id` e `external_ref`. Cada código funciona uma só vez e expira em 10 minutos.

Este endpoint segue o padrão OAuth e responde erros fora do formato da API, com status `400` e `{ "error": "<código>", "error_description": "…" }`. Os códigos são `unsupported_grant_type`, `invalid_grant` (código inválido, expirado ou já usado) e os de autorização `invalid_client`, `invalid_request` e `invalid_scope`. O limite é de 30 requisições por minuto.

Permissões (scopes): `numbers:read`, `contacts:read`, `campaigns:read`, `campaigns:write`, `audiences:write`, `templates:read`, `templates:write`. Hoje nenhum endpoint exige `contacts:read`; ele está reservado.

O usuário pode desconectar o seu aplicativo quando quiser na Resgata (Integrações › Aplicativos conectados); você pode desconectar com `DELETE /connection`, que responde `{ "object": "connection", "status": "revoked" }`. Os dois caminhos enviam o webhook `connection.revoked`.

### Os dois logins: Resgata e Meta

Conectar um usuário envolve duas etapas de OAuth, ambas em telas da Resgata, então você nunca lida com credenciais do Facebook:

1. **Consentimento na Resgata** (`/autorizar`, acima). Se o usuário ainda não vinculou uma conta oficial do WhatsApp Business da Meta, a tela de consentimento mostra antes a etapa "Conectar a Meta (WhatsApp Business)" (login do Facebook, escolha da empresa) e só habilita **Autorizar** depois que ela der certo.
2. **Vínculo com a Meta.** Acontece dentro dessa tela, ou depois, por um link de configuração que você pede com `POST /connection/meta-link`.

Por padrão, os aplicativos exigem a etapa da Meta. Você pode desligá-la por aplicativo no portal do desenvolvedor (visão geral do aplicativo, "Conexão com a Meta"). Autorizar sem uma conta da Meta vinculada quando ela é exigida é recusado com `422 meta_required` (a tela de consentimento já impede isso).

Um número de WhatsApp conectado (QR code) **não** é necessário. Vincular a conta da Meta ocupa uma vaga de número do plano Resgata do usuário, como vincular no aplicativo da Resgata. Quando o plano não tem vaga livre, o vínculo é cancelado e a configuração mostra `meta_linked` como incompleto; `POST /audiences` e `POST /campaigns` respondem `409 no_slot_available` para uma conexão cujo vínculo com a Meta não tem número.

#### Link de configuração da etapa da Meta

`POST /connection/meta-link` (token de conexão) com `{ "return_url": "https://app.seudominio.com/integracoes/resgata/meta-return" }`. Resposta:

```json
{ "data": { "url": "https://app.resgatafacil.com/vincular/…", "expires_at": 1767226800 } }
```

Abra `url` no navegador do usuário (não precisa de login na Resgata). O link é de uso único e expira em 30 minutos. Quando o usuário termina, enviamos o navegador para o seu `return_url` com `?status=linked|failed&connection=conn_…` acrescentado (os parâmetros de query que já existiam são mantidos). Depois de `status=linked`, `GET /connection/setup` mostra `meta_linked` como `complete` sem que você precise fazer mais nada.

`return_url` precisa ter a mesma origem (esquema, host e porta) de uma das URIs de redirecionamento cadastradas no aplicativo (`https`, ou `http://localhost…` em desenvolvimento); qualquer outra coisa responde `422 invalid_request_error`. Não dependa do `action_url` estático da etapa `meta_linked`: chame este endpoint, ou passe `?return_url=…` em `GET /connection/setup`, e o `action_url` de uma etapa `meta_linked` incompleta será um link de configuração novo.

## O que ainda falta para o usuário

`GET /connection/setup` (token de conexão) informa se a conta oficial do WhatsApp Business da Meta do usuário está pronta para enviar, para você desenhar o checklist na sua própria interface. Um número de WhatsApp conectado (QR code) **não** é obrigatório: a plataforma só precisa que a conta oficial da Meta esteja vinculada e saudável.

```json
{ "object": "connection_setup", "connection": "conn_…", "ready": false,
  "steps": [
    { "id": "workspace", "title": "Conta Resgata Fácil", "status": "complete", "required": true, "detail": null, "action_url": null },
    { "id": "meta_linked", "title": "Vincular a Meta oficial", "status": "incomplete", "required": true,
      "detail": "Nenhuma conta da Meta oficial vinculada.", "action_url": "https://app.resgatafacil.com/meta-official/vincular" },
    { "id": "payment_method", "title": "Forma de pagamento na Meta", "status": "unknown", "required": false,
      "detail": "A Meta não nos informa a forma de pagamento. Confira no Billing Hub da Meta…", "action_url": "https://business.facebook.com/…" } ] }
```

Cada etapa tem um `status`:

| Status | Significado |
|---|---|
| `complete` | Concluída |
| `incomplete` | O usuário ainda precisa fazer algo; `action_url` abre o lugar certo |
| `blocked` | Há algo errado do lado da Meta (rejeição, restrição, problema de pagamento, número que saiu da Cloud API); `detail` explica o quê |
| `unknown` | Ainda não sabemos dizer (a Meta não informou) |

`ready` é `true` quando toda etapa `required` está `complete` e nenhuma etapa está `blocked`. `detail` é uma explicação curta, legível por pessoas, em português, e pode ser `null`.

| Id da etapa | O que verifica | Obrigatória |
|---|---|---|
| `workspace` | A conta Resgata existe | sim |
| `meta_linked` | Há um vínculo ativo com a Meta oficial, com conta do WhatsApp Business e número, sem pedido de reconexão | sim |
| `business_verified` | A empresa está verificada no Gerenciador de Negócios da Meta | não |
| `account_review` | A conta do WhatsApp Business passou pela análise da Meta | sim |
| `display_name` | O nome de exibição está aprovado | sim |
| `phone_status` | O número está conectado à Cloud API | sim |
| `payment_method` | A Meta está cobrando os envios normalmente (`complete`), informa um problema de pagamento (`blocked`) ou ainda não temos sinal (`unknown`) | não |
| `messaging_restriction` | Não há restrição ativa de envio (por exemplo 130497 ou 131031) | sim |
| `webhook_subscribed` | Os webhooks da Meta estão inscritos (a Resgata corrige isso automaticamente) | não |
| `business_profile` | Descrição, categoria da empresa e pelo menos um contato (e-mail, site ou endereço) estão preenchidos | não |
| `template_available` | Existe pelo menos um modelo de mensagem aprovado | não |
| `messaging_tier` | Informativa: o limite diário de mensagens (`detail`) | não |

A Meta não expõe um sinal positivo de "tem forma de pagamento", então `payment_method` só vira `complete` depois que a Meta cobrou um envio do número (ou quando a Resgata paga a conta). Até lá fica `unknown` e não impede o `ready`; um problema de pagamento informado sempre impede.

Os valores vêm de dados que a Resgata já mantém sincronizados com a Meta (verificações de saúde e webhooks da Meta), então o endpoint é rápido e não faz chamadas à Meta. Depois que o usuário corrige algo na Meta, pode levar alguns minutos para aparecer aqui.

`action_url` abre o lugar certo na Resgata (ou no Gerenciador de Negócios da Meta) para o usuário concluir a etapa, incluindo o login da Meta oficial.

## Endpoints

| Método e caminho | Credencial | Observações |
|---|---|---|
| `GET /ping` | qualquer | Informa com quem você está autenticado: `application`, `environment`, `credential` (`application_key` ou `connection_token`), `connection` e `api_version` |
| `GET /connections`, `GET /connections/{id}` | chave do aplicativo | Conexões do seu aplicativo no ambiente da chave. A lista tem 50 por página (`?page=`) |
| `POST /connections` | chave de aplicativo do sandbox | `{ "external_ref": "…" }` cria uma conexão de sandbox (`409 external_ref_in_use` se já existir). No live responde `403 consent_required`: as conexões vêm do OAuth |
| `POST /connections/{id}/tokens` | chave do aplicativo | Emite um token de conexão (o segredo aparece uma vez, em `secret`; `name` opcional). `DELETE /connections/{id}/tokens/{token}` revoga, com o id numérico do token |
| `GET /connection` | token de conexão | A conexão atual |
| `GET /connection/setup` | token de conexão | Etapas de configuração (acima). O parâmetro opcional `?return_url=…` transforma a ação de `meta_linked` em um link de configuração |
| `POST /connection/meta-link` | token de conexão | `{ "return_url" }` → `{ data: { url, expires_at } }`, o link de configuração da Meta (acima) |
| `DELETE /connection` | token de conexão | Desconecta |
| `GET /numbers` | token com `numbers:read` | Números do workspace, se estão conectados (`connected`) e o `status` |
| `GET /templates`, `GET /templates/{id}` | token com `templates:read` | Os modelos de mensagem da conta do WhatsApp Business vinculada, veja Modelos |
| `POST /templates`, `POST /templates/media`, `POST /templates/{id}/sync`, `DELETE /templates/{id}` | token com `templates:write` | Criar (envia à Meta para aprovação), enviar mídia de cabeçalho, atualizar o status a partir da Meta e excluir |
| `POST /audiences` | token com `audiences:write` | Cria um público (as pessoas que uma campanha vai receber) a partir de uma lista JSON de contatos, veja Públicos |
| `GET /audiences/{id}` | token com `campaigns:read` | Um público com quantos contatos foram aceitos |
| `POST /campaigns` | token com `campaigns:write` | Inicia uma campanha: um modelo aprovado enviado a um público, veja Campanhas |
| `GET /campaigns`, `GET /campaigns/{id}` | token com `campaigns:read` | Campanhas criadas por esta conexão, com o andamento |
| `POST /campaigns/{id}/pause`, `POST /campaigns/{id}/resume` | token com `campaigns:write` | Pausa e retoma uma campanha |
| `POST /oauth/token` | nenhuma | Troca o código de autorização pelo token de conexão (acima) |
| `GET /oauth/sandbox/authorize` | nenhuma | Redirecionamento de consentimento do sandbox, veja Sandbox |
| `POST /sandbox/...` | credencial do sandbox | Ajudantes do sandbox (números, eventos, mensagens recebidas, opt-outs, reset), veja Sandbox |

Os erros usam `{ "error": { "type", "message" } }`, às vezes com campos extras (`fields`, `allowed`, `scope`). O `type` é a classe geral do erro ou um código específico:

| Status | `type` |
|---|---|
| 401 | `authentication_error` (credencial ausente, inválida ou revogada) |
| 403 | `permission_error` (rota exige outro tipo de credencial, ou o token não tem a permissão; o campo `scope` diz qual) |
| 404 | `not_found` |
| 422 | `invalid_request_error` (com `fields` quando for erro de campo) |
| 429 | `rate_limit_error` |

Os demais códigos específicos de cada endpoint (`meta_not_ready`, `no_slot_available`, `plan_not_allowed`…) estão descritos nas seções abaixo. Rotas do sandbox chamadas com credencial live respondem `403 sandbox_only`; os limites do sandbox respondem `422 sandbox_limit_reached`. Limite: 120 requisições por minuto por credencial; respeite o `Retry-After`.

## Modelos

Modelos são as mensagens que uma campanha envia. Crie-os pela API e eles vão para a Meta para aprovação, exatamente como os modelos criados no aplicativo da Resgata. Eles ficam na conta do WhatsApp Business que o usuário vinculou à Meta, então a conexão precisa ter `meta_linked` completo em `GET /connection/setup`; caso contrário `POST /templates` responde `409 meta_not_ready`.

Os ids têm o formato `tpl_…`. Status: `pending`, `approved`, `rejected`, `paused`, `disabled`, `in_appeal`, `limit_exceeded`, `reinstated`. Só modelos `approved` e `reinstated` podem ser usados em campanhas.

```json
{ "id": "tpl_123", "object": "template", "name": "promocao_da_semana", "category": "MARKETING",
  "language": "pt_BR", "status": "pending", "rejection_reason": null,
  "components": [ { "type": "BODY", "text": "Oi {{1}}, confira nossa promoção." } ],
  "variables": { "count": 1, "placeholders": [1] }, "created_at": 1767225600, "updated_at": 1767225600 }
```

### Criando um modelo

`POST /templates` com corpo JSON. `name` é texto livre: ele é normalizado para a forma minúscula `a-z0-9_` que a Meta exige (`"Promoção da Semana"` vira `promocao_da_semana`). `category` é `MARKETING`, `UTILITY` ou `AUTHENTICATION`.

| Campo | Observações |
|---|---|
| `body.text` | Obrigatório nos modelos estruturados, até 1024 caracteres. Use `{{1}}`, `{{2}}`… para variáveis, com uma entrada em `body.examples` por variável |
| `header` | `{ "type": "text", "text": "…" }` (até 60 caracteres) ou `{ "type": "image" \| "video" \| "document", "media_id": "…" }`. `media_url` (um link público) funciona no lugar de `media_id` |
| `footer` | Texto, até 60 caracteres |
| `buttons` | Até 3: `QUICK_REPLY`, `URL` (`url`, https) ou `PHONE_NUMBER` (`phone_number`). Texto do botão até 25 caracteres |
| `carousel.cards` | De 2 a 10 cartões `{ "media_type": "image" \| "video", "media_id", "body"?, "buttons": [ … ] }`. Com carrossel, o texto principal não leva cabeçalho, rodapé nem botões |
| `components` | Avançado: o array `components` cru da Meta, junto com `name`, `category` e `language`. Use para modelos `AUTHENTICATION` (botões de OTP) ou qualquer coisa que os campos acima não cubram. Ele substitui os campos estruturados |

```sh
curl -X POST $BASE/templates -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "name": "promocao_da_semana", "category": "MARKETING", "language": "pt_BR",
  "header": { "type": "image", "media_id": "<de POST /templates/media>" },
  "body": { "text": "Oi {{1}}, confira nossa promoção.", "examples": ["Maria"] },
  "footer": "Responda PARAR para sair",
  "buttons": [ { "type": "URL", "text": "Ver oferta", "url": "https://seusite.com/oferta" } ]
}'
```

Respostas: `201` com o modelo (`status: pending`), ou `200` com `"duplicated": true` quando já existe um modelo idêntico (o existente é devolvido e nada é enviado à Meta). Em casos raros a Meta aceita o modelo, mas ainda não temos o registro local: a resposta é `202` com `{ "data": { "object": "template", "id": null, "status": "pending" } }`; consulte `GET /templates` depois. Entrada inválida é `422` com os nomes de campo desta API:

```json
{ "error": { "type": "invalid_request_error", "message": "O modelo tem campos inválidos.",
  "fields": { "body.text": ["O texto da mensagem é obrigatório."], "buttons": ["Use no máximo 3 botões."] } } }
```

Outros erros: `422 invalid_request_error` quando o corpo vem vazio (`Envie o modelo em JSON.`), `409 template_in_progress` (o mesmo modelo está sendo criado agora, tente de novo em alguns segundos), `409 meta_not_ready`, `422 invalid_request_error` quando a Meta recusa o modelo (a mensagem diz o motivo) e `502 meta_unavailable`.

### Mídia de cabeçalho

`POST /templates/media` (multipart, campo `file`: jpg, png, mp4 ou pdf, até 25 MB) envia o arquivo à Meta e devolve `{ "data": { "object": "template_media", "media_id": "…", "mime_type": "…" } }`. Passe esse `media_id` em `header.media_id` ou `carousel.cards[].media_id` ao criar o modelo. Erros: `422 invalid_request_error` (arquivo ausente, tipo ou tamanho inválido, ou recusado pela Meta), `409 meta_not_ready` e `502 meta_unavailable`.

### Consultando, atualizando e excluindo

- `GET /templates` lista os modelos, do mais novo para o mais antigo. Filtros: `status`, `category` (`MARKETING`, `UTILITY` ou `AUTHENTICATION`), `language`, `name` (contém). `limit` vai de 1 a 100 (padrão 25); siga `meta.next_cursor` com `?cursor=…` para a próxima página.
- `GET /templates/{id}` devolve um modelo.
- `POST /templates/{id}/sync` pergunta à Meta o status atual e devolve o modelo atualizado (`502 meta_unavailable` se a Meta não responder).
- `DELETE /templates/{id}` exclui o modelo na Meta e na Resgata e responde `{ "id", "object": "template", "deleted": true }`. Quando a Meta não permite que a conta exclua o modelo, ele é arquivado e a resposta traz `"deleted": false, "archived": true`. Se a exclusão for recusada, a resposta é `422 invalid_request_error`.

### Eventos

`template.approved`, `template.rejected` e `template.status_changed` disparam quando um modelo muda de status. `template.approved` cobre `approved` e `reinstated`, `template.rejected` cobre `rejected`, e `template.status_changed` cobre qualquer outra mudança (`paused`, `disabled`, `in_appeal`, `limit_exceeded`, volta para `pending`…).

```json
{ "type": "template.rejected", "data": {
  "template": { "id": "tpl_123", "name": "promocao_da_semana", "category": "MARKETING", "language": "pt_BR",
                "status": "rejected", "rejection_reason": "INVALID_FORMAT" },
  "previous_status": "pending" } }
```

Modelos que o cliente cria no aplicativo da Resgata também disparam esses eventos para os aplicativos conectados, porque ficam na mesma conta. Quem decide o status é a Meta, então o evento pode chegar de minutos a dias depois da criação.

## Públicos

Um público é a lista de pessoas para quem uma campanha envia. São os seus próprios leads: envie-os na requisição, nada é lido dos contatos da Resgata do cliente.

`POST /audiences` (token com `audiences:write`):

```json
{ "name": "Leads de novembro",
  "contacts": [
    { "phone": "(11) 98888-0001", "name": "Maria Souza", "variables": ["Dentista", "Sexta"] },
    { "phone": "11988880002", "name": "João Pereira" } ] }
```

`phone` é obrigatório (qualquer formato brasileiro comum, com ou sem código do país). `name` é opcional. `variables` preenchem os `{{n}}` do modelo: uma lista (a posição 1 é `variable_1`) ou um objeto `{ "2": "Sexta" }`, até 15 valores de 250 caracteres. Uma requisição aceita até 5.000 contatos (200 no sandbox). O `name` do público tem até 150 caracteres.

Resposta `201`:

```json
{ "data": { "id": "aud_…", "object": "audience", "name": "Leads de novembro",
  "contacts": { "received": 2, "accepted": 2, "rejected": 0 },
  "rejections": {}, "rejected_samples": [], "created_at": 1767225600 } }
```

Os contatos que não aceitamos são contados em `rejections` por motivo (`invalid`, `duplicated`, `quarantined`, `internacional_nao_habilitado`, `variable_too_long`, `incomplete`) e listados, até 100, em `rejected_samples` com a posição na sua lista. Quando nenhum contato é válido a resposta é `422 no_valid_contacts`. Erros de formato são `422 invalid_request_error` com `fields`. O plano do cliente decide se públicos importados são permitidos (`403 plan_not_allowed` no plano gratuito).

Contatos que pediram para sair (opt-out) nunca recebem mensagens: as mesmas listas de bloqueio usadas pelo aplicativo valem para as campanhas da API.

`GET /audiences/{id}` (token com `campaigns:read`) devolve o mesmo formato do `data` acima, ou `404 not_found`.

## Campanhas

Campanhas são a única forma de enviar pela API: não existe endpoint para enviar uma mensagem a uma pessoa. Uma campanha é um modelo aprovado mais um público, e segue as mesmas regras de uma campanha criada no aplicativo da Resgata pelo usuário que conectou você: o plano, as regras de horário, o limite de gastos, os limites da Meta para a conta do WhatsApp Business dele e as listas de opt-out. Os custos do WhatsApp são cobrados pela Meta na forma de pagamento da conta do próprio cliente.

`POST /campaigns` (token com `campaigns:write`):

```json
{ "name": "Black Friday", "template_id": "tpl_123", "audience_id": "aud_…",
  "variables": { "1": "@nome", "2": "10% de desconto" },
  "schedule_at": "2026-11-27T09:00:00-03:00" }
```

| Campo | |
|---|---|
| `template_id` | Obrigatório. Um modelo `approved` ou `reinstated` da conta vinculada |
| `audience_id` | Obrigatório |
| `variables` | Um valor para cada `{{n}}` do corpo do modelo. `@nome` é o nome do contato, `@variavel_N` é o `variables[N-1]` do contato, e qualquer outra coisa é enviada como está para todos |
| `name` | Opcional |
| `schedule_at` | Opcional. Data ISO 8601 no futuro. Exige um plano que permita agendamento (`403 schedule_not_allowed`) |
| `send_until` | Opcional. Depois desse momento a campanha para de enviar |
| `daily_send_limit` ou `contacts_limit` | Opcionais, números inteiros. Enviar essa quantidade por dia, ou pausar depois dessa quantidade de contatos (não os dois) |

Respostas: `201` com a campanha, ou `200` com `"duplicated": true` quando uma campanha idêntica foi criada nos últimos 10 minutos (a existente é devolvida e nada novo é enviado). Erros: `409 meta_not_ready`, `409 no_slot_available`, `409 template_not_approved`, `409 waba_restricted` (a Meta restringiu a conta do cliente), `422 template_blocked` (modelo suspenso pela equipe Resgata Fácil), `422 invalid_request_error` (variáveis ou dados de contato faltando, com mensagem), `422 spend_limit_reached`, `422 empty_audience`, `403 plan_not_allowed`, `403 schedule_not_allowed`, `404 not_found` (modelo ou público) e `502 meta_unavailable`.

```json
{ "data": { "id": "cmp_123", "object": "campaign", "name": "Black Friday",
  "status": "running", "pause_reason": null,
  "template_id": "tpl_123", "audience_id": "aud_…",
  "scheduled_at": null, "send_until": null,
  "progress": { "total": 2, "sent": 1, "delivered": 1, "read": 0, "failed": 0, "skipped": 0, "pending": 1, "percent": 50 },
  "created_at": 1767225600, "finished_at": null } }
```

Status: `scheduled`, `pending` (aguardando a Meta ou o modelo), `running`, `paused`, `finished`, `failed` (nada pôde ser enviado) e `canceled`. `pause_reason` é `MANUAL_USER` depois de uma pausa sua, ou o motivo pelo qual a Resgata pausou (limite diário atingido, qualidade na Meta, limite de gastos…). `scheduled_at` e `send_until` voltam em ISO 8601.

- `GET /campaigns` lista as campanhas que esta conexão criou, da mais nova para a mais antiga (filtro `name`, `limit` de 1 a 100, `meta.next_cursor`). As campanhas que o cliente fez no aplicativo da Resgata não aparecem.
- `GET /campaigns/{id}` devolve uma campanha com o andamento.
- `POST /campaigns/{id}/pause` e `/resume`. Pausar uma campanha já encerrada responde `409 campaign_not_active`. Retomar responde `409 campaign_not_paused`, `422 daily_limit_reached` (o próximo lote sai sozinho), `422 campaign_limit_reached`, `422 campaign_expired`, `422 empty_audience`, `409 channel_not_ready`, `409 waba_restricted` ou `422 spend_limit_reached` quando não consegue continuar.

### Eventos de campanha

`campaign.started` (as primeiras mensagens entraram na fila), `campaign.paused` (por você, pelo cliente ou pela Resgata), `campaign.finished` e `campaign.failed` trazem `data.campaign` com o mesmo formato acima. `message.sent|delivered|read|failed`, `message.received` e `contact.opted_out` de uma campanha que você criou vão só para a sua conexão, com `campaign_id` preenchido com o id `cmp_…`.

## Webhooks

Configure os endpoints no portal do desenvolvedor: URL, os eventos desejados e o segredo de assinatura (`whsec_…`, gerado ou o seu próprio). Endpoints de produção precisam ser `https` e resolver para um endereço público.

Envelope:

```json
{ "id": "evt_…", "object": "event", "type": "message.received", "api_version": "2026-11-01",
  "created": 1767225600, "environment": "live",
  "connection": { "id": "conn_…", "external_ref": "your-user-id" },
  "data": { … } }
```

Cabeçalhos: `X-Resgata-Signature: t=<unix>,v1=<hex>`, `X-Resgata-Event-Id`, `X-Resgata-Delivery-Id`, `X-Resgata-Delivery-Attempt`.

Verificação: calcule `HMAC_SHA256(secret, t + "." + rawBody)` em hexadecimal, compare em tempo constante com cada `v1` do cabeçalho e rejeite quando `t` tiver mais de 300 segundos.

```js
const crypto = require('crypto');
function verify(rawBody, header, secret) {
  const parts = Object.fromEntries(header.split(',').map((p) => p.split('=')));
  if (Math.abs(Date.now() / 1000 - Number(parts.t)) > 300) return false;
  const expected = crypto.createHmac('sha256', secret).update(`${parts.t}.${rawBody}`).digest('hex');
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(parts.v1));
}
```

A entrega é **pelo menos uma vez** e sem ordem garantida. Deduplique por `X-Resgata-Event-Id`. Responda `2xx` em até 10 segundos. As falhas são reenviadas após 15s, 1min, 5min, 15min, 1h, 12h e 24h (8 tentativas no total). Um endpoint com 10 entregas abandonadas consecutivas é desativado; reative-o no portal. No portal você pode enviar eventos de teste e reenviar qualquer entrega, e ele guarda 30 dias de logs de requisições e de webhooks.

Eventos: `campaign.started`, `campaign.paused`, `campaign.finished`, `campaign.failed`, `template.approved`, `template.rejected`, `template.status_changed`, `connection.created`, `connection.revoked`, `number.connected`, `number.disconnected`, `message.received`, `message.sent`, `message.delivered`, `message.read`, `message.failed`, `contact.opted_out`, `ping`.

Cada endpoint recebe apenas os eventos nos quais está inscrito. Endpoints criados antes de um tipo de evento existir não o recebem automaticamente: abra o endpoint no portal (Webhooks, Editar) e marque os novos eventos, ou use "Selecionar todos". Sem os eventos `campaign.*`, o seu aplicativo nunca descobre que uma campanha terminou.

`message.*` e `contact.opted_out` cobrem envios e respostas de campanhas feitas por números da Meta oficial. Nas campanhas criadas por esta API eles trazem o id `cmp_…` e chegam só à conexão que criou a campanha; nas campanhas que o cliente cria no aplicativo da Resgata, chegam a todos os aplicativos conectados.

## Sandbox

O sandbox é uma cópia completa da API que roda sobre serviços simulados. Não precisa de número de WhatsApp, de conta da Meta, de cliente nem de nada nosso: cadastre-se, crie uma chave de teste no portal e construa toda a sua integração. Nada que uma credencial de teste faça chega a um número real, e credenciais live não podem usar as rotas do sandbox (`403 sandbox_only`). Os payloads e os eventos de webhook têm exatamente o formato dos reais.

Os dados do sandbox são separados por ambiente (eventos, endpoints, chaves e logs nunca se misturam). Configure os endpoints de webhook do sandbox no portal como os live; os eventos do sandbox vão apenas para endpoints criados para o ambiente sandbox.

### Primeiros passos

1. Crie uma conexão de sandbox com a sua chave `rf_test_app_…`: `POST /connections {"external_ref": "your-user-id"}`. Ela já vem com um número simulado conectado.
2. Emita um token de conexão: `POST /connections/{id}/tokens`.
3. Ou pule os passos 1 e 2 e teste o próprio fluxo de OAuth (abaixo).

### Endpoints do sandbox

Todos exigem uma credencial `rf_test_*`. "Token" significa um token de conexão do sandbox (os ajudantes não exigem nenhuma permissão).

| Método e caminho | Credencial | O que faz |
|---|---|---|
| `POST /connections` | chave do aplicativo | Cria uma conexão de sandbox (e o número padrão dela) |
| `GET /oauth/sandbox/authorize` | nenhuma | Redirecionamento de consentimento sem cliente (veja abaixo) |
| `GET /numbers` | token | Lista os números do sandbox com o `status` |
| `POST /sandbox/numbers` | token | `{ "name"?, "phone"?, "status"? }` cria um número (padrão `connected`). `409 phone_in_use` se o telefone já existir |
| `PATCH /sandbox/numbers/{id}` | token | `{ "status" }` muda o estado e dispara o evento `number.*` correspondente |
| `DELETE /sandbox/numbers/{id}` | token | Remove um número (dispara `number.disconnected` se estava conectado) |
| `POST /templates`, `GET /templates`, … | token | Mesmo contrato do live, veja Modelos. Nada é enviado à Meta |
| `POST /audiences`, `POST /campaigns`, `GET /campaigns`, … | token | Mesmo contrato do live, veja Públicos e Campanhas. Uma campanha envia uma mensagem simulada por contato, pelos cenários abaixo, 50 contatos por segundo, e dispara `campaign.*` e `message.*` |
| `POST /templates/media` | token | Devolve um `media_id` falso (`sbx_media_…`) para você montar modelos com cabeçalho e carrossel |
| `POST /sandbox/templates/{id}/status` | token | `{ "status", "reason"? }` move um modelo para qualquer status e dispara o evento `template.*` correspondente |
| `POST /sandbox/inbound-messages` | token | `{ "from", "text" }` simula a resposta de um cliente (`message.received`); responde `201` com a mensagem |
| `POST /sandbox/opt-outs` | token | `{ "phone" }` simula um opt-out (`contact.opted_out`); responde `201` com `{ "object": "opt_out", "phone" }` |
| `POST /sandbox/events` | token | Dispara qualquer evento documentado nos seus endpoints de sandbox (abaixo) |
| `POST /sandbox/meta-link` | token | `{ "status": "linked\|pending\|failed" }` define o estado simulado do vínculo com a Meta (veja Testando o fluxo de OAuth) |
| `POST /sandbox/reset` | token ou chave do aplicativo | Apaga os dados do sandbox (abaixo) |

### Estados dos números

`connected`, `disconnected`, `restricted`, `banned`, `qr_pending`. `connected` é `true` somente nesse estado. `GET /connection/setup` acompanha os números e simula as verificações da Meta: com um número `connected` todas as etapas ficam `complete`; `restricted` deixa `messaging_restriction` como `blocked`; `banned` deixa `meta_linked` e `phone_status` como `blocked`; `disconnected` e `qr_pending` deixam `phone_status` como `incomplete`; sem números, `meta_linked` fica `incomplete`. Os outros estados só importam enquanto nenhum número estiver `connected`: um número conectado basta para o `ready`.

Mudar o estado com `PATCH /sandbox/numbers/{id}` dispara `number.connected` (estado `connected`) ou `number.disconnected` (qualquer outro estado), com `data.number` = `{ id, name, phone, status }` e `data.source` = `sandbox_<status>`. Repetir o mesmo status não dispara nada.

### Resultados das mensagens (telefones mágicos)

Inclua esses números em um público (com qualquer formatação; o estilo `+55 00 0000-0000` funciona) para obter resultados determinísticos para a mensagem deles em uma campanha. Qualquer outro número segue `queued → sent (0s) → delivered (2s) → read (5s)`.

| Telefone | Resultado |
|---|---|
| `5500000000000` | `message.failed`, código `131026` (não entregável) |
| `5500000000001` | `message.failed`, código `131047` (mensagem de reengajamento) |
| `5500000000002` | `message.failed`, código `131048` (limite de taxa de spam atingido) |
| `5500000000003` | `message.failed`, código `131049` (não entregue para manter o ecossistema saudável) |
| `5500000000004` | `message.failed`, código `131056` (limite de taxa do par atingido) |
| `5500000000010` | `sent`, nunca entregue |
| `5500000000011` | `sent`, `delivered`, nunca lida |
| `5500000000012` | fica em `queued`, nunca enviada |
| `5500000000020` | lenta: `sent` após 30s, `delivered` após 60s, `read` após 120s |
| `5500000000030` | vai para `read` e, 7s depois, `contact.opted_out` |
| `5500000000031` | vai para `read` e, 8s depois, uma resposta `message.received` ("Recebi sua mensagem!") |

### Resultados dos modelos (nomes mágicos)

Um modelo do sandbox fica `pending` e depois `approved`, cerca de 5 segundos depois (`template.approved`). O nome decide os outros resultados:

| O nome contém | Resultado |
|---|---|
| `_reject` | `rejected` após cerca de 3 segundos, com `rejection_reason: INVALID_FORMAT` (`template.rejected`) |
| `_pause` | `approved` e, 5 segundos depois, `paused` (`template.approved` e depois `template.status_changed`) |
| `_pending` | fica `pending` para sempre |
| `_slow` | `approved` após 60 segundos |

Os modelos do sandbox passam pela mesma validação dos live (limites, botões, regras de carrossel), então um modelo inválido falha do mesmo jeito nos dois. `template_available` em `GET /connection/setup` fica `complete` assim que a conexão tem um modelo aprovado no sandbox.

### Disparando eventos

`POST /sandbox/events` com `{ "type": "message.delivered" }` (qualquer tipo da lista de Eventos) monta um payload realista e o entrega nos seus endpoints de sandbox inscritos, para você testar os handlers sem esperar. Campos opcionais: `phone`, `text` (eventos de mensagem), `number_id` (eventos de número e de opt-out) e `data`, que é mesclado sobre o `data` gerado (por exemplo `{"data": {"message": {"from_name": "Maria"}}}`). Eventos de mensagem deixam uma mensagem de sandbox armazenada. `connection.revoked` e os eventos de número só enviam o evento, nunca alteram a conexão nem o número. A resposta é `202` com o envelope do evento e `meta.deliveries` (para quantos endpoints foi enfileirado). Um `type` desconhecido responde `422` com a lista `allowed`.

### Reset

`POST /sandbox/reset` com um token de conexão apaga as mensagens, os números e os eventos dessa conexão e recria o número conectado padrão (os tokens e a conexão permanecem). Com a chave do aplicativo, faz isso para todas as conexões de sandbox do aplicativo e também apaga os eventos de sandbox que não pertencem a nenhuma conexão; acrescente `{"delete_connections": true}` para apagar também as conexões de sandbox e seus tokens. Dados live nunca são tocados.

### Testando o fluxo de OAuth

Em vez de mandar o navegador para `/autorizar` (live, exige um cliente Resgata de verdade), mande para:

`GET https://app.resgatafacil.com/api/platform/v1/oauth/sandbox/authorize?client_id=…&redirect_uri=…&state=…&code_challenge=…&code_challenge_method=S256&scope=numbers:read%20campaigns:read&external_ref=…`

Ele recebe os mesmos parâmetros do fluxo live (a URI de redirecionamento precisa estar cadastrada no seu aplicativo) e redireciona imediatamente, como se um cliente tivesse aprovado, para `redirect_uri?code=…&state=…`. Troque o código em `POST /oauth/token` exatamente como no live: você recebe um token `rf_test_conn_…`, um evento `connection.created` e uma conexão de sandbox com um número padrão. Reusar um `external_ref` reaproveita a conexão dele; omiti-lo gera um. Acrescente `simulate=access_denied` ou `simulate=external_ref_in_use` para receber esses redirecionamentos de erro. Requisições inválidas (cliente desconhecido, redirecionamento não cadastrado, PKCE ausente) respondem JSON `422` e nunca redirecionam.

Acrescente `meta=linked|pending|failed` para simular a etapa da Meta (padrão `linked`: a conexão já vem com a conta da Meta pronta, como antes). Com `pending` ou `failed`, `GET /connection/setup` mostra `meta_linked` e tudo o que depende dele como `incomplete` (`ready: false`) até você concluir a etapa da Meta:

- `POST /connection/meta-link` com um token de sandbox devolve uma URL de sandbox assinada (`…/api/platform/v1/oauth/sandbox/meta-link?…`) que redireciona para o seu `return_url` com o mesmo contrato `?status=linked&connection=conn_…` do live e marca a conexão como vinculada. Acrescente `"simulate": "failed"` à requisição para receber `status=failed`.
- `POST /sandbox/meta-link` com um token de sandbox e `{ "status": "linked|pending|failed" }` define o estado diretamente, sem navegador.

### Limites e retenção

Por aplicativo: 20 conexões de sandbox e 5000 eventos de sandbox. Por conexão: 5 números, 500 mensagens, 50 modelos, 20 públicos (200 contatos cada) e 20 campanhas. Acima disso a API responde `422 sandbox_limit_reached`; `POST /sandbox/reset` libera tudo. Mensagens e eventos do sandbox são apagados após 7 dias, e conexões de sandbox sem uso por 90 dias são apagadas junto com os dados.

### Passo a passo com curl

```sh
BASE=https://app.resgatafacil.com/api/platform/v1
KEY=rf_test_app_…

CONN=$(curl -s -X POST $BASE/connections -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"external_ref":"user-1"}' | jq -r .data.id)
TOKEN=$(curl -s -X POST $BASE/connections/$CONN/tokens -H "Authorization: Bearer $KEY" | jq -r .data.secret)
AUTH="Authorization: Bearer $TOKEN"

curl -s $BASE/numbers -H "$AUTH"
curl -s -X POST $BASE/audiences -H "$AUTH" -H 'Content-Type: application/json' -d '{"name":"Teste","contacts":[{"phone":"5500000000003","name":"Maria"}]}'
curl -s -X POST $BASE/sandbox/numbers -H "$AUTH" -H 'Content-Type: application/json' -d '{"name":"Loja 2","status":"qr_pending"}'
curl -s -X PATCH $BASE/sandbox/numbers/2 -H "$AUTH" -H 'Content-Type: application/json' -d '{"status":"banned"}'
curl -s -X POST $BASE/sandbox/events -H "$AUTH" -H 'Content-Type: application/json' -d '{"type":"message.received","text":"Quero agendar"}'
curl -s -X POST $BASE/sandbox/reset -H "$AUTH"
```

## Removido: mensagens individuais

`POST /messages` e `GET /messages/{id}` não existem mais (só funcionavam no sandbox). As mensagens são enviadas somente criando uma campanha com um modelo aprovado e um público. As permissões `messages:send` e `messages:read` foram aposentadas: pedidos de autorização que ainda as listam são aceitos e elas são ignoradas. Os eventos de mensagem (`message.sent|delivered|read|failed|received`) continuam iguais.
