# Checklist: official Sign in with ChatGPT migration

**Status:** in progress (2026-10-05). Items describe the replacement contract in
[spec.md](spec.md). No live account/browser probe or published-release walkthrough
has run. Old Codex test outcomes do not satisfy these gates.

## Browser-first onboarding and CLI

- [ ] First-run ChatGPT selection opens the system browser once with no API key,
  device code, pasted code, or second launch keypress.
- [ ] Selecting an unconnected ChatGPT row in `/providers` starts the same flow;
  a connected row offers account management.
- [ ] Waiting/cancellation keep the interface usable and prevent stale sign-in
  results from changing the active account.
- [ ] Only browser-launch failure shows the manual URL. The fallback uses the
  same local callback flow rather than a device-code method.
- [ ] The callback page acknowledges verification in progress without announcing
  success before validation finishes.
- [ ] Browser CLI `likha --provider chatgpt --login` works without entering the
  TUI. `--device-login` explains its unsupported/deprecated status and points to
  the browser method without contacting a device endpoint.

## Authorization and account identity

- [ ] OpenAI shows Likha's own application name during new registration; Likha
  does not impersonate Codex, OpenCode, or another client.
- [ ] Invalid state, nonce, signature, issuer, audience, expiry, changed returning
  identity, or changed returning client ID cannot activate or overwrite a login.
- [ ] Declined/identity-only consent does not enable inference. The user can
  explicitly enable plan access later; ineligible entitlement produces guidance
  rather than a consent/retry loop.
- [ ] Account/workspace registrations stay separate, including same-email
  workspaces, with distinct stable labels and a visible active choice.
- [ ] Add-account, saved-account selection, and reconnect preserve other
  registrations and their credentials. Selection does not rotate accounts to
  evade limits.
- [ ] Host identity exists before first authorization and survives restarts,
  account switching, and sign-out. Reconnect reuses the saved registration.

## Local credential lifecycle

- [ ] Credentials remain in private local state (`0700` directory, `0600` files)
  and do not enter the repository, history database, logs, or diagnostics.
- [ ] Overlapping requests/processes do not reuse a rotating refresh token;
  successful refresh saves the matching replacement set atomically.
- [ ] A restarted process uses the saved current credentials. Storage failures
  are visible and do not masquerade as successful persistent refresh.
- [ ] Transient failures preserve recoverable credentials. Terminal refresh or
  confirmed disconnection stops requests and asks the user to reconnect.
- [ ] Sign-out stops selected-account requests, attempts remote revocation, and
  clears local access/refresh/ID tokens even if remote revocation fails.
- [ ] Unconfirmed revocation shows a warning and ChatGPT-settings disconnect
  guidance; registration and host metadata remain for reconnect.
- [ ] Legacy Codex credentials require fresh official sign-in without damaging
  stored API keys, sessions, or unrelated configuration.

## Models, inference, and user control

- [ ] After validated consent, Likha fetches current account-specific eligible
  models from the public API, displays reported names, and refreshes choices on
  account switch. Empty/failed discovery does not substitute curated eligibility.
- [ ] Public stateless streaming Responses requests carry required history and
  supported namespaced tool definitions through Likha's existing loop.
- [ ] Completed response events alone establish success. Failed/incomplete/
  malformed/interrupted streams cannot dispatch tools from partial output.
- [ ] Text, structured tool calls, tool-result fidelity, edit/command reviews,
  and cancellation satisfy the predefined-provider full probe.
- [ ] Scope, entitlement, region/policy, unsupported-capability, quota, and
  temporary errors show appropriate recovery without closing the conversation.
- [ ] Limit errors pause plan requests and direct users to ChatGPT Usage without
  inferring a reset, switching accounts, or falling back to API billing.
- [ ] Shared allowance and credits require the user's authorization. Usage/cost
  remain unknown unless reported; no exact-zero, unlimited, or free-inference
  claim appears. Context usage remains distinct from plan usage.
- [ ] **Continue with ChatGPT**, the once-only plan-use confirmation, **Using
  ChatGPT plan**, and **Manage usage** follow official UI guidance and do not
  imply OpenAI endorsement.
- [ ] Background model requests require express consent consistent with SIWC
  terms; sign-in does not grant new tool permissions or waive child budgets.

## Verification record

- [ ] Integrated build, `go test ./...`, `go vet ./...`, and targeted race checks
  pass; parent records the command/date and actual covered behavior.
- [ ] Local protocol/security tests cover the migration without substituting
  skipped, always-passing, or mock-only checks for functional evidence.
- [ ] README, provider/setup/usage guides, help, and feature docs describe the
  same browser-first public API contract and honest verification status.
- [ ] An explicitly user-approved live probe succeeds with an eligible account
  and real browser. Record actual plan/workspace/model evidence, without tokens.
- [ ] A published-binary walkthrough succeeds on the stated supported targets
  before status becomes `verified (release)`.

Until the parent records migration results, keep status **in progress**. Passing
fixture tests can support **implemented (local)** when integrated behavior is
covered; they cannot establish OAuth success for Plus/Pro, live provider
acceptance, or release verification.
