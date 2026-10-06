# Context: Reasoning replay

Code, tests and docs this feature touches, with the facts that shaped the
spec. Decisions live in [spec.md](spec.md); sources in
[research.md](research.md).

## Wire paths

- Every API-key provider, including `claude` (`https://api.anthropic.com/v1`),
  `gemini` (`…/v1beta/openai`), `deepseek` and `openrouter`, goes through
  OpenAI-compatible `/chat/completions` in `internal/model/client.go`. Only
  `chatgpt` uses the Responses API (`codex.go`, `responses_stream.go`). That
  path is out of scope.
- `internal/model/client.go`
  - `Message{Role, Content, Reasoning, ToolCalls, ToolCallID}` and
    `ToolCall{ID, Name, Arguments}` have no JSON tags. Sessions persist them
    with plain `encoding/json`, so new fields need `json:",omitempty"` to
    keep old encodings byte-identical.
  - `requestMessage` / `requestToolCall` are the wire structs. `Content` is
    always sent (even empty). There is no reasoning field, so display
    reasoning is never sent today.
  - `Client.stream` builds the body. The message loop is the single place
    where replay fields are added (T9–T11). The HTTP 400 path that wraps
    `EffortRejectedError` is where `ReplayRejectedError` joins (T12). The
    `stream_options` retry on a 400 runs before it and is unchanged.
  - `streamChunk.Delta` already decodes `reasoning` and `reasoning_content`
    for display: `reasoning` wins when both are present. Tool-call fragments
    decode `index`, `id`, `type`, `function`; `extra_content` and
    `reasoning_details` are dropped today.
  - `consumeStream` accumulates text, display reasoning and tool calls, and
    returns `Message{}` on every error, so a failed or retried attempt can
    never leave replay state behind.
  - `Client` holds `url` (the request URL, which `ReplaySource().Endpoint`
    records), `base` (endpoint base) and `model`. `SetModel` mutates
    `model` in place for `/models` switches on the same provider. A provider
    switch builds a new client (`tui/models_all.go`, `model.New`). `New`
    receives only the endpoint, not the provider row, hence T3's lookup by
    base URL. `openai` and `chatgpt` share `https://api.openai.com/v1`, so
    rows that share a base must share a replay wire (T2). The issuer match
    uses the request URL instead, because those two rows (and native versus
    compatibility Claude) share a base but not a route.
- `internal/model/fork.go`: `Fork` copies endpoint, model, key, session and
  effort fields; `ForkForTask` wraps `Fork`. Both must copy `replayWire`.
  `WithoutEffort` (compaction, naming) is a fork too; `WithoutReplay`
  forks with `ReplayNone` for the same background requests.
- `internal/model/context_estimate.go`: `EstimateInputTokens` excludes
  `Reasoning` "because it is not sent back". Replay text is sent, so T16
  counts it. Callers: `agent/turn_execution.go`, `tui/tui.go`.
- `internal/model/provider.go`: the `Providers` table. A per-row
  `Replay ReplayWire` joins `Effort EffortWire`.

## Agent

- `internal/agent/turn_execution.go`
  - The main loop appends `assistant` (as returned by `StreamUsage`) to
    history, so captured replay flows into history without changes.
  - Steer now (specs/steering-prompts M2) keeps
    `Message{Role: "assistant", Content: partial}` only when `partial` is
    non-empty, and drops partial reasoning. T13 adds DeepSeek's unsigned
    reasoning to that kept message.
  - `validateToolCallIDs` failure clears `ToolCalls` but keeps the message;
    `Replay` may stay and is harmless (its signatures are on the dropped
    calls).
- `internal/agent/compaction.go`: the replacement is a fresh `developer`
  message, so replay is dropped by construction. The summarize request sends
  the full history, with tools nil, through `backgroundClient`:
  `WithoutEffort()` then `WithoutReplay()` (a fork with `ReplayNone`), so it
  carries no replay state (decision 8 as amended 2026-10-07). DeepSeek
  ignores `reasoning_content` without tools, and the appended user message
  starts a new Gemini turn, so `/compact` succeeds whether earlier turns
  lack state or carry state the provider rejected. That is why the error
  hint names it. A 400 on the summarize request is a plain provider error.
- `internal/agent/sessionname.go`: the same shape as compaction, through
  the same `backgroundClient`.
- `internal/agent/explore_loop.go`, `profile_runner.go`: children start from
  their brief, not the parent's history, and append their fork's
  `assistant`. They clip `Content` and `Reasoning` to 64 KiB for display;
  the replay text must not be clipped. A clipped turn keeps only its
  unsigned replay text (`dropClippedReplay`), since signed state no longer
  matches it. `exploreLoopMessages` copies `ToolCalls` but
  shares other fields.

## Sessions

- `internal/session/session.go`: `Snapshot.History []model.Message` is JSON
  in the snapshot column (`schemaVersion = 1`); new optional fields need no
  migration. `sameConversation` compares fields one by one and `ToolCall`
  with `==`. It must learn `Replay`.
- `internal/session/task_records.go`: explore task records persist child
  histories with a 32 MiB capacity charge per record, a field-by-field
  `sameTaskRecord` (`slices.Equal` on `ToolCalls`), and a validator that
  rejects tool messages carrying `Reasoning`. All three must learn
  `Replay` / `ThoughtSignature`.

## TUI

- `internal/tui/effort.go` `effortRejectedNote` renders the
  `EffortRejectedError` hint with `errors.As`. The `ReplayRejectedError`
  hint follows the same pattern.
- `internal/tui/agent_detail.go` shows child `Reasoning`; replay is never
  rendered.
- `internal/tui/tui.go` `applyModel` (`SetModel`) and `models_all.go`
  (`model.New`) are the switch points. Neither needs replay changes because
  the source check runs per request. `recalculateContext` in `tui.go` calls
  the free `EstimateInputTokens` (T16).

## Related specs

- [claude-native](../claude-native/spec.md): the native Messages client
  produces and sends `Replay.Blocks` under `ReplayAnthropicBlocks`.
- [model-effort](../model-effort/spec.md): the per-row wire-kind pattern
  (`EffortWire`) that `ReplayWire` copies. No change to effort.
- [model-recovery](../model-recovery/spec.md) (planned, not implemented):
  FR-36 retries discard partial attempts; `ReplayRejectedError` is a
  rejected (non-retryable) kind, and its planned `StreamError` wraps it.
- [chunked-content](../chunked-content/spec.md): M2 adds Mistral's replay
  form (a new `ReplayWire` kind and field) to this data model.
- [steering-prompts](../steering-prompts/spec.md): Steer now keeps partial
  text; T13 extends it on DeepSeek.
- [conversation-compaction](../conversation-compaction/spec.md): `/compact`
  is the hint's way out.

## Provider matrix

| Provider | Replay wire (shipped) | Match | Requirement (primary source) | Live probe |
| --- | --- | --- | --- | --- |
| `deepseek` | `reasoning_content` | endpoint | required with tools, all earlier turns; 400 without | pending (user) |
| `gemini` | tool-call `thought_signature` | endpoint + model | required for current-turn calls on Gemini 3; not enforced on 2.5 | pending (user) |
| `openrouter` | none until probed (`reasoning_details` built) | endpoint + model | pass back unmodified; no error documented without it | pending (user, local build) |
| `claude` (native, claude-native) | content blocks | endpoint + model | required with tool results; thinking disabled without | pending (user) |
| `claude` (compat endpoint) | none | — | thinking not returned | n/a |
| `chatgpt` | none | — | encrypted reasoning recommended, not required | n/a |
| `xai` | none | — | encrypted reasoning on Responses only, optional | n/a |
| `groq`, `cerebras` | none | — | `reasoning` field; no replay requirement documented | n/a |
| `mistral` | one leading `thinking` content chunk (chunked-content T10, 2026-10-07); none on compaction or naming | endpoint + model | thinking chunks recommended; no error documented | pending (user, chunked-content T9) |
| `together`, `bedrock`, `dialagram`, `opencode-zen`, `opencode-go` | none | — | **unverified**; hosted DeepSeek/Kimi/GLM models may need `reasoning_content` | candidate |
