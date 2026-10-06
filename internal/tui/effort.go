package tui

import (
	"errors"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"likha/internal/model"
	"likha/internal/providers"
)

// liveEffort is a model's effort levels as its provider's live model list
// publishes them (OpenRouter); the zero value defers to the bundled catalog.
type liveEffort struct {
	levels       []string // lowest first
	defaultLevel string   // the provider's default level; "" when unknown
}

// effortStep is the effort choice shown after a model is picked in /models,
// or on its own for /effort (specs/model-effort). Cursor 0 is Default;
// cursor i is levels[i-1].
type effortStep struct {
	row         modelsRow // target provider and model
	levels      []string  // the model's published levels, lowest first
	liveDefault string    // provider default from the live list; "" when unknown
	cursor      int
	effortOnly  bool // /effort: set the level without switching models
}

// effortLevelHints are the short plain-language descriptions shown beside
// common level names; any other name shows no hint.
var effortLevelHints = map[string]string{
	"none":    "no reasoning, fastest",
	"minimal": "lightest reasoning",
	"low":     "light reasoning",
	"medium":  "balanced",
	"high":    "deeper reasoning",
	"xhigh":   "very deep, slower",
	"max":     "deepest, slowest, most tokens",
}

// effortLevelsFor returns the levels Likha offers for a provider and model,
// preferring the given live list (or, failing that, the cached /models
// section) over the catalog.
func (m *ui) effortLevelsFor(p model.Provider, modelID string, live liveEffort) ([]string, string) {
	if len(live.levels) == 0 {
		live = m.cachedLiveEffort(p.Name, modelID)
	}
	levels := model.EffortLevels(p, modelID, live.levels)
	if len(live.levels) == 0 {
		return levels, ""
	}
	return levels, live.defaultLevel
}

// liveEffortsByModel collects the live effort levels a model list published,
// keyed by model ID; the first entry for an ID wins. Models without levels
// are left out, so the bundled catalog applies to them.
func liveEffortsByModel(details []model.ModelDetails) map[string]liveEffort {
	efforts := make(map[string]liveEffort)
	for _, detail := range details {
		if detail.ID == "" || len(detail.Efforts) == 0 {
			continue
		}
		if _, exists := efforts[detail.ID]; !exists {
			efforts[detail.ID] = liveEffort{levels: detail.Efforts, defaultLevel: detail.DefaultEffort}
		}
	}
	return efforts
}

// cachedLiveEffort returns the live levels from the last /models fetch of
// the provider, if any.
func (m *ui) cachedLiveEffort(providerID, modelID string) liveEffort {
	if m.modelsCache.sections == nil {
		return liveEffort{}
	}
	return m.modelsCache.sections[providerID].efforts[modelID]
}

// activeProvider returns the provider row of the live client; a custom
// endpoint yields the zero Provider, which carries no effort.
func (m *ui) activeProvider() model.Provider {
	if m.client == nil {
		return model.Provider{}
	}
	p, _ := lookupProviderByBase(m.client.Base(), m.conn.ProviderCanonical)
	return p
}

// storedEffort reads the level stored for the pair; a config read error
// counts as Default because the switch itself already reports it.
func (m *ui) storedEffort(providerID, modelID string) string {
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err != nil {
		return ""
	}
	return cfg.StoredEffort(providerID, modelID)
}

// applyEffort installs the effort for the newly active provider and model.
// A nil choice resolves the stored level (launch, setup, sign-in): a stored
// level the model no longer publishes falls back to Default with a notice.
// A non-nil choice was picked in the effort step and is stored, "" meaning
// Default. It returns the sentence the model-switch entry appends, or "".
func (m *ui) applyEffort(p model.Provider, modelID string, live liveEffort, choice *string) string {
	if len(live.levels) == 0 {
		live = m.cachedLiveEffort(p.Name, modelID)
	}
	// Remembered so /effort offers the live levels before /models has run.
	m.activeLive = live
	levels, _ := m.effortLevelsFor(p, modelID, live)
	if len(levels) == 0 {
		m.client.SetEffort(model.EffortUnsupported, "")
		if reason := model.EffortUnavailableReason(p); reason != "" && model.IsReasoningModel(p.Name, modelID) {
			return " Effort stays at the provider default: " + reason + "."
		}
		return ""
	}
	level := ""
	if choice != nil {
		level = *choice
		if err := m.storeEffort(p.Name, modelID, level); err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Store effort: " + err.Error()})
		}
	} else if stored := m.storedEffort(p.Name, modelID); stored != "" {
		if slices.Contains(levels, stored) {
			level = stored
		} else {
			m.entries = append(m.entries, entry{role: "Likha", content: "Stored effort " + stored + " is not offered for " + modelID + "; using default."})
		}
	}
	m.client.SetEffort(p.Effort, level)
	return " Effort: " + effortLabel(level) + "."
}

// storeEffort records the level for the pair in config.json; Default removes
// the entry.
func (m *ui) storeEffort(providerID, modelID, level string) error {
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err != nil {
		return err
	}
	cfg.SetStoredEffort(providerID, modelID, level)
	return providers.SaveStoredConfig(m.stateDir, cfg)
}

// effortLabel names a level for display; "" is Default.
func effortLabel(level string) string {
	if level == "" {
		return "default"
	}
	return level
}

// openEffortStep shows the effort choice for row. The cursor starts on the
// level last used with the pair, else on Default (decision 2).
func (m *ui) openEffortStep(row modelsRow, levels []string, liveDefault string, effortOnly bool) {
	step := &effortStep{row: row, levels: levels, liveDefault: liveDefault, effortOnly: effortOnly}
	current := m.storedEffort(row.provider.Name, row.model)
	if effortOnly {
		current = m.client.Effort()
	}
	if i := slices.Index(levels, current); i >= 0 {
		step.cursor = i + 1
	}
	if effortOnly {
		m.dialog = dialogState{kind: dialogEffort, open: true}
	}
	m.dialog.effort = step
	m.layoutWidth = 0
}

// updateEffortStep handles keys while the effort step is open. Esc returns
// to the model list (or closes /effort) and changes nothing.
func (m *ui) updateEffortStep(msg tea.KeyMsg) tea.Cmd {
	step := m.dialog.effort
	switch msg.String() {
	case "up":
		if step.cursor > 0 {
			step.cursor--
		}
	case "down":
		if step.cursor < len(step.levels) {
			step.cursor++
		}
	case "esc":
		if step.effortOnly {
			m.dialog = dialogState{}
		} else {
			m.dialog.effort = nil
		}
		m.layoutWidth = 0
	case "enter":
		level := ""
		if step.cursor > 0 {
			level = step.levels[step.cursor-1]
		}
		if step.effortOnly {
			m.dialog = dialogState{}
			m.layoutWidth = 0
			m.setSessionEffort(step.row.provider, step.row.model, level)
			return nil
		}
		m.finishModelSelection(step.row, &level)
	}
	return nil
}

// setSessionEffort applies an /effort choice to the current model.
func (m *ui) setSessionEffort(p model.Provider, modelID, level string) {
	if err := m.storeEffort(p.Name, modelID, level); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Store effort: " + err.Error()})
		return
	}
	m.client.SetEffort(p.Effort, level)
	m.entries = append(m.entries, entry{role: "Likha", content: "Effort for " + modelID + " set to " + effortLabel(level) + " and stored for later runs."})
}

// handleEffortCommand runs /effort: no argument opens the step for the
// current model, a level sets it directly, and anything else is a visible
// error that keeps the draft (spec points 8–13).
func (m *ui) handleEffortCommand(arg string) tea.Cmd {
	if m.client == nil {
		m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
		return nil
	}
	if m.working || m.pending != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Cancel the active run and resolve its review before changing effort."})
		return nil
	}
	p := m.activeProvider()
	// A /models fetch since the switch is fresher than the levels fetched
	// with it (startup, setup, sign-in).
	live := m.cachedLiveEffort(p.Name, m.modelName)
	if len(live.levels) == 0 {
		live = m.activeLive
	}
	levels, liveDefault := m.effortLevelsFor(p, m.modelName, live)
	if len(levels) == 0 {
		reason := model.EffortUnavailableReason(p)
		if reason == "" {
			reason = m.modelName + " publishes no effort levels"
		}
		m.entries = append(m.entries, entry{role: "Likha", content: "Effort is not available: " + reason + "."})
		return nil
	}
	row := modelsRow{provider: p, model: m.modelName}
	if arg == "" {
		m.openEffortStep(row, levels, liveDefault, true)
		return nil
	}
	if arg == "default" {
		m.setSessionEffort(p, m.modelName, "")
		return nil
	}
	if !slices.Contains(levels, arg) {
		m.input = []rune("/effort " + arg)
		m.edit.endCaret(m.input)
		m.entries = append(m.entries, entry{role: "Error", content: "Unknown effort \"" + arg + "\" for " + m.modelName + "; accepted: default, " + strings.Join(levels, ", ") + "."})
		return nil
	}
	m.setSessionEffort(p, m.modelName, arg)
	return nil
}

// effortRejectedNote returns the hint appended to a turn error when the
// rejected request carried effort (spec point 17), else "".
func effortRejectedNote(err error) string {
	var rejected *model.EffortRejectedError
	if !errors.As(err, &rejected) {
		return ""
	}
	return "Sent with effort " + rejected.Level + "; /effort default sends none."
}

// effortStepView renders the effort step over the dimmed main view.
func (m *ui) effortStepView(base []string) string {
	step := m.dialog.effort
	title := "Effort · " + step.row.model
	if step.row.provider.DisplayName != "" {
		title += " (" + step.row.provider.DisplayName + ")"
	}
	hint := "↑/↓ navigate  Enter switch  Esc back to models"
	if step.effortOnly {
		hint = "↑/↓ navigate  Enter apply  Esc cancel"
	}
	type effortRow struct {
		label  string
		detail []string
	}
	defaultDetail := "provider decides"
	if step.liveDefault != "" {
		defaultDetail += " (" + step.liveDefault + ")"
	}
	// /effort marks the level in use, as /models marks the live model.
	current := func(label, level string) string {
		if step.effortOnly && level == m.client.Effort() {
			return label + " (current)"
		}
		return label
	}
	rows := []effortRow{{label: current("Default", ""), detail: []string{defaultDetail}}}
	for _, level := range step.levels {
		var detail []string
		if h := effortLevelHints[level]; h != "" {
			detail = []string{h}
		}
		rows = append(rows, effortRow{label: current(level, level), detail: detail})
	}

	boxWidth := max(runewidth.StringWidth(title), runewidth.StringWidth(hint)+2)
	for _, row := range rows {
		if w := runewidth.StringWidth(row.label) + runewidth.StringWidth(modelDetailText(row.detail)) + 8; w > boxWidth {
			boxWidth = w
		}
	}
	boxWidth = min(m.width-4, boxWidth+4)
	inner := boxWidth - 4

	content := []string{
		withBase(m.theme.Title, m.theme.Base).Render(fit(title, inner)),
		m.theme.Base.Render(fit("", inner)),
	}
	windowed, start := windowList(len(rows), step.cursor, max(1, m.height-10))
	for i := start; i < start+windowed; i++ {
		content = append(content, m.modelDialogRow(rows[i].label, rows[i].detail, i == step.cursor, inner))
	}
	content = append(content, m.theme.Base.Render(fit("", inner)))
	for _, line := range wrapHint(hint, inner) {
		content = append(content, withBase(m.theme.Help, m.theme.Base).Render(fit(line, inner)))
	}
	return m.overlayDialogBox(base, content, boxWidth)
}
