package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"likha/internal/agent"
)

// SchemaVersion is the --json event schema; fields keep their meaning while
// it stays the same.
const SchemaVersion = 1

// maxEventField bounds arguments and content in events and progress lines.
const maxEventField = 4 << 10

// startInfo opens every run's output.
type startInfo struct {
	SessionID, Repository, Provider, Model, Effort string
}

// renderer writes one run's output. Errors are kept and reported by finish,
// so a closed stdout fails the run instead of being ignored.
type renderer interface {
	start(info startInfo)
	event(ev agent.TurnEvent)
	finish(result runResult) error
}

func newRenderer(cfg Config, stdout, stderr io.Writer) renderer {
	if cfg.JSON {
		return &jsonRenderer{cfg: cfg, out: json.NewEncoder(stdout)}
	}
	return &textRenderer{cfg: cfg, stdout: stdout, stderr: stderr}
}

// autoApproval finds the label an auto-approved command result carries.
var autoApproval = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(agent.CommandApprovalPrefix) + `auto-approved \((.+)\)$`)

func autoApprovedReason(content string) string {
	if match := autoApproval.FindStringSubmatch(content); match != nil {
		return match[1]
	}
	return ""
}

// textRenderer prints the final answer on stdout and plain progress lines on
// stderr, without color or cursor movement.
type textRenderer struct {
	cfg            Config
	stdout, stderr io.Writer
	err            error
}

func (t *textRenderer) line(format string, args ...any) {
	if t.err != nil {
		return
	}
	_, t.err = fmt.Fprintf(t.stderr, format+"\n", args...)
}

func (t *textRenderer) start(info startInfo) {
	t.line("likha: headless run in %s with %s / %s (effort %s, session %s)", info.Repository, info.Provider, info.Model, info.Effort, info.SessionID)
}

func (t *textRenderer) event(ev agent.TurnEvent) {
	switch ev.Kind {
	case "notice":
		t.line("likha: note: %s", t.cfg.clean(firstLine(ev.Text)))
	case "tool_result":
		if ev.ToolCall == nil || ev.ToolResult == nil {
			return
		}
		name := ev.ToolCall.Name
		switch outcome := toolOutcome(ev.ToolResult); outcome {
		case "ok":
			if reason := autoApprovedReason(ev.ToolResult.Content); reason != "" {
				t.line("likha: tool %s: auto-approved (%s)", name, reason)
				return
			}
			t.line("likha: tool %s: ok", name)
		default:
			t.line("likha: tool %s: %s: %s", name, outcome, t.cfg.clean(bound(errorLine(ev.ToolResult.Content), 400)))
		}
	}
	// A run error is reported once, by finish.
}

func (t *textRenderer) finish(result runResult) error {
	if result.Status == statusCompleted || result.saveFailed {
		if result.Text != "" && t.err == nil {
			_, t.err = fmt.Fprintln(t.stdout, result.Text)
		}
	}
	if result.Err != "" {
		t.line("likha: %s", result.Err)
	}
	t.line("likha: %s", summary(result))
	return t.err
}

// summary is the final progress line.
func summary(result runResult) string {
	parts := []string{result.Status, "session " + result.SessionID,
		plural(result.Totals.Requests, "request"), plural(result.Totals.ToolCalls, "tool call"),
		plural(result.Totals.Refusals, "refusal")}
	if result.Totals.usageSeen {
		parts = append(parts, fmt.Sprintf("%d prompt + %d completion tokens", result.Totals.Prompt, result.Totals.Completion))
		if result.Totals.CostKnown {
			parts = append(parts, fmt.Sprintf("$%.4f", result.Totals.Cost))
		}
	}
	return strings.Join(parts, " · ")
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// errorLine is the line of a failed or refused result that names why.
func errorLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "Error: ") {
			return strings.TrimPrefix(line, "Error: ")
		}
	}
	return firstLine(content)
}

// jsonRenderer writes one JSON object per line on stdout.
type jsonRenderer struct {
	cfg Config
	out *json.Encoder
	err error
}

func (j *jsonRenderer) write(value any) {
	if j.err != nil {
		return
	}
	j.err = j.out.Encode(value)
}

type startEvent struct {
	Type       string `json:"type"`
	Schema     int    `json:"schema"`
	SessionID  string `json:"session_id"`
	Repository string `json:"repository"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
}

type deltaEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type toolStartEvent struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolResultEvent struct {
	Type         string `json:"type"`
	CallID       string `json:"call_id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	AutoApproved string `json:"auto_approved,omitempty"`
	Content      string `json:"content"`
}

type noticeEvent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usageEvent struct {
	Type             string   `json:"type"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	ReasoningTokens  int64    `json:"reasoning_tokens"`
	CachedTokens     int64    `json:"cached_tokens"`
	Cost             *float64 `json:"cost,omitempty"`
}

type resultEvent struct {
	Type             string   `json:"type"`
	Status           string   `json:"status"`
	Text             string   `json:"text"`
	SessionID        string   `json:"session_id"`
	Requests         int      `json:"requests"`
	ToolCalls        int      `json:"tool_calls"`
	Refusals         int      `json:"refusals"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	ReasoningTokens  int64    `json:"reasoning_tokens"`
	Cost             *float64 `json:"cost,omitempty"`
	Error            string   `json:"error,omitempty"`
}

func (j *jsonRenderer) start(info startInfo) {
	j.write(startEvent{Type: "start", Schema: SchemaVersion, SessionID: info.SessionID, Repository: info.Repository,
		Provider: info.Provider, Model: info.Model, Effort: info.Effort})
}

func (j *jsonRenderer) event(ev agent.TurnEvent) {
	switch ev.Kind {
	case "text":
		j.write(deltaEvent{Type: "text", Delta: ev.Text})
	case "reasoning":
		j.write(deltaEvent{Type: "reasoning", Delta: ev.Text})
	case "tool_start":
		if ev.ToolCall != nil {
			j.write(toolStartEvent{Type: "tool_start", CallID: ev.ToolCall.ID, Name: ev.ToolCall.Name,
				Arguments: j.cfg.clean(bound(ev.ToolCall.Arguments, maxEventField))})
		}
	case "tool_result":
		if ev.ToolCall != nil && ev.ToolResult != nil {
			j.write(toolResultEvent{Type: "tool_result", CallID: ev.ToolCall.ID, Name: ev.ToolCall.Name,
				Status: toolOutcome(ev.ToolResult), AutoApproved: autoApprovedReason(ev.ToolResult.Content),
				Content: j.cfg.clean(bound(ev.ToolResult.Content, maxEventField))})
		}
	case "notice":
		j.write(noticeEvent{Type: "notice", Text: j.cfg.clean(ev.Text)})
	case "usage":
		if ev.Usage != nil {
			event := usageEvent{Type: "usage", PromptTokens: ev.Usage.Prompt, CompletionTokens: ev.Usage.Completion,
				ReasoningTokens: ev.Usage.Reasoning, CachedTokens: ev.Usage.CacheRead}
			if cost, ok := requestCost(j.cfg, *ev.Usage); ok {
				event.Cost = &cost
			}
			j.write(event)
		}
	}
}

func (j *jsonRenderer) finish(result runResult) error {
	event := resultEvent{Type: "result", Status: result.Status, SessionID: result.SessionID,
		Requests: result.Totals.Requests, ToolCalls: result.Totals.ToolCalls, Refusals: result.Totals.Refusals,
		PromptTokens: result.Totals.Prompt, CompletionTokens: result.Totals.Completion, ReasoningTokens: result.Totals.Reasoning,
		Error: result.Err}
	if result.Status == statusCompleted || result.saveFailed {
		event.Text = result.Text
	}
	if result.Totals.usageSeen && result.Totals.CostKnown {
		cost := result.Totals.Cost
		event.Cost = &cost
	}
	j.write(event)
	return j.err
}
