# Context: edit seams

- `internal/actions/edit.go`: current diff proposal, text caps, ancestor/target identity checks, staging and publication.
- `internal/agent/agent.go`: edit approval dispatch and cancellation boundaries.
- `internal/tui/tui.go`, `internal/tui/scroll.go`: pending review, read-to-end gate, viewport safety.
- `internal/session/session.go`: interrupted proposal/recovery metadata belongs to private state.
- Existing regression entry points: `internal/actions/edit_test.go`, `internal/agent/approval_test.go`, `internal/tui/permission_e2e_test.go`.
- [Registry](../tool-registry/spec.md), [output](../tool-output/spec.md): effect enforcement and review/result separation.
