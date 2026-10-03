package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

func TestConversationPersistsAndResumesWithContext(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	store, err := session.Open(base, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, snapshot)
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.entries = append(m.entries, entry{role: "You", content: "What changed?"}, entry{role: "Assistant", content: "The file was updated."})
	history := []model.Message{{Role: "user", Content: "What changed?"}, {Role: "assistant", Content: "The file was updated."}}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "done", History: history})

	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, saved)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(resumed.View(), "The file was updated.") || len(resumed.history) != 2 || resumed.history[1].Content != "The file was updated." {
		t.Fatalf("completed conversation not resumed: entries = %+v, history = %+v", resumed.entries, resumed.history)
	}
}

func TestInterruptedApprovalDoesNotResumeAsPermission(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	store, err := session.Open(base, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.entries = append(m.entries, entry{role: "You", content: "Proceed safely"})
	m.persist()
	m.working, m.runID = true, 1
	m.cancel = func() {}
	pending := &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "Working directory: " + root + "\nCommand: touch marker", Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: pending})
	if m.pending == nil {
		t.Fatal("approval did not appear")
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, loaded)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if resumed.pending != nil || resumed.working || strings.Contains(resumed.View(), "touch marker") || len(pending.Reply) != 0 {
		t.Fatal("unfinished command approval survived a session restart")
	}
	if _, err := os.Stat(root + "/marker"); !os.IsNotExist(err) {
		t.Fatalf("pending command ran unexpectedly: %v", err)
	}
}
