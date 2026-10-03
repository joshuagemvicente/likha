# Context: MCP identity enhancement

- `internal/mcp/config.go`, `internal/mcp/mcp.go`: current private stdio config, discovery, request deadline, local cancellation.
- `internal/mcp/manager.go`: raw names, server attribution, manager-lifetime trust.
- `internal/agent/agent.go`: built-in-first dispatch and fallback approvals.
- [Registry](../tool-registry/spec.md): qualified names/schema/common authorization.
- [Plan mode](../plan-mode/spec.md), [explore](../explore-agents/spec.md): stricter eligibility ceilings.
