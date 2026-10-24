package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/transcript"
)

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

func command(m *ui, line string) { m.Update(key(line)); m.Update(key("enter")) }

func variantTestUI(t *testing.T, id string) *ui {
	t.Helper()
	state := t.TempDir()
	client, err := model.New("https://api.openai.com/v1", id, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	return modelsTestUI(t, client, id, providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, state)
}

func TestCtrlTCyclesShippedVariantsWithoutEditing(t *testing.T) {
	m := variantTestUI(t, "gpt-5.5")
	m.input = []rune("keep my draft")
	m.edit = editState{caret: 4, selectionAnchor: 1, selecting: true, ring: [][]rune{[]rune("kill")}, lastWasYank: true}
	before := m.edit
	before.ring = [][]rune{[]rune("kill")}
	for _, want := range []string{"none", "low", "medium", "high", "xhigh", ""} {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
		if m.client.Variant() != want || string(m.input) != "keep my draft" || !reflect.DeepEqual(m.edit, before) {
			t.Fatalf("cycle = %q, input=%q, edit=%+v", m.client.Variant(), string(m.input), m.edit)
		}
	}
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if choice, exists := cfg.StoredVariant("openai", "gpt-5.5"); err != nil || choice != "" || !exists {
		t.Fatalf("explicit Default not saved: %+v, %v", cfg, err)
	}
}

func TestCtrlTNoVariantsAndModalOwnership(t *testing.T) {
	m := variantTestUI(t, "gpt-4o")
	before := len(m.entries)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if len(m.entries) != before {
		t.Fatal("zero variants was not silent")
	}
	for _, surface := range []string{"command", "mention", "dialog", "setup"} {
		t.Run(surface, func(t *testing.T) {
			m := variantTestUI(t, "gpt-5.5")
			m.input = []rune("draft")
			switch surface {
			case "command":
				m.commandPopup.open = true
			case "mention":
				m.mention.open = true
			case "dialog":
				m.openVariants([]model.Variant{{ID: "high"}})
			case "setup":
				m.mode = modeSetup
			}
			m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			if m.client.Variant() != "" || string(m.input) != "draft" {
				t.Fatal("overlay leaked Ctrl+T")
			}
		})
	}
}

func TestModelsApplyBeforeVariantChooserAndEscKeepsModel(t *testing.T) {
	m := variantTestUI(t, "gpt-4o")
	m.dialog = dialogState{kind: dialogModels, open: true}
	p, _ := model.LookupProvider("openai")
	m.dialogModelRows = []modelsRow{{provider: p, model: "gpt-5.5"}}
	m.Update(key("enter"))
	if m.modelName != "gpt-5.5" || m.dialog.kind != dialogVariants || m.dialogItems[0] != "Default" {
		t.Fatalf("picker = %q, %+v", m.modelName, m.dialog)
	}
	m.Update(key("esc"))
	if m.dialog.open || m.modelName != "gpt-5.5" {
		t.Fatal("Esc undid the selected model")
	}
}

func TestModelsRestoreNamedPreferenceAndRememberedDefaultReopens(t *testing.T) {
	for _, choice := range []string{"high", ""} {
		m := variantTestUI(t, "gpt-4o")
		if err := providers.UpdateStoredConfig(context.Background(), m.stateDir, func(cfg *providers.StoredProviderConfig) error {
			cfg.SetStoredVariant("openai", "gpt-5.5", choice)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		p, _ := model.LookupProvider("openai")
		m.dialog = dialogState{kind: dialogModels, open: true}
		m.dialogModelRows = []modelsRow{{provider: p, model: "gpt-5.5"}}
		m.Update(key("enter"))
		if m.client.Variant() != choice || m.dialog.open != (choice == "") {
			t.Fatalf("preference %q = %q, open=%v", choice, m.client.Variant(), m.dialog.open)
		}
	}
}

func TestVariantsSearchAndRetiredCommands(t *testing.T) {
	m := variantTestUI(t, "gpt-5.5")
	m.handleCommand("/variants")
	m.Update(key("high"))
	if got := len(m.dialogMatches()); got != 2 {
		t.Fatalf("search found %d variants", got)
	}
	m.Update(key("enter"))
	if m.client.Variant() != "high" {
		t.Fatal("filtered selector chose wrong row")
	}
	for _, line := range []string{"/effort", "/effort high", "/effor", "/variants high"} {
		m.handleCommand(line)
		if string(m.input) != line || lastEntry(m).role != "Error" {
			t.Fatalf("command %q was not refused", line)
		}
	}
	if len(commandMatches("effort")) != 0 || strings.Contains(commandHelp, "/effort") {
		t.Fatal("retired command still advertised")
	}
}

func TestVariantRunSnapshotAndMixedQueueFIFO(t *testing.T) {
	m := variantTestUI(t, "gpt-5.5")
	m.selectVariant("low")
	active, err := m.captureSelection()
	if err != nil {
		t.Fatal(err)
	}
	m.runClient, m.working = active.client, true
	m.runProvider, m.runModel = active.selection.Provider, active.selection.Model
	m.steer = make(chan string, 64)
	m.steerNow = make(chan struct{}, 1)
	m.enqueue("same")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlT}) // low -> medium
	m.enqueue("different")
	m.selectVariant("low")
	m.enqueue("later same")
	if active.client.Variant() != "low" || len(m.steer) != 1 || len(m.queueSelections) != 3 || m.queueSelections[2].sent {
		t.Fatal("selection changed active execution or overtook held work")
	}
	m.steerNowPress()
	if len(m.steerNow) != 0 {
		t.Fatal("different selection interrupted active run")
	}
	m.dropQueueHead(1)
	m.working = false
	// Resolve the next group without executing the returned command: all
	// requests in startTurn's goroutine see the captured immutable fork.
	if m.queueSelections[0].client.Variant() != "medium" || m.queueSelections[1].client.Variant() != "low" {
		t.Fatal("queued choices were not captured")
	}
}

func TestTurnFooterRecordsExecutionVariant(t *testing.T) {
	m := variantTestUI(t, "gpt-5.5")
	m.selectVariant("high")
	m.beginTurnFooter()
	m.selectVariant("low")
	m.appendTurnFooter(0, false)
	info, ok := transcript.DecodeTurn(lastEntry(m).content)
	if !ok || info.Variant != "high" {
		t.Fatalf("footer = %+v", info)
	}
	note := variantRejectedNote(&model.VariantRejectedError{Variant: "high", Err: context.Canceled})
	if !strings.Contains(note, "/variants") {
		t.Fatal("rejection hint missing")
	}
}

func TestLegacyVariantMigrationAtLaunch(t *testing.T) {
	m := variantTestUI(t, "gpt-5.5")
	if err := providers.UpdateStoredConfig(context.Background(), m.stateDir, func(cfg *providers.StoredProviderConfig) error {
		cfg.SetStoredEffort("openai", "gpt-5.5", "high")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, _ := model.LookupProvider("openai")
	m.applyVariant(p, m.modelName, liveEffort{}, nil)
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	choice, ok := cfg.StoredVariant(p.Name, m.modelName)
	if err != nil || !ok || choice != "high" || m.client.Variant() != "high" {
		t.Fatal("legacy preference did not migrate")
	}
}

func TestOldSessionWithoutVariantSelection(t *testing.T) {
	m := variantTestUI(t, "gpt-4o")
	if m.snapshot.Selection != nil {
		t.Fatal("startup committed a draft without submission")
	}
}
