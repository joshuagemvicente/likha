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

// TestModelsDialogSharedIdsDisambiguate covers the router-proxy shape the
// user hit: the active provider (dialagram) re-lists its upstream's
// models, so identical ids appear under two sections. Each colliding row
// renders `id · Provider` so the duplicates read as honest per-provider
// listings, while unique ids and the (current) marker keep today's labels.
func TestModelsDialogSharedIdsDisambiguate(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "opencode-go", "k-go")
	storedProviderKey(t, stateDir, "dialagram", "k-dia")
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "dialagram", Model: "nexum-router"}); err != nil {
		t.Fatal(err)
	}
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		switch {
		case strings.Contains(base, "opencode.ai/zen/go"):
			return []string{"go-alpha", "go-beta", "go-gamma"}, nil
		case strings.Contains(base, "dialagram.me"):
			// The router's list carries its own model plus the upstream
			// ids — the reported duplicate shape.
			return []string{"nexum-router", "go-alpha", "go-beta", "go-gamma"}, nil
		default:
			return nil, fmt.Errorf("unexpected base %q", base)
		}
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://dialagram.me/router/v1", "nexum-router", "k-dia")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "nexum-router", providers.Connection{Provider: "Dialagram", ProviderCanonical: "dialagram", Verified: true}, stateDir)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runModelsCmds(t, m, cmd)
	view := stripANSI(m.View())
	if !strings.Contains(view, "go-alpha · Dialagram") || !strings.Contains(view, "go-alpha · Opencode Go") {
		t.Fatalf("colliding ids not labeled per provider: %q", view)
	}
	if !strings.Contains(view, "nexum-router (current)") {
		t.Fatalf("unique id lost its bare label or live marker: %q", view)
	}
	if countHeaderLines(view, "Dialagram") != 1 || countHeaderLines(view, "Opencode Go") != 1 {
		t.Fatalf("section headers wrong: %q", view)
	}
}

// TestModelsDialogLongListsPinHeader is the screenshot shape: both providers
// list 30+ models, the terminal is ~40 rows tall, and the cursor sits
// roughly 15 rows into the second (OpenRouter) section — the OpenAI title
// has scrolled away and the OpenRouter title may clip at the window top.
// Every visible model row must carry its own section's title: the sticky
// pin supplies the OpenRouter title above its rows, the row list carries
// the OpenAI title above its rows or those rows scroll out together, and
// no row ever renders under the other provider's title.
func TestModelsDialogLongListsPinHeader(t *testing.T) {
	const listLen = 30
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "openrouter") {
			return makeIds("router-", listLen), nil
		}
		return makeIds("o-", listLen), nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "o-1", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-1", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runModelsCmds(t, m, cmd)
	if len(m.dialogMatches()) != 2*listLen {
		t.Fatalf("matches = %d, want %d", len(m.dialogMatches()), 2*listLen)
	}
	// Walk the cursor ~15 rows past the OpenRouter section header: 30
	// OpenAI rows first, then 15 into the router list.
	for range listLen + 15 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	view := stripANSI(m.View())
	lines := strings.Split(view, "\n")

	// Slice the centered dialog box out of the full-screen render, so
	// dimmed background text can never influence the assertions (the
	// │ slice pattern used by TestModelsDialogSharedIdsDisambiguate):
	// anchor on the ╭/╰ border lines and keep only the rows between them.
	var boxLines []string
	inBox := false
	for _, line := range lines {
		switch {
		case strings.Contains(line, "╭") && strings.Contains(line, "╮"):
			inBox = true
		case inBox && strings.Contains(line, "╰") && strings.Contains(line, "╯"):
			inBox = false
		case inBox:
			first := strings.Index(line, "│")
			last := strings.LastIndex(line, "│")
			if first < 0 || last <= first {
				continue
			}
			plain := strings.TrimSpace(stripANSI(strings.TrimSuffix(strings.TrimPrefix(line[first:last], "│"), "│")))
			boxLines = append(boxLines, plain)
		}
	}
	box := strings.Join(boxLines, "\n")
	if !strings.Contains(box, "OpenRouter") {
		t.Fatalf("dialog box not found or OpenRouter title missing:\n%s", view)
	}

	// Every content row (header or model row) must be accounted for by its
	// owning section's title appearing above it, before an intervening
	// title of the other section. The "no o-* row under OpenRouter / no
	// router-* row under OpenAI" wording says the same thing: the box
	// opens under the pinned/live section's title, so a row attributed to
	// the wrong section is a pin failure.
	type titleRow struct {
		title string // section title the current run belongs to
		pick  int    // -1 title row otherwise: model rows seen since that title
	}
	current := titleRow{"", -1}
	attributed := false
	for _, plain := range boxLines {
		switch {
		case plain == "OpenAI" || plain == "OpenRouter":
			if current.pick >= 0 && current.pick == 0 {
				t.Fatalf("orphan header %q with no rows following it:\n%s", current.title, view)
			}
			current = titleRow{title: plain, pick: 0}
		case strings.Contains(plain, "Model selection") || strings.Contains(plain, "Enter switch"):
			continue // box title / hint rows, not section content
		default:
			if strings.TrimSpace(plain) == "" {
				continue
			}
			fields := strings.Fields(plain)
			if len(fields) == 0 {
				t.Fatalf("non-empty box row has no fields (box extraction bug):\n%s", plain)
			}
			id := fields[0]
			if !strings.HasPrefix(id, "o-") && !strings.HasPrefix(id, "router-") {
				continue // box chrome; only model rows carry the ids
			}
			if current.title == "" {
				t.Fatalf("model row %q renders before any section title:\n%s", plain, view)
			}
			want := "OpenAI"
			if strings.HasPrefix(id, "router-") {
				want = "OpenRouter"
			}
			if current.title != want {
				t.Fatalf("row %q renders under %q title, want %q:\n%s", plain, current.title, want, view)
			}
			current.pick++
			attributed = true
		}
	}
	if !attributed {
		t.Fatalf("no model rows visible mid-section:\n%s", view)
	}
	// The pinned title occupies one window slot, so the box content plus
	// borders must stay inside m.height — spill would push the bottom
	// border (and with it the hint) off the screen.
	boxRows := len(boxLines) + 2 // ── top and bottom border rows
	if boxRows > m.height {
		t.Fatalf("dialog box rows (%d) exceed terminal height (%d):\n%s", boxRows, m.height, view)
	}
	// The OpenRouter section is entirely reachable: its title must show
	// exactly once in the dialog box, either as the pinned title above the
	// window rows or as the run start after the last OpenAI row.
	if countHeaderLinesByBox(boxLines, "OpenRouter") != 1 {
		t.Fatalf("OpenRouter title rendered %d times in the dialog box:\n%s", countHeaderLinesByBox(boxLines, "OpenRouter"), view)
	}
	// The box content must fit the terminal: with the hint row and bottom
	// border intact no tail content was clipped off the render.
	if lines[len(lines)-1] == "" && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	if strings.Count(strings.Join(lines, "\n"), "Enter switch") != 1 {
		t.Fatalf("hint line clipped from the dialog box bottom:\n%s", view)
	}
}

// countHeaderLinesByBox counts exact header matches on one dialog-box row,
// the box-local variant of countHeaderLines (models_all_test.go).
func countHeaderLinesByBox(box []string, header string) int {
	n := 0
	for _, plain := range box {
		if plain == header {
			n++
		}
	}
	return n
}

func makeIds(prefix string, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s%d", prefix, i+1)
	}
	return ids
}
