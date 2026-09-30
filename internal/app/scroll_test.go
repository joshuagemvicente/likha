package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/session"
)

func TestCaretBlinksSolidWhileTyping(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Idle: the blink phase defaults to visible.
	if !m.caretVisible() {
		t.Fatal("caret not visible while idle")
	}
	// A tick toggles it off; a keystroke forces it solid again.
	m.Update(caretTickMsg{})
	if m.caretVisible() {
		t.Fatal("caret tick did not toggle the blink phase off")
	}
	for _, r := range []rune("hi") {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !m.caretVisible() {
		t.Fatal("caret not solid right after typing")
	}
	view := m.View()
	if !strings.Contains(view, string(m.input)+"█") {
		t.Fatalf("block caret missing at the end of the draft: %q", view)
	}

	// While a run is active the composer has no caret.
	m.working = true
	if m.caretVisible() {
		t.Fatal("caret rendered while a run is active")
	}
	m.working = false

	// While an approval is pending the caret hides as well.
	request := &approvalRequest{Kind: "command", Title: "c", Body: "b", Reply: make(chan bool, 1)}
	m.Update(turnEvent{runID: 1, kind: "approval", approval: request})
	if m.pending != nil && m.caretVisible() {
		t.Fatal("caret rendered during a pending review")
	}
}

func TestScrollbarAppearsOnlyWhenContentOverflows(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m.scrollbarRows(m.bodyHeight()) != nil {
		t.Fatal("scrollbar rendered for content that fits")
	}
	for range 100 {
		m.entries = append(m.entries, entry{role: "Assistant", content: "filler line to overflow the viewport"})
	}
	m.View() // lay out the new content
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	bar := m.scrollbarRows(m.bodyHeight())
	if len(bar) != m.bodyHeight() {
		t.Fatalf("scrollbar rows = %d, want body height %d", len(bar), m.bodyHeight())
	}
	if !strings.Contains(strings.Join(bar, ""), "█") {
		t.Fatal("scrollbar has no thumb")
	}
	// The scrollbar occupies the body's last column in the view.
	view := m.View()
	rows := strings.Split(view, "\n")
	if !strings.HasSuffix(rows[len(m.header())], "█") && !strings.Contains(rows[len(m.header())], "│") {
		t.Fatalf("scrollbar column missing in the rendered body: %q", rows[len(m.header())])
	}
}

func TestScrollbarDragRepositions(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for range 100 {
		m.entries = append(m.entries, entry{role: "Assistant", content: "filler line to overflow"})
	}
	m.View() // lay out the new content
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	maxScroll := m.scrollMax()

	// A click mid-track repositions the viewport proportionally.
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 79, Y: m.bodyHeight() / 2})
	m.clampScroll()
	if m.following && maxScroll > 0 {
		t.Fatal("scrollbar click did not unhook following")
	}
	if m.scroll == 0 || m.scroll >= maxScroll {
		t.Fatalf("mid-track click moved scroll to %d of max %d", m.scroll, maxScroll)
	}

	// A wheel event on another column still scrolls; End returns to the
	// pinned newest view.
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !m.following {
		t.Fatal("End did not return to the bottom")
	}
}

func TestSmoothScrollLineGranular(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for range 60 {
		m.entries = append(m.entries, entry{role: "Assistant", content: "filler message for scrolling"})
	}
	m.View() // lay out
	m.jumpBottom()
	start := m.scroll
	m.scrollBy(-scrollWheelLines)
	if m.scroll != start-scrollWheelLines {
		t.Fatalf("wheel scroll moved %d lines, want %d", start-m.scroll, scrollWheelLines)
	}
	// Streaming growth does not drag the view while the user is anchored
	// above the bottom (browser behavior); returning to the bottom re-pins.
	for range 5 {
		m.entries = append(m.entries, entry{role: "Assistant", content: "streamed line"})
	}
	m.View()
	m.clampScroll()
	if m.following || m.scroll == m.scrollMax() {
		t.Fatal("streamed content dragged the anchored view")
	}
	m.jumpBottom()
	if !m.following || m.scroll != m.scrollMax() {
		t.Fatalf("pinned view drifted: scroll=%d max=%d", m.scroll, m.scrollMax())
	}
}
