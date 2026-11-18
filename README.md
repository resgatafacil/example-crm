# Funil Demo: example CRM for the Resgata Fácil Platform API

A small, readable CRM written in Go that shows partners how to integrate with the
**Resgata Fácil Platform API**: OAuth (authorization code with PKCE), the REST API and
signed webhooks. It also exposes a **CRMSOLUT-style leads API**, so the whole flow works
end to end: Resgata reads this CRM's leads, and this CRM sends messages through Resgata
and reacts to the webhooks that come back.

The CRM itself is fictional ("Funil Demo"). Everything about the integration is real and
meant to be copied.

> Nota em português: este repositório é um CRM de exemplo para parceiros que vão integrar
> com a API, os webhooks e o OAuth do Resgata Fácil. A interface está em português do
> Brasil; o código e esta documentação estão em inglês. Veja "Quick start" para rodar no
> sandbox sem precisar de cliente, número de WhatsApp ou conta na Meta.

## What you get

- **Funnel board, leads and timeline**: flows with ordered steps, leads with stage, opt-out
  flag and a timeline of notes, messages and replies.
- **OAuth with PKCE** to connect a user to Resgata Fácil, with friendly errors, a setup
  checklist (`GET /connection/setup`) and the workspace numbers (`GET /numbers`).
- **Send a WhatsApp message** from a lead (`POST /messages`) and watch the status move to
  sent, delivered, read or failed as webhooks arrive.
- **Webhook receiver** with signature verification, replay window, deduplication and an
  events page (valid and rejected deliveries, expandable JSON, filter by type).
- **Sandbox tools**: fire any documented event, simulate a customer reply or an opt-out,
  create numbers and change their state, reset the sandbox.
- **CRMSOLUT-style API** (`/api/v1/unidades`, `/fluxos`, `/leads`) with per-unit bearer
  keys, cursor pagination and rate limits, in the exact shape Resgata's CRMSOLUT
  integration consumes.
- A **mock of the Resgata platform** (`cmd/mockresgata`) to try everything offline.

## Quick start

Requirements: Go 1.26 or newer. No cgo, no database server (SQLite is embedded).

### 1. Against the real Resgata sandbox

1. In the developer portal create an application, copy its `client_id` (`app_…`) and
   register the redirect URI `http://localhost:8080/resgata/callback`.
2. Create a **sandbox** webhook endpoint pointing at your public URL plus
   `/webhooks/resgata` (see [Local development with a tunnel](#local-development-with-a-tunnel))
   and copy its signing secret (`whsec_…`).
3. Configure and run:

   ```sh
   cp .env.example .env
   openssl rand -base64 32
   ```

   Paste the generated value into `APP_ENCRYPTION_KEY`, then fill `RESGATA_CLIENT_ID` and
   `RESGATA_WEBHOOK_SECRET`. Optionally set `RESGATA_APP_KEY` (a `rf_test_app_…` key) to
   enable the "create connection with the app key" shortcut.

   ```sh
   make run
   ```

4. Open <http://localhost:8080>. If anything required is missing, a setup screen lists
   exactly which variables to set. Follow "Primeiros passos" on the home page: it ticks
   itself from real state.

### 2. Offline, with the mock platform

```sh
make mock
```

In another terminal, with the same secret in both processes:

```sh
export RESGATA_API_URL=http://localhost:9090 RESGATA_APP_URL=http://localhost:9090
export RESGATA_CLIENT_ID=app_mock RESGATA_WEBHOOK_SECRET=<any value> APP_ENCRYPTION_KEY=$(openssl rand -base64 32)
make run
```

The mock sends signed webhooks to `http://localhost:8080/webhooks/resgata` (override with
`MOCK_WEBHOOK_URL`). It understands the sandbox magic phone numbers.

### 3. Docker

```sh
cp .env.example .env
docker compose up --build
docker compose --profile offline up --build
```

The second command also starts the mock platform. The compose file keeps the databases in
the `funil-data` volume mounted at `/data`; the image sets `DATA_DIR=/data`, so portal mode
stores its registry and workspaces there too. Keep that volume on persistent storage if you
want workspaces to survive a redeploy.

## Configuration

Everything comes from environment variables (a `.env` file in the working directory is
loaded for convenience and never overrides real environment variables). **No key, token or
secret lives in the code or in the repository.**

| Variable | Required | Default | What it is |
|---|---|---|---|
| `APP_ENCRYPTION_KEY` | yes | | 32 random bytes in base64. Encrypts the Resgata connection token at rest (AES-GCM). `openssl rand -base64 32` |
| `RESGATA_CLIENT_ID` | yes | | Application id (`app_…`) from the developer portal |
| `RESGATA_WEBHOOK_SECRET` | yes | | Webhook signing secret (`whsec_…`) from the portal |
| `RESGATA_ENVIRONMENT` | no | `sandbox` | `sandbox` or `live`. Sandbox uses `/oauth/sandbox/authorize` and shows the sandbox tools; live uses `/autorizar` |
| `RESGATA_APP_KEY` | no | | `rf_test_app_…` key, only for the sandbox shortcut that creates a connection without OAuth |
| `RESGATA_API_URL` | no | `https://app.resgatafacil.com/api/platform/v1` | Platform API base URL |
| `RESGATA_APP_URL` | no | `https://app.resgatafacil.com` | Where `/autorizar` lives |
| `PUBLIC_URL` | no | `http://localhost` + `APP_ADDR` | Public address of this app. Builds the OAuth redirect URI (`PUBLIC_URL/resgata/callback`) and the webhook URL shown in the UI |
| `APP_ADDR` | no | `:8080` | Listen address |
| `DATABASE_PATH` | no | `data/funil.db` | SQLite file |
| `ADMIN_PASSWORD` | no | | If set, the admin UI asks for HTTP basic auth (any user name, this password). **If unset the UI is open to anyone who can reach it**: fine on localhost, not on the internet |
| `CRM_API_RATE_LIMIT` | no | `60` | Requests per minute per CRM API key |
| `DATA_DIR` | no | folder of `DATABASE_PATH` | Where portal mode keeps `registry.db` and `workspaces/<id>.db` |
| `PORTAL_SSO_SECRET` | no | | Turns [portal gating](#gating-the-demo-with-the-resgata-developers-portal) on and replaces `ADMIN_PASSWORD`. At least 16 characters; `openssl rand -hex 32` |
| `PORTAL_URL` | no | `https://developers.resgatafacil.com` | Where the gate page and "Sair" send people |
| `WORKSPACE_TTL_HOURS` | no | `24` | Hours without activity before a workspace is deleted |
| `MAX_WORKSPACES` | no | `50` | Workspaces that can exist at once; beyond it new developers see a "demo is full" page |
| `SESSION_TTL_HOURS` | no | `8` | Lifetime of the session cookie |
| `WORKSPACE_PURGE_INTERVAL_MINUTES` | no | `15` | How often idle workspaces are looked for |

Demo data (units, flows, 15 leads, including sandbox magic numbers) is created the first
time the database is empty. Pass `--no-seed` to start empty.

## Gating the demo with the Resgata developers portal

By default the demo is one workspace, open on localhost or behind `ADMIN_PASSWORD`. To host
it publicly for people who have access to the Resgata developers portal, set
`PORTAL_SSO_SECRET`. Portal mode changes three things:

- **Entry only through the portal.** The portal sends the signed-in developer to
  `GET /portal-login?token=<token>`. Without a valid session every admin page shows
  "Entre pelo portal de desenvolvedores" with a button to `PORTAL_URL`. `/webhooks/resgata`,
  `/api/v1/*`, static files and `/healthz` stay reachable, because they carry their own
  authentication.
- **One workspace per developer.** The token's `sub` becomes a stable workspace id
  (the first 16 hex characters of `sha256("funil-demo/workspace/v1:" + sub)`). Each
  workspace is its own SQLite file with its own seeded data, CRM API keys (they look like
  `crm_<workspace id>_<random>`), encrypted Resgata connection and event log. The workspace
  id is also the `external_ref` of its Resgata connection, which is how webhooks find their
  way back: the one application-level webhook secret verifies the signature, then the event
  goes to the workspace named by `connection.external_ref`. Events for an unknown workspace
  are answered with `200` and dropped, so Resgata does not keep retrying them.
- **Cleanup.** A workspace idle for `WORKSPACE_TTL_HOURS` is deleted: the app first calls
  `DELETE /connection` with the workspace's token (failures are logged and the purge
  continues), then removes the database and the registry row. "Reiniciar" in the header
  does the same for the developer's own workspace and seeds it again. `MAX_WORKSPACES`
  protects the host and the sandbox quotas of your Resgata application, which are counted
  per application.

### The login token

The portal builds the token; this app only verifies it.

```
token     = base64url(payload) + "." + base64url(HMAC_SHA256(secret, base64url(payload)))
payload   = {"sub": "<stable developer id>", "name": "<display name>",
             "iat": <unix>, "exp": <iat + 60>, "jti": "<unique id>"}
```

Both parts use raw base64url (no `=` padding) and the HMAC covers the first part exactly as
it is sent. The key is `PORTAL_SSO_SECRET`. The app accepts a token once (`jti` is
remembered until it expires), rejects `exp` in the past (5 seconds of clock skew allowed),
tokens issued in the future and lifetimes above 5 minutes, and compares signatures in
constant time.

Go, using this repository's package:

```go
token, err := portal.Sign(secret, portal.Claims{
	Subject: "dev-123", Name: "Maria Souza",
	IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), ID: randomID,
})
loginURL := "https://demo.example.com/portal-login?token=" + url.QueryEscape(token)
```

Any other language:

```
payload = json({sub, name, iat, exp, jti})
part1   = base64url_nopad(payload)
part2   = base64url_nopad(hmac_sha256(key = secret, message = part1))
token   = part1 + "." + part2
```

For local work, `cmd/mktoken` signs one from `PORTAL_SSO_SECRET`:

```sh
go run ./cmd/mktoken -sub dev-1 -name "Maria Souza" -url http://localhost:8080
```

It prints a ready-to-open login link (omit `-url` to print only the token). Each link works
once, so run it again for every sign-in.

### Security notes for portal mode

- The session cookie is `workspaceId.expiry.signature`, signed with a key derived from
  `APP_ENCRYPTION_KEY` (HKDF-SHA256 with a fixed context label), `HttpOnly`,
  `SameSite=Lax`, and `Secure` when `PUBLIC_URL` is `https`. It carries no personal data.
- A cookie whose workspace was purged no longer works; its owner lands on the gate page.
- The token travels in a URL, so it lives for a minute, works once and the response is
  `Cache-Control: no-store`. Serve the app over https in production.
- Keep `PORTAL_SSO_SECRET` out of the repository and out of client code. Rotating it signs
  everybody out of the login link flow but keeps existing sessions until they expire.
- The demo runs only on the Resgata sandbox. Do not point portal mode at live credentials.

## The flow, step by step

```
 ┌────────────┐  1. authorize (PKCE)   ┌───────────────────────┐
 │  Funil Demo│ ─────────────────────► │ Resgata Fácil          │
 │  (this app)│ ◄───────────────────── │ /autorizar or sandbox  │
 │            │  2. code + state       │                       │
 │            │  3. POST /oauth/token  │                       │
 │            │ ─────────────────────► │  Platform API          │
 │            │ ◄───────────────────── │  /api/platform/v1      │
 │            │  token (stored         │                       │
 │            │  encrypted)            │                       │
 │            │  4. GET /connection/setup, /numbers             │
 │            │  5. POST /messages ───────────────────────────► │
 │            │                        │                       │
 │            │  6. signed webhooks    │   WhatsApp / sandbox   │
 │            │ ◄───────────────────── │   message.sent ...     │
 └─────┬──────┘  POST /webhooks/resgata└───────────┬───────────┘
       │                                           │
       │  7. GET /api/v1/leads (Bearer crm_…)      │
       └◄──────────────────────────────────────────┘
          Resgata reads this CRM like it reads CRMSOLUT
```

1. **Connect.** `GET /resgata/conectar` creates a random `state` and a PKCE `code_verifier`
   (kept server side), then redirects to `/autorizar` (live) or
   `/oauth/sandbox/authorize` (sandbox) with `client_id`, `redirect_uri`, `state`,
   `code_challenge` (S256), `scope` and `external_ref`. The verifier never leaves the server.
2. **Callback.** `GET /resgata/callback` checks the one-time `state`, handles
   `error=access_denied|external_ref_in_use`, then exchanges the code at
   `POST /oauth/token` with the verifier. The returned connection token is encrypted with
   AES-GCM before it is stored.
3. **Setup checklist.** `GET /connection/setup` returns what the end user still has to do
   in Resgata. The UI draws the steps and links to each `action_url`.
4. **Send.** `POST /messages` with `to`, `text` and an optional `number_id`. In live,
   Resgata answers `501 not_available` today; the UI explains it. The CRM refuses to send
   to leads that opted out.
5. **Receive.** Resgata calls `POST /webhooks/resgata`. The handler:
   - reads the **raw body** and verifies `X-Resgata-Signature` (`t=<unix>,v1=<hex>`):
     `HMAC_SHA256(secret, t + "." + rawBody)`, constant-time comparison against **every**
     `v1`, rejected when `|now - t| > 300s`;
   - stores the event (rejected ones too, for the events page);
   - **deduplicates** on `X-Resgata-Event-Id` (delivery is at least once and unordered);
   - applies it: `message.*` statuses only ever move forward (a late `sent` never
     overwrites `read`; `failed` is final), `message.received` lands on the lead matching
     the phone (or creates a lead in the first step of the first flow),
     `contact.opted_out` sets `opt_out`, `connection.revoked` deletes the token;
   - answers `2xx` quickly. If processing fails it answers `500` and the event is **not**
     marked processed, so the retry is applied instead of dropped as a duplicate.
6. **Let Resgata read your leads.** Create a key on **Chaves da API** and give Resgata the
   base URL plus the key (see below).

## The CRMSOLUT-style API

Base `/api/v1`, `Authorization: Bearer crm_<random>`. Keys belong to a unit and only see
that unit's leads. They are stored hashed (SHA-256) and shown once.

```sh
BASE=http://localhost:8080/api/v1
KEY=crm_...

curl -H "Authorization: Bearer $KEY" $BASE/unidades
curl -H "Authorization: Bearer $KEY" $BASE/fluxos
curl -H "Authorization: Bearer $KEY" "$BASE/leads?estagio=lead&limite=2"
curl -H "Authorization: Bearer $KEY" "$BASE/leads?limite=2&cursor=<proximo_cursor>"
```

| Route | Notes |
|---|---|
| `GET /unidades` | The key's unit: `id`, `nome`, `cidade`, `estado`, `ativa` |
| `GET /fluxos` | Flows with ordered `etapas` (`id`, `nome`, `ordem`, `inicial`) |
| `GET /leads` | Filters `organizacao_id`, `fluxo_id`, `etapa_id`, `estagio` (`lead`, `avaliado`, `cliente`), `limite` (1 to 500, default 50), `cursor` |

Envelope: `{ "dados": [...], "paginacao": { "quantidade", "tem_mais", "proximo_cursor" } }`.
A lead has `id`, `nome`, `celular` (E.164), `email`, `estagio`, `opt_out`, `unidade`,
`fluxo`, `etapa` (each `{id, nome}`), `criado_em` and `alterado_em` (ISO 8601). `opt_out`
turns `true` when Resgata reports `contact.opted_out`.

Errors: `401` bad key, `403` suspended unit or another unit's id, `422`
`{ "erro": { "campos": [{ "campo", "motivo" }] } }`, `429` with `Retry-After`. Every
authenticated answer carries `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset`.

To have the Resgata app read this CRM instead of CRMSOLUT in a test environment, set its
`CRMSOLUT_BASE_URL` to this app's `/api/v1` and paste a key into the CRMSOLUT integration
screen. The contract was verified by running Resgata's own PHP CRMSOLUT client and lead
reader against this API.

## Where each Resgata concept lives

| Concept | File |
|---|---|
| Platform API client (`/connection`, `/numbers`, `/messages`, sandbox helpers) | `internal/resgata/client.go` |
| PKCE verifier and challenge, authorize URL, OAuth error messages | `internal/resgata/oauth.go` |
| Webhook signature verification and signing | `internal/resgata/webhook.go` |
| Event envelope types, failure code explanations | `internal/resgata/events.go` |
| OAuth start, callback, disconnect | `internal/web/oauth.go` |
| Webhook receiver (verify, store, dedupe, apply, answer) | `internal/web/webhook.go` |
| What each event does to the CRM | `internal/events/apply.go` |
| Token encryption at rest (AES-GCM) | `internal/crypto/box.go` |
| CRMSOLUT-style API and rate limiter | `internal/crmapi/` |
| Connection page, setup checklist, numbers | `internal/web/connection.go`, `internal/web/templates/connection.html` |
| Send a message from a lead | `internal/web/leads.go` (`leadMessage`) |
| Sandbox tools | `internal/web/sandbox.go` |
| Offline mock of the platform | `internal/mockresgata/mock.go` |
| Environment configuration and the setup screen | `internal/config/config.go`, `internal/web/templates/setup.html` |
| Portal login token and session cookie | `internal/portal/` |
| Per-developer workspaces, registry and cleanup | `internal/workspace/manager.go`, `internal/web/portal.go` |
| Gate page and the "Olá, Nome" bar | `internal/web/templates/gate.html`, `internal/web/templates/layout.html` |

## Local development with a tunnel

Resgata has to reach `POST /webhooks/resgata` from the internet, so on your laptop expose
port 8080 with any tunnel and use the public address in the portal and in `PUBLIC_URL`:

```sh
cloudflared tunnel --url http://localhost:8080
ngrok http 8080
```

Set `PUBLIC_URL=https://<your-tunnel-host>`, register
`https://<your-tunnel-host>/webhooks/resgata` as a webhook endpoint and
`https://<your-tunnel-host>/resgata/callback` as a redirect URI, then restart. The redirect
URI must be identical to the registered one. Without a tunnel you can still run
everything with the [offline mock](#2-offline-with-the-mock-platform).

## Security notes

- **Signature first.** Webhooks are authenticated only by their HMAC signature, verified on
  the raw bytes, with a 5 minute replay window and constant-time comparison. Nothing is
  applied before it passes.
- **PKCE (S256) and one-time `state`.** The verifier is generated per attempt, kept on the
  server and sent only to `/oauth/token`. A callback with an unknown or reused `state` is
  refused.
- **Encrypted token.** The connection token is encrypted with AES-GCM using
  `APP_ENCRYPTION_KEY`. It is never rendered and is deleted when the connection is revoked.
- **No secrets in code.** Configuration is environment only; `.env` is git-ignored; CRM API
  keys are stored as SHA-256 hashes; tests generate their own random values.
- **Admin UI.** Set `ADMIN_PASSWORD` (or use portal mode) for anything reachable from the internet. POSTs also
  require a same-origin request (`Sec-Fetch-Site` and `Origin` checks). The CRM API and the
  webhook endpoint have their own authentication and are not behind the admin password.
- **Content-Security-Policy.** Scripts only from the app itself. Inline style attributes are
  allowed (`style-src-attr`); there is no inline script.
- Opt-outs are respected server side: a lead with `opt_out` cannot be messaged.

## Development

```sh
make test    # go test -race ./...
make lint    # gofmt check and go vet
make build   # static binaries in bin/
```

The tests cover signature verification (tolerance window, several `v1` values, malformed
headers), deduplication and retry-after-failure, PKCE (RFC 7636 vector), the CRMSOLUT-style
API (auth, filters, cursor pagination, validation, rate limit), portal tokens and session
cookies (valid, forged, expired, replayed), workspace isolation, webhook routing by
`external_ref`, reset, purge and the capacity limit, and end-to-end flows that run OAuth,
send messages and receive signed webhooks against the mock platform, for one developer and
for two side by side.

Source files carry no comments by convention; the explanations live in this README.

## License

MIT
