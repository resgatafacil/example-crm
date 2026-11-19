# Resgata Fácil Platform API (integration guide for partners)

Version `2026-11-01`. Base URL `https://app.resgatafacil.com/api/platform/v1`. JSON in, JSON out, times as Unix seconds.

## Concepts

- **Application**: your product (for example CRMSOLUT). Created in the developer portal, with its domain, OAuth redirect URIs, API keys and webhook endpoints.
- **Connection**: one of your users linked to one Resgata Workspace. Every API call and every webhook carries the connection, so you route to the right user with `connection.id` or your own `external_ref`.
- **Environment**: `live` (real WhatsApp numbers) or `sandbox` (fake services, nothing leaves our servers). The credential decides the environment: `rf_live_*` or `rf_test_*`.
- **Credentials**
  - Application key `rf_<env>_app_…`: acts for the whole application (list connections, issue tokens, create sandbox connections).
  - Connection token `rf_<env>_conn_…`: acts for one user's connection. Send as `Authorization: Bearer <credential>`.

Keys and tokens are shown once and stored hashed. Rotate by creating a new one and revoking the old.

## Connecting a user (OAuth, live)

Authorization code with PKCE (S256 is mandatory).

1. Register the exact redirect URIs of your app in the portal (`https://…`, or `http://localhost…` for development).
2. Send the user's browser to `https://app.resgatafacil.com/autorizar` with:
   `client_id` (your application id `app_…`), `redirect_uri`, `state`, `code_challenge`, `code_challenge_method=S256`, `scope` (space separated), `external_ref` (your id for this user, optional but recommended).
3. The user signs in (if needed), **connects their Meta (WhatsApp Business) account with Facebook** when the application requires it (see below), sees your name and the permissions, and approves. Only the Workspace root person or an admin can approve. We redirect to `redirect_uri?code=…&state=…`, or `?error=access_denied|external_ref_in_use&state=…`.
4. From your server, `POST /oauth/token` with `grant_type=authorization_code`, `code`, `code_verifier`, `redirect_uri`, `client_id`. Response: `access_token` (the connection token), `scope`, `connection_id`, `external_ref`. Codes work once and expire in 10 minutes.

Scopes: `numbers:read`, `contacts:read`, `campaigns:read`, `campaigns:write`, `audiences:write`, `templates:read`, `templates:write`.

The user can disconnect you any time in Resgata (Integrações › Aplicativos conectados); you can disconnect with `DELETE /connection`. Both send a `connection.revoked` webhook.

### The two sign-ins: Resgata and Meta

Connecting a user takes two OAuth steps, both on Resgata screens, so you never handle Facebook credentials:

1. **Resgata consent** (`/autorizar`, above). If the user has not linked a Meta official WhatsApp Business account yet, the consent screen shows a "Conectar a Meta (WhatsApp Business)" step first (Facebook login, choose the business) and enables **Autorizar** only after it succeeds.
2. **Meta link.** It happens inside that screen, or later through a setup link you request with `POST /connection/meta-link`.

Applications require the Meta step by default. You can switch it off per application in the developer portal (application overview, "Conexão com a Meta"). Authorizing without a linked Meta account when it is required is refused with `422 meta_required` (the consent screen already prevents it).

A connected WhatsApp number (QR code) is **not** needed. Linking the Meta account does take one number slot of the user's Resgata plan, like linking it in the Resgata app. When the plan has no free slot the link is cancelled and the setup shows `meta_linked` as incomplete; `POST /audiences` and `POST /campaigns` answer `409 no_slot_available` for a connection whose Meta link has no number.

#### Setup link for the Meta step

`POST /connection/meta-link` (connection token) with `{ "return_url": "https://app.seudominio.com/integracoes/resgata/meta-return" }`. Response:

```json
{ "data": { "url": "https://app.resgatafacil.com/vincular/…", "expires_at": 1767226800 } }
```

Open `url` in the user's browser (no Resgata login needed). It is single use and expires in 30 minutes. When the user finishes, we send the browser to your `return_url` with `?status=linked|failed&connection=conn_…` added (existing query parameters are kept). After `status=linked`, `GET /connection/setup` shows `meta_linked` as `complete` without anything else from you.

`return_url` must have the same origin (scheme, host and port) as one of the redirect URIs registered on the application (`https`, or `http://localhost…` for development); anything else answers `422 invalid_request_error`. Do not rely on the static `action_url` of the `meta_linked` step: call this endpoint, or pass `?return_url=…` to `GET /connection/setup` and the `action_url` of an incomplete `meta_linked` step is a fresh setup link.

## What is still missing for the user

`GET /connection/setup` (connection token) tells you whether the user's Meta official WhatsApp Business account is ready to send, so you can draw the checklist in your own UI. A connected WhatsApp number (QR code) is **not** required: the platform only needs the Meta official account linked and healthy.

```json
{ "object": "connection_setup", "connection": "conn_…", "ready": false,
  "steps": [
    { "id": "workspace", "title": "Conta Resgata Fácil", "status": "complete", "required": true, "detail": null, "action_url": null },
    { "id": "meta_linked", "title": "Vincular a Meta oficial", "status": "incomplete", "required": true,
      "detail": "Nenhuma conta da Meta oficial vinculada.", "action_url": "https://app.resgatafacil.com/meta-official/vincular" },
    { "id": "payment_method", "title": "Forma de pagamento na Meta", "status": "unknown", "required": false,
      "detail": "A Meta não nos informa a forma de pagamento. Confira no Billing Hub da Meta…", "action_url": "https://business.facebook.com/…" } ] }
```

Each step has a `status`:

| Status | Meaning |
|---|---|
| `complete` | Done |
| `incomplete` | The user still has to do something; `action_url` opens the right place |
| `blocked` | Something is wrong on the Meta side (rejected, restricted, payment problem, number left the Cloud API); `detail` says what |
| `unknown` | We cannot tell yet (Meta has not reported it) |

`ready` is `true` when every `required` step is `complete` and no step at all is `blocked`. `detail` is a short human-readable explanation in Portuguese and may be `null`.

| Step id | What it checks | Required |
|---|---|---|
| `workspace` | The Resgata account exists | yes |
| `meta_linked` | An active Meta official link with a WhatsApp Business account and phone number, not asking to be reconnected | yes |
| `business_verified` | The business is verified in Meta Business Manager | no |
| `account_review` | The WhatsApp Business account passed Meta's review | yes |
| `display_name` | The display name is approved | yes |
| `phone_status` | The phone number is connected to the Cloud API | yes |
| `payment_method` | Meta is billing deliveries normally (`complete`), reports a payment problem (`blocked`) or we have no signal yet (`unknown`) | no |
| `messaging_restriction` | No active restriction on sending (for example 130497 or 131031) | yes |
| `webhook_subscribed` | Meta webhooks are subscribed (Resgata repairs this automatically) | no |
| `business_profile` | Description, business category and at least one contact (email, website or address) are filled in | no |
| `template_available` | At least one approved message template exists | no |
| `messaging_tier` | Informational: the daily messaging limit (`detail`) | no |

Meta does not expose a positive "has a payment method" signal, so `payment_method` becomes `complete` only after Meta has billed a delivery for the number (or when Resgata pays the account). Until then it is `unknown` and does not stop `ready`; a reported payment problem always does.

The values are read from data Resgata already keeps in sync with Meta (health checks and Meta webhooks), so the endpoint is fast and makes no calls to Meta. After the user fixes something in Meta, it can take a few minutes to show up here.

`action_url` opens the right place in Resgata (or Meta Business Manager) for the user to finish that step, including the Meta official login.

## Endpoints

| Method and path | Credential | Notes |
|---|---|---|
| `GET /ping` | any | Who you are authenticated as |
| `GET /connections`, `GET /connections/{id}` | application key | Connections of your application in the key's environment |
| `POST /connections` | sandbox application key | `{ "external_ref": "…" }` creates a sandbox connection. In live, connections come from OAuth |
| `POST /connections/{id}/tokens` | application key | Issues a connection token (secret shown once). `DELETE /connections/{id}/tokens/{token}` revokes |
| `GET /connection` | connection token | The current connection |
| `GET /connection/setup` | connection token | Setup steps (above). Optional `?return_url=…` turns the `meta_linked` action into a setup link |
| `POST /connection/meta-link` | connection token | `{ "return_url" }` → `{ data: { url, expires_at } }`, the Meta setup link (above) |
| `DELETE /connection` | connection token | Disconnect |
| `GET /numbers` | token with `numbers:read` | Numbers of the workspace, whether they are connected (`connected`) and their `status` |
| `GET /templates`, `GET /templates/{id}` | token with `templates:read` | The message templates of the linked WhatsApp Business account, see Templates |
| `POST /templates`, `POST /templates/media`, `POST /templates/{id}/sync`, `DELETE /templates/{id}` | token with `templates:write` | Create (submits to Meta for approval), upload header media, refresh the status from Meta, delete |
| `POST /audiences` | token with `audiences:write` | Creates an audience (the people a campaign will message) from a JSON list of contacts, see Audiences |
| `GET /audiences/{id}` | token with `campaigns:read` | One audience with how many contacts were accepted |
| `POST /campaigns` | token with `campaigns:write` | Starts a campaign: an approved template sent to an audience, see Campaigns |
| `GET /campaigns`, `GET /campaigns/{id}` | token with `campaigns:read` | Campaigns created by this connection with their progress |
| `POST /campaigns/{id}/pause`, `POST /campaigns/{id}/resume` | token with `campaigns:write` | Pause and resume a campaign |
| `GET /oauth/sandbox/authorize` | none | Sandbox consent redirect, see Sandbox |
| `POST /sandbox/...` | sandbox credential | Sandbox helpers (numbers, events, inbound messages, opt-outs, reset), see Sandbox |

Errors use `{ "error": { "type", "message" } }` with `authentication_error` (401), `permission_error` (403), `not_found` (404), `invalid_request_error` (422), `rate_limit_error` (429). Sandbox routes called with a live credential answer `403 sandbox_only`; sandbox caps answer `422 sandbox_limit_reached`. Limit: 120 requests per minute per credential; respect `Retry-After`.

## Templates

Templates are the messages a campaign sends. Create them through the API and they go to Meta for approval, exactly like templates created in the Resgata app. They live in the WhatsApp Business account the user linked to Meta, so the connection must have `meta_linked` complete in `GET /connection/setup`; otherwise `POST /templates` answers `409 meta_not_ready`.

Ids look like `tpl_…`. Statuses: `pending`, `approved`, `rejected`, `paused`, `disabled`, `in_appeal`, `limit_exceeded`, `reinstated`. Only `approved` and `reinstated` templates can be used by campaigns.

```json
{ "id": "tpl_123", "object": "template", "name": "promocao_da_semana", "category": "MARKETING",
  "language": "pt_BR", "status": "pending", "rejection_reason": null,
  "components": [ { "type": "BODY", "text": "Oi {{1}}, confira nossa promoção." } ],
  "variables": { "count": 1, "placeholders": [1] }, "created_at": 1767225600, "updated_at": 1767225600 }
```

### Creating a template

`POST /templates` with a JSON body. `name` is free text: it is normalized to the lowercase `a-z0-9_` form Meta requires (`"Promoção da Semana"` becomes `promocao_da_semana`). `category` is `MARKETING`, `UTILITY` or `AUTHENTICATION`.

| Field | Notes |
|---|---|
| `body.text` | Required for structured templates, up to 1024 characters. Use `{{1}}`, `{{2}}`… for variables, with one entry in `body.examples` per variable |
| `header` | `{ "type": "text", "text": "…" }` (up to 60 characters) or `{ "type": "image" \| "video" \| "document", "media_id": "…" }`. `media_url` (a public link) works instead of `media_id` |
| `footer` | Text, up to 60 characters |
| `buttons` | Up to 3: `QUICK_REPLY`, `URL` (`url`, https) or `PHONE_NUMBER` (`phone_number`). Button text up to 25 characters |
| `carousel.cards` | 2 to 10 cards of `{ "media_type": "image" \| "video", "media_id", "body"?, "buttons": [ … ] }`. With a carousel the main text carries no header, footer or buttons |
| `components` | Advanced: the raw Meta `components` array, together with `name`, `category` and `language`. Use it for `AUTHENTICATION` templates (OTP buttons) or anything the fields above do not cover. It replaces the structured fields |

```sh
curl -X POST $BASE/templates -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "name": "promocao_da_semana", "category": "MARKETING", "language": "pt_BR",
  "header": { "type": "image", "media_id": "<from POST /templates/media>" },
  "body": { "text": "Oi {{1}}, confira nossa promoção.", "examples": ["Maria"] },
  "footer": "Responda PARAR para sair",
  "buttons": [ { "type": "URL", "text": "Ver oferta", "url": "https://seusite.com/oferta" } ]
}'
```

Responses: `201` with the template (`status: pending`), or `200` with `"duplicated": true` when an identical template already exists (the existing one is returned and nothing is sent to Meta). Invalid input is `422` with the field names of this API:

```json
{ "error": { "type": "invalid_request_error", "message": "O modelo tem campos inválidos.",
  "fields": { "body.text": ["O texto da mensagem é obrigatório."], "buttons": ["Use no máximo 3 botões."] } } }
```

Other errors: `409 template_in_progress` (the same template is being created right now, retry in a few seconds), `409 meta_not_ready`, `422 invalid_request_error` when Meta refuses the template (the message says why) and `502 meta_unavailable`.

### Header media

`POST /templates/media` (multipart, field `file`: jpg, png, mp4 or pdf, up to 25 MB) uploads the file to Meta and returns `{ "data": { "media_id": "…" } }`. Pass that `media_id` in `header.media_id` or `carousel.cards[].media_id` when creating the template.

### Reading, refreshing and deleting

- `GET /templates` lists templates, newest first. Filters: `status`, `category`, `language`, `name` (contains). `limit` is 1 to 100 (default 25); follow `meta.next_cursor` with `?cursor=…` for the next page.
- `GET /templates/{id}` returns one template.
- `POST /templates/{id}/sync` asks Meta for the current status and returns the refreshed template (`502 meta_unavailable` if Meta does not answer).
- `DELETE /templates/{id}` deletes the template on Meta and in Resgata. When Meta does not let the account delete it, the template is archived instead and the response says `"archived": true`.

### Events

`template.approved`, `template.rejected` and `template.status_changed` fire when a template changes status. `template.approved` covers `approved` and `reinstated`, `template.rejected` covers `rejected`, and `template.status_changed` covers every other change (`paused`, `disabled`, `in_appeal`, `limit_exceeded`, back to `pending`…).

```json
{ "type": "template.rejected", "data": {
  "template": { "id": "tpl_123", "name": "promocao_da_semana", "category": "MARKETING", "language": "pt_BR",
                "status": "rejected", "rejection_reason": "INVALID_FORMAT" },
  "previous_status": "pending" } }
```

Templates the customer creates in the Resgata app also fire these events for connected applications, because they live in the same account. Meta decides the status, so the event can arrive minutes to days after creation.

## Audiences

An audience is the list of people a campaign sends to. They are your own leads: send them in the request, nothing is read from the customer's Resgata contacts.

`POST /audiences` (token with `audiences:write`):

```json
{ "name": "Leads de novembro",
  "contacts": [
    { "phone": "(11) 98888-0001", "name": "Maria Souza", "variables": ["Dentista", "Sexta"] },
    { "phone": "11988880002", "name": "João Pereira" } ] }
```

`phone` is required (any common Brazilian format, with or without country code). `name` is optional. `variables` fill `{{n}}` placeholders of the template: a list (position 1 is `variable_1`) or an object `{ "2": "Sexta" }`, up to 15 values of 250 characters. One request takes up to 5,000 contacts (200 in the sandbox).

Response `201`:

```json
{ "data": { "id": "aud_…", "object": "audience", "name": "Leads de novembro",
  "contacts": { "received": 2, "accepted": 2, "rejected": 0 },
  "rejections": {}, "rejected_samples": [], "created_at": 1767225600 } }
```

Contacts we do not accept are counted in `rejections` by reason (`invalid`, `duplicated`, `quarantined`, `internacional_nao_habilitado`, `variable_too_long`, `incomplete`) and listed, up to 100, in `rejected_samples` with their position in your list. When no contact is valid the answer is `422 no_valid_contacts`. Shape errors are `422 invalid_request_error` with `fields`. The customer's plan decides whether imported audiences are allowed (`403 plan_not_allowed` on the free plan).

Contacts that opted out are never messaged: the same block lists the app uses apply to API campaigns.

## Campaigns

Campaigns are the only way to send through the API: there is no endpoint to send one message to one person. A campaign is an approved template plus an audience, and it follows the same rules as a campaign created in the Resgata app by the user who connected you: their plan, their schedule rules, their spend limit, the Meta limits of their WhatsApp Business account and the opt-out lists. WhatsApp costs are billed by Meta to the payment method of the customer's own account.

`POST /campaigns` (token with `campaigns:write`):

```json
{ "name": "Black Friday", "template_id": "tpl_123", "audience_id": "aud_…",
  "variables": { "1": "@nome", "2": "10% de desconto" },
  "schedule_at": "2026-11-27T09:00:00-03:00" }
```

| Field | |
|---|---|
| `template_id` | Required. An `approved` or `reinstated` template of the linked account |
| `audience_id` | Required |
| `variables` | One value per `{{n}}` of the template body. `@nome` is the contact's name, `@variavel_N` is the contact's `variables[N-1]`, anything else is sent as it is for everyone |
| `name` | Optional |
| `schedule_at` | Optional ISO 8601 date in the future. Needs a plan that allows scheduling (`403 schedule_not_allowed`) |
| `send_until` | Optional. After this moment the campaign stops sending |
| `daily_send_limit` or `contacts_limit` | Optional integers. Send that many per day, or pause after that many contacts (not both) |

Responses: `201` with the campaign, or `200` with `"duplicated": true` when an identical campaign was created in the last 10 minutes (the existing one is returned and nothing new is sent). Errors: `409 meta_not_ready`, `409 no_slot_available`, `409 template_not_approved`, `409 waba_restricted` (Meta restricted the customer's account), `422 invalid_request_error` (missing variables or contact data, with a message), `422 spend_limit_reached`, `422 empty_audience`, `403 plan_not_allowed`, `403 schedule_not_allowed`, `404 not_found` (template or audience) and `502 meta_unavailable`.

```json
{ "data": { "id": "cmp_123", "object": "campaign", "name": "Black Friday",
  "status": "running", "pause_reason": null,
  "template_id": "tpl_123", "audience_id": "aud_…",
  "scheduled_at": null, "send_until": null,
  "progress": { "total": 2, "sent": 1, "delivered": 1, "read": 0, "failed": 0, "skipped": 0, "pending": 1, "percent": 50 },
  "created_at": 1767225600, "finished_at": null } }
```

Statuses: `scheduled`, `pending` (waiting for Meta or for the template), `running`, `paused`, `finished`, `failed` (nothing could be sent) and `canceled`. `pause_reason` is `MANUAL_USER` after your pause, or the reason Resgata paused it (daily limit reached, Meta quality, spend limit…).

- `GET /campaigns` lists the campaigns this connection created, newest first (`name` filter, `limit` 1 to 100, `meta.next_cursor`). Campaigns the customer made in the Resgata app are not listed.
- `GET /campaigns/{id}` returns one with its progress.
- `POST /campaigns/{id}/pause` and `/resume`. Resume answers `409 campaign_not_paused`, `422 daily_limit_reached` (the next batch leaves by itself), `422 campaign_limit_reached`, `422 campaign_expired`, `409 waba_restricted` or `422 spend_limit_reached` when it cannot continue.

### Campaign events

`campaign.started` (the first messages were queued), `campaign.paused` (by you, by the customer or by Resgata), `campaign.finished` and `campaign.failed` carry `data.campaign` with the same shape as above. `message.sent|delivered|read|failed`, `message.received` and `contact.opted_out` of a campaign you created go only to your connection, with `campaign_id` set to the `cmp_…` id.

## Webhooks

Configure endpoints in the developer portal: URL, the events you want, and the signing secret (`whsec_…`, generated or your own). Production endpoints must be `https` and resolve to a public address.

Envelope:

```json
{ "id": "evt_…", "object": "event", "type": "message.received", "api_version": "2026-11-01",
  "created": 1767225600, "environment": "live",
  "connection": { "id": "conn_…", "external_ref": "your-user-id" },
  "data": { … } }
```

Headers: `X-Resgata-Signature: t=<unix>,v1=<hex>`, `X-Resgata-Event-Id`, `X-Resgata-Delivery-Id`, `X-Resgata-Delivery-Attempt`.

Verify: compute `HMAC_SHA256(secret, t + "." + rawBody)` as hex, compare in constant time with every `v1` in the header, and reject when `t` is more than 300 seconds old.

```js
const crypto = require('crypto');
function verify(rawBody, header, secret) {
  const parts = Object.fromEntries(header.split(',').map((p) => p.split('=')));
  if (Math.abs(Date.now() / 1000 - Number(parts.t)) > 300) return false;
  const expected = crypto.createHmac('sha256', secret).update(`${parts.t}.${rawBody}`).digest('hex');
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(parts.v1));
}
```

Delivery is **at least once** and not ordered. Deduplicate on `X-Resgata-Event-Id`. Answer `2xx` within 10 seconds. Failures retry after 15s, 1m, 5m, 15m, 1h, 12h and 24h (8 attempts in total). An endpoint with 10 consecutive abandoned deliveries is disabled; re-enable it in the portal. You can send test events and replay any delivery from the portal, which keeps 30 days of request and webhook logs.

Events: `campaign.started`, `campaign.paused`, `campaign.finished`, `campaign.failed`, `template.approved`, `template.rejected`, `template.status_changed`, `connection.created`, `connection.revoked`, `number.connected`, `number.disconnected`, `message.received`, `message.sent`, `message.delivered`, `message.read`, `message.failed`, `contact.opted_out`, `ping`.

Each endpoint receives only the events it is subscribed to. Endpoints created before an event type existed do not get it automatically: open the endpoint in the portal (Webhooks, Editar) and select the new events, or use "Selecionar todos". Without `campaign.*` your app never learns that a campaign finished.

`message.*` and `contact.opted_out` cover campaign sends and replies made through Meta official numbers. For campaigns created through this API they carry the `cmp_…` id and reach only the connection that created the campaign; for campaigns the customer creates in the Resgata app they reach every connected application.

## Sandbox

The sandbox is a complete copy of the API that runs on fake services. It needs no WhatsApp number, no Meta account, no customer and nothing from us: sign up, create a test key in the portal and build your whole integration. Nothing a test credential does can reach a real number, and live credentials cannot use the sandbox routes (`403 sandbox_only`). Payloads and webhook events have exactly the shape of the live ones.

Sandbox data is separate per environment (events, endpoints, keys and logs never mix). Configure your sandbox webhook endpoints in the portal like live ones; sandbox events go only to endpoints created for the sandbox environment.

### Getting started

1. Create a sandbox connection with your `rf_test_app_…` key: `POST /connections {"external_ref": "your-user-id"}`. It comes with one connected fake number.
2. Issue a connection token: `POST /connections/{id}/tokens`.
3. Or skip steps 1 and 2 and test the OAuth flow itself (below).

### Sandbox endpoints

All of them need a `rf_test_*` credential. "Token" means a sandbox connection token (no scope is needed for the helpers).

| Method and path | Credential | What it does |
|---|---|---|
| `POST /connections` | application key | Creates a sandbox connection (and its default number) |
| `GET /oauth/sandbox/authorize` | none | Consent redirect without a customer (see below) |
| `GET /numbers` | token | Lists the sandbox numbers with their `status` |
| `POST /sandbox/numbers` | token | `{ "name"?, "phone"?, "status"? }` creates a number (default `connected`) |
| `PATCH /sandbox/numbers/{id}` | token | `{ "status" }` changes the state and fires the matching `number.*` event |
| `DELETE /sandbox/numbers/{id}` | token | Removes a number (fires `number.disconnected` if it was connected) |
| `POST /templates`, `GET /templates`, … | token | Same contract as live, see Templates. Nothing is sent to Meta |
| `POST /audiences`, `POST /campaigns`, `GET /campaigns`, … | token | Same contract as live, see Audiences and Campaigns. A campaign sends one simulated message per contact through the scenarios below, 50 contacts per second, and fires `campaign.*` and `message.*` |
| `POST /templates/media` | token | Returns a fake `media_id` (`sbx_media_…`) so you can build header and carousel templates |
| `POST /sandbox/templates/{id}/status` | token | `{ "status", "reason"? }` moves a template to any status and fires the matching `template.*` event |
| `POST /sandbox/inbound-messages` | token | `{ "from", "text" }` simulates a customer reply (`message.received`) |
| `POST /sandbox/opt-outs` | token | `{ "phone" }` simulates an opt-out (`contact.opted_out`) |
| `POST /sandbox/events` | token | Fires any documented event at your sandbox endpoints (below) |
| `POST /sandbox/meta-link` | token | `{ "status": "linked\|pending\|failed" }` sets the simulated Meta link state (see Testing the OAuth flow) |
| `POST /sandbox/reset` | token or application key | Wipes sandbox data (below) |

### Number states

`connected`, `disconnected`, `restricted`, `banned`, `qr_pending`. `connected` is `true` only in that state. `GET /connection/setup` follows the numbers and simulates the Meta checks: with a `connected` number every step is `complete`; `restricted` makes `messaging_restriction` `blocked`; `banned` makes `meta_linked` and `phone_status` `blocked`; `disconnected` and `qr_pending` leave `phone_status` `incomplete`; with no numbers `meta_linked` is `incomplete`. The other states only matter while no number is `connected`: one connected number is enough for `ready`.

Changing the state with `PATCH /sandbox/numbers/{id}` fires `number.connected` (state `connected`) or `number.disconnected` (any other state), with `data.number` = `{ id, name, phone, status }` and `data.source` = `sandbox_<status>`. The same status twice fires nothing.

### Message outcomes (magic phone numbers)

Include these numbers in an audience (any formatting, `+55 00 0000-0000` style works) to get deterministic outcomes for their message in a campaign. Any other number goes `queued → sent (0s) → delivered (2s) → read (5s)`.

| Phone | Outcome |
|---|---|
| `5500000000000` | `message.failed`, code `131026` (undeliverable) |
| `5500000000001` | `message.failed`, code `131047` (re-engagement message) |
| `5500000000002` | `message.failed`, code `131048` (spam rate limit hit) |
| `5500000000003` | `message.failed`, code `131049` (not delivered to keep the ecosystem healthy) |
| `5500000000004` | `message.failed`, code `131056` (pair rate limit hit) |
| `5500000000010` | `sent`, never delivered |
| `5500000000011` | `sent`, `delivered`, never read |
| `5500000000012` | stays `queued`, never sent |
| `5500000000020` | slow: `sent` after 30s, `delivered` after 60s, `read` after 120s |
| `5500000000030` | goes to `read`, then 7s later `contact.opted_out` |
| `5500000000031` | goes to `read`, then 8s later a `message.received` reply ("Recebi sua mensagem!") |

### Template outcomes (magic names)

A sandbox template goes `pending`, then `approved` about 5 seconds later (`template.approved`). The name decides the other outcomes:

| Name contains | Outcome |
|---|---|
| `_reject` | `rejected` after about 3 seconds with `rejection_reason: INVALID_FORMAT` (`template.rejected`) |
| `_pause` | `approved`, then `paused` 5 seconds later (`template.approved`, then `template.status_changed`) |
| `_pending` | stays `pending` forever |
| `_slow` | `approved` after 60 seconds |

Sandbox templates go through the same validation as live ones (limits, buttons, carousel rules), so an invalid template fails the same way in both. `template_available` in `GET /connection/setup` is `complete` once the connection has an approved sandbox template.

### Firing events

`POST /sandbox/events` with `{ "type": "message.delivered" }` (any type in the Events list) builds a realistic payload and delivers it to your subscribed sandbox endpoints, so you can test handlers without waiting. Optional fields: `phone`, `text` (message events), `number_id` (number and opt-out events) and `data`, which is merged over the generated `data` (for example `{"data": {"message": {"from_name": "Maria"}}}`). Message events leave a stored sandbox message. `connection.revoked` and the number events only send the event, they never change the connection or the number. Answer `202` with the event envelope and `meta.deliveries` (how many endpoints it was queued for).

### Reset

`POST /sandbox/reset` with a connection token deletes that connection's messages, numbers and events and recreates the default connected number (tokens and the connection stay). With the application key it does that for every sandbox connection of the application and also deletes sandbox events that belong to no connection; add `{"delete_connections": true}` to delete the sandbox connections and their tokens too. Live data is never touched.

### Testing the OAuth flow

Instead of sending the browser to `/autorizar` (live, needs a real Resgata customer), send it to:

`GET https://app.resgatafacil.com/api/platform/v1/oauth/sandbox/authorize?client_id=…&redirect_uri=…&state=…&code_challenge=…&code_challenge_method=S256&scope=numbers:read%20campaigns:read&external_ref=…`

It takes the same parameters as the live flow (the redirect URI must be registered on your application) and redirects immediately, as if a customer had approved, to `redirect_uri?code=…&state=…`. Exchange the code at `POST /oauth/token` exactly like live: you get an `rf_test_conn_…` token, a `connection.created` event and a sandbox connection with a default number. Reusing an `external_ref` reuses its connection; leaving it out generates one. Add `simulate=access_denied` or `simulate=external_ref_in_use` to get those error redirects. Invalid requests (unknown client, unregistered redirect, missing PKCE) answer `422` JSON and never redirect.

Add `meta=linked|pending|failed` to simulate the Meta step (default `linked`: the connection comes with the Meta account ready, as before). With `pending` or `failed`, `GET /connection/setup` shows `meta_linked` and everything that depends on it as `incomplete` (`ready: false`) until you finish the Meta step:

- `POST /connection/meta-link` with a sandbox token returns a signed sandbox URL (`…/api/platform/v1/oauth/sandbox/meta-link?…`) that redirects to your `return_url` with the same `?status=linked&connection=conn_…` contract as live and marks the connection as linked. Add `"simulate": "failed"` to the request to get `status=failed` instead.
- `POST /sandbox/meta-link` with a sandbox token and `{ "status": "linked|pending|failed" }` sets the state directly, with no browser.

### Limits and retention

Per application: 20 sandbox connections and 5000 sandbox events. Per connection: 5 numbers, 500 messages, 50 templates, 20 audiences (200 contacts each) and 20 campaigns. Beyond that the API answers `422 sandbox_limit_reached`; `POST /sandbox/reset` frees everything. Sandbox messages and events are deleted after 7 days, and sandbox connections unused for 90 days are deleted with their data.

### Curl walkthrough

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

## Removed: individual messages

`POST /messages` and `GET /messages/{id}` no longer exist (they only worked in the sandbox). Messages are sent only by creating a campaign with an approved template and an audience. The `messages:send` and `messages:read` scopes are retired: authorization requests that still list them are accepted and the scopes are ignored. Message events (`message.sent|delivered|read|failed|received`) are unchanged.
