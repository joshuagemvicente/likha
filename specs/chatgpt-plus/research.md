# ChatGPT Plus (Codex backend) OAuth 2 — OpenCode/OMP research for Lisa

Researched 2026-09-29. Primary sources: `sst/opencode` `dev` branch (byte-identical at `anomalyco/opencode`), OMP harness docs (`omp://`), and the Lisa repo in cwd. No code edits made.

## 1. OAuth 2 flow for ChatGPT Plus (Codex backend) in OpenCode

All flow constants below come from **`packages/opencode/src/plugin/openai/codex.ts`** (sst/opencode `dev`; fetched via `https://raw.githubusercontent.com/sst/opencode/dev/packages/opencode/src/plugin/openai/codex.ts`, identical at `https://raw.githubusercontent.com/anomalyco/opencode/dev/...`). The plugin registers auth methods on provider id `"openai"` with labels `"ChatGPT Pro/Plus (browser)"`, `"ChatGPT Pro/Plus (headless)"`, and `"Manually enter API Key"`.

### Constants (top of `codex.ts`)

| Constant | Value |
|---|---|
| `CLIENT_ID` | `app_EMoamEEZ73f0CkXaXp7hrann` |
| `ISSUER` | `https://auth.openai.com` |
| `CODEX_API_ENDPOINT` | `https://chatgpt.com/backend-api/codex/responses` |
| `OAUTH_PORT` | `1455` (redirect URI `http://localhost:1455/auth/callback`) |
| `ALLOWED_MODELS` | `gpt-5.5`, `gpt-5.3-codex-spark`, `gpt-5.4`, `gpt-5.4-mini`, `gpt-6-sol`, `gpt-6-luna` |
| `DISALLOWED_MODELS` | `gpt-5.5-pro` (also `gpt-5.6` rejected in the filter) |

### Browser flow (authorization code + PKCE)

- **PKCE** (`generatePKCE`): verifier = 43 random chars from `A-Za-z0-9-._~`; challenge = base64url(SHA-256(verifier)); method `S256`.
- **Authorize URL** (`buildAuthorizeUrl`): `GET {ISSUER}/oauth/authorize` with params `response_type=code`, `client_id`, `redirect_uri=http://localhost:1455/auth/callback`, `scope=openid profile email offline_access`, `code_challenge`, `code_challenge_method=S256`, `id_token_add_organizations=true`, `codex_cli_simplified_flow=true`, `state`, `originator=opencode`. `state` is base64url of 32 random bytes.
- **Local callback server** (`startOAuthServer`): `http.createServer` on port 1455; routes `/auth/callback` (validates `state` against the pending request — CSRF check; handles `error`/`error_description`; 400 on missing code; replies a branded success/error HTML page via `OauthCallbackPage`), `/cancel` (rejects "Login cancelled"), else 404. A 5-minute timeout rejects a pending flow (`waitForOAuthCallback`); the server is closed after completion (`stopOAuthServer`).
- **Token exchange** (`exchangeCodeForTokens`): `POST {ISSUER}/oauth/token`, `Content-Type: application/x-www-form-urlencoded`, body: `grant_type=authorization_code`, `code`, `redirect_uri`, `client_id`, `code_verifier`. Response `TokenResponse`: `{ id_token, access_token, refresh_token, expires_in? }`. Expiry stored as `Date.now() + (expires_in ?? 3600) * 1000`.
- **Claims** (`IdTokenClaims`, `parseJwtClaims`, `extractAccountIdFromClaims`): JWT part[1] base64url-decoded; account id resolved in order: `claims.chatgpt_account_id` → `claims["https://api.openai.com/auth"].chatgpt_account_id` → `claims.organizations[0].id`. `extractAccountId` prefers `id_token` claims, then `access_token`. Other claims: `email`, `chatgpt_compute_residency` (top-level or under `https://api.openai.com/auth`); `"no_constraint"` residency is ignored.
- **Success result** persisted by opencode's auth layer: `{ type: "oauth", refresh, access, expires, accountId }`.

### Headless device flow ("ChatGPT Pro/Plus (headless)")

- Initiate: `POST {ISSUER}/api/accounts/deviceauth/usercode` with JSON body `{ client_id: CLIENT_ID }` and header `User-Agent: opencode/<InstallationVersion>`. Response: `{ device_auth_id, user_code, interval }` (interval parsed as seconds, min 1, ×1000 ms).
- User visits `{ISSUER}/codex/device` and enters `user_code`.
- Poll: `POST {ISSUER}/api/accounts/deviceauth/token` with `{ device_auth_id, user_code }`. On `200`: response `{ authorization_code, code_verifier }` — then exchange at `POST {ISSUER}/oauth/token` (form-encoded) with `grant_type=authorization_code`, `code=<authorization_code>`, `redirect_uri={ISSUER}/deviceauth/callback`, `client_id`, `code_verifier` (server-supplied verifier — no local PKCE pair in this flow). `403`/`404` → keep polling after `interval + OAUTH_POLLING_SAFETY_MARGIN_MS` (3000 ms); any other status → `{ type: "failed" }`.

### Token storage

`packages/opencode/src/auth/index.ts`: single JSON file at `path.join(Global.Path.data, "auth.json")` — `Global.Path.data` = `path.join(xdgData, "opencode")` per `packages/core/src/global.ts`, i.e. **`~/.local/share/opencode/auth.json`**, written with **mode 0600**. Shape: `{ "<providerID>": <Info> }` where `Info` is a discriminated union:
- `oauth`: `{ type: "oauth", refresh: string, access: string, expires: NonNegativeInt (epoch ms), accountId?: string, enterpriseUrl?: string }`
- `api`: `{ type: "api", key: string, metadata?: Record<string,string> }`
- `wellknown`: `{ type: "wellknown", key, token }`

Also exports `OAUTH_DUMMY_KEY = "opencode-oauth-dummy-key"` — the placeholder "apiKey" the plugin returns so OpenAI SDK-style call paths still see an apiKey value while the custom fetch does real OAuth. `process.env.OPENCODE_AUTH_CONTENT` can override the whole store (used by tests/containers).

### Refresh flow

`refreshAccessToken` in `codex.ts`: `POST {issuer}/oauth/token`, form-encoded `grant_type=refresh_token`, `refresh_token`, `client_id`. In the auth `loader`'s custom `fetch`, if `currentAuth.access` is empty or `expires < Date.now()`, a single-flight `refreshPromise` refreshes and re-persists via `input.client.auth.set({ path: { id: "openai" }, body: {type:"oauth", refresh, access, expires, accountId} })`, falling back to the previously stored `accountId` if the new tokens carry none.

### API base URL and request shaping (the custom `fetch` in the auth loader)

- Strips any caller-supplied `Authorization` header, then sets `authorization: Bearer <access>`.
- Sets `ChatGPT-Account-Id: <accountId>` when an account id is known (extracted from claims at login/refresh).
- **URL rewrite**: any request whose path includes `/v1/responses` **or `/chat/completions`** is rewritten to `CODEX_API_ENDPOINT` = `https://chatgpt.com/backend-api/codex/responses` — i.e. the Codex backend speaks the **OpenAI Responses API**, and opencode maps its chat-completions-style calls onto it. Non-matching paths pass through unchanged.
- When rewriting, sets `x-openai-internal-codex-residency: <residency>` if the access-token claims carry a compute residency (other than `no_constraint`).
- Optional WebSocket transport via `OpenAIWebSocketPool` (same plugin dir, `ws-pool.ts`).

### Additional request headers (`chat.headers` hook in `CodexAuthPlugin`)

For `providerID === "openai"`: `originator: opencode`, `User-Agent: opencode/<version> (<platform> <release>; <arch>)`, and **`session-id: <opencode sessionID>`** (per-conversation). A temporary header marks title-generation requests for HTTP fallback when websockets are installed. `chat.params` clears `maxOutputTokens` ("match codex cli").

### Model filtering (oauth-mode `models()` hook)

Only when `ctx.auth.type === "oauth"`: drops models with `options.reasoningMode === "pro"`; keeps `ALLOWED_MODELS` ids; rejects `gpt-5.5-pro` and `gpt-5.6`; otherwise regex `^gpt-(\d+)(?:\.(\d+))?` must yield major > 5 or (major = 5 and minor > 4) — i.e. newer gpt-5.4+ variants pass. Costs are zeroed (`input/output/cache` 0 — subscription, not pay-per-token), and `gpt-5.5`/`gpt-5.6` get limits `{ context: 400_000, input: 272_000, output: 128_000 }`.

## 2. Same flow in OMP (this harness)

OMP models ChatGPT/Codex as its own provider **`openai-codex`** (transport API `openai-codex-responses`). Sources: `omp://provider-quirks.md` ("OpenAI Codex" section), `omp://providers.md`, `omp://auth-broker-gateway.md`, `omp://models.md`, `omp://environment-variables.md`.

- **OAuth login flows**: declared in `packages/catalog/src/compat/rules/auth/openai-codex.kdl` (`login "oauth-code"`, engine `packages/ai/src/registry/engine/oauth-code.ts`) and `openai-codex-device.kdl`; hooks in `packages/ai/src/registry/oauth/openai-codex.ts`. Browser flow uses **PKCE S256** (`createOpenAICodexAuthorizationUrl`) with fixed local port **1455** (`http://localhost:1455/auth/callback`), client ID **`app_EMoamEEZ73f0CkXaXp7hrann`**, and the simplified CLI flow flags. Headless device flow (`loginOpenAICodexDevice`) uses `https://auth.openai.com/api/accounts/deviceauth/usercode` and polls `deviceauth/token` — endpoint-for-endpoint the same flow opencode implements.
- **Refresh & claims**: `refreshOpenAICodexToken` posts `grant_type: refresh_token` to `https://auth.openai.com/oauth/token`; `getTokenProfile` extracts `chatgpt_account_id` and `email` from JWT claims namespaces `https://api.openai.com/auth` and `https://api.openai.com/profile`.
- **Requests**: target `https://chatgpt.com/backend-api/codex/responses` (or `providers.openai-codex.baseUrl` override) over SSE or WebSocket (`OpenAI-Beta: responses_websockets=2026-02-06`). Headers include `ChatGPT-Account-Id` (`getCodexAccountId`), `session_id`/`session-id`, `x-codex-installation-id`, `x-codex-window-id`, `x-codex-turn-metadata` (JSON with `turn_id`, `installation_id`, `parent_turn_id`, `request_kind`), `x-codex-parent-thread-id`, `x-openai-subagent`. Residency claim `chatgpt_data_residency` (fallback `chatgpt_compute_residency`) → `x-openai-internal-codex-residency` header; enterprise workspaces otherwise 401 with `Workspace is not authorized in this region.`
- **Storage**: OAuth credentials live in the local SQLite auth store **`~/.omp/agent/agent.db`** (or `PI_CODING_AGENT_DIR` relocation), or in the auth-broker SQLite vault when `OMP_AUTH_BROKER_URL` is set (broker callback port map includes `openai-codex:1455`). Env fallback: `OPENAI_CODEX_OAUTH_TOKEN`. Resolution ladder (`omp://providers.md`): runtime `--api-key` → `models.yml` config key → **stored OAuth credential (multiple accounts ranked and rotated; each ChatGPT org/workspace counts as its own account)** → login-sourced API key → provider env var → other stored key.
- **Rate-limit/plan nuances**: 5-hour primary window and 7-day secondary window (usage via `GET /wham/usage` on canonical `chatgpt.com` origins; response headers `x-codex-primary-used-percent`, `x-codex-primary-window-minutes`, `x-codex-primary-reset-at`, `x-codex-secondary-*`); Spark meter (`-spark` model suffix, e.g. `gpt-5.3-codex-spark`) is isolated into its own `spark` scope so exhausting it doesn't block normal chat (`codexRankingStrategy`, `packages/ai/src/usage/openai-codex.ts`); saved rate-limit reset credits via `GET/POST /wham/rate-limit-reset-credits[/consume]`. Up to 5 retries on transient errors, 429 backoff within a 5-minute budget. Codex backend returns HTTP 400 `Unsupported parameter` for sampling params (`temperature`, `top_p`, …) — stripped by `transformRequestBody`.
- **Models**: default model `gpt-5.5` (descriptor in `packages/catalog/src/provider-models/descriptors.ts`); dynamic discovery `fetchCodexModels` (`packages/catalog/src/discovery/codex.ts`) queries `/codex/models` or `/models` on the backend with `v2StreamingEnabled: true`, parsing `reasoning_presets`. Variants: `codex`, `codex-max`, `codex-mini`, `codex-spark`. Web-search default chain uses `openai-codex/gpt-6-luna`, `gpt-5.6-luna`, `gpt-5.6`, `gpt-5.5`. (Consistent with opencode's `ALLOWED_MODELS`.) [INFERENCE] models.dev metadata was not directly consulted; the model-ID lists above are from the two codebases.

## 3. Lisa provider integration points (cwd)

- **Provider registry** — `internal/model/provider.go`: `Provider{Name, DisplayName, BaseURL, KeyEnv, DefaultModel, Hosted, SessionHeader}`; `Providers` table (openai, openrouter, bedrock, dialagram, opencode-zen, opencode-go); `LookupProvider` case-insensitive. `SessionHeader` already exists for `x-opencode-session` (opencode-zen/go rows) — the seam where `ChatGPT-Account-Id` or `session-id` semantics would slot in. Adding a provider "is a table row"; a non-OpenAI-compatible protocol is a new client surface (v1 excludes it) — relevant because the Codex backend is a **Responses** API, not chat completions.
- **Credential resolution** — `internal/app/provider.go` `resolveProvider()`: ladder = `--api-key` flag (incl. `LISA_API_KEY`) → `p.KeyEnv` env → `storedKey(stateDir)` → error; persists via `storeKey` when `persistKey`. An OAuth provider needs a richer credential than a static string here (refresh/access/expires/accountId), so this function and `keyfile.go` are the primary touch points.
- **Credential storage** — `internal/app/keyfile.go`: `<stateDir>/providers.json`, `map[string]string`, mode 0600, one key per provider; never in the repo or session DB. OAuth tokens would need either a second file or a schema change to that map.
- **Config storage** — `internal/app/config.go`: `<stateDir>/config.json` `{provider, model, theme}` (first-run setup result), 0600.
- **Model client** — `internal/model/client.go`: `New(endpoint, model, apiKey)` enforces HTTPS for hosted endpoints (plain HTTP only loopback `/v1`), no redirects; `Stream()` POSTs `<base>/chat/completions` (SSE) with headers `Authorization: Bearer <apiKey>`, `User-Agent: lisa/dev` (overwritten by build tag), `Accept: text/event-stream`, plus `c.sessionHeader: c.sessionID` when set (`SetSessionHeader`/`SetSession`). `Check()` does a read-only model-list fetch. **Gaps for OAuth**: no token refresh, no extra header injection beyond the session header, no URL rewrite, and only the chat-completions wire format — the Codex backend at `https://chatgpt.com/backend-api/codex/responses` speaks Responses (opencode/OMP both rewire chat/completions-style calls onto it, per `codex.ts` `fetch` rewrite and OMP's `openai-codex-responses` transport).
- **Wiring** — `internal/app/run.go:224-278`: `resolveProvider` → `resolveModel` → `model.New` → `client.SetSessionHeader(selected.SessionHeader); client.SetSession(snapshot.ID)`. First-run setup in `internal/app/tui.go:316-337` builds the client the same way from the setup form.
- **Test patterns** — `internal/app/run_test.go`: table tests over every `model.Providers` row asserting `endpoint/verified/display/key`; key-resolution ladder tests (flag vs env vs stored vs persist) against `t.TempDir()`. `internal/model/client_test.go`: `httptest` servers asserting method/paths/headers (e.g. lines 288-292 assert the `x-opencode-session` header reaches the server), SSE fixtures via `sendEvents`. `internal/app/agent_test.go`/`tui_test.go` spin httptest SSE servers through `model.New(server.URL+"/v1", ...)`. An OAuth-credentialed provider would be tested the same way: httptest token endpoint + loopback callback, asserting refresh-before-expiry and header injection.

### Where an OAuth-credentialed provider would slot in (synthesis)

1. New `Provider` row (e.g. `chatgpt-codex`) with `Hosted: true` but no static `KeyEnv`; `BaseURL` pointing at a Codex/Responses-compatible endpoint — or a new client surface per the v1-spec rule, since the Codex backend is Responses-wire, which Lisa's `client.go` does not speak.
2. Auth acquisition: port-1455 loopback HTTP server + PKCE S256 + `codex_cli_simplified_flow=true` authorize at `https://auth.openai.com/oauth/authorize`; device flow as the headless alternative; token exchange at `/oauth/token`.
3. Storage: extend `providers.json` (or a sibling `auth.json`) with `{type:"oauth", refresh, access, expires, accountId}` at 0600.
4. Client changes: refresh on 401/expiry, send `ChatGPT-Account-Id`, honor compute-residency, per-conversation `session-id` header (Lisa already has the session-header seam), and either implement the Responses wire format or route through a gateway that translates chat-completions to Responses.
5. Rate-limit nuance to surface in UI: 5h/7d windows, Spark meter separation, zero token cost (subscription).