# Checklist: Conversation compaction (`/compact`)

Status mirrors [spec.md](spec.md) acceptance criteria; unchecked until
observable in the real TUI.

- [ ] `/compact` summarizes via exactly one model round-trip; the next turn
      sends the summary in place of prior turns, with the current user prompt
      verbatim.
- [ ] A compaction marker entry is visible, and the summary is fully readable
      through normal paging with nothing clipped.
- [ ] `/compact <focus instructions>` steers the summary; the instructions are
      not stored as conversation content.
- [ ] Exit and resume after compaction restores the compacted history and
      marker entry; a pending approval from before relaunch never replays.
- [ ] A failed or cancelled summarize leaves history unchanged, shows a visible
      error, persists nothing, and a retry succeeds.
- [ ] `/compact` with a pending approval is refused visibly and changes
      nothing.
- [ ] `/compact` during an active run is inert; on an empty session it is a
      visible no-op with no model call.
- [ ] `/compact` with no configured provider is refused at command dispatch per
      resolved decision 4, with a clear visible error and no request anywhere.
- [ ] `go test ./...` from the project root passes, including the compaction
      feature's tests.

## Locally verified

Automated coverage already lands these outcomes locally (tests in
`internal/app/compaction_test.go` and `internal/app/tui_test.go`): refusal
paths (no configured client, pending review, empty history) change nothing with
zero HTTP requests; `/compact` performs exactly one summarize POST carrying the
prior history plus the instruction (focus wording included when given) and no
tool definitions, then swaps the history for the single `developer` summary
message, shows the marker entry, and persists the compacted state so resume
restores it; a failing summarize shows a visible error and keeps the
byte-identical history, and the retry then succeeds; Esc during the summarize
cancels it with the history untouched. The boxes stay unchecked until the
behavior is observed in the real TUI.
