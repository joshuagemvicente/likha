# Research: provider-issued reasoning replay

Fetched 2026-10-06 from official provider documentation. Claims not
confirmed from a primary source are marked **unverified**. Claims taken from
an archived copy of a page that has since moved are marked **archived**.

## DeepSeek thinking mode

Source: https://api-docs.deepseek.com/guides/thinking_mode

- Thinking mode is **on by default** (default effort `high`). It is
  controlled by `{"thinking": {"type": "enabled"|"disabled"}}` and
  `reasoning_effort` (`low`/`high`/`max`; others mapped).
- Reasoning comes back in `reasoning_content`, "at the same level as
  `content`". Streaming uses `delta.reasoning_content`.
- With `tools` in the request: "the `reasoning_content` of all previous
  turns should be passed back to the API and will be concatenated into the
  context." In the Tool Calls section: "the reasoning_content must be fully
  passed back to the API in all subsequent requests — even for turns where
  the model did not perform a tool call. If your code does not correctly
  pass back reasoning_content, the API will return a 400 error."
- Without `tools`: "reasoning_content does not need to be passed back; even
  if passed to the API, it will be ignored".
- Sample code appends `response.choices[0].message`, which "contains all
  necessary fields for the `assistant` message, including `content`,
  `reasoning_content`, and `tool_calls`."
- Models (https://api-docs.deepseek.com/quick_start/pricing, fetched
  2026-10-06): `deepseek-flash` and `deepseek-v4-pro`, both with thinking
  on by default. A session can switch between them, which is why decision 2
  matches DeepSeek text on the endpoint only. Whether DeepSeek accepts one
  model's `reasoning_content` on the other is **unverified**; nothing in
  the docs ties the text to a model.
- Consequence for Likha: every main turn sends tools, so a DeepSeek tool
  loop fails on its second request today. The same failure is widely
  reported against other agents (for example
  https://github.com/anomalyco/opencode/issues/24722,
  https://github.com/langchain-ai/langchainjs/issues/10883); these are
  secondary sources.
- **Unverified:** what DeepSeek does with an earlier assistant message that
  has no `reasoning_content` because another model wrote it (after a model
  switch, or in a session saved before this feature). The docs do not say;
  a 400 is likely. Whether `reasoning_content: ""` is accepted for such
  messages is also unverified. The live probe (tasks T18) records it.
- **Unverified:** whether a partial (Steer now) `reasoning_content` is
  accepted. Nothing in the docs suggests that content is validated beyond
  being present.

## Gemini thought signatures (OpenAI-compatible endpoint)

Sources:
- https://docs.cloud.google.com/vertex-ai/generative-ai/docs/thought-signatures
  (live, last updated 2026-10-05, fetched 2026-10-06). It documents the
  function-calling rules and the Chat Completions shape below for Vertex
  AI. That the Gemini Developer API's compatible endpoint
  (`generativelanguage.googleapis.com/v1beta/openai`) behaves the same is
  an inference (**unverified**); the live probe confirms it.
- https://ai.google.dev/gemini-api/docs/thought-signatures now says "This
  page has moved" and points to the Thinking guide, which documents only the
  Interactions API. An **archived** copy (last updated 2025-12-22) at
  https://web.archive.org/web/20260113214539/https://ai.google.dev/gemini-api/docs/thought-signatures
  carried the same rules for the Gemini API; it could not be re-fetched
  during review.
- https://ai.google.dev/gemini-api/docs/openai (live): "Gemini 3 supports
  OpenAI compatibility for thought signatures in chat completion APIs", and
  it links to the moved page.
- https://ai.google.dev/gemini-api/docs/thinking (live, Interactions API).

Findings (from the live Vertex page unless marked **archived**):

- Gemini 3: "you must return the thought signatures from previous responses
  in your subsequent requests, even when using `MINIMAL` thinking levels";
  omitting one is a 400. Gemini 2.5 does not enforce this.
- Placement: for a single call, the call carries a signature. For parallel
  calls, "the thought_signature is attached only to the first functionCall
  part". For sequential steps, "each function call will have a signature
  and you must pass all signatures back".
- Validation scope: "omitting a required `thought_signature` for the first
  `functionCall` part in any step of the current turn results in a `400`
  error". A turn begins at the most recent user message that is not a
  function response. Earlier turns are not validated (**archived**: "Only
  current turn is required; we don't validate on previous turns").
- Error text (**archived**): "Function call `<Function Call>` in the
  `<index>` content block is missing a thought_signature."
- OpenAI-compatible shape: `"extra_content": {"google":
  {"thought_signature": "<SIGNATURE_A>"}}` on individual `tool_calls`
  entries, not on the message. In the parallel example the second call has
  no `extra_content`.
- Text parts: "Including this signature in the next request is
  **recommended**", and "The API does **not** strictly enforce validation
  for signatures in non-functionCall parts." Where the OpenAI-compatible
  endpoint puts a text-response signature is not documented
  (**unverified**), so it is out of scope.
- Streaming: "When streaming, this signature might be returned in a part
  with empty text content, so be sure to parse all parts until
  `finish_reason` is returned." Which delta carries a tool call's
  `extra_content` on the compatible endpoint is **unverified**: T6 accepts
  it on any fragment of the call's index.
- History without signatures: "You can set thought_signature to
  `skip_thought_signature_validator`, but, this should be a last resort as
  it will negatively impact model performance." The live page shows this
  for REST function-call parts; its Chat Completions example does not show
  it. Whether the compatible endpoint accepts it inside `extra_content` is
  **unverified**; the live probe checks it. The **archived** page also
  listed `context_engineering_is_the_way_to_go`; the live page does not, so
  Likha uses only the skip value.
- Gemini 2.5 (**archived**): the signature sits "in the first part
  (regardless of type). It is optional to return that part." Decision 5
  never adds a skip value to a call the current model issued, so the
  default `gemini-2.5-flash` loop is unchanged.
- Interactions API (live): "When switching models within a session, you
  should still resend the previous model's thought blocks. The backend
  manages compatibility." This is for the Interactions API only; Likha does
  not use it.

## OpenRouter `reasoning_details`

Source: https://openrouter.ai/docs/use-cases/reasoning-tokens (Markdown
copy at the same URL with `.md`).

- Types: `reasoning.summary` (`summary`), `reasoning.encrypted` (`data`),
  `reasoning.text` (`text`, `signature`). Common fields: `id`, `format`
  (`anthropic-claude-v1`, `google-gemini-v1`, `openai-responses-v1`,
  `xai-responses-v1`, …), optional `index`.
- Streaming: "`reasoning_details` appears in `choices[].delta.reasoning_details`
  for each chunk"; "The `reasoning_details` array in each chunk may contain
  one or more reasoning objects"; "For encrypted reasoning, the content may
  appear as `[REDACTED]` in streaming responses"; "The complete reasoning
  sequence is built by concatenating all chunks in order."
- Passing back: either `message.reasoning` (string) or
  `message.reasoning_details` (array). "Use `reasoning_details` when
  working with models that return special reasoning types (such as
  encrypted or summarized)". The example comment says "Pass back
  unmodified". (The earlier draft's claim that `reasoning_content` is an
  alias was not found on the page during review: **unverified**.)
- Omission: the page documents no error when `reasoning_details` are left
  out; it describes preserving them as keeping "reasoning continuity".
  Today's Likha requests to OpenRouter work without them, which is why the
  row ships disabled (decision 4).
- Model switching: "You can easily switch between OpenAI reasoning models
  … and Anthropic reasoning models … without changing your code structure"
  when using `reasoning_details`. This is about client code, not about
  sending one model's details to another.
- "Preserving reasoning blocks is useful specifically for tool calling."
  "When providing reasoning_details blocks, the entire sequence of
  consecutive reasoning blocks must match the outputs generated by the
  model during the original request; you cannot rearrange or modify the
  sequence of these blocks."
- **Unverified:** whether "concatenating all chunks in order" means
  appending array elements or merging same-index fragments' text.
  Secondary reports (not primary sources) say unmerged fragments break the
  conversation: replaying per-delta fragments to Claude through OpenRouter
  returns 400 "Invalid signature in thinking block", and every later
  request fails the same way
  (https://github.com/agno-agi/agno/issues/8794,
  https://github.com/langchain-ai/langchain/issues/36400). Another reports a
  gateway rejecting replayed details that still carry the streaming
  `index` ("must not contain streaming index",
  https://github.com/code-yeongyu/senpi/issues/2122). Reports disagree on
  whether encrypted `data` is concatenated or kept whole. T7 merges
  consecutive same-`type`, same-`index` fragments; the probe confirms the
  rule and the `index` question with an Anthropic and a Gemini model
  before the row ships enabled.
- **Unverified:** OpenRouter's behavior when details from one model are
  sent to another. Likha never does this (decision 2).

## Anthropic thinking blocks (native client, via claude-native)

Sources: https://platform.claude.com/docs/en/build-with-claude/thinking,
https://platform.claude.com/docs/en/build-with-claude/extended-thinking,
https://platform.claude.com/docs/en/api/openai-sdk.

- Each thinking block carries a `signature`, "an encrypted copy of the full
  reasoning that you pass back unchanged in multi-turn and tool-use
  conversations".
- "when you return tool results, you must pass the thinking blocks from the
  assistant message back to the API, complete and unmodified." "Within the
  latest assistant message, the sequence of consecutive `thinking` blocks
  must match what the model generated … you can't rearrange, edit, or
  partially drop them. This includes `redacted_thinking` blocks." "Modified
  thinking blocks are rejected with a 400 error."
- Thinking toggled mid-turn: "the API doesn't error. Instead, it silently
  disables thinking for that request." It "may strip thinking blocks that
  would create an invalid turn structure, or disable thinking when the
  conversation history is incompatible with thinking being enabled."
- Streaming: `thinking_delta` events, then "a single `signature_delta`
  event just before the block's `content_block_stop`".
- Old blocks: "Pass all thinking blocks back in multi-turn conversations,
  and the API automatically filters them". Opus 4.5 and later Opus, Sonnet
  4.6 and later Sonnet, and the Fable and Mythos models keep all prior
  turns; earlier models keep the last turn only.
- Model switches: "Keep passing thinking blocks back unchanged when you
  switch models … A thinking block is readable only by the model that
  produced it and certain other models, and the API ignores or drops the
  blocks the target model can't read." For example, "A switch from Claude
  Opus 5.5 up to Claude Fable 5.1 on the Claude API keeps the earlier
  turns' reasoning". Likha's same-model rule for signed state (decision 2)
  gives up that reasoning, against Anthropic's advice, but never causes an
  error. claude-native may propose widening the rule for `Blocks` by
  amending this spec.
- Interleaved thinking places thinking blocks between `tool_use` blocks, so
  `Blocks` stores the whole ordered content array (T1).
- OpenAI-compatible endpoint (Likha's current `claude` row): "the OpenAI SDK
  doesn't return Claude's thinking", and `reasoning_effort` is "Ignored".
  There is nothing to replay on that path.

## Other providers Likha lists

- **OpenAI / ChatGPT (Responses):** https://developers.openai.com/api/docs/guides/reasoning
  says "we highly recommend you pass back any reasoning items returned with
  the last function call". With `store: false`, reasoning items carry
  `encrypted_content`. It is a recommendation; no error is documented, and
  "Our systems will smartly ignore any reasoning items that aren't relevant".
  Out of scope.
- **xAI:** https://docs.x.ai/docs/guides/reasoning documents encrypted
  reasoning only on the Responses API. Clients "that ignore the field keep
  working as before". Nothing on chat completions. No replay.
- **Groq:** https://console.groq.com/docs/reasoning returns `reasoning`
  (parsed) or `<think>` in content (raw; incompatible with tools). It says
  nothing about passing reasoning back. No replay.
- **Cerebras:** https://inference-docs.cerebras.ai/capabilities/reasoning
  returns `delta.reasoning`. Passing an earlier `reasoning` back is
  supported for Qwen (`clear_thinking`) but not required. No replay.
- **Mistral:** https://docs.mistral.ai/capabilities/reasoning/ returns
  thinking as content chunks and says "always replay the full assistant
  message (including ThinkChunk)". No error is documented. This belongs to
  the chunked-content spec, because it changes `content` itself.
- **Together, Bedrock (OpenAI-compatible), Dialagram, Opencode Zen/Go:** no
  replay requirement was found in their docs (**unverified**). They proxy or
  host DeepSeek, Kimi and GLM models, whose upstream APIs use
  `reasoning_content` and may need it back. They are candidates for a later
  row change after a probe.
