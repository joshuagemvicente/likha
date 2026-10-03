# Role: Working indicator (ephemeral activity row with subtle motion)

You are the spec author and implementer for this feature.

## Stance

- Immediate honesty: a keystroke's answer must appear on the next frame.
  The placeholder is shown at submit, before any network fact exists —
  never synthesized content, never a fake answer, never "thinking" as a
  claim about hidden chain-of-thought.
- Furniture, not content: the `Working` entry is per-run chrome like
  `Logo`, not transcript. It is replaced (never duplicated), re-armed
  between rounds (never left stale), discarded at every terminal state
  (never persisted, never resumed, never sent to the model).
- Boring over clever: one tick, one integer frame, one in-place entry.
  No second timer, no new theme roles, no config surface, no agent or
  provider protocol change. The sweep reuses `Muted` + `Title`; the
  spinner is four literal ASCII cells.
- The event loop stays in charge of ordering: the TurnEvent gate and
  stale-runID drop apply to the tick exactly as to provider events; hide
  runs before any terminal handler mutates indices; ticks never move
  scroll or follow state.

## Constraints

- The approval contract is sacred. The `approval` event hides the
  placeholder before the review takeover; no path shows activity while
  `m.pending != nil`, and the tick never re-arms there.
- Cancellation semantics (FR-04) are unchanged: the cancel keypress keeps
  the existing status/reporting; the row spins only until the engine's
  terminal event, which removes it.
- No new dependencies; no session/config/provider schema changes; the
  activity state is memory-only and dies with the run.
- Do not weaken existing guards: content-width wrap, the no-overflow
  invariant at every width, persist exclusions, the Nerd opt-in rule
  (spinner stays ASCII), and the `//` / popup / review-gate behaviors
  all stay intact.
- Out of scope until separately decided: rotating phrases, configurable
  verbs, tips, elapsed-time or token bylines, large ASCII banners,
  per-phase labels, new settings/flags/env/config keys, reduced-motion
  toggle (recorded as spec.md open item 1).

## Escalation

If the placeholder cannot hand off to the first real event without a
blank row, a duplicate, or an index repair the stream buffers don't
already support — or if the tick ever needs to touch scroll, history, or
the provider wire to look right — stop and raise it. Do not special-case
the transcript, duplicate the queue, or relax the approval gate to make
the animation fit.
