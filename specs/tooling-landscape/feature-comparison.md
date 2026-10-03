# Lisa vs OpenCode V2 vs OMP — product feature comparison

**Date:** 2026-10-03 · **Status:** product-level comparison note. Complements
[research.md](research.md) (callable tools, delegation, permissions) and
[../agent-harness/research.md](../agent-harness/research.md) (prompt/runtime layers); this
note does not repeat their tool-policy detail — it compares the user-visible feature surface.

**Method:** primary sources only.

- **Lisa** — this repository's working tree on `main` (uncommitted changes included), 2026-10-03;
  code claims were verified against the files cited below (two independent inventories:
  docs/specs and code). Status words follow the repo's own vocabulary
  (`implemented (local)` / `in progress` / `draft` / `planned`).
- **OpenCode V2** — `opencode.ai/v2/*` docs, fetched 2026-10-03 via content negotiation. V2 is
  the v2.x line (latest tag `v2.0.22`; docs advertise build 2.0.6) maintained alongside V1
  (`v1.18.34`). Canonical repo: `github.com/anomalyco/opencode` (`sst/opencode` redirects).
  Docs do not label features as draft; "active unless stated otherwise" is assumed.
- **OMP** — the shipped `omp://` documentation (134 files) of the oh-my-pi harness, same snapshot
  date. Several subsystems are opt-in by default; "documented" does not mean enabled.

**Caveats:** docs describe intent, not live probes. No hosted, desktop, or OS-level surface was
exercised. OpenCode V2 dropped several V1 surfaces (sharing, LSP, GitHub, IDE extension); those
are called out as V1-only where the difference matters. Rows marked `n/s` were not surveyed.

## 1. Snapshot

| | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| Implementation | Go + Bubble Tea, single binary | TypeScript/Effect; per-user background server + clients | TypeScript (Bun) + native crates; single binary |
| Interfaces | Full-screen TUI only (plus `--sessions`, `--resume`, `--device-login`, `--help/--version`) | TUI, `mini` interface, desktop app, password-protected web UI, server API, JS client, embedded SDK (incl. Cloudflare workerd), ACP, plugins | TUI, `-p` print / `--mode json|rpc|acp`, in-process SDK, browser-relay, collab + livestream |
| Foundation models | BYOK hosted-only: 12 predefined providers + custom OpenAI-compatible endpoint; ChatGPT OAuth; no local inference | models.dev catalog, provider runtime packages, local discovery (Ollama/LM Studio/vLLM), Console/Go hosted tiers | models.dev-style catalog + `models.yml`, role-based routing, local tiny models (title/memory/TTS/STT), opt-in auth broker |
| Sessions | SQLite per repository, resume, auto-naming, manual `/compact` | Server-managed, tabs, export/import, fork, snapshots (undo/redo/revert) | JSONL append-only tree per cwd, fork/branch/tree, export/share/collab |
| Extensibility | None (MCP client only; `internal/skills` placeholder) | Plugin API (server + CLI), transforms/hooks, RPC, plugin CLI | Extensions + hooks + custom tools, plugin manager, Claude-compatible marketplace |
| Approvals | Per-proposal approval, page-seen review gate, MCP trust-on-first-use | Ordered allow/ask/deny rules, saved project-scoped approvals, policies | read/write/exec tiers, `always-ask`/`write`/`yolo` (yolo default), per-tool overrides, safety overrides |
| Release state | `implemented (local)`, no published release, release gate outstanding | Shipped v2.x line + V1 maintenance line | Shipped harness |

## 2. Feature matrix

Legend: ✅ implemented / shipped · ◐ partial, opt-in, or draft/planned · ✖ absent or unsupported
in the documented surface · `n/s` not surveyed.

### 2.1 Interfaces and distribution

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| Full-screen TUI | ✅ alternate-screen, continuous scroll, scrollbar, status cockpit | ✅ tabs, diff viewer, command palette, `/btw`, steering/queue | ✅ Agent Hub, session picker, git UI |
| Desktop app | ✖ | ✅ macOS/Windows/Linux installers (needed for browser automation) | ✖ |
| Web UI | ◐ draft (`specs/web-ui`, no code) | ✅ password-protected, `opencode pair` one-time links + QR | ◐ browser client for collab sessions (`my.omp.sh`) |
| Headless / print / scripting | ✖ (only `--sessions` list, `--device-login`) | ✅ `opencode run` one-shot, server API, SDK; `serve` foreground | ✅ `-p` print, `--mode json|rpc|acp`, RPC host tools |
| Server API / SDK | ✖ | ✅ intentional V2 breaking API, `@opencode/client`, embedded SDK/workerd | ✅ JSON-RPC over stdio + in-process SDK |
| Editor integration (ACP) | ✖ | ✅ `opencode acp` (Zed etc.) | ✅ `omp acp` |
| Session sharing / collab | ✖ | ✖ explicitly unsupported in V2 (V1 had it) | ✅ E2E-encrypted `/share`, live `/collab`, `/record` + livestream |
| Install / update | ◐ curl installer + scripts; no release published; update = notify-only | ✅ curl/brew/npm/AUR/Docker; update `disable|notify|auto`; `upgrade`, `uninstall` | ✅ `omp update --canary/--stable` |
| Background service | ✖ process-per-TUI | ✅ one shared per-user server owns sessions/tools/permissions | ✖ |

### 2.2 Providers and models

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| Provider catalog | ✅ 12 predefined (11 key-based + ChatGPT OAuth), only `opencode-go` live-verified | ✅ models.dev + provider packages (openai/anthropic/google/azure/bedrock/…), per-project availability | ✅ catalog + `models.yml` custom providers + extension-registered providers |
| Auth methods | ✅ API key (flag/env/stored), ChatGPT OAuth browser + headless device login | ✅ API key, OAuth, provider command, env; multi-account activate/rename/switch/delete | ✅ 7-layer key resolution, `/login` OAuth, opt-in auth broker + gateway |
| Custom OpenAI-compatible endpoint | ✅ `--endpoint`, loopback HTTP only, "unverified" marker | ✅ custom providers with settings/headers/models | ✅ custom providers with compat flags + discovery types |
| Local model discovery | ✖ by design | ✅ Ollama/LM Studio/vLLM auto-discovery | ◐ local tiny models for title/memory/TTS/STT |
| Multi-account | ✖ one credential per provider | ✅ saved provider accounts | ◐ account pools via auth broker |
| Model roles / variants | ✖ single active pair; `/models` switches | ✅ `model#variant`, per-agent model | ✅ `modelRoles` (default/smol/slow/vision/plan/commit/tiny/memory/task/advisor) + fallback chains |
| Reasoning control | ◐ `/think` draft only; reasoning streams and renders | ✅ reasoning-field compatibility, variants for effort | ✅ thinking levels per model/agent, `/thinking` |
| Context-window metadata | ✅ override > provider metadata > catalog; ctx tracker in progress | ✅ catalog limits | ✅ catalog + compaction thresholds |
| Spend / usage | ✅ session spend from curated pricing; unknown pricing hidden | ✅ Console budgets, `stats` | ✅ `omp usage` provider limits, `stats` dashboard, service tiers |

### 2.3 Sessions and history

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| Persistence / resume | ✅ per-repo SQLite; `--resume`, `/sessions`, `--sessions` | ✅ create/list/delete/export/import; session tabs | ✅ JSONL tree; `--continue` (terminal breadcrumbs), `/resume` picker, foreign import (`@claude/@codex`) |
| Fork / branch / tree | ✖ linear | ✅ fork from a message (keybind, ACP) | ✅ `/tree` navigate, `/branch` new file, `--fork`, branch summaries (opt-in) |
| Undo / revert / snapshots | ◐ stale-edit refusal only | ✅ git-worktree snapshots; `/undo`, `/redo`, per-message Revert | ◐ `checkpoint`/`rewind` conversation-state pair (opt-in) |
| Export / dump / share | ✖ | ✅ `session export` (`--sanitize`, JSON), import; sharing unsupported | ✅ standalone HTML export, `/dump`, E2E `/share`, collab |
| Naming | ✅ auto title after first turn | ✅ title agent (hidden) | ✅ tiny-model titles, `/rename` |
| Prompt history | ✅ Up/Down recall, persisted | n/s | ✅ SQLite FTS5 `history.db`, `Ctrl+R` search |
| Fresh / clear semantics | ◐ new session via `/quit`+relaunch; no in-place reset | n/s | ✅ `/new`, `/fresh` (stream rotation), `/clear` (context reset only), `/delete`, `/restart` |

### 2.4 Context, prompt, and compaction

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| System prompt control | ✖ FR-19 specified, not implemented; no harness prompt sent | ✅ per-agent system prompts + env/date block + skill/MCP sections | ✅ Handlebars templates, override chain, `PERSONALITY.md` |
| Instruction files | ✖ (grep: no AGENTS.md/CLAUDE.md loading; `repo-init` draft) | ✅ global + root→cwd + nested-on-read `AGENTS.md`; hot reload; no CLAUDE.md fallback | ✅ 8+ conventions (incl. CLAUDE/GEMINI/Codex), sticky `RULES.md`, `@imports`, rulebook/TTSR |
| Mentions / attachments / references | ✅ `@file`/`@folder` (16 files/64 KiB caps) | ✅ attachments (images/PDF/dirs, 20 MiB), references (outside dirs, git repos) | ◐ image paste (OSC 5522), skills; references n/s |
| Skills | ◐ draft (`custom-commands`); placeholder package | ✅ `SKILL.md` + HTTP catalogs, autoinvoke metadata | ✅ multi-ecosystem skills + managed (auto-learned) skills |
| Memory | ✖ | ✖ | ✅ 5 backends (off/local/hindsight/mnemopi/sharpshooter), opt-in, `memory://` URLs |
| Compaction | ◐ manual `/compact` only (one summarizer call) | ✅ automatic default + manual; keep ~15k recent tokens; provider-native checkpoints | ✅ 6 triggers, 5 methods (remote→snapcompact→handoff→shake→soft), async speculation, per-agent thresholds |
| Tool-output pruning / elision | ✖ (256 KiB command cap discards the rest) | ◐ keep.tokens/compaction config | ✅ superseded-read pruning, useless-result elision, `artifact://` spill |
| Context notebook (anti-summarization) | ✖ | ✖ | ✅ experimental `context_notes` + `new_context` rollover |

### 2.5 Tools and agent loop

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| Built-in model tools | 5: `glob`, `read`, `grep`, `edit_file`, `run_command` + MCP tools | read, glob, grep, edit, write, patch (GPT), shell, webfetch, websearch, question, skill, subagent, execute | Registry incl. read/bash/edit/write/glob/grep/find/ast_grep/ast_edit/lsp/task/todo/ask/eval/web_search/github/debug/memory/checkpoint… |
| Subagents | ✖ (explicitly deferred) | ✅ `subagent` tool, depth default 1, background mode, agent definitions (build/plan/general/explore + custom) | ✅ `task` depth default 2, spawn allowlists, roles, Agent Hub, vibe mode, advisors, prewalk |
| Web fetch / search | ✖ | ✅ `webfetch`, `websearch` (5 providers) | ✅ `web_search` (~30 adapters, incl. keyless) |
| Browser / computer use | ✖ | ✅ Code Mode browser namespace (desktop app) | ✅ browser prelude (incl. relay to real Chrome) + computer use (opt-in) |
| LSP / AST tooling | ✖ | ✖ explicitly not run in V2 (V1 had LSP) | ✅ ~50 LSP servers, `ast_grep` (opt-in), `ast_edit` (default on) |
| Code execution / eval | ✖ | ✅ Code Mode `execute` (JS, tool-call plumbing) | ✅ `eval` Python/JS kernels, `%magic`s, notebooks |
| Todo / plan | ◐ `/plan` draft; no todo tool | ✅ built-in `plan` agent (read-only, writes plan files) | ✅ `todo` state machine + `/todo` HUD; plan mode read-only enforcement |
| Parallel read-only tool calls | ✖ loop dispatches calls sequentially (`internal/agent/agent.go`) | ✅ Code Mode parallel calls | n/s (batched `task` calls run independently) |
| Round cap | ◐ hard fail at 32 rounds; FR-20 auto-continue planned | ✅ `steps` limit removes tools and asks to summarize | ✅ auto-continue after compaction/round checkpoints |

### 2.6 Permissions and safety

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| Approval model | ✅ per-proposal edit/command approval + scroll-to-end gate; no blanket grants | ✅ ordered `allow/ask/deny` last-match-wins; external-directory and `.env` asks by default | ✅ read/write/exec tiers; `always-ask`/`write`/`yolo` (default); per-tool overrides |
| Durable / saved approvals | ✖ (v1 deliberately has none; MCP trust is session-scoped TOFU) | ✅ save-always project-scoped pattern; never overrides deny | ✅ persistent per-tool policy keys |
| Policies (hard deny) | ✖ | ✅ `experimental.policies` (tighten-only), Console-managed policies | ◐ tool-declared deny + safety overrides cannot be bypassed |
| Sandbox | ✖ documented as "working directory is not a sandbox" | ✖ not documented for V2 | ✖ approval is explicitly not containment |
| Secret redaction | ✖ | ◐ export `--sanitize` | ✅ `secrets` obfuscation, share redaction |
| MCP trust | ✅ trust-on-first-use per server per session | ✅ permission actions per MCP tool (`<server>_<tool>`) | ✅ per-server keys, user deny/prompt lists |

### 2.7 Extensions and integrations

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| MCP transports | ◐ stdio only, Claude-Desktop-shaped `mcp.json` | ✅ stdio + Streamable HTTP, OAuth (PKCE, dynamic client registration), timeouts | ✅ stdio + HTTP + SSE, OAuth, `${VAR}`/`!command` secrets, cross-tool config import |
| Hooks | ✖ | ✅ plugin transforms + domain hooks (prompt/context/compaction/model/http/tool) | ✅ `pi.on` events; `tool_call` block/rewrite, `tool_result` patch |
| Plugin system | ✖ | ✅ server plugins (`Plugin.define`), CLI plugins, plugin RPC, `plugin add/check/update` | ✅ extensions, custom tools, plugin manager, Claude-compatible marketplace |
| Marketplace | ✖ | ◐ npm/git plugin install (no marketplace docs) | ✅ `/marketplace` (skills, commands, agents, rules, hooks, tools, MCP, LSP, DAP) |
| Worktrees / VCS | ✖ | ✅ worktree strategies + VCS providers | ◐ `omp worktree` / PR checkouts via github tool |
| GitHub integration | ✖ | ✖ no V2 docs (V1 had GitHub Actions) | ✅ `github` tool + `issue://`/`pr://` cache (opt-in, needs `gh`) |
| Formatters | ✖ | ✅ ~30 built-in post-edit formatters | n/s |
| Notifications / sounds | ✖ | ✅ system notifications + attention sounds per event | ◐ terminal notifications (`ask`), TTS opt-in |
| Diff viewer | ◐ review panes per proposal | ✅ multi-source diff viewer (branch/committed/working/turn), mark-reviewed | ◐ per-tool renderers; `omp git` split diff |

### 2.8 TUI and UX

| Capability | Lisa | OpenCode V2 | OMP |
| --- | --- | --- | --- |
| Themes | ✅ 22 adaptive families, live preview, color-literal preview | ✅ ~30 built-ins + custom JSON, light/dark/system | ✅ built-in + custom JSON, symbol presets, color-blind mode, hot reload |
| Keybinding remap | ✖ fixed chords (readline-style composer) | ✅ cli.json keybinds + leader key | ✅ `keybindings.yml` action map |
| Composer | ✅ readline editing, kill-ring, 4 styles, steering queue, prompt history | ✅ multiline, `!` shell mode, palette, `/btw` side questions, queue-vs-steer | ✅ Vim mode, draft recovery, OSC 5522 paste, push-to-talk STT |
| Session tabs | ✖ | ✅ persistent tabs, quick-switch slots | ◐ session picker; tab-like switching n/s |
| Activity indicators | ◐ working indicator (feature tests pass; walkthrough pending) | ✅ tool/diff/attention states | ✅ activity rows, cost/tokens per agent |
| Voice / dictation | ◐ `/transform` draft (Wispr-style) | ✖ | ✅ STT/TTS, `/live` voice mode |

## 3. What Lisa can copy — ranked

Tiers by size and fit with Lisa's stated design (BYOK, approval-gated, repository-confined).
Items already covered by Lisa drafts are marked; copy the external mechanism only where it is
better-specified than the draft.

### Tier 1 — small, high value, mostly already on the roadmap

1. **Instruction files (`AGENTS.md`) + `/init`.** *Source:* OpenCode V2
   ([instructions](https://opencode.ai/v2/docs/instructions/)) loads global + root→cwd +
   nested-on-read `AGENTS.md` with hot reload and no CLAUDE.md fallback; OMP discovers 8+
   conventions with a sticky `RULES.md` and `@imports`. *Lisa today:* nothing loads instruction
   files (verified: no matches in `internal/`); `specs/repo-init` and FR-19 are drafts only.
   *Shape:* versioned static prompt first, a trailing dynamic `<project-context>` block with a
   ~32 KiB cap, root→cwd concatenation, never persisted into session history. This is P0/P1 in
   the existing [agent-harness research](../agent-harness/research.md).
2. **Tool-round auto-continue (FR-20).** *Source:* OMP emits a visible checkpoint and continues;
   V2's `steps` limit removes tools and asks the model to summarize instead of erroring. *Lisa
   today:* hard fail at 32 rounds (`internal/agent/agent.go` "model exceeded 32 consecutive tool
   rounds"). Already specified in `specs/agent-loop`.
3. **Auto-compaction + output pruning.** *Source:* V2 auto-compacts by default keeping ~15k
   recent tokens; OMP has six triggers (incl. mid-turn maintenance), method fallback
   (`remote → snapcompact → handoff → shake → soft`), superseded-read pruning, useless-result
   elision, and `artifact://` spill for truncated output. *Lisa today:* manual `/compact` only;
   256 KiB command output is discarded wholesale; context tracker is in progress. *Shape:*
   threshold trigger driven by the context tracker, keep-window, and artifact spill for truncated
   command output.
4. **Session export / dump.** *Source:* OMP `/export` (standalone HTML incl. subagent transcripts)
   and `/dump`; V2 `session export --sanitize` + import. *Lisa today:* none. Cheap TUI/CLI surface;
   E2E sharing (OMP `/share`) is a later option, not v1.
5. **`/undo` for approved writes.** *Source:* V2 snapshots restore files and return the prompt
   (`/undo`, `/redo`, per-message revert). *Lisa today:* stale-edit refusal protects against
   overwrite but there is no rollback. *Shape:* snapshot the target before applying an approved
   `edit_file` (repo-confined), expose `/undo`; do not change approval semantics.
6. **Working-indicator + notification parity.** *Source:* V2 system notifications/attention sounds
   per event (permission/question/done/error/subagent); OMP activity rows. *Lisa today:* working
   indicator is in progress; no desktop notifications. Small win once the indicator lands.

### Tier 2 — medium, needs design work

7. **Skills (`SKILL.md` packs).** *Source:* V2 (`skills` array, HTTP catalogs, autoinvoke
   metadata) and OMP (multi-ecosystem discovery, `skill://` reads, `/skill:<name>` commands,
   managed skills). *Lisa today:* `internal/skills` placeholder; `custom-commands` draft.
   *Shape:* discovery from `~/.config/lisa/skills` + `.lisa/skills`, name/description in the
   prompt, explicit `skill://` reads, no new execution permissions.
8. **Subagents (`task` tool).** *Source:* V2 subagent tool (depth 1, background, per-agent
   definitions/permissions) and OMP (depth 2, spawn allowlists, role-routed models, Agent Hub,
   structured output). *Lisa today:* none; explicitly deferred. *Prerequisites before copying:*
   child tool set, spawn permission boundary (OMP treats the parent `task` approval as
   authorization; children run `yolo` headless), cancellation, and reporting UX. Keep depth 1 and
   read-only-biased children for a first slice.
9. **MCP Streamable HTTP transport (+ OAuth).** *Source:* both V2 and OMP. *Lisa today:* stdio
   only (`internal/mcp/config.go` accepts command/args/env). Boring, additive change.
10. **Hooks surface before full plugins.** *Source:* OMP `pi.on` (`tool_call` can rewrite input,
    `tool_result` can patch content, fail-closed); V2 plugin transforms/hooks. *Lisa today:*
    none. *Shape:* minimal in-process event surface (`tool_call`, `tool_result`, turn/session
    lifecycle) — the existing research rates this P3; it stays out of the approval critical path.
11. **Plan mode / thinking control / text transforms.** *Source:* V2 built-in `plan` agent
    (read-only, may write plan files); OMP plan-mode children forced read-only and thinking
    levels. *Lisa today:* all three are drafts (`specs/plan-mode`, `specs/thinking-control`,
    `specs/text-transforms`). Implement against these drafts; copy OMP's rule that plan mode
    blocks side-effecting MCP calls.
12. **Model roles for cheap side tasks.** *Source:* OMP `modelRoles` routes title/compaction/
    memory to small models; V2 variants/per-agent models. *Lisa today:* session naming,
    compaction, and summaries all use the active model. *Shape:* optional role config
    (`title`, `summarize`) with fallback to the active model.
13. **Session fork / branch.** *Source:* V2 fork-from-message; OMP `/tree`, `/branch`, `--fork`.
    *Lisa today:* linear snapshots only. Medium design change to the SQLite snapshot store;
    valuable for exploratory editing once subagents/skills expand.
14. **Keybinding file + user themes.** *Source:* V2 `cli.json` keybinds with leader key and
    themes dir; OMP `keybindings.yml` + theme JSON with hot reload. *Lisa today:* fixed chords,
    22 built-in themes. *Shape:* map action IDs → chords with defaults; load custom theme JSON.

### Tier 3 — architectural; only with product intent

15. **Headless + server + SDK + web UI.** *Source:* V2 is the reference shape (per-user
    background server, breaking HTTP API, `@opencode/client`, embedded SDK, `pair` web UI, ACP);
    OMP (`-p` print, `--mode json|rpc|acp`, host tools, SDK). *Lisa today:* TUI-only; `web-ui`
    draft exists; the agent core is already decoupled from the TUI (`ARCHITECTURE.md`), which is
    the necessary precondition. Natural sequence: `lisa -p` one-shot → RPC/server → web.
16. **Plugin system + package manager / marketplace.** *Source:* V2 plugin API + CLI plugin
    management; OMP extensions/custom tools + Claude-compatible marketplace. Only worth copying
    after hooks prove insufficient.
17. **Memory backends.** *Source:* OMP's five opt-in backends with `memory://` and explicit
    `learn`/`recall` tools. *Lisa today:* none. High complexity, privacy-sensitive; treat as a
    non-goal until cross-session recall is an explicit requirement.
18. **Sandboxing.** Neither V2 nor OMP ships an OS sandbox; Lisa's own disclosure ("not a
    sandbox") matches the market default. If containment becomes a goal, copy from Claude Code /
    Codex patterns surveyed in [agent-harness/research.md](../agent-harness/research.md), not
    from these two.

### Do not copy (conflicts with Lisa's stated design)

- **Yolo-default approvals** (OMP default `yolo`) — contradicts Lisa's per-proposal gate.
- **Hosted control plane** (OpenCode Console accounts, budgets, team policies) — Lisa is
  BYOK/no-account by design.
- **Self-updating binary** (V2 `update: auto`) — Lisa's notify-only + installer split is deliberate.
- **Silent context rewrites without a visible divider** — both copy sources keep the display
  transcript intact; Lisa already does this for `/compact` (keep it).

### Lisa strengths to preserve while copying

Review-to-end approval gate, no blanket permissions (v1), MCP session-scoped TOFU, ChatGPT
browser + headless device login, path-confined tools with symlink defense, readline-grade
composer, continuous-scroll transcript with scrollbar, 22 adaptive themes, configurable status
line, steering-prompt queue with held-queue semantics.

## 4. Limits

- OpenCode V2 claims are docs-based (fetched 2026-10-03); the V2 API reference page serves HTML
  only and was not parsed. V2 docs prohibit assuming V1 behavior; migration notes were used for
  deltas.
- OMP claims are from its shipped `omp://` docs; the running build may differ, and most
  subsystems are opt-in.
- Lisa claims cover the working tree at scan time (uncommitted models/providers work included).
  Real-terminal walkthroughs and the release gate remain outstanding for several
  `implemented (local)` features, per `specs/README.md`.
- No hosted provider, desktop app, browser backend, or OS-level surface was exercised; effort
  estimates are absent because none were measured.
