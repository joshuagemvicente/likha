package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
)

// liveEffort retains older discovery callers while carrying presence-aware
// capabilities for the variant resolver. Empty known metadata is not absent.
type liveEffort struct {
	levels       []string
	defaultLevel string
	capabilities *model.ReasoningCapabilities
}

func (l liveEffort) reasoning() *model.ReasoningCapabilities {
	if l.capabilities != nil {
		return l.capabilities
	}
	if l.levels != nil {
		return &model.ReasoningCapabilities{EffortsKnown: true, Efforts: l.levels, DefaultEffort: l.defaultLevel}
	}
	return nil
}

func liveEffortsByModel(details []model.ModelDetails) map[string]liveEffort {
	result := make(map[string]liveEffort)
	for _, detail := range details {
		if detail.ID == "" || detail.Reasoning == nil && detail.Efforts == nil {
			continue
		}
		if _, exists := result[detail.ID]; !exists {
			result[detail.ID] = liveEffort{levels: detail.Efforts, defaultLevel: detail.DefaultEffort, capabilities: detail.Reasoning}
		}
	}
	return result
}

func (m *ui) cachedLiveEffort(provider, id string) liveEffort {
	return m.modelsCache.sections[provider].efforts[id]
}

func (m *ui) activeProvider() model.Provider {
	if m.client == nil {
		return model.Provider{}
	}
	p, _ := lookupProviderByBase(m.client.Base(), m.conn.ProviderCanonical)
	return p
}

func (m *ui) liveVariants() liveEffort {
	live := m.cachedLiveEffort(m.activeProvider().Name, m.modelName)
	if live.reasoning() == nil {
		live = m.activeLive
	}
	return live
}

func (m *ui) variantChoices() ([]model.Variant, error) {
	p := m.activeProvider()
	if !m.client.SupportsVariants(p) {
		return nil, nil
	}
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err != nil {
		return nil, err
	}
	return m.client.Variants(p, m.liveVariants().reasoning(), cfg.VariantConfig(p.Name, m.modelName))
}

func (m *ui) applyVariant(p model.Provider, id string, live liveEffort, choice *string) {
	if live.reasoning() == nil {
		live = m.cachedLiveEffort(p.Name, id)
	}
	m.activeLive = live
	notice, err := providers.ApplyVariant(context.Background(), m.stateDir, m.client, p, live.reasoning(), choice)
	if notice != "" {
		m.entries = append(m.entries, entry{role: "Likha", content: notice})
	}
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Select variant: " + err.Error()})
		return
	}
}

func (m *ui) selectVariant(id string) {
	p := m.activeProvider()
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err == nil {
		err = m.client.SetVariant(p, m.liveVariants().reasoning(), cfg.VariantConfig(p.Name, m.modelName), id)
	}
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Select variant: " + err.Error()})
		return
	}
	m.selectionRevision++
	m.layoutWidth = 0
	if err := providers.UpdateStoredConfig(context.Background(), m.stateDir, func(cfg *providers.StoredProviderConfig) error {
		cfg.SetStoredVariant(p.Name, m.modelName, id)
		return nil
	}); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Variant changed for future work but was not saved: " + err.Error()})
	}
}

func (m *ui) cycleVariant() {
	choices, err := m.variantChoices()
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Read variants: " + err.Error()})
		return
	}
	if len(choices) == 0 {
		return
	}
	m.selectVariant(model.CycleVariant(m.client.Variant(), choices))
}

func (m *ui) openVariants(choices []model.Variant) {
	m.dialog = dialogState{kind: dialogVariants, open: true, variants: choices}
	m.dialogItems = []string{"Default"}
	for i, variant := range choices {
		m.dialogItems = append(m.dialogItems, variant.ID)
		if variant.ID == m.client.Variant() {
			m.dialog.cursor = i + 1
		}
	}
	m.layoutWidth = 0
}

func (m *ui) handleVariantsCommand(arg string) tea.Cmd {
	if arg != "" {
		m.input = []rune("/variants " + arg)
		m.edit.endCaret(m.input)
		m.entries = append(m.entries, entry{role: "Error", content: "Use /variants without arguments, then select a variant."})
		return nil
	}
	if m.client == nil {
		m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
		return nil
	}
	if m.working || m.pending != nil {
		m.entries = append(m.entries, entry{role: "Error", content: runDraftCommandRefusal})
		return nil
	}
	choices, err := m.variantChoices()
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Read variants: " + err.Error()})
		return nil
	}
	if len(choices) == 0 {
		m.entries = append(m.entries, entry{role: "Likha", content: "Variants are not available for this model and request protocol."})
		return nil
	}
	m.openVariants(choices)
	return nil
}

func variantRejectedNote(err error) string {
	var rejected *model.VariantRejectedError
	if errors.As(err, &rejected) {
		return "Sent with variant " + rejected.Variant + "; use /variants → Default to remove the preset. The provider error above may have another cause."
	}
	var legacy *model.EffortRejectedError
	if errors.As(err, &legacy) {
		return "Sent with effort " + legacy.Level + "; use /variants → Default to remove the preset."
	}
	return ""
}

func (m *ui) composerSelection() model.Selection {
	return model.Selection{Provider: m.conn.ProviderCanonical, Model: m.modelName, Variant: m.client.Variant()}
}

// commitSelection records selection as the session's committed one. It
// bumps the revision so a session restore still in flight cannot replace the
// selection the user just committed (MV-25).
func (m *ui) commitSelection(selection model.Selection) {
	if m.snapshot.ID == "" {
		return
	}
	m.snapshot.Selection = &selection
	m.selectionRevision++
}

func variantLabel(id string) string {
	if strings.TrimSpace(id) == "" {
		return "Default"
	}
	return id
}
