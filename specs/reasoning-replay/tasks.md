# Tasks: Reasoning replay

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md); sources in [research.md](research.md).
Each milestone leaves `go test ./...` and `go vet ./...` green and can land
on its own. M1 and the core of M3 (T9, T10) are the shared foundation that
[claude-native](../claude-native/tasks.md) and
[chunked-content](../chunked-content/tasks.md) M2 also build on. Amend v1-spec
FR-39, FR-15 and the closing sentence of FR-37 before M1 starts.

**Foundation landed (2026-10-06, local).** Coordinator decisions that refine
the tasks below:

- `ReplaySource.Endpoint` is the issuing client's full request URL
  (`Client.url`, e.g. `https://api.openai.com/v1/chat/completions`), not
  `Client.Base()`: `openai` and `chatgpt` share a base but not a route, and
  native Claude posts to `/v1/messages` on the compatibility route's base.
  `ReplaySource()` returns `{url, model}`.
- `NewReasoningReplay` validates `Details` and `Blocks` and stores them as
  `json.Marshal` writes them (compact, HTML-escaped), so a copy read back
  from a session compares `Equal` and a re-save is not conversation
  activity. Empty or `null` means absent.
- Every shipped row stays `ReplayNone` until the per-provider feature work
  enables `deepseek` and `gemini` together with their capture and encoding.
- Explore task records strip `Replay` and `ThoughtSignature`
  (`explore.cloneMessages`): task transcripts are display and resume
  records, never request history. Children still keep their own state in
  their live loop history. This replaces T14's capacity accounting and
  spec rule 11's "saved task transcripts keep it".
- Agent loops drop signed replay state wherever they change an assistant
  message: a dropped malformed tool-call batch (`dropToolCalls`) or a child
  reply clipped to 64 KiB (`dropClippedReplay`). Both keep only
  `Replay.UnsignedOnly()` (Source plus `ReasoningContent`, or nil when no
  unsigned text exists) and clear tool-call signatures. Unsigned text stays
  because DeepSeek rejects later tool requests whose earlier turns lack it
  and a changed turn's plain text is still text (spec decision 6); signed
  `Details`, `Blocks` and signatures describe the exact turn the issuer
  returned. Replay text itself is never clipped.
- Loop-level tests used an unexported `replyHook` seam to stand in for
  capture until T5–T7 landed. It is gone: the loops' replay tests now run
  through real capture and encoding (see the implementation notes).

## M1 — Data model (`internal/model`)

- [x] **T1. Replay types.** New file `internal/model/replay.go`:

      ```go
      // ReplaySource identifies the endpoint and model that issued replay state.
      type ReplaySource struct {
          Endpoint string // request URL of the issuing client (not Base())
          Model    string // Client.Model() of the issuing client
      }

      // ReasoningReplay is provider-issued reasoning state that goes back
      // unchanged, and only to a matching issuer. It is never displayed.
      // A non-nil value with only Source set means the issuer sent nothing.
      type ReasoningReplay struct {
          Source           ReplaySource
          ReasoningContent *string         `json:",omitempty"` // chat reasoning_content (DeepSeek); nil = field absent
          Details          json.RawMessage `json:",omitempty"` // OpenRouter reasoning_details array, assembled
          Blocks           json.RawMessage `json:",omitempty"` // Anthropic content blocks, in order
      }
      ```

      `ReasoningContent` is a pointer so "the provider sent an empty
      string" (send `""` back) differs from "the provider sent no field"
      (send nothing). Later forms (chunked-content M2 for Mistral) add
      their own field here.
      Add `Replay *ReasoningReplay `json:",omitempty"`` to `Message` and
      `ThoughtSignature string `json:",omitempty"`` to `ToolCall`. `ToolCall`
      stays all-string, so `==` in `sameConversation` and `slices.Equal` in
      `sameTaskRecord` still compile and compare the signature. Update the
      `Message.Reasoning` comment: display only, never sent; replay state
      lives in `Replay`.
      Invariants, documented on the types:
      - A captured `ThoughtSignature` implies a non-nil `Replay` with
        `Source` set. A signature on a message without a matching `Replay`
        is never sent.
      - `Replay` is immutable once captured. History copies share the
        pointer, so nothing mutates it.
      - `Blocks` holds the assistant turn's full content array as returned
        (thinking, redacted_thinking, text, tool_use, in order), so
        interleaved thinking keeps its position. claude-native owns how it
        is produced and sent.
      *Verify:* JSON round trip of a message with every field, including
      `ReasoningContent` set to `""`. A snapshot JSON saved before this
      change decodes with `Replay == nil`. A message without replay marshals
      byte-identically to the pre-change encoding (golden).
- [x] **T2. Provider replay wire.** *(`deepseek` and `gemini` enabled
      with their capture and encoding.)* In `replay.go` add `type ReplayWire uint8`
      with `ReplayNone` (zero value), `ReplayReasoningContent`,
      `ReplayThoughtSignature`, `ReplayReasoningDetails` and
      `ReplayAnthropicBlocks` (set only by the claude-native client). Add a
      method `(ReplayWire) modelBound() bool`: false for
      `ReplayReasoningContent`, true for the others (spec decision 2). Add
      `Provider.Replay ReplayWire` in `provider.go`. Set `deepseek`:
      `ReplayReasoningContent` and `gemini`: `ReplayThoughtSignature`. Every
      other row, `openrouter` included, stays `ReplayNone` (spec decision 4).
      *Verify:* table test over `Providers`: `deepseek` and `gemini` carry
      their wire, every other row is `ReplayNone`, and rows that share a
      `BaseURL` (`openai` and `chatgpt`) share a `Replay` value, so the
      lookup in T3 cannot depend on row order.
- [x] **T3. Client state.** *(`NewOAuth` resolves it too.)* Add `replayWire ReplayWire` to `Client`. `New`
      resolves it from the `Providers` row whose `BaseURL` equals the
      parsed base, so every construction site (app, setup, `/models`,
      sign-in) gets it without new wiring. Custom endpoints get
      `ReplayNone`. `Fork` and `ForkForTask` copy it; `WithoutEffort`
      inherits it through `Fork`. Add `ReplaySource() ReplaySource`
      returning `{c.url, c.Model()}` (request URL, see the note above), and an unexported
      `setReplayWire` for tests (OpenRouter fixtures, T7). `SetModel` needs
      no change, because the source is read per request.
      *Verify:* table test: each known base gives its row's wire, a custom
      base gives none; fork, task-fork and without-effort tests assert
      inheritance.

## M2 — Capture (`consumeStream`)

- [x] **T4. Pass the wire into the decoder.** `consumeStream` takes a small
      `replayCapture{wire ReplayWire; source ReplaySource}` value. With
      `ReplayNone` it behaves exactly as today. With any other wire, a
      successful result always carries `Replay{Source: source}`, filled by
      T5–T7 when the stream carried state (spec decision 9). The
      OAuth/Responses path is untouched.
      *Verify:* the existing `consumeStream` tests pass unchanged with
      `ReplayNone`; a stream without reasoning on a `ReplayThoughtSignature`
      capture returns `Replay` with only `Source` set.
- [x] **T5. DeepSeek.** With `ReplayReasoningContent`, concatenate every
      `delta.reasoning_content` piece separately from the display builder.
      The display builder keeps its current `reasoning`-first rule. If any
      chunk carried the field (even empty), set `ReasoningContent` to the
      concatenation.
      *Verify:* a stream fixture with `reasoning_content` and tool calls
      gives the exact text; a fixture where the field is present but empty
      gives a pointer to `""`; a fixture with only `delta.reasoning` gives
      `ReasoningContent == nil`.
- [x] **T6. Gemini.** With `ReplayThoughtSignature`, add
      `ExtraContent *struct{ Google *struct{ ThoughtSignature string
      `json:"thought_signature"` } `json:"google"` } `json:"extra_content"``
      to the tool-call fragment in `streamChunk`. Store it on
      `streamedToolCall`: the last non-empty value per call index wins, so
      a gateway that repeats it does not double it. Copy it into
      `ToolCall.ThoughtSignature`.
      *Verify:* fixtures for a single call with a signature, two parallel
      calls where only the first has one, and a signature arriving on a
      later fragment of the same index than the call's name.
- [x] **T7. OpenRouter.** With `ReplayReasoningDetails`, decode
      `delta.reasoning_details` as `[]json.RawMessage` and keep the
      elements in arrival order. At `[DONE]`, assemble streamed fragments
      into complete objects: consecutive elements with the same `type` and
      `index` (and no conflicting non-empty `id`) form one object; their
      `text`, `summary` and `data` strings are concatenated in order; every
      other field (`signature`, `id`, `format`, unknown fields) takes the
      last non-empty value. Elements without `index` are kept as they are.
      Re-encode as one JSON array into `Replay.Details`; the result is
      valid JSON by construction. Whether `index` must be stripped before
      replay, and whether `data` is split or repeated across fragments, are
      **unverified** (secondary reports disagree); the probe in T18 settles
      both before the row ships enabled (research.md).
      *Verify:* with the wire set through `setReplayWire`, a fixture with
      three `reasoning.text` fragments of index 0 (the last carrying only
      `signature`) and one `reasoning.encrypted` of index 1 gives two
      objects with the exact concatenated text, the signature and the
      data; unknown fields survive; the shipped `openrouter` client
      captures nothing.
- [x] **T8. Failure paths.** Every error return from `consumeStream` still
      returns `Message{}`, so no replay state survives a failed, cancelled
      or retried (FR-36) attempt.
      *Verify:* fixtures that end before `[DONE]`, report a stream error, or
      are cancelled mid-stream return `Replay == nil` on each wire.

## M3 — Replay (`Client.stream` request building)

- [x] **T9. Request fields.** Extend `requestMessage` with
      `ReasoningContent *string `json:"reasoning_content,omitempty"`` and
      `ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"``,
      and `requestToolCall` with `ExtraContent *requestExtraContent
      `json:"extra_content,omitempty"``. The pointer keeps an empty
      `reasoning_content` sendable. All three stay unset unless T10 fills
      them, so bodies without replay are unchanged.
      *Verify:* golden body test: a history with no replay encodes
      byte-identically to the pre-change body.
- [x] **T10. Send gate.** Add `(*Client).replayFor(m Message)
      *ReasoningReplay`: it returns `m.Replay` only when
      `c.replayWire != ReplayNone`, `m.Role == "assistant"`, `m.Replay !=
      nil`, the endpoint matches, and, when `c.replayWire.modelBound()`,
      the model matches too. In the message loop, fill exactly the field of
      `c.replayWire` from it; for `ReplayThoughtSignature`, set
      `extra_content` only on calls with a non-empty signature.
      `Message.Reasoning` is never read here. claude-native's builder uses
      the same gate.
      *Verify:* body tests per wire: matching issuer sends; other endpoint
      and `ReplayNone` send nothing; other model sends nothing for
      signatures and details, and still sends DeepSeek text. A golden test
      with a history full of foreign replay state on a `ReplayNone` provider
      is byte-identical to the pre-feature body.
- [x] **T11. Gemini skip value.** With `ReplayThoughtSignature`, find the
      last `user` message. For each later assistant message with tool calls
      whose `replayFor` is nil (another model or endpoint issued it, or an
      older session wrote it), send `skip_thought_signature_validator` on
      its first call only. Messages the current model issued are sent as
      received, signed or not. Earlier messages are left alone.
      *Verify:* a current-turn call from another model gets the skip value;
      a previous-turn one does not; an own signed call is unchanged; an own
      unsigned call (Gemini 2.5) has no `extra_content`.
- [x] **T12. Replay error.** While building, count `Missing`: with
      `ReplayReasoningContent` and tools present, assistant messages whose
      `replayFor` is nil; with `ReplayThoughtSignature`, messages that got
      the skip value; zero for every other wire. Count `Sent`: assistant
      messages that carried replay fields. On HTTP 400 with either count
      above zero, return `*ReplayRejectedError{Missing, Sent int; Err
      error}` (`errors.As`, no string matching). When the request also
      carried effort, `EffortRejectedError` wraps it, so `errors.As` finds
      both and both hints show; model-recovery's `StreamError` (planned)
      wraps it the same way once it lands. The `stream_options` retry runs
      first, unchanged. The TUI appends the spec's `/compact` hint next to
      `effortRejectedNote`.
      *Verify:* httptest 400 with a foreign DeepSeek turn gives
      `Missing > 0`; a 400 with only own replay gives `Sent > 0`; a 400 on a
      `ReplayNone` client or with neither count gives no
      `ReplayRejectedError`; a 400 with effort and replay matches both
      types; the TUI test shows the hint once.

## M4 — Agent, sessions and estimate

- [x] **T13. Steer now (DeepSeek).** Add
      `(*Client).PartialAssistant(text, reasoning string) Message` to
      `model`. With `ReplayReasoningContent` it sets
      `Replay{Source, ReasoningContent: &reasoning}`; with any other wire it
      returns text only. `turn_execution.go` accumulates the streamed
      reasoning next to `partial` and, only when `partial` is non-empty (as
      today), appends the message `PartialAssistant` builds. `agent` still
      imports only `model`.
      *Verify:* a steer-now test on a DeepSeek-wire client keeps the partial
      reasoning as replay; on a Gemini-wire client it keeps none; a stop
      before any text keeps no message on either.
- [x] **T14. Sessions.** *(Done with the coordinator change above: task
      records strip replay, so no capacity charge is needed; `Equal` is the
      by-value comparison.)* `sameConversation` (`session.go`) and
      `sameTaskRecord` (`task_records.go`) compare `Replay` by value
      (`Source`, `ReasoningContent` by value, `bytes.Equal` for `Details`
      and `Blocks`). Task-record capacity accounting charges replay bytes
      and signatures. The tool-result validator rejects a tool message
      carrying `Replay`, as it already does for `Reasoning`. No schema
      version bump: the snapshot is JSON in an existing column. DeepSeek
      reasoning is stored twice (display `Reasoning` and replay text); this
      is accepted for now, because they differ when a provider sends both
      `reasoning` and `reasoning_content`.
      *Verify:* save/resume round trip keeps replay; a snapshot differing
      only in replay counts as changed; a legacy snapshot fixture loads; a
      task record over capacity because of replay bytes fails with
      `ErrTaskCapacity`.
- [x] **T15. Compaction and children.**
      `CompactHistoryUsage` builds a fresh developer message, and explore
      and profile loops append the fork's own `assistant` values.
      `exploreLoopMessages` copies `ToolCalls` and shares `Replay` by
      pointer (immutable, T1). Display clipping in the explore and profile
      loops never shortens the replay text; when it clips `Content`, the
      loop keeps only the unsigned part (`dropClippedReplay`), because
      signed state no longer matches the turn.
      *Verify:* compaction output has `Replay == nil`; an explore child's
      second request body carries its first response's state and none of
      the parent's; a profile task the same; a clipped child turn resends
      its unsigned text and no signed state.
- [x] **T16. Context estimate.** *(Skip values counted since T11; Anthropic
      blocks are left to claude-native, whose blocks replace content.)* Add `(*Client).EstimateInputTokens(messages,
      tools)`, which counts the package-level estimate plus the replay text
      `replayFor` would send (`ReasoningContent`, `Details`, signatures,
      skip values). Switch `turn_execution.go` to it, and `tui.go`'s
      `recalculateContext` when a client exists. The free function stays
      for callers without a client.
      *Verify:* the estimate rises by the replay text for a matching issuer
      and is unchanged for a foreign one.

## M5 — Evidence

- [x] **T17. Docs and record.** Amend the `Message.Reasoning` comment, the
      `Stream` doc comment ("Reasoning is never sent back") and
      `EstimateInputTokens`' comment to the FR-15 wording. Add a
      `docs/providers.md` note for DeepSeek and Gemini, and for OpenRouter
      say replay is off until its probe. Add a CHANGELOG Unreleased entry
      stating which checks ran.
      *Verify:* `grep -rn "never sent back" internal/` finds only wording
      consistent with FR-15 as amended; the CHANGELOG entry names the
      automated checks and says the live probes are pending.
- [ ] **T18. Live probes (user).** The user runs a 3-round tool loop on
      `deepseek` (`deepseek-flash`, then a switch to `deepseek-v4-pro`),
      `gemini` (a Gemini 3 model, and `gemini-2.5-flash` as the regression
      check), and a switch from another provider to DeepSeek mid-session.
      For OpenRouter, the user runs the same loop on a local build with the
      row set to `ReplayReasoningDetails`, on one Anthropic and one Gemini
      model, and checks whether `index` is accepted on replay. Outcome,
      date and model go in [context.md](context.md#provider-matrix). The
      OpenRouter row ships enabled only in a separate change that cites a
      passing probe. Agents never mark a row verified.
      *Verify:* each matrix row names a date, model and outcome from the
      user's run; rows without one stay "pending (user)".

## Implementation notes (2026-10-06, local)

Capture lives in `internal/model/replay_stream.go`, request encoding,
`ReplayRejectedError` and `PartialAssistant` in `replay_request.go`;
`client.go` only threads them through `consumeStream` and `Client.stream`.
Deviations and refinements:

- **T6/T7 decoding.** `extra_content` and `reasoning_details` are decoded as
  raw JSON and parsed only under their own wire, instead of a typed struct on
  every fragment, so a provider without replay can never fail a stream on
  their shape (`TestStreamWithoutReplayCapturesNothing`). On its own wire a
  malformed shape fails the stream like any malformed event.
- **T7 assembly.** Assembled objects are re-encoded from a map, so their keys
  come out in sorted order; values are unchanged. A streamed `text`,
  `summary` or `data` piece that is not a string fails the stream. The rule
  stays unverified until T18 (the row ships `ReplayNone`).
- **T10/T11.** A matching issuer's own signatures go back on every turn;
  only the skip value is limited to the current turn. A clipped child turn
  on Gemini loses its signature (`dropClippedReplay`) and so gets the skip
  value on its first call, because the call is in the child's current turn.
- **T12.** `ReplayRejectedError` applies to HTTP 400 only, as the spec says;
  effort keeps its 400/422 rule and wraps it. Skip-valued messages count as
  `Missing`, not `Sent`.
- **T13.** The agent keeps the reasoning pieces its `onReasoning` callback
  saw. That callback prefers `reasoning` over `reasoning_content` when a
  chunk carries both; DeepSeek sends only `reasoning_content`, so they match.
- **Tests without a seam.** `replyHook` is removed. Agent tests point a
  shipped provider row's `BaseURL` at an `httptest` server for the test's
  duration (as `tui` tests already do), so `model.New` resolves the row's
  real wire and every loop runs through real capture and encoding:
  main-loop DeepSeek replay, the malformed batch on DeepSeek and Gemini,
  clipped and unclipped explore and profile children on both, a parent and
  child that never share state, Steer now on both, and `/compact`.
- **Checklist.** `checklist.md` items describe live outcomes; they stay
  unticked until the user's probes (T18) run.
