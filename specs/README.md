# Lisa Spec Conventions

This directory is the source of truth for Lisa's behavior and delivery. The contract is
**spec per feature**: each feature has its own folder with tasks, context, role, checklist,
and status, so implementation cannot drift from the plan. The overarching product contract
stays in [v1-spec.md](v1-spec.md); feature folders refine it and never contradict it.

## Layout

```
specs/
  README.md              ← this file: conventions and feature index
  v1-spec.md             ← product contract (single source for FRs, acceptance, release gate)
  setup-plan.md          ← setup and delivery notes
  <feature>/             ← one folder per feature
    spec.md              ← WHAT and WHY: context, role, scope, functional changes, acceptance criteria
    tasks.md             ← HOW: ordered implementation tasks, each with verification
    checklist.md         ← progress gates, mirrors v1-spec.md wording where it overlaps
    context.md           ← links to the code, tests, and docs this feature touches
    role.md              ← the acting stance and constraints for whoever executes this spec
```

## Rules

1. A feature folder is created before its implementation starts; `tasks.md` begins empty of
   checkmarks.
2. `spec.md` states user-visible behavior only. Implementation notes belong in `tasks.md`.
3. Requirement wording in `spec.md` must match v1-spec.md's tone: verifiable sentences, no
   marketing, no unbounded "later" work. If a feature needs a new functional requirement,
   amend v1-spec.md first, then reference it here.
4. `checklist.md` items are phrased as observable outcomes, not code changes.
5. Status words are only: **planned**, **in progress**, **implemented (local)**,
   **verified (release)**. The release gate in v1-spec.md defines the last one.
6. The automated entry point for every feature is `go test ./...` from the project root;
   integration tests live in `tests/integration/`.
7. No skipped, always-passing, or mock-only tests may be used to claim a feature works.

## Feature index

| Feature | Folder | Status |
| --- | --- | --- |
| Predefined accepted providers (BYOK) | [predefined-providers/](predefined-providers/spec.md) | implemented (local) — hosted-provider probes outstanding |
| First-run provider setup in the TUI | [first-run-setup/](first-run-setup/spec.md) | implemented (local) |
| Slash commands in the prompt input | [slash-commands/](slash-commands/spec.md) | planned — after the release gate |
| curl installer for released binaries | [curl-install/](curl-install/spec.md) | implemented (local) — no release published |
