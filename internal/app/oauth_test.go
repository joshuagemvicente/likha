package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"likha/internal/model"
	"likha/internal/providers"
)

func appOAuthCredentials(t *testing.T, dir, clientID string) model.OAuthCredentials {
	t.Helper()
	host, err := providers.EnsureOAuthHost(dir)
	if err != nil {
		t.Fatal(err)
	}
	return model.OAuthCredentials{
		Issuer: model.ChatGPTIssuer, Subject: "subject-" + clientID, Email: "user@example.test",
		ClientID: clientID, HostID: host, IDToken: "verified-id-token", TokenType: "Bearer",
		Access: "access-" + clientID, Refresh: "refresh-" + clientID, Expires: time.Now().Add(time.Hour).UnixMilli(),
		Scopes: []string{"openid", "email", "offline_access", model.ChatGPTPlanScope},
	}
}

func appOAuthTokenSet(c model.OAuthCredentials) model.TokenSet {
	return model.TokenSet{
		Issuer: c.Issuer, Subject: c.Subject, Email: c.Email, ClientID: c.ClientID, HostID: c.HostID,
		AccessToken: c.Access, RefreshToken: c.Refresh, IDToken: c.IDToken, TokenType: c.TokenType,
		Expires: time.UnixMilli(c.Expires), Scopes: append([]string(nil), c.Scopes...),
	}
}

func stubAppBrowser(t *testing.T, fn func(context.Context, model.BrowserLoginOptions) (model.TokenSet, error)) {
	t.Helper()
	old := browserLoginFunc
	browserLoginFunc = fn
	t.Cleanup(func() { browserLoginFunc = old })
}

func stubAppOAuthModels(t *testing.T, fn func(context.Context, *model.Client) ([]model.ModelDetails, error)) {
	t.Helper()
	old := oauthModelsFunc
	oauthModelsFunc = fn
	t.Cleanup(func() { oauthModelsFunc = old })
}

func TestDeviceLoginFlagDeprecatedWithoutAuthOrRepositoryAccess(t *testing.T) {
	stubAppBrowser(t, func(context.Context, model.BrowserLoginOptions) (model.TokenSet, error) {
		t.Fatal("deprecated flag reached auth network")
		return model.TokenSet{}, nil
	})
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--provider", "chatgpt", "--device-login", "/not/a/repository"}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "no longer supported") || !strings.Contains(stderr.String(), "--provider chatgpt --login") {
		t.Fatalf("deprecated flag: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestLoginFlagAutomaticallyLaunchesWithoutTTYOrRepository(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LIKHA_STATE_DIR", dir)
	t.Setenv("LIKHA_PROVIDER", "")
	t.Setenv("LIKHA_ENDPOINT", "")
	creds := appOAuthCredentials(t, dir, "issued-cli-login")
	opens := 0
	oldOpen := openLoginBrowser
	openLoginBrowser = func(raw string) error {
		opens++
		if !strings.Contains(raw, "auth.openai.com") {
			t.Fatal("browser launcher received the wrong origin")
		}
		return nil
	}
	t.Cleanup(func() { openLoginBrowser = oldOpen })
	stubAppBrowser(t, func(_ context.Context, options model.BrowserLoginOptions) (model.TokenSet, error) {
		if options.HostID != creds.HostID || options.Port != 0 || options.Credentials.Registered() {
			t.Fatalf("new login did not use persisted host and ephemeral callback: %+v", options)
		}
		if err := options.OpenBrowser("https://auth.openai.com/oauth/authorize?state=test"); err != nil {
			return model.TokenSet{}, err
		}
		return appOAuthTokenSet(creds), nil
	})
	stubAppOAuthModels(t, func(_ context.Context, client *model.Client) ([]model.ModelDetails, error) {
		if client.Base() != model.ChatGPTResource || client.APIKey() != "" || client.Model() != "" {
			t.Fatal("login discovery used an API-key client or guessed model")
		}
		return []model.ModelDetails{{ID: "real-account-model"}}, nil
	})
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--login", filepath.Join(dir, "not-a-repository")}, &stdout, &stderr)
	if code != 0 || opens != 1 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "You're using your ChatGPT plan") || !strings.Contains(stdout.String(), "only if opted in") {
		t.Fatalf("browser login: code=%d opens=%d stdout=%q stderr=%q", code, opens, stdout.String(), stderr.String())
	}
	saved, signedIn, err := providers.StoredOAuth(dir, "chatgpt")
	if err != nil || !signedIn || !reflect.DeepEqual(saved, creds) {
		t.Fatalf("issued registration was not stored: signedIn=%v err=%v", signedIn, err)
	}
	if cfg, err := providers.LoadStoredConfig(dir); err != nil || cfg.Provider != "" {
		t.Fatalf("login unexpectedly changed the default provider: %+v %v", cfg, err)
	}
}

func TestLoginBrowserFailureDropsTokenHintFromFallback(t *testing.T) {
	dir := t.TempDir()
	creds := appOAuthCredentials(t, dir, "issued-manual-cli")
	stubAppBrowser(t, func(_ context.Context, options model.BrowserLoginOptions) (model.TokenSet, error) {
		options.OnBrowserError("https://auth.openai.com/oauth/authorize?state=opaque&id_token_hint=SECRET_HINT", errors.New("launcher unavailable"))
		return appOAuthTokenSet(creds), nil
	})
	stubAppOAuthModels(t, func(context.Context, *model.Client) ([]model.ModelDetails, error) {
		return []model.ModelDetails{{ID: "account-model"}}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := browserLoginFlow("chatgpt", dir, &stdout, &stderr); code != 0 {
		t.Fatalf("manual fallback did not complete: %q", stderr.String())
	}
	if text := stdout.String() + stderr.String(); !strings.Contains(text, "state=opaque") || strings.Contains(text, "SECRET_HINT") || strings.Contains(text, "id_token_hint") {
		t.Fatalf("unsafe manual fallback: %q", text)
	}
}

func TestLoginRetainsSelectedRegistration(t *testing.T) {
	dir := t.TempDir()
	creds := appOAuthCredentials(t, dir, "issued-reconnect-cli")
	if err := providers.StoreOAuth(dir, "chatgpt", creds); err != nil {
		t.Fatal(err)
	}
	stubAppBrowser(t, func(_ context.Context, options model.BrowserLoginOptions) (model.TokenSet, error) {
		if !reflect.DeepEqual(options.Credentials, creds) || options.HostID != creds.HostID {
			t.Fatal("reconnect discarded the selected issued registration")
		}
		return appOAuthTokenSet(creds), nil
	})
	stubAppOAuthModels(t, func(context.Context, *model.Client) ([]model.ModelDetails, error) {
		return []model.ModelDetails{{ID: "real-model"}}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := browserLoginFlow("chatgpt", dir, &stdout, &stderr); code != 0 {
		t.Fatalf("reconnect: %q", stderr.String())
	}
}

func TestOAuthStartupUsesAccountDiscoveryAndFirstEligibleDefault(t *testing.T) {
	stubAppOAuthModels(t, func(context.Context, *model.Client) ([]model.ModelDetails, error) {
		return []model.ModelDetails{{ID: "first-account-model", ContextWindow: 64000}, {ID: "another-account-model"}}, nil
	})
	name, windows, _, err := discoverOAuthModel(context.Background(), nil, "")
	if err != nil || name != "first-account-model" || windows[name] != 64000 {
		t.Fatalf("authenticated default = %q windows=%v err=%v", name, windows, err)
	}
	if _, _, _, err := discoverOAuthModel(context.Background(), nil, "unavailable-model"); err == nil || !strings.Contains(err.Error(), "not available to this ChatGPT account") {
		t.Fatalf("explicit unavailable model was accepted: %v", err)
	}
}

func TestOAuthMissingAndLegacyStartupRequestsBrowserSetup(t *testing.T) {
	p, _ := model.LookupProvider("chatgpt")
	for _, legacy := range []bool{false, true} {
		dir := t.TempDir()
		if legacy {
			if err := os.WriteFile(providers.KeyFilePath(dir), []byte(`{"chatgpt":{"type":"oauth","access":"legacy","refresh":"legacy","expires":9999999999999}}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		needed, err := oauthSetupRequired(p, dir, true)
		if err != nil || !needed {
			t.Fatalf("interactive startup did not select browser onboarding (legacy=%v): %v", legacy, err)
		}
		needed, err = oauthSetupRequired(p, dir, false)
		if needed || err == nil || !strings.Contains(err.Error(), "--provider chatgpt --login") || strings.Contains(err.Error(), "--device-login") {
			t.Fatalf("headless startup did not offer official browser login (legacy=%v): %v", legacy, err)
		}
	}
}

func TestOAuthCustomEndpointRejectedBeforeLogin(t *testing.T) {
	t.Setenv("LIKHA_STATE_DIR", t.TempDir())
	t.Setenv("LIKHA_ENDPOINT", "")
	stubAppBrowser(t, func(context.Context, model.BrowserLoginOptions) (model.TokenSet, error) {
		t.Fatal("custom OAuth endpoint reached browser authorization")
		return model.TokenSet{}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--provider", "chatgpt", "--endpoint", "https://example.invalid/v1", "--login"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "official public OpenAI API") {
		t.Fatalf("custom endpoint: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestLoginModelFailureKeepsPreviousAccountActive(t *testing.T) {
	dir := t.TempDir()
	old := appOAuthCredentials(t, dir, "issued-existing-cli")
	fresh := appOAuthCredentials(t, dir, "issued-failed-cli")
	if err := providers.StoreOAuth(dir, "chatgpt", old); err != nil {
		t.Fatal(err)
	}
	stubAppBrowser(t, func(context.Context, model.BrowserLoginOptions) (model.TokenSet, error) {
		return appOAuthTokenSet(fresh), nil
	})
	stubAppOAuthModels(t, func(context.Context, *model.Client) ([]model.ModelDetails, error) {
		return nil, errors.New("models access denied")
	})
	var stdout, stderr bytes.Buffer
	if code := browserLoginFlow("chatgpt", dir, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "nothing was stored") || strings.Contains(stdout.String(), "You're using your ChatGPT plan") {
		t.Fatalf("failed discovery: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	accounts, active, err := providers.OAuthAccounts(dir, "chatgpt")
	if err != nil || active != old.ClientID || len(accounts) != 1 {
		t.Fatalf("failed login changed active account: active=%q count=%d err=%v", active, len(accounts), err)
	}
}
