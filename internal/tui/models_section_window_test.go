package tui

// Regression for the /providers-switch report ("dialagram section title
// removed" with both providers' models visible): a short-viewport /models
// open clips the dialog window, so the test parks the cursor on the last
// row of the second section and requires every visible model row to carry
// its own section's header directly above it.

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
)

func modelsRowProvider(t *testing.T, m *ui, id string) (model.Provider, bool) {
	t.Helper()
	for _, r := range m.dialogModelRows {
		if r.model == id {
			return r.provider, true
		}
	}
	return model.Provider{}, false
}

func TestModelsDialogShortViewportKeepsSections(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "openrouter") {
			return []string{"router-a", "router-b", "router-c", "router-d"}, nil
		}
		return []string{"o-a", "o-b", "o-c", "o-d"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "o-a", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-a", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runModelsCmds(t, m, cmd)
	if len(m.dialogMatches()) != 8 {
		t.Fatalf("matches = %d, want 8", len(m.dialogMatches()))
	}
	// Park the cursor on the last row of the second section: the window
	// clips the top, exactly the reported shape (one title scrolled away
	// while both providers' models are reachable).
	for range m.dialogMatches() {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	view := stripANSI(m.View())
	lines := strings.Split(view, "\n")
	for _, id := range []string{"router-a", "router-b", "router-c", "router-d", "o-a", "o-b", "o-c", "o-d"} {
		p, ok := modelsRowProvider(t, m, id)
		if !ok {
			t.Fatalf("row %q missing from dialog rows", id)
		}
		var rowLine int = -1
		for i, line := range lines {
			if strings.Contains(line, "  "+id+" ") || strings.Contains(line, "  "+id+" (") || strings.HasSuffix(strings.TrimRight(line, " │"), "  "+id) {
				rowLine = i
				break
			}
		}
		if rowLine < 0 {
			continue // scrolled out: fine, must not appear misattributed
		}
		found := false
		for j := rowLine - 1; j >= 0 && j >= rowLine-6; j-- {
			plain := strings.TrimSpace(strings.Trim(strings.TrimSpace(stripANSI(lines[j])), "│ "))
			if plain == p.DisplayName {
				found = true
				break
			}
			if strings.Contains(lines[j], "Model selection") {
				break
			}
		}
		if !found {
			t.Fatalf("row %q visible without its %q header: %q", id, p.DisplayName, view)
		}
	}
	// The full-height dialog still shows both section headers.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	full := stripANSI(m.View())
	if !strings.Contains(full, "OpenRouter") || !strings.Contains(full, "OpenAI") {
		t.Fatalf("section headers missing at full height: %q", full)
	}
}
