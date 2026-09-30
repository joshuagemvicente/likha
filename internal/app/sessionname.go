package app

import (
	"context"
	"errors"
	"strings"

	"lisa/internal/model"
)

// nameInstruction is the user message appended to the first turn to ask the
// model for a short session name.
const nameInstruction = `Generate a session name for the conversation above. 2 to 6 words. Capture the task or topic. Output ONLY the name: no quotes, no surrounding punctuation, no trailing period, no explanation.`

// generateSessionName produces a short session name from the first completed
// turn with ONE plain model call. The input history is never modified; on any
// failure the caller keeps the derived title and shows nothing.
func generateSessionName(ctx context.Context, client *model.Client, history []model.Message) (string, error) {
	if client == nil {
		return "", errors.New("no model client configured")
	}
	hasUser, hasAssistant := false, false
	for _, msg := range history {
		switch msg.Role {
		case "user":
			hasUser = true
		case "assistant":
			hasAssistant = true
		}
	}
	if !hasUser || !hasAssistant {
		return "", errors.New("need a completed turn: at least one user and one assistant message")
	}

	// Copy the input so the caller's slice is never mutated; the appended
	// instruction could otherwise run past its backing array.
	msgs := make([]model.Message, 0, len(history)+1)
	msgs = append(msgs, history...)
	msgs = append(msgs, model.Message{Role: "user", Content: nameInstruction})

	assistant, err := client.Stream(ctx, msgs, nil, nil, nil)
	if err != nil {
		return "", err
	}
	return sanitizeSessionName(assistant.Content)
}

// sanitizeSessionName normalizes the model's raw output and rejects anything
// that cannot serve as a session name.
func sanitizeSessionName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	// Drop a trailing period before quote-stripping so a quoted, period-suffixed
	// name exposes its wrapping quotes, and again after, for `"name."` output.
	if strings.HasSuffix(name, ".") {
		name = strings.TrimSuffix(name, ".")
	}
	// Strip one layer of wrapping double or single quotes.
	if len(name) >= 2 &&
		((name[0] == '"' && name[len(name)-1] == '"') ||
			(name[0] == '\'' && name[len(name)-1] == '\'')) {
		name = name[1 : len(name)-1]
	}
	// Collapse every whitespace run, including newlines, to single spaces.
	name = strings.Join(strings.Fields(name), " ")
	if strings.HasSuffix(name, ".") {
		name = strings.TrimSuffix(name, ".")
	}
	if name == "" {
		return "", errors.New("model returned an empty session name")
	}
	if n := len([]rune(name)); n > 60 {
		return "", errors.New("session name too long: " + name)
	}
	words := strings.Fields(name)
	if len(words) < 2 || len(words) > 6 {
		return "", errors.New("session name must be 2 to 6 words: " + name)
	}
	return name, nil
}
