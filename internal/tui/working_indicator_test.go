package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"lisa/internal/agent"
	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
)

func workingTestUI(t *testing.T) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func TestWorkingIndicatorAppearsAtRunStart(t *testing.T) {
	client, err := model.New("http://127.0.0.1:1/v1", "test-model", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "test-model", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	cmd := m.startTurn("hello", nil)
	if cmd == nil || !m.hasActivity() || m.entries[len(m.entries)-1].role != "Working" {
		t.Fatalf("submit did not immediately append activity: cmd=%v entries=%+v", cmd != nil, m.entries)
	}
	if !strings.Contains(stripANSI(m.View()), workingLabel) {
		t.Fatal("activity label not visible on first render after submit")
	}
	m.cancel()
	close(m.abandon)

	compactClient, err := model.New("http://127.0.0.1:1/v1", "test-model", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	compact := NewUI("/sample", nil, compactClient, "test-model", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	compact.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	compact.history = []model.Message{{Role: "user", Content: "old conversation"}}
	compactCmd := compact.startCompaction("")
	if compactCmd == nil || !compact.hasActivity() || !strings.Contains(stripANSI(compact.View()), workingLabel) {
		t.Fatalf("compaction did not immediately show activity: cmd=%v entries=%+v", compactCmd != nil, compact.entries)
	}
	compact.cancel()
	close(compact.abandon)

	noClient := workingTestUI(t)
	noClient.startTurn("hello", nil)
	if noClient.hasActivity() {
		t.Fatal("unconfigured submit showed activity despite not starting a run")
	}
}

func TestWorkingIndicatorTickLifecycleDoesNotScroll(t *testing.T) {
	m := workingTestUI(t)
	m.working, m.runID = true, 12
	m.showActivity()
	gen := m.activityGeneration
	m.scroll, m.following = 3, false
	for i := 0; i < 10; i++ {
		_, cmd := m.Update(activityTickMsg{runID: m.runID, generation: gen})
		if cmd == nil {
			t.Fatalf("tick %d unexpectedly stopped", i)
		}
	}
	if m.activityFrame != 10 {
		t.Fatalf("activity frame=%d, want 10", m.activityFrame)
	}
	if m.scroll != 3 || m.following {
		t.Fatalf("activity ticks changed scroll state: scroll=%d following=%t", m.scroll, m.following)
	}
	frame := m.activityFrame
	m.Update(activityTickMsg{runID: m.runID - 1, generation: gen})
	if m.activityFrame != frame {
		t.Fatal("stale run tick advanced activity")
	}
	m.hideActivity()
	if _, cmd := m.Update(activityTickMsg{runID: m.runID, generation: gen}); cmd != nil {
		t.Fatal("tick re-armed after activity was hidden")
	}
}

func TestWorkingIndicatorHandoffAndRoundRearm(t *testing.T) {
	m := workingTestUI(t)
	m.working, m.runID = true, 4
	m.reasoningStream = -1
	m.events = make(chan agent.TurnEvent, 8)
	m.showActivity()
	count := len(m.entries)
	m.Update(agent.TurnEvent{RunID: 4, Kind: "context", ContextKnown: true, ContextTokens: 123})
	if !m.hasActivity() || len(m.entries) != count {
		t.Fatal("context telemetry dismissed the activity row")
	}
	m.Update(agent.TurnEvent{RunID: 4, Kind: "reasoning", Text: "real reasoning"})
	if m.hasActivity() || len(m.entries) != count || m.entries[len(m.entries)-1].role != "Reasoning" || m.entries[len(m.entries)-1].content != "real reasoning" {
		t.Fatalf("reasoning did not replace placeholder in place: %+v", m.entries)
	}

	answer := workingTestUI(t)
	answer.working, answer.runID = true, 41
	answer.streaming, answer.reasoningStream = -1, -1
	answer.showActivity()
	answerCount := len(answer.entries)
	answer.Update(agent.TurnEvent{RunID: 41, Kind: "text", Text: "real answer"})
	if answer.hasActivity() || len(answer.entries) != answerCount || answer.entries[len(answer.entries)-1].role != "Assistant" || answer.entries[len(answer.entries)-1].content != "real answer" {
		t.Fatalf("answer did not replace placeholder in place: %+v", answer.entries)
	}

	tool := workingTestUI(t)
	tool.working, tool.runID = true, 5
	tool.events = make(chan agent.TurnEvent, 8)
	tool.showActivity()
	tool.Update(agent.TurnEvent{RunID: 5, Kind: "tool_start", Text: "Tool: reading"})
	if tool.hasActivity() || tool.entries[len(tool.entries)-1].role != "Tool" {
		t.Fatalf("tool_start did not replace activity: %+v", tool.entries)
	}
	tool.Update(agent.TurnEvent{RunID: 5, Kind: "tool_result", Text: "Tool: done"})
	if !tool.hasActivity() || tool.entries[len(tool.entries)-1].role != "Working" {
		t.Fatalf("tool result did not re-arm activity: %+v", tool.entries)
	}

	steer := workingTestUI(t)
	steer.working, steer.runID = true, 6
	steer.events = make(chan agent.TurnEvent, 8)
	steer.showActivity()
	steer.Update(agent.TurnEvent{RunID: 6, Kind: "steer", Text: "follow up", History: []model.Message{{Role: "user", Content: "follow up"}}})
	if !steer.hasActivity() || steer.entries[len(steer.entries)-1].role != "Working" {
		t.Fatalf("steer delivery did not re-arm activity: %+v", steer.entries)
	}

	approval := workingTestUI(t)
	approval.working, approval.runID = true, 7
	approval.showActivity()
	approval.Update(agent.TurnEvent{RunID: 7, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "edit", Title: "Review", Body: "diff", Reply: make(chan bool, 1)}})
	if approval.hasActivity() || approval.pending == nil {
		t.Fatalf("approval did not take over from activity: pending=%v entries=%+v", approval.pending != nil, approval.entries)
	}
}

func TestWorkingIndicatorTerminalCleanupAndPersistence(t *testing.T) {
	for _, kind := range []string{"done", "error", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			m := workingTestUI(t)
			m.working, m.runID = true, 9
			m.events = make(chan agent.TurnEvent, 2)
			m.showActivity()
			if kind == "cancel" {
				m.cancelling = true
			}
			eventKind := kind
			if kind == "cancel" {
				eventKind = "done"
			}
			m.Update(agent.TurnEvent{RunID: 9, Kind: eventKind, Text: "provider failed", History: []model.Message{{Role: "user", Content: "hello"}}})
			if m.hasActivity() || m.working {
				t.Fatalf("terminal %s left activity or working state", kind)
			}
			if _, cmd := m.Update(activityTickMsg{runID: 9, generation: m.activityGeneration}); cmd != nil {
				t.Fatalf("terminal %s tick re-armed", kind)
			}
			want := map[string]string{"done": "Ready", "error": "Error", "cancel": "Cancelled"}[kind]
			if m.status != want {
				t.Fatalf("terminal %s status=%q, want %q", kind, m.status, want)
			}
		})
	}
	compact := workingTestUI(t)
	compact.working, compact.runID = true, 10
	compact.showActivity()
	compact.Update(agent.TurnEvent{RunID: 10, Kind: "compacted", Text: "brief", History: []model.Message{{Role: "user", Content: "brief"}}})
	if compact.hasActivity() || compact.working || compact.entries[len(compact.entries)-1].role != "Lisa" {
		t.Fatalf("compaction completion left activity or lost marker: %+v", compact.entries)
	}

	root, base := t.TempDir(), t.TempDir()
	store, err := session.Open(base, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, base, store, snapshot)
	m.working = true
	m.entries = append(m.entries, entry{role: "You", content: "hello"})
	m.showActivity()
	m.persist()
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range saved.Entries {
		if e.Role == "Working" {
			t.Fatal("ephemeral activity was persisted")
		}
	}
	resumed := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, base, store, saved)
	if resumed.hasActivity() {
		t.Fatal("resumed session restored ephemeral activity")
	}
}
