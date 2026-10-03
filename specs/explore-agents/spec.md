# Feature: awaited nested explore agents

**Status:** planned. **Phase:** 2. **Product requirements:** FR-28.
Depends on Phase 1 registry, cancellable reads, and output identity.

## Invocation and context

The main model invokes `task` with `agent: "explore"`, a short `description`,
and a scoped `prompt`. Only the built-in explore role exists in this phase.
Use a maximum 120-character description and 32-KiB brief. An accepted call
creates a stable task ID, parent/run/session identity, depth, deadline, and
round/spawn accounting; its tool result arrives when the child terminates.
Independent task calls may execute concurrently, within the shared scheduler.

Each child starts a separate model context with the compiled harness, actual
canonical repository, root instructions snapshot, and explicit brief/findings
or file references supplied for that task. Do not clone the parent conversation,
credentials, pending approvals, or automatic output artifacts. Child findings
are untrusted task data, not instructions that supersede user/runtime policy.
Use the active parent provider/model; no cheaper-model or provider fallback.
If the inherited model/context is unavailable, return an attributed failure.

## Enforced capability ceiling

At depth 1, catalog only repository `glob`, `read`, `grep`, and `task` that can
spawn explore. At depth 2, omit `task` and refuse a crafted spawn call too.
Both depths exclude edits, shell, MCP, web, skills, questions, checklist, and
artifact-read tools. Parent capability ∩ user/mode policy ∩ explore allowlist
defines the effective set. An arbitrary task prompt cannot add a capability.
Record a forged-tool refusal and return it to the child model without effects.

Depth counts the main agent as 0. The allowed tree is main → explore → explore.
Nested spawning is required; this feature must not silently degrade to a
single-level agent. Plan mode still permits the same bounded read-only tree.

## Scheduler and budgets

The [shared decisions](../tooling-platform/decisions.md) define four executing
children, 16 accepted children per main run, a five-minute per-child deadline,
and 32 model requests per child. Count nested, finished, failed, and cancelled
accepted children against the total. Refuse a seventeenth creation explicitly.
Malformed/refused spawn requests that never create a child do not use that count.

Queued/waiting parents hold no execution permit. A running parent that invokes
children releases its slot before awaiting them, and reacquires permission
before its next model/read execution. A FIFO-ready queue schedules descendants
and resumed parents without exceeding the run-wide cap. Concurrency is not
four slots per parent, and the eight implementation workers are unrelated.

Start deadlines at accepted creation, including queue and nested wait time.
Use the earlier of child and ancestor deadlines. Keep timers active while
queued/waiting, remove expired work without executing it, and bound provider
requests and repository walks by that context. A cancellation-resistant provider
response cannot start subsequent child tools or turn an expired task into success.

The child's 32-request cap is terminal for that child, unlike main-run checkpoint
continuation. At exhaustion, return available bounded findings with `limited`
status and the reason. Do not issue a hidden 33rd summarization request. Existing
main FR-20 continuation is unchanged and cannot reset a child's limits.

## Results, failures, and steering

Return task ID, role/depth, final status, findings with repository file/line
references when known, tool refusals/errors, and budget/completeness metadata.
The inline cap applies. Preserve original parent tool-call result ordering even
when siblings finish in a different order; attributed progress may appear sooner.
Merge only this bounded result into the parent model history, not the child
conversation. Persist the detailed child record for user inspection.

Child error, timeout, or budget exhaustion returns an honest partial/failure
result; the parent can continue or retry within the remaining shared budget.
No failure automatically widens tools or falls back to shell/main write access.
The parent can verify findings with its own tools; read-only work supplies no
permission to edit. Parent steering prompts wait for a safe tool-settle point,
as FR-21 requires, rather than mutating a child's hidden context mid-request.

Whole-run cancellation stops every descendant and pending task. Branch cancel
stops the selected task and its descendants; deliver one cancelled result to its
parent. Cancelled nodes do not free historical spawn-budget count. Drain/report
terminal outcomes and stop new work, even if results arrive after cancellation.

## Persistence and interrupted resume

Save task identities, relationships, brief, tools/results, status/budgets, and
provider-reported usage in private session state. Exclude transient channels,
live credentials, current permissions, and in-flight handles. On relaunch, mark
queued/running/waiting tasks interrupted and show available partial results.
Resolve the parent transcript's interrupted call record without replaying the
task. A new explicit task call uses a new ID and fresh run budget.

## Acceptance guide

- Delegate one scoped exploration; confirm fresh context and same model.
- Launch siblings, nest from each, and inspect depth-2 tool definitions. A
  forged depth-3/forbidden-tool request is refused at dispatch.
- Fill four slots with depth-1 agents awaiting nested work; descendants still
  execute and parents resume without exceeding four executing children.
- Queue extra tasks, reach spawn/time/round limits, and cancel a branch. Return
  limited/cancelled findings with no surviving subsequent work or duplicate result.
- Cancel the whole run, interrupt the app, and resume. Inspect partial records;
  neither task nor approval replays, and main/child usage stays attributable.
