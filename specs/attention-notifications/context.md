# Context: Attention notifications

Code, dependencies and prior art this feature touches, verified against the
working tree on 2026-10-07. All decisions live in [spec.md](spec.md).

## Where the three events happen (`internal/tui`)

- `tui.go`, the `"approval"` case of the turn-event switch (around line
  618): sets `m.pending = v.Approval`, calls `markAwaitingApproval()`
  (`tool_wiring.go:364`) and sets the `Review … before approval` status.
  Every approval kind arrives here: edits, commands and MCP first calls are
  all emitted by `awaitApproval` in `internal/agent/agent.go` (around line
  165–171) as `TurnEvent{Kind: "approval"}`. A cancelling run skips the
  review (`if m.cancelling { break }`), so no signal is due then.
- `tui.go`, the `"ask"` case (around line 563): calls `openAskQuestion`
  (`ask_view.go:185`) and `markAwaitingAnswer`. `openAskQuestion` refuses
  with a skipped reply when another interaction is open, so a signal belongs
  after the successful open, not on the event.
- `tui.go`, the `"consent"` case (around line 584): `openConsent`
  (`web_consent.go:44`). Web consent is an approval for decision 3.
- `tui.go:750` `finishRun`: the single terminal path. `m.cancelling` marks a
  user cancel (no signal, D5); `v.Kind == "error"` is the failure branch;
  otherwise the run finished. The run start time for D4 must be recorded
  where the run starts (the same place `m.working` is set true).

## The question dialog (`internal/tui/ask_view.go`)

- `askState` holds `pending`, `responded`, `generation` and the
  `committed` reply callback. `answerAsk` (line 375) records the transcript
  note, flips `responded`, closes, then replies: exactly-once delivery.
- `closeAsk` bumps `generation`; `askClosedMsg` routing compares
  generations. A timer message must carry the generation it was started for
  and be ignored when it no longer matches; that is how a timer that fires
  after an answer, a skip, a cancel or a newer question is dropped.
- `handleAskKey` (line 514) swallows every key while a question is pending:
  the right place to stop the countdown on the first key (D8).
- `askAnswerNote` keeps the resume vocabulary (`question skipped`,
  `ask answered (…)`); `reportUnansweredQuestions` (`plan_wiring.go:125`)
  and `askInterruptedNote` (line 477) read it. The timed-out note must start
  with `question skipped` so resume classifies it as skipped.
- `askHint` / `askQuestionnaireHint` are the pinned key help; the countdown
  is a separate muted line, not a hint change.

## Tool results (`internal/tools/ask_user.go`)

- Single question: `answer.Skipped` returns `Refused` with
  `The user skipped the question.` (around line 178).
- Questionnaire: `askQuestionnaireResult` refuses with
  `askQuestionnaireSkipped` (line 405) when `answer.Skipped`.
- `AskAnswer` (line 91) has `Text`, `Skipped`, `Answers`; a timeout needs a
  way to carry "timed out after N minutes" to the result text, e.g. a
  `TimedOut` duration field, so the model's refusal says why.

## Settings (`internal/providers/config.go`)

- `StoredProviderConfig` already keeps optional sections (`Composer`,
  `StatusLine`, `Web json.RawMessage`) and `FlagEnabled` resolves
  default-on pointers. An `Attention *StoredAttentionConfig` with
  `Bell *bool`, `Desktop *bool` (default on, D1) and
  `AskSkipAfterMinutes int` (0 = off, 1–120) fits the same pattern. Saving a
  theme or model must keep the section, as it does for `Web`.

## Writing escape sequences (bubbletea v1.3.10)

- `go.mod`: `github.com/charmbracelet/bubbletea v1.3.10`. This version has
  no public API to write a raw sequence; `standardRenderer.execute` is
  private. `SetWindowTitle` is the only escape-writing command.
- The renderer writes each frame with a single `r.out.Write(buf.Bytes())`
  (`standard_renderer.go` lines 283, 537, 588, 617). A mutex-guarded writer
  passed to `tea.WithOutput` lets Likha write BEL/OSC 9 between frames
  without splitting one.
- `internal/app/run.go:474` builds the program with `tea.WithAltScreen()`,
  `tea.WithMouseCellMotion()`, `tea.WithInput(os.Stdin)`,
  `tea.WithOutput(stdout)`. Focus reporting is `tea.WithReportFocus()`
  (`options.go:248`), delivering `tea.FocusMsg` / `tea.BlurMsg`
  (`focus.go`). Only `internal/app` may build the program (AGENTS.md), so
  the writer and option belong there and reach the TUI as a small
  interface.

## Sequences

- Bell: `0x07`.
- OSC 9: `ESC ] 9 ; <text> BEL`. Text must have control characters removed
  (no BEL, ESC or C1 bytes) and be length-bounded. ConEmu uses `OSC 9;<n>`
  with a leading number for other features; the text starts with `Likha`,
  never a digit.
- tmux passthrough: `ESC P tmux; <sequence with each ESC doubled> ESC \`,
  only when `TMUX` is set; tmux delivers it only with `allow-passthrough`
  on (tmux 3.3+).
- Which terminals show OSC 9 as a desktop notification (iTerm2, Ghostty,
  WezTerm and others) is to be checked in the live walkthrough, not
  assumed.

## Prior art

- Claude Code: a `Notification` hook fires when a tool needs permission and
  when the prompt has been idle 60 seconds; permission prompts never decide
  on their own ([hooks guide](https://code.claude.com/docs/en/hooks-guide.md)).
  A third-party issue mirror describes an `askUserQuestionTimeout` setting
  for `AskUserQuestion`; not confirmed in official docs.
- OpenCode V2: system notifications and attention sounds per event
  (permission, question, done, error).
- OMP: terminal notifications from `ask`.

## Docs to update when implemented

- `docs/usage.md`: the `attention` section of `config.json`, beside the
  `status_line` paragraph.
- `docs/tools.md`: the `ask_user` paragraph gains the opt-in auto-skip.
- `specs/ask-user/spec.md` and `specs/permission-ui/spec.md`: cross-links.
