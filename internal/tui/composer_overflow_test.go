package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mattn/go-runewidth"
	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
)

// overflowUI builds a ui instance at the given size for overflow testing.
func overflowUI(t *testing.T, style string, width, height int) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local", Verified: true, ComposerStyle: style}, t.TempDir(), nil, session.Snapshot{})
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
	// The decision bar replaces the composer while a review is pending; it
	// must fit at 40 and 80 columns for every composer style (FR-22), for
	// both hint variants (gate pending and ready).
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			for _, ready := range []bool{false, true} {
				m := overflowUI(t, style, width, 24)
				m.pending = &agent.ApprovalRequest{Kind: "edit"}
				if !ready {
					m.reviewSeen = []bool{false}
				}
				rows := m.composerLines()
				if len(rows) != 2 {
					t.Fatalf("%s/%d ready=%v: decision bar rows = %d, want 2", style, width, ready, len(rows))
				}
				for i, row := range rows {
					if w := plainWidth(row); w > m.width {
						t.Fatalf("%s/%d ready=%v: decision row %d width %d > %d: %q", style, width, ready, i, w, m.width, stripANSI(row))
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

// TestComposerBarDecisionFocusAndGate pins the decision bar's single focus
// marker and the muted Approve before the page gate is satisfied. ANSI is
// forced so the Selected and Muted roles are distinguishable.
func TestComposerBarDecisionFocusAndGate(t *testing.T) {
	forceANSI(t)
	for _, style := range composerStyles {
		for _, width := range []int{40, 80} {
			m := overflowUI(t, style, width, 24)
			m.pending = &agent.ApprovalRequest{Kind: "edit"}
			m.reviewSeen = []bool{false} // a page is still unread
			m.reviewFocus = focusApprove
			bar := strings.Join(m.reviewActionLines(), "\n")
			if got := strings.Count(stripANSI(bar), "> "); got != 1 {
				t.Fatalf("%s/%d: focus markers = %d, want 1: %q", style, width, got, stripANSI(bar))
			}
			if !strings.Contains(bar, withBase(m.theme.Muted, m.theme.Base).Render("> [ Approve ]")) || strings.Contains(bar, withBase(m.theme.Selected, m.theme.Base).Render("> [ Approve ]")) {
				t.Fatalf("%s/%d: pre-gate Approve is not muted: %q", style, width, bar)
			}
			// Every page read: the focused Approve takes the Selected role.
			m.reviewSeen = []bool{true}
			bar = strings.Join(m.reviewActionLines(), "\n")
			if !strings.Contains(bar, withBase(m.theme.Selected, m.theme.Base).Render("> [ Approve ]")) {
				t.Fatalf("%s/%d: ready Approve is not selected: %q", style, width, bar)
			}
			// Decline focused: the marker moves and Approve returns to muted.
			m.reviewFocus = focusDecline
			bar = strings.Join(m.reviewActionLines(), "\n")
			if !strings.Contains(bar, withBase(m.theme.Selected, m.theme.Base).Render("> [ Decline ]")) {
				t.Fatalf("%s/%d: Decline is not the focused button: %q", style, width, bar)
			}
			if got := strings.Count(stripANSI(bar), "> "); got != 1 {
				t.Fatalf("%s/%d: focus markers = %d, want 1: %q", style, width, got, stripANSI(bar))
			}
			if !strings.Contains(bar, withBase(m.theme.Muted, m.theme.Base).Render("  [ Approve ]")) || strings.Contains(bar, withBase(m.theme.Selected, m.theme.Base).Render("> [ Approve ]")) {
				t.Fatalf("%s/%d: unfocused Approve is not muted: %q", style, width, bar)
			}
		}
	}
}

// TestRoundedComposerBoxAcrossWidths pins the rounded box: full-width edges,
// the "> " prompt on the draft's first row, wrapped rows indented under the
// text column inside the box, and no row wider than the terminal.
func TestRoundedComposerBoxAcrossWidths(t *testing.T) {
	for _, width := range []int{41, 60, 80, 120} {
		m := overflowUI(t, "rounded", width, 24)
		m.input = []rune(strings.Repeat("word ", 60))
		m.edit.caret = len(m.input)
		m.caretTyped = true
		style, fixed, inputWidth := m.composerLayout()
		if style != "rounded" || fixed != 3 || inputWidth != width-6 {
			t.Fatalf("width %d: layout = %s/%d/%d, want rounded/3/%d", width, style, fixed, inputWidth, width-6)
		}
		rows := m.composerLines()
		plain := make([]string, len(rows))
		for i, row := range rows {
			plain[i] = stripANSI(row)
			if w := plainWidth(row); w != width {
				t.Fatalf("width %d: row %d width %d, want %d: %q", width, i, w, width, plain[i])
			}
		}
		if len(rows) < 5 || strings.TrimSpace(plain[0]) != "" {
			t.Fatalf("width %d: want gap, top edge, wrapped input, bottom edge: %q", width, plain)
		}
		if plain[1] != "╭"+strings.Repeat("─", width-2)+"╮" || plain[len(plain)-1] != "╰"+strings.Repeat("─", width-2)+"╯" {
			t.Fatalf("width %d: rounded edges wrong: %q / %q", width, plain[1], plain[len(plain)-1])
		}
		if !strings.HasPrefix(plain[2], "│ > word") || !strings.HasSuffix(plain[2], " │") {
			t.Fatalf("width %d: first input row = %q, want prompt inside the box", width, plain[2])
		}
		for i := 3; i < len(plain)-1; i++ {
			if !strings.HasPrefix(plain[i], "│   ") || strings.HasPrefix(plain[i], "│   >") || !strings.HasSuffix(plain[i], " │") {
				t.Fatalf("width %d: continuation row %d = %q, want 2-cell indent inside the box", width, i, plain[i])
			}
		}
		if !strings.Contains(strings.Join(plain, "\n"), "█") {
			t.Fatalf("width %d: caret missing: %q", width, plain)
		}
		assertViewport(t, m.View(), width, 24)
	}
}

func TestRoundedComposerPlaceholderAndBorderRole(t *testing.T) {
	forceANSI(t)
	m := overflowUI(t, "rounded", 80, 24)
	m.caretOn = false
	rows := m.composerLines()
	if got := stripANSI(rows[2]); !strings.HasPrefix(got, "│ > Ask Likha… // escapes a slash") {
		t.Fatalf("placeholder row = %q", got)
	}
	border := withBase(m.theme.Border, m.theme.Base)
	if !strings.Contains(rows[1], border.Render("╭"+strings.Repeat("─", 78)+"╮")) {
		t.Fatalf("top edge not in the Border role: %q", rows[1])
	}
	if !strings.Contains(rows[2], border.Render("│ ")) || !strings.Contains(rows[2], withBase(m.theme.Muted, m.theme.Base).Render(fit("Ask Likha… // escapes a slash", 74))) {
		t.Fatalf("placeholder not muted inside a Border box: %q", rows[2])
	}
	m.working = true
	if got := stripANSI(m.composerLines()[2]); !strings.HasPrefix(got, "│ > Type to queue…") {
		t.Fatalf("working placeholder row = %q", got)
	}
}

func TestRoundedComposerFallsBackToMinimalAtMinWidth(t *testing.T) {
	m := overflowUI(t, "rounded", 40, 12)
	minimal := overflowUI(t, "minimal", 40, 12)
	for _, draft := range []string{"", strings.Repeat("narrow ", 30)} {
		m.input, minimal.input = []rune(draft), []rune(draft)
		m.edit.caret, minimal.edit.caret = len(m.input), len(minimal.input)
		if got, want := strings.Join(m.composerLines(), "\n"), strings.Join(minimal.composerLines(), "\n"); got != want {
			t.Fatalf("rounded at 40 cols = %q, want minimal %q", stripANSI(got), stripANSI(want))
		}
		if style, fixed, inputWidth := m.composerLayout(); style != "minimal" || fixed != 2 || inputWidth != 40 {
			t.Fatalf("layout at 40 cols = %s/%d/%d, want minimal/2/40", style, fixed, inputWidth)
		}
	}
	if m.composerStyle != "rounded" {
		t.Fatalf("narrow fallback changed the selection to %q", m.composerStyle)
	}
	m.Update(tea.WindowSizeMsg{Width: 41, Height: 12})
	if style, _, _ := m.composerLayout(); style != "rounded" {
		t.Fatalf("41 cols resolved %q, want rounded", style)
	}
}

func TestRoundedComposerTailHidesPromptOnContinuation(t *testing.T) {
	m := overflowUI(t, "rounded", 60, 12)
	m.input = []rune(strings.Repeat("tail ", 400))
	m.edit.caret = len(m.input)
	rows := m.composerLines()
	for i, row := range rows[2 : len(rows)-1] {
		if strings.HasPrefix(stripANSI(row), "│ > ") {
			t.Fatalf("scrolled tail row %d shows the first-row prompt: %q", i, stripANSI(row))
		}
	}
	assertViewport(t, m.View(), 60, 12)
}

func TestRoundedComposerReviewBarUnchanged(t *testing.T) {
	for _, width := range []int{40, 80} {
		rounded := overflowUI(t, "rounded", width, 24)
		minimal := overflowUI(t, "minimal", width, 24)
		for _, m := range []*ui{rounded, minimal} {
			m.pending = &agent.ApprovalRequest{Kind: "edit"}
			m.reviewSeen = []bool{false}
		}
		if got, want := strings.Join(rounded.composerLines(), "\n"), strings.Join(minimal.composerLines(), "\n"); got != want {
			t.Fatalf("width %d: rounded review bar differs: %q vs %q", width, stripANSI(got), stripANSI(want))
		}
	}
}

func TestRoundedBoxRunesSets(t *testing.T) {
	tl, tr, bl, br, h, v := roundedBoxRunes(false)
	if tl+tr+bl+br+h+v != "╭╮╰╯─│" {
		t.Fatalf("unicode box = %q", tl+tr+bl+br+h+v)
	}
	tl, tr, bl, br, h, v = roundedBoxRunes(true)
	if tl+tr+bl+br+h+v != "++++-|" {
		t.Fatalf("ascii box = %q", tl+tr+bl+br+h+v)
	}
}
