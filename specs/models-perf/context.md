# Context: Fast `/models` open (cache + progressive render)

Code paths this feature touches. No design decisions hidden in prose — the
behavior in [spec.md](spec.md) is the whole contract.

## Code

- `internal/tui/models_all.go` — `fetchAllModels` (today: per-row
  `StoredKey`/`StoredOAuth` reads, `wg.Wait` over all targets, one
  combined `allModelsMsg`); `modelsTarget` / `allModelsMsg` (split toward
  per-section messages); `startModelsFetch` (today: loading on, single
  fan-out command — becomes cache-serve + `tea.Batch` of per-target
  commands); `handleAllModels` (today: all-or-nothing populate — becomes
  slot insert + cursor no-yank rule + refresh merge); `modelsSources`
  (joins the single-read path); `applyModelRow` / `activateModelClient`
  (untouched).
- `internal/tui/tui.go` — `ui` struct (new cache field: last-good sections
  by canonical provider name + `fetchedAt`; in-memory only); `Update`
  `allModelsMsg` branch (per-section branches); `lastModels` (unchanged
  shape).
- `internal/tui/dialog.go` — `dialogState` loading guard (Enter no-op only
  with zero sections present now); `confirmDialog` `dialogModels` branch
  (stale-list guards stay over selectable rows); `dialogView` loading
  placeholder (cold-open only); `commandHelp` (only if refresh wording
  needs it).
- `internal/model/client.go` — `ListModels` / `fetchModelList`
  (per-provider fetch, unchanged semantics); `noRedirectHTTPClient`
  (per-fetch construction → shared client, same no-redirect policy).
- `internal/providers/keyfile.go` — `ReadCredentials` (single read per
  fetch set) vs `StoredKey` / `StoredOAuth` (today: one file read per
  provider row).
- Tests to extend: `internal/tui/models_all_test.go` (`listModelsFunc`
  seam counts calls for the zero-call cached open), plus dialog tests for
  staggered arrivals, mid-flight Enter, and cursor no-yank
  (`models_sessions_dialog_test.go` holds the loading/reopen precedent).

## Related specs

- [all-models/](../all-models/spec.md) — grouping, ordering, filtering,
  apply paths, failure attribution; this feature changes only fetch
  timing, never those semantics. Its Non-goals item "no prefetching or
  caching" is superseded here, by design.
- [v1-spec.md](../v1-spec.md) — FR-03 (refinement needs no amendment),
  FR-11 (visible errors: the error path still owns total failure).
- [chatgpt-plus/](../chatgpt-plus/spec.md) — curated list precedent
  (already zero-network; stays the instant row).
- [predefined-providers/](../predefined-providers/spec.md) — accepted
  list, key storage.
- [README.md](../README.md) — feature index row (set to planned now,
  implemented (local) at implementation time).

## Precedent

- House only: `internal/update/throttle.go` (timestamp-throttled network
  check with fail-open on corrupt state — the TTL + fail-to-cache shape);
  the `/providers` dialog (configured-state rows from one credential
  read). No external precedent — no external behavior is being matched.
- Data-structure note: ordering stays a slice in final order with a
  map-by-provider-name for cache/merge slots. The bottleneck is network
  waits, not lookup — nothing fancier is warranted.
