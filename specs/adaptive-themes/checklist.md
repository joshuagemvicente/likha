# Checklist — Adaptive themes

Observable outcomes. Mirrors this feature's spec acceptance criteria; no box
is ticked until the automated suite (or a named walkthrough) covers it.

## Live preview (M1)

- [ ] `↑/↓` in `/themes` visibly re-tints dialog chrome AND the dimmed
      conversation behind it before Enter.
- [ ] Enter persists the highlighted theme to `config.json` with the
      existing transcript note; Esc restores the committed theme with no
      config write and no transcript entry.
- [ ] The `(current)` marker always names the Esc target (committed theme),
      distinct from the highlighted row.

## Full-surface color (M2–M3)

- [ ] Nord→Habamax (any pair) visibly changes borders, selection, prose
      surfaces, status bar, and canvas background — not just accents.
- [ ] Every family × both variants passes the contrast gate; expected
      per-family overrides: zero (any override recorded in `tasks.md`).
- [ ] Limited-profile renders (ANSI / no-color) stay legible with content
      intact; backgrounds never carry meaning alone.

## Gates

- [ ] `go test ./...` passes with this feature's tests included, including
      the phase-2 overflow suite unchanged.
- [ ] No skipped or mock-only tests claimed as coverage.
- [ ] Live terminal walkthrough (Ghostty): preview sweep across families,
      band legibility, degradation eyeball — same gate as the tui-layout
      walkthrough items.
