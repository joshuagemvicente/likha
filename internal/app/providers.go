package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
)

// /providers implements in-session provider switching: a selection dialog
// over the predefined providers, an inline key-entry modal for providers
// without a stored key, and a live build-verify-activate swap that never
// touches config.json (session-only; setup remains the defaults path).

// keyState is the dedicated API-key entry screen for an unconfigured
// provider. The key is stored only after the connection check passes.
type keyState struct {
	open     bool
	provider model.Provider
	input    []rune
	checking bool
	err      string
}

// keyCheckMsg carries the connection-check result for the key modal.
type keyCheckMsg struct {
	provider model.Provider
	key      string
	models   []string
	err      error
}

// openKeyModal switches to the key-entry screen for a provider with no
// stored key. Esc is the only exit besides success.
func (m *ui) openKeyModal(p model.Provider) {
	m.keyModal = keyState{open: true, provider: p}
	m.status = "Provider key required"
	m.layoutWidth = 0
}

// closeKeyModal discards the modal and returns to the provider dialog with
// nothing changed: no stored key, no client swap.
func (m *ui) closeKeyModal() {
	m.keyModal = keyState{}
	m.layoutWidth = 0
}

// updateKeyModal handles keys while the key-entry modal is open. The input
// mirrors the setup flow's masked field; Enter checks, Esc cancels.
func (m *ui) updateKeyModal(msg tea.KeyMsg) tea.Cmd {
	if !m.keyModal.open {
		return nil
	}
	if m.keyModal.checking {
		// A late check result is ignored once the modal moves on; esc keeps
		// the entered key but cancels the wait (same as setup's esc retreat).
		if msg.String() == "esc" {
			m.keyModal.checking = false
			m.layoutWidth = 0
		}
		return nil
	}
	switch msg.Type {
	case tea.KeyBackspace:
		if len(m.keyModal.input) > 0 {
			m.keyModal.input = m.keyModal.input[:len(m.keyModal.input)-1]
		}
	case tea.KeyRunes, tea.KeySpace:
		r := msg.Runes
		if msg.Type == tea.KeySpace {
			r = []rune{' '}
		}
		m.keyModal.input = append(m.keyModal.input, r...)
	case tea.KeyEnter:
		if key := strings.TrimSpace(string(m.keyModal.input)); key != "" {
			m.keyModal.checking = true
			m.keyModal.err = ""
			m.layoutWidth = 0
			p := m.keyModal.provider
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ids, err := model.ListModels(ctx, p.BaseURL, key)
				return keyCheckMsg{provider: p, key: key, models: ids, err: err}
			}
		}
	case tea.KeyEsc:
		m.closeKeyModal()
	}
	return nil
}

// handleKeyCheckMsg finishes the key modal: the key persists only after the
// check passes, then the provider activates immediately.
func (m *ui) handleKeyCheckMsg(v keyCheckMsg) {
	if !m.keyModal.open || !m.keyModal.checking || m.keyModal.provider.Name != v.provider.Name {
		// Late result after esc or a restart of the modal: ignore.
		return
	}
	m.keyModal.checking = false
	if v.err != nil {
		m.keyModal.err = v.err.Error()
		m.layoutWidth = 0
		return
	}
	p := v.provider
	modelID := providers.SwitchModelID(p, v.models)
	if modelID == "" {
		m.keyModal.err = "Provider reports no models; no model to switch to."
		m.layoutWidth = 0
		return
	}
	if err := providers.StoreKey(m.stateDir, p.Name, v.key); err != nil {
		m.keyModal.err = err.Error()
		m.layoutWidth = 0
		return
	}
	m.keyModal = keyState{}
	m.activateProvider(p, v.key, model.OAuthCredentials{}, modelID)
}

// startProviderSwitch begins the live switch for an already-configured
// provider: verify before anything becomes visible (async), then activate.
// The previous provider stays active on failure. Key providers verify with a
// model-list request; OAuth providers verify by validating the stored login,
// which needs no network while the token is unexpired.
func (m *ui) startProviderSwitch(p model.Provider, key string, creds model.OAuthCredentials) tea.Cmd {
	m.status = "Checking " + p.DisplayName
	m.layoutWidth = 0
	if p.Auth == model.AuthOAuth {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := model.NewOAuth(p.BaseURL, p.DefaultModel, model.ChatGPTIssuer, model.ChatGPTClientID, creds)
			if err != nil {
				return providerSwitchMsg{provider: p, creds: creds, err: err}
			}
			err = client.Check(ctx)
			return providerSwitchMsg{provider: p, creds: creds, err: err}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ids, err := model.ListModels(ctx, p.BaseURL, key)
		return providerSwitchMsg{provider: p, key: key, models: ids, err: err}
	}
}

// providerSwitchMsg carries the verification result for a live switch. The
// creds field is zero for key providers; for OAuth providers it carries the
// stored login reused to build the activated client.
type providerSwitchMsg struct {
	provider model.Provider
	key      string
	creds    model.OAuthCredentials
	models   []string
	err      error
}

func (m *ui) handleProviderSwitchMsg(v providerSwitchMsg) tea.Cmd {
	m.layoutWidth = 0
	if v.err != nil {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: "Switch to " + v.provider.DisplayName + " failed: " + v.err.Error()})
		return nil
	}
	var reported []string
	if v.provider.Auth != model.AuthOAuth {
		// OAuth providers carry a curated static model list in the provider
		// row; only key providers resolve the model from what was reported.
		reported = v.models
	}
	modelID := providers.SwitchModelID(v.provider, reported)
	if modelID == "" {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: "Switch to " + v.provider.DisplayName + " failed: the provider reports no models."})
		return nil
	}
	return m.activateProvider(v.provider, v.key, v.creds, modelID)
}

// activateProvider swaps the live client and session identity for the new
// provider. config.json is never read or written; providers.json is only
// touched by the key modal. The stream buffers reset so no bytes from the
// previous provider bleed into the next turn.
func (m *ui) activateProvider(p model.Provider, key string, creds model.OAuthCredentials, modelID string) tea.Cmd {
	var client *model.Client
	var err error
	if p.Auth == model.AuthOAuth {
		client, err = model.NewOAuth(p.BaseURL, modelID, model.ChatGPTIssuer, model.ChatGPTClientID, creds)
		if client != nil {
			client.SetOAuthSaver(func(c model.OAuthCredentials) error {
				return providers.StoreOAuth(m.stateDir, p.Name, c)
			})
		}
	} else {
		client, err = model.New(p.BaseURL, modelID, key)
	}
	if err != nil {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: "Switch to " + p.DisplayName + " failed: " + err.Error()})
		return nil
	}
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
	m.conn = providers.Connection{Provider: p.DisplayName, Verified: true, Theme: m.conn.Theme, Nerd: m.conn.Nerd, Mcp: m.conn.Mcp}
	m.status = "Connected"
	m.layoutWidth = 0
	m.entries = append(m.entries, entry{role: "Lisa", content: "Provider switched to " + p.DisplayName + " for this session."})
	return nil
}

// handleProvidersCommand implements /providers and /providers <n-or-name>.
// A bare command opens the selection dialog; a number indexes the predefined
// list position-stably (independent of any dialog filter state) and an
// exact canonical name applies directly, matching /themes.
func (m *ui) handleProvidersCommand(line, arg string) tea.Cmd {
	if arg == "" {
		return m.openDialog(dialogProviders)
	}
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(model.Providers) {
			m.input = []rune(line)
			m.edit.endCaret(m.input)
			m.entries = append(m.entries, entry{role: "Error", content: "Usage: /providers <n-or-name> — run /providers first for the numbered list. " + commandHelp})
			return nil
		}
		return m.applyProviderDirect(model.Providers[n-1])
	}
	p, ok := model.LookupProvider(arg)
	if !ok {
		m.input = []rune(line)
		m.edit.endCaret(m.input)
		m.entries = append(m.entries, entry{role: "Error", content: "Unknown provider " + arg + "; run /providers for the list. " + commandHelp})
		return nil
	}
	return m.applyProviderDirect(p)
}

// applyProviderDirect starts a switch from a typed argument: providers
// without a stored key enter the key modal first; the rest verify async.
// OAuth providers never enter the key modal (the switcher cannot sign in):
// without a stored login the user is pointed at first-run setup or the
// device-login flag instead.
func (m *ui) applyProviderDirect(p model.Provider) tea.Cmd {
	m.layoutWidth = 0
	if p.Auth == model.AuthOAuth {
		creds, ok, err := providers.StoredOAuth(m.stateDir, p.Name)
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Read stored credentials: " + err.Error()})
			return nil
		}
		if !ok {
			m.status = "Error"
			m.entries = append(m.entries, entry{role: "Error", content: p.DisplayName + " has no stored sign-in; sign in during first-run setup or run lisa --provider chatgpt --device-login"})
			return nil
		}
		return m.startProviderSwitch(p, "", creds)
	}
	key, err := providers.StoredKey(m.stateDir, p.Name)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Read stored API keys: " + err.Error()})
		return nil
	}
	if key == "" {
		m.openKeyModal(p)
		return nil
	}
	return m.startProviderSwitch(p, key, model.OAuthCredentials{})
}

// Custom endpoints (spec §6.1): the switch machinery is provider-agnostic
// (activateProvider builds a client from an arbitrary BaseURL), but no
// custom row is surfaced in the dialog until this gate flips. Enabling the
// feature later removes the gate; it adds no plumbing.
const customEndpointsEnabled = false

// providersDialogItems builds one display row per predefined provider with
// its configured state. The current session's provider position is returned
// for the dialog cursor (starts on the active provider, mirroring themes).
func (m *ui) providersDialogItems() (items []string, activeIndex int) {
	activeIndex = -1
	for _, p := range model.Providers {
		configured, labelSuffix := "", " — not configured"
		if p.Auth == model.AuthOAuth {
			_, signedIn, err := providers.StoredOAuth(m.stateDir, p.Name)
			if err == nil && signedIn {
				configured = "✔ "
				labelSuffix = ""
			} else {
				// A read error counts as not signed in; the switch path
				// surfaces it in full when the row is selected.
				labelSuffix = " — not signed in (first-run setup or --device-login)"
			}
		} else {
			key, err := providers.StoredKey(m.stateDir, p.Name)
			if err == nil && key != "" {
				configured = "✔ "
				labelSuffix = ""
			}
		}
		label := configured + p.DisplayName + " (" + p.Name + ")" + labelSuffix
		if m.conn.Provider == p.DisplayName {
			activeIndex = len(items)
			label += "  [connected]"
		}
		items = append(items, label)
	}
	return items, activeIndex
}

// keyModalView renders the key-entry screen over the provider dialog, which
// stays open beneath: Esc returns to it with nothing changed. Opened by a
// direct argument, it instead overlays the dimmed main view. The input is
// masked with paste dots exactly like the setup flow's key stage.
func (m *ui) keyModalView() string {
	var base []string
	if m.dialog.open {
		base = strings.Split(m.dialogView(), "\n")
	} else {
		base = strings.Split(m.mainView(), "\n")
	}
	width, height := m.width, m.height
	var title, status string
	if m.keyModal.checking {
		title = "API key for " + m.keyModal.provider.DisplayName + " — checking connection…"
	} else {
		title = "API key for " + m.keyModal.provider.DisplayName + " — paste, Enter to check, Esc to cancel"
	}
	switch {
	case m.keyModal.checking:
		status = "Checking…"
	case m.keyModal.err != "":
		status = "Check failed: " + m.keyModal.err
	}
	masked := strings.Repeat("•", len(m.keyModal.input))
	if len(m.keyModal.input) == 0 && m.keyModal.err == "" && !m.keyModal.checking {
		masked = "(paste your API key, then Enter)"
	}

	boxWidth := len(title) + 4
	if w := len(masked) + 6; w > boxWidth {
		boxWidth = w
	}
	if w := len(status) + 2; w > boxWidth {
		boxWidth = w
	}
	boxWidth = min(width-4, boxWidth+4)
	inner := boxWidth - 4

	var content []string
	content = append(content, m.theme.Title.Render(fit(title, inner)))
	content = append(content, fit("", inner))
	content = append(content, fit(masked, inner))
	if status != "" {
		style := m.theme.Muted
		if m.keyModal.err != "" {
			style = m.theme.Error
		}
		for _, line := range wrap(status, inner) {
			content = append(content, style.Render(fit(line, inner)))
		}
	}

	top := max(0, (height-len(content)-2)/2)
	left := max(2, (width-boxWidth)/2)
	for j := 0; j < len(content)+2 && top+j < height; j++ {
		var box string
		switch {
		case j == 0:
			box = m.theme.Border.Render("╭" + strings.Repeat("─", boxWidth-2) + "╮")
		case j == len(content)+1:
			box = m.theme.Border.Render("╰" + strings.Repeat("─", boxWidth-2) + "╯")
		default:
			box = m.theme.Border.Render("│ ") + content[j-1] + m.theme.Border.Render(" │")
		}
		base[top+j] = spliceRowOn(base[top+j], left, boxWidth, box, m.theme.BaseBG())
	}
	return strings.Join(base[:height], "\n")
}
