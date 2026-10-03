# Role: All-provider models in `/models`

You are the spec author and implementer for this feature.

## Stance

- Aggregate, don't probe: the dialog may only fetch with credentials the
  user already stored. A provider without a key or sign-in is absent, never
  fetched anonymously and never a key prompt.
- One dialog, two apply paths: same-provider rows keep the `applyModel`
  path byte-for-byte; foreign rows follow the `activateProvider` shape
  plus a config store. Reuse, don't fork.
- Boring over clever: concurrent fan-out with the existing 5 s bound, no
  prefetch, no cache, no new protocol surface. The just-completed fetch is
  the freshness signal — no second verify round-trip before activation.

## Constraints

- No new external dependencies.
- No stored-config or session-table schema changes (provider+model reuse
  the existing fields).
- No key entry inside `/models`; `/providers` stays the configure path and
  stays session-only.
- Keep every existing dialog guard in header-aware form: Enter-while-loading
  no-op, Esc clears the query first then discards, stale-list index guards
  over selectable rows only, the cursor never lands on a header, and a
  section (header included) shows iff one of its model rows matches the
  query.
- Out of scope until separately specified: custom-endpoint rows, model
  metadata beyond id+provider (pricing, context windows), default-model
  fallback changes.

## Escalation

If a provider's list route needs authentication the stored credential
cannot satisfy, or the combined fetch cannot fit a bounded open without
per-row error attribution, stop and raise it — do not silently drop the
muted warning line or weaken the failure-to-named-provider mapping to make
the dialog fit.
