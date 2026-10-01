# Role — repository structure refactor

Acting stance and constraints for whoever executes this spec.

- Refactoring engineer, not feature author. The user-visible product is
  frozen: no string, keybinding, approval outcome, diff rendering, file
  write, or session behavior may change. Any urge to "improve while here"
  (rename a function, tighten an error, restyle a dialog) is a spec change
  first — otherwise it is out of scope.
- The spec is the contract: file assignments live in `context.md`, order
  and verification in `tasks.md`, outcomes in `checklist.md`. If the code
  disagrees with any of them (a missed call site, a second `*ui` receiver,
  a test the inventory omitted), the spec is amended before the code moves,
  not after.
- Moves are whole-file by default (`git mv` + package header + imports).
  Only `mentions.go` and `providers.go` split, and only on the declared
  cut lines with the declared rule (`tea.Cmd`/`tea.KeyMsg`/`*ui` ⇒ stays).
  No other file is opened for surgery.
- Exports are minimal and forced: T0's constructor/accessor table is the
  complete export list for `internal/providers`; anything not on it stays
  unexported. A new exported symbol not forced by a verified call site is a
  spec violation, not initiative.
- Verification is the four baseline commands plus the diff audit, per task.
  Tests move; tests are never rewritten, weakened, or renamed (except the
  two declared renames: `statusbar_test.go` → `status_view_test.go`,
  `tui_m2_keys_test.go` → `tui_keys_test.go`). A red suite stops the phase.
- Sequencing is load-bearing: Phase 2 never starts before the Phase 1 gate
  is green; Phase 2 tasks run in T0→T7 order; each task leaves
  `go build ./...` green. Parallel moves across tasks are forbidden —
  interleaved package renames are how import graphs break silently.
- FR-06/07/08 (approval gating) and FR-16 (MCP trust) are the behaviors most
  at risk from re-homing: `approval_test.go` and the MCP suites are the
  oracles, and they pass unmodified or the move is wrong.
