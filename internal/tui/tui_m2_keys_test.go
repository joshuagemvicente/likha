package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

// Update-level tests for the M2 composer chords (specs/tool-rendering-terminal-keys).

func newKeysTestUI(t *testing.T) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func chord(m *ui, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: true})
}

func TestComposerChordWiring(t *testing.T) {
	m := newKeysTestUI(t)

	// Ctrl+W kills the previous word with its run in one keystroke.
	sendRunes(m, "hello world")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if string(m.input) != "hello" {
		t.Fatalf("ctrl+w = %q", string(m.input))
	}
	m.input = nil
	m.edit.endCaret(nil)

	// Alt+Backspace is the same action.
	sendRunes(m, "hello world")
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace, Alt: true})
	if string(m.input) != "hello" {
		t.Fatalf("alt+backspace = %q", string(m.input))
	}
	m.input = nil
	m.edit.endCaret(nil)

	// Alt+B moves to the word start; typed runes land there.
	sendRunes(m, "hello world")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	if m.edit.caret != 6 {
		t.Fatalf("caret after alt+b = %d, want 6", m.edit.caret)
	}
	sendRunes(m, "X")
	if string(m.input) != "hello Xworld" {
		t.Fatalf("insert at caret = %q", string(m.input))
	}
	m.input = nil
	m.edit.endCaret(nil)

	// Ctrl+K kills to the end; Ctrl+Y restores the newest kill at the caret.
	sendRunes(m, "hello world")
	m.edit.caret = 5
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if string(m.input) != "hello" {
		t.Fatalf("ctrl+k = %q", string(m.input))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if string(m.input) != "hello world" {
		t.Fatalf("ctrl+y = %q", string(m.input))
	}
	m.input = nil
	m.edit.endCaret(nil)

	// Ctrl+U kills to the start.
	sendRunes(m, "hello world")
	m.edit.caret = 5
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if string(m.input) != " world" {
		t.Fatalf("ctrl+u = %q", string(m.input))
	}
	m.input = nil
	m.edit.endCaret(nil)

	// Ctrl+T at the end of the buffer: the last two runes swap.
	sendRunes(m, "abcd")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if string(m.input) != "abdc" {
		t.Fatalf("ctrl+t = %q", string(m.input))
	}
}

// While a run is active and no review is pending, the composer stays live:
// edits apply and Enter queues the draft instead of sending it (FR-21).
func TestTuiKeyComposerLiveWhileWorking(t *testing.T) {
	m := newKeysTestUI(t)
	m.working = true
	sendRunes(m, "hello world")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if string(m.input) != "hello" {
		t.Fatalf("ctrl+w while working = %q, want %q", string(m.input), "hello")
	}
	if !m.caretVisible() {
		t.Fatal("caret hidden while a run is active")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := string(m.input); got != "" {
		t.Fatalf("queued draft not cleared: %q", got)
	}
	if len(m.queue) != 1 || m.queue[0] != "hello" {
		t.Fatalf("queue = %v, want [hello]", m.queue)
	}
	if last := m.entries[len(m.entries)-1]; last.role != "Queued" || last.content != "hello" {
		t.Fatalf("queued row = %+v, want role Queued content hello", last)
	}
}

// While a review owns the keyboard, editing is inert and no draft is queued.
func TestTuiKeyComposerInertWhileReviewing(t *testing.T) {
	m := newKeysTestUI(t)
	request := tuiKeyApproval(m, "command", "Shell command", "touch marker")
	m.input = []rune("draft text")
	m.edit.endCaret(m.input)
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyCtrlW},
		{Type: tea.KeyBackspace, Alt: true},
		{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true},
		{Type: tea.KeyCtrlU},
		{Type: tea.KeyCtrlK},
		{Type: tea.KeyCtrlY},
		{Type: tea.KeyCtrlT},
		{Type: tea.KeyCtrlJ},
		{Type: tea.KeyRunes, Runes: []rune("nope")},
	} {
		m.Update(key)
	}
	if string(m.input) != "draft text" {
		t.Fatalf("editing leaked while reviewing: %q", string(m.input))
	}
	if len(m.queue) != 0 {
		t.Fatalf("reviewing queued a draft: %v", m.queue)
	}
	if m.pending == nil || len(request.Reply) != 0 {
		t.Fatal("editing keys decided the review")
	}
}

// Enter with a plain draft while working appends a Queued row, pushes the
// queue, clears the draft, and leaves history untouched (FR-21).
func TestTuiKeyEnterQueuesPlainDraft(t *testing.T) {
	m := newKeysTestUI(t)
	m.working = true
	m.history = []model.Message{{Role: "user", Content: "prior"}}
	m.input = []rune("queue me")
	m.edit.endCaret(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := string(m.input); got != "" {
		t.Fatalf("draft not cleared after queueing: %q", got)
	}
	if len(m.queue) != 1 || m.queue[0] != "queue me" {
		t.Fatalf("queue = %v, want [queue me]", m.queue)
	}
	if last := m.entries[len(m.entries)-1]; last.role != "Queued" || last.content != "queue me" {
		t.Fatalf("queued row = %+v", last)
	}
	if len(m.history) != 1 || m.history[0].Content != "prior" {
		t.Fatalf("queueing changed history: %+v", m.history)
	}
}

// //word keeps its escape while working: the literal remainder is queued.
func TestTuiKeyDoubleSlashQueuesLiteralPrompt(t *testing.T) {
	m := newKeysTestUI(t)
	m.working = true
	m.input = []rune("//x")
	m.edit.endCaret(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.queue) != 1 || m.queue[0] != "x" {
		t.Fatalf("//x queued %v, want [x]", m.queue)
	}
	if last := m.entries[len(m.entries)-1]; last.role != "Queued" || last.content != "x" {
		t.Fatalf("//x row = %+v, want Queued x", last)
	}
	if len(m.input) != 0 {
		t.Fatalf("//x kept the draft: %q", string(m.input))
	}
}

// A slash command is refused while a run is active: the frozen message is
// shown, the draft is kept, and nothing is queued.
func TestTuiKeySlashCommandRefusedWhileWorking(t *testing.T) {
	m := newKeysTestUI(t)
	m.working = true
	m.history = []model.Message{{Role: "user", Content: "prior"}}
	m.input = []rune("/compact")
	m.edit.endCaret(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	const refusal = "Commands are inactive while a run is active. Esc cancels the run; // queues a literal slash."
	last := m.entries[len(m.entries)-1]
	if last.role != "Error" || last.content != refusal {
		t.Fatalf("refusal entry = %+v", last)
	}
	if got := string(m.input); got != "/compact" {
		t.Fatalf("refusal did not keep the draft: %q", got)
	}
	if len(m.queue) != 0 {
		t.Fatalf("refusal queued %v", m.queue)
	}
	if len(m.history) != 1 || m.history[0].Content != "prior" {
		t.Fatalf("refusal changed history: %+v", m.history)
	}
}

// A steer event adopts the engine history, flips the matching Queued row to
// You, and pops the queue.
func TestTuiKeySteerEventFlipsQueuedRow(t *testing.T) {
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	m.entries = append(m.entries, entry{role: "Queued", content: "steer me"})
	m.queue = []string{"steer me"}
	m.history = []model.Message{{Role: "user", Content: "seed"}}
	history := []model.Message{{Role: "user", Content: "seed"}, {Role: "user", Content: "steer me"}}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "steer", Text: "steer me", History: history})
	if len(m.history) != 2 || m.history[1].Content != "steer me" {
		t.Fatalf("history not adopted: %+v", m.history)
	}
	if len(m.queue) != 0 {
		t.Fatalf("queue not popped: %v", m.queue)
	}
	delivered := 0
	for _, e := range m.entries {
		if e.role == "Queued" {
			t.Fatalf("stale Queued row left: %+v", m.entries)
		}
		if e.content == "steer me" {
			if e.role != "You" {
				t.Fatalf("delivered row role = %q, want You", e.role)
			}
			delivered++
		}
	}
	if delivered != 1 {
		t.Fatalf("steer row count = %d, want 1", delivered)
	}
}

// A terminal event with a non-empty queue holds it: rows and queue survive,
// no turn starts, and Enter on an empty draft sends the whole batch (the
// head becomes the prompt, the rest pre-fill the fresh queue).
func TestTuiKeyTerminalEventHoldsQueue(t *testing.T) {
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 1)
	m.entries = append(m.entries,
		entry{role: "Queued", content: "head"},
		entry{role: "Queued", content: "rest"})
	m.queue = []string{"head", "rest"}
	m.history = []model.Message{{Role: "user", Content: "first"}}
	done := []model.Message{{Role: "user", Content: "first"}, {Role: "assistant", Content: "ok"}}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "done", History: done})
	if m.working {
		t.Fatal("done left the run working")
	}
	if len(m.queue) != 2 {
		t.Fatalf("queue after hold = %v, want both messages", m.queue)
	}
	queuedRows := 0
	for _, e := range m.entries {
		if e.role == "Queued" {
			queuedRows++
		}
	}
	if queuedRows != 2 {
		t.Fatalf("queued rows after hold = %d, want 2 (%+v)", queuedRows, m.entries)
	}
	// Enter on an empty draft sends the batch: with no client, startTurn
	// restores the head as the draft and keeps the rest queued.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := string(m.input); got != "head" {
		t.Fatalf("held-queue send prompt = %q, want %q", got, "head")
	}
	if len(m.queue) != 1 || m.queue[0] != "rest" {
		t.Fatalf("rest queue = %v, want [rest]", m.queue)
	}
}

func TestEscPrefixReturnInsertsNewlineAndDecayClears(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "hello")
	// ESC then Return inside the decay window: newline, not submit.
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if string(m.input) != "hello\n" {
		t.Fatalf("esc+enter = %q, want newline inserted", string(m.input))
	}
	// ESC then another chord: the window cancels, the chord edits normally.
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	sendRunes(m, "x")
	if string(m.input) != "hello\nx" {
		t.Fatalf("chord inside window = %q", string(m.input))
	}
	// ESC alone, then decay expiry: the draft clears as bare ESC always did.
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m.Update(escDecayMsg{})
	if len(m.input) != 0 {
		t.Fatalf("decay expiry did not clear: %q", string(m.input))
	}
	// Ctrl+J (the LF byte ctrl+enter sends) inserts a newline too.
	sendRunes(m, "a")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if string(m.input) != "a\n" {
		t.Fatalf("ctrl+j = %q", string(m.input))
	}
	// And plain Enter still submits: with no client, startTurn restores the
	// flattened prompt as the draft — proving the flatten and the submit.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if string(m.input) != "a" {
		t.Fatalf("submitted draft not flattened back: %q", string(m.input))
	}
}

func TestBracketedPasteFlattensAndInsertsAtCaret(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "keep two")
	chord(m, "b") // caret at the start of "two"
	// A bracketed paste arrives as one KeyRunes with Paste set.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("IN\nSERT")})
	if string(m.input) != "keep IN SERTtwo" {
		t.Fatalf("paste at caret = %q", string(m.input))
	}
	if strings.Contains(string(m.input), "\n") {
		t.Fatal("pasted newline must flatten to a space")
	}
}

func TestComposerCaretRendersAtInsertionPoint(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "ab cd ef")
	chord(m, "b") // caret at the start of "ef"
	chord(m, "b") // caret at the start of "cd"
	view := m.composerLines()
	joined := strings.Join(view, "\n")
	// The block renders exactly at the insertion point (index 2, the
	// position between 'b' and the run), not at the tail.
	if !strings.Contains(joined, "ab █cd ef") {
		t.Fatalf("caret not at insertion point: %q", joined)
	}
}

func TestAltChordsDoNotInsertTheirRune(t *testing.T) {
	m := newKeysTestUI(t)
	// alt+d with the caret on the run before "two": run and word die.
	m.input = []rune("word two")
	m.edit.endCaret(m.input)
	m.edit.caret = 4
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d"), Alt: true})
	if string(m.input) != "word" {
		t.Fatalf("alt+d on the run = %q", string(m.input))
	}
	// alt+d at the word start kills the word, leaving the run behind.
	m.input = []rune("word two")
	m.edit.endCaret(m.input)
	m.edit.caret = 5
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d"), Alt: true})
	if string(m.input) != "word " {
		t.Fatalf("alt+d at word start = %q", string(m.input))
	}
	// alt+x is not a bound chord; today's composer inserts the rune — the
	// pre-M2 behavior for unbound alt+runes, unchanged here.
	chord(m, "x")
	if !strings.Contains(string(m.input), "x") {
		t.Fatalf("unbound alt+runes behavior changed: %q", string(m.input))
	}
}
