package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"likha/internal/explore"
	"likha/internal/providers"
	"likha/internal/session"
)

// lastSGR returns the last SGR sequence in s, or "" when s has none.
func lastSGR(s string) string {
	at := strings.LastIndex(s, "\x1b[")
	if at < 0 {
		return ""
	}
	end := strings.IndexByte(s[at:], 'm')
	if end < 0 {
		return s[at:]
	}
	return s[at : at+end+1]
}

// The scrollbar splice cuts every body row one cell short. A row painted to
// the full width (the user band) lost its closing reset with that cell, so
// its background bled into the scrollbar glyph.
func TestScrollbarSpliceClosesRowStyle(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for range 40 {
		m.entries = append(m.entries, entry{role: "You", content: "a prompt on the user band"})
	}
	m.layoutWidth = 0
	rows := strings.Split(m.View(), "\n")
	body := m.bodyHeight()
	bar := m.scrollbarRows(body)
	if bar == nil {
		t.Fatal("test fixture must overflow the viewport")
	}
	for i := range body {
		row := rows[len(m.header())+i]
		cut, ok := strings.CutSuffix(row, bar[i])
		if !ok {
			t.Fatalf("body row %d does not end in the scrollbar: %q", i, row)
		}
		if sgr := lastSGR(cut); sgr != "" && sgr != "\x1b[0m" {
			t.Fatalf("body row %d leaves %q open before the scrollbar: %q", i, sgr, row)
		}
	}
}

// hostile is untrusted text that would retitle the window, write the
// clipboard, clear the screen, and move the cursor if it reached the
// terminal verbatim.
const hostile = "x\x1b]0;pwned\x07 \x1b]52;c;aGk=\x1b\\ \x1b[2J\x1b[H \x9b31m \x1bP+q\x1b\\ \r\t\u202e\u200b 日本 🎉 e\u0301 end"

// assertTerminalSafe fails when view carries a control byte, an escape other
// than SGR, or a row wider than width by either width measure.
func assertTerminalSafe(t *testing.T, view string, width int) {
	t.Helper()
	for i, row := range strings.Split(view, "\n") {
		if w := displayWidth(stripANSI(row)); w > width {
			t.Fatalf("row %d is %d cells, width %d: %q", i, w, width, row)
		}
		for j := 0; j < len(row); j++ {
			switch row[j] {
			case '\a', '\r', 0x08:
				t.Fatalf("row %d carries control byte %#x: %q", i, row[j], row)
			case 0x1b:
				end := j + 2
				if j+1 >= len(row) || row[j+1] != '[' {
					t.Fatalf("row %d carries non-CSI escape: %q", i, row)
				}
				for end < len(row) && (row[end] >= '0' && row[end] <= '9' || row[end] == ';') {
					end++
				}
				if end >= len(row) || row[end] != 'm' {
					t.Fatalf("row %d carries non-SGR CSI %q: %q", i, row[j:min(len(row), end+1)], row)
				}
				j = end
			}
		}
		if strings.ContainsRune(row, '\u009b') || strings.ContainsRune(row, '\u202e') {
			t.Fatalf("row %d carries a C1 CSI or bidi override: %q", i, row)
		}
	}
}

func TestMainViewNeutralizesHostileContent(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	base := len(m.entries) // NewUI may open with a logo entry
	m.entries = append(m.entries,
		entry{role: "You", content: hostile},
		entry{role: "Reasoning", content: hostile + "\n\n" + hostile},
		entry{role: "Assistant", content: "# " + hostile + "\n\n- " + hostile + "\n\n```go\n" + hostile + "\n```\n\n| " + hostile + " | b |\n|---|---|\n| c | d |"},
		entry{role: "Tool", content: ""},
		entry{role: "Tool", content: ""},
		entry{role: "Error", content: hostile},
		entry{role: "Likha", content: hostile},
		entry{role: "Queued", content: hostile},
	)
	m.thoughtExpanded = map[int]bool{0: true}
	m.toolRecords = append(m.toolRecords,
		session.ToolRecord{EntryIndex: base + 3, CallID: "c1", Name: "run_command", Arguments: `{"command":"` + strings.ReplaceAll(hostile, "\x1b", `\u001b`) + `"}`, Status: "succeeded", Content: hostile + "\n" + hostile},
		session.ToolRecord{EntryIndex: base + 4, CallID: "c2", Name: "edit", Status: "succeeded", Diff: "--- a/" + hostile + "\n+++ b/" + hostile + "\n@@ -1,1 +1,1 @@\n-" + hostile + "\n+" + hostile + "\n"},
	)
	for _, width := range []int{40, 60, 97} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 300}) // the whole transcript on one page
		m.layoutWidth = 0
		view := m.View()
		for _, want := range []string{"end", "Run(", "Updated "} {
			if !strings.Contains(stripANSI(view), want) {
				t.Fatalf("width %d: %q missing from the view:\n%s", width, want, stripANSI(view))
			}
		}
		assertTerminalSafe(t, view, width)
	}
}

// go-runewidth measures a regional-indicator flag as one cell (the width of
// its first rune), while every terminal and Bubble Tea's renderer (which
// truncates by x/ansi widths) give it two. Padding by go-runewidth alone
// made each flag row one cell too wide per flag, so Bubble Tea cut the
// scrollbar column off those rows.
func TestFlagRowsFitTheTerminal(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	for range 30 {
		m.entries = append(m.entries, entry{role: "Assistant", content: "flags 🇺🇸🇯🇵, thumbs 👍🏽, wide 日本"})
	}
	m.layoutWidth = 0
	for i, row := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(row); w != m.width {
			t.Fatalf("row %d is %d cells by Bubble Tea's measure, want %d: %q", i, w, m.width, ansi.Strip(row))
		}
	}
	if got := ansi.StringWidth(fit("🇺🇸 x", 10)); got != 10 {
		t.Fatalf("fit(flag, 10) is %d cells, want 10", got)
	}
}

// An overlay box spliced over wide text must start at its own column on
// every row. A wide rune straddling the box's left edge used to stay whole
// on the left, pushing the box one cell right on that row only, so the
// dialog border zig-zagged over CJK transcript text.
func TestSpliceRowKeepsTheBoxColumnOverWideText(t *testing.T) {
	under := strings.Repeat("日", 30) // 60 cells
	box := strings.Repeat("X", 11)
	for left := 5; left <= 8; left++ {
		got := stripANSI(spliceRow(under, left, len(box), box))
		at := strings.Index(got, box)
		if at < 0 {
			t.Fatalf("left %d: box missing from %q", left, got)
		}
		if col := runewidth.StringWidth(got[:at]); col != left {
			t.Fatalf("left %d: box starts at column %d: %q", left, col, got)
		}
		if w := runewidth.StringWidth(got); w != 60 {
			t.Fatalf("left %d: spliced row is %d cells, want 60: %q", left, w, got)
		}
	}
}

func TestInspectorsNeutralizeHostileContent(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m.entries = append(m.entries, entry{role: "Tool"})
	m.toolRecords = append(m.toolRecords, session.ToolRecord{EntryIndex: len(m.entries) - 1, CallID: "c1", Name: "mcp__s__t__d", SourceKind: "mcp", Server: hostile, SourceTool: hostile,
		Arguments: `{"q":"` + strings.ReplaceAll(hostile, "\x1b", `\u001b`) + `"}`, Status: "failed", Content: hostile, Warnings: []string{hostile}})
	m.taskRecords = append(m.taskRecords, explore.Record{ID: "t1", SessionID: m.snapshot.ID, Description: hostile, Reason: hostile, Model: hostile, Provider: hostile, Status: explore.Failed, Depth: 1})
	m.layoutWidth = 0
	m.openToolResultInspection()
	if !m.toolInspectionVisible() {
		t.Fatal("tool inspection did not open")
	}
	assertHostileShown(t, m)
	m.closeToolInspection()
	m.openAgentInspection()
	if !m.agentInspectionVisible() {
		t.Fatal("agent inspection did not open")
	}
	assertHostileShown(t, m)
	m.agentInspector.taskID, m.agentInspector.details = "t1", true
	assertHostileShown(t, m)
}

// assertHostileShown renders the current view and checks that the hostile
// fixture reached it, neutralized.
func assertHostileShown(t *testing.T, m *ui) {
	t.Helper()
	view := m.View()
	if !strings.Contains(stripANSI(view), `\u001B]0;pwned`) {
		t.Fatalf("hostile fixture not shown escaped:\n%s", stripANSI(view))
	}
	assertTerminalSafe(t, view, m.width)
}

// A tagged ask_user option padded by go-runewidth alone came out a cell too
// wide per flag, pushing the dialog's right border out on that row.
func TestAskLineWithFlagFitsItsWidth(t *testing.T) {
	line := askLine{text: "> 🇯🇵 Japan", suffix: " (Recommended)"}
	if got := ansi.StringWidth(line.render(30)); got != 30 {
		t.Fatalf("tagged option row is %d cells, want 30: %q", got, line.render(30))
	}
}
