package tui

import (
	"errors"

	"likha/internal/model"
)

// replayRejectedHint is the reasoning-replay hint of specs/reasoning-replay
// rule 12. Every later request repeats the same history, so /compact is the
// way out.
const replayRejectedHint = "Earlier turns' reasoning state may be missing or not accepted by this model; /compact continues from a summary."

// replayRejectedNote returns the hint appended to a turn error when the
// rejected request lacked required reasoning state or carried some, else "".
func replayRejectedNote(err error) string {
	var rejected *model.ReplayRejectedError
	if !errors.As(err, &rejected) {
		return ""
	}
	return replayRejectedHint
}
