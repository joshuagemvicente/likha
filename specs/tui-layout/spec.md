# Feature: TUI layout adjustment (status bar enrichment, header declutter, overflow fix, role backgrounds)

**Status:** implemented (local) — phases 1–3 done. The whole
automated suite (`go test ./...`) is green; the only unverified aspect is how
phases 1–3 render in a real terminal (see checklist.md).

## Context (implemented for phases 1–3; phase 3 via adaptive-themes, walkthrough pending)

- `internal/app/tui.go` `header()` returns zero lines at ≥56 cols; below 56
  it renders one compact line, `Likha · <repo basename>` (the logo block is
  skipped there). Every `len(m.header())` consumer (bodyHeight, composer
  limit, popup budgets, page math) tolerates the zero-line form; a long
  basename re-wraps into the body instead of clipping.
- `internal/app/run.go` ships the bare ASCII `logo` constant; a fresh
  session appends it as the transcript's first entry, never persisted, and
  it renders pure-ASCII with no repository path.
- The bottom status bar (`internal/app/status_line.go`) renders: provider +
  model (`statusIdentity`, model via the curated display-name map with slug
  fallback), `ctx` (`contextSegment`: `ctx 34% · 68k/200k` wide, `ctx 34%`
  narrow, `ctx —` when unmeasured or the window is unknown), Likha mark
  bottom-right (≥70 cols), then the gated segments — `Folder` (home-relative),
  git state (`gitState` from `gitStatus` in `status_sources.go`: branch,
  detached short SHA, `↓n↑m`, staged/dirty counts, untracked), `MCP`,
  session title (`statusSessionTitle`, model-generated when present),
  `envSegment` (`macOS arm64`), `spendSegment`, minutes, `tokensSegment`,
  version, and the update notice.
- `internal/app/sessionname.go` + `internal/app/tui.go`
  `autoNameAfterFirstTurn` fire the one naming call after the first
  completed turn of a fresh session; `internal/model/naming.go`
  (`ModelDisplayName` + curated catalog) feeds the display-name map and
  `internal/model/pricing.go` (`TurnCost`) prices the spend segment.
- Body content hard-wraps at `contentWidth(m.width)` in `rebuild()` — the
  viewport minus two padding columns at every width (the phase-2 120-column
  cap was removed: lines reach the right edge); resize re-flows via
  `layoutWidth = 0`.
- Themes (`internal/ui/theme.go` + `internal/ui/adaptive.go`) carry foreground
  roles plus the phase-3 canvas/band backgrounds (`BgBase`, `BgUser`,
  `BgTool`, `BgModel`); reasoning stays muted-flat, tools muted-on-band.

## Phase 1a — Header declutter + mini logo (bottom-right)

User-visible behavior:

1. On terminals ≥56 cols the top header disappears; page 1 opens with the
   ASCII logo block only (its embedded `Repository:` line removed — the
   logo is pure logo; the path lives in the status bar's Folder segment).
2. A small Likha mark renders at the bottom-RIGHT of the status bar so the
   harness identity is always visible: the existing left-side "Likha"
   wordmark MOVES to the right end (one line, theme Title style, first
   thing dropped on narrow screens). A multi-line ASCII mark is rejected:
   the status bar is height-stable by design (`statusLineHeight`).
3. Below 56 cols (logo skipped) a single compact header line remains —
   `Likha · <repo basename>` — so FR-12 identity exists on the smallest
   terminals. (Resolved: `Likha · <basename>`.)
4. `statusLineOpts` defaults change: `Folder` and `Branch` become default-on
   (they now carry what the header dropped); `statusIdentity` stays
   mandatory.
5. Layout accounting: `header()` returning zero lines must keep
   `bodyHeight`, `composerLines`, popup budgets, and page-boundary math
   correct; every `len(m.header())` subtraction is audited in the change.
6. Spec impact: FR-12 and §7 header wording were amended in v1-spec.md
   before implementation (FR-12 now states the ≥56-col zero-line header,
   the pure logo block, and the bottom-right Likha mark; §7 matches).

## Phase 1b — Status bar content (detailed)

The bottom bar becomes the single source of session state. Segments, in
priority order (optional segments retire left-side first before anything
clips; identity never retires entirely):

1. **Context window** — upgrade `contextSegment` from bare percentage to
   used/total with remaining: `ctx 34% · 68k/200k` on wide screens, degrading
   to `ctx 34%` (narrow two-row status) and to `ctx —` (no measured usage or
   unknown window). The window source stays the existing documented table
   (`model.ContextWindow`); a model missing from the table never shows a
   fabricated number (existing rule; see "Documented deviations" below).
   Warning styling at ≥80% stays.
2. **Model display name** — the status bar shows the model's DISPLAY name,
   not the raw slug where one is known (e.g. `gpt-5.5` → `GPT-5.5`). Source:
   a curated display-name map beside `windows.go`, falling back to the slug
   (resolved: curated map, maintained alongside the window table per
   release). `/models` and stored config keep using slugs — display-only
   change.
3. **Folder** — existing `statusFolder` (home-relative), default-on.
4. **Git state** — extend the existing bounded `git status --porcelain` call
   to `-b` (branch header line) so one command yields: branch name,
   staged count, worktree-dirty count, untracked count (porcelain `??`
   split out as its own segment), and ahead/behind vs upstream, rendered
   with compact arrows (resolved: `↓1↑2`). No new git
   invocations; same 3 s bound and hide-on-failure behavior. Detached HEAD
   renders the short SHA. No upstream → no ahead/behind segment. Nerd Font
   opt-in rule applies to any glyph choices. (See "Documented deviations"
   below — the inline `.git`/HEAD `currentBranch` reader was deleted;
   branch text comes entirely from the `-b` header line via `gitState`.)
5. **Environment** — new segment: OS + arch from `runtime.GOOS`/`GOARCH`,
   rendered as `macOS`/`Linux`/`Windows` + `arm64`/`amd64` (e.g.
   `macOS arm64`), gated by width exactly like other optional segments.
6. **Session spend** — new segment: cumulative dollars for the session,
   `$0.42` (cents precision under $1). Sources in priority order:
   provider-reported cost when the response carries it, else a curated
   local price table keyed by model slug *(superseded by
   [model-metadata](../model-metadata/spec.md), user-approved 2026-10-04:
   the bundled catalog keyed by provider and model)* (per-Mtok input/output, sourced
    like `windows.go`), else the segment hides. ChatGPT plan allowance does
    not imply a known zero-dollar charge: after the SIWC migration, show a
    monetary cost only when reported, because account-side credits may be
    separately enabled. See `chatgpt-plus` and `model-metadata` (2026-10-05).
    NEVER fabricate charges for unknown models. (Resolved: reported + curated table. Open follow-up
   only if the table proves costly to maintain; the raw token counts stay
   in `tokensSegment`.)
7. Everything existing that is not listed (MCP summary, minutes, version,
   update notice, session title) keeps its current behavior and priority.

## Phase 1c — Auto-generated session names

User-visible behavior:

1. After the FIRST completed turn of a fresh session, Likha generates the
   session name with ONE plain model call (no subagents, §2) conditioned on
   the user's prompt and the assistant's response — e.g. prompt "I want you
   to create this feature" yields a name like "Feature scaffolding".
2. The name is short (2–6 words, no quotes, no trailing period), replaces
   the derived-from-history title everywhere: the status bar Session
   segment, `/sessions` listings, and resume dialogs.
3. Generation is fire-and-forget: it never blocks the next prompt, shows no
   transcript entry, and a failure silently keeps the derived title (a
   naming failure must never surface as a conversation error).
4. The stored session title is updated in SQLite exactly like a completed
   event; resume shows the generated name.
5. Rules: exactly one generation per session (first turn only — later
   renames are the user's job, e.g. a future `/title` command is out of
   scope here); resuming a session never re-generates.
6. Resolved: generation runs after the first COMPLETED turn (the name can
   reflect what actually happened), using the session's configured
   provider/model (single-provider stance, same as transforms); if the user
   exits mid-generation the name simply stays derived — the session is
   unaffected.
7. *Implemented note:* all of 1a/1b/1c shipped together in one wave — the
   `Folder`/`Branch` default flip with the `*bool` pointer migration
   (nil = on, explicit false disables; see `flagEnabled` in
   `internal/app/config.go`) included, so every config string in README
   examples changed shape accordingly.

---

## Documented deviations from the original plan (2026-09-30)

1. **`ctx —` when the window is unknown.** The original wording allowed a
   bare percentage "when provider usage headers report one" for models
   missing from the window table. Implementation renders `ctx —` instead:
   without a documented window any percentage has no denominator and would
   be fabricated, consistent with the no-fabrication rule. The bare
   `ctx 34%` survives only on the two-row narrow status when a real window
   exists (`TestContextSegmentFormats`).
2. **`currentBranch` deleted.** The original "no new git invocations" note
   described `currentBranch` (.git/HEAD reader) coexisting with the
   extended porcelain call; since branch text now comes entirely from the
   `-b` header line, the helper (and `gitStatusCounts`) became dead code
   and was deleted. `gitStatus`/`parseGitStatus`/`gitState` supersede both.
   The guarantee — one bounded `git status --porcelain -b` invocation,
   refreshed at session start and after each tool result — is unchanged.

## Phase 2 — One scrollbar, terminal-width breakpoints (implemented, local)

Unchanged from the approved plan, now verified by the suite:

- **Content width at every width.** `contentWidth(termWidth)` is the
  viewport minus two padding columns with no capped measure (the original
  120-column cap above 140 terminals was removed: long lines reach the
  right edge instead of stopping short of it). `rebuild()` applies it to
  conversation entries, pending review bodies, and the narrow-header
  fallback re-wrap. The status bar, composer, and popups keep rendering at
  the full terminal width.
- **Logo guard.** The startup logo block is raw, pre-formatted ASCII that
  skips `rebuild()`'s wrapper; as a safety net its raw lines now hard-wrap
  at content width if any line is wider than the viewport (a no-op for the
  current 31-column art; every logo line is pinned at ≤ 33 display cells).
- **Dialog sizing fix.** The dialog box width is now measured in display
  cells (`runewidth.StringWidth`) instead of UTF-8 bytes, so CJK labels no
  longer inflate the box. The same probe exposed and fixed a latent bug:
  the provider column in `/models` rows stayed reserved in the width
  accounting after the too-narrow layout dropped the column from the row,
  inflating the box. Both are covered by
  `TestDialogFitsCJKLabelsAndDropsProviderColumnWhenNecessary`.
- **No-overflow invariant, pinned per render path** — "no rendered line
  exceeds the viewport" in display cells, asserted by
  `assertRowsWithinViewport` on ANSI-stripped rows:
  - Conversation body with unbreakable tokens, CJK, control/zero-width
    runes, at widths 40–200 (`TestBodyNeverOverflowsTheViewport`, pinning
    the viewport-minus-two wrap bound).
  - Pending multi-page reviews (`TestPendingReviewNeverOverflowsTheViewport`).
  - Logo lines (`TestLogoLinesStayWithinTheViewport`).
  - Dialogs (`TestDialogFits…`, both CJK-wide and narrow-drop cases).
  - Composer drafts in every composer style at 40/80/141 columns — long
    tokens, CJK, control and zero-width runes
    (`TestComposerNoOverflowAnyStyleAnyWidth`).
  - `@`-mention and `/`-command popups
    (`TestMentionPopupRowsNeverOverflow`, `TestCommandPopupRowsNeverOverflow`).
  - The breakpoint function itself (`TestContentWidthBreakpoints`).
- **Audit outcome.** The composer (`composer.go`) and popup paths
  (`mentions.go`, `commandcomplete.go`) needed no fixes — they were already
  correct; the tests now pin that. The changed files are `tui.go` (breakpoints,
  logo guard, dialog cell-width + provider-column fix), `tui_test.go`, and
  the new `composer_overflow_test.go`.
- **Resize re-flow.** Lines laid out at the capped width re-wrap, and no
  over-wide row survives (`TestResizeReflowsToNewWidth`, 141 → 100 columns).

The real-terminal scrollbar-absence walkthrough stays pending (checklist).

## Phase 3 — Differentiated backgrounds for user / tool / model content

Status: **landed 2026-10-01 via `specs/adaptive-themes/`** (read that spec as
the buildable contract for this phase — its status is implemented (local),
walkthrough pending). This section stays as the
requirements record; the adaptive-themes spec owns the design that closed
the three open decisions:

- Tints: derived via `mix()` (accent toward base canvas, ~8–12%) with a
  per-family override map only where a derived tint fails the contrast
  gate — no hand-picked 88-cell table.
- Tool activity vs result: one shared `BgTool` band; distinction stays in
  prefix text.
- Reasoning background: none — `Muted` foreground only; the quietest layer
  stays flat.

Required behavior (unchanged): `BgUser`/`BgTool`/`BgModel` (+ `BgBase`
canvas) roles with light/dark variants via `lipgloss.AdaptiveColor`;
full-width bands (padding paints for free in `mainView`'s
`style.Render(fit(line, width))`); muted foregrounds preserved; `Error`
fails by foreground; `Likha`/system + logo sit on `BgBase`; status bar,
composer, scrollbar, and dialogs render on `BgBase`; composer input uses
`Normal` fg; degradation under limited profiles collapses bands to legible
plain text with `You:`/`Tool:`/… labels and fg roles intact (FR-15).

> Superseded for the transcript by [transcript-redesign](../transcript-redesign/spec.md) (user-approved 2026-10-04): glyphs replace the `You:`/`Tool:`/… labels; only the user prompt keeps a band.

## Post-refactor path map (verified 2026-09-30)

The system-architecture and Phase 1.5 changes (see
`specs/structure-refactor/`) moved the files this spec named. Phase 3
touch points, current locations:
| Old path (this spec's history) | Current path |
| --- | --- |
| `ui/theme.go` (`Named`/`Resolve`/`fromPalette`, `Theme` roles, families) | `internal/ui/theme.go` — new roles land here; new file `internal/ui/adaptive.go` (`mix`, `legible`, AdaptiveColor constructors) per adaptive-themes M2 |
| `internal/app/tui.go:1697-1708` (`rebuild()` entry→style switch: `Reasoning`/`Tool` → Muted, `Error` → Error, rest bare) | `internal/app/tui.go:1415-1430` — the exact switch phase 3 re-maps (`You` → Normal-on-BgUser, `Assistant` → Normal-on-BgModel, `Tool` → Muted-on-BgTool) |
| `internal/app/tui.go` `mainView` + `lineStyles` | Unchanged mechanism: full-width bands need no new layout math; adding backgrounds cannot alter visible widths (`stripANSI`/`splitAtWidth` at `internal/app/tui.go:1667-1712`), but the phase-2 overflow suite is re-run as the regression gate |
| `internal/app/tui.go` `updateDialog`/`confirmDialog`/`applyTheme` (preview per adaptive-themes M1) | Same file post-1.5 UNLESS Phase 1.5 landed meanwhile — then `setup.go`/`dialog.go`/`view.go` per `specs/structure-refactor/architecture-phase-1.md` §11; resolve by symbol name, not line number |
| Entry roles consumed by the band map | `"You"` (tui.go:309), `"Assistant"` (tui.go:776), `"Tool"` (tui.go:795,839), `"Reasoning"` (tui.go:768), `"Error"`, `"Likha"`, `"Logo"` — role strings unchanged by phase 3 |

## Acceptance criteria (phases 1–3 verified locally via `go test ./...`)

Automated coverage truthfully disclosed per box. Anything requiring a real
terminal render is left unchecked pending the TUI walkthrough.

### Phase 1a
- [x] No top header at ≥56 cols; page 1 shows the logo block only
      (`TestHeaderCollapsesToFiftySixColumns`).
- [x] Small Likha mark visible at the bottom-right of the status bar at
      wide widths; it retires before any identity information clips
      (`TestLikhaMarkPlacementAndRetirement`). *Mark separation and flush-right
      rendering checked against string output, not a real terminal cursor
      position.*
- [x] <56 cols: single compact identity line (FR-12 intact)
      (`TestHeaderCollapsesToFiftySixColumns`, long-basename re-wrap case).
- [x] The logo block contains no repository path
      (`TestLogoEntryIsPureLogo`).
- [x] Zero-line header keeps page-boundary and popup layout math correct
      across resizes (`TestHeaderCollapsesToFiftySixColumns` asserts
      `bodyHeight`/`pageCount` and a full-height frame per width; popup and
      composer budgets covered by the status-line viewport tests).

### Phase 1b
- [x] Context segment shows used/total tokens with percentage when the
      window is known; degrades to percentage-only and to `ctx —` without
      fabricating numbers (`TestContextSegmentFormats`,
      `TestStatusCtxPercentAndWarningThreshold`).
- [x] The status bar shows the model display name where the map has one,
      the slug otherwise; slugs unchanged in `/models`, config, and API
      (`TestStatusShowsModelDisplayName`; slug-preserving behavior covered by
      the existing dialog/config tests).
- [x] Folder and Branch segments are default-on and dynamic (branch
      refreshes mid-session) (`TestStatusLineConfigPointerDefaults`,
      `TestStatusSessionTitleAndBranchRefresh`).
- [x] Git segments report staged, worktree-dirty, untracked, and
      ahead/behind from ONE bounded git call; failures hide segments
      (`TestGitSegmentsRenderFromGitState`, `TestGitStatus…` family).
- [x] Environment segment shows OS + arch (`TestEnvSegmentText`).
- [x] Session spend renders provider-reported or table-priced dollars,
      `$0.00` for the subscription row, and hides for unknown pricing —
      never a fabricated amount (`TestSpendSegmentPricing`).
- [~] The full bar degrades gracefully: at minimum width, identity + ctx +
      page position remain and nothing clips mid-glyph. *Assertions hold by
      string-prefix/fit accounting in
      `TestStatusViewportAcrossComposerStyles` and
      `TestStatusOptionalOrderAndNarrowControls`; actual glyph widths on a
      real terminal remain a walkthrough item.*

### Phase 1c
- [x] A fresh session's name is model-generated after the first completed
      turn and appears in the status bar, `/sessions`, and resume dialogs
      (`TestAutoSessionNameAppliesAndPersists`, plus the dialog reuse of
      `NamedTitle`).
- [x] Exactly one generation per session; resumed sessions never re-generate
      (`TestAutoSessionNameSkipsResumedSessions`).
- [x] Generation failure or early exit leaves the derived title and a fully
      usable session; no error entry is ever shown for naming
      (`TestAutoSessionNameFailureStaysSilent`,
      `TestAutoSessionNameExitBeforeCompletionPersistsNothing`).

### Phases 2–3
- [x] Phase 2: no horizontal overflow at any tested width, pinned per render
      path — conversation body with pathological tokens/CJK/control runes at
      widths 40–200 (`TestBodyNeverOverflowsTheViewport`, pinning the
      viewport-minus-two wrap bound), pending reviews
      (`TestPendingReviewNeverOverflowsTheViewport`), logo lines
      (`TestLogoLinesStayWithinTheViewport`), dialogs
      (`TestDialogFitsCJKLabelsAndDropsProviderColumnWhenNecessary`),
      composer across all styles
      (`TestComposerNoOverflowAnyStyleAnyWidth`), mention and command popups
      (`TestMentionPopupRowsNeverOverflow`,
      `TestCommandPopupRowsNeverOverflow`), and the breakpoint function
      itself (`TestContentWidthBreakpoints`), with the resize re-flow pinned
      by `TestResizeReflowsToNewWidth`. *(All measured in display cells on
      ANSI-stripped render output; scrollbar absence on a real terminal is
      still the walkthrough item.)*
- [x] Phase 3: role backgrounds distinguishable and degrading to plain text.
  (Landed via adaptive-themes M3: `TestBandRolesAcrossThemes`,
  `TestLogoOnCanvasAcrossThemes`, `TestDefaultBandsAreNoOps`,
  `TestContrastMatrix`, `TestDegradationAcrossProfiles`,
  `TestDegradedBandsKeepWidths`, plus the phase-2 overflow-suite regression.
  Human eyeball pending the walkthrough.)
- [x] `go test ./...` passes for every phase before it is described as done
      (green as of the phase 2 implementation).

## Resolved decisions (questionnaire, 2026-09-30)

1. Phase 1a narrow fallback: `Likha · <basename>` below 56 cols.
2. Phase 1b ahead/behind rendering: compact arrows (`↓1↑2`).
3. Phase 1b model display names: curated map beside the window table.
4. Phase 1b session spend: provider-reported cost + curated price table;
   hide when unknown; `$0.00` for the subscription row.
5. Phase 1c generation: after the first completed turn, with the session's
   configured model; early exit keeps the derived title.
6. Phase 2 content-width cap: removed — the transcript wraps to the
   viewport minus two padding columns at every width.

All phase 1a–1c decisions are resolved and implemented; the two wording
deviations from this questionnaire's original assumptions are recorded
above ("Documented deviations"). Phase 3's three items (palette tints,
tool-band split, reasoning background) were closed by
`specs/adaptive-themes/spec.md` "Resolved decisions": derived tints via
`mix()` + override-only-on-failure, one shared `BgTool` band, no reasoning
background. The adaptive-themes spec is authoritative for those three; this
spec keeps the requirements record only.

## What remains for phase 3 (owned by adaptive-themes)

- The real-terminal walkthrough that retires the walkthrough items noted in
  the acceptance criteria and checklist — for phase 2 it covers the absence
  of a horizontal scrollbar at the tested widths (the invariant is pinned
  against rendered string output only); for phase 3 the preview sweep, band
  legibility, and degradation eyeball (adaptive-themes open item 3).
- Phase 3 implementation is landed (see the boxes above); only the
  walkthrough stays open.
