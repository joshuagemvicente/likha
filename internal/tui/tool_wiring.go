package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/actions"
	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tooloutput"
	"likha/internal/tools"
)

type toolsCatalogMsg struct {
	entries    []tools.CatalogEntry
	generation uint64
	runID      uint64
	sessionID  string
}

func (m *ui) openOutputStore() {
	m.outputs = nil
	m.setToolOutputStore(nil)
	m.showEditRecovery(false)
	if m.stateDir == "" || m.snapshot.ID == "" || m.store == nil {
		return
	}
	outputs, err := tooloutput.Open(m.stateDir, m.snapshot.ID)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Private tool output unavailable: " + err.Error()})
		return
	}
	m.outputs = outputs
	m.setToolOutputStore(outputs)
}

func (m *ui) toolRunOptions(runID uint64) agent.RunOptions {
	options := agent.RunOptions{Outputs: m.outputs, SessionID: m.snapshot.ID, Provider: m.conn.ProviderCanonical, PlanMode: m.planMode, InitMode: m.initRun}
	store, sessionID := m.store, m.snapshot.ID
	if store != nil && sessionID != "" {
		options.TasksEnabled = m.client != nil && m.repo != nil
		options.SaveTask = store.SaveTask
		// Plan checklist updates persist through the store immediately and
		// mirror into the snapshot on the UI event; the apply hook only
		// writes private session state, never repo or provider state.
		options.PlanApply = func(ctx context.Context, steps []tools.PlanStep) error {
			return store.SavePlan(sessionID, session.Plan{Steps: steps, UpdatedAt: time.Now().UTC()})
		}
		options.EditJournal = func(proposal *actions.EditProposal) error {
			journalID := ""
			return proposal.SetJournal(func(progress actions.EditProgress) error {
				data, err := json.Marshal(progress)
				if err != nil {
					return err
				}
				id, err := store.RecordEditProgress(sessionID, journalID, data)
				if err == nil {
					journalID = id
				}
				return err
			})
		}
	}
	// The ask broker blocks the tool handler, never the UI loop: the reply
	// channel has room for exactly one send and the run context terminates
	// the wait when the run is cancelled.
	// Both brokers capture this run's channel here, on the UI goroutine:
	// tool goroutines must never read m.events, which the next run replaces.
	events := m.events
	if events != nil && runID != 0 {
		options.Ask = m.askBroker(events, runID)
	}
	advert, load := m.skillCatalog()
	options.SkillAdvert = advert
	options.SkillLoad = load
	taskAgents, taskProfiles := m.profileRunInputs()
	options.TaskAgents = taskAgents
	options.TaskProfiles = taskProfiles
	search, fetch, webIssues := m.webHooks(events, runID)
	for _, issue := range webIssues {
		m.entries = append(m.entries, entry{role: "Likha", content: issue})
	}
	options.WebSearch = search
	options.WebFetch = fetch
	// Command permissions (specs/command-permissions): the grant store is
	// captured here on the UI goroutine; the trust hooks touch only the
	// private config file, never UI state, because tool goroutines call them.
	options.CommandGrants = m.commandGrants
	options.EditGrant = m.editGrant
	if stateDir, root := m.stateDir, m.root; stateDir != "" {
		options.CommandTrusted = func(check, fingerprint string) bool {
			return providers.CommandTrusted(stateDir, root, check, fingerprint)
		}
		options.TrustChecks = func(checks map[string]string) error {
			return providers.TrustCommandChecks(stateDir, root, checks)
		}
	}
	return options
}

// askBroker pairs one ask_user call with a single UI reply. Send delivery is
// blocking because the events channel is the FIFO the working UI drains.
func (m *ui) askBroker(events chan<- agent.TurnEvent, runID uint64) func(context.Context, string, tools.AskRequest) (tools.AskAnswer, error) {
	return func(ctx context.Context, callID string, req tools.AskRequest) (tools.AskAnswer, error) {
		reply := make(chan tools.AskAnswer, 1)
		id := fmt.Sprintf("ask_%d_%d", runID, atomic.AddUint64(&m.askSequence, 1))
		ev := agent.TurnEvent{RunID: runID, Kind: "ask", Ask: &agent.AskInteraction{
			ID: id, CallID: callID, Request: req, Reply: reply,
		}}
		select {
		case events <- ev:
		case <-ctx.Done():
			return tools.AskAnswer{}, ctx.Err()
		}
		select {
		case answer := <-reply:
			return answer, nil
		case <-ctx.Done():
			return tools.AskAnswer{}, ctx.Err()
		}
	}
}

func (m *ui) showEditRecovery(explicit bool) {
	if m.store == nil || m.snapshot.ID == "" {
		return
	}
	records, err := m.store.EditJournals(m.snapshot.ID)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Private edit recovery records unavailable: " + err.Error() + ". No edits were replayed."})
		return
	}
	count := 0
	for _, record := range records {
		var progress actions.EditProgress
		if json.Unmarshal(record.Progress, &progress) != nil || progress.Root != m.snapshot.Root {
			m.entries = append(m.entries, entry{role: "Error", content: "Private edit recovery record has invalid ownership; no edits were replayed."})
			continue
		}
		if progress.Status == "completed" {
			continue
		}
		count++
		status := progress.Status
		if status != "failed" && status != "interrupted" {
			status = "interrupted (last durable stage: " + status + ")"
		}
		text := fmt.Sprintf("Edit recovery record %s: %s.\nAffected paths: %s\nLast confirmed applied: %s\nLast confirmed restored: %s\nUnresolved: %s\nRetained files: %s\nLeftover directories: %s",
			record.ID, status, strings.Join(progress.Paths, ", "), strings.Join(progress.Applied, ", "), strings.Join(progress.Restored, ", "), strings.Join(progress.Unresolved, ", "), strings.Join(progress.RetainedFiles, ", "), strings.Join(progress.LeftoverDirectories, ", "))
		if progress.Pending.Action != "" {
			text += fmt.Sprintf("\nUnconfirmed boundary: %s %s (%s → %s). A crash can occur on either side of this write intent.", progress.Pending.Action, progress.Pending.Path, progress.Pending.From, progress.Pending.To)
		}
		text += "\nNothing was replayed or rolled back. Inspect current files and the private sessions.sqlite edit_journal record before manual recovery; saved identities and names are evidence, not permission to overwrite current work. Original/proposed content remains private and is not added to model context."
		m.entries = append(m.entries, entry{role: "Likha", content: text})
	}
	if explicit && count == 0 {
		m.entries = append(m.entries, entry{role: "Likha", content: "No incomplete edit recovery records for this session."})
	}
}

func (m *ui) handleToolsCommand(arg string) tea.Cmd {
	if m.working || m.pending != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Tool catalog commands are inactive during a run; Ctrl+O inspects available result rows."})
		return nil
	}
	if arg == "clear-output" {
		if m.outputs == nil {
			m.entries = append(m.entries, entry{role: "Likha", content: "No private output store is available for this session."})
			return nil
		}
		if err := m.outputs.Clear(); err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Clear retained output: " + err.Error()})
		} else {
			m.entries = append(m.entries, entry{role: "Likha", content: "Retained output cleared. Saved previews remain; missing output never reruns its source action."})
		}
		m.persist()
		return nil
	}
	if arg == "recovery" {
		m.showEditRecovery(true)
		return nil
	}
	if strings.TrimSpace(arg) != "" {
		m.entries = append(m.entries, entry{role: "Error", content: "Use /tools, /tools clear-output, or /tools recovery."})
		return nil
	}
	m.toolCatalogGen++
	generation, runID, sessionID := m.toolCatalogGen, m.runID, m.snapshot.ID
	repo, root, servers, options := m.repo, m.root, m.conn.Mcp, m.toolRunOptions(m.runID)
	m.status = "Loading tools"
	return func() tea.Msg {
		return toolsCatalogMsg{entries: agent.ToolCatalog(repo, root, servers, options), generation: generation, runID: runID, sessionID: sessionID}
	}
}

// startToolItem opens a call's single transcript item at tool_start (spec
// transcript-redesign § Persistence): one Tool entry plus one record with
// status running — queued for explore tasks until accepted. The result
// updates both in place, so new sessions store one entry per call. An event
// without a call keeps a plain row.
func (m *ui) startToolItem(text string, call *model.ToolCall) {
	m.entries = append(m.entries, entry{role: "Tool", content: text})
	if call == nil {
		return
	}
	m.turnToolItems++
	record := session.ToolRecord{EntryIndex: len(m.entries) - 1, CallID: call.ID, Name: call.Name, Arguments: call.Arguments, Status: "running"}
	if call.Name == "task" {
		record.SourceKind, record.SourceTool = "builtin", "task"
		record.Status, record.Content = "queued", "Awaiting explore task acceptance"
	}
	m.toolRecords = append(m.toolRecords, record)
	if call.Name == "task" {
		if profile := taskProfileFromArguments(call.Arguments); profile != "" {
			if content, ok := m.taskRowContentForProfile(call.ID, profile); ok {
				m.toolRecords[len(m.toolRecords)-1].Content = content
			}
		}
	}
	m.trackToolCall(call.ID, true)
}

// trackToolCall notes a call this run shows as an item; live calls still
// await their result.
func (m *ui) trackToolCall(callID string, live bool) {
	if callID == "" {
		return
	}
	if m.runToolCalls == nil {
		m.runToolCalls = make(map[string]bool)
	}
	m.runToolCalls[callID] = true
	if !live {
		delete(m.liveToolCalls, callID)
		return
	}
	if m.liveToolCalls == nil {
		m.liveToolCalls = make(map[string]bool)
	}
	m.liveToolCalls[callID] = true
}

// liveToolRecord finds the record a live call's result updates, or -1. Only
// this run's started calls qualify, and the newest wins, so a provider that
// reuses call IDs across rounds or turns never rewrites an older item.
func (m *ui) liveToolRecord(callID string) int {
	if callID == "" || !m.liveToolCalls[callID] {
		return -1
	}
	for i := len(m.toolRecords) - 1; i >= 0; i-- {
		record := m.toolRecords[i]
		if record.CallID == callID && record.EntryIndex >= 0 && record.EntryIndex < len(m.entries) && m.entries[record.EntryIndex].role == "Tool" {
			return i
		}
	}
	return -1
}

// recordToolResult settles a call's item: the record started at tool_start
// takes the result's status, source, and content in place, and the entry
// keeps the result text as before. A result whose start never reached the
// UI gets an item of its own.
func (m *ui) recordToolResult(call model.ToolCall, result tools.Result, text string) {
	record := session.ToolRecord{
		CallID: call.ID, Name: call.Name, Arguments: call.Arguments,
		SourceKind: result.Source.Kind, Server: result.Source.Server, SourceTool: result.Source.Tool,
		Status: string(result.Status), Content: result.Content, Truncated: result.Truncated,
		Warnings: append([]string(nil), result.Warnings...), ArtifactID: result.ArtifactID,
		NextOffset: result.NextOffset, Cursor: result.Cursor,
	}
	if call.Name == "task" {
		var outcome struct {
			TaskID string `json:"task_id"`
		}
		if json.Unmarshal([]byte(result.Content), &outcome) == nil {
			record.TaskID = outcome.TaskID
		}
	}
	if body, ok := m.approvedEdits[call.ID]; ok {
		// Only an applied edit shows its diff; a refused, failed, or
		// cancelled one never claims a change it did not make.
		delete(m.approvedEdits, call.ID)
		if result.Status == tools.Succeeded || result.Status == tools.Limited {
			record.Diff = reviewedDiff(body)
		}
	} else if result.Diff != "" && (result.Status == tools.Succeeded || result.Status == tools.Limited) {
		// An edit applied under Approve always had no review; the engine
		// hands its diff over on the result (specs/approve-always).
		record.Diff = reviewedDiff(result.Diff)
	}
	if call.Name == "plan_update" {
		// An accepted replace mirrors into the snapshot; refusals and
		// persistence failures keep the previous plan untouched.
		m.syncPlanFromCall(call.Arguments, result.Status == tools.Succeeded)
	}
	if at := m.liveToolRecord(call.ID); at >= 0 {
		previous := m.toolRecords[at]
		record.EntryIndex = previous.EntryIndex
		if record.TaskID == "" {
			record.TaskID = previous.TaskID
		}
		m.toolRecords[at] = record
		m.entries[record.EntryIndex].content = text
	} else {
		m.entries = append(m.entries, entry{role: "Tool", content: text})
		record.EntryIndex = len(m.entries) - 1
		m.toolRecords = append(m.toolRecords, record)
		m.turnToolItems++
	}
	m.trackToolCall(call.ID, false)
	if m.approvalCall == call.ID {
		m.approvalCall = ""
	}
}

// unexecutedToolContent is the history text for a call a cancelled run never
// executed (agent appendUnexecuted).
const unexecutedToolContent = "Error: action not executed; run interrupted"

// recordUnexecutedCalls gives each call of this turn that a cancelled run
// never started an item of its own, so an interrupted round still shows
// every call it asked for. Calls that already have an item are not
// repeated; the entry keeps the history text it always stored.
func (m *ui) recordUnexecutedCalls(history []model.Message) {
	if m.priorLen <= len(history) {
		history = history[m.priorLen:]
	}
	calls := make(map[string]model.ToolCall)
	for _, message := range history {
		for _, call := range message.ToolCalls {
			calls[call.ID] = call
		}
	}
	for _, message := range history {
		if message.Role != "tool" || message.Content != unexecutedToolContent || m.runToolCalls[message.ToolCallID] {
			continue
		}
		m.entries = append(m.entries, entry{role: "Tool", content: message.Content})
		call, ok := calls[message.ToolCallID]
		if !ok || message.ToolCallID == "" {
			continue
		}
		m.toolRecords = append(m.toolRecords, session.ToolRecord{EntryIndex: len(m.entries) - 1, CallID: call.ID, Name: call.Name,
			Arguments: call.Arguments, Status: string(tools.Cancelled), Content: message.Content})
		m.turnToolItems++
		m.trackToolCall(call.ID, false)
	}
}

// markAwaitingApproval moves the call under review to "awaiting approval".
// Approvals come only from calls that run alone (mutating tools never join a
// parallel read group), so the newest live running call is the one asking.
func (m *ui) markAwaitingApproval() {
	m.approvalCall = ""
	for i := len(m.toolRecords) - 1; i >= 0; i-- {
		record := &m.toolRecords[i]
		if m.liveToolCalls[record.CallID] && record.Name != "task" && record.Status == "running" {
			record.Status = "awaiting approval"
			m.approvalCall = record.CallID
			return
		}
	}
}

// markAwaitingAnswer moves the ask_user call whose question is now open to
// "awaiting answer". A dropped question never opened, so it stays running.
func (m *ui) markAwaitingAnswer(callID string) {
	if !m.ask.pending || m.ask.callID != callID {
		return
	}
	m.setLiveToolStatus(callID, "running", "awaiting answer")
}

// setLiveToolStatus moves this run's live call from one transient status to
// another; a call that already settled or moved on is left alone.
func (m *ui) setLiveToolStatus(callID, from, to string) {
	if callID == "" || !m.liveToolCalls[callID] {
		return
	}
	for i := len(m.toolRecords) - 1; i >= 0; i-- {
		if record := &m.toolRecords[i]; record.CallID == callID {
			if record.Status == from {
				record.Status = to
				m.layoutWidth = 0
			}
			return
		}
	}
}

// rememberApprovedEdit keeps the body of an edit review the user approved,
// keyed by the call under review, until the call's result decides whether it
// was applied (recordToolResult). Only edit reviews carry a diff.
func (m *ui) rememberApprovedEdit() {
	if m.pending == nil || m.pending.Kind != "edit" || m.approvalCall == "" {
		return
	}
	if m.approvedEdits == nil {
		m.approvedEdits = make(map[string]string)
	}
	m.approvedEdits[m.approvalCall] = m.pending.Body
}

// reviewedDiff is the unified diff of an edit review body: the edit tool's
// review opens with its affected-files list, which the diff restates, so the
// body is cut at the first file header ("--- " then "+++ "). A body without
// one is kept whole.
func reviewedDiff(body string) string {
	for at := 0; at < len(body); {
		line, rest, _ := strings.Cut(body[at:], "\n")
		if strings.HasPrefix(line, "--- ") && strings.HasPrefix(rest, "+++ ") {
			return body[at:]
		}
		next := strings.IndexByte(body[at:], '\n')
		if next < 0 {
			break
		}
		at += next + 1
	}
	return body
}

// resumeAfterApproval returns the reviewed call to running once the user has
// answered, and restarts the dot's blink that the review paused.
func (m *ui) resumeAfterApproval() tea.Cmd {
	if at := m.liveToolRecord(m.approvalCall); at >= 0 && m.toolRecords[at].Status == "awaiting approval" {
		m.toolRecords[at].Status = "running"
	}
	m.approvalCall = ""
	if !m.working {
		return nil
	}
	return m.armActivityTick()
}

func (m *ui) adjustToolRecordsAfterRemoval(index int) {
	kept := m.toolRecords[:0]
	for _, record := range m.toolRecords {
		if record.EntryIndex == index {
			continue
		}
		if record.EntryIndex > index {
			record.EntryIndex--
		}
		kept = append(kept, record)
	}
	m.toolRecords = kept
}

func (m *ui) remapToolRecords(indices map[int]int) {
	kept := m.toolRecords[:0]
	for _, record := range m.toolRecords {
		if index, ok := indices[record.EntryIndex]; ok {
			record.EntryIndex = index
			kept = append(kept, record)
		}
	}
	m.toolRecords = kept
}
