# Phase 2 implementation evidence — 2026-10-03

**Status:** integrated; final checks in progress. The user separately
authorized Phase 2 and another eight parallel implementation workers.
Phase 3 remains disabled.

## Worker outcomes

- Workers 1–5 and 7–8 delivered their owned files. Worker 6 (persistence) hit
  its usage limit mid-task and was redeployed; it finished and hardened
  `internal/session/task_records.go`: an additive `task_records` table
  (schema version unchanged, `edit_journal` precedent), `SaveTask`/`Tasks`
  store methods, `Snapshot.Tasks` with `sameConversation` inclusion, and
  `ResumeTasks` reconciliation. Worker 5's follow-up (physical request
  budget) also hit its limit, but its owned `internal/model/fork.go` already
  contains the completed `ForkForTask` gate, and the child loop consumes it.
- The coordinator composed shared contracts: `RunOptions` task fields, frozen
  `TurnEvent` task/task_runtime/tool_checkpoint kinds, `tools.Result`
  `SourceTaskID` retention keys, the `/agents` command, snapshot/UI wiring
  (`internal/tui/task_wiring.go`), main-prompt guidance, and these records.

## Recorded decisions

- `model.TokenUsage` keeps its existing public shape (prompt-seen flag only)
  because existing tests compare whole structs; widening it would require
  rewriting existing test expectations, which the constraints forbid. Child
  usage therefore treats a usage event that named prompt tokens as one full
  report; a prompt-only report is indistinguishable and its completion total
  is recorded as known zero. The limitation is documented at
  `internal/explore/node.go` (summarizeUsageLocked) and here.
- `tool_checkpoint` events persist the batch's unexecuted calls with sentinel
  tool results (the pre-existing "Error: action not executed; run
  interrupted" text), so a crash mid-batch resumes with a protocol-valid
  history and `ResumeTasks` upgrades task sentinles to attributed outcomes.
  No automatic replay occurs.
- Persistence contract: `SaveTask` writes one versioned row per task in a
  single-connection transaction (rejects foreign sessions, wrong roots, stale
  versions via `ErrTaskStale`, and over-budget payloads via `ErrTaskCapacity`
  at 32 MiB per record; 4096 records / 256 MiB per session, 16384 / 512 MiB
  per database). `Store.Save` commits the snapshot update and a full
  task-row rewrite in one transaction, so blobs and rows never disagree after
  a crash; `nil` `Tasks` (legacy/empty snapshots) rewrites nothing so a save
  without tasks never deletes live runtime rows. A non-nil list is treated as
  the caller's complete authoritative set — shipped wiring serializes
  `acceptTaskRecord` and `persist()` on the UI goroutine, and a transient
  dropped row self-heals on the next task event and save. `Tasks()` skips
  undecodable/damaged rows (owner violations still fail the read); invalid
  records during a rewrite stay blob-only and reconverge through the version
  merge on the next resume. `ResumeTasks` stamps `FinishedAt`, bumps the
  version, dedupes warnings, and repairs parent history idempotently.

## Constraints and baseline

- No new tests and no code-review pass. Existing checks remain intact.
- Phase 1's latest build, full test suite, and race suite passed before this
  phase. The recorded Phase 1 manual/provider gaps remain unverified; the user
  explicitly requested proceeding with Phase 2 without claiming release evidence.
- Existing Phase 1 changes and unrelated README/CHANGELOG, app/model/provider,
  setup/model-menu changes and tests remain in place. Pre-existing untracked
  `internal/tui/sel_probe_test.go.bak` and `lisa` are preserved.

## Eight concurrent ownership areas

| Worker | Owned area |
| --- | --- |
| 1 | FIFO run-wide execution permits and cancellation-safe waiting |
| 2 | Accepted task identities, nesting, budgets, deadlines, branch/run lifecycle |
| 3 | Fresh scoped child model loop and awaited nested task joins |
| 4 | Explore prompt/profile, capability intersection, task schema/result adapter |
| 5 | Inherited client forks, isolated telemetry, physical request-budget hook |
| 6 | Private child-record persistence and interrupted history reconciliation |
| 7 | Agent profile/tree, task-row inspection, branch-cancel confirmation |
| 8 | Child transcript/artifact inspection and separate usage presentation |

The coordinator owns shared turn-loop/registry/TUI composition, snapshot wiring,
commands/help, and these evidence records. Eight implementation workers do not
increase the four-executing-child runtime limit.

## Intended integrated contract

- Main depth 0 can await depth-1 explore; depth-1 can await depth-2 explore.
  Depth-2 spawning and all write/shell/MCP/web/question/skill/artifact-read calls
  are refused by the child registry, not merely discouraged by a prompt.
- Shared run limits: four executing children and 16 accepted children; each
  child gets five minutes from acceptance, including queues/waits, and at most
  32 generating model requests. Ancestor deadlines apply. Waiting parents
  release execution permits and rejoin the FIFO-ready queue before resuming.
- Child contexts contain the compiled explore instructions, actual root, frozen
  root instructions, and explicit brief/file references—not the parent's history
  or approvals. Forks inherit the active provider/model without fallback and
  cannot overwrite the main client's context/rate/token telemetry.
- Task results are awaited and merged in original parent tool-call order.
  Failures, limits, and cancellation retain attributed partial findings. Main
  steering settles only after the current tool group; child contexts are not
  silently steered or granted broader tools.
- Task records are private and independently versioned by task identity, while
  one UI writer coordinates the display snapshot. Interrupted resume repairs
  saved call results without spawning, rerunning, or restoring permissions.
- `/agents` and focused task rows inspect the profile/tree, child transcript,
  bounded local artifact pages, and measured child usage. Dedicated branch
  confirmation cancels a node and descendants; Esc/Ctrl+C cancels the whole run
  even inside inspection. Drafts and held steering prompts remain separate.

## Walkthrough outcomes — 2026-10-03

A throwaway probe program (`probe/`, deleted after recording) drove the real
runtime — `explore.Manager` + `agent.ExploreRunner`, the explore registry, fork
telemetry, and private session persistence — against a scripted fake
OpenAI-compatible SSE provider, under `go run -race`. Every recorded outcome
passed with zero data races:

- Fresh child contexts: a child's first request carries at most system,
  developer, and one user brief; markers from other tasks never leaked, and the
  inherited model name appears on every request.
- Depth-2 nesting completed through the awaited task tool; the depth-2 record,
  per-fork completion-token totals (distinct per task), and the parent's saved
  transcript all confirmed isolation.
- Forged child calls were refused by the registry: edit/run_command refusals
  inside a depth-1 child, and a depth-2 child's task call (the tool is absent
  at max depth). The child continued within its own budget.
- Five concurrent parents under four execution permits, each nesting a child,
  all ten tasks completed without deadlock — waiting parents release slots.
- The seventeenth accepted child was refused at the 16-task run budget.
- An endless tool-calling child stopped at 32 generating requests with
  `limited`, the reason naming the limit, and partial findings retained.
- A caller deadline (800 ms against a five-second provider stall) returned
  `limited` promptly, including queue/wait time.
- Branch cancellation returned `cancelled` for the target task while the
  sibling completed normally; whole-run cancellation settled both tasks.
- Private persistence: completed and running records saved with versions; a
  reopened store reloaded them; a simulated crash record became `interrupted`
  with reason/warnings, the parent's sentinel tool result was replaced with a
  bounded attributed outcome, and a second resume made no further change.
- Ordered tool batches preserved exact call order with legitimate
  read/grep successes.

One contract was corrected during the walkthrough: the model-facing task
outcome's `accepted_children` reported the shared run-wide count; it now
reports the task's own accepted descendants (record/outcome split documented in
`internal/explore/node.go`/`records.go`), while `/agents` keeps displaying the
shared budget from the private record.

## Final checks

- `go build ./...` — passed (after the walkthrough corrections).
- `go test ./...` — passed.
- `go test -race ./...` — passed, and the probe itself ran under `-race` clean.
- Scoped `git diff --check` and gofmt over changed files — clean.

## Still unverified (require interactive TUI or live services)

- ~~`/agents` entry, tree/detail rendering, artifact paging, focused task-row
  inspection, and confirm-dialog key handling in a real terminal, including
  narrow layouts and resize.~~ **User-verified 2026-10-04:** the `/agents`
  task tree and profile display work as intended in the live TUI. The
  remaining inspection sub-scenarios (artifact paging under jumbo outputs,
  confirm-dialog key handling, narrow/resize layouts) stay unverified.
- Missing-artifact presentation and bounded local artifact pages under real
  large outputs.
- Live hosted-provider probes (ChatGPT/Codex Responses wire, real rate-limit
  and usage telemetry variance) and published-binary behavior.
- Cancellation cannot reverse external effects; no live external-work scenario
  was exercised (the child ceiling is repository reads only).
