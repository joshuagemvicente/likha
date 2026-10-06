package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"likha/internal/model"
	"likha/internal/providers"
)

var browserLoginFunc = model.BrowserLogin
var oauthModelsFunc = func(ctx context.Context, client *model.Client) ([]model.ModelDetails, error) {
	return client.Models(ctx)
}
var openLoginBrowser = func(raw string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", raw).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", raw).Run()
	default:
		return exec.Command("xdg-open", raw).Run()
	}
}

func validateOAuthEndpoint(p model.Provider, endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = os.Getenv("LIKHA_ENDPOINT")
	}
	if endpoint = strings.TrimSpace(endpoint); endpoint != "" && strings.TrimRight(endpoint, "/") != strings.TrimRight(p.BaseURL, "/") {
		return errors.New("ChatGPT authorization only supports the official public OpenAI API endpoint; remove --endpoint and LIKHA_ENDPOINT (API keys are configured separately with --provider openai)")
	}
	return nil
}

func oauthSetupRequired(p model.Provider, stateDir string, interactive bool) (bool, error) {
	_, signedIn, err := providers.StoredOAuth(stateDir, p.Name)
	if err != nil {
		return false, err
	}
	if signedIn {
		return false, nil
	}
	if interactive {
		return true, nil
	}
	return false, fmt.Errorf("provider %s requires browser authorization (legacy Codex logins are unsupported); run likha --provider %s --login, or run Likha in an interactive terminal to sign in automatically", p.Name, p.Name)
}

func bindOAuthStorage(client *model.Client, stateDir, provider string) {
	client.SetOAuthRefresher(func(ctx context.Context, expected model.OAuthCredentials, force bool) (model.OAuthCredentials, error) {
		return providers.RefreshOAuth(ctx, stateDir, provider, expected, force,
			func(ctx context.Context, old model.OAuthCredentials) (model.OAuthCredentials, error) {
				return model.RefreshCredentials(ctx, nil, old)
			})
	})
}

// discoverOAuthModel does not use public/keyless discovery or a curated list.
// An explicit model must belong to this account; otherwise the first eligible
// reported model is the default.
func discoverOAuthModel(ctx context.Context, client *model.Client, requested string) (string, map[string]int64, []model.ModelDetails, error) {
	details, err := oauthModelsFunc(ctx, client)
	if err != nil {
		return requested, nil, details, err
	}
	windows := make(map[string]int64)
	first, found := "", false
	for _, detail := range details {
		if detail.ID == "" {
			continue
		}
		if first == "" {
			first = detail.ID
		}
		found = found || detail.ID == requested
		if detail.ContextWindow > 0 {
			windows[detail.ID] = detail.ContextWindow
		}
	}
	if first == "" {
		return requested, nil, details, errors.New("this ChatGPT account reports no eligible models; reconnect with --login or /providers chatgpt")
	}
	if requested != "" && !found {
		return requested, windows, details, fmt.Errorf("model %q is not available to this ChatGPT account; choose an eligible model with /models or omit --model", requested)
	}
	if requested == "" {
		requested = first
	}
	return requested, windows, details, nil
}

func safeLoginURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	q := u.Query()
	for _, key := range []string{"id_token_hint", "id_token", "access_token", "refresh_token"} {
		q.Del(key)
	}
	u.RawQuery, u.Fragment = q.Encode(), ""
	return u.String()
}

func browserLoginFlow(providerName, stateDir string, stdout, stderr io.Writer) int {
	p, ok := model.LookupProvider(providerName)
	if !ok {
		fmt.Fprintf(stderr, "likha: unknown provider %q (see --help)\n", providerName)
		return 2
	}
	if p.Auth != model.AuthOAuth {
		fmt.Fprintln(stderr, "likha: --login is for browser authorization; use --provider chatgpt (API-key providers use --api-key)")
		return 2
	}
	host, err := providers.EnsureOAuthHost(stateDir)
	if err != nil {
		fmt.Fprintf(stderr, "likha: prepare login storage: %v\n", err)
		return 1
	}
	accounts, active, err := providers.OAuthAccounts(stateDir, p.Name)
	if err != nil {
		fmt.Fprintf(stderr, "likha: read saved accounts: %v\n", err)
		return 1
	}
	var registration model.OAuthCredentials
	for _, account := range accounts {
		if account.ClientID == active {
			registration = account
			break
		}
	}
	interrupt, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupt, 5*time.Minute)
	defer cancel()
	fmt.Fprintln(stdout, "Opening your browser for ChatGPT sign-in and consent…")
	ts, err := browserLoginFunc(ctx, model.BrowserLoginOptions{
		HostID: host, Credentials: registration, Port: 0, OpenBrowser: openLoginBrowser,
		OnBrowserError: func(raw string, _ error) {
			fmt.Fprintln(stdout, "Couldn't open the browser automatically. Open this link to continue:")
			if safe := safeLoginURL(raw); safe != "" {
				fmt.Fprintln(stdout, safe)
			}
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "likha: browser sign-in: %v\n", err)
		return 1
	}
	creds := ts.Credentials()
	if !creds.Registered() || !creds.HasPlanScope() {
		fmt.Fprintln(stderr, "likha: browser sign-in did not validate account and shared ChatGPT plan access; nothing was stored")
		return 1
	}
	client, err := model.NewOAuth(p.BaseURL, "", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		fmt.Fprintf(stderr, "likha: configure ChatGPT: %v\n", err)
		return 1
	}
	client.SetOAuthRefresher(func(ctx context.Context, expected model.OAuthCredentials, force bool) (model.OAuthCredentials, error) {
		if err := ctx.Err(); err != nil {
			return model.OAuthCredentials{}, err
		}
		if force || expected.Access == "" || expected.Expires <= time.Now().Add(30*time.Second).UnixMilli() {
			return model.OAuthCredentials{}, errors.New("ChatGPT authorization needs reconnecting; run --login again")
		}
		return expected, nil
	})
	fmt.Fprintln(stdout, "Checking models available to this account…")
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	_, _, _, err = discoverOAuthModel(checkCtx, client, "")
	checkCancel()
	if err != nil {
		fmt.Fprintf(stderr, "likha: verify ChatGPT models: %v (nothing was stored)\n", err)
		return 1
	}
	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "likha: browser sign-in cancelled: %v (nothing was stored)\n", err)
		return 1
	}
	if err := providers.StoreOAuth(stateDir, p.Name, creds); err != nil {
		fmt.Fprintf(stderr, "likha: store login: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "You're using your ChatGPT plan. Login stored for later runs.")
	fmt.Fprintln(stdout, "Usage shares your ChatGPT plan allowance and limits; credits only if opted in in ChatGPT Settings. API-key billing is separate.")
	fmt.Fprintln(stdout, "Usage: https://chatgpt.com/settings/usage")
	return 0
}
