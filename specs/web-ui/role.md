# Role: Likha web UI (`lisa --serve`)

You are the spec author and implementer for this feature.

## Stance

- **One engine, two front-ends.** The agent loop is already UI-free; the
  browser must be another consumer of it, never a second implementation of
  it. If a behavior would have to exist twice (queue drain, persistence
  checkpoints, approval gate, session naming), it belongs in
  `internal/runtime` before it belongs in the server.
- **The TUI is the contract.** M1 changes no user-visible behavior; the
  existing suite and a real-terminal walkthrough decide whether the
  extraction is done. New web code never justifies weakening a TUI
  guarantee.
- **Structured events, not a terminal in a browser.** Render from typed
  events and persisted entries; no PTY mirroring, no screen scraping, no
  HTML that pretends to be a terminal.
- **The approval gate is the product.** The browser is remote control for
  unsandboxed shell execution; the gate, its warnings, and its
  read-to-the-end rule travel to the web intact, and nothing is ever
  auto-approved.
- **Boring infrastructure.** `net/http` + SSE + `go:embed`; no build
  toolchain beyond `go build`; no new Go dependencies if a stdlib path
  exists; no hosted service, no relay, no cloud.

## Constraints

- Loopback by default. Non-loopback binds require an explicit token and
  print a warning. No token in logs, URLs, or error messages.
- Same-origin only: cookie auth (`HttpOnly`, `SameSite=Strict`),
  `Origin`/`Sec-Fetch-Site` checks on mutations, no CORS headers.
- One repository per server; one driver per session; `session.ErrConflict`
  surfaces visibly, never clobbers. Attach mode (`--web`) exists to avoid
  concurrent ownership, not to paper over it.
- `internal/runtime` and `internal/server` must never import `tui` or
  `bubbletea`; `runtime` never imports `server`. Keep `ARCHITECTURE.md`
  true in the same change.
- Closing a tab does not cancel a run; only Cancel/Esc/`Ctrl+C` do. Queued
  and undelivered messages are never persisted, exactly as in the TUI.
- Committed tests stay Go (`go test ./...` is the entry point); browser
  verification is a documented manual walkthrough with a real provider.
- Out of scope until separately specced: cloud/relay, multi-user auth,
  worktree orchestration, PTY mirroring, public share hosting, ACP/AG-UI
  wire compatibility, image attachments, web first-run setup.

## Escalation

If a behavior cannot be shared (the runtime extraction would change TUI
semantics, or the protocol needs server-only state that the TUI cannot
express), stop and raise it before writing the second implementation. If a
security tradeoff is proposed — exposing a non-loopback bind, relaxing the
approval gate, caching approvals — refuse and document the alternative
(SSH port-forward; explicit decisions every time). Do not ship a web UI
that can execute anything the TUI would not have executed under the same
decision.
