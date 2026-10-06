# Tasks: Native Claude client

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md); sources in [research.md](research.md).
Each milestone leaves `go test ./...` and `go vet ./...` green and can land
on its own. Amend v1-spec FR-38 (and the §2 out-of-scope line and §3
technology row that name the accepted non-chat surfaces) before M1 starts.

Prerequisite: [reasoning-replay](../reasoning-replay/tasks.md) M1 (the
`Message.Replay` / `ReasoningReplay.Blocks` data model, `ReplayWire`, and
the base-URL row lookup in `New`). M1–M3 below can land before it; M4 needs
it.

Until M2 routes the `claude` row, the native client is dormant: the row still
uses chat completions and nothing a user sees changes.

## M1 — Request body (`internal/model/anthropic_request.go`)

- [ ] **T1. Wire kind on the row.** Add `type ProviderWire uint8` with
      `WireChat` (zero value) and `WireAnthropic` in `provider.go`, and
      `Provider.Wire ProviderWire`. Leave the `claude` row on `WireChat`
      until T8. Add `wireFor(base string) ProviderWire`, which returns the
      wire of the `Providers` row whose `BaseURL` equals the normalized base
      (no prefix matching; a custom endpoint is `WireChat`). Share the row
      lookup with reasoning-replay T3 rather than writing a second one.
      *Verify:* table test: claude base → `WireAnthropic` (after T8), any
      other row → `WireChat`, `https://api.anthropic.com/v1/` with trailing
      slash → same as without, unknown host → `WireChat`.
- [ ] **T2. Message mapping.** `buildAnthropicRequest(in anthropicInput)
      ([]byte, error)`, a pure function. Input: model, messages, tools,
      thinking mode, max tokens, effort, replay source. Rules, in order:
      1. **System hoisting.** Every `system` or `developer` message before
         the first `user`, `assistant` or `tool` message becomes one
         `{"type":"text","text":…}` block of top-level `system`, in order.
         Empty contents are skipped.
      2. **Later system/developer messages** become a text block in the user
         turn at their position.
      3. **`user`** → text block in a user turn. Empty or whitespace-only
         content is skipped (applies to every rebuilt text block).
      4. **`tool`** → `{"type":"tool_result","tool_use_id":…,"content":…}`
         in a user turn; empty content omits `content` (documented as
         optional). Missing `ToolCallID` is an error, as today.
      5. **`assistant`** → if the replay rule (T13) accepts its
         `Replay.Blocks`, those bytes verbatim; otherwise a text block when
         `Content != ""`, then one `tool_use` block per call with `input` set
         to the raw `Arguments` bytes. `Arguments` must be a JSON object
         (`{}` when empty); otherwise the existing
         `invalid assistant tool call` error. An assistant message that
         maps to no blocks (empty text, no calls, no accepted replay) is
         left out of the request.
      6. **Merging.** Consecutive blocks for the same role form one message.
         In a user message, every `tool_result` block is moved before the
         message's text blocks, preserving order within each group
         (Anthropic: results first, text after).
      7. **Guards.** Unknown roles are an error (as today). A mapped message
         list that is empty, or whose last message is `assistant`, is an
         error with spec point 4's wording.
      8. **No tools.** When `tools` is empty, an assistant tool call is
         rendered as a text line `[tool call <name> <arguments>]` and a tool
         result as `[tool result <id>]\n<content>`, both merged into the
         surrounding turns, and no replay blocks are used (spec point 12).
      *Verify:* table tests for each rule, including: harness system +
      AGENTS.md developer + compaction summary all hoisted; checkpoint
      developer after tool results lands after the `tool_result` blocks;
      parallel tool results merge into one user message; steer text after
      tool results; a trailing assistant message errors; no-tools rendering;
      a whitespace-only user message and an empty assistant message are
      left out.
- [ ] **T3. Tools.** Each `ToolDefinition` →
      `{"name","description","input_schema"}` with `input_schema` the raw
      `Parameters`. Validate `^[A-Za-z0-9_-]{1,128}$`, a JSON object schema,
      and unique names; the error names the tool. No `strict`,
      `eager_input_streaming`, `cache_control` or `tool_choice`.
      *Verify:* golden body for two tools; invalid name and duplicate name
      errors.
- [ ] **T4. Top-level fields.** Body fields, in this order:
      `model`, `max_tokens`, `system` (omitted when empty), `messages`,
      `tools` (omitted when empty), `thinking` (omitted for mode none),
      `output_config` (only with a non-empty effort and `EffortAnthropic`),
      `cache_control` (only when tools are present: `{"type":"ephemeral"}`),
      `stream: true`. Thinking mode adaptive →
      `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`
      and the request header
      `anthropic-beta: thinking-binding-controls-2026-08-01`. Never add
      `temperature`, `top_p`, `top_k`, `stop_sequences`, `metadata`.
      *Verify:* golden bodies for: adaptive with tools, none without tools,
      effort set, effort empty (no `output_config`), and a scan that no
      sampling or `tool_choice` key ever appears.

## M2 — Streaming (`internal/model/anthropic_stream.go`)

- [ ] **T5. SSE decoder.** `consumeAnthropicStream(ctx, r, onText,
      onReasoning, capture) (Message, usageReport, error)`. Read `event:` and
      `data:` lines with the same scanner limits as `consumeStream`
      (1 MiB line, 16 MiB response). Dispatch on the data's `type`:
      - `message_start`: record `message.usage`.
      - `content_block_start`: open block `index` of type `text`,
        `thinking`, `redacted_thinking` (keep `data`) or `tool_use` (keep
        `id`, `name`). Any other type (for example `fallback`, which only
        server-side fallback emits) marks the response "not replayable" and
        is otherwise ignored. Unknown fields such as `input_transformations`
        on `message_start` are ignored.
      - `content_block_delta`: `text_delta` → text and `onText`;
        `thinking_delta` → block thinking and `onReasoning` (non-empty
        pieces only; before a block's first non-empty piece, a blank line
        goes to `onReasoning` when earlier reasoning was already emitted);
        `signature_delta` → block signature;
        `input_json_delta` → append `partial_json`; `citations_delta` and
        unknown delta types are ignored.
      - `content_block_stop`: close the block. A `thinking` block closed
        without a signature, or a `redacted_thinking` block without `data`,
        marks the response not replayable.
      - `message_delta`: record `delta.stop_reason`, `delta.stop_details`
        and `usage` (cumulative; later fields replace earlier ones).
      - `message_stop`: done.
      - `ping` and unknown event types: ignored (Anthropic versioning
        policy).
      - `error`: return an error carrying `error.type` and
        `error.message` (bounded), e.g.
        `model stream error: overloaded_error: Overloaded`.
      *Verify:* fixtures from the documented streams: text only; thinking +
      signature + text; tool use with empty-string first `partial_json`;
      two interleaved thinking blocks around two tool calls; ping events;
      an unknown event type; a mid-stream error event.
- [ ] **T6. Completion rules.**
      - EOF or cancellation before `message_stop` is an error
        (`model stream ended before message_stop`), never a message.
      - Tool calls are emitted in block order. Accumulated input `""`
        becomes `{}`; input that is not a JSON object fails with
        `incomplete structured tool call at index <n>`.
      - `stop_reason` `refusal` → `*RefusalError{Category, Explanation}`
        per spec point 19, read from `stop_details` (either field may be
        null). Like every error return it yields no message, so partial
        text never reaches history. `pause_turn` (server tools only, which
        Likha never declares) → `unexpected stop reason pause_turn`. Every
        other value, including unknown ones, `max_tokens` and
        `model_context_window_exceeded`, returns the message.
      - `Message.Content` is all text blocks concatenated;
        `Message.Reasoning` is the non-empty thinking blocks' text joined
        with a blank line.
      *Verify:* EOF after a text delta errors; refusal with and without a
      category and explanation (`errors.As` finds `*RefusalError`);
      `max_tokens` with a half-written tool input errors; with complete text
      it returns; `max_tokens` inside a thinking block returns the text and
      marks the response not replayable.
- [ ] **T7. Usage.** Map the final usage into `usageReport`:
      `Prompt = input_tokens + cache_creation_input_tokens +
      cache_read_input_tokens`, `cacheRead`, `cacheWrite`,
      `Completion = output_tokens`, `reasoning =
      output_tokens_details.thinking_tokens` when present. `seen.prompt`
      needs `input_tokens`; `seen.completion` needs `output_tokens`.
      No `cost`. Do not apply `withAdditiveReasoning` (Anthropic's thinking
      count is a documented subset of output).
      *Verify:* the spec's 50/9000/400/300 example; message_start-only
      usage plus a message_delta that adds cache fields; a usage-less
      stream reports `ok=false`.

## M3 — Client, discovery and errors (`internal/model`)

- [ ] **T8. Route the row.** Set `Wire: WireAnthropic` on the `claude` row
      and remove its `EffortNote` (decision 8). `New` sets
      `url = base + "/messages"` for `WireAnthropic`; `Client.stream`
      dispatches to `streamAnthropic` the way it dispatches OAuth clients
      to `streamCodex`. `Fork` accepts `/messages` as the inherited route
      for that wire. `ForkForTask` needs no change (it gates the client's
      `url`).
      *Verify:* fork tests for the new route; a task fork's gate counts a
      `/messages` POST and not a `/models` GET.
- [ ] **T9. Request and HTTP errors.** `streamAnthropic` builds the body
      (T2–T4), sends it with the T4 headers, and classifies non-2xx:
      - parse `{"type":"error","error":{"type","message"},"request_id"}`
        (bounded to 64 KiB) and the `request-id` header;
      - message: `model HTTP <status>: <error.type>: <error.message>
        (request <id>)`; raw body when it does not parse;
      - 401/403 → wrap `ErrUnauthorized`;
      - 400/422 with effort sent → `*EffortRejectedError`;
      - 400 when the body carried replayed thinking blocks → reasoning-replay
        T12's `*ReplayGapError` (wrapped by `EffortRejectedError` when both
        apply, as there);
      - keep the status, `retry-after` and `error.details.error_code` on the
        error value so model-recovery's classifier can read them with
        `errors.As` (until model-recovery T1 lands, a small unexported type;
        afterwards wrap `StreamError` with model-recovery T4's status table
        unchanged, plus: a 429 whose `error.details.error_code` is
        `enforced_spend_limit_reached` → quota; in-stream `overloaded_error`
        → overloaded, `api_error`/`timeout_error` → provider-error,
        `rate_limit_error` → rate-limited; other in-stream types → rejected;
        402 → quota).
      Content type must be `text/event-stream`, as on the chat path.
      *Verify:* httptest table over 400 (with and without effort, with and
      without replayed blocks), 401, 403, 404, 413 with a non-JSON body, 429
      with `retry-after`, 429 spend cap, 500, 503, 529, a 200 with a JSON
      body, and in-stream `overloaded_error` and `invalid_request_error`;
      every error message carries the `request-id` header value; each error matches with `errors.Is`/`errors.As`,
      never by substring.
- [ ] **T10. Model discovery.** `ListModels`, `ListModelsWithDetails`,
      `Check` and `Client.Models` route `WireAnthropic` bases to
      `fetchAnthropicModels`: `GET {base}/models?limit=1000` with the native
      headers, following `has_more` / `last_id` → `after_id` for at most 10
      pages. Decode `id`, `display_name`, `max_input_tokens` (context window,
      source `max_input_tokens`), `max_tokens`, and `capabilities`:
      `thinking.types.adaptive.supported`, and `effort.<level>.supported` for
      `low, medium, high, xhigh, max` in that order (→ `ModelDetails.Efforts`,
      validated with `catalog.ValidateEfforts`; no `DefaultEffort`). 401/403
      → `ErrUnauthorized`; other failures → `ErrUnexpectedResponse`; an empty
      list is an error.
      `Check` stores the decoded per-model output limit and adaptive flag in
      an unexported, mutex-guarded `anthropicModelInfo` map on the client.
      `Fork` shares the pointer.
      *Verify:* two-page fixture; a model with `capabilities: null`; 401;
      `Check` fills the map and a fork sees it.
- [ ] **T11. Thinking mode and output limit.** `anthropicThinking(model)`:
      the client map's adaptive flag when known; else the bundled exact-ID
      table `claudeAdaptiveModels` (`claude-opus-5-5`, `claude-fable-5-1`,
      `claude-fable-5`, `claude-sonnet-5-5`, `claude-opus-4-7`, plus
      `claude-opus-5`, `claude-opus-4-8`, `claude-opus-4-6`,
      `claude-sonnet-5`, `claude-sonnet-4-6` from the same primary table);
      else none. `anthropicMaxTokens(model)`: the map's `max_tokens`, else
      `catalog.Lookup("claude", model).Output`, else 64,000.
      *Verify:* table test over live-known, table-only and unknown models;
      `claude-haiku-4-5` is none in both sources.

## M4 — Replay (needs reasoning-replay M1)

- [ ] **T12. Capture.** Every completed response records replay state
      built with `NewReasoningReplay(c.ReplaySource(), ...)`: the client's
      full request URL (`.../v1/messages`, not `Base()`, so the
      compatibility route on the same base is a different issuer) and the
      requested model ID, not the response's `model` field, as
      reasoning-replay rule 2 requires. When the response is replayable (T5)
      and holds at least one thinking or redacted-thinking block, also
      marshal its content array in block order into `Blocks` —
      `{"type":"text","text"}`, `{"type":"thinking","thinking","signature"}`,
      `{"type":"redacted_thinking","data"}`,
      `{"type":"tool_use","id","name","input":<raw arguments>}`. Failed,
      cancelled, refused and steered responses get no `Replay` at all.
      *Verify:* the captured bytes for the interleaved fixture equal the
      expected array; a text-only response has `Replay.Source` set and empty
      `Blocks`; a thinking block without a signature gives empty `Blocks`.
- [ ] **T13. Send.** In T2 rule 5, use `Replay.Blocks` only when all hold:
      tools are present, `Blocks` is non-empty, the shared
      `(*Client).replayFor` gate returns the message's state (its
      `Replay.Source` equals `c.ReplaySource()`: request URL and model; no
      separate comparison), and the blocks are consistent with the message
      (text blocks concatenate to `Content`; `tool_use` blocks equal
      `ToolCalls` in order by id, name and arguments bytes). Otherwise
      rebuild without thinking.
      *Verify:* same-model replay is byte-identical to the captured array;
      other model, other endpoint, edited `Content`, and no-tools requests
      rebuild without thinking.
- [ ] **T14. Session round trip.** Rely on reasoning-replay's persistence.
      *Verify:* save and resume a session holding Claude replay state, then
      build the next request and compare with the pre-save body.

## M5 — Callers, effort and docs

- [ ] **T15. Effort wire kind.** Add `EffortAnthropic` to `EffortWire`
      (`effort.go`) with a doc comment naming `output_config.effort`. The
      `claude` row stays `EffortUnsupported`; FR-40's probe flips it.
      `EffortUnavailableReason` then reads "not yet verified for Anthropic
      Claude". Update model-effort's spec decision 5, functional point 6,
      out-of-scope "Claude effort" bullet and acceptance line on Claude, the
      `claude` row of its context.md provider matrix, and FR-37's Claude
      clause, in the same commit (they cite the removed note).
      *Verify:* a client set to `EffortAnthropic` + `high` sends
      `output_config`; `EffortUnsupported` + `high` sends none; a 400 with
      effort returns `EffortRejectedError`.
- [ ] **T16. Background callers.** No agent change. Confirm with tests that
      compaction and session naming on a Claude client send no tools, no
      `cache_control`, no `output_config` and no thinking blocks, and that
      an explore child's requests replay its own blocks only.
      *Verify:* agent tests against the T9 test server.
- [ ] **T17. Docs.** `docs/providers.md`: the claude row uses the native
      Messages API, `x-api-key`, pending-probe. `CHANGELOG.md` Unreleased
      entry stating automated checks only. Mark claude-provider's decisions 1,
      3 and 5 and the Bearer `/v1/models` question superseded by this spec.
      *Verify:* `grep -n "OpenAI-compatible endpoint ignores effort"` over
      `specs/`, `docs/` and `internal/` finds only history (CHANGELOG,
      superseded notes); `docs/providers.md` and README still say
      pending-probe for `claude`.

## M6 — Live probe (run by the user)

- [ ] **T18. Admission probe.** The user runs the probe list in
      [context.md](context.md#live-probe) with their own key and records the
      outcome there. Agents prepare the steps and the record format only and
      never mark the provider verified.
      *Verify:* every row of the live-probe table has a date and outcome
      written by the user; only then may a separate change move `claude`
      out of pending-probe.
