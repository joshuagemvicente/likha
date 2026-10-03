# Context: question seams

- `internal/agent/agent.go`: current ApprovalRequest/reply-channel seam; no question tool yet.
- `internal/tui/tui.go`, `internal/tui/dialog.go`, `internal/tui/editor.go`: interaction ordering and draft/focus handling.
- [Steering](../steering-prompts/spec.md): held/queued state and safe model-request boundaries.
- `internal/session/session.go`: persist completed events rather than pending channels.
- [Registry](../tool-registry/spec.md): typed refusals, schema validation, interaction serialization.
