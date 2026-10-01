package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
	lisaui "lisa/internal/ui"
)

func TestThemesModalSelection(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "default"}, stateDir, nil, session.Snapshot{})
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
	m := NewUI("/sample", nil, client, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
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

// openThemesDialog opens the /themes dialog and fails the test otherwise.
func openThemesDialog(t *testing.T, m *ui) {
	t.Helper()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open || m.dialog.kind != dialogThemes {
		t.Fatalf("themes dialog did not open: open=%t", m.dialog.open)
	}
}

// configSnapshot returns the raw config.json bytes ("" when absent).
func configSnapshot(t *testing.T, stateDir string) string {
	t.Helper()
	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// committedTheme loads the stored theme for assertions.
func committedTheme(t *testing.T, stateDir string) string {
	t.Helper()
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Theme
}

// themeSelectedOpen returns the opening escape sequence of the theme's
// Selected role (the dialog's row-highlight style). Tests assert the dialog
// render contains the candidate's opener to prove the preview is visible.
func themeSelectedOpen(t *testing.T, name string) string {
	t.Helper()
	forceANSI(t)
	rendered := lisaui.Resolve(name, lisaui.HasDarkBackground()).Selected.Render("x")
	idx := strings.Index(rendered, "m")
	if idx < 0 {
		t.Fatalf("theme %s selected style rendered no escape: %q", name, rendered)
	}
	return rendered[:idx+1]
}

// cursorOn moves the dialog cursor to the named theme via down keys so the
// preview path under test is key-driven, not a direct function call.
func cursorOn(t *testing.T, m *ui, name string) {
	t.Helper()
	for range m.dialogItems {
		matches := m.dialogMatches()
		if m.dialog.cursor < len(matches) && m.dialogItems[matches[m.dialog.cursor]] == name {
			return
		}
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	matches := m.dialogMatches()
	if m.dialog.cursor >= len(matches) || m.dialogItems[matches[m.dialog.cursor]] != name {
		t.Fatalf("could not move cursor to %q", name)
	}
}

// TestThemesPreviewKeepsCommittedNameAndConfig verifies arrows re-resolve
// m.theme to the candidate while themeName and config.json stay committed.
func TestThemesPreviewKeepsCommittedNameAndConfig(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "default"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "default"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openThemesDialog(t, m)
	before := configSnapshot(t, stateDir)

	cursorOn(t, m, "everforest")
	if m.themeName != "default" {
		t.Fatalf("preview wrote themeName = %q, want committed default", m.themeName)
	}
	if committedTheme(t, stateDir) != "default" || configSnapshot(t, stateDir) != before {
		t.Fatal("preview wrote config.json before Enter")
	}
	if entries := len(m.entries); entries != 1 {
		t.Fatalf("preview appended transcript entries: %d", entries)
	}
	forceANSI(t)
	got := m.theme.Selected.Render("x")
	want := lisaui.Resolve("everforest", lisaui.HasDarkBackground()).Selected.Render("x")
	if got != want {
		t.Fatal("arrow navigation did not re-resolve m.theme to the everforest candidate")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.themeName != "default" {
		t.Fatalf("preview wrote themeName after up = %q", m.themeName)
	}
	got = m.theme.Selected.Render("x")
	want = lisaui.Resolve("kanagawa", lisaui.HasDarkBackground()).Selected.Render("x")
	if got != want {
		t.Fatal("up navigation did not re-resolve m.theme to the kanagawa candidate")
	}
	if configSnapshot(t, stateDir) != before {
		t.Fatal("preview wrote config.json on up navigation")
	}
}

// TestThemesEnterCommitsPreview verifies Enter stores the candidate and notes it.
func TestThemesEnterCommitsPreview(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "default"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "default"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openThemesDialog(t, m)
	before := len(m.entries)

	cursorOn(t, m, "habamax")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open {
		t.Fatal("Enter did not close the dialog")
	}
	if m.themeName != "habamax" {
		t.Fatalf("committed theme = %q, want habamax", m.themeName)
	}
	if committedTheme(t, stateDir) != "habamax" {
		t.Fatal("Enter did not persist the candidate to config.json")
	}
	found := false
	for _, e := range m.entries[before:] {
		if e.role == "Lisa" && strings.Contains(e.content, "Theme set to habamax") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing Theme set entry: %+v", m.entries[before:])
	}
}

// TestThemesEscRestoresCommitted verifies Esc restores the committed theme
// with no config write and no transcript entry.
func TestThemesEscRestoresCommitted(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "habamax"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "habamax"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openThemesDialog(t, m)
	cursorOn(t, m, "everforest")
	forceANSI(t)
	previewed := m.theme.Selected.Render("x")
	if previewed == lisaui.Resolve("habamax", lisaui.HasDarkBackground()).Selected.Render("x") {
		t.Fatal("navigation did not preview the candidate")
	}
	beforeCfg := configSnapshot(t, stateDir)
	beforeEntries := len(m.entries)
	infoBefore, err := os.Stat(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("Esc did not close the dialog")
	}
	restored := m.theme.Selected.Render("x")
	want := lisaui.Resolve("habamax", lisaui.HasDarkBackground()).Selected.Render("x")
	if restored != want {
		t.Fatal("Esc did not restore the committed theme styles")
	}
	if m.themeName != "habamax" {
		t.Fatalf("Esc changed themeName = %q", m.themeName)
	}
	if configSnapshot(t, stateDir) != beforeCfg {
		t.Fatal("Esc wrote config.json")
	}
	if infoAfter, err := os.Stat(providers.ConfigFilePath(stateDir)); err != nil || !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Fatalf("Esc touched config.json mtime: before=%v after=%v err=%v", infoBefore.ModTime(), infoAfter.ModTime(), err)
	}
	if len(m.entries) != beforeEntries {
		t.Fatalf("Esc appended transcript entries: %+v", m.entries[beforeEntries:])
	}
}

// TestThemesTwoStageEscRestores verifies the query-clear Esc path also
// discards the preview: first Esc clears the filter (restoring the
// committed theme), second Esc closes with nothing committed.
func TestThemesTwoStageEscRestores(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "habamax"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "habamax"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openThemesDialog(t, m)
	beforeCfg := configSnapshot(t, stateDir)
	beforeEntries := len(m.entries)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ever")})
	if m.dialog.query != "ever" {
		t.Fatalf("query not applied: %q", m.dialog.query)
	}
	forceANSI(t)
	previewed := m.theme.Selected.Render("x")
	if previewed == lisaui.Resolve("habamax", lisaui.HasDarkBackground()).Selected.Render("x") {
		t.Fatal("filtering did not preview the narrowed candidate")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dialog.open || m.dialog.query != "" {
		t.Fatalf("first Esc did not clear the query: open=%t q=%q", m.dialog.open, m.dialog.query)
	}
	if got, want := m.theme.Selected.Render("x"), lisaui.Resolve("habamax", lisaui.HasDarkBackground()).Selected.Render("x"); got != want {
		t.Fatal("first Esc did not restore the committed theme")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("second Esc did not close the dialog")
	}
	if m.themeName != "habamax" || committedTheme(t, stateDir) != "habamax" || configSnapshot(t, stateDir) != beforeCfg {
		t.Fatal("two-stage Esc committed or wrote config")
	}
	if len(m.entries) != beforeEntries {
		t.Fatalf("two-stage Esc appended entries: %+v", m.entries[beforeEntries:])
	}
}

// TestThemesDirectArgStillCommits covers the /themes <n-or-name> path.
func TestThemesDirectArgStillCommits(t *testing.T) {
	for _, arg := range []string{"3", "nord"} {
		stateDir := t.TempDir()
		if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "default"}); err != nil {
			t.Fatal(err)
		}
		m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "default"}, stateDir, nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/themes " + arg)})
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if m.dialog.open {
			t.Fatalf("direct /themes %s opened the dialog", arg)
		}
		want := arg
		if arg == "3" {
			want = lisaui.ThemeNames()[2]
		}
		if m.themeName != want {
			t.Fatalf("/themes %s committed %q, want %q", arg, m.themeName, want)
		}
		if committedTheme(t, stateDir) != want {
			t.Fatalf("/themes %s did not persist %q", arg, want)
		}
		found := false
		for _, e := range m.entries {
			if e.role == "Lisa" && strings.Contains(e.content, "Theme set to "+want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("/themes %s missing Theme set entry: %+v", arg, m.entries)
		}
	}
}

// TestThemesDialogPreviewRenderDiffers asserts the preview is visible: the
// dialog render mid-preview carries the candidate family's Selected-row
// escape and differs from the committed-theme render.
func TestThemesDialogPreviewRenderDiffers(t *testing.T) {
	forceANSI(t)
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "default"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "default"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openThemesDialog(t, m)
	committed := m.dialogView()
	cursorOn(t, m, "everforest")
	previewed := m.dialogView()
	if previewed == committed {
		t.Fatal("dialog render unchanged mid-preview")
	}
	if accent := themeSelectedOpen(t, "everforest"); !strings.Contains(previewed, accent) {
		t.Fatal("dialog render lacks the candidate Selected-row escape")
	}
}

// TestThemesCurrentMarkerNamesEscTarget asserts the (current) marker reads
// the committed themeName (the Esc target), never the live preview.
func TestThemesCurrentMarkerNamesEscTarget(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "habamax"}); err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: "habamax"}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openThemesDialog(t, m)
	cursorOn(t, m, "everforest")
	for i, item := range m.dialogItems {
		got := m.dialogLabel(i, m.dialogItems)
		switch item {
		case "habamax":
			if !strings.Contains(got, "(current)") {
				t.Fatalf("committed row lost its marker mid-preview: %q", got)
			}
		case "everforest":
			if strings.Contains(got, "(current)") {
				t.Fatalf("preview candidate wrongly marked current: %q", got)
			}
		}
	}
}

// TestSetupThemePreviewChangesLiveTheme verifies the first-run setupTheme
// stage previews on up/down without committing.
func TestSetupThemePreviewChangesLiveTheme(t *testing.T) {
	stateDir := t.TempDir()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.mode != modeSetup {
		t.Fatalf("setup did not start: mode=%q", m.mode)
	}
	m.setup.stage = setupTheme
	m.setup.cursor = 0
	m.themeName = "default"
	m.previewSetupTheme()

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got, want := m.theme.Selected.Value(), lisaui.Resolve(lisaui.ThemeNames()[1], lisaui.HasDarkBackground()).Selected.Value(); got != want {
		t.Fatal("setup down did not preview the next theme")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got, want := m.theme.Selected.Value(), lisaui.Resolve(lisaui.ThemeNames()[0], lisaui.HasDarkBackground()).Selected.Value(); got != want {
		t.Fatal("setup up did not preview the previous theme")
	}
	if m.themeName != "default" {
		t.Fatalf("setup preview committed themeName = %q", m.themeName)
	}
	if configSnapshot(t, stateDir) != "" {
		t.Fatal("setup preview wrote config.json")
	}
}
