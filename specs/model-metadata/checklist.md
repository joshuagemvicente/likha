# Checklist — Model metadata catalog

Observable outcomes.

## Catalog (S1)

- [x] `go generate ./internal/model/catalog` rewrites `models.json` byte for
      byte the same from the same upstream input.
- [x] The change report lists price changes, added/removed models, and
      provider defaults without entries.
- [x] An override without `ref` or `verified` fails generation.
- [x] No model in `models.json` lacks `source`; every override row has `ref`
      and `verified`.

## Pricing and context (S2–S3)

- [x] Claude `claude-opus-5-5`: $4/$20 per 1M, 1M context.
- [x] OpenRouter, Groq, Together, Bedrock requests are priced from their own
      catalog rows or reported cost, never first-party prices.
- [x] A request with cached prompt tokens costs less than the same request
      uncached, by the catalog's cache-read rate.
- [x] A `gpt-5.5` request above 272K prompt tokens uses the $10/$45 tier.
- [x] OpenRouter's `usage.cost` is the request cost.
- [x] A five-round tool run adds five requests to spend and tokens.
- [ ] ChatGPT plan usage does not fabricate `$0.00`; provider-reported charges
      are retained and unknown costs hide spend (SIWC migration, 2026-10-05).

## UI (S4)

- [x] Spend shows `~$x.xx` after any estimated request and `$x.xx` when all
      priced requests were exact.
- [x] `/models` and setup rows show `ctx` and `$in/$out` when known.

## Live verification (user, pending)

- [ ] OpenRouter: Likha's spend equals the dashboard for one run.
- [ ] Claude: one run's estimated spend matches the Console usage page.
- [ ] OpenAI or DeepSeek: a run with cache hits matches the dashboard.
- [ ] `opencode-go` (live-verified provider): estimated spend is plausible
      against its billing page.
