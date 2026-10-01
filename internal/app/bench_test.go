package app

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/providers"
	"lisa/internal/session"
)

// benchUI builds a UI with a realistic long transcript: assistant prose,
// tool activity and reasoning entries, all the segments a real session shows.
func benchUI(entries int, width, height int) *ui {
	opts := providers.StoredStatusLineConfig{
		Changes: true,
		Staged:  true,
		MCP:     true,
		Session: true,
		Minutes: true,
		Tokens:  true,
		Version: true,
		Update:  true,
	}
	m := newUI("/sample/repo", nil, nil, "gpt-5.3-codex",
		providers.Connection{Provider: "Opencode Zen", Verified: true, Theme: "gruvbox", StatusLine: opts},
		"", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	paragraph := strings.Repeat("The repository walk opens each path component by descriptor, so a concurrent symlink substitution cannot escape the root. ", 6)
	for i := range entries {
		switch i % 4 {
		case 0:
			m.entries = append(m.entries, entry{role: "You", content: fmt.Sprintf("question %d: %s", i, paragraph[:200])})
		case 1:
			m.entries = append(m.entries, entry{role: "Assistant", content: paragraph})
		case 2:
			m.entries = append(m.entries, entry{role: "Tool", content: "read: " + paragraph[:400]})
		default:
			m.entries = append(m.entries, entry{role: "Reasoning", content: paragraph[:300]})
		}
	}
	m.usageSeen = true
	m.lastPromptTokens = 68_000
	m.spendKnown = true
	m.spend = 0.42
	m.git = gitState{Branch: "main", Ahead: 2, Dirty: 7, Staged: 1, Untracked: 3}
	m.gitOK = true
	return m
}

// invalidate is what every content change does before the next frame.
func invalidate(m *ui) { m.layoutWidth = 0 }

// BenchmarkViewSteady is a frame with no content change: the layout cache is
// warm, so this measures the pure per-frame cost bubbletea pays on every
// caret blink and every redraw.
func BenchmarkViewSteady(b *testing.B) {
	m := benchUI(400, 120, 40)
	m.View()
	b.ResetTimer()
	for range b.N {
		m.View()
	}
}

// BenchmarkViewStreaming is one streaming token delta: content changed, so
// every frame re-wraps the whole transcript.
func BenchmarkViewStreaming(b *testing.B) {
	m := benchUI(400, 120, 40)
	m.View()
	b.ResetTimer()
	for range b.N {
		invalidate(m)
		m.entries[1].content += "tok"
		m.View()
	}
}

// BenchmarkRebuild isolates the transcript layout pass (wrap + style index).
func BenchmarkRebuild(b *testing.B) {
	m := benchUI(400, 120, 40)
	m.rebuild()
	b.ResetTimer()
	for range b.N {
		invalidate(m)
		m.rebuild()
	}
}

// BenchmarkRebuildScaling shows the layout pass cost against transcript size,
// which is the axis a long session grows along.
func BenchmarkRebuildScaling(b *testing.B) {
	for _, size := range []int{200, 800, 3200} {
		b.Run(fmt.Sprintf("entries=%d", size), func(b *testing.B) {
			m := benchUI(size, 120, 40)
			m.rebuild()
			b.ResetTimer()
			for range b.N {
				invalidate(m)
				m.rebuild()
			}
		})
	}
}

// BenchmarkStatusLine isolates the status row measurement, which asks the
// identity fit repeatedly and re-measures every segment per attempt.
func BenchmarkStatusLine(b *testing.B) {
	m := benchUI(400, 120, 40)
	b.ResetTimer()
	for range b.N {
		m.statusLineRows(1, 40)
	}
}

// BenchmarkWrap measures the wrap helper alone on one long entry.
func BenchmarkWrap(b *testing.B) {
	paragraph := strings.Repeat("The repository walk opens each path component by descriptor. ", 40)
	for range b.N {
		wrap(paragraph, 118)
	}
}

// BenchmarkFit measures the pad/clip helper on one viewport row.
func BenchmarkFit(b *testing.B) {
	row := strings.Repeat("assistant content line ", 7)
	for range b.N {
		fit(row, 120)
	}
}
