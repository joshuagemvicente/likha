package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"likha/internal/model"
	"likha/internal/providers"
)

func oauthSignedIn(creds model.OAuthCredentials) bool {
	return creds.Registered() && creds.HasPlanScope() && (creds.Access != "" || creds.Refresh != "")
}

func oauthAccountLabel(account model.OAuthCredentials, active string) string {
	label := account.Email
	if label == "" {
		label = account.Subject
	}
	if label == "" {
		label = "ChatGPT account"
	}
	suffix := account.ClientID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	label += " · …" + suffix
	if account.ClientID == active {
		label += " [active]"
	}
	if !oauthSignedIn(account) {
		label += " — signed out"
	}
	return label
}

func (m *ui) reloadOAuthAccounts() {
	accounts, active, err := providers.OAuthAccounts(m.stateDir, m.keyModal.provider.Name)
	if err != nil {
		m.keyModal.err = "Read ChatGPT accounts: " + err.Error()
		return
	}
	m.keyModal.accounts, m.keyModal.activeClientID = accounts, active
	m.keyModal.signedIn = false
	for _, account := range accounts {
		if account.ClientID == active {
			m.keyModal.signedIn = oauthSignedIn(account)
		}
	}
	m.keyModal.accountCursor = min(m.keyModal.accountCursor, len(accounts))
}

func (m *ui) refreshProvidersDialog() {
	if m.dialog.open && m.dialog.kind == dialogProviders {
		m.dialogItems, _ = m.providersDialogItems()
	}
}

func (m *ui) openOAuthProviderAuth(p model.Provider) tea.Cmd {
	m.cancelOAuthLogin()
	m.keyModal = keyState{open: true, auth: true, provider: p}
	m.reloadOAuthAccounts()
	if m.keyModal.err != "" {
		return nil
	}
	var registration model.OAuthCredentials
	for i, account := range m.keyModal.accounts {
		if account.ClientID == m.keyModal.activeClientID {
			m.keyModal.accountCursor = i
			registration = account
			break
		}
	}
	if !m.keyModal.signedIn {
		return m.startOAuthFlow(p, registration, oauthProvider, false)
	}
	return nil
}

func (m *ui) updateOAuthProviderAuth(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.cancelOAuthLogin()
		if m.cancel != nil {
			m.cancel()
		}
		return tea.Quit
	case "esc":
		m.closeKeyModal()
		return nil
	}
	if m.keyModal.checking {
		return nil
	}
	if m.keyModal.notice {
		if msg.String() == "enter" {
			if err := acknowledgeChatGPTPlan(m.stateDir, m.keyModal.noticeClientID); err != nil {
				m.keyModal.err = "Store plan acknowledgement: " + err.Error()
				return nil
			}
			m.keyModal.notice, m.keyModal.err = false, ""
			m.layoutWidth = 0
		}
		return nil
	}
	p := m.keyModal.provider
	var selected model.OAuthCredentials
	if m.keyModal.accountCursor < len(m.keyModal.accounts) {
		selected = m.keyModal.accounts[m.keyModal.accountCursor]
	}
	switch msg.String() {
	case "up":
		m.keyModal.accountCursor = max(0, m.keyModal.accountCursor-1)
	case "down":
		m.keyModal.accountCursor = min(len(m.keyModal.accounts), m.keyModal.accountCursor+1)
	case "a":
		m.keyModal.accountCursor = len(m.keyModal.accounts)
		return m.startOAuthFlow(p, model.OAuthCredentials{}, oauthProvider, false)
	case "r":
		if selected.ClientID != "" {
			return m.startOAuthFlow(p, selected, oauthProvider, false)
		}
	case "ctrl+l":
		if selected.ClientID != "" && oauthSignedIn(selected) {
			return m.startOAuthLogout(p, selected)
		}
	case "enter":
		if selected.ClientID == "" || !oauthSignedIn(selected) || m.keyModal.err != "" {
			return m.startOAuthFlow(p, selected, oauthProvider, false)
		}
		if selected.ClientID != m.keyModal.activeClientID {
			if selected.Expires <= time.Now().Add(30*time.Second).UnixMilli() {
				return m.startOAuthFlow(p, selected, oauthProvider, false)
			}
			return m.startOAuthFlow(p, selected, oauthProvider, true)
		}
	}
	m.layoutWidth = 0
	return nil
}

type oauthLogoutMsg struct {
	attempt  uint64
	clientID string
	err      error
}

func (m *ui) startOAuthLogout(p model.Provider, account model.OAuthCredentials) tea.Cmd {
	m.cancelOAuthLogin()
	m.oauth.surface, m.oauth.provider = oauthProvider, p
	if m.isLiveProvider(p) && account.ClientID == m.keyModal.activeClientID {
		// Stop requests before clearing the local token mapping, even if a
		// logout is initiated while a run is still winding down.
		if m.cancel != nil {
			m.cancel()
		}
		if m.client != nil {
			m.client.InvalidateOAuth()
		}
		m.status = "ChatGPT signed out"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	m.oauth.cancel = cancel
	m.keyModal.checking, m.keyModal.err = true, ""
	m.oauth.progress = "Signing out locally and requesting revocation…"
	attempt, stateDir := m.oauth.id, m.stateDir
	cmd := func() tea.Msg {
		defer cancel()
		err := providers.LogoutOAuth(ctx, stateDir, p.Name, account.ClientID, revokeOAuthFunc)
		return oauthLogoutMsg{attempt: attempt, clientID: account.ClientID, err: err}
	}
	if m.isLiveProvider(p) && m.working {
		m.cancelling = true
		m.pending, m.reviewSeen = nil, nil
		return tea.Batch(cmd, cancelWatchdog(m.runID))
	}
	return cmd
}

func (m *ui) handleOAuthLogout(v oauthLogoutMsg) {
	if v.attempt != m.oauth.id || !m.keyModal.open || !m.keyModal.checking {
		return
	}
	m.keyModal.checking = false
	m.oauth.cancel = nil
	m.reloadOAuthAccounts()
	if v.err != nil {
		m.keyModal.err = "Signed out locally; remote revocation warning: " + v.err.Error()
		for _, account := range m.keyModal.accounts {
			if account.ClientID == v.clientID && oauthSignedIn(account) {
				m.keyModal.err = "Sign out failed: " + v.err.Error()
			}
		}
	}
	m.modelsGen++
	delete(m.modelsCache.sections, m.keyModal.provider.Name)
	m.refreshProvidersDialog()
	m.layoutWidth = 0
}

func (m *ui) oauthProviderAuthView() string {
	var base []string
	if m.dialog.open {
		base = strings.Split(m.dialogView(), "\n")
	} else {
		base = strings.Split(m.mainView(), "\n")
	}
	title := "ChatGPT accounts"
	foot := "↑/↓ select · Enter use · r reconnect · a add · Ctrl+L sign out · Esc back"
	var body []string
	switch {
	case m.keyModal.checking:
		body = append(body, m.oauth.progress)
		if m.oauth.manualURL != "" {
			body = append(body, m.oauth.manualURL)
		}
		foot = "Esc cancel · Ctrl+C quit"
	case m.keyModal.notice:
		title = chatGPTPlanTitle
		body = []string{chatGPTPlanBody, "Usage: " + chatGPTUsageURL, "", "Got it · Enter"}
		foot = "Enter Got it · Esc back"
	default:
		labels := make([]string, 0, len(m.keyModal.accounts)+1)
		for _, account := range m.keyModal.accounts {
			labels = append(labels, oauthAccountLabel(account, m.keyModal.activeClientID))
		}
		labels = append(labels, "Add a ChatGPT account…")
		count, start := windowList(len(labels), m.keyModal.accountCursor, max(1, min(6, m.height-12)))
		for i := start; i < start+count; i++ {
			marker := "  "
			if i == m.keyModal.accountCursor {
				marker = "> "
			}
			body = append(body, marker+labels[i])
		}
		body = append(body, "", "Shared ChatGPT plan allowance; credits only if opted in in ChatGPT Settings.", "Usage: "+chatGPTUsageURL)
	}
	if m.keyModal.err != "" {
		body = append(body, "", m.keyModal.err)
	}
	boxWidth := runewidth.StringWidth(title) + 8
	for _, line := range body {
		boxWidth = max(boxWidth, runewidth.StringWidth(line)+6)
	}
	boxWidth = min(max(12, m.width-4), max(boxWidth, min(76, m.width-4)))
	inner := boxWidth - 4
	content := []string{withBase(m.theme.Title, m.theme.Base).Render(fit(title, inner)), m.theme.Base.Render(fit("", inner))}
	for _, line := range body {
		for _, chunk := range wrap(line, inner) {
			content = append(content, m.theme.Base.Render(fit(chunk, inner)))
		}
	}
	content = append(content, m.theme.Base.Render(fit("", inner)))
	for _, line := range wrapHint(foot, inner) {
		content = append(content, withBase(m.theme.Help, m.theme.Base).Render(fit(line, inner)))
	}
	return m.drawOverlayBox(base, boxWidth, content)
}
