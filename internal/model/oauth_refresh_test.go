package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshCredentialsPreservesRegistrationAndOmittedFields(t *testing.T) {
	fixture := newOAuthFixture(t)
	old := fixture.credentials()
	fixture.tokenHook = func(form url.Values) {
		if !reflect.DeepEqual(form, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {old.ClientID}, "refresh_token": {old.Refresh}, "resource": {ChatGPTResource},
		}) {
			t.Errorf("refresh form: %v", form)
		}
	}
	fixture.tokenReply = func(url.Values) map[string]any {
		return map[string]any{"access_token": "new-access", "expires_in": 1200}
	}
	updated, err := RefreshCredentials(context.Background(), fixture.client, old)
	if err != nil {
		t.Fatal(err)
	}
	want := old
	want.Access, want.Expires = updated.Access, updated.Expires
	if !reflect.DeepEqual(updated, want) || updated.Access != "new-access" || updated.Expires <= time.Now().UnixMilli() || old.Access != "old-access" {
		t.Fatalf("refresh did not preserve omitted data: %#v", updated)
	}
	updated.Scopes[0] = "changed"
	if old.Scopes[0] == "changed" {
		t.Fatal("refresh aliases saved scopes")
	}
}

func TestRefreshCredentialsValidatesNewIDTokenWithoutLoginNonce(t *testing.T) {
	fixture := newOAuthFixture(t)
	old := fixture.credentials()
	fixture.tokenReply = func(url.Values) map[string]any {
		reply := fixture.defaultTokens()
		claims := fixture.claims()
		delete(claims, "nonce")
		claims["email"] = "new@example.test"
		reply["id_token"] = signOAuthFixture(t, oauthFixtureRSA(t), claims)
		return reply
	}
	updated, err := RefreshCredentials(context.Background(), fixture.client, old)
	if err != nil || updated.IDToken == old.IDToken || updated.Email != "new@example.test" || updated.Refresh != "refresh-fixture" ||
		updated.Subject != old.Subject || updated.ClientID != old.ClientID || updated.HostID != old.HostID || !updated.HasPlanScope() {
		t.Fatalf("validated refresh: %#v, %v", updated, err)
	}
}

func TestRefreshCredentialsPreservesOmittedEmail(t *testing.T) {
	fixture := newOAuthFixture(t)
	old := fixture.credentials()
	fixture.tokenReply = func(url.Values) map[string]any {
		reply := fixture.defaultTokens()
		claims := fixture.claims()
		delete(claims, "email")
		reply["id_token"] = signOAuthFixture(t, oauthFixtureRSA(t), claims)
		return reply
	}
	updated, err := RefreshCredentials(context.Background(), fixture.client, old)
	if err != nil || updated.Email != old.Email {
		t.Fatalf("omitted email discarded: %v", err)
	}
}

func TestRefreshCredentialsRejectsInvalidIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		badKey bool
	}{
		{name: "subject", change: func(c map[string]any) { c["sub"] = "another-account-secret" }},
		{name: "issuer", change: func(c map[string]any) { c["iss"] = "https://other.example" }},
		{name: "audience", change: func(c map[string]any) { c["aud"] = "oaiapp_other" }},
		{name: "expiry", change: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{name: "signature", badKey: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOAuthFixture(t)
			old := fixture.credentials()
			fixture.tokenReply = func(url.Values) map[string]any {
				reply := fixture.defaultTokens()
				claims := fixture.claims()
				if test.change != nil {
					test.change(claims)
				}
				key := oauthFixtureRSA(t)
				if test.badKey {
					key = oauthWrongRSA(t)
				}
				reply["id_token"] = signOAuthFixture(t, key, claims)
				return reply
			}
			updated, err := RefreshCredentials(context.Background(), fixture.client, old)
			if err == nil || updated.Access != "" || strings.Contains(err.Error(), "another-account-secret") || old.Access != "old-access" {
				t.Fatalf("invalid refresh identity accepted: %#v, %v", updated, err)
			}
		})
	}
}

func TestRefreshCredentialsNeverAcceptsExplicitPlanScopeRemoval(t *testing.T) {
	for _, scope := range []any{"", "openid email", ChatGPTPlanScope + ".extra", nil} {
		t.Run(fmt.Sprintf("%v", scope), func(t *testing.T) {
			fixture := newOAuthFixture(t)
			fixture.tokenReply = func(url.Values) map[string]any {
				reply := fixture.defaultTokens()
				reply["scope"] = scope
				delete(reply, "id_token")
				return reply
			}
			updated, err := RefreshCredentials(context.Background(), fixture.client, fixture.credentials())
			if err == nil || updated.Access != "" {
				t.Fatalf("removed permission accepted: %#v, %v", updated, err)
			}
		})
	}
}

func TestRefreshCredentialsRejectsLegacyAndIdentityOnlyBeforeHTTP(t *testing.T) {
	fixture := newOAuthFixture(t)
	var requests atomic.Int32
	client := &http.Client{Transport: oauthRoundTripper(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("should not be contacted")
	})}
	identityOnly := fixture.credentials()
	identityOnly.Scopes = []string{"openid"}
	for _, old := range []OAuthCredentials{
		{Access: "legacy-secret", Refresh: "legacy-refresh", AccountID: "legacy-id"},
		{Issuer: ChatGPTIssuer, Subject: oauthFixtureSubject, ClientID: ChatGPTClientID, HostID: oauthFixtureHostID},
		identityOnly,
	} {
		if _, err := RefreshCredentials(context.Background(), client, old); err == nil {
			t.Fatal("unregistered/identity-only grant accepted")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid credentials were sent to a token endpoint")
	}
}

func TestOAuthTokenErrorsAreRedactedAndClassified(t *testing.T) {
	for _, code := range []string{"invalid_grant", "invalid_client", "refresh_token_reused", "token_expired", "secret-response-body"} {
		t.Run(code, func(t *testing.T) {
			fixture := newOAuthFixture(t)
			fixture.tokenHTTPHook = func(w http.ResponseWriter, r *http.Request) bool {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w, `{"error":%q,"error_description":"refresh-fixture secret-response-body"}`, code)
				return true
			}
			_, err := RefreshCredentials(context.Background(), fixture.client, fixture.credentials())
			var failure *OAuthError
			if !errors.As(err, &failure) || failure.StatusCode != 400 || failure.Temporary() ||
				strings.Contains(err.Error(), "secret-response-body") || strings.Contains(err.Error(), "refresh-fixture") {
				t.Fatalf("unsafe/unclassified OAuth error: %v", err)
			}
			wantLogin := code == "invalid_grant" || code == "refresh_token_reused" || code == "token_expired"
			if failure.RequiresLogin() != wantLogin {
				t.Fatalf("terminal classification for %q: %t", code, failure.RequiresLogin())
			}
		})
	}
	fixture := newOAuthFixture(t)
	client := &http.Client{Transport: oauthRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("request leaked old-refresh and id-token-secret")
	})}
	_, err := RefreshCredentials(context.Background(), client, fixture.credentials())
	var failure *OAuthError
	if !errors.As(err, &failure) || !failure.Temporary() || strings.Contains(err.Error(), "old-refresh") || strings.Contains(err.Error(), "id-token-secret") {
		t.Fatalf("transport error was not redacted: %v", err)
	}
}

func TestRevokeCredentialsUsesDiscoveredEndpointAndIssuedClient(t *testing.T) {
	fixture := newOAuthFixture(t)
	credentials := fixture.credentials()
	var calls atomic.Int32
	fixture.revokeHook = func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		form := parseOAuthFixtureForm(t, r)
		if !reflect.DeepEqual(form, url.Values{"token": {credentials.Refresh}, "token_type_hint": {"refresh_token"}, "client_id": {credentials.ClientID}}) {
			t.Errorf("revocation form: %v", form)
		}
		w.WriteHeader(http.StatusOK) // empty 200, including an already-invalid token
	}
	if err := RevokeCredentials(context.Background(), fixture.client, credentials); err != nil || calls.Load() != 1 {
		t.Fatalf("revocation failed: %v (%d)", err, calls.Load())
	}
	credentials.Refresh = ""
	if err := RevokeCredentials(context.Background(), fixture.client, credentials); err != nil || calls.Load() != 1 {
		t.Fatal("signed-out registration tried to revoke again")
	}
}

func TestRevokeCredentialsRejectsUntrustedEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"https://evil.example/revoke", "http://auth.openai.com/revoke", "https://auth.openai.com:444/revoke",
		"https://auth.openai.com@evil.example/revoke", "https://auth.openai.com/revoke?token=secret", "", "/revoke",
	} {
		t.Run(endpoint, func(t *testing.T) {
			fixture := newOAuthFixture(t)
			fixture.discoveryHook = func(metadata map[string]any) { metadata["revocation_endpoint"] = endpoint }
			fixture.revokeHook = func(http.ResponseWriter, *http.Request) { t.Error("untrusted revocation was attempted") }
			if err := RevokeCredentials(context.Background(), fixture.client, fixture.credentials()); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("untrusted endpoint accepted or echoed: %v", err)
			}
		})
	}
}

func TestRevokeCredentialsBoundedRetries(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			fixture := newOAuthFixture(t)
			var calls atomic.Int32
			fixture.revokeHook = func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "refresh-fixture raw-server-secret")
			}
			err := RevokeCredentials(context.Background(), fixture.client, fixture.credentials())
			want := int32(1)
			if status == 500 {
				want = 3
			}
			if err == nil || calls.Load() != want || strings.Contains(err.Error(), "raw-server-secret") || strings.Contains(err.Error(), "refresh-fixture") {
				t.Fatalf("revocation retries/redaction: %d, %v", calls.Load(), err)
			}
		})
	}
}

func TestRevokeCredentialsRetriesNetworkFailureAndStopsOnContext(t *testing.T) {
	fixture := newOAuthFixture(t)
	original := fixture.client.Transport
	var calls atomic.Int32
	fixture.client.Transport = oauthRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/accounts/oauth/revoke" && calls.Add(1) == 1 {
			return nil, errors.New("network error with refresh-fixture")
		}
		return original.RoundTrip(r)
	})
	if err := RevokeCredentials(context.Background(), fixture.client, fixture.credentials()); err != nil || calls.Load() != 2 {
		t.Fatalf("network failure was not retried: %v, calls=%d", err, calls.Load())
	}
	fixture.revokeHook = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := RevokeCredentials(ctx, fixture.client, fixture.credentials()); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("revocation did not honor backoff cancellation: %v", err)
	}
}

func TestOAuthHTTPDoesNotFollowRedirectsOrMutateSuppliedClient(t *testing.T) {
	fixture := newOAuthFixture(t)
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	t.Cleanup(other.Close)
	fixture.revokeHook = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/secret-token", http.StatusTemporaryRedirect)
	}
	jar, _ := cookiejar.New(nil)
	fixture.client.Jar = jar
	fixture.client.Timeout = time.Hour
	fixture.client.CheckRedirect = func(*http.Request, []*http.Request) error { t.Error("caller redirect hook was used"); return nil }
	bounded := oauthHTTPClient(context.Background(), fixture.client)
	if bounded.Timeout != oauthHTTPTimeout || bounded.Jar != nil || fixture.client.Timeout != time.Hour || fixture.client.Jar != jar {
		t.Fatal("OAuth client is unbounded or mutated the caller client")
	}
	err := RevokeCredentials(context.Background(), fixture.client, fixture.credentials())
	var failure *OAuthError
	if !errors.As(err, &failure) || failure.StatusCode != http.StatusTemporaryRedirect || redirected.Load() != 0 || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("token-bearing redirect followed/echoed: %v, requests=%d", err, redirected.Load())
	}
	fixture.client.Timeout = time.Millisecond
	if oauthHTTPClient(context.Background(), fixture.client).Timeout != time.Millisecond {
		t.Fatal("shorter caller timeout was discarded")
	}
}

func TestOAuthHTTPResponseBodiesAreBounded(t *testing.T) {
	fixture := newOAuthFixture(t)
	fixture.tokenHTTPHook = func(w http.ResponseWriter, r *http.Request) bool {
		_, _ = io.WriteString(w, strings.Repeat("secret", oauthResponseLimit))
		return true
	}
	_, err := RefreshCredentials(context.Background(), fixture.client, fixture.credentials())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("oversized token body accepted/echoed: %v", err)
	}
	fixture.tokenHTTPHook = nil
	fixture.jwksHook = func(w http.ResponseWriter, r *http.Request) bool {
		_, _ = io.WriteString(w, strings.Repeat("secret", oauthResponseLimit))
		return true
	}
	_, err = RefreshCredentials(context.Background(), fixture.client, fixture.credentials())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("oversized OIDC key body accepted/echoed: %v", err)
	}
}
