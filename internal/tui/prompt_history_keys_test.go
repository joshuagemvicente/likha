package tui

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/providers"
	"lisa/internal/session"
)

func newPromptHistoryKeysUI(t *testing.T, prompts []string) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{PromptHistory: prompts})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func pressComposerKey(m *ui, key tea.KeyType) {
	m.Update(tea.KeyMsg{Type: key})
}

func TestPromptHistoryRecallAndRestoreDraft(t *testing.T) {
	m := newPromptHistoryKeysUI(t, []string{"older", "newest"})
	m.input = []rune("unsent draft")
	m.edit.endCaret(m.input)

	pressComposerKey(m, tea.KeyUp) // first edge press moves to the start
	if string(m.input) != "unsent draft" || m.edit.caret != 0 {
		t.Fatalf("first Up = %q at %d; want draft at start", string(m.input), m.edit.caret)
	}
	pressComposerKey(m, tea.KeyUp)
	if string(m.input) != "newest" {
		t.Fatalf("first recalled prompt = %q; want newest", string(m.input))
	}
	pressComposerKey(m, tea.KeyUp) // move to start before recalling older
	pressComposerKey(m, tea.KeyUp)
	if string(m.input) != "older" {
		t.Fatalf("older prompt = %q; want older", string(m.input))
	}
	pressComposerKey(m, tea.KeyDown)
	if string(m.input) != "newest" {
		t.Fatalf("newer prompt = %q; want newest", string(m.input))
	}
	pressComposerKey(m, tea.KeyDown)
	if string(m.input) != "unsent draft" {
		t.Fatalf("Down did not restore exact draft: %q", string(m.input))
	}
}

func TestPromptHistoryEditResetsTraversalWithoutLosingDraft(t *testing.T) {
	m := newPromptHistoryKeysUI(t, []string{"older", "newest"})
	m.input = []rune("draft")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyUp)
	pressComposerKey(m, tea.KeyUp)
	if string(m.input) != "newest" {
		t.Fatalf("recalled prompt = %q; want newest", string(m.input))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	if string(m.input) != "newest!" {
		t.Fatalf("edited recalled prompt = %q; want newest!", string(m.input))
	}
	pressComposerKey(m, tea.KeyDown)
	if string(m.input) != "newest!" {
		t.Fatalf("Down trapped or discarded the edited draft: %q", string(m.input))
	}
	pressComposerKey(m, tea.KeyUp) // edge -> start
	pressComposerKey(m, tea.KeyUp) // begin a fresh traversal
	pressComposerKey(m, tea.KeyDown)
	if string(m.input) != "newest!" {
		t.Fatalf("fresh traversal did not restore edited draft: %q", string(m.input))
	}
}

func TestPromptHistoryPopupsOwnArrowKeys(t *testing.T) {
	m := newPromptHistoryKeysUI(t, []string{"older", "newest"})
	m.input = []rune("/")
	m.edit.endCaret(m.input)
	m.commandPopup.open = true
	m.syncCommand()
	if len(m.commandPopup.matches) < 2 {
		t.Fatal("expected command matches for popup test")
	}
	m.commandPopup.cursor = 1
	pressComposerKey(m, tea.KeyUp)
	if m.commandPopup.cursor != 0 || string(m.input) != "/" || m.promptHistory.cursor != len(m.promptHistory.items) {
		t.Fatalf("Up escaped command popup: cursor=%d input=%q history cursor=%d", m.commandPopup.cursor, string(m.input), m.promptHistory.cursor)
	}

	m.commandPopup.open = false
	m.input = []rune("@")
	m.edit.endCaret(m.input)
	m.mention.open = true
	m.mention.matches = []string{"alpha", "beta"}
	m.mention.cursor = 1
	pressComposerKey(m, tea.KeyUp)
	if m.mention.cursor != 0 || string(m.input) != "@" || m.promptHistory.cursor != len(m.promptHistory.items) {
		t.Fatalf("Up escaped mention popup: cursor=%d input=%q history cursor=%d", m.mention.cursor, string(m.input), m.promptHistory.cursor)
	}
}

func TestPromptHistoryRecordsAcceptedQueueAndIdleCommands(t *testing.T) {
	m := newPromptHistoryKeysUI(t, nil)
	m.working = true
	m.input = []rune("/help")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyEnter)
	if got := m.promptHistory.all(); len(got) != 0 {
		t.Fatalf("rejected slash entered history: %v", got)
	}

	m.input = []rune("//literal")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyEnter)
	m.input = []rune("steer me")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyEnter)
	want := []string{"literal", "steer me"}
	if got := m.promptHistory.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("queued prompt history = %q; want %q", got, want)
	}
	if !reflect.DeepEqual(m.queue, want) {
		t.Fatalf("queued prompts = %q; want %q", m.queue, want)
	}
	m.working = false
	m.input = nil
	m.edit.endCaret(nil)
	pressComposerKey(m, tea.KeyEnter) // deliver the held queue explicitly
	if got := m.promptHistory.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("held-queue delivery recorded prompts twice: %q; want %q", got, want)
	}

	idle := newPromptHistoryKeysUI(t, nil)
	idle.input = []rune("/help")
	idle.edit.endCaret(idle.input)
	pressComposerKey(idle, tea.KeyEnter)
	if got := idle.promptHistory.all(); !reflect.DeepEqual(got, []string{"/help"}) {
		t.Fatalf("idle slash command history = %q; want [/help]", got)
	}

	escaped := newPromptHistoryKeysUI(t, nil)
	escaped.input = []rune("//literal")
	escaped.edit.endCaret(escaped.input)
	pressComposerKey(escaped, tea.KeyEnter)
	if got := escaped.promptHistory.all(); len(got) != 0 || string(escaped.input) != "/literal" {
		t.Fatalf("unavailable-provider prompt was accepted: history=%q draft=%q", got, string(escaped.input))
	}
}

func TestPromptHistoryDoesNotRecordEmptyOrTooSmallSubmit(t *testing.T) {
	m := newPromptHistoryKeysUI(t, nil)
	pressComposerKey(m, tea.KeyEnter)
	m.input = []rune("too small")
	m.edit.endCaret(m.input)
	m.width, m.height = minWidth-1, minHeight-1
	pressComposerKey(m, tea.KeyEnter)
	if got := m.promptHistory.all(); len(got) != 0 {
		t.Fatalf("unaccepted prompt entered history: %v", got)
	}
	if string(m.input) != "too small" {
		t.Fatalf("too-small submit changed draft: %q", string(m.input))
	}
}

func TestPromptHistoryCaretMovesByVisualRowAndStopsAtEdges(t *testing.T) {
	m := newPromptHistoryKeysUI(t, []string{"past"})
	m.width = 5
	m.input = []rune("abcdefghij")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyUp)
	if m.edit.caret != 5 {
		t.Fatalf("Up within wrapped input caret = %d; want 5", m.edit.caret)
	}
	pressComposerKey(m, tea.KeyDown)
	if m.edit.caret != 10 {
		t.Fatalf("Down within wrapped input caret = %d; want 10", m.edit.caret)
	}

	m.composerVertical.reset()
	m.input = []rune("abcdef")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyUp) // internal row to first row, same column
	if m.edit.caret != 1 {
		t.Fatalf("Up to first visual row caret = %d; want 1", m.edit.caret)
	}
	pressComposerKey(m, tea.KeyUp) // first-row edge moves to start
	if m.edit.caret != 0 {
		t.Fatalf("Up at first-row edge caret = %d; want 0", m.edit.caret)
	}
	pressComposerKey(m, tea.KeyUp) // next press recalls history
	if string(m.input) != "past" {
		t.Fatalf("Up past first-row edge = %q; want past", string(m.input))
	}
}

func TestPromptHistoryPersistCopiesSubmittedPrompts(t *testing.T) {
	m := newPromptHistoryKeysUI(t, []string{"saved"})
	m.promptHistory.append("new")
	m.persist()
	if want := []string{"saved", "new"}; !reflect.DeepEqual(m.snapshot.PromptHistory, want) {
		t.Fatalf("snapshot prompt history = %q; want %q", m.snapshot.PromptHistory, want)
	}
}

func TestLegacySessionSeedsPromptHistoryFromTranscript(t *testing.T) {
	snapshot := session.Snapshot{
		Entries: []session.Entry{
			{Role: "You", Content: "first prompt"},
			{Role: "Assistant", Content: "response"},
			{Role: "You", Content: "second prompt"},
		},
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if want := []string{"first prompt", "second prompt"}; !reflect.DeepEqual(m.promptHistory.all(), want) {
		t.Fatalf("legacy prompt history = %q; want %q", m.promptHistory.all(), want)
	}
	pressComposerKey(m, tea.KeyUp)
	if string(m.input) != "second prompt" {
		t.Fatalf("legacy history recalled %q; want newest prompt", string(m.input))
	}
}

func TestAcceptedPromptHistoryPersistsAndResumesPerSession(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}

	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input = []rune("/help")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyEnter)

	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/help"}; !reflect.DeepEqual(loaded.PromptHistory, want) {
		t.Fatalf("saved history = %q; want %q", loaded.PromptHistory, want)
	}

	resumed := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, loaded)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	pressComposerKey(resumed, tea.KeyUp)
	if string(resumed.input) != "/help" {
		t.Fatalf("resumed prompt history recalled %q; want /help", string(resumed.input))
	}
}

func TestUnknownSlashCommandDoesNotEnterPromptHistory(t *testing.T) {
	m := newPromptHistoryKeysUI(t, nil)
	m.input = []rune("/not-a-command")
	m.edit.endCaret(m.input)
	pressComposerKey(m, tea.KeyEnter)
	if got := m.promptHistory.all(); len(got) != 0 {
		t.Fatalf("unknown slash command entered history: %q", got)
	}
	if string(m.input) != "/not-a-command" {
		t.Fatalf("unknown slash command draft was lost: %q", string(m.input))
	}
}
