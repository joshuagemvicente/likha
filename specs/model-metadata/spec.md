# Feature: Model metadata catalog (pricing, context windows, token accounting)

**Status:** in progress (2026-10-04)

Likha bundles its own model catalog, `internal/model/catalog/models.json`. It
is generated from the open models.dev catalog, limited to Likha's predefined
providers and their tool-capable models, corrected by a hand-checked
`overrides.json`, committed to the repository, and embedded in the binary.
Every price and context window is looked up by **(provider, model)** and
carries its source. When nothing reliable exists, Likha shows unknown instead
of guessing.

Refines v1-spec FR-15/status-line spend and the tui-layout phase 1b spend
segment. Supersedes, by user approval on 2026-10-04, these earlier rules:

| Earlier rule | Where | Replaced by |
| --- | --- | --- |
| Spend uses "a curated local price table keyed by model slug" | tui-layout spec §1b.6, `internal/model/pricing.go` | The bundled catalog keyed by (provider, model), § Lookup |
| Context windows come from a documented catalog keyed by model ID (`windows.go`), provider-independent | tui-redesign spec, `internal/model/windows.go` | The bundled catalog keyed by (provider, model); user override and provider `/models` still win |
| "Likha's existing Claude metadata needs no changes" (`claude-` 200K and pricing prefixes) | claude-provider spec | Claude entries come from the catalog; current models are 1M context per Anthropic's docs |
| Family-prefix and placeholder prices (`claude-opus` $15/$75, `minimax`/`kimi`/`glm` "conservative placeholder") | `pricing.go`, `pricing_test.go` | Removed. A model absent from the catalog has unknown pricing |

Unknown pricing hides the spend segment (never `$—`, never guessed), with
`ctx —` for unknown windows and user context-window overrides in `config.json`.
The 2026-10-05 Sign in with ChatGPT migration supersedes the earlier exact
subscription-zero rule: account-side credit usage can be separately opted into,
so the provider name alone cannot establish a request's monetary cost. ChatGPT
cost is unknown unless reported by the provider; plan usage is managed in
ChatGPT Settings, separately from OpenAI API-key billing.

## Context

The 2026-10-04 review (recorded in chat and in `context.md`) found:

1. **Wrong prices.** The Claude provider's default `claude-opus-5-5` was priced
   by the `claude-opus` prefix at $15/$75 per 1M tokens; Anthropic documents
   $4/$20. `claude-haiku-4-5` was $0.80/$4; Anthropic documents $1/$5.
2. **Made-up prices.** `minimax`, `kimi`, and `glm` carried an explicitly
   undocumented "conservative placeholder".
3. **Provider ignored.** `TurnCost(model, …)` stripped `vendor/` prefixes and
   priced OpenRouter, Groq, Together, and Bedrock calls with first-party
   prices.
4. **Cached tokens at full price.** Usage parsing kept only prompt/completion
   totals, so cache reads (10% of input on most Claude models, 50–90% off on
   OpenAI, DeepSeek) were billed as fresh input.
5. **Only the last request of a run counted.** The TUI read
   `Client.LastTokenUsage()` once at run end; an agent run makes one request
   per tool round, so every earlier round was missing from spend and token
   totals.
6. **No price tiers** (for example, `gpt-5.5` is $10/$45 above 272K prompt
   tokens instead of $5/$30).
7. **The spec's "provider-reported cost first" rule was never implemented**,
   although OpenRouter always returns `usage.cost`.

## Resolved decisions (user, 2026-10-04)

1. **Own catalog file.** Likha keeps its own `models.json`, built from an
   upstream catalog and limited to Likha's providers and their models.
2. **Upstream is models.dev** (`https://models.dev/api.json`, MIT,
   `anomalyco/models.dev`). The stencil.so catalog OMP uses was checked on
   2026-10-04: same 226 providers, same 8,393 models, identical cost and
   limit values, but without `last_updated`, so models.dev is used directly.
   The upstream URL is one generator flag.
3. **No catalog downloads at runtime.** Numbers change only when the file is
   regenerated, reviewed, and released. The only runtime metadata source
   besides the file is the provider's own `/models`, which Likha already
   calls with the user's key.
4. **Unknown stays unknown.** No borrowing a price from the same model at
   another provider, from a similar model, or from a family prefix.

## The catalog file

`internal/model/catalog/models.json`, schema 1, embedded with `go:embed`:

```json
{
  "schema": 1,
  "generated": "2026-10-04",
  "upstream": { "name": "models.dev", "url": "https://models.dev/api.json" },
  "providers": {
    "claude": {
      "upstream": "anthropic",
      "models": {
        "claude-opus-5-5": {
          "name": "Claude Opus 5.5",
          "context": 1000000,
          "output": 128000,
          "cost": { "input": 4, "output": 20, "cache_read": 0.2, "cache_write": 5 },
          "reasoning": true,
          "modalities": ["text", "image", "pdf"],
          "updated": "2026-09-22",
          "source": "models.dev"
        }
      }
    }
  }
}
```

- **Keys** are Likha canonical provider names (`internal/model/provider.go`)
  and the exact model IDs the provider's API accepts.
- **Provider mapping** (Likha → upstream): `openai`→`openai`,
  `openrouter`→`openrouter`, `bedrock`→`amazon-bedrock`,
  `opencode-zen`→`opencode`, `opencode-go`→`opencode-go`, `groq`→`groq`,
  `xai`→`xai`, `together`→`togetherai`, `mistral`→`mistral`,
  `cerebras`→`cerebras`, `claude`→`anthropic`, `deepseek`→`deepseek`,
  `gemini`→`google`. `chatgpt` and `dialagram` have no upstream: `chatgpt`
  is curated in `overrides.json` (subscription billing), `dialagram` has no
  entries until someone verifies them.
- **Rows kept:** upstream models with `tool_call: true` (Likha requires
  tools), including `deprecated` ones, which keep `"status": "deprecated"`.
- **Fields:** `name`; `context` (tokens); `input` (max prompt tokens, only
  when upstream gives a smaller input limit); `output` (max output tokens);
  `cost` in **US dollars per 1M tokens** with `input`, `output`, and optional
  `cache_read`, `cache_write`, `reasoning`, and `tiers`; `reasoning`
  (capability flag); `modalities` (input); `status`; `updated` (upstream
  `last_updated`); `source` (`models.dev` or `override`); for overrides `ref`
  (doc URL or repo path), `verified` (date), optional `note`, and on a
  models.dev row an override changed, `overridden`: the fields it replaced.
  `ref` and `verified` vouch only for those fields; the rest of the row is
  still upstream data. A row an override added has no `overridden` list.
- **Tiers:** `[{ "above": 272000, "input": 10, "output": 45, "cache_read": 1 }]`.
  A request whose prompt exceeds `above` tokens is priced entirely at that
  tier (the highest matching tier wins). Generated from upstream `tiers`
  (`type: "context"`). Upstream uses `context_over_200k` as an alias of the
  first context tier whatever its threshold (200K, 256K, 272K, 512K in the
  2026-10-04 file), so it never sets a threshold: when it disagrees with the
  first tier the run fails, and when `tiers` is absent the row is left
  unpriced (the threshold is unknown) until an override states the card.
- **Optional rates:** an absent `cache_read`/`cache_write` means "no separate
  rate known": those tokens are priced at the input rate. An absent
  `reasoning` rate means reasoning tokens are priced at the output rate.
- **Provider billing:** `"billing": "subscription"` on a provider (only
  `chatgpt`) identifies plan usage, not an exact zero-dollar charge. It prevents
  applying API-key token prices to subscription requests.
- **Output is deterministic:** sorted keys, two-space indent, trailing
  newline, so regenerations diff cleanly.

## The generator

`go generate ./internal/model/catalog` runs
`go run ./internal/model/catalog/gen`, which:

1. Reads the upstream catalog from `-upstream` (default
   `https://models.dev/api.json`) or `-in <file>` (offline and tests). With
   `-in`, the file records `upstream.url` as `file:<path>` unless
   `-upstream` is also given to name the URL the file was downloaded from.
2. Maps providers, keeps tool-capable rows, and converts fields as above.
3. Applies `overrides.json` field by field. An override must carry `ref` and
   `verified`; it can change fields, add a model upstream lacks, set a
   provider's `billing`, or drop a model (`"drop": true`). The file is
   decoded strictly: an unknown or misspelled key at any level fails the
   run. A `cost` replaces the whole card and must state `input` and `output`
   (and so must each of its tiers). An override of a models.dev row must
   change at least one field, and the row records which (`overridden`).
4. Writes `models.json` and prints a change report against the previous
   file: providers and model counts, prices changed (old → new, the whole
   card: every rate, every tier threshold and rate), limits, status and
   source changes, models added and removed, and each provider's
   `DefaultModel` that has no entry.
5. Exits non-zero on: an override without `ref`/`verified`, an unknown key
   in `overrides.json`, an override cost missing `input` or `output`, a
   negative or non-finite price or limit, a price tier whose type is not
   `context` or whose size is not a positive count, a `context_over_200k`
   that disagrees with the first context tier, an unknown provider in the
   mapping, or an upstream fetch/parse failure. Partial files are never
   written.

Regenerating is a reviewed change: the diff and report are read before the
commit, so an upstream mistake cannot slip in unnoticed.

## Lookup

`model.Metadata(provider, modelID)` returns the catalog entry for the exact
pair (IDs compared after trimming whitespace; no prefix matching, no
cross-provider fallback, no `vendor/` stripping). Callers combine it:

**Context window**, first positive value wins:
1. user override (`config.json` `context_windows[provider][model]`);
2. the provider's live `/models` metadata (existing behavior);
3. the catalog entry's `input` limit if present, else `context`;
4. unknown → `ctx —`.

**Request cost**, first available wins:
1. **provider-reported cost** in the response usage (`usage.cost`, US
   dollars, OpenRouter always sends it, plus
   `cost_details.upstream_inference_cost` when positive: on a BYOK request
   the upstream provider bills that on top of OpenRouter's fee; or xAI's
   `usage.cost_in_usd_ticks`, 1e10 ticks per dollar, when `cost` is absent)
   → exact;
2. **subscription billing** (`chatgpt`) without reported cost → unknown;
3. **catalog price** for (provider, model) applied to the token breakdown
   (§ Token accounting) → estimate;
4. unknown → that request's cost is unknown.

Session spend sums request costs. It is shown once at least one request is
priced; if any priced request was an estimate, the segment renders with a
leading `~` (`~$0.42`); all-exact renders `$0.42`. A request with unknown
cost leaves the segment as it was (not shown if nothing is priced yet), as
today. Explore/agent task usage uses the same rules with the task's own
provider and model, and every child request is also added to the session
spend and token totals: each `task` event adds the growth of the record's
usage sums since the newest version already counted (a restored record is
the baseline, never new usage).

## Token accounting

Usage is parsed per request from the final usage object of the chat-completions
stream (and the Codex terminal event):

| Field | Source | Meaning |
| --- | --- | --- |
| prompt | `prompt_tokens` / `input_tokens` | all prompt tokens, cached included |
| completion | `completion_tokens` / `output_tokens` | all output tokens, reasoning included |
| cache read | `prompt_tokens_details.cached_tokens`; DeepSeek `prompt_cache_hit_tokens`; Codex `input_tokens_details.cached_tokens` | subset of prompt |
| cache write | `prompt_tokens_details.cache_write_tokens` | subset of prompt |
| reasoning | `completion_tokens_details.reasoning_tokens`; Codex `output_tokens_details.reasoning_tokens` | subset of completion (after the xAI rule below) |
| total | `total_tokens` | decides whether reasoning is additive |
| cost | `cost` (number) + `cost_details.upstream_inference_cost` when positive; else xAI `cost_in_usd_ticks` / 1e10 | provider-charged USD |

Sources: OpenAI SDK `CompletionUsage` type (cached and cache-write tokens are
"present in the prompt"; reasoning tokens count toward completion), DeepSeek
API docs (`prompt_tokens` = hit + miss), OpenRouter usage-accounting docs
(usage and `cost` always included in the last SSE message). Anthropic's
OpenAI-compatible endpoint returns empty detail objects and does not support
prompt caching, so its prompt tokens are correctly priced at the input rate.
xAI counts reasoning on top of completion: its documented example is
`prompt_tokens` 32, `completion_tokens` 9, `reasoning_tokens` 94,
`total_tokens` 135 (docs.x.ai `openapi.json`). When reasoning exceeds the
completion count, or `total_tokens` equals prompt + completion + reasoning
rather than prompt + completion, reasoning is added to completion at parse
time, so every reasoning token is counted and billed.

Estimated cost of one request, with subsets clamped so they never exceed their
totals:

```
fresh  = prompt - cacheRead - cacheWrite
cost   = fresh*input + cacheRead*cacheReadRate + cacheWrite*cacheWriteRate
       + (completion - reasoning)*output + reasoning*reasoningRate     (per 1M)
```

Rates come from the matching tier when the prompt exceeds a tier's `above`.

**Per-request accumulation.** The agent loop emits one usage event per model
request (`TurnEvent{Kind: "usage"}`) carrying the request's breakdown; the
TUI adds every request to the session totals and spend, replacing the single
`LastTokenUsage()` read at run end. Explore children already record per
request; they gain the breakdown and reported cost.

**No repricing.** A request's cost is computed when it completes. Explore
task records keep storing their computed cost (existing `Usage.Cost`), so
reopening a session never reprices them. `/agents` never prices a task's
summed totals as one request (tiers apply per request); only a record
without a stored cost whose totals are exactly one request (saved before
per-request pricing) is estimated. A stored known $0 for a model ID the old
table priced at $0 by ID alone (the ChatGPT model IDs, after stripping one
`vendor/` prefix) is unknown. Legacy ChatGPT zero-cost task records are also
unknown because their stored format does not establish whether a zero was
provider-reported or inferred. Main-session spend stays in memory
as today (persistence is out of scope).

## User-visible behavior

- **Spend:** `$0.42` when every priced request was provider-reported;
  `~$0.42` when any was estimated from the catalog; hidden when
  nothing could be priced. Cached tokens no longer inflate spend, and every
  tool round counts.
- **`/models`:** each row shows context and price per 1M tokens when known,
  right-aligned after the model (e.g. `1M ctx · $4/$20`). Unknown parts are
  omitted. The provider column stays as today.
- **Setup model list:** the context column gains the same price label.
- **Agent detail (`/agents`):** cost uses the task's provider and the same
  `~` rule.
- **`--debug-models` log:** each logged model also records the catalog
  entry's source and date when one exists.

## Non-goals

- No runtime fetch of models.dev, stencil.so, or any catalog.
- No parsing of price fields from providers' `/models` responses in this
  feature (OpenRouter's per-response `usage.cost` already gives the exact
  number; per-provider list formats need probing first).
- No user price overrides in `config.json` (corrections go into
  `overrides.json` and a release).
- No time-of-day pricing, batch discounts, audio or image token pricing, or
  non-USD currencies. Where a provider has peak and off-peak cards
  (DeepSeek: off-peak is half), `overrides.json` keeps the peak card, so
  estimates are an upper bound rather than under-reporting.
- No persistence of main-session spend across resume.
- No change to which models a provider lists, the provider table, defaults,
  or `/models` fetching.

## Acceptance criteria

- [ ] `go generate ./internal/model/catalog` regenerates `models.json`
      deterministically from a fixed upstream file and prints the change
      report; an override without `ref`/`verified` fails the run.
- [ ] `models.json` has entries for every mapped provider; every provider
      `DefaultModel` has an entry or is listed as a known gap with a reason.
- [ ] `claude-opus-5-5` on `claude` resolves to $4/$20 per 1M and 1M context;
      the same ID on another provider does not borrow it.
- [ ] Cached prompt tokens are priced at the cache-read rate; reasoning
      tokens at the reasoning rate when one exists; tiers apply above their
      threshold.
- [ ] OpenRouter's `usage.cost` is used as the exact request cost.
- [ ] Every model request of a run is added to spend and token totals.
- [ ] Spend shows `~` when any priced request was an estimate.
- [ ] `/models` and setup rows show known context and price per 1M.
- [ ] No placeholder or prefix price remains.
