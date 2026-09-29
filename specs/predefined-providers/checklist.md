# Checklist: Predefined accepted providers (BYOK)

Mirrors the acceptance criteria in [spec.md](spec.md). An item is checked only when its
observable outcome has been exercised, not when code exists.

- [x] Every listed provider's endpoint shape is accepted by one OpenAI-compatible
      client surface (`internal/model`), with provider settings coming from the table,
      not code paths. *(Verified by `TestNewAcceptsListedAndLocalEndpoints` against a
      local protocol server; live hosted services still pending.)*
- [x] Cancellation mid-stream works against the OpenAI-compatible surface and leaves the
      TUI usable for a new prompt. *(Pre-existing cancellation tests pass; per-provider
      live cancellation still pending.)*
- [x] A revoked or wrong key is surfaced by the startup check and again before each agent
      run, with a distinct error message, before any prompt is sent. *(Verified by
      `TestCheckFailureClasses`, integration tests, and an observed TUI smoke against a
      401 server; live-provider revoked keys still pending.)*
- [x] An unlisted OpenAI-compatible endpoint shows the visible unverified warning; a
      listed provider does not. *(Verified in the observed TUI smoke:
      `Provider: Custom endpoint (unverified)`.)*
- [x] API keys are stored only in the private state directory (mode 0600), never in the
      selected repository, never in `sessions.sqlite`. *(Verified by
      `TestResolveProviderRequiresAndStoresHostedKeys`.)*
- [x] Session continuity is unaffected: existing sessions resume regardless of which
      provider is configured afterward. *(Session tests unchanged and passing.)*
- [x] Release documentation states each provider's base URL, key format, probe status,
      and which provider receives conversation content. *(README; probe status marked
      "not yet probed" until live evidence exists.)*
- [x] `go test ./...` and the feature smoke pass before any of the above is described as
      verified. *(Observed: full suite, `-race`, and two TUI smokes — streaming against a
      fake provider and the 401 startup/pre-run failure path.)*
