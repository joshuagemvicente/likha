# Feature: Prompt history and recall

**Status:** implemented

## Context

Prompt recall lets users reuse or revise an earlier submission without
retyping it. The interaction follows OpenCode's chronological Up/Down history
model while respecting Likha's editable, multiline composer and its completion
popups. History belongs to a session and remains available when that session
is resumed.

## User-visible behavior

### Recall navigation

- With no popup owning the arrow keys, Up moves the caret to the start of the
  first visual row if needed; once it is there, Up recalls the most recent
  accepted submission. Up at that edge while browsing history moves to the
  previous (older) submission.
- The first history-navigation Up saves the current live draft. Down moves the
  caret to the end of the last visual row if needed; once it is there, Down
  moves to the next (newer) submission. Down past the newest entry restores
  the saved draft text unchanged.
- A recalled submission is an editable draft. Editing it does not alter the
  saved history entry. Each accepted submission is a separate entry, in
  acceptance order, including repeated identical submissions.
- History does not wrap. Up at the oldest entry and Down after restoring the
  live draft do not jump to the opposite end of history.
- If there are no history entries, Up at the top edge leaves the draft
  unchanged.

### Composer rows and popups

- In a multiline or visually wrapped draft, Up and Down first move the caret
  between visual rows. At the top row, Up first moves the caret to the start
  of the draft if needed, then navigates history; at the bottom row, Down
  first moves it to the end if needed, then navigates history. A logical
  newline or terminal wrap does not by itself trigger history navigation
  before the caret reaches the relevant edge.
- When a completion or other popup owns Up/Down, those keys continue to
  navigate that popup. They do not move through prompt history while the
  popup is open. History navigation resumes when the popup no longer owns the
  keys.
- The rule applies both to the initial live draft and to a recalled draft.
  Within a recalled multiline or wrapped submission, arrows move between its
  visual rows before traversing to another history entry.

### Entries and session lifetime

- A submission enters history when Likha accepts it for processing: a normal
  prompt accepted for sending, a recognized slash command accepted for
  dispatch, or a prompt accepted into the run's queue. The recorded entry is
  the user's submitted input, so recalling a slash command allows it to be
  submitted again.
- An empty submission or a submission rejected before processing (for
  example, an unknown slash command) does not enter history. Once accepted,
  an entry remains recallable even if its later model request or command
  action fails.
- Each session has its own persisted recall history. Resuming a session
  restores that session's history; a new session does not inherit another
  session's entries. History is not shared globally across sessions.
- A session saved before prompt-history storage was introduced seeds recall
  from its saved user transcript on resume.
- A queued prompt becomes recallable when it is accepted into the queue.
  Recall history is separate from queue delivery: persisting or recalling a
  queued prompt does not restore the outstanding queue or send the prompt on
  resume.
- History is available whenever the composer accepts editing, including
  while a run is active and prompts can be queued. It does not bypass an
  input gate that makes the composer unavailable.

## Non-goals

- No Ctrl+R or other searchable history interface; navigation is chronological
  Up/Down only.
- No cross-session or global history.
- Recalling an entry only puts it back in the composer; it does not submit,
  dispatch, queue, or otherwise repeat it automatically.

## Acceptance criteria

- [x] With a live draft and at least one accepted entry, Up moves to the start
      of the first row when needed, then saves the draft and recalls newest.
- [x] Repeated Up at the top edge traverses entries from newer to older; Down
      traverses newer entries, and Down past newest restores the draft text.
- [x] Up at oldest and Down after restoring the live draft do not wrap.
- [x] In multiline or wrapped drafts, Up/Down move between visual rows first
      and navigate history only from the top/bottom edge.
- [x] Popups that own Up/Down keep those keys; history position is untouched.
- [x] Accepted normal prompts, recognized slash commands, and queued prompts
      are recallable; repeated identical submissions remain separate entries.
- [x] Empty, unaccepted, and unknown slash submissions do not enter history.
- [x] Resuming a session restores only its own history, seeding legacy sessions
      from their saved user transcript; queued prompts are not requeued or sent.
- [x] Editing a recalled entry changes only the composer draft, not the saved
      history entry, and recall never submits automatically.
- [x] No Ctrl+R history-search interaction is exposed.
