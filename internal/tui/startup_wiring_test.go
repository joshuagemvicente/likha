package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/providers"
	"likha/internal/session"
)

// openStartupStore opens a real session store for root, as the composition
// root does before NewUI.
func openStartupStore(t *testing.T, stateDir, root string) *session.Store {
	t.Helper()
	store, err := session.Open(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// completeSetup drives first-run setup through the provider, key, model and
// theme stages with the same messages the interactive flow receives.
func completeSetup(t *testing.T, m *ui) {
	t.Helper()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // openai
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-test")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(setupCheckMsg{attempt: m.setup.checkAttempt, models: []string{"alpha"}})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // model
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // theme
	if m.mode != modeMain || m.client == nil {
		t.Fatalf("setup did not complete: mode=%q client=%v err=%q", m.mode, m.client != nil, m.setup.err)
	}
}

// Regression: a session that starts in first-run setup never opened its
// private tool output store, so after setup read_output was unavailable and
// oversized results were not retained until a relaunch.
func TestSetupSessionOpensToolOutputStore(t *testing.T) {
	stateDir, root := t.TempDir(), t.TempDir()
	store := openStartupStore(t, stateDir, root)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Setup: true}, stateDir, store, snapshot)
	completeSetup(t, m)
	if options := m.toolRunOptions(1); options.Outputs == nil {
		t.Fatal("the first run after setup has no private tool output store")
	}
}

// Regression: resuming a session into first-run setup skipped loading its
// transcript, so the first save after setup replaced the stored entries
// with only the new ones.
func TestSetupResumeKeepsStoredTranscript(t *testing.T) {
	stateDir, root := t.TempDir(), t.TempDir()
	store := openStartupStore(t, stateDir, root)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Entries = []session.Entry{{Role: "You", Content: "earlier prompt"}, {Role: "Assistant", Content: "earlier answer"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Setup: true}, stateDir, store, loaded)
	completeSetup(t, m)
	m.persist()
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	var contents []string
	for _, e := range saved.Entries {
		contents = append(contents, e.Content)
	}
	joined := strings.Join(contents, "\n")
	if !strings.Contains(joined, "earlier prompt") || !strings.Contains(joined, "earlier answer") {
		t.Fatalf("stored transcript lost its earlier entries after setup: %q", contents)
	}
}

// Regression: a launch with --resume reconciled unanswered questions before
// the stored transcript was loaded, so a question left open when Likha last
// exited was never reported as interrupted.
func TestResumeLaunchReportsUnansweredQuestion(t *testing.T) {
	stateDir, root := t.TempDir(), t.TempDir()
	store := openStartupStore(t, stateDir, root)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Entries = []session.Entry{{Role: "You", Content: "pick one"}, {Role: "Likha", Content: planQuestionMarker + "\nWhich file?"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "test-model", providers.Connection{Provider: "Test"}, stateDir, store, loaded)
	if !hasEntry(m, "Likha", "A model question from the previous run was interrupted before an answer") {
		t.Fatal("resumed launch did not report the unanswered question")
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if last := saved.Entries[len(saved.Entries)-1].Content; !strings.Contains(last, "interrupted before an answer") {
		t.Fatalf("interruption note not saved; last entry %q", last)
	}
	if len(saved.Entries) != 3 {
		t.Fatalf("saved %d entries, want the two stored plus the note", len(saved.Entries))
	}

	// The saved note settles the question: launching with --resume again
	// adds no second note.
	NewUI(root, nil, nil, "test-model", providers.Connection{Provider: "Test"}, stateDir, store, saved)
	again, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	notes := 0
	for _, e := range again.Entries {
		if strings.Contains(e.Content, "A model question from the previous run was interrupted before an answer") {
			notes++
		}
	}
	if notes != 1 || len(again.Entries) != 3 {
		t.Fatalf("after a second resume the session holds %d notes in %d entries, want 1 note in 3", notes, len(again.Entries))
	}
}
