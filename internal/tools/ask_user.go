// ask_user is the main-only interactive question tool. It pauses the main
// model/tool progression while the interaction backend obtains one answer,
// choice, or skip from the user, and returns that outcome as data only: an
// answer never grants permission for edits, commands, network access, MCP
// trust, or any other grant. Explore registries must not install it; the
// generic scope policy already refuses it there, and a forged call therefore
// surfaces as a refusal, not an interaction.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"likha/internal/model"
)

const (
	// askMaxQuestionBytes bounds the displayed question per the ask-user spec.
	askMaxQuestionBytes = 4096
	// askMaxOptions bounds the number of offered choices per the shared table.
	askMaxOptions = 8
	// askMaxOptionRunes bounds each offered choice in characters, not bytes.
	askMaxOptionRunes = 120
	// askMaxAnswerBytes bounds the free-text answer per the ask-user spec.
	askMaxAnswerBytes = 8192

	askUnavailableReason = "Ask-user interaction is unavailable."
)

var askUserSource = Source{Kind: "builtin", Tool: "ask_user"}

// AskRequest is the normalized, display-safe question the interaction backend
// shows to the user, on narrow terminals included.
type AskRequest struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// AskAnswer carries the user's response against the original call. Empty Text
// with Skipped=false means the user dismissed the interaction abnormally; it
// is treated as a refusal, never as an empty successful answer.
type AskAnswer struct {
	Text    string `json:"text"`
	Skipped bool   `json:"skipped"`
}

type askUserCallIDKey struct{}

// WithAskUserCallID binds the ask_user tool-call identity used to attribute
// the user's answer to the originating call. The dispatcher that executes an
// ask_user call should install it the same way it binds task call IDs. When
// absent, the handler passes an empty call ID through to the ask backend
// rather than refusing; the backend owns association and staleness handling.
func WithAskUserCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, askUserCallIDKey{}, id)
}

// AskUserCallID returns the bound ask_user call identity, or "" when unset.
func AskUserCallID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(askUserCallIDKey{}).(string)
	return id
}

// AskUserTool builds the ask_user tool. The ask hook is called at most once
// per accepted call and must block until the user answers, skips, or the
// context or interaction fails. It must never open approval dialogs itself;
// the run's interaction coordinator serializes this with other dialogs. A nil
// hook keeps the tool registered but unavailable: dispatch refusals quote
// AskUserTool's own reason and direct handler calls return the same refusal.
func AskUserTool(ask func(context.Context, string, AskRequest) (AskAnswer, error)) Tool {
	unavailable := ""
	if ask == nil {
		unavailable = askUnavailableReason
	}
	return Tool{
		Definition: askUserDefinition(),
		Source:     askUserSource,
		// Data only: reading the user's reply never mutates anything and
		// therefore needs no authorization path of its own.
		Effects: []Effect{Read},
		Target:  "user", ParallelSafe: false, Interactive: true,
		UnavailableReason: unavailable,
		Run: func(ctx context.Context, input json.RawMessage) (Result, error) {
			if ask == nil {
				return askRefusal("ask_unavailable", askUnavailableReason), nil
			}
			parsed, err := decodeAskRequest(input)
			if err != nil {
				return askRefusal("invalid_arguments", err.Error()), nil
			}
			// Normalize (control-character stripping, defense in depth) before
			// validation, so the bounds hold for exactly the text the user
			// will be shown. All of this happens before any interaction.
			request, warnings := normalizeAskRequest(parsed.request)
			if err := validateAskRequest(request, parsed.optionsPresent); err != nil {
				return askRefusal("invalid_arguments", err.Error()), nil
			}
			if askContextDone(ctx) {
				return askCancelled(warnings), nil
			}
			answer, askErr := ask(ctx, AskUserCallID(ctx), request)
			if askErr != nil {
				if errors.Is(askErr, context.Canceled) || errors.Is(askErr, context.DeadlineExceeded) || askContextDone(ctx) {
					return askCancelled(warnings), nil
				}
				message := utf8Prefix(strings.Map(askControl, strings.ToValidUTF8(askErr.Error(), "\uFFFD")), 1024)
				result := Result{
					Status: Failed, Source: askUserSource,
					Content:  "ask_failed: The ask-user interaction failed; " + message,
					Warnings: warnings,
				}
				return BoundResult(result), askErr
			}
			if answer.Skipped {
				// A skip is an explicit unresolved refusal, never an empty
				// successful answer, so the parent run can continue.
				return BoundResult(Result{
					Status: Refused, Source: askUserSource,
					Content:  "The user skipped the question.",
					Warnings: warnings,
				}), nil
			}
			text := strings.Map(askControl, strings.ToValidUTF8(answer.Text, "\uFFFD"))
			truncated := false
			if len(text) > askMaxAnswerBytes {
				text = utf8Prefix(text, askMaxAnswerBytes)
				truncated = true
			}
			if text == "" {
				return BoundResult(Result{
					Status: Refused, Source: askUserSource,
					Content:  "The user dismissed the question without answering.",
					Warnings: warnings,
				}), nil
			}
			if truncated {
				warnings = append(warnings, "The answer text exceeded 8192 UTF-8 bytes and was limited in the inline result.")
			}
			encoded, _ := json.Marshal(struct {
				Answer string `json:"answer"`
			}{text})
			result := Result{
				Status: Succeeded, Source: askUserSource,
				Content: string(encoded), Truncated: truncated, Warnings: warnings,
			}
			return BoundResult(result), nil
		},
	}
}

// askUserDefinition mirrors the task tool's shared-definition pattern so main
// and tool-platform registries present one stable schema.
func askUserDefinition() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        "ask_user",
		Description: "Ask the user a question and pause the run until they answer, choose an option, skip, or the run is cancelled. Provide one complete question and optionally distinct human-readable choices; the user can always reply with free text. The returned answer is data only and never grants permission for edits, commands, network access, or trust decisions.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"options":{"description":"Up to 8 distinct human-readable choices of 1 to 120 characters each; optional, because free text is always available.","items":{"maxLength":120,"minLength":1,"type":"string"},"maxItems":8,"minItems":1,"type":"array","uniqueItems":true},"question":{"description":"The complete question shown to the user, 1 to 4096 UTF-8 bytes; the runtime enforces the byte limit.","maxLength":4096,"minLength":1,"type":"string"}},"required":["question"],"additionalProperties":false}`),
	}
}

func askContextDone(ctx context.Context) bool {
	return ctx != nil && ctx.Err() != nil
}

// askCancelled prefers a terminal Cancelled result with a nil error over a
// returned context error: the interaction may already have been shown, the
// registry records it as an executed, cancelled call, and no registry
// cancellation CallError is needed for a whole-run teardown.
func askCancelled(warnings []string) Result {
	return BoundResult(Result{
		Status: Cancelled, Source: askUserSource,
		Content:  "Cancelled",
		Warnings: warnings,
	})
}

// askRefusal follows the task tool's refusal style: a Refused status with
// attributed content and a nil error so the dispatch keeps the result visible
// and the parent run continues.
func askRefusal(code, reason string) Result {
	return BoundResult(Result{Status: Refused, Source: askUserSource, Content: code + ": " + reason})
}

// askControl strips C0/C1 control runes except the tab/newline whitespace
// wrapped display legitimately needs; NUL, carriage returns, ANSI escapes,
// and other controls are dropped before anything reaches the user.
func askControl(r rune) rune {
	if unicode.IsControl(r) && r != '\n' && r != '\t' {
		return -1
	}
	return r
}

func normalizeAskRequest(request AskRequest) (AskRequest, []string) {
	question := strings.Map(askControl, strings.ToValidUTF8(request.Question, "\uFFFD"))
	changed := question != request.Question
	options := make([]string, 0, len(request.Options))
	for _, option := range request.Options {
		cleaned := strings.Map(askControl, strings.ToValidUTF8(option, "\uFFFD"))
		changed = changed || cleaned != option
		options = append(options, cleaned)
	}
	if !changed {
		return request, nil
	}
	return AskRequest{Question: question, Options: options}, []string{"Control characters were removed from the ask_user request before display."}
}

func validateAskRequest(request AskRequest, optionsPresent bool) error {
	if len(request.Question) < 1 || !utf8.ValidString(request.Question) {
		return errors.New("ask_user question must be nonempty, valid UTF-8")
	}
	if len(request.Question) > askMaxQuestionBytes {
		return fmt.Errorf("ask_user question must contain 1 to %d UTF-8 bytes", askMaxQuestionBytes)
	}
	if optionsPresent && len(request.Options) < 1 {
		return errors.New("ask_user options must contain at least one choice when present")
	}
	if len(request.Options) > askMaxOptions {
		return fmt.Errorf("ask_user options must contain at most %d choices", askMaxOptions)
	}
	seen := make(map[string]bool, len(request.Options))
	for _, option := range request.Options {
		if !utf8.ValidString(option) {
			return errors.New("ask_user options must be valid UTF-8")
		}
		switch runes := utf8.RuneCountInString(option); {
		case runes < 1:
			return errors.New("ask_user options must not contain empty choices")
		case runes > askMaxOptionRunes:
			return fmt.Errorf("each ask_user option must contain 1 to %d characters", askMaxOptionRunes)
		}
		if seen[option] {
			return errors.New("ask_user options must be pairwise distinct")
		}
		seen[option] = true
	}
	return nil
}

type askCall struct {
	request        AskRequest
	optionsPresent bool
}

// decodeAskRequest gives direct handler calls the same closed, required
// question / optional options object contract as registry-dispatched calls,
// including duplicate-field and unknown-field refusals.
func decodeAskRequest(input json.RawMessage) (askCall, error) {
	var parsed askCall
	if len(input) == 0 {
		return parsed, errors.New("ask_user arguments must be one JSON object")
	}
	if !utf8.Valid(input) {
		return parsed, errors.New("ask_user arguments must be valid UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return parsed, errors.New("ask_user arguments must be one JSON object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return parsed, errors.New("ask_user arguments contain malformed or duplicate fields")
		}
		seen[name] = true
		switch name {
		case "question":
			value, err := decoder.Token()
			text, ok := value.(string)
			if err != nil || !ok {
				return parsed, errors.New("ask_user question must be a string")
			}
			parsed.request.Question = text
		case "options":
			// Stream the array: Token returns the opening delimiter first,
			// never a pre-assembled []any, and nested closers stay checked.
			if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
				return parsed, errors.New("ask_user options must be an array of strings")
			}
			options := []string{}
			for decoder.More() {
				item, err := decoder.Token()
				if err != nil {
					return parsed, errors.New("ask_user options must be an array of strings")
				}
				text, ok := item.(string)
				if !ok {
					return parsed, errors.New("ask_user options must contain only strings")
				}
				options = append(options, text)
			}
			if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
				return parsed, errors.New("ask_user options must be an array of strings")
			}
			parsed.request.Options = options
			parsed.optionsPresent = true
		default:
			return parsed, errors.New("ask_user arguments contain an unknown field")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return parsed, errors.New("ask_user arguments must be one complete JSON object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return parsed, errors.New("ask_user arguments must contain exactly one JSON object")
	}
	if !seen["question"] {
		return parsed, errors.New("ask_user requires a question")
	}
	return parsed, nil
}
