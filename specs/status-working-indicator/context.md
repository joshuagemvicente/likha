# Context: Model-running status

- `internal/tui/status_line.go`: status state, hint variants, width fitting,
  and final status-row styling.
- `internal/tui/status_activity.go`: model-running predicate, spinner frames,
  and styled rendering confined to the bottom status hint.
- `internal/tui/tui.go`: shared activity-clock arming and event handoffs.
- `internal/tui/status_activity_test.go`: bottom placement and lifecycle tests.
- `specs/working-indicator/`: existing transcript indicator; leave its
  rendering unchanged.
