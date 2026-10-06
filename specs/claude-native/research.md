# Research: Anthropic Messages API for a native Claude client

Fetched 2026-10-06 from platform.claude.com (primary docs) and cross-checked
against the bundled `claude-api` reference skill (cached 2026-09-25, a
secondary source; claims taken only from it are labelled). Re-checked by an
adversarial review the same day; corrections are folded in below. Claims
not confirmed from a primary page are marked **unverified**. No live request
was sent; the live probe is the user's (see [context.md](context.md#live-probe)).

## Endpoint, headers, request shape

Sources: https://platform.claude.com/docs/en/api/messages/create ,
https://platform.claude.com/docs/en/api/errors

- `POST https://api.anthropic.com/v1/messages`, headers `x-api-key`,
  `anthropic-version: 2023-06-01`, `content-type: application/json`.
  Beta features add `anthropic-beta: <name>`.
- `max_tokens` is required ("The maximum number of tokens to generate before
  stopping").
- `system` is a string or an array of text blocks. The Messages reference
  says "there is no `"system"` role for input messages". Other docs describe
  mid-conversation `role: "system"` messages on some models only (Opus 5,
  5.5, 4.8, Fable, Mythos, Sonnet 5.5; not Sonnet 5, Opus 4.7, Haiku 4.5).
  The two sources conflict; Likha uses neither form (decision 3).
- "Consecutive `user` or `assistant` turns in your request will be combined
  into a single turn." Likha merges them itself so the body it sends is the
  body that is checked (decision 3, T2).
- Tool definition: `name` matches `^[a-zA-Z0-9_-]{1,128}$`, `description`,
  `input_schema` (JSON Schema object). Optional `cache_control`,
  `eager_input_streaming`, `strict`.
- `tool_use` block: `id`, `name`, `input` (object). `tool_result` block:
  `tool_use_id`, optional `content` (string or blocks), optional `is_error`.
  `thinking` block: `thinking`, `signature`. `redacted_thinking`: `data`.
- Top-level `cache_control`: "automatically applies a cache_control marker
  to the last cacheable block in the request."
- `output_config.effort`: `low | medium | high | xhigh | max`.

## Tool results placement

Source: https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls

- "Tool result blocks must immediately follow their corresponding tool use
  blocks in the message history."
- "In the user message containing tool results, the tool_result blocks must
  come FIRST in the content array. Any text must come AFTER all tool
  results." Text after results is correct "when the assistant turn calls
  only client tools"; Likha declares only client tools.
- "You cannot include any messages between the assistant's tool use message
  and the user's tool result message." A missing result produces
  "tool_use ids were found without tool_result blocks immediately after".
- An empty tool result (no `content`) is a documented example.
- **Unverified:** whether a request whose history contains `tool_use` /
  `tool_result` blocks must also declare `tools`. Not stated on the pages
  read. Decision 6 renders tool history as text on tool-less requests, so
  Likha does not depend on the answer.

## Streaming events

Source: https://platform.claude.com/docs/en/build-with-claude/streaming

- Flow: `message_start` (Message with empty content), then per block
  `content_block_start` / `content_block_delta`… / `content_block_stop`,
  then `message_delta` (stop reason, usage), then `message_stop`.
- "The token counts shown in the `usage` field of the `message_delta`
  event are *cumulative*."
- "Event streams may also include any number of `ping` events."
- Mid-stream errors: `event: error` with
  `{"type": "error", "error": {"type": "overloaded_error", "message": "Overloaded"}}`
  — "would normally correspond to an HTTP 529".
- "new event types may be added, and your code should handle unknown event
  types gracefully."
- Deltas: `text_delta`, `input_json_delta` (`partial_json`, accumulate and
  parse at `content_block_stop`; the first fragment can be `""`),
  `thinking_delta`, `signature_delta` ("sent just before the
  `content_block_stop` event"), `citations_delta`.
- With `display: "omitted"` a thinking block streams one empty
  `thinking_delta` and a `signature_delta`.
- The documented tool-use stream shows the first `input_json_delta` with
  `"partial_json": ""`.
- Under the `thinking-binding-controls-2026-08-01` header, `message_start`'s
  message also carries an `input_transformations` array. A `fallback`
  content block appears only with server-side fallback, which Likha never
  requests.

## Thinking modes per model

Source: https://platform.claude.com/docs/en/build-with-claude/thinking
(table "Configuring thinking")

| Model | No `thinking` | `adaptive` | `enabled` + budget | `disabled` |
| --- | --- | --- | --- | --- |
| Opus 5.5 | adaptive | adaptive | 400 | 400 |
| Sonnet 5.5 | adaptive | adaptive | 400 | 400 (`between_tools` instead) |
| Fable 5.1, Fable 5, Mythos 5.1, Mythos 5 | adaptive | adaptive | 400 | 400 |
| Opus 5 | adaptive | adaptive | 400 | off at `high` or below |
| Sonnet 5 | adaptive | adaptive | 400 | off |
| Opus 4.8, Opus 4.7 | off | adaptive | 400 | off |
| Opus 4.6, Sonnet 4.6 | off | adaptive | deprecated | off |
| Opus 4.5, Sonnet 4.5, Haiku 4.5 | off | 400 | extended thinking | off |

- Display: "`display` works in both modes: set it alongside
  `type: "adaptive"` or `type: "enabled"`." `"summarized"` is the default
  on Opus 4.6, Sonnet 4.6 and earlier; `"omitted"` is the default on Fable
  5.1/5, Mythos 5.1/5, Opus 5.5/5/4.8/4.7, Sonnet 5.5/5. "Switching
  `display` values between turns in a conversation is supported."
  Sending `display: "summarized"` explicitly on Opus 4.6 / Sonnet 4.6 is
  implied by "works in both modes" but not shown in an example
  (**unverified** for those two models; neither is in Likha's default
  list).
- Progress updates: on Fable 5.1, Mythos 5.1, Opus 5.5, Sonnet 5.5 and
  Fable 5 the model may write a note between tool calls as its own
  `thinking` block; with `display: "summarized"` it carries summary text
  "not distinguishable from a reasoning block".
- Interleaved thinking is automatic with adaptive thinking; on Fable, Opus
  5.5/5/4.8/4.7 and Sonnet 5.5 "reasoning between tool calls always appears
  in thinking blocks". "Claude Haiku 4.5 does not support interleaved
  thinking."
- Sampling: on Fable, Mythos, Opus 5.5/5/4.8/4.7 and Sonnet 5.5/5,
  "non-default `temperature`, `top_p`, or `top_k` values return a 400
  error on every request". Likha sends none.
- Prefill and forced tool use: Opus 5.5, Sonnet 5.5, Fable 5.1, Mythos 5.1
  "reject forced tool use on every request with a 400 error"; Claude 4.6
  and later reject assistant prefill
  (https://platform.claude.com/docs/en/api/errors). Likha sends neither.
- Output limits ("Output limits" table): 128K for Fable, Mythos, Opus
  5.5/5/4.8/4.7/4.6, Sonnet 5.5/5/4.6; 64K for Opus 4.5, Sonnet 4.5, Haiku
  4.5.
- Context: "On Claude 4.5 models and newer, if input tokens plus
  `max_tokens` exceeds the context window size, the API accepts the
  request", stopping with `model_context_window_exceeded` if generation
  reaches the limit.

## Replaying thinking

Sources: https://platform.claude.com/docs/en/build-with-claude/thinking ,
https://platform.claude.com/docs/en/build-with-claude/preserved-thinking ,
https://platform.claude.com/docs/en/api/errors

- "Required: within a tool-use turn, pass thinking blocks back.
  Recommended: across turns, pass everything back. Allowed: outside tool
  use, omit prior turns' thinking."
- "Within the latest assistant message, the sequence of consecutive
  `thinking` blocks must match what the model generated in the original
  request: you can't rearrange, edit, or partially drop them. This
  includes `redacted_thinking` blocks." Modified blocks → 400
  ("`thinking` or `redacted_thinking` blocks in the latest assistant message
  cannot be modified").
- Model switches: "the API ignores or drops the blocks the target model
  can't read". Opus 5.5 reads Opus 5 and earlier Opus/Sonnet/Haiku blocks;
  Fable 5.1 reads Opus 5.5 blocks on the Claude API; Sonnet 5.5 blocks are
  read only by Opus 5.5 (Claude API and Google Cloud). Sonnet 5.5 blocks
  from another account are dropped silently. Anthropic's guidance is "Keep
  passing thinking blocks back unchanged when you switch models" and let
  the API drop what it cannot read. Likha's same-model rule (decision 5,
  reasoning-replay decision 2) deviates: it is safe, but it loses earlier
  reasoning on an upgrade switch that the API would have kept.
- Preservation by model: Opus 4.5 and later Opus, Sonnet 4.6 and later
  Sonnet, and Fable/Mythos keep all prior turns' thinking in context (billed
  as input); earlier models and Haiku 4.5 keep the last turn only.
- Preserved thinking (prefix check): a block "stays valid only while the
  top-level `system` prompt, the `tools`, and the messages before it are
  unchanged". The comparison is byte-level, except that whitespace and JSON
  formatting of `tool_use` input do not count. Only Fable 5.1, Opus 5.5 and
  Sonnet 5.5 run the check (Mythos 5.1, Opus 5, Sonnet 5 and earlier do
  not). Enforced by default for accounts created on or after 2026-08-31
  00:00 UTC (400), otherwise only when
  `thinking.block_binding.prefix_mismatch_behavior` is set: any value,
  including `"error"`, opts an older account's request into enforcement.
  With the header and the field unset, an older account's failing block
  still reaches the model and is listed as `thinking_mismatch_allowed`. The beta
  header `thinking-binding-controls-2026-08-01` allows the field; values
  `"error"` and `"drop_block"`. With `"drop_block"` the API "drops each
  failing block and every thinking block after it; request succeeds.
  Dropped blocks aren't billed." The drop applies to that request only, so
  the field must be sent on every request. Models that don't run the check
  accept the object (claude-api reference, secondary). On Sonnet 5.5,
  `block_binding` works only with `adaptive`, never with `between_tools`. Sending `block_binding` without the header is a 400 ending
  "block_binding: Extra inputs are not permitted".
- Removing thinking "from the start of the history, from the end, or all of
  them" keeps the rest valid; "removing one from the middle invalidates
  the thinking blocks after it". Likha's same-model rule removes another
  model's blocks consistently on every request, so the blocks it keeps are
  exactly the sequence that model saw.
- Things that do not invalidate thinking: changing `effort`, `max_tokens`,
  `thinking.display`, adding or moving `cache_control`.
- Per-turn reminders: without the `clear_at` beta, "append the reminder as
  a text block after the `tool_result` blocks and leave earlier copies in
  place" (claude-api reference, secondary). Likha's tool-round checkpoint
  does not (context.md).
- A thinking block that never received its `signature_delta` (cut off at
  `max_tokens`) cannot be replayed; Likha keeps no blocks for such a
  response (decision 5). The API's exact error for an empty signature is
  **unverified**.
- Signatures are "compatible across platforms" and opaque.
- **Unverified:** that the `thinking-binding-controls-2026-08-01` header is
  accepted for every organization on the Claude API (an unknown or
  unenabled beta value is a 400 "Unexpected value(s) … for the
  `anthropic-beta` header"). The value is listed in the API reference's
  `anthropic-beta` enum and the preserved-thinking page documents it for
  the Claude API. Live probe steps 4 and 11 cover it.
- **Unverified:** what happens when blocks issued under one organization's
  key are replayed with another organization's key on the same endpoint
  (for example after re-running setup with a new key). Documented only for
  Sonnet 5.5 (dropped silently). Not covered by the live probe.

## Effort

Source: https://platform.claude.com/docs/en/build-with-claude/effort

- GA, "no beta header required", at `output_config.effort`.
- Supported models: Fable 5.1/5, Mythos 5.1/5, Opus 5.5/5/4.8/4.7/4.6,
  Opus 4.5 (`claude-opus-4-5-20251101`), Sonnet 5.5/5/4.6. Not Haiku 4.5.
- `xhigh`: Fable, Mythos 5.x, Opus 5.5/5/4.8/4.7, Sonnet 5.5/5. `max`:
  the same plus Opus 4.6, Sonnet 4.6, Mythos Preview.
- Default `high` everywhere except Opus 5.5 (`medium`). "Setting `effort`
  to the model's default … produces exactly the same behavior as omitting
  the `effort` parameter."
- Changing top-level effort between requests restarts the prompt cache but
  does not invalidate thinking.
- The effort page's supported-model list omits Haiku 4.5 and Sonnet 4.5;
  the claude-api reference (secondary) says effort "errors on Sonnet 4.5 /
  Haiku 4.5" and that Opus 4.5 takes only `low`/`medium`/`high`. The
  bundled catalog already publishes no levels for Haiku 4.5. The exact
  error for an unsupported level is **unverified** (FR-40's probe records
  it).

## Model list

Source: https://platform.claude.com/docs/en/api/models/list

- `GET /v1/models`, `limit` default 20, max 1000, cursors `after_id` /
  `before_id`; response `data`, `has_more`, `first_id`, `last_id`.
- Model: `id`, `display_name`, `created_at`, `type: "model"`, `line`,
  `max_input_tokens` (context window), `max_tokens` (max `max_tokens`),
  `capabilities` ("or null"; "Keys are always present for all known
  capabilities") with `thinking.types.adaptive.supported`,
  `thinking.types.enabled.supported`, `effort.supported`, and
  `effort.low|medium|high|max.supported`, `effort.xhigh` ("or null").
- No default-effort field.
- The documented example response shows `max_input_tokens: 0` and
  `line: "haiku"` for Opus 5, which is placeholder data; Likha treats a
  zero window as unknown, as `positiveWindow` already does.

## Prompt caching

Source: https://platform.claude.com/docs/en/build-with-claude/prompt-caching

- Automatic caching: one top-level `cache_control`; "applies the cache
  breakpoint to the last cacheable block and moves it forward as
  conversations grow." GA; uses one of 4 breakpoint slots.
- Prefix order `tools` → `system` → `messages`; a change invalidates that
  level and later ones. Tool definition changes invalidate everything;
  thinking and effort changes invalidate system and messages.
- Lookback 20 blocks; consecutive `tool_use` or `tool_result` blocks count
  as one position.
- Minimum cacheable prompt: 512 tokens (Fable 5.1/5, Mythos, Opus 5.5/5,
  Sonnet 5.5), 1,024 (Opus 4.8, Sonnet 5/4.6/4.5), 2,048 (Opus 4.7), 4,096
  (Opus 4.6/4.5, Haiku 4.5). Shorter prompts are processed uncached without
  error.
- Prices: 5-minute writes 1.25× input, 1-hour writes 2×, reads 0.1× (0.05×
  on Opus 5.5, 0.025× on Fable 5.1/Mythos 5.1). The bundled catalog's
  `claude-opus-5-5` row (cache_read 0.2, cache_write 5 on input 4) matches.
- The lookback window is 20 blocks per breakpoint; a Likha tool round adds
  far fewer, so the automatic breakpoint at the tail keeps hitting.
- Usage: `total_input_tokens = cache_read_input_tokens +
  cache_creation_input_tokens + input_tokens`; "`input_tokens` represents
  only the tokens that come after the last cache breakpoint". Optional
  `cache_creation` object splits 5m/1h writes.

## Usage and thinking tokens

Source: https://platform.claude.com/docs/en/build-with-claude/thinking-steering-and-cost

- "read `usage.output_tokens_details.thinking_tokens` … always less than
  or equal to `output_tokens` … When streaming, this breakdown appears only
  on the final `message_delta` event." `output_tokens` is the billed total.
  The page names no beta header, but the API reference's beta enum still
  lists `thinking-token-count-2026-05-13`; whether the field appears without
  it is **unverified**. Likha reports reasoning tokens only when present.
- Display does not change billing: summarized or omitted, the full thinking
  is billed as output.

## Rate limits and `max_tokens`

Source: https://platform.claude.com/docs/en/api/rate-limits

- "The `max_tokens` parameter does not factor into OTPM rate limit
  calculations, so there is no rate limit downside to setting a higher
  `max_tokens` value." (decision 9)
- Cache reads do not count toward ITPM on current models.
- 429 carries `retry-after`, except the monthly spend cap, whose body has
  `error.details.error_code: "enforced_spend_limit_reached"` and no
  `retry-after`; "Retrying … fails until access resumes." A user-set spend
  limit is a 400 `invalid_request_error`.

## Errors

Source: https://platform.claude.com/docs/en/api/errors

- 400 `invalid_request_error`, 401 `authentication_error`, 402
  `billing_error`, 403 `permission_error`, 404 `not_found_error`, 409
  `conflict_error`, 413 `request_too_large`, 429 `rate_limit_error`, 500
  `api_error`, 504 `timeout_error`, 529 `overloaded_error`.
- Shape: `{"type":"error","error":{"type","message"},"request_id"}`; every
  response has a `request-id` header. "`type` values will grow over time."
- Errors can arrive after a 200 inside the SSE stream.
- No 422 is documented. Likha's `effortRejectedStatus` also accepts 422,
  which is harmless here.
- A 400 `invalid_request_error` is also returned when a spend limit the user
  set is reached ("You have reached your specified API usage limits").
  Likha shows it as a rejected request (with the effort hint if effort was
  sent, as on the chat path).
- A 413 on the direct API comes from Cloudflare "before the request reaches
  the API servers", so its body may not be Anthropic's JSON error.
- Keys with access to several workspaces must send `anthropic-workspace-id`
  (https://platform.claude.com/docs/en/api/openai-sdk and the models list
  reference). Out of scope (spec.md).

## Stop reasons

Source: https://platform.claude.com/docs/en/build-with-claude/handling-stop-reasons

- `end_turn`, `max_tokens` ("If an incomplete tool_use block is present …
  retry the request with a higher `max_tokens`"), `stop_sequence`,
  `tool_use`, `pause_turn` (server-tool loops only), `refusal` (with
  `stop_details`, "`null` for all stop reasons other than `refusal`"),
  `model_context_window_exceeded` ("Treat the response as truncated").

## Refusals

Source: https://platform.claude.com/docs/en/build-with-claude/refusals-and-fallback

- Fable 5.1, Fable 5, Opus 5.5, Opus 5 and Sonnet 5.5 run safety
  classifiers. A refusal is HTTP 200 with `stop_reason: "refusal"` and
  `stop_details: {"type":"refusal","category":…,"explanation":…}`.
  `category` and `explanation` "are both `null` when the refusal does not map
  to a named category". Categories include `cyber`, `bio`, `frontier_llm`,
  `reasoning_extraction`, `general_harms`. The explanation "is not stable, so
  display it rather than parse it".
- "A refusal can arrive before any output, or mid-stream after partial
  output. In either case, treat any partial output as incomplete and
  discard it." (decision 12, spec point 19)
- Some categories are billed even before output; usage is reported. Likha
  returns an error for a refusal, so that usage is not counted (FR-36
  already says failed attempts may be billed).

## Prior Likha evidence

- claude-provider (2026-10-04): unauthenticated `GET /v1/models` returned
  "x-api-key header is required"; an invalid Bearer key returned
  "invalid x-api-key". The native client sends `x-api-key`, which removes
  the question.
