# Context: Likha web UI (`likha --serve`)

Code paths this feature touches. The behavior in [spec.md](spec.md) is the
contract; the order of work is [tasks.md](tasks.md).

## Existing code the web UI consumes unchanged

- `internal/agent/agent.go` — `RunTurn(ctx, client, repo, root, prior,
  prompt, mcp, steer, emit)` is the whole engine: SSE streaming with
  `text`/`reasoning` callbacks, the 32-round tool loop, `steer` drains at
  provider-call boundaries (`drainSteer`), `tool_result`/`done`/`error`
  events, and `requestApproval` — a `TurnEvent{Kind:"approval"}` carrying
  `ApprovalRequest{Kind, Title, Body, Reply chan bool}` that blocks the run
  goroutine until a decision or `ctx` cancellation. Also UI-free:
  `ExpandFileReferences` (`@` mentions), `CompactHistory`,
  `GenerateSessionName`.
- `internal/session/session.go` — `Store.Open/Create/List/Load/Save`,
  schema v1 (`sessions` table: `id, repository, updated_ns, title,
  snapshot BLOB, revision`), WAL, single connection, optimistic concurrency
  (`Save` compares revision+snapshot, else `ErrConflict`); `Snapshot{ID,
  Root, History, Entries []Entry, NamedTitle}` and `Entry{Role, Content}`
  (the persisted, displayable transcript rows).
- `internal/providers/provider.go`, `config.go`, `keyfile.go` —
  `ResolveProvider`, `Connection`, `Load/SaveStoredConfig`,
  `Store/StoredKey/OAuth`, private state paths. No TTY assumptions.
- `internal/model/client.go` — `New`/`NewOAuth`, `EnsureConnected`,
  `Stream`, `SetModel`, `SetSession`, `LastTokenUsage`.
- `internal/repository` — gitignore-aware listing and `Glob/Read/Grep`
  used by tools and the mention popup.
- `internal/actions` — `PrepareEdit`/`Apply`, `RunCommand` (pgid-isolated,
  256 KiB output cap). Approval orchestration stays in `agent`.
- `internal/app/run.go` — composition root: flags, `resolveModel`,
  `resolveRoot`, store/provider/client/repo/MCP construction, and the
  `interactive` gate before the single `tea.NewProgram` call. `--serve`
  slots in before that gate; `--sessions`/`--device-login` are the
  precedents for headless modes.

## Code this feature moves or adds

- `internal/tui/tui.go` — `ui.startTurn` (~L176: allocates `events` cap 64,
  `abandon`, `steer` cap 64, stamps `RunID`), the `agent.TurnEvent` case
  (~L429: run-ID filter, `text`/`reasoning` buffering, `approval`,
  `tool_result` history adoption + persist, `steer`, `compacted`,
  `done`/`error` usage accounting), `enqueue`/`flushQueue`,
  `startCompaction`, `autoNameAfterFirstTurn`, `persist` (L1108: skips
  `Logo` and `Queued` rows), and the core fields of `ui` (`history`,
  `entries`, `queue`, `steer`, `events`, `abandon`, `cancel`, `runID`,
  `working`, usage/spend, `snapshot`). M1 moves the lifecycle half into
  `internal/runtime` and leaves render state in `tui`.
- `internal/runtime/` (new) — one `Session` per conversation owning start/
  steer/cancel/decide/compact, event fan-out, persistence checkpoints,
  queue flush, and auto-naming. Must not import `bubbletea`/`lipgloss`/
  `tui`/`server` (same discipline as `internal/agent`).
- `internal/server/` (new) — HTTP router, auth middleware, session actors,
  SSE hub with per-session `seq` + `Last-Event-ID` replay ring,
  `go:embed` assets. Imports `runtime`, `providers`, `model`, `session`,
  `repository`, `mcp`; never `tui`.
- `internal/server/assets/` (new) — `index.html`, `app.js`, `styles.css`,
  `vendor/` (pinned `marked`, `DOMPurify` + license/checksum note). Native
  ES modules; no build step; embedded into the binary.
- `internal/agent/agent.go` (additive) — `TurnEvent.ToolName/
  ToolArguments/ToolResult`, `ApprovalRequest.Path/Command/Server/Tool`
  so cards render from structure, not from parsing display strings. TUI
  ignores the new fields.
- `ARCHITECTURE.md` — add `internal/runtime` and `internal/server` to the
  diagram and owns/may-not-import table; note that `tui` and `server` are
  sibling front-ends.
- `cmd/likha`/`internal/app` — `--serve`, `--port`, `--host`, `--token`
  (M2); `--web` attach mode (M4).

## Tests to extend

- `internal/tui/tui_test.go`, `permission_e2e_test.go`,
  `steering_e2e_test.go`, `composer_test.go`, `status_view_test.go` — the
  behavior contract for M1; expected to pass with only mechanical
  message-type changes after the runtime extraction.
- `internal/agent/agent_test.go`, `approval_test.go` — scripted `httptest`
  SSE provider with call counters; the pattern for M2 protocol tests and
  for asserting the new structured fields.
- `tests/integration/cli_test.go` — black-box `go run ./cmd/likha` with
  `LIKHA_STATE_DIR` isolation; extend for `--serve` startup/refusal cases.
- New: `internal/runtime/*_test.go` (steer ordering, cancel, persistence
  checkpoints, naming, conflict), `internal/server/*_test.go` (auth, SSE
  replay/overflow, session actors, approval decisions), plus an
  end-to-end HTTP test driving a full turn through the real store and a
  fake provider.
- Browser verification is a documented manual walkthrough (repo
  convention), not a committed browser suite; committed tests stay Go.

## Related specs

- [v1-spec.md](../v1-spec.md) — FR-24/FR-25 are added (text in
  [spec.md §8](spec.md)); FR-14 (scroll/follow rules) and FR-22
  (Approve/Decline with the read-to-end gate) are restated for the browser,
  not amended.
- [permission-ui/](../permission-ui/spec.md) — the decision bar's labels and
  gate are the browser card's contract.
- [steering-prompts/](../steering-prompts/spec.md) — queue/drain semantics
  the web composer must reproduce; the runtime extraction must not change
  them.
- [conversation-compaction/](../conversation-compaction/spec.md) — the
  `compacted` marker and `/compact` behavior.
- [tui-layout/](../tui-layout/spec.md), [adaptive-themes/](../adaptive-themes/spec.md),
  [context-tracker/](../context-tracker/spec.md) — status segments and
  theme families the web header mirrors.
- [file-references/](../file-references/spec.md) — `@` expansion and the
  gitignore-aware listing the web picker reuses.
- [providers-connect/](../providers-connect/spec.md) — the `/providers`
  key-entry flow the M3 web modal mirrors.
- [structure-refactor/](../structure-refactor/spec.md) — package discipline
  this feature extends (sibling front-ends over a shared engine).
- [README.md](../README.md) — "Work in the terminal" stays the primary
  story; a "Web UI" section is added at release.

## Precedent

- External: opencode (local server owns sessions; REST + SSE; pair link),
  Codex App Server (typed item lifecycle; server-initiated approvals),
  Claude Code Remote Control (local process stays host; browser is a
  window), Omnara (`seq` + `Last-Event-ID`), Aider `--browser` (embedded
  in-process UI). Recorded with sources in [research.md](research.md).
- House: `--device-login` (headless mode beside the TUI), the
  `Reply chan bool` approval contract, the `steer` channel + `abandon`
  pattern, and the `session.ErrConflict` rule are the shapes this feature
  reuses rather than reinvents.
