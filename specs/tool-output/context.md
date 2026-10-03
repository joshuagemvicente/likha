# Context: output seams

- `internal/actions/command.go`: existing combined stdout/stderr cap and process-group cancellation.
- `internal/agent/agent.go`: tool_start/result events and model history encoding.
- `internal/tui/tui.go`, `internal/tui/view.go`, `internal/tui/scroll.go`: transcript rebuild/render/focus and scrolling.
- `internal/session/session.go`: private snapshots, schema/version behavior, optimistic revisions.
- [Muted tool rendering](../tool-rendering-terminal-keys/spec.md): retain styling; this feature adds the formerly deferred expansion UI.
- [Shared bounds](../tooling-platform/decisions.md): output privacy, caps, and lifetime.
