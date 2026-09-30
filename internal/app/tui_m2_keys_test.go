package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"lisa/internal/session"
)

// Update-level tests for the M2 composer chords (specs/tool-rendering-terminal-keys).

func newKeysTestUI(t *testing.T) *ui {
	t.Helper()
	m := newUI("/sample", nil, nil, "", connection{provider: "OpenAI", verified: true}, t.TempDir(), nil, session.Snapshot{})
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

func TestComposerInertWhileWorking(t *testing.T) {
	m := newKeysTestUI(t)
	m.input = []rune("draft text")
	m.edit.endCaret(m.input)
	m.working = true
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
		{Type: tea.KeyEnter},
	} {
		m.Update(key)
	}
	if string(m.input) != "draft text" {
		t.Fatalf("editing leaked while working: %q", string(m.input))
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
