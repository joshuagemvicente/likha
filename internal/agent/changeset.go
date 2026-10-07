package agent

import (
	"context"
	"errors"

	"likha/internal/actions"
)

// ErrChangeSetTooLarge marks a RecordChangeSet failure caused by the
// session's undo history bound rather than by storage.
var ErrChangeSetTooLarge = errors.New("change set too large to undo")

// recordChangeSet saves an applied edit's change set through the session
// hook. The edit is already on disk, so a failure is never an edit failure
// (FR-09): it returns the warning that marks the edit not undoable.
func recordChangeSet(ctx context.Context, options RunOptions, set actions.ChangeSet) []string {
	if options.RecordChangeSet == nil {
		return nil
	}
	if err := options.RecordChangeSet(TaskCallID(ctx), set); err != nil {
		if errors.Is(err, ErrChangeSetTooLarge) {
			return []string{"not undoable: too large"}
		}
		return []string{"not undoable: undo history could not be saved"}
	}
	return nil
}
