# Tasks: Steering prompts (type, queue, and interrupt while a run is active)

**Status:** implemented (local) — M1 tasks 1–7 done; task 8 (M2) deferred
pending the `Ctrl+Q` terminal probe. The 2026-10-03 hold-semantics
amendment is implemented (follow-up slice at the end). Ordered; each task
carries its verification. No checkmarks until work starts. Entry point:
`go test ./...` from the project root; `go test -race ./...` before calling
the feature done.

Milestone M1 is the whole user-visible contract (spec.md § M1). M2 is
confirmed as `Ctrl+Q` (spec.md § Resolved decisions) and lands after M1
with the chord probe recorded first.

1. **Steering input in `agent.RunTurn`.** Add the queue input to the turn
   loop: a `steer <-chan string` parameter, a non-blocking drain helper, a
   drain before each `client.Stream` call, and a drain in place of `done`
   when the assistant returns no tool calls (continue the loop instead).
   Each drained message is `ExpandFileReferences`-expanded and appended as
   its own `model.Message{Role: "user"}` to the private history, then
   emitted as `TurnEvent{Kind: "steer", Text: <raw text>, History:
   <post-append copy>}` so the UI can mark delivery and stay consistent.
   Delivery happens strictly after the previous round's tool settlement;
   cancellation paths (`ctx.Err`) never drain. Update the one production
   caller (`tui.go:250`) and every test caller
   (`agent_test.go`, `approval_test.go`, `mentions_test.go`).
   *Verify:* httptest SSE scripts (existing `agent_test.go` pattern) — a
   message sent while round 1 streams appears in round 2's decoded request
   body as a `user` message after the `tool` result, and its `steer` event
   precedes round 2's first `text`; a message queued during a no-tool-call
   response yields another request instead of `done`, with `done` only after
   the queue empties; cancel with a non-empty queue emits `error` and
   delivers nothing; `@file` in a steered message is expanded in the wire
   body while the event carries the raw text.

2. **Live composer + enqueue path in the TUI.** Add queue state to `ui`
   (`queue []string` plus the running turn's `steer chan<- string`,
   capacity 64, pre-fillable for the flush path). Make
   `editable()` = `m.pending == nil`; drop the `working` condition from
   `caretVisible()` (keep pending/keyModal/dialog); drop the `!m.working`
   gates in `syncPopups` so `@`/`/` popups work on a run draft. Rewire the
   Enter path: empty → no-op; plain text while working → enqueue (append a
   `Queued` row, push `m.queue`, non-blocking send on the steer channel,
   clear the draft) leaving `m.history` untouched; `/`-prefixed while
   working → visible refusal entry with the draft kept; `//word` → enqueue
   `word`. The command popup's exact-name Enter already falls through to
   this path (`commandcomplete.go:118-127`).
   *Verify:* rewrite `TestComposerInertWhileWorking`
   (`tui_m2_keys_test.go:91-113`) into a live-while-working test plus a
   still-inert-while-reviewing test; new tests assert the Queued row, queue
   contents, cleared draft, unchanged `m.history`, and no queued text in the
   saved snapshot; refusal keeps the draft and queues nothing; `//x` queues
   `x`; caret renders while working.

3. **Steer event handling and queue visibility.** Add the `"steer"` case to
   the TurnEvent switch (inside the existing `working && RunID` gate):
   adopt `v.History`, flip the first matching `Queued` row to `You`, pop
   `m.queue`, persist; if no queued row matches (flush path), append a
   `You` row so transcript and history stay consistent. Status line: while
   working with a non-empty queue, wide hint renders `· N queued`, narrow
   hint `NQ` (`status_line.go:76-100`). Composer: empty draft during a run
   uses a queueing placeholder (`composer.go:59-66`).
   *Verify:* unit tests for flip/pop/history-adopt/persist; status tests
   for both layouts and the empty-queue no-op; placeholder test.

4. **Turn-end flush.** In the terminal handlers (`done`/`error`, and
   `compacted` for the compaction finish), when `m.queue` is non-empty:
   remove the queued rows and start the next turn immediately with the head
   as the prompt and the rest pre-filled into the fresh steer channel (new
   runID, fresh events). Ctrl+D / `/quit` exits without sending.
   *Verify:* a real httptest-driven turn via the `driveTurn` helper
   (`status_view_test.go:30-41`) queueing mid-stream then cancelling with
   Esc: the captured request bodies show the queued text as the next turn's
   first user message, in order, with no extra key press; error-path test
   (scripted 500) flushes the same way; quit test asserts nothing further is
   sent and nothing persists.

5. **Persistence exclusion.** `persist()` skips role `Queued` alongside
   `Logo` (`tui.go:994-996`); queued text never enters `m.history` before
   the engine delivers it.
   *Verify:* queue a message, force a persist (tool_result or steer event),
   reload from the store, assert no `Queued` entry and no queued text in
   `History`; resume test proves a fresh session cannot display a queue.

6. **Queued row rendering.** Add the `Queued` case to the role style switch
   (`view.go:130-143`): muted foreground on the user band, rendered as
   `Queued: <text>`, flipping to the existing `You` style on delivery.
   *Verify:* rendering assertions for the prefix and style, matrix coverage
   alongside `bands_test.go`, and the no-overflow invariant tests stay
   green.

7. **v1-spec and docs (apply at implementation start).** Amend
   `specs/v1-spec.md`: FR-17 wording per spec.md § Functional changes, add
   FR-21. Amend stale wording elsewhere that assumes Enter is ignored while
   working (`specs/slash-commands/spec.md` table row, `repo-init/checklist`
   item) to the new visible-refusal rule. Update `README.md`
   ("Editing is inert while a turn streams…" and the input section),
   `CHANGELOG.md`, and the `specs/README.md` index row.
   *Verify:* help/README text renders; specs/README row matches status;
   `go test ./...` green.

8. **M2 — send now (`Ctrl+Q`, confirmed).** Probe `Ctrl+Q` delivery on
   Ghostty first (Windows Terminal backlog); bind `Alt+Q` instead only if
   the probe fails, and record the probe here. Grow the steer input to
   carry a force flag, wrap each `client.Stream` call in a per-round child
   context with a watcher `select` so a force request cancels only that
   round's request; deliver the queue immediately after the aborted round
   and continue the same run (settled tool history preserved); defer the
   force to the next safe point while a tool call is executing; remove the
   aborted partial response from the transcript exactly as a cancelled
   stream is removed today (`tui.go:506-508`).
   *Verify:* agent tests abort a blocked SSE response on the force signal
   and assert the following request contains the steered message without a
   new run; a force during a running tool defers to the boundary and never
   cancels the command; TUI test asserts the aborted partial entry is gone
   and the queued rows flip in order.

## Notes

- Do not add a second copy of the queue: `m.queue` is the UI's FIFO mirror;
  the engine's channel is the delivery path. Both are touched only on the
  UI goroutine (engine receives only), so no locking is introduced.
- Approval handling, the review gate, and `m.cancelling` semantics are
  untouched; queueing is simply unreachable while `m.pending != nil`.
- No new dependencies; no session schema, config, or provider changes.

## Follow-up slice (2026-10-03, implemented): hold, never auto-send

User-confirmed amendment after the first real-TUI report: a run end holds
the queue instead of auto-starting the next turn.

- `agent.RunTurn`: `ctx.Err()` guard before drain (b) — a cancelled run
  never consumes a queued prompt.
- `tui.go`: the `steer` case resets the stream buffers (a fresh assistant
  bubble per round) and counts deliveries; steer events are emitted through
  the non-droppable path; `flushQueue` is replaced by `sendHeldQueue`
  (Enter on an empty draft) plus `reconcileQueue` (terminal backstop);
  `done`/`error`/`compacted` hold the queue; idle Esc clears it.
- `status_line.go` / `composer.go`: held-queue count and placeholder.
- Regression tests: `steering_hold_test.go` (error hold, Esc clear,
  reconcile, hints, fresh bubble, pinned merge), rewritten
  `TestSteerE2EQueuedMessageAfterEscHoldsUntilEnter` and
  `TestTuiKeyTerminalEventHoldsQueue`.
