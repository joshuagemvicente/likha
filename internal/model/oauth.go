package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
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
	"sync/atomic"
	"time"
)

const (
	oauthScope         = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	oauthAuthorizeURL  = ChatGPTIssuer + "/api/accounts/authorize"
	oauthTokenURL      = ChatGPTIssuer + "/api/accounts/oauth/token"
	oauthLoginTimeout  = 5 * time.Minute
	oauthHTTPTimeout   = 30 * time.Second
	oauthTokenLifetime = time.Hour
)

// ErrPlanScopeRequired means identity was verified, but inference was not
// authorized. BrowserLogin returns the verified registration alongside this
// error, so it can be retained without making it an active inference account.
var ErrPlanScopeRequired = errors.New("ChatGPT plan usage is not enabled; enable ChatGPT plan usage in account settings or configure an API-key provider")

// TokenSet contains token-endpoint data and the verified OIDC registration.
// AccountID is retained for legacy callers only; SIWC never derives identity
// from an access token or an organization claim.
type TokenSet struct {
	IDToken       string
	AccessToken   string
	RefreshToken  string
	Expires       time.Time
	AccountID     string
	Issuer        string
	Subject       string
	Email         string
	ClientID      string
	HostID        string
	TokenType     string
	Scopes        []string
	scopeReturned bool
}

// Credentials converts a validated TokenSet to a private storage record.
func (t TokenSet) Credentials() OAuthCredentials {
	c := OAuthCredentials{
		Refresh: t.RefreshToken, Access: t.AccessToken, AccountID: t.AccountID,
		Issuer: t.Issuer, Subject: t.Subject, Email: t.Email, ClientID: t.ClientID,
		HostID: t.HostID, IDToken: t.IDToken, TokenType: t.TokenType,
		Scopes: append([]string(nil), t.Scopes...),
	}
	if !t.Expires.IsZero() {
		c.Expires = t.Expires.UnixMilli()
	}
	return c
}

// BrowserLoginOptions describes one pending attempt, never an active account.
// Zero Credentials registers a new account. A retained registration reuses its
// issued ID and host ID; HostID may be omitted only for a returning account.
// HTTPClient is a transport seam, not an issuer override. Public origins stay
// fixed even when a test RoundTripper routes requests to a local fixture.
type BrowserLoginOptions struct {
	HostID         string
	Credentials    OAuthCredentials
	Port           int
	OpenBrowser    func(string) error
	OnBrowserError func(string, error)
	HTTPClient     *http.Client
}

// NewPKCE returns a fresh RFC 7636 S256 verifier/challenge pair.
func NewPKCE() (verifier, challenge string, err error) {
	verifier, err = RandomState()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// RandomState returns a base64url-encoded, 256-bit random value. Login state,
// nonce, and PKCE use independent calls.
func RandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("could not generate sign-in entropy")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validIssuedClientID(id string) bool {
	if id == "" || id == ChatGPTClientID || id == "app_EMoamEEZ73f0CkXaXp7hrann" || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func loginRegistration(options BrowserLoginOptions) (clientID, hostID string, returning bool, err error) {
	old := options.Credentials
	returning = old.Registered()
	if !returning && (old.ClientID != "" || old.Subject != "" || old.Issuer != "" || old.HostID != "" ||
		old.Access != "" || old.Refresh != "" || old.IDToken != "" || old.AccountID != "" || old.Email != "" ||
		old.TokenType != "" || old.Expires != 0 || len(old.Scopes) != 0) {
		return "", "", false, errors.New("legacy or incomplete ChatGPT credentials require a fresh sign-in")
	}
	hostID = options.HostID
	if returning && hostID == "" {
		hostID = old.HostID
	}
	if strings.TrimSpace(hostID) == "" || hostID != strings.TrimSpace(hostID) || len(hostID) > 1024 || strings.ContainsAny(hostID, "\r\n\x00") {
		return "", "", false, errors.New("a stable host identity is required before ChatGPT sign-in")
	}
	clientID = ChatGPTClientID
	if returning {
		if old.Issuer != ChatGPTIssuer || !validIssuedClientID(old.ClientID) || old.HostID != hostID {
			return "", "", false, errors.New("ChatGPT registration does not match this issuer and host")
		}
		clientID = old.ClientID
	}
	return clientID, hostID, returning, nil
}

func authorizationURL(options BrowserLoginOptions, clientID, hostID, redirectURI, challenge, state, nonce string, returning bool) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"scope": {oauthScope}, "resource": {ChatGPTResource}, "ext_agent_host_id": {hostID},
		"state": {state}, "nonce": {nonce}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	if returning {
		if options.Credentials.IDToken != "" {
			q.Set("id_token_hint", options.Credentials.IDToken)
		}
		if options.Credentials.Email != "" {
			q.Set("login_hint", options.Credentials.Email)
		}
	} else {
		q.Set("agent_name_hint", "Likha")
	}
	return oauthAuthorizeURL + "?" + q.Encode()
}

func manualAuthorizationURL(authorize string) string {
	u, _ := url.Parse(authorize) // constructed locally, not supplied by the caller
	q := u.Query()
	q.Del("id_token_hint")
	u.RawQuery = q.Encode()
	return u.String()
}

func openBrowserCommand() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "rundll32"
	default:
		return "xdg-open"
	}
}

func openSystemBrowser(ctx context.Context, u string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{u}
	if runtime.GOOS == "windows" {
		args = []string{"url.dll,FileProtocolHandler", u}
	}
	command := exec.CommandContext(ctx, openBrowserCommand(), args...)
	command.WaitDelay = time.Second
	return command.Run()
}

type oauthCallback struct {
	code     string
	clientID string
	err      error
}

func callbackHandler(state, clientID, callbackHost string, returning bool, results chan<- oauthCallback) http.Handler {
	var consumed atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.URL.EscapedPath() != "/auth/callback" || r.Host != callbackHost {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "This callback requires GET.", http.StatusMethodNotAllowed)
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, "Invalid callback parameters.", http.StatusBadRequest)
			return
		}
		for _, values := range q {
			if len(values) != 1 {
				http.Error(w, "Duplicate callback parameters.", http.StatusBadRequest)
				return
			}
		}
		// Unsolicited callbacks, including errors, must not abort the real attempt.
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid sign-in state.", http.StatusBadRequest)
			return
		}
		result := oauthCallback{code: q.Get("code"), clientID: clientID}
		if oauthErr := q.Get("error"); oauthErr != "" {
			if oauthErr == "access_denied" {
				result.err = fmt.Errorf("%w (access_denied)", ErrPlanScopeRequired)
			} else {
				result.err = &OAuthError{Operation: "authorization", Code: safeOAuthCode(oauthErr)}
			}
		} else if result.code == "" {
			result.err = &OAuthError{Operation: "authorization", Code: "invalid_response"}
		} else if returning {
			if returned, present := q["client_id"]; present && returned[0] != clientID {
				result.err = &OAuthError{Operation: "authorization", Code: "client_mismatch"}
			}
		} else {
			result.clientID = q.Get("client_id")
			if !validIssuedClientID(result.clientID) {
				result.err = &OAuthError{Operation: "authorization", Code: "incomplete_registration"}
			}
		}
		if !consumed.CompareAndSwap(false, true) {
			http.Error(w, "This sign-in callback was already received.", http.StatusConflict)
			return
		}
		if result.err != nil {
			http.Error(w, "Sign-in was not completed. Return to Likha.", http.StatusBadRequest)
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "Return to Likha; verifying sign-in.")
		}
		// Flush the neutral response before the main flow can close the server.
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Buffered, one-shot, non-blocking delivery also handles cancellation
		// racing a delayed callback without stranding an HTTP handler.
		select {
		case results <- result:
		default:
		}
	})
}

// BrowserLogin performs official SIWC registration/reauthorization. The
// listener is loopback-only, bounded, single-use, and closed on every return.
// No credentials may be activated until the ID token and grant are validated.
func BrowserLogin(ctx context.Context, options BrowserLoginOptions) (TokenSet, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthLoginTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return TokenSet{}, err
	}
	clientID, hostID, returning, err := loginRegistration(options)
	if err != nil {
		return TokenSet{}, err
	}
	registration := pendingOAuthRegistration{clientID: clientID, hostID: hostID, returning: returning}
	for attempt := 0; attempt < 2; attempt++ {
		tokens, err := browserLoginAttempt(ctx, options, &registration)
		var failure *OAuthError
		if attempt == 0 && errors.As(err, &failure) && failure.Operation == "code exchange" && failure.Code == "invalid_grant" {
			// The code is unusable, not the newly issued registration. Start one
			// fresh authorization using that ID, never dynamic_agent_client again.
			// The previous listener is closed before a new attempt is started.
			registration.returning = true
			continue
		}
		return tokens, err
	}
	return TokenSet{}, &OAuthError{Operation: "code exchange", Code: "invalid_grant"}
}

type pendingOAuthRegistration struct {
	clientID  string
	hostID    string
	returning bool
}

func browserLoginAttempt(ctx context.Context, options BrowserLoginOptions, registration *pendingOAuthRegistration) (TokenSet, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return TokenSet{}, err
	}
	clientID, hostID, returning := registration.clientID, registration.hostID, registration.returning
	verifier, challenge, err := NewPKCE()
	if err != nil {
		return TokenSet{}, err
	}
	state, err := RandomState()
	if err != nil {
		return TokenSet{}, err
	}
	nonce, err := RandomState()
	if err != nil {
		return TokenSet{}, err
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(options.Port)))
	if err != nil {
		return TokenSet{}, fmt.Errorf("could not open ChatGPT loopback callback on port %d", options.Port)
	}
	defer ln.Close()
	callbackHost := ln.Addr().String()
	redirectURI := "http://" + callbackHost + "/auth/callback"
	results := make(chan oauthCallback, 1)
	srv := &http.Server{
		Handler:           callbackHandler(state, clientID, callbackHost, returning, results),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	defer srv.Close()
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case serveErr <- errors.New("ChatGPT loopback callback stopped unexpectedly"):
			default:
			}
		}
	}()

	httpClient := oauthHTTPClient(ctx, options.HTTPClient)
	provider, err := discoverOAuthProvider(ctx, httpClient)
	if err != nil {
		return TokenSet{}, err
	}
	authorize := authorizationURL(options, clientID, hostID, redirectURI, challenge, state, nonce, returning)
	browserResult := make(chan error, 1)
	go func() {
		if options.OpenBrowser != nil {
			browserResult <- options.OpenBrowser(authorize)
		} else {
			browserResult <- openSystemBrowser(ctx, authorize)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return TokenSet{}, ctx.Err()
		case err := <-serveErr:
			return TokenSet{}, err
		case err := <-browserResult:
			browserResult = nil
			if err != nil && options.OnBrowserError != nil {
				// The opener's error may contain the URL (and its ID-token hint).
				options.OnBrowserError(manualAuthorizationURL(authorize), errors.New("could not open the system browser"))
			}
		case result := <-results:
			if result.err != nil {
				return TokenSet{}, result.err
			}
			registration.clientID = result.clientID
			tokens, err := requestOAuthTokens(ctx, httpClient, url.Values{
				"grant_type": {"authorization_code"}, "client_id": {result.clientID}, "code": {result.code},
				"code_verifier": {verifier}, "redirect_uri": {redirectURI}, "resource": {ChatGPTResource},
			}, "code exchange")
			if err != nil {
				return TokenSet{}, err
			}
			identity, err := verifyOAuthIdentity(ctx, provider, tokens.IDToken, result.clientID, nonce, options.Credentials.Subject)
			if err != nil {
				return TokenSet{}, err
			}
			tokens.Issuer, tokens.Subject, tokens.Email = identity.issuer, identity.subject, identity.email
			if !identity.emailPresent && returning {
				tokens.Email = options.Credentials.Email
			}
			tokens.ClientID, tokens.HostID = result.clientID, hostID
			if err := ctx.Err(); err != nil {
				return TokenSet{}, err
			}
			if !tokens.Credentials().HasPlanScope() {
				return tokens, ErrPlanScopeRequired
			}
			return tokens, nil
		}
	}
}
