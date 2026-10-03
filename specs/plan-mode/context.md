# Context: plan mode

- `internal/agent/agent.go`: current dispatch and request context; Phase 1 registry replaces scattered gates.
- `internal/tui/tui.go`, `internal/tui/dialog.go`, `internal/tui/commandcomplete.go`: idle mode command and active-run command refusal.
- `internal/tui/status_line.go`: persistent mode marker.
- `internal/session/session.go`: persist conversation/checklist, not live mode or permission grants.
- [Registry](../tool-registry/spec.md), [harness](../agent-harness/spec.md): enforce mode and compose one system layer.
- [Checklist](../plan-todo/spec.md), [web](../web-tools/spec.md), [explore](../explore-agents/spec.md): allowed state/network/child ceilings.
- [Product contract](../v1-spec.md): FR-32 and existing approval/resume rules.
