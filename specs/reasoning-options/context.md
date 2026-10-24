# Context: Reasoning options and tool support in the bundled catalog

Code, data and docs this feature touches, with the facts that shaped the
spec. All decisions live in [spec.md](spec.md). Paths verified on
`feat/attention-automation` at `18663da` on 2026-10-07.

## Code

- `internal/model/catalog/catalog.go`
  - `Model` (≈line 62) has `Reasoning bool` and `Efforts []string`; there
    is no tool or budget field. `Schema = 1`.
  - `Validate` → `validateModel` → `ValidateEfforts` (≈line 266) checks
    effort level names. The new rules 8–13 go here.
  - `Parse` uses `json.Unmarshal`, so unknown fields are ignored. Adding
    fields does not break an older reader (D1).
- `internal/model/catalog/gen/main.go`
  - `upstreamModel` already decodes `tool_call` and `reasoning_options`.
    `ToolCall` is used only to filter rows.
  - `upstreamReasoningOption` (≈line 186) decodes only `type` and `values`.
    Its comment says budget and toggle are ignored as outside model-effort.
  - `upstreamEfforts` (≈line 411) takes the first `effort` entry and drops
    `default` (Groq Qwen rows publish it).
  - The override merge (≈line 490) sets `efforts` wholesale and records it
    in `Overridden`.
- `internal/model/catalog/overrides.json` — lines 32–34 hold the only
  `efforts` overrides (Gemini 2.5 Pro, Flash, Flash-Lite, cited to
  `ai.google.dev/gemini-api/docs/openai`, verified 2026-10-06).
- Readers of `Efforts` outside the catalog package (effort step, `/effort`,
  effort probe) read `Efforts` only. Keeping it derived means they need no
  change.

## Data checked on 2026-10-07

Sources: `https://models.dev/api.json` and
`https://catalog.stencil.so/models.json`, both fetched 2026-10-07. They were
compared against the committed `models.json` (generated 2026-10-06, 823 rows,
650 with `reasoning: true`).

- **Stencil versus models.dev**: same provider and model sets for all 13
  mapped upstreams (`amazon-bedrock`, `cerebras`, `anthropic`, `deepseek`,
  `google`, `groq`, `mistral`, `openai`, `opencode-go`, `opencode`,
  `openrouter`, `togetherai`, `xai`). Zero differences in `limit`,
  `reasoning`, `reasoning_options`, `tool_call` or `cost`. Stencil lacks
  `last_updated`, `release_date`, `knowledge`, `family`, `attachment`,
  `temperature`, `structured_output`, `open_weights` and others. It adds
  `int`, `tps` and `kind`. Whole file: 226 providers, 8,418 models.
- **Option types**: only `effort`, `budget_tokens` and `toggle` exist
  anywhere in models.dev (all 8,418 models). No entry carries keys beyond
  `type`, `values`, `min` and `max`. No model repeats a type. No budget
  bound is fractional.
- **Likha's upstream-sourced rows** (807 matched): `effort` 411, `toggle`
  237, `budget_tokens` 68. Combinations: effort only 314, toggle only 117,
  `[]` 75, toggle+effort 67, toggle+budget 30, toggle+effort+budget 23,
  budget only 8, effort+budget 7.
- **Presence**: reasoning rows with a non-empty list 575, with `[]` 75.
  Non-reasoning rows with no list 166. No non-reasoning row has options.
- **Budget bounds** in Likha's rows: min 1,024 only (47), no bounds (12),
  max 81,920 only (4), max 262,144 only (3), 128–32,768 (2). Across all of
  models.dev, 209 entries have both bounds, 156 min only, 47 max only.
- **Effort value sets** most common across models.dev: `low/medium/high`
  (717), `low/medium/high/xhigh/max` (487), `low/high/max` (300),
  `minimal/low/medium/high` (249). `none` and `minimal` appear as levels.
  `default` appears on Groq `qwen/qwen3.6-27b` and `qwen/qwen3.8-27b`.
- **Examples**: `anthropic/claude-haiku-4-5` → `[budget_tokens min 1024]`;
  `anthropic/claude-sonnet-5` → `[toggle, effort low…max]`;
  `google/gemini-2.5-flash` → `[toggle, budget_tokens 0–24576]`;
  `google/gemini-2.5-pro` → `[budget_tokens 128–32768]`.

## Related specs

- [model-metadata](../model-metadata/spec.md) — catalog contract. Decision
  2 (upstream is models.dev; Stencil checked 2026-10-04). § Fields lists
  the row format this spec extends. Non-goals: no runtime catalog fetch.
- [model-effort](../model-effort/spec.md) — reads `efforts`. Puts token
  budget control out of scope, which this spec keeps.
- [effort-probe](../effort-probe/spec.md) — probes levels from `efforts`.
  Unaffected.
