package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// oauthScope is the OAuth scope requested at login: identity claims plus a
// refresh token.
const oauthScope = "openid profile email offline_access"

// oauthTokenLifetime is the validity assumed when a token response carries
// no expires_in.
const oauthTokenLifetime = 3600 * time.Second

// oauthHTTPTimeout bounds each token request when the caller passes no
// http.Client.
const oauthHTTPTimeout = 30 * time.Second

// TokenSet is one OAuth token response plus the parsed account identity.
type TokenSet struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
	// Expires is now + expires_in, defaulting to one hour when the response
	// carries no expires_in.
	Expires   time.Time
	AccountID string
}

// Credentials converts a TokenSet into storable OAuth credentials.
func (t TokenSet) Credentials() OAuthCredentials {
	c := OAuthCredentials{
		Refresh:   t.RefreshToken,
		Access:    t.AccessToken,
		AccountID: t.AccountID,
	}
	if !t.Expires.IsZero() {
		c.Expires = t.Expires.UnixMilli()
	}
	return c
}

// tokenResponse is the wire shape of an OAuth token endpoint reply.
type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// oauthError is the wire shape of a token endpoint error reply.
type oauthError struct {
	Error string `json:"error"`
}

// snippet shortens a raw response body for error messages.
func snippet(s string, limit int) string {
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

// tokenFromResponse decodes a successful token endpoint body into a TokenSet,
// applying the expires_in (defaulting to one hour) and resolving the account
// identity from the returned tokens.
func tokenFromResponse(body []byte) (TokenSet, error) {
	var resp tokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return TokenSet{}, fmt.Errorf("decode token response: %w", err)
	}
	lifetime := oauthTokenLifetime
	if resp.ExpiresIn > 0 {
		lifetime = time.Duration(resp.ExpiresIn) * time.Second
	}
	ts := TokenSet{
		IDToken:      resp.IDToken,
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		Expires:      time.Now().Add(lifetime),
	}
	ts.AccountID = AccountIDFromToken(resp.IDToken, resp.AccessToken)
	return ts, nil
}

// parseTokenEndpoint consumes a token endpoint response: non-2xx fails with
// the JSON error field when present, otherwise the body becomes a TokenSet.
func parseTokenEndpoint(resp *http.Response) (TokenSet, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenSet{}, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e oauthError
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			return TokenSet{}, fmt.Errorf("oauth token endpoint returned %d: %s", resp.StatusCode, e.Error)
		}
		return TokenSet{}, fmt.Errorf("oauth token endpoint returned %d: %s", resp.StatusCode, snippet(strings.TrimSpace(string(body)), 200))
	}
	return tokenFromResponse(body)
}

// NewPKCE returns a fresh S256 verifier/challenge pair. The verifier is 43
// characters from the RFC 7636 unreserved set; challenge = base64url(SHA256(verifier)).
func NewPKCE() (verifier, challenge string, err error) {
	// 64-symbol alphabet so a masked random byte selects a symbol without
	// modulo bias. 43 characters is the minimum S256 entropy per RFC 7636.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	raw := make([]byte, 43)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("pkce entropy: %w", err)
	}
	verifierBytes := make([]byte, len(raw))
	for i, r := range raw {
		verifierBytes[i] = alphabet[r&63]
	}
	verifier = string(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// AuthorizeURL builds the OAuth authorization URL: GET {issuer}/oauth/authorize
// with the standard ChatGPT/Codex PKCE parameters and the given state.
func AuthorizeURL(issuer, clientID, redirectURI, verifier, state string) string {
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {clientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {oauthScope},
		"code_challenge":             {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {ChatGPTOriginator},
		"state":                      {state},
	}
	return strings.TrimSuffix(issuer, "/") + "/oauth/authorize?" + q.Encode()
}

// RandomState returns a base64url-encoded 32-byte random state token.
func RandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("state entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenURL resolves the standard token endpoint relative to the issuer.
func tokenURL(issuer string) string { return strings.TrimSuffix(issuer, "/") + "/oauth/token" }

// postForm issues the form-encoded token request shared by both grants.
func postForm(ctx context.Context, httpClient *http.Client, tokenEndpoint string, form url.Values) (TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return TokenSet{}, fmt.Errorf("token request: %w", err)
	}
	return parseTokenEndpoint(resp)
}

// ExchangeCode exchanges an authorization code for tokens:
// POST {issuer}/oauth/token with grant_type=authorization_code.
func ExchangeCode(ctx context.Context, httpClient *http.Client, issuer, clientID, code, verifier, redirectURI string) (TokenSet, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: oauthHTTPTimeout}
	}
	return postForm(ctx, httpClient, tokenURL(issuer), url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
}

// RefreshTokens rotates a token set: POST {issuer}/oauth/token with
// grant_type=refresh_token.
func RefreshTokens(ctx context.Context, httpClient *http.Client, issuer, clientID, refreshToken string) (TokenSet, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: oauthHTTPTimeout}
	}
	return postForm(ctx, httpClient, tokenURL(issuer), url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	})
}

// chatgptClaimLocations is the precedence order of claim locations holding
// the ChatGPT account id inside one token payload.
type chatgptClaims struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	Auth             *struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
	Organizations []struct {
		ID string `json:"id"`
	} `json:"organizations"`
}

// AccountIDFromToken extracts the ChatGPT account id from a JWT (id_token
// preferred, then access_token): claims.chatgpt_account_id, else the
// https://api.openai.com/auth claim, else organizations[0].id. Malformed
// JWTs yield "" without panicking.
func AccountIDFromToken(idToken, accessToken string) string {
	for _, tok := range []string{idToken, accessToken} {
		if tok == "" {
			continue
		}
		if id := accountIDFromToken(tok); id != "" {
			return id
		}
	}
	return ""
}

// accountIDFromToken decodes one token's middle segment and applies the three
// claim locations in precedence order.
func accountIDFromToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims chatgptClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return ""
	}
	if claims.ChatGPTAccountID != "" {
		return claims.ChatGPTAccountID
	}
	if claims.Auth != nil && claims.Auth.ChatGPTAccountID != "" {
		return claims.Auth.ChatGPTAccountID
	}
	if len(claims.Organizations) > 0 {
		return claims.Organizations[0].ID
	}
	return ""
}

// openBrowserCommand is the OS command that opens a URL in a browser.
func openBrowserCommand() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// BrowserLogin runs the loopback browser authorization flow: it binds
// 127.0.0.1:{port}, opens the authorize URL, accepts the callback, exchanges
// the code, and always shuts the listener down. The caller's context bounds
// the whole wait.
func BrowserLogin(ctx context.Context, issuer, clientID string, port int, openBrowser func(string) error, httpClient *http.Client) (TokenSet, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: oauthHTTPTimeout}
	}
	verifier, _, err := NewPKCE()
	if err != nil {
		return TokenSet{}, err
	}
	state, err := RandomState()
	if err != nil {
		return TokenSet{}, err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return TokenSet{}, fmt.Errorf("listen on %d: %w", port, err)
	}
	defer ln.Close()

	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/callback":
			q := r.URL.Query()
			if e := q.Get("error"); e != "" {
				http.Error(w, "authorization failed: "+e, http.StatusBadRequest)
				errCh <- errors.New(e)
				return
			}
			// Constant-time state check so the loopback endpoint cannot act
			// as a timing oracle for the one-time state value.
			if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
				http.Error(w, "state mismatch", http.StatusBadRequest)
				errCh <- errors.New("state mismatch in callback")
				return
			}
			code := q.Get("code")
			if code == "" {
				http.Error(w, "missing authorization code", http.StatusBadRequest)
				errCh <- errors.New("callback carried no code")
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<html><body><p>Login complete. You can close this window.</p></body></html>")
			codeCh <- code
		case "/cancel":
			http.Error(w, "login cancelled", http.StatusBadRequest)
			errCh <- errors.New("login cancelled")
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if openBrowser == nil {
		openBrowser = func(u string) error { return exec.Command(openBrowserCommand(), u).Start() }
	}
	// A failure to open the browser never blocks the flow: the server is
	// already listening and the user can paste the URL manually.
	_ = openBrowser(AuthorizeURL(issuer, clientID, redirectURI, verifier, state))

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return TokenSet{}, err
	case <-ctx.Done():
		return TokenSet{}, ctx.Err()
	}
	return ExchangeCode(ctx, httpClient, issuer, clientID, code, verifier, redirectURI)
}

// deviceUserCodeRequest is the JSON body of the usercode request.
type deviceUserCodeRequest struct {
	ClientID string `json:"client_id"`
}

// deviceUserCodeResponse is the reply of the usercode endpoint. The real
// issuer encodes interval as a JSON string ("5"), other servers may use a
// number, so it decodes tolerantly.
type deviceUserCodeResponse struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	Interval     json.RawMessage `json:"interval"` // seconds; number or numeric string
}

// intervalSeconds decodes the usercode interval, accepting a JSON number or
// a numeric string; it returns 0 when absent or unusable.
func intervalSeconds(raw json.RawMessage) int64 {
	trimmed := strings.TrimSpace(string(raw))
	trimmed = strings.Trim(trimmed, `"`)
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// deviceAuthGrant is a successful deviceauth token poll.
type deviceAuthGrant struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

// DeviceLogin runs the headless device flow: request a user code, display it,
// poll until the authorization code arrives, then exchange it.
func DeviceLogin(ctx context.Context, issuer, clientID, userAgent string, showCode func(userCode, verifyURL string) error, httpClient *http.Client) (TokenSet, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: oauthHTTPTimeout}
	}
	base := strings.TrimSuffix(issuer, "/")

	// 1. Obtain the device code.
	body, err := json.Marshal(deviceUserCodeRequest{ClientID: clientID})
	if err != nil {
		return TokenSet{}, fmt.Errorf("encode usercode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/api/accounts/deviceauth/usercode", strings.NewReader(string(body)))
	if err != nil {
		return TokenSet{}, fmt.Errorf("build usercode request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return TokenSet{}, fmt.Errorf("usercode request: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if readErr != nil {
		return TokenSet{}, fmt.Errorf("read usercode response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return TokenSet{}, fmt.Errorf("usercode endpoint returned %d: %s", resp.StatusCode, snippet(strings.TrimSpace(string(payload)), 200))
	}
	var uc deviceUserCodeResponse
	if err := json.Unmarshal(payload, &uc); err != nil {
		return TokenSet{}, fmt.Errorf("decode usercode response: %w", err)
	}

	// 2. Show the code; a display error aborts the flow.
	if err := showCode(uc.UserCode, base+ChatGPTDevicePath); err != nil {
		return TokenSet{}, fmt.Errorf("show device code: %w", err)
	}

	interval := time.Duration(intervalSeconds(uc.Interval)) * time.Second
	if interval < time.Second {
		interval = time.Second
	}

	// 3. Poll until the authorization code is granted, honoring ctx between
	// polls and retrying only on 403/404 ("not finished yet").
	pollBody, err := json.Marshal(map[string]string{
		"device_auth_id": uc.DeviceAuthID,
		"user_code":      uc.UserCode,
	})
	if err != nil {
		return TokenSet{}, fmt.Errorf("encode device token request: %w", err)
	}
	for {
		pollReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/api/accounts/deviceauth/token", strings.NewReader(string(pollBody)))
		if err != nil {
			return TokenSet{}, fmt.Errorf("build device token request: %w", err)
		}
		pollReq.Header.Set("Content-Type", "application/json")
		pollReq.Header.Set("User-Agent", userAgent)
		pollResp, err := httpClient.Do(pollReq)
		if err != nil {
			return TokenSet{}, fmt.Errorf("device token poll: %w", err)
		}
		pollPayload, readErr := io.ReadAll(io.LimitReader(pollResp.Body, 1<<20))
		pollResp.Body.Close()
		if readErr != nil {
			return TokenSet{}, fmt.Errorf("read device token response: %w", readErr)
		}
		switch {
		case pollResp.StatusCode == http.StatusOK:
			// 4. Exchange the authorization code with the server-supplied
			// verifier.
			var grant deviceAuthGrant
			if err := json.Unmarshal(pollPayload, &grant); err != nil {
				return TokenSet{}, fmt.Errorf("decode device token response: %w", err)
			}
			return ExchangeCode(ctx, httpClient, issuer, clientID, grant.AuthorizationCode,
				grant.CodeVerifier, base+"/deviceauth/callback")
		case pollResp.StatusCode == http.StatusForbidden || pollResp.StatusCode == http.StatusNotFound:
			// The user has not completed the step yet; keep polling.
			select {
			case <-ctx.Done():
				return TokenSet{}, ctx.Err()
			case <-time.After(interval + 3*time.Second):
			}
		default:
			return TokenSet{}, fmt.Errorf("device token endpoint returned %d: %s",
				pollResp.StatusCode, snippet(strings.TrimSpace(string(pollPayload)), 200))
		}
	}
}
