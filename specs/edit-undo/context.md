# Context: Undo Likha's edits

Code, specs and facts this feature touches. All decisions live in
[spec.md](spec.md). Line numbers drift; anchor on the names.

## Code

- `internal/actions/edit.go`
  - `Edit` holds `old`, `new`, `before os.FileInfo` (nil for a new file) and
    the canonical `root`/`parts`/`parents` — everything a single-file change
    set needs (tasks T1).
  - `(*Edit).Apply` checks the target twice, writes a `.likha-edit-<nonce>`
    temp file with `O_NOFOLLOW`, keeps the original mode (setuid/setgid/
    sticky included), and publishes by `Linkat` (create, fails with
    `EEXIST`) or `Renameat` (replace). New files get mode 0644.
  - `openParent` / `readTarget` walk each component with
    `O_DIRECTORY|O_NOFOLLOW`; they are the confinement and symlink defense
    undo must reuse (spec point 12). `ErrStaleEdit` is the stale sentinel.
  - `unified` renders the diff shown in reviews; undo's reverse diff uses it
    with old/new swapped.
- `internal/actions/targeted_edits.go`
  - `EditProposal` / `targetedProposal` (`files` with `old`, `next`,
    `before`; `dirs` with `before == nil` for directories to create).
  - `(*EditProposal).Apply` stages every output, moves originals aside by
    exclusive rename, links outputs, verifies the whole group, then
    cleans up; `targetedApplyReport` has `Applied`, `Restored`,
    `Unresolved`. An apply with `Unresolved` paths is not undoable (D9).
    The exclusive-rename publish path is what undo's multi-file apply
    should reuse (T5); `targetedExclusiveRenameSupported` gates it per
    platform (`targeted_rename_*.go`).
- `internal/actions/targeted_journal.go` — `EditProgress`,
  `EditFileRecord` (already stores `OldText`/`NewText` privately), and
  `SetJournal`. The journal is write-ahead recovery evidence, explicitly
  "not authorization to replay/recover on resume"; change sets are a
  separate record with a different purpose (undo on request).
- `internal/agent/tool_registry.go`
  - `edit_file` registration: `Authorize` runs `PrepareEdit` and
    `authorizeEdit`; `Run` calls `prepared.Apply()` and returns
    `Applied edit to <path>` (plus `autoEditLabel` under Approve always).
  - `edit` registration: refuses when `options.EditJournal == nil`; `Run`
    calls `options.EditJournal(batch)` then `batch.Apply(ctx)`.
  - `RunOptions` gains the `RecordChangeSet` hook (T3).
- `internal/agent/edit_grant.go` — `authorizeEdit`: Approve always and
  `InitMode`/warning always-ask rules. Undo's review must not offer
  `RememberEdits` (D2).
- `internal/session/session.go` — `configure` creates `edit_journal`
  (`ON DELETE CASCADE` on `sessions(id)`); the new `edit_changesets` table
  goes beside it. `internal/session/edit_journal.go` —
  `RecordEditProgress` / `EditJournals` show the ownership checks
  (`session_id` + `repository`) and the 76 MiB payload guard to mirror.
  `internal/session/delete.go` relies on the cascade.
- `internal/tui/tool_wiring.go` — `toolRunOptions` wires
  `EditJournal` to `store.RecordEditProgress`; the change-set hook is wired
  the same way. `showEditRecovery` renders interrupted journals on resume
  (D10 reuses it).
- `internal/tui/dialog.go` — `commandHelp`, `handleCommand` (reserved
  commands; `/plan` shows the `m.working || m.pending != nil` refusal
  pattern). `internal/tui/tui.go` — `runDraftCommandRefusal`,
  `startTurn` (where the undo note joins the next request, T7),
  `m.snapshot.History`.
- `internal/tui/commandcomplete.go` — the `/` autocomplete list.

## Specs

- [v1-spec](../v1-spec.md) FR-05 (confinement, symlink escapes), FR-06/07
  (review before any write, stale refusal), FR-09 (honest results), FR-10
  (no silent replay after relaunch).
- [approve-always](../approve-always/spec.md) — the edit grant undo must not
  extend.
- [targeted-edits](../targeted-edits/spec.md) and
  [repo-init](../repo-init/spec.md) — the two edit tools and `/init`'s
  always-ask rule.
- [feature comparison](../tooling-landscape/feature-comparison.md) Tier 1
  item 5 — the original proposal.

## Prior art

- OpenCode V2: git-worktree snapshots; `/undo`, `/redo`, per-message
  revert. Heavier (captures command effects) — not chosen (interview).
- OMP: opt-in `checkpoint`/`rewind` conversation-state pair.
- Claude Code: checkpoints that rewind files edited by its edit tools;
  shell effects are not tracked — the same honesty line as decision 4.
