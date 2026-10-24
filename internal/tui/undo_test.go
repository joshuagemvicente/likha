package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/actions"
	"likha/internal/providers"
	"likha/internal/session"
)

// Command, review and resume tests for specs/edit-undo.

type undoFixture struct {
	root, base string
	store      *session.Store
	m          *ui
}

func newUndoFixture(t *testing.T) *undoFixture {
	t.Helper()
	root, base := t.TempDir(), t.TempDir()
	store, err := session.Open(base, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return &undoFixture{root: root, base: base, store: store, m: m}
}

// applyEdit writes path through the edit tool's apply and records its change
// set through the same hook a run uses.
func (f *undoFixture) applyEdit(t *testing.T, callID, path, content string) {
	t.Helper()
	edit, err := actions.PrepareEdit(f.root, path, content)
	if err != nil {
		t.Fatal(err)
	}
	if err := edit.Apply(); err != nil {
		t.Fatal(err)
	}
	set, ok := edit.ChangeSet()
	if !ok {
		t.Fatal("applied edit has no change set")
	}
	record := f.m.toolRunOptions(0).RecordChangeSet
	if record == nil {
		t.Fatal("a session-backed run has no change-set hook")
	}
	if err := record(callID, set); err != nil {
		t.Fatal(err)
	}
}

func (f *undoFixture) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func submit(m *ui, line string) {
	sendRunes(m, line)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestUndoReviewRevertsNewestChangeSet(t *testing.T) {
	f := newUndoFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f.applyEdit(t, "call_1", "a.txt", "new\n")
	// Approve always for edits never covers an undo (D2).
	f.m.editGrant.Allow()

	submit(f.m, "/undo")
	if f.m.pending == nil || f.m.pending.Kind != undoReviewKind || f.m.undo == nil {
		t.Fatalf("no undo review opened; last entry %+v", lastEntry(f.m))
	}
	if !strings.HasPrefix(f.m.pending.Title, "Undo: 1 file(s) from ") || !strings.Contains(f.m.pending.Body, "-new\n+old\n") {
		t.Fatalf("undo review = %q\n%s", f.m.pending.Title, f.m.pending.Body)
	}
	if f.m.pending.Remember != "" {
		t.Fatal("the undo review offers Approve always")
	}
	bar := stripANSI(strings.Join(f.m.composerLines(), "\n"))
	if !strings.Contains(bar, "[ Undo ]") || !strings.Contains(bar, "[ Cancel ]") || strings.Contains(bar, "Approve") {
		t.Fatalf("decision bar = %q", bar)
	}
	if f.read(t, "a.txt") != "new\n" {
		t.Fatal("opening the review wrote to the repository")
	}

	f.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.m.pending != nil || f.m.undo != nil {
		t.Fatal("review still open after Undo")
	}
	if f.read(t, "a.txt") != "old\n" {
		t.Fatalf("a.txt = %q, want the original", f.read(t, "a.txt"))
	}
	last := lastEntry(f.m)
	if last.role != "Likha" || !strings.HasPrefix(last.content, "Undid 1 file(s) from ") || !strings.HasSuffix(last.content, ": a.txt. Effects of shell commands are not undone.") {
		t.Fatalf("result entry = %+v", last)
	}
	note := f.m.history[len(f.m.history)-1]
	if note.Role != "developer" || !strings.Contains(note.Content, "restored a.txt") || strings.Contains(note.Content, "old\n") || strings.Contains(note.Content, "new\n") {
		t.Fatalf("model note = %+v", note)
	}
	saved, err := f.store.Load(f.m.snapshot.ID)
	if err != nil || len(saved.History) == 0 || saved.History[len(saved.History)-1].Content != note.Content {
		t.Fatalf("the model note was not persisted: %v", err)
	}
	// The revert ran through the private edit journal (D10): its record
	// is complete, so resume reports nothing interrupted.
	journals, err := f.store.EditJournals(f.m.snapshot.ID)
	if err != nil || len(journals) != 1 || !strings.Contains(string(journals[0].Progress), `"status":"completed"`) {
		t.Fatalf("undo journal = %+v, %v", journals, err)
	}

	submit(f.m, "/undo")
	if last := lastEntry(f.m); last.content != undoNothing || f.m.pending != nil {
		t.Fatalf("second /undo = %+v", last)
	}
}

func TestUndoRepeatsNewestFirst(t *testing.T) {
	f := newUndoFixture(t)
	f.applyEdit(t, "call_1", "a.txt", "one\n")
	f.applyEdit(t, "call_2", "b.txt", "two\n")

	submit(f.m, "/undo")
	f.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(f.root, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("newest change (b.txt) not reverted first: %v", err)
	}
	if f.read(t, "a.txt") != "one\n" {
		t.Fatal("older change reverted early")
	}
	submit(f.m, "/undo")
	f.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(f.root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("older change not reverted: %v", err)
	}
	submit(f.m, "/undo")
	if last := lastEntry(f.m); last.content != undoNothing {
		t.Fatalf("third /undo = %+v", last)
	}
}

func TestUndoRefusesFileChangedSinceLikhaWroteIt(t *testing.T) {
	f := newUndoFixture(t)
	f.applyEdit(t, "call_1", "a.txt", "likha\n")
	if err := os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("user\n"), 0644); err != nil {
		t.Fatal(err)
	}
	submit(f.m, "/undo")
	last := lastEntry(f.m)
	if f.m.pending != nil || last.role != "Error" || last.content != "Can't undo: a.txt changed since Likha wrote it. Nothing was reverted." {
		t.Fatalf("changed file: pending %v, last %+v", f.m.pending, last)
	}
	if f.read(t, "a.txt") != "user\n" {
		t.Fatal("refused undo wrote to the file")
	}
	if _, ok, _ := f.store.LatestChangeSet(f.m.snapshot.ID); !ok {
		t.Fatal("a refused undo dropped the change set")
	}
}

func TestUndoCancelKeepsChangeSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
	}{
		{"escape", []tea.KeyMsg{{Type: tea.KeyEsc}}},
		{"cancel button", []tea.KeyMsg{{Type: tea.KeyRight}, {Type: tea.KeyEnter}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUndoFixture(t)
			f.applyEdit(t, "call_1", "a.txt", "one\n")
			submit(f.m, "/undo")
			for _, key := range tc.keys {
				f.m.Update(key)
			}
			if f.m.pending != nil || f.m.undo != nil || lastEntry(f.m).content != "Undo cancelled; nothing was written." {
				t.Fatalf("cancel: pending %v, last %+v", f.m.pending, lastEntry(f.m))
			}
			if f.read(t, "a.txt") != "one\n" {
				t.Fatal("a cancelled undo wrote to the file")
			}
			submit(f.m, "/undo")
			if f.m.pending == nil {
				t.Fatal("the change set was not undoable after a cancel")
			}
		})
	}
}

func TestUndoArgumentsAndBusyStatesRefuse(t *testing.T) {
	f := newUndoFixture(t)
	f.applyEdit(t, "call_1", "a.txt", "one\n")

	submit(f.m, "/undo everything")
	if last := lastEntry(f.m); last.content != undoArgumentError || string(f.m.input) != "/undo everything" {
		t.Fatalf("argument refusal: %+v, draft %q", last, string(f.m.input))
	}
	f.m.input = nil
	f.m.edit.endCaret(nil)

	f.m.working = true
	f.m.handleCommand("/undo")
	if last := lastEntry(f.m); last.content != runDraftCommandRefusal || f.m.pending != nil {
		t.Fatalf("during a run: %+v", last)
	}
	f.m.working = false

	submit(f.m, "/undo")
	review := f.m.pending
	f.m.handleCommand("/undo")
	if last := lastEntry(f.m); !strings.Contains(last.content, "A review is pending") || f.m.pending != review {
		t.Fatalf("during a review: %+v", last)
	}
	if f.read(t, "a.txt") != "one\n" {
		t.Fatal("a refused /undo wrote to the file")
	}
}

func TestUndoReviewNamesCommandsThatRanAfterTheChange(t *testing.T) {
	f := newUndoFixture(t)
	f.applyEdit(t, "call_edit", "a.txt", "one\n")
	f.m.toolRecords = []session.ToolRecord{
		{CallID: "call_old", Name: "run_command", Status: "succeeded"},
		{CallID: "call_edit", Name: "edit_file", Status: "succeeded"},
	}
	submit(f.m, "/undo")
	if f.m.pending == nil || f.m.pending.Warning != "" {
		t.Fatalf("a command before the change produced a notice: %+v", f.m.pending)
	}
	f.m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	f.m.toolRecords = append(f.m.toolRecords,
		session.ToolRecord{CallID: "call_declined", Name: "run_command", Status: "refused"})
	submit(f.m, "/undo")
	if f.m.pending.Warning != "" {
		t.Fatal("a declined command produced a notice")
	}
	f.m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	f.m.toolRecords = append(f.m.toolRecords,
		session.ToolRecord{CallID: "call_ran", Name: "run_command", Status: "failed"})
	submit(f.m, "/undo")
	if f.m.pending.Warning != undoCommandsRan {
		t.Fatalf("warning = %q", f.m.pending.Warning)
	}
}

func TestUndoWorksAfterResume(t *testing.T) {
	f := newUndoFixture(t)
	f.applyEdit(t, "call_1", "a.txt", "one\n")
	id := f.m.snapshot.ID
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.Open(f.base, f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(f.root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", reopened, loaded)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	submit(m, "/undo")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(f.root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("undo after resume did not remove the created file: %v", err)
	}

	// Another session of the same repository never sees this history.
	other, err := reopened.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := reopened.LatestChangeSet(other.ID); ok {
		t.Fatal("another session sees the change set")
	}
}

func TestUndoSaysWhenOlderChangesWereDropped(t *testing.T) {
	f := newUndoFixture(t)
	for i := range session.MaxChangeSets + 1 {
		f.applyEdit(t, "call", filepath.Join("f"+string(rune('a'+i))+".txt"), "x\n")
	}
	for {
		record, ok, err := f.store.LatestChangeSet(f.m.snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if err := f.store.DeleteChangeSet(f.m.snapshot.ID, record.ID); err != nil {
			t.Fatal(err)
		}
	}
	submit(f.m, "/undo")
	if last := lastEntry(f.m); last.content != undoNothingDropped {
		t.Fatalf("last = %+v", last)
	}
}

func TestUndoListedInHelpAndAutocomplete(t *testing.T) {
	if !strings.Contains(commandHelp, "/undo ") {
		t.Fatal("/help does not list /undo")
	}
	found := false
	for _, item := range commands {
		found = found || item.Name == "undo"
	}
	if !found {
		t.Fatal("the / autocomplete does not offer undo")
	}
}
