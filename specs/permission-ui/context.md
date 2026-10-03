# Context: Approve/Decline buttons for permission reviews

Code paths this feature touches. No design decisions hidden in prose — the
behavior in [spec.md](spec.md) is the whole contract.

## Code

- `internal/tui/tui.go` — pending key block `y`/`Y`/`n`/`N` (719-747),
  page-gate status (725), cancel path kept (753-768), approval event
  handling where `pending`/`reviewSeen` are set and `scroll`/`following`
  reset (455-465), `reviewSeen` clearing on decisions (743-746).
- `internal/tui/composer.go` — pending text override to replace with the
  decision bar (52-58), caret gating (47-50), per-style row assembly
  (74-110) the bar must reuse; `composerLines()` is consumed by both
  `bodyHeight()` and `mainView()` (`view.go:32,178`), which is why the bar
  replaces the composer instead of adding a layout segment.
- `internal/tui/status_line.go` — review hints (78-88): gate row (79-80)
  and normal row (88) carry the `Y/N PgUp/PgDn ^C` fragments to replace;
  `reviewMark` (66-72) unchanged.
- `internal/tui/view.go` — pending proposal layout (78-90); the body
  rendering is unchanged. `pageCount()`/`bodyHeight()` stay as they are.
- `internal/tui/scroll.go` — `markSeenFromScroll` (238-259),
  `scrollPosition` (261-275): the gate truth source (`reviewSeen`) is
  untouched; `reviewReady()` is a new read over it.
- `internal/tui/dialog.go` — the focused-row convention to reuse:
  `theme.Selected.Render(fit("> "+label, …))` (549, 608).
- `internal/ui/theme.go` — the `Selected` role (band per family; `default`
  has no band — the `> ` marker must carry focus there).

## Tests to rewrite / extend

- Approval key sites driving `y`/`n`: `internal/tui/tui_test.go:689,694,
  707,806,812,817,1718`; `internal/tui/status_line_test.go:153,165` (and
  the `Y/N` assertion at 159); `internal/tui/composer_test.go:105,120`.
- Helpers to reuse: `newKeysTestUI` (`tui_m2_keys_test.go:17-21`),
  `composerTestUI` (`composer_test.go:15-19`), `statusTestUI`
  (`status_line_test.go:18-22`), `bandsTestUI` (`bands_test.go:19-22`),
  `stripANSI`/`forceANSI` (`dialog_test_helpers_test.go:15-20`), the
  approval-arm pattern (pending + reviewSeen + `m.events`) in
  `tui_test.go:634-664` and `composer_test.go:95-125`.
- Overflow invariants: `composer_overflow_test.go:28-76` (extend to the
  review bar).
- New tests: focus movement/wrap, default focus, gated Approve (no reply on
  the `Reply` channel, review still open), letter inertness, bar rendering
  per theme family and at 40 columns.

## Related specs

- [v1-spec.md](../v1-spec.md) — FR-22 added; FR-06/FR-07/FR-08/FR-09,
  FR-14 (mouse capture off) unchanged.
- [steering-prompts/](../steering-prompts/spec.md) — the pending review
  blocks queueing/editing; delivery after the tool settles is unchanged.
  Its `context.md` is cross-linked to this folder.
- [tui-layout/](../tui-layout/spec.md) — status-line segments and the
  review page indicator; hint wording only.
- [tool-rendering-terminal-keys/](../tool-rendering-terminal-keys/spec.md)
  — chord-probe discipline; the `Esc`-prefix decay window is unrelated
  (reviews never arm it).
- [adaptive-themes/](../adaptive-themes/spec.md) — the `Selected` band and
  the `default` family's no-band look define the focus-marker fallback.
- [slash-commands/providers.md](../slash-commands/providers.md) (line 204)
  and [update-notification/banner.md](../update-notification/banner.md)
  (lines 207, 247) contain stale `Esc/Y/N` wording to update.

## Precedent

- External: Claude Code permission prompt (option list, `↑/↓`, Enter, Esc,
  Tab; `y`/`n` no longer default), OpenCode approvals (Allow
  once/always/Reject, `←/→`, Enter), OMP (documented prompt body; approve/
  deny selector; ACP `session/request_permission`). Cited in spec.md
  § Research findings.
- House: dialog cursor rendering (`dialog.go:549,608`) and the `Selected`
  theme role; the review gate + `reviewSeen` machinery already in
  `scroll.go`. The decision bar is the composer's pending branch rewritten,
  not a new layout system.
