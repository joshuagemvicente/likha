# Tasks: tool registry

Follow [coordinator constraints](../tooling-platform/spec.md#verification-and-authorization-to-work).

1. Freeze shared interfaces before workers edit integration files. Define a
   catalog query for a run scope and an invocation that returns a typed result.
   Run scope owns canonical repo, mode, capability ceiling, model identity,
   cancellation, budget, and session-output access. Keep handlers behind it.
   **Done:** feature owners can build against one documented interface.
2. Put registration/schema/policy/result types in a leaf module such as
   `internal/tools`; inject execution/interaction capabilities. Avoid a
   registry → agent → registry cycle. Runtime task registration injects a
   scheduler callback rather than importing the turn loop into leaf handlers.
   **Done:** existing runtime/TUI dependency rules remain intact.
3. Migrate the five built-ins and MCP adapter through this path. Keep original
   inputs, approval contents, and old history decoding. Add deterministic
   server-qualified MCP names and safe legacy resolution.
   **Done:** existing workflow and duplicate-server walkthroughs have results.
4. Implement schema/eligibility checks, interaction serialization, contiguous
   parallel-safe read groups, typed errors, cancellation, and result ordering.
   **Done:** the acceptance guide records exercised and unverified cases.
5. Wire `/tools` into the command catalog/help with availability reasons.
   The coordinator owns shared TUI/runtime composition changes.
   **Done:** user guide names match the actual catalog.
6. Run existing `go build ./...`, `go test ./...`, and `go test -race ./...`.
   Record failures; do not add tests or weaken existing ones.
   **Done:** commands/results and remaining evidence gaps are recorded.
