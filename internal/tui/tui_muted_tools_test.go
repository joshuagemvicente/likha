package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

// Muted tool entries (specs/tool-rendering-terminal-keys M1, glyphs per
// transcript-redesign): a Tool entry lays out muted like Reasoning, each
// under its own block glyph instead of a role label.

func newMutedTestUI(t *testing.T) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
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
	m.reasoningStream = -1 // as submit arms it: no reasoning stream open yet
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "tool_start", Text: "reading internal/app/tui.go"})
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "tool_result", Text: "read 120 lines"})

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

	// Every Tool entry's first row carries the tool glyph and its paired
	// style is Muted on the canvas, mirroring the Reasoning branch.
	found := 0
	for i, e := range m.entries {
		if e.role != "Tool" {
			continue
		}
		at := m.entryLines[i]
		line := m.lines[at]
		if !strings.HasPrefix(line, m.blocks.ToolHeader+" ") {
			t.Fatalf("Tool line %q lacks the %q glyph", line, m.blocks.ToolHeader)
		}
		if got := m.lineStyles[at]; colorName(got.GetForeground()) != colorName(muted.GetForeground()) {
			t.Fatalf("Tool line %q styled %v, want Muted %v", line, got, muted)
		}
		found++
	}
	if found == 0 {
		t.Fatalf("no Tool line laid out: %v", m.lines)
	}
	// Reasoning stays muted too, under its own glyph.
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "reasoning", Text: "thinking"})
	m.rebuild()
	reasoning := false
	for i, line := range m.lines {
		if strings.HasPrefix(line, m.blocks.Thought+" ") {
			reasoning = true
			if style := m.lineStyles[i]; colorName(style.GetForeground()) != colorName(muted.GetForeground()) {
				t.Fatalf("Reasoning line %q not muted: %v", line, style)
			}
		}
		for _, label := range []string{"Tool:", "Reasoning:"} {
			if strings.HasPrefix(line, label) {
				t.Fatalf("role label %q still laid out: %q", label, line)
			}
		}
	}
	if !reasoning {
		t.Fatalf("no Reasoning line laid out: %v", m.lines)
	}
}

// TestToolEntriesMutedAcrossEveryTheme renders a Tool entry under each
// predefined theme (dark variant) and asserts the muted role carries it and
// the render is non-empty — no hardcoded color path in the transcript.
func TestToolEntriesMutedAcrossThemes(t *testing.T) {
	for _, name := range likhaui.ThemeNames() {
		theme := likhaui.Resolve(name, true)
		m := newMutedTestUI(t)
		m.theme = theme
		m.themeName = name
		m.entries = append(m.entries, entry{role: "Tool", content: "listing files"})
		m.layoutWidth = 0
		view := m.View()
		if !strings.Contains(view, "⏺ listing files") {
			t.Fatalf("theme %s: tool entry not visible: %q", name, view)
		}
		// The rebuilt Tool line carries the theme's own Muted style.
		var toolIdx = -1
		for i, line := range m.lines {
			if line == "⏺ listing files" {
				toolIdx = i
				break
			}
		}
		if toolIdx < 0 {
			t.Fatalf("theme %s: Tool line missing from layout", name)
		}
		if got := m.lineStyles[toolIdx]; colorName(got.GetForeground()) != colorName(theme.Muted.GetForeground()) {
			t.Fatalf("theme %s: Tool style %v, want theme Muted %v", name, got, theme.Muted)
		}
	}
}
