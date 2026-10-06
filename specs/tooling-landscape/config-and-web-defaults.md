# Config location, config shape, and web-tool defaults across five CLIs

**Date:** 2026-10-05. **Status:** research note feeding three Likha decisions:
(1) should `web_search`/`web_fetch` be on by default, (2) where should config
live on macOS, (3) should web settings stay in `tools.json` or move into one
main config file.

**Method:** pinned upstream source plus first-party docs fetched 2026-10-05.
No live CLI probes. Where docs and source disagree, or a claim comes only from
docs, the text says so. Claims not confirmed in either are marked
**unverified**. This note covers only config/state layout and web defaults. For
tool inventories, approvals, and web data flow, see [research.md](research.md),
[cli-workflows.md](cli-workflows.md#web-and-data-flow), and
[../agent-harness/research.md](../agent-harness/research.md).

## Snapshots

| Product | Snapshot used |
| --- | --- |
| Codex CLI | `openai/codex` `main` @ [`823ea83`](https://github.com/openai/codex/tree/823ea830c0fd418b09ff02d36cad9a1fff66465b) (2026-10-05). Latest release `rust-v0.160.0` (2026-10-01). Docs: `developers.openai.com/codex/*` now 308-redirects to `learn.chatgpt.com/docs/*`. |
| Claude Code | Closed source. Live docs at `code.claude.com/docs/en/*.md`. npm `@anthropic-ai/claude-code` 2.1.289 (modified 2026-10-03). |
| OpenCode | `anomalyco/opencode` (`sst/opencode` redirects here). V1 line: `dev` @ [`907b3bc`](https://github.com/anomalyco/opencode/tree/907b3bc518fa48e90e8ec24dd327d13eee71c36c) (package 1.18.34 = latest GitHub release). V2 line: tag `v2.0.23` @ [`0fd7e28`](https://github.com/anomalyco/opencode/tree/0fd7e2829449b052abf0078666669302923d77af) (git tag only, no GitHub release). Docs: `opencode.ai/docs` (V1) and `opencode.ai/v2/docs` (V2). |
| OMP | `can1357/oh-my-pi` `main` @ [`c4963c0`](https://github.com/can1357/oh-my-pi/tree/c4963c0bc3233272e8087ce293c8032344835e57) (coding-agent 18.6.2; latest release v18.6.1). |
| Pi | `earendil-works/pi` `main` @ [`5b6c792`](https://github.com/earendil-works/pi/tree/5b6c792b424e73edefbfa558b901bcd64788dad2) (v1.0.3). `badlogic/pi-mono` redirects here (GitHub API resolves it to `earendil-works/pi`). |

Permalink prefixes used below: `CX` = `https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/`,
`OC1` = `https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/`,
`OC2` = `https://github.com/anomalyco/opencode/blob/0fd7e2829449b052abf0078666669302923d77af/`,
`OMP` = `https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/`,
`PI` = `https://github.com/earendil-works/pi/blob/5b6c792b424e73edefbfa558b901bcd64788dad2/`.

---

## Findings

### Codex CLI

**A. Location.**
- One root, `CODEX_HOME`. When unset it is `~/.codex` (`dirs::home_dir()` + `.codex`). If `CODEX_HOME` is set, the directory must already exist or startup errors. There is no XDG or Application Support branch, so macOS and Linux behave the same ([CX `codex-rs/utils/home-dir/src/lib.rs` L5-62](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/utils/home-dir/src/lib.rs#L5-L62)).
- Config and data share that root:
  - `config.toml` ([CX `core/src/config/mod.rs` L269](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/core/src/config/mod.rs#L269))
  - `sessions/` and `archived_sessions/` ([CX `rollout/src/lib.rs` L86-87](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/rollout/src/lib.rs#L86-L87))
  - `log/`, unless `log_dir` is set
  - SQLite DBs such as `state_5.sqlite` and `logs_2.sqlite` ([CX `state/src/sqlite.rs` L34-39](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/state/src/sqlite.rs#L34-L39)). These live under `sqlite_home`, which resolves from config, then `CODEX_SQLITE_HOME`, then `CODEX_HOME` ([CX `core/src/config/mod.rs` L4092-4109](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/core/src/config/mod.rs#L4092-L4109)).
- Credentials: `auth.json` in `CODEX_HOME` by default. `cli_auth_credentials_store` can be `file` (default), `keyring`, `auto`, or `ephemeral`. MCP OAuth defaults to `auto` (keyring, then `CODEX_HOME/.credentials.json`) ([CX `config/src/types.rs` L136-165](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/config/src/types.rs#L136-L165)).

**B. Shape.**
- A single TOML file holds everything non-secret: `mcp_servers`, `model_providers`, `profiles`, `features`, top-level `web_search`, and `[tools.web_search]` ([CX `config/src/config_toml.rs` L296, L329, L363, L472, L700](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/config/src/config_toml.rs#L472)).
- Layers, highest first: CLI/`--config`, project `.codex/config.toml` (trusted projects only), `--profile`, `~/.codex/config.toml`, cloud-managed, `/etc/codex/config.toml`, built-ins ([Config basics](https://learn.chatgpt.com/docs/config-file/config-basic)).

**C. Web.**
- `web_search` modes are `disabled | cached | indexed | live`. The enum default is `Cached` ([CX `protocol/src/config_types.rs` L376-382](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/protocol/src/config_types.rs#L376-L382)).
- Resolution order: explicit `web_search` key, then legacy feature flags, then `Cached` ([CX `core/src/config/mod.rs` L2697-2708, L3784-3785](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/core/src/config/mod.rs#L2697-L2708)).
- With no outer sandbox (`PermissionProfile::Disabled`, i.e. full access), the turn prefers `Live`. `Live`/`Indexed` also require the provider capability `external_web_access` ([CX L3100-3146](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/core/src/config/mod.rs#L3100-L3146)).
- `--search` pushes `web_search="live"` ([CX `tui/src/startup_orchestration.rs` L68-72](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/tui/src/startup_orchestration.rs#L68-L72)). Its help text says the hosted tool runs with "no per-call approval" ([CX `tui/src/cli.rs` L73-75](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/tui/src/cli.rs#L73-L75)).
- Backing: the OpenAI Responses hosted `web_search` tool. Cached mode sends `external_web_access: false` ([CX `core/src/tools/hosted_spec.rs` L14-20](https://github.com/openai/codex/blob/823ea830c0fd418b09ff02d36cad9a1fff66465b/codex-rs/core/src/tools/hosted_spec.rs#L14-L20)).
- Docs: "Codex enables cached search by default… an OpenAI-maintained index"; set `web_search = "live"` or `"disabled"`; "with full access, web search defaults to live results"; it does not use the sandbox network proxy. Custom providers default to `supports_standalone_web_search = false`, and "standalone web search… is off by default" ([Web search](https://learn.chatgpt.com/docs/web-search)).
- No third-party key is needed (it uses the model provider's own credential).
- Codex has no general client-side URL fetch tool. Hosted page-open behavior depends on model/API ([cli-workflows.md](cli-workflows.md#web-and-data-flow)).

### Claude Code

**A. Location.**
- `~/.claude` on all OSes (`%USERPROFILE%\.claude` on Windows). `CLAUDE_CONFIG_DIR` moves "settings, session history, and plugins" ([settings](https://code.claude.com/docs/en/settings), [env-vars](https://code.claude.com/docs/en/env-vars)). Docs describe no XDG or Application Support location for user files.
- `/Library/Application Support/ClaudeCode/` (macOS) and `/etc/claude-code/` (Linux) hold only admin-managed `managed-settings.json` and `managed-mcp.json` ([managed settings](https://code.claude.com/docs/en/managed-settings)).
- Sessions: `~/.claude/projects/<project>/<session>.jsonl`. Prompt history: `~/.claude/history.jsonl`. Caches, backups, and other app data also sit under `~/.claude` ([.claude directory](https://code.claude.com/docs/en/claude-directory)).
- Credentials ([authentication](https://code.claude.com/docs/en/authentication)):
  - macOS: Keychain, falling back to `~/.claude/.credentials.json` (0600) when the Keychain rejects the write.
  - Linux: `~/.claude/.credentials.json` (0600).
  - With `CLAUDE_CONFIG_DIR`, the credential file moves under it and the Keychain entry is keyed to it.
- Config and data are not split: one dot-dir.

**B. Shape.**
- Strict JSON only; comments and trailing commas are errors ([settings](https://code.claude.com/docs/en/settings)).
- Settings files: `~/.claude/settings.json` (user), project `.claude/settings.json` (shared), `.claude/settings.local.json` (personal; approvals are saved here), plus managed settings.
- `~/.claude.json` is an app-written file for OAuth session, per-project trust, personal MCP servers, and `/config` global keys.
- Project MCP lives in `.mcp.json`.
- So user-facing config spans two files: settings, plus app state and MCP. **Unverified:** whether `~/.claude.json` moves under `CLAUDE_CONFIG_DIR`. Docs say every `~/.claude` path moves, but `~/.claude.json` sits beside that directory, not inside it.

**C. Web** ([permissions](https://code.claude.com/docs/en/permissions), [tools reference](https://code.claude.com/docs/en/tools-reference)):
- `WebSearch` and `WebFetch` are both built in and available by default.
- WebSearch: Manual mode asks before each call ("Approval required: Yes"). "Don't ask again" is saved permanently per repository. Rules take no specifier: a bare `WebSearch` in `permissions.allow`/`deny`.
- WebSearch backend: Anthropic's server-side web search, which is not configurable. It is unavailable on Bedrock and needs an Anthropic-hosted deployment on Foundry. No extra key. Capped at 200 searches per session.
- WebFetch: asks, except for a built-in list of preapproved documentation domains. Approval is saved per repository and domain as `WebFetch(domain:…)` in `.claude/settings.local.json`. Fetched content passes through a separate extraction-model call, and a domain safety check runs first.
- Disable either tool with a bare deny rule, e.g. `"deny": ["WebFetch"]` removes the tool.
- These prompt defaults describe Manual mode. Other permission modes, e.g. auto with its classifier, change who decides ([permissions](https://code.claude.com/docs/en/permissions)).

### OpenCode

**A. Location (V1 and V2 identical).**
- Uses `xdg-basedir` on every OS, macOS included:
  - config `$XDG_CONFIG_HOME/opencode` (default `~/.config/opencode`)
  - data `~/.local/share/opencode`
  - cache `~/.cache/opencode`
  - state `~/.local/state/opencode`
  - logs `data/log`

  Sources: [OC1 `packages/core/src/global.ts` L3-29](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/core/src/global.ts#L3-L29); V2's own copy at [OC2 `packages/util/src/global-roots.ts` L4-19](https://github.com/anomalyco/opencode/blob/0fd7e2829449b052abf0078666669302923d77af/packages/util/src/global-roots.ts#L4-L19). `xdg-basedir` falls back to `~/.config` and friends without checking the OS ([sindresorhus/xdg-basedir `index.js` @ 4c068bc](https://github.com/sindresorhus/xdg-basedir/blob/4c068bce91132119b8590627ee51b70790551b80/index.js)).
- Env overrides:
  - `OPENCODE_CONFIG_DIR` replaces the config dir ([OC1 global.ts L64](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/core/src/global.ts#L64))
  - `OPENCODE_CONFIG` adds a custom config file
  - `OPENCODE_CONFIG_CONTENT` supplies inline config
  - `OPENCODE_DB` overrides the database path ([OC2 `packages/cli/src/database-path.ts`](https://github.com/anomalyco/opencode/blob/0fd7e2829449b052abf0078666669302923d77af/packages/cli/src/database-path.ts))
- Config and data are split.
- Credentials: V1 `data/auth.json` ([OC1 `packages/opencode/src/auth/index.ts` L10](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/auth/index.ts#L10)). V2 imports `auth.json` into the data-dir SQLite database ([OC2 migration `20260805200742_import_legacy_credentials.ts`](https://github.com/anomalyco/opencode/blob/0fd7e2829449b052abf0078666669302923d77af/packages/core/src/database/migration/20260805200742_import_legacy_credentials.ts)).
- Sessions: `data/opencode.db` ([OC1 `packages/core/src/database/database.ts` L53](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/core/src/database/database.ts#L53)).
- `/Library/Application Support/opencode/` is used only for admin-managed config ([V1 config docs](https://opencode.ai/docs/config)).

**B. Shape.**
- One main JSON/JSONC file, `opencode.json(c)`. It holds model, providers, permissions, `mcp`, and `websearch`. V1 also reads a legacy `config.json` and splits TUI settings into `tui.json` ([OC1 `packages/opencode/src/config/config.ts` L141, L272-274](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/config/config.ts#L272-L274)).
- Project config: `opencode.json(c)` or `.opencode/opencode.json(c)`, merged from the farthest directory to the nearest ([V2 config](https://opencode.ai/v2/docs/config)).
- Secrets stay out of the config file.

**C. Web.**
- **V1 (current release):**
  - `webfetch` is always registered. The default ruleset is `"*": "allow"`, so it runs without a prompt ([OC1 `agent/agent.ts` L119-136](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/agent/agent.ts#L119-L136)). Docs: "By default, all tools are enabled and don't need permission to run" ([V1 tools](https://opencode.ai/docs/tools)).
  - `websearch` is exposed only when the provider is `opencode` or `opencode-go`, or when `OPENCODE_ENABLE_EXA` or `OPENCODE_ENABLE_PARALLEL` is truthy ([OC1 `tool/registry.ts` L58-65, L293-295](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/tool/registry.ts#L58-L65); [`effect/runtime-flags.ts`](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/effect/runtime-flags.ts)).
  - Search backend: Exa's hosted MCP (`mcp.exa.ai`, key optional) or Parallel's MCP. The choice is by flag, `OPENCODE_WEBSEARCH_PROVIDER`, or a session-hash coin flip ([OC1 `tool/mcp-websearch.ts` L4-7](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/tool/mcp-websearch.ts#L4-L7); [`tool/websearch.ts`](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/tool/websearch.ts)).
- **V2:**
  - `webfetch` falls under the base policy `{action:"*", effect:"allow"}`, so no prompt ([V2 permissions](https://opencode.ai/v2/docs/permissions)).
  - `websearch` is registered by default. Docs: "The first search asks you to allow web search and select a provider. OpenCode remembers your choice."
  - Providers: Exa, Firecrawl, Parallel, Tavily, TinyFish.
  - Config: `"websearch": {"provider": "<id>|random"}`, or `"websearch": false` to remove the tool ([V2 websearch](https://opencode.ai/v2/docs/websearch)).
  - Source: when no provider is selected, the first call shows a form with Allow / Choose another / Disable, then persists the choice in KV ([OC2 `packages/core/src/tool/plugin/websearch.ts` L49-136](https://github.com/anomalyco/opencode/blob/0fd7e2829449b052abf0078666669302923d77af/packages/core/src/tool/plugin/websearch.ts#L49-L136)).
  - Exa runs keyless when no key is connected ([OC2 `plugin/websearch/exa.ts` L8, L45-49](https://github.com/anomalyco/opencode/blob/0fd7e2829449b052abf0078666669302923d77af/packages/core/src/plugin/websearch/exa.ts#L45-L49)). The docs table lists `EXA_API_KEY` without marking it optional; the source treats it as optional.

### OMP (oh-my-pi, `omp`)

**A. Location.**
- Root `~/.omp`, agent dir `~/.omp/agent` ([OMP `packages/utils/src/dirs.ts` L28](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/packages/utils/src/dirs.ts#L28)).
- Overrides:
  - `PI_CONFIG_DIR` renames the root.
  - `PI_CODING_AGENT_DIR` moves the default profile's agent dir.
  - `--profile`/`OMP_PROFILE` selects `~/.omp/profiles/<name>/agent`.

  Source: [docs/settings.md L13-28](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/settings.md#L13-L28).
- Optional XDG on Linux **and macOS**: data/state/cache move to `$XDG_*_HOME/omp` only when the env var is set **and** that `omp` directory already exists (`omp config init-xdg` creates it but does not move files). `config.yml` never moves ([OMP `dirs.ts` L364-420](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/packages/utils/src/dirs.ts#L364-L420)). The file header comment says "On Linux", but the code checks `linux || darwin`.
- Default is one dot-dir; splitting into XDG directories is opt-in.
- Sessions: `agent/sessions`. Credentials: `agent/agent.db` (SQLite, also holds legacy DB settings) or a remote auth broker ([OMP `dirs.ts` L911-958](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/packages/utils/src/dirs.ts#L911-L958); [docs/providers.md L63](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/providers.md#L63)).

**B. Shape.**
- Main settings: YAML `~/.omp/agent/config.yml` (`config.yaml` also accepted; legacy `settings.json` migrates to it) ([`dirs.ts` L31](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/packages/utils/src/dirs.ts#L31); [docs/settings.md](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/settings.md)).
- Separate files: `models.yml`, `mcp.json`, `secrets.yml`, `keybindings.yml`, `lsp.json` ([docs/models.md](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/models.md), [docs/mcp-config.md](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/mcp-config.md)).
- Project: `<cwd>/.omp/config.yml`. OMP also reads `.claude`, `.codex`, and `.gemini` sources ([docs/config-usage.md](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/config-usage.md)).
- The generic loader accepts YAML, JSON, and JSONC.

**C. Web.**
- Two keys in the main `config.yml`, both default `true`: `web_search.enabled` and `fetch.enabled` ("Allow the read tool to fetch and process URLs"). URL fetch goes through `read`, not a separate tool ([OMP `packages/coding-agent/src/tools/settings.ts` L700-710, L776-785](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/packages/coding-agent/src/tools/settings.ts#L700-L785)).
- `web_search` is in the `read` approval tier. The default approval mode is `yolo`, and even `always-ask` auto-approves `read`, so search never prompts by default ([docs/tools/web_search.md](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/tools/web_search.md); [docs/approval-mode.md L20-24](https://github.com/can1357/oh-my-pi/blob/c4963c0bc3233272e8087ce293c8032344835e57/docs/approval-mode.md#L20-L24)).
- Backend: a `web` model-role chain. Default order: `web/parallel` → `web/hosted` (the session model's native grounding) → exa → firecrawl → searxng → startpage → duckduckgo → ecosia → google → mojeek. Parallel, Exa, and Firecrawl have keyless paths, and docs warn the chain is "not a guarantee of zero billing".
- Configure through `modelRoles.web` and `retry.fallbackChains.web`. Legacy `providers.webSearch*` keys are auto-migrated.

### Pi (`earendil-works/pi`, formerly `badlogic/pi-mono`)

**A. Location.**
- `~/.pi/agent` on all OSes. `PI_CODING_AGENT_DIR` overrides the whole agent dir ([PI `packages/coding-agent/src/config.ts` L581-656](https://github.com/earendil-works/pi/blob/5b6c792b424e73edefbfa558b901bcd64788dad2/packages/coding-agent/src/config.ts#L581-L656)). No XDG and no Application Support.
- Everything sits in one dir: `settings.json`, `models.json`, `auth.json` (API keys and OAuth), `mcp.json`, `mcp-auth.json`, `keybindings.json`, `sessions/`, `bin/`, and a debug log.
- Sessions can be moved with `PI_CODING_AGENT_SESSION_DIR`, `--session-dir`, or `sessionDir` ([docs/configuration.md](https://github.com/earendil-works/pi/blob/5b6c792b424e73edefbfa558b901bcd64788dad2/packages/coding-agent/docs/configuration.md); [docs/sessions.md L50](https://github.com/earendil-works/pi/blob/5b6c792b424e73edefbfa558b901bcd64788dad2/packages/coding-agent/docs/sessions.md#L50)).

**B. Shape.**
- Several JSON files, one per concern: settings, models, auth, mcp, keybindings.
- Project: `.pi/settings.json` and `.pi/mcp.json`, loaded only after project trust is granted ([docs/configuration.md](https://github.com/earendil-works/pi/blob/5b6c792b424e73edefbfa558b901bcd64788dad2/packages/coding-agent/docs/configuration.md)).

**C. Web.**
- None in core. The built-in tool union is `read | bash | powershell | edit | write | grep | find | ls` ([PI `src/core/tools/index.ts` L95-104](https://github.com/earendil-works/pi/blob/5b6c792b424e73edefbfa558b901bcd64788dad2/packages/coding-agent/src/core/tools/index.ts#L95-L104)).
- A case-insensitive grep for `websearch`, `web_search`, and `webfetch` across `packages/coding-agent/src` at this commit finds nothing.
- Network access comes from shell, extensions, or MCP ([cli-workflows.md](cli-workflows.md#web-and-data-flow)).

---

## Comparison

| | Global config dir (macOS = Linux?) | Env override | Config vs data split | Main config file(s) | Secrets | Search default | Fetch default | First-use prompt |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **Codex** | `~/.codex` (same) | `CODEX_HOME`, `CODEX_SQLITE_HOME` | No (one dir) | `config.toml` (single, TOML) | `auth.json` or keyring | **On** (cached OpenAI index; live under full access) | No generic fetch tool | No |
| **Claude Code** | `~/.claude` + `~/.claude.json` (same) | `CLAUDE_CONFIG_DIR` | No | `settings.json` (+ app-written `~/.claude.json`) | Keychain (macOS) / `.credentials.json` | Available, **asks** (Manual) | Available, **asks** per domain except preapproved docs | Yes; "don't ask again" saved per repo |
| **OpenCode V1** | `~/.config/opencode` (same, XDG on macOS too) | `XDG_*`, `OPENCODE_CONFIG[_DIR/_CONTENT]` | **Yes** (`~/.local/share/opencode` data) | `opencode.json(c)` (+ `tui.json`) | `data/auth.json` | **Off** unless OpenCode provider or env flag | **On**, no prompt | No |
| **OpenCode V2** | same as V1 | same + `OPENCODE_DB` | **Yes** | `opencode.json(c)` | data-dir DB | **On**; consent + provider pick on first use | **On**, no prompt | Search only |
| **OMP** | `~/.omp/agent` (same; opt-in XDG for data/state/cache) | `PI_CONFIG_DIR`, `PI_CODING_AGENT_DIR`, `OMP_PROFILE`, `XDG_*` | Opt-in | `config.yml` (+ `models.yml`, `mcp.json`, `secrets.yml`) | `agent.db` or broker | **On** (`web_search.enabled: true`) | **On** (`fetch.enabled: true`) | No (yolo; read tier) |
| **Pi** | `~/.pi/agent` (same) | `PI_CODING_AGENT_DIR`, `PI_CODING_AGENT_SESSION_DIR` | No | `settings.json` (+ `models.json`, `mcp.json`) | `auth.json` | None | None | n/a |
| **Likha today** | `~/Library/Application Support/likha` vs `~/.config/likha` (**differs**) | `LIKHA_STATE_DIR` | No | `config.json` + `tools.json` (+ `mcp.json`) | `providers.json`, `tool-keys.json` | **Off** (absent `tools.json`) | **Off** | Yes, per backend / per origin ([web-tools spec](../web-tools/spec.md#consent-and-data-flow)) |

Likha's current location comes from Go [`os.UserConfigDir`](https://pkg.go.dev/os#UserConfigDir), which returns `$HOME/Library/Application Support` on Darwin and `$XDG_CONFIG_HOME` or `~/.config` on Unix ([internal/app/run.go L193-201](../../internal/app/run.go)).

Cross-cutting observations (facts, not recommendations):

1. **None of the five keeps user config under `~/Library/Application Support`.** Claude Code and OpenCode use `/Library/Application Support/<app>/` only for admin-managed policy.
2. Four of five use one home dot-directory that is identical on macOS and Linux (`~/.codex`, `~/.claude`, `~/.omp`, `~/.pi`). Only OpenCode uses XDG, and it applies XDG on macOS too.
3. Only OpenCode splits config from data by default. OMP offers the split as opt-in. The other three keep sessions and DBs next to config.
4. Every product that has web toggles keeps them in the **main settings file**: Codex `web_search`, OpenCode `websearch`, OMP `web_search.enabled`/`fetch.enabled`, and Claude Code `permissions` in `settings.json`. **None uses a dedicated tools file.**
5. Every product keeps secrets out of the main config: a separate JSON file, an OS keyring, or SQLite.
6. MCP placement is split. Codex and OpenCode keep it in the main file. Claude Code (`.mcp.json`/`~/.claude.json`), Pi, and OMP use a separate `mcp.json`-style file.
7. Web search is available by default in 4 of 5 (Codex, Claude Code, OpenCode V2, OMP). OpenCode V1 gates it, and Pi has none.
   - Fetch is available by default where a fetch tool exists (Claude Code, OpenCode, OMP).
   - Prompting differs. Claude Code asks for both tools. OpenCode V2 asks once for search. Codex, OpenCode fetch, and OMP never ask.

---

## Implications for Likha (recommendations, separate from findings)

### 1. Default-on for web tools

**Recommend: turn both tools on by default and keep Likha's existing first-use consent as the gate.** This matches the Claude Code and OpenCode V2 pattern: the tool is available, and the user is asked before the first use.
- Treat an absent `web` section as `search.enabled = true` (backend `duckduckgo`, keyless) and `fetch.enabled = true`.
- Keep explicit `enabled: false` as the off switch, like OpenCode V2 `"websearch": false` and OMP `web_search.enabled: false`.

Why this is safe:
- Likha's per-backend search consent and per-origin fetch consent ([web-tools spec](../web-tools/spec.md#consent-and-data-flow)) are already stricter than what Codex, OMP, and OpenCode's fetch do by default.
- Nothing goes on the network until the user approves.

Caveats:
- DuckDuckGo is an unofficial HTML endpoint, as the spec already notes. If it breaks, a default-on search fails visibly instead of silently. Keep the error explicit and never fall back to another backend, per the existing spec.
- The `explore` subagent should stay offline, per the spec.

Smaller step if the user prefers: turn **fetch** on by default (the Claude Code per-origin model) and leave search behind first-use enablement. On first search, ask "enable web search via DuckDuckGo?" and persist the answer, as OpenCode V2 persists its provider choice.

### 2. macOS config location

**Recommend: stop using `os.UserConfigDir()` on macOS. Use `$XDG_CONFIG_HOME/likha`, else `~/.config/likha`, on every Unix, macOS included (the OpenCode pattern). Keep `LIKHA_STATE_DIR` as the top override.**
- Linux users are already at this path, so only macOS migrates.
- It matches Likha's own planned paths: `~/.config/likha/skills` in [feature-comparison.md](feature-comparison.md), and the [tooling-platform user guide](../tooling-platform/user-guide.md).
- It needs one small change: replace `os.UserConfigDir()` with an explicit XDG lookup.

A dot-dir (`~/.likha`) is the more common choice (4 of 5). It is a reasonable alternative, but it would move Linux users too and give up XDG.

**Config/data split:** do not split now. Three of five keep sessions and DBs beside config, and Likha's single private 0700 directory is simpler to secure and back up. If a split is wanted later, follow OpenCode: config in `~/.config/likha`; `sessions.sqlite` and `tool-output/` in `$XDG_DATA_HOME/likha` (`~/.local/share/likha`).

**Migration concerns** (macOS only, when `LIKHA_STATE_DIR` is unset):
- Trigger: the new dir is absent and `~/Library/Application Support/likha` exists. Do a one-time `os.Rename` of the whole directory. Both paths are normally on the same volume. If they are not, copy and verify first.
- Move `sessions.sqlite` together with its `-wal`/`-shm` files, and only while no other Likha process holds the DB.
- Keep 0700 on the directory and 0600 on the secret files (`providers.json`, `tool-keys.json`).
- If both directories exist, use the new one and print a one-line warning naming the old path. Never merge them.
- Update user-facing docs that state the old path: `README.md`, `docs/usage.md`, `specs/setup-plan.md`, `specs/tooling-platform/user-guide.md`, `specs/user-agents/user-guide.md`.

### 3. Single config file vs `tools.json`

**Recommend: fold `tools.json` into `config.json` as a top-level `web` object**, i.e. `{"web":{"search":{"enabled":true,"backend":"duckduckgo"},"fetch":{"enabled":true}}}`. All four products with web toggles keep them in the main settings file.

What stays separate:
- Secrets: `providers.json` and `tool-keys.json`. Every product isolates credentials. Merging the two secret files with each other is optional and out of scope here.
- `mcp.json`: its Claude-Desktop shape matches Claude Code, Pi, and OMP.

Implementation notes:
- `config.json` is rewritten by Likha (`internal/providers/config.go`, `StoredProviderConfig`). The writer must round-trip the new `web` key, or a theme or model save will erase it.
- Back-compat: when `config.json` has no `web` key and `tools.json` exists, read `tools.json`. Then migrate it once and delete it, or keep reading it for one release.
- With recommendation 1, "absent" now means **on**, not off. Make sure a migrated `tools.json` with explicit `false` values keeps them.
