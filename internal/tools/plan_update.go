package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"likha/internal/model"
)

// PlanStepStatus is the declared state of one step in the persisted checklist.
type PlanStepStatus string

const (
	PlanPending    PlanStepStatus = "pending"
	PlanInProgress PlanStepStatus = "in_progress"
	PlanCompleted  PlanStepStatus = "completed"
	PlanBlocked    PlanStepStatus = "blocked"
)

// PlanStatuses is the closed, human-readable status vocabulary enforced by the
// plan_update schema and its handler.
const PlanStatuses = "pending|in_progress|completed|blocked"

// PlanStep is one record of the main agent's declared-work checklist. Status is
// a plain string so session persistence can round-trip untyped records.
type PlanStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// PlanUpdateResult summarizes one accepted replacement of the plan list. It is
// an internal summary, never a per-step report; callers render or encode it.
type PlanUpdateResult struct {
	Total, Completed, InProgress, Blocked, Pending int
}

const (
	planMaxSteps = 32
	planMaxID    = 64
	planMaxTitle = 240
)

// PlanUpdateTool replaces the persisted plan checklist as one session-state
// operation. The apply hook (wired at registration time) owns durability: it
// receives the complete validated replacement list, so it can either succeed
// or leave the previous list unchanged — never partially apply. A nil hook
// refuses instead of panicking or fabricating success. Empty steps clear the
// plan; a valid nonempty replacement reports bounded summary counts. Invalid
// arguments are refusals and never reach the apply hook.
func PlanUpdateTool(apply func(ctx context.Context, steps []PlanStep) error) Tool {
	return Tool{
		Definition: model.ToolDefinition{
			Name:        "plan_update",
			Description: "Replace the persisted plan checklist with one complete ordered list of steps; there are no per-step create/update/delete variants and no hidden scheduling. Each step has a stable nonempty id, plain nonempty title text, and status pending|in_progress|completed|blocked. Send the full list every call, including unchanged steps; an empty list clears the plan visibly. This records the agent's declared work only: it executes nothing, verifies nothing, launches agents, and writes no repository files.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","minItems":0,"maxItems":32,"items":{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":64},"title":{"type":"string","minLength":1,"maxLength":240},"status":{"type":"string","enum":["pending","in_progress","completed","blocked"]}},"required":["id","title","status"],"additionalProperties":false}}},"required":["steps"],"additionalProperties":false}`),
		},
		Source: Source{Kind: "builtin", Tool: "plan_update"},
		// Plan updates change harness-owned session state only, so no
		// repository/command/network effect is declared and read-only mode
		// stays permissive by contract.
		Effects: nil,
		Target:  "session", ParallelSafe: false, Interactive: false,
		Run: func(ctx context.Context, input json.RawMessage) (Result, error) {
			steps, err := decodePlanSteps(input)
			if err != nil {
				return planRefusal("invalid_arguments", err.Error()), nil
			}
			if err := ctx.Err(); err != nil {
				return BoundResult(Result{Status: Cancelled, Content: "cancelled: The plan was not changed."}), err
			}
			if apply == nil {
				return planRefusal("plan_unsaved", "The plan persistence runtime is unavailable; the list was not changed."), nil
			}
			if len(steps) == 0 {
				if err := apply(ctx, nil); err != nil {
					return planSaveFailed(err), nil
				}
				return BoundResult(Result{Status: Succeeded, Content: `{"cleared":true}`}), nil
			}
			if err := apply(ctx, steps); err != nil {
				return planSaveFailed(err), nil
			}
			return BoundResult(Result{Status: Succeeded, Content: planSummaryContent(steps)}), nil
		},
	}
}

// decodePlanSteps gives direct handler calls the registry's closed-required-
// string-object contract: exactly one object with exactly one steps array of
// strict step objects, with duplicate and unknown fields refused outright.
func decodePlanSteps(input json.RawMessage) ([]PlanStep, error) {
	if !utf8.Valid(input) {
		return nil, errors.New("plan_update arguments must be valid UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errors.New("plan_update arguments must be one JSON object")
	}
	var steps []PlanStep
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return nil, errors.New("plan_update arguments contain malformed or duplicate fields")
		}
		seen[name] = true
		if name != "steps" {
			return nil, errors.New("plan_update arguments contain an unknown field")
		}
		steps, err = decodeStepList(decoder)
		if err != nil {
			return nil, err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("plan_update arguments must be one complete JSON object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("plan_update arguments must contain exactly one JSON object")
	}
	return steps, validatePlanSteps(steps)
}

func decodeStepList(decoder *json.Decoder) ([]PlanStep, error) {
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, errors.New("plan_update steps must be one JSON array")
	}
	steps := make([]PlanStep, 0, planMaxSteps)
	for decoder.More() {
		step := PlanStep{}
		seen := make(map[string]bool)
		if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
			return nil, errors.New("each plan_update step must be one JSON object")
		}
		for decoder.More() {
			token, err := decoder.Token()
			name, ok := token.(string)
			if err != nil || !ok || seen[name] {
				return nil, errors.New("each plan_update step must contain valid id, title, and status fields once")
			}
			seen[name] = true
			var target *string
			switch name {
			case "id":
				target = &step.ID
			case "title":
				target = &step.Title
			case "status":
				target = &step.Status
			default:
				return nil, errors.New("each plan_update step may contain only id, title, and status")
			}
			value, err := decoder.Token()
			text, ok := value.(string)
			if err != nil || !ok {
				return nil, errors.New("each plan_update step's id, title, and status must be strings")
			}
			*target = text
		}
		if _, err := decoder.Token(); err != nil {
			return nil, errors.New("each plan_update step must be one complete JSON object")
		}
		if !seen["id"] || !seen["title"] || !seen["status"] {
			return nil, errors.New("each plan_update step requires id, title, and status")
		}
		steps = append(steps, step)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("plan_update steps must be one complete JSON array")
	}
	return steps, nil
}

// validatePlanSteps enforces the bounded-list contract before the apply hook
// can run: at most thirty-two steps with pairwise unique nonempty ids, nonempty
// titles within their rune limits, and statuses drawn only from the enum.
func validatePlanSteps(steps []PlanStep) error {
	if len(steps) > planMaxSteps {
		return fmt.Errorf("plan_update steps must contain at most %d steps", planMaxSteps)
	}
	ids := make(map[string]bool, len(steps))
	for _, step := range steps {
		if n := utf8.RuneCountInString(step.ID); n < 1 || n > planMaxID {
			return fmt.Errorf("plan_update step id must contain 1 to %d characters", planMaxID)
		}
		if ids[step.ID] {
			return fmt.Errorf("plan_update step ids must be unique; %q repeats", step.ID)
		}
		ids[step.ID] = true
		if n := utf8.RuneCountInString(step.Title); n < 1 || n > planMaxTitle {
			return fmt.Errorf("plan_update step title must contain 1 to %d characters", planMaxTitle)
		}
		if !PlainPlanText(step.ID) || !PlainPlanText(step.Title) {
			return errors.New("plan_update step id and title must be plain single-line text without control or format characters")
		}
		switch PlanStepStatus(step.Status) {
		case PlanPending, PlanInProgress, PlanCompleted, PlanBlocked:
		default:
			return fmt.Errorf("plan_update step status must be one of %s", PlanStatuses)
		}
	}
	return nil
}

// PlainPlanText reports whether text can be shown as one checklist row as
// it is: no line breaks, terminal controls or invisible format characters.
// The checklist renders one line per step, so an embedded line break could
// forge an extra step and its status in the user's view.
func PlainPlanText(text string) bool {
	return strings.IndexFunc(emojiTagSequence.ReplaceAllString(text, "\U0001F3F4"), notPlainRune) < 0
}

// SanitizePlanText replaces every character PlainPlanText refuses with a
// space. It serves steps stored before plan_update refused them: such a step
// stays visible, on its own line. text must be valid UTF-8.
func SanitizePlanText(text string) string {
	var out strings.Builder
	last := 0
	for _, loc := range emojiTagSequence.FindAllStringIndex(text, -1) {
		out.WriteString(strings.Map(plainRune, text[last:loc[0]]))
		out.WriteString(text[loc[0]:loc[1]])
		last = loc[1]
	}
	out.WriteString(strings.Map(plainRune, text[last:]))
	return out.String()
}

// emojiTagSequence is one of the three subdivision flags Unicode recommends
// for general interchange (RGI): England, Scotland and Wales, each a black
// flag, the subdivision code (gbeng, gbsct, gbwls) in tag characters, and a
// cancel tag. Tag characters anywhere else, or in any other code, could
// carry text the user never sees ("IGNORE PREVIOUS INSTRUCTIONS" spelled in
// tags renders as nothing, and even a well-formed code renders as a plain
// black flag that hides its letters), so only these three keep them. The
// plain black flag holds no tag characters and is always allowed.
var emojiTagSequence = regexp.MustCompile(`\x{1F3F4}\x{E0067}\x{E0062}(?:\x{E0065}\x{E006E}\x{E0067}|\x{E0073}\x{E0063}\x{E0074}|\x{E0077}\x{E006C}\x{E0073})\x{E007F}`)

func notPlainRune(r rune) bool {
	// The zero-width joiner only fuses emoji sequences such as a
	// technologist (man, ZWJ, laptop); unlike bidi and other format
	// controls it cannot reorder or hide text, so it stays allowed.
	if r == '\u200d' {
		return false
	}
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) ||
		unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

func plainRune(r rune) rune {
	if notPlainRune(r) {
		return ' '
	}
	return r
}

// planSummaryContent encodes the internal bounded counts for one accepted
// replacement; the empty plan is reported by the dedicated cleared marker.
func planSummaryContent(steps []PlanStep) string {
	summary := PlanUpdateResult{Total: len(steps)}
	for _, step := range steps {
		switch PlanStepStatus(step.Status) {
		case PlanInProgress:
			summary.InProgress++
		case PlanCompleted:
			summary.Completed++
		case PlanBlocked:
			summary.Blocked++
		default:
			summary.Pending++
		}
	}
	content, _ := json.Marshal(struct {
		Steps      int `json:"steps"`
		Completed  int `json:"completed"`
		InProgress int `json:"in_progress"`
		Blocked    int `json:"blocked"`
		Pending    int `json:"pending"`
	}{summary.Total, summary.Completed, summary.InProgress, summary.Blocked, summary.Pending})
	return string(content)
}

func planRefusal(code, reason string) Result {
	return BoundResult(Result{Status: Refused, Content: code + ": " + reason})
}

// planSaveFailed is an executed failure, not a refusal: the valid replacement
// ran against persistence and lost, so the previous plan remains unchanged.
// The nil error keeps attribution consistent with other tools' Failed results.
func planSaveFailed(err error) Result {
	detail := "persistence error"
	if err != nil {
		detail = err.Error()
	}
	return BoundResult(Result{Status: Failed, Content: "plan_save_failed: The plan update could not be persisted; the previous plan is unchanged. (" + detail + ")"})
}
