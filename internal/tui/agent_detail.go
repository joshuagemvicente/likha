package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/tooloutput"
	"likha/internal/tools"
)

const agentDetailPageLines = 128

// agentDetailState belongs to the inspector, not to the child model context.
// An empty artifactID means no retained output has been requested. The cursor
// follows artifact identity when a live record adds more tool results.
type agentDetailState struct {
	sessionID, runID, taskID  string
	recordVersion             uint64
	selectedArtifact          int
	artifactID                string
	outputs                   *tooloutput.Store
	page                      tooloutput.Page
	pageSet, loading          bool
	pageOffset, requestOffset int
	pageSteps                 []int
	pageIndex                 int
	pageErr                   string
}

// Route this message from ui.Update to handleAgentDetailOutput. Every local
// read captures its owner and version; a late page cannot follow a new row,
// session, record revision, output store, or inspection generation.
type agentDetailOutputMsg struct {
	generation               uint64
	sessionID, runID, taskID string
	artifactID               string
	recordVersion            uint64
	offset                   int
	outputs                  *tooloutput.Store
	page                     tooloutput.Page
	err                      error
}

// resetAgentDetail invalidates only the leaf page. The tree owns open/details,
// cursor, scroll, task selection, and branch cancellation. Call this on close,
// row changes, and session changes; no composer or transcript state is touched.
func (m *ui) resetAgentDetail() {
	m.agentInspector.generation++
	m.agentInspector.detail = agentDetailState{selectedArtifact: -1}
}

func agentDetailArtifacts(record explore.Record) []explore.ToolRecord {
	var artifacts []explore.ToolRecord
	seen := make(map[string]bool)
	for _, tool := range record.Tools {
		id := tool.Result.ArtifactID
		if id != "" && !seen[id] {
			seen[id] = true
			artifacts = append(artifacts, tool)
		}
	}
	return artifacts
}

// Lookup follows the selected stable ID, never a cursor position that sibling
// progress can move. Duplicate snapshots contribute only their newest version.
func (m *ui) agentDetailRecord() (explore.Record, bool) {
	var latest explore.Record
	found := false
	for _, record := range m.taskRecords {
		if record.ID != m.agentInspector.taskID || record.SessionID != m.snapshot.ID {
			continue
		}
		if !found || record.Version >= latest.Version {
			latest, found = record, true
		}
	}
	return latest, found
}

func (m *ui) syncAgentDetail(record explore.Record) *agentDetailState {
	state := &m.agentInspector.detail
	if state.sessionID != record.SessionID || state.runID != record.RunID || state.taskID != record.ID || state.outputs != m.outputs {
		m.resetAgentDetail()
		state = &m.agentInspector.detail
		state.sessionID, state.runID, state.taskID = record.SessionID, record.RunID, record.ID
		state.recordVersion, state.outputs = record.Version, m.outputs
	}
	if state.recordVersion != record.Version {
		// Retained bytes are immutable, so a readable page can survive task
		// progress. An in-flight read cannot overwrite a newer snapshot.
		m.agentInspector.generation++
		state.recordVersion = record.Version
		state.loading, state.pageErr = false, ""
	}
	state.selectedArtifact = -1
	for i, tool := range agentDetailArtifacts(record) {
		if tool.Result.ArtifactID == state.artifactID {
			state.selectedArtifact = i
			break
		}
	}
	if state.artifactID != "" && state.selectedArtifact < 0 {
		m.resetAgentDetail()
		return m.syncAgentDetail(record)
	}
	return state
}

// handleAgentDetailKey runs before the tree's generic detail scrolling. Only
// artifact controls are claimed here. Return/Back/idle Esc and Cancel branch
// remain tree controls; active Esc/Ctrl+C remain whole-run cancellation.
func (m *ui) handleAgentDetailKey(msg tea.KeyMsg) (handled bool, cmd tea.Cmd) {
	inspector := &m.agentInspector
	if !inspector.open || !inspector.details || inspector.confirm || inspector.sessionID != m.snapshot.ID {
		return false, nil
	}
	if m.pending != nil || m.mode == modeSetup || m.keyModal.open || m.dialog.open || m.mention.open || m.commandPopup.open {
		return false, nil
	}
	key := msg.String()
	if key == "ctrl+o" {
		key = "o"
	}
	if key != "o" && key != "left" && key != "right" {
		return false, nil
	}
	record, ok := m.agentDetailRecord()
	if !ok {
		return true, nil
	}
	state := m.syncAgentDetail(record)
	artifacts := agentDetailArtifacts(record)
	switch key {
	case "o":
		if len(artifacts) == 0 {
			m.status = "No retained child output; the saved transcript and result previews remain available"
			return true, nil
		}
		index := toolWrappedIndex(state.selectedArtifact+1, len(artifacts))
		inspector.scroll = 0
		return true, m.loadAgentDetailOutput(record, artifacts[index].Result.ArtifactID, 1)
	case "left":
		if state.artifactID == "" {
			return false, nil // the tree may use Left as Back before output opens
		}
		if !state.loading && state.pageSet && state.pageIndex > 0 {
			return true, m.loadAgentDetailOutput(record, state.artifactID, state.pageSteps[state.pageIndex-1])
		}
		return true, nil
	case "right":
		if state.artifactID == "" {
			return false, nil
		}
		if state.loading {
			return true, nil
		}
		if state.pageErr != "" || !state.pageSet {
			return true, m.loadAgentDetailOutput(record, state.artifactID, max(1, state.requestOffset))
		}
		if state.page.NextOffset > state.pageOffset {
			return true, m.loadAgentDetailOutput(record, state.artifactID, state.page.NextOffset)
		}
		return true, nil
	}
	return false, nil
}

// loadAgentDetailOutput reads only references actually saved on the selected
// child's current record. It never creates storage, executes a tool, contacts a
// provider, or merges inspected content into the main or child model history.
func (m *ui) loadAgentDetailOutput(record explore.Record, artifactID string, offset int) tea.Cmd {
	inspector := &m.agentInspector
	if !inspector.open || !inspector.details || inspector.sessionID != m.snapshot.ID || record.SessionID != m.snapshot.ID || inspector.taskID != record.ID {
		return nil
	}
	current, ok := m.agentDetailRecord()
	if !ok || current.RunID != record.RunID || current.Version != record.Version {
		return nil
	}
	index := -1
	for i, tool := range agentDetailArtifacts(current) {
		if tool.Result.ArtifactID == artifactID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}
	state := m.syncAgentDetail(current)
	if state.artifactID != artifactID {
		m.resetAgentDetail()
		state = m.syncAgentDetail(current)
		state.artifactID, state.selectedArtifact = artifactID, index
	} else if state.loading {
		return nil
	}
	offset = max(1, offset)
	state.requestOffset = offset
	inspector.generation++
	state.pageErr, state.loading = "", false
	if m.outputs == nil {
		state.pageErr = "session output storage is not attached; the saved preview is still available"
		return nil
	}
	state.loading = true
	generation, outputs := inspector.generation, m.outputs
	sessionID, runID, taskID, version := current.SessionID, current.RunID, current.ID, current.Version
	return func() tea.Msg {
		page, err := outputs.Read(artifactID, offset, agentDetailPageLines)
		return agentDetailOutputMsg{generation: generation, sessionID: sessionID, runID: runID, taskID: taskID,
			artifactID: artifactID, recordVersion: version, offset: offset, outputs: outputs, page: page, err: err}
	}
}

func (m *ui) handleAgentDetailOutput(msg agentDetailOutputMsg) {
	inspector := &m.agentInspector
	if !inspector.open || !inspector.details || inspector.sessionID != msg.sessionID || m.snapshot.ID != msg.sessionID || inspector.taskID != msg.taskID || inspector.generation != msg.generation {
		return
	}
	record, ok := m.agentDetailRecord()
	if !ok || record.RunID != msg.runID {
		return
	}
	state := m.syncAgentDetail(record)
	if !state.loading || inspector.generation != msg.generation || record.Version != msg.recordVersion || state.recordVersion != msg.recordVersion || state.artifactID != msg.artifactID || state.requestOffset != msg.offset || m.outputs != msg.outputs {
		return
	}
	state.loading = false
	if msg.err != nil {
		state.pageErr = msg.err.Error()
		return // keep the saved preview or the last readable page
	}
	step := -1
	for i, offset := range state.pageSteps {
		if offset == msg.offset {
			step = i
			break
		}
	}
	if step < 0 {
		if state.pageSet {
			state.pageSteps = state.pageSteps[:state.pageIndex+1]
		}
		state.pageSteps = append(state.pageSteps, msg.offset)
		step = len(state.pageSteps) - 1
	}
	state.page, state.pageSet, state.pageErr = msg.page, true, ""
	state.pageOffset, state.pageIndex, inspector.scroll = msg.offset, step, 0
}

// agentDetailLines uses the same control-character escaping, cell-width
// wrapping, and selectable plain text as tool inspection. All child context is
// private historical data, including forged tool arguments and refusals.
func (m *ui) agentDetailLines(record explore.Record) []toolInspectionLine {
	var lines []toolInspectionLine
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	state := m.syncAgentDetail(record)
	label := record.Description
	if label == "" {
		label = "Explore task"
	}
	add(label, m.theme.Title)
	add("Task: "+record.ID+" · status: "+string(record.Status), m.theme.Normal)
	add("Local inspection only; no request, rerun, permission grant, or parent-context injection.", m.theme.Muted)
	artifacts := agentDetailArtifacts(record)
	if len(artifacts) > 0 {
		add(fmt.Sprintf("Retained outputs: %d · o opens the next · Left/Right pages the selected output", len(artifacts)), m.theme.Muted)
	}
	if state.selectedArtifact >= 0 {
		lines = m.appendAgentDetailOutput(lines, artifacts[state.selectedArtifact], len(artifacts))
	}
	add("", m.theme.Base)
	add("Task and budgets", m.theme.Title)
	parent := record.ParentID
	if parent == "" {
		parent = "main agent"
	}
	add(fmt.Sprintf("Role: %s · depth: %d/%d · parent: %s", record.Agent, record.Depth, explore.MaxDepth, parent), m.theme.Muted)
	add("Parent call: "+record.ParentCallID, m.theme.Muted)
	add("Session: "+record.SessionID+" · run: "+record.RunID, m.theme.Muted)
	add("Provider / model: "+record.Provider+" / "+record.Model, m.theme.Normal)
	add("Repository: "+record.Root, m.theme.Muted)
	add(fmt.Sprintf("Model requests: %d/%d · shared accepted children: %d/%d (finished and nested tasks still count)", record.Rounds, explore.MaxRequests, record.SpawnUsed, explore.MaxChildren), m.theme.Muted)
	add("Accepted: "+agentDetailTime(record.AcceptedAt), m.theme.Muted)
	add("Started: "+agentDetailTime(record.StartedAt)+" · finished: "+agentDetailTime(record.FinishedAt), m.theme.Muted)
	add("Deadline: "+agentDetailTime(record.Deadline), m.theme.Muted)
	for _, timing := range agentDetailTiming(record, time.Now()) {
		add(timing, m.theme.Muted)
	}
	if record.Reason != "" {
		add("Status / budget reason: "+record.Reason, m.theme.Warning)
	}
	for _, warning := range record.Warnings {
		add("Warning: "+warning, m.theme.Warning)
	}
	add("", m.theme.Base)
	add("Measured child usage", m.theme.Title)
	add(AgentUsageSummary(record), m.theme.Normal)
	add("This child's own requests only; descendant usage is separate. The main request's context display is unchanged.", m.theme.Muted)
	add("Nested agents use the configured hosted provider and can multiply cost. Time, concurrency, and request limits are not a cost cap.", m.theme.Muted)
	add("", m.theme.Base)
	add("Full brief", m.theme.Title)
	if record.Prompt == "" {
		add("No brief was saved.", m.theme.Muted)
	} else {
		add(record.Prompt, m.theme.Normal)
	}
	add("", m.theme.Base)
	add("Saved findings", m.theme.Title)
	if record.Findings == "" {
		add("No findings have been saved. Available partial messages and results appear below.", m.theme.Muted)
	} else {
		add(record.Findings, m.theme.Normal)
	}
	add("", m.theme.Base)
	add("Private child context and transcript", m.theme.Title)
	add(fmt.Sprintf("Saved snapshot · version %d · %d message(s), %d tool result(s). Messages/results may be partial or bounded; missing content is never reconstructed from the parent or regenerated.", record.Version, len(record.History), len(record.Tools)), m.theme.Muted)
	if record.Status != explore.Completed {
		add("Completeness: "+string(record.Status)+"; inspect the available partial record, warnings, and tool preview limits.", m.theme.Warning)
	}
	if len(record.History) == 0 {
		add("No child messages were retained. The full brief and any saved tool results remain inspectable.", m.theme.Muted)
	}
	resultByCall := make(map[string]int)
	for i, tool := range record.Tools {
		if tool.CallID != "" {
			resultByCall[tool.CallID] = i
		}
	}
	shown := make(map[int]bool)
	for i, message := range record.History {
		role := message.Role
		if role == "" {
			role = "unknown role"
		}
		add("", m.theme.Base)
		add(fmt.Sprintf("Message %d · %s", i+1, role), m.theme.Title)
		if message.ToolCallID != "" {
			add("Tool call: "+message.ToolCallID, m.theme.Muted)
		}
		if message.Reasoning != "" {
			add("Saved reasoning (not sent back to the provider)", m.theme.Muted)
			add(message.Reasoning, m.theme.Muted)
		}
		if index, found := resultByCall[message.ToolCallID]; found && message.Role == "tool" {
			lines = m.appendAgentDetailTool(lines, record.Tools[index])
			shown[index] = true
			if message.Content != "" && message.Content != record.Tools[index].Result.Content {
				add("Saved child-context message (distinct from the result preview)", m.theme.Muted)
				add(message.Content, m.theme.Muted)
			}
		} else if message.Content != "" {
			add(message.Content, m.theme.Normal)
		} else if message.Reasoning == "" && len(message.ToolCalls) == 0 {
			add("No textual message content was saved.", m.theme.Muted)
		}
		for _, call := range message.ToolCalls {
			add("Requested tool: "+call.Name+" · call: "+call.ID, m.theme.Muted)
			add("Arguments", m.theme.Muted)
			add(agentDetailArguments(call.Arguments), m.theme.Muted)
			if _, found := resultByCall[call.ID]; !found {
				add("No attributed result saved for this call; it may be pending or outside the retained snapshot.", m.theme.Muted)
			}
		}
	}
	for i, tool := range record.Tools {
		if shown[i] {
			continue
		}
		add("", m.theme.Base)
		add("Saved tool result outside the retained message sequence", m.theme.Title)
		lines = m.appendAgentDetailTool(lines, tool)
	}
	return lines
}

func agentDetailArguments(arguments string) string {
	if arguments == "" {
		return "No arguments were saved."
	}
	var formatted bytes.Buffer
	if json.Indent(&formatted, []byte(arguments), "", "  ") == nil {
		return formatted.String()
	}
	return arguments
}

func (m *ui) appendAgentDetailTool(lines []toolInspectionLine, tool explore.ToolRecord) []toolInspectionLine {
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	result := tool.Result
	add("Tool: "+tool.Name+" · call: "+tool.CallID, m.theme.Normal)
	add("Source: "+toolSourceLabel(result.Source), m.theme.Muted)
	status := string(result.Status)
	if status == "" {
		status = "unknown (not recorded)"
	}
	style := m.theme.Muted
	if result.Status == tools.Refused || result.Status == tools.Failed || result.Status == tools.Limited || result.Status == tools.Cancelled {
		style = m.theme.Warning
	}
	add("Result status: "+status, style)
	if result.Truncated {
		add("Preview completeness: clipped / incomplete; this does not change the recorded result status.", m.theme.Warning)
	}
	for _, warning := range result.Warnings {
		add("Warning: "+warning, m.theme.Warning)
	}
	if result.ArtifactID != "" {
		add("Output reference: "+result.ArtifactID+" (session-owned ID; o cycles retained outputs)", m.theme.Muted)
	}
	if result.NextOffset > 0 {
		add(fmt.Sprintf("Saved continuation: offset %d", result.NextOffset), m.theme.Muted)
	}
	if result.Cursor != "" {
		add("Saved result cursor: "+result.Cursor, m.theme.Muted)
	}
	add("Arguments", m.theme.Muted)
	add(agentDetailArguments(tool.Arguments), m.theme.Muted)
	add("Saved result preview", m.theme.Muted)
	if result.Content == "" {
		add("No textual output was saved.", m.theme.Muted)
	} else {
		add(result.Content, m.theme.Muted)
	}
	return lines
}

func (m *ui) appendAgentDetailOutput(lines []toolInspectionLine, tool explore.ToolRecord, count int) []toolInspectionLine {
	state := &m.agentInspector.detail
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	add("", m.theme.Base)
	add(fmt.Sprintf("Retained output %d/%d · %s", state.selectedArtifact+1, count, tool.Name), m.theme.Title)
	add("Call: "+tool.CallID+" · reference: "+state.artifactID, m.theme.Muted)
	add("Private local session storage may contain secrets. Reading these pages adds nothing to model context.", m.theme.Muted)
	add("Source: "+toolSourceLabel(tool.Result.Source)+" · result status: "+string(tool.Result.Status), m.theme.Muted)
	if tool.Result.Truncated {
		add("Saved inline preview is clipped; output capture may also be incomplete.", m.theme.Warning)
	}
	for _, warning := range tool.Result.Warnings {
		add("Result warning: "+warning, m.theme.Warning)
	}
	if state.loading {
		add(fmt.Sprintf("Reading retained output locally at offset %d…", state.requestOffset), m.theme.Muted)
	}
	if state.pageErr != "" {
		add("Retained output unavailable: "+state.pageErr, m.theme.Error)
		add("The saved preview remains available. No tool is rerun; Right retries only the local read.", m.theme.Muted)
	}
	if !state.pageSet {
		add("Saved result preview", m.theme.Title)
		if tool.Result.Content == "" {
			add("No textual preview was saved.", m.theme.Muted)
		} else {
			add(tool.Result.Content, m.theme.Muted)
		}
		return lines
	}
	add(fmt.Sprintf("Local page %d · offset %d · at most %d source lines", state.pageIndex+1, state.pageOffset, agentDetailPageLines), m.theme.Title)
	if state.page.Truncated {
		add("Page completeness: continuation or a capture/page limit; consult the offset and warnings below.", m.theme.Warning)
	}
	for _, warning := range state.page.Warnings {
		add("Output warning: "+warning, m.theme.Warning)
	}
	if state.page.NextOffset > state.pageOffset {
		add(fmt.Sprintf("More retained output at offset %d · Right reads next · Left returns to a visited page", state.page.NextOffset), m.theme.Muted)
	} else {
		add("End of retained output, not proof of a complete capture or successful execution. Left returns to a visited page.", m.theme.Muted)
	}
	if state.page.Content == "" {
		add("No retained text at this offset. The saved result preview remains in the transcript below.", m.theme.Muted)
	} else {
		add(state.page.Content, m.theme.Muted)
	}
	return lines
}

func agentDetailTime(value time.Time) string {
	if value.IsZero() {
		return "not recorded"
	}
	return value.Format(time.RFC3339)
}

func agentDetailTerminal(status explore.State) bool {
	switch status {
	case explore.Completed, explore.Limited, explore.Failed, explore.Cancelled, explore.Interrupted:
		return true
	}
	return false
}

func agentDetailTiming(record explore.Record, now time.Time) []string {
	var lines []string
	end := record.FinishedAt
	if end.IsZero() && !agentDetailTerminal(record.Status) {
		end = now
	}
	if !record.AcceptedAt.IsZero() && !end.IsZero() {
		lines = append(lines, "Elapsed since acceptance: "+max(time.Duration(0), end.Sub(record.AcceptedAt)).Round(time.Second).String()+" (includes queue and nested wait)")
	} else {
		lines = append(lines, "Elapsed since acceptance: unknown (timestamps not recorded)")
	}
	if !record.AcceptedAt.IsZero() {
		queueEnd := record.StartedAt
		if queueEnd.IsZero() {
			queueEnd = end
		}
		if !queueEnd.IsZero() {
			lines = append(lines, "Initial queue time: "+max(time.Duration(0), queueEnd.Sub(record.AcceptedAt)).Round(time.Second).String())
		}
	}
	if !record.Deadline.IsZero() && !agentDetailTerminal(record.Status) {
		lines = append(lines, "Deadline remaining: "+max(time.Duration(0), record.Deadline.Sub(now)).Round(time.Second).String())
	}
	lines = append(lines, "Deadlines include queue and child-wait time and keep running during inspection; cumulative child-wait time is not separately recorded.")
	return lines
}

// agentDetailUsage prices only provider-measured tokens, not context estimates
// or the usage embedded in nested task tool outcomes. TurnCost's subscription
// rows are valid only for the actual ChatGPT provider, never by model ID alone.
func agentDetailUsage(record explore.Record) explore.Usage {
	usage := record.Usage
	usage.ReportedRequests, usage.UnknownRequests = max(0, usage.ReportedRequests), max(0, usage.UnknownRequests)
	if missing := record.Rounds - usage.ReportedRequests; missing > usage.UnknownRequests {
		// Older/partial snapshots may not include an unknown-request count.
		// Do not turn the reported subtotal into a complete measurement.
		usage.UnknownRequests = missing
		usage.PromptKnown, usage.CompletionKnown = false, false
	}
	if usage.PromptTokens < 0 {
		usage.PromptTokens, usage.PromptKnown = 0, false
	}
	if usage.CompletionTokens < 0 {
		usage.CompletionTokens, usage.CompletionKnown = 0, false
	}
	if math.IsNaN(usage.Cost) || math.IsInf(usage.Cost, 0) || usage.Cost < 0 {
		usage.Cost, usage.CostKnown = 0, false
	}
	chatGPT := strings.EqualFold(strings.TrimSpace(record.Provider), "chatgpt")
	if chatGPT && (record.Rounds > 0 || usage.ReportedRequests > 0 || usage.UnknownRequests > 0 || usage.PromptKnown || usage.CompletionKnown || usage.CostKnown) {
		usage.Cost, usage.CostKnown = 0, true
		return usage
	}
	unitCost, unitKnown := model.TurnCost(record.Model, 1, 1)
	subscriptionOnly := unitKnown && unitCost == 0
	if usage.CostKnown && usage.Cost == 0 && subscriptionOnly && !chatGPT {
		usage.CostKnown = false
	}
	if !usage.CostKnown && usage.PromptKnown && usage.CompletionKnown && !subscriptionOnly {
		usage.Cost, usage.CostKnown = model.TurnCost(record.Model, usage.PromptTokens, usage.CompletionTokens)
	}
	return usage
}

// AgentUsageSummary is separate from the main request's context tracker. A
// partial reported measurement is never labelled as the child's known total.
func AgentUsageSummary(record explore.Record) string {
	usage := agentDetailUsage(record)
	tokens := func(value int64, known bool) string {
		if !known {
			if value > 0 {
				return fmt.Sprintf("%d reported tokens (total unknown)", value)
			}
			return "unknown"
		}
		return fmt.Sprintf("%d tokens", value)
	}
	cost := "unknown (tokens or pricing not recorded)"
	if usage.CostKnown {
		cost = fmt.Sprintf("$%.4f", usage.Cost)
		if strings.EqualFold(strings.TrimSpace(record.Provider), "chatgpt") {
			cost = "$0.00 subscription token charge (plan fees excluded)"
		} else if usage.UnknownRequests > 0 {
			cost += " reported (total unknown)"
		}
	} else if usage.Cost > 0 {
		cost = fmt.Sprintf("$%.4f reported (total unknown)", usage.Cost)
	}
	return fmt.Sprintf("Input: %s · output: %s · %d reported request(s) · %d request(s) with unknown usage · cost: %s",
		tokens(usage.PromptTokens, usage.PromptKnown), tokens(usage.CompletionTokens, usage.CompletionKnown), usage.ReportedRequests, usage.UnknownRequests, cost)
}

// TotalAgentUsage counts each task's newest record once. It does not inspect
// parent findings/tool outcomes, which may repeat a descendant's measurements.
// Numeric fields preserve reported subtotals; Known flags require full coverage.
func TotalAgentUsage(records []explore.Record) explore.Usage {
	latest := make(map[string]explore.Record)
	for _, record := range records {
		if record.ID == "" {
			continue
		}
		if prior, found := latest[record.ID]; !found || record.Version >= prior.Version {
			latest[record.ID] = record
		}
	}
	known := len(latest) > 0
	total := explore.Usage{PromptKnown: known, CompletionKnown: known, CostKnown: known}
	measured := false
	for _, record := range latest {
		usage := agentDetailUsage(record)
		if record.Rounds == 0 && usage.ReportedRequests == 0 && usage.UnknownRequests == 0 && !usage.PromptKnown && !usage.CompletionKnown && !usage.CostKnown && usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.Cost == 0 {
			continue // a queued child with no requests has no missing measurement
		}
		measured = true
		total.PromptTokens += usage.PromptTokens
		total.CompletionTokens += usage.CompletionTokens
		total.ReportedRequests += usage.ReportedRequests
		total.UnknownRequests += usage.UnknownRequests
		total.PromptKnown = total.PromptKnown && usage.PromptKnown
		total.CompletionKnown = total.CompletionKnown && usage.CompletionKnown
		total.Cost += usage.Cost
		costComplete := usage.UnknownRequests == 0 || strings.EqualFold(strings.TrimSpace(record.Provider), "chatgpt")
		total.CostKnown = total.CostKnown && usage.CostKnown && costComplete
	}
	if !measured {
		total.PromptKnown, total.CompletionKnown, total.CostKnown = false, false, false
	}
	return total
}
