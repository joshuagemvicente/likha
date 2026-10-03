# Tools and agents: phase index

**Status:** Phase 1 implemented locally; walkthrough/release verification pending.
Implementation authorized on 2026-10-03 and started with eight parallel workers.
Phases 2–3 remain planned and disabled. See [implementation evidence](implementation.md).

Read [spec.md](spec.md) for release scope, [tasks.md](tasks.md) for scheduling,
[decisions.md](decisions.md) for shared bounds, and [user-guide.md](user-guide.md)
for the planned CLI workflow. Each linked feature folder has its own spec,
tasks, checklist, context, and role. Unchecked items describe work to do.

## Phase 1: core tools

| Feature | Scope | Prerequisites |
| --- | --- | --- |
| [Tool registry](../tool-registry/spec.md) | Validated dispatch, policy, provenance, `/tools`, safe concurrency | Shared contracts agreed before worker integration |
| [Harness](../agent-harness/spec.md) | Tool contract, real repository identity, root AGENTS.md | Registry catalog |
| [Agent loop](../agent-loop/spec.md) | Main-run checkpoint continuation and honest results | Harness; retain steering/cancel semantics |
| [Repository tools](../repository-tools/spec.md) | Directory/range reads, scoped discovery, ignores, partial results | Registry |
| [Targeted edits](../targeted-edits/spec.md) | Exact replacements/create, one complete multi-file review | Registry and current approval seam |
| [Tool output](../tool-output/spec.md) | Expandable rows, paginated private artifacts, caps | Registry result identity; session persistence |
| [MCP](../mcp-support/spec.md) | Qualified names, collision handling, visible server trust | Registry; current stdio adapter |

Exit walkthrough: inspect repository without unnecessary shell → create/modify
proposal → decline and approve → stale refusal → approved check → inspect
truncated output → cancel/resume. An external-path shell attempt still asks.

## Phase 2: nested explore

| Feature | Scope | Prerequisites |
| --- | --- | --- |
| [Explore runtime](../explore-agents/spec.md) | Awaited nested tasks, read-only enforcement, scheduler and budgets | Phase 1 execution/result contracts |
| [Agent inspection](../agent-inspection/spec.md) | Runtime tree, child transcripts/usage, branch cancellation, resume | Explore event/persistence contract; tool output |

Exit walkthrough: fan out children → nest once → queue past execution cap →
receive ordered results → cancel one branch → cancel all → resume interrupted
records. Waiting parents must not deadlock their descendants.

## Phase 3: supporting workflows

| Feature | Scope | Prerequisites |
| --- | --- | --- |
| [Ask user](../ask-user/spec.md) | One question, choices/free text, exactly-once answer | Registry interaction seam; preserved steering state |
| [Plan/todo](../plan-todo/spec.md) | One bounded persisted checklist | Session state and registry |
| [Plan mode](../plan-mode/spec.md) | Live read-only mode; MCP/shell/edit refusals | Registry mode policy; explore; distinct checklist |
| [Markdown skills](../markdown-skills/spec.md) | Strict global SKILL.md discovery/loading and user invocation | Harness, registry, command catalog |
| [Web tools](../web-tools/spec.md) | Brave search, direct public-HTTPS fetch, explicit consent | Registry network policy; private credential configuration |

Exit walkthrough: question without losing draft → update/resume checklist →
read-only mode with attempted forbidden action → load skill → authorize search
and fetch → refuse unsafe destination → clear grants by switching session.

## Phase 4: user-authored agent profiles (approved 2026-10-04)

| Feature | Scope | Prerequisites |
| --- | --- | --- |
| [User agents](../user-agents/spec.md) | Declarative AGENT.md profiles, built-in review profile, profile-scoped task dispatch | Explore runtime; registry scope intersection; skills-style discovery |

Deferred still: write-capable children (needs isolated worktrees), project-local
profiles (needs a trust/precedence policy), memory, AST/LSP, teams, plugins.

## Working rules

- Read a feature's `spec.md` and `role.md` before changing its code.
- Read `tasks.md` for ownership, dependency, and completion rules.
- Reach `context.md` when locating the current code seam; it is an inventory,
  not a claim that the new feature already exists.
- Use `checklist.md` to record observable evidence, including unverified items.
- Keep writers, teams, memory, AST/LSP, new MCP transports, project-local
  profiles, and executable extensions out of these phases; user-authored
  profiles moved into Phase 4 by user approval on 2026-10-04.
- Follow the [shared implementation constraints](spec.md#verification-and-authorization-to-work).
