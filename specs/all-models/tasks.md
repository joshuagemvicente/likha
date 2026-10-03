# Tasks: All-provider models in `/models`

**Status:** planned — not started. Ordered slices; the entry point is
`go test ./...` from the project root.

1. **Aggregation source set + fan-out.** Build the fetch set from stored
   credentials (non-empty `StoredKey` per API-key provider, stored OAuth
   login for `chatgpt`; corrupt reads count as unconfigured, mirroring
   `providersDialogItems`), fetch concurrently with the existing 5 s bound,
   combine in order (active provider first, then predefined order), and
   collect per-provider errors. `chatgpt` contributes `ChatGPTModels` with
   no network. Verify: unit test through the same path the dialog calls,
   with an injectable list function over fake endpoints — combined order,
   failure attribution, all-fail error, curated-list-no-network. (The seam
   is required: `model.Providers` URLs are fixed, so tests cannot point
   rows at `httptest` servers without it.)
2. **Dialog result + rendering.** New all-providers result message feeding
   the `handleModelsResult` shape: combined items as (provider identity,
   model id) pairs with one non-selectable display-name header row per
   section (headers excluded from `dialogMatches` and the cursor space, so
   navigation/Enter/Esc guards must index selectable rows only; a section
   shows iff one of its rows matches the query); cursor on the live pair;
   `dialogLabel` keeps ` (current)` on the live row; no per-row provider
   column and no narrow-drop logic to carry over; loading / partial-warning
   / empty-error states. Verify: dialog tests drive `/models` keystrokes
   and assert headers, cursor-never-on-header, skip-over-header navigation,
   query-hides-empty-sections, every render state, Enter-while-loading
   no-op, and Esc-discard.
3. **Cross-provider apply.** Same-provider row → the existing `applyModel`
   path untouched. Other-provider row → build the client from the stored
   credential (`model.New` / `NewOAuth` with saver and session header),
   swap live state exactly like `activateProvider` (buffers reset, no bleed
   from the previous provider), store provider+model in `config.json`, one
   entry naming both; construction failure closes the dialog with a visible
   error and keeps the old session. No second verify round-trip: the list
   fetch that just succeeded is the freshness signal. Verify: selecting a
   foreign row (fake endpoint) puts the next turn on the new endpoint with
   the new model; failure keeps the old client; stored config holds the
   pair.
4. **Docs.** `commandHelp` ("/models list models from every configured
   provider"), README command table, CHANGELOG, and the specs/README index
   row → implemented (local). No v1-spec amendment: this refines FR-03, it
   does not change the contract. Verify: help text renders the new wording.
