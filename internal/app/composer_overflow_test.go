package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mattn/go-runewidth"
	"lisa/internal/session"
)

// overflowUI builds a ui instance at the given size for overflow testing.
func overflowUI(t *testing.T, style string, width, height int) *ui {
	t.Helper()
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local", verified: true, composerStyle: style}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// plainWidth measures a rendered row's visible cell count after stripping
// ANSI escape sequences — the width a terminal would actually occupy.
func plainWidth(row string) int {
	return runewidth.StringWidth(stripANSI(row))
}

func TestComposerNoOverflowAnyStyleAnyWidth(t *testing.T) {
	drafts := []struct {
		name  string
		input string
	}{
		{"long token", strings.Repeat("x", 500)},
		{"cjk", strings.Repeat("漢字の下書き", 40)},
		{"control and zero width", "a\tb\x7f​SAME-​WIDTH​tail"},
		{"empty", ""},
		{"caret mid draft", ""},
	}
	for _, style := range composerStyles {
		for _, width := range []int{40, 80, 141} {
			for _, d := range drafts {
				m := overflowUI(t, style, width, 24)
				if d.name != "empty" && d.name != "caret mid draft" {
					m.input = []rune(d.input)
				}
				if m.input != nil && len(m.input) > 3 {
					m.edit.caret = 2
				}
				rows := m.composerLines()
				for i, row := range rows {
					if w := plainWidth(row); w > m.width {
						t.Fatalf("%s/%s width %d: composer row %d width %d > %d: %q", style, d.name, width, i, w, m.width, stripANSI(row))
					}
				}
			}
		}
	}
}

func TestComposerTailRowsAndCaretVisibleForLongDraft(t *testing.T) {
	long := strings.Repeat("word ", 120)
	for _, style := range composerStyles {
		m := overflowUI(t, style, 80, 24)
		m.input = []rune(long)
		m.caretTyped = true
		view := stripANSI(m.View())
		if !strings.Contains(view, "█") {
			t.Fatalf("%s: caret row missing from frame: %q", style, view)
		}
		// The editable tail is what is visible; the draft's final word must
		// be on screen (FR-17: the tail shows with the caret row).
		if !strings.Contains(view, "word") {
			t.Fatalf("%s: draft tail not visible in frame: %q", style, view)
		}
	}
}

func TestMentionPopupRowsNeverOverflow(t *testing.T) {
	longPath := "deeply/nested/dir/with/" + strings.Repeat("segment/", 10) + strings.Repeat("f", 200-13)
	cjk := strings.Repeat("漢字", 30)
	for _, width := range []int{40, 80} {
		m := overflowUI(t, "bordered", width, 24)
		m.mention.open = true
		m.mention.matches = []string{longPath, cjk}
		m.mention.cursor = 1
		rows := m.mentionLines()
		if len(rows) < 3 {
			t.Fatalf("width %d: popup rows = %d, want match rows + hint", width, len(rows))
		}
		for i, row := range rows {
			if w := plainWidth(row); w > width {
				t.Fatalf("width %d: popup row %d width %d > %d: %q", width, i, w, width, stripANSI(row))
			}
		}
	}
}

func TestCommandPopupRowsNeverOverflow(t *testing.T) {
	saved := commands
	commands = []commandItem{
		{Name: strings.Repeat("c", 60), Description: strings.Repeat("d", 200)},
		{Name: "normal", Description: "Normal command"},
	}
	defer func() { commands = saved }()
	for _, width := range []int{40, 80} {
		m := overflowUI(t, "bordered", width, 24)
		m.commandPopup.open = true
		m.commandPopup.matches = commands
		rows := m.commandLines()
		for i, row := range rows {
			if w := plainWidth(row); w > width {
				t.Fatalf("width %d: popup row %d width %d > %d: %q", width, i, w, width, stripANSI(row))
			}
		}
	}
}
