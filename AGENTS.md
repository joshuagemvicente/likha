# AGENTS.md

Likha is a terminal coding agent written in Go (module `likha`, Go 1.24, Bubble Tea TUI, CGo-free SQLite via `modernc.org/sqlite`). Keep these points in mind when working here.

## Commands

- Build: `go build -o bin/likha ./cmd/likha`
- Run: `go run ./cmd/likha` (add `--debug-models .` to log model metadata)
- All tests: `go test ./...` — the automated entry point for every feature
- One package: `go test ./internal/agent/`
- One test: `go test ./internal/tui/ -run TestName`
- Vet: `go vet ./...`
- Format with `gofmt` (standard Go toolchain)
- Regenerate model catalog: `go generate ./internal/model/catalog`
- Local release archives (writes `dist/`, publishes nothing): `./scripts/release.sh v1.2.3`

No Makefile and no CI workflows (no `.github/`); these commands come from the README and `ARCHITECTURE.md`.

## Layout

- `cmd/likha` — entry point; the only package that imports `internal/app`
- `internal/app` — composition root: flags, wiring, model/root resolution, the single `tea.NewProgram` call
- `internal/tui` — all Bubble Tea UI: model, dialogs, setup, composer, status line
- `internal/agent` — turn loop, tool registry, approvals, compaction, session naming
- `internal/actions` — edit/command execution; `internal/cmdpolicy` — command classification
- `internal/providers` — provider identity, credentials, `config.json`/`providers.json`
- `internal/model` — streaming client, pricing; `internal/model/catalog` — bundled prices and context windows
- `internal/mcp`, `internal/webtools`, `internal/explore` — MCP client, web tools, subagents
- `internal/ui` — themes + glyphs only (no project-package imports)
- `internal/session`, `internal/repository`, `internal/transcript`, `internal/markdown`, `internal/highlight` — SQLite history, repo reading, transcript rendering, markdown/highlighting
- `specs/` — spec-first source of truth (see below); `docs/` — user guides
- `scripts/` — `install.sh`, `release.sh`, `smoke-release.sh`
- `tests/integration/` — CLI integration tests (package `integration_test`; execs `go run ./cmd/likha` with a temp `LIKHA_STATE_DIR`)

Tests sit beside source as `*_test.go` in the same package, except `tests/integration/`.

## Import rules (from `ARCHITECTURE.md`)

- No import cycles; only `cmd/likha` imports `internal/app`.
- `tui` must never import `app`.
- `agent` must never import `bubbletea`, `lipgloss`, `tui`, or `providers` (grep-checked in review, not linted).
- `providers` may import only `model` and the `mcp` manager type; `mcp` only `model`.
- `ui` must not import project packages.

## Go style

- Write clean, understandable Go: idiomatic style (Effective Go / Go Code Review Comments), descriptive names, small focused functions with early returns, and no cleverness — a reader should see intent without reverse-engineering. Comments explain *why*, not what; exported symbols carry doc comments.

  ```go
  // Guards first, happy path last: every branch is one visible decision.
  func validateProvider(c Config) error {
      if c.Provider == "" {
          return errors.New("provider is required")
      }
      if c.Model == "" {
          return fmt.Errorf("provider %s: model is required", c.Provider)
      }
      if !c.Model.Known() {
          return fmt.Errorf("provider %s: unknown model %s", c.Provider, c.Model)
      }
      return nil
  }
  ```

- `gofmt` everything and keep `go vet ./...` quiet. Delete dead code, unused parameters, and commented-out blocks rather than leaving them for later.
- Errors are part of the API: check every one, wrap with `%w` and enough context to locate the failure, match wrapped causes with `errors.Is`/`errors.As` (never substring checks), and never report a failed action as successful (FR-09 applies to code too — no swallow-and-continue without a stated reason).

  ```go
  // Wrap at the seam that knows the context; keep %w so callers can still match.
  if err := store.Save(sess); err != nil {
      return fmt.Errorf("save session %s: %w", sess.ID, err)
  }

  // Distinguish causes precisely — no strings.Contains(err.Error(), ...).
  if errors.Is(err, os.ErrNotExist) {
      return nil, fmt.Errorf("resume: session %s is gone", sess.ID)
  }
  ```

- Prefer simple constructs: plain structs, plain functions, and table-driven tests over reflection or exotic generics; interfaces stay small and are defined where they are consumed.

  ```go
  cases := []struct {
      name string
      in   string
      want string
  }{
      {"empty", "", ""},
      {"raw slug", "gpt-4o", "GPT-4o"},
      {"unknown kept as-is", "zzz-9", "zzz-9"},
  }
  for _, tc := range cases {
      t.Run(tc.name, func(t *testing.T) {
          if got := modelName(tc.in); got != tc.want {
              t.Fatalf("modelName(%q) = %q, want %q", tc.in, got, tc.want)
          }
      })
  }
  ```

- Concurrency and resources: pass `context.Context` as the first parameter, keep goroutine ownership explicit (no leaks, no unbounded queues), and pair every Open/Start with a `defer`ed Close/Stop.

  ```go
  func (s *store) list(ctx context.Context, repoRoot string) ([]Session, error) {
      rows, err := s.db.QueryContext(ctx, sqlSessions, repoRoot) // ctx flows through
      if err != nil {
          return nil, fmt.Errorf("list sessions: %w", err)
      }
      defer rows.Close() // every acquisition gets a paired release
      // ...
  }
  ```

- Keep behavior-preserving cleanups separate: broad restructurings are their own `refactor(...)` commit, never mixed into a feature or fix.

## Where changes go

- New tool: `internal/agent/agent.go` (`agentTools` + `dispatchTool` case) plus `internal/actions/` for execution; reads run direct, mutations go through `requestApproval`.
- New provider: catalog entry in `internal/model` + `internal/providers` for resolution/credentials; the switch flow lives in `internal/tui/providers.go`.
- Keybinding: `internal/tui/tui.go` `Update` is the sole dispatcher. The delegation order (setup → mention → command → keyModal → dialog → keys) is load-bearing — keep it verbatim.
- Status segment: `internal/tui/status_line.go` (render) + `status_sources.go` (data) + a `providers.StoredStatusLineConfig` field.
- Dialog: `internal/tui/dialog.go` (`dialogKind`, open/update/view/confirm); slash entry point in `handleCommand`.

## Spec-first workflow

- Behavior changes start from a feature folder under `specs/<feature>/` (`spec.md`, `tasks.md`, `checklist.md`, `context.md`). The overarching contract is `specs/v1-spec.md`; feature specs refine it and never contradict it. New functional requirements are amended into `v1-spec.md` first.
- `spec.md` states user-visible behavior only; implementation notes go in `tasks.md`.
- Status vocabulary, exactly: planned, in progress, implemented (local), verified (release).
- Keep `go test ./...` passing. Skipped, always-passing, or mock-only tests may not be used to claim a feature works.
- Add an entry under **Unreleased** in `CHANGELOG.md`, stating honestly what was verified (automated checks, headless probe, live probe, or interactive walkthrough).

## Commits and PR titles

- Use conventional commit-style messages and PR titles: `type(scope): summary`.
- Valid types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`.
- Scopes are optional; use the affected package or area when helpful: `tui`, `agent`, `providers`, `model`, `app`, `catalog`, `specs`, `integration`, `docs`.
- Examples: `fix(tui): simplify thinking toggle styling`, `docs: update contributing guide`, `chore(catalog): regenerate models.json`.

## Gotchas

- `internal/model/catalog/models.json` is generated by `./gen` from the models.dev catalog plus hand-checked `overrides.json` — never edit it by hand.
- Pre-release: no version has been published; build from source. Per-change verification status lives in `CHANGELOG.md`.
- Catalog lookups match the exact (provider, model) pair: no prefix matching, no borrowing rows across providers.
