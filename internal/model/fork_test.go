package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestForkInheritsConfigurationWithoutSharingTelemetry(t *testing.T) {
	parent := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer static-key" || r.Header.Get("x-opencode-session") != "parent-session" {
			t.Error("fork did not preserve the inherited provider identity")
		}
		sendEvents(w, `{"choices":[{"delta":{"content":"fork answer"}}]}`, `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`, `[DONE]`)
	})
	parent.apiKey = "static-key"
	parent.SetSessionHeader("x-opencode-session")
	parent.SetSession("parent-session")
	parent.markConnected()
	parent.setTokenUsage(parent.beginTokenUsage(), usageReport{usage: TokenUsage{Prompt: 90, Completion: 20, PromptSeen: true}, seen: usageSeen{prompt: true, completion: true}, ok: true})
	child, err := parent.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if child.http != parent.http || child.Model() != parent.Model() || child.Base() != parent.Base() || !child.checked {
		t.Fatal("fork did not inherit the configuration snapshot")
	}
	if _, ok := child.LastTokenUsage(); ok || child.tokenRequest != 0 {
		t.Fatal("fork inherited the parent's request telemetry")
	}
	if _, err := child.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if usage, ok := parent.LastTokenUsage(); !ok || usage.Prompt != 90 || usage.Completion != 20 {
		t.Fatal("fork overwrote parent telemetry")
	}
	if usage, ok := child.LastTokenUsage(); !ok || usage.Prompt != 7 || usage.Completion != 2 {
		t.Fatal("fork did not capture its own telemetry")
	}
	if err := child.SetModel("child-model"); err != nil || parent.Model() == child.Model() {
		t.Fatal("fork model reconfiguration changed the parent")
	}
}

func TestForkOAuthSharesStorageRefresherAndInvalidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer saved-access" {
			t.Error("fork did not use the shared storage callback")
		}
		codexOK(w)
	}))
	t.Cleanup(server.Close)
	parent := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	child, err := parent.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if parent.oauth != child.oauth || parent.oauth.client != parent {
		t.Fatal("fork copied credentials or rebound the OAuth owner")
	}
	var callbacks atomic.Int32
	child.SetOAuthRefresher(func(_ context.Context, creds OAuthCredentials, force bool) (OAuthCredentials, error) {
		callbacks.Add(1)
		if force {
			t.Error("shared callback unexpectedly forced rotation")
		}
		creds.Access = "saved-access"
		return creds, nil
	})
	if _, err := parent.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Stream(context.Background(), hello, nil, nil, nil); err != nil || callbacks.Load() != 2 {
		t.Fatalf("child stream error=%v callbacks=%d", err, callbacks.Load())
	}
	child.InvalidateOAuth()
	for _, client := range []*Client{parent, child} {
		if _, err := client.Models(context.Background()); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("invalidated fork could access models: %v", err)
		}
		if _, err := client.Fork(); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("invalidated fork could create a usable child: %v", err)
		}
	}
	if callbacks.Load() != 2 || parent.oauth.creds.Access != "" || parent.oauth.creds.Refresh != "" {
		t.Fatal("local sign-out allowed a late token reload")
	}
}

func TestForkOAuthConcurrentStorageAccessRotatesOnceAndReloadsEveryCaller(t *testing.T) {
	var tokens, responses atomic.Int32
	release := make(chan struct{})
	var persisted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/oauth/token" {
			tokens.Add(1)
			select {
			case <-release:
				writeToken(w, "shared-rotation")
			case <-r.Context().Done():
			}
			return
		}
		responses.Add(1)
		if !persisted.Load() || r.Header.Get("Authorization") != "Bearer shared-rotation" {
			t.Error("fork used an unpersisted or stale rotated token")
		}
		codexOK(w)
	}))
	t.Cleanup(server.Close)
	parent := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	var storageMu sync.Mutex
	stored := expiredOAuthCredentials()
	var callbacks atomic.Int32
	parent.SetOAuthRefresher(func(ctx context.Context, expected OAuthCredentials, force bool) (OAuthCredentials, error) {
		callbacks.Add(1)
		storageMu.Lock()
		defer storageMu.Unlock()
		if stored.Access != expected.Access || usableClientOAuthToken(stored) && !force {
			return cloneClientOAuthCredentials(stored), nil
		}
		fresh, err := RefreshCredentials(ctx, parent.http, stored)
		if err != nil {
			return OAuthCredentials{}, err
		}
		stored = fresh
		persisted.Store(true)
		return cloneClientOAuthCredentials(stored), nil
	})
	const n = 5
	children := make([]*Client, n)
	waiting := make(chan struct{}, n)
	errs := make(chan error, n)
	for i := range children {
		var err error
		children[i], err = parent.Fork()
		if err != nil {
			t.Fatal(err)
		}
		go func(child *Client) {
			_, err := child.oauth.access(&observeOAuthWait{Context: context.Background(), wait: waiting}, false)
			errs <- err
		}(children[i])
	}
	waitOAuthSignals(t, waiting, n)
	close(release)
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if callbacks.Load() != n || tokens.Load() != 1 {
		t.Fatalf("callbacks=%d refreshes=%d; want one reload per caller and one rotation", callbacks.Load(), tokens.Load())
	}
	for _, child := range children {
		if _, err := child.Stream(context.Background(), hello, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if responses.Load() != n || tokens.Load() != 1 || callbacks.Load() != 2*n {
		t.Fatalf("responses=%d refreshes=%d callbacks=%d", responses.Load(), tokens.Load(), callbacks.Load())
	}
}

func TestForkOAuthInvalidationCannotResurrectLateRefresh(t *testing.T) {
	parent, err := NewOAuth(ChatGPTResource, "m", ChatGPTIssuer, "issued-client-test", validOAuthCredentials())
	if err != nil {
		t.Fatal(err)
	}
	child, err := parent.Fork()
	if err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	parent.SetOAuthRefresher(func(_ context.Context, creds OAuthCredentials, _ bool) (OAuthCredentials, error) {
		close(started)
		<-finish // deliberately simulate a callback that ignores cancellation
		creds.Access = "late-token"
		return creds, nil
	})
	result := make(chan error, 1)
	go func() { _, err := child.oauth.access(context.Background(), false); result <- err }()
	waitOAuthSignals(t, started, 1)
	parent.oauth.mu.Lock()
	flight := parent.oauth.flight
	parent.oauth.mu.Unlock()
	child.InvalidateOAuth()
	if err := <-result; !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("pending token access survived sign-out: %v", err)
	}
	close(finish)
	waitOAuthSignals(t, flight.done, 1)
	parent.oauth.mu.Lock()
	defer parent.oauth.mu.Unlock()
	if !parent.oauth.invalidated || parent.oauth.creds.Access != "" || parent.oauth.creds.Refresh != "" || flight.token != "" {
		t.Fatal("late refresh resurrected locally signed-out credentials")
	}
}

func TestForkOAuthInvalidationCancelsActiveStream(t *testing.T) {
	serverCancelled := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		sendEvents(w, `{"type":"response.output_text.delta","delta":"first"}`)
		<-r.Context().Done()
		close(serverCancelled)
	}))
	t.Cleanup(server.Close)
	parent := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	child, err := parent.Fork()
	if err != nil {
		t.Fatal(err)
	}
	text := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		_, err := child.Stream(context.Background(), hello, nil, func(string) { text <- struct{}{} }, nil)
		result <- err
	}()
	waitOAuthSignals(t, text, 1)
	parent.InvalidateOAuth()
	if err := <-result; err == nil {
		t.Fatal("sign-out returned an active stream as successful")
	}
	waitOAuthSignals(t, serverCancelled, 1)
	if _, err := child.Stream(context.Background(), hello, nil, nil, nil); err == nil || requests.Load() != 1 {
		t.Fatalf("invalidated stream retried: error=%v requests=%d", err, requests.Load())
	}
}

func TestForkForTaskGatesResponsesRetriesButNotOAuthOrModels(t *testing.T) {
	var tokens, responses, models atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/oauth/token":
			writeToken(w, fmt.Sprintf("token-%d", tokens.Add(1)))
		case "/v1/models":
			models.Add(1)
			fmt.Fprint(w, `{"models":[{"slug":"m","display_name":"M","visibility":"list"}]}`)
		case "/v1/responses":
			if responses.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			codexOK(w)
		default:
			t.Errorf("unexpected task request %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	parent := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	var reservations atomic.Int32
	child, err := parent.ForkForTask(func(context.Context) error { reservations.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := child.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if reservations.Load() != 2 || responses.Load() != 2 || models.Load() != 1 || tokens.Load() != 2 {
		t.Fatalf("reservations=%d responses=%d models=%d tokens=%d", reservations.Load(), responses.Load(), models.Load(), tokens.Load())
	}
}

func TestForkForTaskDeniedRequestPreservesCallbackError(t *testing.T) {
	var requests atomic.Int32
	parent := localClient(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	denied := errors.New("fixture task budget exhausted")
	child, err := parent.ForkForTask(func(context.Context) error { return denied })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := child.Stream(ctx, hello, nil, nil, nil); !errors.Is(err, denied) || requests.Load() != 0 {
		t.Fatalf("denied fork error=%v requests=%d", err, requests.Load())
	}
}
