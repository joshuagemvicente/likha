```
 ____   ___  __ ___ __ __  _____
/  _/  /___\|  |  //  |  \/  _  \
|  |---|   ||  _ < |  _  ||  _  |
\_____/\___/|__|__\\__|__/\__|__/
```

<div align="center">

**A terminal coding agent for one repository at a time. Bring your own key or ChatGPT plan.**

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.24+](https://img.shields.io/badge/go-1.24%2B-00ADD8.svg)](go.mod)
![Platforms: macOS | Linux](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey.svg)
![Status: pre-release](https://img.shields.io/badge/status-pre--release-orange.svg)

[Install](#install) · [Quick start](#quick-start) · [Providers](#providers) · [Usage](#usage) · [Docs](#documentation) · [Development](#development)

</div>

Likha runs in your terminal, reads your repository, and works through it with a model you choose. It talks directly to a hosted provider with your own API key, or uses eligible shared ChatGPT Plus/Pro allowance after your explicit sign-in and consent. Likha hosts no models, needs no service of its own, and bundles no local inference.

Every file edit and every risky shell command is shown to you as an exact diff or command before it runs. Conversations are stored locally in SQLite so you can resume them.

> [!NOTE]
> **Pre-release.** No release has been published yet; build from source for now. `opencode-go` is live-verified (2026-09-29). Other providers await live probes. The official Sign in with ChatGPT migration is **in progress**; no real account/browser probe has run, and fixture tests do not establish Plus/Pro sign-in success. Many interactive surfaces await a manual walkthrough. The [CHANGELOG](CHANGELOG.md) records the verification status of each change.

## Features

| | |
| --- | --- |
| **Bring your own key or plan** | 15 predefined providers (OpenAI, Anthropic, Gemini, OpenRouter, Bedrock, Groq, xAI, Mistral, DeepSeek, and more), any OpenAI-compatible endpoint, or eligible ChatGPT Plus/Pro allowance through official browser sign-in. |
| **Review before anything changes** | Edits show a full diff and commands show the exact shell line. You approve only after scrolling through the whole proposal. Stale edits are refused rather than overwriting newer changes. |
| **Command approvals** | Read-only commands run without asking. Test, lint, and build checks can be trusted per repository. Destructive commands always ask, and catastrophic ones are refused. |
| **Read-only subagents** | The model can delegate investigation to bounded `explore` and `review` child agents, or to profiles you write in Markdown. |
| **Plan mode** | `/plan` makes the session read-only: every edit, command, and MCP call is refused until you turn it off. |
| **MCP servers** | Use stdio MCP servers with a Claude Desktop–style `mcp.json`. Each server needs your approval the first time it is used. |
| **Web tools (opt-in)** | `web_fetch` for public HTTPS pages and `web_search` (keyless DuckDuckGo, or Brave, Tavily, or Exa with a key), each needing consent before first use. |
| **Skills** | Global Markdown instruction files that you load into a turn with `/skill <name>`. |
| **Resumable sessions** | Local SQLite history per repository, auto-generated session names, and `/compact` to summarize long conversations. |
| **Spend and context tracking** | The status line shows context usage, tokens, and session spend from provider-reported costs or a bundled price catalog. It never guesses a price for an unknown model. |
| **A polished TUI** | Markdown and syntax-highlighted answers, inline diffs, collapsible reasoning, smooth scrolling, built-in copy, 22 themes, and Nerd Font or plain-ASCII glyph modes. |

## Install

**Requirements:** macOS or Linux, an interactive terminal, and an API key for a [supported provider](#providers) or eligible ChatGPT Plus/Pro access with browser consent. ChatGPT sign-in needs a browser that can reach the local callback on the machine running Likha. Building from source needs Go 1.24 or later.

### From source (current path)

```sh
git clone https://github.com/joshuagemvicente/likha.git
cd likha
go build -o bin/likha ./cmd/likha
./bin/likha --version
```

You can also skip the build and run it with `go run ./cmd/likha` from the project root. Put `bin/likha` on your `PATH` to run `likha` from any repository.

### Install script (once a release is published)

```sh
curl -fsSL https://raw.githubusercontent.com/gem/likha/main/scripts/install.sh | sh
```

The script detects your OS and architecture, checks the archive against its SHA-256 manifest, and installs to `~/.local/bin/likha` without sudo. To pin a version, append `| sh -s -- v1.2.3`. For overrides, local release archives, and what the checksum does and does not prove, see [docs/install.md](docs/install.md).

## Quick start

```sh
cd ~/projects/my-app
likha
```

On the first launch, pick a **provider**, connect, choose a **model**, and pick a **theme**. API-key providers ask for a masked key and check it. Selecting **ChatGPT** opens your system browser automatically for OpenAI sign-in and plan-sharing consent, then fetches eligible models for that account. No code or second browser-launch keypress is needed; Likha shows a manual URL only if browser launch fails. Your choice is saved for later launches.

To skip setup, pass a provider and key on the command line or set them in the environment:

```sh
export LIKHA_OPENAI_API_KEY="sk-..."
likha --provider openai ~/projects/my-app

# ChatGPT Plus/Pro: the same browser sign-in outside the TUI, then launch
likha --provider chatgpt --login
likha --provider chatgpt ~/projects/my-app
```

ChatGPT uses shared plan allowance, not unlimited or free inference. Optional
credits follow your explicit opt-in in [ChatGPT settings](https://chatgpt.com/settings/usage).
Likha does not fall back to separately billed OpenAI API-key use. Legacy Codex
logins need a fresh sign-in; `--device-login` is deprecated and unsupported.

Then ask for something, such as *"find where we parse the config and add a timeout option"*, and press **Enter**. Type `@` to attach a file or folder, `/` to see the slash commands, and press **Ctrl+D** to exit.

## Providers

| Provider | `--provider` | Key variable | Default model | Probe status |
| --- | --- | --- | --- | --- |
| OpenAI | `openai` | `LIKHA_OPENAI_API_KEY` | `gpt-4o-mini` | pending-probe |
| Anthropic Claude | `claude` | `LIKHA_CLAUDE_API_KEY` | `claude-opus-5-5` | pending-probe |
| Google Gemini | `gemini` | `LIKHA_GEMINI_API_KEY` | `gemini-2.5-flash` | pending-probe |
| ChatGPT (Plus/Pro) | `chatgpt` | none — browser sign-in | from eligible account models | pending-probe |
| OpenRouter | `openrouter` | `LIKHA_OPENROUTER_API_KEY` | `openai/gpt-4o-mini` | pending-probe |
| Amazon Bedrock | `bedrock` | `LIKHA_BEDROCK_API_KEY` | `anthropic.claude-3-5-haiku-20241022-v1:0` | pending-probe |
| Opencode Go | `opencode-go` | `LIKHA_OPENCODEGO_API_KEY` | `glm-5.3-flash` | live-verified* |
| Opencode Zen | `opencode-zen` | `LIKHA_OPENCODE_ZEN_API_KEY` | `gpt-5.3-codex` | pending-probe |
| DeepSeek | `deepseek` | `LIKHA_DEEPSEEK_API_KEY` | `deepseek-flash` | pending-probe |
| Groq | `groq` | `LIKHA_GROQ_API_KEY` | pass `--model` | pending-probe |
| xAI | `xai` | `LIKHA_XAI_API_KEY` | pass `--model` | pending-probe |
| Mistral AI | `mistral` | `LIKHA_MISTRAL_API_KEY` | pass `--model` | pending-probe |
| Together AI | `together` | `LIKHA_TOGETHER_API_KEY` | pass `--model` | pending-probe |
| Cerebras | `cerebras` | `LIKHA_CEREBRAS_API_KEY` | pass `--model` | pending-probe |
| Dialagram | `dialagram` | `LIKHA_DIALAGRAM_API_KEY` | pass `--model` | pending-probe |

\* A user-run probe passed on 2026-09-29; repeat it after provider-facing request changes. *Pending-probe* means the provider has not yet passed Likha's full live check (streamed text, structured tool calls, cancellation, and revoked-key handling). The model must support SSE streaming **and structured tool calls**.

For API-key providers, Likha looks for a key in this order: `--api-key`, `LIKHA_API_KEY`, the provider's own `LIKHA_<NAME>_API_KEY`, and finally the private `providers.json`. ChatGPT uses its own saved account/workspace registration and tokens instead. Both model discovery and ChatGPT Responses requests use the documented public `https://api.openai.com/v1` API; OpenAI API-key billing remains separate. Use `--endpoint URL` for any other OpenAI-compatible endpoint; it runs with a visible **unverified** marker. See [docs/providers.md](docs/providers.md) for setup, account management, sign-out, and [SIWC terms](https://openai.com/policies/sign-in-with-chatgpt-terms/).

## Usage

```text
likha [options] [repository]       # repository defaults to the current directory
likha --sessions [repository]      # list saved sessions (no model needed)
likha --resume ID [repository]     # resume one
```

Put options before the repository path. `likha --help` lists every option, provider, and environment variable.

### Keys

| Key | Action |
| --- | --- |
| **Enter** | Send the prompt. During a run, it queues the prompt as a steering message instead. |
| **Ctrl+Enter** | During a run, steer now: stop the response being written and deliver the draft (and any queued messages) at once. Shift+Enter and Ctrl+J do the same, since terminals send them as one key. |
| **Alt+Return** / **Ctrl+J** | Insert a newline (while idle; during a run, use Alt+Return). |
| **Esc** / **Ctrl+C** | Cancel the active run (Esc clears a selection first). |
| **Ctrl+D** | Exit. |
| **PgUp/PgDn**, **Home/End**, mouse wheel | Scroll the transcript. |
| **Tab** / **Shift+Tab** | Focus a tool call or reasoning block. |
| **Enter** / **Ctrl+O** on a focused item | Inspect full tool output, or expand reasoning. |
| Mouse drag, then **Alt+C** | Copy highlighted text to the clipboard. |
| **Ctrl+G** | Change the composer style. |
| **Ctrl+W, Ctrl+U/K/Y, Alt+B/F, …** | Readline-style editing. |

### Slash commands

| Command | Action |
| --- | --- |
| `/models` | Switch the provider and model for this session. |
| `/providers` | Manage stored keys or ChatGPT accounts; an unconnected ChatGPT selection opens the browser. |
| `/sessions [n]` | List, resume, or delete (**Ctrl+D**) saved sessions. |
| `/compact [focus]` | Summarize the conversation so far into a brief and continue from it. |
| `/plan` | Toggle read-only plan mode. |
| `/init [guidance]` | Survey the repository and propose a root `AGENTS.md` for review. |
| `/install <request>` | Interview you about an install, show a plan, then run each step with approval. |
| `/todo` | Show the model's plan checklist. |
| `/agents` | Inspect subagent profiles, the task tree, and child transcripts. |
| `/tools` | Inspect available tools, their permissions, and retained output. |
| `/mcp` | Show MCP servers and their tools. |
| `/skills`, `/skill <name> [request]` | List skills, or load one into a turn. |
| `/themes [name]` | Preview and pick a color theme. |
| `/help`, `/quit` | List commands, or exit. |

Slash commands are never sent to the model. Start a prompt with `//` to send a literal slash. [docs/usage.md](docs/usage.md) covers the full interface, including the transcript, status line segments, file references, compaction, themes, and copy and paste.

## Command approvals

Likha sorts every shell command the model requests before deciding whether to ask you:

| Class | Examples | What happens |
| --- | --- | --- |
| **Refused** | `rm -rf /`, `mkfs`, `dd` onto a device, `curl … \| sh` | It never runs. The model is told you can run it yourself. |
| **Always ask** | `rm`, `git push`, `git reset --hard`, `--force`, `sudo`, deploys, secrets, paths outside the repository | Only **Approve** or **Decline** is offered. |
| **Ask** | Installs, scripts, `mv`/`cp`, redirects, `git commit`, network tools | **Approve always** is also offered: it covers the command's prefix (`go test …`, `npm run lint …`) for the session, or the exact string for interpreters, executors like `npx`, and network tools. |
| **Checks** | `go test`, `npm test`, `cargo check`, `pytest`, `make lint`, … | **Trust repo checks** is also offered; trust is reset if those scripts change. |
| **Read-only** | `git status`, `git diff`, `ls`, `cat`, `<tool> --version` | Runs without asking. |

Only a single plain command can skip the prompt. Chains, pipes, substitutions, and redirects always ask. Edit and MCP reviews offer **Approve always** too: for an edit, every later repository edit in the session applies without a review (its diff still shows in the transcript, marked `auto-approved`, and the status line shows `AUTO-EDIT`); for an MCP tool, plain **Approve** runs that one call and **Approve always** trusts the server for the session. Every Approve always grant lives in memory only and ends when you switch or delete the session or relaunch Likha. **Approval is not a sandbox:** an approved command can reach outside the repository and the network. Every command runs with Likha's API key variables removed from its environment. See [docs/tools.md](docs/tools.md) for the tool set, subagents, MCP, and web tools.

## Configuration and data

Everything Likha stores lives in a private state directory with user-only permissions. Nothing is written to your repository. The default location is `$XDG_CONFIG_HOME/likha` (or `~/.config/likha`) on macOS and Linux; set `LIKHA_STATE_DIR` to override it. Older macOS builds used `~/Library/Application Support/likha`; Likha moves that directory to the new location on first launch (if both exist, it uses the new one and leaves the old one alone).

| File | Contents |
| --- | --- |
| `config.json` | Provider, model, theme, composer style, status line segments, command trust, web tool settings (`web`) |
| `providers.json` | API keys, separate ChatGPT registrations/tokens, and active account choice (mode 0600) |
| `sessions.sqlite` | Conversation history — treat it, and any backups, as sensitive |
| `mcp.json` | MCP server definitions |
| `tool-keys.json` | Keys for keyed web search backends (Brave, Tavily, Exa) |
| `agents/<name>/AGENT.md` | Your subagent profiles |
| `skills/<name>/SKILL.md` | Your skills |

| Environment variable | Purpose |
| --- | --- |
| `LIKHA_PROVIDER`, `LIKHA_MODEL`, `LIKHA_ENDPOINT` | Same as `--provider`, `--model`, `--endpoint` |
| `LIKHA_API_KEY`, `LIKHA_<NAME>_API_KEY` | API keys |
| `LIKHA_STATE_DIR` | Private state directory |
| `LIKHA_THEME`, `LIKHA_ASCII=1`, `LIKHA_NERD=1` | Theme and glyph set |
| `LIKHA_DEBUG_MODELS=1` | Log model IDs and context metadata to `model-metadata.log` |
| `LIKHA_UPDATE_CHECK=0` | Disable the daily update check |

**Privacy:** prompts, conversation history, and tool results go to the provider you configure, and that provider's privacy policy applies. Likha identifies itself with `User-Agent: likha/<version>`.

ChatGPT credentials stay in protected local files with atomic writes and locked
token rotation. Host identity and registrations survive sign-out; Likha attempts
remote revocation and clears local tokens, warning if remote revocation was not
confirmed. ChatGPT usage/cost remain unknown unless OpenAI reports them. See the
[sign-in guide](docs/providers.md#sign-in-with-chatgpt-pluspro) before migrating.

## Documentation

| Guide | Covers |
| --- | --- |
| [Providers and sign-in](docs/providers.md) | Provider table, key resolution, first-run setup, official ChatGPT browser sign-in, accounts, allowance, and sign-out |
| [Using Likha](docs/usage.md) | Transcript, composer and editing keys, selection and copy, status line, spend, slash commands, compaction, `@` references, themes, sessions |
| [Tools and permissions](docs/tools.md) | Repository tools, edit and command reviews, command approvals, subagents, plan mode, skills, web tools, MCP |
| [Installing and releasing](docs/install.md) | Install script, local release archives, checksum verification, the manual release walkthrough |
| [Architecture](ARCHITECTURE.md) | Package map, import rules, and where each kind of change goes |
| [Specifications](specs/README.md) | The [v1 product contract](specs/v1-spec.md) and one folder per feature |
| [Changelog](CHANGELOG.md) | Unreleased changes and what has been verified |

## Development

```sh
go build -o bin/likha ./cmd/likha   # build
go test ./...                       # the full test suite
go vet ./...
go run ./cmd/likha --debug-models . # run against this repository, logging model metadata
```

| Path | What lives there |
| --- | --- |
| `cmd/likha` | Entry point (calls `app.Run`) |
| `internal/app` | CLI flags and wiring |
| `internal/tui` | The Bubble Tea interface: setup, dialogs, status line, composer |
| `internal/agent` | The turn loop, tool registry, compaction, session naming |
| `internal/providers`, `internal/model` | Provider identity, credentials, the streaming client, pricing |
| `internal/model/catalog` | Bundled model prices and context windows (`go generate ./internal/model/catalog`) |
| `internal/actions`, `internal/cmdpolicy` | Edit and command execution, command classification |
| `internal/mcp`, `internal/webtools`, `internal/explore` | MCP client, web tools, subagents |
| `specs/` | Feature specs, tasks, and checklists |
| `scripts/` | `install.sh`, `release.sh`, `smoke-release.sh` |

[ARCHITECTURE.md](ARCHITECTURE.md) has the dependency diagram and import rules. Release archives are built with `./scripts/release.sh v1.2.3`, which writes them to `dist/` and does not publish anything; see [docs/install.md](docs/install.md#build-verify-and-install-a-local-archive).

### Contributing

Likha is developed spec-first. Before you change behavior:

1. Find or create the feature folder under [`specs/`](specs/README.md) (`spec.md`, `tasks.md`, `checklist.md`, `context.md`). The [v1 spec](specs/v1-spec.md) is the overarching contract.
2. Keep `go test ./...` passing. Skipped, always-passing, or mock-only tests can't be used to claim a feature works.
3. Add an entry under **Unreleased** in [CHANGELOG.md](CHANGELOG.md), and state honestly what was verified: automated checks, a headless probe, a live probe, or an interactive walkthrough.
4. Report status using only the words in the spec conventions: *planned*, *in progress*, *implemented (local)*, *verified (release)*.

## License

[MIT](LICENSE) © 2026 Likha contributors
