# Tasks: Model request recovery

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green and can ship on its own. M1 alone fixes the
misleading error from the 2026-10-05 report.

Fixed policy values (spec decisions 3–6), defined once in `internal/model`:

| Name | Value |
| --- | --- |
| max attempts per request | 5 |
| max retries after a mid-stream drop | 2 (3 attempts) |
| backoff | 1s · 2^(retry−1), ±25% jitter, each wait ≤ 30s |
| Retry-After honored up to | 60s (longer → fail now, show reset) |
| stall timeout (no bytes) | 300s |

## M1 — Classify failures and say what happened (`internal/model`)

- [ ] **T1. Typed stream error.** Add `StreamError` with a `Kind`, HTTP
      status, bounded provider detail, `RetryAfter`/`ResetAt`, `OutputSeen`,
      `Attempts`, `Elapsed`, and `Cause`. Its methods are `Error()` (plain
      wording per spec point 9, with no provider name, since the TUI adds
      that), `Retryable()` and `Unwrap()`. Kinds: connect, drop-before-output,
      drop-mid-stream, stalled, rate-limited, overloaded (5xx/529),
      provider-error (retryable in-stream), quota, auth, rejected (other
      4xx), malformed, too-large, TLS. Auth errors must still satisfy
      `errors.Is(err, ErrUnauthorized)`.
      *Verify:* table test over every kind for `Retryable()` and wording.
- [ ] **T2. Tell the 16 MiB cap apart from a closed stream.** Replace
      `io.LimitReader(resp.Body, maxResponseBytes+1)` on both wire paths
      with a counting reader. Exceeding the cap gives the too-large kind; EOF
      without `[DONE]` or a terminal event gives a drop. Remove the message
      "ended before [DONE] or exceeded response limit".
      *Verify:* httptest server streaming 16 MiB+1 → too-large; server
      closing after one delta → drop-mid-stream; closing after headers →
      drop-before-output.
- [ ] **T3. Track whether output was seen.** In `consumeStream` and
      `consumeCodexStreamDetailed`, set `OutputSeen` once any text,
      reasoning or tool-call fragment has been parsed. A role-only or
      usage-only chunk does not count.
      *Verify:* unit tests on both consumers.
- [ ] **T4. Classify HTTP statuses.**
      - Retryable: 408, 429, 500, 502, 503, 504, 520–524, 529.
      - Not retryable: 400, 401, 403, 404, 413, 422, and all other statuses.
      - A 429 whose body names a usage, quota, credit or billing limit
        (`insufficient_quota`, `usage_limit`, `quota`, `billing`, `credit`,
        `spend`) is the quota kind and is not retried.
      - Parse `retry-after-ms`, then `retry-after` (seconds or HTTP date).
      - Transport errors: `x509`/TLS errors are not retried; other dial,
        reset, EOF or timeout errors are the connect kind.
      *Verify:* table test with recorded bodies from OpenAI, Anthropic's
      compatible endpoint, OpenRouter and Codex; Retry-After in both forms.
- [ ] **T5. Classify in-stream errors.**
      - Chat SSE `error` objects (OpenRouter `finish_reason:"error"` shape
        included) with a numeric code of 429, 5xx or 529, or a type or code
        containing `overloaded`, `server_error`, `rate_limit` or `timeout`,
        are the provider-error kind (retryable). Others stay malformed or
        rejected.
      - Codex `response.failed`/`error` with `server_error` or
        `rate_limit_exceeded` are retryable; `usage_limit_reached` and
        `insufficient_quota` are the quota kind.
      - `event: error` without a payload stays non-retryable.
      *Verify:* fixture streams per shape (see research.md § Wire formats).

## M2 — Retry loop and stall watchdog (`internal/model`)

- [ ] **T6. Split one attempt from the loop.** Move today's bodies of
      `stream` (chat) and `streamCodex` into per-attempt functions. The
      existing corrective retries (the `stream_options` fallback, Codex's
      single forced refresh on 401) stay inside one attempt and do not use
      the transient budget. Under `ForkForTask` they still reserve requests,
      as today.
- [ ] **T7. Retry policy.** `Client.stream` loops over attempts:
      - Stop on non-retryable errors and on parent-context cancellation.
      - Enforce 5 attempts in total, and at most 2 retries after an
        output-seen drop.
      - Wait the backoff, or Retry-After when given; a Retry-After over 60s
        fails immediately with `ResetAt`.
      - Waits use `select` on `ctx.Done()`, so Esc ends them at once.
      - The final error carries `Attempts` and the total `Elapsed`.
      - Keep the policy in an unexported `retryPolicy` field with a
        test-injectable clock and sleep. `New`, `NewOAuth` and `Fork` copy it.
      *Verify:* httptest scenarios from the spec's acceptance list (503 →
      200; three mid-stream drops → error "3 attempts"; five pre-output
      failures → "5 attempts"; Retry-After 10s honored and 120s fails fast;
      cancel during wait returns `context.Canceled` within 1s) on both the
      chat and the Codex path.
- [ ] **T8. Stall watchdog.** For each attempt, create
      `context.WithCancelCause`. Arm a timer before `Do` and reset it on
      every body `Read` through a wrapping reader. When it fires, cancel with
      `errStalled`; if the parent context is still live, that becomes the
      stalled kind. Make the timeout injectable for tests.
      *Verify:* httptest server that sends headers then sleeps, with a 50ms
      test timeout, on both paths; a server that sends an SSE comment every
      20ms is never stalled.
- [ ] **T9. Retry observer and opt-out.** Add
      `model.WithRetryObserver(ctx, func(RetryNotice))` and
      `model.WithoutRetry(ctx)`. `RetryNotice` carries the failed attempt's
      `*StreamError`, the next attempt number, the limit and the wait. The
      observer runs before each wait, synchronously, on the streaming
      goroutine. A context value keeps the five `Stream`/`StreamUsage`
      callers and their tests unchanged.
      *Verify:* the observer sees exactly one notice per retry, in order;
      `WithoutRetry` makes exactly one request.

## M3 — Agent callers (`internal/agent`)

- [ ] **T10. Main turn.** `RunTurnWithOptions` attaches an observer that
      emits `TurnEvent{Kind: "retry", Retry: &RetryInfo{…}}` with the
      attempt, limit, wait, cause wording and `OutputSeen`. Steering is not
      drained between attempts (spec point 7).
      *Verify:* agent test with a fake server: drop, then success, gives
      events text… → retry → text… → done; history holds only the
      successful assistant message.
- [ ] **T11. Explore children and profile tasks.** In `explore_loop.go` and
      `profile_runner.go`, the observer must **reset the `partial`
      accumulator**. Otherwise attempt 1's text is concatenated with attempt
      2's. It records `node.Warn("Retrying 2/3: <cause>")` so the line shows
      in `/agents`. Budget and time accounting need no change, because each
      attempt is already a gated POST and the child deadline covers waits.
      *Verify:* child test: drop then success → one warning, clean
      transcript, 2 requests counted; 32-request budget exhausted during
      recovery → limited result naming the cause.
- [ ] **T12. Compaction and naming.** Compaction keeps the policy, and its
      `onText` consumer resets on a retry notice. `sessionname.go` wraps its
      context in `model.WithoutRetry`.
      *Verify:* naming test asserts one request against a failing server.

## M4 — Transcript and status (`internal/tui`, `internal/transcript`)

- [ ] **T13. Recovery row.** Add `transcript.RetryRole` entries holding the
      cause, attempt, limit and wait (JSON, versioned like Turn entries).
      While the wait is live, render a countdown using the existing activity
      tick. After that, render the static text. Keep the row ASCII-legible.
      *Verify:* view test at 40 and 120 columns, with ASCII glyphs.
- [ ] **T14. Interrupted blocks.** On a `retry` event, close the open
      reasoning or assistant stream (`closeReasoning`, `streaming = -1`,
      `streamBuf.Reset()`) and leave the entry in place. Render a Reasoning
      or Assistant entry as interrupted when it is directly followed by a
      Retry entry or by the run's final model-error entry. This is a
      render-time rule, so the persisted entry schema does not change.
      Change `finishRun` so it no longer deletes the streaming entry on
      error (spec point 10).
      *Verify:* the persisted snapshot round-trips; resumed sessions render
      the label; no model-history change.
- [ ] **T15. Status line and footer.** Set status to "Retrying" during waits
      and retried attempts. Add `Retries int` and `Failed bool` (omitempty)
      to `TurnInfo`/`turnJSON`, and render `· N retry/retries`. Old Turn
      entries still decode.
      *Verify:* footer encode/decode test with legacy JSON.
- [ ] **T16. Final error wording.** The Error entry reads
      `<Provider display name> <cause wording> (<N> attempts). Type /retry to
      try again.` Omit `/retry` for auth errors and point to `/providers`
      instead.
      *Verify:* view test per kind.

## M5 — `/retry`

- [ ] **T17. Command.** Add `/retry` to `reservedCommands`, the help text
      and `handleCommand`. Eligibility is that the last Turn entry has
      `Failed: true` and no You entry follows it, derived from persisted
      entries, so it works after resume. Otherwise show the "Nothing to
      retry" notice. While a run is active, FR-21's refusal applies.
- [ ] **T18. Resend without a prompt.** Add `RunOptions.Resend`:
      `RunTurnWithOptions` uses `prior` as-is and appends no user message.
      If the failed round had appended an assistant message without tool
      calls (the `validateToolCallIDs` path), drop that message so the
      resent request equals the failed one. The resend starts a new turn
      footer.
      *Verify:* tool-result rounds before the failure are resent unchanged,
      no tool reruns, and no approval is reused (agent and TUI tests).

## M6 — Docs, contract, verification

- [ ] **T19.** `docs/usage.md`: add a "When a provider fails" section with
      the limits table, `/retry`, and the usage-honesty note (spec point 16).
      Add `/retry` to the slash-command list. `docs/providers.md`: one line
      linking it.
- [ ] **T20.** `CHANGELOG.md` Unreleased entry per milestone, stating what
      was verified. Update the status in `specs/README.md` and FR-36's
      status in `v1-spec.md`.
- [ ] **T21. Headless probe.** A local fault-injecting SSE proxy in front of
      a real provider: cut mid-stream, return 503, stall. Record the
      transcripts in [context.md](context.md).
- [ ] **T22. Live probe (user-run).** Rerun the original
      `opencode-go`/`glm-5.3-flash` long-thinking turn and one ChatGPT-
      sign-in turn. Record the outcomes in [context.md](context.md).
