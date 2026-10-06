package agent

import (
	"context"
	"errors"
	"strings"

	"likha/internal/model"
)

// compactPrefix is prepended to the summary so a later model turn knows the
// conversation was summarized and where to pick the task back up.
const compactPrefix = "Conversation summary (compacted). Earlier turns are no longer available; continue the task from this summary.\n\n"

// compactInstruction is the base instruction appended as the final user
// message of the summarize call.
const compactInstruction = `Summarize the conversation above into a compact continuation brief for a coding agent. Preserve the user's goal, decisions made, files changed and how, pending next steps, and open questions. Output only the summary text.`

// compactHistory summarizes the conversation with one model call and returns
// the replacement history plus the summary text. The input slice is never
// modified. Errors leave the caller's history untouched.
func CompactHistory(ctx context.Context, client *model.Client, history []model.Message, focus string, onText func(string)) ([]model.Message, string, error) {
	replacement, summary, _, _, err := CompactHistoryUsage(ctx, client, history, focus, onText)
	return replacement, summary, err
}

// CompactHistoryUsage is CompactHistory plus the summarization request's own
// usage breakdown (usageOK=false when the response reported none), so the
// caller can add it to session spend (specs/model-metadata). Usage is
// returned whenever the request completed, even if the summary is rejected.
func CompactHistoryUsage(ctx context.Context, client *model.Client, history []model.Message, focus string, onText func(string)) (replacement []model.Message, summary string, usage model.RequestUsage, usageOK bool, err error) {
	if client == nil {
		return nil, "", usage, false, errors.New("no model client configured")
	}
	if len(history) == 0 {
		return nil, "", usage, false, errors.New("nothing to compact")
	}
	instruction := compactInstruction
	if f := strings.TrimSpace(focus); f != "" {
		instruction += " Emphasize: " + f + "."
	}
	// Copy the input so the caller's slice is never mutated; the appended
	// instruction could otherwise run past its backing array.
	summarizeMsgs := make([]model.Message, 0, len(history)+1)
	summarizeMsgs = append(summarizeMsgs, history...)
	summarizeMsgs = append(summarizeMsgs, model.Message{Role: "user", Content: instruction})

	assistant, usage, usageOK, err := client.StreamUsage(ctx, summarizeMsgs, nil, onText, nil)
	if err != nil {
		return nil, "", usage, usageOK, err
	}
	summary = strings.TrimSpace(assistant.Content)
	if summary == "" {
		return nil, "", usage, usageOK, errors.New("model returned an empty summary")
	}
	replacement = []model.Message{{
		Role:    "developer",
		Content: compactPrefix + summary,
	}}
	return replacement, summary, usage, usageOK, nil
}
