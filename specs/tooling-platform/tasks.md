# Tasks: three-phase tools and agents delivery

**Status:** Phase 1 implemented locally; walkthrough/release verification pending.
Phases 2–3 remain planned. See [implementation evidence](implementation.md).
Checked delivery items mean integrated code, not an exercised release claim.

## Before implementation

- [x] User checks the [phase scopes](README.md) and authorizes application changes.
- [x] Record existing worktree changes and baseline build/test results without
      reverting user work. Preserve the untracked files present during drafting.
- [x] Coordinator freezes catalog/call/result/approval/task/event/persistence
      interfaces and worker write sets before parallel integration.

## Eight parallel worker areas

Deploy eight implementation subagents after approval. Assign independent
modules/files; workers build against agreed contracts and send integration
patches to the coordinator rather than racing on shared files. The coordinator
owns `internal/agent/agent.go`, `internal/tui/tui.go`, command composition,
shared model/config changes, and final snapshot wiring. Workers may prepare
modules across later phases, but expose them only after prerequisites pass.

| Worker | Feature ownership | Integration handoff |
| --- | --- | --- |
| 1 | Registry/schema/policy + MCP adapter | Catalog/invoke interfaces, qualified-name mapping |
| 2 | Repository inspection + root instruction loader | Cancellable scoped handlers, instruction snapshot |
| 3 | Reviewed exact-text edit engine | Multi-path proposals, preflight/apply/recovery result |
| 4 | Output capture/artifact storage | Private refs, pagination, caps/lifecycle |
| 5 | Nested explore scheduler/runtime | Task state/events, permits, budgets, terminal results |
| 6 | Tool/agent inspection UI + child telemetry presentation | Leaf views/focus actions, attributed tree/expansion |
| 7 | Questions/checklist/plan-mode/skills | Main-only handlers, pure interaction/state records |
| 8 | Brave search/public-HTTPS fetch | Private web setup, grants, transport/results |

Eight workers do not change the runtime four-child execution limit. Use
handoffs/dependency waits where needed instead of simultaneous edits to shared
files. Integration is implementation work, not an extra code-review pass.

For this Phase 1 checkpoint, the eight concurrent ownership areas were registry,
repository inspection, harness, exact edits, artifacts, inspection UI, MCP
identity, and command capture/session records. The broader table above remains
the later-phase scheduling map; no later-phase runtime was exposed.

## Phase 1: dependable core tools

- [x] Land [registry/catalog](../tool-registry/tasks.md) and common authorization.
- [x] Land [harness/root instructions](../agent-harness/tasks.md) and
      [main-loop continuation](../agent-loop/tasks.md).
- [x] Land [repository polish](../repository-tools/tasks.md), including Image 2
      dedicated-tool routing and Image 1 external-path refusal/gating.
- [x] Land [exact edits](../targeted-edits/tasks.md), whole-change review,
      stale refusal, safe parent creation, and honest partial-apply reporting.
- [x] Land [output/expansion](../tool-output/tasks.md) and qualified MCP names.
- [x] Record existing-check results and [core walkthrough](user-guide.md#phase-1-core-tools)
      evidence/gaps before exposing dependent agent workflows.

## Phase 2: awaited nested exploration

- [ ] Land [explore runtime](../explore-agents/tasks.md): depth, fresh contexts,
      shared spawn/deadline/round budgets, queued/waiting permit release.
- [ ] Land [agent inspection](../agent-inspection/tasks.md): child tree,
      transcripts/usage, branch cancellation, coordinated persistence.
- [ ] Record nesting, four-waiting-parent progress, failure/limit, branch/run
      cancellation, and interrupted-resume outcomes or mark them unverified.
- [ ] Keep writer/custom roles, background tasks, and expanded child tools disabled.

## Phase 3: supporting workflows

- [ ] Land [ask user](../ask-user/tasks.md), preserving drafts/queues and one answer.
- [ ] Land [checklist](../plan-todo/tasks.md) and separate [read-only mode](../plan-mode/tasks.md).
- [ ] Land [global Markdown skills](../markdown-skills/tasks.md) and documented strict format.
- [ ] Land [optional web](../web-tools/tasks.md), Brave disclosure, scoped grants,
      public transport/address policy, and no fallback providers/shell.
- [ ] Align shipped command/help/setup/provider claims with actual enablement.
- [ ] Record combined walkthrough evidence; keep unexercised behaviors unverified.

## Verification constraint

At integration checkpoints run existing `go build ./...`, `go test ./...`, and
`go test -race ./...`. Add no new automated tests and perform no code-review
pass. Do not delete, skip, or weaken existing checks to make them green.
Record baseline vs new failures and any proposed contract migration that needs
the user's decision. Manual acceptance guides describe scenarios, not newly
created test programs. Missing credentials/walkthroughs leave explicit gaps.

`implemented (local)` describes landed code plus its recorded evidence, not
automatic behavioral verification. `verified (release)` still requires the
published-binary/provider walkthrough in the product contract.

## Dependency map

```text
approved contracts
  ├─ registry ── harness/loop, repo tools, edits, MCP names
  └─ result/session identity ── output artifacts + expansion
       └─ phase 1 checkpoint
            ├─ explore scheduler ── agent tree/transcripts/usage
            └─ phase 2 checkpoint
                 ├─ questions + checklist + read-only mode
                 ├─ global prompt-only skills
                 └─ optional Brave + direct HTTPS fetch
                      └─ phase 3 checkpoint
```
