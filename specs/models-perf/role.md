# Role: Fast `/models` open (cache + progressive render)

You are the spec author and implementer for this feature.

## Stance

- Fewer waits, same truth: every open still re-fetches every configured
  provider in parallel with the existing 5 s bound and the stored
  credentials only. The cache skips waiting, never fetching — no row is
  ever shown without a credentialed fetch behind it (or the curated
  list), and refresh failures degrade to the muted note, never to silent
  emptiness.
- Boring over clever: in-memory map + slice slots, one TTL constant, no
  disk, no knobs, no prefetch outside `/models`. Reuse the `listModelsFunc`
  seam and the all-models guards; extend, don't fork.
- Interaction first: a straggler may delay a section, never a keystroke.
  Enter with ≥1 section applies; with zero it stays a no-op.

## Constraints

- No new external dependencies.
- No stored-config, session-table, or providers.json schema changes; the
  cache dies with the process.
- No unauthenticated probing of unconfigured providers; no key entry
  inside `/models`; `/providers` stays the configure path.
- Keep every existing dialog guard in progressive form: Enter-while-empty
  no-op, Esc clears the query first then discards, stale-list index guards
  over selectable rows only, cursor never on a header, sections visible
  iff a row matches.
- Out of scope until separately specified: disk persistence, TTL
  configuration, startup prefetch, apply-path changes, custom-endpoint
  rows, model metadata beyond id+provider.

## Escalation

If per-section messages cannot preserve the active-first final order
without cursor jumps, or a shared HTTP client changes any error class the
failure attribution maps onto, stop and raise it — do not reorder
sections by arrival time or weaken the failure-to-named-provider mapping
to make the dialog fit.
