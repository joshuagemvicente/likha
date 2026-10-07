# Checklist: Attention notifications

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md). Checked items are covered by
automated tests (`internal/tui/attention_test.go`,
`internal/app/terminal_output_test.go`,
`internal/providers/attention_config_test.go`,
`internal/tools/ask_user_test.go`) as of 2026-10-07; nothing here has had a
real-terminal walkthrough.

## Signals

- [x] An edit, command, MCP first-call review and a web consent each emit
      one bell and one desktop notification when they open.
- [x] An `ask_user` question emits one signal when it opens; questionnaire
      page moves emit none.
- [x] A run of 10 seconds or more that finishes signals `run finished`; one
      that fails signals `run failed`.
- [x] A shorter run and a user-cancelled run signal nothing.
- [x] `bell: false` and `desktop: false` each silence only their own
      signal; both off writes nothing.
- [x] While the terminal reports focus, nothing is signalled.
- [x] Notification text names Likha, the folder and the event only.
- [ ] Inside tmux with passthrough on, the desktop notification reaches the
      outer terminal. *(Only the wrapped bytes are tested; delivery needs
      the live walkthrough.)*
- [x] Signals never alter the screen, transcript, draft or steering queue.

## Question auto-skip

- [x] With no limit set, questions wait indefinitely, as before.
- [x] With a limit set, the dialog shows a countdown legible in ASCII mode
      and without color. *(Plain text with no glyphs or color, checked
      with styling stripped at 80 and 40 columns; it keeps the spec's `·`
      separator, as the rest of the UI does.)*
- [x] An untouched question closes at the limit; the model receives the
      timed-out refusal against the original call ID, exactly once.
- [x] The transcript says the question was skipped after N minutes, and
      resume shows it skipped without reopening it.
- [x] The first key in the dialog stops the countdown.
- [x] Esc during the countdown cancels the run; no late skip is delivered.
- [x] A question waiting behind another interaction does not time out
      before it opens.

## Approvals

- [x] Approval reviews and consent dialogs never close on a timer.

## Verification record

- [x] Automated suite green (`go test ./...`, `go vet ./...`, `-race` on
      `internal/app` and `internal/tui`).
- [ ] Live walkthrough recorded per terminal in [context.md](context.md). *(outstanding)*
