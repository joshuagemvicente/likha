# Checklist: Undo Likha's edits

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Recording

- [ ] Every applied `edit_file` and `edit` call on the main agent becomes one
      undoable change set, including Approve always and `/init` edits.
- [ ] A failed, cancelled or unresolved apply is not undoable.
- [ ] Saved original content never reaches the model or the repository.
- [ ] Only the 20 newest change sets and 64 MiB of originals are kept per
      session; an oversized set is applied but marked not undoable.

## `/undo`

- [ ] `/undo` opens a review with the reverse diff and **Undo**/**Cancel**;
      Approve always never applies to it.
- [ ] Undo restores replaced files' content and permissions, removes files
      Likha created, and removes created directories that are empty.
- [ ] Repeated `/undo` takes change sets newest first, then prints
      `Nothing to undo.`
- [ ] If any file in the set changed since Likha wrote it, nothing is
      written and every changed path is named.
- [ ] Same content saved through a rename does not block undo.
- [ ] Cancel leaves the set undoable.
- [ ] The success entry says effects of shell commands are not undone; the
      review warns when commands ran after the change.
- [ ] Symlink and outside-repository paths are refused, never followed.

## Model, session and availability

- [ ] The next model request carries an undo note naming paths only.
- [ ] Undo history survives resume and is deleted with its session.
- [ ] An interrupted undo is reported on resume, never replayed.
- [ ] `/undo` refuses during a run or pending review, takes no arguments,
      and appears in `/help` and autocomplete.

## Verification record

- [ ] Automated suite green (`go test ./...`, `go vet ./...`, `gofmt`).
- [ ] Real-terminal walkthrough (tasks W1).
