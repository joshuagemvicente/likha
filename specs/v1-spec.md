# Lisa: v1 Terminal Coding Agent Specification and Delivery Plan

**Status:** Features 1–4 pass `go test ./...`, `go test -race ./...`, and local-protocol TUI smoke for repository reads, reviewed edits/commands, SQLite resume, and interrupted approval. Feature 5 produces four local archives; the macOS ARM64 archive passed checksum and installation checks. Before the provider-only pivot, the documented local model (Ollama `qwen3:4b`) was verified locally for connection, setup-skip, structured tool calls, edit approval, and session resume (2026-09-29); the `local` provider row has since been removed (see CHANGELOG.md). `opencode-go` is live-verified with a real key (2026-09-29). The ChatGPT (Plus/Pro) OAuth provider is implemented (local): login flows, token refresh, and the Responses wire pass the automated suite, but a live probe with a real ChatGPT Plus account has not run — treat it as not yet live-verified. Remaining unverified: a published-binary walkthrough on each supported target, and live probes for the other unprobed hosted providers — see specs/predefined-providers/ and specs/chatgpt-plus/.

## 1. Purpose and objectives

Lisa is a shareable, local-first terminal coding agent. A user opens a repository, describes a change, observes the agent's actions, reviews proposed edits and commands, and checks the result. Lisa is an alternative to terminal-based coding agents, not a full in-terminal code editor.

The v1 goal is for someone other than the developer to install a release and complete that workflow using the published documentation alone. A successful session can be resumed after exiting the application.

### Project identity

- The project and application name is **Lisa**; the executable is `lisa`.
- The initial TUI screen identifies Lisa and displays this ASCII logo where space allows:

  ```text
   _     ___ ____    _
  | |   |_ _/ ___|  / \
  | |    | |\___ \ / _ \
  | |___ | | ___) / ___ \
  |_____|___|____/_/   \_\
  ```

- Identity must not compete with essential state or controls on small terminals. Color is optional; the plain-text logo remains legible without it.

## 2. Scope and boundaries

### In scope

- One local user, one selected repository, and one active agent run at a time.
- Interactive TUI chat with streaming model responses and cancellation.
- A predefined list of accepted hosted model providers, configured BYOK-style: the user supplies an API key and Lisa verifies the connection. Unlisted OpenAI-compatible endpoints are allowed with a visible unverified warning. Lisa hosts no models and bundles no local inference server. Providers that use account login instead of an API key are configured through an OAuth sign-in whose credentials live in the same private state directory.
- Repository-scoped listing, searching, and reading; agent-proposed file edits; approval-gated shell commands.
- A readable diff and explicit approval before each edit; exact command and working directory shown before each command.
- Local configuration and repository-associated session history that can be resumed.
- Versioned macOS and Linux binaries, a license, installation and first-run documentation, limitations, and release notes.

### Out of scope

- Built-in code editor, language-server integration, concurrent agents, plugins, cloud sync, hosting or bundling any model or local inference server (Ollama, llama.cpp, and similar), protocol adapters beyond OpenAI chat-completions and the accepted list's documented exceptions (the ChatGPT Codex Responses surface).
- Windows support until terminal behavior is tested.
- An operating-system security sandbox for shell commands. A repository working directory does not constrain an approved command's filesystem or network access.

## 3. Recommended technology stack

| Concern | v1 choice | Reason |
| --- | --- | --- |
| Application and agent runtime | Go | One codebase and distributable binaries for the supported targets. |
| Terminal interface | Bubble Tea and Lip Gloss | Established Go tools for interactive terminal views and styling. |
| Model integration | OpenAI-compatible chat API with a predefined accepted provider list; the accepted list may include documented non-chat-completions surfaces (the ChatGPT Codex Responses API) | One primary protocol surface; adding a provider is configuration; a documented exception (the ChatGPT Responses wire) is its own client surface. |
| Predefined providers | OpenAI, OpenRouter, Amazon Bedrock, Dialagram, Opencode Zen, Opencode Go, ChatGPT (Plus/Pro), Groq, xAI, Together AI, Mistral AI, Cerebras (hosted: BYOK key, except ChatGPT's OAuth login) | Every listed provider is hosted: it receives the user's key or login and the conversation content, and must pass the full probe (streamed text, structured tool calls, mid-stream cancellation, tool-result fidelity) before it is described as accepted. Providers added without probe results remain pending-probe. Lisa hosts no models. |
| Persistence | Local configuration and SQLite session database | Keeps repository-associated, resumable history in private local state without requiring a cloud service. |

Compatibility is a behavior to verify, not something guaranteed by an endpoint calling itself OpenAI-compatible. A model that only writes textual suggestions for tools does not satisfy the structured-tool-call requirement. Each predefined provider ships with the probe result used to admit it; a listed provider without probe evidence must not be described as accepted.

## 4. Functional requirements

| ID | Requirement |
| --- | --- |
| FR-01 | `lisa` accepts a repository path or uses the current directory; it displays the selected repository before an agent run begins. |
| FR-02 | The user can configure one hosted model provider from the predefined list with an API key. If no provider is configured, the interactive TUI offers a first-run setup: provider selection, masked API-key entry, a connection check, and model selection; the choice is stored in the private state directory. The model name is optional: without it, Lisa uses the provider's documented default, or — for an endpoint outside the list — the first model that endpoint reports. A startup connection check and a check before each agent run surface a dead or revoked key before prompts are sent. Missing configuration, connectivity failures, revoked keys, and unsupported capabilities produce clear errors. An unlisted OpenAI-compatible endpoint is allowed with a visible unverified warning. Providers that use account login instead of an API key are configured through the same first-run setup via an OAuth sign-in, with a headless device-login alternative (`--device-login`); stored login credentials follow the same private-state rules as API keys and refresh automatically. |
| FR-03 | The TUI accepts prompts, streams responses, and distinguishes assistant text, tool requests, tool results, approval requests, and errors. A prompt beginning with `/` is a command from the reserved list (`/sessions`, `/models`, `/model`, `/compact`, `/quit`, `/help`) and acts on the application, never on the conversation; unknown commands produce a visible error and are never sent to the model, and `//` escapes a literal slash. |
| FR-04 | The user can cancel an active run. Cancellation prevents subsequent agent actions in that run and leaves the interface usable. |
| FR-05 | The agent can list, search, and read repository files. File tools reject paths resolving outside the selected repository, including path traversal and symlink escapes. |
| FR-06 | The agent can propose edits. Lisa displays affected paths and a readable diff before any write. No write proceeds without explicit approval of that proposal. |
| FR-07 | Rejecting an edit leaves files unchanged. If an affected file changes while approval is pending, Lisa refuses the stale proposal rather than overwriting the newer content. |
| FR-08 | Lisa shows the exact command and working directory before requiring approval for each shell command. Rejection prevents execution; execution reports output and exit status. |
| FR-09 | Failed, rejected, and cancelled actions are recorded and reported accurately to the user and model; none is represented as successful. |
| FR-10 | Local sessions are stored in SQLite, associated with their repository, and can be listed and resumed. Pending approvals are not silently replayed or granted after relaunch. |
| FR-11 | Model, tool, path, write-conflict, and command errors remain visible without unexpectedly closing the TUI. |
| FR-12 | The initial TUI screen identifies Lisa and displays the ASCII logo when there is room without obscuring essential controls. On wide terminals the logo block is the only top-of-screen furniture: identity, repository, branch, provider, and model are carried by the status bar, which renders a small Lisa mark at its right end. On terminals too small for the logo, a single compact identity line is shown instead. |
| FR-13 | Versioned macOS and Linux releases contain a `lisa` binary; a user can install and use one using the documentation alone. |
| FR-14 | The interactive TUI occupies the full terminal viewport in the alternate screen. It has no scrollable pane and does not push streamed text or command output into terminal scrollback. Content longer than the available view scrolls continuously, browser-like: the mouse wheel moves the view a few lines per notch instead of a page at a time, PgUp/PgDn step one viewport, Home and End jump to the very top and to the newest content, and a status position indicator plus a right-edge scrollbar (clickable and draggable) show where the viewport sits. Nothing is silently clipped or unreachable. While the viewport sits at the bottom it follows new content like a chat; scrolling up anchors it until the user returns to the bottom. Mouse capture stays off, so the terminal's native text selection keeps working. |
| FR-15 | The user can choose a color theme from the predefined list (first-run setup stage, `/themes` command, or `--theme`/`LISA_THEME`); palettes adapt to a detected light or dark terminal, and the choice persists in the private state directory. Thinking-model reasoning renders in the theme's muted role and is never sent back to the provider. Nerd Font icons are strictly opt-in (`--nerd-fonts`/`LISA_NERD=1`), appear only as status and review markers, and plain-text output remains complete and legible without them. |
| FR-16 | The user can configure Model Context Protocol (MCP) servers over stdio in `mcp.json` inside the private state directory; their tools join the agent's tool set. Every server's first tool call is shown with server name, tool identity, and arguments, and requires explicit approval; approval trusts that server for the session and resets on relaunch. A crashed or hung server produces a clear failed tool result without breaking the TUI, `/mcp` lists configured servers and their state, and configured servers are killed on exit. Built-in tools and the approval flow are unchanged. |
| FR-17 | The prompt input behaves like a native terminal line editor: the user can move a visible cursor (arrow keys, Home/End, Ctrl+A/B/E/F), kill text (Ctrl+U to start, Ctrl+K to end, Ctrl+W / Ctrl+Backspace / Alt+Backspace word back, Ctrl+Delete / Alt+D word forward with whitespace runs collapsing into one deletion), and yank the last kill with Ctrl+Y from a small kill ring. The cursor renders as a blinking block caret, solid while the user is typing and blinking when idle, hidden while an approval is pending. Highlighting and copying text is handled by the terminal itself, not by Lisa; bracketed-paste text is inserted at the cursor. Editing stays live during an active run while no approval is pending; Enter queues the draft as a steering prompt (FR-21) instead of sending it. While an approval is pending, editing is inert and the caret is hidden, as today. |
| FR-18 | The user can attach images to a prompt by pasting from the system clipboard (Ctrl+V); the draft shows a removable `[Image #N]` token as one editing unit. Only PNG, JPEG, and WebP pass the format guard (GIF is rejected with a clear message even when the provider would accept it), each image is capped at 5 MB with a clear error, and a provider or model that rejects images produces a clear error suggesting a text-only retry. Image data stays in the session history and is resent with each turn; attachments persist across relaunch via the private state directory, and a missing stored image on resume is dropped with a visible note. Image content never bypasses the approval flow or reaches the model without the user's prompt. |
| FR-19 | Every agent request includes a compiled-in harness system message, sent first, whose tool contract directs the model to use the built-in read-only tools (`read`, `glob`, `grep`) for repository inspection and to reserve `run_command` for actions only the shell can perform. Read-only repository inspection consequently requires no approval prompt and no shell command. |
| FR-20 | A turn that reaches the consecutive tool-round checkpoint does not fail: Lisa shows a visible notice in the conversation, tells the model to continue, and the turn keeps running until the model finishes or the user cancels. |
| FR-21 | While a run is active, the prompt input stays usable: the user can type a draft and press Enter to queue it as a steering prompt. Queued prompts appear in the conversation as queued until the agent receives them, then as sent user messages. A queued prompt reaches the model at the next safe point in the same run — after the current tool calls settle, before the next model request, or in place of ending the run while prompts remain queued — in the order typed; a run ends only with an empty queue. If a run ends with prompts still queued (cancellation, error, or a finished compaction), they are held and stay visible as queued; pressing Enter on an empty draft sends the whole held queue as the next turn, and a bare Escape clears it. Reserved `/` commands stay inactive while a run is active and are refused with a visible message (`//` still queues literal text). Queue contents are never persisted and never replayed on resume. |
| FR-22 | An approval review presents the decision as labelled actions — **Approve** and **Decline** — with exactly one focused action, moved with the arrow keys or Tab and confirmed with Enter; the focused action is visibly highlighted on every theme. Approve remains unavailable until the review has been read to its end; Decline rejects the proposal and lets the model continue. Escape keeps its cancel-run meaning (FR-04). No letter-key approval shortcuts exist. |
| FR-23 | The status bar shows input-context usage for the active conversation's latest model request as used/limit and percentage when the model context window is known. Lisa prefers provider-reported input usage, otherwise shows a visibly approximate estimate calculated before each request; it resolves limits from a positive `context_windows` override in private `config.json` (keyed by provider and model), provider/model metadata, then Lisa's documented model catalog. Estimates are marked approximate, unknown limits are shown as unknown rather than guessed, and tracker data is recalculated after compaction and on session resume. Missing usage telemetry or estimate data does not fail a model request. |

Read operations do not need per-call approval but are repository-scoped. Approval for a write or command applies only to the displayed proposal; there is no blanket session-level permission in v1.

## 5. Non-functional requirements

### Safety and privacy

- Treat model-supplied paths, edit content, commands, and repository content as untrusted. Validate resolved read/write targets rather than checking path-string prefixes alone.
- Never report an action complete before its result is known. An edit that cannot still be applied as displayed fails visibly instead of replacing newer content.
- No rejected proposal may partially change files. Interrupted approvals do not become approvals on session resume.
- Repository content and model output cannot override user approval decisions.
- Explain in the interface and documentation that an approved shell command may access locations outside the repository and use the network; command approval is not sandboxing.
- Store configuration and the SQLite session database in private local state. Explain that content sent to the configured provider is visible to that provider; every listed provider is hosted, and its privacy policy governs what it receives. API keys and OAuth login credentials live only in the private state directory, never in the repository or session database. Lisa hosts no models and requires no service of its own.

### Reliability and usability

- A connection or tool failure preserves the session and permits a new prompt or normal exit.
- Long responses, command output, chat history, prompts, and diffs remain inspectable through continuous scrolling (nothing clipped or unreachable); streaming text and the logo do not obscure pending approvals.
- Show whether Lisa is waiting for the model, executing a tool, or waiting for the user.
- Following a process interruption, unfinished work must not appear as a completed action in session history.
- Published binaries run on their stated macOS and Linux targets.

## 6. Architecture and components

1. **TUI:** Renders identity, conversation, activity, reviews, input, and errors. Sends user decisions to the runtime.
2. **Agent runtime:** Maintains the conversation, streams model output, dispatches supported tool calls, and honors cancellation.
3. **Model client:** Sends conversation and tool definitions to the endpoint and consumes streamed text and structured tool calls. It rejects incompatible responses rather than interpreting tool instructions from prose.
4. **Repository tools:** Resolve and validate paths, list/search/read files, prepare diffs, and check whether an approved edit still applies.
5. **Approval coordinator:** Holds a proposed write or command until its specific approval or rejection. There is no alternate write/command route around it.
6. **Command runner:** Executes an approved command with the displayed repository working directory and returns output and exit status.
7. **Session store:** Uses local SQLite transactions to persist repository-associated conversation events, user decisions, and completed action results so a prior session can be displayed and continued.

These components describe responsibilities within Lisa; v1 does not require a plugin architecture or separate services.

## 7. Interfaces and data flow

### TUI contract

The terminal view distinguishes **conversation**, **activity**, **review**, and **input**. A review shows either the full proposed diff with affected paths or the exact shell command with its working directory. Approve, reject, and cancel actions must be discoverable. On a small terminal, a pending review takes priority over branding.

The status bar's context tracker reports input tokens for the active conversation's latest model request, not generated output or cumulative session usage (FR-23). It distinguishes provider-reported counts from estimates, shows no percentage when the model limit is unknown, and does not imply that estimating context prevents overflow.

The interactive UI owns the full terminal viewport and redraws a fixed-size frame as terminal dimensions change. The terminal itself does not scroll. When content exceeds a frame, Lisa scrolls the view continuously (wheel scrolls line-granular; PgUp/PgDn, Home, and End jump; a right-edge scrollbar shows and controls the position) and keeps a page-position indicator in the status line. Approving an edit must not require accepting unseen diff content: the whole proposal remains reachable before the approval decision, and approval requires having scrolled to the end of the pending content. Streaming updates and command output are captured and rendered in the frame, not written directly to terminal scrollback. At dimensions too small to show an approval and its controls safely, Lisa displays a resize message and does not accept that approval until the viewport is usable. On exit, it restores the terminal screen.

### Model and tool contract

- **Model input:** Conversation context, current prompt, and definitions of available tools. A startup connection check and a pre-run check verify the configured provider before prompts are sent.
- **Model output:** Streamed assistant text and structured tool requests. Unsupported tool-call behavior produces a visible error; plain assistant text never executes a tool.
- **Accepted providers:** The release documentation lists each accepted provider with its base URL, key format or login flow, and the probe result used to admit it. An endpoint not on the list runs with a visible unverified warning.
- **Read/search:** Validated repository-relative request to a result or a clear error.
- **Edit:** Proposed changes to diff preview to explicit decision to apply-or-reject result. Approval of a stale proposal produces a conflict result without overwriting files.
- **Command:** Exact request to displayed approval to execution-or-rejection result; execution reports output and exit status.
- **Session:** Persist completed events and explicit decisions. On resume, restore context but do not execute pending actions or infer permission from prior approvals.

```text
Launch Lisa -> select repository and session -> enter prompt
                                              |
                                      stream model response
                                              |
                         read/search -> validate -> execute -> report
                         edit -> diff -> approve/reject -> report
                         command -> review -> approve/reject -> report
                                              |
                                    continue or await prompt
```

Cancellation stops further actions in the run. If cancellation or interruption occurs while approval is pending, that approval does not carry forward.

## 8. Assumptions, constraints, and dependencies

- **Assumptions:** One person uses one Lisa instance against one repository at a time; the user holds an API key for a predefined hosted provider; the user's terminal can display the TUI and diffs.
- **Constraints:** Lisa is a terminal coding agent rather than an editor; it does not operate, bundle, or host a model service, and it ships no local inference integration; endpoint compatibility must be demonstrated before a provider is described as accepted; approval is not command sandboxing.
- **Dependencies:** Go and the selected TUI libraries for builds; a verified hosted provider from the predefined list with the user's API key for the documented walkthrough; a process for producing versioned binaries on supported targets.

## 9. Delivery plan

1. **Identity and connection:** Starting from the already testable CLI startup slice, build the full-screen, non-scrolling TUI with paged content; accept a repository, load local configuration, select a hosted provider from the predefined list, verify the connection, and stream a basic conversation with cancellation. Validate viewport resizing, long content, startup, and failure states before adding tools.
2. **Read-only agent loop:** Expose repository list/search/read tools through structured model calls. Enforce resolved-path confinement and show tool activity and results. Prove that invalid paths do not return data.
3. **Reviewed actions:** Add edit proposals and diff review with stale-change detection. Add exact-command review and approval-gated execution. Preserve rejection and failure outcomes in the conversation.
4. **Session continuity:** Set up a private local SQLite session database, persist completed conversation events and decisions, and list and resume sessions for the canonical repository. Version the schema and define migration and consistent backup procedures. Verify cancellation and process interruption cannot trigger a pending action on resume.
5. **Shareable release:** Document installation, the predefined provider list with each provider's probe result, base URL, key format, and privacy note, the first-run example, permissions, and limitations. Add a license and release notes, publish versioned macOS/Linux binaries, and perform the release-gate walkthrough from a published binary.

Each phase is complete only when its stated behavior works in the actual TUI. The plan does not include a plugin system, editor, cloud service, or other out-of-scope feature.

## 10. Acceptance criteria and validation

| Area | Acceptance criterion | Validation method |
| --- | --- | --- |
| Identity | The release binary is `lisa`; startup identifies Lisa and shows the ASCII logo where space allows. | Launch at normal and constrained terminal sizes; check that essential controls remain visible. |
| Full-screen visuals | The TUI fills the terminal without terminal or pane scrolling; content longer than one viewport is scrollable line-granular to its very first and last line, including the complete diff before approval. | Exercise a long conversation, long command output, and a multi-viewport proposal in a terminal; use wheel, PgUp/PgDn, Home/End, and the scrollbar (click and drag), resize during streaming and approval, and confirm no output enters terminal scrollback and the terminal restores on exit. |
| Installation | A new user installs and launches a published binary using the documentation alone. | Follow the published instructions in a clean environment. |
| Model | Each predefined provider streams text and invokes a structured tool call; the startup and pre-run checks surface a dead or revoked key; an unlisted endpoint shows the unverified warning. The ChatGPT (Plus/Pro) provider ships pending its live probe with a real account, like the other unprobed hosted providers. | Run each listed provider end to end; exercise an unavailable endpoint, a revoked key, an incompatible response, and an unlisted endpoint. |
| Repository reads | In-repository files can be listed, searched, and read; traversal and symlink escapes fail. | Exercise valid paths and attempted escapes in a sample repository. |
| Edit approval | A file is unchanged before approval and after rejection; approval applies the displayed diff. | Compare file contents before and after each decision. |
| Edit conflict | An intervening file change is not overwritten by an older proposal. | Modify a target while its proposal awaits approval, then attempt approval. |
| Command approval | Rejection does not run a command; approval runs the exact displayed command and reports output and exit status. | Use one command with an observable effect and one with a nonzero exit status. |
| Cancellation | Cancellation prevents later tool actions in the run and permits another prompt. | Cancel during streaming and with an action pending. |
| Resume | SQLite-backed completed conversation context reopens for the selected repository; interrupted actions remain unexecuted. | Exit and relaunch after a completed turn and again during pending approval; confirm a different repository cannot select the saved session. |
| Release | Published macOS/Linux binaries, license, setup guide, limitations, and release notes consistently identify Lisa. | Install on each stated target and complete the documented example without developer assistance. |

**Release gate:** Using a published Lisa binary and a predefined hosted provider with the user's API key, a new user opens a sample repository, requests a small change, inspects and approves its diff, approves a check command, inspects the result, exits, and resumes the session. All steps must work without assistance from the developer.
