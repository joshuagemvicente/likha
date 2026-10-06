# Feature: Native Claude client (Anthropic Messages API)

**Status:** implemented (local) 2026-10-06 — automated suite green; live
admission probe outstanding (the provider stays pending-probe); FR-36
automatic retries await model-recovery, which this wire classifies for but
does not run. Decisions recorded 2026-10-06 (user delegated defaults).
Requirement: v1-spec FR-38 ([v1-spec.md](../v1-spec.md)). Uses the replay data
model owned by [reasoning-replay](../reasoning-replay/spec.md) (FR-39).
Effort on this wire is enabled only by the effort probe (FR-40).
Supersedes the "one table row, no client changes" decision of
[claude-provider](../claude-provider/spec.md) for the `claude` row.

## Context

The `claude` provider row reaches Anthropic through its OpenAI SDK
compatibility layer at `https://api.anthropic.com/v1/chat/completions`.
That route works for text and tools, but it loses what makes Claude useful
in a coding agent:

- **No visible reasoning.** Current Claude models think in `thinking`
  blocks. The compatibility layer does not return them, so the muted
  reasoning view stays empty for Claude while it fills for DeepSeek or
  OpenRouter.
- **No reasoning across tool calls.** Anthropic requires the thinking blocks
  of a tool-use turn to come back unchanged with the tool results. Chat
  completions has no field for them.
- **No effort.** The compatibility layer ignores effort, which is why the
  row carries the note "Anthropic's OpenAI-compatible endpoint ignores
  effort" ([model-effort](../model-effort/spec.md) decision 5).
- **No prompt caching.** Every agent round re-bills the whole conversation
  at the full input price. Cache reads cost 5–10% of that.
- **A model list behind the wrong credential.** `GET /v1/models` requires
  `x-api-key`; the open question in claude-provider is whether a Bearer key
  is accepted there.

Anthropic's native Messages API (`POST /v1/messages`, server-sent events)
carries all of these. This feature replaces the compatibility route for the
`claude` row with a native client inside `internal/model`. The agent loop
does not change: it still calls `Stream` and `StreamUsage` with Likha's
messages and tool definitions, and gets back one assistant message.

Claude models differ in what they accept. Sending a configuration a model
does not support is a 400:

| Model (in Likha's `claude` list) | No `thinking` field | `adaptive` | `enabled` + budget |
| --- | --- | --- | --- |
| Opus 5.5, Fable 5.1, Fable 5, Sonnet 5.5 | adaptive (always on) | adaptive | 400 |
| Opus 4.7 (also 4.8) | thinking off | adaptive | 400 |
| Haiku 4.5 (also Opus 4.5, Sonnet 4.5) | thinking off | 400 | extended thinking |

Display defaults differ too: on Opus 4.7 and every later model (Opus 4.8,
5, 5.5, Sonnet 5, 5.5, Fable 5, 5.1), thinking text is `omitted` (empty)
unless the request asks for `summarized`; Opus 4.6, Sonnet 4.6 and earlier
models default to `summarized`. Sources are in [research.md](research.md).

## Role

Make Claude a first-class provider without changing the agent: stream
Claude's reasoning to the existing muted renderer, keep its reasoning valid
across tool calls, bill cached input at the cached price, and report
accurate usage. Never send a thinking mode, sampling parameter or tool choice
that the selected model rejects. Never claim the provider is accepted before
the user's live probe.

## Scope

In scope: every request Likha makes for the `claude` provider (main turns,
explore children, profile tasks, compaction, session naming), the connection
check, model discovery in setup, `/providers` and `/models`, usage and cost
reporting, error reporting, and carrying effort once FR-40's probe passes.

Out of scope:

- Other routes to Claude: `bedrock` and OpenRouter keep their
  chat-completions wire. A custom `--endpoint` that is not exactly
  `https://api.anthropic.com/v1` keeps chat completions.
- Claude subscription sign-in (OAuth). The row stays API-key only.
- Thinking budgets (`budget_tokens`) and turning thinking off. Models that
  support only budgeted thinking run without thinking.
- Beta features beyond the one needed for preserved thinking (decision 7):
  server-side compaction, context editing, per-message effort,
  `display: "updates"`, refusal fallbacks, fast mode, task budgets,
  mid-conversation tool changes.
- Server tools (web search, code execution) and Anthropic-defined tools.
- Images and documents. Likha sends none today.
- Rate-limit headers in the status bar (`anthropic-ratelimit-*`).
- API keys that act on more than one workspace. Anthropic requires an
  `anthropic-workspace-id` header for those; Likha sends none, so such a key
  gets Anthropic's own error, shown as any other rejected request.
- The agent's own history shape. Agent-side edits that invalidate replayed
  thinking are absorbed on the wire (decision 7), not fixed in the agent.
  Known consequence: the tool-round checkpoint note (round 32 and every 32
  rounds after) is sent at the end of each later request but never kept in
  history, so it moves every round. On models that check preserved thinking
  (Fable 5.1, Opus 5.5, Sonnet 5.5), each request after a checkpoint
  therefore loses the previous rounds' reasoning for the rest of that run.
  The request still succeeds. Keeping the note in history is an agent change
  left to a follow-up.

## Decisions (2026-10-06, user delegated defaults)

1. **The `claude` row selects the native wire; nothing else changes.** The
   wire follows the endpoint: exactly `https://api.anthropic.com/v1` (the
   `claude` row's base URL) uses the native API, and anything else keeps
   chat completions. Reason: every place Likha builds a client (startup,
   setup, `/providers`, `/models`, task forks) already passes the row's base
   URL, so no caller changes, and a custom endpoint never changes wire
   behind the user's back.
2. **Authentication is `x-api-key` with `anthropic-version: 2023-06-01`.**
   No Bearer header is sent. Reason: the native API documents only this
   pair, and it settles claude-provider's open `/v1/models` question.
3. **System and leading developer messages become the top-level system
   prompt; later ones become user-turn text.** Reason: a system prompt that
   stays the same through a conversation keeps both the prompt cache and
   replayed thinking valid, and a `system` role inside `messages` is not
   accepted by every Claude model in Likha's list.
4. **Thinking is adaptive with summarized display wherever the model
   supports adaptive thinking; otherwise no thinking field is sent.**
   Support comes from the live model list's capabilities, then a bundled
   exact-ID table, and an unknown model gets no thinking field. Reason: this
   is the only configuration that every adaptive model accepts and that
   streams readable reasoning; no field never causes a 400.
5. **Replay keeps the assistant turn's full content array, and sends it
   only to the model that produced it.** Thinking, redacted thinking, text
   and tool calls are stored in their original order with the issuing
   endpoint and model (reasoning-replay's data model) and sent back
   verbatim only to that endpoint and model, and only while the stored text
   and tool calls still match the message. A response whose thinking block
   arrived without its signature (cut off at the output limit) keeps no
   blocks. Reason: Anthropic rejects modified, reordered or partially
   dropped thinking in the latest turn, and interleaved thinking sits
   between tool calls. Anthropic recommends passing blocks to any model and
   letting the API drop what that model cannot read; Likha keeps
   reasoning-replay's same-model rule instead, which gives up earlier
   reasoning after an upgrade switch (for example Opus 4.7 → Opus 5.5) but
   never sends one model's blocks to another. Model switches happen between
   runs, where earlier thinking is recommended, not required.
6. **A request with no tool definitions sends tool history as text and no
   replay state.** This covers compaction and session naming. Reason: the
   tool list is part of the prefix that replayed thinking is checked
   against, so every replayed block would be dropped anyway; plain text
   removes any dependence on whether `tool_use` blocks need a `tools` array
   (unverified). This is the Claude form of reasoning-replay decision 9.
7. **Preserved-thinking mismatches drop the block instead of failing.**
   Every request that sends adaptive thinking also sends
   `block_binding.prefix_mismatch_behavior: "drop_block"` with the
   `thinking-binding-controls-2026-08-01` beta header. Reason: Likha
   rebuilds the system prompt and tool list between runs (plan mode, skills,
   MCP changes, a fresh AGENTS.md snapshot), and on Fable 5.1, Opus 5.5 and
   Sonnet 5.5 an account created on or after 2026-08-31 rejects replayed
   thinking after such a change with a 400. Losing that reasoning is better
   than failing the run. Trade-off: on older accounts, setting the field
   opts the request into the check, so a mismatched block is dropped where
   it would otherwise still reach the model. Likha cannot tell an account's
   age, and one behavior for every account is easier to test.
8. **Effort is sent as `output_config.effort`, and only after FR-40's probe
   passes.** Until then the `claude` row carries no effort. The
   "OpenAI-compatible endpoint ignores effort" note is removed when the
   native client ships, because it is no longer true. Reason: effort is a
   separate, probe-gated feature (model-effort), and the note would
   otherwise give a false reason.
9. **`max_tokens` is the model's published output limit.** It comes from the
   live model list, else the bundled catalog, else 64,000. Reason: the API
   requires `max_tokens`, output rate limits count only generated tokens, and
   a cap too low truncates long tool inputs and reasoning. Streaming avoids
   request timeouts. Claude 4.5 and later models accept a request whose
   input plus `max_tokens` exceeds the context window; earlier models
   (none in Likha's list) reject it, which near a full window shows as a
   rejected request.
10. **Prompt caching uses Anthropic's automatic top-level `cache_control`
    (5-minute lifetime) on requests that carry tools.** Requests without
    tools (compaction, session naming) send none. Reason: one GA field,
    no breakpoint bookkeeping, and the cache follows the conversation tail.
    Tool-less requests do not share the main prefix, so a cache write
    would cost 1.25× for nothing.
11. **Cache reads and writes are part of the prompt count.** Anthropic
    reports `input_tokens` after the last cache breakpoint only; Likha adds
    `cache_read_input_tokens` and `cache_creation_input_tokens` so the
    prompt count, the context tracker and spend all see the whole input.
12. **Refusals end the request with a visible error naming the category
    and Anthropic's explanation.** Partial output from a refused response is
    discarded from history, as Anthropic directs. No fallback model is
    requested. Reason: fallbacks are a beta that switches models behind the
    user's back, and Likha does not switch models without the user
    (model-recovery scope).
13. **The provider stays pending-probe.** Nothing in this feature describes
    `claude` as accepted. The live admission probe is run by the user and
    recorded in [context.md](context.md#live-probe).

## Functional changes

### Choosing the wire

1. `--provider claude` (and choosing Anthropic Claude in setup,
   `/providers` or `/models`) sends model requests to
   `POST https://api.anthropic.com/v1/messages` with `stream: true`. Every
   other provider is unchanged.
2. The request carries `x-api-key: <key>`, `anthropic-version: 2023-06-01`,
   `content-type: application/json`, `accept: text/event-stream` and Likha's
   user agent. The key source order is unchanged (`--api-key` →
   `LIKHA_API_KEY` → `LIKHA_CLAUDE_API_KEY` → stored key).

### Conversation

3. The agent's conversation reaches Claude unchanged in meaning:
   - the system prompt and the repository instructions that open the
     conversation, and a compaction summary at its start, form the system
     prompt, in order;
   - a later developer note (such as the tool-round checkpoint) is sent as
     text in the user turn where it occurs;
   - tool calls are sent as the assistant's tool calls and tool output as
     their results, every result in the user turn right after its call,
     before any text in that turn;
   - consecutive messages from the same side are sent as one turn;
   - empty or whitespace-only text is left out, and a message left with
     nothing to send is dropped from the request (never from history).
4. Likha never sends `temperature`, `top_p`, `top_k`, `tool_choice` or an
   assistant prefill. A conversation that would end with an assistant turn
   fails before sending, with
   `Claude request would end with an assistant turn; send a prompt to continue.`
5. Tool definitions keep their names, descriptions and schemas. A tool name
   Claude does not accept (outside `a–z A–Z 0–9 _ -`, or longer than 128
   characters) fails the request before sending, naming the tool.

### Reasoning

6. On a model that supports adaptive thinking, Claude's summarized
   reasoning streams into the muted reasoning view as it arrives, as it does
   for other reasoning providers. Several thinking blocks in one response
   are shown in order, separated by a blank line.
7. On a model that supports only budgeted thinking (Haiku 4.5), or a model
   Likha cannot classify, the request carries no thinking field and no
   reasoning is shown. The request succeeds.
8. Redacted thinking is never shown. It is kept for replay like other
   thinking.
9. Display reasoning is never sent back. What is sent back is the provider's
   own content blocks, under the replay rules below (FR-15 as amended by
   FR-39).

### Replay

10. A completed Claude response records the endpoint and model that
    produced it, as every replay provider does (reasoning-replay rule 2).
    When it contains thinking, it also keeps its full content array, in
    order. Both are saved with the session and restored on resume. A
    response whose thinking was cut off before its signature arrived keeps
    no content array.
11. A later request to the same endpoint and model sends that array
    verbatim for the assistant turn, so thinking stays attached to the tool
    calls it led to. Any other model, any other endpoint, or an assistant
    turn whose text or tool calls no longer match the array gets plain text
    and tool calls without thinking.
12. A request without tool definitions sends earlier tool calls and results
    as plain text lines and sends no thinking (decision 6).
13. If the API finds that a replayed block no longer matches the conversation
    before it, it drops that block and the later ones for that request and
    the request continues (decision 7). The user sees no error.
14. A response stopped by Steer now, cancelled, refused or failed keeps no
    replay state (reasoning-replay rule 14).

### Effort

15. Until FR-40's probe enables effort for `claude`, no `output_config` is
    sent and the effort step is skipped with
    `Effort stays at the provider default: not yet verified for Anthropic Claude.`
16. After FR-40's probe enables it, a non-Default level is sent as
    `output_config: {"effort": "<level>"}`. Default sends no
    `output_config`. Levels come from the live model list's capabilities,
    else the bundled catalog (model-effort decision 8).

### Output limit and stop reasons

17. Every request sets `max_tokens` to the model's published output limit
    (decision 9).
18. A response that stops at `max_tokens` or at the context window is
    returned like a finished one, unless a tool call in it is incomplete;
    then the request fails with the existing
    `incomplete structured tool call` error and no tool runs.
19. A refusal ends the request with
    `Claude declined the request (category: <category>).`, or without the
    parenthesis when Anthropic names no category, followed by Anthropic's
    explanation on the next line when it gives one. Text already streamed
    stays visible, marked interrupted, and is never kept in history or sent
    back to the model. The request is not retried.

### Caching and usage

20. Requests with tools ask Anthropic to cache the conversation automatically
    (decision 10). Requests without tools do not.
21. The usage of each request reports:
    - prompt tokens = uncached input + cache reads + cache writes;
    - cache reads and cache writes separately;
    - completion tokens = output tokens, which include thinking;
    - reasoning tokens when Anthropic reports a thinking-token count.
22. Spend is computed from the bundled Claude prices, with cache reads and
    writes at their own rates, as for other providers. Anthropic reports no
    dollar cost.

### Models and connection check

23. The connection check, setup's model list, `/providers` and `/models`
    read `GET https://api.anthropic.com/v1/models` with the same headers,
    following pages until the list is complete.
24. Each model shows its display name and its context window from
    `max_input_tokens`. Its output limit, thinking support and effort levels
    come from the same response.
25. A model list that fails or returns nothing is an error, as today. Likha
    never substitutes a bundled list.

### Errors

26. HTTP 401 and 403 report rejected credentials, as on other providers, on
    model requests and on the model list.
27. HTTP 400 on a request that carried effort shows the effort hint
    (`Sent with effort <level>; /effort default sends none.`). HTTP 400 on a
    request that carried replayed thinking shows reasoning-replay's
    `/compact` hint (its rule 13); both show when both apply. Other 400s,
    402, 404 and 413 are shown with Anthropic's error type and message (or
    the raw body when it is not Anthropic's JSON error), and are never
    retried.
28. The statuses FR-36 treats as transient (408, 429, 5xx including 529)
    are transient here too, and so is an error event inside the stream
    whose type is `overloaded_error`, `api_error`, `rate_limit_error` or
    `timeout_error`. A 429 whose error code is `enforced_spend_limit_reached`
    is a monthly spend cap: it is shown as a quota error and never retried.
    Any other in-stream error type is shown and not retried.
29. Every error shown for a Claude request includes Anthropic's request ID
    when one was returned, so the user can quote it to support.
30. A stream that ends before `message_stop` is a dropped stream (FR-36),
    never a finished response.

### Provider status

31. README, `docs/providers.md` and v1-spec keep `claude` pending-probe until
    the live probe in [context.md](context.md#live-probe) is recorded.

## Acceptance criteria

- A test server playing Anthropic receives `POST /v1/messages` with
  `x-api-key`, `anthropic-version: 2023-06-01`, `stream: true` and a
  `max_tokens`, and no `Authorization` header.
- Given harness system + AGENTS.md developer message + user + assistant
  with two tool calls + two tool results + a steer, the body has one
  top-level system with two text blocks, and messages user / assistant
  (two `tool_use`) / user (two `tool_result`, then the steer text).
- On `claude-opus-5-5` the body carries
  `thinking: {"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`
  and the beta header; on `claude-haiku-4-5` it carries neither.
- A streamed thinking block appears in the reasoning view piece by piece
  while it streams, and the finished transcript entry holds the whole
  summary.
- The second request of a tool loop replays the first response's content
  array byte for byte, thinking and signature included. After switching to
  another model, the same history is sent without thinking. A response cut
  off at the output limit inside a thinking block is replayed without
  thinking.
- A compaction request (no tools) sends tool history as text and no
  thinking blocks.
- No body ever contains `temperature`, `top_p`, `top_k` or `tool_choice`.
  At Default effort no body contains `output_config`.
- With usage `input_tokens 50, cache_read_input_tokens 9000,
  cache_creation_input_tokens 400, output_tokens 300` the request reports
  prompt 9450, cache read 9000, cache write 400, completion 300.
- Requests with tools carry top-level `cache_control: {"type":"ephemeral"}`;
  requests without tools do not.
- `/models` lists models from `GET /v1/models` across two pages with
  display names and context windows.
- 401 and 403 show the rejected-credentials message; a 400 on a request
  with effort shows the effort hint; a 400 on a request with replayed
  thinking shows the `/compact` hint; a 529 and a mid-stream
  `overloaded_error` are retried under FR-36; a 429 with
  `enforced_spend_limit_reached` is not; an EOF before `message_stop` is a
  visible error, not an answer. Every one of these errors shows the
  request ID the test server returned.
- A refusal with category `cyber` and an explanation shows
  `Claude declined the request (category: cyber).` and the explanation;
  the partial text before it is not in the next request's body.
- A session holding Claude replay state is saved, resumed, and its next
  request body equals the body built before the save.
- `go test ./...` passes. No test claims the provider works; the live probe
  is recorded separately by the user.
