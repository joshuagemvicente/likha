package agent

import (
	"context"
	"errors"
	"math"

	"likha/internal/model"
)

// autoCompactPercent is the share of a known context window at which a
// main-turn request is compacted before it is sent (specs/auto-compaction
// decision 1). It is fixed; there is no user setting for it.
const autoCompactPercent = 85

// autoCompactFocus steers the automatic summary toward the run in progress
// (specs/auto-compaction D4), the way a /compact focus argument would.
const autoCompactFocus = "the task in progress: what is already done and what remains"

// autoCompactNote follows the kept user prompts so the model resumes the
// run instead of starting it over (spec point 7).
const autoCompactNote = "Work already done in this run is in the summary above; continue from it without repeating completed steps."

// errNothingToCompact reports that the history holds only the run's own
// user prompts, which automatic compaction keeps verbatim, so a summary
// would replace nothing.
var errNothingToCompact = errors.New("nothing earlier to summarize")

// shouldAutoCompact reports whether a request estimated at tokens reaches
// autoCompactPercent of window. An unknown estimate or window never
// triggers (D1): Likha does not compact on a guess.
func shouldAutoCompact(tokens int64, known bool, window int64) bool {
	if !known || window <= 0 || tokens <= 0 {
		return false
	}
	if tokens > math.MaxInt64/100 {
		return true
	}
	return tokens*100 >= autoCompactPercent*window
}

// contextPercent is tokens as a whole percentage of window, rounded to the
// nearest point, as the status line shows it.
func contextPercent(tokens, window int64) int {
	if window <= 0 {
		return 0
	}
	return int(math.Round(float64(tokens) / float64(window) * 100))
}

// compactRun summarizes history with the /compact summarizer and returns the
// replacement for a run that keeps going: the summary, then every user
// message from runStart on unchanged (the run's prompt and the steering
// prompts already delivered, D3), then autoCompactNote. The input slice is
// never modified. Usage is returned whenever the summarize request
// completed, even if its summary was rejected.
func compactRun(ctx context.Context, client *model.Client, history []model.Message, runStart int) (replacement []model.Message, summary string, usage model.RequestUsage, usageOK bool, err error) {
	kept := runUserMessages(history, runStart)
	if len(kept) == len(history) {
		return nil, "", usage, false, errNothingToCompact
	}
	brief, summary, usage, usageOK, err := CompactHistoryUsage(ctx, client, history, autoCompactFocus, nil)
	if err != nil {
		return nil, "", usage, usageOK, err
	}
	replacement = make([]model.Message, 0, len(brief)+len(kept)+1)
	replacement = append(replacement, brief...)
	replacement = append(replacement, kept...)
	replacement = append(replacement, model.Message{Role: "developer", Content: autoCompactNote})
	return replacement, summary, usage, usageOK, nil
}

// runUserMessages copies the user messages at or after runStart, in order.
func runUserMessages(history []model.Message, runStart int) []model.Message {
	var kept []model.Message
	for _, message := range history[min(max(runStart, 0), len(history)):] {
		if message.Role == "user" {
			kept = append(kept, message)
		}
	}
	return kept
}
