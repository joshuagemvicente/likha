# Tasks — Adaptive themes (live preview + full-surface color)

Ordered implementation tasks. Each ends with a verification step; no
checkmarks until the step is done. Automated entry point: `go test ./...`
from the project root. Order stays M1 → M2 → M3 → M4; M1 can ship alone.

## M1 — Live preview (the hover effect)

1. **Split `applyTheme` into preview vs commit.** In `internal/app/tui.go`:
   `previewTheme(name)` sets `m.theme = lisaui.Resolve(name, …)` plus a new
   ephemeral `previewName` field, `layoutWidth = 0`, no config write, no
   transcript entry; `commitTheme(name)` keeps the existing `applyTheme`
   body verbatim (resolve, `themeName`, `saveStoredConfig`, `"Theme set
   to …"` entry) and clears `previewName`. Invariant: preview never writes
   `themeName`, so the `(current)` marker in `dialogLabel` (which reads
   `m.themeName`) always names the Esc target.
   Verify: existing `TestThemesModalSelection` still green unchanged.
2. **Hook cursor moves + Esc + setup stage.** In `updateDialog`
   (`tui.go:804-815`): after `up`/`down`/`pgup`/`pgdown` and query-reset,
   when `kind == dialogThemes` and matches non-empty, resolve through the
   filter indirection (`dialogItems[matches[cursor]]`, same as
   `confirmDialog`) into `previewTheme`. `confirmDialog`'s `dialogThemes`
   branch calls `commitTheme`. `esc` branch: `m.theme =
   Resolve(m.themeName, …)`, clear preview, `layoutWidth = 0`, no write.
   Same preview hook on `setupTheme` stage `up`/`down` (`tui.go:405-417`).
   Verify: extend `themes_modal_test.go` — arrows change `m.theme` to the
   candidate while `m.themeName` and the config file stay on the old theme;
   Enter commits + stores; Esc restores with no write and no entry.
3. **Dialog-through preview eyeball.** `dialogView` needs no change (base
   renders via `mainView`, `dimRow` preserves colors), but assert it: a test
   rendering `dialogView` mid-preview contains the candidate's accent escape
   and differs from the committed-theme render.
   Verify: new test in `themes_modal_test.go`; full `go test ./...` green.

## M2 — Adaptive color helpers (`ui` package)

4. **`ui/adaptive.go`: `mix` + `legible`.** Hex-space `mix(fg, bg string,
   ratio float64) string` (tint derivation) and `legible(fg, bg string)
   bool` contrast gate, pure functions, no lipgloss dependency at the unit
   level. Tuned 2026-10-01 (landed): user 10%, tool 10%, model 8%
   accent-into-base (`bandUserRatio`/`bandToolRatio`/`bandModelRatio` in
   `internal/ui/adaptive.go`; the model band takes the quietest tint since
   assistant prose fills the most rows).
   Verify: table test (`ui/adaptive_test.go`) — mix endpoints (0 → bg,
   1 → fg), gate rejects near-identical pairs, accepts accent-on-base for
   all twenty-two families × both variants.
5. **Carry both variants in every role.** Convert `fromPalette` to build
   each style with `lipgloss.AdaptiveColor{Light: lightVariant,
   Dark: darkVariant}` (per-render selection via the renderer's
   `HasDarkBackground`, verified in module cache). `Named`/`Resolve` keep
   their signatures and call sites (`newUI`, `applySetupTheme`,
   `applyTheme`/`commitTheme`); the `dark` argument becomes advisory/compat.
   No new Go dependencies (lipgloss already in `go.mod`).
   Verify: new test type-asserts each role's `GetForeground()` to
   `AdaptiveColor` with both fields set and Light != Dark where the family
   defines two variants; existing theme-resolution tests green.
6. **Contrast gate over the matrix.** Run `legible` over every family ×
   light/dark × role-fg-on-its-bg. Any failure gets a named per-family
   override recorded here (failing pair + measured ratio); landed 2026-10-01
   with zero failures, so `bandOverrides` stays empty.

## M3 — Full-surface roles

7. **New roles + `rebuild()` wiring.** `Theme` gains `BgBase`, `BgUser`,
   `BgTool`, `BgModel` (background tints derived via `mix` per M2; `default`
   family: `BgBase` pure terminal default — today's look is the fallback,
   not a casualty). `rebuild()`: `You` → Normal-on-BgUser, `Assistant` →
   Normal-on-BgModel, `Tool` → Muted-on-BgTool, `Reasoning` → Muted flat,
   `Error` → Error-on-base, `Lisa`/system/logo → existing styles on BgBase.
   Keep `default`'s `Normal` fg uncolored (today's plain) to minimize
   breakage to style-equality tests.
   Verify: `lineStyles` assertions per role across all families (mirror
   `TestToolEntriesMutedAcrossThemes`); phase-2 overflow suite
   (`TestBodyNeverOverflows…` family) green unchanged — backgrounds cannot
   alter widths, asserted not assumed.
8. **Chrome on the canvas.** Status bar, composer, scrollbar, dialog render
   on `BgBase`; typed composer input uses `Normal` fg (currently plain),
   placeholder stays Muted. Bands are full-width free via the existing
   `style.Render(fit(line, width))` in `mainView`.
   Verify: statusbar/composer tests updated only where they pin exact
   styles, never re-pinned to incidental output; `go test ./...` green.
9. **Degradation.** Under forced ANSI / no-color profiles (mirror the
   `forceANSI` helper in `dialog_test_helpers_test.go`), every role band
   collapses to legible plain text: role labels and content intact, no
   meaning carried by background alone.
   Verify: degradation test renders each role under `termenv.ANSI` and
   asserts visible content + distinct-or-plain styles.

## M4 — Inline color swatches (2026-10-01)

11. **`internal/ui/swatch.go`: `FindSwatches` + `SwatchStyle`.** Pure
    detector (hex 3/4/6/8, `rgb()`/`rgba()`, `hsl()`/`hsla()`, comma/space/`/`
    alpha; longest-match, identifier-glued stays plain) plus the contrast-fg
    swatch style (WCAG 0.179 gate, same `luminance` as the M2 gate).
    Verify: `internal/ui/swatch_test.go` tables (lengths, alpha-drop, clamp,
    wrap, glued-plain, contrast black/white).
12. **Render in `internal/tui/view.go`.** `rebuild` stores parallel
    `lineSpans` (nil for logo/review/header/separators); `mainView` paints
    via `renderSwatches` (band render, then per-span repaint; fit-safe
    offsets, stale-proof indices). Existing `lineStyles` assertions hold:
    styles stay the band role (spans ride alongside, never inside).
    Verify: `internal/tui/swatch_test.go` (hex/hsl/rgb paint, text+widths
    intact, theme-switch + default keep the swatch, wrapped swatches keep
    widths) + full `go test ./...` incl. the overflow suite.
13. **Docs.** README themes paragraph, CHANGELOG Unreleased entry, this
    spec's M4 section + criterion, checklist M4 box.
    Verify: suite green; boxes ticked only where covered.

## M5 — Docs and spec bookkeeping (was M4)

10. **Docs.** README themes section (preview behavior, bands, degradation
   note), `--help` theme line if wording drifts, CHANGELOG Unreleased
   entries. Amend `specs/themes/spec.md` (selection-UX paragraph → preview
   semantics; decision 6 → Normal-fg + bands) and close
   `specs/tui-layout/spec.md` phase 3 (point at this folder; tick checklist
   boxes the suite covers). Add this feature's row to `specs/README.md`
   index.
