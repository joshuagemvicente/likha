# Research: chunked message content in chat-completions streams

Fetched 2026-10-06 from provider docs, the Mistral SDK source, and public
issue trackers. Claims not confirmed from a primary source are marked
**unverified**. Secondary sources (other projects' issues and PRs) are
labelled as such.

## Mistral (primary)

### Reasoning docs

https://docs.mistral.ai/capabilities/reasoning and
https://docs.mistral.ai/studio/conversations/reasoning:

- Reasoning is controlled by `reasoning_effort`.
  - `"high"`: "The response includes a full thinking chunk before the
    final answer, at the cost of increased token usage."
  - `"none"`: "The model thinks minimally and the thinking chunk is
    omitted from the response." `message.content` is then a plain string.
  - `zai-glm-5-3` takes `"low"`, `"high"` and `"max"` instead of `"none"`.
- Listed reasoning models: `mistral-small-latest`, `mistral-medium-3-5`,
  `mistral-large-4-0`, `zai-glm-5-3`. The page describes reasoning as set
  by `reasoning_effort` and gives no default; reading it as "none reason
  by default" is a paraphrase (re-checked 2026-10-06). The native reasoning models (`magistral-small-latest`,
  `magistral-medium-latest`) are noted as deprecated.
- At `high`, `message.content` is a list:

  ```json
  [
    {"type": "thinking", "thinking": [{"type": "text", "text": "…trace…"}]},
    {"type": "text", "text": "…answer…"}
  ]
  ```

- Streaming, quoted: "delta.content changes shape during the response:
  (1) Thinking phase: delta is a list containing a ThinkChunk;
  (2) Transition: a single list containing both a closing ThinkChunk and the
  first TextChunk; (3) Answer phase: delta is a plain string."
- The official streaming sample handles each delta as follows:
  - Skips empty deltas.
  - Prints a string delta as answer text.
  - In a list delta, prints each `TextChunk` inside a `ThinkChunk.thinking`
    as thinking, and each top-level `TextChunk` as answer.
- Multi-turn, quoted: "always replay the full assistant message (including
  `ThinkChunk`) back into the message history. Dropping the reasoning
  trace across turns degrades model performance." And: "Do not strip
  `ThinkChunk` from assistant messages before replaying them. … Stripping
  them increases token efficiency but significantly degrades output
  quality." 
  - The docs state degraded quality as the consequence. They do not say
    the request is rejected. Whether Mistral rejects a stripped history
    when tools are present is **unverified**.

### API reference

https://docs.mistral.ai/api/endpoint/chat: `reasoning_effort` enum is
`none | minimal | low | medium | high | xhigh`, plus `prompt_mode`
(`"reasoning"`). The page fetch did not include the stream response schema,
so the delta schema below comes from the SDK.

### Official Python SDK (generated from Mistral's OpenAPI)

`mistralai/client-python` at commit
`e8dfa1c8a2d0975cebfc0136b2d9060ff555494d` (2026-09-28, release v3.0.0),
`src/mistralai/client/models/`:

- `deltamessage.py`: `content: OptionalNullable[Union[str, List[ContentChunk]]]`.
  A delta's content may be absent, null, a string, or a chunk list.
- `contentchunk.py`: `ContentChunk` is an open union discriminated on
  `type`:
  - `image_url`, `document_url`, `text`, `reference`, `file`, `thinking`,
    `input_audio`;
  - any other value parses as `UnknownContentChunk`, which keeps the raw
    payload. The SDK itself does not fail on unknown chunk types.
- `textchunk.py`: `{type: "text", text: str}`.
- `thinkchunk.py`:
  - `thinking: List[TextChunk | ReferenceChunk | ToolReferenceChunk]`
    (required).
  - `type: "thinking"`.
  - `signature: Optional[Nullable[str]]`, documented as "Signature to
    replay some reasoning blocks across turns."
  - `closed: Optional[bool]`, documented as "Whether the thinking chunk is
    closed or not. Currently only used for prefixing."
- `referencechunk.py`: `{type: "reference", reference_ids: [...]}`.
- `toolreferencechunk.py`:
  - `{type: "tool_reference", tool, title}`;
  - optional `url`, `favicon`, `description`.
- `assistantmessage.py`: request `content` may be a string or a list of
  `ContentChunk`, so a `thinking` chunk can be sent back.

Consequences:

- A `thinking` chunk can never nest another `thinking` chunk directly. Its
  items are text, reference or tool_reference.
- Whether `signature` is ever non-null on chat completions is
  **unverified**.
- Whether the transition delta's "closing" thinking chunk sets
  `closed: true` is **unverified**. The SDK says `closed` is for prefixing.

### Secondary reports (not Mistral-authored)

- earendil-works/pi issue #10080
  (https://github.com/earendil-works/pi/issues/10080), Mistral
  Conversations API, `zai-glm-5-3`:
  - Error: "400 Invalid request payload: Expected at most one leading
    ThinkChunk, got 2".
  - Cause: thinking was replayed as several thinking chunks. The proposed
    fix merges all thinking into one leading chunk, joining stored blocks
    with newlines. Likha joins streamed fragments with no separator
    (spec decision 6), because its fragments are pieces of one stream, not
    separately stored blocks.
  - Whether chat completions enforces the same rule is **unverified**.
  - The model reportedly fragments thinking across several chunks with
    empty text deltas between them.
- langchain-ai/langchainjs PR #11731
  (https://github.com/langchain-ai/langchainjs/pull/11731) says reasoning
  models such as `zai-glm-5-3` "always return thinking content before
  answers".
  - If true, chunked content can arrive with no `reasoning_effort` sent.
    **Unverified** against Mistral docs, which say none reason by default.
  - The PR's error ("Mistral only supports types 'text' or 'image_url' …")
    comes from that client library, not from Mistral.
- mistralai/client-python issue #628
  (https://github.com/mistralai/client-python/issues/628, 2026-09-20,
  open, model reported as `zai-glm-latest`) reports intermittent GLM 5.3
  stream faults, possibly not limited to that model:
  - empty replies;
  - thinking-only replies;
  - tool calls with an empty or tag-polluted function name.

  Likha's existing tool-call validation already fails incomplete calls.
  Thinking-only replies are out of scope (spec Scope).

## Other Likha providers

Every other API-key provider streams string `content`. Where the docs
cover reasoning, it arrives in a separate string field.

| Provider | Reasoning field in deltas | `delta.content` | Source |
| --- | --- | --- | --- |
| OpenRouter | `reasoning` (string) and `reasoning_details` (array, separate field) | string | https://openrouter.ai/docs/use-cases/reasoning-tokens |
| Groq | `reasoning` (`reasoning_format: parsed`); `<think>` tags in content for `raw` | string | https://console.groq.com/docs/reasoning |
| Cerebras | `reasoning` (`parsed`); prepended to content for `raw` | string | https://inference-docs.cerebras.ai/capabilities/reasoning |
| Together | `reasoning` or `reasoning_content` by model; `<think>` tags in content for DeepSeek-R1 | string | https://docs.together.ai/docs/reasoning-models-guide |
| DeepSeek | `reasoning_content` | string | https://api-docs.deepseek.com/guides/thinking_mode (via [model-effort research](../model-effort/research.md)) |
| xAI | chat completions has no ciphertext field; field name for plain reasoning not shown on the fetched page | string, **unverified** | https://docs.x.ai/docs/guides/reasoning |
| Gemini compat | thoughts only with `extra_body.google.thinking_config.include_thoughts`, which Likha does not send; shape not documented on the page | string, **unverified** | https://ai.google.dev/gemini-api/docs/openai |
| Bedrock compat | not documented on pages fetched | **unverified** | — |
| opencode Zen/Go, Dialagram | relays; behavior undocumented | **unverified** | — |
| Claude compat | quoted: "the OpenAI SDK doesn't return Claude's thinking" | string; request-side assistant arrays allow only `text` (and ignored `refusal`) | https://platform.claude.com/docs/en/api/openai-sdk |

Notes:

- OpenRouter's `reasoning_details` is an array, but it is its own delta
  field, not `content`. Parsing or replaying it belongs to
  [reasoning-replay](../reasoning-replay/spec.md), not this spec.
- Whether OpenRouter passes Mistral's chunk list through or flattens it to
  a string is **unverified**. Its docs say `content` is a string.
- Whether any relay sends the same thinking both as a `thinking` chunk in
  `content` and in a `reasoning`/`reasoning_content` field of the same
  delta is **unverified**. If one does, Likha shows it twice (spec point
  4); no source documents it.
- `<think>` tags inside string content (Groq `raw`, Together DeepSeek-R1)
  are plain text to Likha today and stay so. Splitting them out is not in
  scope.

## Likha catalog (bundled `models.json`, generated 2026-10-06)

Mistral rows with `reasoning: true`:

| Model | Efforts |
| --- | --- |
| `mistral-small-latest`, `mistral-small-2603` | none, high |
| `mistral-medium-latest`, `mistral-medium-2604` | none, high |
| `zai-glm-5-2` | none, high, max |
| `zai-glm-5-3` | low, high, max |
| `magistral-medium-latest` | none listed; deprecated native reasoning model |

## Review re-check (2026-10-06)

Re-fetched the Mistral reasoning page, `thinkchunk.py`, `deltamessage.py`,
`contentchunk.py`, the chat API reference, pi #10080, langchainjs #11731,
client-python #628, the OpenRouter reasoning page and the Claude
OpenAI-compatibility page. Field names, chunk types, the three streaming
phases, the effort values and the secondary reports match the claims
above, with the quote corrections applied. Also confirmed:

- `deltamessage.py` lists further optional delta fields (`tool_call_id`,
  `index`, `metadata`). Likha does not read them; unknown JSON fields are
  already ignored by `encoding/json`.
- The SDK's `UnknownContentChunk` keeps unknown chunks rather than failing,
  which supports skipping them (spec decision 3). The SDK gives no
  guidance on a reply made only of unknown chunks; Likha's visible error
  for that case (spec point 8) is a Likha choice.
- The bundled catalog's Mistral reasoning rows and levels match the table
  above (`models.json`, generated 2026-10-06).

