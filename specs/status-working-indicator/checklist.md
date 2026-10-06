# Checklist: Model-running status

- [x] Mistaken transcript changes are reverted; unrelated edits are preserved.
- [x] The spinner appears beneath the input and animates during output.
- [x] Terminal states and review remove the model-running indicator.
- [x] Fixed width, controls, ASCII, Nerd Font, themes, and no-color are verified.
- [x] `go test ./...` and targeted TUI race checks pass (2026-10-04).
- [ ] Real-terminal feel-check is completed.
- [x] After the corrected dot implementation, the user selected the Braille
  spinner from replacement-animation options; it replaces the dots in place.
