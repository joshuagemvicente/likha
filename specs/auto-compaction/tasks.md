# Tasks: Automatic compaction

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green. Nothing here changes `/compact`.

Fixed values, defined once in `internal/agent`:

| Name | Value |
| --- | --- |
| trigger | estimate ≥ 85% of a known window (`tokens*100 >= 85*window`) |
| attempts per run | one stand-down after a failed or insufficient compaction (D5, D6) |
| automatic focus | "the task in progress: what is already done and what remains" |

## M1 — Decide and compact inside the agent loop (`internal/agent`)

- [ ] **T1. Run options.** Add `ContextWindow int64` and `AutoCompact bool`
      to `RunOptions`. Zero window or `false` disables the feature. Explore
      and profile options never set them.
      *Verify:* compile; existing tests unchanged.
- [ ] **T2. Trigger predicate.** `shouldAutoCompact(tokens int64, ok bool,
      window int64) bool` — false when `!ok` or `window <= 0`.
      *Verify:* table test: 84%, 85%, 100%, unknown estimate, zero window.
- [ ] **T3. Keep-verbatim compaction.** `compactRun(ctx, client, history,
      runStart int)` summarizes `history` with the automatic focus and
      returns `[summary developer msg] + user messages in history[runStart:]
      + developer continuation note` ("Work already done in this run is in
      the summary above; continue from it without repeating completed
      steps."). Returns a sentinel error when `history[:]` holds nothing but
      the kept messages (nothing to summarize).
      *Verify:* unit test with a fake model: request body carries the full
      history + instruction + focus; the replacement order is exact; the
      input slice is untouched.
- [ ] **T4. Wire into `RunTurnWithOptions`.** After the `context` estimate,
      if the predicate fires and the run has not stood down: emit
      `compacting`, call `compactRun`, then
      - success: replace `history`, rebuild `request`, re-estimate, emit
        `auto_compacted` (summary, history, usage, percent, window) and a
        fresh `context` event; if the new estimate still triggers, emit the
        stand-down `notice` and set `stoodDown`;
      - nothing to summarize: stand-down `notice`, send unchanged;
      - error with `ctx.Err() != nil`: `fail(err)` (cancellation, D7);
      - other error: `notice` with the error text, send the original
        request, set `stoodDown` (D6).
      Usage from the summarize call is emitted as a `usage` event either way.
      *Verify:* agent tests with an httptest provider for each branch,
      asserting event order and the exact second request body.

## M2 — Show it and keep the run alive (`internal/tui`)

- [ ] **T5. Pass values in.** `tool_wiring.go` sets `ContextWindow` from
      `m.contextWindow` when `m.contextWindowKnown`, and `AutoCompact` from
      config (T8).
- [ ] **T6. Handle `compacting` / `auto_compacted`.** `compacting` sets the
      status to `Compacting…` without ending the run. `auto_compacted`
      swaps `m.history`, recalculates context, appends the automatic
      divider entry (spec point 5 wording), persists, marks the turn for
      the `· compacted` footer, and rebases `m.priorLen` and `m.turnSent`
      so `reconcileQueue` and the unexecuted-call backstop count from the
      kept user messages. It does **not** clear `m.steer`/`m.steering`.
      *Verify:* TUI test: a run that triggers, compacts, and finishes; a
      steering prompt queued during compaction is delivered in the same run;
      `finishRun` leaves no stray queued rows.
- [ ] **T7. Footer.** Add `· compacted` to the turn footer when the run
      compacted at least once.
      *Verify:* footer render test.

## M3 — Opt-out and docs

- [ ] **T8. Config key.** `StoredProviderConfig.AutoCompact *bool`
      (`auto_compact`), nil = on, preserved across saves like `Web`.
      *Verify:* round-trip test; saving a theme keeps `auto_compact: false`.
- [ ] **T9. Docs.** `docs/usage.md` compaction section and config paragraph;
      pointer from `specs/conversation-compaction/spec.md` decision 1.
- [ ] **T10. CHANGELOG** entry under Unreleased stating what was verified.

## W — Walkthrough (user)

- [ ] **W1.** Real terminal, a provider with a known window and a
      `context_windows` override set small (e.g. 20000) to reach 85%
      quickly: watch the divider, footer, status line, Esc during
      compaction, and resume.
