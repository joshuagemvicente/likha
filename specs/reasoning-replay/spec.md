# Feature: Reasoning replay (send provider-required reasoning state back)

**Status:** implemented (local) 2026-10-06 — automated suite green; live
probes (tasks T18, run by the user) outstanding, so no provider is verified.
Decisions recorded 2026-10-06 (user delegated defaults). Requirement: v1-spec FR-39
(amended into [v1-spec.md](../v1-spec.md)). FR-39 also amends FR-15
and the closing sentence of FR-37, which both say reasoning "is never sent
back to the provider", and FR-21's Steer now rule, which discarded partial
reasoning (rule 13). This spec owns the shared replay data model.
[claude-native](../claude-native/spec.md) uses it for Anthropic thinking
blocks, and [chunked-content](../chunked-content/spec.md) M2 uses it for
Mistral thinking.

## Context

Some reasoning providers return opaque reasoning state with each assistant
message and want it back unchanged on later requests in the same
conversation. For some, leaving it out is an error:

| Provider (Likha row) | State | Required when | Without it |
| --- | --- | --- | --- |
| DeepSeek (`deepseek`) | `reasoning_content` on every earlier assistant message | the request carries `tools`, which every Likha main turn does | HTTP 400 (documented) |
| Google Gemini 3 (`gemini`) | `tool_calls[].extra_content.google.thought_signature` | function calls in the current turn | HTTP 400 (documented) |
| OpenRouter (`openrouter`) | `reasoning_details`, unmodified | recommended for tool use on reasoning models | no error documented; the model loses its earlier reasoning |
| Anthropic native Messages ([claude-native](../claude-native/spec.md)) | `thinking` / `redacted_thinking` blocks, unmodified | tool results following a thinking turn | no error; thinking is disabled for that request. Modified blocks are a 400 |

Likha keeps only display reasoning today, and FR-15 says reasoning "is never
sent back to the provider". DeepSeek enables thinking by default, so a
DeepSeek tool loop fails on its second request. Gemini 3 fails the same way
after its first tool call. Sources are in [research.md](research.md).

Display reasoning and replay state are different things. Display reasoning is
text Likha shows in the transcript. Replay state is a payload the provider
issued for itself. This feature sends only the second, only back to the
issuer, and only where the provider row records the need.

## Role

Make multi-step tool loops work on providers that require their own reasoning
state back. Never send display reasoning. Never send one provider's state to
another, and never send signed state to a model other than the one that
signed it. Never change a request for a provider whose row does not record
replay.

## Scope

In scope: capturing replay state from chat-completions streams on DeepSeek,
Gemini and OpenRouter; sending it back on later requests; keeping it through
session save and resume, explore children and profile tasks; the shared data
model the native Claude client and Mistral replay also use.

Out of scope:

- ChatGPT (Responses API) encrypted reasoning items. OpenAI recommends passing
  them back but does not require it, and requests succeed without them.
- xAI, Groq, Cerebras, Together, Bedrock, Dialagram, Opencode Zen and
  Opencode Go. None documents a replay requirement on chat completions. Any
  of them can be enabled later by a provider-row change and a live probe.
- Mistral thinking chunks. They change `content` itself, so
  [chunked-content](../chunked-content/spec.md) M2 owns them and adds its own
  replay form to this data model.
- Gemini thought signatures on text-only responses. Google documents them as
  recommended and not validated, and does not document where the
  OpenAI-compatible endpoint returns them.
- Translating state between providers or models.
- Showing replay state anywhere in the TUI.

## Decisions (2026-10-06, user delegated defaults)

1. **Replay state is stored on the assistant message with the endpoint and
   model that issued it.** The endpoint is the full request URL, not the
   provider's base URL, so two wires on one base (OpenAI chat completions
   and the ChatGPT Responses route; Anthropic's compatibility route and its
   native Messages route) are different issuers. Reason: the issuer is the
   only safe target.
2. **How closely the request must match the issuer depends on the form.**
   Unsigned reasoning text (DeepSeek `reasoning_content`) goes back to any
   model on the same endpoint. Signed or encrypted state (Gemini signatures,
   OpenRouter details, Anthropic blocks) goes back only to the same endpoint
   and model. Reason: DeepSeek offers two models (`deepseek-flash`,
   `deepseek-v4-pro`) and rejects a tool request whose earlier turns lack
   `reasoning_content`, so an endpoint-and-model rule would break every
   switch between them. The text is plain context on the same provider.
   Signatures are model-bound. For Anthropic, this gives up reasoning that
   an upgrade switch could have kept (research.md); claude-native may widen
   it by amending this spec.
3. **State that does not match is left out of the request and kept in
   history.** Reason: switching back to the issuer sends it again.
4. **Each provider row records whether it needs replay and in which form.**
   `deepseek` and `gemini` ship enabled, because their docs say a request
   without the state fails with 400, so not sending it is the failing
   direction. `openrouter` ships disabled until the user's live probe
   passes. Its docs document no error without replay. Sending wrongly
   assembled details is a 400 that comes back on every later request (see
   research.md), and the stream assembly rule is not documented. All enabled
   rows still need a live probe before they are called verified.
5. **On Gemini, a current-turn tool call that the current model did not
   issue gets Google's documented skip value** (`skip_thought_signature_validator`)
   on the first call of its assistant message. Calls the current model
   issued are sent exactly as received, with or without a signature.
   Reason: this covers `/retry` after a model switch and sessions saved
   before this feature, which would otherwise fail with 400, and leaves
   Gemini 2.5 tool loops (the `gemini` default model) as they are today.
   Google calls the value a last resort that lowers quality.
6. **Steer now keeps unsigned reasoning text (DeepSeek), never signed
   state.** Reason: DeepSeek rejects earlier turns without
   `reasoning_content`, and a prefix of plain text is still text. A signed
   payload cut short would be rejected.
7. **A 400 on a request where replay may be the cause adds a hint** naming
   `/compact`. It applies when the request lacked state the provider
   documents as required, or carried replay state. Reason: in both cases
   every later request repeats the same history and fails the same way, and
   a summary is the way out. Absence of OpenRouter or Anthropic state never
   causes a 400, so it never triggers the hint.
8. **Background requests send no replay state** (amended 2026-10-07).
   Compaction and session naming are tool-less and end with a new user
   message, so neither provider needs the state there: DeepSeek ignores
   `reasoning_content` when no tools are sent, and the closing user message
   starts a new Gemini turn, which Google does not validate. Reason: the
   original rule (send matching state like any other request) let `/compact`
   carry every own Gemini signature the provider had just rejected, so the
   way out of a rejection failed the same way and the session was stuck.
   Without replay state, a 400 on these requests is a plain provider error
   with no `/compact` hint (rule 12). claude-native defines its own
   tool-less shape (its decision 6). *Amended 2026-10-07 by
   [chunked-content](../chunked-content/spec.md) T10:* the same holds for
   Mistral thinking chunks. Mistral documents no error for a history
   without them, so compaction and session naming send Mistral messages as
   plain string content, and `/compact` still works after Mistral rejected
   the replayed shape.
9. **Every completed response on an enabled provider is marked with its
   issuer, even when it carried no state.** Reason: it tells "this model
   issued nothing" (Gemini 2.5, a non-thinking turn) apart from "another
   model or an older Likha wrote this", which rules 7 and 12 need.

## Functional changes

### What is kept

1. When a response on a provider whose row records replay carries replay
   state, Likha keeps it with that assistant message, together with the
   endpoint and model that issued it:
   - DeepSeek: the message's `reasoning_content`.
   - Gemini: each tool call's thought signature.
   - OpenRouter: the message's `reasoning_details`, assembled from the
     stream into the provider's complete objects, in order.
   - Native Claude: the assistant turn's content blocks, in order
     ([claude-native](../claude-native/spec.md)).

   Every completed response on such a provider also records its issuer,
   even when it carried no state (decision 9).
2. Replay state is kept as the provider issued it. Likha never edits,
   reorders or trims it, and never merges it with another message's state.
3. A response on any other provider gets no replay state. Its saved session
   is identical to one saved before this feature.

### What is sent

4. A later request sends an assistant message's replay state, in the
   provider's own field, only to a matching issuer (decision 2): DeepSeek
   reasoning text to the same endpoint; every other form to the same
   endpoint and model. Other assistant messages are sent as today.
5. Display reasoning is never sent, on any provider (FR-15).
6. A request to a provider whose row does not record replay is
   byte-identical to the same request before this feature, whatever replay
   state the history holds.
7. On Gemini, an assistant message in the current turn (after the last user
   prompt) whose tool calls the current model did not issue is sent with
   Google's skip value on its first tool call (decision 5). Earlier turns
   are sent without signatures, as Google does not validate them.

### Model and provider switches

8. After `/models` switches model or provider, earlier assistant messages
   whose state does not match are sent without it (decision 3). The state
   stays in history and is sent again after switching back. What the
   provider does then:
   - DeepSeek, between its own models: the reasoning text is still sent.
   - DeepSeek, from another provider or an older session: earlier assistant
     messages lack `reasoning_content`. Whether DeepSeek rejects this is
     **unverified** (research.md). If it returns 400, rule 12's hint
     applies.
   - Gemini: the next prompt starts a new turn, so nothing earlier is
     validated. When no prompt comes between (`/retry` after a switch),
     rule 7 covers the current turn's calls.
   - OpenRouter and native Claude: the request succeeds; the new model
     starts without the old model's reasoning.

### Sessions, compaction and children

9. Replay state is saved with the session and restored on resume. Sessions
    saved before this feature load unchanged and carry none.
10. `/compact` replaces history with a summary that carries no replay state.
11. Explore children and profile tasks keep and send their own replay state
    within their own loops. Saved task transcripts do not keep it. A child never
    receives the parent's state, and the parent never receives a child's.

### Failures

12. When the provider rejects a request with HTTP 400 and the request either
    lacked state the provider documents as required (DeepSeek: an earlier
    assistant message from another endpoint or an older session; Gemini: a
    call sent with the skip value) or carried replay state, the visible
    error adds:
    `Earlier turns' reasoning state may be missing or not accepted by this model; /compact continues from a summary.`
    A 400 on a request with neither shows no hint. The request is not
    retried (FR-36 never retries rejected requests). When the request also
    carried effort, both hints show.
13. A request stopped by Steer now keeps its visible text, as today. When
    that text is kept on DeepSeek, the reasoning text streamed so far is
    kept with it as `reasoning_content` (decision 6). Nothing is kept when
    no text streamed, as today. Signed state is never kept from a stopped
    or failed request.

### Context tracker

14. The context estimate counts the replay state that the next request will
    send, and nothing that it will leave out.

## Acceptance criteria

- A test server playing DeepSeek sees, on the second request of a tool loop,
  the first response's `reasoning_content` on that assistant message, and the
  display reasoning nowhere else in the body. After a switch between two
  DeepSeek models on the same endpoint, the text is still sent.
- A test server playing Gemini sees each tool call's thought signature
  returned at `tool_calls[].extra_content.google.thought_signature`. Parallel
  calls that came without a signature are sent without `extra_content`.
- A Gemini tool call issued by the current model without a signature is sent
  without `extra_content` (Gemini 2.5 bodies are unchanged). A current-turn
  call issued by another model or an older session carries the skip value
  on its message's first call; a previous-turn one does not.
- With the OpenRouter replay form enabled on a test client, the server sees
  `reasoning_details` as the complete objects assembled from the stream, in
  order. With the shipped `openrouter` row, request bodies are byte-identical
  to a build without this feature.
- Assistant messages whose signed state came from another model or
  endpoint, or whose DeepSeek text came from another endpoint, are sent
  without replay fields.
- On every provider without replay, request bodies are byte-identical to a
  build without this feature, including for histories that hold replay state
  from another provider.
- Replay state survives save and resume, and a session saved before this
  feature loads and continues.
- `/compact` output carries no replay state.
- An explore child's second request sends its own first response's state,
  and none of the parent's.
- A 400 on a request with a missing required state or with sent state shows
  the `/compact` hint once. A 400 on a request with neither, including any
  request to a provider without replay, does not.
- Live probes (run by the user): a 3-round tool loop completes on DeepSeek
  and Gemini 3, and Gemini 2.5 still completes one. A local build with the
  OpenRouter form enabled completes one on an Anthropic and a Gemini model
  before the row ships enabled. The outcomes are recorded in
  [context.md](context.md#provider-matrix).
