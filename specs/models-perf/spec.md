# Spec: Fast `/models` open (cache + progressive render)

**Status:** implemented (local) — automated suite green; real-TUI walkthrough outstanding.

## Context

Bare `/models` fans out over every configured provider and blocks the dialog
on the slowest one: `fetchAllModels` (`internal/tui/models_all.go:76-166`)
`wg.Wait()`s for every per-provider `ListModels` fetch (5 s bound each)
before emitting one combined `allModelsMsg`. Each fetch builds a fresh HTTP
client (`noRedirectHTTPClient`, `internal/model/client.go:127`) — new
TLS handshake per provider on every open — and there is no cache, so every
open pays full network cost. Timed at ~1 s wall.

Row assembly is O(total rows, hundreds) and negligible: no data structure
fixes this. The fix is fewer network waits per open — serve the last good
list instantly, fill fresh sections as they arrive.

## User-visible behavior

1. A `/models` open within 5 min of a successful fetch renders the last
   list immediately with no `Fetching…` spinner: same sections, same order
   (active provider first, then predefined order), cursor on the live pair.
   The list is interactive at once.
2. After the instant render a background refresh re-fetches every
   configured provider in parallel. Fresh sections replace cached ones in
   place; final order never changes. The cursor stays where the user put
   it — it moves to the live pair on arrival only if the user has not
   navigated yet in that open.
3. A cold open (no cache) or an expired cache renders sections as they
   arrive instead of waiting for all: rows appear in final-order positions.
   Enter applies as soon as at least one section is present; with zero
   sections present Enter still does nothing (today's loading guard).
4. Stragglers never gate interaction. A provider that errors or times out
   contributes no rows and is named in the muted note once known. Cached
   rows stay visible when a refresh fails; the dialog shows the error path
   only when no rows exist at all.
5. Provider-set changes show on the next open: newly configured providers
   fetch live and appear; removed providers' sections disappear (no ghost
   rows). The cache is in-memory only — a restart opens cold.
6. The `chatgpt` row still contributes its curated list instantly with no
   network. Grouping, filtering, cursor, and both apply paths are otherwise
   exactly the all-models behavior.

## Errors

- No cache and every fetch fails or is empty → the dialog error path
  (`loadErr`), as today; Esc closes; the session is untouched.
- Cached rows plus a failed refresh → cached rows stay with a muted note
  naming the unreachable providers; the error path never replaces a
  visible list.
- A corrupt `providers.json` reads as unconfigured for that row (the
  existing `/providers` stance).

## Non-goals

- No on-disk cache and no stored-config / session-table schema changes.
- No TTL flag or env knob; no prefetch at startup or outside `/models`.
- No change to either apply path, `/providers`, key entry, custom-endpoint
  rows, or model metadata beyond id+provider.

## Acceptance criteria

- [ ] A repeat `/models` open within TTL renders the cached list
      immediately (no loading state) with the cursor on the live pair.
- [ ] Background refresh replaces sections in place in final order; the
      cursor is never yanked after the user navigates.
- [ ] A cold open shows the first-arriving section before stragglers
      finish; Enter applies with ≥1 section present and is a no-op with
      zero.
- [ ] A slow/failed provider is named in the muted note; cached rows
      survive a failed refresh; total failure with no cache shows the
      dialog error.
- [ ] A newly configured provider appears and a removed one disappears on
      the next open; a restart opens cold.
- [ ] `chatgpt` contributes instantly with no network; apply paths and
      filtering are unchanged.
- [ ] `go test ./...` from the project root passes with the feature's
      tests included; no mock-only test claims the dialog works.

Refines [all-models/](../all-models/spec.md) without amending it; no
v1-spec amendment (FR-03 refinement, same as all-models).
