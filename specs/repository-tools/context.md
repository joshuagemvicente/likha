# Context: repository tooling

- `internal/repository/repository.go`: glob/regex/read, descriptor confinement, existing safety limits.
- `internal/repository/tree.go`: directory entries, ignore rules, current mention listing behavior.
- `internal/agent/mentions.go`: separate @ expansion caps; preserve them.
- `internal/agent/agent.go`: current string results and tool schemas.
- Existing regression entry points: `internal/repository/`, `internal/agent/`.
- [Harness](../agent-harness/spec.md), [registry](../tool-registry/spec.md), [output](../tool-output/spec.md): routing, bounds, and representation.
