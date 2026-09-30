# Lisa

Lisa is a terminal coding agent for one repository at a time. It streams responses from a hosted model provider on the predefined accepted list, using your own API key (BYOK) — Lisa hosts no models and bundles no local inference server. It reads repository files and asks permission before editing a file or running a shell command. Conversations are stored in local SQLite so you can resume them. The full-screen interface scrolls its transcript continuously, like a browser, rather than using terminal scrollback or fixed pages.

**Release status:** Local scripts build macOS and Linux archives; no release has been published. A local macOS ARM64 archive passed the documented checksum/install checks and an interactive workflow against a protocol server. `opencode-go` is **live-verified** (user session on 2026-09-29: setup flow, connection check, streaming, and agent tool usage with a real key). `opencode-zen` shares the same verified surface and session header but awaits a live-key turn; the other hosted providers (OpenAI, OpenRouter, Amazon Bedrock, Dialagram) and the new ChatGPT sign-in remain wired but unprobed — a live probe with a real ChatGPT Plus account has not run yet. See the [v1 specification](specs/v1-spec.md), [feature test plan](specs/feature-test-plan.md), [setup plan](specs/setup-plan.md), and [unreleased changes](CHANGELOG.md).

## Requirements and model providers

- Go 1.24 or later to build from source or produce local release archives; macOS or Linux. A downloaded, prebuilt archive does not require Go.
- An interactive terminal (Lisa does not run its TUI through a pipe).
- A hosted provider from the predefined accepted list and your API key. The model must support SSE streaming **and structured tool calls**. Lisa hosts no models and bundles no local inference server.

### Predefined accepted providers

| Provider | `--provider` name | API key | Base URL (default) | Default model |
| --- | --- | --- | --- | --- |
| OpenAI | `openai` | `LISA_OPENAI_API_KEY` | `https://api.openai.com/v1` | `gpt-4o-mini` |
| OpenRouter | `openrouter` | `LISA_OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1` | `openai/gpt-4o-mini` |
| Amazon Bedrock | `bedrock` | `LISA_BEDROCK_API_KEY` | `https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1` (other regions via `--endpoint`) | `anthropic.claude-3-5-haiku-20241022-v1:0` |
| Dialagram | `dialagram` | `LISA_DIALAGRAM_API_KEY` | `https://dialagram.me/router/v1` | none — pass `--model` |
| Opencode Zen | `opencode-zen` | `LISA_OPENCODE_ZEN_API_KEY` | `https://opencode.ai/zen/v1` | `gpt-5.3-codex` |
| Opencode Go | `opencode-go` | `LISA_OPENCODEGO_API_KEY` | `https://opencode.ai/zen/go/v1` | `glm-5.3-flash` |
| ChatGPT (Plus/Pro) | `chatgpt` | none — ChatGPT browser sign-in | `https://chatgpt.com/backend-api/codex` | `gpt-5.5` |

Key resolution order: `--api-key`, then `LISA_API_KEY`, then the provider's own `LISA_<NAME>_API_KEY`, then the private state directory (`providers.json`, mode 0600). Passing `--api-key` once stores the key for later runs. Keys are never read from the selected repository and never stored in the session database. `chatgpt` is the exception: it has no API key and signs in through your browser instead — see [Sign in with ChatGPT (Plus/Pro)](#sign-in-with-chatgpt-pluspro). Endpoint URLs may not contain credentials, query strings, or fragments; plain HTTP is accepted only for loopback hosts.

Lisa checks the connection when it starts and again before each agent run; a dead or revoked key is reported before any prompt is sent. An endpoint outside the list runs with a visible **unverified** warning in the interface. `opencode-go` is live-verified; the remaining providers, including ChatGPT, are **not yet probed** against their live services — see `specs/predefined-providers/`.

Lisa identifies itself to providers with `User-Agent: lisa/<version>`. The Opencode providers additionally receive your stable conversation ID in `x-opencode-session`, which they require for routing and prompt caching. ChatGPT requests go to the OpenAI Responses wire at `<base>/responses` with a `ChatGPT-Account-Id` header and the same stable conversation ID in `session-id`.

Run without a model name: Lisa uses the provider's documented default (listed above), or — for an endpoint outside the list — the first model that endpoint reports. Override with `LISA_MODEL` or `--model`. `lisa --help` prints every option, provider, environment variable, and example.

### First-run setup (no flags needed)

Run `lisa` with no provider configured and the TUI walks you through setup:

1. **Choose a provider** — ↑/↓ and Enter over the predefined list.
2. **Paste your API key** — masked input; the key is checked against the provider before anything else happens. Selecting `chatgpt` skips this stage entirely: instead of a key, Lisa shows a "Press Enter to open the browser" login stage that signs in through your ChatGPT account.
3. **Choose a model** — picked from the models the provider reports; a single model auto-selects. ChatGPT has no model-list route, so setup offers its curated list (`gpt-5.5`, the default, first).

The choice is stored in the private state directory (`config.json`, `providers.json`, both mode 0600), so the next launch goes straight to the conversation. Explicit flags or environment variables skip setup and override the stored choice. On a non-interactive terminal, unconfigured invocations fail with a clear error instead of starting setup.

For example, using the stored key from setup:

```sh
go run ./cmd/lisa /path/to/repository
```

Or explicitly for one run:

```sh
export LISA_API_KEY="sk-..."
go run ./cmd/lisa --provider opencode-go /path/to/repository
```

A stored configuration is used on every launch. `--model` and `--endpoint` override the stored choice and the provider defaults. A provider outside the list cannot be selected with `--provider`; use `--endpoint` to point at another OpenAI-compatible base URL (runs unverified). The provider must emit structured tool calls, not just write tool instructions as prose. Lisa reports connection, revoked-key, or incompatible-protocol errors in the conversation and keeps the UI open.

Omit the repository argument to use the current directory. Put options before the repository path, for example `go run ./cmd/lisa --provider opencode-go /path/to/repository`. `--help` prints usage and `--version` prints the build version without entering the TUI.

### Sign in with ChatGPT (Plus/Pro)

The `chatgpt` provider is keyless: it uses your ChatGPT subscription through OpenAI's Codex backend instead of an API key. First-run setup shows it as "ChatGPT (Plus/Pro)" and selecting it never asks for a key.

**Browser sign-in (interactive).** After choosing ChatGPT, setup shows a **Press Enter to open the browser** stage. Lisa starts an OAuth 2 loopback listener on `http://localhost:1455` (OpenAI's public Codex CLI PKCE client; the login screen identifies it as such), opens your browser to sign in with your ChatGPT account, and completes the flow from the callback. After sign-in the model stage offers the curated ChatGPT list — `gpt-5.5` (the default), `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.3-codex-spark`, `gpt-6-sol`, `gpt-6-luna` — and no model is fetched from the backend. No API key is ever asked for or entered.

**Headless sign-in (no TTY):** `lisa --provider chatgpt --device-login` runs the device-code flow without an interactive terminal or a repository. It prints the verification URL (`https://auth.openai.com/codex/device`) and a code, waits for you to approve it in a browser elsewhere, then stores the login — the whole wait is bounded at ten minutes and Ctrl+C aborts:

```sh
go run ./cmd/lisa --provider chatgpt --device-login
```

**What is stored:** the login's refresh token, access token, expiry, and ChatGPT account ID persist only in the private state directory's `providers.json` (mode 0600) under the `chatgpt` row — never in the repository or the session database. The file is schema v2, where every entry is a typed object (`"api"` key or `"oauth"` login); files written by older versions hold a plain map of API keys and are migrated in memory on read.

**Requests and refreshes:** conversation turns go to the OpenAI Responses wire at `https://chatgpt.com/backend-api/codex/responses` with a `ChatGPT-Account-Id` header and the current conversation's ID in `session-id`. Access tokens expire; Lisa refreshes them automatically (concurrent turns share one in-flight refresh) and re-persists the fresh token set. If a refresh fails, Lisa reports "chatgpt login expired; sign in again" — the fix is another browser sign-in or `--device-login`, never a new key.

**Usage display:** when the backend reports it, the main view's footer shows your plan's five-hour rate-window usage (used percent and window from the response's `x-codex-primary-*` headers). ChatGPT plans' usage windows — five hours and seven days — apply to Lisa turns exactly as they do in ChatGPT.

**Privacy and terms:** conversation content goes to OpenAI's ChatGPT consumer backend, so its privacy policy governs what it receives. The browser login screen shows OpenAI's public Codex CLI client, not a Lisa-issued one. Using a ChatGPT subscription through a third-party harness is subject to OpenAI's consumer terms. Because your plan pays, there is no per-token cost: the status bar's spend segment renders a known `$0.00` on ChatGPT models rather than hiding.

Note that ChatGPT sign-in ships pending a live probe: Lisa's implementation is tested locally against fixtures, but a live probe with a real ChatGPT Plus account has not run yet, so treat the provider as not yet live-verified.

## Work in the terminal

Lisa opens with no top header at all on terminals at least 56 columns wide — the ASCII logo renders once as the first transcript block and there is no additional chrome — so identity (repository, branch, provider, model) is carried by the status bar instead. Fresh sessions show the logo where it scrolls away naturally as the conversation grows; resumed sessions never redraw or persist it. On narrower terminals the logo block is skipped and a single compact line, `Lisa · <repository basename>`, is the only header, so identity survives on the smallest screens.

The alternate-screen UI shows the repository, model, status, and scroll position. It does not scroll the terminal or a pane. Type a request and press **Enter**. The transcript scrolls **continuously, like a browser**: the **mouse wheel** moves the view a few lines per notch (not a page at a time), **PgUp/PgDn** (or **Ctrl+P/Ctrl+N**) step one viewport, **Home** jumps to the very top, and **End** jumps back to the newest content. A **scrollbar on the right edge** shows the position — click or drag it to move anywhere. The draft stays in the composer while you browse. While the view sits at the bottom it follows new content like a chat; scrolling up anchors it until you return. Resize to at least 40 columns by 12 rows if prompted; unsafe-size screens cannot accept a prompt or approve a review. **Ctrl+C** or **Esc** cancels an active run and drains completed tool results into the session before showing cancellation; **Ctrl+C** while idle or **Ctrl+D** exits. The terminal screen is restored on exit.

Conversation text wraps to a **content width**: the viewport minus two padding columns, capped at 120 columns on terminals wider than 140. On an ultrawide monitor lines keep a readable measure instead of stretching edge to edge, so no horizontal scrolling is ever needed — every rendered path (transcript, reviews, dialogs, composer, popups) is pinned by tests to stay within the viewport, and resizing re-wraps the transcript immediately. The status bar, composer, and popups still use the full terminal width.

The composer defaults to `minimal` (a rule above the input). While idle, press **Ctrl+G** to choose `bordered` (a rounded box), `borderless` (just the input), or `chatter` (an inline `You ›` prefix). **↑/↓** navigate, typing filters, **Enter** applies, and **Esc** cancels. The choice is stored as `composer.style` in the private `config.json` and restored on launch. At 40 columns, `bordered` and `chatter` render as `minimal` without changing the stored choice. Review decisions and page navigation stay in the status line beneath the composer.

### Editing keys

The draft is a readline-style editor with a visible block caret. Word keys treat a run of spaces, tabs, or newlines as one boundary:

- **Ctrl+W / Ctrl+Backspace / Alt+Backspace** delete the previous word together with the spaces in front of it; **Ctrl+Delete** and **Alt+D** delete the next word (Ctrl+Delete needs a terminal that sends the chord — most don't, Alt+D always works).
- **Alt+B / Alt+F** move the caret one word back/forward.
- **Ctrl+U** kills to the start of the draft, **Ctrl+K** to the end, and **Ctrl+Y** yanks the most recent kill (a repeat cycles through the last eight kills); an edit ends the cycle.
- **Ctrl+T** transposes the two characters before the caret.
- **Alt+Return** (or **Ctrl+Return / Shift+Return** where the terminal sends them; **Ctrl+J** always works) inserts a newline into the draft. Bare **Enter** still sends, flattening the newlines to spaces; pasted text (bracketed paste) inserts at the caret with newlines flattened too.
- **Esc** clears an idle draft as always — but a Return arriving right after Esc (within ~50 ms) is the Alt+Return encoding and inserts a newline instead.

Editing is inert while a turn streams or a review is pending, and the transcript styles render tool activity and model reasoning in the theme's muted role.

### Status line

The status line renders as one row wide and two rows below 70 columns so review keys and page navigation remain visible. It opens with the active provider · model (the model shown under its display name — `GPT-4o`, not `gpt-4o` — where the curated map has one, the slug otherwise), context usage, then the enabled optional segments in the order folder, branch, ahead/behind arrows, changed and staged file counts, untracked count, MCP servers, session, environment (`macOS arm64`), spend, minutes, tokens, version, and update. A small `Lisa` mark closes the row at the far right end at width ≥ 70, and it is the first thing dropped under width pressure (the page controls and mode hints never lose their right-hand slot).

**Measured usage:** after each completed run, Lisa captures the provider's final token usage from the response (`prompt_tokens`/`completion_tokens`, with the `input`/`output` aliases accepted) and shows `ctx <n>% · <used>/<window>` — the last response's prompt tokens as a share and the used/total pair against the model's documented context window, with a visible warning past 80%. Narrow (two-row) layouts drop the used/total pair and keep the bare `ctx <n>%`; `ctx —` remains whenever usage has not been measured or the model's context window is not publicly documented (for example ChatGPT's curated `gpt-5.x` models) — a percentage is never fabricated without a real window. With `tokens` enabled, `tokens <n>` shows the cumulative prompt+completion count across completed turns; with `minutes`, `minutes <n>m` shows the elapsed session time. The ChatGPT plan's rate-window summary stays a separate display and is never labeled `ctx`.

**Session spend:** once a completed turn carries cost the bar accumulates it as `$0.42` (two decimals always). The amount comes from the provider when its response reports cost, else from a curated per-million-token price table maintained beside the context-window table; the ChatGPT subscription models render the known `$0.00` (your plan pays, not tokens). A model with no documented pricing keeps the segment hidden — no cost is ever estimated.

Two optional segments are on by default now: `folder` (working repository path with the home directory abbreviated as `~`) and `branch` (current Git branch, hidden outside one or outside a git repository). The remaining optional segments are off by default. To enable them on the next launch, add `"status_line": {"changes": true, "staged": true, "mcp": true, "session": true, "minutes": true, "tokens": true, "version": true, "update": true}` to the existing private `config.json` beside your stored provider/model settings; set individual fields to `false` or omit them to hide them (an absent `folder`/`branch` key means on; an explicit `false` turns them off). One bounded 3-second `git status --porcelain -b` — read at session start and refreshed after each tool result — feeds all git segments: `branch`, a compact `↓<n>↑<m>` ahead/behind-vs-upstream pair (hidden when in sync or with no upstream; the short SHA renders when HEAD is detached), `changes` and `staged` dirty and staged file counts (a zero count hides the segment), and an untracked-file count; any git failure silently hides the git segments. `mcp` shows a condensed `mcp <running>/<configured>` count of configured servers and hides itself when none are configured; `session` shows the session title — a short model-generated name once the first turn completes (see below), or the first user prompt before it exists; the environment segment shows the operating system and architecture (e.g. `macOS arm64`) and is not separately gated; `version` shows the build version.

With `update` enabled, the line also shows `update → vX` when the throttled startup check finds a newer published release. The check runs at most once per 24 hours per state directory (`update_check.json`, mode 0600), serves silently on every failure, never notifies development builds, can be pointed at another endpoint with `LISA_UPDATE_API`, and can be turned off entirely with `LISA_UPDATE_CHECK=0`.

### Slash commands

A prompt starting with `/` is a command, acting on the application and **never sent to the model**:

| Command | Action |
| --- | --- |
| `/sessions` | List saved sessions for the repository (newest first). |
| `/sessions <n>` | Resume that session in place — no relaunch needed. |
| `/models` | Open a dialog of the provider's models; ↑/↓ navigate, Enter applies. |
| `/providers` | Open a dialog to switch the provider for this session (not-configured rows prompt for the API key inline; stored config untouched). The keyless `chatgpt` row cannot be switched to mid-session — sign in through first-run setup or `--device-login` instead. |
| `/compact [focus]` | Summarize the conversation so far into one compact brief and continue from it — the summarized turns are replaced, the session stays resumable. |
| `/quit` | Exit, same as Ctrl+D. |
| `/help` | Print the command list in the conversation. |

Unknown `/commands` show an error and are never sent to the model; the draft is restored. To send a literal slash to the model, start with two: `//what is /quit`. Commands do not interrupt an active run or a pending review.

**Command popup.** Typing `/` at the start of the prompt opens a popup listing the reserved commands with one-line descriptions; keep typing to filter (case-insensitive substring match), **↑/↓** to move, **Tab** to complete, and **Esc** to dismiss. **Enter** completes the highlighted command unless the draft is already an exact command name — a fully typed `/compact` plus Enter sends it, while `/comp` plus Enter completes to `/compact ` first. The popup closes the moment a space is typed (arguments begin) and never coexists with the `@` file popup; both popups show the hint `↑/↓ select  Tab complete  Esc dismiss`.

### Conversation compaction (`/compact`)

Long sessions resend their entire history to the provider every turn, and eventually that exceeds the model's context window. `/compact` is the escape hatch: Lisa makes **one** summarize model call over the conversation so far — preserving your goal, the decisions made, files changed and how, pending next steps, and open questions — and replaces the summarized turns with that brief, so the task continues without the transcript. Anything after `/compact` (for example `/compact focus on the approval flow refactor`) steers what the summary emphasizes without being stored as conversation content.

The compacted session shows a visible marker entry ("Conversation compacted. Summary of earlier turns:") and the summary stays fully readable through normal paging; the brief travels as the conversation context on every later turn, and the session persists and resumes in its compacted state. `/compact` is **manual-only** — there is no automatic compaction near the context limit. It is refused with a visible message while a review is pending, on an empty session ("Nothing to compact yet."), and without a configured model; a failing or cancelled summarize (Esc cancels it) leaves the history untouched with a visible error, and you can retry.

### File references (`@`)

Type `@` in the prompt to open a file popup listing the repository's files **and folders**; keep typing to filter, ↑/↓ to move, **Enter** or **Tab** to complete, **Esc** to close. An `@path` reference in a submitted prompt — for example `explain @internal/app/agent.go` — inlines that file's content into what the model receives, so it works with the context immediately instead of spending tool rounds reading it. A **folder reference** (`@src`, `@internal/`) inlines the folder's tree instead: one path per line, so the model sees structure and reads individual files on demand.

The listing and the popup **never include** paths excluded by the repository's `.gitignore` files (root and per-directory rules are honored), `node_modules`, hidden directories like `.git`, or symlinks. Unresolvable references stay literal so typos are visible. Referenced content is per-turn (not persisted in the session); files are capped at 16 / 64 KiB per prompt and folder listings at 200 entries — larger files should still go through the read tool.

### Themes

Lisa ships a small set of named color themes that map onto its style roles (headings, cursor, hints, warnings, errors) and adapt to your terminal's light or dark background automatically:

`default` · `catppuccin` · `habamax` · `gruvbox` · `tokyonight` · `nord` · `dracula` · `solarized` · `rose-pine` · `kanagawa` · `everforest`

Pick one four ways:

- **First-run setup** offers a theme stage right after model selection.
- **`/themes`** opens a selection dialog: **↑/↓** move the highlight, **Enter** applies the highlighted theme and stores it in `config.json`, **Esc** closes without changing anything. The dialog opens on the currently applied theme.
- **`/themes <n-or-name>`** applies directly without the dialog.
- **`--theme <name>` or `LISA_THEME`** pre-selects it at launch (explicit settings beat stored config).

Conversation prose stays uncolored except error entries — the visual-restraint rule holds in every theme.

### Nerd Font markers (opt-in)

If your terminal uses a patched **Nerd Font**, run Lisa with `--nerd-fonts` or `LISA_NERD=1` and the status/review markers become icon glyphs (clock while waiting, check when done, cross on errors, pencil during reviews). Lisa cannot detect a patched font reliably, so this is strictly opt-in: without it, output stays plain ASCII and always legible. Conversation prose and the logo are never replaced by icons.

### Repository tools and reviews

Lisa offers `list_files`, `read_file`, and literal `search_files` for repository text. These tools reject traversal and symlink escapes; size and result limits can return errors. To request a change, try: “Use edit_file to change notes.txt from old to new.” The model must actually request the tool. `edit_file` proposes replacement content or a new file and shows the affected path and a diff **before** writing. Review every page with PgDn/Ctrl+N (and PgUp/Ctrl+P to revisit); **Y** approves that proposal only after every page has been viewed, while **N** rejects immediately. If the file changes after review starts, the stale edit is refused rather than overwriting it.

### MCP servers

Lisa can use tools from **MCP servers** (Model Context Protocol) you configure — the same ecosystem OpenCode/Claude Desktop use. Configure them in `mcp.json` inside Lisa's private state directory (`~/.config/lisa/` on Linux, `~/Library/Application Support/lisa/` on macOS), same shape as Claude Desktop so configs can be pasted verbatim:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/some/dir"],
      "env": { "SOME_KEY": "value" }
    }
  }
}
```

- **stdio only for v1** — each server is launched as a child process, handshaken (MCP 2025-03-26), and killed when Lisa exits. HTTP transports are deferred.
- **Trust-on-first-use per server:** the first tool call from a server shows the server name, tool, and arguments, and requires explicit approval; once approved, that server's tools run for the rest of the session. Trust resets on relaunch.
- A crashed server produces a clear failed tool result and stays dead until relaunch (see `/mcp`); the TUI stays usable.
- **`/mcp`** lists configured servers, their state, and their tools.
- Invalid `mcp.json` fails loudly at startup; the file is 0600 in private state, never in the repository.

To check work, try: “Use run_command to run `git status --short`.” The review shows the **exact shell command** and canonical repository working directory. Commands containing invisible or control characters, including tabs and line breaks, are rejected before review; control characters in other review text are escaped visibly. Again, **Y** approves only after all review pages have been viewed; **N** rejects without execution. An approved command runs via `sh -c` in that directory; its output and exit status appear in the conversation (long output is paged, and output above the command limit is marked truncated). **The working directory is not a sandbox. An approved command can read or change files outside the repository, access the network, and spawn detached processes that outlive cancellation.** Cancellation stops later agent actions, but it cannot guarantee that every process spawned by an approved command has terminated. Inspect the whole command before approving. Esc/Ctrl+C cancels a pending review without approving it. There is no blanket permission for edits or commands.

### Session names

A fresh session starts with a derived title (its first prompt, verbatim) and, right after the **first completed turn**, Lisa makes one background model call — the configured provider and model, not a second configuration — to generate a short session name (2–6 words, surrounding quotes and a trailing period stripped) such as `Fix Parser Bug`. The name replaces the derived title everywhere the session is shown: the status bar's `session` segment, `/sessions`, the resume dialogs, and `--sessions` listings. Generation is fire-and-forget: it never blocks the next prompt, writes no transcript entry, and a failure (or exiting before it completes) silently keeps the derived prompt-based title. Exactly one name is generated per session; resumed sessions never re-generate, and later renames remain manual (a `/title` command is future work).

## Local sessions and privacy

New launches create a session automatically. To find a prior session and continue it for the **same repository**:

```sh
go run ./cmd/lisa --sessions /path/to/repository
go run ./cmd/lisa --resume SESSION_ID /path/to/repository
```

Replace `SESSION_ID` with an ID from the listing; `--sessions` lists the ID, local update time, and title without needing a model or TUI — a fresh session shows the title derived from its first prompt until the auto-generated name replaces it (see [Session names](#session-names)). A session from another repository is not listed or loadable here. Resuming restores saved conversation and completed tool outcomes, **not** a pending approval or permission to replay an unfinished action. The repository path is canonicalized, so equivalent symlink paths select the same repository.

Lisa stores session history in `sessions.sqlite` under the operating system's user configuration directory's `lisa` child (normally `~/Library/Application Support/lisa` on macOS, `$XDG_CONFIG_HOME/lisa` on Linux when set, otherwise `~/.config/lisa`). Set `LISA_STATE_DIR` to choose another private local directory, for example `export LISA_STATE_DIR="$HOME/.local/state/lisa"` **before** starting or listing sessions. Lisa creates the state directory with user-only permissions and the database with user-only permissions; SQLite may create `-wal` and `-shm` files beside it. State is not stored in the selected repository. Session history can include prompts, repository text, proposed changes, and command output; treat backups as sensitive. Use SQLite's `.backup` operation for a consistent backup, not a plain copy of `sessions.sqlite` while its WAL may hold recent changes.

Lisa sends prompts, prior conversation, and tool results to the configured provider. That provider can see this content; its privacy policy governs what it receives. API keys are stored only in the private state directory. Repository read tools are confined to the repository, but approved shell commands are not.

## Install a published release with curl

When a release is published for [gem/lisa](https://github.com/gem/lisa), a developer on macOS or Linux can install it without cloning this repository:

```sh
curl -fsSL https://raw.githubusercontent.com/gem/lisa/main/scripts/install.sh | sh
```

The installer detects your OS and architecture (darwin/linux × amd64/arm64), downloads that release's archive and its SHA-256 checksum manifest, refuses any archive whose checksum does not match the manifest, checks that the binary reports the release version, and installs it to `~/.local/bin/lisa` without sudo. Pin a version for CI or reproducibility:

```sh
curl -fsSL https://raw.githubusercontent.com/gem/lisa/main/scripts/install.sh | sh -s -- v1.2.3
```

Optional overrides: `LISA_VERSION` (same as the pinned argument; conflicting values are refused), `LISA_INSTALL_DIR` (default `$HOME/.local/bin`), and, for self-hosted mirrors or tests, `LISA_RELEASE_BASE` (each release lives under `<BASE>/<VERSION>/`; plain HTTP is accepted only for loopback hosts) and `LISA_RELEASE_API` (latest-release lookup URL). If `~/.local/bin` is not on your `PATH`, the installer prints the `export PATH=...` line to add.

What this does **not** prove: the checksum manifest comes from the same host as the archive, so verification protects against corrupted or truncated downloads, not against a compromised release host. Latest-release resolution uses the GitHub API, which is rate-limited; if the lookup fails, pin a version.

This has been exercised locally against a served release on macOS ARM64 (checksum mismatch, unknown version, and success paths), but no real release has been published yet; the [published-binary walkthrough](#manual-tui-release-walkthrough-still-required) remains the release gate before relying on it.

## Build, verify, and install a local archive

From the Lisa project root, `release.sh` builds four versioned archives (`darwin`/`linux` × `amd64`/`arm64`) in `dist/`, each containing `lisa`, `README.md`, and `LICENSE`, plus a SHA-256 manifest. It **does not publish** them:

```sh
./scripts/release.sh v1.2.3
```

Select the archive matching your machine, for example `darwin_arm64` on Apple Silicon, `darwin_amd64` on Intel macOS, `linux_amd64` on x86-64 Linux, or `linux_arm64` on ARM64 Linux. Verify the locally generated files **before extraction** (the manifest checks all four archives, so keep them together in `dist/`):

```sh
cd dist
shasum -a 256 -c lisa_v1.2.3_checksums.txt  # macOS
# or: sha256sum -c lisa_v1.2.3_checksums.txt # Linux
cd ..
```

Then run the local installation smoke script. Its interface is `./scripts/smoke-release.sh dist/lisa_v1.2.3_<host>_<arch>.tar.gz v1.2.3`, with `<host>`/`<arch>` replaced by the selected target. For example, on Apple Silicon:

```sh
./scripts/smoke-release.sh dist/lisa_v1.2.3_darwin_arm64.tar.gz v1.2.3
```

This extracts into a temporary directory and checks `--version` and `--help`, **not** model connectivity. To install the verified archive locally, substitute the same filename below:

```sh
archive=dist/lisa_v1.2.3_darwin_arm64.tar.gz  # example: replace for your host
tmp=$(mktemp -d)
tar -xzf "$archive" -C "$tmp"
mkdir -p "$HOME/.local/bin"
install -m 755 "$tmp/lisa" "$HOME/.local/bin/lisa"
rm -rf "$tmp"
"$HOME/.local/bin/lisa" --version
```

Ensure `$HOME/.local/bin` is on your `PATH`, or invoke the binary by its full path. The archive's README and LICENSE are available by extracting them alongside the binary. For an ordinary source build instead, run `go build -o bin/lisa ./cmd/lisa`.

### Manual TUI release walkthrough (still required)

This is a **procedure to perform**, not a claim that it passed on any published archive or provider:

1. On each supported host/architecture, verify the checksum, install its local archive, check `--version` and `--help`, and run `"$HOME/.local/bin/lisa" /path/to/test-repository` in an interactive terminal. Complete the first-run setup with a hosted provider (`--provider opencode-go` with a key skips setup) and confirm the connection check reports **Connected** before prompting.
2. Ask Lisa to use `read_file` or `search_files`; check the returned repository content. Ask it to use `edit_file` on a disposable file. Inspect all diff pages, press **N**, and confirm the file did not change. Ask again, review every page, press **Y**, and confirm the displayed change was applied. Try a multi-page diff and a narrow viewport.
3. Ask it to use `run_command` for a harmless check such as `git status --short`. Inspect the exact command and working directory; reject once with **N**, then request it again, review all pages, approve with **Y**, and inspect the actual output and exit status. Test cancellation without granting permission.
4. Exit with Ctrl+D, run `"$HOME/.local/bin/lisa" --sessions /path/to/test-repository`, and resume its ID with `--resume SESSION_ID`. Confirm completed conversation and tool results return, but an interrupted pending review never executes after relaunch. Check that another repository cannot list or resume that session.

Release publication and provider compatibility remain unverified until this walkthrough succeeds on the intended targets with a live-verified provider. For source checks, run `go test ./...` from the Lisa project root.
