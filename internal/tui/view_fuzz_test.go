package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/providers"
	"likha/internal/session"
)

// FuzzMainView renders arbitrary transcript text in every prose role and as
// tool output, and checks the frame: exactly height rows, none wider than
// the terminal, and no escape other than SGR.
func FuzzMainView(f *testing.F) {
	for _, s := range []string{"hello", hostile, "# h\n\n```go\nx\n```\n| a | b |\n|-|-|\n|1|2|", strings.Repeat("word ", 200), "thumbs 👍🏽 日本", "\t\tx\r\ny"} {
		f.Add(s, uint8(40), uint8(12))
		f.Add(s, uint8(83), uint8(30))
	}
	// NewUI costs ~20 ms; one model per fuzz worker keeps the fuzzer fast.
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, f.TempDir(), nil, session.Snapshot{})
	f.Fuzz(func(t *testing.T, text string, w, h uint8) {
		width, height := 40+int(w)%100, 12+int(h)%40
		// A lone regional indicator is one cell to go-runewidth and every
		// layout helper but two to x/ansi, a known limit; paired flags are
		// covered by TestFlagRowsFitTheTerminal.
		text = strings.Map(func(r rune) rune {
			if r >= 0x1F1E6 && r <= 0x1F1FF {
				return -1
			}
			return r
		}, text)
		m.Update(tea.WindowSizeMsg{Width: width, Height: height})
		m.entries = append(m.entries[:0],
			entry{role: "You", content: text},
			entry{role: "Reasoning", content: text},
			entry{role: "Assistant", content: text},
			entry{role: "Tool"},
			entry{role: "Error", content: text},
			entry{role: "Queued", content: text},
		)
		m.thoughtExpanded = map[int]bool{0: true}
		m.toolRecords = append(m.toolRecords[:0], session.ToolRecord{EntryIndex: 3, CallID: "c1", Name: "run_command", Arguments: `{"command":"ls"}`, Status: "succeeded", Content: text})
		m.layoutWidth = 0
		view := m.View()
		if rows := strings.Count(view, "\n") + 1; rows != height {
			t.Fatalf("%dx%d: view has %d rows", width, height, rows)
		}
		assertTerminalSafe(t, view, width)
	})
}
