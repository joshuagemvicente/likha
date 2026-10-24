# Feature: Undo Likha's edits (`/undo`)

**Status:** planned — decisions recorded 2026-10-07 (user interview); no
implementation started. Requirement: [v1-spec FR-43](../v1-spec.md#attention-and-automation-amendment-user-approved-2026-10-07).

## Context

Likha writes repository files only through approved (or Approve-always
granted) `edit_file` and `edit` proposals (FR-06, FR-07). Once an edit is
applied there is no way back except the user's own tools: Likha refuses
stale proposals, but it cannot roll back a change the user approved and
then regretted. OpenCode V2 restores files with `/undo` from worktree
snapshots, OMP has a checkpoint/rewind pair, and Claude Code rewinds to a
checkpoint. Likha is the only one of the four with no rollback at all
([feature comparison](../tooling-landscape/feature-comparison.md) Tier 1,
item 5).

Likha already keeps private, session-owned recovery records for multi-file
edits (the edit journal). Those records describe an apply in progress and
are never replayed. Undo is a different, explicit user action: it writes
the files back to what they were before Likha's change, after showing the
user exactly what will be written.

## Role

Let the user take back Likha's most recent applied file change, one change
set at a time, without ever overwriting work that happened after it. Be
honest about what undo cannot reach: effects of shell commands.

## Scope

In scope: every repository write Likha applies through `edit_file` or
`edit` on the main agent, including writes under the Approve always grant
and `/init` proposals; a `/undo` reserved command; keeping undo history
with the session so it survives resume.

Out of scope:

- Undoing effects of shell commands (`run_command`), MCP tools or anything
  outside Likha's edit tools. The transcript says so (decision 4).
- `/redo`. After an undo, the model or user can make the change again.
- Rewinding the conversation. Undo changes files only; the transcript and
  model history stay, with a note added (point 13).
- Choosing an older change set out of order. `/undo` always takes the
  newest remaining one.
- Git-based or whole-worktree snapshots.
- Undo in headless mode ([headless-print](../headless-print/spec.md)).

## Decisions (user interview, 2026-10-07)

1. **Undo restores Likha's own applied edits, per change set.** A change set
   is everything one applied `edit_file` or `edit` call wrote: one file for
   `edit_file`, every file (and created directory) for one `edit` call.
2. **Snapshots are taken before each applied edit**, whether it was
   approved in a review or let through by Approve always.
3. **`/undo` reverts the most recent change set and can be repeated**, each
   time taking the next older one.
4. **Shell command effects are not undone**, and the transcript says so.
5. **Files Likha created are removed** by undo; files Likha replaced get
   their previous content and permissions back.
6. **Undo never overwrites newer work.** If a file in the change set has
   changed since Likha wrote it, undo refuses.

## Drafting defaults (confirm in review)

These were not part of the interview. Each is the drafting choice; review
may change any of them before implementation.

- D1. **All or nothing per change set.** If any file in the set changed
  since Likha wrote it, the whole set is refused and nothing is written.
  Partial undo of a multi-file change would leave code inconsistent.
- D2. **Undo shows a review first.** `/undo` opens the same review used for
  edits, showing the reverse diff, with **Undo** and **Cancel**. Approve
  always never covers it.
- D3. **Undo history persists with the session** and works after resume. It
  is deleted with the session.
- D4. **Bounded history:** the 20 most recent change sets per session, and
  at most 64 MiB of saved original content per session. Older sets are
  dropped first; `/undo` past them says older changes cannot be undone.
- D5. **The model is told** on its next request that the user undid a change
  set, naming the files, never their content.
- D6. **`/undo` refuses during an active run or pending review**, like every
  reserved command.
- D7. **`/init` edits are undoable** like any other edit.
- D8. **`/undo` is allowed in plan mode.** It is the user's action, not the
  agent's.
- D9. **An edit whose apply failed or was left unresolved is not undoable.**
  Its private edit-journal record keeps its existing interrupted/failed
  reporting.
- D10. **Undo of a multi-file set uses the same private journal** as a
  multi-file edit, so an interrupted undo is reported as an interrupted
  record on resume and is never replayed.
- D11. **Changed means content or permissions differ** from what Likha
  wrote, or the file is missing, or a file now exists where Likha removed
  one. A different inode with identical content and mode is not a change
  (editors that save by rename keep undo working).

## Functional changes

### Recording change sets

1. Each successful `edit_file` or `edit` apply on the main agent records one
   change set: the time, the tool call it came from, each affected path, its
   content and permissions before the write (or that it did not exist), and
   what Likha wrote. Directories Likha created are recorded too.
2. A change set is recorded only when the apply finished successfully. An
   apply that failed, was cancelled before publishing, or ended with
   unresolved paths records nothing undoable (D9).
3. Recording never changes what the edit review shows or what the model is
   told about the edit.
4. Saved original content is private session data. It is never shown to the
   model and never written into the repository.
5. The 20-set and 64 MiB bounds (D4) drop the oldest sets first. A single
   change set larger than 64 MiB is applied as today but recorded as not
   undoable, and the edit's tool item says `not undoable: too large`.

### `/undo`

6. `/undo` takes no argument. Any argument makes no change and shows
   `/undo takes no arguments; it reverts Likha's most recent file change.`
   with the draft kept.
7. With no change set left, `/undo` prints `Nothing to undo.` If older sets
   were dropped by the bounds, it prints
   `Nothing to undo. Older changes in this session can no longer be undone.`
8. Otherwise `/undo` checks every file in the newest change set (D11):
   - When none changed, it opens a review titled
     `Undo: <n> file(s) from <time>` showing the reverse diff: removed
     files as deletions, restored files as their diff back to the original.
     Created directories that will be removed are listed.
   - When any changed, it writes nothing and shows
     `Can't undo: <path> changed since Likha wrote it. Nothing was reverted.`
     listing every changed path. The change set stays on the history so
     the user can fix the file and try again.
9. **Undo** in the review writes the original content and permissions back,
   removes files Likha created, and removes directories Likha created when
   they are empty. The same stale checks as an edit apply run immediately
   before each write; a file that changes during the review makes the
   whole undo refuse (D1).
10. **Cancel** or Esc closes the review with nothing written. The change set
    stays on the history.
11. After a successful undo the conversation shows
    `Undid <n> file(s) from <time>: <paths>. Effects of shell commands are not undone.`
    and the change set is removed from the history; the next `/undo` takes
    the one before it.
12. Undo writes stay inside the repository with the same path confinement
    and symlink defenses as edits (FR-05): a path that now resolves through
    a symlink or outside the repository is refused, never followed.

### What the model sees

13. The next model request in the session carries a short note that the user
    undid a change, naming each path and whether it was restored or
    removed, and saying that earlier tool results about those files are out
    of date. The note never contains file content.
14. Undo starts no run by itself.

### Shell commands

15. When an approved or auto-approved command ran after the change set was
    applied, the undo review says
    `Commands ran after this change. Their effects are not undone.` If that
    command changed a file in the set, point 8 already refuses.

### Availability

16. `/undo` refuses during an active run or a pending review or question
    with the shared reserved-command message (D6).
17. `/undo` is listed in `/help` and in the `/` autocomplete.
18. After `/sessions` resume, the resumed session's change sets are
    available (D3); another session's never are.
19. An undo interrupted by a crash or exit shows on resume like an
    interrupted edit: affected paths and last confirmed effects, with
    nothing replayed (D10).

## Acceptance criteria

- Approve an `edit_file` that replaces a file, `/undo`, approve the review:
  the file's content and mode match the original; the model's next request
  carries the undo note.
- An `edit` call that replaces two files and creates one file in a new
  directory: one `/undo` restores both, removes the created file and the
  now-empty directory.
- Two applied edits: `/undo` twice reverts newest first; a third `/undo`
  prints `Nothing to undo.`
- Change one file of a two-file set by hand, then `/undo`: refused, both
  paths unchanged, the changed path named.
- Save a Likha-written file again with an editor that writes by rename,
  same content: `/undo` still works.
- Cancel the undo review: nothing written, the set is still undoable.
- Run a command after an edit: the undo review shows the commands notice.
- Resume the session in a new process: `/undo` still reverts the last set.
- Edits under Approve always and `/init` proposals are undoable.
- `/undo` during a run or pending review shows the reserved-command refusal.
- Replace a parent directory with a symlink after the edit: undo refuses
  and nothing outside the repository is touched.
