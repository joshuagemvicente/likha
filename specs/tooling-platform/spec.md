# Feature: Tool and agent platform: three delivery phases

**Status:** Phase 1 implemented locally; release verification pending. Scope and
separate application implementation approval received on 2026-10-03.
Phases 2–3 remain planned. See [implementation evidence](implementation.md).

## Goal

Make repository work and nested exploration useful in Likha's CLI. Compare
selected workflows with Claude Code, OpenCode V2, OMP, Pi, and Codex CLI;
adopt useful behavior without promising feature-count parity.

This file coordinates the release. Each feature has its own behavior spec,
tasks, checklist, context, and role. Start with the [phase index](README.md).
Use the [CLI benchmark](../tooling-landscape/cli-workflows.md) for comparisons
and the [user guide](user-guide.md) for setup and operation requirements.

## Approved delivery phases

| Phase | Feature specs | Exit condition |
| --- | --- | --- |
| 1: core tools | [registry/catalog](../tool-registry/spec.md), [repository inspection](../repository-tools/spec.md), [targeted edits](../targeted-edits/spec.md), [output/expansion](../tool-output/spec.md), [harness](../agent-harness/spec.md), [loop](../agent-loop/spec.md), [MCP](../mcp-support/spec.md) | Inspect → propose → review → verify is legible, bounded, cancellable, and preserves existing approvals. |
| 2: exploration | [nested explore runtime](../explore-agents/spec.md), [agent inspection](../agent-inspection/spec.md) | Awaited nested children obey shared limits; users can inspect and cancel branches without losing the parent. |
| 3: supporting workflows | [questions](../ask-user/spec.md), [checklist](../plan-todo/spec.md), [read-only mode](../plan-mode/spec.md), [skills](../markdown-skills/spec.md), [web](../web-tools/spec.md) | Optional workflows have explicit setup, permission, failure, and resume behavior. |

Each phase contains multiple feature specs. Its exit condition is a manual
acceptance target, not a claim that passing existing tests covers new behavior.
Implementation scheduling and the eight-worker ownership plan live in
[tasks.md](tasks.md); the user must approve implementation separately.

## Pre-implementation baseline

`internal/agent/agent.go` declares and dispatches five built-in model tools:

| Tool | Current behavior |
| --- | --- |
| `glob` | Finds repository-relative regular-file paths. |
| `read` | Reads repository-confined UTF-8 text. |
| `grep` | Searches repository text with Go regular expressions. |
| `edit_file` | Proposes full-file replacement or creation; user reviews a diff before writing. |
| `run_command` | Runs a shell command from the repository cwd after explicit approval; it is not sandboxed. |

Configured MCP tools are appended to the tool list. MCP calls currently use
trust-on-first-use **per server, for the session**: the first call requires
approval; later calls to the trusted server do not prompt again until relaunch.
The MCP spec now uses that resolved contract throughout. Phase 1 adds
server-qualified identities without changing the trust grant.

There is no built-in web, ask-user, plan/todo, skills, LSP, AST, memory, or
subagent tool. `/compact`, `/mcp`, `/models`, etc. are app commands, not
model-callable tools. Related planned/deferred feature specs include
[`agent-loop`](../agent-loop/spec.md),
[`agent-harness`](../agent-harness/spec.md),
[`plan-mode`](../plan-mode/spec.md),
[`repo-init`](../repo-init/spec.md), and
[`custom-commands`](../custom-commands/spec.md).

## Vocabulary and product rules

- **Tool:** a capability the model can invoke, with a schema and an execution
  handler.
- **Skill:** reusable prompt guidance loaded on demand. A skill does not add
  tools, execute code, or grant permissions.
- **Agent:** a named prompt/model/tool profile. Defining an agent does not run
  it or grant it capabilities.
- **Task/subagent:** a parent-invoked operation that runs work under an agent
  definition and returns a result. The `task` tool is not the agent itself.
- **Permission:** the runtime decision about an operation's effects. A tool or
  agent's requested permissions cannot override the user's policy or approval.

Keep these concepts separate in the model descriptions, TUI, config files, and
docs. In particular, `/agents` is a user-facing management surface; it is not a
delegation call. Peer teams, shared mailboxes, and autonomous agent swarms are
outside the approved phases.

## Architecture contract

Replace the agent loop's parallel hard-coded tool-definition list and dispatch
switch with a registry-backed module. Its small external interface should let
the loop:

1. request the definitions available for a specific run/agent/mode; and
2. invoke one named tool with validated arguments and receive a bounded result
   or a typed error/refusal.

The registry owns name lookup, schema validation, source attribution,
eligibility filtering, dispatch, result limits, and the common authorization
path. Keep handlers and source-specific details behind this seam.

Each registered tool declares, at minimum:

- stable name, description, input schema, and source (`builtin`, MCP server, or
  future adapter);
- effect class (`read`, `write`, `exec`, or `network`), whether it can ask the
  user, and whether it is safe to run concurrently;
- cancellation behavior and output limits; and
- any platform/backend requirements that can make it unavailable.

The concrete representation and package placement are implementation details
for the registry tasks. It must not create an import cycle with the turn loop or
UI. UI approval travels through the existing approval seam; tool code does not
directly render dialogs or mutate TUI state.

### Authorization invariants

1. Every write, shell execution, and network operation is authorized centrally;
   there is no built-in or custom-agent bypass path.
2. `run_command` approval remains explicit and shows the exact command, cwd,
   and the warning that cwd is not a sandbox. A later sandbox is a separate
   control, not a reinterpretation of approval.
3. Edit/patch tools present a complete reviewable diff before writing and
   reject stale proposals.
4. Preserve current MCP session-scoped server trust until an explicit decision
   changes it. Attribute every MCP call to its server/tool. Plan mode blocks
   MCP calls unless a future reviewed policy says otherwise.
5. A child's effective tools and permissions are the intersection of the
   parent's effective capabilities, user policy, and that agent's allowlist.
   An agent definition can narrow capabilities, never add or approve them.
6. Tool refusal, invalid input, timeout, cancellation, and truncation are
   returned to the model and shown to the user; none is reported as success.

## Approved capability scope

The three phases keep existing approval behavior while making routine code
work more complete and legible. Feature specs own their detailed contracts.

### Required capability set across the three phases

- **Repository inspection:** retain `glob`, `read`, and `grep`, add directory
  listing, line ranges, scoped searches, and partial-result metadata. Respect
  ignores by default. Steer repository inspection to these approval-free,
  repository-confined tools; keep shell approval if the model chooses shell.
- **File changes:** support create and unique exact-text replacements in
  addition to the current full-content proposal. Every write still produces
  one complete diff review, stale checks, explicit approval, and a visible
  result. Preserve existing tool-call compatibility while names/schemas are
  migrated. A multi-file proposal has one review; a stale target blocks it.
  Deletion, rename, fuzzy patches, and binary edits are deferred.
- **Shell:** retain `run_command` with the current exact-command review and
  explicit unsandboxed warning. Do not add blanket command approval.
- **Ask the user:** add an `ask_user` interaction for a concise question and
  optional choices. A pending question is cancellable; its answer returns as a
  tool result exactly once.
- **Plan/todo:** add one persisted, bounded checklist tool. Implement `/plan`
  separately as a live permission mode; checklist updates grant no authority.
- **Web:** add configured Brave `web_search` and direct public-HTTPS
  `web_fetch`. First use asks for conversation-scoped backend/origin consent.
  Results identify sources, destination, limits, and data flow. Search requires
  setup; fetch requires explicit enablement. Neither falls back to shell or a
  different vendor. Local conversion makes no extra model request.
- **Skills:** load strict global `<stateDir>/skills/<name>/SKILL.md` prompt
  packs on model or user invocation. Skills add instructions, not tools,
  executable resources, configuration changes, or permissions. Custom slash
  command templates remain a separate deferred feature.
- **Delegation:** add awaited `task` and repository-read-only `explore`.
  Main depth 0 may create depth-1 children, which may create depth-2 explore
  children. Four children may execute across the entire tree; waiting parents
  release execution slots. Each child gets a scoped brief and the active
  parent model. Shared spawn and per-child time/round limits apply.
- **MCP:** retain the existing stdio adapter and session-scoped TOFU behavior;
  make tool provenance visible and prevent name collisions.
- **Outputs:** use compact expandable rows and capped private, session-linked
  artifacts. Retained output does not enter model context until requested.

### Shared UX

- Add `/tools` (or equivalent) to inspect the active tool catalog, source,
  concise description, and permission class. Clearly distinguish unavailable
  optional tools from configured tools.
- Tool activity and results stay in the transcript; activity indicators do
  not replace the tool name/result. Keep approval previews focused on the
  operation and decision the user must make.
- Add `/agents` for the explore capability profile and runtime tree: parent,
  depth, queued/running/waiting status, budgets, usage, transcript, and branch
  cancellation. Do not imply user-authored profiles exist yet.

## Deferred roadmap — not approved for these phases

These items require a separate interview, scope approval, and feature specs.
They are not dependencies or exit gates for the approved three phases.

- **User-authored agents:** support declarative Markdown/frontmatter profiles
  under the private Likha state directory, with optional project-local profiles
  only after a trust/precedence policy is specified. Initial fields: stable
  name, description, instructions, optional compatible model, and tool
  allowlist. Reject duplicate/reserved names and malformed definitions
  visibly. Definitions are data/prompt, never executable code.
- **Useful built-in profiles:** `explore` (read-only), `review` (read/search
  plus optional LSP), and `implement` (write-capable only when isolation is
  available). Users can create their own roles without changing global tool
  permissions.
- **Write-capable child agents:** require an isolated worktree or equivalent
  per-child workspace. The parent reviews/promotes resulting changes; no
  concurrent writers share the live checkout. Until isolation exists,
  write-capable children remain disabled. An isolated workspace does not
  itself authorize writes; the future spec must define approval there too.
- **Code intelligence:** optional AST query/edit and LSP symbol/diagnostic
  adapters. Missing language servers or unsupported languages degrade
  gracefully to `grep`/`read`; semantic tools never silently fall back to
  shell commands.
- **Memory:** opt-in, visible, project-scoped memory with read/write/forget
  controls, size limits, and a clear “sent to the configured provider” notice.
  Memory is not inferred from secrets and is never a permission source.
- **Policy configuration:** per-tool and per-agent allow/ask/deny rules may be
  added only with a dedicated permission spec and visible explanation of
  persistence scope. Agent frontmatter is not the policy file.

## Outside the approved phases

- Importing Claude Code, OpenCode, OMP, Pi, or Codex private runtimes;
  integrate their useful concepts, not their private runtime.
- Arbitrary user-authored executable plugins, hooks, or scripts. MCP remains
  the existing external tool integration mechanism.
- Agent teams, peer messaging, unbounded recursion, or background agents.
  Bounded nested explore is in scope; arbitrary recursive swarms are not.
- Automatic approval, hidden shell execution, or treating a cwd restriction as
  a sandbox.
- Making optional LSP/AST or web setup a prerequisite for repository work,
  or silently sending project content to a new provider. Brave is the chosen
  optional first search adapter, not a mandatory service for using Likha.

## Release gates

### Three-phase evidence and release gate

- The registry and policy path cover built-ins, MCP, web, and task dispatch;
  no mutating or network tool has an alternate path.
- Existing regression checks run with recorded results; new question, plan,
  skill, web, and read-only task flows have recorded walkthrough evidence or
  explicit unverified labels. No new automated tests are requested.
- Acceptance guides name cancellation, refusal, malformed schemas, unavailable
  backends, output caps, stale edits, and session resume; record which were
  exercised rather than equating old tests with their coverage.
- A human walkthrough covers inspect → plan/question → edit/review → verify,
  shell approval, web source display, MCP first-use trust, child result/cancel,
  narrow terminal layout, and terminal restoration.
- At least one supported hosted provider has a live structured-tool probe for
  the shipped core. README/provider claims remain limited to providers actually
  probed; unverified tool behavior is labelled honestly.

### Deferred-feature gates (not current release gates)

- User-authored agent profiles load, validate, list, and fail safely; their
  allowlists cannot exceed parent/user permissions.
- Read-only child agents pass cancellation, context-isolation, output, and
  resume tests. Write-capable agents remain disabled until isolated-worktree
  review is complete.
- Optional AST/LSP and memory features each have independent enablement,
  lifecycle/error handling, documentation, and tests.
- Provider compatibility claims match live tests for each advertised provider;
  setup docs explain optional backends, trust, permissions, data flow, and
  recovery from a failed tool.
- macOS and Linux alpha/beta walkthroughs pass; users can disable optional
  tools and return to the five-tool baseline.

## Verification and authorization to work

The approved implementation constraint is eight parallel workers on independent
areas, no new tests, and no code-review pass. Run existing build/test checks and
record their results. Do not delete, skip, or weaken existing checks to claim
completion. Document manual scenarios for behavior existing tests do not cover.

The phase gate above describes evidence still required before a release claim;
it does not request new automated test creation. Passing old checks alone is
insufficient to mark a new feature `verified (release)`. Until a scenario has
been exercised, label its new behavior unverified.

The user separately authorized implementation from the start of the phase index
and requested parallel subagents. Phase 1 used eight independent worker areas.
During integration, the user also approved adapting the existing context-tracker
test assertions to the new harness/catalog contract without adding tests or
weakening its history, steering, usage, and estimate checks.
