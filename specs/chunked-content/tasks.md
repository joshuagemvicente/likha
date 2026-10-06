# Tasks: Chunked message content

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves `go test ./...`
and `go vet ./...` green and can land on its own.

- M1 does not depend on reasoning-replay: Mistral replies parse and display,
  and nothing is sent back.
- M2 starts only after the replay foundation
  ([reasoning-replay](../reasoning-replay/spec.md)) has landed its data model
  and request encoding.

## M1 — Parse chunked content (`internal/model`)

- [x] **T1. A content type that accepts string or list.** Replace
      `Delta.Content *string` with a small type, for example
      `streamContent`, that has its own `UnmarshalJSON`:
      - JSON `null` or absent: no pieces.
      - JSON string: one answer piece, empty or not.
      - JSON array: one piece per element, in order:
        - `text` → answer piece. `text` must be a JSON string; `null` or
          absent gives no piece.
        - `thinking` → one reasoning piece per `text` item in `thinking`.
          `thinking` must be a JSON array; `null` or absent gives no
          pieces. A `text` item's `text` follows the same rule as a `text`
          chunk. Non-`text` items and items without a string `type` are
          skipped.
        - any other `type`, or no string `type` → a skipped piece that
          records the type (empty when there is none), clipped to 32 bytes
          as the tool-call type is in `consumeStream`.
      - Anything else (number, boolean, object), a non-object array
        element, a non-string non-null `text`, or a non-array non-null
        `thinking` → an error wrapping a package sentinel such as
        `errMalformedContent`. `process` already wraps it as
        `malformed model stream event: %w`.

      Keep each element as `json.RawMessage` until its `type` is read, so a
      skipped chunk's other fields are never decoded and cannot fail the
      turn. Do not decode `signature` or `closed` in M1: nothing reads them
      until M2, and dead fields are not kept (AGENTS.md). Use no reflection
      and no generic union helper.
      *Verify:* table test on `streamContent.UnmarshalJSON` covering:
      - null, absent, `""` and `"hi"`;
      - the docs' thinking/text list;
      - a transition list (thinking then text);
      - a thinking chunk with `reference` and `tool_reference` items;
      - `image_url`, `reference`, `future_kind`, and a type-less chunk,
        each recorded as skipped with its type;
      - `null`/absent `text` and `thinking`, which give no pieces;
      - each broken shape from spec decision 4. Each returns an error, and
        a stream test shows the error from `consumeStream` matches the
        sentinel with `errors.Is` (never a substring check). Note that
        `encoding/json` returns a custom `UnmarshalJSON` error unwrapped
        in a `*json.UnmarshalTypeError`, so `errors.As` on that type would
        not match.
- [x] **T2. Emit pieces in order in `consumeStream`.** For each piece in
      the delta's content, in order:
      - answer: write to `text`, and call `onText` when non-empty;
      - reasoning: write to `reasoning`, and call `onReasoning` when
        non-empty.

      - skipped: remember the first skipped type for the reply.

      Then handle `Delta.Reasoning` / `Delta.ReasoningContent` as today,
      then the tool calls. A string-content delta must take exactly
      today's path: same writes, same callback calls.

      After `[DONE]`, when the reply has no text, no reasoning and no tool
      call but a skipped type was recorded, return an error such as
      `model reply held only content Likha cannot show (type %q)` (spec
      point 8). Classify it as not retryable if model-recovery has landed.
      *Verify:*
      - an httptest stream with Mistral's three phases (list thinking
        deltas, transition list, string deltas, `[DONE]`). Assert the
        ordered sequence of `onReasoning`/`onText` calls and the final
        `Message.Content` and `Message.Reasoning`;
      - a stream with two thinking deltas plus an empty-`text`
        separator. Reasoning equals the concatenation, and no empty
        callback fires;
      - a stream with list thinking followed by indexed tool-call
        fragments. The tool calls are complete and the reasoning is kept;
      - a stream of only skipped chunks plus `[DONE]` returns the
        only-unshowable-content error naming the first type, and no
        message;
      - a stream of skipped chunks plus one `text` chunk returns that text
        and no error;
      - a stream of empty string deltas plus `[DONE]` still returns an
        empty assistant message, as today.
- [x] **T3. Regression guard.** Run every existing `client_test.go`,
      `usage_detail_test.go` and `context_usage_stream_test.go` stream test
      unchanged. Add one byte-for-byte request-body assertion: a
      conversation that contains a reply parsed from chunks is re-sent with
      string assistant content and no thinking (spec point 10).
      *Verify:* `go test ./internal/model/`.
- [ ] **T4. Recovery coordination.** *(Landed first: model-recovery T3 has no `OutputSeen` on main yet, so it applies this rule and adds the test; see notes.)* With
      [model-recovery](../model-recovery/tasks.md) T3 (`OutputSeen`):
      - non-empty answer and reasoning pieces from chunks set it;
      - skipped chunks do not.

      Whichever of this task and model-recovery T3 lands second applies
      the rule and adds its test.
      *Verify:* a `consumeStream` test, added by whichever lands second,
      in which a list delta holding only an `image_url` chunk leaves
      `OutputSeen` false and a list delta holding a `thinking` chunk sets
      it.
- [x] **T5. Compaction and naming read answer text only.** No code change
      is expected: `compaction.go` and `sessionname.go` pass a nil
      reasoning callback and use `Content`.
      *Verify:* an agent test with an httptest server whose compaction
      reply holds a `thinking` chunk and a `text` chunk; the summary equals
      the text alone.
- [x] **T6. Docs and matrix.**
      - In [model-effort/context.md](../model-effort/context.md):
        - set the `mistral` matrix row to `pending-probe`, with the note
          "chunk content parses (chunked-content); probe high with tools;
          enable only after chunked-content M2 (thinking replay)";
        - point the "Known gaps → Mistral chunk content" bullet and the
          `client.go` `streamChunk` bullet to this spec, as resolved;
        - in model-effort tasks T20, replace "Mistral stays blocked until
          its chunk-list content parses" with "Mistral may be probed once
          chunked-content M1 lands, and is enabled only after its M2 lands
          (chunked-content decision 10)".
      - Set `docs/providers.md`'s `mistral` effort row to `pending-probe`.
      - Add the feature index row in `specs/README.md`.
      - Add an **Unreleased** entry in `CHANGELOG.md` stating automated
        checks only, with no live Mistral probe.

      These edits are made by the implementer of this milestone, not by the
      spec author.
      *Verify:* `grep -n mistral specs/model-effort/context.md
      docs/providers.md` shows no `blocked`, and
      `internal/model/provider.go`'s `mistral` row still has the zero
      `Effort` value.

## M2 — Record Mistral thinking for replay (after reasoning-replay lands)

Depends on the foundation in [reasoning-replay](../reasoning-replay/spec.md).
The foundation owns the field names, persistence, the endpoint-and-model
guard and the per-provider replay flag. Reasoning-replay lists Mistral as
out of its scope and defers it here, so this milestone owns the Mistral
parts: setting the `mistral` row's replay flag, filling the state from
parsed chunks, and encoding Mistral's list-shaped `content` on resend. If
the foundation's request message cannot carry list `content`, add that
here as the smallest change to the foundation's encoder, without changing
any other provider's bytes (reasoning-replay point 6).

- [x] **T7. Fill the replay state from thinking chunks.** Set the
      `mistral` row's replay flag in `internal/model/provider.go` in the
      foundation's form. Only a row with that flag records state, so a
      relay that passes Mistral chunks through records none
      (reasoning-replay point 3). On the `mistral` row, when a reply
      contained at least one `thinking` chunk:
      - store its full, unclipped joined thinking text in the replay state;
      - store the last non-empty string `signature` seen, if any. A
        `signature` that is `null`, absent, empty or not a string counts as
        none and never fails the turn (spec decision 7);
      - tag it with the issuing endpoint and model, as the foundation
        requires.

      The display `Reasoning` stays as today; explore and profile clipping
      must not shorten the replay copy. A reply with only string content,
      or only `reasoning`/`reasoning_content` fields, records no Mistral
      replay state.
      *Verify:* unit test on the parsed message: the replay state is
      present, the signature is kept, a numeric signature is dropped
      without error, string-only replies carry none, and the same chunked
      stream on a row without the flag carries none. Joining one reply's
      thinking fragments is not "merging with another message's state"
      (reasoning-replay point 2); the state is still that one message's.
- [x] **T8. Wire shape on resend (Mistral case).** With the `mistral` row's
      replay flag set (reasoning-replay decision 4), and sent
      to the same endpoint and model, an assistant message with Mistral
      replay state encodes `content` as a list:
      - exactly one leading `{"type":"thinking","thinking":[{"type":"text","text":…}]}`,
        with `"signature"` only when one was stored;
      - then `{"type":"text","text":<Content>}` when `Content` is non-empty;
      - `tool_calls` unchanged.

      The request never holds more than one thinking chunk: a third-party
      report shows Mistral's Conversations API returning 400 "Expected at
      most one leading ThinkChunk, got 2" (research.md, secondary; whether
      chat completions enforces it is unverified). Thinking that streamed
      after answer text started is still sent inside the one leading
      chunk. Sent to any other endpoint or model, the message encodes
      exactly as today.
      *Verify:* request-body test against an httptest server, for the same
      model, a different model, and a different provider, plus a message
      whose thinking arrived in two separate chunks around answer text
      (one thinking chunk on the wire).
- [ ] **T9. Live probe (user-run).** The user runs a Mistral reasoning model
      (`mistral-medium-latest` or `zai-glm-5-3`) at `reasoning_effort:
      "high"` with tools, across at least two tool rounds and one
      follow-up prompt. The probe records:
      - whether the transition delta sets `closed`;
      - whether `signature` is ever non-null;
      - whether thinking arrives without any effort field (`zai-glm-5-3`);
      - whether a history without thinking is rejected.

      The results go in [research.md](research.md) and the model-effort
      matrix. Agents never record this probe as passed on the user's
      behalf.
      *Verify:* the user's report, quoted with its date in research.md.
- [x] **T10. Turn the `mistral` row's replay on.** *(Landed 2026-10-07,
      local; see "T10 notes" below. The live probe, T9, stays open.)* *(Added 2026-10-06 by
      review: T7 built the capture and T8 the wire, but the row keeps
      `ReplayNone` because FR-39's rejection path does not exist yet.)*
      Only after reasoning-replay T12 lands `ReplayRejectedError` and the
      TUI's `/compact` hint, in one change:
      - set `Replay: ReplayThinkingChunk` on the `mistral` row (T12's
        `Sent` count already includes assistant messages encoded with a
        thinking chunk since the 2026-10-07 merge, see below, and
        `replaySentText` already returns the thinking for this wire);
      - decide, by amending reasoning-replay decision 8 if needed, whether
        compaction and session naming leave Mistral thinking out, so that
        `/compact` still works when Mistral rejects the replayed shape;
      - reword the child-detail label in `internal/tui/agent_detail.go`
        ("display only; never sent to the provider"), which becomes untrue
        for Mistral children once their thinking is replayed;
      - amend v1-spec FR-39 to name Mistral thinking chunks (same endpoint
        and model), set the `mistral` row of the
        [reasoning-replay matrix](../reasoning-replay/context.md#provider-matrix)
        to the thinking-chunk wire, and update the `replyHook` comment in
        `internal/agent/turn_execution.go` and the CHANGELOG.
      *Verify:* an httptest 400 on a request carrying Mistral thinking gives
      `errors.As(err, *ReplayRejectedError)` with `Sent > 0`; the TUI shows
      the `/compact` hint once; a compaction request after a chunked reply
      holds what the amended decision 8 says.

## T10 notes (2026-10-07, local)

Automated suite green (`go test ./...`, `go vet ./...`, `gofmt`). No live
Mistral request was sent; T9 is the user's.

- **Row.** `internal/model/provider.go`'s `mistral` row sets
  `Replay: ReplayThinkingChunk`. `TestMistralRowKeepsReplayOff` became
  `TestMistralRowReplaysThinking` (the shipped base resolves the wire; a
  relay and a custom endpoint still resolve `ReplayNone`; effort stays
  unsupported), and `TestShippedProviderReplayWires` lists `mistral`.
- **Decision 8.** No code change: `backgroundClient` already forks with
  `WithoutReplay`, so compaction and naming send Mistral messages as plain
  strings. Reasoning-replay decision 8 is amended to say so, and
  `TestBackgroundRequestsSendNoReplay` now runs on the `mistral` row too
  (no `"thinking"` key and no thinking text in either body; a 400 is a
  plain error without the hint).
- **Rejection.** `TestMainLoopReplaysMistralThinkingAndTypesItsRejection`
  runs the real main loop on the `mistral` row: the second request carries
  one leading thinking chunk with the first reply's text, exactly once, and
  a 400 on it is a `ReplayRejectedError` with `Sent == 1`.
  `TestMistralThinkingRejectionShowsCompactHint` (TUI) gets that error from
  a real client on the row and shows the `/compact` hint once.
- **Child-detail label.** It said "display only; never sent to the
  provider", which became untrue once Mistral children replay their
  thinking (and was already loose for DeepSeek). It now reads "display
  copy; a provider that needs reasoning back got the child's own copy
  during its run".
- **`replyHook`.** The task names a `replyHook` comment in
  `internal/agent/turn_execution.go`; no such identifier exists on main,
  and no comment there says Mistral thinking is not sent, so nothing
  changed. The `WithoutReplay` and `ReplayThinkingChunk` doc comments in
  `internal/model/replay.go` were updated instead.
- **Agent test helper.** `replayRequest` in `internal/agent/replay_test.go`
  decodes `content` through a small `wireContent` type (string or chunk
  list), since a Mistral replay request no longer decodes into a string.

## Merge with reasoning-replay (2026-10-07)

Both branches added replay capture to `consumeStream`. The merge keeps one
path for each direction and deletes `thinking_replay.go`:

- **Capture.** reasoning-replay's `replayCollector` (`replay_stream.go`)
  gains the Mistral thinking accumulator (`addContent`), active only under
  `ReplayThinkingChunk`; `finish` fills `Thinking`/`ThinkingSignature`.
  The separate `replayCapture.replayFor`/`chunkThinking` pair is gone.
- **Request.** `requestContent` moved to `replay_request.go`;
  `replayEncoder` fills the leading thinking chunk through
  `fillReplayFields`, so `contentFor` is gone and a 400 on a request
  carrying Mistral thinking is a `ReplayRejectedError` with `Sent > 0`
  (tested with the wire enabled in-process). The row stayed `ReplayNone`
  until T10 (landed 2026-10-07).

## Implementation notes (2026-10-06, local)

M1 and M2's capture (T7) and wire (T8) landed on `feat/chunked-content`;
automated suite green (`go test ./...`, `go vet ./...`, `gofmt`). T10
(turning the `mistral` row's replay on) waited for reasoning-replay T12
and landed 2026-10-07.
T9 (live probe) is the user's and stays open; nothing records Mistral as
working. Deviations and choices:

- **Files.** The decoder lives in `internal/model/stream_content.go`
  (`streamContent`, `errMalformedContent`, `errUnshowableContent`); Mistral
  capture and request encoding live in `internal/model/thinking_replay.go`.
  `client.go` changes only the `Delta.Content` type, the piece loop in
  `consumeStream`, the end-of-stream checks, and two lines in `stream`.
- **T1.** Each chunk decodes once into a struct of `json.RawMessage`
  fields, so a skipped chunk's payload is captured raw and never
  interpreted. A list element inside `thinking` that is not an object
  (for example a bare string) is treated as malformed, the same rule as a
  top-level list item; the spec names only top-level items, so this is a
  stricter reading. Items skipped inside `thinking` (`reference`,
  `tool_reference`, unknown or type-less items) are recorded as skipped
  pieces too, so a reply made only of them ends with the
  only-unshowable-content error naming the first such type, as spec
  decision 3 requires (fixed in review; the first version let it end as a
  silent empty answer). The decoder records `thinking` presence
  and the `signature` from M1 onward, because M2 landed in the same branch.
- **T2.** The only-unshowable-content error is
  `model reply held only content Likha cannot show (type "…")`, wrapping
  the `errUnshowableContent` sentinel; tests match it with `errors.Is`.
  Model-recovery has not landed, so there is no retryable classification
  to set.
- **T4 not done.** `OutputSeen` (model-recovery T3) does not exist on main.
  This task landed first, so model-recovery T3 applies the rule (non-empty
  answer and reasoning pieces from chunks set it; skipped chunks do not)
  and adds the `consumeStream` test.
- **T7.** New wire `ReplayThinkingChunk` (appended after
  `ReplayAnthropicBlocks`). **Deviation:** the `mistral` row does not set
  it yet. Review found that turning it on before reasoning-replay T12
  would ship replay without FR-39's rejection hint, so a 400 against the
  replayed shape would repeat on every later turn as a bare error; the
  flag moves to the new T10. Until then no shipped row captures or sends
  Mistral thinking, and tests enable the wire with `setReplayWire`
  (`TestMistralRowKeepsReplayOff` guards the row). Replay state gains
  `Thinking *string` (nil: no thinking chunk; `""`: a chunk without text)
  and `ThinkingSignature string`, filled from thinking chunks alone, so
  `reasoning`/`reasoning_content` field text stays out of the replay copy.
  Following reasoning-replay decision 9, every completed `mistral` reply
  carries `Replay{Source}` even without thinking; "no Mistral replay
  state" therefore means `Thinking == nil`. `consumeStream` takes the
  `replayCapture{wire, source}` value from reasoning-replay T4.
  `Equal` compares the new fields, the context estimate counts the sent
  thinking and signature, and `UnsignedOnly` keeps unsigned thinking but
  drops thinking that carried a signature, like the other signed forms.
- **T8.** `requestMessage.Content` is now a `requestContent` whose
  `MarshalJSON` writes the plain string unless Mistral thinking goes back;
  `TestRequestContentEncodesLikeAString` proves it encodes byte for byte
  like the old string field (HTML escapes, control bytes, U+2028/U+2029
  and invalid UTF-8 included). The replayed message
  holds one leading thinking chunk, then a text chunk only when `Content`
  is non-empty, then `tool_calls` unchanged.
