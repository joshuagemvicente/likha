# Web UIs for terminal coding agents — research record

Researched 2026-10-03 for `specs/web-ui/spec.md`. Sources: primary docs and
repos of each product (URLs inline). Two research passes: first-party
products, then third-party wrappers and emerging protocols. Facts only; the
proposal's decisions cite this file.

## 1. First-party products

### opencode (sst/opencode) — closest analogue
- The TUI is a **client of a local HTTP server**: `opencode` starts TUI +
  server, `opencode serve` runs the server headless, and the TUI can attach
  to a remote server. The web UI is served from that same server.
  (<https://opencode.ai/docs/server/>, <https://opencode.ai/v2/docs/cli/web>)
- Transport: **REST + SSE**; OpenAPI 3.1 at `GET /doc`; events at
  `GET /event` (per-project) and `GET /global/event`. Endpoints cover
  sessions CRUD, prompts (`prompt_async` returns 204 without waiting),
  abort, permission responses, file/find, MCP, and `/tui/*` controls that
  let a web/IDE client drive the terminal UI.
  (<https://opencode.ai/docs/server/>)
- Default `127.0.0.1:4096`, `--hostname/--port/--cors/--mdns`; v2 background
  service defaults to 49374 and `opencode pair` prints a one-time link that
  sets a 30-day session cookie. Auth: `OPENCODE_SERVER_PASSWORD` HTTP basic
  (username `opencode`) or password+cookie; docs recommend SSH
  port-forwarding rather than exposing the port.
  (<https://opencode.ai/docs/server/>, <https://opencode.ai/v2/docs/cli/web>)
- Sessions live in the server (SQLite), so TUI/web/IDE share them; static
  share snapshots publish to a hosted service.
  (<https://opencode.ai/docs/share/>)

### Claude Code
- Three distinct models: cloud sandboxes (claude.ai/code), **Remote
  Control** (`claude remote-control`: local process stays host, browser or
  phone is a window, outbound HTTPS only, never an inbound port), and
  `--teleport` (one-way cloud → terminal).
  (<https://code.claude.com/docs/en/claude-code-on-the-web.md>,
  <https://code.claude.com/docs/en/remote-control.md>)
- Headless transport: `--output-format stream-json` JSONL and the Agent SDK.
- Session state is server-side; cross-surface "Continue in" menu, QR pairing,
  diff pane with inline line comments.
- Auth is account-based (API keys unsupported for cloud/RC).

### OpenAI Codex
- One harness behind four surfaces (CLI/TUI, IDE, macOS app, web) exposed by
  the **Codex App Server**: long-lived process, JSON-RPC over stdio (also
  WebSocket), typed primitives Item/Turn/Thread with `item/started`,
  `item/*/delta`, `item/completed`, `turn/completed`, and server-initiated
  approval requests that pause a turn.
  (<https://openai.com/index/unlocking-the-codex-harness/>,
  <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>)
- Web runtime: a worker provisions a container, launches the App Server
  inside it, and the browser talks to the backend over HTTP + SSE.
- Stated direction: refactor the TUI into an App Server client so it can
  attach to remote servers.

### Gemini CLI
- No web UI. Headless only: `-p`, `--output-format json` or `stream-json`
  (JSONL `init`/`message`/`tool_use`/`tool_result`/`error`/`result`).
  (<https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/headless.md>)

### Aider `--browser`
- Streamlit front end embedded in the normal Aider process; the Streamlit
  server runs on the host (filesystem access), browser is a client over a
  persistent WebSocket, one session per tab; it delegates to the existing
  `Coder`/repo/git engine.
  (<https://aider.chat/docs/usage/browser.html>,
  <https://docs.streamlit.io/develop/concepts/architecture/architecture>)

### Goose (block/goose)
- Electron/React desktop launches the bundled CLI and talks to its **ACP
  server**; `goose serve` is ACP over HTTP/WebSocket on `127.0.0.1:3284`
  (`/acp`), gated by `GOOSE_SERVER__SECRET_KEY`. Sessions in SQLite
  (`sessions.db`), shared by CLI and desktop.
  (<https://github.com/block/goose>, <https://goose-docs.ai/blog/2026/04/08/goose-acp-and-new-tui/>)

### OpenHands
- Browser (React "Agent Canvas") → Agent Server (REST + WebSocket, optional
  Bearer `session_api_key`, CORS allowlist) → workspace, optionally a Docker
  sandbox. The UI WebSocket is the per-conversation event stream.
  (<https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-agent-server/openhands/agent_server/README.md>,
  <https://github.com/OpenHands/OpenHands/blob/main/docs/architecture.md>)

## 2. Third-party wrappers and protocols

### Happy Coder (slopus/happy, MIT, ~24k★)
- wrapper + daemon + relay: `happy claude|codex` replaces the CLI; a local
  daemon registers the machine; a Fastify/Postgres/Socket.IO server relays
  to Expo clients (web/iOS/Android).
- **Structured, not PTY**: `update` events (`new-message`, `update-session`,
  `new-artifact`) carry a per-user monotonic `seq`; an RPC channel forwards
  bash/file/ripgrep to the owning session; payloads are client-side
  encrypted (relay sees routing metadata only). Local daemon exposes a
  localhost control server.
  (<https://github.com/slopus/happy/blob/main/docs/protocol.md>)

### claude-code-webui and siteboon/claudecodeui
- sugyan's React/Hono app streamed SDK JSON over `POST /api/chat` with an
  abort endpoint; archived 2026-05-29.
- siteboon's Express+React app runs a chat WebSocket **and a separate
  PTY/xterm.js shell**; the two paths can diverge; it shipped with no auth
  by default and a critical shell-injection advisory (patched 1.25.0).
  (<https://github.com/siteboon/claudecodeui/security/advisories/GHSA-gv8f-wpm2-m5wr>)

### Orchestrators
- **Omnara** (Go): SSE `GET /events/stream` with ordered `sequence` and
  `Last-Event-ID` resume; kinds `agent_input|model_output|tool_result|
  context_checkpoint`; approvals/questions are `interactions` attached to
  tool calls; Bearer auth. (<https://docs.omnara.com/events/streaming>)
- **Vibe Kanban** (Rust, sunsetting): kanban → workspace (git worktree +
  branch + terminal + dev server) per task, launching any agent CLI; local
  web app with tunnel mode; diff review with inline comments.
  (<https://github.com/BloopAI/vibe-kanban>)
- **Conductor** (macOS): parallel agents in isolated worktrees, diff
  viewer, several agents per workspace; isolation explicitly not a security
  boundary. (<https://www.conductor.build/docs>)
- **Crystal** (Electron, deprecated): Express API + SQLite + WebSocket;
  spawns Claude Code per session in a worktree; tables for outputs,
  messages, diffs; explicit lifecycle states.
  (<https://github.com/stravu/crystal/blob/main/docs/CRYSTAL_ARCHITECTURE.md>)

### Protocols
- **ACP** (Zed, agentclientprotocol.com): JSON-RPC 2.0, agents as stdio
  subprocesses, `session/new|load|prompt|cancel`, agent→client
  `session/update`, client→agent `session/request_permission`. v1 stable;
  experimental **Web Transport** (Streamable HTTP/2 POST+SSE or
  WebSocket), SSE resume deferred.
  (<https://agentclientprotocol.com/protocol/v1/overview>,
  <https://agentclientprotocol.github.io/python-sdk/web-transport/>)
- **AG-UI**: backend↔frontend SSE event protocol (`RUN_*`, `TEXT_MESSAGE_*`,
  `TOOL_CALL_*`, `STATE_SNAPSHOT/DELTA`); generic, not coding-specific.
  (<https://github.com/ag-ui-protocol/ag-ui>)
- **MCP Apps (SEP-1865, Final)**: server-declared `ui://` HTML resource
  rendered in a sandboxed iframe with a postMessage JSON-RPC bridge.
  (<https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/seps/1865-mcp-apps-interactive-user-interfaces-for-mcp.md>)

### PTY mirroring vs structured events
- xterm.js is a renderer only; ttyd/wetty wrap shells over WebSocket. Happy's
  deep-dive documents the real cost of honest PTY mirroring: frame protocol,
  headless xterm snapshot restore, scrollback ring buffer, backpressure,
  PTY lifetime decoupled from socket lifetime
  (<https://github.com/slopus/happy/blob/main/docs/competition/superset/terminal-sync.md>).
- Observed failures in wrappers that mix paths: broken mobile shell, chat vs
  shell state divergence, shell injection, brittle text-scraping for
  approvals.

## 3. Convergence

1. **The agent runs on your machine/host; the UI is a remote.** Local
   server + browser (opencode, Goose, Aider), or wrapper+daemon+relay
   (Happy), or cloud sandbox (Claude cloud, Codex web, Terragon, Omnara).
2. **Structured, ordered, resumable event streams** win over PTY scraping:
   `seq` + `Last-Event-ID` (Omnara), per-user `seq` (Happy), typed item
   lifecycle (Codex), ACP `session/update`.
3. **Approvals are first-class objects**, not prompt text: ACP
   `session/request_permission`, opencode permission responses, Omnara
   `interactions`, Codex server→client requests.
4. **A session is a durable entity** with resume/cancel/fork; UIs render
   from the log, not scrollback.
5. **Transport consensus:** SSE for one-way streams, WebSocket for
   full-duplex, plain POST for control. Auth locally is a shared secret
   (password/cookie/token); cloud uses accounts.
6. **Terminal and web share state** either by making the server own
   sessions and the TUI a client (opencode; Codex's direction) or by
   explicit handoff/remote-control (Claude RC, `--teleport`).
7. **Orchestration (worktrees, kanban, PR handoff) is a separate layer**
   with high churn (Vibe Kanban sunsetting, Crystal deprecated).
8. **PTY mirroring is not what first-party products ship**; it is a
   fallback for wrapping CLIs you do not control.

## 4. What this proposal takes and leaves

Adopted: local loopback server owning sessions (opencode); REST + SSE with
`seq`/`Last-Event-ID` replay (opencode + Omnara); typed event kinds
(Codex lifecycle, Lisa's existing `agent.TurnEvent` vocabulary); approvals
as first-class requests with explicit decisions (all); one runtime, two
front-ends, attach mode (Aider embedded, Claude RC, Codex's stated
direction); pairing link + cookie auth (opencode pair, Goose secret key).

Left out for v1: cloud sandboxes, relays/E2E encryption (Happy), PTY/xterm
mirroring, worktree-per-task orchestration (Conductor/Vibe Kanban), public
share hosting (opencode share), multi-user auth, ACP/AG-UI wire
compatibility (possible later adapter; ACP's web transport is experimental).
