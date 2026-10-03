# Harness layers of AI agent terminals — comparative research

**Date:** 2026-10-03 · **Status:** research note feeding [spec.md](spec.md) (agent-harness) and related drafts.

**Decision override:** this note records alternatives, not the approved scope.
The questionnaire selected root-only AGENTS.md, a 32-KiB reject-on-overflow cap,
and no imports/compatibility fallbacks. Use the [three-phase index](../tooling-platform/README.md)
and [five-CLI comparison](../tooling-landscape/cli-workflows.md) for the current
adoption map; historical suggestions below do not authorize wider loading.
**Method:** primary sources only — official docs and source repositories. Terminal source claims cite repository paths at pinned commits (clones fetched 2026-10-03); doc claims cite official documentation URLs. Where evidence is docs-only (closed-source Claude Code) or absent, the text says so. Analysis and ratings are marked as such. No live probe of hosted/cloud surfaces was run.

Terminals surveyed: **Claude Code**, **OpenAI Codex**, **OpenCode**, **OMP**, **Gemini CLI**, **Aider** (contrast case), plus **Likha** (current state and four-layer confirmation).

---

## 1. Model: four layers, two harness sub-layers

The requested four layers are defined here as they are used throughout:

1. **Foundation model** — the inference backend and how it is reached: bundled/hosted vs BYO key, local models, transport.
2. **Agent harness** — the context-to-action loop: system/instruction prompt assembly, turn loop, tool dispatch, approvals, session/context management.
3. **Repository/workspace harness** — the repository-aware tooling: file read/search/edit, ignore rules, indexing/repo maps, git integration, worktree isolation.
4. **Runtime harness** — the execution environment: process/shell execution, sandboxing, terminal UI, extension/plugin execution, headless/SDK surfaces.

Within the agent harness, this report distinguishes two sub-layers the task asks about:

- **Prompt-layer harness** — the compiled or generated system prompt, instruction files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `CONVENTIONS.md`), precedence rules, per-model prompt variants, dynamic context injection (date, cwd, git), and prompt-cache layout.
- **Runtime harness** — the loop mechanics, tool gating, sandboxing, subagents, hooks, MCP, compaction, and headless/SDK runtimes.

The recurring design tension across terminals: **static prompt prefix stability** (for provider prompt caching) versus **dynamic context freshness**; and **approval speed** versus **containment strength**.

---

## 2. Terminal findings

### 2.1 Claude Code (Anthropic)

**Snapshot.** Closed source. Anthropic publishes docs, engineering posts, and a few open components (the `srt` sandbox runtime [8], Agent SDK packages, plugin “mods” [10]); the CLI source and the exact system prompt are not published — the docs state the system prompt “isn’t published” [2]. All prompt claims below are behavioral, from official docs.

**Prompt-layer harness**

- The system prompt is compiled-in; users can replace or append via `--system-prompt`/`--system-prompt-file` and `--append-system-prompt`/`--append-system-prompt-file`, or restructure behavior with **output styles** (Default, Proactive, Concise, Explanatory, Learning, or custom markdown; a custom style replaces the built-in software-engineering instructions unless `keep-coding-instructions: true`) [2][14]. The Agent SDK exposes three starting points: minimal (tool-calling only), the `claude_code` preset, or a custom string, plus `append` [14].
- **CLAUDE.md hierarchy** (broadest → most specific): managed policy (e.g. `/Library/Application Support/ClaudeCode/CLAUDE.md`, `/etc/claude-code/CLAUDE.md`) → user `~/.claude/CLAUDE.md` → project `./CLAUDE.md` or `./.claude/CLAUDE.md` → local `./CLAUDE.local.md` [1]. Files are **concatenated, ordered filesystem-root → cwd**; within a directory `CLAUDE.local.md` follows `CLAUDE.md`; the model resolves conflicts (docs warn it “may pick one arbitrarily”) [1]. Subdirectory files load on demand when Claude reads files there [1].
- **AGENTS.md** is supported as an alternative: by default it loads only when no CLAUDE.md/CLAUDE.local.md exists in cwd or above; `/config` offers `claude-md-and-agents-md`, `claude-md`, and `managed-only` modes; AGENTS.md loading ships as a built-in mod [1][10].
- **Rules**: `.claude/rules/**.md` at enterprise/user/project level; rules with `paths:` frontmatter load only when matching files are read; user rules load before project rules [1].
- **@imports**: `@path` expands at launch, relative to the importing file, max 4 hops, skipped inside code spans/fences; a project file importing outside the working directory triggers a one-time approval dialog [1].
- **Auto memory**: Claude-authored per-project notes under `~/.claude/projects/<project>/memory/`; `MEMORY.md` first 200 lines / 25 KB load per session; toggleable [1].
- **Dynamic context is conversation content, not system text**: an environment block (cwd, platform, shell, OS, git-repo flag) and git block (branch, status, recent commits) load at startup; running context arrives as `<system-reminder>` messages; CLAUDE.md is “delivered as a user message after the system prompt” [11].
- **Prompt caching**: layered System → Project context → Conversation; changing model/effort invalidates everything, tool-set changes invalidate the system layer; CLAUDE.md edits apply at `/clear`/`/compact`/restart; `--exclude-dynamic-system-prompt-sections` moves per-user text into the first user message for cross-user cache hits [11][14].

**Runtime harness**

- Loop: context → action → verification, repeating until a no-tool-call message; turns are budgeted by `max_turns` and `max_budget_usd` [9].
- **Parallel tool execution**: multiple calls in one turn run concurrently when read-only, sequentially when state-modifying; SDK tools default sequential unless `readOnlyHint` [9].
- Tools: Read/Edit/Write/NotebookEdit; Bash/PowerShell; Glob/Grep (or embedded `bfs`/`ugrep` on Unix defaults); WebFetch/WebSearch; Agent, Skill, AskUserQuestion, Todo/Workflow/ToolSearch, LSP, MCP tools, cron/messaging [7].
- **Permissions**: `ToolName(specifier)` allow/ask/deny rules (e.g. `Bash(npm run *)`, `Read(~/secrets/**)`); modes `default`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`; deny rules apply in every mode; `auto` routes prompts to a separate classifier model [3].
- **Sandboxing**: OS-level (not containers) — macOS Seatbelt `sandbox-exec`, Linux/WSL2 bubblewrap + socat + optional seccomp, Windows WFP; filesystem write allowlists (default cwd/temp/added dirs), read deny-then-allow, network allow-only via host proxies; two modes (auto-allow vs regular permissions) and a strict mode disabling the unsandboxed retry; built-in file tools, MCP/LSP servers, and hooks run **outside** the sandbox [8][16].
- **Subagents**: markdown + frontmatter in `.claude/agents/`, `~/.claude/agents/`, plugins, or `--agents`; each has its own context window, system prompt, tool allow/deny, model, `permissionMode`, `maxTurns`, skills, MCP servers, hooks, memory, optional `isolation: worktree`; the main system prompt is *not* inherited [5].
- **Hooks**: ~30 lifecycle events (session/turn/tool-call/compaction/subagent/…) with matchers, five handler types (`command`, `http`, `mcp_tool`, `prompt`, `agent`); command hooks exchange JSON via stdin/stdout with allow/deny/ask decisions [4].
- **Mods**: JS/TS handlers running **in-process**, able to draw UI, rewrite tool calls/prompts, and run `/commands` without a model turn [10]. Dynamic workflows let the model author JS orchestration scripts (`agent()`, `parallel()`, `pipeline()`) [13].
- Compaction: clears old tool outputs first, then summarizes; `PreCompact`/`PostCompact` hooks; re-injects project-root CLAUDE.md after compaction [9]. Sessions persist as JSONL under `~/.claude/projects/` with `--resume`/`--fork-session` [9][12]. Headless `claude -p` and the Agent SDK expose the same loop programmatically [12]. LSP tool for definitions/references/type errors [7].

**Four-layer mapping.** Foundation: hosted Anthropic models only (subscription OAuth, Console key, Bedrock/Vertex/Foundry, gateways) [15]. Agent harness: compiled prompt + conversation-carried instructions + hook/permission guardrails [1][4][9]. Repository layer: Read/Edit/Write/Glob/Grep, path-scoped permissions, worktree isolation, checkpoints/rewind [1][7]. Runtime layer: per-command Bash processes, OS sandbox `srt`, TUI, in-process mods, headless/SDK [8][10][12].

### 2.2 OpenAI Codex

**Snapshot.** Open-source Rust CLI (`github.com/openai/codex` @ `9d2b6030` [17]) plus the Codex cloud/web surface; docs at `developers.openai.com/codex` [25].

**Prompt-layer harness**

- Base instructions live in `codex-rs/protocol/src/prompts/base_instructions/default.md` [18]: coding-agent identity, personality, the **AGENTS.md contract**, planning guidance, task-execution rules, validation policy, and detailed final-answer formatting rules.
- **Per-model prompt templates**: `codex-rs/prompts/src/model_instructions.rs` renders instructions from the model’s captured metadata (`instructions_template`); a missing template warns and yields empty instructions [19]. Permission-mode-specific templates exist under `codex-rs/prompts/templates/permissions/approval_policy/` (`never.md`, `on_request.md`, `on_request_rule_request_permission.md`, `unless_trusted.md`) [19].
- **AGENTS.md discovery** (`codex-rs/core/src/agents_md.rs`) [20]: walk up from cwd to a project root (default marker `.git`, configurable `project_root_markers`; no marker → cwd only; empty list disables parent traversal); concatenate every `AGENTS.md` from **project root down to cwd**; never walk past the project root; `AGENTS.override.md` is the preferred local override; total size capped by `project_doc_max_bytes` (default **32 KiB**) [20][23]. Untrusted projects skip project instructions [20].
- The base prompt also defines scope semantics: an AGENTS.md’s scope is its directory tree; deeper files win conflicts; direct system/developer/user instructions outrank AGENTS.md; root-to-cwd files are preloaded and need not be re-read [18].
- Approval-mode templates steer escalation behavior, including segment-splitting of shell commands for rule evaluation and `prefix_rule` guidance that forbids broad prefixes like `python3` [19].

**Runtime harness**

- **Approval policy** (`AskForApproval` in `codex-rs/protocol/src/protocol.rs`) [21]: `untrusted` (ask unless an exec-policy rule allows), `on-request` (default; model decides), `granular` (per-category booleans: sandbox approvals, rules, skill scripts, request_permissions, MCP elicitations), `never` (never ask; failures return to the model).
- **Sandbox modes** (`SandboxMode` in `config_types.rs`) [21]: `read-only`, `workspace-write`, `danger-full-access`, with `NetworkAccess` `restricted`/`enabled`; platform runtimes (Seatbelt on macOS, Landlock/seccomp on Linux) selected accordingly; docs live under `developers.openai.com/codex` security pages [21][25].
- **Escalation**: the model requests `sandbox_permissions: "require_escalated"` plus a justification and optional `prefix_rule`; approved commands run outside the sandbox, and command segments are independently evaluated against rules [19].
- Tool surface is broad (`codex-rs/core/src/tools/spec_plan.rs`, `handlers/`): shell/exec command handlers, `apply_patch` (the prompt insists on this exact tool), `update_plan`, `view_image`, web search, MCP tools/resources, `request_user_input`, `sleep`, tool search, code-mode execution, extension tools, and a **multi-agent family** (spawn/wait/send/close/resume agents, v1 and v2 namespaces) [22].
- Compaction: `codex-rs/core/src/compact.rs` implements automatic compaction with summaries, pre/post-compact hooks, and distinct initial-context injection rules for pre-turn vs mid-turn compaction [24].
- Config: `config.toml` covers `approval_policy`, `sandbox_mode`, `model_providers`, `mcp_servers`, `project_doc_max_bytes`, `project_doc_fallback_filenames`, and more [23][25]. Headless `codex exec`, SDKs, GitHub integration, and cloud tasks are documented [25].

**Four-layer mapping.** Foundation: OpenAI-hosted models (ChatGPT auth or API key), no local inference evident in the CLI code path surveyed [25]. Agent harness: base + per-model prompts, AGENTS.md, approval-policy templates, tool loop [18][19][22]. Repository layer: `apply_patch`, read/search tools, AGENTS.md scoping, exec policy over repo commands [18][19][22]. Runtime layer: OS sandboxes + escalations, multi-agent runtime, hooks (pre/post compact), MCP, headless `exec` [21][22][24][25].

### 2.3 OpenCode

**Snapshot.** Open source (TypeScript); canonical repo is `anomalyco/opencode` (`sst/opencode` redirects), researched at commit `c42ae0d5` on `dev` [27]. Docs at `opencode.ai/docs` [35].

**Prompt-layer harness**

- **Per-model-family system prompts**: `packages/opencode/src/session/system.ts` selects from `packages/opencode/src/session/prompt/*.txt` by model id — `anthropic.txt`, `gpt.txt`, `beast.txt` (gpt-4/o1/o3), `gemini.txt`, `codex.txt`, `kimi.txt`, `trinity.txt`, `meta.txt`, `gpt-astra.txt`, plus `default.txt` [28]. Files range from 26 to 155 lines; `default.txt` carries identity, tone rules, tool-use norms, and a strict “≤4 lines unless asked” output discipline [28].
- **Environment block** appended to the prompt: model id, provider id, working directory, workspace root, is-git-repo, platform, today’s date, plus any configured project references [28].
- **Skills and MCP instructions** are appended as separate generated sections (skills listed verbosely in the prompt; MCP server instructions rendered in `<mcp_instructions>`) [28].
- **Instruction files** (`packages/opencode/src/session/instruction.ts`) [29]: global `~/.config/opencode/AGENTS.md` (plus `~/.claude/CLAUDE.md` when the Claude prompt flag is enabled) — first existing wins; project-level `AGENTS.md`, then `CLAUDE.md`, then deprecated `CONTEXT.md`, found via `findUp` bounded by the worktree — **first match wins, ancestors are not stacked**; `config.instructions` adds glob/URL sources (URLs fetched with a 5s timeout). Loaded instruction files are tracked per assistant message and can be cleared [29].
- **Agents** (`packages/opencode/src/agent/agent.ts`) [30]: schema includes `name`, `description`, `mode` (`subagent` | `primary` | `all`), `prompt` (custom system prompt), `model`, `temperature/topP`, `permission` ruleset, `steps`; agents are defined natively and via frontmatter in config; an agent generator (LLM-authored agent definitions) is also present [30].
- Default permission rules exemplify intent: `"*": "allow"`, `doom_loop: "ask"`, `external_directory: "ask"` except whitelisted skill/temp/reference dirs, `.env` reads `"ask"` [30].

**Runtime harness**

- Loop in `packages/opencode/src/session/prompt.ts`: step counter with a `while` loop and `maxSteps`; on each step it assembles skills/env/instructions/MCP instructions/model messages concurrently [32].
- **Permissions** (`packages/opencode/src/permission/index.ts`) [31]: wildcard rules evaluated last-match-wins across rulesets (`allow`/`ask`/`deny`); `deny` short-circuits; otherwise asks are deferred and published as permission events until the UI replies (accept/reject); approved rules accumulate in state [31].
- Tools (`packages/opencode/src/tool/`): apply_patch, edit, read, write, glob, grep, shell/bash, webfetch, websearch, task (subagents), todo, skill, plan enter/exit, LSP, MCP websearch, truncate helpers [33].
- **Subagents** (`tool/task.ts`) [33]: `subagent_type`, optional `task_id` (resume), stack-depth check (`subagent_depth`, default **1**), permission ask for the spawn (`task` permission, pattern = agent type), foreground default and `background: true` behind an experimental flag with explicit “do not poll” instructions [33].
- **Plugins/hooks** (`packages/plugin/src/index.ts`) [34]: `chat.message`, `chat.params`, `permission.ask`, `tool.execute.before`, `tool.execute.after`, `experimental.chat.messages.transform` — i.e. a typed interception surface over the same events [34].
- **Compaction** (`session/compaction.ts`): overflow detection via `isOverflow`, automatic compaction (`auto: true`), pruning of old tool outputs, summary messages with an `auto`/`overflow` context, and autocontinue behavior [32].
- **LSP** integration is first-class (`src/lsp/`, LSP tool) [33]; TUI is a client of an OpenCode server/SDK (package split) [27][35]. MCP, permissions, and plugin docs are on `opencode.ai/docs` [35].

**Four-layer mapping.** Foundation: BYO provider keys across many providers (model selection in prompts by id) [27][35]. Agent harness: per-model prompt files + instruction files + agents + permission/plugin interception [28][29][30][34]. Repository layer: read/edit/write/glob/grep, LSP diagnostics, apply_patch, project references [33]. Runtime layer: permission-gated shell/TS runtime, subagent sessions, plugin hooks, server/TUI split [31][33][34].

### 2.4 OMP

**Snapshot.** The harness executing this research. Evidence: its own shipped documentation (`omp://…`), which is primary for its behavior [36]–[44].

**Prompt-layer harness**

- **Ordered block array** (`packages/coding-agent/src/system-prompt.ts`, `prompts/system/*`) [36]: block 0 is the default/custom/template instruction block; all working-directory-derived content follows in one trailing `<project-context>` footer — workstation data, `<repo-rules>` context files with absolute paths, `<dir-context>` pointers, `<workspace-tree>`, workspace roots, nested-repo context, completion requirements, append text. The provider cache breakpoint sits on the last block before `<project-context>`, so the static prefix is shared across sessions/directories [36].
- **Override chain** (highest first) [36]: `--system-prompt-template <path>` (Handlebars) / `--system-prompt` (plain, path-or-literal) → discovered `SYSTEM_TEMPLATE.md` → discovered `SYSTEM.md` (project before user; literal beats template at the same scope) ; append via `--append-system-prompt` / `APPEND_SYSTEM.md`. `PERSONALITY.md` replaces the personality block; `TITLE_SYSTEM.md` overrides session-title generation; the programmatic `systemPrompt` option is the only full replacement [36].
- **Context files** [37]: multi-provider discovery (native `.omp/AGENTS.md` first, then Claude/Codex/Gemini/OpenCode/GitHub/agents-md/claude-md conventions), one user file + one project file per directory depth, higher-priority provider shadowing at equal depth, byte-identical copies collapsed, ancestors kept and ordered **farthest first, cwd last**; injection as a single `<repo-rules>` block with absolute paths. Standalone `AGENTS.md`/`CLAUDE.md` walks to the repo root and enclosing workspaces [37].
- **Sticky `RULES.md`** is a special always-apply rule whose full body rides every request, surviving long conversations; regular rules support globs, `alwaysApply`, per-agent `agents:` scoping, conditions (regex/ast-grep), and a judge-model `question` trigger (TTSR) [37][38].
- **@imports** in context files expand inline (relative to the importing file, `~/`, recursion ≤5 hops, cycles skipped, code spans untouched) [37].
- **Skills** are discovered from multiple ecosystems, listed by name+description in the prompt, and read on demand via `skill://` [44]. System prompt templates receive live data (`toolInventory`, `skills`, `rules`, `xdevDocs`) and are re-rendered on rebuilds without re-reading files [36].

**Runtime harness**

- **Approval tiers and modes** [39]: tools declare `read`/`write`/`exec` tiers (undeclared = `exec`); modes `always-ask` (auto-approve read), `write` (auto-approve read+write), `yolo` (default, everything); per-tool user overrides `allow|deny|prompt`; tool-declared `deny` is absolute; safety overrides force prompts for critical patterns (e.g. `rm -rf /`, fork bombs); subagents run headless with `yolo` because the parent `task` approval is the boundary [39].
- **Bash runtime** [40]: command normalization, block-command interception toward dedicated tools, direnv/devenv preflight, non-interactive hardening env, PTY path (xterm-headless overlay) vs non-PTY, shell session reuse per session, async/background jobs, output sink with tail window (50 KB), optional head window and middle elision (artifact spill to `artifact://`), per-line column cap, live updates [40].
- **Subagents** [41]: markdown agent definitions (`name`, `description`, `systemPrompt`, `tools`, `spawns`, `model`, `thinkingLevel`, `output`, `blocking`, `autoloadSkills`, `prewalk`, `advisor`); discovery precedence project `.omp/agents` → user → extension roots → Claude marketplace → bundled; spawn policies (`*`, list, none), depth cap default **2**, blocked self-recursion guard, plan mode restricting children to read-only tools [41].
- **Hooks** [42]: factory modules (`pi.on(event, handler)`) over session, agent/context, and tool events; `tool_call` can block/rewrite input/inject trusted context, `tool_result` can override content; fail-closed on handler errors; context transforms chain. Extensions load `.ts/.js` factories from `.omp/extensions`, hooks, plugins, or explicit paths; extensions are **not sandboxed** and share one process runtime [42][44].
- **Compaction** [43]: six trigger paths (manual, overflow recovery, incomplete-output recovery, post-turn threshold, mid-turn threshold, idle); method order `remote → snapcompact → handoff → shake → soft` with provider-native server compaction for OpenAI Responses/Anthropic beta; tool-output pruning, useless-result elision, superseded-read pruning; auto-continue after compaction; subagents pin mid-turn checks because an assignment is one turn [43].
- Additional runtime surfaces: MCP servers/tools with naming and trust, `xd://` tool devices, hooks-based UI, internal URL schemes, RPC/ACP/print modes [42][44].

**Four-layer mapping.** Foundation: multi-provider BYO keys plus local models [36][44]. Agent harness: layered prompt blocks + discovery of every major instruction convention + rules/TTSR + skills [36][37][38][44]. Repository layer: tool suite (`read`, `edit`, `write`, `grep`, `glob`, bash, LSP, ast-edit, etc.) with artifacts and shells [40][44]. Runtime layer: sophisticated shell runtime, approval tiers, subagents, in-process extensions/hooks, multi-strategy compaction [39][40][41][42][43].

### 2.5 Gemini CLI

**Snapshot.** Open source (`google-gemini/gemini-cli` @ `fb972b2f`, Apache-2.0) [45]; docs in `docs/`.

**Prompt-layer harness**

- `PromptProvider.getCoreSystemPrompt` composes the prompt from section snippets (`snippets.ts` vs `snippets.legacy.ts` selected by model generation): preamble, core mandates, sub-agent list, agent skills, task tracker, hook context, primary workflows, plus tool-name and memory sections; approval mode and interactivity alter wording [46].
- **Full replacement**: `GEMINI_SYSTEM_MD=true` reads `.gemini/system.md`, or a path selects a custom file; it replaces the built-in prompt entirely, with substitution variables `${AgentSkills}`, `${SubAgents}`, `${AvailableTools}`, and `${<tool>_ToolName}`; a missing file is a hard error; the UI shows a custom-prompt indicator [47].
- **GEMINI.md hierarchy** [48]: global `~/.gemini/GEMINI.md` → workspace and parent-directory files → just-in-time files discovered when a tool accesses that directory, up to a trusted root; contents are concatenated; `/memory show` and `/memory reload` expose/reload the hierarchy; `@file.md` imports (relative/absolute) expand via a dedicated import processor (with its own tests) [48]. The file-name set is configurable via `getAllGeminiMdFilenames` [46][48].
- **Settings precedence** [53]: defaults → system defaults file → user → project → system settings (override) → env vars → CLI args.
- **Model-aware prompts**: modern vs legacy snippet sets behind `supportsModernFeatures` [46].

**Runtime harness**

- Loop: `client.ts` `sendMessageStream` with `MAX_TURNS = 100` and bounded turns [54]; tool scheduling and policy live under `packages/core/src/policy` and `tools/` [45].
- **Approval modes** (`policy/types.ts`): `PLAN` < `DEFAULT` < `AUTO_EDIT` < `YOLO` by permissiveness; plan mode restricts to read-only tools; YOLO auto-approves [49].
- Tools (via `tools/tool-names.ts` and docs): `read_file`, `write_file`, `edit`, `glob`, `grep`, `ls`, `run_shell_command`, `web_fetch`, `google_web_search`, `write_todos`, `read_many_files`, `get_internal_docs`, `activate_skill`, `ask_user`, `enter_plan_mode`/`exit_plan_mode`, tracker task tools, and `invoke_agent` for subagents [45][46].
- **Sandboxing** [50]: `-s`/`GEMINI_SANDBOX`/settings; macOS Seatbelt profiles (`permissive-open`, `permissive-proxied`, `restrictive-open`, `restrictive-proxied`, `strict-open`, `strict-proxied`) and container methods (Docker/Podman/runsc/lxc); a sandbox manager plus sandboxed filesystem service live in `core/src/services/` [45][50].
- **Hooks** [52]: `SessionStart/End`, `BeforeAgent`, `AfterAgent`, `BeforeModel`, `AfterModel`, `BeforeToolSelection`, `BeforeTool`, `AfterTool`, `PreCompress`, `Notification`; stdin/stdout JSON contract, strict no-extra-stdout rule; can block turns/tools, rewrite arguments, filter tools, inject context, retry/halt [52].
- **Checkpointing** [51]: before file-modifying tools, a shadow git snapshot is committed under `~/.gemini/history/<project_hash>` plus conversation history; `/restore` reverts files and re-proposes the tool call; disabled by default [51]. Trusted-folders controls which paths get JIT context/tools [45].
- Extensions: `gemini-extension.json` manifest, extension loader, ACP mode for editors, headless mode, checkpointing, and MCP server support [45][53].

**Four-layer mapping.** Foundation: Gemini models (plus local LiteRT path present in `core/src`), hosted-first [45]. Agent harness: generated prompt sections + GEMINI.md hierarchy + approval modes + hooks [46][48][49][52]. Repository layer: read/edit/glob/grep/ls tools, ignore handling (`.geminiignore`), checkpoints [45][51]. Runtime layer: sandbox managers (Seatbelt/containers), tool scheduler, extensions, ACP/headless [45][50][53].

### 2.6 Aider (contrast case: repository-aware assistant, not a tool loop)

**Snapshot.** Open source Python assistant (`Aider-AI/aider` @ `5dc9490`) [55]; docs in `aider/website/docs` [60].

**Prompt-layer harness**

- **Prompt classes per edit format** (`aider/coders/*_prompts.py`) [56]: each coder defines `main_system`, `system_reminder`, `example_messages`, and fixed scaffolding strings (`files_content_prefix`, `files_content_assistant_reply`, `files_no_full_files`, `repo_content_prefix`, `read_only_files_prefix`) [56][52]. Whole-file mode’s reminder dictates an exact file-listing format; udiff/patch/editblock formats carry their own grammars [56].
- Context assembly: files added to the chat are inlined with the “trust this as the true contents” prefix; **repo map** summaries are introduced as read-only, and the model must ask to add files before editing them [56].
- **Repo map** (`repomap.py`) [58]: tree-sitter tags per language, a file/symbol graph ranked with networkx **PageRank**, token-budgeted (`map_tokens`, context-window–scaled), with a tags cache on disk and refresh settings [58].
- `CONVENTIONS.md` is a documented read-only context file [60]; chat history is summarized with a dedicated summarizer prompt when too large [56][61].

**Runtime harness**

- Single-request edit pipeline rather than a general tool loop: `send_message` → parse edits → apply → optionally lint/test; on parse or lint/test failure it reflects with a synthetic message, capped at **3 reflections** [57].
- Function calling exists but only for specific formats (`editblock_func_coder.py` sets `functions = [...]`; `functions = None` in the base coder) [57].
- **Git automation**: `auto_commit` commits applied edits with generated commit messages (`commit_system` prompt) [57][61]; `.aiderignore` and gitigntegration control file set [57][59].
- Modes: code/ask/architect/help; **architect mode** uses two models — an architect for proposals and an editor model that applies them, with confirmation [57][60].
- No approval prompts, no sandbox, no subagents, no MCP (grep found no MCP client code) — containment is “human in the chat loop” plus git undo [55][57][60].

**Four-layer mapping.** Foundation: BYO keys across many models, local models via litellm-style routing [55][60]. Agent harness: format-specific prompts + reflections; no generic tool loop [56][57]. Repository layer: repo map + explicit file set + aiderignore + auto-commit [57][58][59]. Runtime layer: Python process, optional lint/test subprocesses, git snapshots; no sandbox/UI runtime in the terminal-agent sense [57][60].

### 2.7 Likha (current state) — do all four layers exist?

Verified against this repository on 2026-10-03.

| Layer | Present? | Evidence |
| --- | --- | --- |
| **Foundation model** | **No — by design (BYO).** Likha hosts no models and bundles no local inference; it connects to hosted providers with the user’s key. | `internal/app/run.go` package doc (“Likha talks directly to the configured model provider’s OpenAI-compatible API with your own API key (BYOK); it hosts no models and bundles no local inference server”); `internal/model/` (client, providers, Codex OAuth); `internal/providers/` (keys/config). [62][65] |
| **Agent harness** | **Yes, but the prompt half is missing.** Turn loop, tool dispatch, approvals, round cap, compaction, mentions, session naming exist; no system prompt / instruction-file layer is sent. | `internal/agent/agent.go` (`RunTurn`, `agentTools` = glob/read/grep/edit_file/run_command, `dispatchTool`, `requestApproval`, 32-round cap), `mentions.go`, `compaction.go`, `sessionname.go` [62]. FR-19 (compiled-in harness system message) and FR-20 (round-cap auto-continue) are specified in `specs/v1-spec.md` but not implemented; `specs/agent-harness/spec.md` is draft; `specs/agent-loop/spec.md` is planned [67]. |
| **Repository/workspace harness** | **Yes, minimal.** Path-confined read/search tools, ignore-aware tree, diff-preview edits, shell execution; no LSP, repo map, checkpoints, or worktree isolation. | `internal/repository/repository.go` (glob/read/grep with limits and `.git`/symlink refusal), `tree.go` (`.gitignore` + node_modules-aware tree for `@` completion), `internal/actions/edit.go` (`PrepareEdit` diff → `Apply` atomic replace, repo-confined, 1 MiB cap), `command.go` (`sh -c` with process-group cancel, 256 KiB output cap); `@file` expansion in `internal/agent/mentions.go`; git status only for the status bar [63][64][66]. |
| **Runtime harness** | **Yes, minimal.** Bubbletea TUI event loop drives `RunTurn`; session persistence in a per-repo SQLite store; MCP stdio manager; no OS sandbox (approval-gated only), no hooks/extensions, skills package is a placeholder. | `internal/tui/tui.go` (event pump → `agent.RunTurn`), `internal/session/session.go` (SQLite store), `internal/mcp/` (manager/config, trust-on-first-use), `internal/actions/command.go` doc comment: “The working directory is not a sandbox”; `internal/skills/skills.go` placeholder [66]. |

**Answer in one line:** Likha includes the **agent harness (runtime half), the repository/workspace harness, and the runtime harness** in minimal form; it deliberately **does not include a foundation model**, and the **prompt-layer harness is specified but not yet implemented** (FR-19/FR-20, `specs/agent-harness/`, `specs/agent-loop/`).

---

## 3. Comparison tables

### 3.1 Prompt-layer harness

| Terminal | Base prompt control | Instruction files | Precedence model | Per-model variants | Dynamic context |
| --- | --- | --- | --- | --- | --- |
| Claude Code | Compiled, unpublished; `--system-prompt[-file]` replace, `--append-system-prompt[-file]`; output styles [2][14] | CLAUDE.md (managed/user/project/local), AGENTS.md fallback, `.claude/rules/` | Concatenation root→cwd; local last; model resolves conflicts [1] | SDK presets; `CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT`; dynamic-section exclusion [14] | Env + git blocks at startup; `<system-reminder>` messages; CLAUDE.md as user message [11] |
| Codex | `base_instructions/default.md` + per-model `instructions_template`; approval-mode templates [18][19] | AGENTS.md (+ `AGENTS.override.md`, fallbacks) | Root→cwd concatenation; deeper wins; direct instructions outrank files; 32 KiB cap [18][20] | Yes — template resolved from model metadata; empty if missing [19] | Prompt includes planning/validation policy; environment handled by harness [18] |
| OpenCode | Per-model-family `.txt` prompts selected by model id [28] | AGENTS.md (global + first project match), CLAUDE.md fallback, `config.instructions` globs/URLs [29] | Global first; project first-match-wins (no stacking); per-agent prompt overrides [29][30] | 11+ prompt files (anthropic/gpt/beast/gemini/codex/kimi/trinity/meta/astra/copilot/default) [28] | Environment block (cwd, worktree, git flag, platform, date) + references + skills/MCP sections [28] |
| OMP | Bundled Handlebars template; template/plain override chain; append; `PERSONALITY.md` [36] | `.omp/AGENTS.md` + foreign conventions; `RULES.md` sticky; rules/skills [37][38][44] | Project-first discovery; literal > template; provider priorities; one file per scope/depth; farthest-first injection [36][37] | Template is model-agnostic; provider-side cache breakpoint; data-driven sections [36] | `<project-context>` footer (workstation, tree, dir-context, nested repos) after static block [36] |
| Gemini CLI | Generated section snippets; `GEMINI_SYSTEM_MD` full replacement with substitutions [46][47] | GEMINI.md (global/workspace/JIT), configurable filenames, `@` imports [48] | Concatenated global → workspace → JIT; settings layers system > project > user [48][53] | Modern vs legacy snippet sets by model generation [46] | Prompt includes approval-mode/interactivity wording; env context via `environmentContext` [46] |
| Aider | Prompt class per edit format (`main_system`, `system_reminder`, examples) [56] | CONVENTIONS.md read-only; `.aider.conf.yml` [60] | No discovery hierarchy; explicit files + repo map [56][58] | Per edit format, per weak/editor model role [57][60] | Repo map summaries + added files + shell command prompts [56][58] |
| Likha | **Absent** (FR-19 pending) — only tool definitions + conversation [62][67] | None (repo-init draft) [67] | — | — | Compaction summary as `developer` message [62] |

### 3.2 Runtime harness

| Terminal | Loop | Approvals | Sandbox | Subagents | Hooks/extensions | Context mgmt |
| --- | --- | --- | --- | --- | --- | --- |
| Claude Code | Until no tool calls; turn/budget caps; read-only tools parallel [9] | 6 modes + allow/ask/deny rules; classifier for `auto` [3] | OS sandbox `srt` (Seatbelt/bubblewrap/WFP) + network proxy [8][16] | Frontmatter agents w/ own context, worktree isolation [5] | ~30 hook events; in-process JS/TS mods [4][10] | Tool-output clearing → summarization; checkpoints; JSONL sessions [9][12] |
| Codex | Tool loop; plan tool; multi-agent tools [22] | `untrusted`/`on-request`/`granular`/`never` + prefix rules [19][21] | `read-only`/`workspace-write`/`danger-full-access`, network policy, Seatbelt/Landlock [21][25] | Spawn/wait/send/close/resume agent tools (v1/v2) [22] | Extension tools; pre/post compact hooks [22][24] | Auto-compaction with initial-context injection rules [24] |
| OpenCode | Step loop with `maxSteps` [32] | Wildcard allow/ask/deny rules, last-match-wins, per-agent [30][31] | None observed; permission-gated shell [31][33] | `task` tool, depth default 1, background experimental [33] | Plugin hooks on chat/permission/tool execute [34] | Overflow-triggered auto compaction + prune [32] |
| OMP | Tool loop with internal URL devices [44] | Tiers read/write/exec + `always-ask`/`write`/`yolo` + per-tool rules [39] | None (approval is not containment; explicit) [40] | Rich definitions, spawn policy, depth 2, role models, prewalk/advisor [41] | Hook factories + extension modules (in-process, unsandboxed) [42][44] | 6 compaction triggers, method order incl. provider-native, pruning [43] |
| Gemini CLI | `sendMessageStream`, MAX_TURNS 100 [54] | `plan`/`default`/`autoEdit`/`yolo` policy [49] | Seatbelt profiles or Docker/Podman/runsc/lxc [50] | `invoke_agent` + agent registry [45][46] | 10 hook events, JSON stdin/stdout [52] | Compression service + PreCompress hook; shadow-git checkpoints [51][52] |
| Aider | Edit → apply → reflect, max 3 [57] | None (chat-loop confirmation) [60] | None [55] | None [55] | None (git/lint/test integration) [57] | Chat-history summarizer; repo-map budgets [56][58] |
| Likha | `for range 32` tool rounds, fails at cap [62] | Diff/command/MCP approvals [62] | None — “working directory is not a sandbox” [64] | None | None (`internal/skills` placeholder) [66] | Manual `/compact` summarization only [62] |

### 3.3 Attribute summary (assessment)

| Terminal | Flexibility | Structure | Usability | Key integration points |
| --- | --- | --- | --- | --- |
| Claude Code | High (styles, flags, rules, hooks, mods) | High (documented hierarchies) | High (polished TUI, SDK, IDE) | Hooks, MCP, LSP, SDK, worktrees |
| Codex | High (policies, sandbox modes, templates) | High (pinned prompt files, config schema) | High (TUI, exec, cloud, SDK) | Approval rules, sandbox, multi-agent, MCP |
| OpenCode | High (per-agent config, plugins, providers) | Medium-high (many conventions) | High (TUI/server split) | Plugins, LSP, MCP, SDK |
| OMP | Very high (templates, every discovery convention, hooks, agents) | High (explicit block/precedence contracts) | Medium-high (dense feature set) | Hooks, extensions, MCP, internal URLs |
| Gemini CLI | High (system prompt replacement, extensions) | High (docs, settings layers) | High (TUI, ACP, checkpointing) | Hooks, extensions, MCP, ACP |
| Aider | Medium (formats/modes) | Medium (fixed scaffolding strings) | High for edits, low for autonomy | Git, repo map, lint/test |
| Likha | Low today (no prompt/extension surface) | Medium (small, explicit codebase) | Medium (TUI solid; limited tooling) | Approvals, MCP, sessions; hooks absent |

---

## 4. Cross-cutting patterns (what robust harnesses converge on)

1. **Compiled, versioned base prompt + user override chain.** Every mature harness keeps a built-in prompt in a reviewable file and layers user control: replace vs append vs template (OMP), flags + output styles (Claude), full env-var replacement (Gemini), per-model templates (Codex, OpenCode). Prompts are data, not magic strings.
2. **Instruction files with deterministic precedence and caps.** Root→cwd concatenation (Codex, Claude), nearest-wins with provider priorities (OMP, OpenCode), or global→workspace→JIT (Gemini). All bound size (Codex 32 KiB, Claude 25 KB auto memory) and scope (@imports with hop limits).
3. **Prompt-cache-aware layout.** Static prompt first, dynamic working-directory content in one trailing block, cache breakpoint before it (OMP); layered System → Project → Conversation (Claude); model-specific prompt files keyed by id (OpenCode, Codex).
4. **Approval is a policy layer, sandboxing is a separate containment layer.** Codex and Claude pair modes/rules with OS sandboxes; OMP documents explicitly that approval is not containment; the sandboxed surface excludes built-ins/MCP/LSP (Claude) — containment scope must be stated.
5. **Tool-output discipline.** Output caps, truncation with artifacts (`artifact://` / spill files), pruning of superseded reads, and per-turn stale-result elision appear in OMP, Claude, Codex, and Gemini (40k truncation threshold in Gemini).
6. **Context management is automated and multi-strategy.** Overflow recovery, threshold checks (including mid-turn), summarization vs mechanical elision vs provider-native compaction, and auto-continue after compaction.
7. **Extension surface = hooks first, in-process second.** Typed event hooks over tool calls and turns exist in all five agentic terminals; in-process modules (Claude mods, OMP extensions) are powerful but explicitly unsandboxed and same-process.
8. **Subagents are a runtime primitive with explicit policy.** Definition formats, own context windows, spawn allowlists, depth caps, and model roles (Claude, OMP, OpenCode, Codex, Gemini) — Likha has none, which is a deliberate scope choice rather than an omission.
9. **Headless/SDK surface follows the TUI.** `claude -p` + Agent SDK, `codex exec` + SDK, OpenCode server, Gemini ACP/headless, OMP print/RPC/ACP.
10. **Repo-layer depth varies:** repo maps (Aider), LSP + diagnostics (OMP, OpenCode, Claude), apply-patch primitives (Codex, OpenCode), checkpoint/restore or worktrees (Gemini, Claude).

---

## 5. Recommendations

### 5.1 For agent terminals generally (robustness, structure, flexibility, usability)

- **Robustness:** version the base prompt as a file, ship a documented override chain, and keep harness-authored messages out of persisted user history; pair every approval mode with an explicit statement of what remains unsandboxed. Cap and truncate tool output everywhere; make compaction a state machine with overflow/recovery paths, not a single summarizer call.
- **Structure:** adopt one precedence contract per concern (system prompt, instruction files, rules, context) and test it; keep prompt blocks ordered static→dynamic for cache stability; separate approval policy from containment; expose per-agent overrides (prompt, tools, permissions, model) as data.
- **Flexibility:** hooks over tool calls/turns as the minimum extension API; skills as on-demand prompt packs; per-model prompt variants only where measured; discovery adapters for foreign instruction conventions to reduce migration cost.
- **Usability:** `/init`-style instruction-file generation; prompt/context inspection commands (`/context`, `/memory show`, `/dump`); checkpoint/rewind or shadow-git undo; parallel read-only tools and clear progress rendering; headless mode for CI.

### 5.2 For Likha (prioritized; maps to existing specs)

1. **P0 — Implement FR-19 as a versioned prompt file.** Add `internal/agent/prompt.md` embedded with `go:embed` per `specs/agent-harness/spec.md`: identity; tool contract (built-in `read`/`glob`/`grep` are the default for repository inspection; `run_command` reserved for actions only the shell can perform — the `agent-loop` defect); workflow rules; output discipline. Send it as the first message per request; never persist it (session snapshots must contain user-owned history only) [62][67].
2. **P0 — Implement FR-20 (round-cap auto-continue).** Replace the hard fail at 32 rounds with a visible checkpoint notice and a `developer`-role continue instruction, per `specs/agent-loop/spec.md` [67].
3. **P1 — Project instructions loader + `/init`.** Add `internal/agent/instructions.go` discovering `AGENTS.md` from repo root → cwd (Codex pattern), with a size cap (~32 KiB) and `@import` reuse of the existing mention expander; optionally honor `CLAUDE.md`/`LIKHA.md` as fallbacks. Wire the draft `specs/repo-init/spec.md` to generate one.
4. **P1 — Cache-stable prompt layout.** Keep the static prompt as its own message and inject dynamic context (cwd, git branch/status, tree) in a separate trailing message; this mirrors OMP’s block boundary and Claude’s layer order, and matters for provider prompt caching as soon as the prompt lands [11][36].
5. **P2 — Sandbox option + approval tiers.** Introduce `read`/`write`/`exec` tiers per tool (`read`/`glob`/`grep` = read; `edit_file` = write; `run_command`/MCP = exec) and modes `always-ask`/`auto-edit`/`yolo`; add an opt-in OS sandbox for `run_command` (macOS `sandbox-exec`, Linux Landlock) with network policy. Document, like OMP, that shell approval is not containment until then [39][40][21][8].
6. **P2 — Automated context management.** Trigger compaction at a context threshold (not only manual `/compact`), prune stale tool outputs, and spill truncated command output to a retrievable artifact instead of dropping it (Likha truncates at 256 KiB today) [43][40].
7. **P3 — Hooks before plugins.** A minimal in-process hook surface (`tool_call`, `tool_result`, `turn_start/end`, `session_*`) unblocks policy injection and observability without committing to a plugin runtime; keep it out of the approval contract’s critical path [42][4][52].
8. **P3 — Deferred scope, design for it now.** Subagents, skills, and LSP are out of v1, but the prompt/session design should not preclude per-agent prompts, own-context children, or `Skill`-style on-demand packs [41][44][33].

---

## 6. Caveats and limits

- **Claude Code** is closed source; its prompt text and loop internals are unpublished. All its claims are docs-based behavior, not source verification [2].
- **Codex/OpenCode/Gemini/Aider** claims come from clones at the pinned commits above; the projects move quickly (Codex especially — tool namespaces and multi-agent APIs are recent).
- **OMP** behavior is documented by its own `omp://` docs; the running build may differ from the docs’ latest revision.
- No live probes: Codex cloud, Claude hosted enterprise paths, Gemini extensions marketplace, and OMP remote compaction endpoints were not exercised.
- Attribute ratings in §3.3 are the author’s assessment, not measured benchmarks.

---

## 7. Bibliography

**Claude Code (docs-based)**
[1] https://code.claude.com/docs/en/memory
[2] https://code.claude.com/docs/en/settings · https://code.claude.com/docs/en/settings-reference
[3] https://code.claude.com/docs/en/permission-modes · https://code.claude.com/docs/en/permissions
[4] https://code.claude.com/docs/en/hooks
[5] https://code.claude.com/docs/en/sub-agents
[6] https://code.claude.com/docs/en/mcp
[7] https://code.claude.com/docs/en/tools-reference
[8] https://code.claude.com/docs/en/sandboxing · https://github.com/anthropics/sandbox-runtime
[9] https://code.claude.com/docs/en/how-claude-code-works · https://code.claude.com/docs/en/agent-sdk/agent-loop
[10] https://code.claude.com/docs/en/plugins/overview · https://code.claude.com/docs/en/plugins/mods/overview
[11] https://code.claude.com/docs/en/context-window · https://code.claude.com/docs/en/prompt-caching
[12] https://code.claude.com/docs/en/headless · https://code.claude.com/docs/en/agent-sdk/overview
[13] https://claude.com/blog/a-harness-for-every-task-dynamic-workflows-in-claude-code
[14] https://code.claude.com/docs/en/output-styles · https://code.claude.com/docs/en/cli-reference
[15] https://code.claude.com/docs/en/iam
[16] https://www.anthropic.com/engineering/claude-code-sandboxing

**OpenAI Codex (source: github.com/openai/codex @ 9d2b60303e83198905604e704116daa8998c3c47, 2026-10-02)**
[17] Repository: https://github.com/openai/codex
[18] `codex-rs/protocol/src/prompts/base_instructions/default.md`
[19] `codex-rs/prompts/src/model_instructions.rs` · `codex-rs/prompts/templates/permissions/approval_policy/{never,on_request,on_request_rule_request_permission,unless_trusted}.md`
[20] `codex-rs/core/src/agents_md.rs`
[21] `codex-rs/protocol/src/protocol.rs` (`AskForApproval`, `NetworkAccess`) · `codex-rs/protocol/src/config_types.rs` (`SandboxMode`)
[22] `codex-rs/core/src/tools/spec_plan.rs` · `codex-rs/core/src/tools/handlers/`
[23] `codex-rs/config/src/config_toml.rs`
[24] `codex-rs/core/src/compact.rs`
[25] https://developers.openai.com/codex (CLI, config reference, security/sandbox)
[26] https://agents.md

**OpenCode (source: github.com/anomalyco/opencode @ c42ae0d56b6f86f8df39d451d6d2cfe6414b3928, branch dev)**
[27] Repository: https://github.com/anomalyco/opencode (canonical; sst/opencode redirects)
[28] `packages/opencode/src/session/system.ts` · `packages/opencode/src/session/prompt/*.txt`
[29] `packages/opencode/src/session/instruction.ts`
[30] `packages/opencode/src/agent/agent.ts`
[31] `packages/opencode/src/permission/index.ts`
[32] `packages/opencode/src/session/prompt.ts` · `packages/opencode/src/session/processor.ts` · `packages/opencode/src/session/compaction.ts`
[33] `packages/opencode/src/tool/` (`task.ts`, `registry.ts`, `lsp.ts`, …) · `packages/opencode/src/lsp/`
[34] `packages/plugin/src/index.ts`
[35] https://opencode.ai/docs

**OMP (shipped documentation)**
[36] `omp://system-prompt-customization.md`
[37] `omp://context-files.md`
[38] `omp://rulebook-matching-pipeline.md`
[39] `omp://approval-mode.md`
[40] `omp://bash-tool-runtime.md`
[41] `omp://task-agent-discovery.md`
[42] `omp://hooks.md`
[43] `omp://compaction.md`
[44] `omp://extension-loading.md` · `omp://skills.md`

**Gemini CLI (source: github.com/google-gemini/gemini-cli @ fb972b2f87fe7d5b06d37eac711490162d98de2c)**
[45] Repository: https://github.com/google-gemini/gemini-cli
[46] `packages/core/src/prompts/promptProvider.ts` · `packages/core/src/prompts/snippets.ts`
[47] `docs/cli/system-prompt.md`
[48] `docs/cli/gemini-md.md` · `packages/core/src/utils/memoryDiscovery.ts` · `packages/core/src/utils/memoryImportProcessor.ts`
[49] `packages/core/src/policy/types.ts`
[50] `docs/cli/sandbox.md`
[51] `docs/cli/checkpointing.md` · `docs/cli/trusted-folders.md`
[52] `docs/hooks/index.md`
[53] `docs/reference/configuration.md`
[54] `packages/core/src/core/client.ts`

**Aider (source: github.com/Aider-AI/aider @ 5dc9490bb35f9729ef2c95d00a19ccd30c26339c)**
[55] Repository: https://github.com/Aider-AI/aider
[56] `aider/coders/base_prompts.py` · `aider/coders/*_prompts.py`
[57] `aider/coders/base_coder.py`
[58] `aider/repomap.py`
[59] `aider/repo.py`
[60] `aider/website/docs/usage/conventions.md` · `usage/modes.md` · `more/edit-formats.md`
[61] `aider/prompts.py`

**Likha (this repository)**
[62] `internal/agent/agent.go`, `mentions.go`, `compaction.go`, `sessionname.go`
[63] `internal/repository/repository.go`, `tree.go`
[64] `internal/actions/edit.go`, `command.go`
[65] `internal/model/client.go`, `internal/providers/`
[66] `internal/tui/tui.go`, `internal/session/session.go`, `internal/mcp/`, `internal/skills/skills.go`
[67] `specs/agent-harness/spec.md` · `specs/agent-loop/spec.md` · `specs/README.md` · `specs/v1-spec.md` (FR-19, FR-20)
[68] `ARCHITECTURE.md`
