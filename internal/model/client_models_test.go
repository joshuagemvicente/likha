package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientOAuthModelsUsesPublicAccountCatalog(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("catalog request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access-old" || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != UserAgent {
			t.Error("catalog request lacks bearer auth or standard headers")
		}
		for _, header := range []string{"originator", "ChatGPT-Account-Id", "session-id", "x-private-session"} {
			if r.Header.Get(header) != "" {
				t.Errorf("catalog sent unsupported header %s", header)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		// Metadata names from other providers are not a SIWC contract.
		fmt.Fprint(w, `{"models":[
			{"slug":"second","display_name":"Second (Account)","visibility":"list","context_window":999999,"context_length":999999,"limit":{"context":999999}},
			{"slug":"hidden","display_name":"Hidden","visibility":"hide"},
			{"slug":"first","display_name":"First (Account)","visibility":"list"},
			{"slug":"internal","display_name":"Internal"}]}`)
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	client.SetSessionHeader("x-private-session")
	client.SetSession("unused-session")
	if err := client.SetModel("another-model"); err != nil {
		t.Fatal(err)
	}
	details, err := client.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelDetails{{ID: "second", DisplayName: "Second (Account)"}, {ID: "first", DisplayName: "First (Account)"}}
	if !reflect.DeepEqual(details, want) || requests.Load() != 1 {
		t.Fatalf("catalog = %#v, requests=%d; want %#v and one GET", details, requests.Load(), want)
	}
}

func TestClientOAuthModelsRejectsIncompatibleOrEmptyCatalog(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"id":"gpt-5.5"}]}`,
		`[{"slug":"m","display_name":"Model","visibility":"list"}]`,
		`{"models":null}`,
		`{"models":{}}`,
		`{"models":[]}`,
		`{"models":[{"slug":"hidden","display_name":"Hidden","visibility":"hide"}]}`,
		`{"models":[{"display_name":"Model","visibility":"list"}]}`,
		`{"models":[{"slug":"model","visibility":"list"}]}`,
		`{"models":[{"slug":"model","display_name":"Model","visibility":true}]}`,
		`{"models":[null]}`,
		`not-json`,
		``,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, body)
			}))
			t.Cleanup(server.Close)
			client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
			details, err := client.Models(context.Background())
			if !errors.Is(err, ErrUnexpectedResponse) || len(details) != 0 {
				t.Fatalf("Models = (%#v, %v), want actionable catalog failure without fallback", details, err)
			}
			if !strings.Contains(err.Error(), "discovery") {
				t.Fatalf("catalog failure is not actionable: %v", err)
			}
		})
	}
}

func TestClientModelsDelegatesAPIKeyModelDetails(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer api-test" {
			t.Errorf("non-OAuth catalog request = %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"api-model","context_length":65536}]}`)
	})
	client.apiKey = "api-test"
	details, err := client.Models(context.Background())
	want := []ModelDetails{{ID: "api-model", ContextWindow: 65536, ContextWindowSource: "context_length"}}
	if err != nil || !reflect.DeepEqual(details, want) {
		t.Fatalf("non-OAuth Models = (%#v, %v), want %#v", details, err, want)
	}
}

func TestOAuthCheckDetectsRevocationWithCachedToken(t *testing.T) {
	var tokens, models, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/oauth/token":
			tokens.Add(1)
			writeToken(w, "replacement-rejected")
		case "/v1/models":
			models.Add(1)
			if r.Method != http.MethodGet || r.Header.Get("Authorization") == "" {
				t.Error("check did not send an authenticated GET")
			}
			w.Header().Set("x-request-id", "request-revoked")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"detail":"selected registration was disconnected"}`)
		default:
			posts.Add(1)
			t.Error("check sent a prompt instead of using /models")
		}
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	err := client.Check(context.Background())
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "request-revoked") || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("Check error = %v, want the actual revocation failure and request ID", err)
	}
	if models.Load() != 2 || tokens.Load() != 1 || posts.Load() != 0 || client.checked {
		t.Fatalf("models=%d refreshes=%d prompts=%d checked=%v", models.Load(), tokens.Load(), posts.Load(), client.checked)
	}
}

func TestOAuthEnsureConnectedRechecksEveryRunBoundary(t *testing.T) {
	var models atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Error("connection probe did not use /models")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if models.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"models":[{"slug":"live-model","display_name":"Live Model","visibility":"list"}]}`)
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	if err := client.EnsureConnected(context.Background()); err == nil || client.checked {
		t.Fatal("a failed probe was remembered as connected")
	}
	if err := client.EnsureConnected(context.Background()); err != nil || !client.checked {
		t.Fatalf("successful probe: %v, checked=%v", err, client.checked)
	}
	if err := client.EnsureConnected(context.Background()); err != nil || models.Load() != 3 {
		t.Fatalf("EnsureConnected cached the successful OAuth probe: %v, calls=%d", err, models.Load())
	}
	// Explicit Check is a fresh remote revocation probe, even after success.
	if err := client.Check(context.Background()); err != nil || models.Load() != 4 {
		t.Fatalf("Check did not make an authenticated GET: %v, calls=%d", err, models.Load())
	}
}

func TestOAuthRequestsDoNotRetryPolicyOrUsageFailures(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, operation := range []string{"models", "stream"} {
			t.Run(fmt.Sprintf("%s/%d", operation, status), func(t *testing.T) {
				var requests, tokens atomic.Int32
				body := `{"error":{"code":"subscription_sharing_usage_limit_exceeded","param":"model"}}`
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/accounts/oauth/token" {
						tokens.Add(1)
						writeToken(w, "should-not-be-used")
						return
					}
					requests.Add(1)
					if r.Header.Get("Authorization") != "Bearer access-old" {
						t.Error("request changed its billing credentials")
					}
					w.Header().Set("x-request-id", "request-limit")
					w.WriteHeader(status)
					fmt.Fprint(w, body)
				}))
				t.Cleanup(server.Close)
				client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
				var err error
				if operation == "models" {
					_, err = client.Models(context.Background())
				} else {
					_, err = client.Stream(context.Background(), []Message{{Role: "user", Content: "test"}}, nil, nil, nil)
				}
				if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || !strings.Contains(err.Error(), body) || !strings.Contains(err.Error(), "request-limit") {
					t.Fatalf("lost HTTP diagnostic: %v", err)
				}
				if requests.Load() != 1 || tokens.Load() != 0 || client.APIKey() != "" {
					t.Fatalf("requests=%d tokens=%d: retry or billing fallback", requests.Load(), tokens.Load())
				}
			})
		}
	}
}

func TestOAuthModelsDoesNotFollowRedirect(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		leaked.Store(true)
		fmt.Fprint(w, `{"models":[{"slug":"m","display_name":"M","visibility":"list"}]}`)
	}))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	_, err := client.Models(context.Background())
	if !errors.Is(err, ErrUnexpectedResponse) || leaked.Load() {
		t.Fatalf("redirect error=%v leaked=%v", err, leaked.Load())
	}
}
