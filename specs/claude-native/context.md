# Context: Native Claude client

Code, tests and docs this feature touches, with the facts that shaped the
spec. Decisions live in [spec.md](spec.md); sources in
[research.md](research.md).

## Wire paths today

- Every API-key provider, including `claude`
  (`https://api.anthropic.com/v1`), goes through OpenAI-compatible
  `/chat/completions`. Only `chatgpt` uses another wire (Responses API).
- `internal/model/client.go`
  - `Message` {Role, Content, Reasoning (display only), ToolCalls,
    ToolCallID}; `ToolCall` {ID, Name, Arguments}; `ToolDefinition`
    {Name, Description, Parameters}.
  - `New(endpoint, name, apiKey)` sets `url = base + "/chat/completions"`.
    It does not know the provider; the native wire is chosen from the base
    URL (decision 1), the same lookup reasoning-replay T3 adds.
  - `Client.stream` dispatches OAuth clients to `streamCodex` before
    building the chat body. `streamAnthropic` is dispatched the same way.
  - The chat body sends `Authorization: Bearer`. The native wire sends
    `x-api-key` instead.
  - `consumeStream` requires `[DONE]`; the native decoder requires
    `message_stop`.
  - `fetchModelList`, `ListModels`, `ListModelsWithDetails`, `Check` and
    `decodeModelDetails` handle `/models` with Bearer auth and OpenAI or
    bare-array envelopes. Anthropic's list is a `data` envelope with
    `has_more`/`last_id` pagination and `x-api-key` auth.
  - `ErrUnauthorized`, `ErrUnreachable`, `ErrUnexpectedResponse`: reused.
- `internal/model/codex.go`, `responses_stream.go`: the other non-chat wire
  and the pattern to follow — a pure request builder
  (`BuildCodexRequest`), a separate stream decoder
  (`consumeCodexStreamDetailed`), and a dispatch in `Client.stream`.
- `internal/model/client_models.go`: `Client.Models` (OAuth branch for
  ChatGPT). Add a `WireAnthropic` branch.
- `internal/model/fork.go`: `Fork` rejects an inherited `url` that is not
  `base + "/chat/completions"` (or `/responses` for OAuth). Accept
  `/messages` for the native wire. `ForkForTask` gates POSTs to `child.url`,
  so task budgets count native requests without change.
- `internal/model/usage_report.go`: `usageReport`, `usageDetail`
  (`cacheRead`, `cacheWrite` are subsets of prompt), `withAdditiveReasoning`
  (xAI only; not for Anthropic). `RequestUsage` in `pricing.go` prices
  cache reads and writes at their own catalog rates.
- `internal/model/effort.go`: `EffortWire`, `EffortLevels`,
  `EffortUnavailableReason`, `EffortRejectedError`, `effortRejectedStatus`
  (400/422), `liveEfforts` (OpenRouter's shape; the Anthropic list needs its
  own decoder).
- `internal/model/provider.go`
  - `claude` row: `DefaultModel: "claude-opus-5-5"`,
    `KeyEnv: "LIKHA_CLAUDE_API_KEY"`,
    `EffortNote: "Anthropic's OpenAI-compatible endpoint ignores effort"`.
    The note is removed in T8.
  - `ClaudeModels` (`claude-opus-5-5`, `claude-fable-5-1`,
    `claude-fable-5`, `claude-sonnet-5-5`, `claude-haiku-4-5`,
    `claude-opus-4-7`) is not referenced outside `provider.go`. The
    exact-ID thinking table (T11) is separate and keyed by exact IDs.
- `internal/model/catalog/models.json`, `claude` provider: e.g.
  `claude-opus-5-5` context 1,000,000, output 128,000, cost input 4,
  output 20, cache_read 0.2, cache_write 5, efforts low…max. `Output`
  is the `max_tokens` fallback (decision 9).

## Construction sites (no change needed with decision 1)

- `internal/app/run.go`: `model.New(chosen, modelName, key)` after
  `providers.ResolveProvider`; `model.ListModels` in `resolveModel`.
- `internal/tui/setup.go`: `model.ListModelsWithDetails(ctx, p.BaseURL,
  key)` for the setup model list; `model.New(p.BaseURL, modelID, key)`.
- `internal/tui/providers.go`: `model.ListModels(ctx, p.BaseURL, key)` for
  the connection check in `/providers`.
- `internal/tui/models_all.go`: `model.New(row.provider.BaseURL, …)` in
  `activateModelClient`.

## Agent message shapes (what the mapping must handle)

- `internal/agent/harness.go` `HarnessMessages`: `system` (harness + run
  identity), then optional `developer` (root AGENTS.md snapshot). Rebuilt
  per run; plan, init and install modes and the skill advert append to the
  `system` content (`turn_execution.go` lines 103–131). Between runs the
  system prompt can therefore change — the reason for decision 7.
- `internal/agent/turn_execution.go`
  - `request := harness + history (+ continuation)` each round.
  - The tool-round checkpoint (`round%32 == 0`, `continuation`) is a
    `developer` message appended at the end of every later request of the
    run and never added to history. On the native wire it becomes text
    after the latest tool results. Each round it moves one turn later, so
    the user turn it left changes and every thinking block produced after
    it fails Anthropic's prefix check on Fable 5.1, Opus 5.5 and Sonnet 5.5.
    With decision 7 they are dropped: from the first request after a
    checkpoint, each request loses the previous rounds' reasoning, including
    the within-tool-loop thinking Anthropic calls required. The request
    still succeeds. Anthropic's documented fix is to keep the reminder in
    history after the tool results and never delete earlier copies, which
    is an agent change (spec.md out of scope; foundation follow-up).
  - Steer prompts are appended as `user` messages after tool results.
  - Steer now keeps partial visible text as an `assistant` message without
    tool calls, then the steer `user` message follows.
- `internal/agent/compaction.go`: summary request = history + `user`
  instruction, `tools == nil`, via `client.WithoutEffort()`. The result
  replaces history with one `developer` message (hoisted to system).
- `internal/agent/sessionname.go`: `StreamUsage(ctx, msgs, nil, nil, nil)`.
- `internal/agent/explore_profile.go`, `profile_prompt.go`: children get a
  `system` plus root `developer` messages; forks of the parent client.
- `internal/agent/explore_loop.go`, `profile_runner.go`: a child clips the
  assistant's `Content` to 64 KiB (`exploreStreamBytes`), replaces invalid
  UTF-8, and drops all `ToolCalls` when their IDs are malformed. Any of
  these makes the message differ from its replay blocks, which is why T13
  checks consistency before sending blocks.
- `internal/agent/turn_execution.go` Steer now keeps only the streamed text
  as a new `assistant` message (no tool calls, no replay), so it is always
  rebuilt as plain text.
- `internal/session/session.go`: messages persist as plain JSON;
  `sameConversation` compares `ToolCall` values with `==`.

## Specs this feature touches

- [v1-spec.md](../v1-spec.md)
  - FR-38 (new): this feature.
  - §2 out-of-scope "protocol adapters beyond OpenAI chat-completions and
    the accepted list's documented exceptions (the Sign in with ChatGPT
    Responses surface)" and the §3 "Model integration" row must name the
    Anthropic Messages surface as a documented exception.
  - FR-15 (amended by FR-39): reasoning display and replay.
  - FR-36: transient errors; the native wire must classify into the same
    kinds.
  - FR-37: its Claude clause cites the compatibility endpoint.
- [reasoning-replay](../reasoning-replay/spec.md): owns `Message.Replay`
  (`ReasoningReplay.Blocks`), `ReplayWire` (`ReplayAnthropicBlocks`), the
  same-endpoint-and-model rule, persistence and the Steer-now rule.
  Claude-specific deviation: decision 6 (no replay on tool-less requests).
- [model-effort](../model-effort/spec.md): `EffortAnthropic` joins the wire
  kinds; decision 5, point 6, the out-of-scope "Claude effort" bullet and
  the provider matrix row for `claude` change (T15).
- [model-recovery](../model-recovery/spec.md): "both wire paths" becomes
  three; its T1 `StreamError` kinds must cover the native errors (T9).
- [claude-provider](../claude-provider/spec.md): decisions 1 ("no client
  changes"), 3, and 5 (native wire and caching out of scope) and the Bearer
  `/v1/models` question are superseded for the `claude` row.
- [model-metadata](../model-metadata/spec.md): cache read/write accounting.

## Live probe

Run by the user with their own key. Agents prepare the steps and never mark
the provider verified. Record date, model, and outcome per line.

| # | Check | Expected | Outcome |
| --- | --- | --- | --- |
| 1 | `likha --provider claude` startup check | model list loads; context windows shown in `/models` | pending |
| 2 | Bad key | "rejected the credentials" on the check and on a prompt | pending |
| 3 | Prompt on `claude-opus-5-5` | text streams; reasoning streams in the muted view | pending |
| 4 | Prompt that needs two tool rounds | second request accepted with replayed thinking; turn completes | pending |
| 5 | Same session, second prompt | status/usage shows cache reads > 0 | pending |
| 6 | `/models` → `claude-haiku-4-5`, prompt with tools | succeeds with no reasoning shown | pending |
| 7 | Switch back to `claude-opus-5-5` mid-session, prompt | succeeds | pending |
| 8 | Esc during streaming | run cancels within a second; no error entry | pending |
| 9 | `/compact` then prompt | summary produced; next prompt succeeds | pending |
| 10 | Resume the session after restart, prompt | succeeds | pending |
| 11 | Same session: toggle plan mode (or edit AGENTS.md), prompt that uses tools | succeeds; no thinking-signature error | pending |

Effort on this wire is probed separately under FR-40.
