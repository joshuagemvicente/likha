# Context: agent inspection seams

- `internal/tui/tui.go`: current one-run event channel, RunID filtering, save points, composer/approval dispatch.
- `internal/tui/dialog.go`, `internal/tui/view.go`, `internal/tui/scroll.go`: idle dialogs, rendered content, viewport/focus.
- `internal/tui/status_line.go`, `internal/tui/status_sources.go`: main context/spend display.
- `internal/session/session.go`: current flat snapshots and optimistic revisions.
- `internal/model/client.go`: per-client latest-request telemetry requires task-aware adaptation.
- [Tool output](../tool-output/spec.md), [explore](../explore-agents/spec.md): inspectable artifacts and task state.
