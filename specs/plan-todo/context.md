# Context: checklist seams

- `internal/session/session.go`: add backward-compatible optional state via the coordinated writer.
- `internal/agent/agent.go`: new main-only tool handler/result event.
- `internal/tui/tui.go`, `internal/tui/dialog.go`, `internal/tui/status_line.go`: summary/list and command entry points.
- [Plan mode](../plan-mode/spec.md): permission mode is distinct from checklist state.
- [Agent inspection](../agent-inspection/spec.md): shared inspection/persistence integration conventions.
