package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"likha/internal/providers"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

func TestThemeBackgroundFollowsPalette(t *testing.T) {
	for _, name := range likhaui.ThemeNames() {
		theme := likhaui.Resolve(name, true)
		m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: name}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		forceANSI(t)
		view := m.View()
		bg := theme.BaseBG()
		if name == "default" {
			if bg != "" {
				t.Fatalf("default theme must keep the terminal default, got %q", bg)
			}
			if strings.Contains(view, "48;2") {
				t.Fatalf("default view must not paint a background: %q", view[:120])
			}
			continue
		}
		if bg == "" {
			t.Fatalf("theme %s: missing canvas background", name)
		}
		if !strings.Contains(view, "48;2") {
			t.Fatalf("theme %s (%s): main view carries no background fill", name, bg)
		}
	}
}

func TestThemePreviewAndEscRestore(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "default"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("dialog did not open")
	}
	committedBG := m.theme.BaseBG()
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.themeName != "default" {
		t.Fatalf("preview wrote committed name: %q", m.themeName)
	}
	if m.theme.BaseBG() == committedBG {
		t.Fatalf("arrow down did not preview a new background")
	}
	cfg, _ := providers.LoadStoredConfig(stateDir)
	if cfg.Theme == "catppuccin" {
		t.Fatal("preview persisted to config")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open || m.themeName != "default" || m.theme.BaseBG() != committedBG {
		t.Fatalf("esc did not restore: open=%v name=%q bg=%q", m.dialog.open, m.themeName, m.theme.BaseBG())
	}
	cfg, _ = providers.LoadStoredConfig(stateDir)
	if cfg.Theme == "catppuccin" {
		t.Fatal("esc persisted a theme")
	}
}
