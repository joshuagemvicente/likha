package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
)

func composerPlainTextRows(rows []composerTextRow) []string {
	plain := make([]string, len(rows))
	for i, row := range rows {
		var text strings.Builder
		for _, glyph := range row.glyphs {
			text.WriteString(glyph.text)
		}
		plain[i] = text.String()
	}
	return plain
}

func TestComposerSourceLayoutMatchesSafeWrap(t *testing.T) {
	for _, draft := range []string{"", "abcdef", "ab界🙂z", "a\t\x1b\u0301\u200b\u202eb", "a\n\nb\n", "abc\n界"} {
		input := []rune(draft)
		for _, width := range []int{1, 3, 6, 40, 74} {
			for _, caret := range []int{0, len(input) / 2, len(input)} {
				for _, show := range []bool{false, true} {
					text := draft
					if show {
						text = string(input[:caret]) + "█" + string(input[caret:])
					}
					layout := layoutComposerDraft(input, width, caret, show)
					if got, want := composerPlainTextRows(layout.rows), wrap(text, width); !reflect.DeepEqual(got, want) {
						t.Fatalf("draft %q width %d caret %d visible %t: rows = %q, want %q", draft, width, caret, show, got, want)
					}
				}
			}
		}
	}
}

func TestComposerSelectionHighlightsOnlySourceCharacters(t *testing.T) {
	forceANSI(t)
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			t.Run(fmt.Sprintf("%s/%d", style, width), func(t *testing.T) {
				m := overflowUI(t, style, width, 12)
				m.input = []rune("head A界\t\u0301\nnext\x1b\u200b tail")
				m.edit.setSelection(m.input, 5, len(m.input)-5)
				view := m.composerTextViewport()
				rows := m.composerLines()
				normal := withBase(m.theme.Normal, m.theme.Base)
				selected := textSelectionStyle(m.theme)
				first := normal.Render("head ") + selected.Render("A界\\u0009\\u0301") + normal.Render(strings.Repeat(" ", view.width-20))
				second := selected.Render("next\\u001B\\u200B") + normal.Render(fit(" tail", view.width-16))
				if !strings.Contains(rows[view.textOffset], first) || !strings.Contains(rows[view.textOffset+1], second) {
					t.Fatalf("source highlighting includes decoration/padding or misses an escape: %q", rows)
				}
				if got := m.edit.selectedText(m.input); got != "A界\t\u0301\nnext\x1b\u200b" {
					t.Fatalf("selected source = %q", got)
				}
				if strings.Contains(stripANSI(strings.Join(rows, "\n")), "█") {
					t.Fatal("selection inserted a synthetic caret")
				}
				for i, row := range rows {
					if got := plainWidth(row); got != width {
						t.Fatalf("row %d width = %d, want %d", i, got, width)
					}
				}
				assertViewport(t, m.View(), width, 12)
				m.edit.setSelection(m.input, len(m.input)-5, 5)
				if got := strings.Join(m.composerLines(), "\n"); got != strings.Join(rows, "\n") {
					t.Fatal("reversing the source range changed its rendering")
				}
			})
		}
	}
}

func TestComposerSelectionNeverHighlightsSyntheticCaret(t *testing.T) {
	forceANSI(t)
	m := overflowUI(t, "rounded", 80, 24)
	input := []rune("A界\tZ")
	layout := layoutComposerDraft(input, 20, 1, true)
	normal, selected := withBase(m.theme.Normal, m.theme.Base), textSelectionStyle(m.theme)
	got := renderComposerTextRow(layout.rows[0], 20, normal, selected, 0, len(input), true)
	want := selected.Render("A") + normal.Render("█") + selected.Render("界\\u0009Z") + normal.Render(strings.Repeat(" ", 9))
	if got != want {
		t.Fatalf("caret or padding used selection style: %q, want %q", got, want)
	}
}

func TestComposerSelectionHighlightsEscapesAcrossSoftWrap(t *testing.T) {
	forceANSI(t)
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			m := overflowUI(t, style, width, 12)
			_, _, textWidth := m.composerLayout()
			anchor := textWidth - 3
			m.input = []rune(strings.Repeat("x", anchor) + "\t界z")
			m.edit.setSelection(m.input, anchor, anchor+1)
			view, rows := m.composerTextViewport(), m.composerLines()
			selected := textSelectionStyle(m.theme)
			if !strings.Contains(rows[view.textOffset], selected.Render("\\u0")) || !strings.Contains(rows[view.textOffset+1], selected.Render("009")) {
				t.Fatalf("%s/%d: split escape not fully highlighted: %q", style, width, rows)
			}
			if got := m.edit.selectedText(m.input); got != "\t" {
				t.Fatalf("split escape copied display text %q", got)
			}
			assertViewport(t, m.View(), width, 12)
		}
	}
}

func TestComposerSelectionNewlineDoesNotHighlightPadding(t *testing.T) {
	forceANSI(t)
	for _, style := range composerStyles {
		m := overflowUI(t, style, 80, 24)
		m.input = []rune("first\nsecond")
		m.caretOn, m.caretTyped = false, false
		before := strings.Join(m.composerLines(), "\n")
		m.edit.setSelection(m.input, 5, 6)
		if got := strings.Join(m.composerLines(), "\n"); got != before {
			t.Fatalf("%s: a selected newline highlighted synthetic cells", style)
		}
		if got := m.edit.selectedText(m.input); got != "\n" {
			t.Fatalf("newline source selection = %q", got)
		}
	}
}

func TestComposerSelectionBlinkKeepsMouseGeometryStable(t *testing.T) {
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			for _, extent := range []int{0, 9} {
				m := overflowUI(t, style, width, 12)
				m.input = []rune(strings.Repeat("draft界\t ", 100))
				anchor := len(m.input) / 2
				m.edit.setSelection(m.input, anchor, anchor+extent)
				m.caretOn, m.caretTyped = true, false
				before := strings.Join(m.composerLines(), "\n")
				left, top, textWidth, rows := m.composerTextBounds()
				var hits []int
				for y := top; y < top+rows; y++ {
					for x := left; x <= left+textWidth; x++ {
						hit, ok := m.composerCaretAt(x, y)
						if !ok {
							t.Fatal("visible selection row was not a mouse hit")
						}
						hits = append(hits, hit)
					}
				}
				m.caretOn = false
				if got := strings.Join(m.composerLines(), "\n"); got != before || strings.Contains(stripANSI(got), "█") {
					t.Fatalf("%s/%d extent %d: blink changed selection rendering", style, width, extent)
				}
				l, y, w, n := m.composerTextBounds()
				if l != left || y != top || w != textWidth || n != rows {
					t.Fatal("blink changed selection bounds")
				}
				i := 0
				for y := top; y < top+rows; y++ {
					for x := left; x <= left+textWidth; x++ {
						if hit, ok := m.composerCaretAt(x, y); !ok || hit != hits[i] {
							t.Fatal("blink changed source-coordinate mouse mapping")
						}
						i++
					}
				}
				assertViewport(t, m.View(), width, 12)
			}
		}
	}
}

func TestComposerCaretAtSourceCoordinateRoundTrips(t *testing.T) {
	for _, style := range composerStyles {
		for _, size := range [][2]int{{40, 12}, {41, 12}, {80, 24}} {
			for _, draft := range []string{
				strings.Repeat("abc界", 100),
				strings.Repeat("a\t\u0301\x1b\u200b界", 60),
				strings.Repeat("first\n\n界last\n", 20),
			} {
				m := overflowUI(t, style, size[0], size[1])
				m.input = []rune(draft)
				for _, caret := range []int{0, len(m.input) / 2, len(m.input)} {
					for _, phase := range []string{"caret off", "caret on", "empty selection"} {
						m.edit.clearSelection()
						m.edit.caret = caret
						m.caretOn, m.caretTyped = phase == "caret on", false
						if phase == "empty selection" {
							m.edit.setSelection(m.input, caret, caret)
							m.caretOn = true
						}
						view := m.composerTextViewport()
						layout := layoutComposerDraft(m.input, view.width, caret, m.caretVisible() && !m.edit.selecting)
						left, top, textWidth, rows := m.composerTextBounds()
						if textWidth != view.width || rows != len(view.rows) {
							t.Fatal("rendering and bounds disagree")
						}
						for source, position := range layout.positions {
							row := position.row - view.hidden
							if row < 0 || row >= rows {
								continue
							}
							if got, ok := m.composerCaretAt(left+position.column, top+row); !ok || got != source {
								t.Fatalf("%s/%dx%d %s caret %d: source %d at (%d,%d) = (%d,%t), hidden %d", style, size[0], size[1], phase, caret, source, position.column, position.row, got, ok, view.hidden)
							}
						}
						position := layout.positions[caret]
						if m.edit.selecting && (position.row < view.hidden || position.row >= view.hidden+rows) {
							t.Fatal("active caret scrolled out of the draft")
						}
						frame := strings.Split(m.View(), "\n")
						composer := m.composerLines()
						for row := range rows {
							if frame[top+row] != composer[view.textOffset+row] {
								t.Fatal("mouse y offset does not match full-screen rendering")
							}
						}
						assertViewport(t, m.View(), size[0], size[1])
					}
				}
			}
		}
	}
}

func TestComposerCaretAtNearestWideEscapeAndNewline(t *testing.T) {
	for _, style := range composerStyles {
		m := overflowUI(t, style, 80, 24)
		m.input = []rune("A界\tB\n\nC")
		m.edit.setSelection(m.input, 0, 0)
		left, top, width, rows := m.composerTextBounds()
		if rows != 3 {
			t.Fatalf("%s: visible rows = %d, want 3", style, rows)
		}
		for _, hit := range [][3]int{
			{-100, 0, 0}, {0, 0, 0}, {1, 0, 1}, {2, 0, 1}, {3, 0, 2},
			{6, 0, 2}, {7, 0, 3}, {9, 0, 3}, {10, 0, 4}, {width + 100, 0, 4},
			{-100, 1, 5}, {width + 100, 1, 5}, {0, 2, 6}, {1, 2, 7}, {width + 100, 2, 7},
		} {
			if got, ok := m.composerCaretAt(left+hit[0], top+hit[1]); !ok || got != hit[2] {
				t.Fatalf("%s: (%d,%d) = (%d,%t), want %d", style, hit[0], hit[1], got, ok, hit[2])
			}
		}
		for _, y := range []int{top - 1, top + rows, 0, m.height - 1} {
			if _, ok := m.composerCaretAt(left, y); ok {
				t.Fatalf("%s: non-input row %d was a hit", style, y)
			}
		}
		m.input = []rune("A界B")
		m.edit.clearSelection()
		m.edit.caret, m.caretOn, m.caretTyped = 1, true, false
		left, top, _, _ = m.composerTextBounds()
		for _, hit := range [][2]int{{0, 0}, {1, 1}, {2, 1}, {3, 1}, {4, 2}, {5, 3}} {
			if got, ok := m.composerCaretAt(left+hit[0], top); !ok || got != hit[1] {
				t.Fatalf("%s: middle synthetic caret mapping at %d = (%d,%t), want %d", style, hit[0], got, ok, hit[1])
			}
		}
	}
}

func TestComposerTextBoundsWithCompletionPopup(t *testing.T) {
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			for _, popup := range []string{"mention", "command"} {
				m := overflowUI(t, style, width, 12)
				m.input = []rune(strings.Repeat("draft ", 100))
				m.edit.setSelection(m.input, len(m.input), len(m.input))
				if popup == "mention" {
					m.mention.open, m.mention.matches = true, []string{"file-a", "file-b"}
				} else {
					m.commandPopup.open, m.commandPopup.matches = true, commands[:min(2, len(commands))]
				}
				view := m.composerTextViewport()
				_, top, _, rows := m.composerTextBounds()
				frame, composer := strings.Split(m.View(), "\n"), m.composerLines()
				for row := range rows {
					if frame[top+row] != composer[view.textOffset+row] {
						t.Fatalf("%s/%d %s: completion popup changed composer y mapping", style, width, popup)
					}
				}
				assertViewport(t, m.View(), width, 12)
			}
		}
	}
}

func TestComposerTextBoundsStyleInsetsAndTail(t *testing.T) {
	for _, style := range composerStyles {
		for _, width := range []int{40, 41, 80} {
			m := overflowUI(t, style, width, 12)
			m.input = []rune("text")
			m.edit.caret = len(m.input)
			left, top, textWidth, rows := m.composerTextBounds()
			wantLeft, wantWidth, bottom := 0, width, 0
			if width > minWidth {
				switch style {
				case "rounded":
					wantLeft, wantWidth, bottom = 4, width-6, 1
				case "bordered":
					wantLeft, wantWidth, bottom = 2, width-4, 1
				case "chatter":
					wantLeft, wantWidth = 6, width-6
				}
			}
			wantTop := m.height - m.statusLineHeight() - bottom - 1
			if left != wantLeft || top != wantTop || textWidth != wantWidth || rows != 1 {
				t.Fatalf("%s/%d: bounds = (%d,%d,%d,%d), want (%d,%d,%d,1)", style, width, left, top, textWidth, rows, wantLeft, wantTop, wantWidth)
			}
			m.input = []rune(strings.Repeat("draft ", 200) + "TAIL")
			m.edit.caret = len(m.input)
			before := m.composerTextViewport()
			if before.hidden == 0 || !strings.Contains(strings.Join(composerPlainTextRows(before.rows), "\n"), "TAIL") {
				t.Fatalf("%s/%d: ordinary end caret lost the editable tail", style, width)
			}
			m.edit.setSelection(m.input, len(m.input), len(m.input))
			selected := m.composerTextViewport()
			if selected.hidden == 0 || !strings.Contains(strings.Join(composerPlainTextRows(selected.rows), "\n"), "TAIL") {
				t.Fatalf("%s/%d: end selection lost the editable tail", style, width)
			}
			m.edit.extendSelection(m.input, len(m.input)/2)
			view := m.composerTextViewport()
			position := layoutComposerDraft(m.input, view.width, m.edit.caret, false).positions[m.edit.caret]
			if position.row < view.hidden || position.row >= view.hidden+len(view.rows) {
				t.Fatalf("%s/%d: extending selection into the hidden draft lost its caret", style, width)
			}
			assertViewport(t, m.View(), width, 12)
		}
	}
}

func TestComposerPlaceholderMapsOnlyToEmptySource(t *testing.T) {
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			for _, state := range []string{"idle", "working", "queued"} {
				m := overflowUI(t, style, width, 12)
				m.working = state == "working"
				if state == "queued" {
					m.queue = []string{"one", "two"}
				}
				m.edit.setSelection(m.input, 0, 0)
				left, top, textWidth, rows := m.composerTextBounds()
				for y := top; y < top+rows; y++ {
					for _, x := range []int{left - 100, left, left + 8, left + textWidth + 100} {
						if got, ok := m.composerCaretAt(x, y); !ok || got != 0 {
							t.Fatalf("placeholder copied display coordinates (%d,%t)", got, ok)
						}
					}
				}
				for _, row := range m.composerTextViewport().rows {
					for _, glyph := range row.glyphs {
						if glyph.source != -1 {
							t.Fatal("placeholder has a selectable source rune")
						}
					}
				}
				if got := m.edit.selectedText(m.input); got != "" {
					t.Fatalf("empty source selected %q", got)
				}
			}
		}
	}
}

func TestComposerMouseBoundsHiddenByOverlays(t *testing.T) {
	for _, state := range []struct {
		name string
		set  func(*ui)
	}{
		{"setup", func(m *ui) { m.mode = modeSetup }},
		{"pending review", func(m *ui) { m.pending = &agent.ApprovalRequest{} }},
		{"provider key", func(m *ui) { m.keyModal.open = true }},
		{"dialog", func(m *ui) { m.dialog.open = true }},
		{"ask", func(m *ui) { m.ask.pending = true }},
		{"consent", func(m *ui) { m.consent.pending = true }},
		{"agent inspector", func(m *ui) { m.agentInspector.open = true; m.agentInspector.sessionID = m.snapshot.ID }},
		{"tool inspector", func(m *ui) { m.toolInspector.open = true; m.toolInspector.sessionID = m.snapshot.ID }},
		{"too narrow", func(m *ui) { m.width = 39 }},
		{"too short", func(m *ui) { m.height = 11 }},
	} {
		t.Run(state.name, func(t *testing.T) {
			m := overflowUI(t, "rounded", 80, 24)
			left, top, _, _ := m.composerTextBounds()
			state.set(m)
			if l, y, w, n := m.composerTextBounds(); l != 0 || y != 0 || w != 0 || n != 0 {
				t.Fatalf("hidden input exposed bounds (%d,%d,%d,%d)", l, y, w, n)
			}
			if _, ok := m.composerCaretAt(left, top); ok {
				t.Fatal("hidden input was a mouse hit")
			}
		})
	}
}

func TestComposerSelectionCaretVerticalWideRuneWrapBoundary(t *testing.T) {
	for _, style := range composerStyles {
		for _, width := range []int{40, 41, 80} {
			t.Run(fmt.Sprintf("%s/%d", style, width), func(t *testing.T) {
				m := overflowUI(t, style, width, 12)
				textWidth := m.composerInputWidth()
				m.input = []rune(strings.Repeat("a", textWidth-1) + "界" + strings.Repeat("b", textWidth*2))
				caret := textWidth - 1
				m.edit.setSelection(m.input, caret, caret)
				position := layoutComposerDraft(m.input, textWidth, caret, false).positions[caret]
				if position.row != 1 || position.column != 0 {
					t.Fatalf("wide-rune insertion position = (%d,%d), want (1,0)", position.row, position.column)
				}
				want := 2*textWidth - 2
				next, moved := m.composerSelectionCaretVertical(caret, 1)
				if !moved || next != want {
					t.Fatalf("selection down at wide-rune boundary = (%d,%t), want (%d,true)", next, moved, want)
				}
				if previous, moved := m.composerSelectionCaretVertical(next, -1); !moved || previous != caret {
					t.Fatalf("selection up at wide-rune boundary = (%d,%t), want (%d,true)", previous, moved, caret)
				}
				if m.edit.caret != caret || m.edit.selectionAnchor != caret || !m.edit.selecting {
					t.Fatal("vertical coordinate lookup mutated the selected source range")
				}
			})
		}
	}
}

func TestComposerSelectionCaretVerticalEscapedControls(t *testing.T) {
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			for _, escaped := range []rune{'\t', '\x1b', '\u0301', '\u200b'} {
				m := overflowUI(t, style, width, 12)
				textWidth := m.composerInputWidth()
				m.input = append([]rune(strings.Repeat("a", textWidth-1)), escaped)
				m.input = append(m.input, []rune(strings.Repeat("b", textWidth*2))...)
				caret := textWidth
				position := layoutComposerDraft(m.input, textWidth, caret, false).positions[caret]
				if position.row != 1 || position.column != 5 {
					t.Fatalf("%s/%d %U: position after wrapped escape = (%d,%d), want (1,5)", style, width, escaped, position.row, position.column)
				}
				next, moved := m.composerSelectionCaretVertical(caret, 1)
				if !moved || next != 2*textWidth {
					t.Fatalf("%s/%d %U: selection down after wrapped escape = (%d,%t), want (%d,true)", style, width, escaped, next, moved, 2*textWidth)
				}
				if previous, moved := m.composerSelectionCaretVertical(next, -1); !moved || previous != caret {
					t.Fatalf("%s/%d %U: selection up after wrapped escape = (%d,%t), want (%d,true)", style, width, escaped, previous, moved, caret)
				}
			}
		}
	}
}

func TestComposerSelectionCaretVerticalRetainsPreferredColumn(t *testing.T) {
	for _, style := range composerStyles {
		m := overflowUI(t, style, 80, 24)
		m.input = []rune("a\tb\nx\nabcdefghij")
		// The insertion point before b is display column 7, not rune column 2.
		// A short intervening row must not replace that preferred column.
		caret := 2
		for _, step := range [][2]int{{1, 5}, {1, 13}, {-1, 5}, {-1, 2}} {
			next, moved := m.composerSelectionCaretVertical(caret, step[0])
			if !moved || next != step[1] {
				t.Fatalf("%s: selection vertical from %d direction %d = (%d,%t), want (%d,true)", style, caret, step[0], next, moved, step[1])
			}
			caret = next
		}
		m.composerVertical.reset()
		if next, moved := m.composerSelectionCaretVertical(5, 1); !moved || next != 7 {
			t.Fatalf("%s: preferred-column reset = (%d,%t), want (7,true)", style, next, moved)
		}
		if next, moved := m.composerSelectionCaretVertical(len(m.input), 1); moved || next != len(m.input) {
			t.Fatalf("%s: movement below last row = (%d,%t)", style, next, moved)
		}
	}
}

func TestComposerTextViewportUsesFrozenGestureGeometry(t *testing.T) {
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			for _, draft := range []string{"middle caret", "wrapped caret"} {
				m := overflowUI(t, style, width, 12)
				m.input, m.edit.caret = []rune("abcd"), 1
				if draft == "wrapped caret" {
					textWidth := m.composerInputWidth()
					m.input = []rune(strings.Repeat("a", textWidth-1) + "bc")
					m.edit.caret = textWidth - 1
				}
				m.caretOn, m.caretTyped = true, false
				ordinary := m.composerTextViewport()
				left, top, _, _ := m.composerTextBounds()
				if !m.handleTextSelectionMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: left + 3, Y: top}) {
					t.Fatal("composer press did not start a gesture")
				}
				frozen, active := m.frozenComposerViewport()
				if !active || !reflect.DeepEqual(m.composerTextViewport(), frozen) {
					t.Fatalf("%s/%d %s: composer ignored the frozen gesture viewport", style, width, draft)
				}
				if frozen.hidden != ordinary.hidden || frozen.width != ordinary.width || len(frozen.rows) != len(ordinary.rows) {
					t.Fatal("freezing changed visible wrap geometry")
				}
				for i, row := range frozen.rows {
					before := ordinary.rows[i]
					if row.columns != before.columns || !reflect.DeepEqual(row.points, before.points) || len(row.glyphs) != len(before.glyphs) {
						t.Fatal("freezing changed insertion points or display cell widths")
					}
					for j, glyph := range row.glyphs {
						want := before.glyphs[j]
						if want.source == -1 {
							want.text = " "
						}
						if glyph != want {
							t.Fatalf("frozen glyph = %#v, want %#v", glyph, want)
						}
					}
				}
				m.edit.extendSelection(m.input, len(m.input))
				m.caretOn = false
				m.mention.open, m.mention.matches = true, []string{"one", "two", "three"}
				if got := m.composerTextViewport(); !reflect.DeepEqual(got, frozen) {
					t.Fatal("selection, blink, or completion changed the frozen source map")
				}
			}
		}
	}
}

func TestComposerTextViewportUsesOutputSnapshotGeometry(t *testing.T) {
	for _, style := range composerStyles {
		m := overflowUI(t, style, 80, 24)
		m.input, m.edit.caret = []rune("abcd"), 1
		m.caretOn, m.caretTyped = true, false
		m.mention.open, m.mention.matches = true, []string{"one"}
		ordinary := m.composerTextViewport()
		m.rebuild()
		m.freezeTranscriptSelection()
		m.selection.surface = selectionTranscript
		frozen, active := m.frozenComposerViewport()
		if !active || !reflect.DeepEqual(frozen, ordinary) {
			t.Fatalf("%s: output snapshot did not retain the pre-press composer viewport", style)
		}
		m.edit.caret, m.caretOn = len(m.input), false
		m.mention.matches = []string{"one", "two", "three", "four"}
		if got := m.composerTextViewport(); !reflect.DeepEqual(got, frozen) {
			t.Fatalf("%s: output snapshot recomputed composer or popup geometry", style)
		}
		m.clearTranscriptSelection()
		if _, active := m.frozenComposerViewport(); active {
			t.Fatal("clearing output selection retained its composer viewport")
		}
		if got := m.composerTextViewport(); reflect.DeepEqual(got, frozen) {
			t.Fatal("clearing output selection did not restore live composer geometry")
		}
	}
}
