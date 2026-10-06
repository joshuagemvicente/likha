package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

// High-level UI fixtures represent a completed, verified auth flow. Signed
// token validation is exercised by the model package, not mocked here as proof.
func oauthUICredentials(t *testing.T, dir, clientID string) model.OAuthCredentials {
	t.Helper()
	host, err := providers.EnsureOAuthHost(dir)
	if err != nil {
		t.Fatal(err)
	}
	return model.OAuthCredentials{
		Issuer: model.ChatGPTIssuer, Subject: "subject-" + clientID, Email: "user@example.test",
		ClientID: clientID, HostID: host, Access: "access-" + clientID, Refresh: "refresh-" + clientID,
		IDToken: "verified-id-token", TokenType: "Bearer", Expires: time.Now().Add(time.Hour).UnixMilli(),
		Scopes: []string{"openid", "email", "offline_access", model.ChatGPTPlanScope},
	}
}

func oauthUITokenSet(c model.OAuthCredentials) model.TokenSet {
	return model.TokenSet{
		Issuer: c.Issuer, Subject: c.Subject, Email: c.Email, ClientID: c.ClientID, HostID: c.HostID,
		AccessToken: c.Access, RefreshToken: c.Refresh, IDToken: c.IDToken, TokenType: c.TokenType,
		Expires: time.UnixMilli(c.Expires), Scopes: append([]string(nil), c.Scopes...),
	}
}

func stubOAuthModels(t *testing.T, details []model.ModelDetails, err error) {
	t.Helper()
	previous := oauthModelsFunc
	oauthModelsFunc = func(context.Context, *model.Client) ([]model.ModelDetails, error) { return details, err }
	t.Cleanup(func() { oauthModelsFunc = previous })
}

func stubOAuthBrowser(t *testing.T, fn func(context.Context, model.BrowserLoginOptions) (model.TokenSet, error)) {
	t.Helper()
	previous := browserLoginFunc
	browserLoginFunc = fn
	t.Cleanup(func() { browserLoginFunc = previous })
}

func oauthBatch(t *testing.T, cmd tea.Cmd) tea.BatchMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("browser authorization did not start")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) < 2 {
		t.Fatal("browser authorization did not schedule the flow and progress waiter")
	}
	return batch
}

func runOAuthFlow(t *testing.T, m *ui, cmd tea.Cmd) oauthLoginMsg {
	t.Helper()
	batch := oauthBatch(t, cmd)
	result, ok := batch[0]().(oauthLoginMsg)
	if !ok {
		t.Fatal("browser command returned no auth result")
	}
	m.Update(result)
	return result
}

type oauthUITransport func(*http.Request) (*http.Response, error)

func (fn oauthUITransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

// Keep the production public origin intact while routing test bytes to a
// local fixture. NewOAuth correctly rejects arbitrary endpoint overrides.
func routeOAuthUIToServer(t *testing.T, server *httptest.Server) {
	t.Helper()
	previous := http.DefaultTransport
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport = oauthUITransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.openai.com" {
			return nil, errors.New("unexpected test network origin")
		}
		copy := req.Clone(req.Context())
		copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
		copy.Host = target.Host
		return previous.RoundTrip(copy)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func TestChatGPTFirstSelectionLaunchesBrowserAndDiscoversAccountModels(t *testing.T) {
	dir := t.TempDir()
	creds := oauthUICredentials(t, dir, "issued-browser-first")
	var opens int
	previousOpen := openBrowser
	openBrowser = func(raw string) error {
		opens++
		if !strings.Contains(raw, "auth.openai.com") {
			t.Fatalf("wrong browser destination: %q", raw)
		}
		return nil
	}
	t.Cleanup(func() { openBrowser = previousOpen })
	stubOAuthBrowser(t, func(ctx context.Context, options model.BrowserLoginOptions) (model.TokenSet, error) {
		if options.HostID != creds.HostID || options.Port != 0 || options.Credentials.Registered() {
			t.Fatalf("first authorization options: %+v", options)
		}
		if err := options.OpenBrowser("https://auth.openai.com/oauth/authorize?state=test"); err != nil {
			return model.TokenSet{}, err
		}
		return oauthUITokenSet(creds), nil
	})
	stubOAuthModels(t, []model.ModelDetails{{ID: "account-only", DisplayName: "Account model"}, {ID: "second-real-id"}}, nil)
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, dir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	setupCursorOnChatgpt(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.setup.checking || strings.Contains(m.View(), "enter open browser") {
		t.Fatalf("first selection did not autostart: %q", m.View())
	}
	if _, extra := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); extra != nil {
		t.Fatal("a second Enter started another browser attempt")
	}
	if result := runOAuthFlow(t, m, cmd); result.err != nil {
		t.Fatal(result.err)
	}
	if opens != 1 || m.setup.stage != setupPlanNotice {
		t.Fatalf("opens=%d stage=%d error=%q", opens, m.setup.stage, m.setup.err)
	}
	if !reflect.DeepEqual(m.setup.models, []string{"account-only", "second-real-id"}) {
		t.Fatalf("did not use authenticated discovery: %v", m.setup.models)
	}
	if !strings.Contains(m.View(), chatGPTPlanTitle) || !strings.Contains(m.View(), "Got it") {
		t.Fatalf("first plan acknowledgement missing: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupModel || m.setup.modelCursor != 0 || !strings.Contains(m.View(), "Account model (account-only)") {
		t.Fatalf("automatic continuation used the wrong models: %q", m.View())
	}
	if acknowledged, err := chatGPTPlanAcknowledged(dir, creds.ClientID); err != nil || !acknowledged {
		t.Fatalf("plan acknowledgement did not persist: %v %v", acknowledged, err)
	}
}

func TestExplicitChatGPTSetupAutostartsFromInit(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true, ProviderCanonical: "chatgpt"}, t.TempDir(), nil, session.Snapshot{})
	if m.setup.stage != setupLogin || model.Providers[m.setup.cursor].Name != "chatgpt" {
		t.Fatal("explicit ChatGPT did not preselect browser setup")
	}
	if cmd := m.Init(); cmd == nil || !m.setup.checking || m.oauth.cancel == nil {
		t.Fatal("Init did not start browser authorization without a keypress")
	}
	m.cancelOAuthLogin()
}

func TestOAuthBrowserFailureShowsOnlySanitizedManualLink(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	setupCursorOnChatgpt(t, m)
	stubOAuthBrowser(t, func(ctx context.Context, options model.BrowserLoginOptions) (model.TokenSet, error) {
		options.OnBrowserError("https://auth.openai.com/oauth/authorize?client_id=issued&state=opaque&id_token_hint=SECRET_ID_TOKEN", errors.New("launch failed"))
		<-ctx.Done()
		return model.TokenSet{}, ctx.Err()
	})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if strings.Contains(m.View(), "https://auth.openai.com/oauth/authorize") {
		t.Fatal("manual authorization URL appeared before a launch failure")
	}
	batch := oauthBatch(t, cmd)
	finished := make(chan tea.Msg, 1)
	go func() { finished <- batch[0]() }()
	m.Update(batch[1]())
	view := m.View()
	if !strings.Contains(view, "Couldn't open the browser automatically") || !strings.Contains(view, "client_id=issued") || strings.Contains(view, "SECRET_ID_TOKEN") || strings.Contains(view, "id_token_hint") {
		t.Fatalf("unsafe or missing manual fallback: %q", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	select {
	case late := <-finished:
		m.Update(late)
	case <-time.After(time.Second):
		t.Fatal("Esc did not cancel browser callback waiting")
	}
	if m.mode != modeSetup || m.setup.stage != setupProvider {
		t.Fatal("cancelled attempt changed setup")
	}
}

func TestOAuthLateSuccessCannotStoreOrSupersedeRetry(t *testing.T) {
	dir := t.TempDir()
	creds := oauthUICredentials(t, dir, "issued-late-result")
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, dir, nil, session.Snapshot{})
	setupCursorOnChatgpt(t, m)
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	oldID := m.oauth.id
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	currentID := m.oauth.id
	client, err := oauthCandidate(model.Providers[m.setup.cursor], creds)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(oauthLoginMsg{attempt: oldID, creds: creds, client: client, details: []model.ModelDetails{{ID: "late-model"}}})
	if m.oauth.id != currentID || !m.setup.checking || len(m.setup.models) != 0 {
		t.Fatal("late success replaced the newer attempt")
	}
	if _, signedIn, err := providers.StoredOAuth(dir, "chatgpt"); err != nil || signedIn {
		t.Fatalf("late success wrote credentials: %v %v", signedIn, err)
	}
	m.cancelOAuthLogin()
}

func TestOAuthFailedModelCheckDoesNotStoreOrActivate(t *testing.T) {
	dir := t.TempDir()
	creds := oauthUICredentials(t, dir, "issued-model-failure")
	stubOAuthBrowser(t, func(context.Context, model.BrowserLoginOptions) (model.TokenSet, error) {
		return oauthUITokenSet(creds), nil
	})
	stubOAuthModels(t, nil, errors.New("plan access was revoked"))
	m := newProvidersUI(t, dir, "https://example.invalid/v1")
	previous := m.client
	result := runOAuthFlow(t, m, m.openProviderAuth(mustChatGPTProvider(t)))
	if result.err == nil || !strings.Contains(m.keyModal.err, "plan access was revoked") {
		t.Fatalf("model discovery failure was hidden: %q", m.keyModal.err)
	}
	if _, signedIn, err := providers.StoredOAuth(dir, "chatgpt"); err != nil || signedIn || m.client != previous {
		t.Fatalf("failed discovery stored or activated a login: %v %v", signedIn, err)
	}
}

func mustChatGPTProvider(t *testing.T) model.Provider {
	t.Helper()
	p, ok := model.LookupProvider("chatgpt")
	if !ok {
		t.Fatal("ChatGPT provider missing")
	}
	return p
}

func TestOAuthStorageFailureVisibleWithoutLiveActivation(t *testing.T) {
	dir := t.TempDir()
	creds := oauthUICredentials(t, dir, "issued-storage-failure")
	stubOAuthBrowser(t, func(context.Context, model.BrowserLoginOptions) (model.TokenSet, error) {
		return oauthUITokenSet(creds), nil
	})
	stubOAuthModels(t, []model.ModelDetails{{ID: "eligible-model"}}, nil)
	m := newProvidersUI(t, dir, "https://example.invalid/v1")
	previous := m.client
	batch := oauthBatch(t, m.openProviderAuth(mustChatGPTProvider(t)))
	result := batch[0]()
	// An unreadable credential path makes storage fail after validation, without
	// relying on file permissions that root could bypass.
	if err := os.Mkdir(providers.KeyFilePath(dir), 0700); err != nil {
		t.Fatal(err)
	}
	m.Update(result)
	if !strings.Contains(m.keyModal.err, "Store ChatGPT sign-in") || m.client != previous || m.keyModal.notice {
		t.Fatalf("storage failure activated or was hidden: %q", m.keyModal.err)
	}
}

func TestOAuthAccountsStableSameEmailLabelsAndNonSwitchingAuthView(t *testing.T) {
	dir := t.TempDir()
	first := oauthUICredentials(t, dir, "issued-workspace-aaaa1111")
	second := oauthUICredentials(t, dir, "issued-workspace-bbbb2222")
	for _, c := range []model.OAuthCredentials{second, first} {
		if err := providers.StoreOAuth(dir, "chatgpt", c); err != nil {
			t.Fatal(err)
		}
	}
	m := newProvidersUI(t, dir, "https://example.invalid/v1")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	previous := m.client
	if cmd := m.openProviderAuth(mustChatGPTProvider(t)); cmd != nil {
		t.Fatal("opening configured account management started authorization")
	}
	view := m.View()
	for _, text := range []string{"user@example.test", "…aaaa1111 [active]", "…bbbb2222", "reconnect", "sign out"} {
		if !strings.Contains(view, text) {
			t.Fatalf("account management missing %q: %q", text, view)
		}
	}
	if m.client != previous || m.conn.Provider != "OpenAI" {
		t.Fatal("opening account management switched the live provider")
	}
	if label := oauthAccountLabel(first, first.ClientID); label == oauthAccountLabel(second, first.ClientID) {
		t.Fatal("same-email workspace registrations had identical labels")
	}
}

func TestOAuthLogoutClearsTokensAndShowsRevocationWarning(t *testing.T) {
	dir := t.TempDir()
	creds := storedTestOAuth(t, dir)
	client, err := model.NewOAuth(model.ChatGPTResource, "eligible-model", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		t.Fatal(err)
	}
	bindOAuthStorage(client, dir, "chatgpt")
	m := NewUI("/sample", nil, client, "eligible-model", providers.Connection{Provider: mustChatGPTProvider(t).DisplayName, ProviderCanonical: "chatgpt"}, dir, nil, session.Snapshot{})
	m.openProviderAuth(mustChatGPTProvider(t))
	runCtx, runCancel := context.WithCancel(context.Background())
	m.cancel = runCancel
	previousRevoke := revokeOAuthFunc
	revokeOAuthFunc = func(context.Context, model.OAuthCredentials) error { return errors.New("revocation offline") }
	t.Cleanup(func() { revokeOAuthFunc = previousRevoke })
	cmd := m.updateKeyModal(tea.KeyMsg{Type: tea.KeyCtrlL})
	if runCtx.Err() == nil {
		t.Fatal("logout did not cancel active request context before token clearing")
	}
	if _, err := client.Models(context.Background()); err == nil {
		t.Fatal("logged-out client remained usable")
	}
	m.Update(cmd())
	if !strings.Contains(m.keyModal.err, "Signed out locally") || !strings.Contains(m.keyModal.err, "revocation offline") {
		t.Fatalf("remote warning hidden: %q", m.keyModal.err)
	}
	accounts, active, err := providers.OAuthAccounts(dir, "chatgpt")
	if err != nil || active != "" || len(accounts) != 1 || accounts[0].Access != "" || accounts[0].Refresh != "" || accounts[0].IDToken != "" {
		t.Fatalf("tokens retained after logout: active=%q count=%d err=%v", active, len(accounts), err)
	}
}

func TestOAuthReconnectReusesSelectedRegistrationAndNotice(t *testing.T) {
	dir := t.TempDir()
	creds := storedTestOAuth(t, dir)
	if err := acknowledgeChatGPTPlan(dir, creds.ClientID); err != nil {
		t.Fatal(err)
	}
	stubOAuthBrowser(t, func(_ context.Context, options model.BrowserLoginOptions) (model.TokenSet, error) {
		if options.Credentials.ClientID != creds.ClientID || options.HostID != creds.HostID {
			t.Fatal("reconnect did not reuse issued registration and stable host")
		}
		return oauthUITokenSet(creds), nil
	})
	stubOAuthModels(t, []model.ModelDetails{{ID: "real-model"}}, nil)
	m := newProvidersUI(t, dir, "https://example.invalid/v1")
	m.openProviderAuth(mustChatGPTProvider(t))
	result := runOAuthFlow(t, m, m.updateKeyModal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}))
	if result.err != nil || m.keyModal.notice || m.keyModal.checking {
		t.Fatalf("reconnect repeated the notice or stayed waiting: %q", m.keyModal.err)
	}
	if info, err := os.Stat(filepath.Join(dir, "oauth-notices.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("notice permissions: %v %v", info, err)
	}
}

func TestOAuthAccountChangesRefusedWhileTurnActive(t *testing.T) {
	dir := t.TempDir()
	storedTestOAuth(t, dir)
	m := newProvidersUI(t, dir, "https://example.invalid/v1")
	p := mustChatGPTProvider(t)
	m.conn.ProviderCanonical, m.conn.Provider = p.Name, p.DisplayName
	m.openProviderAuth(p)
	m.working = true
	cmd := m.updateKeyModal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil || !strings.Contains(m.keyModal.err, "Cancel the active run") {
		t.Fatalf("account changes allowed during a turn: %q", m.keyModal.err)
	}
}
