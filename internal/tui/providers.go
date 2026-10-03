package tui

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"likha/internal/model"
	"likha/internal/providers"
)

// /providers implements the connection manager: a selection dialog over the
// predefined providers where Enter opens that provider's auth surface — the
// masked key-entry modal for providers without a stored key, an auth-state
// view for configured ones. A passing connection check stores the key and
// returns to the dialog; nothing on this surface ever switches the live
// provider (activation happens through /models) and config.json is never
// read or written here (setup remains the defaults path).

// keyState backs the provider auth overlay: either the auth-state view of a
// configured provider (auth) or the dedicated API-key entry screen for an
// unconfigured one. The key is stored only after the connection check passes.
type keyState struct {
	open     bool
	auth     bool // auth-state view: the stored credential, not key entry
	provider model.Provider
	signedIn bool   // auth view (OAuth): a stored login is present
	source   string // auth view (key): where the stored key came from
	input    []rune
	checking bool
	err      string
}

// keyCheckMsg carries the connection-check result for the key modal.
type keyCheckMsg struct {
	provider model.Provider
	key      string
	err      error
}

// openKeyModal switches to the key-entry screen for a provider with no
// stored key. Esc is the only exit besides a passing check.
func (m *ui) openKeyModal(p model.Provider) {
	m.keyModal = keyState{open: true, provider: p}
	m.status = "Provider key required"
	m.layoutWidth = 0
}

// closeKeyModal discards the overlay and returns to the provider dialog with
// nothing changed: no stored key, no client swap.
func (m *ui) closeKeyModal() {
	m.keyModal = keyState{}
	m.layoutWidth = 0
}

// openProviderAuth routes one provider row to its auth surface and never
// switches anything: a configured key provider opens the auth-state view, an
// unconfigured one the key-entry modal, and an OAuth provider the sign-in
// state view (the switcher cannot sign in interactively).
func (m *ui) openProviderAuth(p model.Provider) tea.Cmd {
	m.layoutWidth = 0
	if p.Auth == model.AuthOAuth {
		_, signedIn, err := providers.StoredOAuth(m.stateDir, p.Name)
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Read stored credentials: " + err.Error()})
			return nil
		}
		m.keyModal = keyState{open: true, auth: true, provider: p, signedIn: signedIn}
		return nil
	}
	key, err := providers.StoredKey(m.stateDir, p.Name)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Read stored API keys: " + err.Error()})
		return nil
	}
	if key != "" {
		m.keyModal = keyState{open: true, auth: true, provider: p,
			source: "stored in the private state directory (providers.json)"}
		return nil
	}
	if p.KeyEnv != "" && os.Getenv(p.KeyEnv) != "" {
		m.keyModal = keyState{open: true, auth: true, provider: p,
			source: "provided via environment variable " + p.KeyEnv}
		return nil
	}
	m.openKeyModal(p)
	return nil
}

// updateKeyModal handles keys while the provider auth overlay is open. In the
// key-entry modal the input mirrors the setup flow's masked field: Enter
// checks, Esc cancels. In the auth-state view Enter re-opens the key modal to
// replace the stored key (key providers only) and Esc returns to the dialog.
func (m *ui) updateKeyModal(msg tea.KeyMsg) tea.Cmd {
	if !m.keyModal.open {
		return nil
	}
	if m.keyModal.auth {
		switch msg.String() {
		case "enter":
			if m.keyModal.provider.Auth == model.AuthOAuth {
				// Sign-in cannot happen here: the modal is for keys only.
				return nil
			}
			// Replace: the stored key stays until a new one passes the check.
			m.keyModal = keyState{open: true, provider: m.keyModal.provider}
			m.status = "Replace provider key"
			m.layoutWidth = 0
		case "esc":
			m.closeKeyModal()
		}
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
				_, err := model.ListModels(ctx, p.BaseURL, key)
				return keyCheckMsg{provider: p, key: key, err: err}
			}
		}
	case tea.KeyEsc:
		m.closeKeyModal()
	}
	return nil
}

// handleKeyCheckMsg finishes the key modal: the key persists only after the
// check passes, and the flow returns to the providers dialog — the live
// provider and model stay untouched. A failed check keeps the previously
// stored key (if any) and shows the error in the modal.
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
	if err := providers.StoreKey(m.stateDir, p.Name, v.key); err != nil {
		m.keyModal.err = err.Error()
		m.layoutWidth = 0
		return
	}
	m.keyModal = keyState{}
	if m.dialog.open && m.dialog.kind == dialogProviders {
		// Refresh the rows in place so the stored key shows as ✔ without
		// disturbing the open filter or the cursor.
		items, _ := m.providersDialogItems()
		m.dialogItems = items
	} else {
		// Direct form (/providers <n-or-name> [key]): land on the dialog so
		// the freshly stored row is visible and Enter-auth takes over.
		m.openDialog(dialogProviders)
	}
	m.entries = append(m.entries, entry{role: "Likha", content: "Stored the API key for " + p.DisplayName + "."})
	m.status = "Key stored"
	m.layoutWidth = 0
}

// handleProvidersCommand implements /providers, /providers <n-or-name>, and
// /providers <n-or-name> [key]. A bare command opens the selection dialog; a
// number indexes the predefined list position-stably (independent of any
// dialog filter state) and an exact canonical name resolves via
// LookupProvider, matching /themes. A target opens that provider's auth
// surface; a trailing key argument stores that key after a passing check.
// Nothing on this command ever switches the live provider.
func (m *ui) handleProvidersCommand(line, arg string) tea.Cmd {
	target, keyArg, hasKey := strings.Cut(arg, " ")
	keyArg = strings.TrimSpace(keyArg)
	if target == "" {
		return m.openDialog(dialogProviders)
	}
	var p model.Provider
	if n, err := strconv.Atoi(target); err == nil {
		if n < 1 || n > len(model.Providers) {
			m.input = []rune(line)
			m.edit.endCaret(m.input)
			m.entries = append(m.entries, entry{role: "Error", content: "Usage: /providers <n-or-name> [key] — run /providers first for the numbered list. " + commandHelp})
			return nil
		}
		p = model.Providers[n-1]
	} else {
		var ok bool
		p, ok = model.LookupProvider(target)
		if !ok {
			m.input = []rune(line)
			m.edit.endCaret(m.input)
			m.entries = append(m.entries, entry{role: "Error", content: "Unknown provider " + target + "; run /providers for the list. " + commandHelp})
			return nil
		}
	}
	if hasKey {
		return m.applyProviderKeyArgument(p, keyArg)
	}
	return m.openProviderAuth(p)
}

// applyProviderKeyArgument stores a key passed flag-style with the command:
// the key modal's check machinery runs without further input — the modal
// seeds into its checking state and the result lands in handleKeyCheckMsg,
// which stores the key and returns to the providers dialog. A failed check
// keeps the modal open with the error and stores nothing.
func (m *ui) applyProviderKeyArgument(p model.Provider, key string) tea.Cmd {
	m.layoutWidth = 0
	if key == "" {
		return m.openProviderAuth(p)
	}
	if p.Auth == model.AuthOAuth {
		m.entries = append(m.entries, entry{role: "Error", content: p.DisplayName + " uses ChatGPT login; it has no API key to store."})
		return m.openProviderAuth(p)
	}
	m.keyModal = keyState{open: true, provider: p, checking: true, input: []rune(key)}
	m.status = "Checking " + p.DisplayName
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := model.ListModels(ctx, p.BaseURL, key)
		return keyCheckMsg{provider: p, key: key, err: err}
	}
}

// Custom endpoints (spec §6.1): the auth machinery is provider-agnostic (the
// key modal checks any BaseURL), but no custom row is surfaced in the dialog
// and the direct form resolves only predefined names, so nothing reaches it
// until this gate flips. Enabling the feature later removes the gate; it adds
// no plumbing.
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
				// A read error counts as not signed in; the auth view
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

// keyModalView renders the provider auth overlay over the provider dialog,
// which stays open beneath: Esc returns to it with nothing changed. Opened by
// a direct argument, it instead overlays the dimmed main view. The key-entry
// modal masks its input with paste dots exactly like the setup flow's key
// stage; the auth-state view shows the stored credential's source.
func (m *ui) keyModalView() string {
	if m.keyModal.auth {
		return m.providerAuthView()
	}
	var base []string
	if m.dialog.open {
		base = strings.Split(m.dialogView(), "\n")
	} else {
		base = strings.Split(m.mainView(), "\n")
	}
	width := m.width
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
	content = append(content, withBase(m.theme.Title, m.theme.Base).Render(fit(title, inner)))
	content = append(content, m.theme.Base.Render(fit("", inner)))
	content = append(content, m.theme.Base.Render(fit(masked, inner)))
	if status != "" {
		style := m.theme.Muted
		if m.keyModal.err != "" {
			style = m.theme.Error
		}
		for _, line := range wrap(status, inner) {
			content = append(content, withBase(style, m.theme.Base).Render(fit(line, inner)))
		}
	}
	return m.drawOverlayBox(base, boxWidth, content)
}

// providerAuthView renders the auth-state overlay for a configured provider:
// the provider, where its stored key came from or its ChatGPT sign-in state,
// and the footer. Enter never switches anything; Esc returns to the provider
// dialog beneath (or the main view for the direct form).
func (m *ui) providerAuthView() string {
	var base []string
	if m.dialog.open {
		base = strings.Split(m.dialogView(), "\n")
	} else {
		base = strings.Split(m.mainView(), "\n")
	}
	width := m.width
	p := m.keyModal.provider
	title := "Auth for " + p.DisplayName
	footer := "Esc back"
	var body []string
	if p.Auth == model.AuthOAuth {
		if m.keyModal.signedIn {
			body = append(body, "Signed in.")
		} else {
			body = append(body, "Not signed in: sign in during first-run setup or run likha --provider chatgpt --device-login.")
		}
	} else {
		body = append(body, "✔ Connected — key "+m.keyModal.source+".")
		footer = "Enter replace key  Esc back"
	}

	boxWidth := runewidth.StringWidth(title) + 4
	for _, line := range body {
		if w := runewidth.StringWidth(line) + 6; w > boxWidth {
			boxWidth = w
		}
	}
	if w := runewidth.StringWidth(footer) + 2; w > boxWidth {
		boxWidth = w
	}
	boxWidth = min(width-4, boxWidth+4)
	inner := boxWidth - 4

	content := []string{withBase(m.theme.Title, m.theme.Base).Render(fit(title, inner)), m.theme.Base.Render(fit("", inner))}
	for _, line := range body {
		// Long hints wrap instead of truncating: a clipped
		// "likha --provider chatgpt --device-login" is useless.
		for _, chunk := range wrap(line, inner) {
			content = append(content, m.theme.Base.Render(fit(chunk, inner)))
		}
	}
	content = append(content, m.theme.Base.Render(fit("", inner)))
	content = append(content, withBase(m.theme.Help, m.theme.Base).Render(fit(footer, inner)))
	return m.drawOverlayBox(base, boxWidth, content)
}

// drawOverlayBox composes the bordered box for the provider auth overlays:
// each overlaid row is the dimmed (or intact) base text left of the box, the
// full-intensity box row, and the base text right of it — the background
// keeps its content instead of collapsing to a solid band.
func (m *ui) drawOverlayBox(base []string, boxWidth int, content []string) string {
	width, height := m.width, m.height
	top := max(0, (height-len(content)-2)/2)
	left := max(2, (width-boxWidth)/2)
	for j := 0; j < len(content)+2 && top+j < height; j++ {
		var box string
		switch {
		case j == 0:
			box = withBase(m.theme.Border, m.theme.Base).Render("╭" + strings.Repeat("─", boxWidth-2) + "╮")
		case j == len(content)+1:
			box = withBase(m.theme.Border, m.theme.Base).Render("╰" + strings.Repeat("─", boxWidth-2) + "╯")
		default:
			border := withBase(m.theme.Border, m.theme.Base)
			box = border.Render("│ ") + content[j-1] + border.Render(" │")
		}
		base[top+j] = spliceRowOn(base[top+j], left, boxWidth, box, m.theme.BaseBG())
	}
	return strings.Join(base[:height], "\n")
}
