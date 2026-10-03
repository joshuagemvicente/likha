# Callable tools and delegation — focused landscape

**Date:** 2026-10-03 · **Status:** focused research note for Likha’s tool UX.
**Updated comparison:** [cli-workflows.md](cli-workflows.md) covers all five
requested CLIs including Codex, current permission/delegation caveats, and the
approved adoption map. Its snapshot corrections supersede earlier default
assumptions in this note; none of these comparisons is a live behavior probe.
**See also:** [feature-comparison.md](feature-comparison.md) — product-level Likha vs OpenCode V2 vs OMP feature surface.
**Method:** first-party documentation and pinned upstream source only. Products
change quickly; version and source caveats are called out below. For prompt,
context, and broader harness comparisons, see
[agent-harness/research.md](../agent-harness/research.md).

## Scope and vocabulary

This note separates four things that are easy to conflate:

- **Callable tool:** a capability declared to the model and dispatched by the
  agent loop (e.g. `read`, `bash`, `task`).
- **Command/UI action:** a user-invoked slash command, keybinding, or menu item.
  It may submit a prompt or change session state; it is not necessarily a model
  tool.
- **Integration/extension:** configured MCP servers, plugins, extensions, or
  language services that can add model-callable tools or other behavior.
- **Agent definition vs. running child:** a role/configuration is not itself a
  running subagent. A **team** additionally has peer messaging or shared
  coordination rather than only returning a child result to its parent.

### Snapshot caveat

**OpenCode has two documentation surfaces at this snapshot.** The normal
`/docs` pages are the V1 configuration/API (e.g. `bash`, `task`, `permission`);
the site announces V2 and `opencode.ai/v2/docs` describes a new surface (e.g.
`shell`, `subagent`, `permissions`). This note treats V2 as the current
documented surface and mentions V1 only where the change matters. The earlier
[harness comparison](../agent-harness/research.md) describes a V1-era source
snapshot, not the V2 tool API. Do not mix their names or default policies.

**Oh My Pi is `can1357/oh-my-pi` (`omp`), not Pi’s upstream coding agent.**
**Pi** here means the `pi` coding agent in `earendil-works/pi` (formerly
`badlogic/pi-mono`; that URL now redirects). OMP has its own `@oh-my-pi` package
and runtime, even though the names and history are related.

## Callable tool catalogs

Inventories below are grouped for comparison, not a claim that every named
capability is active in every configuration. “Built-in” means shipped by the
project; “optional” means platform-, model-, setting-, extension-, host-, or
user-configuration-dependent.

| Product | Shipped/model-callable surface | Optional or configured additions; not the same thing as tools |
| --- | --- | --- |
| **Claude Code** | File/repo: `Read`, `Edit`, `Write`, `NotebookEdit`, `Glob`, `Grep`; execution: `Bash`, `PowerShell`; web: `WebFetch`, `WebSearch`; planning/worktree: `EnterPlanMode`, `ExitPlanMode`, `EnterWorktree`, `ExitWorktree`; delegation/questions: `Agent`, `Workflow`, `AskUserQuestion`, `SendMessage`; task APIs: `TaskCreate`, `TaskGet`, `TaskList`, `TaskUpdate`, `TaskOutput` (deprecated), `TaskStop`, plus `TodoWrite` (disabled by default); MCP resources: `ListMcpResourcesTool`, `ReadMcpResourceTool` (when available); other tools include `Skill`, `LSP`, `Monitor`, `CronCreate`/`CronDelete`/`CronList`, `ScheduleWakeup`, `Artifact`, `RemoteTrigger`, `PushNotification`, `SendUserFile`, `ShareOnboardingGuide`, `ReportFindings`, `SendFeedback`, `SubagentHandback`, and `EndConversation`. Availability gates vary by release, model, provider, and surface. On macOS/Linux/WSL, `Glob`/`Grep` are absent by default unless restored by flags, subagent configuration, or removal of Bash. `LSP` stays inactive until a code-intelligence plugin is installed. [1][2] | Connected MCP servers add tools; skills are prompt packs run through `Skill`, not a new tool namespace; hooks/plugins add integration behavior. `/commands` are user-facing commands, not callable tools. Many listed tools have explicit plan/model/surface gates; Artifact and remote tools also require eligible plans/authentication. [1][3][4][5] |
| **OpenCode V2** | Workspace: `read`, `glob`, `grep`, `edit`, `write`, `patch`; execution: `shell`; web: `webfetch`, `websearch`; interaction: `question`; extensions: `skill`; orchestration: `subagent`, `execute` (Code Mode). `patch` is exposed for supported GPT models; other models use `edit`/`write`. The desktop app additionally exposes a browser namespace through Code Mode. [6] | MCP servers and plugin-defined custom tools add tools; browser is app-hosted, not part of the fixed CLI catalog. Skills add instructions rather than executable tools. `/commands` expand prompt templates; a command template can run an explicit shell block outside the agent permission flow, so that path is distinct and deserves care. [6][7][8] |
| **Oh My Pi (`omp`)** | Core source declares `read`, `bash`, `edit`, `write`, `glob`, `grep`, `find`, `ast_grep`, `ast_edit`, `lsp`, `task`, `wait`, `todo`, `ask`, `eval`, `web_search`, `security_scan`, `checkpoint`, `rewind`, `context_notes`, `new_context`, `debug`, `github`, `ida`, `learn`, `manage_skill`, and memory tools (`memory_edit`, `retain`, `recall`, `reflect`). Hidden/internal tools include `think`, `yield`, and `goal`. These are a **registry**, not a guarantee that all are enabled: several are settings-, backend-, provider-, or host-gated. [9][10] | User/project MCP, extension/custom tools, and plugin roots can add tools. Some additional integrations need configuration or a local capability (e.g. GitHub, IDA, LSP, memory backend). The model-facing `task` tool dispatches agents; slash commands and the `/agents` hub are UI surfaces, not agents themselves. [10][11][12][29] |
| **Pi coding agent (`pi`)** | The default active tools are `read`, `bash`, `edit`, `write`. Built-in but not in that default set: `powershell` (Windows), `grep`, `find`, `ls`; users can select them with `--tools`/`defaultTools`. Built-in extension tools `codemode` and `tool_search` are off by default; configured MCP can activate them. [13][14][17] | Extensions/packages can register model-callable tools, slash commands, hooks, and providers. MCP is built-in/configurable (stdio or streamable HTTP); server tools may be direct, deferred, Code Mode-only, or hidden. Pi deliberately ships without a built-in subagent tool or plan mode. Slash commands and `!` shell entry are user interaction surfaces, not ordinary model tool calls. [13][15][16][17][30] |
| **Likha (current repo)** | Exactly five built-ins are declared to the model: `glob`, `read`, `grep`, `edit_file`, `run_command`. Repo reads are path-confined; edit is a full-file proposal shown as a diff; the exact shell command requires approval. [19][21][31] | User-configured MCP server tools are appended to the model tool list; they are not Likha-authored built-ins. `/mcp` reports server/tool status. Likha has no agent/subagent/team tool or plugin runtime in the current implementation; skills remain a placeholder. [20][22][28] |

### Commands, MCP, and other integrations

The same slash syntax hides different products’ mechanics. Claude custom
commands and skills, OpenCode commands, and Pi/OMP extension commands are
primarily user-invoked workflows; do not count them as model-callable tools
unless the product explicitly registers a tool for them. OpenCode V2 commands
submit prompt templates and can opt into a child session; shell blocks inside
templates are a separate execution path. Pi extensions explicitly distinguish
`registerCommand()` from `registerTool()`. MCP is different: it supplies
model-callable server tools, subject to each harness’s exposure and permission
policy. [5][7][8][15][16][17][29][30]

Likha already reflects this separation in its drafts: `/init` starts an ordinary
agent turn whose file proposal still uses the standard diff approval, and
custom markdown commands are prompt text only—not code, tools, or a permission
shortcut. [23][24]

## Who can spawn what?

| Product | Spawn/delegation model | Gate and boundary |
| --- | --- | --- |
| **Claude Code** | Main session invokes built-in/custom subagent roles with `Agent`; they get their own contexts and return a result. Subagents can themselves spawn subagents when `Agent` remains available and depth allows. `Workflow` orchestrates multiple background subagents. Experimental **agent teams** turn named Agent calls into teammates when `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`: a lead coordinates, teammates message one another and share tasks. Teammates cannot create nested teams; the main lead owns team creation. [3][4] | A normal subagent launch does not itself prompt; the child’s tool calls still use the applicable permission rules. Tool allowlists, per-agent permission mode, model, MCP servers, and optional worktree isolation can be set per agent. Team permission requests surface to the lead/user. Teams are disabled by default and are not available in non-interactive `-p` runs. [1][3][4] |
| **OpenCode V2** | A primary agent invokes configured `mode: subagent` roles with the `subagent` tool; built-ins include `general` and read-only `explore`, while primary `build` and `plan` are not children. Child context is fresh. Default nesting depth is one; built-in `general` cannot launch another child. There is no V2 team/shared-mailbox primitive in the documented core. Commands may opt into running in a background child session, but that remains command dispatch. [6][7] | Parent `subagent` permission controls which agent IDs can launch; child uses its own configured permissions. Global/agent permission rules are ordered, last match wins. No match resolves to ask, though shipped policy starts permissively with `* allow` plus targeted asks. [7][8] |
| **Oh My Pi** | Model-callable `task` launches a named role (default `task`). A parent’s `spawns` field can allow any (`*`/unset), allow a list, or disable spawning; defaults allow any. Nested spawning is possible up to `task.maxRecursionDepth` (default 2), and children at the limit lose `task`. Batched task calls can launch one child per item; with async enabled they run independently in the background. Eval-backed `agent()`/`workpool()` are other configured orchestration routes. The Agent Hub monitors/steers task agents; this is task/subagent orchestration, not Claude-style peer teams. [11][12] | Parent spawn policy, disabled-agent settings, depth, and plan-mode restrictions are checked before child startup. Plan-mode children are restricted to read/search/web-search tools and cannot spawn further. [11] |
| **Pi coding agent** | No built-in spawn capability, subagent definitions, or plan mode. A trusted extension/package can implement an agent tool or nested model calls; that is opt-in extension behavior, not a Pi default. [13][15] | No built-in spawn permission model because there is no built-in spawn tool. Extension code runs in-process with the Pi process’s OS permissions. [15][16] |
| **Likha** | None in the current tool definitions or turn loop. No parent/child agent capability or team runtime is implemented; subagents and other advanced surfaces are explicitly deferred in the harness draft. [19][23] | Do not imply that “agent roles” exist just because a future project instruction or slash command names a role. Any future spawn permission needs its own spec; it is not part of current edit/shell approval. [23] |

## Permission and approval differences that affect UX

| Product | Ordinary default and approval model | What approval does **not** mean |
| --- | --- | --- |
| **Claude Code** | In Manual/default mode, in-workspace reads are generally unprompted; file modifications and shell commands prompt, except a fixed read-only Bash command set. `deny` rules take precedence over `ask`, which takes precedence over `allow`; modes (`acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`) also alter behavior, and `auto` uses a classifier. File approvals and saved command/domain rules have different persistence scopes. [2] | Command allow-rules are not a complete security boundary. Claude separately documents OS-level sandboxing and its limits; approval mode and sandbox are separate controls. [2][18] |
| **OpenCode V2** | Permission rules use `allow`/`ask`/`deny`, ordered last-match-wins; no matching rule means ask. Shipped defaults allow most actions but ask on external-directory access and `.env` reads. An approval can be once, saved always (tool-proposed pattern, project-scoped), or rejected; rejection also rejects other pending requests in that session. Child agents have their own configured rules. [8] | Shell runs with host filesystem, process, and network authority. The shell scanner’s directory inference is best-effort, not a sandbox. [8] |
| **Oh My Pi** | Tools declare `read`/`write`/`exec`; unknown tools default to `exec`, and MCP tools declare `write` regardless of MCP annotations. Modes are `always-ask`, `write`, and `yolo` (the documented default); per-tool user policy can allow/deny/prompt. Tool-level deny/prompt and critical safety overrides can still apply. Headless subagents run with yolo for ordinary tier prompts; parent `task` approval is the authorization boundary. [12] | Approval policy is not containment. Approved shell/extension work can retain ambient access; the approval docs explicitly make that distinction. [12] |
| **Pi coding agent** | No per-tool approval prompt by default. The normal security boundary is the Pi process’s operating-system identity and any external sandbox/container. Extensions can add confirmation policies, but those are custom extension behavior. Project trust gates loading project configuration/extensions/resources; it does **not** constrain tool access after launch. [15][16] | Seeing a tool call in the transcript, trusting a project, or reviewing a diff is not a security boundary. [15] |
| **Likha** | Read/search: no approval. `edit_file`: user approves the reviewed diff; stale proposals are refused. `run_command`: explicit approval for the exact command and working directory; the dialog warns that it can access outside the repo and use the network. MCP: currently a **server-level, session-scoped trust-on-first-use** gate, not an approval for every individual call. [19][20][21][22] | Likha’s shell runs from the repository as cwd but is not sandboxed. An approval grants the requested action; it does not limit that action’s OS/network reach. [21] |

## Likha-focused findings and recommendations

These are UX recommendations, not new requirements. They preserve the existing
draft scope and approval semantics.

1. **Fix tool choice through the drafted harness prompt before changing policy.**
   `agent-loop/spec.md` identifies the concrete friction: without a system
   message, models use `run_command` for repo searches that should use
   approval-free `glob`/`grep`/`read`. Its resolution is steering, not weakening
   the approval gate. Keep the distinction explicit: search/read by default;
   shell for actions that need a shell (tests/build/git, etc.). [23]
2. **Use the existing transcript contract to make capability use legible.**
   Tool calls/results already have `Tool:` transcript entries; keep the
   working-indicator spec’s generic activity row and phase status separate
   from those entries. The tool-rendering spec already calls for muted tool
   rows and explicitly excludes collapse/expand UI, so prefer its planned
   rendering rather than adding a second status surface or changing transcript
   shape. [19][25][32][33][34]
3. **Keep approval previews concrete and preserve the current gate.** For edits,
    lead with target path and readable diff; for shell, show exact command,
    cwd, and the outside-repo/network warning; for MCP, identify server and
    tool. The labelled Approve/Decline decision-bar spec already improves
    controls while retaining read-to-end and per-proposal approval for the
    existing edit/shell flow. Do not add persistent or blanket grants as a UX
    shortcut: that permission-policy change is outside the decision-bar spec,
    and MCP trust is separately scoped to a server and session. [21][22][26]
4. **Show configured MCP as a separate capability source.** Keep `/mcp` as the
   management/status surface, label server-provided tools as such in tool
   activity, and explain that approving the first call trusts that server for
   this session. This avoids presenting configured integrations as Likha core
   tools, and makes the actual trust scope visible. [20][22]
5. **Keep user commands distinct from capabilities.** `/init` and future
   markdown custom commands should remain ordinary prompts; the custom-command
   draft explicitly forbids code execution, tool registration, and approval
   bypasses. `/plan` is different: its draft identifies the current MCP
   trust-on-first-use rule as a reason that “approved” does not imply
   read-only. Its recommended safe behavior is to block all MCP calls in plan
   mode until that open decision is resolved. [24][27][28]
6. **Distinguish planned children from current capabilities.** The current
   runtime has no delegation. The approved [explore spec](../explore-agents/spec.md)
   defines depth-2 read-only nesting, awaited results, shared budgets, and
   cancellation. Teams/writers/custom roles remain deferred; do not describe
   the planned runtime as shipped or inherit another product's permission defaults.

### Likha spec consistency note

The current MCP implementation and
[`mcp-support/spec.md`](../mcp-support/spec.md) use server-level trust after the
first approval in the running manager's lifetime, resetting on app relaunch.
The phase documentation reconciles the former contradictory per-call wording.
Planned plan mode blocks every MCP call even when trusted; naming/registry
improvements do not change the trust grant. [20][22][27]

## Sources

### Claude Code — official docs

[1] [Tools reference](https://code.claude.com/docs/en/tools-reference) ·
[3] [Subagents](https://code.claude.com/docs/en/sub-agents) ·
[4] [Agent teams](https://code.claude.com/docs/en/agent-teams) ·
[5] [Commands](https://code.claude.com/docs/en/commands)

[2] [Permissions](https://code.claude.com/docs/en/permissions) ·
[18] [Sandboxing](https://code.claude.com/docs/en/sandboxing)

### OpenCode — official docs

[6] [V2 Tools](https://opencode.ai/v2/docs/tools) ·
[7] [V2 Agents](https://opencode.ai/v2/docs/agents) ·
[8] [V2 Permissions](https://opencode.ai/v2/docs/permissions)

V1 comparison surfaces (legacy API names): [V1 Tools](https://opencode.ai/docs/tools/),
[V1 Agents](https://opencode.ai/docs/agents/), and
[V1 Permissions](https://opencode.ai/docs/permissions/). These pages are still
published; their names and policies must not be assumed to describe V2.

### Oh My Pi — official repository/docs

Repository snapshot: [`can1357/oh-my-pi` @ `9348320cc4a30a7195d36a1f05a6c11bcb701a17`](https://github.com/can1357/oh-my-pi/tree/9348320cc4a30a7195d36a1f05a6c11bcb701a17), fetched 2026-10-03.

[9] [`builtin-names.ts`](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/packages/coding-agent/src/tools/builtin-names.ts) ·
[10] [`tools/index.ts`](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/packages/coding-agent/src/tools/index.ts)

[11] [Task agent discovery and selection](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/task-agent-discovery.md) ·
[12] [Tool approval mode](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/approval-mode.md) ·
[29] [MCP configuration](https://github.com/can1357/oh-my-pi/blob/9348320cc4a30a7195d36a1f05a6c11bcb701a17/docs/mcp-config.md)

### Pi coding agent — official repository/docs

Repository snapshot: [`earendil-works/pi` @ `a276dabe57911253350bffb93cb7d7aff6a73261`](https://github.com/earendil-works/pi/tree/a276dabe57911253350bffb93cb7d7aff6a73261), fetched 2026-10-03. The old [`badlogic/pi-mono`](https://github.com/badlogic/pi-mono) URL redirects to this upstream.

[13] [Coding-agent README](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/README.md) ·
[14] [CLI and tool reference](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/docs/cli.md) ·
[15] [Security and project trust](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/docs/security.md) ·
[16] [Extensions](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/docs/extensions.md) ·
[17] [MCP servers](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/docs/mcp.md)

[30] [Interactive usage](https://github.com/earendil-works/pi/blob/a276dabe57911253350bffb93cb7d7aff6a73261/packages/coding-agent/docs/usage.md)

### Likha — local source/specs

[19] [`internal/agent/agent.go`](../../internal/agent/agent.go) ·
[20] [`internal/mcp/manager.go`](../../internal/mcp/manager.go) ·
[21] [`internal/actions/command.go`](../../internal/actions/command.go) and
[`edit.go`](../../internal/actions/edit.go) ·
[22] [`specs/mcp-support/spec.md`](../mcp-support/spec.md) ·
[23] [`specs/agent-loop/spec.md`](../agent-loop/spec.md) and
[`specs/agent-harness/spec.md`](../agent-harness/spec.md) ·
[24] [`specs/repo-init/spec.md`](../repo-init/spec.md) and
[`specs/custom-commands/spec.md`](../custom-commands/spec.md) ·
[25] [`internal/tui/tui.go`](../../internal/tui/tui.go) ·
[26] [`specs/permission-ui/spec.md`](../permission-ui/spec.md) ·
[27] [`specs/plan-mode/spec.md`](../plan-mode/spec.md) ·
[28] [`specs/slash-commands/spec.md`](../slash-commands/spec.md) ·
[31] [`internal/repository/repository.go`](../../internal/repository/repository.go) ·
[32] [`internal/tui/status_line.go`](../../internal/tui/status_line.go) ·
[33] [`specs/tool-rendering-terminal-keys/spec.md`](../tool-rendering-terminal-keys/spec.md) ·
[34] [`specs/working-indicator/spec.md`](../working-indicator/spec.md)

## Limits

- Claude Code is closed-source; catalog and behavior claims above are based on
  official docs, which include release/platform/model gates.
- OpenCode V2 claims are based on the live docs retrieved 2026-10-03, not a
  pinned V2 source revision. V1 docs remain accessible and use a different API;
  this note does not claim V2 and the earlier V1 source snapshot are identical
  implementations.
- OMP and Pi source links are pinned snapshots. Their large, fast-moving
  catalogs can change after those revisions; OMP’s many settings make “built
  in” different from “currently callable.”
- No live interactive probes were run against hosted integrations. Optional
  MCP servers, plugins, browser hosts, and local language services were not
  installed or exercised.
