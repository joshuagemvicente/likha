# Tasks: ask user

1. Register the bounded main-only schema and typed pending-question/result state.
   **Done:** validation and source/call identity exist before UI interaction.
2. Add dedicated choice/free-text/Skip rendering through the shared interaction
   seam. Preserve composer drafts/queues; keep answers distinct from approvals.
   **Done:** answer/skip/manual-cancel walkthroughs retain steering state.
3. Add exactly-once delivery and interrupted-history reconciliation, including
   late submissions after cancel and legacy session loading.
   **Done:** resume cannot reopen or infer an unanswered question.
4. Add help/setup-free user guidance and run existing checks under
   [shared constraints](../tooling-platform/role.md); record unverified UI cases.
