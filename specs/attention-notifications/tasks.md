# Tasks: Attention notifications

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green and can ship on its own. Drafting defaults D1–D9
were adopted as written when the user asked for implementation (2026-10-07).

**Status:** M1–M3 and T12 implemented (local) 2026-10-07, automated checks
only; W1 (live walkthrough) outstanding.

Implementation notes (2026-10-07):

- The locked writer (`internal/app/terminal_output.go`) keeps `Read`,
  `Close` (a no-op) and `Fd` on a terminal file, because Bubble Tea finds
  the TTY through a `term.File` assertion; a plain wrapper would lose
  terminal-size detection and resize handling.
- `tea.WithReportFocus()` is added only when at least one signal is on, so
  a user with both off sees no terminal-mode change.
- A `/compact` run is a run too: it records its start, and a finished
  compaction of 10s or more signals `run finished`.
- The timed-out refusal says `1 minute` for a one-minute limit and
  `N minutes` otherwise.

Fixed values:

| Name | Value |
| --- | --- |
| run-end minimum duration | 10s (D4) |
| auto-skip range | 1–120 minutes; 0 or absent = off |
| notification text bound | 128 bytes after cleaning |

## M1 — Settings and the signal writer

- [x] **T1. Stored settings.** Add `StoredAttentionConfig` to
      `internal/providers/config.go` (`Bell`, `Desktop` as default-on
      `*bool`, `AskSkipAfterMinutes int`) and an `Attention` field on
      `StoredProviderConfig`. Out-of-range minutes load as off with a
      visible notice at launch, not an error.
      *Verify:* table test over absent, `false`, `true`, 0, 1, 120, 121, -1;
      saving a theme keeps the section byte-equal.
- [x] **T2. Attention writer.** New small type in `internal/tui` (or a leaf
      package) with `Signal(event)` that builds BEL and/or
      `ESC ] 9 ; Likha · <folder>: <event> BEL`, cleans control bytes,
      bounds length, and wraps OSC 9 for tmux when `TMUX` is set (D7).
      It writes through an injected `io.Writer`.
      *Verify:* golden byte tests for each event, each on/off combination,
      a folder name containing ESC/BEL/`;`, and the tmux wrap.
- [x] **T3. Frame-safe output.** In `internal/app/run.go`, wrap `stdout` in
      a mutex-guarded writer passed to `tea.WithOutput`, hand the TUI a
      narrow `attentionSink` interface over the same lock, and add
      `tea.WithReportFocus()`.
      *Verify:* race test writing frames and signals concurrently; every
      signal lands between whole frame writes. `go test -race ./internal/app/`.

## M2 — Fire the signals

- [x] **T4. Focus tracking.** Handle `tea.FocusMsg`/`tea.BlurMsg` in
      `Update` (sole dispatcher; keep the delegation order verbatim) and
      store `focus: unknown|focused|blurred`. Focused suppresses (D3).
      *Verify:* TUI test: focused → no bytes; blurred → bytes; unknown →
      bytes.
- [x] **T5. Review and question events.** Signal once after
      `m.pending` is set in the `"approval"` case, after `openConsent`, and
      after a successful `openAskQuestion` (not when it refuses). Track the
      open interaction so a repaint or page move never re-signals.
      *Verify:* TUI tests for edit, command, MCP, consent and questionnaire
      paging; one signal each.
- [x] **T6. Run end.** Record run start where `m.working` becomes true;
      in `finishRun`, signal `run finished`/`run failed` when not
      cancelling and the run lasted ≥ 10s (D4, D5). Inject the clock.
      *Verify:* fake-clock tests for 3s, 12s, error and cancelled runs.

## M3 — Question auto-skip

- [x] **T7. Timer message.** When a question opens with a limit set,
      schedule a one-second tick carrying the ask `generation`. Ignore ticks
      whose generation no longer matches (answered, skipped, cancelled,
      newer question). The deadline starts at open (spec 15).
      *Verify:* tests that a stale tick after answer/cancel/new question
      delivers nothing.
- [x] **T8. Countdown line and first-key stop.** Render the muted
      countdown line in the ask view (ASCII-safe). In `handleAskKey`, the
      first key clears the deadline (D8) before normal handling.
      *Verify:* view golden at 80 and 40 columns; ↓ then waiting past the
      limit keeps the dialog open.
- [x] **T9. Timed-out result.** On expiry call `answerAsk` with
      `AskAnswer{Skipped: true, TimedOut: <limit>}` and the note
      `question skipped (timed out after N min)`. In
      `internal/tools/ask_user.go`, a timed-out skip refuses with
      `The question was skipped automatically: no answer within N minutes.`
      for both forms.
      *Verify:* tools table test for both forms; TUI test that the result
      reaches the original call ID exactly once.
- [x] **T10. Resume.** Confirm `reportUnansweredQuestions` treats the
      timed-out note as skipped.
      *Verify:* session round-trip test: no "interrupted" note, no reopen.
- [x] **T11. Approvals unaffected.** A test that an approval review and a
      consent stay open past a 1-minute limit with a fake clock.

## M4 — Docs and walkthrough

- [x] **T12. Docs.** `docs/usage.md` (`attention` section), `docs/tools.md`
      (`ask_user` auto-skip), cross-links in ask-user and permission-ui
      specs, CHANGELOG entry stating what was verified.
- [ ] **W1. Live walkthrough** (user): Ghostty, iTerm2 and one other
      terminal, inside and outside tmux; record which show the desktop
      notification in [context.md](context.md).
