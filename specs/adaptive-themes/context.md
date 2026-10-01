# Context — Adaptive themes

Code, tests, and docs this feature touches.

## Code

- `internal/app/tui.go` — `updateDialog` cursor branches (`:804-815`),
  `confirmDialog` `dialogThemes` branch (`:868-872`), `esc` discard
  (`:822-835`), `applyTheme` (`:894-907`) to split into
  preview/commit, `rebuild()` role-style switch (`:1697-1708`), `mainView`
  full-width render (`:1758-1770`), `dialogView` dim-through overlay
  (`:1908-2041`), `dialogLabel` `(current)` marker (`:2097-2114`),
  `setupTheme` stage (`:405-417`), `newUI` theme resolve (`:246-254`).
- `ui/theme.go` — `Theme` struct (8 roles → +4 backgrounds), `palette`,
  `families`, `fromPalette` (resolve-time `lipgloss.Color` → per-render
  `AdaptiveColor`), `Named`/`Resolve` (signatures stable).
- `ui/adaptive.go` (new) — `mix`, `legible`, band-ratio constants.
- `internal/app/composer.go` — composer input fg (`:88-116`), border/chrome
  roles on `BgBase`.
- `internal/app/status_line.go` — status segments on `BgBase`
  (`renderStatusRow`, `statusLineRows`).

## Tests

- `internal/app/themes_modal_test.go` — extend `TestThemesModalSelection`
  (preview-before-commit, Esc-restore, commit-store) + new
  dialog-through-preview render test.
- `internal/app/tui_muted_tools_test.go` — role-style assertion pattern to
  mirror for bands (`TestToolEntriesMutedAcrossThemes`).
- `internal/app/dialog_test_helpers_test.go` — `forceANSI` helper to mirror
  for degradation tests.
- `internal/app/tui_test.go` + `composer_overflow_test.go` — phase-2
  no-overflow invariant suite; must stay green untouched.
- `ui/adaptive_test.go` (new) — mix/gate tables + full-matrix contrast test.

## Docs

- `README.md` themes section, `--help` theme line, `CHANGELOG.md`.
- `specs/themes/spec.md` — selection-UX + decision-6 amendments.
- `specs/tui-layout/spec.md` + `checklist.md` — phase 3 closure.
- `specs/README.md` — feature-index row.

## Related specs

- `specs/themes/spec.md` — role conventions this feature extends.
- `specs/tui-layout/spec.md` — phase 3 absorbed here; its three open
  decisions resolved in this spec.
- `specs/tool-rendering-terminal-keys/` — muted-role precedent and
  per-family legibility test pattern.

## Facts that shape the work

- `dialogView` already renders base through `mainView` and dims with
  `dimRow`, which preserves color escapes — preview shows through with no
  dialog changes.
- `mainView` already paints full-width (`style.Render(fit(line, width))`);
  background bands need only style changes, not layout changes.
- Styles never alter visible widths (`stripANSI`/`splitAtWidth`); the
  overflow suite pins that independently.
- `lipgloss.AdaptiveColor` selects Light/Dark per render via the renderer's
  cached background detection — no watcher, no new dependency.
