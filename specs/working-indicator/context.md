# Context: Working indicator (ephemeral activity row with subtle motion)

Code paths this feature touches. No design decisions hidden in prose — the
behavior in [spec.md](spec.md) is the whole contract.

## Code

- `internal/tui/tui.go` — `ui` fields (60-141, adds `activity`,
  `activityFrame`); `Init` caret loop (212-231, untouched — the activity
  tick is armed per-run, not at startup); `waitEvent` (233-235, batched
  with the tick); `startTurn` (240-302, shows activity with
  `Waiting for model`, batches tick; nil-client early return shows none);
  `sendHeldQueue` (326-340, inherits via `startTurn`); `startCompaction`
  (391-425, shows activity with `Compacting…`); TurnEvent gate (570-573)
  and cases: `context` (575-580, ignores), `reasoning` (581-594),
  `text` (595-602), `approval` (603-614, hides before takeover),
  `tool_start`/`tool_result` (615-632, result re-arms), `steer`
  (633-656, re-arms), `compacted` (657-673), `done`/`error` (674-735,
  hide first); cancel keypress (967-978, tick keeps spinning until the
  terminal event); `persist` (1227-1243, skips `Working` like `Logo`).
- `internal/tui/scroll.go` — beside the caret clock (92-101):
  `activityTickMsg{runID}`, `activityTickInterval` (120 ms), tick
  constructor; tick handling never touches `scroll`/`following`.
- `internal/tui/view.go` — `rebuild` (52-161): `Working` special-case in
  the role switch (130-149) — no `Role: ` prefix, `<spinner> Working…`
  through the existing `wrap`/`fit` path; `renderSwatches` (244-267) and
  the overflow helpers (`wrap` 381-415, `fit` 421-446) unchanged.
- `internal/ui/theme.go` — read-only: `Muted` base (`Muted: style(...)`
  371) and `Title` accent (364) supply the two activity colors; no new
  roles, no palette changes.
- `internal/ui/glyphs.go` — untouched: spinner frames are literal ASCII,
  never glyph-set output.

## Tests to extend

- `internal/tui/status_view_test.go:30-41` — `driveTurn` for real-turn
  show/handoff/cleanup tests; `status_line_test.go:223` for the submit
  path; `tui_test.go:1233-1244` for multi-turn sequencing.
- `internal/tui/tui_muted_tools_test.go` — activity tests mirror its
  arm-directly pattern (`working = true; runID++`, drive events through
  `Update`); `bands_test.go` for per-theme render + forced-profile
  degradation (`SetColorProfile`, `forceANSI` in
  `dialog_test_helpers_test.go`).
- `internal/tui/composer_overflow_test.go` — `plainWidth` + `stripANSI`
  pattern for the 40–200 no-overflow assertions on the activity row.
- `internal/tui/scroll_test.go:23` — caret-tick precedent for asserting
  the activity tick never moves scroll/follow state.
- `internal/tui/session_test.go` / `tui_test.go:1196` (`persist` call
  sites) — Working-exclusion assertions alongside the Queued ones.

## Related specs

- [v1-spec.md](../v1-spec.md) — no FR change expected (§5 reliability
  bullet and §7 activity distinction already describe this).
- [steering-prompts/](../steering-prompts/spec.md) — the queue/flush
  (`sendHeldQueue`), `steer` re-arm, and `Queued`-vs-`Working` furniture
  precedent; this spec never confuses a deliverable queue row with the
  non-deliverable placeholder.
- [conversation-compaction/](../conversation-compaction/spec.md) —
  `startCompaction` / `compacted` is the second arming site.
- [tool-rendering-terminal-keys/](../tool-rendering-terminal-keys/spec.md) —
  the Muted role this row renders in.
- [adaptive-themes/](../adaptive-themes/spec.md) — band/role/degradation
  rules the sweep obeys (existing roles only, meaning never carried by
  background alone).
- [tui-layout/](../tui-layout/spec.md) — phase-2 no-overflow invariant
  the activity row inherits.
- [README.md](../README.md) — gains the feature index row at
  implementation start.

## Precedent

- External: OpenCode `spinner.tsx` (compact spinner + static `⋯`
  fallback), OMP `loader.ts` + `shimmer.ts` (live working row, colored
  sweep — Lisa's closest reference, slowed to a 120 ms tick), Claude Code
  `SpinnerAnimationRow.tsx` + `settings-reference`
  (`spinnerVerbs` / `prefersReducedMotion` — both deferred here).
  Cited in spec.md § Context.
- House: `Logo` + `Queued` per-run furniture in `persist()`; `abandon`
  channel one-way pattern; `driveTurn` real-turn test harness.
