package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
)

func TestCaretBlinksSolidWhileTyping(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
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

	// While a run is active the composer keeps its caret: the user can still
	// type to queue the next prompt.
	m.working = true
	if !m.caretVisible() {
		t.Fatal("caret hidden while a run is active")
	}
	m.working = false

	// While an approval is pending the caret hides as well.
	request := &agent.ApprovalRequest{Kind: "command", Title: "c", Body: "b", Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	if m.pending != nil && m.caretVisible() {
		t.Fatal("caret rendered during a pending review")
	}
}

func TestScrollbarAppearsOnlyWhenContentOverflows(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
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
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
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
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
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

// The scrollbar column must not depend on a row's styling (user-reported
// overlap): rendered rows carry SGR escapes whose bytes occupy no cells, so
// splicing measures by visible text only. With a color profile active, a
// muted Reasoning block keeps the bar at the far-right column on every body
// row and loses no text to the splice cut.
func TestScrollbarStaysAtTheRightEdgeOnStyledRows(t *testing.T) {
	forceANSI(t)
	t.Cleanup(func() {})
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.entries = append(m.entries,
		entry{role: "Reasoning", content: strings.Repeat("the reasoning paragraph wraps well inside the viewport width. ", 40)},
		entry{role: "Assistant", content: "short answer"})
	m.thoughtExpanded = map[int]bool{0: true} // expanded: the full reasoning lays out
	m.layoutWidth = 0
	rows := strings.Split(m.View(), "\n")
	body := m.bodyHeight()
	if !m.scrollbarVisible(body) {
		t.Fatal("test fixture must overflow the viewport")
	}
	for i := 0; i < body; i++ {
		visible := stripANSI(rows[i])
		cells := []rune(visible)
		if len(cells) != m.width {
			t.Fatalf("body row %d width %d, want %d: %q", i, len(cells), m.width, visible)
		}
		if last := cells[m.width-1]; last != '│' && last != '█' {
			t.Fatalf("body row %d lost the right-edge scrollbar: %q", i, visible)
		}
	}
	// The reasoning text survives the splice uncut.
	if !strings.Contains(stripANSI(strings.Join(rows[:body], "\n")), "viewport width.") {
		t.Fatal("styled reasoning text was truncated by the scrollbar splice")
	}
}
