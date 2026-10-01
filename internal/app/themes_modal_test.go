package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
)

func TestThemesModalSelection(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "default"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// A bare /themes opens the modal with the cursor on the applied theme.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open || m.dialog.kind != dialogThemes || !strings.Contains(m.View(), "Theme selection") {
		t.Fatalf("modal did not open: open=%t view=%q", m.dialog.open, m.View())
	}
	if !strings.Contains(m.View(), "> default") {
		t.Fatalf("cursor not on the applied theme: %q", m.View())
	}
	if !strings.Contains(m.View(), "everforest") {
		t.Fatalf("theme list incomplete: %q", m.View())
	}

	// Up/down move the selected state only; the applied theme is unchanged.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.dialog.cursor != 2 {
		t.Fatalf("cursor = %d after two downs, want 2", m.dialog.cursor)
	}
	if m.themeName != "default" {
		t.Fatalf("theme applied before Enter: %q", m.themeName)
	}
	if !strings.Contains(m.View(), "> habamax") {
		t.Fatalf("selected row not highlighted: %q", m.View())
	}
	// Up moves back — the setter works in both directions.
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.dialog.cursor != 1 || !strings.Contains(m.View(), "> catppuccin") {
		t.Fatalf("cursor after up = %d", m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor 2 = habamax

	// Enter applies the selected theme and closes the modal.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open {
		t.Fatal("modal did not close on Enter")
	}
	if m.themeName != "habamax" {
		t.Fatalf("applied theme = %q, want habamax", m.themeName)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Theme != "habamax" {
		t.Fatalf("stored theme: %+v %v", cfg, err)
	}

	// Reopening starts on the applied theme; Esc discards a change.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.cursor != 2 {
		t.Fatalf("cursor after reopen = %d, want 2 (habamax)", m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("Esc did not close the modal")
	}
	if m.themeName != "habamax" {
		t.Fatalf("Esc changed the applied theme to %q", m.themeName)
	}
	cfg, err = providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Theme != "habamax" {
		t.Fatalf("Esc persisted a theme: %+v %v", cfg, err)
	}
}

func TestThemesModalBlocksPromptInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("modal did not open")
	}
	// Typing filters the modal rows; the draft stays untouched.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	if len(m.input) != 0 {
		t.Fatalf("prompt input leaked while modal open: %q", string(m.input))
	}
	if m.dialog.query != "hello" || m.dialog.cursor != 0 {
		t.Fatalf("query not applied: q=%q cursor=%d", m.dialog.query, m.dialog.cursor)
	}
	// First Esc clears the query, the second closes.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dialog.open || m.dialog.query != "" {
		t.Fatalf("Esc did not clear the query: open=%t q=%q", m.dialog.open, m.dialog.query)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open || len(m.input) != 0 {
		t.Fatalf("modal close state wrong: open=%t input=%q", m.dialog.open, string(m.input))
	}
}
