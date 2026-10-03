# Tasks: exact edits

1. Register the explicit operation schema and prepare all final file snapshots
   from original content; reuse existing preview-safe text and path checks.
   **Done:** ambiguous/overlapping/invalid operations yield no proposal/effect.
2. Extend proposal data to include all paths/parents and one full review body.
   Preserve current read-to-end and narrow-terminal approval behavior.
   **Done:** one complete review governs the whole prepared change.
3. Implement group preflight, staging, publication, and safe partial-failure
   reporting. Document the non-atomic crash boundary and recovery manifest.
   **Done:** stale refusal/recovery walkthroughs identify actual disk state.
4. Route `edit_file` through the same reviewed apply contract where compatible;
   leave original name/required fields and historical results intact.
   **Done:** existing edit workflows and records remain usable.
5. Run existing checks under [shared constraints](../tooling-platform/role.md).
   Record unexercised race/crash cases; add no tests or review pass.
