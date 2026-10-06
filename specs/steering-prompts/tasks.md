# Tasks: Steering prompts (type, queue, and interrupt while a run is active)

**Status:** M1 implemented (local) — tasks 1–7 done, plus the 2026-10-03
hold-semantics follow-up slice at the end. M2 (`Ctrl+Enter` steer now)
implemented (local) 2026-10-05, live probe outstanding; task 8 below is
replaced by the M2 slice at the end of this file. Ordered; each task carries its verification. No
checkmarks until work starts. Entry point: `go test ./...` from the project
root; `go test -race ./...` before calling the feature done.

Milestone M1 is the whole user-visible contract (spec.md § M1). M2 is
steer now on `Ctrl+Enter` (spec.md § M2, re-specified 2026-10-05).

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

8. ~~**M2 — send now (`Ctrl+Q`).**~~ Superseded 2026-10-05; never
   implemented. See "M2 slice: steer now" at the end of this file.

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

## M2 slice: steer now (`Ctrl+Enter`) — implemented (local) 2026-10-05

M2.1–M2.6 are done and green under `go test ./...` and `go test -race` for
agent, model, and tui. Deviations recorded during implementation: partial
usage is never available (both transports report usage only in the final
frame), so `stream_interrupted.Usage` is always nil and spend turns
approximate; the interruption note is inserted before trailing `Queued:`
rows so the order reads partial → note → `You:`; the status line carries no
key hint (FR-12). M2.7 (live probe) is outstanding.

Contract: spec.md § M2. Ordered; each task carries its verification. The
v1-spec FR-17/FR-21 amendments and the tool-rendering-terminal-keys note are
already written (marked planned); flip them when the slice is verified.

M2.1. **Steer-now signal in the turn loop.** `RunOptions` gains
   `SteerNow <-chan struct{}` (capacity 1; the UI sends without blocking).
   In `RunTurnWithOptions` (`internal/agent/turn_execution.go`), before
   each round's `drainSteer`, discard any stale signal still buffered (a
   press during tool execution is delivered by the ordinary boundary
   drain). Wrap `client.StreamUsage` in a per-round
   `context.WithCancel(ctx)` plus a watcher goroutine that selects on
   `SteerNow` and the round context's `Done`, records "steered", and
   cancels only the round. Wrap `onText` to accumulate the round's visible
   text. When the stream returns an error with `ctx.Err() == nil` and
   "steered" set: append `model.Message{Role: "assistant", Content:
   partial}` only when `partial` is non-empty (no `ToolCalls`, no
   `Reasoning`), emit `TurnEvent{Kind: "stream_interrupted", History:
   <copy>}`, and `continue` so the next round's drain delivers the queue.
   Run cancellation always takes the `fail` path, even when a steer is
   also set. The watcher exits when the round ends, so it never leaks
   across rounds.
   *Verify:* agent tests with the httptest SSE pattern from
   `agent_test.go`, using a handler that streams text, then blocks:
   (a) a steer plus signal mid-stream produces a second request in the same
   run whose body ends with the partial assistant text and then the steered
   `user` message, and no `done` or `error` between them; (b) a signal with
   only reasoning streamed adds no assistant message; (c) a stream that had
   begun a tool call has no tool call in the next request and no tool
   executes; (d) a signal sent while a tool runs does not cancel it, and
   the message is delivered at the boundary; (e) a run cancel racing a
   steer emits `error`; (f) a signal with no queued text never aborts a
   later round (stale-signal discard); (g) `go test -race`.

M2.2. **Clean cancellation on every transport.** Confirm that a cancelled
   context ends `client.stream` promptly for chat-completions
   (`consumeStream` ctx checks, `internal/model/client.go`) and for the
   Codex path (`streamCodex` and `consumeCodexStreamDetailed`,
   `internal/model/codex.go`) with no goroutine left reading the body.
   Find out whether usage seen before the cut (an early `message_start` /
   `response.created` usage frame) can be returned on error; if so,
   return it so the interrupted round counts it.
   *Verify:* model tests cancel mid-stream on both paths (extend the
   `client_test.go` cancel-in-`onText` precedent) and assert prompt
   return, `context.Canceled`, and any partial usage.

M2.3. **Usage on interrupt.** `stream_interrupted` carries the round's
   usage when M2.2 found it (`Usage`, `UsageKnown`). The TUI adds known
   usage through `addRequestUsage`; when it is unknown, it sets
   `spendEstimated` so spend renders with `~` and invents no number.
   *Verify:* TUI tests for the known and unknown cases.

M2.4. **Chord routing in the TUI.** In `internal/tui/tui.go` `Update`,
   split the `case "ctrl+j", "alt+enter", "ctrl+enter", "shift+enter"`
   arm: `alt+enter` always inserts a newline (Esc-prefix Return is
   unreachable during a run, where Esc cancels at once); the LF family steers when
   `m.working && m.editable()` with no popup open, and inserts a newline
   otherwise. Extract Enter's working-branch text rules (flatten, `/`
   refusal, `//` escape, prompt-history append) into one helper used by
   both Enter and steer now, so the two can never drift. `steerNow`
   enqueues the draft through the existing `enqueue` (Queued row, `m.queue`,
   steer channel), then sends on `m.steerNow` without blocking; an empty
   draft with a non-empty queue only sends the signal; empty draft and
   empty queue is a no-op. Create `m.steerNow` (capacity 1) in
   `startTurnDisplay` next to `m.steer`, nil it wherever `m.steer` is
   nilled, and pass it as `options.SteerNow` in `toolRunOptions`.
   Compaction runs have no steer channel, so the chord falls through to
   queue-and-hold. Leave `ask_view.go`'s own newline handling unchanged.
   *Verify:* key tests — working plus draft steers (Queued row, channel
   send, signal sent); idle inserts a newline; `alt+enter` inserts a newline
   while working; pending approval inert; popup
   open inert; `/x` refused with the draft kept; `//x` steers `x`; empty
   draft plus queue sends the signal only; compaction queues and holds.
   Update the newline tests in `tui_m2_keys_test.go` that assume
   `ctrl+j` always inserts a newline while working.

M2.5. **Interrupted-response rendering.** Add a `stream_interrupted` case
   to the TurnEvent switch (gate it like `steer`, on the non-droppable
   event path): adopt `v.History`; keep the streaming assistant entry when
   it has text, drop it when empty (with `adjustToolRecordsAfterRemoval`);
   append a muted `Likha` entry `Response interrupted to deliver your
   message.`; reset `m.streaming`/`m.streamBuf`; `closeReasoning`;
   persist. Session entries are role/content only
   (`internal/session/session.go`), so the note is its own entry and
   survives resume with no schema change. The existing `steer` case then
   flips the rows to `You:` and opens a fresh bubble. Status: `Steering…`
   from the press until the next `text`/`reasoning`; `Steer waits for the
   running tool` when the press lands while a tool record is live.
   *Verify:* TUI tests drive a real turn (`driveTurn`,
   `status_view_test.go`) with a blocking SSE handler: the partial
   assistant entry, then the note, then `You:`, then a fresh assistant
   bubble; the turn footer appears once, at the real end; the persisted
   snapshot holds the partial text and note; status strings for both
   phases.

M2.6. **Hints and docs.** The working placeholder (`composer.go`) names
   both keys (`Type to queue… (Enter queues · Ctrl+Enter steers now)`).
   The status line carries no key hint: at 130 columns it pushed the
   session title and git segments out and broke FR-12's model-identity
   rule. Update README's
   input section and the keys guide under `docs/`, and add a CHANGELOG
   **Unreleased** entry stating what was verified (automated vs live
   probe).
   *Verify:* placeholder and hint tests at 40/56/80 columns; the
   no-overflow invariant tests stay green.

M2.7. **Live probe (user).** On Ghostty: mid-stream Ctrl+Enter and
   Shift+Enter steer; Alt+Return inserts a newline in a running draft;
   idle, all three insert a newline. Record the result in a table here.
   Until it is recorded, the CHANGELOG says the probe is outstanding, and
   the status stays implemented (local).

### M2 notes

- One extra one-way channel (`SteerNow`) beside `steer`; both are written
  only on the UI goroutine and read only by the turn loop. No locks, no
  second queue, and the UI never edits the running turn's history.
- The stopped round counts toward the round-cap checkpoint like any other
  round (`continue` increments `round`); no checkpoint semantics change.
- The approval path is untouched: the chord is unreachable while
  `m.pending != nil`, and a pending approval's tool is "executing" for M2's
  purposes, so it is never aborted.
