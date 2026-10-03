# Context: exploration seams

- `internal/agent/agent.go`: current single-context RunTurn, serial dispatch, events, interrupted-call records.
- `internal/model/client.go`: current latest-request token state; concurrent tasks need per-request attribution.
- `internal/agent/sessionname.go`: existing background title request also shares the client.
- `internal/repository/repository.go`: current operations lack walk-level context; Phase 1 supplies cancellation.
- `internal/session/session.go`: flat snapshot history, optimistic revision saves; no child tree currently exists.
- [Registry](../tool-registry/spec.md), [agent inspection](../agent-inspection/spec.md), [shared limits](../tooling-platform/decisions.md): dispatch, event, and budget contracts.
