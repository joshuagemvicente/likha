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

## Amendment 2026-10-05: questionnaires and asking rules

5. Extend the `ask_user` schema and validation (`internal/tools/ask_user.go`)
   with `questions` (1–4), option objects (label, description, recommended),
   the one-recommended rule, and the either-form rule. Keep the single-question
   form and its result text unchanged.
   **Done:** validation tests for every bound and refusal.
6. Extend `internal/tui/ask_view.go` with per-question pages, `n of N`,
   Recommended tag and initial focus, muted descriptions, ←/→ navigation, and
   whole-questionnaire submission.
   **Done:** UI tests for navigation, edit-earlier-answer, skip, all-skipped,
   and narrow layout.
7. Return one per-question result; persist and render it in the transcript
   (`internal/tui/tool_items.go`, `internal/transcript/task.go`) and keep
   resume reconciliation for interrupted questionnaires.
   **Done:** exactly-once and resume tests cover the batch form.
8. Add the "When the agent asks" rules to `internal/agent/prompt.md`.
   **Done:** a prompt-content test; the user runs the live probe.
