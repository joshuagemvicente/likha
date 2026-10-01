# Context: repository structure refactor (package split)

Code paths and facts this refactor touches. **No behavior changes** — nothing
below describes a user-visible change. Spec is the deliverable; this file is
the ground truth the implementer verifies against before moving a line.

## Repo topology (verified 2026-09-30)

Module `lisa`. Go source lives in: `cmd/lisa/main.go` (11 lines, calls only
`app.Run`), `internal/` (8 packages), and root `ui/`. Leaf packages are
stable and out of scope: `actions`, `mcp`, `model`, `repository`, `session`,
`update` (all real code with colocated tests). `tests/integration/cli_test.go`
pins CLI behavior. `dist/` and `.cache/` are gitignored build artifacts; 158
tracked files. `go list ./...` packages:

```
lisa/cmd/lisa, lisa/internal/{actions,app,mcp,model,repository,session,skills,update},
lisa/tests/integration, lisa/ui
```

Dependency direction verified: nothing imports `lisa/internal/app` except
`cmd/lisa`; `app` imports everything below it. No cycles exist, so the
refactor is re-homing, never re-wiring.

## Phase 1 — exact file inventory

**A. `ui/` → `internal/ui`.** Two files, both `package ui`, no subpackages:
`ui/glyphs.go` (32 lines, `Glyphs` + theme-adjacent markers), `ui/theme.go`
(147 lines, seven style roles, doc comment lives on line 1 of this file —
keep it; it is the package doc). Zero test files. Consumers found by grep
(four, all `lisaui "lisa/ui"` aliased — the alias tells the story):

- `internal/app/run.go:21` — `lisaui.Named(themeName, true)` at line 222
- `internal/app/tui.go:23` — fields `theme lisaui.Theme`, `glyphs
  lisaui.Glyphs` (lines 76-77); call sites: `lisaui.ThemeNames()`
  (184, 412, 914, 1845), `lisaui.Resolve` (249, 558, 898),
  `lisaui.HasDarkBackground()` (248, 558, 898), `lisaui.AsciiGlyphs()` (250),
  `lisaui.NerdGlyphs()` (252)
- `internal/app/status_markers_test.go:10` — `lisaui.NerdGlyphs().Waiting/
  Review` (34-35), `lisaui.ThemeNames/Resolve` (63-64)
- `internal/app/tui_muted_tools_test.go:10` — `lisaui.ThemeNames/Resolve`
  (89-90)

Because the alias exists at every call site, the mechanical change is
`git mv ui internal/ui` + `s|lisa/ui|internal/ui|g` on exactly these four
files, with the alias deletable (no other `ui.` symbol in scope collides —
verified: `grep ui\.` shows only `lisaui.` uses plus prose).

**B. Delete `internal/skills`.** Single file `internal/skills/skills.go`
(4 lines, `package skills`, placeholder comment pointing at
`specs/slash-commands/spec.md`). Verified zero importers (`grep -rln
'internal/skills'` → empty). Deleting the directory removes the package from
`go list`; no import line changes anywhere. The inert `/skills` reservation
in the TUI command path (`tui.go:handleCommand`) is TUI behavior and stays.

**C. Status test consolidation.** Source files: `internal/app/status_line.go`
(411 lines; `(m *ui) contextSegment` at 111; renders status/footer and header
composition) and `internal/app/status_sources.go` (245 lines; pure funcs:
`statusFolder`, `homeRelative`, `gitStatus`, `detachedHead`,
`parseGitStatus/Header`, `parseGitNumber`, `readSmallFile`,
`statusSessionTitle`, `singleLineTitle` — zero Bubbletea imports). Test files
today:

- `status_line_test.go` (363 lines) — 5 tests, all status/footer behavior:
  `TestStatusViewportAcrossComposerStyles`, `...OptionalOrderAndNarrowControls`
  (52), `...LongDraftAndMentionPopupKeepTranscriptVisible` (120),
  `...ReviewPagesRemainReachableAfterResize` (140).
- `status_sources_test.go` (422 lines) — pure-source funcs:
  `TestStoredStatusLineConfigRoundTripAndLegacy` (16),
  `TestStatusFolderHomeBoundary` (65), `...SymlinkedHome` (81),
  `TestStatusSessionTitleEntryHistoryAndFallback` (97).
- `statusbar_test.go` (531 lines) — 14 tests exercising `tui.go` `ui` methods,
  NOT status files: `m.header`, `m.statusTitle`, `m.statusLineRows`,
  `m.spendSegment`/`m.contextSegment`, `m.startTurn`, `m.cancel`,
  `m.abandon` (17× `m.Update`, `m.View`, `m.entries`). Examples:
  `TestHeaderCollapsesToFiftySixColumns` (41), `TestLogoEntryIsPureLogo`
  (70), `TestLisaMarkPlacementAndRetirement` (84),
  `TestContextSegmentFormats` (113).
- `status_markers_test.go` (88 lines) — 3 tests, `ui` rendering via
  `m.Update`/`m.View`/`m.theme`: `TestStatusIdentityShowsUnverifiedWarning`
  (16), `TestNerdMarkersOnlyWithOptIn` (33),
  `TestErrorEntriesRenderThemeError` (62).

Mapping key (decided now, not at implementation time): rename
`statusbar_test.go` → `tui_status_test.go`?? **No.** Cheapest true-to-name
assignment: its subjects all live in `tui.go`, so the honest home is
`tui_test.go` merge — but `tui_test.go` is already 1,812 lines and merging
risks collision review. Decision: rename to `status_view_test.go` with zero
function-name changes (all 14 names are unique project-wide; only
`package`-line untouched). Likewise `status_markers_test.go` →
`status_markers_test.go` keeps its name (it tests marker/theme contract —
name is accurate; its `lisaui` import changes per item A). Net: one file
rename, zero function churn, and afterward every `status_*_test.go` name
describes its subject.

## Phase 2 — per-file split assignment (decided now)

`internal/app` non-test sources (17) and their Bubbletea coupling, measured
by non-test `github.com/charmbracelet` imports plus `*ui` receiver presence:

Pure (no `tea`/`lipgloss`, import only stdlib + `lisa/internal/{model,…}`):

- `agent.go` (227 lines): `approvalRequest`, `turnEvent{kind,text,runID,
  history,approval}`, `runTurn` (42), `dispatchTool` (122),
  `requestApproval` (218). `runTurn` diffuses through exactly one seam:
  `emit func(turnEvent)`; the TUI injects a closure at `tui.go:609`
  (`startTurn`), `tui.go:573` `waitEvent`, receives at `tui.go:1031` and
  stores `pending *approvalRequest` (108). This closure/channel pair IS the
  `agent↔tui` interface — moving `agent.go` whole requires only that the two
  type names travel with it. → **`internal/agent`** + `agent_test.go`,
  `approval_test.go`.
- `compaction.go` (69 lines): `compactHistory(ctx, client, history, focus,
  onText)` — one pure function + its test. → **`internal/agent`**.
- `sessionname.go`: `generateSessionName` (+ `sanitizeSessionName`) — pure
  model call, `*model.Client` in, `(string, error)` out. → **`internal/agent`**
  + `sessionname_test.go`.
- `config.go`: `storedComposerConfig`, `storedStatusLineConfig`,
  `storedProviderConfig`, `flagEnabled`, `configFilePath`,
  `load/saveStoredConfig`. Consumed by both `run.go` and status/theme code —
  belongs with config ownership. → **`internal/providers`**.
- `keyfile.go`: `keyFilePath`, `storedCredential` (schema v2),
  `read/writeCredentials`, `storedKey/OAuth`, `storeKey/OAuth`. Imports
  `lisa/internal/model` only for `OAuthCredentials`. → **`internal/providers`**
  + any keyfile tests.
- `provider.go`: `connection` struct (display/verified/err/setup/**theme/
  composerStyle/statusLine/nerd/mcp** — note the struct already aggregates
  UI prefs, which is exactly why it configures both targets),
  `resolvedProvider`, `resolveProvider(...)`. → **`internal/providers`**.

Split-required (both layers in one file, `*ui` methods + pure logic):

- `mentions.go` (281 lines). Pure half: `mentionToken`, caps
  (`mentionMaxTotal/Files/Index/Listing`), `expandFileReferences(prompt,
  repo)` (41 — called by `agent.go:45` as the FIRST line of `runTurn`, i.e.
  agent-side consumption), `buildFileIndex`, `mentionQuery`,
  `mentionMatches`, `completeMention`. UI half: `mentionState`,
  `fileIndexMsg`, `(m *ui) startMention/syncMention/mentionActive/
  updateMention/mentionLines`. Cut between `completeMention` (144) and
  `mentionState` (154). Pure half + `mentions_test.go` expansion cases →
  **`internal/agent`**; UI half stays (lands in Phase-2 `internal/tui`).
- `providers.go` (418 lines). Pure half: `switchModelID`,
  `customEndpointTarget`, `activateProvider`?? — verify at implementation:
  catalog resolution + `resolveModel`-adjacent logic (see `run.go:333`
  `resolveModel(name, p model.Provider, endpoint, key)` — stays anchor).
  UI half: `keyState`, `keyCheckMsg`, `(m *ui) openKeyModal/closeKeyModal/
  updateKeyModal/handleKeyCheckMsg`, `startProviderSwitch`,
  `providerSwitchMsg`, `handleProviderSwitchMsg`, `handleProvidersCommand`,
  `applyProviderDirect`, `providersDialogItems`, `keyModalView`. The pure
  boundary is drawn where `*ui` receivers end — anything returning or taking
  `tea.Cmd`/`tea.KeyMsg` stays. → UI stays, data moves to
  **`internal/providers`**.
- `mcp.go` (217 lines): `mcpManager` (mu/path/servers), `newMcpManager`,
  `lookup`, `Tools`, `Call(ctx,…, approve func…)` (!), `Status`, `Summary`,
  `Trusted`, `Stop`, plus alias `toolDefinition = model.ToolDefinition`.
  No `tea`. `Call`'s `approve` callback is the seam: `dispatchTool`
  (agent.go:205) passes the `requestApproval` closure; TUI's `/mcp` path
  passes its own. → move whole to **`internal/mcp`**; `agent` imports the
  manager type only. Zero signature changes.

UI (stays together, becomes `internal/tui`):

- `tui.go` (2,207 lines, the `ui` struct at 70 + `newUI` 246 + `Update` 1012
  + `View`/`mainView` 1714/1737), `composer.go`, `editor.go`, `scroll.go`
  (257 lines), `status_line.go`, `status_sources.go`,
  `commandcomplete.go` (`(m *ui) updateCommand`), providers modal half,
  mentions UI half, `run.go`'s program construction?? → **`run.go` stays in
  `internal/app`** as the composition root: it builds `newUI(..., connection
  {...}, ...)` (line 321) — the constructor call site is the one place all
  three new packages meet, and that place remains `app`. `run.go`'s helpers
  (`usageText`, `Run`, `resolveModel`, `resolveRoot`, `deviceLoginFlow`)
  keep their signatures; `Run(args, stdout, stderr) int` is the unchanged
  contract `cmd/lisa/main.go` calls.

## Phase-2 tests land with their subjects

Known test files: `agent_test.go`, `approval_test.go`, `compaction_test.go`,
`mentions_test.go` (split to match mentions cut), `sessionname_test.go`,
`session_test.go` (sessions dialog/command — `*ui`, → tui),
`models_sessions_dialog_test.go`, `dialog_test_helpers_test.go`,
`models_*`, `themes_modal_test.go`, `run_test.go`, `mcp_test.go` (manager
cases → `internal/mcp`), `providers_test.go` (812 lines — split with
providers.go by symbol), `composer*_test.go`, `editor_test.go`,
`scroll_test.go`, `status_*` (consolidated in Phase 1, → tui),
`tui_test.go` + `tui_m2_keys_test.go` + `tui_muted_tools_test.go`
(M1/M2-era suites — **naming note**: milestone-suffixed names are process
residue; phase 2 renames to subject names `tui_keys_test.go` /
`tui_tools_test.go` while `tool-rendering-terminal-keys` is `in progress`,
with that spec's checklist as the authority that nothing regressed).

## Decisions already taken (implementer does not re-decide)

1. Target names are `internal/agent`, `internal/providers`, `internal/tui`
   — not `core`, not `service`, not `pkg/`. `internal/app` remains as the
   composition root; it is not deleted.
2. `run.go` stays in `internal/app`. The `tea.NewProgram` call site (321)
   and `newUI` constructor stay exactly where they are.
3. The agent↔tui event contract (`turnEvent`, `approvalRequest`,
   `emit func(turnEvent)`, `waitEvent`, `events chan turnEvent` buffered 64
   at `tui.go:600`) moves with `agent.go`; the TUI keeps its
   channel/closure half. Approval semantics come from
   [v1-spec.md](../v1-spec.md) FR-06/07/08 — unchanged, so `approval_test.go`
   is the oracle.
4. MCP manager moves to `internal/mcp` (not `agent`): it is server lifecycle,
   and two callers (`dispatchTool`, `/mcp` UI path) keep the seam real.
5. `cmd/lisa/main.go` is untouched in both phases.
6. Status-line test inventory post-Phase-1: exactly `status_line_test.go`
   (footer/view behavior), `status_sources_test.go` (pure funcs),
   `status_view_test.go` (14 tests, ex-`statusbar_test.go`),
   `status_markers_test.go` (3 tests). Phase-2 tui package inherits all four.
