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
	options := agent.RunOptions{Outputs: m.outputs, SessionID: m.snapshot.ID, Provider: m.conn.ProviderCanonical, PlanMode: m.planMode}
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
	if m.events != nil && runID != 0 {
		options.Ask = m.askBroker(runID)
	}
	advert, load := m.skillCatalog()
	options.SkillAdvert = advert
	options.SkillLoad = load
	taskAgents, taskProfiles := m.profileRunInputs()
	options.TaskAgents = taskAgents
	options.TaskProfiles = taskProfiles
	search, fetch, webIssues := m.webHooks()
	for _, issue := range webIssues {
		m.entries = append(m.entries, entry{role: "Likha", content: issue})
	}
	options.WebSearch = search
	options.WebFetch = fetch
	return options
}

// askBroker pairs one ask_user call with a single UI reply. Send delivery is
// blocking because the events channel is the FIFO the working UI drains.
func (m *ui) askBroker(runID uint64) func(context.Context, string, tools.AskRequest) (tools.AskAnswer, error) {
	return func(ctx context.Context, callID string, req tools.AskRequest) (tools.AskAnswer, error) {
		reply := make(chan tools.AskAnswer, 1)
		id := fmt.Sprintf("ask_%d_%d", runID, atomic.AddUint64(&m.askSequence, 1))
		ev := agent.TurnEvent{RunID: runID, Kind: "ask", Ask: &agent.AskInteraction{
			ID: id, CallID: callID, Request: req, Reply: reply,
		}}
		select {
		case m.events <- ev:
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

func (m *ui) recordToolResult(index int, call model.ToolCall, result tools.Result) {
	record := session.ToolRecord{
		EntryIndex: index, CallID: call.ID, Name: call.Name, Arguments: call.Arguments,
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
		for i := len(m.toolRecords) - 1; i >= 0; i-- {
			if m.toolRecords[i].CallID == call.ID && m.toolRecords[i].Name == "task" && m.toolRecords[i].TaskID == record.TaskID {
				m.toolRecords[i].Status = record.Status
				break
			}
		}
	}
	if call.Name == "plan_update" {
		// An accepted replace mirrors into the snapshot; refusals and
		// persistence failures keep the previous plan untouched.
		m.syncPlanFromCall(call.Arguments, result.Status == tools.Succeeded)
	}
	m.toolRecords = append(m.toolRecords, record)
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
