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
   `likhaui "likha/ui"` to `likhaui "likha/internal/ui"`, **keeping the alias**:
   the `ui` struct type in `internal/app` shadows a bare `ui` package
   qualifier (verified by compiler error on the alias-drop attempt).
   No other line changes.
   Verified: `grep -rn 'likha/ui' --include='*.go' .` → empty;
   `go list ./...` shows `likha/internal/ui`, no `likha/ui`;
   `go build ./... && go test ./internal/app/`.

3. **Retain `internal/skills` (deletion struck).** [x] DONE 2026-10-01 —
   zero importers verified (`grep -rln 'internal/skills'`, `.cache`
   excluded); the 4-line placeholder left in place: `custom-commands`,
   `text-transforms`, and `slash-commands` depend on the path existing
   (see spec.md Phase 1 §2 and `architecture-phase-1.md` §6).
   Verified: `go list ./...` still shows `likha/internal/skills`;
   `go build ./...`.

4. **Rename `statusbar_test.go` → `status_view_test.go`.** [x] DONE 2026-10-01
   — `git mv` only; all 14 test function names unchanged (uniqueness
   pre-verified). Rationale is in `context.md`: subjects are `tui.go` view
   methods, and the name finally says so. `status_markers_test.go` keeps
   its name (accurate).
   Verified: `go test ./internal/app/ -run 'TestHeader|TestLogo|TestLikhaMark|
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

6. **T0 — freeze the cross-package contract.** [x] DONE 2026-10-01 —
   Full audit (`conn.*`, `connection{`, `res.`, keyfile/config, agent-loop
   callers, `Version`) recorded as the T0 amendment in `context.md`;
   user-approved via questionnaire (exported fields not accessors,
   `Version` in `tui`, `NewMcpManagerForTest` exception allowed).
   Key reversal from the draft: exported fields everywhere instead of
   unexported + constructor + accessors (~40 test literals + 3 rebuild
   sites made accessors pure ceremony; matches `model.Provider` /
   `session.Snapshot` precedent). No code moved in this task.

7. **T1 — create `internal/providers`.** [x] DONE 2026-10-01 — Files moved
   whole with exported fields; 17 consumers codemodded; resolve/config +
   `SwitchModelID` tests moved to `internal/providers/provider_test.go`.
   Full suite green.

8. **T2 — create `internal/agent` core.** [x] DONE 2026-10-01 —
   `agent.go` (`RunTurn`, `TurnEvent`, `ApprovalRequest` exported),
   `compaction.go`, `sessionname.go` moved whole. `TurnEvent` fields
   exported (`Kind/Text/History/Approval/RunID`) — required by tui
   `Update` and tests. No `bubbletea`/`lipgloss` in agent (verified).
   Tests moved with subjects; `serveModels` duplicated to app helpers.

9. **T3 — split `mentions.go`.** [x] DONE 2026-10-01 — Pure half
   (`ExpandFileReferences`, `BuildFileIndex`, `MentionQuery`,
   `MentionMatches`, `CompleteMention`) → `internal/agent/mentions.go`;
   UI half stays (now `internal/tui/mentions.go`), calling `agent.*`.
   Expansion + index + runTurn-through-expansion tests moved to agent.

10. **T4 — move the MCP manager into `internal/mcp`.** [x] DONE 2026-10-01
    — As `McpManager` (+ approved `NewMcpManagerForTest`); single-use
    `toolDefinition` alias dropped. Findings: package fake server echoes
    raw args (one assertion updated to the package's own format);
    `Status()` never started servers (pre-existing) — kept verbatim,
    surface test starts the server eagerly via `Tools()`. Manager cases →
    `internal/mcp`; `/mcp` surface test stays in tui. FR-16 covered.

11. **T5 — split `providers.go` and finish the TUI half.** [x] DONE
    2026-10-01 — Pure catalog funcs moved (T1); UI half verified clean in
    `internal/tui/providers.go`. `tui_m2_keys_test.go` rename deferred —
    owning feature `tool-rendering-terminal-keys` is `in progress`
    (recorded in architecture-phase-1 §2).

12. **T6 — form `internal/tui` and shrink `app`.** [x] DONE 2026-10-01 —
    12 UI sources + all UI tests → `internal/tui`; `NewUI` exported;
    `Version` + `logo` → `tui`; `run.go` reads `tui.Version`;
    release.sh ldflags → `likha/internal/tui.Version` (verified with a
    stamped `--version` run). `internal/app` holds only `run.go` +
    `run_test.go`; `cmd/likha/main.go` byte-identical. Suite + race green.

13. **T7 — Phase 2 gate + docs.** [x] DONE 2026-10-01 — `go build`,
    `go vet`, `go test ./...` (11 packages), `go test -race` on
    `tui`/`agent`/`providers`/`mcp`, `gofmt -l` clean. `ARCHITECTURE.md`
    written (diagram, owns/may-not-import, where-do-I-add-X, history);
    README points to it. Phase 2 `implemented (local)`.

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
