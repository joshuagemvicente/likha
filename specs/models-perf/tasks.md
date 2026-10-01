# Tasks: Fast `/models` open (cache + progressive render)

**Status:** implemented (local). Ordered slices; the entry point is
`go test ./...` from the project root.

1. **Fetch hygiene: one credential read, one shared HTTP client.**
   Read `providers.json` once per fetch set (`ReadCredentials`, not one
   `StoredKey`/`StoredOAuth` call per row) and replace the per-fetch
   `noRedirectHTTPClient()` construction with a shared client (same
   no-redirect policy) so repeat opens reuse keep-alive connections. Add a
   per-provider elapsed log on the slow path so the next straggler is
   named, not guessed. Verify: table test over a temp state dir — fetch
   set identical to today's for N configured providers plus
   added/removed/corrupt rows; full suite green.
2. **In-memory cache with TTL + background refresh.** New cache keyed by
   provider canonical name holding last-good sections plus `fetchedAt`;
   TTL 5 min; `ui` owns it (dies with the process — no disk, no schema
   change). Open path: fresh cache → render instantly (loading off,
   cursor on live pair) and spawn the refresh as the command's work;
   stale/missing → today's loading state. Refresh merges by key: dropped
   providers vanish, new ones appear, failures only touch the muted note
   and never clear visible rows. Verify: with the injectable
   `listModelsFunc` seam counting calls, second open inside TTL makes
   zero list calls and renders cached rows; expired TTL refetches;
   credential removal drops the ghost section; refresh total failure
   keeps cached rows with the muted note.
3. **Progressive per-section render.** Split the single combined message
   into one message per provider (tea.Batch of per-target commands over
   the existing seam); the handler inserts each arrival into its
   final-order slot (active first, then predefined order) and enables
   Enter with ≥1 section while keeping the zero-section Enter no-op.
   Cursor rule: jump to the live pair on arrival only before the user's
   first navigation in that open; after that the cursor never moves on
   refresh. Verify: dialog tests with staggered fake fetches — first
   section visible before the straggler finishes, Enter applies mid-flight,
   cursor jumps to the live pair pre-navigation and stays put after, Esc
   still discards with nothing applied.
4. **Docs.** CHANGELOG entry, specs/README index row →
   implemented (local); `commandHelp` wording only if the refresh note
   needs it. No v1-spec amendment: this refines FR-03 through all-models.
   Verify: help text renders; `go test ./...` green.
