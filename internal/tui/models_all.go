package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
)

// modelsSection is one configured provider's contribution to the /models
// dialog: the provider record plus its reported model ids in reported order.
// The chatgpt row carries the curated list with no network.
//
// All-provider models (specs/all-models): openDialog fans out one command
// per configured provider and the section handlers render the sections in
// order, so the dialog never needs a /providers switch first.
type modelsSection struct {
	provider model.Provider
	models   []string
}

// modelsRow is one selectable dialog row: a model id plus the section's
// provider. Headers render from section boundaries, never as rows, so the
// cursor space, dialogMatches, and the confirm guards only ever see
// selectable rows.
type modelsRow struct {
	provider model.Provider
	model    string
}

// modelsSectionMsg carries one provider's arrived /models section. gen is
// the open generation from startModelsFetch; stale generations drop.
type modelsSectionMsg struct {
	gen     uint64
	section modelsSection
}

// modelsProviderFailedMsg names one provider whose fetch errored or reported
// nothing. errSample carries the fetch error text, or "" when the list was
// merely empty.
type modelsProviderFailedMsg struct {
	gen         uint64
	displayName string
	errSample   string
}

// listModelsFunc fetches one provider's model ids. It exists so tests can
// point rows at canned data: model.Providers URLs are fixed, so the dialog
// cannot redirect rows at httptest servers without this seam.
var listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
	return model.ListModels(ctx, base, apiKey)
}

// observeModelsFetch reports one API list fetch: provider canonical name,
// elapsed, and the fetch error (nil on success). Nil by default; tests and
// the slow-path log hook set it.
var observeModelsFetch func(provider string, elapsed time.Duration, err error)

// modelsTarget is one fetch unit: the provider plus the key to list with.
// oauth targets skip the network and contribute the curated list.
type modelsTarget struct {
	provider model.Provider
	key      string
	oauth    bool
}

// fetchModelsTarget lists one target: oauth targets contribute the curated
// list with no network, API targets list through listModelsFunc under the
// existing 5 s bound and report through observeModelsFetch.
func fetchModelsTarget(tg modelsTarget) ([]string, error) {
	if tg.oauth {
		return model.ChatGPTModels, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	ids, err := listModelsFunc(ctx, tg.provider.BaseURL, tg.key)
	if observeModelsFetch != nil {
		observeModelsFetch(tg.provider.Name, time.Since(start), err)
	}
	return ids, err
}

// buildModelsTargets computes the /models fetch set in final display order:
// the active provider's section first, then predefined-list order. The set
// comes from stored credentials read once (a non-empty Key per API-key
// provider, Type "oauth" per OAuth row); a read error or corrupt
// providers.json reads as unconfigured for every row, mirroring
// providersDialogItems. The active provider always contributes: when it
// holds no stored credential it lists through the live client's key
// (flag/env sessions), and a live client outside the predefined list falls
// back to a single live fetch so custom endpoints keep today's behavior.
//
// chatgpt contributes model.ChatGPTModels with no network: the Codex backend
// has no OpenAI-shaped list route, which is also why /models on a chatgpt
// session errors today.
func buildModelsTargets(stateDir, activeBase, activeCanonical, activeKey, activeDisplay string) []modelsTarget {
	var targets []modelsTarget
	creds, err := providers.ReadCredentials(stateDir)
	if err != nil {
		creds = nil
	}
	for _, p := range model.Providers {
		if p.Auth == model.AuthOAuth {
			if creds != nil && creds[p.Name].Type == "oauth" {
				targets = append(targets, modelsTarget{provider: p, oauth: true})
			}
			continue
		}
		if creds != nil && creds[p.Name].Key != "" {
			targets = append(targets, modelsTarget{provider: p, key: creds[p.Name].Key})
		}
	}
	activeIdx := -1
	for i, t := range targets {
		if activeBase != "" && t.provider.BaseURL == activeBase {
			activeIdx = i
			break
		}
	}
	if activeIdx < 0 && activeCanonical != "" {
		for i, t := range targets {
			if strings.EqualFold(t.provider.Name, activeCanonical) {
				activeIdx = i
				break
			}
		}
	}
	if activeIdx > 0 {
		targets = append([]modelsTarget{targets[activeIdx]}, append(targets[:activeIdx], targets[activeIdx+1:]...)...)
	} else if activeIdx < 0 && activeBase != "" {
		if p, ok := lookupProviderByBase(activeBase, activeCanonical); ok {
			if p.Auth == model.AuthOAuth {
				targets = append([]modelsTarget{{provider: p, oauth: true}}, targets...)
			} else {
				targets = append([]modelsTarget{{provider: p, key: activeKey}}, targets...)
			}
		} else {
			name := activeCanonical
			if name == "" {
				name = "custom"
			}
			display := activeDisplay
			if display == "" {
				display = "Current provider"
			}
			targets = append([]modelsTarget{{
				provider: model.Provider{Name: name, DisplayName: display, BaseURL: activeBase},
				key:      activeKey,
			}}, targets...)
		}
	}
	return targets
}

// modelsSources lists the configured providers: API-key rows with a stored
// key, OAuth rows with a stored login. Used for the zero-network refusal.
func (m *ui) modelsSources() (apis []model.Provider, oauth []model.Provider) {
	creds, err := providers.ReadCredentials(m.stateDir)
	if err != nil {
		return nil, nil
	}
	for _, p := range model.Providers {
		if p.Auth == model.AuthOAuth {
			if creds[p.Name].Type == "oauth" {
				oauth = append(oauth, p)
			}
			continue
		}
		if creds[p.Name].Key != "" {
			apis = append(apis, p)
		}
	}
	return apis, oauth
}

// lookupProviderByBase resolves the live client's base URL (falling back to
// the stored canonical name) to its predefined row.
func lookupProviderByBase(base, canonical string) (model.Provider, bool) {
	for _, p := range model.Providers {
		if base != "" && p.BaseURL == base {
			return p, true
		}
	}
	if canonical != "" {
		return model.LookupProvider(canonical)
	}
	return model.Provider{}, false
}

// startModelsFetch opens the /models dialog and fans out one command per
// configured provider, so early sections render before the slow ones land.
// A fresh cache serves instantly (cursor on the live pair) while the live
// fetches refresh behind it. The refusal when nothing is configured stays
// in handleCommand (zero network traffic); this runs only with a live
// client.
func (m *ui) startModelsFetch() tea.Cmd {
	m.modelsGen++
	gen := m.modelsGen
	activeBase, activeKey := "", ""
	if m.client != nil {
		activeBase, activeKey = m.client.Base(), m.client.APIKey()
	}
	stateDir, canonical, display := m.stateDir, m.conn.ProviderCanonical, m.conn.Provider
	targets := buildModelsTargets(stateDir, activeBase, canonical, activeKey, display)
	m.modelsArrived = map[string]modelsSection{}
	m.modelsTargetOrder = m.modelsTargetOrder[:0]
	for _, tg := range targets {
		m.modelsTargetOrder = append(m.modelsTargetOrder, tg.provider.Name)
	}
	m.modelsFailures = nil
	m.modelsErrSample = ""
	m.modelsNavigated = false
	m.modelsPending = len(targets)
	keep := make(map[string]bool, len(targets))
	for _, tg := range targets {
		keep[tg.provider.Name] = true
	}
	m.dialog = dialogState{kind: dialogModels, open: true, cursor: 0, loading: true}
	m.dialogModelRows = nil
	m.dialogModelsNote = ""
	m.dialog.loadErr = ""
	m.layoutWidth = 0
	if cached := m.modelsCache.snapshot(canonical, activeBase, keep); m.modelsCache.fresh() && len(cached) > 0 {
		for _, s := range cached {
			m.modelsArrived[s.provider.Name] = s
		}
		m.applyModelsSections(cached)
		m.dialog.loading = false
	}
	m.status = "Listing models"
	if len(targets) == 0 {
		m.rebuildModelsRows()
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(targets))
	for _, tg := range targets {
		tg := tg
		if tg.oauth {
			cmds = append(cmds, func() tea.Msg {
				return modelsSectionMsg{gen: gen, section: modelsSection{provider: tg.provider, models: model.ChatGPTModels}}
			})
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			ids, err := fetchModelsTarget(tg)
			if err != nil || len(ids) == 0 {
				sample := ""
				if err != nil {
					sample = err.Error()
				}
				return modelsProviderFailedMsg{gen: gen, displayName: tg.provider.DisplayName, errSample: sample}
			}
			return modelsSectionMsg{gen: gen, section: modelsSection{provider: tg.provider, models: ids}}
		})
	}
	return tea.Batch(cmds...)
}

// handleModelsSection records one provider's arrived section and rebuilds
// the rows in final order. A result from an earlier open (gen mismatch) or
// arriving after Esc closed the dialog is discarded with nothing applied.
func (m *ui) handleModelsSection(msg modelsSectionMsg) {
	if !(m.dialog.open && m.dialog.kind == dialogModels) || msg.gen != m.modelsGen {
		return
	}
	m.modelsCache.upsert(msg.section)
	if m.modelsArrived == nil {
		m.modelsArrived = map[string]modelsSection{}
	}
	m.modelsArrived[msg.section.provider.Name] = msg.section
	m.modelsPending--
	m.rebuildModelsRows()
}

// handleModelsProviderFailed names one provider's failed fetch and rebuilds.
// An empty list counts as a failure with no error sample, as before.
func (m *ui) handleModelsProviderFailed(msg modelsProviderFailedMsg) {
	if !(m.dialog.open && m.dialog.kind == dialogModels) || msg.gen != m.modelsGen {
		return
	}
	seen := false
	for _, f := range m.modelsFailures {
		if f == msg.displayName {
			seen = true
			break
		}
	}
	if !seen {
		m.modelsFailures = append(m.modelsFailures, msg.displayName)
	}
	if m.modelsErrSample == "" {
		m.modelsErrSample = msg.errSample
	}
	m.modelsPending--
	m.rebuildModelsRows()
}

// applyModelsSections rebuilds the selectable rows from sections and places
// the cursor: the live pair while the user has not navigated, a clamp into
// the visible matches after (arrivals never yank the cursor). Each section
// contributes one row per distinct id in first-occurrence order: a provider
// that re-lists an id still renders it once.
func (m *ui) applyModelsSections(sections []modelsSection) {
	rows := make([]modelsRow, 0)
	flat := make([]string, 0)
	counts := make(map[string]int)
	for _, s := range sections {
		// One provider may re-list an id (a proxy echoes its upstream
		// catalog), so keep only the first occurrence per section; the
		// reported order of the survivors is preserved. Ids shared across
		// providers are unaffected: each section tracks its own seen set.
		seen := make(map[string]bool, len(s.models))
		for _, id := range s.models {
			if seen[id] {
				continue
			}
			seen[id] = true
			rows = append(rows, modelsRow{provider: s.provider, model: id})
			flat = append(flat, id)
			counts[id]++
		}
	}

	m.dialogModelCounts = counts
	m.lastModels = flat
	m.dialogModelRows = rows
	if !m.modelsNavigated {
		// Reset first: a shrunken list must not leave the cursor out of range
		// when the live pair is absent from it.
		m.dialog.cursor = 0
		for i, r := range rows {
			if r.model == m.modelName && m.isLiveProvider(r.provider) {
				m.dialog.cursor = i
				break
			}
		}
		return
	}
	matches := m.dialogMatches()
	switch {
	case len(matches) == 0:
		m.dialog.cursor = 0
	case m.dialog.cursor >= len(matches):
		m.dialog.cursor = len(matches) - 1
	case m.dialog.cursor < 0:
		m.dialog.cursor = 0
	}
}

// rebuildModelsRows assembles the arrived sections in final target order and
// refreshes rows, note, cursor, and loading state. Failed providers
// contribute no rows: with other rows listed they are named in a muted
// note, with none the dialog shows the error once every fetch has settled.
func (m *ui) rebuildModelsRows() {
	m.layoutWidth = 0
	sections := make([]modelsSection, 0, len(m.modelsTargetOrder))
	for _, name := range m.modelsTargetOrder {
		if s, ok := m.modelsArrived[name]; ok {
			sections = append(sections, s)
		}
	}
	m.applyModelsSections(sections)
	if len(m.modelsFailures) > 0 {
		m.dialogModelsNote = "Unreachable: " + strings.Join(m.modelsFailures, ", ") + "."
	} else {
		m.dialogModelsNote = ""
	}
	rows := m.dialogModelRows
	m.dialog.loading = len(rows) == 0 && m.modelsPending > 0
	if m.modelsPending == 0 {
		if len(rows) == 0 {
			if m.modelsErrSample != "" {
				m.dialog.loadErr = m.modelsErrSample
			} else if len(m.modelsFailures) > 0 {
				m.dialog.loadErr = "No models listed by " + strings.Join(m.modelsFailures, ", ") + "."
			} else {
				m.dialog.loadErr = "The provider reports no models."
			}
			m.dialogModelRows = nil
			m.dialogModelsNote = ""
			return
		}
		keep := make(map[string]bool, len(m.modelsTargetOrder))
		for _, name := range m.modelsTargetOrder {
			keep[name] = true
		}
		m.modelsCache.removeNotIn(keep)
	}
}

// isLiveProvider reports whether the row's provider is the session's live
// provider: by stored canonical name when known, else by display name (the
// /providers switch drops the canonical, so display is the fallback).
func (m *ui) isLiveProvider(p model.Provider) bool {
	if m.conn.ProviderCanonical != "" {
		return strings.EqualFold(p.Name, m.conn.ProviderCanonical)
	}
	return p.DisplayName == m.conn.Provider
}

// modelLabel renders one /models row: the id plus the live-pair marker.
// Only the live provider+model pair carries (current); a bare id match
// under another provider does not. When the same id is listed by more
// than one section (a proxy re-lists its upstream models, so two
// providers report the same ids), the id renders with its provider name
// — otherwise identical rows read like duplicates of each other.
func (m *ui) modelLabel(orig int) string {
	row := m.dialogModelRows[orig]
	label := row.model
	if m.dialogModelCounts[row.model] > 1 {
		label = row.model + " · " + row.provider.DisplayName
	}
	if row.model == m.modelName && m.isLiveProvider(row.provider) {
		return label + " (current)"
	}
	return label
}

// applyModelRow switches to the selected row. The dialog is already closed
// by confirmDialog. Same-provider rows keep the applyModel path exactly;
// foreign rows build the client from the stored credential and move the
// session to the new provider+model in one step, storing the pair for
// future runs. Construction failure appends a visible error naming the
// provider and keeps the old session; nothing is stored first. The
// just-completed list fetch is the freshness signal: no second verify
// round-trip.
func (m *ui) applyModelRow(row modelsRow) {
	if m.isLiveProvider(row.provider) {
		m.applyModel(row.model)
		return
	}
	if row.provider.Auth == model.AuthOAuth {
		creds, ok, err := providers.StoredOAuth(m.stateDir, row.provider.Name)
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Read stored credentials: " + err.Error()})
			return
		}
		if !ok {
			m.status = "Error"
			m.entries = append(m.entries, entry{role: "Error", content: row.provider.DisplayName + " has no stored sign-in; sign in during first-run setup or run lisa --provider chatgpt --device-login"})
			return
		}
		client, err := model.NewOAuth(row.provider.BaseURL, row.model, model.ChatGPTIssuer, model.ChatGPTClientID, creds)
		if err != nil {
			m.status = "Error"
			m.entries = append(m.entries, entry{role: "Error", content: "Switch to " + row.provider.DisplayName + " failed: " + err.Error()})
			return
		}
		client.SetOAuthSaver(func(c model.OAuthCredentials) error {
			return providers.StoreOAuth(m.stateDir, row.provider.Name, c)
		})
		m.activateModelClient(row.provider, client, row.model)
		return
	}
	key, err := providers.StoredKey(m.stateDir, row.provider.Name)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Read stored API keys: " + err.Error()})
		return
	}
	if key == "" {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: row.provider.DisplayName + " has no stored API key; configure it in /providers first."})
		return
	}
	client, err := model.New(row.provider.BaseURL, row.model, key)
	if err != nil {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: "Switch to " + row.provider.DisplayName + " failed: " + err.Error()})
		return
	}
	m.activateModelClient(row.provider, client, row.model)
}

// activateModelClient swaps the live client for the chosen provider+model,
// stores the pair in config.json, and names both in one conversation entry.
// The swap mirrors activateProvider: stream buffers reset so no bytes from
// the previous provider bleed into the next turn.
func (m *ui) activateModelClient(p model.Provider, client *model.Client, modelID string) {
	if p.SessionHeader != "" {
		client.SetSessionHeader(p.SessionHeader)
		client.SetSession(m.snapshot.ID)
	}
	m.client = client
	m.modelName = modelID
	m.streamBuf.Reset()
	m.streaming = -1
	m.reasoningBuf.Reset()
	m.reasoningStream = -1
	m.pending = nil
	m.reviewSeen = nil
	m.jumpBottom()
	m.conn.Provider = p.DisplayName
	m.conn.ProviderCanonical = p.Name
	m.conn.Verified = true
	m.status = "Connected"
	m.layoutWidth = 0
	m.entries = append(m.entries, entry{role: "Lisa", content: "Model switched to " + modelID + " on " + p.DisplayName + " and stored for later runs."})
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Store model: " + err.Error()})
		return
	}
	cfg.Provider, cfg.Model = p.Name, modelID
	if err := providers.SaveStoredConfig(m.stateDir, cfg); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Store model: " + err.Error()})
	}
}
