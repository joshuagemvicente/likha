# Tasks: TUI layout adjustment

Phase 1 and phase 2 are implemented (local). Recorded below in past tense, in
the order the work actually landed; phase 3 has no tasks yet (order stays
1 → 2 → 3, each phase planned separately).

## Wave 1 — contracts (before wiring)

1. Amended `specs/v1-spec.md` FR-12 and §7: the top-of-screen furniture on
   ≥56-col terminals is the logo block plus the status bar only; the
   status bar carries identity with a small Lisa mark at its right end; a
   single compact identity line covers narrow terminals. Verified before
   any `header()` change.
2. `internal/app/config.go`: `status_line.folder` and `.branch` migrated to
   `*bool` (`nil` → default-on, explicit `false` disables, resolved
   through one `flagEnabled` consumer); every other toggle stays a plain
   bool so old configs keep round-tripping (covered by
   `TestStoredStatusLineConfigRoundTripAndLegacy`,
   `TestStatusLineConfigPointerDefaults`).
3. `internal/model/naming.go`: curated display-name catalog beside the
   window table (`ModelDisplayName`, exact IDs then family prefixes), slug
   fallback when unmapped. `internal/model/pricing.go`: per-Mtok
   `TurnCost` table with documented sources, `ok=false` → hide, and the
   ChatGPT subscription rows pinned at a known `$0.00`.

## Wave 2 — wiring

4. `internal/app/tui.go`: `header()` reduced to zero lines at ≥56 cols and
   one compact line (`Lisa · <repo basename>`) below; every
   `len(m.header())` consumer (bodyHeight, composer, popups, page math)
   audited against the zero-line form; the fresh-session logo entry now
   carries the bare `logo` constant only (`run.go` — the embedded
   `Repository:` line is gone).
5. `internal/app/status_line.go`: Lisa mark moved to the bottom-right end
   of the row (first thing dropped under width pressure, ≥70-col
   threshold unchanged); `contextSegment` upgraded to
   `ctx <n>% · <used>/<window>` with the narrow bare-percentage fallback
   and the honest `ctx —`; new `envSegment` (`macOS arm64`) and
   `spendSegment` (`$0.42`; hidden when pricing is unknown); model
   rendered through `ModelDisplayName`.
6. `internal/app/status_sources.go`: `currentBranch` and `gitStatusCounts`
   deleted; one bounded `git status --porcelain -b`
   (`gitStatus` → `parseGitStatus` → `gitState`) now feeds branch,
   detached short SHA, `↓n↑m`, staged/changed, and untracked counts.
   Refresh sites (session start, tool results) unchanged.
7. `internal/app/sessionname.go`: one generate call after the first
   completed turn of a fresh session (`autoNameAfterFirstTurn` +
   `nameGeneratedMsg`), sanitized to 2–6 words, persisted via the normal
   SQLite save path; failure/early exit silently keeps the derived title
   (`internal/session/session.go` gained the `NamedTitle` field).

## Deviations recorded (from the implementation wave)

- `ctx —` replaces the original "bare percentage when usage headers
  report one" wording for unknown-window cases (no-fabrication rule;
  see spec.md "Documented deviations").
- `currentBranch` was deleted rather than kept beside the porcelain call:
  branch text now comes entirely from the `-b` header line, superseding
  the original reading of "no new git invocations" as a second git
  source.

## Verification

- `go test ./...` green (2026-09-30): the whole suite, including the new
  statusbar/sessionname tests and every layout-viewport assertion.
- Outstanding: a real-terminal walkthrough (actual mark placement,
  narrow-degradation glyph widths, scrollbar absence) gates phases 2–3.

## Phase 2 — content-width breakpoints + overflow invariant

Audit result first: the audit found the composer path (`composer.go`) and
both popup paths (`mentions.go`, `commandcomplete.go`) already correct — no
fixes were needed there. All code changes landed in `internal/app/tui.go`,
with the invariant pinned by tests in `tui_test.go` and the new
`composer_overflow_test.go`.

1. `internal/app/tui.go`: `contentWidth(termWidth)` — the viewport minus
   two padding columns, capped at a 120-column measure above 140 columns —
   becomes the wrap width in `rebuild()` for conversation entries, pending
   review bodies, and the narrow-header fallback re-wrap. The status bar,
   composer, and popups intentionally keep the full terminal width.
2. `internal/app/tui.go` (logo guard): raw logo lines, which skip the
   wrapper, now hard-wrap at content width when a line is wider than the
   viewport — a no-op for the current fixed-width art, but the one raw
   render path is now provably safe (`TestLogoLinesStayWithinTheViewport`
   also pins every logo line at ≤ 33 display cells).
3. `internal/app/tui.go` (dialog fixes found by the audit): dialog box
   width is measured with `runewidth.StringWidth` instead of `len()`, so
   CJK labels (3 UTF-8 bytes per display cell) no longer inflate the box;
   and the latent provider-column bug — the width accounting still reserved
   the provider column after the too-narrow layout dropped it from
   `/models` rows — is fixed by dropping it from the accounting too
   (`TestDialogFitsCJKLabelsAndDropsProviderColumnWhenNecessary` covers
   both).
4. Invariant tests, "no rendered line exceeds the viewport" in display
   cells via `assertRowsWithinViewport`: conversation body against long
   tokens, CJK, and control/zero-width runes at widths 40–200 including the
   cap sides (`TestBodyNeverOverflowsTheViewport`, which also pins the
   120-column cap); pending reviews (`TestPendingReviewNeverOverflows…`);
   logo (`TestLogoLinesStayWithinTheViewport`); dialogs (CJK-wide and
   narrow-drop); composer drafts in every style at 40/80/141 columns
   (`TestComposerNoOverflowAnyStyleAnyWidth`); mention/command popups
   (`TestMentionPopupRowsNeverOverflow`,
   `TestCommandPopupRowsNeverOverflow`); the breakpoint function
   (`TestContentWidthBreakpoints`); resize re-flow 141 → 100 columns with
   no over-wide row left behind (`TestResizeReflowsToNewWidth`).

## Verification (phase 2)

- `go test ./...` green (2026-09-30), including the phase 2 invariant
  suite; the named phase 2 tests were additionally run individually to
  confirm each pins what its box claims.
- Outstanding: the real-terminal walkthrough now gates phase 3 only —
  for phase 2 it covers the absence of a horizontal scrollbar at the
  tested widths, which string-level assertions alone cannot observe.
