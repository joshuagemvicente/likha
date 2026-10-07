# Context: Automatic compaction

Code, tests and docs this feature touches, with the facts that shaped the
spec. All decisions live in [spec.md](spec.md). Paths verified against
`main` at `269fc54` on 2026-10-07.

## Code

- `internal/agent/compaction.go`
  - `CompactHistoryUsage(ctx, client, history, focus, onText)` makes the one
    summarize request through `backgroundClient` (no effort, no replay
    state) and returns a single `developer` message prefixed with
    `compactPrefix`, the summary text, and the request's usage. It never
    mutates the input and returns an error on an empty summary.
  - The replacement is the whole history. Keeping the current run's user
    prompts (spec D3) needs a new helper around it, not a change to its
    contract, because `/compact` relies on the full replacement.
- `internal/agent/turn_execution.go`
  - `RunTurnWithOptions` is the main loop. Each round: `applySteer(drainSteer(steer))`,
    build `request` from `mainHarness` + `history` (+ `continuation`), then
    `client.EstimateInputTokens(request, definitions)` and a `Kind:
    "context"` event, then `client.StreamUsage`. The trigger belongs
    between the estimate and the stream call, where tool results of the
    previous round are already in `history` and no call is dangling.
  - `continuation` (round-cap checkpoint note) is appended after history;
    the compacted request must keep that ordering.
  - Events the TUI already consumes: `context`, `usage`, `notice`,
    `steer`, `tool_checkpoint`, `done`, `error`. The loop does not know the
    context window today.
- `internal/agent/tool_registry.go` — `RunOptions`. The agent may not import
  `providers` or `tui` (AGENTS.md), so the resolved window and the opt-out
  must arrive here as plain values (e.g. `ContextWindow int64`,
  `AutoCompact bool`).
- `internal/agent/explore_loop.go`, `internal/agent/profile_runner.go` —
  child loops; they never call the estimator and stay out of scope.
- `internal/model/context_estimate.go` — `Client.EstimateInputTokens`
  (request + tools + replay text) and `Client.EstimateHistoryTokens`
  (idle estimate). `ok=false` means no estimate; treat as "do not compact".
- `internal/model/windows.go` — `ResolveContextWindow(provider, model,
  override, metadata)`: override > metadata > catalog prompt limit, never a
  default.
- `internal/tui/tui.go`
  - `startCompaction` (≈1104) runs `/compact` as its own run: sets
    `Compacting…`, resets `priorLen`, emits one terminal `compacted` event.
  - The `compacted` case in the event switch (≈716) swaps history, calls
    `recalculateContext`, appends the `Likha` divider entry
    (`"Conversation compacted. Summary of earlier turns:\n\n" + summary`),
    ends the run, persists, and holds the steering queue. Automatic
    compaction needs a non-terminal variant that does the swap, divider,
    recalculation and persist but keeps the run, the activity row and the
    steering channel alive.
  - `resolveContextWindow` (≈1257) already computes `m.contextWindow` /
    `m.contextWindowKnown`; pass them into `RunOptions` at run start.
  - `finishRun`, `reconcileQueue` and `priorLen` assume the run's history
    starts at `priorLen`. After a mid-run replacement the history is
    shorter than `priorLen`, so the queue backstop and unexecuted-call
    backstop must be rebased (see tasks T5).
- `internal/tui/tool_wiring.go:46` — builds `agent.RunOptions` for main turns.
- `internal/tui/dialog.go:270` — `/compact` dispatch and refusals; unchanged.
- `internal/providers/config.go` — `StoredProviderConfig`; the opt-out is a
  new `AutoCompact *bool \`json:"auto_compact,omitempty"\`` (nil = on), read
  through the existing `flagEnabled` pattern.

## Tests to extend

- `internal/tui/tui_test.go` — `TestCompactSummarizesReplacesHistoryAndPersists`,
  `TestCompactFailureKeepsHistoryAndRetries`, `TestCompactEscCancelsWithoutTouchingHistory`,
  `TestCompactRefusalsChangeNothing`: patterns for an httptest provider that
  serves both the summarize call and turn requests.
- `internal/tui/context_tracker_lifecycle_test.go` —
  `TestContextEstimateRecalculatedOnResumeAndCompaction`.
- `internal/tui/model_metadata_test.go` — `TestCompactionUsageIsCounted`.
- `internal/tui/steer_now_test.go` — `TestSteerNowCompactionAndCancellingHold`
  (the hold rule that automatic compaction must *not* apply).
- `internal/agent/context_tracker_events_test.go` — agent-level event order.

## Docs

- `docs/usage.md` § "Conversation compaction (`/compact`)" and the
  status-line `config.json` paragraph.
- `specs/conversation-compaction/spec.md` resolved decision 1 says
  "manual-only in v1"; add a pointer to this feature when it lands.

## Facts that shaped the spec

- The tracker estimate is local and marked `~`; provider accounting can be
  higher. 85% leaves room for that error plus the summarize instruction,
  which itself must fit (the summarize request carries no tool definitions,
  so it is smaller than the request that triggered it).
- FR-36 never retries a rejected (400/413) request, including context
  overflow, so the pre-request trigger is the only automatic protection.
- A run's history is complete at the trigger point: every tool call of the
  previous round has a result, so the summarize request is well formed on
  every wire (chat, Responses, native Claude).
