# Checklist — Adaptive themes

Observable outcomes. Mirrors this feature's spec acceptance criteria; no box
is ticked until the automated suite (or a named walkthrough) covers it.

## Live preview (M1)

- [x] `↑/↓` in `/themes` visibly re-tints dialog chrome AND the dimmed
      conversation behind it before Enter. (`TestThemesPreviewKeepsCommittedNameAndConfig`,
      `TestThemesDialogPreviewRenderDiffers`; human eyeball pending the
      walkthrough below.)
- [x] Enter persists the highlighted theme to `config.json` with the
      existing transcript note; Esc restores the committed theme with no
      config write and no transcript entry. (`TestThemesEnterCommitsPreview`,
      `TestThemesEscRestoresCommitted`, `TestThemesTwoStageEscRestores`,
      `TestThemesDirectArgStillCommits`.)
- [x] The `(current)` marker always names the Esc target (committed theme),
      distinct from the highlighted row. (`TestThemesCurrentMarkerNamesEscTarget`.)

## Full-surface color (M2–M3)

- [x] Nord→Habamax (any pair) visibly changes borders, selection, prose
      surfaces, status bar, and canvas background — not just accents.
      (`TestThemeBackgroundFollowsPalette`, `TestBandRolesAcrossThemes`,
      `TestLogoOnCanvasAcrossThemes`, `TestDefaultBandsAreNoOps`; human
      eyeball pending the walkthrough below.)
- [x] Every family × both variants passes the contrast gate; expected
      per-family overrides: zero (any override recorded in `tasks.md`).
      (`TestContrastMatrix`, `TestMixEndpoints`, `TestLegibleAccentsOnBase`,
      `TestLegibleRejects`, `TestRolesCarryBothVariants`,
      `TestResolveVariantArgAdvisory`; overrides: zero.)
- [x] Limited-profile renders (ANSI / no-color) stay legible with content
  intact; backgrounds never carry meaning alone.
  (`TestDegradationAcrossProfiles`, `TestDegradedBandsKeepWidths`.)
  > Superseded for the transcript by [transcript-redesign](../transcript-redesign/spec.md) (user-approved 2026-10-04): the degradation tests now assert glyphs instead of role labels.

## Inline swatches (M4)

- [x] `#4493f8` (and `#rgb`, `rgb()`/`hsl()` forms) previews its own color
  inline: swatch background with contrast foreground, text/widths/copy
  unchanged, theme switches keep it. (`TestFindSwatchesHex/Func`,
  `TestSwatchStyleContrast`,
  `TestSwatchPaintsHexBackground/HSLAndRGB/KeepsBandAndRestoresOnTheme/SkipsLogoAndWrapsClean`.)

## Gates

- [x] `go test ./...` passes with this feature's tests included, including
      the phase-2 overflow suite unchanged.
- [x] No skipped or mock-only tests claimed as coverage.
- [ ] Live terminal walkthrough (Ghostty): preview sweep across families,
      band legibility, degradation eyeball — same gate as the tui-layout
      walkthrough items.
