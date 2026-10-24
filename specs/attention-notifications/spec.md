# Feature: Attention notifications (bell, desktop notice, opt-in question auto-skip)

**Status:** implemented (local) 2026-10-07 — decisions recorded 2026-10-07
(user interview); drafting defaults D1–D9 adopted as written when the user
asked for implementation. Automated checks only (`go test ./...`,
`go vet ./...`, `gofmt`, `-race` on app, tui, tools and providers); no
real-terminal walkthrough yet (W1), so which terminals show OSC 9, and tmux
passthrough end to end, are unverified. Requirement: [v1-spec FR-41](../v1-spec.md#attention-and-automation-amendment-user-approved-2026-10-07).
Refines [ask-user](../ask-user/spec.md) (FR-30) and
[permission-ui](../permission-ui/spec.md); contradicts neither.

## Context

A run can stop and wait for the user in three ways: an approval review (an
edit, a command, an MCP server's first call, or a web consent), an
`ask_user` question, or the end of the run. Today Likha shows each of these
only inside its own window. A user who switched to a browser or another
terminal tab while a long run worked has no way to know that the run is
parked on them. The run waits indefinitely, which is correct for approvals,
but the time is lost.

Claude Code notifies on permission prompts and after 60 seconds of idle
input. OpenCode V2 sends system notifications and sounds per event. OMP
sends terminal notifications from `ask`. None of them approves anything on a
timer. Sources are listed in
[../tooling-landscape/feature-comparison.md](../tooling-landscape/feature-comparison.md)
and [context.md](context.md).

## Role

Tell the user, through their terminal, when Likha needs them or has
finished. Let a user who runs unattended choose to have unanswered
questions skipped after a set time, so the model continues without an
answer instead of waiting forever. Never let time decide an approval and
never invent an answer.

## Scope

In scope: the full-screen TUI; approval reviews of every kind, web consent,
`ask_user` single questions and questionnaires; run completion and run
failure; one opt-in timeout for `ask_user` only.

Out of scope:

- Any timeout on approvals, web consent or MCP trust. These wait until the
  user decides or cancels (decision 3).
- OS-native notification helpers (`osascript`, `notify-send`), sounds other
  than the terminal bell, and push notifications to phones.
- Picking an option, the recommended option or any text on the user's
  behalf when a question times out.
- Explore children and profile tasks. They cannot ask questions or open
  reviews of their own (FR-28, FR-30), so they cannot trigger a signal.
- Headless mode ([headless-print](../headless-print/spec.md), FR-44): it has
  no TUI and never emits a bell or desktop notice.

## Decisions (user interview, 2026-10-07)

1. **Two signals: the terminal bell and an OSC 9 desktop notification.**
   Likha writes the bell character and an OSC 9 escape sequence to the
   terminal. Terminals that support OSC 9 show a desktop notification;
   others ignore it. Likha starts no helper process.
2. **Each signal can be turned off on its own.**
3. **Approvals never time out.** This includes edit, command and MCP
   reviews and web consent.
4. **`ask_user` may auto-skip, opt-in, off by default.** When the user sets a
   time limit and a question stays unanswered that long, Likha returns the
   existing skip result, marked as timed out. It never picks an option.
5. **Signals fire on three events:** an approval review opens, an `ask_user`
   question opens, and a run ends normally or with an error.

## Drafting defaults (confirm in review)

These were chosen while drafting and adopted as written for the 2026-10-07
implementation; they remain open to review.

- D1. **Both signals are on by default.** A user who never opens
  `config.json` gets them.
- D2. **Settings live in `config.json`** under `attention`, beside
  `status_line` and `web`, with no new slash command. The limit is in
  whole minutes from 1 to 120; absent or 0 means off.
- D3. **No signal while the terminal is known to be focused.** Likha asks the
  terminal for focus reports. While the terminal reports focus, neither
  signal fires. When the terminal never reports focus, both fire.
- D4. **A run-end signal needs a run of at least 10 seconds.** Shorter runs
  end while the user is still watching. Reviews and questions always
  signal, whatever the run length.
- D5. **A user-cancelled run sends no signal.** The user pressed Esc, so the
  user is present.
- D6. **Notification text is generic.** It names Likha, the repository
  folder and the event, never a file path, command, question text or model
  output, because desktop notification history can be seen by others.
- D7. **Inside tmux, the OSC 9 sequence is wrapped for tmux passthrough.**
  It reaches the outer terminal only when tmux allows passthrough; the bell
  is sent plain and tmux handles it as usual.
- D8. **The first key pressed in a question dialog cancels its auto-skip.**
  A user who has started answering is present, so the question no longer
  times out. The countdown line then disappears.
- D9. **A questionnaire auto-skips as a whole.** Because any key stops the
  timer (D8), a timed-out questionnaire has no recorded answers, and every
  question is reported skipped.

## Functional changes

### Signals

1. When an approval review opens (edit, command, MCP first call, `/init` or
   `/install` proposal, web consent), Likha emits the enabled signals once.
2. When an `ask_user` question or questionnaire opens, Likha emits the
   enabled signals once. Moving between questionnaire pages emits nothing.
3. When a run ends normally or with an error, Likha emits the enabled
   signals once, subject to D4 and D5.
4. Reviews and questions are serialized (FR-30), so each one signals when it
   opens, not when it is queued.
5. The desktop notification reads `Likha · <folder>: <event>`, where
   `<event>` is one of `approval needed`, `question waiting`,
   `run finished` or `run failed` (D6).
6. Signals never change the screen, the transcript, the composer draft or
   the steering queue. A terminal that ignores OSC 9 shows nothing extra.
7. With both signals off, Likha writes neither sequence.
8. Signals are not repeated while the same review or question stays open.

### Question auto-skip

9. With no limit set, questions wait until answered, skipped or cancelled,
   as today.
10. With a limit set, the question dialog shows a muted line
    `Auto-skips in M:SS unless you answer · any key keeps it open`, counting
    down each second. It is legible without color or Nerd Font glyphs.
11. When the limit passes with the dialog untouched, the question closes and
    the model receives the existing skip refusal, worded so the model can
    tell why: `The question was skipped automatically: no answer within
    <N> minutes.` A questionnaire returns the same refusal for the whole
    questionnaire (D9).
12. The transcript records `question skipped (timed out after <N> min)`.
    Resume treats it as a skipped question, never as an unanswered one, and
    does not reopen it (FR-30).
13. Any key in the dialog stops the countdown for that question (D8). The
    question then waits like a question with no limit.
14. Esc still cancels the run (FR-04). A cancelled run, an expired question
    and a later timer firing never deliver a second result; delivery stays
    exactly once.
15. The timer runs only while the question is on screen. A question that
    cannot open yet because another interaction is open does not time out
    while it waits.
16. A timed-out question grants nothing. The model may ask again only within
    its normal rules.

### Approvals

17. Approval reviews and consent dialogs never close on a timer, with or
    without an auto-skip limit set.

## Acceptance criteria

- An edit review, a command review, an MCP first-call review and a web
  consent each write one bell and one OSC 9 sequence when they open.
- An `ask_user` question writes one bell and one OSC 9 sequence; moving
  between questionnaire pages writes nothing more.
- A 12-second run that ends normally signals `run finished`; one that ends
  with an error signals `run failed`; a 3-second run and a cancelled run
  signal nothing.
- With `bell` off only the OSC 9 sequence is written; with `desktop` off
  only the bell; with both off nothing.
- While the terminal reports focus nothing is written; after it reports
  blur, signals resume.
- Notification text never contains a path, a command, the question or model
  output.
- With a 1-minute limit, an untouched question closes after one minute, the
  model receives the timed-out refusal, and the transcript says so.
- With a limit set, pressing ↓ once stops the countdown; the question stays
  open past the limit.
- Esc during the countdown cancels the run; no skip result is delivered
  afterwards.
- An approval review left open for longer than the limit stays open.
- Resuming a session with a timed-out question shows it skipped and does
  not reopen it.
