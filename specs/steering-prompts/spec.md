# Feature: Steering prompts (type, queue, and interrupt while a run is active)

**Status:** implemented (local) — M1 automated suite green; real-TUI
walkthrough outstanding. M2 (`Ctrl+Q` send-now) is not implemented; its
terminal probe is the gate.

## Context

Lisa freezes its prompt input for the whole duration of a turn. `editable()`
is `!m.working && m.pending == nil` (`internal/tui/tui.go:919-921`), the Enter
key returns early while `working` (`tui.go:876-878`), and the caret is hidden
(`internal/tui/scroll.go:106-111`). A user who thinks of a correction
mid-stream cannot even compose it. `agent.RunTurn` makes the freeze
structural: it owns a private history built from `prior` plus one prompt
(`internal/agent/agent.go:47-49`) and has no input point after it starts; the
UI replaces `m.history` wholesale from `TurnEvent.History`
(`tui.go:481-500`).

Every reference agent keeps the input live while a run is active; they differ
in what a submitted draft does (research below). Lisa takes the
Enter-queues shape, with delivery inside the running turn: the queue is
visible, reorderable by waiting, and never aborts tool execution silently.

## Research findings (recorded 2026-10-02)

**Claude Code** (`code.claude.com/docs/en/interactive-mode`, fetched
2026-10-02):

- Type a message and press Enter while Claude is working: it is *queued*,
  listed in the conversation in gray until sent.
- "Messages: if you queue a message while Claude is running tool calls,
  Claude Code passes it to Claude as soon as those tool calls finish, within
  the same turn. When the turn ends with messages still queued, they go out
  without another key press, in the order you typed them."
- `Ctrl+Enter` (or `Ctrl+X Ctrl+S`) is *send now*: it interrupts a
  response-only turn (or backgrounds backgroundable work) and sends the
  queue immediately. `Esc` interrupts the turn and sends queued messages
  right away. Up-arrow from the first input row takes queued messages back.

**OpenCode** (`opencode.ai/docs/keybinds`, `anomalyco/opencode` CONTEXT.md,
fetched 2026-10-02):

- Input is independent of the running turn (`input_submit: return`;
  `input_newline: shift+return,ctrl+return,alt+return,ctrl+j`;
  `session_interrupt: escape`).
- Two delivery classes: "Steering prompts promote at the next **Safe
  Provider-Turn Boundary** while the current **Session Drain** still requires
  continuation"; "A queued prompt does not promote while the current Session
  Drain requires continuation. The runner promotes one queued prompt when the
  Session would otherwise become idle." A queued prompt is admitted but not
  model-visible until promotion.

**OMP** (omp docs `keybindings.md`, `extensions.md`, `agent-hub.md`,
`provider-compat-reference.md`, fetched 2026-10-02):

- `app.message.followUp` = `Ctrl+Q`, `Ctrl+Enter`: "Queue a follow-up
  message"; `app.message.dequeue` = `Alt+Up`, `Shift+Up`: "Dequeue a queued
  message back into the editor".
- Delivery modes: `steer` (default; interrupts the current run), `followUp`
  (queued to run after the current run), `aside` (inject at the next step
  boundary while a run is live). A steer aborts remaining tool execution but
  preserves already-produced tool results.
- The Agent Hub input steers a running subagent on Enter. For capable
  transports (GPT-6 family Codex WebSocket) OMP can send a native
  `response.steer` mid-response (`supportsSteering`).

**Decisions this forces for Lisa:**

- Enter = queue (Claude Code / OpenCode shape). OMP's plain-Enter-steers is
  not copied: Ctrl+Return is already Lisa's newline chord (user-confirmed
  2026-09-30, `specs/tool-rendering-terminal-keys/`), and OMP's
  Ctrl+Q/Ctrl+Enter followUp chord is therefore unavailable for queueing.
- Delivery at provider-call boundaries inside the same run = OpenCode's safe
  boundary plus Claude Code's "as soon as those tool calls finish". Turn
  end diverges (amended 2026-10-03): Lisa holds the queue for an explicit
  Enter instead of Claude Code's auto-flush.
- No provider-wire steering (`response.steer`-style): Lisa's client has one
  request/response `Stream` call per round (`internal/model/client.go:344`);
  a queued message attaches to the next request.

## User-visible behavior

### M1 — live composer, queue, in-run delivery

1. **Composer stays live during a run.** While a run is active and no
   approval is pending, the draft accepts text, paste, kills, word motion,
   newline chords, and the `@`/`/` completion popups exactly as when idle;
   the block caret is visible. While an approval is pending, editing is
   inert and the caret hidden, exactly as today.
2. **Enter queues.** With a non-empty plain draft while a run is active,
   Enter turns the draft into a queued message: a `Queued:` row appears in
   the conversation (muted on the user band), the draft clears, and the run
   is untouched. Queue order is submission order.
3. **Commands stay inactive.** A draft beginning with `/` (including a
   completed command name from the popup) is refused while a run is active
   with a visible message and the draft kept; nothing is sent and no command
   runs. `//word` keeps its escape: it queues the literal prompt `word`.
4. **Delivery inside the same run.** A queued message reaches the model at
   the next safe point: after the current tool calls settle and before the
   next model request, or in place of ending the turn when the model has
   nothing further to run. Delivery order is queue order. Each delivered
   message flips its row from `Queued:` to `You:` at the moment it is
   appended to the conversation history.
5. **A run ends only with an empty queue.** `done` is not emitted while
   messages remain queued, so the working state, status line, and one-shot
   session auto-naming are unchanged in shape. `@file` references in a
   queued message expand when it is delivered, exactly like a submitted
   prompt; unresolvable references stay literal.
6. **Turn end with messages still queued — hold, never auto-send.** If the
   run ends for any reason other than an empty queue (user cancellation,
   model/tool error, compaction finish), the remaining messages stay
   queued: rows stay `Queued:`, the status line keeps naming the count, and
   nothing starts a turn. Enter on an empty draft sends the whole held
   batch as the next turn (oldest as the prompt, the rest delivered at the
   first boundary); a bare Esc with an empty draft clears it with a visible
   note. Ctrl+D / `/quit` exits without sending; queued text dies with the
   process. (Amended 2026-10-03; supersedes the earlier Claude-Code-style
   auto-flush.)
7. **Queue visibility.** While the queue is non-empty the status line names
   the count (`2 queued` on the wide layout, `2Q` on the narrow one) — both
   during a run and while a held batch waits. With the queue empty and the
   draft empty, the working placeholder says the draft will be queued; with
   a held batch, the idle placeholder says Enter sends and Esc clears.
8. **Never persisted before delivery.** A queued message is not part of
   `m.history` and not written to the session until it is delivered; resume
   never replays an undelivered message and never restores a queue.

### M2 — send now (`Ctrl+Q`)

A queued message can be delivered without waiting for the current model
response to finish: `Ctrl+Q` aborts only the response currently streaming
(the aborted partial response disappears from the transcript exactly as a
cancelled stream does today), then delivers the queue at once and continues
the same run with its settled tool history. If a tool call is executing, the
flush is deferred to the next safe point — a steering message must never
kill a running approved command. The binding ships after the Ghostty
(primary) / Windows Terminal (backlog) probe confirms the chord is
delivered; if a host swallows it, `Alt+Q` is the fallback, recorded in
tasks.md per the `tool-rendering-terminal-keys` probe discipline.
With M1 holding the queue on cancel, M2 is the only way to deliver without
waiting for a boundary; it remains deferred on its open probe.

### Non-goals

- No mid-tool interruption and no backgrounding of running tool work.
- No persistent or cross-restart queue; no draft persistence.
- No queueing of slash commands or shell commands (Lisa has no `!` shell
  mode); commands are refused, not held.
- No dequeue/recall chord in M1 (OMP's `app.message.dequeue`); noted as a
  possible follow-up.
- No provider-specific mid-response steering; no approval-flow changes.

## Interactions and edge cases

- **Approval pending:** queueing is unavailable while the review gate owns
  the keyboard (`y`/`n`, page gate). A message queued before the approval
  event is delivered after the tool settles (approved or rejected).
- **`/compact`:** commands stay refused during a run, including compaction;
  a message queued during compaction is held after the `compacted` event and
  waits for Enter.
- **Round cap:** a delivery between rounds consumes no extra round; the
  existing 32-round behavior (`specs/agent-loop/`) is unchanged.
- **Cancellation:** Esc/Ctrl+C still cancels the run; the newly started
  flush turn is itself cancellable and its own queue state is independent.
- **Queue overflow:** an implausibly long queue (beyond the delivery
  channel's capacity) never blocks the UI; excess falls to the turn-end
  flush path.

## Functional changes (to apply in v1-spec.md when implementation starts)

- **FR-17 amended.** Replace "hidden while a run is active or an approval is
  pending" with "hidden while an approval is pending", and replace the final
  sentence "Editing is inert during an active run and while an approval is
  pending, as today." with: "Editing stays live during an active run while no
  approval is pending; Enter queues the draft as a steering prompt (FR-21)
  instead of sending it. While an approval is pending, editing is inert and
  the caret is hidden, as today."
- **FR-21 added.** "While a run is active, the prompt input stays usable: the
  user can type a draft and press Enter to queue it as a steering prompt.
  Queued prompts appear in the conversation as queued until the agent
  receives them, then as sent user messages. A queued prompt reaches the
  model at the next safe point in the same run — after the current tool
  calls settle, before the next model request, or in place of ending the run
  while prompts remain queued — in the order typed; a run ends only with an
  empty queue. If a run ends with prompts still queued (cancellation,
  error, or a finished compaction), they are held and stay visible as
  queued; Enter on an empty draft sends the whole held queue as the next
  turn, and a bare Escape clears it. Reserved `/`
  commands stay inactive while a run is active and are refused with a
  visible message (`//` still queues literal text). Queue contents are never
  persisted and never replayed on resume."
- **FR-04 (cancellation) is unchanged:** cancellation still prevents
  further actions in the run it stopped and never consumes the queue; a
  later Enter starts a *new* turn.

## Resolved decisions (user-confirmed, 2026-10-02)

1. **Cancel/error with a queue (amended 2026-10-03):** remaining messages
   are held — nothing auto-starts; Enter sends the whole batch and a bare
   Esc clears it (M1 item 6). Supersedes the 2026-10-02 auto-flush choice.
2. **Send now:** implemented in M2 as `Ctrl+Q`.
3. **Commands during a run:** refused with a visible message and the draft
   kept; nothing is held or dispatched.
4. **Queued rows:** in-conversation `Queued:` rows, muted on the user band,
   flipping to `You:` on delivery.

## Open probe (implementation-time)

- `Ctrl+Q` delivery on Ghostty (primary) and Windows Terminal (documented
  target backlog) — XON/flow-control swallowing. Bind `Alt+Q` instead only
  if the probe fails; record the probe in tasks.md like the composer-key
  probes in `specs/tool-rendering-terminal-keys/`.

## Acceptance criteria

- [ ] While a run streams, typing, paste, kills, word motion, newline
      chords, and the `@`/`/` popups work; the caret is visible. While an
      approval is pending, editing is inert and the caret hidden.
- [ ] Enter with a non-empty plain draft during a run appends a `Queued:`
      row, clears the draft, and leaves `m.history` untouched; repeated
      submissions queue in order.
- [ ] A draft beginning with `/` is refused visibly during a run and keeps
      the draft; `//word` queues the literal text `word`.
- [ ] A queued message appears in the next provider request after the
      current tool calls settle, in queue order, and its transcript row
      flips to `You:`; a message queued during the final response continues
      the run instead of ending it.
- [ ] `done` is never emitted while messages remain queued; a cancel, error,
      or finished compaction with a non-empty queue holds the messages (no
      turn starts); Enter on an empty draft sends the whole held batch, a
      bare Esc clears it with a visible note, and Ctrl+D exits without
      sending.
- [ ] A delivered steer opens a fresh assistant bubble (answers are never
      glued to the previous round) and is delivered exactly once even if a
      steer event is missed (terminal reconciliation).
- [ ] Queued rows and queued text are absent from the stored session and
      from a resumed session.
- [ ] The status line names the queued count while a run is active and
      while a held batch waits.
- [ ] (M2) `Ctrl+Q` delivers queued messages without ending the run and
      without killing a running tool; the aborted partial response is gone
      from the transcript; the Ghostty probe is recorded.
- [ ] `go test ./...` and `go test -race ./...` pass from the project root.
