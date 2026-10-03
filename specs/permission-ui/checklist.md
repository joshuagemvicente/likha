# Checklist: Approve/Decline buttons for permission reviews

Observable outcomes; unchecked until seen in the real TUI. Status mirrors
[spec.md](spec.md) acceptance criteria.

- [ ] A pending review shows `[ Approve ]` and `[ Decline ]` with exactly
      one focused button, visible on every theme family (focus marker plus
      band where the theme has one).
- [ ] `←/→` and `Tab`/`Shift+Tab` move focus; `Enter` confirms the focused
      action.
- [ ] Approve before reading to the end is refused with
      `Scroll to the end to approve` and sends no reply; after the end is
      reached it approves exactly as the old `y` path did.
- [ ] Decline rejects without executing and the rejection is visible; the
      model continues.
- [ ] `y`/`n` and every other letter are inert during a review.
- [ ] `Esc`/`Ctrl+C` still cancel the run from a pending review.
- [ ] No `Y/N`, `Yes/No`, or "press Y" wording remains in the UI, README,
      or specs.
- [ ] The bar fits 40×12 and every composer style without overflow.
- [ ] `go test ./...` and `go test -race ./...` pass from the project root.
