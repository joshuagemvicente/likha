package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"likha/internal/model"
)

// askRecorder is an ask backend that records every call and replies with a
// fixed answer.
type askRecorder struct {
	calls   int
	callID  string
	request AskRequest
	answer  AskAnswer
	err     error
}

func (r *askRecorder) ask(_ context.Context, callID string, request AskRequest) (AskAnswer, error) {
	r.calls++
	r.callID = callID
	r.request = request
	return r.answer, r.err
}

func runAsk(t *testing.T, recorder *askRecorder, arguments string) Result {
	t.Helper()
	result, err := AskUserTool(recorder.ask).Run(WithAskUserCallID(context.Background(), "call-1"), json.RawMessage(arguments))
	if err != nil && recorder.err == nil {
		t.Fatalf("Run returned error %v", err)
	}
	return result
}

func questionnaireArgs(t *testing.T, questions any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"questions": questions})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func walkSchema(node any, visit func(map[string]any)) {
	switch value := node.(type) {
	case map[string]any:
		visit(value)
		for _, child := range value {
			walkSchema(child, visit)
		}
	case []any:
		for _, child := range value {
			walkSchema(child, visit)
		}
	}
}

func TestAskUserDefinitionAcceptsBothFormsWithoutCombinators(t *testing.T) {
	definition := askUserDefinition()
	var schema map[string]any
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		t.Fatalf("parameters are not JSON: %v", err)
	}
	if _, ok := schema["required"]; ok {
		t.Fatalf("top-level required must be dropped so either form validates, got %v", schema["required"])
	}
	objects := 0
	walkSchema(schema, func(node map[string]any) {
		for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
			if _, ok := node[keyword]; ok {
				t.Errorf("schema must not use %s", keyword)
			}
		}
		if node["type"] == "object" {
			objects++
			if node["additionalProperties"] != false {
				t.Errorf("object schema without additionalProperties:false: %v", node)
			}
		}
	})
	if objects != 3 {
		t.Fatalf("expected closed objects at arguments, question, and option levels; got %d", objects)
	}
	properties := schema["properties"].(map[string]any)
	questions := properties["questions"].(map[string]any)
	if questions["minItems"] != float64(1) || questions["maxItems"] != float64(4) {
		t.Fatalf("questions bounds = %v..%v, want 1..4", questions["minItems"], questions["maxItems"])
	}
	entry := questions["items"].(map[string]any)
	options := entry["properties"].(map[string]any)["options"].(map[string]any)
	if options["minItems"] != float64(1) || options["maxItems"] != float64(8) {
		t.Fatalf("question options bounds = %v..%v, want 1..8", options["minItems"], options["maxItems"])
	}
	option := options["items"].(map[string]any)["properties"].(map[string]any)
	if label := option["label"].(map[string]any); label["minLength"] != float64(1) || label["maxLength"] != float64(120) {
		t.Fatalf("label bounds = %v", label)
	}
	if description := option["description"].(map[string]any); description["maxLength"] != float64(200) {
		t.Fatalf("description bound = %v", description)
	}
	if option["recommended"].(map[string]any)["type"] != "boolean" {
		t.Fatalf("recommended must be a boolean: %v", option["recommended"])
	}
	for _, phrase := range []string{"questions", "recommended option first", "free text or skip"} {
		if !strings.Contains(definition.Description, phrase) {
			t.Errorf("description does not mention %q: %s", phrase, definition.Description)
		}
	}
	if err := New().Register(AskUserTool((&askRecorder{}).ask)); err != nil {
		t.Fatalf("registry rejected the ask_user schema: %v", err)
	}
}

func TestAskUserSingleFormUnchanged(t *testing.T) {
	recorder := &askRecorder{answer: AskAnswer{Text: "yes"}}
	result := runAsk(t, recorder, `{"question":"Proceed?","options":["yes","no"]}`)
	if result.Status != Succeeded || result.Content != `{"answer":"yes"}` || len(result.Warnings) != 0 {
		t.Fatalf("single-form answer = %+v", result)
	}
	if recorder.callID != "call-1" || recorder.request.Question != "Proceed?" || strings.Join(recorder.request.Options, ",") != "yes,no" || recorder.request.IsQuestionnaire() {
		t.Fatalf("backend saw %q %+v", recorder.callID, recorder.request)
	}

	recorder = &askRecorder{answer: AskAnswer{Skipped: true}}
	if result := runAsk(t, recorder, `{"question":"Proceed?"}`); result.Status != Refused || result.Content != "The user skipped the question." {
		t.Fatalf("single-form skip = %+v", result)
	}
	recorder = &askRecorder{answer: AskAnswer{}}
	if result := runAsk(t, recorder, `{"question":"Proceed?"}`); result.Status != Refused || result.Content != "The user dismissed the question without answering." {
		t.Fatalf("single-form dismiss = %+v", result)
	}
	// Answers sent for a single-form call are ignored; Text still decides.
	recorder = &askRecorder{answer: AskAnswer{Text: "ok", Answers: []AskQuestionAnswer{{Text: "other"}}}}
	if result := runAsk(t, recorder, `{"question":"Proceed?"}`); result.Status != Succeeded || result.Content != `{"answer":"ok"}` {
		t.Fatalf("single-form with stray answers = %+v", result)
	}
	recorder = &askRecorder{answer: AskAnswer{Text: strings.Repeat("a", askMaxAnswerBytes+10)}}
	result = runAsk(t, recorder, `{"question":"Proceed?"}`)
	// Truncated results surface as Limited with the shared inline warning.
	if result.Status != Limited || !result.Truncated || result.Content != `{"answer":"`+strings.Repeat("a", askMaxAnswerBytes)+`"}` || len(result.Warnings) == 0 || result.Warnings[0] != askTruncatedWarning {
		t.Fatalf("single-form truncation = status %s truncated %v warnings %v len %d", result.Status, result.Truncated, result.Warnings, len(result.Content))
	}
	recorder = &askRecorder{answer: AskAnswer{Text: "x"}}
	if result := runAsk(t, recorder, `{"question":"Pick","options":["a","a"]}`); result.Status != Refused || result.Content != "invalid_arguments: ask_user options must be pairwise distinct" || recorder.calls != 0 {
		t.Fatalf("single-form duplicate options = %+v", result)
	}
}

func TestAskUserRefusesMalformedCallsBeforeInteraction(t *testing.T) {
	question := func(options ...map[string]any) map[string]any {
		entry := map[string]any{"question": "Which?"}
		if options != nil {
			list := make([]any, len(options))
			for i, option := range options {
				list[i] = option
			}
			entry["options"] = list
		}
		return entry
	}
	label := func(text string) map[string]any { return map[string]any{"label": text} }
	nine := make([]map[string]any, 9)
	for i := range nine {
		nine[i] = label(fmt.Sprintf("choice %d", i))
	}
	five := make([]any, 5)
	for i := range five {
		five[i] = map[string]any{"question": fmt.Sprintf("Question %d?", i)}
	}
	cases := []struct {
		name, arguments, reason string
	}{
		{"neither form", `{}`, "requires a question or questions"},
		{"only options", `{"options":["a"]}`, "requires a question or questions"},
		{"both forms", `{"question":"Q?","questions":[{"question":"Q?"}]}`, "either question (with optional options) or questions, not both"},
		{"options with questions", `{"options":["a"],"questions":[{"question":"Q?"}]}`, "not both"},
		{"zero questions", `{"questions":[]}`, "must contain 1 to 4 questions"},
		{"five questions", questionnaireArgs(t, five), "must contain 1 to 4 questions"},
		{"empty question", questionnaireArgs(t, []any{map[string]any{"question": ""}}), "question 1 must be nonempty"},
		{"oversized question", questionnaireArgs(t, []any{question(), map[string]any{"question": strings.Repeat("é", 2049)}}), "question 2 must contain 1 to 4096 UTF-8 bytes"},
		{"empty options", `{"questions":[{"question":"Q?","options":[]}]}`, "question 1 options must contain at least one choice"},
		{"nine options", questionnaireArgs(t, []any{question(nine...)}), "at most 8 choices"},
		{"empty label", questionnaireArgs(t, []any{question(label(""))}), "label must contain 1 to 120 characters"},
		{"control-only label", questionnaireArgs(t, []any{question(label("\x1b\x07"))}), "label must contain 1 to 120 characters"},
		{"long label", questionnaireArgs(t, []any{question(label(strings.Repeat("ü", 121)))}), "label must contain 1 to 120 characters"},
		{"duplicate labels", questionnaireArgs(t, []any{question(label("a"), label("a"))}), "labels must be pairwise distinct"},
		{"long description", questionnaireArgs(t, []any{question(map[string]any{"label": "a", "description": strings.Repeat("d", 201)})}), "description must contain at most 200 characters"},
		{"two recommended", questionnaireArgs(t, []any{question(map[string]any{"label": "a", "recommended": true}, map[string]any{"label": "b", "recommended": true})}), "question 1 must mark at most one option as recommended"},
		{"missing label", `{"questions":[{"question":"Q?","options":[{"description":"d"}]}]}`, "requires a label"},
		{"missing entry question", `{"questions":[{"options":[{"label":"a"}]}]}`, "entry requires a question"},
		{"unknown entry field", `{"questions":[{"question":"Q?","multi":true}]}`, "unknown field"},
		{"unknown option field", `{"questions":[{"question":"Q?","options":[{"label":"a","value":"x"}]}]}`, "unknown field"},
		{"unknown top-level field", `{"questions":[{"question":"Q?"}],"title":"x"}`, "unknown field"},
		{"duplicate entry field", `{"questions":[{"question":"Q?","question":"R?"}]}`, "malformed or duplicate fields"},
		{"duplicate option field", `{"questions":[{"question":"Q?","options":[{"label":"a","label":"b"}]}]}`, "malformed or duplicate fields"},
		{"duplicate questions", `{"questions":[{"question":"Q?"}],"questions":[{"question":"R?"}]}`, "malformed or duplicate fields"},
		{"questions not array", `{"questions":{"question":"Q?"}}`, "questions must be an array"},
		{"questions null", `{"questions":null}`, "questions must be an array"},
		{"entry not object", `{"questions":["Q?"]}`, "must be one JSON object"},
		{"option not object", `{"questions":[{"question":"Q?","options":["a"]}]}`, "must be one JSON object"},
		{"label not string", `{"questions":[{"question":"Q?","options":[{"label":1}]}]}`, "label must be a string"},
		{"description not string", `{"questions":[{"question":"Q?","options":[{"label":"a","description":null}]}]}`, "description must be a string"},
		{"recommended not boolean", `{"questions":[{"question":"Q?","options":[{"label":"a","recommended":"yes"}]}]}`, "recommended must be a boolean"},
		{"invalid UTF-8", "{\"questions\":[{\"question\":\"Q\xff?\"}]}", "valid UTF-8"},
		{"trailing data", `{"questions":[{"question":"Q?"}]} {}`, "exactly one JSON object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &askRecorder{answer: AskAnswer{Text: "unused"}}
			result := runAsk(t, recorder, tc.arguments)
			if recorder.calls != 0 {
				t.Fatalf("backend was asked for a malformed call")
			}
			if result.Status != Refused || !strings.HasPrefix(result.Content, "invalid_arguments: ") || !strings.Contains(result.Content, tc.reason) {
				t.Fatalf("result = %s %q, want invalid_arguments containing %q", result.Status, result.Content, tc.reason)
			}
		})
	}
}

func TestAskUserQuestionnaireAcceptsUpperBounds(t *testing.T) {
	options := make([]any, askMaxOptions)
	for i := range options {
		options[i] = map[string]any{
			"label":       strings.Repeat("ü", askMaxOptionRunes-1) + fmt.Sprint(i),
			"description": strings.Repeat("ü", askMaxDescriptionRunes),
			"recommended": i == 3,
		}
	}
	questions := make([]any, askMaxQuestions)
	for i := range questions {
		questions[i] = map[string]any{"question": strings.Repeat("q", askMaxQuestionBytes-1) + fmt.Sprint(i), "options": options}
	}
	answers := make([]AskQuestionAnswer, askMaxQuestions)
	for i := range answers {
		answers[i] = AskQuestionAnswer{Text: "free"}
	}
	recorder := &askRecorder{answer: AskAnswer{Answers: answers}}
	result := runAsk(t, recorder, questionnaireArgs(t, questions))
	if recorder.calls != 1 || result.Status != Succeeded {
		t.Fatalf("bounded questionnaire refused: %s %q", result.Status, result.Content)
	}
	if !recorder.request.IsQuestionnaire() || len(recorder.request.Pages()) != askMaxQuestions || len(recorder.request.Questions[0].Options) != askMaxOptions || !recorder.request.Questions[2].Options[3].Recommended || recorder.request.Questions[2].Options[2].Recommended {
		t.Fatalf("backend saw %+v", recorder.request.Questions[2])
	}
}

func TestAskUserQuestionnaireStripsControlCharacters(t *testing.T) {
	recorder := &askRecorder{answer: AskAnswer{Answers: []AskQuestionAnswer{{Text: "Fast", Choice: true}}}}
	result := runAsk(t, recorder, questionnaireArgs(t, []any{map[string]any{
		"question": "Pick\x1b[31m one\r?",
		"options":  []any{map[string]any{"label": "Fa\x00st", "description": "Quick\x07 path", "recommended": true}},
	}}))
	page := recorder.request.Questions[0]
	if page.Question != "Pick[31m one?" || page.Options[0].Label != "Fast" || page.Options[0].Description != "Quick path" || !page.Options[0].Recommended {
		t.Fatalf("normalized request = %+v", page)
	}
	if result.Status != Succeeded || strings.Join(result.Warnings, "|") != askControlWarning {
		t.Fatalf("result = %+v", result)
	}
	if result.Content != `{"answers":[{"question":"Pick[31m one?","answer":"Fast","choice":true}]}` {
		t.Fatalf("content = %s", result.Content)
	}
}

const mixedQuestionnaire = `{"questions":[` +
	`{"question":"Which database?","options":[{"label":"Postgres","description":"Matches prod","recommended":true},{"label":"SQLite"}]},` +
	`{"question":"Which port?"},` +
	`{"question":"Service name?","options":[{"label":"api"}]}]}`

func TestAskUserQuestionnaireMixedAnswers(t *testing.T) {
	recorder := &askRecorder{answer: AskAnswer{Answers: []AskQuestionAnswer{
		{Text: "Postgres", Choice: true},
		{Text: "5433 <local> & \"dev\"\nonly"},
		{Skipped: true, Text: "ignored"},
	}}}
	result := runAsk(t, recorder, mixedQuestionnaire)
	if recorder.calls != 1 || recorder.callID != "call-1" {
		t.Fatalf("backend calls %d id %q", recorder.calls, recorder.callID)
	}
	want := `{"answers":[` +
		`{"question":"Which database?","answer":"Postgres","choice":true},` +
		`{"question":"Which port?","answer":"5433 <local> & \"dev\"\nonly"},` +
		`{"question":"Service name?","skipped":true}]}`
	if result.Status != Succeeded || result.Content != want || result.Truncated || len(result.Warnings) != 0 {
		t.Fatalf("result = %s %v %v\n%s\nwant\n%s", result.Status, result.Truncated, result.Warnings, result.Content, want)
	}
	var decoded struct {
		Answers []map[string]any `json:"answers"`
	}
	if err := json.Unmarshal([]byte(result.Content), &decoded); err != nil || len(decoded.Answers) != 3 {
		t.Fatalf("content is not the answers JSON: %v", err)
	}
}

func TestAskUserQuestionnaireChoiceRequiresOfferedLabel(t *testing.T) {
	recorder := &askRecorder{answer: AskAnswer{Answers: []AskQuestionAnswer{{Text: "MySQL", Choice: true}, {Text: "80", Choice: true}, {Skipped: true}}}}
	result := runAsk(t, recorder, mixedQuestionnaire)
	if result.Status != Succeeded || strings.Contains(result.Content, `"choice"`) {
		t.Fatalf("an unoffered label must not be reported as a choice: %s", result.Content)
	}
}

func TestAskUserQuestionnaireRefusals(t *testing.T) {
	cases := []struct {
		name    string
		answer  AskAnswer
		content string
	}{
		{"whole skip", AskAnswer{Skipped: true}, askQuestionnaireSkipped},
		{"whole skip with answers", AskAnswer{Skipped: true, Answers: []AskQuestionAnswer{{Text: "a"}, {Text: "b"}, {Text: "c"}}}, askQuestionnaireSkipped},
		{"every question skipped", AskAnswer{Answers: []AskQuestionAnswer{{Skipped: true}, {Skipped: true}, {Skipped: true}}}, askQuestionnaireSkipped},
		{"no answers", AskAnswer{}, askQuestionnaireDismissed},
		{"text without answers", AskAnswer{Text: "Postgres"}, askQuestionnaireDismissed},
		{"too few answers", AskAnswer{Answers: []AskQuestionAnswer{{Text: "Postgres", Choice: true}, {Text: "5433"}}}, askQuestionnaireDismissed},
		{"too many answers", AskAnswer{Answers: []AskQuestionAnswer{{Text: "a"}, {Text: "b"}, {Text: "c"}, {Text: "d"}}}, askQuestionnaireDismissed},
		{"empty unskipped answer", AskAnswer{Answers: []AskQuestionAnswer{{Text: "Postgres", Choice: true}, {Text: "\x1b"}, {Skipped: true}}}, askQuestionnaireDismissed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &askRecorder{answer: tc.answer}
			result := runAsk(t, recorder, mixedQuestionnaire)
			if result.Status != Refused || result.Content != tc.content {
				t.Fatalf("result = %s %q, want refusal %q", result.Status, result.Content, tc.content)
			}
		})
	}
}

func TestAskUserQuestionnaireBoundsFreeText(t *testing.T) {
	long := strings.Repeat("é", askMaxAnswerBytes) // 2 bytes per rune
	recorder := &askRecorder{answer: AskAnswer{Answers: []AskQuestionAnswer{{Text: "SQLite", Choice: true}, {Text: long}, {Skipped: true}}}}
	result := runAsk(t, recorder, mixedQuestionnaire)
	if result.Status != Limited || !result.Truncated || len(result.Warnings) == 0 || result.Warnings[0] != askTruncatedWarning {
		t.Fatalf("result = %s truncated %v warnings %v", result.Status, result.Truncated, result.Warnings)
	}
	var decoded struct {
		Answers []askQuestionnaireEntry `json:"answers"`
	}
	if err := json.Unmarshal([]byte(result.Content), &decoded); err != nil {
		t.Fatalf("content is not JSON: %v", err)
	}
	if got := decoded.Answers[1].Answer; got != strings.Repeat("é", askMaxAnswerBytes/2) {
		t.Fatalf("free-text answer kept %d bytes, want %d", len(got), askMaxAnswerBytes)
	}
}

func TestAskUserQuestionnaireOversizedResultStaysValidJSON(t *testing.T) {
	questions := make([]any, askMaxQuestions)
	answers := make([]AskQuestionAnswer, askMaxQuestions)
	for i := range questions {
		questions[i] = map[string]any{"question": strings.Repeat(`"`, askMaxQuestionBytes-1) + fmt.Sprint(i)}
		answers[i] = AskQuestionAnswer{Text: strings.Repeat("\n", askMaxAnswerBytes)}
	}
	recorder := &askRecorder{answer: AskAnswer{Answers: answers}}
	result := runAsk(t, recorder, questionnaireArgs(t, questions))
	if result.Status != Limited || !result.Truncated {
		t.Fatalf("result = %s truncated %v", result.Status, result.Truncated)
	}
	if !strings.Contains(strings.Join(result.Warnings, "|"), askShortenedWarning) {
		t.Fatalf("missing shortened warning: %v", result.Warnings)
	}
	var decoded struct {
		Answers []askQuestionnaireEntry `json:"answers"`
	}
	if err := json.Unmarshal([]byte(result.Content), &decoded); err != nil || len(decoded.Answers) != askMaxQuestions {
		t.Fatalf("oversized content is not intact JSON: %v", err)
	}
	if inline := result.ModelContent(); len(inline) > MaxInlineBytes {
		t.Fatalf("model content %d bytes exceeds inline limit", len(inline))
	}
	if bounded := BoundResult(result); bounded.Content != result.Content {
		t.Fatalf("result content would be cut by BoundResult")
	}
}

func TestAskUserQuestionnaireCancellationAndFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := &askRecorder{}
	result, err := AskUserTool(recorder.ask).Run(ctx, json.RawMessage(mixedQuestionnaire))
	if err != nil || result.Status != Cancelled || recorder.calls != 0 {
		t.Fatalf("pre-cancelled = %+v %v calls %d", result, err, recorder.calls)
	}

	recorder = &askRecorder{err: context.Canceled}
	if result := runAsk(t, recorder, mixedQuestionnaire); result.Status != Cancelled || result.Content != "Cancelled" {
		t.Fatalf("cancelled ask = %+v", result)
	}

	recorder = &askRecorder{err: errors.New("terminal gone")}
	result, err = AskUserTool(recorder.ask).Run(context.Background(), json.RawMessage(mixedQuestionnaire))
	if err == nil || result.Status != Failed || result.Content != "ask_failed: The ask-user interaction failed; terminal gone" {
		t.Fatalf("failed ask = %+v %v", result, err)
	}

	result, err = AskUserTool(nil).Run(context.Background(), json.RawMessage(mixedQuestionnaire))
	if err != nil || result.Status != Refused || result.Content != "ask_unavailable: "+askUnavailableReason {
		t.Fatalf("nil backend = %+v %v", result, err)
	}
}

func TestAskUserQuestionnaireThroughRegistry(t *testing.T) {
	registry := New()
	recorder := &askRecorder{answer: AskAnswer{Answers: []AskQuestionAnswer{{Text: "Postgres", Choice: true}, {Text: "5433"}, {Skipped: true}}}}
	if err := registry.Register(AskUserTool(recorder.ask)); err != nil {
		t.Fatal(err)
	}
	invoke := func(arguments string) Result {
		result, _ := registry.Invoke(context.Background(), Scope{}, model.ToolCall{ID: "call-9", Name: "ask_user", Arguments: arguments})
		return result
	}
	if result := invoke(mixedQuestionnaire); result.Status != Succeeded || !strings.HasPrefix(result.Content, `{"answers":[`) {
		t.Fatalf("registry questionnaire = %s %q", result.Status, result.Content)
	}
	if result := invoke(`{"question":"Proceed?"}`); result.Status != Refused || result.Content != "The user dismissed the question without answering." {
		t.Fatalf("registry single form = %s %q", result.Status, result.Content)
	}
	calls := recorder.calls
	for _, arguments := range []string{
		`{"question":"Q?","questions":[{"question":"Q?"}]}`,
		`{}`,
		`{"questions":[{"question":"Q?","options":[{"label":"a","value":"x"}]}]}`,
		`{"questions":[{"question":"Q?","extra":1}]}`,
		questionnaireArgs(t, []any{map[string]any{"question": "1"}, map[string]any{"question": "2"}, map[string]any{"question": "3"}, map[string]any{"question": "4"}, map[string]any{"question": "5"}}),
		`{"questions":[{"question":"Q?","options":[{"label":"a","recommended":true},{"label":"b","recommended":true}]}]}`,
	} {
		if result := invoke(arguments); result.Status != Refused || !strings.Contains(result.Content, "invalid_arguments") {
			t.Errorf("registry accepted %s: %s %q", arguments, result.Status, result.Content)
		}
	}
	if recorder.calls != calls {
		t.Fatalf("registry refusals reached the backend")
	}
}
