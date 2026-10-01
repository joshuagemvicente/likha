# Checklist — repository structure refactor

Observable outcomes, both phases. Status words per [specs/README.md](../README.md):
only **planned**, **in progress**, **implemented (local)**, **verified (release)**.
No skipped, always-passing, or mock-only tests may claim any item.

## Phase 0 — baseline

- [ ] Feature-index row for `structure-refactor/` exists in `specs/README.md`
      with status `planned`.
- [ ] Baseline outputs for `go build ./...`, `go vet ./...`,
      `go test ./...`, `go test -race ./...`, `gofmt -l .`, `go list ./...`
      are recorded before any move.

## Phase 1 — mechanical moves

- [x] `go list ./...` shows `lisa/internal/ui` and no `lisa/ui`; the package
      doc is intact; the `lisaui` alias is kept (struct-name shadowing);
      TUI themes, glyphs, and Nerd-Font markers behave identically
      (marker/theme suites green).
- [x] `go list ./...` still shows `lisa/internal/skills` (deletion struck —
      `custom-commands`/`text-transforms`/`slash-commands` depend on the
      path); the inert `/skills` reservation still behaves exactly as
      before (no new behavior, no removed behavior).
- [x] `statusbar_test.go` is gone; `status_view_test.go` holds the same 14
      tests with the same names; all status suites pass.
- [x] Phase 1 gate: build + vet green; `go test ./...` green except the two
      pre-existing logo-asset failures recorded in `spec.md` (baseline-
      identical); race green on moved-package consumer suites; diff audit
      shows moves + import lines + doc lines only — zero function-body
      changes by this refactor.

## Phase 1.5 — slim `tui.go` in place

- [x] `setup.go` (416 lines) holds the full first-run setup block, moved
      whole; 11 setup suites green; `tui.go` dropped `os/exec` + `runtime`.
- [x] `dialog.go` (545 lines) holds dialog state, `updateDialog`/
      `dialogView`, and slash-command dispatch, moved whole; 35
      dialog/command suites green; `Update` delegation order verbatim.
- [x] `view.go` (368 lines) holds transcript assembly, layout, and row
      helpers, moved whole; 27 view suites green; shared helpers
      (`windowList`, `fitHint`) deliberately stayed put.
- [x] Phase 1.5 gate: `tui.go` 2,209 → 1,001 lines; no file over ~600
      except `tui.go`; full suite green except the two pre-existing
      logo-asset failures; race green; `gofmt -l` clean on all four
      touched files; test files unmoved; approval flow untouched.

## Phase 2 — package split

- [ ] The T0 contract amendment is recorded in `context.md` (dated accessor
      table) before any Phase 2 file moves; no Phase 2 task re-decides it.
- [ ] `internal/providers` exists; provider resolution, credentials,
      stored config, and OAuth/device-login flows behave identically
      (provider/auth/setup suites green in their new home).
- [ ] `internal/agent` exists; `grep -rn charmbracelet internal/agent/` is
      empty; the tool loop, approval flow (FR-06/07/08), compaction, session
      naming, and @-reference expansion behave identically
      (`approval_test.go` green unmodified).
- [ ] `internal/mcp/manager.go` exists; MCP first-call approval, session
      trust, and crash/hang reporting behave identically (FR-16 suites
      green in their new home).
- [ ] `internal/tui` exists; every UI suite (TUI core, keys, muted tools,
      dialogs, composer, editor, scroll, status, sessions) passes in its
      final home with unmodified assertions.
- [ ] `internal/app` holds only composition (`Run`, model/root resolution,
      device login flow, the single `tea.NewProgram`/`newUI` call site);
      `cmd/lisa/main.go` is byte-identical to baseline.
- [ ] `ARCHITECTURE.md` exists at root with the dependency diagram,
      per-package owns/may-not-import paragraphs, and the five-row
      "where do I add X" table; README points to it.
- [ ] Phase 2 gate: baseline suite + `tests/integration` green, race clean,
      and the manual TUI walkthrough (approve + reject `edit_file`, approve
      + reject `run_command`, `/themes`, `/models`, `/providers`, first-run
      setup, session resume) shows behavior identical to baseline.
- [ ] Status of this refactor is only ever: planned, in progress,
      implemented (local), or verified (release), per the release gate in
      [v1-spec.md](../v1-spec.md).
