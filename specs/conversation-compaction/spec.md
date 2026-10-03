# Spec: Conversation compaction (`/compact`)

**Status:** implemented (local) — one summarize model call replaces the summarized past; behavior covered by the automated suite.

## Context

Likha's sessions are repository-associated SQLite stores ([v1-spec.md](../v1-spec.md)
FR-10) and every turn resends the full history to the provider
(`internal/model/client.go` `Stream`). Long sessions therefore grow without
bound: tool results, diffs, and command output accumulate until the request
exceeds the provider's context window and the model starts failing on an
otherwise healthy session. The user's only recovery today is abandoning the
session.

Terminal coding agents solve this with explicit compaction: Claude Code's
`/compact` summarizes the conversation so far and lets the same task continue
under the summary; auto-compaction near the context limit is the follow-on
behavior. This feature brings that escape hatch to Likha as a plain model call —
one summarize turn, no agent pipeline (v1 §2 keeps subagents and concurrency out
of scope).

## Resolved decisions (by convention, refining v1 without contradiction)

These follow directly from v1-spec.md and existing house patterns; they are
listed so implementation cannot re-litigate them.

- **`/compact` is a reserved slash command** (FR-03): intercepted in the TUI,
  acts on the application, and is never sent to the model as a prompt. FR-03's
  reserved-command list was amended to include it.
- **Compaction is a single plain model call.** Likha sends the existing history
  plus one summarize instruction, takes the streamed summary text back, and
  stores it. No subagents, no concurrency, no second loop.
- **The summary replaces prior turns in the live history.** After compaction the
  next turn sends the summary (as conversation context) instead of the full
  transcript. The history shrinks; it does not keep a hidden verbatim copy.
- **The current user prompt is kept intact.** Whatever user prompt is in flight
  or follows compaction is sent verbatim; it is never summarized into the
  replacement. The last user turn is part of "continue from here", not part of
  the summarized past.
- **The TUI shows a clear compaction marker entry.** A visible entry (distinct
  role line, e.g. a "Likha" system-style note) marks where compaction happened;
  the summary text itself is inspectable through the normal paging behavior
  (FR-14) — no special viewer, no silently clipped content.
- **Compaction is persisted.** The compacted history and the marker entry are
  saved to the SQLite snapshot like any other completed event, so the session
  resumes compacted (see Resume below).
- **Compaction with a pending approval is refused, not silently applied.** A
  summarize call that rewrote history under an undecided edit or command review
  could break the approval's context. While an approval is pending, `/compact`
  shows a visible refusal and changes nothing. (The decision below picks the refusal's UX shape; the refusal rule itself is fixed.)
- **Compaction during an active run is inert**, like other commands (FR-03 /
  slash-commands precedent): the Enter key is already ignored while working.
- **A failed summarize leaves history intact.** If the model call errors, is
  cancelled, or returns an unusable response, the conversation is unchanged, a
  visible error is shown (FR-11), and the user can retry or continue normally.
  No partial compaction is ever persisted.

## User-visible behavior

1. The user types `/compact` (optionally `/compact <focus instructions>`).
   Likha makes one summarize call over the conversation so far, then replaces the
   summarized turns with the summary. A compaction marker entry appears in the
   conversation view and the summary is readable via normal paging.
2. Focus instructions, when given, steer what the summary emphasizes; they are
   part of the summarize call, not stored as conversation content.
3. The next prompt continues the same task against the compacted history.
4. On resume (FR-10), the compacted history and marker entry are restored as
   saved — the session continues compacted, exactly as it was left. Pending
   approvals still never replay or carry across relaunch (v1 §5); compaction
   does not change that rule.
5. `/compact` on an empty session (nothing to summarize) is a visible no-op
   error-style note, not a model call.

## Errors

- Summarize call fails or is cancelled → visible error (FR-11), history
  unchanged, nothing persisted, TUI usable, retry allowed.
- No provider/model configured → visible error; nothing sent anywhere. The
  refusal happens at command dispatch (resolved decision 4), so the request
  never exists.

## Resolved decisions (landing notes for the four open questions)

1. **Manual-only in v1.** There is no token counting and no per-provider limit
   metadata in `internal/model/provider.go`, so no automatic trigger exists to
   build on; auto-compact near the context limit is future work once a source of
   truth exists. (The measured `ctx` in the status line covers provider-reported
   per-turn usage only — no request-side token estimate — and is not a compaction trigger.)
2. **The summary travels as a `developer`-role message.** `developer` is in
   `client.go`'s role whitelist, so the replacement message is sent verbatim as
   context on the next turn; no pre-send remap is needed. It renders in the
   theme's muted role per FR-15, alongside the visible compaction marker entry.
3. **The pending-approval refusal is a plain error entry in the conversation**
   ("A review is pending; resolve it before compacting."). `/compact` is not
   listed as inert while a review is pending; it is a distinct, visible refusal.
4. **`/compact` without a configured model is refused at command dispatch** —
   it produces a visible error entry and never reaches a network path,
   rather than surfacing the normal connection-check error against a nil client.

## Locally verified

Verified local behavior, all through automated tests in
`internal/app/compaction_test.go` and `internal/app/tui_test.go`:

- **Refusal paths** (`TestCompactRefusalsChangeNothing`): with no configured
  client, with a pending review, and on an empty history, `/compact` shows a
  visible entry and changes nothing — every path makes zero HTTP requests.
- **Single model round-trip** (`TestCompactSummarizesReplacesHistoryAndPersists`):
  exactly one POST carries the prior history plus the summarize instruction
  (focus wording included when given); the structural tool definitions are not
  sent (`tools` absent); the replacement history is the single `developer`
  summary message; the marker entry lands in the conversation; the compacted
  history is persisted so the session resumes compacted.
- **Failure leaves history intact** (`TestCompactFailureKeepsHistoryAndRetries`):
  a failing summarize shows a visible error and keeps the byte-identical
  history; the retry then succeeds and persists the compacted state.
- **Cancellation** (`TestCompactEscCancelsWithoutTouchingHistory`): Esc during
  the summarize call cancels it, shows a visible error, and keeps the
  history byte-identical.
- The `go test ./...` acceptance criterion remains listed below — this note
  records what the suite already covers, not that the release gate passed.

## Acceptance criteria

- [ ] `/compact` on a populated session produces a summary via exactly one
      model round-trip; the live history used by the next turn contains the
      summary in place of the summarized turns, and the current user prompt is
      sent verbatim.
- [ ] The conversation view shows a compaction marker entry, and the summary
      text is fully readable through PgUp/PgDn paging with nothing silently
      clipped (FR-14).
- [ ] Focus instructions after `/compact` steer the summary without being
      stored as conversation content.
- [ ] A compacted session exits and resumes with the compacted history and
      marker entry restored; a pending approval before relaunch still never
      replays or grants.
- [ ] A failing or cancelled summarize call leaves the history byte-identical,
      shows a visible error, and persists nothing; a retry then succeeds.
- [ ] `/compact` with a pending approval is refused with a visible message and
      changes nothing.
- [ ] `/compact` during an active run is inert; `/compact` on an empty session
      is a visible no-op without a model call.
- [ ] `/compact` with no configured provider is refused at command dispatch per
      resolved decision 4, with a clear visible error and no request anywhere.
- [ ] `go test ./...` from the project root passes with the compaction feature's
      tests included; no mock-only test claims compaction works.
