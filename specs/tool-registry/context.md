# Context: registry seams

- `internal/agent/agent.go`: current hard-coded definitions/dispatch, approval requests, serial tool execution, interrupted-call reconciliation.
- `internal/model/client.go`, `internal/model/codex.go`: provider schema encoding, tool IDs, system/developer messages.
- `internal/mcp/manager.go`: current raw-name lookup, tool descriptions, trust state.
- `internal/tui/tui.go`, `internal/tui/dialog.go`, `internal/tui/commandcomplete.go`: event integration, single pending approval, command surfaces.
- `ARCHITECTURE.md`: preserve agent/TUI import boundaries.
- [Shared decisions](../tooling-platform/decisions.md): capability ceilings and limits.
