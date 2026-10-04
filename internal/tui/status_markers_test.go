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

// The unverified warning (v1 spec §3, predefined-providers spec 4): a custom
// endpoint outside the accepted list shows a visible "(unverified)" marker in
// the status identity; a listed provider never does.
func TestStatusIdentityShowsUnverifiedWarning(t *testing.T) {
	custom := func(verified bool) *ui {
		m := NewUI("/sample", nil, nil, "local-model", providers.Connection{Provider: "Custom endpoint", Verified: verified}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
		return m
	}
	if row := stripANSI(custom(false).statusLineRows(1, 1)[0]); !strings.Contains(row, "Custom endpoint (unverified)") {
		t.Fatalf("unverified endpoint warning missing: %q", row)
	}
	if row := stripANSI(custom(true).statusLineRows(1, 1)[0]); strings.Contains(row, "unverified") {
		t.Fatalf("listed provider marked unverified: %q", row)
	}
}

// Nerd Font markers (FR-15, themes spec): with the opt-in the status and
// review markers swap to icon glyphs; without it the plain text carries no
// marker at all.
func TestNerdMarkersOnlyWithOptIn(t *testing.T) {
	clock := likhaui.NerdGlyphs().Waiting
	pencil := likhaui.NerdGlyphs().Review
	nerd := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true, Nerd: true}, t.TempDir(), nil, session.Snapshot{})
	nerd.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	nerd.status = "Waiting for model"
	hints := nerd.statusHints(1, 1, false)
	if !strings.Contains(hints[0], clock+" Waiting for model") {
		t.Fatalf("nerd waiting marker missing: %q", hints[0])
	}
	plain := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	plain.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain.status = "Waiting for model"
	hints = plain.statusHints(1, 1, false)
	if !strings.Contains(hints[0], "Waiting for model") || strings.Contains(hints[0], clock) {
		t.Fatalf("plain output changed or carries a nerd marker: %q", hints[0])
	}
	// Review markers: the pencil prefixes the review hint while pending.
	request := &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "b", Reply: make(chan bool, 1)}
	nerd.pending = request
	nerd.status = "Review command before approval"
	hints = nerd.statusHints(1, 1, false)
	if !strings.Contains(hints[0], pencil+" Command review") {
		t.Fatalf("nerd review marker missing: %q", hints[0])
	}
}

// Error entries are the one colored prose role (themes spec): the rebuilt
// "✗" line carries the theme's Error style while "ℹ" notices stay muted.
func TestErrorEntriesRenderThemeError(t *testing.T) {
	for _, name := range likhaui.ThemeNames() {
		theme := likhaui.Resolve(name, true)
		m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m.theme = theme
		m.entries = append(m.entries, entry{role: "Error", content: "startup connection check failed"}, entry{role: "Likha", content: "plain prose"})
		m.layoutWidth = 0
		forceANSI(t)
		_ = m.View()
		var errIdx, noticeIdx = -1, -1
		for i, line := range m.lines {
			switch {
			case strings.HasPrefix(line, "✗ "):
				errIdx = i
			case strings.HasPrefix(line, "ℹ "):
				noticeIdx = i
			}
		}
		if errIdx < 0 || noticeIdx < 0 {
			t.Fatalf("theme %s: layout missing blocks: %q", name, m.lines)
		}
		if got := m.lineStyles[errIdx]; colorName(got.GetForeground()) != colorName(theme.Error.GetForeground()) {
			t.Fatalf("theme %s: Error style %v, want theme Error %v", name, got, theme.Error)
		}
		// Likha notices read muted under their own glyph, never in Error.
		if got := m.lineStyles[noticeIdx]; colorName(got.GetForeground()) != colorName(theme.Muted.GetForeground()) {
			t.Fatalf("theme %s: notice style %v, want Muted", name, got)
		}
	}
}
