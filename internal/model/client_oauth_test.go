package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewOAuthPinsPublicEndpointIssuerAndRegistration(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:1234/v1", "https://chatgpt.com/backend-api/codex",
		"https://api.openai.com.evil.invalid/v1", "https://api.openai.com:443/v1",
		"https://api.openai.com/v1?override=1", "https://api.openai.com/v1#fragment",
		"https://user@api.openai.com/v1", "https://api.openai.com/v1//",
	} {
		if _, err := NewOAuth(endpoint, "m", ChatGPTIssuer, "issued-client-test", validOAuthCredentials()); err == nil {
			t.Errorf("accepted OAuth endpoint override %q", endpoint)
		}
	}
	for _, issuer := range []string{"", "http://127.0.0.1:1234", "https://auth.openai.com.evil.invalid", "https://auth.openai.com:443", ChatGPTIssuer + "/different"} {
		if _, err := NewOAuth(ChatGPTResource, "m", issuer, "issued-client-test", validOAuthCredentials()); err == nil {
			t.Errorf("accepted OAuth issuer override %q", issuer)
		}
	}
	for _, clientID := range []string{"", ChatGPTClientID, "other-issued-client"} {
		if _, err := NewOAuth(ChatGPTResource, "m", ChatGPTIssuer, clientID, validOAuthCredentials()); err == nil {
			t.Errorf("accepted a client ID not issued for these credentials: %q", clientID)
		}
	}
	for _, mutate := range []func(*OAuthCredentials){
		func(c *OAuthCredentials) { c.Issuer = "" },
		func(c *OAuthCredentials) { c.Subject = "" },
		func(c *OAuthCredentials) { c.HostID = "" },
		func(c *OAuthCredentials) { c.ClientID = ChatGPTClientID },
		func(c *OAuthCredentials) { c.ClientID = "app_EMoamEEZ73f0CkXaXp7hrann" },
		func(c *OAuthCredentials) { c.Scopes = []string{"openid", "email", "offline_access"} },
		func(c *OAuthCredentials) { c.TokenType = "DPoP" },
		func(c *OAuthCredentials) { c.Access, c.Refresh = "", "" },
	} {
		creds := validOAuthCredentials()
		mutate(&creds)
		if _, err := NewOAuth(ChatGPTResource, "m", ChatGPTIssuer, creds.ClientID, creds); err == nil {
			t.Fatal("accepted unregistered, unsupported, or unauthorized credentials")
		}
	}
	legacy := OAuthCredentials{Access: "legacy-access", Refresh: "legacy-refresh", Expires: time.Now().Add(time.Hour).UnixMilli(), AccountID: "legacy-decoded-claim"}
	if _, err := NewOAuth(ChatGPTResource, "m", ChatGPTIssuer, ChatGPTClientID, legacy); err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("legacy credentials error = %v", err)
	}
	client, err := NewOAuth(ChatGPTResource, "", ChatGPTIssuer, "issued-client-test", validOAuthCredentials())
	if err != nil || client.Base() != ChatGPTResource || client.Model() != "" || client.APIKey() != "" {
		t.Fatalf("provisional discovery client = %v, error=%v", client, err)
	}
}

// observeOAuthWait records entry into waitAccess rather than relying on sleeps
// to arrange concurrent refresh waiters. WithoutCancel does not consult Done.
type observeOAuthWait struct {
	context.Context
	once sync.Once
	wait chan<- struct{}
}

func (c *observeOAuthWait) Done() <-chan struct{} {
	c.once.Do(func() { c.wait <- struct{}{} })
	return c.Context.Done()
}

func waitOAuthSignals(t *testing.T, signals <-chan struct{}, n int) {
	t.Helper()
	for range n {
		select {
		case <-signals:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out arranging OAuth concurrency")
		}
	}
}

func TestOAuthSingleFlightSharesTransientFailureAndCanRetry(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/oauth/token" {
			t.Error("a failed refresh reached inference")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if calls.Add(1) == 1 {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":"temporarily_unavailable"}`)
			return
		}
		writeToken(w, "recovered")
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	old := cloneClientOAuthCredentials(client.oauth.creds)
	const n = 6
	waiting := make(chan struct{}, n)
	errorsSeen := make(chan error, n)
	for range n {
		go func() {
			_, err := client.oauth.access(&observeOAuthWait{Context: context.Background(), wait: waiting}, false)
			errorsSeen <- err
		}()
	}
	waitOAuthSignals(t, waiting, n)
	close(gate)
	for range n {
		err := <-errorsSeen
		if err == nil || strings.Contains(err.Error(), "sign in again") {
			t.Fatalf("transient refresh error = %v", err)
		}
	}
	if calls.Load() != 1 || client.oauth.invalidated || !reflect.DeepEqual(client.oauth.creds, old) {
		t.Fatalf("failed flight calls=%d invalidated=%v; old credential was not retained", calls.Load(), client.oauth.invalidated)
	}
	token, err := client.oauth.access(context.Background(), false)
	if err != nil || token != "recovered" || calls.Load() != 2 {
		t.Fatalf("retry token=%q error=%v calls=%d", token, err, calls.Load())
	}
}

func TestOAuthRefreshWaitersHonorContextWithoutCancellingSibling(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		close(started)
		select {
		case <-release:
			writeToken(w, "survivor-token")
		case <-r.Context().Done():
			t.Error("one waiter's cancellation cancelled a shared refresh")
		}
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerErr := make(chan error, 1)
	go func() { _, err := client.oauth.access(ownerCtx, false); ownerErr <- err }()
	waitOAuthSignals(t, started, 1)
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiting := make(chan struct{}, 1)
	waiterErr := make(chan error, 1)
	go func() {
		_, err := client.oauth.access(&observeOAuthWait{Context: waiterCtx, wait: waiting}, false)
		waiterErr <- err
	}()
	waitOAuthSignals(t, waiting, 1)
	cancelOwner()
	cancelWaiter()
	if err := <-ownerErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner error=%v", err)
	}
	if err := <-waiterErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error=%v", err)
	}
	survivor := make(chan string, 1)
	survivorErr := make(chan error, 1)
	waiting = make(chan struct{}, 1)
	go func() {
		token, err := client.oauth.access(&observeOAuthWait{Context: context.Background(), wait: waiting}, false)
		survivor <- token
		survivorErr <- err
	}()
	waitOAuthSignals(t, waiting, 1)
	close(release)
	if token, err := <-survivor, <-survivorErr; token != "survivor-token" || err != nil || requests.Load() != 1 {
		t.Fatalf("survivor token=%q error=%v refreshes=%d", token, err, requests.Load())
	}
}

func TestOAuthSaverFailureBlocksRequestsAndRetriesPersistence(t *testing.T) {
	var tokens, responses atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/oauth/token" {
			tokens.Add(1)
			writeToken(w, "rotated-once")
			return
		}
		responses.Add(1)
		if r.Header.Get("Authorization") != "Bearer rotated-once" {
			t.Error("inference reused the old access token")
		}
		codexOK(w)
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	writeErr := errors.New("fixture disk is full")
	var saves atomic.Int32
	client.SetOAuthSaver(func(creds OAuthCredentials) error {
		if creds.Access != "rotated-once" || creds.Refresh != "refresh-new" {
			t.Error("saver did not receive the rotated token set")
		}
		if saves.Add(1) == 1 {
			return writeErr
		}
		return nil
	})
	if _, err := client.Stream(context.Background(), hello, nil, nil, nil); !errors.Is(err, writeErr) || strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("failed persistence error=%v", err)
	}
	if responses.Load() != 0 || !client.oauth.pendingSave || client.oauth.creds.Access != "rotated-once" {
		t.Fatal("unsaved rotation reached inference or was discarded")
	}
	if answer, err := client.Stream(context.Background(), hello, nil, nil, nil); err != nil || answer.Content != "ok" {
		t.Fatalf("persistence retry answer=%+v error=%v", answer, err)
	}
	if tokens.Load() != 1 || saves.Load() != 2 || responses.Load() != 1 || client.oauth.pendingSave {
		t.Fatalf("refreshes=%d saves=%d responses=%d pending=%v", tokens.Load(), saves.Load(), responses.Load(), client.oauth.pendingSave)
	}
}

func TestOAuthTerminalRefreshFailureStopsLocalSession(t *testing.T) {
	for _, code := range []string{"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused"} {
		t.Run(code, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/api/accounts/oauth/token" {
					t.Error("terminal refresh failure reached inference")
				}
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"error":%q}`, code)
			}))
			t.Cleanup(server.Close)
			client := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
			for range 2 {
				if _, err := client.Stream(context.Background(), hello, nil, nil, nil); !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "sign in again") {
					t.Fatalf("terminal refresh error=%v", err)
				}
			}
			if requests.Load() != 1 || !client.oauth.invalidated || client.oauth.creds.Access != "" || client.oauth.creds.Refresh != "" {
				t.Fatalf("terminal refresh continued using tokens; requests=%d", requests.Load())
			}
		})
	}
	client, err := NewOAuth(ChatGPTResource, "m", ChatGPTIssuer, "issued-client-test", func() OAuthCredentials {
		creds := expiredOAuthCredentials()
		creds.Refresh = ""
		return creds
	}())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.oauth.access(context.Background(), false); !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("definitely expired unrenewable session error=%v", err)
	}
}

func TestOAuthStorageRefresherRunsOnEveryCachedTokenAccess(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"models":[{"slug":"m","display_name":"M","visibility":"list"}]}`)
			return
		}
		codexOK(w)
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	stored := validOAuthCredentials()
	var expectedAccess []string
	var calls int
	signedOut := false
	logoutErr := errors.New("fixture selected registration signed out")
	client.SetOAuthRefresher(func(_ context.Context, expected OAuthCredentials, force bool) (OAuthCredentials, error) {
		calls++
		expectedAccess = append(expectedAccess, expected.Access)
		if force {
			t.Error("cached token access forced rotation")
		}
		if signedOut {
			return OAuthCredentials{}, logoutErr
		}
		return cloneClientOAuthCredentials(stored), nil
	})
	if _, err := client.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored.Access, stored.Refresh = "cross-process-access", "cross-process-refresh"
	if _, err := client.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	signedOut = true
	if _, err := client.Models(context.Background()); !errors.Is(err, logoutErr) {
		t.Fatalf("local sign-out error=%v", err)
	}
	if calls != 3 || !reflect.DeepEqual(expectedAccess, []string{"access-old", "access-old", "cross-process-access"}) {
		t.Fatalf("reload calls=%d, expected tokens=%v", calls, expectedAccess)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(auths, []string{"Bearer access-old", "Bearer cross-process-access"}) {
		t.Fatalf("requests used stale or signed-out credentials: %v", auths)
	}
}

func TestOAuthStorageRefresherCannotMixRegistrationsOrScopes(t *testing.T) {
	for _, mutate := range []func(*OAuthCredentials){
		func(c *OAuthCredentials) { c.Subject = "different-subject" },
		func(c *OAuthCredentials) { c.ClientID = "other-client" },
		func(c *OAuthCredentials) { c.HostID = "other-host" },
		func(c *OAuthCredentials) { c.Issuer = "https://untrusted.invalid" },
		func(c *OAuthCredentials) { c.Scopes = []string{"openid"} },
	} {
		client, err := NewOAuth(ChatGPTResource, "m", ChatGPTIssuer, "issued-client-test", validOAuthCredentials())
		if err != nil {
			t.Fatal(err)
		}
		client.SetOAuthRefresher(func(_ context.Context, expected OAuthCredentials, _ bool) (OAuthCredentials, error) {
			mutate(&expected)
			return expected, nil
		})
		if _, err := client.oauth.access(context.Background(), false); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("mixed registration was accepted: %v", err)
		}
	}
}

func TestOAuthConcurrentLate401DoesNotRotateReplacementAgain(t *testing.T) {
	var tokens, oldRequests, newRequests atomic.Int32
	bothOld, newSucceeded := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/oauth/token" {
			tokens.Add(1)
			writeToken(w, "access-new")
			return
		}
		if r.Header.Get("Authorization") == "Bearer access-old" {
			if oldRequests.Add(1) == 1 {
				select {
				case <-bothOld:
				case <-r.Context().Done():
					return
				}
			} else {
				close(bothOld)
				select {
				case <-newSucceeded:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"detail":"old access rejected"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer access-new" {
			t.Error("retry used an unexpected token")
		}
		codexOK(w)
		if newRequests.Add(1) == 1 {
			close(newSucceeded)
		}
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, err := client.Stream(ctx, hello, nil, nil, nil); errs <- err }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if tokens.Load() != 1 || oldRequests.Load() != 2 || newRequests.Load() != 2 {
		t.Fatalf("rotated already-replaced rejection: tokens=%d old=%d new=%d", tokens.Load(), oldRequests.Load(), newRequests.Load())
	}
}

func TestOAuthStorageRefreshHandlesCrossProcess401Rotation(t *testing.T) {
	var mu sync.Mutex
	stored := validOAuthCredentials()
	var forces []bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			// A different process rotates while this request uses the old access.
			mu.Lock()
			stored.Access, stored.Refresh = "other-process-new", "other-process-refresh"
			mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer other-process-new" {
			t.Error("401 retry did not use the persisted cross-process rotation")
		}
		codexOK(w)
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	client.SetOAuthRefresher(func(_ context.Context, expected OAuthCredentials, force bool) (OAuthCredentials, error) {
		mu.Lock()
		defer mu.Unlock()
		forces = append(forces, force)
		// Storage owns expected-token comparison and does not rotate a token
		// already replaced on disk. No token endpoint request is necessary.
		if force && expected.Access == stored.Access {
			t.Error("test should have observed an already-replaced stored token")
		}
		return cloneClientOAuthCredentials(stored), nil
	})
	if _, err := client.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || !reflect.DeepEqual(forces, []bool{false, true}) {
		t.Fatalf("requests=%d callback force values=%v", requests.Load(), forces)
	}
}

func TestOAuthStreamingFailureDoesNotRefreshOrReplayPartialTurn(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/responses" {
			t.Error("partial streaming turn triggered token refresh")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\n\n")
		fmt.Fprint(w, "event: response.failed\ndata: {\"response\":{\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\",\"param\":\"model\"}}}\n\n")
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	var chunks []string
	_, _, ok, err := client.StreamUsage(context.Background(), hello, nil, func(s string) { chunks = append(chunks, s) }, nil)
	if err == nil || ok || requests.Load() != 1 || !reflect.DeepEqual(chunks, []string{"partial"}) {
		t.Fatalf("partial failure error=%v usage=%v requests=%d chunks=%v", err, ok, requests.Load(), chunks)
	}
}
