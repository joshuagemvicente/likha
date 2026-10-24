# Checklist: Attention notifications

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Signals

- [ ] An edit, command, MCP first-call review and a web consent each emit
      one bell and one desktop notification when they open.
- [ ] An `ask_user` question emits one signal when it opens; questionnaire
      page moves emit none.
- [ ] A run of 10 seconds or more that finishes signals `run finished`; one
      that fails signals `run failed`.
- [ ] A shorter run and a user-cancelled run signal nothing.
- [ ] `bell: false` and `desktop: false` each silence only their own
      signal; both off writes nothing.
- [ ] While the terminal reports focus, nothing is signalled.
- [ ] Notification text names Likha, the folder and the event only.
- [ ] Inside tmux with passthrough on, the desktop notification reaches the
      outer terminal.
- [ ] Signals never alter the screen, transcript, draft or steering queue.

## Question auto-skip

- [ ] With no limit set, questions wait indefinitely, as before.
- [ ] With a limit set, the dialog shows a countdown legible in ASCII mode
      and without color.
- [ ] An untouched question closes at the limit; the model receives the
      timed-out refusal against the original call ID, exactly once.
- [ ] The transcript says the question was skipped after N minutes, and
      resume shows it skipped without reopening it.
- [ ] The first key in the dialog stops the countdown.
- [ ] Esc during the countdown cancels the run; no late skip is delivered.
- [ ] A question waiting behind another interaction does not time out
      before it opens.

## Approvals

- [ ] Approval reviews and consent dialogs never close on a timer.

## Verification record

- [ ] Automated suite green (`go test ./...`, `go vet ./...`, `-race` on
      `internal/app` and `internal/tui`).
- [ ] Live walkthrough recorded per terminal in [context.md](context.md).
