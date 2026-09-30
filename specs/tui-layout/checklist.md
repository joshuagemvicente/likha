# Checklist: TUI layout adjustment

Observable outcomes, mirrors [spec.md](spec.md). Status words per
[specs/README.md](../README.md) rules. Phase 1 boxes are ticked where the
automated suite verifiably covers the outcome; the remaining ticks wait for
a real-terminal walkthrough (TUI visuals, scrollbar behavior), which is the
gate for phase 3. Phase 2's string-viewport invariant suite is green as of
2026-09-30 (phase 2's own record below); all boxes covered by a green
`go test ./...` run.

## Phase 1a — header declutter + mini logo
- [x] No top header at ≥56 columns; page 1 opens with the logo block only.
- [x] Small Lisa mark renders at the bottom-right of the status bar and
      retires before identity information clips. *(Verified against
      rendered string output; an actual terminal cursor position is a
      walkthrough item.)*
- [x] Below 56 columns a single compact identity line renders (FR-12 intact).
- [x] The logo block contains no repository path.
- [x] Page-boundary and popup layout math stay correct with a zero-line
      header across resizes.

## Phase 1b — status bar content
- [x] Context shows `used% · used/total` when the window is known;
      percentage-only and `ctx —` degradations never fabricate numbers.
      *(Implemented deviation: with an unknown window the segment reads
      `ctx —`, not a bare percentage — see spec deviations.)*
- [x] Model display name appears where the map has one; slugs unchanged in
      `/models`, stored config, and API calls.
- [x] Folder and Branch are default-on; branch updates mid-session.
- [x] One bounded git call yields branch, staged, worktree-dirty,
      untracked, and ahead/behind; failures hide segments silently.
- [x] Environment segment shows OS and arch.
- [x] Session spend shows provider-reported or table-priced dollars, `$0.00`
      for the subscription row, hides for unknown pricing.
      *(Pricing table exercises every priced turn through a mocked
      provider stream; a real provider's reported cost has not yet been
      observed live — walkthrough item.)*
- [~] At minimum width the bar degrades gracefully (identity + ctx + page
      position survive; nothing clips mid-glyph). *Fit accounting is
      asserted in plain-cell measurements; true glyph widths on a real
      terminal are pending the TUI walkthrough.*

## Phase 1c — auto-generated session names
- [x] Fresh sessions get a model-generated name after the first completed
      turn, visible in the status bar, `/sessions`, and resume dialogs.
- [x] Exactly one generation per session; resumed sessions never re-generate.
- [x] Naming failure or early exit keeps the derived title with no error
      entry and no impact on the session.

## Phase 2 — single scrollbar (implemented, local)
- [~] No horizontal overflow at any tested width across all render paths —
      conversation body, pending reviews, logo, dialogs, composer (every
      style at 40/80/141 columns), and the mention/command popups, all
      pinned in display cells on ANSI-stripped render rows.
      *(Verified by the string-viewport invariant suite only; actual
      scrollbar absence on a real terminal is still pending the
      walkthrough.)*
- [x] Content width follows the agreed breakpoints — viewport minus two
      padding columns, capped at 120 above 140 columns — and re-flows on
      resize (`TestContentWidthBreakpoints`,
      `TestResizeReflowsToNewWidth`).
- [x] Tests pin the no-overflow invariant per render path
      (`TestBodyNeverOverflowsTheViewport`,
      `TestPendingReviewNeverOverflowsTheViewport`,
      `TestLogoLinesStayWithinTheViewport`,
      `TestDialogFitsCJKLabelsAndDropsProviderColumnWhenNecessary`,
      `TestComposerNoOverflowAnyStyleAnyWidth`,
      `TestMentionPopupRowsNeverOverflow`,
      `TestCommandPopupRowsNeverOverflow`).

## Phase 3 — role backgrounds
- [ ] User input, tool output, and model output have distinguishable
      backgrounds; reasoning keeps its muted foreground.
- [ ] Backgrounds degrade to fully legible plain text on limited terminals.
- [ ] No animation or flash is introduced (FR-15).

## Gates
- [x] `go test ./...` passes; no skipped or mock-only tests claimed as
      coverage (git-dependent tests skip only when the `git` binary itself
      is absent from PATH, and say so).
- [x] Status of this feature is only ever: planned, in progress,
      implemented (local), or verified (release). *Currently
      implemented (local), phase 3 pending.*
