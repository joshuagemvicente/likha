# Lisa

Lisa is a terminal coding agent for one repository at a time. It streams responses from a hosted model provider on the predefined accepted list, using your own API key (BYOK) — Lisa hosts no models and bundles no local inference server. It reads repository files and asks permission before editing a file or running a shell command. Conversations are stored in local SQLite so you can resume them. The full-screen interface uses pages rather than terminal scrollback.

**Release status:** Local scripts build macOS and Linux archives; no release has been published. A local macOS ARM64 archive passed the documented checksum/install checks and an interactive workflow against a protocol server. `opencode-go` is **live-verified** (user session on 2026-09-29: setup flow, connection check, streaming, and agent tool usage with a real key). `opencode-zen` shares the same verified surface and session header but awaits a live-key turn; the other hosted providers (OpenAI, OpenRouter, Amazon Bedrock, Dialagram) remain wired but unprobed. See the [v1 specification](specs/v1-spec.md), [feature test plan](specs/feature-test-plan.md), [setup plan](specs/setup-plan.md), and [unreleased changes](CHANGELOG.md).

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

Key resolution order: `--api-key`, then `LISA_API_KEY`, then the provider's own `LISA_<NAME>_API_KEY`, then the private state directory (`providers.json`, mode 0600). Passing `--api-key` once stores the key for later runs. Keys are never read from the selected repository and never stored in the session database. Endpoint URLs may not contain credentials, query strings, or fragments; plain HTTP is accepted only for loopback hosts.

Lisa checks the connection when it starts and again before each agent run; a dead or revoked key is reported before any prompt is sent. An endpoint outside the list runs with a visible **unverified** warning in the interface. `opencode-go` is live-verified; the remaining providers are **not yet probed** against their live services — see `specs/predefined-providers/`.

Lisa identifies itself to providers with `User-Agent: lisa/<version>`. The Opencode providers additionally receive your stable conversation ID in `x-opencode-session`, which they require for routing and prompt caching.

Run without a model name: Lisa uses the provider's documented default (listed above), or — for an endpoint outside the list — the first model that endpoint reports. Override with `LISA_MODEL` or `--model`. `lisa --help` prints every option, provider, environment variable, and example.

### First-run setup (no flags needed)

Run `lisa` with no provider configured and the TUI walks you through setup:

1. **Choose a provider** — ↑/↓ and Enter over the predefined list.
2. **Paste your API key** — masked input; the key is checked against the provider before anything else happens.
3. **Choose a model** — picked from the models the provider reports; a single model auto-selects.

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

## Work in the terminal

The alternate-screen UI shows the repository, model, status, and `Page n/m`. It does not scroll the terminal or a pane. Type a request and press **Enter**. **PgUp/PgDn** (or **Ctrl+P/Ctrl+N**) move through fixed pages of chat, drafts, diffs, and tool results. Resize to at least 40 columns by 12 rows if prompted; unsafe-size screens cannot accept a prompt or approve a review. **Ctrl+C** or **Esc** cancels an active run and drains completed tool results into the session before showing cancellation; **Ctrl+C** while idle or **Ctrl+D** exits. The terminal screen is restored on exit.

Lisa offers `list_files`, `read_file`, and literal `search_files` for repository text. These tools reject traversal and symlink escapes; size and result limits can return errors. To request a change, try: “Use edit_file to change notes.txt from old to new.” The model must actually request the tool. `edit_file` proposes replacement content or a new file and shows the affected path and a diff **before** writing. Review every page with PgDn/Ctrl+N (and PgUp/Ctrl+P to revisit); **Y** approves that proposal only after every page has been viewed, while **N** rejects immediately. If the file changes after review starts, the stale edit is refused rather than overwriting it.

To check work, try: “Use run_command to run `git status --short`.” The review shows the **exact shell command** and canonical repository working directory. Commands containing invisible or control characters, including tabs and line breaks, are rejected before review; control characters in other review text are escaped visibly. Again, **Y** approves only after all review pages have been viewed; **N** rejects without execution. An approved command runs via `sh -c` in that directory; its output and exit status appear in the conversation (long output is paged, and output above the command limit is marked truncated). **The working directory is not a sandbox. An approved command can read or change files outside the repository, access the network, and spawn detached processes that outlive cancellation.** Cancellation stops later agent actions, but it cannot guarantee that every process spawned by an approved command has terminated. Inspect the whole command before approving. Esc/Ctrl+C cancels a pending review without approving it. There is no blanket permission for edits or commands.

## Local sessions and privacy

New launches create a session automatically. To find a prior session and continue it for the **same repository**:

```sh
go run ./cmd/lisa --sessions /path/to/repository
go run ./cmd/lisa --resume SESSION_ID /path/to/repository
```

Replace `SESSION_ID` with an ID from the listing; `--sessions` lists the ID, local update time, and title without needing a model or TUI. A session from another repository is not listed or loadable here. Resuming restores saved conversation and completed tool outcomes, **not** a pending approval or permission to replay an unfinished action. The repository path is canonicalized, so equivalent symlink paths select the same repository.

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
