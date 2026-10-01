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

// The unverified warning (v1 spec §3, predefined-providers spec 4): a custom
// endpoint outside the accepted list shows a visible "(unverified)" marker in
// the status identity; a listed provider never does.
func TestStatusIdentityShowsUnverifiedWarning(t *testing.T) {
	custom := func(verified bool) *ui {
		m := newUI("/sample", nil, nil, "local-model", providers.Connection{Provider: "Custom endpoint", Verified: verified}, t.TempDir(), nil, session.Snapshot{})
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
	clock := lisaui.NerdGlyphs().Waiting
	pencil := lisaui.NerdGlyphs().Review
	nerd := newUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true, Nerd: true}, t.TempDir(), nil, session.Snapshot{})
	nerd.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	nerd.status = "Waiting for model"
	hints := nerd.statusHints(1, 1, false)
	if !strings.Contains(hints[0], clock+" Waiting for model") {
		t.Fatalf("nerd waiting marker missing: %q", hints[0])
	}
	plain := newUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	plain.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain.status = "Waiting for model"
	hints = plain.statusHints(1, 1, false)
	if !strings.Contains(hints[0], "Waiting for model") || strings.Contains(hints[0], clock) {
		t.Fatalf("plain output changed or carries a nerd marker: %q", hints[0])
	}
	// Review markers: the pencil prefixes the review hint while pending.
	request := &approvalRequest{Kind: "command", Title: "Shell command", Body: "b", Reply: make(chan bool, 1)}
	nerd.pending = request
	nerd.status = "Review command before approval"
	hints = nerd.statusHints(1, 1, false)
	if !strings.Contains(hints[0], pencil+" Command review") {
		t.Fatalf("nerd review marker missing: %q", hints[0])
	}
}

// Error entries are the one colored prose role (themes spec): the rebuilt
// Error line carries the theme's Error style while ordinary roles stay plain.
func TestErrorEntriesRenderThemeError(t *testing.T) {
	for _, name := range lisaui.ThemeNames() {
		theme := lisaui.Resolve(name, true)
		m := newUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m.theme = theme
		m.entries = append(m.entries, entry{role: "Error", content: "startup connection check failed"}, entry{role: "Lisa", content: "plain prose"})
		m.layoutWidth = 0
		forceANSI(t)
		_ = m.View()
		plain := lipgloss.NewStyle()
		var errIdx, plainIdx = -1, -1
		for i, line := range m.lines {
			switch {
			case strings.HasPrefix(line, "Error:"):
				errIdx = i
			case strings.HasPrefix(line, "Lisa:"):
				plainIdx = i
			}
		}
		if errIdx < 0 || plainIdx < 0 {
			t.Fatalf("theme %s: layout missing roles: %q", name, m.lines)
		}
		if got := m.lineStyles[errIdx]; got.Value() != theme.Error.Value() {
			t.Fatalf("theme %s: Error style %v, want theme Error %v", name, got, theme.Error)
		}
		if got := m.lineStyles[plainIdx]; got.Value() != plain.Value() {
			t.Fatalf("theme %s: prose style %v, want plain", name, got)
		}
	}
}
