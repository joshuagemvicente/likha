# Context: Steering prompts (type, queue, and interrupt while a run is active)

Code paths this feature touches. No design decisions hidden in prose — the
behavior in [spec.md](spec.md) is the whole contract.

## Code

- `internal/tui/tui.go` — the gate chain: `editable()` (919-921, becomes
  `pending == nil`), Enter submit (867-890, gains the queue branch and the
  working `/` refusal), `ctrl+c`/`esc` cancel (753-768, unchanged but now
  followed by the flush), `ctrl+d` (775-782, quit drops the queue),
  `syncPopups` `!m.working` gates (926-950, removed), TurnEvent gate (429),
  `tool_result` history adopt (481-482), `done`/`error` terminal handling
  (498-547, gains flush), `compacted` (487-497, gains flush), `persist`
  (987-1004, skips `Queued` like `Logo`), `startTurn` (219-258, creates the
  steer channel and initializes/refills the queue), `ui` fields (60-135,
  adds `queue`, `steer`).
- `internal/agent/agent.go` — `RunTurn` (43, gains the steer channel),
  private history (47-49), round loop (62-112), `done` emission (77-79),
  tool round (88-108), cancellation paths (63, 82-85, 93-97, 105-108),
  `appendUnexecuted` (115-119), `requestApproval` (219-227).
- `internal/model/client.go` — `Stream` (344, one request per call; the
  boundary a steer attaches to), user-message validation (358-372: a role
  `user` message is valid anywhere), `consumeStream` cancel checks
  (755-866).
- `internal/model/codex.go` — `BuildCodexRequest` (52-95): user messages map
  to `input_text` items, so a steer is provider-shape safe.
- `internal/tui/scroll.go` — `caretVisible()` (106-111, drops `working`).
- `internal/tui/status_line.go` — `statusState` (47-65) and `statusHints`
  (76-100, gains the queued count on both layouts).
- `internal/tui/composer.go` — `composerLines` (23-111), placeholder
  (59-66, working placeholder), caret (47-50).
- `internal/tui/view.go` — `entry` (17), `rebuild` (56-157), role style
  switch (130-143, gains `Queued`).
- `internal/tui/commandcomplete.go` — exact-name Enter closes the popup and
  falls through (110-134), so the new working refusal covers it with no
  extra path.
- `internal/tui/mentions.go` — popup Enter completion (58-93); the mention
  popup must keep claiming Enter on a run draft.
- `internal/tui/dialog.go` — `handleCommand` (157-162: the `//` escape;
  219-221: unknown-command refusal), `resumeSession` (246-263, untouched;
  resumed snapshots can no longer contain `Queued`).

## Tests to extend

- `internal/tui/tui_m2_keys_test.go:91-113` — `TestComposerInertWhileWorking`
  pins the old FR-17 contract ("editing is inert during a run") and is
  rewritten into live-while-working plus inert-while-reviewing tests.
- `internal/tui/tui_test.go` — key dispatch helpers (`sendRunes` 1220-1222),
  cancellation patterns (528-572, 575-632), pending-approval patterns
  (634-664); `composer_test.go:95-125` (review footer), `status_line_test.go`,
  `bands_test.go` (role styles), `composer_overflow_test.go` (no-overflow
  invariant), `scroll_test.go:14-51` (caret while working — expectation
  flips), `status_view_test.go:30-41` (`driveTurn` for real-turn tests).
- `internal/agent/agent_test.go` — scripted httptest SSE with call counters
  (48, 126, 182, 218) and request-body decoding; `approval_test.go:36-40`
  (`decide` callback) is the model for driving a steer mid-turn from the
  emit closure; `internal/model/client_test.go:188-191` proves a cancel
  inside `onText` aborts the stream.

## Related specs

- [v1-spec.md](../v1-spec.md) — FR-17 is amended and FR-21 added (text in
  spec.md § Functional changes); FR-04/FR-09 are unchanged.
- [tool-rendering-terminal-keys/](../tool-rendering-terminal-keys/spec.md) —
  the composer chord table (Ctrl+Return is newline by user decision, which
  rules out OMP's followUp chord; the M2 probe discipline comes from here).
- [slash-commands/](../slash-commands/spec.md) — the "commands are inert
  during a run" rule changes from *Enter ignored* to *visible refusal*; its
  spec table and [repo-init/checklist.md](../repo-init/checklist.md) wording
  are amended at implementation start.
- [permission-ui/](../permission-ui/spec.md) — the pending review owns the
  keyboard (queueing unavailable) and gains an Approve/Decline decision bar;
  this spec's delivery-after-settlement rule is unchanged.
- [conversation-compaction/](../conversation-compaction/spec.md) — the
  `compacted` terminal event joins the flush set.
- [agent-loop/](../agent-loop/spec.md) — round-cap semantics are unchanged;
  a steer between rounds consumes no extra round.
- [README.md](../README.md) — "Editing is inert while a turn streams or a
  review is pending" (line 103) and the input section are rewritten.

## Precedent

- External: Claude Code (queue + send now + turn-end flush), OpenCode
  (steering vs queued prompt admission at a safe provider-turn boundary),
  OMP (followUp queue, dequeue recall, steer interrupts). Cited in spec.md
  § Research findings.
- House: the approval `Reply chan bool` (`agent.go:219-227`) and the
  `abandon chan struct{}` (`tui.go:82-85`) show the established pattern for
  a UI-owned channel crossing into a running goroutine; the steer channel is
  the same shape, one-way, and drained only at provider-call boundaries.
