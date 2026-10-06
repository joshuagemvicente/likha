# Context: Chunked message content

Code, tests and docs this feature touches, with the facts that shaped the
spec. Decisions live in [spec.md](spec.md); sources in
[research.md](research.md).

## Code

- `internal/model/client.go`
  - `streamChunk.Choices[].Delta.Content` is `*string`. A list-shaped
    `content` makes `json.Unmarshal` fail. `consumeStream`'s `process` then
    returns `malformed model stream event: json: cannot unmarshal array …`,
    and the turn ends with that error.
  - `consumeStream`:
    - writes `Delta.Content` to `text` and calls `onText` when the piece is
      non-empty;
    - then reads `Delta.Reasoning`, else `Delta.ReasoningContent`, into
      `reasoning` and calls `onReasoning`;
    - then reads tool-call fragments.

    At the end, `result.Content = text.String()` and
    `result.Reasoning = reasoning.String()`.
  - The same function rejects a tool-call fragment whose `type` is not
    `function`, clipping the type to 32 bytes in the error. The
    only-unshowable-content error (spec point 8) clips the chunk type the
    same way.
  - `Message.Reasoning` is commented "never sent back to the provider", and
    `requestMessage.Content` is a string. Both stay as they are here; the
    replay foundation changes them.
  - `Client.stream` builds the request. It is unchanged by this feature.
- `internal/model/codex.go`, `responses_stream.go`: the ChatGPT Responses
  path. Not touched.
- Callers that inherit the fix through `Stream`/`StreamUsage`:
  - `internal/agent/turn_execution.go` (main turn);
  - `internal/agent/explore_loop.go` and `profile_runner.go`. They
    accumulate `partial.Reasoning` from `onReasoning` and clip it to
    `exploreStreamBytes`;
  - `internal/agent/compaction.go` and `sessionname.go`. Both pass a nil
    reasoning callback and use only `Content`, so thinking chunks never
    reach a summary or a session name.
  - `turn_execution.go`'s steer-now path keeps only streamed answer text
    from an interrupted request and drops partial reasoning. Thinking
    chunks follow that rule unchanged.
- `internal/session/session.go`: persists `[]model.Message` as plain JSON.
  Content and Reasoning already persist, so this feature needs no new
  fields. `sameConversation` compares `Content`, `Reasoning` and `ToolCall`
  values with `==`, which is unaffected.
- `internal/tui/dialog.go` `resumeSession` rebuilds the transcript from
  the saved transcript entries, not from `Message.Reasoning`. A resumed
  chunked reply therefore looks the way a string-reasoning reply looks
  after resume today.
- `internal/model/provider.go`: the `mistral` row
  (`https://api.mistral.ai/v1`, `LIKHA_MISTRAL_API_KEY`, no default model,
  `Effort` zero value). It is unchanged by this feature.

## Tests

- `internal/model/client_test.go`: `localClient` (httptest server plus
  client) and `sendEvents` (writes `data:` events) are the harness for new
  stream fixtures. Existing string-content tests:
  - `TestStreamTextAndIndexedToolCalls`
  - `TestStreamSplitSSEEventAndToolMetadata`
  - `TestStreamAcceptsDoneWithoutTrailingBlankLine`
  - `TestStreamFailures`

  They must pass unchanged.
- `internal/model/usage_detail_test.go`: usage tests that also exercise
  `consumeStream`.

## Coordinated specs

- [reasoning-replay](../reasoning-replay/spec.md) (FR-39, amends FR-15):
  - owns the replay state on `model.Message`, its persistence, the
    same-endpoint-and-model guard, and the request encoding that sends
    reasoning back;
  - its spec.md Scope lists Mistral as out of scope ("Mistral thinking
    chunks belong to the chunked-content spec"), and its context.md matrix
    shows `mistral | none (chunked-content spec)`;
  - so this spec's M2 sets the `mistral` row's replay flag, fills the state
    for Mistral thinking, and adds the list-`content` encoding. The needs
    are listed in [tasks.md](tasks.md) M2.
- [model-effort](../model-effort/context.md#provider-support-matrix):
  - the matrix row `mistral | reasoning_effort | blocked | chunk-list
    content must parse first` becomes
    `mistral | reasoning_effort | pending-probe | chunk content parses
    (chunked-content); probe high with tools; enable only after
    chunked-content M2 (thinking replay)`;
  - model-effort T20's "Mistral stays blocked until its chunk-list content
    parses" becomes "Mistral may be probed once chunked-content M1 lands,
    and is enabled only after its M2 lands";
  - the "Known gaps → Mistral chunk content" bullet and the `client.go`
    bullet about `streamChunk.Delta.Content` are updated to point here;
  - `internal/model/effort.go` needs no change. When the probe passes, the
    row takes the existing `EffortTopLevel` kind.
- [model-recovery](../model-recovery/spec.md) T3 (`OutputSeen`):
  - text and thinking from chunks count as output;
  - skipped chunks do not;
  - the only-unshowable-content error (spec point 8) is not retryable.

  Whichever lands second applies the rule.
- [effort-probe](../effort-probe/spec.md) (FR-40): its plan table lists
  `mistral` as a top-level `reasoning_effort` provider. The Mistral probe
  sends `reasoning_effort: "high"` with tools. This spec's M1 is its
  precondition; enabling the row also waits for M2 (spec decision 10).
- [additional-providers](../additional-providers/spec.md): the Mistral row
  is still pending its admission probe.

## Docs

- `docs/providers.md` effort table: the `mistral` row reads
  `blocked: chunked reasoning replies do not parse yet` and becomes
  `pending-probe`.
- `CHANGELOG.md` **Unreleased**: one entry naming the automated checks and
  stating that no live Mistral probe has run.
