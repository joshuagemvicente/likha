# Feature: Tool and agent platform (alpha → beta)

**Status:** proposed roadmap — architecture and release scope for review; no
implementation commitment beyond the existing tool and MCP contracts.

## Goal

Make Likha/Likha's tools predictable, safe, inspectable, and extensible enough for
alpha and beta users. Add the practical capabilities users expect from coding
agents without importing another product's private runtime or shipping an
unbounded plugin system.

This is a coordinating spec. Existing feature specs remain authoritative for
their detailed behavior; follow-on specs are required before implementing new
capability families. The comparative research is in
[`tooling-landscape/research.md`](../tooling-landscape/research.md).

## Current baseline

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
The implementation is the current behavior, but `specs/mcp-support/spec.md`
contains conflicting per-call approval wording that must be reconciled.

There is no built-in web, ask-user, plan/todo, skills, LSP, AST, memory, or
subagent tool. `/compact`, `/mcp`, `/models`, etc. are app commands, not
model-callable tools. Existing related drafts include
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
not part of alpha or beta.

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
for the registry spec. It must not create an import cycle with the turn loop or
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

## Alpha scope — dependable daily-use core

Alpha is the first user-testable version. It keeps current safe behavior while
making routine code work more complete and legible.

### Required capability set

- **Repository inspection:** retain `glob`, `read`, and `grep`; steer the model
  to use them instead of shell search. Read-only tools remain repository
  confined and need no approval.
- **File changes:** support create and targeted edit/patch operations in
  addition to the current full-content proposal. Every write still produces
  one complete diff review, stale checks, explicit approval, and a visible
  result. Preserve existing tool-call compatibility while names/schemas are
  migrated.
- **Shell:** retain `run_command` with the current exact-command review and
  explicit unsandboxed warning. Do not add blanket command approval in alpha.
- **Ask the user:** add an `ask_user` interaction for a concise question and
  optional choices. A pending question is cancellable; its answer returns as a
  tool result exactly once.
- **Plan/todo:** add one visible, bounded plan tool (create/update/complete
  steps) rather than several redundant task CRUD tools. Planning state is
  session UI state, not permission to edit or execute.
- **Web:** add `web_search` and `web_fetch` through a documented, configurable
  backend. Network use is explicit, capped, attributable, and cannot execute
  instructions found in results. Search sources/URLs are included in results.
  If no supported backend is configured, these tools are absent with a clear
  explanation; shelling out to `curl` is not the fallback.
- **Skills:** support Markdown-only, on-demand instructions. Skills cannot
  register tools, run code, change model/provider settings, or alter approvals.
  Reuse and resolve the security/configuration decisions in
  [`custom-commands`](../custom-commands/spec.md) before implementation.
- **Delegation:** add a bounded `task` tool and a built-in read-only `explore`
  agent. The child has a separate context, receives only repository-read and
  explicitly enabled web tools, returns a concise result to the parent, and
  cannot spawn another child. Start with one child at a time; parallel
  read-only work can follow only after cancellation and result ordering are
  proven.
- **MCP:** retain the existing stdio adapter and session-scoped TOFU behavior;
  make tool provenance visible and reconcile its spec wording.

### Alpha UX

- Add `/tools` (or equivalent) to inspect the active tool catalog, source,
  concise description, and permission class. Clearly distinguish unavailable
  optional tools from configured tools.
- Tool activity and results stay in the transcript; activity indicators do
  not replace the tool name/result. Keep approval previews focused on the
  operation and decision the user must make.
- Add `/agents` for listing the built-in alpha agent and its current
  capabilities; do not imply users can yet author agents.

## Beta scope — user-configurable agents and code intelligence

Beta builds on an alpha that has passed its exit gate. It broadens capability
without turning agent definitions into executable plugins.

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
  concurrent writers share the live checkout. Until isolation exists, custom
  agents remain read-only or use normal parent-owned approval in a strictly
  serialized flow.
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

## Out of scope through beta

- Importing Claude Code, OpenCode, OMP, or Pi implementations as dependencies;
  integrate their useful concepts, not their private runtime.
- Arbitrary user-authored executable plugins, hooks, or scripts. MCP remains
  the existing external tool integration mechanism.
- Agent teams, peer messaging, recursive swarms, or unbounded background
  agents.
- Automatic approval, hidden shell execution, or treating a cwd restriction as
  a sandbox.
- Required LSP/AST installations, a particular hosted search provider, or
  silently sending project content to a new provider.

## Release gates

### Alpha exit gate

- The registry and policy path cover built-ins, MCP, web, and task dispatch;
  no mutating or network tool has an alternate path.
- All current tool behaviors and approval semantics have regression tests;
  question, plan, skill, web, and read-only task flows have automated tests.
- Cancellation, refusal, malformed schemas, unavailable backends, output caps,
  stale edits, and session resume have explicit tests.
- A human walkthrough covers inspect → plan/question → edit/review → verify,
  shell approval, web source display, MCP first-use trust, child result/cancel,
  narrow terminal layout, and terminal restoration.
- At least one supported hosted provider has a live structured-tool probe for
  the shipped core. README/provider claims remain limited to providers actually
  probed; unverified tool behavior is labelled honestly.

### Beta exit gate

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

## Follow-on specs required before implementation

Keep this roadmap as the dependency and release map. Before coding, produce
buildable specs for:

1. tool registry/schema/policy and `/tools`;
2. web backend, network policy, output and source handling;
3. `ask_user`, plan/todo, and skills invocation/UX;
4. task/subagent lifecycle, context, cancellation, budgets, and TUI;
5. custom agent file format, locations, precedence, and trust;
6. worktree-isolated write agents;
7. AST/LSP adapter contract; and
8. opt-in memory, retention, deletion, and privacy.

Existing drafts should be sharpened rather than copied. Resolve the MCP
per-call-vs-session-trust wording and the plan-mode MCP decision before their
acceptance criteria are used as gates.
