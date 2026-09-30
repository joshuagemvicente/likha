# Context: Conversation compaction (`/compact`)

## Code

| Path | Relevance |
| --- | --- |
| `internal/app/compaction.go` | The feature core: `compactHistory` copies the caller's history, appends the summarize instruction (optional `Emphasize: <focus>.`) as a final user message, makes one `client.Stream` call with no tools, and returns the replacement history — a single `developer` message ("Conversation summary (compacted). Earlier turns are no longer available; continue the task from this summary." + summary) plus the summary text. Errors never touch the caller's slice. |
| `internal/app/tui.go` | `/compact [focus]` dispatch in `handleCommand` (ordered refusals: no client → pending review → empty history → "Nothing to compact yet."), the cancellable summarize turn and its `compacted` event (history swap + "Conversation compacted. Summary of earlier turns:" marker entry + `Save`, so resume restores the compacted state), and the command-popup key handling (`commandQuery`/`syncCommand`/`updateCommand` hooks around the input loop). `commandHelp` lists `/compact [focus]`. |
| `internal/app/commandcomplete.go` | The `/` completion popup: reserved-command list mirroring `handleCommand`, case-insensitive substring filter, draft rewrite (`completeCommand` appends a trailing space), and the shared hint row "↑/↓ select  Tab complete  Esc dismiss". |
| `internal/app/mentions.go` | The `@` file/folder popup, now sharing the same hint row as the command popup; the two popups are mutually exclusive in the input loop. |
| `internal/app/composer.go` | Composer layout: `composerLines` subtracts `mentionLines()` and `commandLines()` when fitting the editable draft tail, so either popup overlays the transcript without displacing the draft or status rows. |
| `internal/session/session.go` | SQLite store: `Snapshot` holds `History []model.Message` plus displayable `Entries`; schema v1 stores the snapshot as one BLOB, so a compacted history persists without schema change (entry/marker rendering rides on `Entries`). FR-10 semantics live here. |
| `internal/app/agent.go` | `runTurn` builds `history` by appending the prior snapshot and the current `prompt`, then resends it full through `Stream` every round — compaction trims what this resends; the summarize call runs as its own cancellable turn beside (not inside) the turn loop. |
| `internal/model/client.go` | `Message` struct and `Stream(ctx, messages, tools, …)`: full-history resend per turn; role whitelist (`system`/`developer`/`user`/`assistant`/`tool`) that admits the `developer`-role summary message. No token counting exists here — the basis for resolved decision 1. |
| `internal/model/provider.go` | Provider rows (OpenAI-compatible + `chatgpt` OAuth/Codex Responses row); any per-provider context-limit metadata for future auto-compact would live here. |

## Related specs

- [v1-spec.md](../v1-spec.md) — FR-03 (reserved commands), FR-10 (resumable
  SQLite sessions, no replayed approvals), FR-11 (visible errors), FR-14
  (paging), §2 scope (no subagents/concurrency → one plain summarize call), §5
  reliability.
- [slash-commands/](../slash-commands/spec.md) — dispatch, inert-while-busy,
  and unknown-command conventions `/compact` follows.
- [prompt-editor/](../prompt-editor/spec.md) — planned FR-17/FR-18 work the
  command input shares; no coupling.

## External precedents (pointers only, not requirements)

- Claude Code `/compact` — summarize-and-continue command, optional focus
  instructions, auto-compact near the context limit:
  https://docs.anthropic.com/en/docs/claude-code/cli-usage
- OpenCode / OMP compaction — summarize-and-replace history pattern referenced
  by the slash-commands spec's precedent list; treat as design prior, not a
  compatibility target.

## Resolved decisions landed (see spec.md)

1. Auto-compact is future work: manual-only in v1 because no token counting or
   per-provider limit metadata exists.
2. The summary is a `developer`-role message — valid in `client.go`'s
   whitelist, rendered muted per FR-15, sent verbatim as context.
3. The pending-approval refusal is a plain error entry ("A review is pending;
   resolve it before compacting.").
4. `/compact` without a configured model is refused at command dispatch, never
   reaching a network path.
