# Context — Model metadata catalog

Code, data, and documentation this feature touches (verified 2026-10-04).

The SIWC migration (2026-10-05) supersedes inferred ChatGPT zero-dollar costs:
`pricing.go` and `agent_detail.go` preserve reported charges but leave plan
requests without monetary telemetry unknown. Primary reference:
https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions
(usage tracking and separately enabled credits).

## Code

- `internal/model/pricing.go`: `TurnCost(model, prompt, completion)`, the
  curated `pricingTable` (exact IDs, family prefixes, three placeholder
  rows), `subscriptionZeroIDs`. Replaced by catalog-backed pricing.
- `internal/model/windows.go`: `contextWindows` table, `ContextWindow`,
  `ResolveContextWindow(provider, model, override, metadata)`, and the
  `TokenUsage` type. The table is replaced; `ResolveContextWindow` keeps its
  precedence and gains the catalog as its fallback.
- `internal/model/naming.go`: `catalogSlug` strips `vendor/` (used by
  `TurnCost`; must not be used for catalog lookups).
- `internal/model/client.go`: `parseFinalUsageDetailed` (prompt/completion
  only), `beginTokenUsage`/`setTokenUsage`/`LastTokenUsage` (last request
  only), `Stream` sets `stream_options.include_usage`.
- `internal/model/usage_report.go`: `usageReport`, `LastRequestUsage`.
- `internal/model/codex.go`: `parseCodexUsageDetailed` (Responses usage).
- `internal/agent/turn_execution.go:145`: reads `LastTokenUsage()` after each
  `Stream` for the context event; the per-request usage event goes here.
- `internal/tui/tui.go:714-728`: run-end usage and spend accumulation (last
  request only); `resolveContextWindow` (:995).
- `internal/tui/status_line.go`: `spendSegment`.
- `internal/explore/node.go:265`: per-request task cost via `TurnCost`
  (model only, provider ignored); `explore.Usage` in `types.go`.
- `internal/tui/agent_detail.go:570`: task cost fallback via `TurnCost`.
- `internal/tui/models_all.go`: `/models` rows (`modelLabel`), per-provider
  `contextWindows` maps.
- `internal/tui/setup_view.go`: setup model rows (context label).
- `internal/app/run.go`: startup metadata discovery and the
  `--debug-models` observer.

## Data checked on 2026-10-04

- models.dev `api.json`: 226 providers, 8,393 models; covers 13 of Likha's
  providers through the mapping in `spec.md`; no `dialagram`. 862
  tool-capable rows across the mapped providers.
- stencil.so `catalog.stencil.so/models.json.zstd`: same providers and
  models, identical `cost` and `limit` for all 8,393; lacks `last_updated`,
  `release_date`, `knowledge`, `family`; adds `int`, `tps`, `kind`. No public
  terms found.
- Anthropic models overview: Opus 5.5 $4/$20, 1M context, 128K output;
  Sonnet 5.5 $2/$10, 1M; Fable 5.1 $10/$50, 1M; Haiku 4.5 $1/$5, 200K, 64K
  output; cache reads 10% of input (5% on Opus 5.5, 2.5% on Fable 5.1).
  Matches the catalog.
- Provider defaults missing from the catalog: `bedrock`
  `anthropic.claude-3-5-haiku-20241022-v1:0` (the catalog's Bedrock Haiku
  rows are 4.5 only); `dialagram`, `groq`, `xai`, `together`, `mistral`,
  `cerebras` have no default.

## Usage-field documentation

- OpenAI: `openai-python` `src/openai/types/completion_usage.py`
  (`prompt_tokens_details.cached_tokens`, `.cache_write_tokens`;
  `completion_tokens_details.reasoning_tokens`).
- OpenRouter: openrouter.ai/docs/use-cases/usage-accounting (usage always
  included, last SSE message; `cost`, `cost_details`, cache fields).
  `cost` is "the total amount charged to your account";
  `cost_details.upstream_inference_cost` is "the actual cost charged by the
  upstream AI provider", set only for BYOK requests (example: 0.95 and 19).
- xAI: docs.x.ai/openapi.json (checked 2026-10-04). `Usage` requires
  `cost_in_usd_ticks` ("accurate cost of this request", 1e10 ticks per
  dollar). Reasoning is counted on top of completion: chat example 32 + 9 +
  94 = 135 `total_tokens`, Responses example 32 + 9 + 110 = 151.
- DeepSeek pricing: api-docs.deepseek.com/quick_start/pricing (checked
  2026-10-04). Off-peak is half of peak; peak is 01:00-04:00 and
  06:00-10:00 UTC on weekdays. Flash peak $0.30/$1.20 (cache hit $0.006),
  V4 Pro peak $1.32/$3.96 ($0.044). models.dev lists the off-peak card.
- Anthropic context windows:
  platform.claude.com/docs/en/build-with-claude/context-windows (checked
  2026-10-04): "Other Claude models, including Claude Sonnet 4.5, have a
  200k-token context window"; 1M is 4.6 and later. models.dev lists 1M for
  Sonnet 4.5.
- DeepSeek: api-docs.deepseek.com/api/create-chat-completion
  (`prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`, `cached_tokens`).
- Anthropic OpenAI compatibility: platform.claude.com/docs/en/api/openai-sdk
  (prompt caching unsupported; detail objects always empty).
