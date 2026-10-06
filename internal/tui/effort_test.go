package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/transcript"
)

// enableEffort lets one predefined provider carry effort for the test, as a
// passed live probe would (specs/model-effort). Rows ship with none enabled.
func enableEffort(t *testing.T, name string, wire model.EffortWire) {
	t.Helper()
	for i, p := range model.Providers {
		if p.Name == name {
			prev := model.Providers[i].Effort
			model.Providers[i].Effort = wire
			t.Cleanup(func() { model.Providers[i].Effort = prev })
			return
		}
	}
	t.Fatalf("unknown provider %q", name)
}

// effortTestUI is an OpenAI session on gpt-4o whose /models lists gpt-4o
// (no effort levels) and gpt-5.5 (none, low, medium, high, xhigh in the
// bundled catalog).
func effortTestUI(t *testing.T, stateDir string) *ui {
	t.Helper()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		return []string{"gpt-4o", "gpt-5.5"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "gpt-4o", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	return modelsTestUI(t, client, "gpt-4o", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
}

func key(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// command types a slash command and presses Enter.
func command(m *ui, line string) {
	m.Update(key(line))
	m.Update(key("enter"))
}

func TestEffortStepSwitchesModelAndLevelTogether(t *testing.T) {
	enableEffort(t, "openai", model.EffortTopLevel)
	stateDir := t.TempDir()
	m := effortTestUI(t, stateDir)
	openModels(t, m)
	m.Update(key("down")) // gpt-5.5
	m.Update(key("enter"))

	step := m.dialog.effort
	if step == nil || !m.dialog.open || m.dialog.kind != dialogModels {
		t.Fatalf("Enter on gpt-5.5 did not open the effort step: %+v", m.dialog)
	}
	if got := strings.Join(step.levels, ","); got != "none,low,medium,high,xhigh" || step.cursor != 0 {
		t.Fatalf("step levels %q cursor %d, want catalog levels on Default", got, step.cursor)
	}
	if m.modelName != "gpt-4o" {
		t.Fatalf("model switched before the effort was chosen: %q", m.modelName)
	}
	view := m.View()
	for _, want := range []string{"Effort · gpt-5.5 (OpenAI)", "Default", "provider decides", "xhigh", "Esc back to models"} {
		if !strings.Contains(view, want) {
			t.Fatalf("effort step view lacks %q:\n%s", want, view)
		}
	}

	for range 4 {
		m.Update(key("down")) // Default → none → low → medium → high
	}
	m.Update(key("enter"))
	if m.dialog.open {
		t.Fatal("dialog stayed open after choosing an effort")
	}
	if m.modelName != "gpt-5.5" || m.client.Effort() != "high" {
		t.Fatalf("session = %q at %q, want gpt-5.5 at high", m.modelName, m.client.Effort())
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Model != "gpt-5.5" || cfg.StoredEffort("openai", "gpt-5.5") != "high" {
		t.Fatalf("stored config = %+v, %v", cfg, err)
	}
	if last := lastEntry(m); !strings.Contains(last.content, "Model switched to gpt-5.5") || !strings.HasSuffix(last.content, "Effort: high.") {
		t.Fatalf("switch entry = %q", last.content)
	}
}

func TestEffortStepEscReturnsToModelList(t *testing.T) {
	enableEffort(t, "openai", model.EffortTopLevel)
	stateDir := t.TempDir()
	m := effortTestUI(t, stateDir)
	openModels(t, m)
	m.Update(key("down"))
	m.Update(key("enter"))
	m.Update(key("down"))
	m.Update(key("esc"))
	if !m.dialog.open || m.dialog.kind != dialogModels || m.dialog.effort != nil || m.dialog.cursor != 1 {
		t.Fatalf("Esc did not return to the model list on the same row: %+v", m.dialog)
	}
	if m.modelName != "gpt-4o" || m.client.Effort() != "" {
		t.Fatalf("Esc changed the session: %q at %q", m.modelName, m.client.Effort())
	}
	cfg, _ := providers.LoadStoredConfig(stateDir)
	if cfg.Model != "gpt-4o" || cfg.Efforts != nil {
		t.Fatalf("Esc changed config.json: %+v", cfg)
	}
}

func TestEffortStepStartsOnRememberedLevel(t *testing.T) {
	enableEffort(t, "openai", model.EffortTopLevel)
	stateDir := t.TempDir()
	m := effortTestUI(t, stateDir)
	cfg, _ := providers.LoadStoredConfig(stateDir)
	cfg.SetStoredEffort("openai", "gpt-5.5", "xhigh")
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	openModels(t, m)
	m.Update(key("down"))
	m.Update(key("enter"))
	if m.dialog.effort == nil || m.dialog.effort.cursor != 5 {
		t.Fatalf("cursor = %+v, want 5 (xhigh)", m.dialog.effort)
	}
	// Choosing Default removes the stored level.
	for range 5 {
		m.Update(key("up"))
	}
	m.Update(key("enter"))
	cfg, _ = providers.LoadStoredConfig(stateDir)
	if cfg.Efforts != nil || m.client.Effort() != "" {
		t.Fatalf("Default kept effort: stored %+v, client %q", cfg.Efforts, m.client.Effort())
	}
	if last := lastEntry(m); !strings.HasSuffix(last.content, "Effort: default.") {
		t.Fatalf("switch entry = %q", last.content)
	}
}

func TestModelWithoutLevelsSwitchesImmediately(t *testing.T) {
	enableEffort(t, "openai", model.EffortTopLevel)
	m := effortTestUI(t, t.TempDir())
	m.client.SetEffort(model.EffortTopLevel, "high")
	openModels(t, m)
	m.Update(key("enter")) // gpt-4o publishes no levels
	if m.dialog.open || m.dialog.effort != nil {
		t.Fatalf("a model without levels opened a step: %+v", m.dialog)
	}
	if m.client.Effort() != "" {
		t.Fatalf("effort %q carried over to a model without levels", m.client.Effort())
	}
	if last := lastEntry(m); strings.Contains(last.content, "Effort") {
		t.Fatalf("non-reasoning switch mentions effort: %q", last.content)
	}
}

func TestUnprobedProviderSkipsStepAndSaysWhy(t *testing.T) {
	m := effortTestUI(t, t.TempDir()) // openai left at EffortUnsupported
	openModels(t, m)
	m.Update(key("down"))
	m.Update(key("enter"))
	if m.dialog.open || m.modelName != "gpt-5.5" {
		t.Fatalf("unprobed provider did not switch on the first Enter: open=%v model=%q", m.dialog.open, m.modelName)
	}
	want := "Effort stays at the provider default: not yet verified for OpenAI."
	if last := lastEntry(m); !strings.HasSuffix(last.content, want) {
		t.Fatalf("switch entry = %q, want suffix %q", last.content, want)
	}
	command(m, "/effort")
	if last := lastEntry(m); last.content != "Effort is not available: not yet verified for OpenAI." {
		t.Fatalf("/effort on an unprobed provider = %q", last.content)
	}
}

func TestEffortCommand(t *testing.T) {
	enableEffort(t, "openai", model.EffortTopLevel)
	stateDir := t.TempDir()
	m := effortTestUI(t, stateDir)
	m.modelName = "gpt-5.5"
	_ = m.client.SetModel("gpt-5.5")

	command(m, "/effort high")
	if m.client.Effort() != "high" {
		t.Fatalf("/effort high left %q", m.client.Effort())
	}
	if cfg, _ := providers.LoadStoredConfig(stateDir); cfg.StoredEffort("openai", "gpt-5.5") != "high" {
		t.Fatalf("/effort high not stored: %+v", cfg.Efforts)
	}

	for _, bad := range []string{"/effort turbo", "/effort high extra", "/effort max", "/effort High"} {
		before := len(m.entries)
		command(m, bad)
		last := lastEntry(m)
		if len(m.entries) != before+1 || last.role != "Error" || !strings.Contains(last.content, "accepted: default, none, low, medium, high, xhigh") {
			t.Fatalf("%s gave %+v", bad, last)
		}
		if string(m.input) != bad {
			t.Fatalf("%s lost the draft: %q", bad, string(m.input))
		}
		if m.client.Effort() != "high" {
			t.Fatalf("%s changed the level to %q", bad, m.client.Effort())
		}
		m.input = nil
		m.edit.endCaret(m.input)
	}

	command(m, "/effort")
	if m.dialog.kind != dialogEffort || m.dialog.effort == nil || m.dialog.effort.cursor != 4 {
		t.Fatalf("bare /effort = %+v, want the step on high", m.dialog)
	}
	if view := m.View(); !strings.Contains(view, "Esc cancel") {
		t.Fatalf("/effort step hint:\n%s", view)
	}
	m.Update(key("down"))
	m.Update(key("enter"))
	if m.dialog.open || m.client.Effort() != "xhigh" || m.modelName != "gpt-5.5" {
		t.Fatalf("/effort step apply = open %v, %q on %q", m.dialog.open, m.client.Effort(), m.modelName)
	}

	command(m, "/effort default")
	if m.client.Effort() != "" {
		t.Fatalf("/effort default left %q", m.client.Effort())
	}
	if cfg, _ := providers.LoadStoredConfig(stateDir); cfg.Efforts != nil {
		t.Fatalf("/effort default kept a stored level: %+v", cfg.Efforts)
	}

	m.working = true
	command(m, "/effort high")
	if last := lastEntry(m); last.role != "Error" || m.client.Effort() != "" {
		t.Fatalf("/effort during a run = %+v at %q", last, m.client.Effort())
	}
}

func TestStoredEffortAppliesAtLaunch(t *testing.T) {
	enableEffort(t, "openai", model.EffortTopLevel)
	cases := []struct {
		name   string
		stored string
		want   string
		notice bool
	}{
		{"valid", "high", "high", false},
		{"no longer published", "max", "", true},
		{"none stored", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			cfg := providers.StoredProviderConfig{Provider: "openai", Model: "gpt-5.5"}
			cfg.SetStoredEffort("openai", "gpt-5.5", tc.stored)
			if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
				t.Fatal(err)
			}
			client, err := model.New("https://api.openai.com/v1", "gpt-5.5", "k")
			if err != nil {
				t.Fatal(err)
			}
			m := NewUI("/sample", nil, client, "gpt-5.5", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir, nil, session.Snapshot{})
			if client.Effort() != tc.want {
				t.Fatalf("effort at launch = %q, want %q", client.Effort(), tc.want)
			}
			notice := "Stored effort max is not offered for gpt-5.5; using default."
			found := false
			for _, e := range m.entries {
				found = found || e.content == notice
			}
			if found != tc.notice {
				t.Fatalf("stale notice shown = %v, want %v: %+v", found, tc.notice, m.entries)
			}
		})
	}
}

func TestStatusBarShowsEffortBesideModel(t *testing.T) {
	enableEffort(t, "openai", model.EffortTopLevel)
	m := effortTestUI(t, t.TempDir())
	m.modelName = "gpt-5.5"
	identity := func(width int) []string {
		var texts []string
		for _, s := range m.statusIdentity(width, "") {
			texts = append(texts, s.text)
		}
		return texts
	}
	if got := identity(200); strings.Contains(strings.Join(got, "|"), "high") {
		t.Fatalf("Default shows a level: %q", got)
	}
	m.client.SetEffort(model.EffortTopLevel, "high")
	got := identity(200)
	name := model.ModelDisplayName("gpt-5.5")
	at := -1
	for i, text := range got {
		if text == name {
			at = i
		}
	}
	if at < 0 || at+1 >= len(got) || got[at+1] != "high" {
		t.Fatalf("level does not follow the model %q: %q", name, got)
	}
	full := statusWidth(m.statusIdentity(200, ""))
	narrow := identity(full - 2)
	if strings.Contains(strings.Join(narrow, "|"), "high") {
		t.Fatalf("level kept on a narrow bar: %q", narrow)
	}
	for _, text := range narrow {
		if strings.HasSuffix(text, "…") {
			t.Fatalf("model shortened before the level dropped: %q", narrow)
		}
	}
}

func TestTurnFooterRecordsEffort(t *testing.T) {
	info := transcript.TurnInfo{Model: "gpt-5.5", Effort: "high"}
	decoded, ok := transcript.DecodeTurn(transcript.EncodeTurn(info))
	if !ok || decoded.Effort != "high" {
		t.Fatalf("decoded footer = %+v, %v", decoded, ok)
	}
	if got := transcript.FooterText(decoded, 80, "…"); !strings.HasPrefix(got, "gpt-5.5 · high · ") {
		t.Fatalf("footer = %q", got)
	}
	if enc := transcript.EncodeTurn(transcript.TurnInfo{Model: "gpt-5.5"}); strings.Contains(enc, "effort") {
		t.Fatalf("Default footer encodes an effort field: %s", enc)
	}
}

func TestRejectedEffortTurnErrorCarriesHint(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.working, m.runID = true, 7
	m.events = make(chan agent.TurnEvent, 1)
	m.cancel = func() {}
	err := &model.EffortRejectedError{Level: "max", Err: errString("model HTTP 400 Bad Request: unsupported reasoning_effort")}
	m.Update(agent.TurnEvent{Kind: "error", RunID: 7, Text: err.Error(), Err: err})
	want := "model HTTP 400 Bad Request: unsupported reasoning_effort\nSent with effort max; /effort default sends none."
	found := false
	for _, e := range m.entries {
		found = found || (e.role == "Error" && e.content == want)
	}
	if !found {
		t.Fatalf("turn error lacks the effort hint: %+v", m.entries)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
