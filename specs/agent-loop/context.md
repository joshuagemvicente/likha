# Context: Agent tool loop (harness steering + round-cap auto-continue)

Code paths this feature touches, validated 2026-10-01.

- `internal/agent/agent.go`
  - `agentTools` (`:29-38`) — the five built-in tool definitions; `grep`
    and `read` descriptions already state repository scoping.
  - `RunTurn` (`:42-113`) — the loop site. History is built as
    `prior + user prompt` with **no** system message; `for range 32`
    bounds the model rounds; on exhaustion
    `fail(fmt.Errorf("model exceeded 32 consecutive tool rounds"))`
    (`:112`) is the only exit after the loop. The continuation notice
    replaces that `fail` with an append + `continue`.
  - `dispatchTool` (`:128-…`) — `glob`/`read`/`grep` run ungated;
    `edit_file`/`run_command`/MCP fallback call `requestApproval`. The
    MCP fallback is the last `switch` case; a harness message must not
    change that ordering.
- `internal/model/client.go` — `Stream` validates roles including
  `system`/`developer` (`:352-356`) and never injects a prompt itself;
  both wires (chat-completions and Codex, `codex.go`) accept a leading
  system message. The harness message rides the existing path.
- `internal/agent/compaction.go` — precedent for appending a
  harness-authored `developer` message (`compactPrefix`) into history;
  the continue-instruction uses the same role so it is never mistaken
  for user input. Note: compaction runs as a separate turn; the
  continue-instruction is appended mid-turn.
- `internal/tui/tui.go` — event switch around `:470-540`. Unknown event
  kinds are ignored by the switch; the new `notice` kind needs a case
  that appends a `Likha`-role entry without ending the turn (the existing
  `error` case at `:511-535` is the shape to differ from: it clears
  `working`).
- `internal/session/session.go` — persisted history snapshot comes from
  the event's `History`; the harness system message must be prepended
  per request, never stored, matching agent-harness §Session wiring.
- `specs/agent-harness/spec.md` — parent feature (draft). This slice
  implements only its tool-contract section; update its "Amend FR-03 or
  add FR-17" note to point at FR-19 to keep the two specs consistent.

Precedent for the notice UX: the `Likha`-role entries already used for
cancellation (`tui.go:532`).
