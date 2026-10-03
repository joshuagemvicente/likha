# Tasks: planned MCP registry enhancement

Baseline stdio/TOFU is implemented (local); these unchecked tasks describe
Phase 1 identity improvements, not a reimplementation or new transport.

1. Route discovery/calls/trust through the registry adapter; retain server
   launch, timeout, crash, and in-memory approval behavior.
   **Done:** first-use server scope and later-call provenance remain visible.
2. Generate stable server-qualified model names, retain original identity,
   reject collisions, and allow only unambiguous non-built-in legacy aliases.
   **Done:** duplicate raw tool names dispatch to the intended server.
3. Preserve historical name/result decoding without redispatch on resume.
   Make manager-lifetime trust distinct from web conversation grants.
   **Done:** old sessions cannot grant trust or select ambiguous aliases.
4. Apply child/plan eligibility before calls, explain startup/cancellation
   limits, and run existing checks under [shared constraints](../tooling-platform/role.md).
   **Done:** unverified new naming/dispatch cases remain labelled.
