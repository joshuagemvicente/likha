package tui

// Regression for the /providers-switch report ("dialagram section title
// removed" with both providers' models visible): a short-viewport /models
// open clips the dialog window, so the test parks the cursor on the last
// row of the second section and requires every visible model row to carry
// its own section's header directly above it.

import (
	"context"
	"fmt"
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

// TestModelsDialogReopenAfterCrossProviderPick is the user-reported flow:
// live on opencode-go, /models, Enter on a dialagram row (cross-provider
// switch), then /models again. The reopen must keep both section titles —
// Dialagram first (now the active provider) — with the cursor on the live
// pair, through the cached instant render and the background refresh, and
// both providers must stay split even when both lists report identical
// ids (proxies re-list upstream names).
func TestModelsDialogReopenAfterCrossProviderPick(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "opencode-go", "k-go")
	storedProviderKey(t, stateDir, "dialagram", "k-dia")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		switch {
		case strings.Contains(base, "opencode.ai/zen/go"):
			return []string{"go-m1", "go-m2"}, nil
		case strings.Contains(base, "dialagram.me"):
			return []string{"dia-m1", "dia-m2"}, nil
		default:
			return nil, fmt.Errorf("unexpected base %q", base)
		}
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://opencode.ai/zen/go/v1", "go-m1", "k-go")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "go-m1", providers.Connection{Provider: "Opencode Go", ProviderCanonical: "opencode-go", Verified: true}, stateDir)

	// Open on opencode-go: both sections in predefined order.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runModelsCmds(t, m, cmd)
	view := stripANSI(m.View())
	if !strings.Contains(view, "Opencode Go") || !strings.Contains(view, "Dialagram") {
		t.Fatalf("first open lost a section title: %q", view)
	}
	// Two downs land on dia-m1 (rows: go-m1, go-m2, dia-m1, dia-m2).
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open {
		t.Fatal("dialog did not close on the cross-provider pick")
	}
	if m.conn.ProviderCanonical != "dialagram" || m.modelName != "dia-m1" {
		t.Fatalf("pick landed on %q/%q, want dialagram/dia-m1", m.conn.ProviderCanonical, m.modelName)
	}

	// Reopen on dialagram: Dialagram first, both titles, cursor on the
	// live pair — instantly (fresh cache) and after the refresh.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, reopen := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	instant := stripANSI(m.View())
	if strings.Contains(instant, "Fetching model list") {
		t.Fatalf("fresh cache did not serve instantly: %q", instant)
	}
	if !strings.Contains(instant, "> dia-m1 (current)") {
		t.Fatalf("instant render missed the live pair: %q", instant)
	}
	runModelsCmds(t, m, reopen)
	after := stripANSI(m.View())
	if strings.Index(after, "Dialagram") > strings.Index(after, "Opencode Go") {
		t.Fatalf("active section not first after reopen: %q", after)
	}
	if !strings.Contains(after, "Opencode Go") || !strings.Contains(after, "Dialagram") {
		t.Fatalf("reopen merged sections: %q", after)
	}

	// Same ids from both endpoints: the sections must still split.
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		return []string{"shared-x", "shared-y"}, nil
	}
	m.modelsCache = modelsCache{}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, sameCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runModelsCmds(t, m, sameCmd)
	same := stripANSI(m.View())
	var lastHeader string
	split := false
	for _, line := range strings.Split(same, "\n") {
		first := strings.Index(line, "│")
		last := strings.LastIndex(line, "│")
		if first < 0 || last <= first {
			continue
		}
		plain := strings.TrimSpace(stripANSI(strings.TrimSuffix(strings.TrimPrefix(line[first:last], "│"), "│")))
		if plain == "Dialagram" || plain == "Opencode Go" {
			lastHeader = plain
		}
		if strings.Contains(plain, "shared-x") && lastHeader != "" {
			split = true
		}
	}
	if !split || strings.Count(same, "shared-x") < 2 {
		t.Fatalf("identical ids merged sections: %q", same)
	}
}

// TestModelsDialogClippedWindowPinsSectionTitle verifies the sticky pin:
// with the cursor parked mid-section on a short terminal, the section
// title clips at the window top, and the dialog must pin that section's
// title above its rows — the previous section's title must not claim them.
func TestModelsDialogClippedWindowPinsSectionTitle(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "opencode-go", "k-go")
	storedProviderKey(t, stateDir, "dialagram", "k-dia")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "opencode.ai/zen/go") {
			return []string{"go-m1", "go-m2", "go-m3", "go-m4", "go-m5", "go-m6"}, nil
		}
		return []string{"dia-m1", "dia-m2", "dia-m3", "dia-m4", "dia-m5", "dia-m6"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://opencode.ai/zen/go/v1", "go-m1", "k-go")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "go-m1", providers.Connection{Provider: "Opencode Go", ProviderCanonical: "opencode-go", Verified: true}, stateDir)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runModelsCmds(t, m, cmd)

	// Cursor to the middle of the dialagram section (matches 6-11, after
	// the six Opencode Go rows; 9 downs = dia-m4): the window then clips
	// the Dialagram title at the top.
	for range 9 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	view := stripANSI(m.View())
	var header, title string
	seenDial := false
	for _, line := range strings.Split(view, "\n") {
		first := strings.Index(line, "│")
		last := strings.LastIndex(line, "│")
		if first < 0 || last <= first {
			continue
		}
		plain := strings.TrimSpace(stripANSI(strings.TrimSuffix(strings.TrimPrefix(line[first:last], "│"), "│")))
		if plain == "Dialagram" || plain == "Opencode Go" {
			header = plain
		}
		if strings.Contains(plain, "dia-m") {
			seenDial = true
			title = header
		}
	}
	if !seenDial {
		t.Fatalf("no dialagram rows visible mid-section: %q", view)
	}
	if title != "Dialagram" {
		t.Fatalf("dialagram rows render under %q title with the window clipped:\n%s", title, view)
	}
}
