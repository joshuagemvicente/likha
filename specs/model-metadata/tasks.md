# Tasks — Model metadata catalog

Ordered tasks. Each ends with a verification step; no checkmarks until it is
done. Every slice ends with `go build ./...`, `go vet ./...`,
`go test ./...`, `go test -race` on touched packages, gofmt, and
`git diff --check`.

## S1 — Catalog file and generator

1. **Catalog types and loader** [x] DONE 2026-10-04 — `internal/model/catalog`: schema types,
   embedded `models.json`, `Lookup(provider, model)`, validation.
   Verify: loader tests (lookup exact pair only, trimmed IDs, unknown pair).
2. **Generator** [x] DONE 2026-10-04 — `internal/model/catalog/gen`: upstream read (`-upstream`
   URL or `-in` file), provider mapping, tool-capable filter, field and tier
   conversion, overrides merge with `ref`/`verified` enforcement, drop,
   deterministic write, change report, failure exits.
   Verify: generator tests on a fixture upstream file (mapping, filter,
   tiers incl. `context_over_200k` fallback, overrides, drop, rejects).
3. **Overrides and first generation** [x] DONE 2026-10-04 — `overrides.json` with the `chatgpt`
   subscription provider and its curated models; generate from models.dev.
   Verify: report reviewed; tests that every mapped provider has entries and
   every `DefaultModel` has an entry or a listed known gap.

## S2 — Lookup and pricing

4. **Pricing** [x] DONE 2026-10-04 — replace `pricingTable`, prefixes, placeholders, and
   `subscriptionZeroIDs` with catalog-backed `RequestCost(provider, model,
   usage)`; tiers; optional-rate fallbacks. Verify: rewritten pricing tests
   (Opus 5.5 $4/$20, Haiku 4.5 $1/$5, no cross-provider borrowing, tiers,
   cache and reasoning rates, unknown hides, ChatGPT $0).
5. **Context windows** [x] DONE 2026-10-04 — catalog fallback in `ResolveContextWindow`
   (`input` limit before `context`); remove the static table. Verify:
   precedence tests updated (override > metadata > catalog > unknown).
6. **Callers** [x] DONE 2026-10-04 — `tui.go`, `agent_detail.go`, `explore/node.go` pass the
   provider. Verify: existing spend/agent tests pass with provider-scoped
   prices.

## S3 — Token accounting

7. **Usage breakdown** [x] DONE 2026-10-04 — parse cache read/write, reasoning, and `cost` in
   chat-completions and Codex usage; expose the full report per request.
   Verify: parser tests per provider shape (OpenAI, OpenRouter, DeepSeek,
   Anthropic-compat empty details, Codex).
8. **Per-request accumulation** [x] DONE 2026-10-04 — agent emits a usage event per request;
   TUI sums every request; explore records the breakdown and reported cost.
   Verify: a multi-round run test counts every request; reported cost wins.

## S4 — UI

9. **Spend marker** [x] DONE 2026-10-04 — `~` when any priced request was estimated. Verify:
   status-line tests for exact, estimated, mixed, hidden.
10. **Rows** [x] DONE 2026-10-04 — `/models` and setup rows show context and price per 1M.
    Verify: row tests incl. narrow widths and unknown parts omitted.
11. **Debug log** [x] DONE 2026-10-04 — catalog source/date in `--debug-models` lines. Verify:
    observer test.

## Close

### SIWC billing correction (2026-10-05)

- [ ] Provider-reported ChatGPT cost wins, including an opted-in credit charge.
      Without reported cost, keep monetary spend unknown rather than infer $0
      from subscription identity. Retain measured tokens and unknown legacy
      subscription-zero task costs. Verify model pricing and TUI spend tests.

12. [x] DONE 2026-10-04 — README, CHANGELOG, specs index, superseded-rule notes in tui-layout,
    tui-redesign, and claude-provider specs. Verify: links resolve.
