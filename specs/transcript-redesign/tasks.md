# Tasks — Transcript redesign

Ordered implementation tasks. Each ends with a verification step; no
checkmarks until the step is done. Automated entry point: `go test ./...`.
Every slice ends with build, vet, test, `test -race`, gofmt, and
`git diff --check`, then a commit and a user walkthrough before the next
slice starts.

## S0 — Spec amendments

1. **Record the superseded rules.** [x] DONE 2026-10-04 — notes added in adaptive-themes spec/role/checklist, tui-layout, themes decision 6, tool-rendering-terminal-keys spec/role, v1-spec FR-03/FR-15, specs index. Add a dated "superseded by
   transcript-redesign (user-approved 2026-10-04)" note at each rule listed
   in `spec.md` § superseded table (adaptive-themes spec/role/checklist,
   tui-layout, themes decision 6, tool-rendering-terminal-keys non-goals and
   role), v1-spec FR-03/FR-15 cross-references, and the specs index row.
   Verify: every superseded rule points to this spec; no other rule text
   changed.

## S1 — Blocks

2. **Block glyph set.** [x] DONE 2026-10-04 — `ui.BlockGlyphSet`, `--ascii`/`LIKHA_ASCII` via `Connection.ASCII`; `TestBlockGlyphSets`, `TestASCIIFlagAndEnvironment`. — `internal/ui` gains a block-glyph set (Unicode +
   ASCII) beside the status glyphs; `--ascii` / `LIKHA_ASCII=1` selects it,
   persisted like `--nerd-fonts`, listed in `--help`. Verify: unit test of
   both sets; flag/env parsing test.
3. **Theme roles.** [x] DONE 2026-10-04 — `Accent`, `Success` (family green; standard green for habamax/rose-pine/night-owl/monokai/horizon), `BgCode`, `BgDiffAdd/Remove`, neutral `default` `BgUser`; contrast matrix extended; overrides: zero. Deviation: `default` `Accent` is ANSI 12 because `default` `Border` has no color. — Add `Accent`, `Success`, `BgCode`, `BgDiffAdd`,
   `BgDiffRemove`; give `default` a neutral `BgUser`. Verify: contrast matrix
   test extended to the new roles across all families × variants; overrides
   (if any) recorded here.
4. **Block model in `rebuild()`.** [x] DONE 2026-10-04 — glyph gutters, user band + padding rows, notices/errors, legacy `Agent` as `ℹ`; `toolEntryStartLine` reads `entryLines`; approved label assertions rewritten (honesty review: none weakened, several strengthened). — Replace the `role+": "` label with a
   glyph gutter and hanging indent per block kind; user band covers the
   prompt block plus one padding row above/below; assistant/notices on base;
   errors `✗`, notices `ℹ`; legacy `Agent` entries as `ℹ`. Remove the
   duplicated label arithmetic in `toolEntryStartLine`. Verify: rewritten
   band/degradation tests assert glyphs and backgrounds; the approved ~22
   label assertions updated; no others weakened.
5. **Word wrap.** [x] DONE 2026-10-04 — `wrapHanging` + 12 tests; overflow invariants at 40/60/80/120. — New transcript wrapper: word boundaries, hard break for
   over-wide tokens, hanging indent, control-rune escaping and swatches
   preserved. Verify: wrap tests (CJK/emoji widths, long tokens, indents) and
   the overflow invariants at 40/60/80/120 columns.
6. **Single tool item.** [x] DONE 2026-10-04 — `tool_start` records, result updates by call ID, `tool_items.go`/`tool_render.go`, legacy merge + header-only requests; stale running records render `Interrupted` (`TestStaleRunningCallStaysInterruptedInLaterRuns`). — Record `tool_start` as a `ToolRecord` (status
   `running`); results update it by call ID; render header (display name +
   formatted args), status dot, `⎿` summary, and preview caps (0 search, 3
   others) with `… +N lines (ctrl+o to expand)`; legacy request/result pairs
   merge; unmatched legacy requests render header-only. Verify: per-tool
   header/summary table tests; legacy session fixture renders; persisted
   session for a new run stores one entry per call.
7. **Status dot + blink.** [x] DONE 2026-10-04 — dot by status on the working tick; outcome words in no-color. — Dot color by status; blink on the existing
   working tick; static when not animating. Verify: render tests per status
   in color and no-color profiles (words carry outcome).
8. **Focus.** [x] DONE 2026-10-04 — one Tab stop per call, selection tint + `›`, inspector unchanged. — Tab/Shift+Tab one stop per tool item; selection tint + `›`
   gutter; Enter/Ctrl+O opens the existing inspector. Verify: focus cycle
   and inspector tests updated.
9. **Rounded composer.** [x] DONE 2026-10-04 — default `rounded`, `minimal` ≤ 40 cols, ASCII edges for every style (`TestComposerASCIIEdgesEveryStyle`). — New `rounded` style as default; `minimal` fallback
   at ≤ 40 columns; existing styles and saved choices honored. Verify:
   composer overflow and style tests.
10. **Docs + commit S1.** [x] docs + commit DONE 2026-10-04; [ ] user walkthrough pending — README/CHANGELOG/`--help`. Full checks; commit;
    user walkthrough.

## S2 — Markdown

11. **Dependencies.** [x] DONE 2026-10-04 — goldmark v1.8.6 + chroma v2.24.1 (newest chroma keeping `go 1.24.0`; v2.25+ needs Go 1.25/1.26); regexp2 v1.12.0 transitive. — Add `github.com/yuin/goldmark` and
    `github.com/alecthomas/chroma/v2`. Verify: `go mod tidy`, build, license
    note in CHANGELOG.
12. **Renderer.** [x] DONE 2026-10-04 — `internal/markdown` (goldmark GFM tables/strikethrough/tasklist) + `internal/tui/markdown_render.go`; per-entry cache; chunked parsing above 8 KiB with a 100 ms parse budget; nesting caps (24 block / 32 inline) after a stack-overflow finding; escape-injection matrix through `View()` found nothing. Known cost: a resize re-renders every answer (~2.7× plain); lazy visible-page layout deferred. — goldmark AST → styled, width-aware rows per § Markdown
    (headings, emphasis, inline code, code panels, lists, quotes, links,
    tables with fallback, rule, images, literal HTML); streaming-safe for
    unclosed fences; per-entry render cache keyed by content, width, theme,
    and glyph set. Verify: golden render tests per element at 40/80 columns;
    escape-sequence injection test; overflow invariants.
13. **Highlighting.** [x] DONE 2026-10-04 — `internal/highlight` (chroma, fence info only, 32 KiB cap, 100 ms per-token deadline); keywords Accent, strings Warning, comments Muted italic, others Normal on `BgCode`; `default` → ANSI 5/2/8; per-layout 40 ms budget newest-first with deferred re-highlight. — Chroma lexer from the fence info string only; style
    generated from the active theme (`default` → ANSI 16); unknown languages
    plain. Verify: highlight tests across 3 families and `default`; no-color
    profile drops colors but keeps text.
14. **Docs + commit S2.** [x] docs + commit DONE 2026-10-04; [ ] user walkthrough pending — Full checks; commit; user walkthrough.

## S3 — Live surfaces

15. **Diffs.** Edit items: summary sentence + up to 10 numbered `+`/`-`
    lines on diff tints, multi-file naming, create summary. Verify: render
    tests for modify/multi-file/create; no-color keeps signs.
16. **Subagent items.** `task` item live `⎿` line from the direct child's
    latest activity; settle line with tool uses + wait/active or outcome;
    stop emitting `Agent` entries. Verify: tests driving record updates
    through queued→running→waiting→completed/failed show one item and no
    `Agent` entries.
17. **Reasoning.** Live 3-line tail; collapse to `✻ Thought for Ns`; Tab
    focus on markers; Enter toggles inline expansion (per view). Verify:
    streaming, collapse, focus, and toggle tests.
18. **Turn footer.** Persisted `Turn` entry: model · duration · N tools
    (+ cancelled); narrow segment dropping. Verify: footer after
    completed/cancelled turns; survives resume.
19. **Docs + commit S3.** Full checks; commit; user walkthrough; update the
    specs index status.
