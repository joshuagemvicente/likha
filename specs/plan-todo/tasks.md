# Tasks: plan/todo

1. Register one bounded full-list update schema and immutable session plan record.
   **Done:** invalid updates preserve prior list and return accurate errors.
2. Add backward-compatible private persistence and interruption annotation via
   the coordinated session writer; distinguish declared completion from evidence.
   **Done:** resumed/legacy plans display without scheduling or permissions.
3. Add compact progress, `/todo`, active Inspect, and full-list help text.
   **Done:** users can inspect all steps without losing composer/queue state.
4. Connect read-only mode eligibility and run existing checks under
   [shared constraints](../tooling-platform/role.md); record unverified cases.
