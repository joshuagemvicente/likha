package tui

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

func selectionTestUI(t *testing.T, width, height int, lines ...string) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true, ASCII: true}, "", nil, session.Snapshot{})
	m.width, m.height = width, height
	m.entries = nil
	m.lines = append([]string(nil), lines...)
	m.lineStyles = make([]lipgloss.Style, len(lines))
	m.lineSpans = make([][]likhaui.Swatch, len(lines))
	m.lineRuns = make([][]lineRun, len(lines))
	m.lineActivity = make([]bool, len(lines))
	m.layoutWidth, m.scroll, m.following = width, 0, false
	return m
}

func selectionMouse(action tea.MouseAction, x, y int) tea.MouseMsg {
	return tea.MouseMsg{Action: action, Button: tea.MouseButtonLeft, X: x, Y: y}
}

func selectionDrag(t *testing.T, m *ui, x1, y1, x2, y2 int) {
	t.Helper()
	for _, event := range []tea.MouseMsg{
		selectionMouse(tea.MouseActionPress, x1, y1),
		selectionMouse(tea.MouseActionMotion, x2, y2),
		selectionMouse(tea.MouseActionRelease, x2, y2),
	} {
		if !m.handleTextSelectionMouse(event) {
			t.Fatalf("selection did not consume %v", event)
		}
	}
}

func TestTextSelectionMouseModernAndLegacy(t *testing.T) {
	tests := []struct {
		name   string
		event  tea.MouseMsg
		action tea.MouseAction
		button tea.MouseButton
	}{
		{"modern press", tea.MouseMsg{Button: tea.MouseButtonLeft}, tea.MouseActionPress, tea.MouseButtonLeft},
		{"modern release wins", tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, Type: tea.MouseLeft}, tea.MouseActionRelease, tea.MouseButtonLeft},
		{"modern motion wins", tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft, Type: tea.MouseLeft}, tea.MouseActionMotion, tea.MouseButtonLeft},
		{"modern wheel wins", tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseLeft}, tea.MouseActionPress, tea.MouseButtonWheelUp},
		{"modern right wins", tea.MouseMsg{Button: tea.MouseButtonRight, Type: tea.MouseLeft}, tea.MouseActionPress, tea.MouseButtonRight},
		{"legacy press", tea.MouseMsg{Type: tea.MouseLeft}, tea.MouseActionPress, tea.MouseButtonLeft},
		{"legacy release", tea.MouseMsg{Type: tea.MouseRelease}, tea.MouseActionRelease, tea.MouseButtonNone},
		{"legacy motion", tea.MouseMsg{Type: tea.MouseMotion}, tea.MouseActionMotion, tea.MouseButtonNone},
		{"legacy wheel", tea.MouseMsg{Type: tea.MouseWheelDown}, tea.MouseActionPress, tea.MouseButtonWheelDown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action, button := selectionMouseActionButton(test.event)
			if action != test.action || button != test.button {
				t.Fatalf("got action/button %v/%v, want %v/%v", action, button, test.action, test.button)
			}
		})
	}
}

func TestTranscriptSelectionForwardBackwardMultiline(t *testing.T) {
	for _, backward := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("backwards=%t/legacy=%t", backward, legacy), func(t *testing.T) {
				m := selectionTestUI(t, 80, 24, "alpha", "  beta  ", "gamma")
				top := len(m.header())
				x1, y1, x2, y2 := 1, top, 3, top+2
				if backward {
					x1, y1, x2, y2 = x2, y2, x1, y1
				}
				events := []tea.MouseMsg{selectionMouse(tea.MouseActionPress, x1, y1), selectionMouse(tea.MouseActionMotion, x2, y2), selectionMouse(tea.MouseActionRelease, x2, y2)}
				if legacy {
					events = []tea.MouseMsg{{Type: tea.MouseLeft, X: x1, Y: y1}, {Type: tea.MouseMotion, X: x2, Y: y2}, {Type: tea.MouseRelease, X: x2, Y: y2}}
				}
				for _, event := range events {
					if !m.handleTextSelectionMouse(event) {
						t.Fatalf("did not consume %v", event)
					}
				}
				if got := m.selectedText(); got != "lpha\n  beta  \ngam" {
					t.Fatalf("selected text = %q", got)
				}
				if m.selection.dragging || !m.textSelectionActive() || m.selection.snapshot == nil {
					t.Fatalf("release lost the selection or retained the drag: %+v", m.selection)
				}
				if m.handleTextSelectionMouse(tea.MouseMsg{Action: tea.MouseActionMotion, X: 20, Y: top}) || m.selectedText() != "lpha\n  beta  \ngam" {
					t.Fatal("hover changed a released selection")
				}
			})
		}
	}
}

func TestTranscriptSelectionRuneBoundariesAndSanitizedControls(t *testing.T) {
	for cell, want := range map[int]int{-1: 0, 0: 0, 1: 1, 2: 2, 3: 2, 4: 3, 5: 3, 6: 4, 100: 4} {
		if got := selectionRuneAtCell("a界🙂z", cell); got != want {
			t.Errorf("cell %d mapped to rune %d, want %d", cell, got, want)
		}
	}
	for cell, want := range map[int]int{0: 0, 1: 1, 2: 3, 3: 3, 4: 4} {
		if got := selectionRuneAtCell("a👩🏽z", cell); got != want {
			t.Errorf("emoji cluster cell %d mapped to rune %d, want %d", cell, got, want)
		}
	}
	m := selectionTestUI(t, 80, 24, "a界🙂z")
	selectionDrag(t, m, 1, 0, 5, 0)
	if got := m.selectedText(); got != "界🙂" {
		t.Fatalf("wide selection = %q", got)
	}
	m = selectionTestUI(t, 80, 24, "a界🙂z")
	selectionDrag(t, m, 4, 0, 2, 0)
	if got := m.selectedText(); got != "🙂" || !utf8.ValidString(got) {
		t.Fatalf("interior-cell backwards selection = %q", got)
	}
	for _, raw := range []string{"a\t\u202eb\x1b[31m", "a\x00b\r\u200dc"} {
		m = selectionTestUI(t, 80, 24, raw)
		selectionDrag(t, m, 0, 0, 79, 0)
		if got, want := m.selectedText(), fitText(raw, 80); got != want || strings.ContainsAny(got, "\x1b\x00\r\t\u202e\u200d") {
			t.Fatalf("sanitized copy = %q, want %q", got, want)
		}
		if got := ansi.Strip(strings.Split(m.View(), "\n")[0]); !strings.HasPrefix(got, m.selectedText()) {
			t.Fatalf("copied text differs from visible row: %q vs %q", got, m.selectedText())
		}
	}
}

func TestTranscriptSelectionBlankAndClickReleaseDoNotFreeze(t *testing.T) {
	for _, point := range [][2]int{{30, 0}, {0, 1}, {0, 10}, {0, 23}} {
		m := selectionTestUI(t, 80, 24, "alpha", "")
		selectionDrag(t, m, 0, 0, 3, 0)
		m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, point[0], point[1]))
		if m.selection.snapshot != nil || m.selection.dragging || m.textSelectionActive() {
			t.Fatalf("blank point %v retained selection: %+v", point, m.selection)
		}
	}
	m := selectionTestUI(t, 80, 24, "alpha")
	selectionDrag(t, m, 2, 0, 2, 0)
	if m.selection.snapshot != nil || m.selection.dragging || m.textSelectionActive() {
		t.Fatal("zero-length click froze the transcript")
	}
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 2, 0))
	m.handleTextSelectionMouse(tea.MouseMsg{Action: tea.MouseActionMotion, X: 4, Y: 0})
	if m.selection.snapshot != nil || m.selection.dragging {
		t.Fatal("lost release followed by hover froze the transcript")
	}
}

func TestTextSelectionReleaseRetainsLastDraggedEndpoint(t *testing.T) {
	m := selectionTestUI(t, 80, 24, "alpha")
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 0, 0))
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, 3, 0))
	// Legacy/X10 release messages can omit button information. A release
	// terminates capture; it is not a new drag toward default coordinates.
	if !m.handleTextSelectionMouse(tea.MouseMsg{Type: tea.MouseRelease}) || m.selectedText() != "alp" || m.selection.dragging {
		t.Fatal("release discarded the last dragged range")
	}
}

func TestTranscriptSelectionSnapshotDeepCopiesAndFreezesRendering(t *testing.T) {
	forceANSI(t)
	m := selectionTestUI(t, 80, 24, "#123456 abc", "  if (x)  ", "◐ Working")
	m.lineSpans[0] = likhaui.FindSwatches(m.lines[0])
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	m.lineRuns[1] = []lineRun{{start: 2, end: 4, style: &style}}
	m.lineStyles[1] = m.theme.Muted
	m.lineActivity[2] = true
	selectionDrag(t, m, 0, 0, 0, 1)
	snapshot := m.selection.snapshot
	before := strings.Split(m.View(), "\n")[:snapshot.body]
	text := m.selectedText()
	m.lines[0] = "REUSED LINE BUFFER"
	m.lineSpans[0][0].Hex = "#ffffff"
	m.lineRuns[1][0].start = 0
	style = style.Foreground(lipgloss.Color("1"))
	m.lineStyles[1] = m.theme.Error
	m.lineActivity[2] = false
	m.activityFrame += 11
	m.entries = []entry{{role: "Assistant", content: "fresh streaming content"}}
	m.layoutWidth = 0
	after := strings.Split(m.View(), "\n")[:snapshot.body]
	if !reflect.DeepEqual(before, after) || m.selectedText() != text {
		t.Fatalf("frozen view/copy mutated:\nbefore %q\nafter %q", before, after)
	}
	if snapshot.lines[0] != "#123456 abc" || snapshot.lineSpans[0][0].Hex != "#123456" || snapshot.lineRuns[1][0].start != 2 || snapshot.lineRuns[1][0].style.GetForeground() != lipgloss.Color("2") || !snapshot.lineActivity[2] {
		t.Fatal("snapshot aliases live lines, spans, activity, runs, or shared style")
	}
	m.clearTranscriptSelection()
	if m.selection.snapshot != nil || m.layoutWidth != 0 || !strings.Contains(ansi.Strip(m.View()), "fresh streaming content") {
		t.Fatal("clearing did not resume fresh layout")
	}
}

func TestTranscriptSelectionAgentStreamingContinuesBehindSnapshot(t *testing.T) {
	m := selectionTestUI(t, 80, 24)
	m.entries = []entry{{role: "Assistant", content: "original text"}}
	m.streaming, m.reasoningStream, m.activity = 0, -1, -1
	m.working, m.runID = true, 1
	m.streamBuf.WriteString("original text")
	m.layoutWidth = 0
	m.View()
	selectionDrag(t, m, 2, 0, 10, 0)
	selected := m.selectedText()
	frozen := strings.Split(ansi.Strip(m.View()), "\n")[:m.bodyHeight()]
	m.Update(agent.TurnEvent{RunID: 1, Kind: "text", Text: " and NEW STREAMED CONTENT"})
	if !strings.Contains(m.entries[0].content, "NEW STREAMED CONTENT") {
		t.Fatal("selection blocked agent ingestion")
	}
	if got := strings.Split(ansi.Strip(m.View()), "\n")[:m.bodyHeight()]; !reflect.DeepEqual(frozen, got) || m.selectedText() != selected {
		t.Fatalf("streaming changed selection/view: %q -> %q", frozen, got)
	}
	m.clearTextSelection()
	if !strings.Contains(ansi.Strip(m.View()), "NEW STREAMED CONTENT") {
		t.Fatal("clear did not expose the accumulated stream")
	}
}

func TestTranscriptSelectionScrollbarOwnershipAndHeaderCoordinates(t *testing.T) {
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %02d", i)
	}
	m := selectionTestUI(t, 40, 12, lines...)
	top, body := len(m.header()), m.bodyHeight()
	if top != 1 || !m.scrollbarVisible(body) {
		t.Fatal("fixture needs a header and scrollbar")
	}
	for _, y := range []int{0, top + body, m.height - 1} {
		if m.updateScrollbarMouse(selectionMouse(tea.MouseActionPress, m.width-1, y)) {
			t.Fatalf("scrollbar stole non-body row %d", y)
		}
	}
	if m.updateScrollbarMouse(selectionMouse(tea.MouseActionMotion, m.width-1, top+2)) {
		t.Fatal("unowned motion started a scrollbar drag")
	}
	m.jumpBottom()
	press := selectionMouse(tea.MouseActionPress, m.width-1, top)
	if m.handleTextSelectionMouse(press) || !m.updateScrollbarMouse(press) || m.scroll != 0 {
		t.Fatal("header-adjusted top of track did not scroll to zero")
	}
	motion := selectionMouse(tea.MouseActionMotion, 3, top+body-1)
	if m.handleTextSelectionMouse(motion) || !m.updateScrollbarMouse(motion) || m.scroll != m.scrollMax() {
		t.Fatal("scrollbar lost its drag outside the scrollbar column")
	}
	if !m.updateScrollbarMouse(selectionMouse(tea.MouseActionRelease, 3, top)) || m.selection.scrollbarDragging {
		t.Fatal("scrollbar release did not end capture")
	}
	m.jumpTop()
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 0, top))
	motion = selectionMouse(tea.MouseActionMotion, m.width-1, top+2)
	if m.updateScrollbarMouse(motion) || !m.handleTextSelectionMouse(motion) || m.scroll != 0 {
		t.Fatal("scrollbar stole a text drag")
	}
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionRelease, m.width-1, top+2))
	if !m.textSelectionActive() || m.selection.scrollbarDragging {
		t.Fatal("text release did not retain selection")
	}
	if m.handleTextSelectionMouse(press) || !m.updateScrollbarMouse(press) || !m.textSelectionActive() {
		t.Fatal("scrollbar press cleared a retained text selection")
	}
	m.updateScrollbarMouse(selectionMouse(tea.MouseActionRelease, m.width-1, top))
}

func TestTranscriptSelectionScrollUsesFrozenGeometry(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %03d", i)
	}
	m := selectionTestUI(t, 80, 24, lines...)
	selectionDrag(t, m, 0, 0, 3, 0)
	text, body, maximum := m.selectedText(), m.bodyHeight(), m.scrollMax()
	m.lines = []string{"live array replaced"}
	m.input = []rune(strings.Repeat("draft\n", 30))
	m.edit.endCaret(m.input)
	if m.bodyHeight() != body || m.scrollMax() != maximum || !m.scrollbarVisible(body) {
		t.Fatal("live transcript/composer changed frozen scroll geometry")
	}
	assertViewport(t, m.View(), m.width, m.height)
	for _, wheel := range []tea.MouseMsg{{Button: tea.MouseButtonWheelDown}, {Type: tea.MouseWheelUp}} {
		if m.handleTextSelectionMouse(wheel) || m.updateScrollbarMouse(wheel) {
			t.Fatal("drag handlers stole a wheel event")
		}
	}
	m.scrollBy(scrollWheelLines)
	if m.scroll != scrollWheelLines {
		t.Fatalf("wheel offset %d", m.scroll)
	}
	m.pageDown()
	if m.scroll != scrollWheelLines+body {
		t.Fatalf("page down offset %d", m.scroll)
	}
	m.pageUp()
	if m.scroll != scrollWheelLines || m.selectedText() != text {
		t.Fatal("paging moved selection endpoints")
	}
	m.jumpBottom()
	if m.scroll != maximum || m.scrollPosition() != "bottom" {
		t.Fatal("jump did not use snapshot maximum")
	}
	m.clearTextSelection()
	m.View()
	if m.scrollMax() == maximum || m.bodyHeight() == body {
		t.Fatal("clear retained frozen layout geometry")
	}
}

func TestReviewScrollbarCaptureSurvivesSelectionExclusion(t *testing.T) {
	m := selectionTestUI(t, 80, 24, strings.Split(strings.Repeat("review\n", 80), "\n")...)
	m.pending = &agent.ApprovalRequest{Title: "Review", Body: "body"}
	press := selectionMouse(tea.MouseActionPress, m.width-1, 0)
	if m.handleTextSelectionMouse(press) || !m.updateScrollbarMouse(press) {
		t.Fatal("review must exclude text selection but permit its scrollbar")
	}
	m.View()
	if !m.selection.scrollbarDragging {
		t.Fatal("rendering an excluded review cancelled scrollbar capture")
	}
	motion := selectionMouse(tea.MouseActionMotion, 2, m.bodyHeight()-1)
	if m.handleTextSelectionMouse(motion) || !m.updateScrollbarMouse(motion) || m.scroll != m.scrollMax() {
		t.Fatal("review scrollbar lost capture outside its column")
	}
	m.updateScrollbarMouse(tea.MouseMsg{Type: tea.MouseRelease})
}

func TestTextSelectionCannotCrossComposerAndTranscript(t *testing.T) {
	for _, style := range composerStyles {
		t.Run(style, func(t *testing.T) {
			m := selectionTestUI(t, 80, 24, "transcript text")
			m.composerStyle = style
			m.input = []rune("alpha\nbeta")
			m.edit.endCaret(m.input)
			left, top, width, rows := m.composerTextBounds()
			if rows < 2 {
				t.Fatal("fixture must contain two composer rows")
			}
			selectionDrag(t, m, left+1, top, left+3, top+1)
			if got := m.selectedText(); got != "lpha\nbet" {
				t.Fatalf("composer multiline selection = %q", got)
			}
			if m.selection.snapshot != nil || m.selection.surface != selectionComposer {
				t.Fatal("composer selection froze transcript")
			}
			m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, left+3, top+1))
			m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, 0, 0))
			m.handleTextSelectionMouse(selectionMouse(tea.MouseActionRelease, 0, 0))
			if strings.Contains(m.selectedText(), "transcript") || m.selection.snapshot != nil {
				t.Fatal("composer drag crossed into transcript")
			}
			selectionDrag(t, m, 0, len(m.header()), left+width, top+1)
			if got := m.selectedText(); got != "transcript text" {
				t.Fatalf("transcript drag into composer = %q", got)
			}
			if _, _, ok := m.edit.selectionRange(m.input); ok {
				t.Fatal("transcript drag retained composer selection")
			}
			if string(m.input) != "alpha\nbeta" || len(m.history) != 0 || len(m.queue) != 0 {
				t.Fatal("mouse drag edited or submitted the prompt")
			}
		})
	}
}

func TestComposerMouseSelectionHiddenRowsUnicodeAndEscapes(t *testing.T) {
	m := selectionTestUI(t, 80, 12, "transcript")
	m.composerStyle = "bordered"
	m.input = []rune(strings.Repeat("hidden row\n", 30) + "a界\tz\nlast")
	m.edit.endCaret(m.input)
	m.caretOn = false
	left, top, _, rows := m.composerTextBounds()
	startY := top + rows - 2
	anchor, ok := m.composerCaretAt(left+1, startY)
	if !ok || anchor < len([]rune(strings.Repeat("hidden row\n", 30))) {
		t.Fatal("hit-test did not account for hidden composer rows")
	}
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, left+1, startY))
	caret, ok := m.composerCaretAt(left+10, startY)
	if !ok {
		t.Fatal("missing composer endpoint")
	}
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, left+10, startY))
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionRelease, left+10, startY))
	if got := m.selectedText(); got != string(m.input[anchor:caret]) || !utf8.ValidString(got) || !strings.Contains(got, "界") || !strings.Contains(got, "\t") {
		t.Fatalf("unicode/escaped source selection = %q", got)
	}
	// Composer copying uses source runes; its visible escape is highlighted
	// atomically by the peer renderer, not copied as a sliced escape string.
	if m.selection.snapshot != nil {
		t.Fatal("composer selection froze the output")
	}
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, left-1, startY))
	if m.textSelectionActive() || m.selection.dragging {
		t.Fatal("composer border/inset started a text drag")
	}
}

func TestComposerDragFreezesPrePressMarkerGeometry(t *testing.T) {
	for _, style := range composerStyles {
		t.Run(style, func(t *testing.T) {
			m := selectionTestUI(t, 80, 24, "transcript")
			m.composerStyle, m.input = style, []rune("abcd")
			m.edit.caret, m.caretOn, m.caretTyped = 1, true, false
			left, top, _, _ := m.composerTextBounds()
			assertViewport(t, m.View(), m.width, m.height)
			if !strings.Contains(ansi.Strip(m.View()), "a█bcd") {
				t.Fatal("fixture must render the original insertion marker")
			}
			m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, left+3, top))
			if m.edit.selectionAnchor != 2 {
				t.Fatalf("anchor %d, want 2 before c", m.edit.selectionAnchor)
			}
			viewport, frozen := m.frozenComposerViewport()
			if !frozen || len(viewport.rows) != 1 {
				t.Fatal("press did not capture a frozen viewport")
			}
			var visible strings.Builder
			for _, glyph := range viewport.rows[0].glyphs {
				visible.WriteString(glyph.text)
			}
			if visible.String() != "a bcd" {
				t.Fatalf("marker moved source glyphs: %q", visible.String())
			}
			m.caretOn, m.caretTyped = false, false
			m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, left+4, top))
			assertViewport(t, m.View(), m.width, m.height)
			if got := m.selectedText(); got != "c" {
				t.Fatalf("one-cell drag selected %q, want c", got)
			}
			m.handleTextSelectionMouse(selectionMouse(tea.MouseActionRelease, left+4, top))
			if _, frozen := m.frozenComposerViewport(); frozen || m.selection.composerGesture != nil || m.selectedText() != "c" {
				t.Fatal("release retained gesture geometry or discarded its range")
			}
			assertViewport(t, m.View(), m.width, m.height)
		})
	}
}

func TestComposerDragExactWidthMarkerAndPopupGeometry(t *testing.T) {
	for _, width := range []int{40, 80} {
		for _, style := range composerStyles {
			t.Run(fmt.Sprintf("width=%d/%s", width, style), func(t *testing.T) {
				m := selectionTestUI(t, width, 24, "transcript")
				m.composerStyle = style
				_, _, inputWidth := m.composerLayout()
				m.input = []rune(strings.Repeat("x", inputWidth))
				m.edit.caret, m.caretOn, m.caretTyped = 1, true, false
				m.mention.open = true
				left, top, _, _ := m.composerTextBounds()
				assertViewport(t, m.View(), m.width, m.height)
				m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, left+3, top))
				m.caretOn, m.caretTyped = false, false
				m.mention.matches = []string{"x1.go", "x2.go", "x3.go", "x4.go", "x5.go"}
				m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, left+4, top))
				assertViewport(t, m.View(), m.width, m.height)
				if m.selectedText() != "x" {
					t.Fatalf("exact-width source selection = %q", m.selectedText())
				}
				m.endTextSelectionDrag()
				assertViewport(t, m.View(), m.width, m.height)
				if m.selection.composerGesture != nil || m.selectedText() != "x" {
					t.Fatal("ending the gesture lost its range or retained geometry")
				}
			})
		}
	}
}

func TestTranscriptSelectionFreezesExactWidthComposerAndCaretBlink(t *testing.T) {
	for _, width := range []int{40, 80} {
		for _, style := range composerStyles {
			for _, markerOn := range []bool{false, true} {
				t.Run(fmt.Sprintf("width=%d/%s/marker=%t", width, style, markerOn), func(t *testing.T) {
					m := selectionTestUI(t, width, 12, "transcript text")
					m.composerStyle = style
					_, _, inputWidth := m.composerLayout()
					m.input = []rune(strings.Repeat("x", inputWidth))
					m.edit.caret, m.caretOn, m.caretTyped = 1, markerOn, false
					assertViewport(t, m.View(), m.width, m.height)
					selectionDrag(t, m, 0, len(m.header()), 3, len(m.header()))
					before := m.View()
					assertViewport(t, before, m.width, m.height)
					bounds := m.selection.snapshot.composerView
					m.Update(caretTickMsg{})
					m.caretTyped = false
					after := m.View()
					assertViewport(t, after, m.width, m.height)
					if before != after {
						t.Fatal("caret blink changed frozen output frame")
					}
					if viewport, frozen := m.frozenComposerViewport(); !frozen || !reflect.DeepEqual(viewport, bounds) {
						t.Fatal("output snapshot lost the corresponding composer viewport")
					}
					m.clearTextSelection()
					assertViewport(t, m.View(), m.width, m.height)
					if _, frozen := m.frozenComposerViewport(); frozen {
						t.Fatal("clear retained frozen composer geometry")
					}
				})
			}
		}
	}
}

func TestTranscriptSelectionFreezesAsynchronousPopupRows(t *testing.T) {
	for _, popup := range []string{"mention", "command"} {
		t.Run(popup, func(t *testing.T) {
			m := selectionTestUI(t, 80, 24, "transcript text")
			m.input = []rune("@x")
			m.edit.endCaret(m.input)
			if popup == "mention" {
				m.mention.open = true
			} else {
				m.input = []rune("/")
				m.edit.endCaret(m.input)
				m.commandPopup.open = true
				m.commandPopup.matches = commandMatches("")
			}
			assertViewport(t, m.View(), m.width, m.height)
			selectionDrag(t, m, 0, 0, 3, 0)
			before := m.View()
			assertViewport(t, before, m.width, m.height)
			if popup == "mention" {
				m.Update(fileIndexMsg{files: []string{"x1.go", "x2.go", "x3.go", "x4.go", "x5.go", "x6.go"}})
				if len(m.mentionLines()) <= len(m.selection.snapshot.mention) {
					t.Fatal("fixture did not asynchronously grow the mention popup")
				}
			} else {
				m.commandPopup.matches = nil
				m.commandPopup.open = false
			}
			assertViewport(t, m.View(), m.width, m.height)
			if got := m.View(); got != before {
				t.Fatal("asynchronous popup change altered the frozen main frame")
			}
			m.mention.matches, m.mention.open = nil, false
			assertViewport(t, m.View(), m.width, m.height)
			if m.View() != before {
				t.Fatal("popup shrink underfilled the frozen frame")
			}
			m.clearTextSelection()
			assertViewport(t, m.View(), m.width, m.height)
			if m.View() == before {
				t.Fatal("clear failed to resume current popup geometry")
			}
		})
	}
}

func TestComposerPressUsesFrozenOutputGeometryBeforeClearing(t *testing.T) {
	m := selectionTestUI(t, 80, 24, "transcript text")
	m.composerStyle, m.input = "borderless", []rune("abcd")
	m.edit.caret, m.caretOn, m.caretTyped = 1, true, false
	m.mention.open = true
	selectionDrag(t, m, 0, 0, 3, 0)
	oldSnapshot := m.selection.snapshot
	m.mention.matches = []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	m.caretOn = false
	left, top, _, _ := m.composerTextBounds()
	assertViewport(t, m.View(), m.width, m.height)
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, left+3, top))
	if m.selection.snapshot != nil || m.edit.selectionAnchor != 2 || m.selection.composerGesture == nil {
		t.Fatal("composer press remapped the visible output snapshot after clearing it")
	}
	if !reflect.DeepEqual(m.selection.composerGesture.mention, oldSnapshot.mention) {
		t.Fatal("composer press discarded pre-press popup geometry")
	}
	// Copies must survive disposal/mutation of the previous output snapshot.
	oldSnapshot.composerView.rows[0].glyphs[3].source = 99
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, left+4, top))
	assertViewport(t, m.View(), m.width, m.height)
	if got := m.selectedText(); got != "c" {
		t.Fatalf("frozen-output-to-composer drag selected %q, want c", got)
	}
	m.endTextSelectionDrag()
	if m.selection.dragging || m.selection.composerGesture != nil || m.selectedText() != "c" {
		t.Fatal("keyboard handoff discarded the source range or retained gesture")
	}
	assertViewport(t, m.View(), m.width, m.height)
}

func TestEndTextSelectionDragRetainsOutputRange(t *testing.T) {
	m := selectionTestUI(t, 80, 24, "transcript text")
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 0, 0))
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, 3, 0))
	snapshot := m.selection.snapshot
	m.endTextSelectionDrag()
	if m.selection.dragging || m.selection.snapshot != snapshot || m.selectedText() != "tra" {
		t.Fatal("keyboard handoff discarded nonempty output selection")
	}
	m.clearTextSelection()
	m.View()
	m = selectionTestUI(t, 80, 24, "transcript text")
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 0, 0))
	m.endTextSelectionDrag()
	if m.selection.snapshot != nil || m.selection.dragging {
		t.Fatal("empty gesture handoff froze the frame indefinitely")
	}
}

func TestTextSelectionUnavailableOnOtherSurfacesAndSmallViewport(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ui)
	}{
		{"width", func(m *ui) { m.width = minWidth - 1 }},
		{"height", func(m *ui) { m.height = minHeight - 1 }},
		{"setup", func(m *ui) { m.mode = modeSetup }},
		{"key", func(m *ui) { m.keyModal.open = true }},
		{"dialog", func(m *ui) { m.dialog.open = true }},
		{"ask", func(m *ui) { m.ask.pending = true }},
		{"consent", func(m *ui) { m.consent.pending = true }},
		{"approval", func(m *ui) { m.pending = &agent.ApprovalRequest{Title: "Approve", Body: "secret"} }},
		{"tool", func(m *ui) { m.toolInspector.open, m.toolInspector.sessionID = true, m.snapshot.ID }},
		{"agent", func(m *ui) { m.agentInspector.open, m.agentInspector.sessionID = true, m.snapshot.ID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := selectionTestUI(t, 80, 24, "alpha")
			selectionDrag(t, m, 0, 0, 3, 0)
			test.change(m)
			if m.selectionSurfaceVisible() || m.textSelectionActive() || m.selectedText() != "" {
				t.Fatal("hidden selection exposed through another surface")
			}
			if m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 0, 0)) || m.selection.snapshot != nil || m.edit.selecting {
				t.Fatal("other surface accepted a drag or retained selection")
			}
		})
	}
	for _, change := range []func(*ui){func(m *ui) { m.width = minWidth - 1 }, func(m *ui) { m.mode = modeSetup }} {
		m := selectionTestUI(t, 80, 24, "alpha")
		selectionDrag(t, m, 0, 0, 3, 0)
		change(m)
		m.View()
		if m.selection.snapshot != nil {
			t.Fatal("View retained a selection behind a new screen")
		}
	}
	// A valid resize (even if the coordinator has not reset yet) must not
	// apply old row/cell coordinates to the new document.
	m := selectionTestUI(t, 80, 24, "alpha")
	selectionDrag(t, m, 0, 0, 3, 0)
	m.width = 81
	m.View()
	if m.selection.snapshot != nil || m.textSelectionActive() {
		t.Fatal("resize retained stale transcript coordinates")
	}
}

func TestTranscriptSelectionHighlightPreservesCellsAndStopsAtChrome(t *testing.T) {
	forceANSI(t)
	for _, name := range likhaui.ThemeNames() {
		t.Run(name, func(t *testing.T) {
			m := selectionTestUI(t, 80, 24, "a界🙂 #123456 tail", "next row")
			m.theme = likhaui.Resolve(name, true)
			m.lineStyles[0] = withBase(m.theme.Muted, m.theme.BgCode)
			m.lineSpans[0] = likhaui.FindSwatches(m.lines[0])
			before := strings.Split(m.View(), "\n")
			selectionDrag(t, m, 1, 0, 12, 0)
			after := strings.Split(m.View(), "\n")
			if len(before) != len(after) || before[0] == after[0] {
				t.Fatal("selection did not highlight the row")
			}
			for row := range before {
				if runewidth.StringWidth(ansi.Strip(after[row])) != m.width {
					t.Fatalf("highlight changed width on row %d", row)
				}
				if row >= m.height-m.statusLineHeight() {
					continue // the coordinator deliberately changes selection hints
				}
				if ansi.Strip(before[row]) != ansi.Strip(after[row]) {
					t.Fatalf("highlight changed visible cells on row %d: %q -> %q", row, before[row], after[row])
				}
				if row > 0 && before[row] != after[row] {
					t.Fatalf("highlight leaked to unselected row/chrome %d", row)
				}
			}
			if strings.ContainsAny(m.selectedText(), "\x1b\r\t") || strings.HasSuffix(m.selectedText(), "tail") {
				t.Fatalf("copied text contains styles or unrelated tail: %q", m.selectedText())
			}
		})
	}
	if !textSelectionStyle(likhaui.Resolve("default", true)).GetReverse() || !textSelectionStyle(likhaui.Theme{}).GetReverse() {
		t.Fatal("default/unset themes need reverse video fallback")
	}
	// No-color output should stay plain, not acquire raw selection escapes.
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := highlightSelectionCells("a界z", 1, 3, textSelectionStyle(likhaui.Theme{})); got != "a界z" {
		t.Fatalf("no-color highlight = %q", got)
	}
}

func TestTextSelectionStylesAcrossColorProfiles(t *testing.T) {
	previous := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.ANSI256, termenv.TrueColor} {
		lipgloss.SetColorProfile(profile)
		for _, name := range likhaui.ThemeNames() {
			style := textSelectionStyle(likhaui.Resolve(name, true))
			if profile == termenv.ANSI || name == "default" {
				if !style.GetReverse() {
					t.Fatalf("%s/%s lacks reverse-video fallback", name, profile.Name())
				}
			} else if style.GetForeground() == style.GetBackground() {
				t.Fatalf("%s/%s foreground equals background", name, profile.Name())
			}
		}
	}
}

func TestTranscriptSelectionReverseVideoDoesNotLeak(t *testing.T) {
	forceANSI(t)
	row := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Faint(true).Render("a界z    ")
	highlighted := highlightSelectionCells(row, 1, 3, textSelectionStyle(likhaui.Theme{})) + "Q"
	reverse := false
	var cells []bool
	for i := 0; i < len(highlighted); {
		if strings.HasPrefix(highlighted[i:], "\x1b[") {
			end := strings.IndexByte(highlighted[i+2:], 'm')
			if end < 0 {
				t.Fatal("dangling SGR escape")
			}
			for _, parameter := range strings.Split(highlighted[i+2:i+2+end], ";") {
				value, _ := strconv.Atoi(parameter)
				switch value {
				case 0, 27:
					reverse = false
				case 7:
					reverse = true
				}
			}
			i += 3 + end
			continue
		}
		r, size := utf8.DecodeRuneInString(highlighted[i:])
		for range runewidth.RuneWidth(r) {
			cells = append(cells, reverse)
		}
		i += size
	}
	for cell, active := range cells {
		if want := cell == 1 || cell == 2; active != want {
			t.Fatalf("cell %d reverse=%t, want %t (wide glyph/padding leak)", cell, active, want)
		}
	}
}

func TestTranscriptSelectionWrappedCodeWhitespaceAndScrollbarExcluded(t *testing.T) {
	forceANSI(t)
	m := selectionTestUI(t, 80, 12)
	m.entries = []entry{{role: "Assistant", content: "```text\n    preserve  internal  and trailing  \n" + strings.Repeat("wide words ", 100) + "\n```"}}
	m.layoutWidth = 0
	m.View()
	m.jumpTop()
	if !m.scrollbarVisible(m.bodyHeight()) {
		t.Fatal("code fixture needs a scrollbar")
	}
	// Select across every snapshot row by scrolling while the drag owns text.
	first := 0
	for first < len(m.lines) && strings.TrimSpace(m.lines[first]) == "" {
		first++
	}
	m.scroll = first
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 0, len(m.header())))
	m.jumpBottom()
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionMotion, m.width-1, len(m.header())+m.bodyHeight()-1))
	m.handleTextSelectionMouse(selectionMouse(tea.MouseActionRelease, m.width-1, len(m.header())+m.bodyHeight()-1))
	got := m.selectedText()
	if !strings.Contains(got, "    preserve  internal") || !strings.Contains(got, "trailing  \n") || !strings.Contains(got, "\n") || strings.ContainsAny(got, "\x1b│█") {
		t.Fatalf("wrapped code lost whitespace or copied chrome: %q", got)
	}
	if got != strings.Join(m.selection.snapshot.plain[first:], "\n") {
		t.Fatalf("copy does not equal wrapped visible rows: %q", got)
	}
	rows := strings.Split(m.View(), "\n")
	for i := range m.bodyHeight() {
		row := ansi.Strip(rows[len(m.header())+i])
		if runewidth.StringWidth(row) != m.width || !strings.ContainsAny(row[len(row)-len("│"):], "│█") {
			t.Fatalf("highlight moved/removed scrollbar: %q", row)
		}
	}
}

func TestTranscriptSelectionOpenCodeAndDiffPanelPadding(t *testing.T) {
	t.Run("open fence", func(t *testing.T) {
		m := selectionTestUI(t, 80, 24)
		m.entries = []entry{{role: "Assistant", content: "```text\n\tA  \n\n    B  "}}
		m.streaming, m.layoutWidth = 0, 0
		m.View()
		selectionDrag(t, m, 0, 0, 79, 2)
		if got := m.selectedText(); got != "*     A  \n  \n      B  " {
			t.Fatalf("open-code copy lost tabs/trailing spaces or kept panel padding: %q", got)
		}
		m.handleTextSelectionMouse(selectionMouse(tea.MouseActionPress, 60, 0))
		if m.selection.snapshot != nil || m.textSelectionActive() {
			t.Fatal("click on code-panel filler retained a snapshot")
		}
	})
	t.Run("edit diff", func(t *testing.T) {
		m := selectionTestUI(t, 80, 24)
		m.entries = []entry{{role: "Tool", content: "Applied edits"}}
		m.toolRecords = []session.ToolRecord{{EntryIndex: 0, Name: "edit", Status: "succeeded", Diff: "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old  \n+new  \n"}}
		m.layoutWidth = 0
		m.View()
		first := -1
		for i, line := range m.lines {
			if strings.Contains(line, "- old") {
				first = i
				break
			}
		}
		if first < 0 {
			t.Fatalf("missing diff rows: %q", m.lines)
		}
		selectionDrag(t, m, 5, first, 79, first+1)
		if got := m.selectedText(); got != "1 - old  \n     1 + new  " {
			t.Fatalf("diff copy lost whitespace or included panel padding: %q", got)
		}
	})
}
