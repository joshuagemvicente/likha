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

- [ ] **T1. A content type that accepts string or list.** Replace
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
- [ ] **T2. Emit pieces in order in `consumeStream`.** For each piece in
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
- [ ] **T3. Regression guard.** Run every existing `client_test.go`,
      `usage_detail_test.go` and `context_usage_stream_test.go` stream test
      unchanged. Add one byte-for-byte request-body assertion: a
      conversation that contains a reply parsed from chunks is re-sent with
      string assistant content and no thinking (spec point 10).
      *Verify:* `go test ./internal/model/`.
- [ ] **T4. Recovery coordination.** With
      [model-recovery](../model-recovery/tasks.md) T3 (`OutputSeen`):
      - non-empty answer and reasoning pieces from chunks set it;
      - skipped chunks do not.

      Whichever of this task and model-recovery T3 lands second applies
      the rule and adds its test.
      *Verify:* a `consumeStream` test, added by whichever lands second,
      in which a list delta holding only an `image_url` chunk leaves
      `OutputSeen` false and a list delta holding a `thinking` chunk sets
      it.
- [ ] **T5. Compaction and naming read answer text only.** No code change
      is expected: `compaction.go` and `sessionname.go` pass a nil
      reasoning callback and use `Content`.
      *Verify:* an agent test with an httptest server whose compaction
      reply holds a `thinking` chunk and a `text` chunk; the summary equals
      the text alone.
- [ ] **T6. Docs and matrix.**
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

- [ ] **T7. Fill the replay state from thinking chunks.** Set the
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
- [ ] **T8. Wire shape on resend (Mistral case).** With the `mistral` row's
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
