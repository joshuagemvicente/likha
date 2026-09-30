package model

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// shortClient mirrors http.DefaultClient but refuses to hang; everything is
// loopback only.
var shortClient = &http.Client{Timeout: 5 * time.Second}

// builder builds an unsigned base64url JWT-shaped token whose payload carries
// the given claims JSON, so AccountIDFromToken exercises the real decoder
// without needing a real issuer signature.
func builder(claims string) string {
	return "sig." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".sig"
}

// requireEqual fails t when got != want.
func requireEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %#v want %#v", what, got, want)
	}
}

// requireForm asserts an application/x-www-form-urlencoded request body and
// returns its parsed fields.
func requireForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		t.Fatalf("content type: got %q", ct)
	}
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
	return r.PostForm
}

// fixtureClaims is the account id carried by the fixture id_token.
const fixtureClaims = `{"chatgpt_account_id":"acct-fixture"}`

// fixtureTokenBody is a canned OAuth token response whose id_token is a
// signed-shape (unsigned base64) JWT carrying the fixture account id.
var fixtureTokenBody = fmt.Sprintf(
	`{"id_token":%q,"access_token":%q,"refresh_token":%q,"expires_in":7200}`,
	builder(fixtureClaims), "tok.acc-tok.signed", "tok.rfr-tok.signed")

// requireStandardTokenReply asserts the token endpoint received exactly the
// wanted form fields, then replies with the fixture body.
func requireStandardTokenReply(t *testing.T, want url.Values) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		got := requireForm(t, r)
		if len(got) != len(want) {
			t.Fatalf("token form fields: got %v want %v", got, want)
		}
		for k, wantVals := range want {
			if len(got[k]) != 1 || got[k][0] != wantVals[0] {
				t.Fatalf("token form %s: got %v want %v", k, got[k], wantVals)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureTokenBody))
	}
}

func TestNewPKCE(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	aVerifier, aChallenge, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	requireEqual(t, "verifier length", len(aVerifier), 43)
	for _, c := range aVerifier {
		if !strings.ContainsRune(alphabet, c) {
			t.Fatalf("verifier rune %c outside the RFC 7636 unreserved set", c)
		}
	}
	sum := sha256.Sum256([]byte(aVerifier))
	requireEqual(t, "challenge", aChallenge, base64.RawURLEncoding.EncodeToString(sum[:]))

	bVerifier, bChallenge, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE second call: %v", err)
	}
	if aVerifier == bVerifier || aChallenge == bChallenge {
		t.Fatal("two NewPKCE calls returned identical values")
	}
}

func TestAuthorizeURL(t *testing.T) {
	got := AuthorizeURL(ChatGPTIssuer, ChatGPTClientID, "http://localhost:1455/auth/callback",
		"test-verifier-value", "test-state-value")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse authorize URL %q: %v", got, err)
	}
	if u.Scheme != "https" || u.Host != "auth.openai.com" || u.Path != "/oauth/authorize" {
		t.Fatalf("authorize endpoint wrong: %s", got)
	}
	q := u.Query()
	wantParams := map[string]string{
		"response_type":              "code",
		"client_id":                  ChatGPTClientID,
		"redirect_uri":               "http://localhost:1455/auth/callback",
		"scope":                      "openid profile email offline_access",
		"code_challenge_method":      "S256",
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
		"originator":                 ChatGPTOriginator,
		"state":                      "test-state-value",
	}
	for k, want := range wantParams {
		requireEqual(t, "authorize param "+k, q.Get(k), want)
	}
	if q.Get("code_challenge") == "" {
		t.Fatal("code_challenge missing")
	}
	// Single encoding: each parameter appears exactly once and no value is
	// percent-encoded twice.
	if strings.Count(got, "redirect_uri=") != 1 {
		t.Fatalf("redirect_uri emitted more than once: %s", got)
	}
	if strings.Contains(got, "%25") {
		t.Fatalf("unwanted double-encoding in %s", got)
	}
}

func TestExchangeCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(requireStandardTokenReply(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"the-code"},
		"redirect_uri":  {"http://localhost:1455/auth/callback"},
		"client_id":     {ChatGPTClientID},
		"code_verifier": {"the-verifier"},
	})))
	t.Cleanup(srv.Close)

	ts, err := ExchangeCode(context.Background(), shortClient, srv.URL, ChatGPTClientID,
		"the-code", "the-verifier", "http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	requireEqual(t, "access token", ts.AccessToken, "tok.acc-tok.signed")
	requireEqual(t, "refresh token", ts.RefreshToken, "tok.rfr-tok.signed")
	requireEqual(t, "account id", ts.AccountID, "acct-fixture")
	if ts.Expires.IsZero() {
		t.Fatal("Expires not set from expires_in")
	}
	if remaining := time.Until(ts.Expires); remaining < 7000*time.Second || remaining > 7300*time.Second {
		t.Fatalf("Expires not ~now+7200s: %v", ts.Expires)
	}
}

func TestExchangeCodeSurfacesErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"bad code"}`))
	}))
	t.Cleanup(srv.Close)
	_, err := ExchangeCode(context.Background(), shortClient, srv.URL, ChatGPTClientID, "c", "v", "http://localhost:1455/auth/callback")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error field not surfaced: %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("status not surfaced: %v", err)
	}
}

func TestRefreshTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(requireStandardTokenReply(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {"tok.rfr-tok.signed"},
		"client_id":     {ChatGPTClientID},
	})))
	t.Cleanup(srv.Close)

	ts, err := RefreshTokens(context.Background(), shortClient, srv.URL, ChatGPTClientID, "tok.rfr-tok.signed")
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	requireEqual(t, "access token", ts.AccessToken, "tok.acc-tok.signed")
	requireEqual(t, "account id", ts.AccountID, "acct-fixture")
	if ts.Expires.IsZero() {
		t.Fatal("Expires not set from expires_in")
	}
}

func TestTokenSetCredentials(t *testing.T) {
	expires := time.Now().Add(7200 * time.Second)
	ts := TokenSet{IDToken: "i", AccessToken: "a", RefreshToken: "r", Expires: expires, AccountID: "acct-x"}
	c := ts.Credentials()
	requireEqual(t, "refresh", c.Refresh, "r")
	requireEqual(t, "access", c.Access, "a")
	requireEqual(t, "account", c.AccountID, "acct-x")
	requireEqual(t, "expires unix ms", c.Expires, expires.UnixMilli())
}

func TestAccountIDFromToken(t *testing.T) {
	direct := builder(`{"chatgpt_account_id":"acct-direct","organizations":[{"id":"acct-org"}]}`)
	nested := builder(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-nested"},"organizations":[{"id":"acct-org"}]}`)
	orgOnly := builder(`{"organizations":[{"id":"acct-org"},{"id":"acct-org2"}]}`)

	requireEqual(t, "direct claim", AccountIDFromToken(direct, ""), "acct-direct")
	requireEqual(t, "nested claim", AccountIDFromToken(nested, ""), "acct-nested")
	requireEqual(t, "org fallback", AccountIDFromToken(orgOnly, ""), "acct-org")

	// Precedence within one token: direct > nested > organizations[0].
	all := builder(`{"chatgpt_account_id":"acct-direct","https://api.openai.com/auth":{"chatgpt_account_id":"acct-nested"},"organizations":[{"id":"acct-org"}]}`)
	requireEqual(t, "direct beats nested and org", AccountIDFromToken("", all), "acct-direct")
	requireEqual(t, "nested beats org", AccountIDFromToken("", nested), "acct-nested")

	// id_token preferred over access_token (different account ids).
	idTok := builder(`{"chatgpt_account_id":"from-id"}`)
	accTok := builder(`{"chatgpt_account_id":"from-access"}`)
	requireEqual(t, "id_token preferred", AccountIDFromToken(idTok, accTok), "from-id")
	// access_token used when id_token carries nothing.
	requireEqual(t, "access token fallback", AccountIDFromToken("", accTok), "from-access")

	requireEqual(t, "no usable claims", AccountIDFromToken(builder(`{"sub":"u"}`), ""), "")

	// Garbage tokens return "" and never panic.
	for _, bad := range []string{"", "not-a-jwt", "a.b", "h.###.s", "a.!!!.b", "...", "x.@@.y"} {
		requireEqual(t, "garbage token", AccountIDFromToken(bad, ""), "")
	}
	requireEqual(t, "both empty", AccountIDFromToken("", ""), "")
}

// freePort yields a port that binds cleanly, for the callback listener.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return port
}

// awaited asserts BrowserLogin ends on the expected terminal outcome within
// five seconds, then returns the TokenSet or the error.
func awaited(t *testing.T, done <-chan TokenSet, fail <-chan error) (TokenSet, error) {
	t.Helper()
	select {
	case ts := <-done:
		return ts, nil
	case err := <-fail:
		return TokenSet{}, err
	case <-time.After(5 * time.Second):
		t.Fatal("BrowserLogin did not return in time")
		return TokenSet{}, nil
	}
}

// requireRebindable asserts the loopback listener was shut down by checking
// the port binds again.
func requireRebindable(t *testing.T, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listener still open: %v", err)
	}
	_ = ln.Close()
}

// issuerPolicy is a fake OAuth issuer over httptest.
func issuerPolicy(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handle))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrowserLogin(t *testing.T) {
	port := freePort(t)
	callback := fmt.Sprintf("http://localhost:%d/auth/callback", port)

	authorizeSeen := make(chan *url.URL, 4)
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/authorize":
			authorizeSeen <- r.URL
			_, _ = w.Write([]byte("authorize page"))
		case r.URL.Path == "/oauth/token":
			got := requireForm(t, r)
			if got.Get("grant_type") != "authorization_code" {
				t.Errorf("grant_type: %q", got.Get("grant_type"))
			}
			if got.Get("code") != "good-code" {
				t.Errorf("code: %q", got.Get("code"))
			}
			if got.Get("code_verifier") == "" {
				t.Error("code_verifier missing")
			}
			if got.Get("redirect_uri") != callback {
				t.Errorf("redirect_uri: %q", got.Get("redirect_uri"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fixtureTokenBody))
		default:
			http.NotFound(w, r)
		}
	})

	done := make(chan TokenSet, 1)
	fail := make(chan error, 1)
	opened := make(chan string, 2)
	// The faked browser: record the authorize URL, then "navigate" to it so
	// the fake issuer sees the request.
	go func() {
		ts, err := BrowserLogin(context.Background(), srv.URL, ChatGPTClientID, port,
			func(u string) error {
				opened <- u
				resp, getErr := http.Get(u)
				if getErr == nil {
					resp.Body.Close()
				}
				return getErr
			}, shortClient)
		if err != nil {
			fail <- err
			return
		}
		done <- ts
	}()

	// The fake issuer captured the authorize URL: assert its shape and take
	// the state to echo back in the callback.
	var authorize *url.URL
	select {
	case authorize = <-authorizeSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("authorize URL not requested")
	}
	if authorize.Query().Get("client_id") != ChatGPTClientID {
		t.Fatalf("authorize client_id: %s", authorize)
	}
	authorizeURLString := <-opened
	requireEqual(t, "openBrowser URL equals authorize URL", authorizeURLString, fmt.Sprintf("%s/oauth/authorize?%s", srv.URL, authorize.RawQuery))
	state := authorize.Query().Get("state")
	if state == "" {
		t.Fatalf("authorize state empty: %s", authorize)
	}

	// Good callback: 200 + a user-facing "close this window" page.
	resp, err := http.Get(callback + "?code=good-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()
	requireEqual(t, "callback status", resp.StatusCode, http.StatusOK)

	ts, err := awaited(t, done, fail)
	if err != nil {
		t.Fatalf("BrowserLogin: %v", err)
	}
	requireEqual(t, "access token", ts.AccessToken, "tok.acc-tok.signed")
	requireEqual(t, "account id", ts.AccountID, "acct-fixture")

	// The listener is closed after return: the port binds again.
	requireRebindable(t, port)
}

// TestBrowserLoginWrongState starts one flow, hits /auth/callback with a bad
// state (400, flow fails) and verifies the port is freed afterwards.
func TestBrowserLoginWrongState(t *testing.T) {
	port := freePort(t)
	callback := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {
		// No browser actually navigates; the flow just waits.
	})
	done := make(chan TokenSet, 1)
	fail := make(chan error, 1)
	go func() {
		ts, err := BrowserLogin(context.Background(), srv.URL, ChatGPTClientID, port,
			func(string) error { return nil }, shortClient)
		if err != nil {
			fail <- err
			return
		}
		done <- ts
	}()

	resp, err := http.Get(callback + "?code=x&state=wrong")
	if err != nil {
		t.Fatalf("wrong-state request: %v", err)
	}
	resp.Body.Close()
	requireEqual(t, "wrong-state status", resp.StatusCode, http.StatusBadRequest)

	if _, err := awaited(t, done, fail); err == nil {
		t.Fatal("expected wrong-state callback to fail the flow")
	}
	requireRebindable(t, port)
}

// TestBrowserLoginProviderError: the callback carries error=...; the provider's
// message surfaces and the flow fails with 400.
func TestBrowserLoginProviderError(t *testing.T) {
	port := freePort(t)
	callback := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {})
	done := make(chan TokenSet, 1)
	fail := make(chan error, 1)
	go func() {
		_, err := BrowserLogin(context.Background(), srv.URL, ChatGPTClientID, port,
			func(string) error { return nil }, shortClient)
		if err != nil {
			fail <- err
		}
	}()
	resp, err := http.Get(callback + "?error=access_denied&state=x")
	if err != nil {
		t.Fatalf("error-param request: %v", err)
	}
	resp.Body.Close()
	requireEqual(t, "provider-error status", resp.StatusCode, http.StatusBadRequest)
	if _, err := awaited(t, done, fail); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("provider error not surfaced: %v", err)
	}
	requireRebindable(t, port)
}

// TestBrowserLoginCancelAndUnknownPaths: GET /cancel fails the login with
// "login cancelled"; other paths 404 without failing the flow.
func TestBrowserLoginCancelAndUnknownPaths(t *testing.T) {
	port := freePort(t)
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {})
	done := make(chan TokenSet, 1)
	fail := make(chan error, 1)
	go func() {
		_, err := BrowserLogin(context.Background(), srv.URL, ChatGPTClientID, port,
			func(string) error { return nil }, shortClient)
		if err != nil {
			fail <- err
		}
	}()

	// Unknown path 404s but does NOT kill the flow: the flow is still live
	// afterwards and produces the /cancel outcome we ask for next.
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/other", port))
	if err != nil {
		t.Fatalf("unknown-path request: %v", err)
	}
	resp.Body.Close()
	requireEqual(t, "unknown-path status", resp.StatusCode, http.StatusNotFound)

	cancelResp, err := http.Get(fmt.Sprintf("http://localhost:%d/cancel", port))
	if err != nil {
		t.Fatalf("cancel request: %v", err)
	}
	cancelResp.Body.Close()
	requireEqual(t, "cancel status", cancelResp.StatusCode, http.StatusBadRequest)
	if _, err := awaited(t, done, fail); err == nil || !strings.Contains(err.Error(), "login cancelled") {
		t.Fatalf("cancel not surfaced: %v", err)
	}
	requireRebindable(t, port)
}

// TestBrowserLoginContextDeadline exercises the nil-openBrowser default (no
// one visits the callback) and the context deadline: the flow aborts with a
// deadline error within the context window.
func TestBrowserLoginContextDeadline(t *testing.T) {
	port := freePort(t)
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {})
	// The openBrowser stub records that the login URL was prepared without
	// ever completing the flow.
	opened := make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var alternateOpenBrowser func(string) error = func(u string) error { opened <- u; return nil }
	_, err := BrowserLogin(ctx, srv.URL, ChatGPTClientID, port, alternateOpenBrowser, shortClient)
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("deadline error: %v", err)
	}
	select {
	case u := <-opened:
		if !strings.Contains(u, "/oauth/authorize?") {
			t.Fatalf("openBrowser URL: %s", u)
		}
	default:
		t.Fatal("authorize URL was never opened")
	}
	requireRebindable(t, port)
}

// TestBrowserLoginNilBrowserOpenScript exercises the nil-openBrowser default
// command selection without launching anything.
func TestBrowserLoginNilBrowserOpenScript(t *testing.T) {
	requireEqual(t, "open command on darwin", openBrowserCommand(), "open")
}

func TestDeviceLogin(t *testing.T) {
	const deviceAuthID = "dev-123"
	const userCode = "ABCD-1234"
	const authorizationCode = "dev-code"
	const codeVerifier = "dev-verifier"
	var mu sync.Mutex
	polls := 0
	pollGuard := func() int {
		mu.Lock()
		defer mu.Unlock()
		return polls
	}

	var srv *httptest.Server
	srv = issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/accounts/deviceauth/usercode":
			if ua := r.Header.Get("User-Agent"); ua != "lisa-test-agent" {
				t.Errorf("usercode User-Agent: %q", ua)
			}
			var req struct {
				ClientID string `json:"client_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode usercode body: %v", err)
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			if req.ClientID != ChatGPTClientID {
				t.Errorf("usercode client_id: %q", req.ClientID)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"device_auth_id":%q,"user_code":%q,"interval":1}`, deviceAuthID, userCode)))
		case r.URL.Path == "/api/accounts/deviceauth/token":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			var req struct {
				DeviceAuthID string `json:"device_auth_id"`
				UserCode     string `json:"user_code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
				req.DeviceAuthID != deviceAuthID || req.UserCode != userCode {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"bad device payload"}`))
				return
			}
			if n == 1 {
				// First poll: provider says not finished yet (404), flow must
				// keep polling rather than fail.
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"authorization_code":%q,"code_verifier":%q}`, authorizationCode, codeVerifier)))
		case r.URL.Path == "/oauth/token":
			got := requireForm(t, r)
			requireEqual(t, "device grant_type", got.Get("grant_type"), "authorization_code")
			requireEqual(t, "device code", got.Get("code"), authorizationCode)
			requireEqual(t, "device code_verifier (server-supplied)", got.Get("code_verifier"), codeVerifier)
			requireEqual(t, "device redirect_uri", got.Get("redirect_uri"), srv.URL+"/deviceauth/callback")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fixtureTokenBody))
		default:
			http.NotFound(w, r)
		}
	})

	var shownUser, shownURL string
	shownOnce := 0
	ts, err := DeviceLogin(context.Background(), srv.URL, ChatGPTClientID, "lisa-test-agent",
		func(userCodeArg, verifyURL string) error {
			shownUser, shownURL = userCodeArg, verifyURL
			shownOnce++
			return nil
		}, shortClient)
	if err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	requireEqual(t, "shown code", shownUser, userCode)
	requireEqual(t, "verify URL", shownURL, srv.URL+ChatGPTDevicePath)
	requireEqual(t, "showCode called once", shownOnce, 1)
	requireEqual(t, "poll count", pollGuard(), 2)
	requireEqual(t, "access token", ts.AccessToken, "tok.acc-tok.signed")
	requireEqual(t, "account id", ts.AccountID, "acct-fixture")
}

// The real issuer encodes interval as a JSON string; the device flow must
// tolerate both encodings (regression against a live-probe failure).
func TestDeviceLoginStringInterval(t *testing.T) {
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/accounts/deviceauth/usercode":
			_, _ = w.Write([]byte(`{"device_auth_id":"d","user_code":"C","interval":"2"}`))
		case r.URL.Path == "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusForbidden)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := DeviceLogin(ctx, srv.URL, ChatGPTClientID, "lisa-test-agent",
		func(string, string) error { return nil }, shortClient)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected polling to continue (deadline hit), got: %v", err)
	}
}

func TestDeviceLoginShowCodeError(t *testing.T) {
	polled := make(chan struct{}, 1)
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/accounts/deviceauth/usercode":
			_, _ = w.Write([]byte(`{"device_auth_id":"d","user_code":"C","interval":1}`))
		case r.URL.Path == "/api/accounts/deviceauth/token":
			select {
			case polled <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusForbidden)
		}
	})
	ts, err := DeviceLogin(context.Background(), srv.URL, ChatGPTClientID, "lisa-test-agent",
		func(string, string) error { return fmt.Errorf("cannot display code") }, shortClient)
	requireEqual(t, "show-code error surfaced", err != nil, true)
	if !strings.Contains(err.Error(), "cannot display code") {
		t.Fatalf("show-code error: %v", err)
	}
	requireEqual(t, "aborts without polling", len(polled), 0)
	requireEqual(t, "zero TokenSet", ts, TokenSet{})
}

func TestDeviceLoginServerError(t *testing.T) {
	srv := issuerPolicy(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/accounts/deviceauth/usercode":
			_, _ = w.Write([]byte(`{"device_auth_id":"d","user_code":"C","interval":1}`))
		case r.URL.Path == "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"slow down"}`))
		}
	})
	_, err := DeviceLogin(context.Background(), srv.URL, ChatGPTClientID, "lisa-test-agent",
		func(string, string) error { return nil }, shortClient)
	if err == nil {
		t.Fatal("expected 500 to abort the device flow")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("500 not surfaced: %v", err)
	}
}
