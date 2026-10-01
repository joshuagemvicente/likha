# Feature: Adaptive themes — live preview + full-surface color

**Status:** planned.

Amends `specs/themes/spec.md` (selection UX, prose rule) and absorbs
`specs/tui-layout/spec.md` phase 3 (role backgrounds, whose three open
decisions are resolved here). Implements v1-spec FR-03 (role distinction)
and FR-15 (theme choice) more fully; no FR wording change needed.

## Context

Two independent gaps explain the current "theme change does almost nothing"
report. Both are by-construction, not bugs:

1. **No preview.** `↑/↓` in `/themes` only moves `dialog.cursor`
   (`internal/app/tui.go:updateDialog`); `m.theme` is untouched until
   `Enter → confirmDialog → applyTheme`. Highlighting Nord vs Habamax shows
   nothing until commit.
2. **Nowhere for the theme to show.** `rebuild()` renders `You`/`Assistant`/
   `Lisa` entries in a bare `lipgloss.NewStyle()` (`tui.go:1697-1708`); only
   `Reasoning`/`Tool` (Muted) and `Error` carry theme color, per the
   themes-spec "prose stays uncolored" rule. And no role anywhere paints a
   background — every surface is the terminal default, so there is no
   OpenCode-style base canvas to re-tint. Borders *do* use `theme.Border`,
   but `default`'s Border is uncolored (`ui/theme.go:22-33`), and without
   preview the user never sees another family's border until Enter.

Reference behavior (OpenCode/OMP/Claude Code, user-reported): a dark base
canvas; moving the highlight re-tints borders, selections, text, and
backgrounds across the whole visible TUI immediately; Enter keeps it, Esc
restores.

## Non-goals

- No new theme families, no palette-hue redesign, no gradients or animation
  (FR-15 visual restraint stands for decoration).
- No key remapping, no new slash commands, no dialog reshaping.
- No terminal-background *change* watcher: sampling stays at resolve/render
  time (lipgloss renderer's cached detection). Live re-tint on terminal bg
  switch is a separate feature if ever asked.
- No per-speaker rainbow foregrounds: prose uses one `Normal` fg; authorship
  distinction comes from background bands, not hue floods.

## User-visible behavior

### M1 — Live preview (the hover effect)

- `↑/↓` (and `PgUp`/`PgDn`, query-reset) inside the `/themes` dialog
  re-resolves `m.theme` to the highlighted family **immediately**. The dialog
  chrome (border, title, selected row) and the dimmed conversation behind it
  re-render in the candidate palette — the existing `dialogView` already
  draws base via `mainView()` and dims with `dimRow` (colors survive dimming),
  so the preview shows through exactly like OpenCode's.
- `Enter` commits: stores to `config.json` + the existing `"Theme set to …"`
  entry. `Esc` restores the committed theme and writes nothing.
- The `(current)` marker (`dialogLabel`) stays on the **committed** theme, so
  the Esc target is always visible; the highlighted row already carries the
  `Selected` style.
- `/themes <n-or-name>` still commits directly (no preview needed).
- First-run `setupTheme` stage gets the same preview hook on `↑/↓` (same
  helper, near-zero cost).

### M2 — Adaptive color helpers (`ui` package)

- New `ui/adaptive.go`: a `mix(fg, bg string, ratio float64)` tint helper, a
  `legible(fg, bg string) bool` contrast gate, and constructors so each role
  style carries **both** variants via `lipgloss.AdaptiveColor{Light, Dark}`
  (verified in module cache `lipgloss@v1.1.0/color.go:94-108`: the variant is
  selected per-render by `Renderer.HasDarkBackground()`).
- Effect: one `Theme` value serves both terminals — family switching stays a
  `Resolve` call, but light/dark adaptation moves from resolve-time branching
  to render time. `Resolve(name, dark)` keeps its signature (callers and
  tests stable); the `dark` argument becomes advisory/compat while styles
  carry both variants.
- Background tints are **derived**, not hand-picked: each band mixes the
  family accent toward the base canvas at a small ratio (~8–12%). A
  per-family override map exists only where a derived tint fails the contrast
  gate. This bounds the palette work (no 88-cell hand table) and guarantees
  harmony.

### M3 — Full-surface roles

- New `Theme` roles: `BgBase` (app canvas — near-black for dark variants,
  paper for light; this is the "pure black background" ask), `BgUser`,
  `BgTool`, `BgModel`. Foreground roles unchanged.
- Wiring (`rebuild()` + `mainView()`): `You` lines → `Normal` fg on `BgUser`;
  `Assistant` → `Normal` fg on `BgModel`; `Tool` → `Muted` fg on `BgTool`
  (activity and result share the band — distinction stays in prefix text);
  `Reasoning` → `Muted` fg, **no band** (quietest layer stays flat);
  `Error` → `Error` fg on base (failures signal by fg, not band);
  `Lisa`/system + logo → existing styles on `BgBase`.
- Bands are full-width for free: `mainView` already renders
  `style.Render(fit(line, width))`, and foreground on padding spaces is
  invisible while background paints the pad.
- This amends themes-spec decision 6 ("prose stays uncolored except Error"):
  prose adopts the single `Normal` fg (family text tint, near-neutral) plus
  authorship bands. Still no gradients, no per-role hue coding.
- Status bar, composer, scrollbar, and dialog render on `BgBase`; typed
  composer input uses `Normal` fg (currently plain); placeholder stays Muted.
- Degradation: under limited color profiles (ANSI, no-color) bands collapse
  to legible plain text — backgrounds never carry meaning alone; role labels
  (`You:`/`Tool:`/…) and fg roles remain.

## Resolved decisions (tui-layout phase 3 open items, closed here)

1. **Palette tints:** derived via `mix()` + override-only-on-failure (M2).
2. **Tool activity vs result:** one shared `BgTool` band.
3. **Reasoning background:** none — foreground-muted only, quietest layer.

## Functional changes (v1-spec.md)

None this round: FR-03/FR-15 already describe role distinction and theme
choice. This feature implements them; if implementation reveals an FR wording
gap, v1-spec is amended first and the checklist mirrors it.

## Research findings (recorded 2026-09-30, from repo + module cache)

- `lipgloss.AdaptiveColor{Light, Dark}` selects per-render via the renderer's
  `HasDarkBackground()` (`color.go:99-104`); `CompleteAdaptiveColor` adds
  per-profile exact values (`color.go:152-162`) — standard `AdaptiveColor`
  suffices, lipgloss auto-degrades hex→ANSI256→ANSI.
- Current `fromPalette` bakes one variant into `lipgloss.Color` at resolve
  time (`ui/theme.go:131-147`); the `dark bool` threads through
  `Named`/`Resolve` and three `tui.go` call sites (`newUI`, `applySetupTheme`,
  `applyTheme`). M2 keeps the signatures, changes what the styles carry.
- Styles never alter visible widths (`stripANSI`/`splitAtWidth` in
  `tui.go:2059-2094`); adding backgrounds cannot break the phase-2
  no-overflow invariant — asserted, not assumed, by re-running that suite.
- bubbletea key delivery is unchanged by this feature; no new chords, no
  probe needed.

## Open items

1. Exact `mix` ratios per band (8–12% starting point; tuned against the
   contrast gate in M2, recorded in `tasks.md`).
2. Per-family overrides: expected zero; any added override names the failing
   pair and its measured ratio.
3. Live Ghostty/terminal walkthrough of preview + bands (same gate as the
   tui-layout walkthrough items).

## Acceptance criteria

- [ ] `↑/↓` in `/themes` visibly re-tints dialog chrome AND the dimmed
      conversation behind it before Enter.
- [ ] Enter persists + notes; Esc restores the committed theme with no config
      write and no transcript entry.
- [ ] Nord→Habamax (any pair) visibly changes borders, selection, prose
      surfaces, status bar, and canvas background — not just accents.
- [ ] Every family × both variants passes the contrast gate; limited-profile
      renders stay legible with content intact.
- [ ] `go test ./...` green, including the phase-2 overflow suite untouched.
