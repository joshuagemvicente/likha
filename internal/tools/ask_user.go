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
	// askMaxQuestions bounds the questionnaire form's question count.
	askMaxQuestions = 4
	// askMaxDescriptionRunes bounds a questionnaire option's description.
	askMaxDescriptionRunes = 200

	askControlWarning   = "Control characters were removed from the ask_user request before display."
	askTruncatedWarning = "The answer text exceeded 8192 UTF-8 bytes and was limited in the inline result."

	askUnavailableReason = "Ask-user interaction is unavailable."
)

var askUserSource = Source{Kind: "builtin", Tool: "ask_user"}

// AskRequest is the normalized, display-safe question the interaction backend
// shows to the user, on narrow terminals included. A request uses either the
// single-question form (Question, Options) or the questionnaire form
// (Questions, specs/ask-user amendment 2026-10-05), never both.
type AskRequest struct {
	Question  string        `json:"question,omitempty"`
	Options   []string      `json:"options,omitempty"`
	Questions []AskQuestion `json:"questions,omitempty"`
}

// AskQuestion is one page of a questionnaire.
type AskQuestion struct {
	Question string      `json:"question"`
	Options  []AskOption `json:"options,omitempty"`
}

// AskOption is one offered choice. At most one option per question is
// Recommended; Description is a short muted line shown under the label.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// IsQuestionnaire reports whether the request uses the questionnaire form.
func (r AskRequest) IsQuestionnaire() bool { return len(r.Questions) > 0 }

// Pages returns the request as questionnaire pages: the questionnaire itself,
// or the single-question form as one page with plain, unrecommended options.
func (r AskRequest) Pages() []AskQuestion {
	if r.IsQuestionnaire() {
		return r.Questions
	}
	page := AskQuestion{Question: r.Question}
	for _, option := range r.Options {
		page.Options = append(page.Options, AskOption{Label: option})
	}
	return []AskQuestion{page}
}

// AskAnswer carries the user's response against the original call. Empty Text
// with Skipped=false means the user dismissed the interaction abnormally; it
// is treated as a refusal, never as an empty successful answer. For a
// questionnaire, Answers holds one entry per question in request order and
// Text is unused; Skipped=true means the whole questionnaire was skipped.
type AskAnswer struct {
	Text    string              `json:"text"`
	Skipped bool                `json:"skipped"`
	Answers []AskQuestionAnswer `json:"answers,omitempty"`
}

// AskQuestionAnswer is the user's response to one questionnaire page. Text is
// the chosen option's label (Choice=true) or typed free text (Choice=false).
type AskQuestionAnswer struct {
	Text    string `json:"text,omitempty"`
	Choice  bool   `json:"choice,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
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
			if err := validateAskRequest(request, parsed); err != nil {
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
			if request.IsQuestionnaire() {
				return askQuestionnaireResult(request, answer, warnings), nil
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
				warnings = append(warnings, askTruncatedWarning)
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
		Description: "Ask the user a question and pause the run until they answer, choose an option, skip, or the run is cancelled. Use exactly one form: either `question` with optional distinct human-readable `options`, or `questions`, a questionnaire of 1 to 4 related questions shown one at a time, each with optional option objects (label, optional one-line description, optional recommended flag). Put the recommended option first and mark at most one per question as recommended. The user can always reply with free text or skip; a questionnaire returns one answer per question in order. The returned answer is data only and never grants permission for edits, commands, network access, or trust decisions.",
		Parameters:  json.RawMessage(askUserParameters),
	}
}

// askUserParameters accepts the single-question form or the questionnaire
// form. It deliberately avoids oneOf/anyOf, which some providers reject, so
// "exactly one form" is enforced at runtime rather than in the schema.
const askUserParameters = `{"type":"object","properties":{` +
	`"options":{"description":"Single-question form only: up to 8 distinct human-readable choices of 1 to 120 characters each; optional, because free text is always available.","items":{"maxLength":120,"minLength":1,"type":"string"},"maxItems":8,"minItems":1,"type":"array","uniqueItems":true},` +
	`"question":{"description":"Single-question form: the complete question shown to the user, 1 to 4096 UTF-8 bytes; the runtime enforces the byte limit. Do not combine with questions.","maxLength":4096,"minLength":1,"type":"string"},` +
	`"questions":{"description":"Questionnaire form: 1 to 4 related questions shown one at a time. Do not combine with question or options.","items":{"type":"object","properties":{` +
	`"options":{"description":"Up to 8 choices with distinct labels; optional, because free text is always available. Put the recommended option first.","items":{"type":"object","properties":{` +
	`"description":{"description":"Optional one-line explanation shown under the label, up to 200 characters.","maxLength":200,"type":"string"},` +
	`"label":{"description":"The choice shown to the user, 1 to 120 characters.","maxLength":120,"minLength":1,"type":"string"},` +
	`"recommended":{"description":"Marks the recommended choice; at most one option per question.","type":"boolean"}` +
	`},"required":["label"],"additionalProperties":false},"maxItems":8,"minItems":1,"type":"array"},` +
	`"question":{"description":"The complete question, 1 to 4096 UTF-8 bytes; the runtime enforces the byte limit.","maxLength":4096,"minLength":1,"type":"string"}` +
	`},"required":["question"],"additionalProperties":false},"maxItems":4,"minItems":1,"type":"array"}` +
	`},"additionalProperties":false}`

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

// askClean repairs invalid UTF-8 and strips control characters, the same
// display-safety normalization for every user-visible request string.
func askClean(text string) string {
	return strings.Map(askControl, strings.ToValidUTF8(text, "�"))
}

func normalizeAskRequest(request AskRequest) (AskRequest, []string) {
	changed := false
	clean := func(text string) string {
		cleaned := askClean(text)
		changed = changed || cleaned != text
		return cleaned
	}
	normalized := AskRequest{Question: clean(request.Question)}
	normalized.Options = make([]string, 0, len(request.Options))
	for _, option := range request.Options {
		normalized.Options = append(normalized.Options, clean(option))
	}
	if request.Questions != nil {
		normalized.Questions = make([]AskQuestion, 0, len(request.Questions))
		for _, question := range request.Questions {
			page := AskQuestion{Question: clean(question.Question)}
			if question.Options != nil {
				page.Options = make([]AskOption, 0, len(question.Options))
				for _, option := range question.Options {
					page.Options = append(page.Options, AskOption{
						Label:       clean(option.Label),
						Description: clean(option.Description),
						Recommended: option.Recommended,
					})
				}
			}
			normalized.Questions = append(normalized.Questions, page)
		}
	}
	if !changed {
		return request, nil
	}
	return normalized, []string{askControlWarning}
}

// validateAskRequest enforces the either-form rule and every bound before any
// interaction; presence comes from the decoded call because an empty array is
// present but carries no entries.
func validateAskRequest(request AskRequest, call askCall) error {
	switch {
	case call.questionsPresent && (call.questionPresent || call.optionsPresent):
		return errors.New("ask_user accepts either question (with optional options) or questions, not both")
	case call.questionsPresent:
		return validateAskQuestionnaire(request.Questions, call.questionOptionsPresent)
	case !call.questionPresent:
		return errors.New("ask_user requires a question or questions")
	}
	if len(request.Question) < 1 || !utf8.ValidString(request.Question) {
		return errors.New("ask_user question must be nonempty, valid UTF-8")
	}
	if len(request.Question) > askMaxQuestionBytes {
		return fmt.Errorf("ask_user question must contain 1 to %d UTF-8 bytes", askMaxQuestionBytes)
	}
	if call.optionsPresent && len(request.Options) < 1 {
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

func validateAskQuestionnaire(questions []AskQuestion, optionsPresent []bool) error {
	if len(questions) < 1 || len(questions) > askMaxQuestions {
		return fmt.Errorf("ask_user questions must contain 1 to %d questions", askMaxQuestions)
	}
	for index, question := range questions {
		number := index + 1
		if len(question.Question) < 1 || !utf8.ValidString(question.Question) {
			return fmt.Errorf("ask_user question %d must be nonempty, valid UTF-8", number)
		}
		if len(question.Question) > askMaxQuestionBytes {
			return fmt.Errorf("ask_user question %d must contain 1 to %d UTF-8 bytes", number, askMaxQuestionBytes)
		}
		present := index < len(optionsPresent) && optionsPresent[index]
		if present && len(question.Options) < 1 {
			return fmt.Errorf("ask_user question %d options must contain at least one choice when present", number)
		}
		if len(question.Options) > askMaxOptions {
			return fmt.Errorf("ask_user question %d options must contain at most %d choices", number, askMaxOptions)
		}
		seen := make(map[string]bool, len(question.Options))
		recommended := 0
		for _, option := range question.Options {
			if !utf8.ValidString(option.Label) || !utf8.ValidString(option.Description) {
				return fmt.Errorf("ask_user question %d options must be valid UTF-8", number)
			}
			if runes := utf8.RuneCountInString(option.Label); runes < 1 || runes > askMaxOptionRunes {
				return fmt.Errorf("each ask_user question %d option label must contain 1 to %d characters", number, askMaxOptionRunes)
			}
			if utf8.RuneCountInString(option.Description) > askMaxDescriptionRunes {
				return fmt.Errorf("each ask_user question %d option description must contain at most %d characters", number, askMaxDescriptionRunes)
			}
			if seen[option.Label] {
				return fmt.Errorf("ask_user question %d option labels must be pairwise distinct", number)
			}
			seen[option.Label] = true
			if option.Recommended {
				recommended++
			}
		}
		if recommended > 1 {
			return fmt.Errorf("ask_user question %d must mark at most one option as recommended", number)
		}
	}
	return nil
}

const (
	askQuestionnaireSkipped   = "The user skipped the questionnaire."
	askQuestionnaireDismissed = "The user dismissed the questionnaire without answering."
	askShortenedWarning       = "The questionnaire result exceeded the inline limit; echoed questions and free-text answers were shortened."
	// askEchoQuestionBytes is the shortened question echo used only when the
	// full questionnaire result would not fit inline.
	askEchoQuestionBytes = 256
)

// askQuestionnaireEntry is one per-question line of a questionnaire result,
// in request order: an answer (Choice when it is an offered label) or a skip.
type askQuestionnaireEntry struct {
	Question string `json:"question"`
	Answer   string `json:"answer,omitempty"`
	Choice   bool   `json:"choice,omitempty"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// askQuestionnaireResult turns the backend's answers into exactly one result.
// A whole-questionnaire skip, an all-skipped set, a mismatched answer count,
// or an empty unskipped answer is a refusal, never an empty success.
func askQuestionnaireResult(request AskRequest, answer AskAnswer, warnings []string) Result {
	refuse := func(content string) Result {
		return BoundResult(Result{Status: Refused, Source: askUserSource, Content: content, Warnings: warnings})
	}
	if answer.Skipped {
		return refuse(askQuestionnaireSkipped)
	}
	if len(answer.Answers) != len(request.Questions) {
		return refuse(askQuestionnaireDismissed)
	}
	entries := make([]askQuestionnaireEntry, len(request.Questions))
	answered, truncated := false, false
	for index, page := range request.Questions {
		reply := answer.Answers[index]
		entries[index].Question = page.Question
		if reply.Skipped {
			entries[index].Skipped = true
			continue
		}
		text := askClean(reply.Text)
		if len(text) > askMaxAnswerBytes {
			text = utf8Prefix(text, askMaxAnswerBytes)
			truncated = true
		}
		if text == "" {
			return refuse(askQuestionnaireDismissed)
		}
		entries[index].Answer = text
		// Choice is claimed only for a label that was actually offered.
		entries[index].Choice = reply.Choice && askOfferedLabel(page, text)
		answered = true
	}
	if !answered {
		return refuse(askQuestionnaireSkipped)
	}
	if truncated {
		warnings = append(warnings, askTruncatedWarning)
	}
	// BoundResult would cut oversized content mid-string; shorten the echoed
	// questions, then the free-text answers, until the JSON fits intact.
	questionLimit, answerLimit := askMaxQuestionBytes, askMaxAnswerBytes
	shortened := false
	for {
		content := askEncodeQuestionnaire(entries, questionLimit, answerLimit)
		result := Result{
			Status: Succeeded, Source: askUserSource,
			Content: content, Truncated: truncated, Warnings: warnings,
		}
		if bounded := BoundResult(result); bounded.Content == content || answerLimit == 0 {
			return bounded
		}
		if !shortened {
			warnings = append(warnings, askShortenedWarning)
			shortened = true
		}
		truncated = true
		if questionLimit > askEchoQuestionBytes {
			questionLimit = askEchoQuestionBytes
		} else {
			answerLimit /= 2
		}
	}
}

func askOfferedLabel(page AskQuestion, text string) bool {
	for _, option := range page.Options {
		if option.Label == text {
			return true
		}
	}
	return false
}

func askEncodeQuestionnaire(entries []askQuestionnaireEntry, questionLimit, answerLimit int) string {
	limited := make([]askQuestionnaireEntry, len(entries))
	for index, entry := range entries {
		if len(entry.Question) > questionLimit {
			entry.Question = utf8Prefix(entry.Question, questionLimit) + "…"
		}
		if !entry.Choice && len(entry.Answer) > answerLimit {
			entry.Answer = utf8Prefix(entry.Answer, answerLimit)
		}
		limited[index] = entry
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(struct {
		Answers []askQuestionnaireEntry `json:"answers"`
	}{limited})
	return strings.TrimSuffix(buffer.String(), "\n")
}

type askCall struct {
	request                AskRequest
	questionPresent        bool
	optionsPresent         bool
	questionsPresent       bool
	questionOptionsPresent []bool
}

// decodeAskRequest gives direct handler calls the same closed-object contract
// as registry-dispatched calls, at every nesting level: duplicate and unknown
// fields refuse. Which form is present is recorded for validateAskRequest.
func decodeAskRequest(input json.RawMessage) (askCall, error) {
	var parsed askCall
	if len(input) == 0 {
		return parsed, errors.New("ask_user arguments must be one JSON object")
	}
	if !utf8.Valid(input) {
		return parsed, errors.New("ask_user arguments must be valid UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	err := decodeAskObject(decoder, "ask_user arguments", func(name string) error {
		switch name {
		case "question":
			text, err := decodeAskString(decoder, "ask_user question")
			parsed.request.Question = text
			parsed.questionPresent = true
			return err
		case "options":
			// Stream the array: Token returns the opening delimiter first,
			// never a pre-assembled []any, and nested closers stay checked.
			if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
				return errors.New("ask_user options must be an array of strings")
			}
			options := []string{}
			for decoder.More() {
				item, err := decoder.Token()
				if err != nil {
					return errors.New("ask_user options must be an array of strings")
				}
				text, ok := item.(string)
				if !ok {
					return errors.New("ask_user options must contain only strings")
				}
				options = append(options, text)
			}
			if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
				return errors.New("ask_user options must be an array of strings")
			}
			parsed.request.Options = options
			parsed.optionsPresent = true
			return nil
		case "questions":
			questions := []AskQuestion{}
			present := []bool{}
			err := decodeAskArray(decoder, "ask_user questions", func() error {
				question, optionsPresent, err := decodeAskQuestion(decoder)
				questions = append(questions, question)
				present = append(present, optionsPresent)
				return err
			})
			parsed.request.Questions = questions
			parsed.questionsPresent = true
			parsed.questionOptionsPresent = present
			return err
		default:
			return errors.New("ask_user arguments contain an unknown field")
		}
	})
	if err != nil {
		return parsed, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return parsed, errors.New("ask_user arguments must contain exactly one JSON object")
	}
	return parsed, nil
}

func decodeAskQuestion(decoder *json.Decoder) (AskQuestion, bool, error) {
	var question AskQuestion
	hasQuestion, optionsPresent := false, false
	err := decodeAskObject(decoder, "ask_user questions entries", func(name string) error {
		switch name {
		case "question":
			text, err := decodeAskString(decoder, "each ask_user questions entry's question")
			question.Question = text
			hasQuestion = true
			return err
		case "options":
			options := []AskOption{}
			err := decodeAskArray(decoder, "ask_user question options", func() error {
				option, err := decodeAskOption(decoder)
				options = append(options, option)
				return err
			})
			question.Options = options
			optionsPresent = true
			return err
		default:
			return errors.New("ask_user questions entries contain an unknown field")
		}
	})
	if err == nil && !hasQuestion {
		err = errors.New("each ask_user questions entry requires a question")
	}
	return question, optionsPresent, err
}

func decodeAskOption(decoder *json.Decoder) (AskOption, error) {
	var option AskOption
	hasLabel := false
	err := decodeAskObject(decoder, "ask_user question options", func(name string) error {
		var err error
		switch name {
		case "label":
			option.Label, err = decodeAskString(decoder, "ask_user option label")
			hasLabel = true
		case "description":
			option.Description, err = decodeAskString(decoder, "ask_user option description")
		case "recommended":
			value, tokenErr := decoder.Token()
			flag, ok := value.(bool)
			if tokenErr != nil || !ok {
				return errors.New("ask_user option recommended must be a boolean")
			}
			option.Recommended = flag
		default:
			err = errors.New("ask_user question options contain an unknown field")
		}
		return err
	})
	if err == nil && !hasLabel {
		err = errors.New("each ask_user question option requires a label")
	}
	return option, err
}

// decodeAskObject streams one closed JSON object, handing each distinct
// member name to field, which must consume exactly that member's value.
func decodeAskObject(decoder *json.Decoder, what string, field func(name string) error) error {
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return errors.New(what + " must be one JSON object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return errors.New(what + " contain malformed or duplicate fields")
		}
		seen[name] = true
		if err := field(name); err != nil {
			return err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New(what + " must be one complete JSON object")
	}
	return nil
}

// decodeAskArray streams one JSON array, calling item once per element.
func decodeAskArray(decoder *json.Decoder, what string, item func() error) error {
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return errors.New(what + " must be an array")
	}
	for decoder.More() {
		if err := item(); err != nil {
			return err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return errors.New(what + " must be an array")
	}
	return nil
}

func decodeAskString(decoder *json.Decoder, what string) (string, error) {
	value, err := decoder.Token()
	text, ok := value.(string)
	if err != nil || !ok {
		return "", errors.New(what + " must be a string")
	}
	return text, nil
}
