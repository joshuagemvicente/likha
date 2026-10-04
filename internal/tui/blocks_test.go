package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"likha/internal/providers"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

// Transcript blocks (transcript-redesign § Glyph language, § Backgrounds,
// § Wrapping): every entry starts with its block glyph in a two-cell gutter,
// wraps at word boundaries under the text column, and only user prompts sit
// on a padded band.

func blocksTestUI(t *testing.T, ascii bool, width int) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, ASCII: ascii}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	return m
}

// layoutEntries replaces the transcript with the given entries and lays it
// out, returning the first layout line of each.
func layoutEntries(m *ui, entries ...entry) []int {
	m.entries = append(m.entries[:0], entries...)
	m.layoutWidth = 0
	m.rebuild()
	return m.entryLines
}

func TestBlockGlyphPerRoleBothSets(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		m := blocksTestUI(t, ascii, 80)
		g := likhaui.BlockGlyphSet(ascii)
		if m.blocks != g {
			t.Fatalf("ascii=%t: ui glyph set %+v, want %+v", ascii, m.blocks, g)
		}
		cases := []struct {
			role, want string
		}{
			{"You", g.User + " prompt"},
			{"Queued", g.User + " queued · held"},
			{"Assistant", g.Assistant + " answer"},
			{"Tool", g.ToolHeader + " tool output"},
			{"Reasoning", g.Thought + " pondering"},
			{"Error", g.Error + " failure"},
			{"Likha", g.Notice + " notice"},
			{"Agent", g.Notice + " legacy agent line"},
		}
		content := map[string]string{"You": "prompt", "Queued": "held", "Assistant": "answer", "Tool": "tool output", "Reasoning": "pondering", "Error": "failure", "Likha": "notice", "Agent": "legacy agent line"}
		var entries []entry
		for _, tc := range cases {
			entries = append(entries, entry{role: tc.role, content: content[tc.role]})
		}
		starts := layoutEntries(m, entries...)
		for i, tc := range cases {
			at := starts[i]
			if tc.role == "You" || tc.role == "Queued" {
				at++ // the band's top padding row comes first
			}
			if got := m.lines[at]; got != tc.want {
				t.Fatalf("ascii=%t %s: first row %q, want %q", ascii, tc.role, got, tc.want)
			}
		}
	}
}

func TestUserBandPaddingRows(t *testing.T) {
	theme := likhaui.Resolve("catppuccin", true)
	m := blocksTestUI(t, false, 60)
	m.theme = theme
	prompt := strings.Repeat("long prompt words ", 8)
	starts := layoutEntries(m,
		entry{role: "Assistant", content: "before"},
		entry{role: "You", content: prompt},
		entry{role: "Assistant", content: "after"},
	)
	band := colorName(theme.BgUser.GetBackground())
	first, next := starts[1], starts[2]
	// Layout: top padding, the wrapped prompt rows, bottom padding, then the
	// blank canvas separator before the next block.
	rows := wrapHanging(prompt, contentWidth(m.width), 2, 2)
	if want := first + 1 + len(rows) + 1 + 1; next != want {
		t.Fatalf("next block starts at %d, want %d: %q", next, want, m.lines)
	}
	for i := first; i < next-1; i++ {
		if bg := colorName(m.lineStyles[i].GetBackground()); bg != band {
			t.Fatalf("user block row %d %q bg %v, want BgUser %v", i, m.lines[i], bg, band)
		}
	}
	if m.lines[first] != "" || m.lines[next-2] != "" {
		t.Fatalf("band padding rows not blank: %q / %q", m.lines[first], m.lines[next-2])
	}
	if !strings.HasPrefix(m.lines[first+1], "> long prompt") {
		t.Fatalf("prompt row %q", m.lines[first+1])
	}
	for _, outside := range []int{first - 1, next - 1, next} {
		if bg := colorName(m.lineStyles[outside].GetBackground()); bg == band {
			t.Fatalf("row %d %q outside the prompt block carries the user band", outside, m.lines[outside])
		}
	}
}

func TestBlocksHangUnderTextColumn(t *testing.T) {
	m := blocksTestUI(t, false, 40)
	text := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu"
	starts := layoutEntries(m, entry{role: "Assistant", content: text + "\nsecond paragraph"})
	at := starts[0]
	end := len(m.lines)
	if !strings.HasPrefix(m.lines[at], "⏺ alpha") {
		t.Fatalf("first row %q", m.lines[at])
	}
	var words []string
	for i := at; i < end; i++ {
		line := m.lines[i]
		if i > at && !strings.HasPrefix(line, "  ") {
			t.Fatalf("continuation row %d %q does not hang under the text column", i, line)
		}
		if i > at && strings.HasPrefix(line, "   ") {
			t.Fatalf("continuation row %d %q over-indented", i, line)
		}
		words = append(words, strings.Fields(strings.TrimPrefix(line, "⏺"))...)
	}
	// Word boundaries: no word is split across rows.
	if got, want := strings.Join(words, " "), text+" second paragraph"; got != want {
		t.Fatalf("words regrouped: %q, want %q", got, want)
	}
}

func TestNoColorProfileKeepsGlyphs(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, ascii := range []bool{false, true} {
		m := blocksTestUI(t, ascii, 80)
		g := m.blocks
		layoutEntries(m,
			entry{role: "You", content: "hello"},
			entry{role: "Assistant", content: "hi there"},
			entry{role: "Tool", content: "listing files"},
			entry{role: "Error", content: "boom"},
			entry{role: "Likha", content: "notice"},
		)
		m.scroll, m.following = 0, false
		view := m.View()
		if strings.Contains(view, "\x1b[") {
			t.Fatalf("ascii=%t: no-color profile emitted escapes: %q", ascii, view)
		}
		for _, want := range []string{g.User + " hello", g.Assistant + " hi there", g.ToolHeader + " listing files", g.Error + " boom", g.Notice + " notice"} {
			if !strings.Contains(view, want) {
				t.Fatalf("ascii=%t: %q missing from no-color view: %q", ascii, want, view)
			}
		}
	}
}

func TestLongProseNeverOverflows(t *testing.T) {
	forceANSI(t)
	prose := strings.Repeat("transcript prose wraps at word boundaries ", 12) +
		"https://example.com/" + strings.Repeat("very-long-path-segment/", 10) + " " +
		strings.Repeat("漢字の下書き", 12) + " tab\there esc\x1b[31m zero​width #4493f8"
	for _, ascii := range []bool{false, true} {
		for _, width := range []int{40, 60, 80, 120} {
			m := blocksTestUI(t, ascii, width)
			layoutEntries(m,
				entry{role: "You", content: prose},
				entry{role: "Queued", content: prose},
				entry{role: "Assistant", content: prose},
				entry{role: "Tool", content: prose},
				entry{role: "Reasoning", content: prose},
				entry{role: "Error", content: prose},
				entry{role: "Likha", content: prose},
				entry{role: "Agent", content: prose},
			)
			for i, line := range m.lines {
				if w := runewidth.StringWidth(line); w > contentWidth(width) {
					t.Fatalf("ascii=%t width %d: layout row %d is %d cells: %q", ascii, width, i, w, line)
				}
				if strings.ContainsRune(line, '\x1b') {
					t.Fatalf("width %d: raw escape reached the layout: %q", width, line)
				}
			}
			for page := 0; page*m.bodyHeight() < len(m.lines); page++ {
				m.scroll, m.following = page*m.bodyHeight(), false
				assertViewport(t, m.View(), width, 24)
			}
		}
	}
}

func TestToolEntryStartLineMatchesLayout(t *testing.T) {
	m := blocksTestUI(t, false, 50)
	starts := layoutEntries(m,
		entry{role: "You", content: strings.Repeat("prompt ", 20)},
		entry{role: "Tool", content: "first tool"},
		entry{role: "Assistant", content: strings.Repeat("answer ", 30)},
		entry{role: "Queued", content: "queued words"},
		entry{role: "Tool", content: "second tool"},
	)
	// The rows themselves pin the start: the Assistant block shares the ⏺
	// glyph, so only the item's own text proves the index lands on it.
	for index, want := range map[int]string{1: "⏺ first tool", 4: "⏺ second tool"} {
		got := m.toolEntryStartLine(index)
		if got != starts[index] || strings.TrimRight(m.lines[got], " ") != want || strings.TrimSpace(m.lines[got-1]) != "" {
			t.Fatalf("tool %d starts at %d (%q after %q), want %q at the layout's %d", index, got, m.lines[got], m.lines[got-1], want, starts[index])
		}
	}
	// Focus moves the gutter to the focus glyph without shifting rows.
	before := len(m.lines)
	m.setToolEntryFocus(4)
	m.rebuild()
	if len(m.lines) != before || m.lines[m.toolEntryStartLine(4)] != m.blocks.Focus+" second tool" {
		t.Fatalf("focused tool row %q, lines %d→%d", m.lines[m.toolEntryStartLine(4)], before, len(m.lines))
	}
}

func TestASCIIFlagReachesComposerAndSurvivesSetup(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true, ASCII: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setup.stage = setupModel
	m.setup.models = []string{"alpha"}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupTheme {
		t.Fatalf("theme stage not reached: %d", m.setup.stage)
	}
	if !m.conn.ASCII || !m.composerASCII() {
		t.Fatal("setup dropped the --ascii selection")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := stripANSI(m.View())
	if !strings.Contains(view, "+"+strings.Repeat("-", 78)+"+") || strings.Contains(view, "╭") {
		t.Fatalf("ascii composer box missing: %q", view)
	}
}
