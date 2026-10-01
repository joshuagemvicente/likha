package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lisa/internal/providers"
	"lisa/internal/session"
	lisaui "lisa/internal/ui"
)

// Muted tool entries (specs/tool-rendering-terminal-keys M1): a Tool entry
// lays out exactly like Reasoning — same Muted role, no other decoration —
// while every other role keeps the plain style.

func newMutedTestUI(t *testing.T) *ui {
	t.Helper()
	m := newUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// TestToolEntriesRenderMutedLikeReasoning drives a live turn's tool events
// through Update and asserts the rebuilt layout carries the theme's Muted
// style for the Tool lines, aligned with m.lines.
func TestToolEntriesRenderMutedLikeReasoning(t *testing.T) {
	m := newMutedTestUI(t)
	// Arm the turn machinery directly: tool events are gated on
	// m.working && matching runID, and the test drives events, not streams.
	m.working = true
	m.runID++
	m.Update(turnEvent{runID: m.runID, kind: "tool_start", text: "reading internal/app/tui.go"})
	m.Update(turnEvent{runID: m.runID, kind: "tool_result", text: "read 120 lines"})

	toolIdx := -1
	for i, e := range m.entries {
		if e.role == "Tool" {
			toolIdx = i
			break
		}
	}
	if toolIdx < 0 {
		t.Fatalf("no Tool entry recorded: %+v", m.entries)
	}
	if m.layoutWidth != m.width {
		m.rebuild()
	}
	muted := m.theme.Muted
	plain := lipgloss.NewStyle()

	// Find the "Tool:" line in m.lines and require its paired style to be
	// Muted, mirroring the Reasoning branch.
	found := false
	for i, line := range m.lines {
		if !strings.HasPrefix(line, "Tool:") {
			continue
		}
		found = true
		if got := m.lineStyles[i]; got.Value() != muted.Value() {
			t.Fatalf("Tool line %q styled %v, want Muted %v", line, got, muted)
		}
	}
	if !found {
		t.Fatalf("no Tool line laid out: %v", m.lines)
	}
	// Reasoning stays muted too, and plain roles stay plain.
	m.Update(turnEvent{runID: m.runID, kind: "reasoning", text: "thinking"})
	m.rebuild()
	for i, line := range m.lines {
		style := m.lineStyles[i]
		switch {
		case strings.HasPrefix(line, "Reasoning:"):
			if style.Value() != muted.Value() {
				t.Fatalf("Reasoning line %q not muted: %v", line, style)
			}
		case strings.HasPrefix(line, "You:"), strings.HasPrefix(line, "Assistant:"):
			if style.Value() != plain.Value() {
				t.Fatalf("role line %q changed style: %v", line, style)
			}
		}
	}
}

// TestToolEntriesMutedAcrossEveryTheme renders a Tool entry under each
// predefined theme (dark variant) and asserts the muted role carries it and
// the render is non-empty — no hardcoded color path in the transcript.
func TestToolEntriesMutedAcrossThemes(t *testing.T) {
	for _, name := range lisaui.ThemeNames() {
		theme := lisaui.Resolve(name, true)
		m := newMutedTestUI(t)
		m.theme = theme
		m.themeName = name
		m.entries = append(m.entries, entry{role: "Tool", content: "listing files"})
		m.layoutWidth = 0
		view := m.View()
		if !strings.Contains(view, "Tool: listing files") {
			t.Fatalf("theme %s: tool entry not visible: %q", name, view)
		}
		// The rebuilt Tool line carries the theme's own Muted style.
		var toolIdx = -1
		for i, line := range m.lines {
			if strings.HasPrefix(line, "Tool:") {
				toolIdx = i
				break
			}
		}
		if toolIdx < 0 {
			t.Fatalf("theme %s: Tool line missing from layout", name)
		}
		if got := m.lineStyles[toolIdx]; got.Value() != theme.Muted.Value() {
			t.Fatalf("theme %s: Tool style %v, want theme Muted %v", name, got, theme.Muted)
		}
	}
}
