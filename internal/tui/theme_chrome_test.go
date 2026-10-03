package tui

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
)

func TestThemeBackgroundFillsChrome(t *testing.T) {
	forceANSI(t)
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "horizon"}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	composer := m.composerLines()
	if len(composer) < 2 || !hasANSIBackground(composer[1]) {
		t.Errorf("composer separator must carry the theme canvas background: %q", composer[1])
	}

	for i := 0; i < 50; i++ {
		m.entries = append(m.entries, entry{role: "Likha", content: "long transcript row"})
	}
	m.layoutWidth = 0
	m.rebuild()
	var track string
	for _, row := range m.scrollbarRows(m.bodyHeight()) {
		if stripANSI(row) == "│" {
			track = row
			break
		}
	}
	if track == "" {
		t.Fatal("test setup did not produce a scrollbar track")
	}
	if !hasANSIBackground(track) {
		t.Errorf("scrollbar track must carry the theme canvas background: %q", track)
	}

	m.mention.open = true
	m.mention.matches = []string{"internal/tui/view.go"}
	assertThemeBackgroundCoversView(t, m.mainView(), m.width)

	m.mention.open = false
	m.commandPopup.open = true
	m.commandPopup.matches = []commandItem{{Name: "themes", Description: "list or apply a color theme"}}
	assertThemeBackgroundCoversView(t, m.mainView(), m.width)
	m.commandPopup.open = false

	for _, style := range composerStyles {
		m.composerStyle = style
		assertThemeBackgroundCoversView(t, m.mainView(), m.width)
	}

	m.pending = &agent.ApprovalRequest{Kind: "edit"}
	m.reviewSeen = []bool{false}
	m.reviewFocus = focusApprove
	assertThemeBackgroundCoversView(t, m.mainView(), m.width)
	m.pending = nil

	m.dialog.open = true
	m.dialog.kind = dialogThemes
	m.dialogItems = []string{"default", "horizon"}
	assertThemeBackgroundCoversView(t, m.dialogView(), m.width)
	m.dialog = dialogState{}

	m.mode = modeSetup
	assertThemeBackgroundCoversView(t, m.setupView(), m.width)

	narrow := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "horizon"}, t.TempDir(), nil, session.Snapshot{})
	narrow.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	narrow.entries = append(narrow.entries, entry{role: "Likha", content: "narrow header row"})
	narrow.layoutWidth = 0
	assertThemeBackgroundCoversView(t, narrow.mainView(), narrow.width)
}

func hasANSIBackground(rendered string) bool {
	coverage := backgroundCoverage(rendered)
	if len(coverage) == 0 {
		return false
	}
	for _, covered := range coverage {
		if !covered {
			return false
		}
	}
	return true
}

func assertThemeBackgroundCoversView(t *testing.T, view string, width int) {
	t.Helper()
	for rowIndex, row := range strings.Split(view, "\n") {
		coverage := backgroundCoverage(row)
		if len(coverage) != width {
			t.Errorf("row %d has %d cells, want %d: %q", rowIndex, len(coverage), width, stripANSI(row))
			continue
		}
		for column, covered := range coverage {
			if !covered {
				t.Errorf("row %d, column %d is missing its theme background: %q", rowIndex, column, stripANSI(row))
				break
			}
		}
	}
}

func backgroundCoverage(rendered string) []bool {
	var coverage []bool
	background := false
	for i := 0; i < len(rendered); {
		if rendered[i] == '\x1b' && i+1 < len(rendered) && rendered[i+1] == '[' {
			end := strings.IndexByte(rendered[i+2:], 'm')
			if end < 0 {
				break
			}
			end += i + 2
			params := strings.Split(rendered[i+2:end], ";")
			for j := 0; j < len(params); j++ {
				code := params[j]
				switch code {
				case "", "0", "49":
					background = false
				case "48":
					background = true
					if j+1 < len(params) && params[j+1] == "2" {
						j += 4
					} else if j+1 < len(params) && params[j+1] == "5" {
						j += 2
					}
				default:
					n, err := strconv.Atoi(code)
					if err == nil && ((n >= 40 && n <= 47) || (n >= 100 && n <= 107)) {
						background = true
					}
				}
			}
			i = end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(rendered[i:])
		cells := runewidth.RuneWidth(r)
		for range cells {
			coverage = append(coverage, background)
		}
		i += size
	}
	return coverage
}
