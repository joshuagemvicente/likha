package headless

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"likha/internal/agent"
	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/session"
	"likha/internal/tools"
	"likha/internal/transcript"
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

// Notices stored for a run stopped before it finished. cancelledEntry is the
// TUI's wording for a cancelled run (internal/tui), so a resumed session
// reads the same whichever interface ran it; stopTail ends the other stops
// the same way.
const (
	cancelledEntry = "Run cancelled" + stopTail
	stopTail       = "; no further tools will execute. Approved shell commands may leave detached processes running."
)

// recorder follows the run's events to build the stored session and the
// final result. Calls are serialized by Run.
type recorder struct {
	started   time.Time
	cancelled bool
	cfg       Config
	history   []model.Message
	entries   []session.Entry
	records   []session.ToolRecord
	// assistant is the index of the entry receiving streamed text, or -1.
	// text collects that entry's deltas until the segment ends (endText):
	// appending each delta to the entry string would copy the whole answer
	// every time.
	assistant int
	text      strings.Builder
	totals    totals
	// requestOpen is set from a main request's estimate until the request
	// completes; requestUsage records whether it reported usage.
	requestOpen, requestUsage bool
	// tasks holds the newest record of each explore or profile child; their
	// requests join the run's totals (runTotals).
	tasks   map[string]explore.Record
	done    bool
	err     error
	errText string
	// errEntry indexes the entry recording the run's error, or -1.
	errEntry int
	// compacted is set once the run was compacted automatically; the turn
	// footer says so.
	compacted bool
}

func newRecorder(cfg Config) *recorder {
	rec := &recorder{
		started:   time.Now(),
		cfg:       cfg,
		history:   cfg.Snapshot.History,
		entries:   append([]session.Entry(nil), cfg.Snapshot.Entries...),
		records:   append([]session.ToolRecord(nil), cfg.Snapshot.ToolRecords...),
		assistant: -1,
		totals:    totals{CostKnown: true},
		errEntry:  -1,
	}
	rec.entries = append(rec.entries, session.Entry{Role: "You", Content: cfg.Prompt})
	return rec
}

func (r *recorder) observe(ev agent.TurnEvent) {
	switch ev.Kind {
	case "context":
		if ev.ContextEstimated && !ev.ContextReestimate {
			// One estimate precedes every model request; a re-estimate
			// measures the same request again. The previous request ends
			// here if nothing closed it (a summarize that failed), so one
			// without usage still makes the cost unknown.
			r.closeRequest()
			r.totals.Requests++
			r.requestOpen, r.requestUsage = true, false
		}
	case "text":
		if r.assistant < 0 {
			r.entries = append(r.entries, session.Entry{Role: "Assistant"})
			r.assistant = len(r.entries) - 1
		}
		r.text.WriteString(ev.Text)
	case "usage":
		if ev.Usage != nil {
			r.requestUsage = true
			r.addUsage(*ev.Usage)
		}
	case "task":
		if ev.Task != nil {
			r.observeTask(*ev.Task)
		}
	case "notice":
		r.endText()
		r.entries = append(r.entries, session.Entry{Role: "Likha", Content: r.cfg.clean(ev.Text)})
	case "tool_start":
		r.closeRequest()
		r.endText()
		r.entries = append(r.entries, session.Entry{Role: "Tool", Content: ev.Text})
		if ev.ToolCall != nil {
			r.records = append(r.records, session.ToolRecord{EntryIndex: len(r.entries) - 1, CallID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments, Status: "running"})
		}
	case "tool_result":
		r.endText()
		r.history = ev.History
		r.recordToolResult(ev)
	case "tool_checkpoint":
		r.history = ev.History
	case "auto_compacted":
		r.observeCompaction(ev)
	case "done":
		r.closeRequest()
		r.history = ev.History
		r.done = true
	case "error":
		r.endText()
		if ev.History != nil {
			r.history = ev.History
		}
		r.err = ev.Err
		r.errText = r.cfg.clean(ev.Text)
		r.entries = append(r.entries, session.Entry{Role: "Error", Content: r.errText})
		r.errEntry = len(r.entries) - 1
	}
}

// observeCompaction adopts the history an automatic compaction produced and
// stores the TUI's divider with the summary. The estimate before the
// compaction opened the request that became the summarize call, so its
// usage closes that request; the next estimate opens the real one.
func (r *recorder) observeCompaction(ev agent.TurnEvent) {
	r.endText()
	r.history = ev.History
	r.compacted = true
	if ev.Usage != nil {
		r.requestUsage = true
		r.addUsage(*ev.Usage)
	}
	r.closeRequest()
	divider := compactionTrigger(ev.ContextTokens, ev.ContextWindow) + " Summary of earlier turns:"
	r.entries = append(r.entries, session.Entry{Role: "Likha", Content: divider + "\n\n" + r.cfg.clean(ev.Text)})
}

// compactionTrigger names what started an automatic compaction in the
// words of the TUI's divider (internal/tui autoCompactDivider), so a
// session reads the same whichever interface compacted it.
func compactionTrigger(tokens, window int64) string {
	if window <= 0 {
		return "Conversation compacted automatically."
	}
	percent := int(math.Round(float64(tokens) / float64(window) * 100))
	return fmt.Sprintf("Conversation compacted automatically at ~%d%% of the %s context window.", percent, humanTokens(window))
}

// humanTokens writes a token count as the TUI's status line does: 950,
// 1.5k, 200k, 1M.
func humanTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 995_000:
		return compactUnit(float64(n)/1000, "k")
	default:
		return compactUnit(float64(n)/1_000_000, "M")
	}
}

// compactUnit writes value with its unit suffix, with one decimal only when
// the value is small and not whole.
func compactUnit(value float64, unit string) string {
	if value < 100 && math.Abs(value-math.Round(value)) > 0.049 {
		return fmt.Sprintf("%.1f%s", value, unit)
	}
	return strconv.FormatInt(int64(math.Round(value)), 10) + unit
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

// endText writes the streamed text to its entry and ends the segment, so
// the next delta opens a new entry.
func (r *recorder) endText() {
	if r.assistant >= 0 {
		r.entries[r.assistant].Content = r.text.String()
		r.text.Reset()
	}
	r.assistant = -1
}

// closeRequest ends the main request whose response is complete. One that
// reported no usage has an unknown cost, so the run's spend is unknown too.
func (r *recorder) closeRequest() {
	if r.requestOpen && !r.requestUsage {
		r.totals.CostKnown = false
	}
	r.requestOpen = false
}

// observeTask keeps the newest version of a child's record.
func (r *recorder) observeTask(record explore.Record) {
	if record.ID == "" {
		return
	}
	if known, ok := r.tasks[record.ID]; ok && known.Version > record.Version {
		return
	}
	if r.tasks == nil {
		r.tasks = make(map[string]explore.Record)
	}
	r.tasks[record.ID] = record
}

// runTotals is the main loop's totals plus every child's requests and
// usage, so a run that delegates counts every request it caused. Each child
// record sums its own requests only, so nested children are not counted
// twice.
func (r *recorder) runTotals() totals {
	sum := r.totals
	for _, task := range r.tasks {
		if task.Rounds == 0 {
			continue // queued or refused before its first request
		}
		usage := task.Usage
		sum.Requests += task.Rounds
		sum.Prompt += usage.PromptTokens
		sum.Completion += usage.CompletionTokens
		sum.Reasoning += usage.ReasoningTokens
		sum.usageSeen = sum.usageSeen || usage.ReportedRequests > 0
		if !usage.CostKnown {
			sum.CostKnown = false
			continue
		}
		sum.Cost += usage.Cost
	}
	return sum
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

// result decides how the run ended. A finished run stays completed even
// when a stop lands after it; otherwise a cancelled context wins over the
// error it caused. An output failure after the finish is still reported,
// by the renderer's finish.
func (r *recorder) result(ctx context.Context) runResult {
	result := runResult{SessionID: r.cfg.Snapshot.ID, Totals: r.runTotals()}
	cause := context.Cause(ctx)
	switch {
	case r.done:
		result.Status, result.Text = statusCompleted, r.cfg.clean(finalAnswer(r.history))
	case errors.Is(cause, ErrInterrupted):
		result.Status, result.Err = statusCancelled, "interrupted by signal"
	case errors.Is(cause, errTimeLimit):
		result.Status, result.Err = statusTimeLimit, fmt.Sprintf("time limit of %s reached", r.cfg.MaxTime)
	case errors.Is(cause, errOutputFailed):
		result.Status, result.Err = statusFailed, errOutputFailed.Error()
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
	var rejected *model.VariantRejectedError
	if errors.As(r.err, &rejected) {
		text += "\nSent with variant " + rejected.Variant + "; use /variants → Default to remove the preset. The provider error may have another cause."
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

// stopNotice is the notice stored for a run whose context was stopped
// before it finished, and whether it was stopped at all. A stop that landed
// after the run finished stopped nothing.
func (r *recorder) stopNotice(ctx context.Context) (string, bool) {
	if r.done {
		return "", false
	}
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, ErrInterrupted):
		return cancelledEntry, true
	case errors.Is(cause, errTimeLimit):
		return fmt.Sprintf("Run stopped: time limit of %s reached", r.cfg.MaxTime) + stopTail, true
	case errors.Is(cause, errOutputFailed):
		return "Run stopped: its output could not be written" + stopTail, true
	}
	return "", false
}

// markStopped records a stopped run as the TUI records a cancel: the turn
// footer reads cancelled, and notice takes the place of the error the stop
// caused ("context canceled", a request cut off), so a resumed session does
// not show the stop as a failure. The caller still reports the stop.
func (r *recorder) markStopped(notice string) {
	r.cancelled = true
	entry := session.Entry{Role: "Likha", Content: notice}
	if r.errEntry >= 0 {
		r.entries[r.errEntry] = entry
		return
	}
	r.endText()
	r.entries = append(r.entries, entry)
}

// endTurn closes the run's transcript with its turn footer. Entries
// observed later, such as warnings from hooks still running, follow it.
func (r *recorder) endTurn() {
	r.endText()
	r.entries = append(r.entries, session.Entry{Role: transcript.TurnRole, Content: transcript.EncodeTurn(transcript.TurnInfo{Model: r.cfg.Client.Model(), Variant: r.cfg.Variant, Duration: time.Since(r.started), Tools: r.totals.ToolCalls, Cancelled: r.cancelled, Compacted: r.compacted})})
}

// save stores the run as an ordinary session. Task records and the plan are
// read back from the store, which the run's hooks wrote, so the snapshot
// save never replaces them with an older copy; the snapshot's own copies win
// where resume reconciliation (resumeState) advanced them. interrupted means
// the run did not complete, so the plan's active steps are saved marked
// interrupted (session.MarkPlanInterrupted).
func save(cfg Config, rec *recorder, resumed, interrupted bool) error {
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
	snapshot.Tasks = newestTasks(snapshot.Tasks, tasks)
	plan, ok, err := cfg.Store.LoadPlan(snapshot.ID)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	// A plan_update during the run rewrote the row; otherwise the snapshot's
	// copy stands, with the interruption resume marked on it. The row is
	// compared with the copy resume loaded, never by clock, so a clock
	// stepped back cannot drop the run's own update.
	if ok && (cfg.resumedPlan == nil || planRewritten(*cfg.resumedPlan, plan)) {
		snapshot.Plan = &plan
	}
	if interrupted {
		session.MarkPlanInterrupted(&snapshot)
	}
	if !resumed && snapshot.NamedTitle == "" {
		snapshot.NamedTitle = sessionTitle(cfg.Prompt)
	}
	// Store.Save names the session in its errors.
	return cfg.Store.Save(snapshot)
}

// planRewritten reports whether the stored row differs from the plan resume
// loaded. Resume may have marked its copy interrupted, so that flag does not
// count; a plan_update always writes a new update time.
func planRewritten(loaded, row session.Plan) bool {
	return !row.UpdatedAt.Equal(loaded.UpdatedAt) || !slices.Equal(row.Steps, loaded.Steps)
}

// newestTasks merges the snapshot's task records with the stored rows,
// keeping the newer version of each. The run's tasks reached only the store;
// resume reconciliation advanced earlier ones only in the snapshot.
func newestTasks(snapshot, stored []explore.Record) []explore.Record {
	merged := slices.Clone(stored)
	index := make(map[string]int, len(merged))
	for i, record := range merged {
		index[record.ID] = i
	}
	for _, record := range snapshot {
		i, found := index[record.ID]
		switch {
		case !found:
			index[record.ID] = len(merged)
			merged = append(merged, record)
		case record.Version > merged[i].Version:
			merged[i] = record
		}
	}
	return merged
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
