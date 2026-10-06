# Feature: Steering prompts (type, queue, and interrupt while a run is active)

**Status:** M1 implemented (local) — automated suite green; real-TUI
walkthrough outstanding. M2 (`Ctrl+Enter` steer now) implemented (local) —
re-specified 2026-10-05; automated suite green (agent, model, tui, race);
the live Ghostty probe and real-TUI walkthrough are outstanding.

## Context

Likha freezes its prompt input for the whole duration of a turn. `editable()`
is `!m.working && m.pending == nil` (`internal/tui/tui.go:919-921`), the Enter
key returns early while `working` (`tui.go:876-878`), and the caret is hidden
(`internal/tui/scroll.go:106-111`). A user who thinks of a correction
mid-stream cannot even compose it. `agent.RunTurn` makes the freeze
structural: it owns a private history built from `prior` plus one prompt
(`internal/agent/agent.go:47-49`) and has no input point after it starts; the
UI replaces `m.history` wholesale from `TurnEvent.History`
(`tui.go:481-500`).

Every reference agent keeps the input live while a run is active; they differ
in what a submitted draft does (research below). Likha takes the
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

**Decisions this forces for Likha:**

- Enter = queue (Claude Code / OpenCode shape). OMP's plain-Enter-steers is
  not copied: Ctrl+Return is already Likha's newline chord (user-confirmed
  2026-09-30, `specs/tool-rendering-terminal-keys/`), and OMP's
  Ctrl+Q/Ctrl+Enter followUp chord is therefore unavailable for queueing.
  *Amended 2026-10-05 (user-confirmed):* Enter still queues, but during a
  run the Return-modifier family (Ctrl+Return, Shift+Return, Ctrl+J) now
  means *steer now* (M2), matching Claude Code's `Ctrl+Enter` send-now.
  The newline meaning stays everywhere else, and Alt+Return keeps it
  inside a running draft.
- Delivery at provider-call boundaries inside the same run = OpenCode's safe
  boundary plus Claude Code's "as soon as those tool calls finish". Turn
  end diverges (amended 2026-10-03): Likha holds the queue for an explicit
  Enter instead of Claude Code's auto-flush.
- No provider-wire steering (`response.steer`-style): Likha's client has one
  request/response `Stream` call per round (`internal/model/client.go:344`);
  a queued message attaches to the next request. M2's steer now stops the
  in-flight request and attaches the message to a fresh one; it is still
  not a wire-level steer.

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

### M2 — steer now (`Ctrl+Enter`)

*Re-specified 2026-10-05 (user-confirmed); supersedes the 2026-10-02
`Ctrl+Q` send-now design, which was never implemented. Two changes: the
chord is Ctrl+Enter, and the interrupted response's visible text is kept
instead of removed.*

Enter keeps queueing exactly as M1 describes. Ctrl+Enter delivers without
waiting for the model to finish the response it is producing — the case M1
cannot help with: one long thinking or answering stream with no tool
boundary coming, heading somewhere the user does not want.

1. **The chord.** While a run is active, the Return-modifier family —
   Ctrl+Enter, Shift+Enter, and Ctrl+J — means *steer now*. On bubbletea
   v1.3.10 all three arrive as the same LF byte, reported `ctrl+j` (probe
   table in
   [tool-rendering-terminal-keys/tasks.md](../tool-rendering-terminal-keys/tasks.md)),
   so Likha cannot tell them apart: Shift+Enter steers too while a run is
   active. Alt+Return stays the newline key inside a running draft when the
   terminal sends it as one chord (ESC and Return together, reported
   `alt+enter`). The split arrival — a separate Esc, then Return — does not
   work during a run: Esc cancels a run immediately and never waits for a
   following key (M1, FR-04). While
   no run is active, the family inserts a newline exactly as today. While a
   completion popup (`@` or `/`) is open, the chord does nothing, as the
   newline chord does today.
2. **Steer with a draft.** With a non-empty draft, Ctrl+Enter takes the
   draft under Enter's text rules: typed newlines flatten to spaces, a
   leading `/` is refused with the draft kept and nothing steered, `//word`
   steers the literal `word`, and the text joins prompt history. The draft
   joins the end of the queue, and the whole queue — earlier queued
   messages first, in typed order — is delivered now.
3. **Steer with an empty draft.** If messages are queued, Ctrl+Enter
   delivers them now. If nothing is queued, it does nothing: no newline, no
   interruption.
4. **What "now" means, by run phase.**
   - *A model request is in flight* — waiting for the first token,
     thinking, or streaming text or tool-call arguments: Likha stops that
     one request, delivers the queue, and sends a fresh request in the same
     run. The run continues with every settled tool result and decided
     approval. No new turn starts, no turn footer is written, and session
     auto-naming is unaffected.
   - *A tool is executing* — a command, an edit being applied, an explore
     task, or an open question: nothing is interrupted. The message
     behaves exactly like an Enter-queued one: it shows as `Queued:` and is
     delivered when the current tool calls settle (M1 item 4). The status
     reads `Steer waits for the running tool`. A steer never cancels a
     running tool or an approved command.
   - *An approval is pending:* the composer is inert (M1 item 1), so the
     chord does nothing.
   - *A response has finished and its tool calls are about to run:* there
     is no request to stop. The tool calls run, and ask for approval, as
     usual; the message is delivered at the next boundary.
   - *`/compact` is running:* the summarize call is never stopped. The
     chord queues like Enter, and the message is held when compaction
     finishes (M1 item 6).
5. **The interrupted response.**
   - Visible text the model had already streamed stays in the transcript
     and stays in conversation history as the assistant's message, so the
     model sees what it had said and does not repeat it. A muted note
     follows it: `Response interrupted to deliver your message.`
   - Reasoning the model had streamed stays visible in its reasoning block,
     which closes, and never enters history (reasoning is never sent back
     to a provider, as today).
   - Tool calls the model had begun streaming are dropped. They never ran,
     never reach approval, and appear nowhere as executed or unexecuted
     calls.
   - If no visible text had streamed yet (nothing, or reasoning only), no
     assistant message is added; the note still appears, and the steer
     follows the last settled message.
6. **The steered message.** Each delivered message flips its `Queued:` row
   to `You:` at the moment it is appended to history, after the interrupted
   text. The model's next output opens a fresh assistant bubble. The status
   reads `Steering…` from the key press until the fresh request starts
   producing output.
7. **Repeated presses.** Pressing Ctrl+Enter again while a stop is already
   under way adds that draft to the same delivery. One request is stopped,
   never two.
8. **Cancellation wins.** Esc or Ctrl+C during or after a steer cancels the
   run exactly as M1 and FR-04 describe. A steer never revives a cancelled
   run, and a steer pressed while the run is cancelling is queued and held
   (M1 item 6).
9. **Usage and spend.** The provider bills the stopped request for what it
   produced. If the provider reported usage before the stop, Likha counts
   it. If it did not, Likha marks session spend as approximate (`~`)
   instead of leaving the request out silently, and invents no number.
10. **Persistence and resume.** Once delivered, the interrupted text, the
    note, and the steered message are ordinary conversation content and
    persist like any other message. Nothing undelivered persists (M1
    item 8), and resume never replays a steer.

### Non-goals

- No mid-tool interruption and no backgrounding of running tool work.
- No persistent or cross-restart queue; no draft persistence.
- No queueing of slash commands or shell commands (Likha has no `!` shell
  mode); commands are refused, not held.
- No dequeue/recall chord in M1 (OMP's `app.message.dequeue`); noted as a
  possible follow-up.
- No provider-specific mid-response steering (no wire-level
  `response.steer`); M2 stops and re-sends instead. No approval-flow
  changes.
- No way to tell Ctrl+Enter from Shift+Enter on bubbletea v1.3.10. Splitting
  them needs a keyboard-protocol-capable input layer (bubbletea v2), which
  would be its own feature.
- No steer-now for explore or profile child agents.

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

M1's changes above are applied in v1-spec.md. M2 adds (applied to v1-spec.md
2026-10-05, marked planned there until implemented):

- **FR-21 amended (M2).** Append: "While a model request is in flight,
  Ctrl+Enter (and Shift+Enter or Ctrl+J, which terminals deliver as the same
  key) steers now: Likha stops that request, delivers the draft and any
  queued prompts in order, and continues the same run with a fresh request.
  Visible text the model had streamed stays in the conversation and its
  history, marked interrupted; partial reasoning and partially streamed tool
  calls are discarded. While a tool is executing, a steer waits for the tool
  calls to settle and never cancels them. Alt+Return inserts a newline in a
  running draft."
- **FR-17 amended (M2).** After "Enter queues the draft as a steering prompt
  (FR-21) instead of sending it", add: "During a run, Ctrl+Enter steers now
  (FR-21) and Alt+Return inserts a newline."

## Resolved decisions (user-confirmed, 2026-10-02)

1. **Cancel/error with a queue (amended 2026-10-03):** remaining messages
   are held — nothing auto-starts; Enter sends the whole batch and a bare
   Esc clears it (M1 item 6). Supersedes the 2026-10-02 auto-flush choice.
2. **Send now:** ~~implemented in M2 as `Ctrl+Q`~~ — superseded
   2026-10-05: M2 is *steer now* on `Ctrl+Enter` (with Shift+Enter and
   Ctrl+J, which share its byte). Enter keeps queueing.
3. **Commands during a run:** refused with a visible message and the draft
   kept; nothing is held or dispatched.
4. **Queued rows:** in-conversation `Queued:` rows, muted on the user band,
   flipping to `You:` on delivery.
5. **Interrupted response (2026-10-05):** keep the visible partial text in
   history with an interruption note; drop partial reasoning and partial
   tool calls.
6. **Steer while a tool runs (2026-10-05):** wait for the tool calls to
   settle; never cancel a running tool.

## Open probe (implementation-time)

- Live Ghostty pass (primary target), run by the user: during a streaming
  response, Ctrl+Enter and Shift+Enter each steer, and Alt+Return inserts a
  newline in the running draft; while idle, all three insert a newline.
  The pty probe already shows the LF byte reaching Update as `ctrl+j`; the
  live pass confirms the terminal sends it. Record the result in tasks.md.
- Windows Terminal stays backlog: it is documented to swallow Ctrl+Enter.
  Shift+Enter is the expected working chord there, unverified.

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
- [ ] (M2) During a streaming response, Ctrl+Enter with a draft stops
      that request and the next provider request in the same run carries
      any earlier queued messages and then the draft, in order; no new turn
      starts and no turn footer is written.
- [ ] (M2) The interrupted response's visible text stays in the transcript
      and in history, followed by the interruption note; partial reasoning
      is not in history; partially streamed tool calls never run, never
      reach approval, and are absent from history.
- [ ] (M2) Ctrl+Enter while a tool executes cancels nothing: the message
      shows `Queued:` and is delivered when the tool calls settle.
- [ ] (M2) Ctrl+Enter on an empty draft delivers the queue now, and does
      nothing when the queue is empty; it does nothing while an approval is
      pending or a completion popup is open; during `/compact` it queues
      and holds.
- [ ] (M2) While a run is active, Alt+Return inserts a newline in the
      draft; while idle, Ctrl+Enter, Shift+Enter, and Ctrl+J insert a
      newline as before.
- [ ] (M2) Esc during or after a steer cancels the run; a stopped request
      with no reported usage marks spend approximate.
- [ ] (M2) The live Ghostty probe is recorded in tasks.md.
- [ ] `go test ./...` and `go test -race ./...` pass from the project root.
