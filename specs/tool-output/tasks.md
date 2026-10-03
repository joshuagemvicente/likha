# Tasks: tool output

1. Define output reference/completeness metadata on the registry result contract.
   **Done:** execution status and payload completeness are distinct everywhere.
2. Add private session-owned artifact storage with byte accounting, safe IDs,
   paginated reads, interrupted metadata, and clear/delete lifecycle.
   **Done:** cap/storage/missing-reference walkthroughs have recorded outcomes.
3. Adapt command capture and large results to keep bounded previews and drain
   remaining streams while respecting retained-output caps.
   **Done:** long output cannot block solely because retention stopped.
4. Register main-only `read_output`; implement expandable focused transcript rows.
   Preserve pending complete review and current native-selection/scroll behavior.
   **Done:** model retrieval and UI expansion have distinct, visible semantics.
5. Run existing checks under [shared constraints](../tooling-platform/role.md);
   report new capture/persistence cases still unverified.
