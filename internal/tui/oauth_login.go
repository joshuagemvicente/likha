package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
)

// These seams replace only the browser flow and authenticated discovery in
// tests. Ordinary API-key discovery keeps its existing injection points.
var browserLoginFunc = model.BrowserLogin
var oauthModelsFunc = func(ctx context.Context, client *model.Client) ([]model.ModelDetails, error) {
	return client.Models(ctx)
}
var revokeOAuthFunc = func(ctx context.Context, creds model.OAuthCredentials) error {
	return model.RevokeCredentials(ctx, nil, creds)
}

type oauthSurface int

const (
	oauthSetup oauthSurface = iota
	oauthProvider
)

// One UI-owned attempt spans browser authorization and model discovery. Its
// generation also guards progress messages from a cancelled or retried flow.
type oauthFlowState struct {
	id        uint64
	surface   oauthSurface
	provider  model.Provider
	cancel    context.CancelFunc
	events    <-chan tea.Msg
	manualURL string
	progress  string
}

type oauthProgressMsg struct {
	attempt uint64
	text    string
}

type oauthBrowserErrorMsg struct {
	attempt uint64
	url     string
}

type oauthLoginMsg struct {
	attempt    uint64
	creds      model.OAuthCredentials
	details    []model.ModelDetails
	client     *model.Client
	selectOnly bool
	err        error
}

func bindOAuthStorage(client *model.Client, stateDir, provider string) {
	client.SetOAuthRefresher(func(ctx context.Context, expected model.OAuthCredentials, force bool) (model.OAuthCredentials, error) {
		return providers.RefreshOAuth(ctx, stateDir, provider, expected, force,
			func(ctx context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
				return model.RefreshCredentials(ctx, nil, old)
			})
	})
}

// oauthCandidate cannot rotate an uncommitted account's refresh token. A new
// login is checked before it is stored; a saved but expired account reconnects
// in the browser instead of refreshing an inactive registration out of band.
func oauthCandidate(p model.Provider, creds model.OAuthCredentials) (*model.Client, error) {
	if !creds.Registered() || !creds.HasPlanScope() {
		return nil, errors.New("ChatGPT browser authorization with shared plan access is required; reconnect this account")
	}
	client, err := model.NewOAuth(p.BaseURL, "", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		return nil, err
	}
	client.SetOAuthRefresher(func(ctx context.Context, expected model.OAuthCredentials, force bool) (model.OAuthCredentials, error) {
		if err := ctx.Err(); err != nil {
			return model.OAuthCredentials{}, err
		}
		if force || expected.Access == "" || expected.Expires <= time.Now().Add(30*time.Second).UnixMilli() {
			return model.OAuthCredentials{}, errors.New("ChatGPT authorization needs reconnecting; sign in again in your browser")
		}
		return expected, nil
	})
	return client, nil
}

func selectedOAuthRegistration(stateDir, provider string) (model.OAuthCredentials, error) {
	accounts, active, err := providers.OAuthAccounts(stateDir, provider)
	if err != nil {
		return model.OAuthCredentials{}, err
	}
	for _, account := range accounts {
		if account.ClientID == active {
			return account, nil
		}
	}
	return model.OAuthCredentials{}, nil
}

func (m *ui) cancelOAuthLogin() {
	if m.oauth.cancel != nil {
		m.oauth.cancel()
	}
	// Keep a monotonic ID even when overlays are discarded and recreated.
	m.oauth = oauthFlowState{id: m.oauth.id + 1}
}

func waitOAuthEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events // a closed attempt yields nil and ends this waiter
	}
}

// manualOAuthURL drops token hints before any authorization URL reaches the
// terminal. The full URL is used only by the browser launcher.
func manualOAuthURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	q := u.Query()
	for _, key := range []string{"id_token_hint", "id_token", "access_token", "refresh_token"} {
		q.Del(key)
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
}

func (m *ui) oauthWaiting() bool {
	if m.oauth.surface == oauthSetup {
		return m.mode == modeSetup && m.setup.stage == setupLogin && m.setup.checking
	}
	return m.keyModal.open && m.keyModal.provider.Auth == model.AuthOAuth && m.keyModal.checking
}

func (m *ui) oauthError(text string) {
	if m.oauth.surface == oauthSetup {
		m.setup.checking = false
		m.setup.err = text
	} else {
		m.keyModal.checking = false
		m.keyModal.err = text
	}
	m.layoutWidth = 0
}

func (m *ui) startOAuthFlow(p model.Provider, registration model.OAuthCredentials, surface oauthSurface, selectOnly bool) tea.Cmd {
	if surface == oauthProvider && m.isLiveProvider(p) && (m.working || m.pending != nil) {
		m.keyModal.err = "Cancel the active run and resolve its review before changing ChatGPT accounts."
		return nil
	}
	m.cancelOAuthLogin()
	m.oauth.surface, m.oauth.provider = surface, p
	host, err := providers.EnsureOAuthHost(m.stateDir)
	if err != nil {
		m.oauthError("Prepare browser sign-in: " + err.Error())
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	m.oauth.cancel = cancel
	events := make(chan tea.Msg, 4)
	m.oauth.events = events
	m.oauth.progress = "Opening your browser…"
	if selectOnly {
		m.oauth.progress = "Checking models available to this account…"
	}
	if surface == oauthSetup {
		m.setup.stage, m.setup.checking, m.setup.err = setupLogin, true, ""
	} else {
		m.keyModal.checking, m.keyModal.err, m.keyModal.notice = true, "", false
	}
	m.layoutWidth = 0
	attempt := m.oauth.id
	observe := m.conn.ModelMetadataObserver
	emit := func(msg tea.Msg) {
		select {
		case events <- msg:
		case <-ctx.Done():
		}
	}
	flow := func() tea.Msg {
		defer close(events)
		defer cancel()
		creds := registration
		if !selectOnly {
			ts, err := browserLoginFunc(ctx, model.BrowserLoginOptions{
				HostID: host, Credentials: registration, Port: 0,
				OpenBrowser: func(raw string) error {
					err := openBrowser(raw)
					if err == nil {
						emit(oauthProgressMsg{attempt: attempt, text: "Waiting for browser sign-in…"})
					}
					return err
				},
				OnBrowserError: func(raw string, _ error) {
					emit(oauthBrowserErrorMsg{attempt: attempt, url: manualOAuthURL(raw)})
				},
			})
			if err != nil {
				return oauthLoginMsg{attempt: attempt, err: err}
			}
			creds = ts.Credentials()
		}
		emit(oauthProgressMsg{attempt: attempt, text: "Checking models available to this account…"})
		client, err := oauthCandidate(p, creds)
		if err != nil {
			return oauthLoginMsg{attempt: attempt, err: err}
		}
		start := time.Now()
		details, err := oauthModelsFunc(ctx, client)
		if observe != nil {
			observe(p.Name, "browser sign-in /models", time.Since(start), details, err)
		}
		if err == nil && len(details) == 0 {
			err = errors.New("this account reports no eligible ChatGPT models; check plan access and reconnect")
		}
		return oauthLoginMsg{attempt: attempt, creds: creds, client: client, details: details, selectOnly: selectOnly, err: err}
	}
	cmds := []tea.Cmd{flow, waitOAuthEvent(events)}
	if surface == oauthSetup {
		cmds = append(cmds, m.armSetupTick())
	}
	return tea.Batch(cmds...)
}

func oauthModelMetadata(details []model.ModelDetails) (ids []string, windows map[string]int64, names map[string]string) {
	windows, names = make(map[string]int64), make(map[string]string)
	seen := make(map[string]bool)
	for _, detail := range details {
		if detail.ID == "" || seen[detail.ID] {
			continue
		}
		seen[detail.ID] = true
		ids = append(ids, detail.ID)
		if detail.ContextWindow > 0 {
			windows[detail.ID] = detail.ContextWindow
		}
		if detail.DisplayName != "" {
			names[detail.ID] = detail.DisplayName
		}
	}
	return
}

func (m *ui) handleOAuthLogin(v oauthLoginMsg) tea.Cmd {
	if v.attempt != m.oauth.id || !m.oauthWaiting() {
		return nil
	}
	if m.oauth.cancel != nil {
		m.oauth.cancel()
		m.oauth.cancel = nil
	}
	if v.err != nil {
		m.oauthError(fmt.Sprintf("ChatGPT sign-in failed: %v — press Enter to retry", v.err))
		return nil
	}
	ids, windows, names := oauthModelMetadata(v.details)
	if !v.creds.Registered() || !v.creds.HasPlanScope() || len(ids) == 0 || v.client == nil {
		m.oauthError("ChatGPT sign-in did not validate account and shared plan access, or reports no eligible models. Press Enter to retry.")
		return nil
	}
	p := m.oauth.provider
	if m.oauth.surface == oauthProvider && m.isLiveProvider(p) && (m.working || m.pending != nil) {
		m.oauthError("Cancel the active run and resolve its review before changing ChatGPT accounts.")
		return nil
	}
	var err error
	if v.selectOnly {
		err = providers.SelectOAuthAccount(m.stateDir, p.Name, v.creds.ClientID)
	} else {
		err = providers.StoreOAuth(m.stateDir, p.Name, v.creds)
	}
	if err != nil {
		m.oauthError("Store ChatGPT sign-in: " + err.Error())
		return nil
	}
	m.oauth.manualURL, m.oauth.progress = "", ""
	m.modelsGen++ // old account-specific model results and caches cannot survive
	delete(m.modelsCache.sections, p.Name)
	bindOAuthStorage(v.client, m.stateDir, p.Name)
	acknowledged, noticeErr := chatGPTPlanAcknowledged(m.stateDir, v.creds.ClientID)
	if m.oauth.surface == oauthSetup {
		m.setup.checking, m.setup.err = false, ""
		m.setup.creds = v.creds
		m.setup.models, m.setup.contextWindows, m.setup.modelNames = ids, windows, names
		m.setup.modelCursor, m.setup.filter = 0, nil
		if !acknowledged {
			m.setup.stage = setupPlanNotice
			if noticeErr != nil {
				m.setup.err = "Read plan acknowledgement: " + noticeErr.Error()
			}
			return nil
		}
		return m.continueOAuthSetup()
	}
	if m.isLiveProvider(p) {
		if m.client != nil {
			m.client.InvalidateOAuth()
		}
		modelID := ids[0]
		for _, id := range ids {
			if id == m.modelName {
				modelID = id
				break
			}
		}
		_ = v.client.SetModel(modelID)
		m.client, m.modelName = v.client, modelID
		m.conn.ContextWindows, m.conn.Err = windows, nil
		m.resolveContextWindow()
		m.recalculateContext(m.history)
		if cfg, err := providers.LoadStoredConfig(m.stateDir); err == nil && cfg.Provider == p.Name {
			cfg.Model = modelID
			if err := providers.SaveStoredConfig(m.stateDir, cfg); err != nil {
				m.entries = append(m.entries, entry{role: "Error", content: "Store selected model: " + err.Error()})
			}
		}
	}
	m.keyModal.checking, m.keyModal.err = false, ""
	m.reloadOAuthAccounts()
	m.keyModal.notice = !acknowledged
	m.keyModal.noticeClientID = v.creds.ClientID
	if noticeErr != nil {
		m.keyModal.err = "Read plan acknowledgement: " + noticeErr.Error()
	}
	m.refreshProvidersDialog()
	m.status = "ChatGPT account connected"
	m.layoutWidth = 0
	return nil
}

func (m *ui) continueOAuthSetup() tea.Cmd {
	m.setup.stage = setupModel
	if len(m.setup.models) == 1 {
		return m.finishSetup(m.setup.models[0])
	}
	return nil
}
