package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/explore"
	"likha/internal/session"
)

func (m *ui) restoreTaskRecords() {
	if m.store != nil && m.snapshot.ID != "" {
		records, err := m.store.Tasks(m.snapshot.ID)
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Load explore records: " + err.Error() + ". No tasks were resumed."})
		} else {
			session.ResumeTasks(&m.snapshot, records)
		}
	} else {
		session.ResumeTasks(&m.snapshot, nil)
	}
	m.taskRecords = append([]explore.Record(nil), m.snapshot.Tasks...)
	m.history = m.snapshot.History
}

func (m *ui) reportInterruptedTasks() {
	count := 0
	for _, record := range m.taskRecords {
		if record.Status == explore.Interrupted {
			count++
		}
	}
	if count > 0 {
		m.entries = append(m.entries, entry{role: "Likha", content: fmt.Sprintf("%d explore task(s) were interrupted. /agents shows saved partial transcripts and findings. Nothing was restarted and no approval was restored.", count)})
	}
}

func (m *ui) acceptTaskRecord(record explore.Record) {
	if record.SessionID != m.snapshot.ID {
		return
	}
	found := false
	for i := range m.taskRecords {
		if m.taskRecords[i].ID == record.ID {
			if m.taskRecords[i].Version >= record.Version {
				return
			}
			m.taskRecords[i] = record
			found = true
			break
		}
	}
	if !found {
		m.taskRecords = append(m.taskRecords, record)
	}
	if record.ParentID == "" {
		for i := len(m.toolRecords) - 1; i >= 0; i-- {
			row := &m.toolRecords[i]
			if row.Name == "task" && row.CallID == record.ParentCallID && (row.TaskID == "" || row.TaskID == record.ID) {
				row.TaskID = record.ID
				row.Status = string(record.Status)
				row.Content = fmt.Sprintf("Task %s · depth %d · %s", record.ID, record.Depth, record.Description)
				break
			}
		}
	}
	// The task's tool item is its only transcript surface (spec
	// transcript-redesign § Subagent items): its ⎿ line follows this record
	// and its direct children on the next layout, so no Agent entry is
	// written for any depth; /agents keeps the full tree.
	m.layoutWidth = 0
	// The runtime's private SaveTask path durably records every child update.
	// This single UI writer coordinates the display snapshot across siblings.
	m.persist()
}

func (m *ui) inspectFocusedTask(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.pending != nil || m.toolInspector.open || m.mention.open || m.commandPopup.open || m.dialog.open || m.keyModal.open {
		return false, nil
	}
	key := msg.String()
	if key != "enter" && key != "ctrl+o" {
		return false, nil
	}
	index, ok := m.focusedToolEntry()
	if !ok && key == "ctrl+o" {
		indices := m.inspectableToolEntries()
		if len(indices) > 0 {
			index = m.firstVisibleToolEntry(indices)
			ok = true
		}
	}
	if !ok {
		return false, nil
	}
	row, ok := m.toolRecordAt(index)
	if !ok || row.Name != "task" {
		return false, nil
	}
	if row.TaskID != "" {
		return true, m.openAgentTaskInspection(row.TaskID)
	}
	return true, m.openAgentInspection()
}

func taskIDFromResult(content string) string {
	var outcome struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal([]byte(content), &outcome) == nil {
		return outcome.TaskID
	}
	return ""
}

func childFindingsPreview(record explore.Record) string {
	text := strings.TrimSpace(record.Findings)
	if text == "" {
		text = record.Reason
	}
	return toolShortText(text, 100)
}
