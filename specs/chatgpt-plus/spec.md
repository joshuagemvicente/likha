# Feature: ChatGPT Plus/Pro login (OAuth 2) provider

**Status:** implemented (local) — live probe with a real ChatGPT Plus account
outstanding. See [research.md](research.md) for the researched flows and
[tasks.md](tasks.md) for the implementation record.

## Context

ChatGPT Plus/Pro subscribers can use their subscription models (no API key,
no per-token billing) through OAuth 2 against OpenAI's Codex backend. Two
agent harnesses do this today and agree on every flow constant:

- **OpenCode** (`sst/opencode` `dev`, `packages/opencode/src/plugin/openai/codex.ts`):
  provider `openai`, auth methods "ChatGPT Pro/Plus (browser)" (loopback
  authorization-code + PKCE) and "ChatGPT Pro/Plus (headless)" (device flow).
- **OMP**: its own `openai-codex` provider with the same endpoints, client ID,
  and port.

Lisa's predefined provider list (`internal/model/provider.go`) is no longer
BYOK-only: every key-based row carries a `KeyEnv` and the client sends one
static Bearer key, while the `chatgpt` row carries a new `Auth: AuthOAuth`
kind whose credential is an `OAuthCredentials` token set (`access`,
`refresh`, `expires`, `accountId`) that refreshes automatically, and whose
backend speaks the **OpenAI Responses** wire format, not chat completions.
Both differences cut across existing v1 rules, which is why this was a spec
feature and not "another table row"; v1-spec.md is amended accordingly
(§2, §3, FR-02, §5, §7).

## Facts established by research (full detail in research.md)

| Fact | Value | Source |
| --- | --- | --- |
| Issuer / authorize | `https://auth.openai.com/oauth/authorize` | opencode `codex.ts`; OMP `oauth-code.ts` engine |
| Client ID (public PKCE client, shared by both harnesses) | `app_EMoamEEZ73f0CkXaXp7hrann` | same |
| Redirect | `http://localhost:1455/auth/callback`, fixed port 1455 | same |
| PKCE | S256, 43-char verifier, `state` CSRF check | opencode `generatePKCE` |
| Scopes | `openid profile email offline_access` | same |
| Extra authorize flags | `codex_cli_simplified_flow=true`, `originator=<tool>` | same |
| Token exchange / refresh | `POST {issuer}/oauth/token`, form-encoded; `expires_in` default 3600 s | same |
| Account ID claim | `chatgpt_account_id` (top-level, or under `https://api.openai.com/auth`, or `organizations[0].id`) | opencode `extractAccountIdFromClaims`; OMP `getTokenProfile` |
| Headless device flow | `POST {issuer}/api/accounts/deviceauth/usercode` → user enters code at `{issuer}/codex/device` → poll `deviceauth/token` → exchange with server-supplied verifier | both |
| API endpoint | `https://chatgpt.com/backend-api/codex/responses` (Responses API) | both |
| Request headers | `authorization: Bearer <access>`, `ChatGPT-Account-Id: <accountId>`, per-conversation `session-id`, `originator`, `User-Agent: <tool>/<version>`; optional `x-openai-internal-codex-residency` from token claims | opencode `chat.headers`; OMP `provider-quirks.md` |
| Sampling params | backend returns HTTP 400 `Unsupported parameter` for `temperature`/`top_p` | OMP quirks doc |
| Rate limits | 5-hour primary + 7-day secondary windows; Spark-suffixed models meter separately; zero per-token cost | OMP quirks doc |
| Token storage (prior art) | opencode: `~/.local/share/opencode/auth.json` mode 0600, `{type:"oauth", refresh, access, expires, accountId}` | opencode `auth/index.ts` |

## Resolved decisions (defaults chosen during implementation)

The former "Open decisions" are resolved by the implemented defaults, each
following the spec's stated preference:

1. **Client ID reuse — resolved: reuse OpenAI's public Codex CLI client**
   (`ChatGPTClientID = app_EMoamEEZ73f0CkXaXp7hrann`). The provider-constant
   comment in `internal/model/provider.go` documents the reuse, and
   README's "Sign in with ChatGPT (Plus/Pro)" section states the login uses
   OpenAI's public Codex CLI client, not a Lisa-issued one. No own client ID
   was sought.
2. **Terms-of-service risk — resolved: document, not accept silently.**
   README's "Privacy and terms" note and the provider-constant comment state
   that using a ChatGPT subscription through a third-party harness is
   subject to OpenAI's consumer terms; the login surfaces never claim a
   Lisa-issued client.
3. **Wire format — resolved: native Responses request/stream mapping** in a
   new `internal/model/codex.go` surface (`BuildCodexRequest`,
   `ConsumeCodexStream`), reached from `Client.Stream` whenever the client
   is OAuth-backed (`streamCodex`). Chat-completions transport of Responses
   payloads was never built and is never assumed; there is no fallback
   path.
4. **Storage schema — resolved: `providers.json` schema v2** typed entries
   (`{"type": "api"|"oauth", ...}`) in `internal/app/keyfile.go`, one file
   at 0600. Files written by older versions hold a plain map of API keys
   and are migrated in memory on read; a sibling `auth.json` was not added.
5. **Port 1455 — resolved: fixed loopback port** (`ChatGPTCallbackPort`),
   matching prior art. A bind failure surfaces as a clear error naming the
   port (`listen on 1455: …` from `BrowserLogin`), so a concurrent login in
   another harness is visible to the user.
6. **Model list — resolved: curated static list** (`ChatGPTModels`:
   `gpt-5.5` default, `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.3-codex-spark`,
   `gpt-6-sol`, `gpt-6-luna`), because the backend's model-list route is not
   OpenAI-shaped. Setup offers this list and never fetches models from the
   backend; the OAuth connection check validates the login without a model
   list.
7. **Headless escape hatch — resolved: the `--device-login` CLI flag**, not
   an environment variable. No `LISA_CHATGPT_REFRESH_TOKEN`-style env var
   exists; `lisa --provider chatgpt --device-login` runs the device flow
   headlessly and stores the login.

## Implementation notes (what was built)

| File | Role |
| --- | --- |
| `internal/model/provider.go` | `chatgpt` row (`Auth: AuthOAuth`, no `KeyEnv`, `SessionHeader: "session-id"`), `AuthKind`, ChatGPT constants (issuer, client ID, callback port 1455, originator), `OAuthCredentials`, curated `ChatGPTModels`. |
| `internal/model/oauth.go` | PKCE browser login (`BrowserLogin`, loopback callback on 1455, state check, bind-failure error) and device flow (`DeviceLogin`), token exchange/refresh, account-ID extraction from token claims. |
| `internal/model/codex.go` | Responses wire: `BuildCodexRequest` (never emits sampling params) and `ConsumeCodexStream` SSE parsing for text, reasoning, and structured tool calls. |
| `internal/model/client.go` | `NewOAuth` builds the `/responses` client; `oauthSession` refreshes stale tokens single-flight and persists fresh sets via `SetOAuthSaver`; one forced-refresh retry on HTTP 401; `Usage()` summarizes the backend's `x-codex-primary-*` rate-window headers. |
| `internal/app/keyfile.go` | `providers.json` schema v2: typed `api`/`oauth` entries, 0600, in-memory migration of the legacy plain-key map on read. |
| `internal/app/provider.go` | `resolveProvider` returns a `resolvedProvider` with an OAuth branch: a stored login is required, `--api-key`/env keys do not apply, and the error names `--device-login`. |
| `internal/app/run.go` | `--device-login` CLI flow (no TUI or repository needed, prints URL/code, 10-minute bound). |
| `internal/app/tui.go` | `setupLogin` stage ("Press Enter to open the browser", 5-minute wait) replacing key entry for the ChatGPT row; stores the login and wires the refresh saver. |

## Acceptance criteria

Checked items are verified locally by the automated suite (setup flow,
refresh/retry, storage); the live-probe item requires a real ChatGPT Plus
account and stays open.

- [ ] **Verified locally:** the setup flow's OAuth stage (browser-login
      stage keyed off `AuthOAuth`, model stage fed by `ChatGPTModels`,
      credential stored on finish) is exercised by the TUI tests with a
      stubbed login; no API key is ever asked for the `chatgpt` row.
- [ ] **Verified locally:** an expired access token refreshes transparently;
      concurrent streams share one in-flight refresh; a mid-flight 401
      forces one refresh and retries exactly once; refresh failure reports
      "chatgpt login expired; sign in again" and keeps the session usable
      (all against `httptest` fixtures in `client_test.go`).
- [ ] **Verified locally:** credentials persist in the private state
      directory at 0600 (factory asserted in the providers.json tests) and
      never appear in the repository or session DB.
- [ ] **Verified locally:** no sampling parameters are sent (the Codex
      request builder omits `temperature`/`top_p`); tool calls map correctly
      to the Responses format (request/stream tests in `codex_test.go`).
- [ ] **Live probe outstanding:** a ChatGPT Plus subscriber completes
      first-run setup end to end and a full session (streamed text + one
      structured tool call + cancellation) runs against the live Codex
      backend — the probe gate from predefined-providers applies before the
      row is called accepted.
- [ ] **Live probe outstanding:** rate-window display is confirmed against
      the real backend's `x-codex-primary-*` headers (the footer wiring is
      tested locally with fixtures).
- [ ] Docs state which backend receives content, the login flow, the
      headless alternative, the storage schema, and the terms note
      (README "Sign in with ChatGPT (Plus/Pro)" and `--help`).

## Integration steps (as built)

The implementation followed the planned order; see [tasks.md](tasks.md) for
the ordered record and verification per step.
