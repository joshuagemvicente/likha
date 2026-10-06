# Research: probing reasoning effort on live providers

Fetched 2026-10-06 from official provider docs and Likha's own code.
Claims not confirmed from a primary source are marked **unverified**.
Provider wire behavior already collected for model effort is in
[model-effort research](../model-effort/research.md) and is not repeated
here, except where it shapes the probe.

## Why one request per level is not enough on its own

- **Silent mapping exists.** OpenRouter: when a model does not support a
  level, OpenRouter will "map your requested effort to the nearest supported
  level"; for Gemini 3, `xhigh` maps down to `high`
  (https://openrouter.ai/docs/use-cases/reasoning-tokens). xAI: on models
  without `xhigh`, "requests with 'xhigh' are treated as 'high'"
  (https://docs.x.ai/docs/guides/reasoning). A 200 therefore does not prove
  the level was applied as named. This is why the probe adds `same as`
  notes (spec point 17).
- **Silent ignoring exists.** Anthropic's OpenAI SDK compatibility page lists
  `reasoning_effort` as "Ignored" and says "Most unsupported fields are
  silently ignored rather than producing errors"; its response table lists
  `usage.completion_tokens_details` as "Always empty"
  (https://platform.claude.com/docs/en/api/openai-sdk, which now redirects
  to …/cli-sdks-libraries/libraries/openai-sdk; re-fetched 2026-10-06). A
  200 with similar output at every level is the only symptom, and on that
  endpoint completion tokens are the only signal. This is why the probe has
  a flat test and an invalid control level (spec points 15–18).
- **Default equals the model's default level.** Anthropic: "Setting `effort`
  to the model's default … produces exactly the same behavior as omitting
  the `effort` parameter entirely"
  (https://platform.claude.com/docs/en/build-with-claude/effort). Comparing
  Default to a level would wrongly flag the default level as ignored, so the
  flat test compares published levels only (spec point 17).
- **An easy task may not separate levels.** Anthropic: "At lower levels,
  Claude can skip thinking entirely for simpler problems", while "At higher
  effort levels, Claude thinks more readily and at greater length", and
  effort "is a behavioral signal, not a strict token budget"
  (https://platform.claude.com/docs/en/build-with-claude/effort). A flat
  result is therefore weak evidence on its own; the probe only calls a
  shape ignored when the invalid control is also accepted (spec point 16).
- **Reasoning varies run to run.** No provider documents reasoning token
  counts as deterministic. The thresholds (10% or 32 tokens flat, 5% `same
  as`) are Likha's choice, not a documented property; this is why the spec
  calls `ignored-suspected` a heuristic. **unverified:** how much a single
  model's reasoning count varies between identical requests.

## Where the signal comes from

- **OpenAI chat completions and OpenRouter** report
  `usage.completion_tokens_details.reasoning_tokens`
  (https://openrouter.ai/docs/use-cases/reasoning-tokens; OpenAI's API
  reference returned HTTP 403 to the fetch, so the chat-completions field
  name rests on OpenRouter's docs and on Likha's existing parser,
  `internal/model/usage_report.go`).
- **OpenAI Responses (ChatGPT)**: "The exact number of reasoning tokens used
  is visible in the usage object of the response object, under
  `output_tokens_details`: `reasoning_tokens`"
  (https://developers.openai.com/api/docs/guides/reasoning, re-fetched
  2026-10-06). That the reasoning tokens are billed as output is
  **unverified** in this pass (not in the fetched excerpt).
- **xAI**: "Usage metrics expose `reasoning_tokens`"; values are `low`,
  `medium`, `high` (default) and `xhigh`, and "Reasoning cannot be
  disabled" (https://docs.x.ai/docs/guides/reasoning, re-fetched
  2026-10-06). The exact nesting under `completion_tokens_details` is
  **unverified** from that page; Likha's parser reads it there and already
  treats xAI's count as additive to completion (`withAdditiveReasoning`).
- **Anthropic Messages** (native, FR-38): the effort page documents
  `output_config.effort` with values low, medium, high, xhigh, max ("Not
  every model that supports `max` supports `xhigh`"), and says effort
  affects "all tokens in the response", including "Tool calls and function
  arguments" and "Thinking (when active)". Every native request carries
  `max_tokens` (claude-native decision 9), which the effort page calls "a
  hard limit on total output (thinking plus response text)". How the native
  API answers an unknown effort value (400 or ignored) is **unverified**. **unverified:** whether the Messages usage object
  reports thinking tokens separately from `output_tokens`. Until confirmed,
  the probe falls back to completion (output) tokens on that path, which
  the spec allows (point 12).
- **Providers that report no reasoning count**: the probe uses completion
  tokens. Because completion includes the visible tool call, the flat test
  is weaker there; the report names the signal so the reader can judge.

## Why there is no output-token cap on the wire

- OpenAI: `max_output_tokens` limits all generated tokens, reasoning
  included; when reached the response is `incomplete`, and "this might occur
  before any visible output tokens are produced, meaning you could incur
  costs for input and reasoning tokens without receiving a visible
  response". OpenAI recommends "reserving at least 25,000 tokens for
  reasoning and outputs" when experimenting
  (https://developers.openai.com/api/docs/guides/reasoning). The re-fetch
  on 2026-10-06 confirmed the `incomplete` status and the 25,000-token
  advice; the "before any visible output tokens" sentence was not in the
  excerpt returned and is **unverified** in this pass.
- Anthropic: `max_tokens` "is a hard limit on total output (thinking plus
  response text)", and higher levels need "a large `max_tokens`"
  (https://platform.claude.com/docs/en/build-with-claude/effort).
- OpenRouter: reasoning tokens count toward `max_tokens` on most providers
  (https://openrouter.ai/docs/use-cases/reasoning-tokens).
- A cap small enough to bound spend would cut off reasoning at high levels
  and turn an honored level into a failed or empty row, which is the
  opposite of what the probe measures. Likha's real chat-completions body
  sends no cap (`Client.stream`, checked 2026-10-06), nor does the
  Responses body (`BuildCodexRequest`), and the probe must test the real
  body. The native Claude body must carry `max_tokens` (the Messages API
  requires it); the probe sends whatever value a real native turn sends.
  Spend is bounded by request count, the cumulative token budget and the
  per-request timeout instead (spec decision 8).
- **unverified:** whether every provider stops generating (and billing)
  when the client cancels a streamed request at the 180 s timeout.

## Levels and request counts

- The largest published level set is seven values: none, minimal, low,
  medium, high, xhigh, max (OpenAI reasoning guide: "Supported values are
  model-dependent and can include `none`, `minimal`, `low`, `medium`,
  `high`, `xhigh`, and `max`"; OpenRouter lists the same seven). Hence the
  cap of 1 + 2 × (7 + 1) = 17 requests.
- Model-specific restrictions matter for the probe's verdicts: "GPT-6 Astra
  does not support `none`"; "GPT-6.1 Sol does not support `none` or
  `minimal` and defaults to `medium`"; "GPT-5.5 defaults to `medium`"
  (https://developers.openai.com/api/docs/guides/reasoning). A catalog list
  that includes an unsupported level shows up as a `rejected` row, which is
  evidence for a catalog override.
- GPT-6 and tools on chat completions. The reasoning guide (re-fetched
  2026-10-06) says: "Use the Responses API for function calling. Chat
  Completions does not support function calling with GPT-6 Astra or GPT-6.1
  Sol", and "GPT-6 Astra does not support `none` reasoning effort. Setting
  `reasoning.effort` to `none` returns HTTP 400." Model-effort research
  records that GPT-6 Sol/Luna support function calling on chat completions
  only at `none`. A tool-bearing Default request can therefore fail with
  400 on these models because of the model's own default level, which is
  why a Default 400/422 does not stop the run (spec decision 4). Whether
  Sol/Luna then accept `none` with tools is **unverified by Likha**; the
  probe task always carries a tool so this is exercised.

## Prior art

- Codex CLI and Claude Code take levels from the server or a bundled list
  and do not probe providers (model-effort research). No coding agent
  surveyed ships a provider effort probe; **unverified** beyond the agents
  listed there.

## Consequences for the spec

1. Count `rejected` only on HTTP 400/422 with effort present while Default
   succeeded; everything else that fails is inconclusive. A Default
   400/422 does not stop the run (GPT-6 with tools).
2. Treat a 200 as "not rejected", not as "honored". Honored needs a
   measurable difference across levels or a validated control.
3. Never compare Default with the model's default level.
4. Report the signal source, since not every provider reports reasoning
   tokens.
5. Keep the request fields identical to a real turn; bound spend outside
   the body.
6. Never call a shape ignored on flatness alone; require the invalid
   control to be accepted too.
7. OpenRouter documents no behavior for an invalid `reasoning.effort`
   value (**unverified** whether it is a 400 or mapped), so its control
   result is a finding, not an expectation.
