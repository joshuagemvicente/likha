package model

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

const (
	oauthFixtureClientID = "oaiapp_likha_fixture"
	oauthFixtureHostID   = "urn:uuid:likha-test-host"
	oauthFixtureSubject  = "opaque-user-one"
)

var oauthLoopbackClient = &http.Client{Timeout: 3 * time.Second}

var fixtureKeys struct {
	sync.Once
	good, wrong *rsa.PrivateKey
	err         error
}

func oauthFixtureRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	fixtureKeys.Do(func() {
		fixtureKeys.good, fixtureKeys.err = rsa.GenerateKey(rand.Reader, 2048)
		if fixtureKeys.err == nil {
			fixtureKeys.wrong, fixtureKeys.err = rsa.GenerateKey(rand.Reader, 2048)
		}
	})
	if fixtureKeys.err != nil {
		t.Fatal(fixtureKeys.err)
	}
	return fixtureKeys.good
}

func oauthWrongRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	_ = oauthFixtureRSA(t)
	return fixtureKeys.wrong
}

// Production delegates crypto to go-oidc. Only the fixture signs JWTs itself,
// using real RSA/SHA256 and a separately served public JWKS.
func signOAuthFixture(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture-rsa","typ":"JWT"}`))
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

type oauthFixture struct {
	t             *testing.T
	server        *httptest.Server
	client        *http.Client
	mu            sync.Mutex
	authorization url.Values
	tokenReply    func(url.Values) map[string]any
	tokenHook     func(url.Values)
	tokenHTTPHook func(http.ResponseWriter, *http.Request) bool
	discoveryHook func(map[string]any)
	revokeHook    func(http.ResponseWriter, *http.Request)
	jwksHook      func(http.ResponseWriter, *http.Request) bool
}

type oauthRoundTripper func(*http.Request) (*http.Response, error)

func (f oauthRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	_ = oauthFixtureRSA(t)
	fixture := &oauthFixture{t: t}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(fixture.server.Close)
	target, _ := url.Parse(fixture.server.URL)
	fixture.client = &http.Client{
		Timeout: 3 * time.Second,
		Transport: oauthRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.Scheme != "https" || r.URL.Host != "auth.openai.com" {
				t.Errorf("fixture received an unpinned request: %s", r.URL)
			}
			clone := r.Clone(r.Context())
			clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
			clone.Host = target.Host
			return http.DefaultTransport.RoundTrip(clone)
		}),
	}
	return fixture
}

func (f *oauthFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		metadata := map[string]any{
			"issuer": ChatGPTIssuer, "authorization_endpoint": oauthAuthorizeURL, "token_endpoint": oauthTokenURL,
			"jwks_uri": ChatGPTIssuer + "/.well-known/jwks.json", "revocation_endpoint": ChatGPTIssuer + "/api/accounts/oauth/revoke",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		if f.discoveryHook != nil {
			f.discoveryHook(metadata)
		}
		_ = json.NewEncoder(w).Encode(metadata)
	case "/.well-known/jwks.json":
		if f.jwksHook != nil && f.jwksHook(w, r) {
			return
		}
		key := oauthFixtureRSA(f.t).PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "kid": "fixture-rsa", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	case "/api/accounts/oauth/token":
		if f.tokenHTTPHook != nil && f.tokenHTTPHook(w, r) {
			return
		}
		form := parseOAuthFixtureForm(f.t, r)
		if form.Get("resource") != ChatGPTResource || form.Get("client_id") != oauthFixtureClientID || form.Has("client_secret") {
			f.t.Errorf("token resource/client/secret: %v", form)
		}
		if form.Get("grant_type") == "authorization_code" {
			f.mu.Lock()
			authorize := cloneOAuthValues(f.authorization)
			f.mu.Unlock()
			sum := sha256.Sum256([]byte(form.Get("code_verifier")))
			if form.Get("redirect_uri") != authorize.Get("redirect_uri") ||
				base64.RawURLEncoding.EncodeToString(sum[:]) != authorize.Get("code_challenge") || form.Get("code") != "code-fixture" {
				f.t.Errorf("code exchange lost PKCE/code/exact redirect: %v", form)
			}
		}
		if f.tokenHook != nil {
			f.tokenHook(form)
		}
		reply := f.defaultTokens()
		if f.tokenReply != nil {
			reply = f.tokenReply(form)
		}
		_ = json.NewEncoder(w).Encode(reply)
	case "/api/accounts/oauth/revoke":
		if f.revokeHook != nil {
			f.revokeHook(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func parseOAuthFixtureForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
		t.Errorf("invalid OAuth form request: %s %v", r.Method, r.Header)
	}
	return r.PostForm
}

func cloneOAuthValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, list := range values {
		clone[key] = append([]string(nil), list...)
	}
	return clone
}

func (f *oauthFixture) claims() map[string]any {
	f.mu.Lock()
	nonce := f.authorization.Get("nonce")
	f.mu.Unlock()
	return map[string]any{
		"iss": ChatGPTIssuer, "sub": oauthFixtureSubject, "aud": oauthFixtureClientID,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "user@example.test",
	}
}

func (f *oauthFixture) defaultTokens() map[string]any {
	return map[string]any{
		"access_token": "access-fixture", "refresh_token": "refresh-fixture", "token_type": "Bearer", "expires_in": 3600,
		"id_token": signOAuthFixture(f.t, oauthFixtureRSA(f.t), f.claims()), "scope": oauthScope,
	}
}

func (f *oauthFixture) credentials() OAuthCredentials {
	return OAuthCredentials{
		Issuer: ChatGPTIssuer, Subject: oauthFixtureSubject, Email: "user@example.test", ClientID: oauthFixtureClientID,
		HostID: oauthFixtureHostID, IDToken: signOAuthFixture(f.t, oauthFixtureRSA(f.t), f.claims()),
		Access: "old-access", Refresh: "old-refresh", TokenType: "Bearer", Scopes: []string{"openid", ChatGPTPlanScope},
	}
}

type oauthLoginResult struct {
	tokens TokenSet
	err    error
}

type oauthAttempt struct {
	authorize   *url.URL
	redirectURI string
	done        <-chan oauthLoginResult
	cancel      context.CancelFunc
}

func startOAuthLogin(t *testing.T, fixture *oauthFixture, options BrowserLoginOptions) oauthAttempt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if options.HostID == "" {
		options.HostID = oauthFixtureHostID
	}
	if options.HTTPClient == nil {
		options.HTTPClient = fixture.client
	}
	originalOpen := options.OpenBrowser
	opened := make(chan *url.URL, 1)
	options.OpenBrowser = func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		fixture.mu.Lock()
		fixture.authorization = cloneOAuthValues(u.Query())
		fixture.mu.Unlock()
		opened <- u
		if originalOpen != nil {
			return originalOpen(raw)
		}
		return nil
	}
	done := make(chan oauthLoginResult, 1)
	go func() {
		tokens, err := BrowserLogin(ctx, options)
		done <- oauthLoginResult{tokens: tokens, err: err}
	}()
	select {
	case u := <-opened:
		return oauthAttempt{authorize: u, redirectURI: u.Query().Get("redirect_uri"), done: done, cancel: cancel}
	case result := <-done:
		t.Fatalf("login failed before opening browser: %v", result.err)
	case <-ctx.Done():
		t.Fatal("browser did not open")
	}
	return oauthAttempt{}
}

func (a oauthAttempt) goodCallback() url.Values {
	return url.Values{"state": {a.authorize.Query().Get("state")}, "code": {"code-fixture"}, "client_id": {oauthFixtureClientID}}
}

func (a oauthAttempt) callback(t *testing.T, values url.Values) (int, string) {
	t.Helper()
	response, err := oauthLoopbackClient.Get(a.redirectURI + "?" + values.Encode())
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, readOAuthTestBody(t, response)
}

func (a oauthAttempt) await(t *testing.T) oauthLoginResult {
	t.Helper()
	select {
	case result := <-a.done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("login did not finish")
		return oauthLoginResult{}
	}
}
