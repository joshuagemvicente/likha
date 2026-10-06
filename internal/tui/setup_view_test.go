package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

// setupStages puts m through every onboarding screen, calling visit on each.
func setupStages(m *ui, visit func(name string)) {
	m.setup.stage = setupProvider
	visit("provider")
	m.setup.filter = []rune("zzz")
	visit("provider no match")
	m.setup.filter = nil
	m.setup.stage = setupKey
	visit("key empty")
	m.setup.keyInput = []rune(strings.Repeat("k", 200))
	visit("key long")
	m.setup.stage, m.setup.checking = setupChecking, true
	visit("checking")
	m.setup.stage, m.setup.checking = setupKey, false
	m.setup.err = "model endpoint rejected the credentials: HTTP 401 Unauthorized: a long provider message that wraps"
	visit("key error")
	m.setup.err = ""
	m.setup.stage = setupModel
	m.setup.models = []string{"alpha", "beta-with-a-rather-long-model-identifier-name", "gamma"}
	m.setup.contextWindows = map[string]int64{"alpha": 128000}
	visit("model")
	m.setup.stage = setupTheme
	visit("theme")
	m.setup.stage, m.setup.checking = setupLogin, false
	visit("login")
	m.setup.checking = true
	visit("login waiting")
	m.setup.checking = false
}

func TestSetupViewFitsEveryStageAndSize(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		for _, size := range [][2]int{{40, 12}, {60, 24}, {80, 24}, {120, 40}} {
			m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true, ASCII: ascii}, t.TempDir(), nil, session.Snapshot{})
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			setupStages(m, func(name string) {
				t.Run(name, func(t *testing.T) { assertViewport(t, m.View(), size[0], size[1]) })
			})
		}
	}
}

// Rows are styled once after fitting: under a color profile the view must
// carry real SGR sequences, never an escaped "\u001B" from re-fitting a
// styled row, and the footer must sit on the last row.
func TestSetupViewStylesAreNotEscaped(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true, Theme: "catppuccin"}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	setupStages(m, func(name string) {
		view := m.View()
		if strings.Contains(view, `\u001B`) {
			t.Fatalf("%s: styled row was re-fitted into a literal escape: %q", name, view)
		}
		if !strings.Contains(view, "\x1b[") {
			t.Fatalf("%s: no styling under a color profile", name)
		}
		assertViewport(t, view, 90, 30)
		rows := strings.Split(view, "\n")
		if last := stripANSI(rows[len(rows)-1]); !strings.Contains(last, "ctrl+c quit") && !strings.Contains(last, "esc") {
			t.Fatalf("%s: footer not pinned to the last row: %q", name, last)
		}
	})
}

// Untrusted text (model IDs, errors) reaches the screen with control runes
// made visible, never as raw escape sequences.
func TestSetupViewEscapesUntrustedText(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setup.stage = setupModel
	m.setup.models = []string{"evil\x1b[31mred"}
	m.setup.err = "bad\x07bell"
	view := m.View()
	if strings.ContainsAny(view, "\x1b\x07") {
		t.Fatalf("raw control runes reached the view: %q", view)
	}
	if !strings.Contains(view, `evil\u001B[31mred`) || !strings.Contains(view, `bad\u0007bell`) {
		t.Fatalf("escaped untrusted text not shown: %q", view)
	}
}

func TestSetupStepperShowsProgressInWords(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setup.stage = setupModel
	if view := m.View(); !strings.Contains(view, "✓ Provider ── ✓ Connect ── ● Model ── ○ Theme") {
		t.Fatalf("stepper missing: %q", view)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if view := m.View(); !strings.Contains(view, "Step 3 of 4  ·  Model") {
		t.Fatalf("narrow stepper missing: %q", view)
	}
	m.conn.ASCII = true
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if view := m.View(); !strings.Contains(view, "+ Provider -- + Connect -- * Model -- - Theme") {
		t.Fatalf("ASCII stepper missing: %q", view)
	}
}

// Regression: the theme stage used to move the provider cursor, so going
// back from it and re-picking a model stored the key under another provider.
func TestSetupThemeCursorDoesNotMoveProvider(t *testing.T) {
	stateDir := t.TempDir()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // openai
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-test")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(setupCheckMsg{models: []string{"alpha", "beta"}})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupTheme {
		t.Fatalf("theme stage not reached: %d", m.setup.stage)
	}
	for range 3 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.setup.stage != setupModel {
		t.Fatalf("esc did not return to the model stage: %d", m.setup.stage)
	}
	if got, want := m.theme.Selected.Value(), likhaui.Resolve(m.themeName, likhaui.HasDarkBackground()).Selected.Value(); got != want {
		t.Fatal("esc from the theme stage kept the previewed theme")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openai" {
		t.Fatalf("provider changed after theme navigation: %+v %v", cfg, err)
	}
	if key, _ := providers.StoredKey(stateDir, "openai"); key != "sk-test" {
		t.Fatalf("key stored under the wrong provider: openai=%q", key)
	}
}

func TestSetupThemeStageStartsOnCurrentTheme(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true, Theme: "nord"}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setup.models = []string{"alpha"}
	m.setup.keyInput = []rune("sk")
	m.finishSetup("alpha")
	if got := likhaui.ThemeNames()[m.setup.themeCursor]; got != "nord" {
		t.Fatalf("theme cursor on %q, want the current theme nord", got)
	}
}

func TestSetupProviderFilter(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("groq")})
	if p := model.Providers[m.setup.cursor]; p.Name != "groq" {
		t.Fatalf("filter did not move the cursor to groq: %q", p.Name)
	}
	view := m.View()
	if !strings.Contains(view, "Groq") || strings.Contains(view, "OpenRouter") || !strings.Contains(view, "1 of 1") {
		t.Fatalf("filtered list wrong: %q", view)
	}
	// Esc clears the filter before it would go anywhere.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.setup.filter) != 0 || m.setup.stage != setupProvider || model.Providers[m.setup.cursor].Name != "groq" {
		t.Fatalf("esc did not just clear the filter: filter=%q stage=%d", string(m.setup.filter), m.setup.stage)
	}
	// No match: Enter does nothing.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupProvider || !strings.Contains(m.View(), `No providers match "zzz"`) {
		t.Fatalf("enter on an empty match set advanced: stage=%d", m.setup.stage)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bedrock")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupKey || model.Providers[m.setup.cursor].Name != "bedrock" || len(m.setup.filter) != 0 {
		t.Fatalf("filtered enter did not open bedrock key entry: stage=%d provider=%q", m.setup.stage, model.Providers[m.setup.cursor].Name)
	}
}

func TestSetupModelStagePreselectsDefaultAndFilters(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // openai, default gpt-4o-mini
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(setupCheckMsg{models: []string{"gpt-4o", "gpt-4o-mini", "o3"}, contextWindows: map[string]int64{"gpt-4o-mini": 128000}})
	if m.setup.models[m.setup.modelCursor] != "gpt-4o-mini" {
		t.Fatalf("default model not preselected: %q", m.setup.models[m.setup.modelCursor])
	}
	view := m.View()
	if !strings.Contains(view, "> gpt-4o-mini") || !strings.Contains(view, "128k context") || !strings.Contains(view, "recommended") {
		t.Fatalf("default row details missing: %q", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o3")})
	if m.setup.models[m.setup.modelCursor] != "o3" || strings.Contains(m.View(), "gpt-4o") {
		t.Fatalf("model filter did not narrow to o3: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // clears the filter only
	if m.setup.stage != setupModel {
		t.Fatalf("esc with a filter left the model stage: %d", m.setup.stage)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.setup.stage != setupKey {
		t.Fatalf("second esc did not go back to key entry: %d", m.setup.stage)
	}
}

// A key already exported in the provider's variable is offered with an empty
// field, checked, and never copied into providers.json.
func TestSetupUsesEnvironmentKeyWithoutStoringIt(t *testing.T) {
	t.Setenv("LIKHA_OPENAI_API_KEY", "sk-from-env")
	stateDir := t.TempDir()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if view := m.View(); !strings.Contains(view, "key found in LIKHA_OPENAI_API_KEY") {
		t.Fatalf("provider row does not note the environment key: %q", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if view := m.View(); !strings.Contains(view, "press Enter to use $LIKHA_OPENAI_API_KEY") || !strings.Contains(view, "enter use env key") {
		t.Fatalf("key stage does not offer the environment key: %q", view)
	}
	if strings.Contains(m.View(), "sk-from-env") {
		t.Fatal("environment key rendered in clear text")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || m.setup.stage != setupChecking || !m.setup.envKey {
		t.Fatalf("empty field did not check the environment key: stage=%d env=%t", m.setup.stage, m.setup.envKey)
	}
	m.Update(setupCheckMsg{models: []string{"alpha"}})
	if m.setup.stage != setupTheme || m.client == nil {
		t.Fatalf("setup did not finish with the environment key: stage=%d err=%q", m.setup.stage, m.setup.err)
	}
	if key, _ := providers.StoredKey(stateDir, "openai"); key != "" {
		t.Fatalf("environment key copied into providers.json: %q", key)
	}
	if cfg, _ := providers.LoadStoredConfig(stateDir); cfg.Provider != "openai" || cfg.Model != "alpha" {
		t.Fatalf("stored config: %+v", cfg)
	}
}

func TestSetupSpinnerTicksOnlyWhileWaiting(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.setup.ticking {
		t.Fatal("check did not arm the spinner")
	}
	before := m.setup.spin
	if _, cmd := m.Update(setupTickMsg{}); cmd == nil || m.setup.spin != before+1 {
		t.Fatalf("tick did not advance and re-arm: spin=%d", m.setup.spin)
	}
	m.Update(setupCheckMsg{err: model.ErrUnauthorized})
	if _, cmd := m.Update(setupTickMsg{}); cmd != nil || m.setup.ticking {
		t.Fatal("spinner kept ticking after the check settled")
	}
}

func TestSetupCompletionLeavesNotice(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(setupCheckMsg{models: []string{"alpha"}})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeMain {
		t.Fatalf("setup did not complete: %q", m.mode)
	}
	last := m.entries[len(m.entries)-1]
	if last.role != "Likha" || !strings.Contains(last.content, "Connected to OpenAI · alpha") || !strings.Contains(last.content, "/models") {
		t.Fatalf("completion notice missing: %+v", last)
	}
}
