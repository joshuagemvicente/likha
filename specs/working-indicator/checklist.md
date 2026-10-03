# Checklist: Working indicator (ephemeral activity row with subtle motion)

Observable outcomes; mirrors [spec.md](spec.md) acceptance criteria.
Status words per [specs/README.md](../README.md) rules. No box is ticked
until the automated suite (or a named walkthrough) covers it.

- [x] Pressing Enter on a prompt shows the activity row on the next frame,
      before any provider event arrives (`Working…` with an ASCII spinner
      under the `You:` prompt; same for the held-queue flush and for
      `/compact` under the last row).
- [x] While working with no real content, the spinner advances on a steady
      tick and the accent sweep moves across the label with text, width,
      and scroll position unchanged.
- [x] The first reasoning / answer / tool / approval event of a round
      replaces the placeholder in place: no blank row, no adjacent
      duplicate, transcript length unchanged across the handoff; `context`
      telemetry alone never dismisses it.
- [x] A run that performs tool rounds shows the indicator again between the
      tool result and the next round's first content; a delivered steering
      prompt re-arms it for the fresh provider round.
- [x] Cancellation, error, completion, and approval takeover each remove
      the placeholder and stop the tick with no orphan row; the existing
      done / error / cancelled / review reporting reads exactly as before.
- [x] The row is absent from persisted sessions: a forced save with the
      indicator visible stores no activity role, and resume never restores
      it.
- [x] With color disabled the row still reads `Working…` with an advancing
      ASCII spinner; at 40–200 columns no rendered row exceeds the viewport.
- [x] `go test ./internal/tui -run '^TestWorkingIndicator'` and its race
      variant pass with the feature-specific tests.
- [ ] `go test ./...` passes. Current unrelated failures are recorded in the
      implementation handoff: session-dialog and prompt-history tests.
- [ ] Real-TUI walkthrough (submit, tool re-arm, cancel, resize) is recorded
      here.
