# Funil Demo: CRM de exemplo para a API da Plataforma Resgata Fácil

Um CRM pequeno e legível, escrito em Go, que mostra para parceiros como integrar com a
**API da Plataforma Resgata Fácil**: OAuth (código de autorização com PKCE), a API REST e
webhooks assinados. Ele também expõe uma **API de leads no estilo CRMSOLUT**, para o fluxo
funcionar de ponta a ponta: o Resgata lê os leads deste CRM, e este CRM envia campanhas pelo
Resgata e reage aos webhooks que voltam. A API da Plataforma não tem envio individual: as
mensagens saem apenas por campanhas (um modelo aprovado mais um público).

A referência completa da API está em [docs/platform-api.md](docs/platform-api.md).

O CRM em si é fictício ("Funil Demo"). Tudo o que diz respeito à integração é real e feito
para ser copiado.

## O que você encontra

- **Funil, leads e linha do tempo**: fluxos com etapas ordenadas, leads com estágio, marca de
  opt-out e uma linha do tempo de notas, mensagens e respostas.
- **OAuth com PKCE** para conectar um usuário ao Resgata Fácil, com mensagens de erro claras,
  e o **vínculo com a Meta** que o cliente também faz (`POST /connection/meta-link`).
- **Checklist de prontidão** (`GET /connection/setup`): as doze etapas que decidem se um
  cliente pode enviar (Meta vinculada, empresa verificada, forma de pagamento, modelo
  aprovado…), cada uma `complete`, `incomplete`, `blocked` ou `unknown`, com a ação para
  resolver.
- **Modelos** (`GET/POST /templates`): listar, criar e acompanhar a aprovação da Meta, com os
  eventos `template.*`.
- **Etapa "Enviar campanha" do fluxo**: escolha um modelo aprovado, escolha os leads de uma
  etapa do funil, mapeie cada `{{n}}` (nome do lead, um campo do lead ou texto fixo) e o CRM
  chama `POST /audiences` e depois `POST /campaigns`. O andamento, a pausa e a retomada
  acompanham os webhooks `campaign.*` e `message.*` ao vivo.
- **Receptor de webhooks** com verificação de assinatura, janela contra repetição,
  deduplicação e uma página de eventos (entregas válidas e rejeitadas, JSON expansível,
  filtro por tipo).
- **Ferramentas de sandbox**: dispare qualquer evento documentado, simule a resposta de um
  cliente ou um opt-out, crie números e mude o estado deles, zere o sandbox. Coloque os
  telefones mágicos do sandbox em uma campanha para ver cada resultado (entregue, lida,
  falha, opt-out, resposta).
- **API no estilo CRMSOLUT** (`/api/v1/unidades`, `/fluxos`, `/leads`) com chaves bearer por
  unidade, paginação por cursor e limite de requisições, exatamente no formato que a
  integração CRMSOLUT do Resgata consome.
- Uma **simulação da plataforma Resgata** (`cmd/mockresgata`) para testar tudo offline.

## Primeiros passos

Requisitos: Go 1.26 ou mais novo. Sem cgo e sem servidor de banco de dados (o SQLite é
embutido).

### 1. Contra o sandbox real do Resgata

1. No portal do desenvolvedor, crie um aplicativo, copie o `client_id` (`app_…`) e cadastre
   a URI de redirecionamento `http://localhost:8080/resgata/callback`.
2. Crie um endpoint de webhook do **sandbox** apontando para a sua URL pública mais
   `/webhooks/resgata` (veja [Desenvolvimento local com túnel](#desenvolvimento-local-com-túnel))
   e copie o segredo de assinatura (`whsec_…`).
3. Configure e rode:

   ```sh
   cp .env.example .env
   openssl rand -base64 32
   ```

   Cole o valor gerado em `APP_ENCRYPTION_KEY` e preencha `RESGATA_CLIENT_ID` e
   `RESGATA_WEBHOOK_SECRET`. Opcionalmente, defina `RESGATA_APP_KEY` (uma chave
   `rf_test_app_…`) para habilitar o atalho "criar conexão com a chave do aplicativo".

   ```sh
   make run
   ```

4. Abra <http://localhost:8080>. Se faltar algo obrigatório, uma tela de configuração lista
   exatamente quais variáveis definir. Siga "Primeiros passos" na página inicial: a lista se
   marca sozinha a partir do estado real.

### 2. Offline, com a plataforma simulada

```sh
make mock
```

Em outro terminal, com o mesmo segredo nos dois processos:

```sh
export RESGATA_API_URL=http://localhost:9090 RESGATA_APP_URL=http://localhost:9090
export RESGATA_CLIENT_ID=app_mock RESGATA_WEBHOOK_SECRET=<qualquer valor> APP_ENCRYPTION_KEY=$(openssl rand -base64 32)
make run
```

A simulação envia webhooks assinados para `http://localhost:8080/webhooks/resgata` (troque
com `MOCK_WEBHOOK_URL`). Ela implementa os mesmos endpoints e formatos do sandbox: o
checklist de prontidão, o vínculo com a Meta, modelos (aprovados em segundos; um nome com
`_reject` é recusado e um com `_pending` fica em análise), públicos, campanhas com andamento,
pausa e retomada, eventos `campaign.*`, `template.*` e `message.*` com ids `cmp_…`, e os
telefones mágicos do sandbox. `POST /messages` responde `404`, como a API real.

### 3. Docker

```sh
cp .env.example .env
docker compose up --build
docker compose --profile offline up --build
```

O segundo comando também sobe a plataforma simulada. O arquivo compose guarda os bancos no
volume `funil-data`, montado em `/data`; a imagem define `DATA_DIR=/data`, então o modo
portal também guarda ali o registro e os workspaces. Mantenha esse volume em armazenamento
persistente se quiser que os workspaces sobrevivam a um novo deploy.

## Configuração

Tudo vem de variáveis de ambiente (um arquivo `.env` no diretório de trabalho é carregado
por conveniência e nunca sobrescreve variáveis de ambiente reais). **Nenhuma chave, token ou
segredo fica no código nem no repositório.**

| Variável | Obrigatória | Padrão | O que é |
|---|---|---|---|
| `APP_ENCRYPTION_KEY` | sim | | 32 bytes aleatórios em base64. Criptografa o token de conexão do Resgata em repouso (AES-GCM). `openssl rand -base64 32` |
| `RESGATA_CLIENT_ID` | sim | | Id do aplicativo (`app_…`) no portal do desenvolvedor |
| `RESGATA_WEBHOOK_SECRET` | sim | | Segredo de assinatura do webhook (`whsec_…`) no portal |
| `RESGATA_ENVIRONMENT` | não | `sandbox` | `sandbox` ou `live`. O sandbox usa `/oauth/sandbox/authorize` e mostra as ferramentas de sandbox; o live usa `/autorizar` |
| `RESGATA_APP_KEY` | não | | Chave `rf_test_app_…`, só para o atalho do sandbox que cria uma conexão sem OAuth |
| `RESGATA_API_URL` | não | `https://app.resgatafacil.com/api/platform/v1` | URL base da API da Plataforma |
| `RESGATA_APP_URL` | não | `https://app.resgatafacil.com` | Onde fica o `/autorizar` |
| `PUBLIC_URL` | não | `http://localhost` + `APP_ADDR` | Endereço público deste app. Monta a URI de redirecionamento do OAuth (`PUBLIC_URL/resgata/callback`) e a URL do webhook mostrada na interface |
| `APP_ADDR` | não | `:8080` | Endereço de escuta |
| `DATABASE_PATH` | não | `data/funil.db` | Arquivo SQLite |
| `ADMIN_PASSWORD` | não | | Se definida, a interface administrativa pede autenticação HTTP basic (qualquer usuário, esta senha). **Se não for definida, a interface fica aberta para quem conseguir alcançá-la**: aceitável no localhost, não na internet |
| `CRM_API_RATE_LIMIT` | não | `60` | Requisições por minuto por chave da API do CRM |
| `DATA_DIR` | não | pasta de `DATABASE_PATH` | Onde o modo portal guarda `registry.db` e `workspaces/<id>.db` |
| `PORTAL_SSO_SECRET` | não | | Liga o [acesso pelo portal](#restringindo-o-demo-ao-portal-de-desenvolvedores-do-resgata) e substitui `ADMIN_PASSWORD`. Pelo menos 16 caracteres; `openssl rand -hex 32` |
| `PORTAL_URL` | não | `https://developers.resgatafacil.com` | Para onde a tela de entrada e o "Sair" levam as pessoas |
| `WORKSPACE_TTL_HOURS` | não | `24` | Horas sem atividade antes de um workspace ser apagado |
| `MAX_WORKSPACES` | não | `50` | Workspaces que podem existir ao mesmo tempo; acima disso, novos desenvolvedores veem uma página de "demo lotado" |
| `SESSION_TTL_HOURS` | não | `8` | Duração do cookie de sessão |
| `WORKSPACE_PURGE_INTERVAL_MINUTES` | não | `15` | Com que frequência se procuram workspaces ociosos |

Os dados de demonstração (unidades, fluxos, 15 leads, incluindo os números mágicos do
sandbox) são criados na primeira vez que o banco está vazio. Use `--no-seed` para começar
vazio.

## Restringindo o demo ao portal de desenvolvedores do Resgata

Por padrão o demo é um único workspace, aberto no localhost ou protegido por
`ADMIN_PASSWORD`. Para hospedá-lo publicamente para quem tem acesso ao portal de
desenvolvedores do Resgata, defina `PORTAL_SSO_SECRET`. O modo portal muda três coisas:

- **Entrada só pelo portal.** O portal envia o desenvolvedor autenticado para
  `GET /portal-login?token=<token>`. Sem uma sessão válida, toda página administrativa
  mostra "Entre pelo portal de desenvolvedores" com um botão para `PORTAL_URL`.
  `/webhooks/resgata`, `/api/v1/*`, os arquivos estáticos e `/healthz` continuam acessíveis,
  porque trazem a própria autenticação.
- **Um workspace por desenvolvedor.** O `sub` do token vira um id estável de workspace (os
  16 primeiros caracteres hexadecimais de `sha256("funil-demo/workspace/v1:" + sub)`). Cada
  workspace é um arquivo SQLite próprio, com dados de demonstração próprios, chaves da API do
  CRM próprias (no formato `crm_<id do workspace>_<aleatório>`), conexão criptografada com o
  Resgata e log de eventos. O id do workspace também é o `external_ref` da conexão dele com o
  Resgata, e é assim que os webhooks encontram o caminho de volta: o único segredo de webhook
  do aplicativo verifica a assinatura, e então o evento vai para o workspace indicado em
  `connection.external_ref`. Eventos de um workspace desconhecido são respondidos com `200` e
  descartados, para o Resgata não ficar tentando de novo.
- **Limpeza.** Um workspace ocioso por `WORKSPACE_TTL_HOURS` é apagado: o app primeiro chama
  `DELETE /connection` com o token do workspace (falhas são registradas no log e a limpeza
  continua) e depois remove o banco e a linha do registro. O "Reiniciar" no cabeçalho faz o
  mesmo com o workspace do próprio desenvolvedor e popula os dados de novo. `MAX_WORKSPACES`
  protege o servidor e as cotas de sandbox do seu aplicativo no Resgata, que são contadas por
  aplicativo.

### O token de login

O portal monta o token; este app só o verifica.

```
token     = base64url(payload) + "." + base64url(HMAC_SHA256(secret, base64url(payload)))
payload   = {"sub": "<id estável do desenvolvedor>", "name": "<nome de exibição>",
             "iat": <unix>, "exp": <iat + 60>, "jti": "<id único>"}
```

As duas partes usam base64url puro (sem o padding `=`) e o HMAC cobre a primeira parte
exatamente como ela é enviada. A chave é `PORTAL_SSO_SECRET`. O app aceita um token uma vez
(o `jti` é lembrado até expirar), rejeita `exp` no passado (com tolerância de 5 segundos de
diferença de relógio), tokens emitidos no futuro e validades acima de 5 minutos, e compara
assinaturas em tempo constante.

Em Go, usando o pacote deste repositório:

```go
token, err := portal.Sign(secret, portal.Claims{
	Subject: "dev-123", Name: "Maria Souza",
	IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), ID: randomID,
})
loginURL := "https://demo.example.com/portal-login?token=" + url.QueryEscape(token)
```

Em qualquer outra linguagem:

```
payload = json({sub, name, iat, exp, jti})
part1   = base64url_nopad(payload)
part2   = base64url_nopad(hmac_sha256(key = secret, message = part1))
token   = part1 + "." + part2
```

Para trabalhar localmente, o `cmd/mktoken` assina um token a partir de `PORTAL_SSO_SECRET`:

```sh
go run ./cmd/mktoken -sub dev-1 -name "Maria Souza" -url http://localhost:8080
```

Ele imprime um link de login pronto para abrir (omita `-url` para imprimir só o token). Cada
link funciona uma vez, então rode o comando de novo a cada entrada.

### Notas de segurança do modo portal

- O cookie de sessão é `workspaceId.validade.assinatura`, assinado com uma chave derivada de
  `APP_ENCRYPTION_KEY` (HKDF-SHA256 com um rótulo de contexto fixo), `HttpOnly`,
  `SameSite=Lax` e `Secure` quando `PUBLIC_URL` é `https`. Ele não carrega dados pessoais.
- Um cookie cujo workspace foi apagado deixa de funcionar; o dono cai na tela de entrada.
- O token viaja em uma URL, então vive por um minuto, funciona uma vez e a resposta tem
  `Cache-Control: no-store`. Sirva o app em https em produção.
- Mantenha `PORTAL_SSO_SECRET` fora do repositório e fora de código de cliente. Trocá-lo
  derruba o fluxo do link de login para todos, mas mantém as sessões existentes até
  expirarem.
- O demo roda somente no sandbox do Resgata. Não aponte o modo portal para credenciais live.

## O fluxo, passo a passo

```
 ┌────────────┐  1. autorizar (PKCE)   ┌───────────────────────┐
 │  Funil Demo│ ─────────────────────► │ Resgata Fácil          │
 │  (este app)│ ◄───────────────────── │ /autorizar ou sandbox  │
 │            │  2. code + state       │                       │
 │            │  3. POST /oauth/token  │                       │
 │            │ ─────────────────────► │  API da Plataforma     │
 │            │ ◄───────────────────── │  /api/platform/v1      │
 │            │  token (guardado       │                       │
 │            │  criptografado)        │                       │
 │            │  4. GET /connection/setup, /templates           │
 │            │  5. POST /audiences, POST /campaigns ─────────► │
 │            │                        │                       │
 │            │  6. webhooks assinados │   WhatsApp / sandbox   │
 │            │ ◄───────────────────── │   message.sent ...     │
 └─────┬──────┘  POST /webhooks/resgata└───────────┬───────────┘
       │                                           │
       │  7. GET /api/v1/leads (Bearer crm_…)      │
       └◄──────────────────────────────────────────┘
          O Resgata lê este CRM como lê o CRMSOLUT
```

1. **Conectar.** `GET /resgata/conectar` cria um `state` aleatório e um `code_verifier` PKCE
   (guardado no servidor) e redireciona para `/autorizar` (live) ou
   `/oauth/sandbox/authorize` (sandbox) com `client_id`, `redirect_uri`, `state`,
   `code_challenge` (S256), `scope` e `external_ref`. O verifier nunca sai do servidor.
   As permissões são `numbers:read templates:read templates:write audiences:write
   campaigns:read campaigns:write`; não existe permissão de mensagem.
2. **Retorno.** `GET /resgata/callback` confere o `state` de uso único, trata
   `error=access_denied|external_ref_in_use` e troca o código em `POST /oauth/token` com o
   verifier. O token de conexão recebido é criptografado com AES-GCM antes de ser guardado.
3. **Meta e prontidão.** Além do Resgata, o cliente vincula a conta do WhatsApp Business pela
   Meta. `GET /resgata/meta` chama `POST /connection/meta-link` com um `return_url` e
   redireciona para o link recebido; o cliente volta para `/integracao`.
   `GET /connection/setup` devolve cada etapa com `status`, `detail` e `action_url`; a
   interface as desenha e oferece o vínculo com a Meta enquanto `meta_linked` não estiver
   completo.
4. **Modelos.** `GET /templates` e `POST /templates` (`name`, `category`, `language`,
   `body.text` com uma entrada em `body.examples` por variável, `footer` opcional). Só
   modelos `approved` podem ser usados; quem decide é a Meta, então o status muda sozinho e
   `template.approved|rejected|status_changed` chegam por webhook.
5. **Enviar uma campanha.** Não existe envio individual. A etapa "Enviar campanha" monta o
   público a partir dos leads do CRM (`POST /audiences`, `contacts[].phone`, `name`,
   `variables`) e cria a campanha (`POST /campaigns` com `template_id`, `audience_id` e
   `variables` como `{"1": "@nome", "2": "@variavel_1"}`). Leads com opt-out nunca entram no
   público. `POST /campaigns/{id}/pause` e `/resume` param e retomam a campanha. Os erros da
   API (`meta_not_ready`, `no_slot_available`, `template_not_approved`, `waba_restricted`,
   `spend_limit_reached`…) viram explicações em linguagem simples.
6. **Receber.** O Resgata chama `POST /webhooks/resgata`. O handler:
   - lê o **corpo cru** e verifica o `X-Resgata-Signature` (`t=<unix>,v1=<hex>`):
     `HMAC_SHA256(secret, t + "." + rawBody)`, comparação em tempo constante com **todos** os
     `v1`, rejeitado quando `|agora - t| > 300s`;
   - guarda o evento (os rejeitados também, para a página de eventos);
   - **deduplica** por `X-Resgata-Event-Id` (a entrega é pelo menos uma vez e sem ordem);
   - aplica o evento: os status de `message.*` só avançam (um `sent` atrasado nunca
     sobrescreve `read`; `failed` é final) e, quando `message.campaign_id` é de uma campanha
     deste CRM, caem no lead correspondente e atualizam os contadores da campanha; os
     eventos `campaign.*` atualizam status e andamento (um `campaign.started` atrasado nunca
     reabre uma campanha encerrada); `message.received` cai no lead com o mesmo telefone (ou
     cria um lead na primeira etapa do primeiro fluxo), `contact.opted_out` marca `opt_out` e
     `connection.revoked` apaga o token;
   - responde `2xx` rápido. Se o processamento falhar, responde `500` e o evento **não** é
     marcado como processado, então a nova tentativa é aplicada em vez de descartada como
     duplicada.
7. **Deixar o Resgata ler seus leads.** Crie uma chave em **Chaves da API** e entregue ao
   Resgata a URL base mais a chave (veja abaixo).

## A API no estilo CRMSOLUT

Base `/api/v1`, `Authorization: Bearer crm_<aleatório>`. As chaves pertencem a uma unidade e
só enxergam os leads dela. São guardadas com hash (SHA-256) e exibidas uma única vez.

```sh
BASE=http://localhost:8080/api/v1
KEY=crm_...

curl -H "Authorization: Bearer $KEY" $BASE/unidades
curl -H "Authorization: Bearer $KEY" $BASE/fluxos
curl -H "Authorization: Bearer $KEY" "$BASE/leads?estagio=lead&limite=2"
curl -H "Authorization: Bearer $KEY" "$BASE/leads?limite=2&cursor=<proximo_cursor>"
```

| Rota | Observações |
|---|---|
| `GET /unidades` | A unidade da chave: `id`, `nome`, `cidade`, `estado`, `ativa` |
| `GET /fluxos` | Fluxos com `etapas` ordenadas (`id`, `nome`, `ordem`, `inicial`) |
| `GET /leads` | Filtros `organizacao_id`, `fluxo_id`, `etapa_id`, `estagio` (`lead`, `avaliado`, `cliente`), `limite` (1 a 500, padrão 50), `cursor` |

Envelope: `{ "dados": [...], "paginacao": { "quantidade", "tem_mais", "proximo_cursor" } }`.
Um lead tem `id`, `nome`, `celular` (E.164), `email`, `estagio`, `opt_out`, `unidade`,
`fluxo`, `etapa` (cada um `{id, nome}`), `criado_em` e `alterado_em` (ISO 8601). `opt_out`
vira `true` quando o Resgata informa `contact.opted_out`.

Erros: `401` chave inválida, `403` unidade suspensa ou id de outra unidade, `422`
`{ "erro": { "campos": [{ "campo", "motivo" }] } }`, `429` com `Retry-After`. Toda resposta
autenticada traz `RateLimit-Limit`, `RateLimit-Remaining` e `RateLimit-Reset`.

Para fazer o aplicativo do Resgata ler este CRM no lugar do CRMSOLUT em um ambiente de
testes, defina o `CRMSOLUT_BASE_URL` dele como o `/api/v1` deste app e cole uma chave na
tela da integração CRMSOLUT. O contrato foi verificado rodando o próprio cliente CRMSOLUT em
PHP e o leitor de leads do Resgata contra esta API.

## Onde mora cada conceito do Resgata

| Conceito | Arquivo |
|---|---|
| Cliente da API da Plataforma (`/connection`, `/numbers`, ajudantes do sandbox) | `internal/resgata/client.go` |
| Cliente de modelos, públicos, campanhas e vínculo com a Meta | `internal/resgata/campaigns.go` |
| Verifier e challenge PKCE, URL de autorização, mensagens de erro do OAuth | `internal/resgata/oauth.go` |
| Verificação e geração da assinatura do webhook | `internal/resgata/webhook.go` |
| Tipos do envelope de evento, explicações dos códigos de falha | `internal/resgata/events.go` |
| Início do OAuth, retorno e desconexão | `internal/web/oauth.go` |
| Receptor de webhooks (verificar, guardar, deduplicar, aplicar, responder) | `internal/web/webhook.go` |
| O que cada evento faz no CRM | `internal/events/apply.go` |
| Criptografia do token em repouso (AES-GCM) | `internal/crypto/box.go` |
| API no estilo CRMSOLUT e limitador de requisições | `internal/crmapi/` |
| Página de conexão, checklist de configuração, números | `internal/web/connection.go`, `internal/web/templates/connection.html` |
| Tela de modelos | `internal/web/modelos.go`, `internal/web/templates/templates.html` |
| Etapa "Enviar campanha", andamento, pausa e retomada | `internal/web/campanhas.go`, `internal/web/templates/campaign_new.html`, `campaign.html` |
| Estado da campanha mantido pelos webhooks | `internal/store/campaigns.go`, `internal/events/apply.go` |
| Ferramentas de sandbox | `internal/web/sandbox.go` |
| Simulação offline da plataforma | `internal/mockresgata/mock.go` |
| Configuração por ambiente e a tela de configuração | `internal/config/config.go`, `internal/web/templates/setup.html` |
| Token de login do portal e cookie de sessão | `internal/portal/` |
| Workspaces por desenvolvedor, registro e limpeza | `internal/workspace/manager.go`, `internal/web/portal.go` |
| Tela de entrada e a barra "Olá, Nome" | `internal/web/templates/gate.html`, `internal/web/templates/layout.html` |

## Desenvolvimento local com túnel

O Resgata precisa alcançar `POST /webhooks/resgata` pela internet, então no seu notebook
exponha a porta 8080 com qualquer túnel e use o endereço público no portal e em `PUBLIC_URL`:

```sh
cloudflared tunnel --url http://localhost:8080
ngrok http 8080
```

Defina `PUBLIC_URL=https://<host-do-seu-túnel>`, cadastre
`https://<host-do-seu-túnel>/webhooks/resgata` como endpoint de webhook e
`https://<host-do-seu-túnel>/resgata/callback` como URI de redirecionamento, e reinicie. A
URI de redirecionamento precisa ser idêntica à cadastrada. Sem túnel você ainda consegue
rodar tudo com a [simulação offline](#2-offline-com-a-plataforma-simulada).

## Notas de segurança

- **Assinatura primeiro.** Os webhooks são autenticados apenas pela assinatura HMAC,
  verificada sobre os bytes crus, com janela de 5 minutos contra repetição e comparação em
  tempo constante. Nada é aplicado antes de ela passar.
- **PKCE (S256) e `state` de uso único.** O verifier é gerado a cada tentativa, fica no
  servidor e só é enviado ao `/oauth/token`. Um retorno com `state` desconhecido ou repetido
  é recusado.
- **Token criptografado.** O token de conexão é criptografado com AES-GCM usando
  `APP_ENCRYPTION_KEY`. Ele nunca é exibido e é apagado quando a conexão é revogada.
- **Sem segredos no código.** A configuração vem só do ambiente; o `.env` está no
  `.gitignore`; as chaves da API do CRM são guardadas como hashes SHA-256; os testes geram
  os próprios valores aleatórios.
- **Interface administrativa.** Defina `ADMIN_PASSWORD` (ou use o modo portal) para qualquer
  coisa acessível pela internet. Os POSTs também exigem requisição de mesma origem
  (verificações de `Sec-Fetch-Site` e `Origin`). A API do CRM e o endpoint de webhook têm
  autenticação própria e não ficam atrás da senha administrativa.
- **Content-Security-Policy.** Scripts só do próprio app. Atributos de estilo inline são
  permitidos (`style-src-attr`); não há script inline.
- Os opt-outs são respeitados no servidor: um lead com `opt_out` não pode receber mensagens.

## Desenvolvimento

```sh
make test    # go test -race ./...
make lint    # verificação do gofmt e go vet
make build   # binários estáticos em bin/
```

Os testes cobrem a verificação de assinatura (janela de tolerância, vários valores de `v1`,
cabeçalhos malformados), a deduplicação e a nova tentativa após falha, o PKCE (vetor da RFC
7636), a API no estilo CRMSOLUT (autenticação, filtros, paginação por cursor, validação,
limite de requisições), os tokens do portal e cookies de sessão (válidos, forjados,
expirados, repetidos), o isolamento entre workspaces, o roteamento de webhooks por
`external_ref`, o reset, a limpeza e o limite de capacidade, e fluxos de ponta a ponta que
rodam OAuth, enviam mensagens e recebem webhooks assinados contra a plataforma simulada, para
um desenvolvedor e para dois lado a lado.

Os arquivos de código-fonte não têm comentários, por convenção; as explicações ficam neste
README.

## Licença

MIT
