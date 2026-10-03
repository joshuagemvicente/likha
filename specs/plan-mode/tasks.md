# Tasks: plan mode

1. Add a live main-run mode flag and eligibility/policy intersection in the
   registry. Refuse all workspace writes, shell, and MCP before any review.
   **Done:** crafted calls and trusted MCP cannot bypass the gate.
2. Toggle `/plan` only while idle; merge mode guidance into the single harness
   system layer. Keep mode out of persisted permissions/runtime scaffolding.
   **Done:** resume starts normal and repeated requests do not duplicate policy.
3. Add a persistent status marker and help distinguishing mode from `/todo`.
   **Done:** marker stays visible through inspection/narrow layouts.
4. Exercise allowed repository/explore/question/checklist/skill/output flows and
   explicit web consent, then leave mode and prepare fresh normal approvals.
   **Done:** no permitted session state operation implies repository write access.
5. Run existing checks under [shared constraints](../tooling-platform/role.md);
   document unverified refusal/UI cases without creating tests or reviewing code.
