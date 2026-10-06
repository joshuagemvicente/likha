# Tasks: official Sign in with ChatGPT migration

**Status:** in progress (2026-10-05). This is the migration plan, not a record of
completed implementation. Parent session owns integrated verification and
CHANGELOG outcomes. The previous Codex fixture results do not verify SIWC.

## 1. Contract and documentation

- [ ] Keep the implementation aligned with the amended [FR-02](../v1-spec.md)
  and [spec.md](spec.md), using the dated official sources in
  [research.md](research.md).
- [ ] Update provider/setup/usage guidance without discarding unrelated local
  edits. Remove advertised device login, fixed Codex client reuse, private
  backend requests, curated ChatGPT eligibility, and exact-zero cost assumptions.
- [ ] Report implementation and automated checks separately from a live
  account/browser probe and the published-release walkthrough.

## 2. Registration and browser authorization

Work in `internal/model/provider.go`, `internal/model/oauth.go`, and the
credential-store seam in `internal/providers`.

- [ ] Set ChatGPT's model API/resource to `https://api.openai.com/v1`; do not use
  Codex device endpoints or another application's client ID.
- [ ] Persist one opaque host ID before authorization begins. Use a documented
  host-ID format, such as a once-generated `urn:uuid:<UUIDv4>`. Preserve it
  across refreshes, restarts, account changes, and sign-out.
- [ ] For new account/workspace registration, authorize with
  `client_id=dynamic_agent_client`, `agent_name_hint=Likha`, and the stable
  `ext_agent_host_id`. For reconnect, use the selected registration's issued
  client ID and omit `agent_name_hint`; associated retained ID-token/email
  hints may be used only for that selected registration.
- [ ] Start a loopback listener on `127.0.0.1` with an ephemeral available port
  and stable `/auth/callback` path before opening the system browser. Keep the
  exact URI, including port, through each attempt; do not substitute `localhost`.
- [ ] Generate cryptographically random state, nonce, and PKCE verifier per
  attempt. Use S256 and request
  `openid profile email offline_access resource.invoke chatgpt.tokens.use.direct`
  with resource `https://api.openai.com/v1` at
  `https://auth.openai.com/api/accounts/authorize`.
- [ ] Validate state before handling callback errors or exchanging code. A new
  registration must return an issued client ID; reconnect may omit it but must
  not replace it with a different one. Never exchange with or persist
  `dynamic_agent_client` as an issued ID.
- [ ] Exchange the code at
  `https://auth.openai.com/api/accounts/oauth/token` as a public client, using
  the issued ID, resource, verifier, and exact callback URI. A consumed/invalid
  code needs a fresh authorization, not reuse of the code.
- [ ] Verify the ID-token signature against OpenAI's published JWKS via OIDC
  discovery. Validate issuer, audience against the issued ID, expiry, and the
  attempt nonce. Use verified `sub` plus issued client ID for identity; a decoded
  JWT payload or email alone is insufficient. Reject reconnect identity changes.
- [ ] Read granted scopes from the token response. Gate inference on plan and
  resource permission and eligible account access; identity-only authorization
  may retain a disabled registration but cannot activate model use. Explicit
  enable-plan recovery may request consent; routine sign-in must not force it.
- [ ] Keep a pending/new login separate from the active account until validation
  finishes. Reply to the browser callback with neutral receipt/verification
  text, not premature success. Close listeners on success, cancellation, error,
  and timeout; ignore stale asynchronous results.

## 3. Protected accounts, refresh, and sign-out

Work in `internal/providers/keyfile.go` and provider resolution, with an auth
storage interface that keeps `internal/model` independent of `providers`.

- [ ] Preserve API-key records and unrelated configuration while migrating the
  credential format. A legacy Codex token set has no official registration:
  require fresh registration/sign-in and do not refresh or send it to SIWC.
- [ ] Store separate records for `(issued client_id, verified sub)`, including
  validated identity, stable label, issuer, granted scopes, access/refresh/ID
  tokens, and expiry/refresh timing. Same-email workspaces remain separate.
- [ ] Store an explicit active selection. Reconnect replaces only the matching
  registration's credentials; account switching does not rotate credentials
  for usage-limit avoidance or sign out all other accounts.
- [ ] Keep private state at `0700` and credential/host files at `0600` on Unix.
  Use owner-only temporary files and atomic replacement; redact tokens and
  authorization URLs carrying ID-token hints from diagnostics. Keep credentials
  out of repository files, SQLite conversation history, child output, and logs.
- [ ] Serialize rotating refreshes per registration across requests and local
  processes. Acquire the credential-store lock, reload the latest set, refresh
  if still needed, and replace access token, rotating refresh token, granted
  scopes, and expiry together. Surface persistence errors instead of claiming
  restart-safe credentials that were not saved.
- [ ] Refresh with the saved issued ID and resource; omit `scope` to retain the
  grant. Respect expiry and provider-supplied refresh timing rather than assuming
  unlimited renewal of a stale token. Terminal refresh errors invalidate tokens
  and direct the user to reconnect; transient failures retain recoverable state.
- [ ] Stop selected-account requests before sign-out. Attempt form-encoded
  revocation using OIDC discovery's `revocation_endpoint`, refresh token,
  `token_type_hint=refresh_token`, and issued client ID. Bound retries for
  network/5xx failures. Clear local access, refresh, and ID tokens even without
  confirmed remote revocation; retain registration and host metadata and show
  the ChatGPT-settings disconnect warning.

## 4. Public models and Responses transport

Work in `internal/model/client.go`, the existing Responses builder/parser in
`internal/model/codex.go`, and model-picker wiring. Existing filename/symbol
names do not authorize the old Codex protocol.

- [ ] Fetch authenticated `GET https://api.openai.com/v1/models` using the
  selected account's bearer access token. Parse the SIWC `models` array, retain
  `visibility == "list"`, preserve ordering, display `display_name`, and request
  the selected `slug`. Refresh on account changes; do not substitute a curated
  list or assume the API-key provider's `data[].id` envelope.
- [ ] Send stateless HTTP/SSE `POST /v1/responses` with `store: false`,
  `stream: true`, and an `input` array carrying all required conversation
  history. Use `instructions` or developer messages for system instructions;
  omit explicit system message items, `previous_response_id`, and `conversation`.
- [ ] Group Likha's function/custom tools in the supported namespace format (or
  documented `additional_tools` input items). Map calls/results to existing
  tool identities and approvals; do not add hosted tool services or pass
  unsupported flat-tool/Responses `tool_search` configurations.
- [ ] Omit the unsupported preview fields/tools listed in official
  [limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations.md),
  including sampling controls, output caps, background requests, and hosted MCP.
  Preserve Likha's local context checks and child budgets.
- [ ] Accept success only after `response.completed`. Handle `response.failed`,
  `response.incomplete`, explicit errors, malformed data, cancellation, and EOF
  without completion separately. Do not execute partial tool calls.
- [ ] Retain HTTP status, error shape/code/parameter, and request ID safely.
  Handle direct-admission `detail` bodies as well as structured API errors.
  Distinguish eligibility/policy/scope errors, unsupported requests, terminal
  authentication failure, and temporary availability failures.
- [ ] Pause plan requests on `subscription_sharing_usage_limit_exceeded` and
  link to ChatGPT Usage. Do not infer a reset time, retry quota as generic 429,
  switch billing/accounts, or use private usage endpoints/headers as a contract.
- [ ] Treat SIWC usage and cost as unknown unless reported. Do not infer exact
  zero cost from provider identity or apply API-key catalog pricing to shared
  plan use. Optional ChatGPT credits require the user's account-settings opt-in.

## 5. TUI and CLI integration

Work in `internal/tui/setup.go`, `setup_view.go`, `providers.go`,
`models_all.go`, and `internal/app/run.go`.

- [ ] First-run ChatGPT selection and selecting an unconnected `/providers`
  row start browser login once, without a second Enter. Show waiting/cancel
  controls; expose a manual URL only if browser launch fails.
- [ ] After validated authorization, fetch the selected registration's eligible
  models and advance to model selection. Handle empty lists, errors, cancellation,
  stale messages, and account changes without changing a working active login.
- [ ] Add saved-account selection, stable/distinct labels, active indication,
  add-account, reconnect, enable-plan access, and sign-out controls.
- [ ] Add `likha --provider chatgpt --login` for the same browser flow outside
  the TUI. Make `--device-login` return a deprecated/unsupported explanation
  pointing to `--login`; remove device login from help examples.
- [ ] Follow official UI guidance: **Continue with ChatGPT**, a once-only first
  plan-use confirmation, **Using ChatGPT plan**, and **Manage usage** linking to
  `https://chatgpt.com/settings/usage`. Do not imply OpenAI endorsement.
- [ ] Keep API-key setup, provider precedence, agent cancellation, tool reviews,
  and first-use web/MCP consent unchanged. Check SIWC background-use terms against
  session naming and any other background requests; obtain express consent before
  such use rather than treating login as that consent.

## 6. Verification gates

- [ ] Run `go build -o bin/likha ./cmd/likha`, `go test ./...`, and
  `go vet ./...` after integration. Run targeted race checks for token rotation,
  overlapping requests, sign-out, and asynchronous setup/account switching.
- [ ] Exercise real local protocol behavior: PKCE/state/nonce rejection,
  signature and audience failures, scope gates, account isolation, locked atomic
  storage and refresh, launcher failure, browser CLI, model envelope, request
  fields/namespaces, completion-only success, quota errors, and cancellation.
  Record fixture/protocol coverage honestly; it does not prove OpenAI consent,
  a user's entitlement, or live API compatibility.
- [ ] With explicit user approval, an eligible Plus/Pro account, and a real
  browser, run the full live probe: new sign-in, consent, current models, streamed
  text, structured tool calls, approval/rejection, cancellation, reconnect,
  restart/refresh, same-email workspace separation where available, and sign-out.
- [ ] Keep the provider pending-probe until that probe succeeds. Only the parent
  may record integrated local status and CHANGELOG outcomes for this work.
- [ ] Complete the published-binary walkthrough on supported targets before
  `verified (release)`. Do not publish or perform live sign-in as part of these
  documentation edits.
