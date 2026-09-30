# Context: ChatGPT Plus/Pro login (OAuth 2) provider

Code, specs, and external evidence this feature touches.

## Code (as implemented)

| Path | Role |
| --- | --- |
| `internal/model/provider.go` | `chatgpt` provider row (`Hosted: true`, no `KeyEnv`, new `Auth: AuthOAuth` kind), ChatGPT OAuth constants (`ChatGPTIssuer`, `ChatGPTClientID`, `ChatGPTCallbackPort = 1455`, `ChatGPTOriginator`, `ChatGPTDevicePath`), `OAuthCredentials` (refresh/access/expires/accountID), curated `ChatGPTModels`. |
| `internal/model/oauth.go` | Login flows: `BrowserLogin` (PKCE S256, loopback server on the fixed port, `state` CSRF check, bind-failure error naming the port) and `DeviceLogin` (usercode → device page → poll → exchange with server-supplied verifier); `ExchangeCode`, `RefreshTokens`, `AccountIDFromToken` claim parsing. |
| `internal/model/codex.go` | Responses wire: `BuildCodexRequest` maps chat turns and tool definitions to the Responses format, never emitting sampling params; `ConsumeCodexStream` parses the SSE events (text, reasoning, structured tool calls, completion/error events). |
| `internal/model/client.go` | `NewOAuth` (URL becomes `base + "/responses"`), `oauthSession` (single-flight clock-aware refresh, stale-token grace, refresh saver via `SetOAuthSaver`), `streamCodex` (one forced-refresh retry on HTTP 401, Codex headers incl. `ChatGPT-Account-Id`/`session-id`/`originator`), `Check` OAuth branch (validates the login without a model list), `Usage()` from the captured `x-codex-primary-*` headers. |
| `internal/app/keyfile.go` | `providers.json` schema v2: typed `api`/`oauth` entries at 0600; `readCredentials` migrates the legacy plain map of keys in memory on read. |
| `internal/app/provider.go` | `resolveProvider` gains the OAuth branch (`resolvedProvider.oauth`/`creds`); a stored login is required, a missing one errors naming `--device-login`. |
| `internal/app/run.go` | `--device-login` headless flow (no TUI/repository; prints URL and code; 10-minute bound) and OAuth client construction with the refresh saver wired to `storeOAuth`. |
| `internal/app/tui.go` | First-run setup `setupLogin` stage ("Press Enter to open the browser", 5-minute context) replaces masked-key entry for the ChatGPT row; `finishSetup` stores the OAuth credential and wires the saver. |
| `internal/app/run_test.go`, `internal/model/client_test.go`, `internal/model/oauth_test.go`, `internal/model/codex_test.go` | Test patterns this feature follows: httptest SSE/token fixtures, table tests per provider row, header assertions, loopback callback tests over `httptest` issuers. |

## Flow constants: now implemented in code

The concrete constants researched below (issuer, client ID, redirect port,
PKCE S256, scopes, device endpoints, Responses URL, headers, no sampling
params, token shapes) are now in code — `internal/model/provider.go` holds
the provider-visible constants and `internal/model/oauth.go`/`codex.go` hold
the flow implementations. [research.md](research.md) remains the source for
the researched detail and citations; do not restate constant values here.

## Spec state

- `specs/predefined-providers/spec.md` — admission rule (full probe before
  "accepted"); this feature's v1-spec amendments (§2, §3, FR-02, §5, §7)
  followed the same §2/§3 amendment pattern.
- `specs/first-run-setup/spec.md` — setup stages; the ChatGPT row replaces
  the key-entry stage with the browser-login stage.
- `specs/v1-spec.md` — FR-02 and §3 now cover OAuth login providers;
  v1-spec.md has been amended (see `tasks.md`).

## External evidence

Full notes with citations: [research.md](research.md).

- OpenCode `codex.ts` (sst/opencode `dev`): complete OAuth constants and flow.
- OMP docs (`provider-quirks.md`, `providers.md`, `auth-broker-gateway.md`):
  same endpoints independently, plus rate-window (5 h/7 d) and residency
  behavior.
- **Unverified / must probe live:** exact model list a Plus account sees
  beyond the curated defaults; whether `originator` is validated
  server-side; rate-window header behavior against a real plan.
