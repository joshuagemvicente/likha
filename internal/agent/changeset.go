package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"likha/internal/actions"
)

// ErrChangeSetTooLarge marks a RecordChangeSet failure caused by the
// session's undo history bound rather than by storage.
var ErrChangeSetTooLarge = errors.New("change set too large to undo")

// recordChangeSet saves an applied edit's change set through the session
// hook. The edit is already on disk, so a failure is never an edit failure
// (FR-09): it returns the warning that marks the edit not undoable. A storage
// failure's cause goes to the user in a notice, so it can be diagnosed; the
// model only needs to know the edit cannot be undone.
func recordChangeSet(ctx context.Context, options RunOptions, set actions.ChangeSet, emit func(TurnEvent)) []string {
	if options.RecordChangeSet == nil {
		return nil
	}
	err := options.RecordChangeSet(TaskCallID(ctx), set)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrChangeSetTooLarge) {
		return []string{"not undoable: too large"}
	}
	emit(TurnEvent{Kind: "notice", Text: fmt.Sprintf("Undo history for %s could not be saved: %v", strings.Join(set.Paths(), ", "), err)})
	return []string{"not undoable: undo history could not be saved"}
}
