# Tasks: explore runtime

1. Freeze task identity/state/result/budget interfaces with registry, session,
   model, and agent-inspection owners. Extract reusable run-scope execution
   without importing TUI packages into runtime/leaf tools.
   **Done:** parent/child contexts and events have distinct typed identities.
2. Implement fresh inherited-model contexts and dispatch capability intersections.
   Root harness/instructions are snapshots; child briefs are bounded task data.
   **Done:** depth-specific catalogs and forged-call refusals have recorded outcomes.
3. Implement the run-wide ready queue and execution permits. Release permits
   before awaited task joins; reacquire for resumed parent execution. Integrate
   accepted-spawn counts and queue/wait-aware deadline/round limits.
   **Done:** four waiting parents can launch descendants without deadlock.
4. Add branch/run cancellation and exactly-once terminal result delivery, preserving
   original parent tool-call order and current steering settle boundaries.
   **Done:** sibling failures/cancels return bounded partial results honestly.
5. Persist child records and reconcile interrupted parent calls on resume.
   Isolate per-request usage from the client's current latest-request telemetry.
   **Done:** child traffic cannot replace the main context/spend display.
6. Run existing checks under [coordination constraints](../tooling-platform/role.md).
   Record unexercised scheduling, provider, and interruption cases; add no tests.
