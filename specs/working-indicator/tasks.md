# Tasks: Working indicator (ephemeral activity row with subtle motion)

**Status:** planned — no implementation started. Ordered; each task carries
its verification. No checkmarks until work starts. Entry point:
`go test ./...` from the project root; `go test -race ./...` before calling
the feature done.

Contract is [spec.md](spec.md) (user-visible behavior only). This file is
the HOW. Line numbers below are the pre-feature locations they refine.

1. **Activity state, tick clock, and lifecycle helpers.** Add to `ui`
   (`internal/tui/tui.go:55-141`): an activity index (`activity int`, `-1`
   when absent) plus a frame counter (`activityFrame int`). Add
   `activityTickMsg{runID uint64}` and
   `activityTickInterval = 120 * time.Millisecond` next to the caret clock
   (`internal/tui/scroll.go:92-101`): one steady tick drives both the
   spinner and the sweep. Helpers (near `startTurn`): `showActivity()`
   (append `entry{role: "Working", content: workingLabel}` when working,
   no duplicate if one is present, `layoutWidth = 0`), `hideActivity()`
   (remove the `Working` entry if still present, repair `streaming` /
   `reasoningStream` indices that sit after the removal, `layoutWidth = 0`,
   no scroll touch), and `hasActivity()`. The placeholder is always the
   trailing entry, so first-event handoff is hide-then-append through the
   existing event logic — no separate replace-in-place helper.
   Tick handling in `Update`: on `activityTickMsg`, drop stale runIDs;
   if no activity is visible, or the run is over (`!m.working`), or a
   review owns the composer (`m.pending != nil`), stop without re-arming;
   otherwise advance the frame, invalidate layout (`layoutWidth = 0`), and
   return the next tick. Ticks never touch `scroll`/`following`.
   *Verify:* unit tests drive `showActivity` / tick / `hideActivity`
   directly: frame advances on matching runID, stale runID drops, tick
   after hide returns nil, scroll offset and `following` are byte-identical
   across ten ticks.

2. **Submit-time display.** In `startTurn` (`tui.go:240-302`): after the
   `You:` append and alongside the existing `status = "Waiting for model"`
   / `working = true` / `jumpBottom()` sequence (`:259-270`), call
   `showActivity()` and return
   `tea.Batch(waitEvent(events), activityTick(runID))` instead of the bare
   `waitEvent`. The no-provider early return (`:250-255`) shows no
   activity. In `startCompaction` (`tui.go:391-425`): same shape —
   `showActivity()` under the last row with `status = "Compacting…"`,
   batched tick. The held-queue flush (`sendHeldQueue → startTurn`)
   inherits this with no extra path. Submit keeps the existing bottom pin;
   re-arms later never move the viewport.
   *Verify:* with an httptest-backed client (the `driveTurn` pattern in
   `status_view_test.go:30-41`), `startTurn` leaves a trailing `Working`
   entry and a `View()` containing `Working…` before any event is driven;
   `/compact` start asserts the same with `status == "Compacting…"`;
   nil-client submit asserts no activity row.

3. **First-event handoff and between-round re-arm.** Inside the TurnEvent
   gate (`tui.go:570-573`): `context` stays invisible and never dismisses
   (`:575-580` untouched). `reasoning` (`:581-594`), `text` (`:595-602`),
   `tool_start` (`:615-632`), and `approval` (`:603-614`) hide the trailing
   placeholder first via `hideActivity()`, then run their existing append
   logic — the first real row lands exactly where the placeholder was, so
   the transcript length is unchanged across the handoff (no blank row, no
   adjacent duplicate). `tool_result` appends its `Tool` entry as today, then re-arms (`showActivity()`) while the run
   continues. `steer` (`:633-656`) keeps its flip/append + history-adopt,
   then re-arms for the fresh provider round. `approval` hides the
   placeholder before the existing pending/review-takeover block.
   *Verify:* scripted event sequences assert entry-count and order:
   show → `context` (still present) → `reasoning` (replaced in place,
   `Reasoning` buffers intact); show → `text` (single `Assistant` entry);
   show → `tool_start` → `tool_result` (activity back after the result);
   show → `steer` (activity present for the new round); show → `approval`
   (no `Working` entry, `pending` set).

4. **Terminal cleanup.** `done` / `error` (`:674-735`), `compacted`
   (`:657-673`), and the cancelled-run terminal path all call
   `hideActivity()` first, then run their existing logic unchanged
   (including the partial-stream removal at `:683-685`, which now operates
   on the post-hide indices). The cancel keypress (`:967-978`,
   `status = "Cancelling"`) deliberately keeps the row spinning until the
   engine's terminal event lands; completion, error, and cancellation all
   end with no `Working` entry and no re-armed tick. `reconcileQueue`,
   `persist`, `refreshStatusSessionTitle`, and `autoNameAfterFirstTurn`
   ordering are unchanged.
   *Verify:* drive a full turn to `done`, a scripted-500 turn to `error`,
   and a mid-stream cancel: each ends with zero `Working` entries, a
   further tick returns nil, and the existing done/error/cancelled entries
   read exactly as before.

5. **Render the row: ASCII spinner plus one-cell accent sweep.**
   Special-case `Working` in `rebuild()` (`internal/tui/view.go:52-161`,
   role switch `:130-149`): it renders with NO `Role: ` prefix — the row's
   plain text is `<spinner> Working…`, spinner from
   `|` `/` `-` `\` at `frame % 4`, label fixed at `Working…`. Base style is
   the theme's `Muted` (the Reasoning/Tool quiet layer); exactly one label
   cell — the sweep head at `frame % labelWidth` — renders in the theme's
   `Title` (accent) foreground; the spinner cell renders in `Title` too.
   No new palette entries, no band background on the row (flat like
   `Reasoning`), Nerd opt-in never changes the frames. The row flows
   through the existing `wrap`/`fit` path so the 40–200 no-overflow
   invariant holds trivially (the row is ~10 cells). With color disabled
   the styles collapse to plain text and the advancing spinner remains the
   signal — assert, don't branch: no profile-specific code.
   *Verify:* `View()` contains `Working…` with the spinner cell present at
   every frame; plain-text (`stripANSI`) row is identical across frames
   while the styled row differs; all 22 themes render it non-empty;
   forced-ANSI and Ascii profiles (`lipgloss.SetColorProfile`, mirror
   `bands_test.go:192-196` / `forceANSI` in
   `dialog_test_helpers_test.go`) keep the text and spinner with no panic;
   widths 40/80/141/200 assert every rendered row within the viewport
   (mirror `composer_overflow_test.go:plainWidth`).

6. **Persistence exclusion.** `persist()` (`tui.go:1227-1243`) skips role
   `Working` alongside `Logo` and `Queued` (`:1234`). Nothing else about
   session save/load changes; resumed snapshots can never contain the
   role, and no migration exists.
   *Verify:* show activity, force a persist (e.g. a `tool_result`), reload
   from the store: zero `Working` entries in the snapshot and none after
   resume; the existing `Queued`-exclusion test still passes unchanged.

7. **Docs and spec bookkeeping.** Add the feature row to the
   [specs index](../README.md) (planned). Add one CHANGELOG Unreleased
   bullet describing the row, handoff, and static-text guarantee. Confirm
   no v1-spec FR change is needed (spec.md § Functional changes); if the
   implementer finds a wording gap, amend v1-spec first per rule 3. Leave
   the reduced-motion toggle and compaction-label questions in spec.md §
   Open items — no settings, flags, env vars, or config keys in this pass.
   *Verify:* index row present with status; CHANGELOG renders; suite
   green; no config/schema diff.

8. **Full acceptance.** `go test ./...`, then `go test -race ./...`.
   Manual walkthrough in a real terminal (Ghostty): submit a prompt and
   confirm the row appears on the next frame before the first token;
   watch a tool round re-arm between result and next content; cancel once;
   resize 40↔200 mid-spin. Record the walkthrough in checklist.md; boxes
   tick only where the suite or the walkthrough covers them.
   *Verify:* both suites green; walkthrough notes recorded.

## Notes

- One clock, two motions: the single 120 ms tick advances the 4-frame
  spinner and the N-cell sweep together (full label sweep ≈ 1 s). Calmer
  and cheaper than OMP's ~30 fps shimmer redraw; no second timer.
- The placeholder is furniture, not content: it follows the `Logo` /
  `Queued` precedent (never persisted, never sent, never resumed) but
  unlike `Queued` it is also never delivered — it is replaced, then
  re-armed, then discarded.
- Ordering inside terminal handlers is load-bearing: hide the activity
  BEFORE the existing partial-stream slice deletion, or the stored
  `streaming` index points one row off.
