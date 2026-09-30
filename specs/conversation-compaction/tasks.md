# Tasks: Conversation compaction (`/compact`)

Implemented record (past tense; decisions landed in [spec.md](spec.md)).

1. **Compaction core** — `internal/app/compaction.go`: `compactHistory` copies
   the caller's history (never mutating it), appends the summarize instruction
   as a final user message (`Emphasize: <focus>.` appended when a focus is
   given), makes one `client.Stream` call with no tools, and returns the
   replacement history — a single `developer` message ("Conversation summary
   (compacted). Earlier turns are no longer available; continue the task from
   this summary." + summary) plus the summary text. Empty/failed summaries
   surface as errors. Verified: `compaction_test.go` success, focus wording,
   nil-client/empty-history, stream-error, context-cancelled, and
   already-compacted cases.
2. **TUI wiring** — `internal/app/tui.go`: `/compact [focus]` joins
   `handleCommand` with ordered refusals (no configured client → pending
   review → empty history → "Nothing to compact yet."), each a visible entry
   with zero network activity; the in-flight summarize runs as a cancellable
   turn that streams the summary; on the `compacted` event the history is
   swapped for the replacement, the marker entry ("Conversation compacted.
   Summary of earlier turns:" + summary) is appended, and `Save` persists the
   compacted snapshot so resume restores it. `/help`'s `commandHelp` now lists
   `/compact [focus]`. Verified: `tui_test.go` refusal, summarize-replace-
   persist, failure-retry, and Esc-cancel cases.
3. **Command popup polish** — `internal/app/commandcomplete.go`: typing `/`
   opens the reserved-command popup over the composer (case-insensitive
   substring filter; closes the moment a space is typed; never open at the
   same time as the `@` mention popup), ↑/↓ move, Tab completes, Enter
   completes unless the query is already an exact command name (then it
   sends), Esc dismisses, and both popups show the shared hint row
   "↑/↓ select  Tab complete  Esc dismiss" (`mentions.go` gained the same
   hint). `composer.go` accounts for the popup rows when fitting the draft
   tail. Verified: `TestCommandPopupOpensFiltersCompletesAndSends`,
   `TestCommandPopupExclusiveWithMentionPopup`.
