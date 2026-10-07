package headless

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/session"
	"likha/internal/tools"
)

// totals sums one run's provider-reported usage.
type totals struct {
	Requests, ToolCalls, Refusals int
	Prompt, Completion, Reasoning int64
	Cost                          float64
	// CostKnown is false as soon as one request's cost is unknown, so a
	// partial sum is never shown as the run's spend.
	CostKnown bool
	usageSeen bool
}

// runResult is how a run ended, for the summary and the result event.
type runResult struct {
	Status    string
	Text      string
	SessionID string
	Err       string
	Totals    totals
	// saveFailed keeps a completed run's answer while reporting failure.
	saveFailed bool
}

func (r *runResult) markSaveFailed(err error) {
	r.saveFailed = true
	r.Status = statusFailed
	message := "session not saved: " + err.Error()
	if r.Err != "" {
		message = r.Err + "; " + message
	}
	r.Err = message
}

func (r runResult) exitCode() int {
	switch r.Status {
	case statusCompleted:
		return ExitCompleted
	case statusCancelled:
		return ExitInterrupted
	default:
		return ExitFailed
	}
}

// recorder follows the run's events to build the stored session and the
// final result. Calls are serialized by Run.
type recorder struct {
	cfg     Config
	history []model.Message
	entries []session.Entry
	records []session.ToolRecord
	// assistant is the index of the entry receiving streamed text, or -1.
	assistant int
	totals    totals
	done      bool
	err       error
	errText   string
}

func newRecorder(cfg Config) *recorder {
	rec := &recorder{
		cfg:       cfg,
		history:   cfg.Snapshot.History,
		entries:   append([]session.Entry(nil), cfg.Snapshot.Entries...),
		records:   append([]session.ToolRecord(nil), cfg.Snapshot.ToolRecords...),
		assistant: -1,
		totals:    totals{CostKnown: true},
	}
	rec.entries = append(rec.entries, session.Entry{Role: "You", Content: cfg.Prompt})
	return rec
}

func (r *recorder) observe(ev agent.TurnEvent) {
	switch ev.Kind {
	case "context":
		if ev.ContextEstimated {
			// One estimate precedes every model request.
			r.totals.Requests++
		}
	case "text":
		if r.assistant < 0 {
			r.entries = append(r.entries, session.Entry{Role: "Assistant"})
			r.assistant = len(r.entries) - 1
		}
		r.entries[r.assistant].Content += ev.Text
	case "usage":
		if ev.Usage != nil {
			r.addUsage(*ev.Usage)
		}
	case "notice":
		r.assistant = -1
		r.entries = append(r.entries, session.Entry{Role: "Likha", Content: r.cfg.clean(ev.Text)})
	case "tool_start":
		r.assistant = -1
		r.entries = append(r.entries, session.Entry{Role: "Tool", Content: ev.Text})
		if ev.ToolCall != nil {
			r.records = append(r.records, session.ToolRecord{EntryIndex: len(r.entries) - 1, CallID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments, Status: "running"})
		}
	case "tool_result":
		r.assistant = -1
		r.history = ev.History
		r.recordToolResult(ev)
	case "tool_checkpoint":
		r.history = ev.History
	case "done":
		r.history = ev.History
		r.done = true
	case "error":
		r.assistant = -1
		if ev.History != nil {
			r.history = ev.History
		}
		r.err = ev.Err
		r.errText = r.cfg.clean(ev.Text)
		r.entries = append(r.entries, session.Entry{Role: "Error", Content: r.errText})
	}
}

func (r *recorder) addUsage(usage model.RequestUsage) {
	r.totals.usageSeen = true
	r.totals.Prompt += usage.Prompt
	r.totals.Completion += usage.Completion
	r.totals.Reasoning += usage.Reasoning
	cost, ok := requestCost(r.cfg, usage)
	if !ok {
		r.totals.CostKnown = false
		return
	}
	r.totals.Cost += cost
}

// requestCost prices one request; ok is false when the cost is unknown.
func requestCost(cfg Config, usage model.RequestUsage) (float64, bool) {
	cost, source := model.RequestCost(cfg.Provider, cfg.Client.Model(), usage)
	return cost, source.Known()
}

func (r *recorder) recordToolResult(ev agent.TurnEvent) {
	if ev.ToolCall == nil || ev.ToolResult == nil {
		r.entries = append(r.entries, session.Entry{Role: "Tool", Content: ev.Text})
		return
	}
	r.totals.ToolCalls++
	if isRefusal(ev.ToolResult) {
		r.totals.Refusals++
	}
	result := ev.ToolResult
	record := session.ToolRecord{
		CallID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments,
		SourceKind: result.Source.Kind, Server: result.Source.Server, SourceTool: result.Source.Tool,
		Status: string(result.Status), Content: result.Content, Truncated: result.Truncated,
		Warnings: append([]string(nil), result.Warnings...), ArtifactID: result.ArtifactID,
		NextOffset: result.NextOffset, Cursor: result.Cursor,
	}
	// The newest running record of this call takes the result in place.
	for i := len(r.records) - 1; i >= 0; i-- {
		if r.records[i].CallID == record.CallID && r.records[i].Status == "running" {
			record.EntryIndex = r.records[i].EntryIndex
			r.records[i] = record
			r.entries[record.EntryIndex].Content = ev.Text
			return
		}
	}
	r.entries = append(r.entries, session.Entry{Role: "Tool", Content: ev.Text})
	record.EntryIndex = len(r.entries) - 1
	r.records = append(r.records, record)
}

// result decides how the run ended. A cancelled context wins over the
// error it caused.
func (r *recorder) result(ctx context.Context) runResult {
	result := runResult{SessionID: r.cfg.Snapshot.ID, Totals: r.totals}
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, ErrInterrupted):
		result.Status, result.Err = statusCancelled, "interrupted by signal"
	case errors.Is(cause, errTimeLimit):
		result.Status, result.Err = statusTimeLimit, fmt.Sprintf("time limit of %s reached", r.cfg.MaxTime)
	case r.done:
		result.Status, result.Text = statusCompleted, r.cfg.clean(finalAnswer(r.history))
	case r.err != nil || r.errText != "":
		result.Status, result.Err = statusFailed, r.failure()
	default:
		result.Status, result.Err = statusFailed, "the run ended without a result"
	}
	return result
}

// failure names the provider and the cause of a failed run.
func (r *recorder) failure() string {
	text := r.errText
	if text == "" && r.err != nil {
		text = r.cfg.clean(r.err.Error())
	}
	if r.cfg.Display != "" {
		return r.cfg.Display + ": " + text
	}
	return text
}

// finalAnswer is the assistant text of the response that ended the run.
func finalAnswer(history []model.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			return strings.TrimSpace(history[i].Content)
		}
	}
	return ""
}

// save stores the run as an ordinary session. Task records and the plan are
// read back from the store, which the run's hooks wrote, so the snapshot
// save never replaces them with an older copy.
func save(cfg Config, rec *recorder, resumed bool) error {
	if cfg.Store == nil {
		return errors.New("no session store")
	}
	snapshot := cfg.Snapshot
	snapshot.History = rec.history
	snapshot.Entries = rec.entries
	snapshot.ToolRecords = rec.records
	snapshot.PromptHistory = append(snapshot.PromptHistory, cfg.Prompt)
	tasks, err := cfg.Store.Tasks(snapshot.ID)
	if err != nil {
		return fmt.Errorf("read task records: %w", err)
	}
	snapshot.Tasks = tasks
	plan, ok, err := cfg.Store.LoadPlan(snapshot.ID)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	if ok {
		snapshot.Plan = &plan
	}
	if !resumed && snapshot.NamedTitle == "" {
		snapshot.NamedTitle = sessionTitle(cfg.Prompt)
	}
	if err := cfg.Store.Save(snapshot); err != nil {
		return fmt.Errorf("save session %s: %w", snapshot.ID, err)
	}
	return nil
}

// maxTitleRunes bounds a title taken from the prompt's first line.
const maxTitleRunes = 80

// sessionTitle is the prompt's first nonblank line, bounded, so a headless
// session is named without a naming request (D3).
func sessionTitle(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > maxTitleRunes {
			return string(runes[:maxTitleRunes-1]) + "…"
		}
		return line
	}
	return ""
}

// toolOutcome is the result status written for a tool call.
func toolOutcome(result *tools.Result) string {
	if result == nil {
		return string(tools.Failed)
	}
	switch result.Status {
	case tools.Succeeded, tools.Limited:
		return "ok"
	case tools.Refused:
		return "refused"
	case tools.Cancelled:
		return "cancelled"
	default:
		return "error"
	}
}
