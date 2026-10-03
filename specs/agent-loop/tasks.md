# Tasks: main agent loop

1. Coordinate the single compiled tool-contract system layer with the harness
   owner. Compose it per request outside stored conversation history.
   **Done:** request-order and snapshot/resume evidence shows no duplication.
2. Replace the main 32-round hard failure with a visible notice and runtime-only
   continuation instruction; preserve cancellation, steering safe points, and
   unexecuted-tool reconciliation. Child runs use their separate round policy.
   **Done:** exercised main checkpoints continue; child limits do not reset.
3. Render the attributed notice without ending working state or discarding the
   live composer/steering queue. Continue normal approval for every effect.
   **Done:** notices, draft, pending decisions, and cancellation remain consistent.
4. Run existing build/test/race checks under
   [coordination constraints](../tooling-platform/role.md). Record checkpoint/
   provider outcomes or missing evidence; create no tests and add no review pass.
   **Done:** landed status and verification gaps are accurate in the index.
