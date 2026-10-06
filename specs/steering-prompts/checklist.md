# Checklist: Steering prompts (type, queue, and interrupt while a run is active)

Observable outcomes; unchecked until seen in the real TUI. Status mirrors
[spec.md](spec.md) acceptance criteria.

- [ ] While a run streams, the composer accepts typing, paste, kills, word
      motion, newline chords, and `@`/`/` popups, with a visible caret;
      while an approval is pending, editing is inert and the caret hidden.
- [ ] Enter with a non-empty plain draft during a run shows a `Queued:` row
      and clears the draft; the conversation history is untouched until
      delivery.
- [ ] A `/`-command typed during a run is refused with a visible message and
      the draft stays put; `//word` queues `word` as literal text.
- [ ] A queued message is delivered after the current tool calls settle, in
      order, and its row flips from `Queued:` to `You:`.
- [ ] A message queued during the final model response continues the run
      instead of ending it; `done` arrives only with an empty queue.
- [ ] Cancelling or hitting an error with messages still queued holds them
      (rows stay `Queued:`, no turn starts); Enter on an empty draft sends
      the whole held batch, a bare Esc clears it, and Ctrl+D exits without
      sending.
- [ ] The status line names the queued count while a run is active and
      while a held batch waits.
- [ ] A queued-but-undelivered message never appears in the stored session
      or after resume.
- [ ] `go test ./...` and `go test -race ./...` pass from the project root.
- [ ] (M2) During a streaming response, Ctrl+Enter with a draft stops the
      response and the run continues with the message; no new turn and no
      extra turn footer.
- [ ] (M2) The partial answer stays on screen, followed by `Response
      interrupted to deliver your message.`, then `You:`, then a fresh
      answer; the model does not repeat what it had already said.
- [ ] (M2) Ctrl+Enter while a command or other tool runs shows `Queued:`
      and `Steer waits for the running tool`, and the tool finishes
      normally.
- [ ] (M2) Shift+Enter steers during a run; Alt+Return inserts a newline in
      a running draft; while idle, Ctrl+Enter and Shift+Enter insert a
      newline.
- [ ] (M2) Ctrl+Enter on an empty draft sends the queue now; it does
      nothing with an empty queue, a pending approval, or an open popup.
- [ ] (M2) Esc right after a steer cancels the run as usual.
- [ ] (M2) Ghostty live probe recorded in tasks.md.
