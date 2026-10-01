# Spec: Repository structure refactor (package split, phase 1 + phase 2)

**Status:** implemented (local) — Phase 1 + Phase 1.5 landed 2026-10-01; Phase 2 (package split) parked

## Context

Lisa's layering is correct at the edges but failing in the middle. Every leaf
package under `internal/` (`actions`, `mcp`, `model`, `repository`, `session`,
`update`) is cohesive, well-named, and test-colocated. The dependency
direction is clean: only `cmd/lisa/main.go` imports `lisa/internal/app`, and
`app` imports the leaves with no cycles. The one structural debt is
concentration: `internal/app` is a single package of ~13,300 lines (17 source
files plus 24 test files) that owns, at once, the agent tool loop and
approval flow, the Bubbletea UI and all its dialogs, provider configuration,
the credential file, the MCP manager, and session naming. `tui.go` alone is
2,207 lines. The consequences are concrete: an `edit_file` approval test
compiles in the same package as Bubbletea key handling; a change to the
provider catalog risks recompiling and re-understanding the entire UI;
themes and glyphs live in a top-level `ui/` package that nothing outside
`internal/app` can use but which sits outside the project's own
`internal/` invariant; and a 4-line `internal/skills` placeholder package
exists for an unimplemented feature (see [custom-commands](../custom-commands/spec.md),
draft). None of this changes any frontend behavior today; it changes how
safely the next feature lands.

This spec is a **pure structure refactor across two ordered phases**. It
touches no functional requirement in [v1-spec.md](../v1-spec.md): every FR
behavior — approval gating (FR-06/07/08), repository scoping (FR-05), session
persistence (FR-10), TUI behavior (FR-03/04/12/14/17) — must remain
observably identical. The deliverable is different packages, moved files,
and a missing placeholder, verified by the existing test suite with no test
rewritten to pass.

## Phase 1 — mechanical moves, zero logic change

Everything in phase 1 is `git mv` plus import-graph updates. No function
moves between types, no signature changes, no behavior change of any kind.

1. **`ui/` moves to `internal/ui`.** The package is consumed only by the TUI
   in four files (`run.go`, `tui.go`, `status_markers_test.go`,
   `tui_muted_tools_test.go`). After the move the import path is
   `lisa/internal/ui` and the `lisaui` alias is **kept**: dropping it
   collides with the `ui` struct type in `internal/app` (field declarations
   `theme ui.Theme` do not resolve — the struct name shadows the package).
   The package doc comment stays; no behavior change.

2. **`internal/skills` placeholder is retained, not deleted.** The
   deletion originally specified here is superseded: `custom-commands`
   (spec + context), `text-transforms` (context), and `slash-commands`
   (spec + role) jointly depend on `internal/skills/skills.go` existing as
   the inert placeholder — the shared custom-commands storage machinery
   loads through it, and transforms are forbidden from creating a parallel
   loader. Four lines, zero importers, zero build cost; the `/skills`
   slash-command reservation lives in the TUI command list and stays inert.
   See `architecture-phase-1.md` §6 for the full conflict record.

3. **Status test files are renamed to match the file under test.**
   `statusbar_test.go` → `status_view_test.go` by `git mv` only: its 14
   tests exercise `tui.go` view methods (`m.header`, `m.statusTitle`,
   `m.statusLineRows`, `m.spendSegment`, …), and merging into the
   1,812-line `tui_test.go` was rejected to avoid collision review.
   `status_markers_test.go` keeps its accurate name. Test function names
   do not change. (This replaces the earlier merge draft; the mapping key
   in `context.md` §C is authoritative.)

Phase 1 exit gate: `go build ./...`, `go vet ./...`, `go test ./...`
(modulo the two pre-existing logo-asset failures recorded below),
`go test -race` on the moved-package consumer suites, all green;
`lisa/ui` absent; total diff is file moves, import lines, and doc
reference lines — reviewers should find no function body changes.
Pre-existing failures (in-flight user work, unrelated to this refactor):
`TestComposerStylesAndNarrowDegradation` and
`TestStartupLogoBlockOpensFreshSessionAndScrollsAway` fail on the baseline
captured before any move (commented-out logo block vs. new `const logo`
asset in `run.go`); `gofmt -l` flags `run.go`/`tui.go` for the same
in-flight edits.

## Phase 2 — split `internal/app` into agent / providers / tui

App is split along the coupling the imports already reveal, in this order.
Ordering matters: files move only when their target exists, so each step
keeps the build green (status "implemented (local)" only at the end).

Target shape:

- **`internal/agent`** — model conversation, tools, approvals. Everything
  here imports `model`/`repository`/`session`/`actions`/`mcp` and must
  import **no** Bubbletea or Lipgloss. Contents: `agent.go` (tool set,
  `dispatchTool`, `requestApproval`), `compaction.go`, `sessionname.go`,
  and the MCP manager moved out of `internal/app/mcp.go` into
  `internal/mcp` (it is pure state + stdio server lifecycle; `dispatchTool`
  consumes it via a narrow interface so agent never imports mcp internals
  beyond the manager type). `mentions.go` is the one split: the pure
  `expandFileReferences` logic (caps, tree inlining, cap accounting) moves
  to `internal/agent` with `mentions_test.go`'s expansion cases; the
  `ui`-method mention popup editing stays with the TUI.
- **`internal/providers`** — configuration, credentials, connection state:
  `provider.go` (`connection` state, `resolvedProvider`),
  `keyfile.go` (credential schema v2 + migration),
  `config.go` (stored composer/status configs), and the provider catalog
  resolution and connection-check logic from `providers.go`. `providers.go`
  is the second split: its `updateKeyModal` Bubbletea key handling and the
  first-run-setup UI half stay with the TUI; the data/half moves.
- **`internal/tui`** — everything on the `ui` struct: `tui.go`,
  `composer.go`, `editor.go`, `scroll.go`, `status_line.go`,
  `status_sources.go`, `commandcomplete.go`, the mention popup, the key
  modal, and first-run setup. Test files follow the struct (`tui_test.go`,
  `models_sessions_dialog_test.go`, `dialog_test_helpers_test.go`,
  `themes_modal_test.go`, `run_test.go`'s UI cases, etc.).
- **`internal/app` shrinks to composition** — `run.go` (program
  construction, the single `tea.NewProgram` call site), agent-driven
  wiring, and nothing else. `cmd/lisa/main.go` keeps calling into `app`;
  `main.go` itself stays 11 lines.

Known couplings the split must survive (called out explicitly so they are
not discovered mid-move): `providers.go` methods sit on `*ui` and are both
modal and catalog — split is by function, not by file; `sessionname.go` and
`compaction.go` are pure (zero Bubbletea imports, verified by grep) and move
whole; `status_sources.go` is free functions, no `tea`, and follows
`status_line` into `internal/tui` atomically.

Rules the split obeys:

- **igate on imports, not vibes.** A file enters `internal/agent` only if
  its (non-test) imports contain no `bubbletea`, `lipgloss`, or `ui`/`tui`
  package paths. A file with any such import belongs to `internal/tui`.
  Exception is `providers.go`/`mentions.go` handled by the declared splits.
- **Tests move with the symbol they test.** No test is rewritten, weakened,
  or renamed in phase 2 except `package` headers and imports.
- **No new seams invented.** The refactor draws existing seams sharper; it
  does not introduce interfaces, registries, or plugin surfaces beyond the
  one narrow manager interface between `agent` and `mcp` (two call sites:
  `dispatchTool` and the `/mcp` status path make the seam real, not
  hypothetical).
- **Enforcement is out of scope.** depguard/go-modguard lint ( forbidding
  `internal/agent` from importing Bubbletea) is proposed as a *follow-up*
  item, not part of this spec.

Phase 2 exit gate: same four commands green from phase 1, plus
`tests/integration` passing, plus a manual TUI walkthrough matching the
release checklist's smoke steps ( approve + reject an `edit_file`, approve +
reject a `run_command`, `/themes`, `/models`, first-run setup path, session
resume): identical visible behavior on the refactored tree.

## Documentation deliverable

`ARCHITECTURE.md` at the repo root after phase 2: one package-dependency
diagram (`cmd` → `app` → `agent`/`providers`/`tui`/`session`/`repository`
/`model`/`mcp`/`actions`), one paragraph per package saying what it owns
and may not import, and the "where do I add X" table for the five most
likely change requests (a tool, a provider, a keybinding, a status segment,
a dialog). README's project-structure section, if any, points to it.

## Non-goals

- No behavioral change of any kind; no FR wording changes.
- No API stabilization or public-package promotion; `internal/` stays
  `internal/`.
- No lint-gate introduction (recorded as follow-up).
- No restructuring of leaf packages (`model`, `session`, `repository`,
  `actions`, `update`, `mcp`'s stdio core); they are already deep modules.
- `specs/` stays exactly as it is — the doc-per-feature tree is a strength.
- `tests/integration/` keeps its location; Go's colocated-test norm is
  already satisfied elsewhere and this directory is small enough to not
  need a home decision.
