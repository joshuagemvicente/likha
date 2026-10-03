# Feature: harness rules, repository identity, and root instructions

**Status:** implemented (local); new acceptance walkthroughs unverified.
**Phase:** 1. **Product requirement:** FR-19.
See [implementation evidence](../tooling-platform/implementation.md).

## Scope

Include one compiled harness system message first on each agent request. State
Likha's role, the actual canonical repository path, available capability profile,
tool-selection guidance, permission boundaries, and honest result reporting.
Use repository glob/read/grep for inspection; reserve shell for operations that
need it. A guessed `/workspace` is not a replacement for the selected root.

The [agent-loop](../agent-loop/spec.md) feature owns main checkpoint continuation.
The registry owns capability enforcement; instruction text cannot enforce a
permission or guarantee that every model chooses dedicated tools.

## Root AGENTS.md

Load only the selected root's `AGENTS.md` using repository confinement. Missing
file is a no-op. Reject unsafe/symlink, non-UTF-8, or oversized instructions with
a visible warning and keep the compiled harness usable. The cap is 32 KiB;
do not truncate a partially loaded rule. Nested/global/compatibility instruction
files, imports, and `/init` generation remain outside this feature.

Project text gives workflow guidance but cannot grant capabilities or override
user decisions. Include it in one attributed developer-context message after
the harness system message, clearly scoped to this repository. Keep identity
and instruction content separate from the saved user conversation. Provider
transports must preserve the role/order rather than duplicate it during encoding.

Freeze root instructions for a run and its children. Re-read on the next main
turn, not while a pending operation is being authorized. Bound available skill
metadata separately in Phase 3; loading skill bodies remains an attributed
interaction, not a second root instruction file.

## Request and history lifecycle

Order is system harness/runtime identity → optional project context → prior
conversation → current user/task prompt, with the effective tool catalog.
Rebuild harness-owned layers for each provider request. Persist visible user/
assistant/tool results and checkpoint notices, not duplicated harness/project
messages. Resume applies current compiled rules/root instructions to the next
turn without turning them into historical approvals.

Children get the same base rules/root snapshot plus their restricted role,
depth/budget, and scoped brief. Main-only tool instructions do not promise a
child access to absent tools. Returned repository/web/child text supplies data;
dispatch and actual user approvals remain authoritative.

## Acceptance guide

- Inspect an actual request: one system message first, real root/profile,
  optional bounded project message next, no duplication across rounds.
- Open without AGENTS.md, with a valid file, and with oversize/unsafe content;
  normal work remains usable and failed loads identify source/limit.
- Ask the Image 2 inspection and Image 1 external-path workflows; document
  observed model selection and unchanged shell gating.
- Change instructions mid-run, resume, and delegate; current run/children keep
  their snapshot and the next turn loads the new safe root text once.
- Include text requesting skipped approvals; runtime refuses unapproved effects.
