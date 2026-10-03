# Web UI — delivery tasks

Ordered milestones. Each task lists its verification. No checkmarks until
implementation starts; this file is the plan of record for
[spec.md](spec.md).

Contract summary: one loopback HTTP server owning sessions, a REST + SSE
protocol, an embedded no-build SPA, and — before any of that — a shared
turn runtime extracted from the TUI so both front-ends run one state
machine. Every milestone ends with `go test ./...` and `go test -race ./...`
green from the project root.

## M0 — Preconditions

- [ ] **T0.1** Land the in-flight working-tree changes (agent/composer/
      client/model/provider diffs present 2026-10-03) and record the base
      commit; the runtime extraction touches `internal/tui/tui.go`, so it
      must not race uncommitted work.
      *Verify:* `git status` clean except intentional files.
- [ ] **T0.2** Amend `specs/v1-spec.md` with FR-24/FR-25 (text in
      [spec.md §8](spec.md)); confirm this folder's index row in
      `specs/README.md` (added with this proposal).
      *Verify:* spec conventions review (`specs/README.md` rules 2–4).

## M1 — Shared turn runtime (`internal/runtime`), no user-visible change

Why first: `ui.startTurn`/`flushQueue`/`persist`/`autoNameAfterFirstTurn`
and the `TurnEvent` consumer in `internal/tui/tui.go` (≈L176–500, L1108)
are the only implementation of steering-drain, persistence checkpoints,
run-ID filtering, queue flush, and auto-naming. The server must not clone
them. Extracting first also makes M4 (attach mode) a wiring task instead of
a rewrite.

- [ ] **T1.1** Define the runtime API in `internal/runtime`:
      `Session` (one conversation), `Start(prompt)`, `Steer(text)`,
      `Decide(approvalID, approve)`, `Cancel()`, `Compact(focus)`,
      `Subscribe(fn(Event)) func()`, `View() View` (entries, history
      summary, title, run state, usage, spend). Events are typed and
      mirror today's kinds: `text_delta`, `reasoning_delta`, `tool_start`,
      `tool_result`, `approval_request`, `approval_resolved`, `steer`,
      `compacted`, `usage`, `title`, `done`, `error`, `status`.
      Constraints: no `bubbletea`/`lipgloss` import (same rule as
      `internal/agent`); persistence through `session.Store` only.
      *Verify:* package compiles; `grep -L`-style import check in review;
      `go vet ./internal/runtime`.
- [ ] **T1.2** Move the turn lifecycle into the runtime: ctx/cancel,
      run ID, `steer chan` (cap 64) + FIFO queue, `abandon` semantics,
      event fan-out with the same terminal-vs-nonterminal send behavior,
      `tool_result` history adoption + persist, `done`/`error` usage
      accounting (`client.LastTokenUsage`), queue flush, one-shot
      `GenerateSessionName` (20s bound), `persist()` (skipping `Logo` and
      `Queued` rows — the existing rule).
      *Verify:* new table-driven unit tests: steer ordering across both
      drain points, cancel mid-tool, queue flush after error/cancel,
      naming fires once, persistence checkpoint set, `ErrConflict`
      surfaces without clobbering.
- [ ] **T1.3** Migrate the TUI onto the runtime: `ui` keeps only render
      state (scroll, layout, composer, dialogs, review focus/seen, theme,
      popups, caches) and subscribes to runtime events; delete the moved
      fields (`history`, `queue`, `steer`, `events`, `abandon`, `cancel`,
      `runID`, `working`, `streamBuf` handling stays render-side).
      *Verify:* existing suite (`go test ./...`, `go test -race ./...`),
      including `permission_e2e_test.go` and `steering_e2e_test.go`,
      passes unmodified except for mechanical message-type renames;
      real-TUI walkthrough: one streaming turn, one queued steering
      prompt, one approved edit, one declined command, `/compact`,
      resume — behavior identical to the base commit.
- [ ] **T1.4** Update `ARCHITECTURE.md`: add `internal/runtime` to the
      diagram and the owns/may-not-import table (`tui` and the future
      `server` both import it; it imports `agent`, `model`, `session`,
      `repository`, `mcp`; never `tui`/`bubbletea`).
      *Verify:* diagram and table match the actual imports.

## M2 — `likha --serve` MVP: the browser drives a full turn

- [ ] **T2.1** `internal/app`: add `--serve`, `--port`, `--host`, `--token`
      (env `LIKHA_SERVE_TOKEN`), slotted before the `!interactive` gate
      (same composition as the TUI: store, providers, client, repo, MCP).
      Default bind `127.0.0.1`, ephemeral port; print
      `http://127.0.0.1:<port>/#pair=<token>`; refuse non-loopback without
      a token with a warning; `Ctrl+C` cancels runs and exits 0.
      *Verify:* `tests/integration` CLI tests: help text, refusal cases,
      `--port 0` URL parse, clean shutdown.
- [ ] **T2.2** `internal/server`: router (`net/http` mux), auth middleware
      (pair token → `HttpOnly` `SameSite=Strict` cookie, single-use,
      per-start), `Origin`/`Sec-Fetch-Site` check on mutations, no CORS,
      no secrets in logs, static asset handler with `go:embed` +
      `http.FileServerFS`, SPA fallback to `index.html`.
      *Verify:* unit tests for pair success/failure/replay, cookie flags,
      foreign-origin rejection, asset fallback.
- [ ] **T2.3** Session manager: per-session runtime actor keyed by ID,
      `List`/`Create`/`Load` from `session.Store`; one driver per session
      (a second prompt while running is a steer, mirroring the TUI);
      `ErrConflict` mapped to a visible 409 + banner; server shutdown stops
      actors cleanly (persisted state only).
      *Verify:* unit tests for create/list/load, concurrent prompt →
      steer, conflict mapping, shutdown persistence.
- [ ] **T2.4** Protocol v1 (appendix below): handlers + SSE hub with a
      per-session ring buffer (512 events), monotonic `seq`, `Last-Event-ID`
      replay, and a stall policy (slow client disconnected with a
      `reconnect` hint; no unbounded buffering).
      *Verify:* `httptest` tests: stream ordering, replay after
      `Last-Event-ID`, overflow → disconnect, snapshot endpoint parity
      with replayed state.
- [ ] **T2.5** `internal/agent` additive fields so the web renders
      structured cards without parsing display strings: `TurnEvent` gains
      `ToolName`, `ToolArguments`, `ToolResult`; `ApprovalRequest` gains
      `Path`, `Command`, `Server`, `Tool` (empty for the kinds that do not
      apply). No behavior change; TUI ignores them.
      *Verify:* agent unit tests for populated/empty fields on each tool
      path; TUI suite unchanged.
- [ ] **T2.6** SPA (vanilla ES modules, no build): `index.html`, `app.js`
      (transcript renderer, SSE client, session store), `styles.css`
      (role bands, diff, cards, responsive), vendored `marked` +
      `DOMPurify` with pinned versions and a license/checksum note. Views:
      session list, transcript (snapshot + live deltas), composer,
      streaming, cancel, approval cards (edit diff with `+N −M`, command
      card with cwd + warning), header status (provider · model · ctx ·
      spend · live dot).
      *Verify:* browser walkthrough against a `httptest` fake provider
      (existing SSE fixture pattern) on a real turn; `go test` asserts the
      embed contains every referenced asset (no 404s).
- [ ] **T2.7** Integration tests: real `session.Store` + fake provider +
      HTTP client: create → prompt → stream → approval → approve → tool
      result → done; decline path; cancel mid-run; restart → resume with
      history intact.
      *Verify:* `go test ./internal/server/... ./tests/integration/...`;
      browser walkthrough with a real provider for one edit + one command.

## M3 — Daily-driver parity

- [ ] **T3.1** Steering UI: Queue action while running, `Queued` rows,
      queued count, delivery confirmed by `steer` events; leftover queue
      runs as the next turn exactly like `flushQueue`.
      *Verify:* browser walkthrough with a slow fake provider; protocol
      test for queue-then-cancel.
- [ ] **T3.2** `@` mentions (repo file/folder listing endpoint, same
      gitignore-aware source as the TUI) and `/` command popup with
      `/compact`, `/models`, `/help`; commands never reach the model;
      unknown commands show an error.
      *Verify:* endpoint tests; browser walkthrough of a folder mention
      inlining the tree.
- [ ] **T3.3** Model switcher (all providers grouped, filter, mid-session
      switch persisted with `SaveStoredConfig`) and `/providers` key modal
      (masked entry, live check, stored-key state).
      *Verify:* protocol tests; browser walkthrough switching provider and
      model.
- [ ] **T3.4** Status fidelity: ctx usage, tokens, spend, session title
      (auto-name event), reasoning collapse, compaction marker, update
      notice — each shown exactly when the TUI would show it, `ctx —`
      otherwise.
      *Verify:* fixture tests with known usage; browser screenshot review.
- [ ] **T3.5** Reconnect and multi-tab: `Last-Event-ID` resume, duplicate
      suppression, first-answer-wins approvals across tabs, running badge
      in the session list.
      *Verify:* two-tab walkthrough; protocol tests for double decision
      (409).
- [ ] **T3.6** Responsive layout at 360–900px and theme mapping from
      `config.json` theme name to CSS variables (families where the
      palette is representable; `default` is plain).
      *Verify:* phone-width walkthrough (read, queue, cancel, approve,
      decline); theme matrix spot-check.

## M4 — Attach mode and polish

- [ ] **T4.1** `likha --web`: start the in-process server alongside the TUI
      sharing one runtime; browser mirrors and drives the live session
      (steer/cancel/approve from either surface, no store conflict because
      one process owns the run); `--port/--host/--token` reuse.
      *Verify:* TUI + browser walkthrough: stream visible in both, steering
      from the browser lands in the terminal transcript and vice versa.
- [ ] **T4.2** `GET /api/sessions/{id}/export.html`: self-contained static
      transcript (inline CSS, no scripts) for offline sharing.
      *Verify:* export opens offline and matches the session.
- [ ] **T4.3** Optional/deferred: OpenAPI description of the HTTP surface;
      web first-run setup wizard; image attachments (FR-18); ACP/AG-UI
      adapter. Each gets its own spec before implementation.

## Appendix A — Protocol v1 (draft)

All endpoints are JSON under the server origin; all require the pairing
cookie except `POST /api/pair` and `GET /` (the SPA shell).

| Method | Path | Body / notes |
| --- | --- | --- |
| POST | `/api/pair` | `{token}` → 204 + cookie; 401 on bad/replayed token |
| GET | `/api/meta` | `{name, version, root, branch, provider, model, theme}` |
| GET | `/api/sessions` | `{sessions:[{id,title,updated,running}]}` |
| POST | `/api/sessions` | → `{id}` (fresh session) |
| GET | `/api/sessions/{id}` | snapshot: `{id,title,entries,run:{state,queued},usage,spend,provider,model}` |
| GET | `/api/sessions/{id}/events` | SSE; `id:` = `seq`; `Last-Event-ID` replay |
| POST | `/api/sessions/{id}/prompt` | `{text}` → `{queued:bool}`; 409 while a decision is pending |
| POST | `/api/sessions/{id}/cancel` | cancels the active run |
| POST | `/api/sessions/{id}/approval` | `{approval_id, decision:"approve"\|"decline"}`; 409 if already resolved |
| POST | `/api/sessions/{id}/model` | `{provider, model}`; applies + persists |
| POST | `/api/sessions/{id}/compact` | `{focus?}` |
| GET | `/api/repo/files` | mention candidates (gitignore-aware) |
| GET | `/api/models` | grouped provider/model catalog |

SSE frame: `event: turn`, `data: {seq, kind, run, …}` with kinds
`text_delta{text}`, `reasoning_delta{text}`, `tool_start{tool,args}`,
`tool_result{tool,result,ok}`, `approval_request{approval_id,kind,title,path?,command?,body}`,
`approval_resolved{approval_id,decision}`, `steer{text}`, `compacted{}`,
`usage{prompt_tokens,completion_tokens,window?}`, `title{title}`,
`done{}`, `error{message}`, `status{state}`. Errors use
`{error:{code,message}}` with 400/401/404/409.

Design rules: the server never ships raw `History` blobs (the agent's
`tool_result`/`done` events carry full history copies — the runtime keeps
them server-side and emits deltas plus entry appends); the snapshot
endpoint is authoritative after any reconnect; event IDs are per session.

## Appendix B — Layout and constraints

```
internal/runtime/     turn lifecycle (M1)
internal/server/      HTTP + SSE + session actors + go:embed assets (M2)
internal/server/assets/  index.html, app.js, styles.css, vendor/ (M2)
internal/app/run.go   --serve / --web wiring (M2/M4)
```

- `internal/server` imports `runtime`, `providers`, `model`, `session`,
  `repository`, `mcp`; never `tui`/`bubbletea`. `runtime` never imports
  `server`.
- Frontend: native ES modules, no build step; vendored libraries are
  pinned and licensed; `go:embed` makes the binary self-contained.
- Testing entry point stays `go test ./...`; browser verification is a
  documented manual walkthrough (repo convention), with fake-provider
  Go tests covering the protocol.

## Appendix C — Risks

1. **TUI refactor risk (M1).** Largest single risk; mitigated by doing it
   first, before new surface area exists, with the existing e2e suite as
   the contract. If the TUI migration proves too invasive mid-flight, the
   documented fallback is a server-local runner plus conformance tests —
   accepted as debt, not silently.
2. **Local privilege surface.** Loopback + token is the opencode model;
   the spec documents that any local process can pair while the token is
   live, and non-loopback binds require explicit opt-in.
3. **Frontend weight.** No-build vanilla keeps `go build` as the only
   toolchain; the cost is hand-written DOM code and vendored markdown
   libraries (sanitized, pinned).
4. **Scope creep into orchestration.** Worktrees/kanban/PR handoff are a
   different product layer with high market churn; explicitly out.
