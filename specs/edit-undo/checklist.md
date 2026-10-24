# Checklist: Undo Likha's edits

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md). Checked items are covered by
automated tests (named in brackets); none has been confirmed in a real
terminal yet (W1).

## Recording

- [x] Every applied `edit_file` and `edit` call on the main agent becomes one
      undoable change set, including Approve always and `/init` edits.
      [`agent.TestAppliedEditsRecordChangeSets`; `/init` uses the same
      `edit_file` path]
- [x] A failed, cancelled or unresolved apply is not undoable.
      [`actions.TestFailedApplyYieldsNoChangeSet`,
      `agent.TestRejectedEditRecordsNoChangeSet`]
- [x] Saved original content never reaches the model or the repository.
      [`tui.TestUndoReviewRevertsNewestChangeSet` checks the model note;
      originals live only in `sessions.sqlite`]
- [x] Only the 20 newest change sets and 64 MiB of originals are kept per
      session; an oversized set is applied but marked not undoable.
      [`session.TestChangeSetsPruneOldestByCount`,
      `session.TestPruneChangeSetsByBytesKeepsHistoryContiguous`,
      `session.TestChangeSetTooLargeIsRefused`,
      `agent.TestChangeSetRecordingFailureKeepsEditApplied`]

## `/undo`

- [x] `/undo` opens a review with the reverse diff and **Undo**/**Cancel**;
      Approve always never applies to it.
      [`tui.TestUndoReviewRevertsNewestChangeSet`]
- [x] Undo restores replaced files' content and permissions, removes files
      Likha created, and removes created directories that are empty.
      [`actions.TestUndoRestoresReplacedFileContentAndMode`,
      `actions.TestUndoMultiFileSetRemovesCreatedFilesAndEmptyDirectories`,
      `actions.TestUndoKeepsNonEmptyCreatedDirectory`]
- [x] Repeated `/undo` takes change sets newest first, then prints
      `Nothing to undo.` [`tui.TestUndoRepeatsNewestFirst`]
- [x] If any file in the set changed since Likha wrote it, nothing is
      written and every changed path is named.
      [`actions.TestPrepareUndoReportsChangedFiles`,
      `actions.TestPrepareUndoRefusesWholeSetWhenOneFileChanged`,
      `actions.TestUndoRefusesFileChangedDuringReview`,
      `tui.TestUndoRefusesFileChangedSinceLikhaWroteIt`]
- [x] Same content saved through a rename does not block undo.
      [`actions.TestPrepareUndoReportsChangedFiles`]
- [x] Cancel leaves the set undoable. [`tui.TestUndoCancelKeepsChangeSet`]
- [x] The success entry says effects of shell commands are not undone; the
      review warns when commands ran after the change.
      [`tui.TestUndoReviewRevertsNewestChangeSet`,
      `tui.TestUndoReviewNamesCommandsThatRanAfterTheChange`]
- [x] Symlink and outside-repository paths are refused, never followed.
      [`actions.TestPrepareUndoReportsChangedFiles` (parent and file
      replaced by symlinks)]

## Model, session and availability

- [x] The next model request carries an undo note naming paths only.
      [`tui.TestUndoReviewRevertsNewestChangeSet`: the note is the newest
      history message and is persisted; history is what the next request
      sends]
- [x] Undo history survives resume and is deleted with its session.
      [`tui.TestUndoWorksAfterResume`,
      `session.TestChangeSetsCascadeOnSessionDelete`,
      `session.TestDatabaseWithoutUndoHistoryMigrates`]
- [x] An interrupted undo is reported on resume, never replayed.
      [`actions.TestUndoJournalsProgressToCompletion`,
      `actions.TestUndoRecoversRemovalWhenJournalFailsMidPublication`; the
      revert writes the existing private edit journal, which
      `showEditRecovery` reports on resume]
- [x] `/undo` refuses during a run or pending review, takes no arguments,
      and appears in `/help` and autocomplete.
      [`tui.TestUndoArgumentsAndBusyStatesRefuse`,
      `tui.TestUndoListedInHelpAndAutocomplete`]

## Verification record

- [x] Automated suite green (`go test ./...`, `go vet ./...`, `gofmt`),
      plus `go test -race` on actions, session, agent and tui. Under `-race`,
      the unrelated `TestAssistantMarkdownRunsShareStyles` fails (also when
      run alone); it passes without `-race`.
- [ ] Real-terminal walkthrough (tasks W1).
