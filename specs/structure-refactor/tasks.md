# Tasks — repository structure refactor

Ordered implementation tasks. Each ends with a verification step; no
checkmarks until the step is done. Automated entry point: `go test ./...`
from the project root. **Zero behavior change is the contract**: any task
that alters a user-visible string, keybinding, approval outcome, or file
write is a spec violation — stop and amend `spec.md` first.

Phase 2 starts only after the Phase 1 gate is green. Within Phase 2, tasks
run in order; each keeps `go build ./...` green so the work is committable
per task.

## Phase 0 — baseline (before any move)

1. **Register and snapshot.** Add the feature-index row to `specs/README.md`:
   `Repository structure refactor (phased package split)` →
   `[structure-refactor/](structure-refactor/spec.md)` → `planned`.
   Record the baseline: `go build ./...`, `go vet ./...`, `go test ./...`,
   `go test -race ./...`, `gofmt -l .` (expect clean), `go list ./...`.
   Save outputs; every later gate diffs against them.
   Verified: index row renders; baselines captured; nothing else touched.

## Phase 1 — mechanical moves

2. **`ui/` → `internal/ui`.** [x] DONE 2026-10-01 — `git mv ui internal/ui`
   (files keep names `glyphs.go`, `theme.go`; the package doc on
   `theme.go:1-3` travels untouched). Import line rewritten in exactly four
   files — `internal/app/run.go:21`, `internal/app/tui.go:23`,
   `internal/app/status_markers_test.go:10`,
   `internal/app/tui_muted_tools_test.go:10` — from
   `lisaui "lisa/ui"` to `lisaui "lisa/internal/ui"`, **keeping the alias**:
   the `ui` struct type in `internal/app` shadows a bare `ui` package
   qualifier (verified by compiler error on the alias-drop attempt).
   No other line changes.
   Verified: `grep -rn 'lisa/ui' --include='*.go' .` → empty;
   `go list ./...` shows `lisa/internal/ui`, no `lisa/ui`;
   `go build ./... && go test ./internal/app/`.

3. **Retain `internal/skills` (deletion struck).** [x] DONE 2026-10-01 —
   zero importers verified (`grep -rln 'internal/skills'`, `.cache`
   excluded); the 4-line placeholder left in place: `custom-commands`,
   `text-transforms`, and `slash-commands` depend on the path existing
   (see spec.md Phase 1 §2 and `architecture-phase-1.md` §6).
   Verified: `go list ./...` still shows `lisa/internal/skills`;
   `go build ./...`.

4. **Rename `statusbar_test.go` → `status_view_test.go`.** [x] DONE 2026-10-01
   — `git mv` only; all 14 test function names unchanged (uniqueness
   pre-verified). Rationale is in `context.md`: subjects are `tui.go` view
   methods, and the name finally says so. `status_markers_test.go` keeps
   its name (accurate).
   Verified: `go test ./internal/app/ -run 'TestHeader|TestLogo|TestLisaMark|
   TestContextSegment|TestStatus' -v` passes with identical test names
   (23 matched tests green).

5. **Phase 1 gate.** [x] DONE 2026-10-01 — `go build ./...` green, `go vet`
   on project trees green, `go test ./...` green except the two pre-existing
   logo-asset failures recorded in spec.md (baseline-identical, in-flight
   user work), `go test -race` green on the moved-package consumer suites
   (`TestNerd*`, `TestErrorEntries*`, `TestToolEntries*`, `TestStatus*`),
   `gofmt -l` clean except the two pre-existing in-flight flags
   (`run.go`, `tui.go`). Diff audit: `git diff -M --stat` shows the `ui/`
   relocation (similarity-detected renames), four import lines, one test
   rename with 0 insertions/deletions, and three doc lines — no function
   body changed by this refactor. Phase 1 `implemented (local)`.

## Phase 2 — split `internal/app` (contract first, then moves)

6. **T0 — freeze the cross-package contract.** Before moving any file, audit
   `connection` field traffic: `grep -n 'conn\.' internal/app/*.go` (reads
   and writes — known sites include `run.go:321` construction with all ten
   fields, `providers.go:236` and `tui.go:544` rebuild-literals preserving
   theme/nerd/mcp/composerStyle, `status_line.go:184` `m.conn.mcp.Summary()`,
   `tui.go:606,724` `m.conn.mcp`). Same audit for `resolvedProvider`
   (`grep -n 'res\.' internal/app/run.go` — known: `res.key` in tests) and
   for keyfile/config symbols (call-site tables are in `context.md`). Then
   write the contract as the package headers demand it:
   - `internal/providers`: exported `Connection` (unexported fields) +
     constructor covering all three construction sites' fields +
     accessors/mutators for every audited `conn.` use; `ResolveProvider`
     (+ exported result type/fields); `LoadStoredConfig`,
     `SaveStoredConfig`, `StoredConfig` (exported fields touched
     cross-package), `StatusLineConfig` + `FlagEnabled`, `ComposerConfig`;
     `StoreKey`, `StoredKey`, `StoreOAuth`, `StoredOAuth`, `KeyFilePath`.
     Nothing else exported.
   - `internal/agent`: exported `TurnEvent`, `ApprovalRequest`,
     `RunTurn`, `DispatchTool` (only if called outside the package —
     verify; else keep private), `CompactHistory`, `GenerateSessionName`,
     `ExpandFileReferences` + mention helpers.
   - `internal/mcp`: exported `McpManager`, `NewMcpManager`, methods
     `Tools`, `Call`, `Stop`, `Status`, `Summary`, `Trusted`.
   Append the finalized accessor table to `context.md` as an amendment
   (date it). No code moves in this task.
   Verified: contract reviewed against the audit table; every `conn.` /
   `res.` site maps to exactly one constructor arg or accessor.

7. **T1 — create `internal/providers`.** Move `config.go`, `keyfile.go`,
   `provider.go` whole (new package header, apply T0 exports). Update
   consumers: `run.go` (193, 230, 252, 259, 278, 405, 413), `composer.go`
   (127–130), `tui.go` (~10 sites incl. 81, 513–532, 559–562, 899–901,
   1002–1004), `status_line.go` (`flagEnabled`), `provider.go`'s internal
   users unchanged. Split `run_test.go`: `resolveProvider` cases move with
   the subject to `internal/providers/provider_test.go` (package header +
   imports only); `resolveModel`/`resolveRoot` cases stay.
   Verified: `go build ./... && go test ./internal/app/ ./internal/providers/`;
   `run_test.go` in `app` contains zero `resolveProvider` references.

8. **T2 — create `internal/agent` core.** Move `agent.go`, `compaction.go`,
   `sessionname.go` whole (apply T0 exports). `agent.go` keeps its imports
   of `actions`/`model`/`repository` and gains none of `bubbletea`/
   `lipgloss`/`internal/tui`. Move `agent_test.go`, `approval_test.go`,
   `compaction_test.go`, `sessionname_test.go` with their subjects
   (headers + imports only). `tui.go` keeps its channel/closure half
   (`events`, `waitEvent`, `pending`, `case turnEvent`); approval semantics
   unchanged per FR-06/07/08.
   Verified: `go build ./... && go test ./internal/agent/ ./internal/app/`;
   `grep -rn charmbracelet internal/agent/` → empty (the import gate);
   `approval_test.go` passes unmodified.

9. **T3 — split `mentions.go`.** Cut between `completeMention` (line ~144)
   and `mentionState` (line ~154): pure half (`mentionToken`, caps,
   `expandFileReferences`, `buildFileIndex`, `mentionQuery`,
   `mentionMatches`, `completeMention`) → `internal/agent/mentions.go`;
   UI half (`mentionState`, `fileIndexMsg`, all `(m *ui)` methods) stays in
   `app` (Phase-2 `internal/tui`). Split `mentions_test.go` by symbol along
   the same line; no test logic changes. `agent.go:45`
   `expandFileReferences` call becomes same-package.
   Verified: `go build ./... && go test ./internal/agent/ ./internal/app/`;
   @-reference expansion cases pass from the agent package.

10. **T4 — move the MCP manager into `internal/mcp`.** Move `app/mcp.go`
    whole to `internal/mcp/manager.go` (apply T0 exports; alias
    `toolDefinition = model.ToolDefinition` travels). Update the five
    external sites: `agent.go` (`Tools`, `Call` + approve closure),
    `run.go:287,292` (`newMcpManager`→`NewMcpManager`, `Stop`),
    `status_line.go:184` (`Summary`), `tui.go:606,724` (`Status`).
    Move manager cases from `app/mcp_test.go` to `internal/mcp/`
    (headers + imports only).
    Verified: `go build ./... && go test ./internal/mcp/ ./internal/agent/
    ./internal/app/`; FR-16 first-call approval flow covered by moved tests.

11. **T5 — split `providers.go` and finish the TUI half.** Pure
    catalog/resolution logic → `internal/providers` (boundary rule: anything
    returning/taking `tea.Cmd`/`tea.KeyMsg` or on `*ui` stays). UI half
    (`keyState`, `keyCheckMsg`, key modal, provider-switch messages,
    dialogs) stays in `app`. Split `providers_test.go` (812 lines) by
    symbol along the same boundary. Rename `tui_m2_keys_test.go` →
    `tui_keys_test.go` (milestone-suffix cleanup; function names unchanged).
    Verified: `go build ./... && go test ./internal/providers/
    ./internal/app/`; `/providers`, `/models`, key-modal, and setup flows
    covered by tests in their final homes.

12. **T6 — form `internal/tui` and shrink `app`.** Move the UI files
    (`tui.go`, `composer.go`, `editor.go`, `scroll.go`, `status_line.go`,
    `status_sources.go`, `commandcomplete.go`, remaining modal/mention
    halves) plus their tests (`tui_test.go`, `tui_keys_test.go`,
    `tui_muted_tools_test.go`, `models_sessions_dialog_test.go`,
    `dialog_test_helpers_test.go`, `themes_modal_test.go`, `composer*`,
    `editor_test.go`, `scroll_test.go`, all four `status_*_test.go`,
    `session_test.go` → renamed `tui_session_test.go`) into `internal/tui`.
    `internal/app` retains only `run.go` (composition: `Run`,
    `resolveModel`, `resolveRoot`, `deviceLoginFlow`, the single
    `tea.NewProgram`/`newUI` call site at ex-`run.go:321`) and whatever
    wiring the compiler demands — nothing else. `cmd/lisa/main.go`
    untouched.
    Verified: `go build ./...`; `go list ./...` shows
    `lisa/internal/{agent,providers,tui}` and a small `lisa/internal/app`;
    full `go test ./...` green.

13. **T7 — Phase 2 gate + docs.** Full baseline suite plus integration:
    `go build ./... && go vet ./... && go test ./... && go test -race ./...
    && go test ./tests/integration/`, `gofmt -l` clean. Manual TUI
    walkthrough on the refactored tree (same steps as the release smoke in
    `specs/feature-test-plan.md`: approve + reject an `edit_file`, approve
    + reject a `run_command`, `/themes`, `/models`, `/providers`,
    first-run setup path, session resume) — identical visible behavior.
    Write `ARCHITECTURE.md` at root: package-dependency diagram
    (`cmd` → `app` → `agent`/`providers`/`tui` + leaves), one paragraph per
    package (owns / may-not-import), and the "where do I add X" table (a
    tool, a provider, a keybinding, a status segment, a dialog). Point
    README's structure section at it. Status becomes `implemented (local)`
    only after this gate.

## Explicitly not tasks

- depguard/gomodguard lint forbidding `internal/agent → bubbletea`
  (follow-up, separate commit after both phases).
- Any FR wording change, any user-visible string/keybinding/flow change.
- Restructuring leaf packages, `specs/`, or `tests/integration/`.
- New interfaces beyond the one `approve func(...)` callback the manager
  already takes, and the T0 constructor/accessors the unexported-field move
  forces.

## Phase 1.5 — slim `tui.go` in place (user-confirmed 2026-09-30, landed 2026-10-01)

6. **`setup.go` (416 lines).** [x] DONE 2026-10-01 — Moved whole, zero body
   edits: setup stage consts + `setupState`, `updateSetup`, `setupWindow`,
   `startSetupCheck` + `setupCheckMsg`, `openBrowser`, `startOAuthLogin` +
   `oauthLoginMsg`, `finishSetup`, `applySetupTheme`, `setupView`,
   `setupLines`, `setupActions`. `tui.go` dropped its `os/exec` + `runtime`
   imports with the move. Test files did not move.
   Verified: `go build ./...` green; setup suites green
   (`TestSetupFlowPicksProviderKeyAndModel`,
   `TestSetupModelStageListsAndSelects`,
   `TestSetupCheckFailureShowsClassifiedErrorAndRetries`,
   `TestSetupOAuth*` ×4, `TestSetupFinishStoresOAuthCredentialForChatGPT`,
   `TestFinishSetupPreservesComposerPreference` — 11/11).

7. **`dialog.go` (545 lines).** [x] DONE 2026-10-01 — Moved whole, zero body
   edits: `dialogKind` consts + `dialogState`, `dialogMatches`,
   `openDialog`, `commandHelp` + `handleCommand`, `handleSessionsCommand`,
   `resumeSession`, `updateDialog` + `dialogWindow` + `confirmDialog`,
   `dialogView`, `dialogLabel`, `dialogKindName`. `Update`'s delegation
   order (setup → mention → command → keyModal → dialog → keys) verbatim.
   Test files did not move.
   Verified: `go build ./...` green; 35 dialog/command suites green
   (`TestHelp|TestUnknownCommand|TestSlash|TestCommand|TestDialog|TestSessions|
   TestResume|TestModels|TestThemes|TestProviders`).

8. **`view.go` (368 lines).** [x] DONE 2026-10-01 — Moved whole, zero body
   edits: `entry`, `markReviewPage`, `header`, `bodyHeight`, `pageCount`,
   `rebuild`, `View`, `mainView`, `dimRow`, `spliceRow`, `stripANSI`,
   `splitAtWidth`, `frame`, `wrap`, `hiddenReviewRune`, `fit`. Shared
   helpers `windowList` and `fitHint` deliberately stayed put
   (`windowList` is called by both setup and dialog views; `fitHint` is
   called only by `status_line.go`). `tui.go` dropped `path/filepath`,
   `unicode`, and `runewidth` imports with the move. Test files did not move.
   Verified: 27 view suites green; full `go test ./...` green except the
   two pre-existing logo-asset failures; `go test -race` green across all
   extracted suites; `gofmt -l` clean on all four touched files.

9. **Phase 1.5 gate.** [x] DONE 2026-10-01 — Size audit: `tui.go`
   2,209 → 1,001 lines; `setup.go` 416, `dialog.go` 545, `view.go` 368;
   no file over ~600 lines except `tui.go` itself. `Update` delegation
   order verbatim; approval flow untouched; test files unmoved. Phase 1.5
   `implemented (local)`.
