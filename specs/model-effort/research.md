# Research: reasoning effort across providers and coding agents

Fetched 2026-10-06 from official docs, catalogs and source repositories.
Claims not confirmed from a primary source are marked **unverified**. Two
claims were re-checked directly and are marked **re-checked**.

## Where per-model levels come from

### models.dev (Likha's catalog source)

- Each model has `reasoning: bool`, `interleaved`, and `reasoning_options`,
  an array of:
  - `{"type":"effort","values":[...]}`: named levels.
  - `{"type":"budget_tokens","min"?,"max"?}`: a thinking-token budget.
  - `{"type":"toggle"}`: thinking can be switched on and off.
- There is no default-level field.
- **Re-checked** in `https://models.dev/api.json`:
  - `openai/gpt-5`: effort minimal, low, medium, high
  - `openai/gpt-5.5`: effort none, low, medium, high, xhigh
  - `anthropic/claude-opus-5-5`: effort low, medium, high, xhigh, max
  - `anthropic/claude-sonnet-4-5`: budget_tokens min 1024 (no effort)
  - `google/gemini-2.5-flash`: toggle, budget_tokens 0–24576
  - `deepseek/deepseek-v4-pro`: toggle, effort low, high, max;
    interleaved field `reasoning_content`
  - `xai/grok-4.7`: effort low, medium, high, xhigh
- **Re-checked:** of the 651 rows in Likha's `models.json` with
  `reasoning: true`, 574 have `reasoning_options` upstream.
- Likha's generator (`internal/model/catalog/gen`) currently drops the field
  and keeps only `reasoning`.

### Live model lists

- **OpenRouter** `GET /api/v1/models` returns, per model,
  `supported_parameters` (includes `reasoning`, `reasoning_effort`) and a
  `reasoning` object with `mandatory`, `default_enabled`,
  `supported_efforts` (highest first) and `default_effort`. Example:
  `openai/gpt-5.1` → `supported_efforts: ["high","medium","low","none"]`,
  `default_effort: "none"`. Budget-only models list no efforts.
- **ChatGPT/Codex backend** (`…/models?client_version=`, used by Codex CLI)
  returns `supported_reasoning_levels: [{effort, description}]` and
  `default_reasoning_level` (`codex-rs/protocol/src/openai_models.rs`).
  Whether the public `https://api.openai.com/v1` model list that Likha's
  Sign in with ChatGPT uses returns these fields is **unverified**. Likha's
  decoder notes that SIWC documents only slug, display name and visibility.
- **No level data:** OpenAI `/v1/models`, opencode Zen `/zen/v1/models`.
  Bedrock-runtime has no `/models` route.

## Provider wire behavior (chat completions unless noted)

| Provider | Field | Levels | Unsupported value |
| --- | --- | --- | --- |
| OpenAI | `reasoning_effort` | per model: none, minimal, low, medium, high, xhigh, max | HTTP 400 |
| ChatGPT (Responses) | `reasoning: {effort}` | per model | probe needed |
| OpenRouter | `reasoning: {effort}` (documented) | none…max | mapped to nearest |
| Anthropic compat | `reasoning_effort` **ignored** | — | ignored |
| Gemini compat | `reasoning_effort` | minimal, low, medium, high; `none` only on 2.5 Flash family | **unverified** |
| xAI | `reasoning_effort` | low, medium, high, xhigh (per model) | downgraded, no error |
| DeepSeek | `reasoning_effort` | low, high, max (others mapped) | mapped |
| Groq | `reasoning_effort` | per model (gpt-oss low/medium/high) | HTTP 400 for some combinations |
| Mistral | `reasoning_effort` | per model (`none`/`high` on most) | **unverified** |
| Cerebras | `reasoning_effort` | per model | some models ignore it |
| Together | `reasoning_effort` (+ `reasoning.enabled`) | per model | **unverified** |
| Bedrock compat | `reasoning_effort` | per model card | **unverified** |
| opencode Zen/Go | chat completions only for open-weight models | — | **unverified** |

Details:

- **Anthropic compat (re-checked).** The field table on
  https://platform.claude.com/docs/en/api/openai-sdk lists
  `reasoning_effort` as "Ignored". The page also says "the OpenAI SDK
  doesn't return Claude's thinking". The only thinking control is
  `thinking: {type: "enabled", budget_tokens}`, and per Anthropic's
  troubleshooting page `enabled` returns 400 on Claude 4.7 and later. Real
  effort is `output_config.effort` on the native Messages API: low, medium,
  high on all effort models, `xhigh` on Opus 4.7+ and Sonnet 5+, `max` on
  4.6+. The default is `high`, except Opus 5.5 which defaults to `medium`.
- **Gemini compat** (https://ai.google.dev/gemini-api/docs/openai): Google
  maps `reasoning_effort` to thinking levels on Gemini 3 and to budgets on
  2.5 (low 1,024, medium 8,192, high 24,576). It cannot be combined with
  `extra_body.google.thinking_config`. Thinking cannot be turned off on
  2.5 Pro or Gemini 3.
- **OpenAI** (https://developers.openai.com/api/docs/guides/reasoning): the
  value set grew by generation. `minimal` exists only on gpt-5, `none`
  arrived in 5.1, `xhigh` in 5.2 and codex-max, and `max` in 5.6 and
  GPT-6. GPT-6 Astra and GPT-6.1 Sol reject `none`.
- **GPT-6 tools on chat completions (unverified by Likha).** OpenAI's
  latest-model guide states GPT-6 Sol/Luna support function calling in chat
  completions only at `reasoning_effort: "none"`. Likha sends tools on every
  turn, so this needs a live probe before the `openai` row is enabled.
- **DeepSeek** (https://api-docs.deepseek.com/guides/thinking_mode): thinking
  is on by default at `high`. With tools, `reasoning_content` must be passed
  back or the request fails with 400.
- **Mistral** (https://docs.mistral.ai/capabilities/reasoning): at `high`,
  `message.content` becomes a list of `thinking`/`text` chunks instead of a
  string.

## How other agents present it

- **opencode** (`packages/opencode/src/provider/transform.ts`):
  - Builds "variants" from models.dev `reasoning_options` first: one per
    effort value, `high`/`max` from a budget, none/high from a toggle.
  - Falls back to hard-coded per-provider lists when a model has no
    `reasoning_options`.
  - Users override in `provider.<id>.models.<id>.variants`.
  - Ctrl+T cycles variants; `opencode run --variant`.
- **Codex CLI:**
  - Levels come from the server model list (`supported_reasoning_levels`,
    `default_reasoning_level`) with a bundled fallback.
  - `/model` picks a model, then an effort.
  - Alt+, and Alt+. step through only the advertised levels.
  - A `/model` choice applies to the session; persisting writes
    `model_reasoning_effort` to `config.toml`.
- **Claude Code** (https://code.claude.com/docs/en/model-config):
  - `/effort low|medium|high|xhigh|max|auto`; in the `/model` picker, ←/→
    sets effort.
  - Also `--effort`, `CLAUDE_CODE_EFFORT_LEVEL`, and per-model settings.
  - `max` is never persisted.
  - `ultrathink` is a prompt keyword and does not change the API effort.
- **Crush:** per-model `reasoning_effort` validated against catalog levels.
  Fallback order is user choice, then catalog default, then first level.
  Has a "Select Reasoning Effort" dialog.
- **Aider:** `--reasoning-effort`, `--thinking-tokens`, and per-model
  `accepts_settings` with a warning on mismatch.

## Consequences for Likha

1. Offer each model's own list. A global ladder is wrong for most models.
2. Treat "send nothing" as its own state (Default). `none` is a real level
   that disables thinking, not the absence of a choice.
3. Gate on the provider as well as the model. The catalog lists levels for
   Claude models, but Likha's Claude route ignores them.
4. Probe each provider before enabling it. Behavior on a bad value ranges
   from 400 to silent mapping to silent ignoring.
