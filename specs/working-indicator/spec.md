# Feature: Working indicator (ephemeral activity row with subtle motion)

**Status:** in progress — implementation and feature-specific tests pass; unrelated full-suite session/prompt-history failures and the real-terminal walkthrough remain.

## Context

After the user presses Enter, Lisa enters the working state but shows no
transcript row until the first provider event arrives. `startTurn`
(`internal/tui/tui.go`) appends the `You:` prompt, sets
`m.status = "Waiting for model"` and `m.working = true`, then waits on the
agent event channel. If the connection check, first request, or tool round
takes time, the transcript sits unchanged: the only signal is the static
status-line state text. Users read this as "nothing happened".

Reference TUIs all fill this gap with an explicit working affordance:

- **OpenCode** renders a compact animated spinner plus a status label next to
  it, with a static fallback when animations are disabled
  (`packages/opencode/src/cli/cmd/tui/component/spinner.tsx`: braille frames
  at an 80 ms interval, `⋯` fallback).
- **OMP (Oh My Pi)** renders a live working row with a colored shimmer sweep
  plus an animated spinner glyph (`packages/tui/src/components/loader.ts`:
  braille frames at 80 ms; `packages/coding-agent/src/modes/theme/shimmer.ts`:
  band swept across the message at ~30 fps redraw).
- **Claude Code** renders a spinner with rotating action verbs, a glimmer
  sweep across the verb, elapsed time and token byline, and `spinnerVerbs` /
  `prefersReducedMotion` settings
  (`src/components/Spinner/SpinnerAnimationRow.tsx`: 50 ms clock, 120 ms
  glyph step; `code.claude.com/docs/en/settings-reference`).

Lisa already has the pieces this builds on: theme roles (`internal/ui/theme.go`),
ASCII/Nerd glyph sets (`internal/ui/glyphs.go`), a caret blink tick
(`internal/tui/scroll.go`), per-role transcript bands (`internal/tui/view.go`
`rebuild()`), phase status strings (`internal/tui/tui.go`, `status_line.go`),
and per-run furniture that is never persisted (`Logo`, `Queued` in
`persist()`).

## Resolved decisions (user-confirmed, 2026-10-03)

1. **Placement: transcript + status.** A temporary activity row appears
   directly under the submitted prompt and turns into the first real
   reasoning / answer / tool event. The status line keeps naming the current
   phase (`Waiting for model`, `Reading repository`, …) and the cancel hint.
2. **Motion: subtle sweep.** Readable text stays fixed; a theme-derived accent
   sweeps gently across a short label plus a small changing ASCII mark. No
   large banner, no multi-color noise.
3. **Wording: generic activity.** The placeholder says `Working…` while
   waiting. It never claims to be reasoning and never implies Lisa can reveal
   private chain-of-thought. Provider-streamed `Reasoning` output stays
   exactly what it is today: actual streamed tokens, rendered muted.

## User-visible behavior

1. **An activity row appears on submit, before any provider output.** When a
   turn starts (fresh prompt or held-queue flush), one ephemeral row appears
   immediately under the `You:` prompt: an ASCII spinner cell plus the fixed
   label `Working…`. It also appears when a compaction run starts
   (`Compacting…` status), under the last transcript row.
2. **The row animates while Lisa is working and no real content exists.**
   The spinner cycles fixed-width ASCII frames (`|`, `/`, `-`, `\`); one
   accent cell sweeps across the label text left-to-right and wraps. Text
   never changes, never reflows, never grows beyond one wrapped line.
3. **The placeholder hands off to real activity — never duplicates it.**
   The first `reasoning`, `text`, `tool_start`, or `approval` event of a
   round replaces the placeholder in place: no blank row left behind, no
   flicker of two adjacent rows. `context` telemetry events never dismiss it
   (they are invisible).
4. **Between tool rounds the indicator returns.** After a `tool_result` while
   the run continues, the activity row shows again until the next round's
   first real event. A delivered steering prompt (`steer`) likewise re-arms
   it for the fresh provider round.
5. **Terminal states clean up.** On `done`, `error`, cancellation, or an
   approval review taking over the composer, the animation stops and the
   placeholder row is gone: completion leaves the real transcript behind it;
   errors and cancellation follow the existing error/cancelled reporting
   with no orphan activity row.
6. **The status line is unchanged in shape.** It keeps the existing phase
   strings and queue counts (`Waiting for model`, `Reading repository`,
   `2 queued`, `Cancelling`, `Review …`, `Compacting…`) plus the existing
   Nerd Font marker rule. The transcript row carries the motion; the status
   line carries the phase and controls.
7. **Honest, legible, theme-native.** The label text reads `Working…` with
   color disabled. Color comes only from the active theme's existing roles
   (muted base, accent sweep head) — no new palette entries, no per-speaker
   hue coding. Spinner frames are plain ASCII in every configuration; the
   Nerd Font opt-in never changes them.
8. **Scroll, layout, and persistence behave like furniture, not content.**
   Submit pins the viewport to the bottom as today; animation ticks never
   move the scroll offset or change follow state. The row obeys the
   content-width wrap and the no-overflow invariant at every width
   (40–200). It is never written to SQLite, never restored on resume, and
   never sent to the model.
9. **Limited profiles degrade to motion without color.** Under ANSI /
   no-color rendering the text and advancing ASCII spinner remain; the sweep
   collapses to plain text. Meaning is never carried by background or color
   alone.

## Scope boundaries

- This is a display and timing feature, not a reasoning feature. It does not
  change which messages Lisa sends, provider selection, model output limits,
  tool dispatch, approval gating, session persistence semantics, or context
  accounting.
- No rotating phrases, no configurable verbs, no tips, no elapsed-time or
  token byline, no large ASCII banner. The label is fixed at `Working…`.
- No new settings, flags, environment variables, or config keys in this
  pass. A reduced-motion / animations toggle is explicitly deferred (see
  open item 1).
- No provider or agent protocol changes: the feature consumes the existing
  `agent.TurnEvent` kinds only.
- FR-15 visual restraint otherwise stands: this adds one functional
  animation to one ephemeral row; it does not open decoration elsewhere.

## Functional changes (v1-spec.md)

None requested: the reliability bullet "Show whether Lisa is waiting for the
model, executing a tool, or waiting for the user" (§5) and the TUI contract's
conversation/activity/review/input distinction (§7) already describe this.
This feature implements them; if implementation reveals an FR wording gap,
v1-spec is amended first and the checklist mirrors it.

## Open items

1. **Reduced-motion / animations toggle** — deferred. Claude Code
   (`prefersReducedMotion`) and OpenCode (`animations_enabled`) both ship
   one. Lisa has no such setting today; this pass ships the static-text
   legibility guarantee instead and records the toggle as the follow-up if
   asked.
2. **Compaction label** — the activity row uses the same `Working…` label
   during compaction while the status line says `Compacting…`. If user
   testing finds that mismatch confusing, a follow-up may give compaction
   its own fixed label; no per-phase label system in this pass.

## Acceptance criteria

- [ ] Pressing Enter on a prompt shows the activity row on the next frame,
      before any provider event arrives.
- [ ] The spinner advances on a steady tick and the accent sweep moves
      across the label without changing text, width, or scroll position.
- [ ] The first reasoning / answer / tool / approval event of a round
      replaces the placeholder with no leftover blank row.
- [ ] A run that performs tool rounds shows the indicator again between the
      tool result and the next round's first content.
- [ ] Cancellation, error, completion, and approval takeover each remove the
      placeholder and stop the tick with no orphan row.
- [ ] The row is absent from persisted sessions: resume never restores it
      and a saved snapshot contains no activity role.
- [ ] With color disabled the row still reads `Working…` with an advancing
      ASCII spinner; at 40–200 columns no rendered row exceeds the viewport.
- [ ] `go test ./...` passes with the feature's tests included.
