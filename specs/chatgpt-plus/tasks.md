# Tasks: ChatGPT Plus/Pro login (OAuth 2) provider

Implementation record, in order. No checkmark claims beyond what landed. The
planned integration steps lived in [spec.md](spec.md) §"Integration steps";
v1-spec.md was amended first per the spec's stated requirement.

1. **v1-spec.md amendment (contract).** §2 in-scope gained the OAuth-login
   concept, out-of-scope narrowed to "protocol adapters beyond OpenAI
   chat-completions and the accepted list's documented exceptions"; §3
   model-integration row names the Codex Responses surface and the
   `chatgpt` row joined the predefined-providers stack row; FR-02 gained the
   OAuth sign-in, `--device-login`, and auto-refresh sentence; §7 switched
   "key format" to "key format or login flow"; §5 privacy bullet now covers
   OAuth login credentials. Verified: wording review only.

2. **OAuth layer (`internal/model/oauth.go`, `provider.go`).** Added the
   `AuthKind` (`AuthOAuth`) kind, the `chatgpt` row with the ChatGPT
   constants and `OAuthCredentials`, and the PKCE browser flow + device
   flow with token exchange/refresh and account-ID claim parsing.
   Verified: 16 tests in `oauth_test.go` covering PKCE, authorize URL,
   exchange/refresh, claim extraction, and both login flows over `httptest`
   (state rejection, provider error, deadline, device codes) — green.

3. **Responses wire (`internal/model/codex.go`).** Native request mapping
   (never emits `temperature`/`top_p`) and SSE stream consumption for text,
   reasoning, and structured tool calls. Verified: 26 tests in
   `codex_test.go` (request building, stream parsing, cancellation,
   malformed events) — green.

4. **Client integration (`internal/model/client.go`).** `NewOAuth` builds
   the `/responses` client; `oauthSession` refreshes single-flight with a
   persistence saver; one forced-refresh retry on 401; `Check` validates the
   OAuth login without a model list; `Usage()` summarizes
   `x-codex-primary-*` rate headers. Verified: OAuth tests in
   `client_test.go` (headers/SSE, refresh+saver, 401 retry, single-flight,
   check path, usage) over `httptest` — green.

5. **App wiring (`internal/app/keyfile.go`, `provider.go`, `run.go`).**
   `providers.json` schema v2 with typed `api`/`oauth` entries at 0600 and
   legacy-map migration on read; `resolveProvider` OAuth branch (stored
   login required, error names `--device-login`); `--device-login` headless
   flow. Verified: `run_test.go` stored-OAuth resolution and persisted-key
   permission tests — green.

6. **TUI setup stage (`internal/app/tui.go`).** `setupLogin` stage replaces
   masked key entry for the ChatGPT row (browser sign-in with 5-minute
   wait) and stores the credential on finish. Verified: `tui_test.go` OAuth
   setup tests (stage transition, success/error/ignore paths, credential
   stored on finish) — green.

7. **Docs.** README gained "Sign in with ChatGPT (Plus/Pro)" (flow, storage,
   refresh, usage display, terms note) and `run.go` `--help` documents the
   `--device-login` flag and the OAuth provider row. Verified: help text
   reviewed.

8. **Verification (2026-09-29).** `go test ./...` passes (all packages).
   The live probe with a real ChatGPT Plus account has not run; per
   [checklist.md](checklist.md) and `specs/README.md`, the provider is
   implemented (local), not accepted.
