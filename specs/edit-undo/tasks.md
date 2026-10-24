# Tasks: Undo Likha's edits

Ordered implementation tasks. Behavior wording lives in [spec.md](spec.md);
code paths in [context.md](context.md). Each milestone leaves
`go test ./...` and `go vet ./...` green. Drafting defaults D1–D11 are
assumed; revise these tasks if review changes one.

**Status 2026-10-07:** M1–M3 and T9 implemented (local), automated checks
only; W1 outstanding. Implementation notes that differ from the plan below:

- Undo reuses the targeted-edit publication machinery directly: an
  internal `remove` flag on a planned file publishes its absence (the
  Likha-written file is moved aside by exclusive rename and discarded only
  after the whole group verifies). The model-facing `edit` tool never sets
  it. Created directories are removed afterwards with
  `unlinkat(AT_REMOVEDIR)`, so a non-empty or replaced entry is kept.
- The 64 MiB bound is measured on the stored JSON payload (originals plus
  metadata), which is slightly stricter than original bytes alone. Pruning
  drops the oldest sets and keeps the remaining history contiguous; an
  `edit_changeset_drops` row records that older sets are gone.
- A recording failure (including too large) is reported as a tool-result
  warning (`not undoable: too large` / `not undoable: undo history could
  not be saved`). Warnings are also part of the model-visible result, so in
  that failure case the model sees the same line.
- The model note (T7) is a `developer` message appended to the session
  history, so it persists with the snapshot and every later request carries
  it as part of the conversation.

Fixed values (D4), defined once in `internal/session`:

| Name | Value |
| --- | --- |
| change sets kept per session | 20 |
| saved original bytes per session | 64 MiB |

## M1 — Capture change sets (`internal/actions`, `internal/session`)

- [x] **T1. Change-set record type.** In `internal/actions`, add
      `ChangeSet` with per-file entries (`Path`, `Existed`, `OldText`,
      `OldMode`, `NewSHA256`, `NewMode`) and `CreatedDirs`. Expose it from
      a successful `(*Edit).Apply` (single file: `old`, `before`, `new`
      already held) and from `(*EditProposal).Apply` (from
      `targetedProposal.files` and the directories it created). Nothing is
      returned when the report has `Unresolved` paths (D9).
      *Verify:* unit tests for replace, create, multi-file with new
      directory, and failed/unresolved apply returning no set.
- [x] **T2. Session table.** Add `edit_changesets` (id, session_id with
      `ON DELETE CASCADE`, repository, created_ns, tool_call_id, size,
      payload BLOB) in `configure`, mirroring `edit_journal`. Add
      `SaveChangeSet`, `LatestChangeSet`, `DeleteChangeSet`, and pruning to
      the T-table bounds inside the save transaction. Ownership checks match
      `RecordEditProgress` (session id + repository root).
      *Verify:* store tests: order, cascade on session delete, pruning by
      count and by bytes, other-repository rows invisible.
- [x] **T3. Record after apply.** In `internal/agent/tool_registry.go`,
      after `prepared.Apply()` / `batch.Apply(ctx)` succeed, call a new
      `RunOptions.RecordChangeSet` hook with the tool call ID; wire it in
      `internal/tui/tool_wiring.go` `toolRunOptions` to the store. A save
      failure keeps the edit applied and adds `not undoable: <reason>` to
      the tool item (spec point 5), never a failed edit result (FR-09).
      *Verify:* agent test with a fake hook; TUI test that a store failure
      leaves the edit result successful with the notice.

## M2 — `/undo` command and review (`internal/tui`, `internal/actions`)

- [x] **T4. Revert plan.** `actions.PrepareUndo(root, ChangeSet)` reopens
      each path with the existing `openParent`/`readTarget` (no symlink
      following), compares content hash and mode to `NewSHA256`/`NewMode`
      (D11), and returns either the list of changed paths or a proposal with
      the reverse diff (`unified`). It never writes.
      *Verify:* table tests: unchanged, content changed, mode changed,
      deleted, renamed-with-same-content, parent replaced by symlink.
- [x] **T5. Revert apply.** `(*UndoProposal).Apply(ctx)` reuses the
      targeted-edit publish machinery (exclusive rename, stale recheck,
      journal via `SetJournal`) for restores and removals, then removes
      created directories only when empty (D10).
      *Verify:* apply tests mirroring the targeted-edit suite: concurrent
      change between review and apply refuses the whole set; journal
      records interrupted state.
- [x] **T6. Command.** Add `case "undo"` to `handleCommand` in
      `internal/tui/dialog.go`; argument error, `Nothing to undo.` variants,
      reserved-command refusal while `m.working || m.pending != nil`; open
      the review dialog with **Undo**/**Cancel** (no Approve always), add
      the commands-ran notice (spec point 15) by checking for
      `run_command` tool records after the set's tool call. Add `/undo` to
      `commandHelp` and the autocomplete list.
      *Verify:* TUI tests for each message and for review cancel.

## M3 — Model note and resume

- [x] **T7. Undo note.** On success, append a pending developer note that
      the next `startTurn` prepends (paths and restored/removed only), and
      persist it with the snapshot so a resume before the next turn keeps
      it.
      *Verify:* the next request body carries the note once; no file
      content appears in any request.
- [x] **T8. Resume.** Confirm change sets load for the resumed session only;
      interrupted undo journals render through `showEditRecovery`.
      *Verify:* integration-style test: apply edit, close store, reopen,
      `/undo`.

## M4 — Docs and walkthrough

- [x] **T9.** `docs/usage.md` `/undo` section (including what is not
      undone), README command list, CHANGELOG entry stating what was
      verified.
- [ ] **W1. Real-terminal walkthrough** of the acceptance criteria in
      [spec.md](spec.md), recorded in [checklist.md](checklist.md).
