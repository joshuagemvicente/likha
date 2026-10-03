# Feature: Likha web UI (`lisa --serve`)

**Status:** draft — awaiting review. No code exists yet; this folder is the
proposal. Research record: [research.md](research.md). Delivery plan:
[tasks.md](tasks.md).

Naming: the binary, module, and package paths stay `lisa`; user-visible
surfaces use the Likha display name already in the TUI. Adjust titles only
if the rename lands differently.

## 1. Why

Lisa is TUI-only. Every other agent surface in the market now has a second
front-end: opencode serves a web UI from the same local server that powers
its TUI; Codex runs one harness behind CLI, IDE, app, and web; Claude Code
adds Remote Control so a browser or phone can drive a session running in a
terminal; Goose, OpenHands, and Aider all render the session in a browser.
The research record in [research.md](research.md) shows the convergent
shape: **the agent stays on the host, the UI is a remote, and the wire is a
structured, resumable event stream** — not a PTY mirror.

Lisa is unusually well positioned for this. `internal/agent` is already
UI-free: `RunTurn(ctx, client, repo, root, prior, prompt, mcp, steer,
emit)` is driven by a context, a steering channel, an emit callback, and a
blocking `ApprovalRequest.Reply` channel. Sessions, providers, credentials,
repository tools, and action execution are all UI-free packages. Only the
Bubbletea layer is terminal-bound. A web front-end is therefore a second
consumer of an existing engine, not a rewrite.

What a browser adds that the terminal cannot:

- **Read and decide away from the keyboard.** Reviewing a 2,000-line diff
  in a terminal is scrollbar archaeology; a browser can lay it out, and a
  phone can approve a command from the couch.
- **A second window on one session.** A session driven in the terminal can
  be watched and steered from a browser on the same machine (attach mode,
  §7) — Lisa's analogue of Claude's Remote Control, without a relay.
- **Native affordances** the TUI deliberately refuses: clickable diffs,
  per-file review, images pasted into prompts, a real session sidebar.

## 2. What this is, and what it is not

**Is:** a loopback HTTP server (`lisa --serve`) that owns sessions and
serves an embedded single-page app; the browser drives the same
`agent.RunTurn` engine the TUI drives, with the same approvals, steering,
compaction, session naming, and SQLite persistence.

**Is not:** a hosted service, a multi-user server, a sandbox, a PTY mirror,
a worktree/kanban orchestrator, or a replacement for the TUI. One
repository per server, exactly like the TUI ("one repository at a time",
README). No cloud, no relay, no inbound exposure by default.

## 3. Starting the server

```
lisa --serve [repository] [--port N] [--host H] [--token T]
```

- Defaults: bind `127.0.0.1`, **ephemeral port**; the startup output prints
  the exact URL including the one-time pairing token. `--port` pins a port;
  a pinned port that is taken fails with a clear message (no silent
  fallback).
- `--host` accepts a non-loopback address only together with `--token`
  (or `LISA_SERVE_TOKEN`) and prints a prominent warning; without a token,
  non-loopback binds are refused. Documentation recommends SSH
  port-forwarding over direct exposure, matching opencode's guidance.
- Startup performs the same connection check as the TUI
  (`Client.EnsureConnected`); a dead key is reported in the browser, not as
  a startup crash. A provider must already be configured (same rule as
  today's non-interactive mode); web-based first-run setup is a later
  milestone (§7).
- `Ctrl+C` stops the server and cancels any running turn; completed state
  is already persisted. Closing a browser tab **does not** cancel a run —
  the run belongs to the server (opencode's `prompt_async` semantics).
- The TUI keeps working exactly as today. `lisa --serve` is a sibling mode
  in `internal/app`, not a change to the TUI path.

## 4. Pairing and access

- The printed URL carries a single-use, 256-bit pairing token. Opening it
  exchanges the token (`POST /api/pair`) for an `HttpOnly`, `SameSite=Strict`
  session cookie scoped to the origin; the token is then removed from the
  address bar. Every API call and the event stream require the cookie.
- The cookie is regenerated each server start; there is no persistence, so
  a stale bookmark fails with a page that explains how to get a fresh link.
- Mutating requests are rejected unless `Origin` matches the server origin
  (and `Sec-Fetch-Site` is same-origin when present). No CORS headers are
  emitted. No token or cookie value is ever logged.
- The event stream (`EventSource`) authenticates with the same cookie; no
  token appears in stream URLs.

This is the opencode/Goose local model (pair link or secret key), chosen
over "no auth by default" — the siteboon advisory is the cautionary tale
([research.md §2](research.md)).

## 5. The browser UI

Single page, three regions on desktop; two on narrow screens.

```
┌──────────────────────────────────────────────────────────────┐
│ Likha · repo-name · main        provider · model · ctx 42%    │
│                                              $0.42 · ● live  │
├────────────┬─────────────────────────────────────────────────┤
│ SESSIONS   │ transcript (continuous, role-banded)             │
│ ▸ Fix bug  │   You: …                                         │
│   Add test │   Likha: streaming text…                         │
│            │   ▸ Thinking (collapsed)                         │
│ [+ New]    │   Tool: read internal/tui/tui.go  ▸ result       │
│            │   ┌─ Approve edit · internal/tui/tui.go ──────┐  │
│            │   │ diff, +12 −4, scroll to the end to approve│  │
│            │   │ [ Approve ]  [ Decline ]                  │  │
│            │   └───────────────────────────────────────────┘  │
├────────────┴─────────────────────────────────────────────────┤
│ Ask Likha…  @ files  / commands        [ Queue ]  [ Cancel ]  │
└──────────────────────────────────────────────────────────────┘
```

### 5.1 Sessions

- Sidebar lists the repository's sessions from the same `sessions.sqlite`
  the TUI uses (`session.Store.List`): model-generated name when present,
  derived first-prompt title otherwise, relative update time, and a running
  badge.
- New session, resume in place, and switch — no page reload; the transcript
  and composer swap under the same URL fragment (`#/s/{id}`).
- A session is **driven by one process at a time**. Sessions owned by a
  running server are marked; a conflicting save from another process keeps
  today's `session.ErrConflict` semantics and surfaces as a visible banner
  ("session changed in another process — reload") rather than silent loss.
  Attach mode (§7) exists precisely to avoid this case.

### 5.2 Transcript

- Rendered from persisted `session.Entry` rows (`Role`, `Content`) plus live
  deltas; roles map to the same bands as the TUI: `You`, `Lisa`, `Tool`,
  `Reasoning`, `Queued`, `Error`. `Logo` and `Queued` rows are never
  persisted (existing `persist()` rule); the web shows `Queued` rows only
  while the message is undelivered.
- Assistant text renders Markdown (sanitized); code blocks are monospace
  with copy buttons. Streaming appends plain text and re-renders Markdown
  on a frame-throttled pass.
- Reasoning renders as a collapsed "Thinking" block, expandable per turn.
- Tool activity renders as cards: tool name, arguments, collapsible result
  with monospace output and truncation markers. Results are the exact
  strings the TUI shows today; structured fields come from additive
  `agent.TurnEvent` fields (§6 of [tasks.md](tasks.md)).
- Compaction inserts the same visible marker as the TUI; errors render on
  the error band; the session header shows title, provider · model, context
  usage, and spend exactly when the TUI would show them (no fabricated
  numbers; `ctx —` when the window is unknown).
- The view follows new content while at the bottom and anchors when
  scrolled up — the same rule as the TUI (FR-14).

### 5.3 Composer

- Enter sends; Shift+Enter inserts a newline. Drafts are kept per session
  in `localStorage`.
- While a run is active the primary action becomes **Queue** (steering): the
  message renders as a `Queued` row and is delivered at the same drain
  points as the TUI (`drainSteer` semantics — after tool calls settle,
  before the next model request, or as the next turn when the run ends).
  **Cancel** aborts the run (same `ctx` cancellation path as Esc).
- `@` opens a file/folder picker backed by the same gitignore-aware
  repository listing the TUI uses; `@path` expansion is server-side in
  `agent.ExpandFileReferences`, unchanged. `/` opens the supported command
  list (`/compact [focus]`, `/models`, `/help` in v1; `/themes` optional).
  Commands are never sent to the model, matching the TUI.
- Pasted images are a later milestone (they already exist in the TUI's
  prompt-editor plan; the web composer accepts text in v1).

### 5.4 Approvals

Approvals are first-class cards in the transcript, not modal interruptions:

- **edit_file**: path, unified diff with `+N −M` stats, syntax-neutral
  coloring, collapsible per file. **Approve** is enabled only after the end
  of the diff has been visible (IntersectionObserver mirroring the TUI's
  scroll-to-end gate); until then it is muted with the status "Scroll to
  the end to approve". **Decline** is always enabled.
- **run_command**: the exact command, the canonical working directory, and
  the same no-sandbox warning text the TUI shows.
- **MCP trust-on-first-use**: server name, tool, and arguments; approval
  grants that server's tools for the rest of the session, exactly as today.
- Decisions are per proposal and first-answer-wins across tabs: a second
  decision for a resolved approval returns `409` and the card shows the
  recorded outcome. Cancel (run-level) resolves any pending card as
  cancelled without executing.
- The web adds **no** auto-approve path, no "always allow", and no
  remembered approval across sessions. The existing warning that approved
  commands run unsandboxed applies verbatim.

### 5.5 Model, provider, and status

- The header shows provider · model, connection state, context usage, and
  session spend; clicking the model opens the `/models`-equivalent dialog:
  all configured providers' models grouped under provider headers, active
  provider first, type-to-filter, switching mid-session persists via
  `SaveStoredConfig` exactly like the TUI.
- Provider connection management (`/providers`: masked key entry, live
  check, stored key state) is in the parity milestone, not the MVP.
- The active theme name from `config.json` maps to CSS variables so the
  browser matches the terminal family where the palette is representable.

### 5.6 Narrow screens

Below ~900px the sidebar becomes a drawer; the transcript stays readable at
360px; the approval card pins to the bottom above the composer. Read,
queue, cancel, and approve/decline must all work at phone width — that is
the point of a web UI. Desktop-first layout, mobile-verified.

## 6. Security posture

- Loopback by default; non-loopback requires an explicit token and warns.
  SSH port-forwarding is the documented remote path in v1.
- Pairing token → `HttpOnly`/`SameSite=Strict` cookie; same-origin checks on
  mutations; no CORS; no token in logs or stream URLs.
- The browser is remote control for **unsandboxed shell execution**. The
  approval gate remains the only boundary; this feature changes where the
  decision is rendered, never what is allowed. Every approval is recorded in
  the transcript with its outcome.
- The server binds one repository per process and reuses the repository
  confinement (`glob`/`read`/`grep` reject traversal and symlink escapes)
  unchanged.
- `--serve` on a shared machine is a local-privilege surface: any local
  process that reads the printed URL can pair while the token is live.
  Documentation states this; token lifetime ends at server exit.

## 7. Later (explicitly out of the MVP, tracked in tasks.md)

- **Attach mode** (`lisa --web`): start the TUI and an in-process server
  sharing one live runtime, so a browser mirrors and drives the session the
  terminal is running. This is the payoff of the shared-runtime refactor
  and the closest analogue to Claude's Remote Control / Aider's embedded
  browser mode.
- **Web first-run setup**: provider picker + masked key entry + live check,
  for headless machines (the server cannot use the TUI's setup wizard).
- **Transcript export**: `GET /api/sessions/{id}/export.html` — a
  self-contained static page (offline shareable artifact, no hosted
  service).
- **Images in the composer** (clipboard paste), matching FR-18.
- **OpenAPI description** of the HTTP surface (opencode's `/doc` pattern)
  and, if demand appears, an ACP/AG-UI adapter. Not v1.

## 8. Functional changes (to apply in v1-spec.md at implementation start)

- **FR-24 added.** "Lisa can serve an embedded web UI from a local,
  loopback-bound HTTP server (`lisa --serve`). The browser drives the same
  agent engine, approvals, steering queue, sessions, and provider settings
  as the TUI; a session is driven by one process at a time, and concurrent
  writes surface the existing conflict error instead of silent loss. Access
  requires a per-start pairing token exchanged for a same-origin session
  cookie; non-loopback binds require an explicit token. Closing the browser
  does not cancel a running turn; an explicit Cancel does."
- **FR-25 added.** "While a run is active the web composer queues steering
  prompts delivered at the same drain points as the TUI (FR-21); approval
  cards present the same proposals as the TUI (FR-22) — labelled Approve and
  Decline, Approve unavailable until the proposal has been read to its end
  — and the web adds no auto-approve path. Approved commands run with the
  same unsandboxed semantics and warnings (FR-08)."
- FR-06/07/08/09/14/21/22 semantics are unchanged; FR-14's rules are
  restated for the browser in §5.2 rather than amended.
- README gains a "Web UI" section when the feature ships; the TUI remains
  the default mode.

## 9. Open decisions (recommended defaults; confirm or override)

1. **Shared runtime first (recommended).** Extract the turn lifecycle
   (queue, persistence checkpoints, run IDs, auto-naming) from
   `internal/tui` into `internal/runtime` and migrate the TUI onto it
   *before* building the server, so one implementation owns the semantics
   both front-ends rely on — and attach mode becomes possible. Alternative:
   let the server drive `agent.RunTurn` with its own ~250-line runner and
   accept two implementations of freshly specced steering/persistence
   behavior; faster to demo, guaranteed drift. Recommendation: extract.
2. **CLI shape: `lisa --serve` (recommended)** — matches the existing mode
   flags (`--sessions`, `--device-login`), no subcommand machinery.
   Alternative: `lisa serve` subcommand, matching opencode/Goose.
3. **Frontend stack: no-build vanilla ES modules + vendored marked/DOMPurify
   (recommended).** The build stays `go build`; assets are embedded with
   `go:embed`; no Node toolchain in the repo. Alternative: Preact/React with
   a Vite build step and committed `dist/` — more familiar, adds a second
   toolchain to a pure-Go project.
4. **Transport: REST + SSE (recommended)** — no new Go dependency
   (`net/http` + `encoding/json`), matches opencode/Omnara, `Last-Event-ID`
   replay. Alternative: WebSocket (needs a dependency; only wins for
   full-duplex, which steering/cancel/approve POSTs do not need).
5. **MVP boundary (§tasks M2):** one full turn with streaming, cancel, and
   both approval kinds through the browser, plus session list/resume —
   steering, mentions, and commands in M3. Alternative: everything at once
   before any review.

## 10. Acceptance criteria

- [ ] `lisa --serve` prints a loopback URL with a pairing token; opening it
      pairs the browser and shows the repository's sessions; the token is
      single-use and the cookie dies with the server.
- [ ] A prompt sent from the browser streams assistant text, reasoning, and
      tool activity into the transcript; the same session resumes in the
      browser after a server restart, with history intact.
- [ ] An `edit_file` proposal renders as a diff card; Approve is refused
      until the diff end has been seen; Decline executes nothing; an
      approved edit applies the exact displayed change.
- [ ] A `run_command` proposal shows the exact command, working directory,
      and warning; approving runs it and streams the real output and exit
      status; declining does not.
- [ ] While a run is active, a second prompt queues as a `Queued` row and is
      delivered inside the same run at the TUI's drain points; Cancel aborts
      the run without executing anything unapproved.
- [ ] Closing the tab leaves the run going; reopening reconnects to the live
      stream (or replays from `Last-Event-ID`) without duplicate entries.
- [ ] Non-loopback bind without a token is refused; a foreign `Origin`
      mutation is rejected; no token appears in logs or event URLs.
- [ ] `go test ./...` and `go test -race ./...` pass from the project root;
      the TUI's behavior is unchanged by the runtime extraction (existing
      suite plus a real-terminal walkthrough).
