package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
)

type selectionRestoredMsg struct {
	sessionID string
	selection model.Selection
	provider  model.Provider
	client    *model.Client
	live      liveEffort
	details   []model.ModelDetails
	notice    string
	err       error
	revision  uint64
}

// restoreCommittedSelection never starts work. Foreign models are checked
// against the destination's visible model inventory before session activation.
func (m *ui) restoreCommittedSelection() tea.Cmd {
	if m.snapshot.Selection == nil || m.client == nil {
		return nil
	}
	saved, sessionID, revision := *m.snapshot.Selection, m.snapshot.ID, m.selectionRevision
	if saved.Provider == m.conn.ProviderCanonical && saved.Model == m.modelName {
		notice, err := providers.ApplySavedVariant(context.Background(), m.stateDir, m.client, m.activeProvider(), m.liveVariants().reasoning(), saved.Variant)
		if notice != "" {
			m.entries = append(m.entries, entry{role: "Likha", content: notice})
		}
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Restore selection: " + err.Error()})
		}
		return nil
	}
	state := m.stateDir
	return func() tea.Msg {
		result := selectionRestoredMsg{sessionID: sessionID, selection: saved, revision: revision}
		p, ok := model.LookupProvider(saved.Provider)
		if !ok {
			result.err = fmt.Errorf("saved provider is unavailable")
			return result
		}
		result.provider = p
		res, err := providers.ResolveProvider(p.Name, "", "", false, state)
		if err != nil {
			result.err = err
			return result
		}
		if res.Endpoint != p.BaseURL {
			result.err = fmt.Errorf("saved provider route is unavailable")
			return result
		}
		var client *model.Client
		if res.OAuth {
			client, err = model.NewOAuth(res.Endpoint, saved.Model, res.Creds.Issuer, res.Creds.ClientID, res.Creds)
			if err == nil {
				bindOAuthStorage(client, state, p.Name)
			}
		} else {
			client, err = model.New(res.Endpoint, saved.Model, res.Key)
		}
		if err != nil {
			result.err = err
			return result
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		details, err := client.Models(ctx)
		if err != nil {
			client.InvalidateOAuth()
			result.err = err
			return result
		}
		idx := slices.IndexFunc(details, func(detail model.ModelDetails) bool { return detail.ID == saved.Model })
		if idx < 0 {
			client.InvalidateOAuth()
			result.err = fmt.Errorf("saved model is not visible on this account")
			return result
		}
		result.live = liveEffortsByModel(details)[saved.Model]
		result.notice, result.err = providers.ApplySavedVariant(ctx, state, client, p, result.live.reasoning(), saved.Variant)
		if result.err != nil {
			client.InvalidateOAuth()
			return result
		}
		result.client, result.details = client, details
		return result
	}
}

func (m *ui) applyRestoredSelection(result selectionRestoredMsg) {
	if result.sessionID != m.snapshot.ID || m.working || result.revision != m.selectionRevision {
		if result.client != nil {
			result.client.InvalidateOAuth()
		}
		return
	}
	if result.err != nil {
		m.entries = append(m.entries, entry{role: "Likha", content: "Saved selection is unavailable; keeping the launch selection: " + result.err.Error()})
		return
	}
	if m.client != nil {
		m.client.InvalidateOAuth()
	}
	m.client, m.modelName, m.activeLive = result.client, result.selection.Model, result.live
	m.selectionRevision++
	m.conn.Provider, m.conn.ProviderCanonical, m.conn.Verified = result.provider.DisplayName, result.provider.Name, true
	m.conn.ContextWindows = make(map[string]int64)
	for _, detail := range result.details {
		if detail.ContextWindow > 0 {
			m.conn.ContextWindows[detail.ID] = detail.ContextWindow
		}
	}
	if result.provider.SessionHeader != "" {
		m.client.SetSessionHeader(result.provider.SessionHeader)
		m.client.SetSession(m.snapshot.ID)
	}
	m.resolveContextWindow()
	m.recalculateContext(m.history)
	m.adoptReplacedClient()
	if result.notice != "" {
		m.entries = append(m.entries, entry{role: "Likha", content: result.notice})
	}
	m.layoutWidth = 0
}
