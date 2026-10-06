package model

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPKCE(t *testing.T) {
	a, challenge, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 43 || strings.ContainsAny(a, "+/=") {
		t.Fatalf("invalid verifier: %q", a)
	}
	sum := sha256.Sum256([]byte(a))
	if challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("challenge does not match verifier")
	}
	b, _, err := NewPKCE()
	if err != nil || a == b {
		t.Fatal("PKCE verifier was not freshly generated")
	}
}

func TestSIWCCredentialsAndProvider(t *testing.T) {
	tokens := TokenSet{
		AccessToken: "access", RefreshToken: "refresh", IDToken: "id", Expires: time.Now().Add(time.Hour),
		Issuer: ChatGPTIssuer, Subject: "subject", Email: "user@example.test", ClientID: oauthFixtureClientID,
		HostID: oauthFixtureHostID, TokenType: "Bearer", Scopes: strings.Fields(oauthScope),
	}
	credentials := tokens.Credentials()
	if !credentials.Registered() || !credentials.HasPlanScope() || credentials.Expires != tokens.Expires.UnixMilli() ||
		credentials.IDToken != tokens.IDToken || credentials.Subject != tokens.Subject || credentials.AccountID != "" {
		t.Fatalf("missing verified credential data: %#v", credentials)
	}
	credentials.Scopes[0] = "changed"
	if tokens.Scopes[0] == "changed" {
		t.Fatal("Credentials aliases the token set's scopes")
	}
	credentials = tokens.Credentials()
	encoded, err := json.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip OAuthCredentials
	if err := json.Unmarshal(encoded, &roundtrip); err != nil || !reflect.DeepEqual(credentials, roundtrip) {
		t.Fatalf("credential roundtrip: %v", err)
	}
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	for _, key := range []string{"refresh", "access", "expires", "issuer", "subject", "email", "client_id", "ext_agent_host_id", "id_token", "token_type", "scopes"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
	for _, change := range []func(*OAuthCredentials){
		func(c *OAuthCredentials) { c.ClientID = ChatGPTClientID },
		func(c *OAuthCredentials) { c.ClientID = "" },
		func(c *OAuthCredentials) { c.Subject = "" },
		func(c *OAuthCredentials) { c.Issuer = "" },
		func(c *OAuthCredentials) { c.HostID = "" },
	} {
		invalid := credentials
		change(&invalid)
		if invalid.Registered() {
			t.Errorf("incomplete registration accepted: %#v", invalid)
		}
	}
	if (OAuthCredentials{Scopes: []string{"openid", ChatGPTPlanScope + ".extra"}}).HasPlanScope() {
		t.Fatal("identity or prefix scope accepted as plan permission")
	}
	provider, ok := LookupProvider("chatgpt")
	if !ok || provider.BaseURL != ChatGPTResource || provider.DefaultModel != "" || provider.SessionHeader != "" || provider.Auth != AuthOAuth ||
		ChatGPTClientID != "dynamic_agent_client" || ChatGPTCallbackPort != 0 || len(ChatGPTModels) != 0 {
		t.Fatalf("ChatGPT provider still uses legacy defaults: %#v", provider)
	}
}

func TestBrowserLoginDynamicRegistration(t *testing.T) {
	fixture := newOAuthFixture(t)
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	q := attempt.authorize.Query()
	want := map[string]string{
		"client_id": ChatGPTClientID, "agent_name_hint": "Likha", "ext_agent_host_id": oauthFixtureHostID,
		"response_type": "code", "scope": oauthScope, "resource": ChatGPTResource, "code_challenge_method": "S256",
	}
	if attempt.authorize.Scheme != "https" || attempt.authorize.Host != "auth.openai.com" || attempt.authorize.Path != "/api/accounts/authorize" {
		t.Fatalf("authorize origin/path: %s", attempt.authorize)
	}
	for key, value := range want {
		if q.Get(key) != value || len(q[key]) != 1 {
			t.Errorf("authorization parameter %s: %q, want %q", key, q.Get(key), value)
		}
	}
	for _, key := range []string{"nonce", "state", "code_challenge"} {
		if q.Get(key) == "" {
			t.Errorf("missing %s", key)
		}
	}
	if q.Get("nonce") == q.Get("state") || q.Has("id_token_hint") || q.Has("login_hint") || q.Has("originator") || q.Has("codex_cli_simplified_flow") {
		t.Fatal("authorization reused entropy or included legacy/returning parameters")
	}
	callback, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || callback.Scheme != "http" || callback.Hostname() != "127.0.0.1" || callback.Port() == "" || callback.Port() == "0" || callback.Path != "/auth/callback" {
		t.Fatalf("not the actual loopback callback: %v, %v", callback, err)
	}
	status, body := attempt.callback(t, attempt.goodCallback())
	if status != http.StatusOK || !strings.Contains(body, "verifying") || strings.Contains(strings.ToLower(body), "complete") || strings.Contains(strings.ToLower(body), "success") {
		t.Fatalf("callback claimed unverified success: %d %q", status, body)
	}
	result := attempt.await(t)
	if result.err != nil {
		t.Fatal(result.err)
	}
	c := result.tokens.Credentials()
	if c.Issuer != ChatGPTIssuer || c.Subject != oauthFixtureSubject || c.ClientID != oauthFixtureClientID || c.HostID != oauthFixtureHostID ||
		c.Email != "user@example.test" || c.IDToken == "" || c.TokenType != "Bearer" || !c.HasPlanScope() || c.AccountID != "" || c.Refresh != "refresh-fixture" {
		t.Fatalf("verified registration was not retained: %#v", c)
	}
	if remaining := time.Until(result.tokens.Expires); remaining < 3590*time.Second || remaining > time.Hour {
		t.Fatalf("invalid access-token expiry: %v", remaining)
	}
	requireOAuthListenerClosed(t, callback.Host)
}

func TestBrowserLoginReturningRegistration(t *testing.T) {
	for _, includeClientID := range []bool{false, true} {
		t.Run(fmt.Sprintf("callback_client_id_%t", includeClientID), func(t *testing.T) {
			fixture := newOAuthFixture(t)
			old := fixture.credentials()
			attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{Credentials: old})
			q := attempt.authorize.Query()
			if q.Get("client_id") != old.ClientID || q.Has("agent_name_hint") || q.Get("id_token_hint") != old.IDToken || q.Get("login_hint") != old.Email ||
				q.Get("ext_agent_host_id") != old.HostID {
				t.Fatalf("returning parameters: %v", q)
			}
			callback := attempt.goodCallback()
			if !includeClientID {
				callback.Del("client_id")
			}
			attempt.callback(t, callback)
			result := attempt.await(t)
			if result.err != nil || result.tokens.ClientID != old.ClientID || result.tokens.Subject != old.Subject {
				t.Fatalf("returning login failed: %v", result.err)
			}
		})
	}
}

func TestBrowserLoginRejectsIncompleteOrChangedClient(t *testing.T) {
	for _, test := range []struct {
		name      string
		returning bool
		clientID  string
		omit      bool
	}{
		{name: "new_missing", omit: true},
		{name: "new_dynamic", clientID: ChatGPTClientID},
		{name: "new_legacy_client", clientID: "app_EMoamEEZ73f0CkXaXp7hrann"},
		{name: "new_invalid", clientID: "client with whitespace"},
		{name: "returning_changed", returning: true, clientID: "oaiapp_other"},
		{name: "returning_empty", returning: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOAuthFixture(t)
			var exchanges atomic.Int32
			fixture.tokenHook = func(url.Values) { exchanges.Add(1) }
			options := BrowserLoginOptions{}
			if test.returning {
				options.Credentials = fixture.credentials()
			}
			attempt := startOAuthLogin(t, fixture, options)
			callback := attempt.goodCallback()
			if test.omit {
				callback.Del("client_id")
			} else {
				callback.Set("client_id", test.clientID)
			}
			status, _ := attempt.callback(t, callback)
			result := attempt.await(t)
			if status != http.StatusBadRequest || result.err == nil || result.tokens.Subject != "" || exchanges.Load() != 0 {
				t.Fatalf("invalid client was exchanged: status=%d err=%v exchanges=%d", status, result.err, exchanges.Load())
			}
		})
	}
}

func TestBrowserLoginOIDCValidation(t *testing.T) {
	for _, test := range []struct {
		name      string
		returning bool
		change    func(map[string]any)
		badSigner bool
		omitID    bool
	}{
		{name: "nonce_mismatch", change: func(c map[string]any) { c["nonce"] = "other-nonce" }},
		{name: "nonce_missing", change: func(c map[string]any) { delete(c, "nonce") }},
		{name: "audience", change: func(c map[string]any) { c["aud"] = ChatGPTClientID }},
		{name: "issuer", change: func(c map[string]any) { c["iss"] = "https://evil.example/secret-claim" }},
		{name: "expired", change: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{name: "no_expiry", change: func(c map[string]any) { delete(c, "exp") }},
		{name: "not_yet_valid", change: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{name: "subject_missing", change: func(c map[string]any) {
			delete(c, "sub")
			c["organizations"] = []any{map[string]any{"id": "not-a-subject"}}
		}},
		{name: "returning_account_mismatch", returning: true, change: func(c map[string]any) { c["sub"] = "other-account" }},
		{name: "multiple_audiences_without_azp", change: func(c map[string]any) { c["aud"] = []string{oauthFixtureClientID, "other"} }},
		{name: "wrong_azp", change: func(c map[string]any) { c["azp"] = "other" }},
		{name: "signature", badSigner: true},
		{name: "id_token_missing", omitID: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOAuthFixture(t)
			fixture.tokenReply = func(url.Values) map[string]any {
				reply := fixture.defaultTokens()
				claims := fixture.claims()
				if test.change != nil {
					test.change(claims)
				}
				key := oauthFixtureRSA(t)
				if test.badSigner {
					key = oauthWrongRSA(t)
				}
				reply["id_token"] = signOAuthFixture(t, key, claims)
				if test.omitID {
					delete(reply, "id_token")
				}
				return reply
			}
			options := BrowserLoginOptions{}
			if test.returning {
				options.Credentials = fixture.credentials()
			}
			attempt := startOAuthLogin(t, fixture, options)
			attempt.callback(t, attempt.goodCallback())
			result := attempt.await(t)
			if result.err == nil || result.tokens.Subject != "" {
				t.Fatalf("invalid ID token accepted: %v", result.err)
			}
			for _, secret := range []string{"secret-claim", "other-account", "other-nonce", "access-fixture", "refresh-fixture"} {
				if strings.Contains(result.err.Error(), secret) {
					t.Errorf("verification error leaked %q: %v", secret, result.err)
				}
			}
		})
	}
}

func TestBrowserLoginRejectsUnsignedIDToken(t *testing.T) {
	fixture := newOAuthFixture(t)
	fixture.tokenReply = func(url.Values) map[string]any {
		reply := fixture.defaultTokens()
		payload, _ := json.Marshal(fixture.claims())
		reply["id_token"] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
		return reply
	}
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	attempt.callback(t, attempt.goodCallback())
	if result := attempt.await(t); result.err == nil {
		t.Fatal("unsigned identity accepted")
	}
}

func TestBrowserLoginGrantedScopes(t *testing.T) {
	for _, scope := range []any{nil, "openid profile email", "", ChatGPTPlanScope + ".extra", "openid " + ChatGPTPlanScope} {
		t.Run(fmt.Sprintf("%v", scope), func(t *testing.T) {
			fixture := newOAuthFixture(t)
			fixture.tokenReply = func(url.Values) map[string]any {
				reply := fixture.defaultTokens()
				if scope == nil {
					delete(reply, "scope")
				} else {
					reply["scope"] = scope
				}
				return reply
			}
			attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
			callback := attempt.goodCallback()
			callback.Set("scope", oauthScope) // untrusted callback scope never grants inference
			attempt.callback(t, callback)
			result := attempt.await(t)
			if scope == "openid "+ChatGPTPlanScope {
				if result.err != nil || !result.tokens.Credentials().HasPlanScope() {
					t.Fatalf("granted plan scope rejected: %v", result.err)
				}
			} else if !errors.Is(result.err, ErrPlanScopeRequired) || !result.tokens.Credentials().Registered() || result.tokens.Credentials().HasPlanScope() {
				t.Fatalf("identity-only registration not retained safely: %v", result.err)
			}
		})
	}
}

func TestBrowserLoginUnsolicitedCallbacksDoNotAbort(t *testing.T) {
	fixture := newOAuthFixture(t)
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	for _, params := range []url.Values{
		{"state": {"wrong"}, "code": {"attacker-code"}, "client_id": {oauthFixtureClientID}},
		{"state": {"wrong"}, "error": {"access_denied"}},
		{"error": {"access_denied"}},
		{"state": {attempt.authorize.Query().Get("state"), "wrong"}, "code": {"attacker-code"}},
	} {
		status, _ := attempt.callback(t, params)
		if status != http.StatusBadRequest {
			t.Fatalf("unsolicited callback status: %d", status)
		}
	}
	select {
	case result := <-attempt.done:
		t.Fatalf("unsolicited callback aborted real login: %v", result.err)
	default:
	}
	attempt.callback(t, attempt.goodCallback())
	if result := attempt.await(t); result.err != nil {
		t.Fatal(result.err)
	}
}

func TestBrowserLoginDenialRequiresState(t *testing.T) {
	fixture := newOAuthFixture(t)
	var exchanges atomic.Int32
	fixture.tokenHook = func(url.Values) { exchanges.Add(1) }
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	status, body := attempt.callback(t, url.Values{
		"state": {attempt.authorize.Query().Get("state")}, "error": {"access_denied"}, "error_description": {"secret-denial-text"},
	})
	result := attempt.await(t)
	if status != http.StatusBadRequest || !errors.Is(result.err, ErrPlanScopeRequired) || exchanges.Load() != 0 ||
		strings.Contains(body, "secret-denial-text") || strings.Contains(result.err.Error(), "secret-denial-text") {
		t.Fatalf("denial handling: status=%d err=%v exchanges=%d", status, result.err, exchanges.Load())
	}
}

func TestBrowserLoginCallbackMethodPathAndDuplicates(t *testing.T) {
	fixture := newOAuthFixture(t)
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	callback := attempt.goodCallback()
	for _, key := range []string{"code", "client_id", "scope", "error"} {
		params := cloneOAuthValues(callback)
		params[key] = []string{"first", "second"}
		if status, _ := attempt.callback(t, params); status != http.StatusBadRequest {
			t.Errorf("duplicate %s: status %d", key, status)
		}
	}
	for _, path := range []string{"/other", "/cancel", "/callback", "/auth/%63allback"} {
		u, _ := url.Parse(attempt.redirectURI)
		u.Path, u.RawPath = path, ""
		if path == "/auth/%63allback" {
			u.Path, u.RawPath = "/auth/callback", path
		}
		response, err := oauthLoopbackClient.Get(u.String())
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("wrong path %s: status %d", path, response.StatusCode)
		}
	}
	request, _ := http.NewRequest(http.MethodPost, attempt.redirectURI+"?"+callback.Encode(), nil)
	response, err := oauthLoopbackClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST callback accepted: %d", response.StatusCode)
	}
	attempt.callback(t, callback)
	if result := attempt.await(t); result.err != nil {
		t.Fatal(result.err)
	}
}

func TestBrowserLoginCallbackConsumedOnceDuringExchange(t *testing.T) {
	fixture := newOAuthFixture(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var exchanges atomic.Int32
	fixture.tokenHook = func(url.Values) {
		exchanges.Add(1)
		entered <- struct{}{}
		<-release
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	callback := attempt.goodCallback()
	attempt.callback(t, callback)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("code exchange did not start")
	}
	for range 3 {
		if status, _ := attempt.callback(t, callback); status != http.StatusConflict {
			t.Errorf("repeated callback status %d", status)
		}
	}
	close(release)
	if result := attempt.await(t); result.err != nil || exchanges.Load() != 1 {
		t.Fatalf("code exchanged more than once: %v (%d)", result.err, exchanges.Load())
	}
}

func TestBrowserLoginCancellationAndRetry(t *testing.T) {
	fixture := newOAuthFixture(t)
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	attempt.cancel()
	if result := attempt.await(t); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancel: %v", result.err)
	}
	u, _ := url.Parse(attempt.redirectURI)
	requireOAuthListenerClosed(t, u.Host)
	if response, err := oauthLoopbackClient.Get(attempt.redirectURI + "?" + attempt.goodCallback().Encode()); err == nil {
		_ = response.Body.Close()
		t.Fatal("delayed callback reached a cancelled listener")
	}
	_, portString, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portString)
	next := startOAuthLogin(t, fixture, BrowserLoginOptions{Port: port})
	if next.authorize.Query().Get("state") == attempt.authorize.Query().Get("state") || next.authorize.Query().Get("nonce") == attempt.authorize.Query().Get("nonce") {
		t.Fatal("retry reused state or nonce")
	}
	next.callback(t, next.goodCallback())
	if result := next.await(t); result.err != nil {
		t.Fatal(result.err)
	}
}

func TestBrowserLoginDeadline(t *testing.T) {
	fixture := newOAuthFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	opened := make(chan string, 1)
	_, err := BrowserLogin(ctx, BrowserLoginOptions{HostID: oauthFixtureHostID, HTTPClient: fixture.client, OpenBrowser: func(u string) error { opened <- u; return nil }})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline was not honored: %v", err)
	}
	select {
	case authorize := <-opened:
		u, _ := url.Parse(authorize)
		callback, _ := url.Parse(u.Query().Get("redirect_uri"))
		requireOAuthListenerClosed(t, callback.Host)
	default:
		t.Fatal("browser was not opened")
	}
}

func TestBrowserLoginBrowserFailureOffersRedactedFallback(t *testing.T) {
	fixture := newOAuthFixture(t)
	old := fixture.credentials()
	fallback := make(chan string, 1)
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{
		Credentials: old,
		OpenBrowser: func(u string) error { return fmt.Errorf("open failed: %s", u) },
		OnBrowserError: func(u string, err error) {
			if strings.Contains(u, old.IDToken) || strings.Contains(err.Error(), old.IDToken) || strings.Contains(u, "id_token_hint") {
				t.Error("ID token escaped in browser failure fallback")
			}
			fallback <- u
		},
	})
	select {
	case manual := <-fallback:
		u, _ := url.Parse(manual)
		if u.Query().Get("state") != attempt.authorize.Query().Get("state") || u.Query().Get("client_id") != old.ClientID || u.Query().Has("id_token_hint") {
			t.Fatalf("invalid manual fallback: %s", manual)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("browser failure did not offer manual URL")
	}
	attempt.callback(t, attempt.goodCallback())
	if result := attempt.await(t); result.err != nil {
		t.Fatalf("browser launch failure cancelled the listener: %v", result.err)
	}
}

func TestBrowserLoginRejectsLegacyAndWrongHostBeforeOpening(t *testing.T) {
	fixture := newOAuthFixture(t)
	for _, options := range []BrowserLoginOptions{
		{},
		{HostID: oauthFixtureHostID, Credentials: OAuthCredentials{Refresh: "legacy-refresh", AccountID: "legacy-account"}},
		{HostID: "another-host", Credentials: fixture.credentials()},
	} {
		options.HTTPClient = fixture.client
		options.OpenBrowser = func(string) error { t.Error("invalid registration opened a browser"); return nil }
		if _, err := BrowserLogin(context.Background(), options); err == nil {
			t.Fatal("incomplete/legacy/wrong-host registration accepted")
		}
	}
}

func TestBrowserLoginDiscoveryIsPinned(t *testing.T) {
	for _, field := range []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri"} {
		t.Run(field, func(t *testing.T) {
			fixture := newOAuthFixture(t)
			fixture.discoveryHook = func(metadata map[string]any) { metadata[field] = "https://untrusted.example/endpoint-secret" }
			_, err := BrowserLogin(context.Background(), BrowserLoginOptions{
				HostID: oauthFixtureHostID, HTTPClient: fixture.client,
				OpenBrowser: func(string) error { t.Error("untrusted discovery opened browser"); return nil },
			})
			if err == nil || strings.Contains(err.Error(), "endpoint-secret") {
				t.Fatalf("untrusted discovery accepted or leaked: %v", err)
			}
		})
	}
}

func TestBrowserLoginCancellationClosesJWKSRequest(t *testing.T) {
	fixture := newOAuthFixture(t)
	entered, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	fixture.jwksHook = func(w http.ResponseWriter, r *http.Request) bool {
		entered <- struct{}{}
		<-r.Context().Done()
		stopped <- struct{}{}
		return true
	}
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{})
	attempt.callback(t, attempt.goodCallback())
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("JWKS fetch did not start")
	}
	attempt.cancel()
	if result := attempt.await(t); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("JWKS cancellation: %v", result.err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled login left its JWKS HTTP request running")
	}
}

func TestBrowserLoginBlockingInjectedOpenerRemainsCancellable(t *testing.T) {
	fixture := newOAuthFixture(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	attempt := startOAuthLogin(t, fixture, BrowserLoginOptions{
		OpenBrowser: func(string) error { <-release; return nil },
	})
	start := time.Now()
	attempt.cancel()
	result := attempt.await(t)
	if !errors.Is(result.err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("blocking opener prevented cancellation: %v", result.err)
	}
	u, _ := url.Parse(attempt.redirectURI)
	requireOAuthListenerClosed(t, u.Host)
}

func TestBrowserLoginCodeInvalidGrantRestartsWithIssuedClient(t *testing.T) {
	for _, failAgain := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry_also_fails_%t", failAgain), func(t *testing.T) {
			fixture := newOAuthFixture(t)
			var exchanges atomic.Int32
			fixture.tokenHTTPHook = func(w http.ResponseWriter, r *http.Request) bool {
				if exchanges.Add(1) == 1 || failAgain {
					form := parseOAuthFixtureForm(t, r)
					if form.Get("client_id") != oauthFixtureClientID || form.Get("grant_type") != "authorization_code" {
						t.Errorf("initial failed exchange lost issued client: %v", form)
					}
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"secret-invalid-code"}`)
					return true
				}
				return false
			}
			opened := make(chan *url.URL, 2)
			first := startOAuthLogin(t, fixture, BrowserLoginOptions{
				OpenBrowser: func(raw string) error { u, _ := url.Parse(raw); opened <- u; return nil },
			})
			<-opened // the dynamic-registration request
			first.callback(t, first.goodCallback())
			var second *url.URL
			select {
			case second = <-opened:
			case <-time.After(3 * time.Second):
				t.Fatal("invalid_grant did not start fresh authorization")
			}
			firstQuery, secondQuery := first.authorize.Query(), second.Query()
			if secondQuery.Get("client_id") != oauthFixtureClientID || secondQuery.Has("agent_name_hint") || secondQuery.Get("ext_agent_host_id") != firstQuery.Get("ext_agent_host_id") ||
				secondQuery.Has("id_token_hint") {
				t.Fatalf("restart used dynamic registration or unverified identity: %v", secondQuery)
			}
			for _, key := range []string{"state", "nonce", "code_challenge"} {
				if secondQuery.Get(key) == "" || secondQuery.Get(key) == firstQuery.Get(key) {
					t.Errorf("restart reused %s", key)
				}
			}
			firstRedirect, _ := url.Parse(first.redirectURI)
			secondRedirect, _ := url.Parse(secondQuery.Get("redirect_uri"))
			if firstRedirect.Scheme != secondRedirect.Scheme || firstRedirect.Hostname() != secondRedirect.Hostname() || firstRedirect.Path != secondRedirect.Path {
				t.Fatal("restart changed callback scheme, host, or path")
			}
			retry := oauthAttempt{authorize: second, redirectURI: secondRedirect.String(), done: first.done, cancel: first.cancel}
			retry.callback(t, retry.goodCallback())
			result := retry.await(t)
			if failAgain {
				var failure *OAuthError
				if !errors.As(result.err, &failure) || failure.Code != "invalid_grant" || result.tokens.Subject != "" || strings.Contains(result.err.Error(), "secret-invalid-code") {
					t.Fatalf("bounded restart saved unverified identity: %v", result.err)
				}
			} else if result.err != nil || result.tokens.Subject != oauthFixtureSubject || result.tokens.ClientID != oauthFixtureClientID {
				t.Fatalf("fresh issued-client authorization failed: %v", result.err)
			}
			if exchanges.Load() != 2 {
				t.Fatalf("unbounded code exchange retries: %d", exchanges.Load())
			}
			requireOAuthListenerClosed(t, secondRedirect.Host)
		})
	}
}

func TestOAuthCallbackDeliveryNeverBlocksOnAbandonedResult(t *testing.T) {
	results := make(chan oauthCallback, 1)
	results <- oauthCallback{code: "previous-result"}
	handler := callbackHandler("expected-state", ChatGPTClientID, "127.0.0.1:12345", false, results)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/auth/callback?state=expected-state&code=code&client_id="+oauthFixtureClientID, nil)
	finished := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("delayed callback blocked on an abandoned result channel")
	}
}

func requireOAuthListenerClosed(t *testing.T, host string) {
	t.Helper()
	listener, err := net.Listen("tcp4", host)
	if err != nil {
		t.Fatalf("callback listener remained open: %v", err)
	}
	_ = listener.Close()
}

func readOAuthTestBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
