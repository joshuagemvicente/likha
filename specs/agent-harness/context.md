# Context: harness

- `internal/agent/agent.go`: request history and effective tool catalog.
- `internal/model/client.go`, `internal/model/codex.go`: role encoding and leading system/developer messages.
- `internal/repository/repository.go`: confined root-file reads.
- `internal/session/session.go`, `internal/agent/compaction.go`: distinguish stored conversation/summary from runtime prompt scaffolding.
- [Loop](../agent-loop/spec.md), [registry](../tool-registry/spec.md), [explore](../explore-agents/spec.md): continuation and restricted child snapshots.
