# Checklist: ChatGPT Plus/Pro login (OAuth 2) provider

Observable outcomes, mirrors [spec.md](spec.md). Status words per
[specs/README.md](../README.md) rules.

- [ ] v1-spec.md is amended (FR-02, §2, §3) before any implementation lands.
- [ ] A ChatGPT Plus/Pro subscriber signs in through the TUI with a browser
      (or device flow headlessly) and never types an API key.
- [ ] Conversation turns stream, invoke structured tool calls, and cancel
      mid-stream against the live Codex backend — the full probe from
      predefined-providers, with a real account, before the provider row is
      called accepted.
- [ ] Expired access tokens refresh transparently; refresh failure shows a
      clear sign-in-again error and keeps the TUI usable.
- [ ] OAuth credentials (access, refresh, expiry, account id) persist only in
      the private state directory at 0600 and never enter the repository or
      session DB.
- [ ] No sampling parameters are sent to the Codex backend.
- [ ] Rate-limit windows are distinguishable from failures in the interface.
- [ ] Docs name the backend that receives content, the login flow, and the
      headless alternative.
- [ ] Status of this feature is only ever: planned, in progress,
      implemented (local), or verified (release).

**Locally verified (2026-09-29, automated suite; all observable outcomes above
remain open until a live probe).** The suite `go test ./...` passes and covers:
browser and device login flows against `httptest` issuers
(`oauth_test.go`: full browser login, wrong-state rejection, provider error,
cancel/unknown paths, context deadline, device login/acceptance);
refresh single-flight (`TestOAuthConcurrentStreamsShareOneRefresh`) and the
one-retry 401 path (`TestOAuthUnauthorizedForcesRefreshAndRetries`), plus
expired-token refresh with persistence
(`TestOAuthRefreshesExpiredTokenAndCallsSaver`); Codex Responses wire via
`codex_test.go` (request mapping without sampling params, tool-call stream
parsing, cancellation and malformed-event handling) and
`TestOAuthStreamSendsCodexHeadersAndParsesSSE`; rate-window summary
(`TestUsageFromCodexRateLimitHeaders`, footer wiring in
`TestUsageFooterShowsCodexRateLimits`); app wiring (`TestResolveProviderStoredOAuth`,
`TestSetupOAuth*`, `TestSetupFinishStoresOAuthCredentialForChatGPT`) with the
0600 credential-file permission asserted for `providers.json`. Not covered by
the suite: the in-memory legacy-map-to-typed migration branch of
`providers.json` (implemented in `readCredentials`, no dedicated test), and
every live-backend interaction (real sign-in, a full turn against
`chatgpt.com`, live rate-window headers).
