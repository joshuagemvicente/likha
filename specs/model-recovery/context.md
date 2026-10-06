# Context: Model request recovery

Code, tests and docs this feature touches, with the facts that shaped the
spec. All decisions live in [spec.md](spec.md).

## Triggering report (2026-10-05)

Session `b63c0fdd…` ("Write repository AGENTS.md"), provider `opencode-go`,
model `glm-5.3-flash`:

- Turn entry: `{"ms":750460,"tools":7,"thoughts_ms":[69419,398417,235979]}`.
- The last reasoning block was 10,773 bytes over 236s and ended mid-sentence
  (`…Could include repo-init spec + v1-spec in one "specs"`).
- No `finish_reason`, no error event and no `[DONE]` arrived. The error
  entry read `model stream ended before [DONE] or exceeded response limit`.
- The stream was about 0.06% of the 16 MiB cap, so the provider side closed
  the connection. Likha sets no HTTP timeout on model streams
  (`noRedirectHTTPClient` has no `Timeout`), so Likha did not close it.

## Code

- `internal/model/client.go`
  - `maxResponseBytes` (16 MiB), `maxEventLineBytes`.
  - `Client.stream` builds and posts the chat-completions request. Its
    `attempt` loop holds the only existing request retry: the
    `stream_options` fallback (`streamUsageOptionRejected`). HTTP errors
    become `model HTTP <status>: <body>`, and 401/403 wrap
    `ErrUnauthorized`.
  - `streamCodex` is the Responses path (ChatGPT sign-in). It retries once
    after a forced token refresh on 401.
  - `consumeStream` parses chat SSE. A missing `[DONE]` produces the
    ambiguous error at the end of the function. `chunk.Error` is fatal
    today.
  - Both paths wrap the body in `io.LimitReader(…, maxResponseBytes+1)`.
    That reader reports EOF at the cap, so cap and drop are
    indistinguishable (tasks T2).
- `internal/model/codex.go`
  - `consumeCodexStreamDetailed` reads lines on a goroutine and reports
    `codex stream ended without a terminal event` on EOF.
    `response.failed`/`error` become `codex stream error: …`.
- `internal/model/fork.go`
  - `Fork` copies client configuration; a new retry-policy field must be
    copied too.
  - `ForkForTask` reserves one child request per generating POST, retries
    included. That is already the accounting spec point 14 requires.
- `internal/agent/turn_execution.go`
  - `RunTurnWithOptions` streams each round through `client.StreamUsage`.
    On error, `fail(err)` emits `error` with the history so far, which
    includes earlier rounds' tool results. Steering drains only at the top
    of each round (FR-21).
  - `validateToolCallIDs` failure appends the assistant message without
    tool calls before failing (tasks T18).
- `internal/agent/explore_loop.go`, `profile_runner.go`: accumulate a
  `partial` message from the stream callbacks and fall back to it on error.
  An internal retry would concatenate attempts unless the accumulator
  resets (tasks T11).
- `internal/agent/compaction.go` (`CompactHistoryUsage`) and
  `sessionname.go`: single `StreamUsage` calls.
- `internal/tui/tui.go`
  - The run-event switch: `reasoning`, `text`, `notice`, `done`/`error`.
  - `finishRun` deletes the open streaming text entry on error and keeps
    reasoning. Spec point 10 changes this.
- `internal/tui/thought_items.go`: `appendTurnFooter` and `TurnInfo`
  (`internal/transcript/footer.go`). Retries and failure go there (T15, T17).
- `internal/tui/commandcomplete.go` (`commands`) and
  `internal/tui/dialog.go` (`handleCommand`): `/retry` registration.

## Specs touched

- [v1-spec.md](../v1-spec.md) FR-36 (new). Consistent with FR-11 (errors
  stay visible), FR-21 (steering at safe points; reserved commands refused
  while a run is active), FR-28 (child request and time budgets), and the
  Reliability NFR "a connection failure preserves the session".
- [slash-commands](../slash-commands/spec.md): add `/retry` to the list when
  M5 lands.
- [explore-agents](../explore-agents/spec.md): child failures already return
  honest partial/failed results; recovery happens inside that.

## Known adjacent gap (out of scope)

`consumeStream` ignores `finish_reason`. A provider that stops at its
output-token limit (`"length"`) and then sends `[DONE]` returns a truncated
message as success. This is not a transport failure, so it isn't retried
here; it deserves its own spec.

## Probe records

- Headless fault-injection probe (T21): not run.
- Live probe (T22, user-run): not run.
