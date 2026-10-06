# Feature: Chunked message content (Mistral thinking and text chunks)

**Status:** planned — decisions recorded 2026-10-06 (user delegated defaults);
no implementation started. No new functional requirement: this is
conformance to [v1-spec FR-03](../v1-spec.md) (responses stream, and
assistant text is distinguished from other output) and FR-15 (thinking-model
reasoning renders in the muted role). Replay builds on
[reasoning-replay](../reasoning-replay/spec.md) (FR-39), which owns the
replay state and defers Mistral's wire shape to M2 of this spec.

## Context

Likha reads every API-key provider's reply as an OpenAI-compatible
chat-completions stream. Each streamed delta's `content` is read as a string.

Mistral's reasoning models send `content` in a different shape when they
think. At `reasoning_effort: "high"` the content is a list of typed chunks:
a `thinking` chunk, which itself holds a list of `text` chunks with the
reasoning trace, then a `text` chunk with the answer. While streaming, the
shape changes during one reply:

1. Thinking phase: each delta's `content` is a list holding a `thinking`
   chunk.
2. Transition: one delta whose list holds the closing `thinking` chunk and
   the first `text` chunk.
3. Answer phase: each delta's `content` is a plain string.

Likha fails the whole turn on the first list-shaped delta with a malformed
stream error, so no Mistral reasoning model can be used with thinking on.
This also blocks Mistral in [model-effort](../model-effort/context.md#provider-support-matrix):
its probe cannot pass while the reply at `high` does not parse. Some Mistral
models may send chunks without any effort field (`zai-glm-5-3`, the
deprecated `magistral-*` models). That is **unverified** (see
[research.md](research.md)). If it holds, those models fail in Likha today
at Default.

No other Likha provider documents list-shaped `content` in stream deltas.
The ones that document reasoning send it in a separate string field
(`reasoning` or `reasoning_content`), which Likha already reads. Sources and
per-provider findings are in [research.md](research.md).

## Role

Read a reply whose content arrives as typed chunks the same way Likha reads
a reply whose content arrives as a string. Answer text goes to the
transcript as assistant text. Thinking goes to the muted reasoning stream.
Content Likha cannot show is skipped and never fails the turn. Replies with
string content behave exactly as today.

## Scope

In scope: the chat-completions stream reader, for every provider that uses
it. This covers the main turn, explore children, profile tasks, compaction
and session naming, because all of them read replies through it.

Out of scope:

- Sending Mistral thinking back to Mistral. Mistral asks clients to replay
  the full assistant message, thinking included, on later turns. Likha
  keeps doing what it does today (FR-15: reasoning is never sent back)
  until [reasoning-replay](../reasoning-replay/spec.md) lands and this
  spec's M2 is done. Reasoning-replay owns the stored replay state, its
  persistence, the same-endpoint-and-model rule and the per-provider replay
  flag; it leaves Mistral to this spec. M2 here marks the `mistral` row as
  needing replay, fills the state from parsed chunks, and defines Mistral's
  wire shape (thinking sent back as a chunk inside `content`).
- Enabling Mistral effort. That needs M2 of this spec and a user-run live
  probe ([model-effort](../model-effort/tasks.md) M5,
  [effort-probe](../effort-probe/spec.md), FR-40).
- Images, documents, audio, citations and references inside content. They
  are skipped (decision 3).
- The ChatGPT Responses path. Its stream has its own event types and is
  unchanged.
- Replies that think and then stop with no answer and no tool call. Likha
  handles them as it handles an empty answer today.
- Splitting `<think>` tags out of string content (Groq `raw`, Together
  DeepSeek-R1). Such text stays answer text, as today.

## Decisions (2026-10-06, user delegated defaults)

1. **Chunked content is accepted from every chat-completions provider, not
   only `mistral`.** Each chunk names its own type, and relays (OpenRouter,
   opencode, Dialagram) may pass Mistral's shape through, so no per-provider
   switch is needed. String content takes the same path as today.
2. **`text` chunks are answer text and `thinking` chunks are reasoning.**
   Within one delta, chunks are handled in list order. Inside a `thinking`
   chunk, each `text` item is reasoning text, also in order. This keeps the
   transition delta's thinking ahead of its first answer text.
3. **Content Likha cannot show is skipped silently when the reply also
   shows something.** This covers:
   - a chunk of any other type (`image_url`, `document_url`, `file`,
     `input_audio`, `reference`, or a type added later);
   - an item inside `thinking` of any type other than `text` (`reference`,
     `tool_reference`);
   - a chunk or item with no string `type`.

   A skipped chunk adds no text and no reasoning. It shows no notice and
   does not count as output. Reason: none of these carry text the
   transcript can show, and failing would throw away a complete answer over
   a citation.

   A reply that ends with no answer text, no reasoning and no tool call,
   but skipped at least one chunk, ends with a visible error naming the
   first skipped type. Reason: before this feature such a reply failed as
   malformed; turning it into a silent empty answer would report a reply
   Likha could not read as a successful one (FR-09).
4. **Broken content still fails the turn as a malformed stream event, as
   today.** This covers:
   - `content` that is a number, a boolean or an object;
   - a list item that is not an object;
   - a `text` chunk, or a `text` item inside `thinking`, whose `text` is
     present and not a string or `null`;
   - a `thinking` chunk whose `thinking` is present and not a list or
     `null`.

   Reason: these are broken streams, not unknown features, and FR-09
   forbids reporting a broken reply as a successful one. A `null` or absent
   `text` or `thinking` is not broken: it adds nothing, the same way `null`
   content does today.
5. **The shape can change on any delta.** A list delta and a string delta
   in the same reply are parts of one answer, in arrival order.
6. **Reasoning from several `thinking` chunks joins into one saved
   reasoning string** in arrival order, with no added separator, as string
   reasoning does today. Streamed thinking chunks are fragments of one
   trace, so a separator would split words. A third-party report says
   Mistral rejects a replayed message with more than one leading thinking
   chunk (research.md, secondary), so one string is also the shape replay
   needs. If thinking arrives after answer text has started, the live
   transcript shows it as string reasoning in that position does today; the
   saved message still holds all thinking in one string.
7. **The chunk fields `signature` and `closed` are never shown.** `closed`
   is ignored. Mistral documents it as used only for prefixing. `signature`
   is read only once replay lands (decision 8); until then it is ignored,
   and its value can never fail a turn.
8. **Mistral thinking is replayed once reasoning-replay lands (M2).**
   Mistral says the model relies on its own trace across turns, and its
   `signature` field exists to replay reasoning. M1 only parses the chunks.
   The replay data model, persistence and the endpoint-and-model guard
   belong to [reasoning-replay](../reasoning-replay/spec.md), which names
   Mistral as out of its scope and defers it here. So M2 of this spec owns
   the Mistral-specific parts: the `mistral` row's replay flag and the wire
   shape, one leading `thinking` chunk followed by the answer text, sent
   only to the endpoint and model that produced it. Reason: the wire shape
   changes `content` itself, which only this spec defines.
9. **No new FR.** The feature makes an existing stream shape conform to
   FR-03 and FR-15. It adds no command and no setting. Its one new message,
   the error for a reply made only of unshowable content, is an ordinary
   model error under FR-09 and FR-11.
10. **The Mistral effort row moves from `blocked` to `pending-probe` once
    M1 lands, and Mistral effort is enabled only after M2 lands and the
    probe passes.** Parsing removes the stated blocker, so the probe can
    run. Mistral's docs tell clients to always replay the thinking, and
    stripping it "significantly degrades output quality", so turning effort
    on before replay would ship a mode the provider documents as degraded.

## Functional changes

### Reading a reply

1. When a delta's `content` is a string, Likha streams it as answer text,
   exactly as today. `null` or absent content adds nothing, as today.
2. When a delta's `content` is a list, Likha handles each chunk in order:
   - **`text`**: its `text` streams as answer text.
   - **`thinking`**: each `text` item in its `thinking` list streams as
     reasoning, in the muted role, the same way `reasoning` and
     `reasoning_content` fields stream today.
   - **anything else**: skipped (decision 3).
3. An empty `text` adds nothing and starts no answer or reasoning block.
4. A delta that also carries a `reasoning` or `reasoning_content` string is
   read as today. That string is handled after the delta's content.
   If a relay sent the same thinking both ways, it would show twice; no
   provider is documented to do this (research.md, **unverified**).
5. Tool calls in the same delta are read exactly as today.
6. The finished assistant message holds the joined answer text as its
   content and the joined thinking as its reasoning. It is saved with the
   session like any other reply. A resumed session shows the reasoning
   muted and the answer as assistant text, as it does for string reasoning
   today.
7. Thinking never becomes answer text. Compaction summaries and session
   names are built from answer text only, so Mistral thinking never enters
   them.

### Failures

8. A reply that ends with no text, reasoning or tool call, after skipping
   at least one chunk, ends the turn with a visible error naming the first
   skipped chunk type (decision 3). The turn is not recorded as successful.
   A reply with no content at all still ends as an empty answer does today.
9. Broken content (decision 4) ends the turn with Likha's existing visible
   malformed-stream error. The turn is not recorded as successful (FR-09,
   FR-11).
10. Apart from point 8, nothing about a skipped chunk is shown or logged.

### What is sent

11. Requests are byte-identical to requests before this feature. Assistant
    messages are still sent with string content, and Mistral thinking is
    not sent back until [reasoning-replay](../reasoning-replay/spec.md)
    lands.

### Effort matrix

12. The `mistral` row in [model-effort](../model-effort/context.md#provider-support-matrix)
    and in `docs/providers.md` changes from `blocked` to `pending-probe`.
    No Mistral effort is sent until M2 has landed and a probe passes
    (decision 10).

## Acceptance criteria

- A test server streams Mistral's three phases: list deltas with a
  `thinking` chunk, then one transition list with a closing `thinking`
  chunk and a `text` chunk, then string deltas. The turn completes. The
  thinking streams as reasoning and the answer as assistant text, in that
  order. The saved message has that content and that reasoning.
- A `thinking` chunk that holds a `reference` item and a `text` item yields
  only the `text` item's reasoning.
- Chunks of type `image_url`, `reference`, an invented `future_kind`, and a
  chunk with no `type`, streamed alongside a `text` chunk, are skipped. The
  turn completes with the text, no error and no notice.
- A reply whose only content is skipped chunks ends the turn with a
  visible error naming the first skipped type, and is not recorded as
  successful.
- `content` as a number, a boolean or an object, a list item that is a
  string, a `text` chunk with numeric `text`, a `text` item inside
  `thinking` with numeric `text`, and a `thinking` chunk whose `thinking`
  is a string each end the turn with the malformed-stream error.
- A `text` chunk with `null` or absent `text`, and a `thinking` chunk with
  `null` or absent `thinking`, add nothing and do not fail the turn.
- Every existing string-content stream test passes unchanged, and request
  bodies are byte-identical, including for a history that holds a reply
  parsed from chunks.
- Two `thinking` chunks in separate deltas give one reasoning string that
  equals their concatenation.
- Tool calls streamed after list-shaped thinking are returned complete,
  with the reasoning kept.
- A compaction or session-naming request whose reply holds thinking chunks
  produces a summary or name from the answer text only.
- The model-effort matrix and `docs/providers.md` list `mistral` as
  `pending-probe`, and no Mistral effort is sent.
- Live (user-run): a Mistral reasoning model at `reasoning_effort: "high"`
  with tools completes a turn in the real TUI. Thinking shows muted and the
  answer shows as assistant text. Until the user reports this probe,
  nothing claims Mistral works.
