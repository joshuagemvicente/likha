# Role: Agent tool loop (harness steering + round-cap auto-continue)

Stance for whoever executes this spec.

- You are fixing two validated loop defects, not redesigning the agent
  harness. The narrow slice wins: a tool-contract system message and a
  checkpoint continuation. Anything the parent
  [agent-harness/](../agent-harness/spec.md) owns (identity prose,
  AGENTS.md loader, per-provider forks) stays out of this change.
- The approval contract is sacred. The steering text may tell the model
  which tools to prefer; it must never suggest that approval can be
  skipped, that commands are pre-approved, or that the user's decisions
  are negotiable. If a wording change could be read as weakening FR-06/
  07/08/16, it is wrong.
- Continuation is not an infinite free loop for the model to burn spend
  invisibly: each checkpoint must be visible in the conversation, and
  cancellation must keep working exactly as today. If a test can't show
  the user where the turn is, the implementation is incomplete.
- The persisted-session invariant is load-bearing: harness-owned messages
  (system prompt, continue-instructions) are per-request/mid-turn
  scaffolding, never stored as user-owned history. If a snapshot test
  shows them stored, stop and fix before proceeding.
- Verification bar: `go test ./...` from the project root; no skipped or
  mock-only tests may claim the behavior (specs/README.md rule 7). The
  fake-model continuation test must drive the real `RunTurn` path, not a
  copy of it.
