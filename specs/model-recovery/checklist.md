# Checklist: Model request recovery

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Recovery

- [ ] A stream the provider closes mid-response is retried automatically.
      The run then completes, and no error entry is left behind.
- [ ] A request that fails before any output with 408, 429 (not quota),
      500, 502, 503, 504, 520–524 or 529, or with a connection error, is
      retried. The run completes on success.
- [ ] A request that receives no data for 5 minutes is treated as dropped
      and retried.
- [ ] At most 5 attempts are made per request. At most 3 attempts are made
      when the drops happen after output has streamed.
- [ ] Waits grow roughly 1s → 2s → 4s → 8s and never exceed 30s. A provider
      Retry-After of 60s or less is honored; a longer one fails at once and
      shows when to try again.
- [ ] Authentication, rejected-request (400/404/413/422), quota/billing,
      malformed-stream, TLS and 16 MiB-overflow failures are never retried.
- [ ] Recovery behaves the same on chat-completions providers and on
      ChatGPT sign-in.

## What the user sees

- [ ] Each retry shows one recovery row with the cause, attempt/limit, a
      live countdown and "esc to stop". It stays legible in ASCII mode and
      without color.
- [ ] Reasoning or text from a dropped attempt stays visible, labelled
      interrupted, and never merges with the next attempt's output.
- [ ] The status line says Likha is retrying while it waits.
- [ ] A turn that succeeded after retries shows the retry count in its
      footer.
- [ ] When recovery gives up, the error names the provider, the cause in
      plain words and the attempt count, and suggests `/retry`. The old
      "ended before [DONE] or exceeded response limit" text no longer
      appears.
- [ ] Esc during a wait cancels the run within one second, and the run ends
      as cancelled, not as an error.

## `/retry`

- [ ] After a failed turn, `/retry` resends the failed request with all
      earlier tool results and no new user message. It works after
      resuming the session.
- [ ] `/retry` never reruns a tool, re-applies an edit or reuses an
      approval.
- [ ] `/retry` after a successful or cancelled turn, or with no turn yet,
      shows "Nothing to retry" and sends nothing. While a run is active it
      is refused like other reserved commands.

## Other callers

- [ ] Explore children and profile tasks recover under the same rules.
      Their retry notes appear in `/agents`, and each attempt counts toward
      the 32-request and five-minute child budgets.
- [ ] Compaction recovers under the same rules, and its summary never
      contains text from a dropped attempt.
- [ ] Session auto-naming makes exactly one request.

## Honesty and verification

- [ ] Usage and spend count only provider-reported usage. The docs state
      that failed attempts may still be billed.
- [ ] The `go test ./...` scenarios use real HTTP (httptest) for each
      acceptance criterion. No skipped or always-passing test backs a claim.
- [ ] The headless fault-injection probe (drop, 503, stall) is recorded in
      [context.md](context.md).
- [ ] The live probe on `opencode-go`/`glm-5.3-flash` and on ChatGPT sign-in
      (user-run) is recorded before the status reaches verified (release).
