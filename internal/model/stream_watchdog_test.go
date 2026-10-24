package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shortenStreamIdle shrinks the idle limit for one test; tests using it must
// not run in parallel.
func shortenStreamIdle(t *testing.T, limit time.Duration) {
	t.Helper()
	previous := streamIdleTimeout
	streamIdleTimeout = limit
	t.Cleanup(func() { streamIdleTimeout = previous })
}

func TestSilentModelStreamIsCancelledAsStalled(t *testing.T) {
	shortenStreamIdle(t, 100*time.Millisecond)
	cases := []struct {
		provider, id, first string
	}{
		{"openai", "gpt-5.5", "event: response.created\n"},
		{"claude", "claude-opus-5-5", "event: message_start\n"},
		{"groq", "openai/gpt-oss-120b", ": keep-alive\n"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.first)
				w.(http.Flusher).Flush()
				select { // Go silent until the client gives up.
				case <-req.Context().Done():
				case <-time.After(10 * time.Second):
				}
			}))
			defer server.Close()
			p, _ := LookupProvider(tc.provider)
			client, err := New(p.BaseURL, tc.id, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.http.Transport = variantTestTransport{target, http.DefaultTransport}
			start := time.Now()
			_, err = client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
			if !errors.Is(err, ErrStreamStalled) || !strings.Contains(err.Error(), "no data for 100ms") {
				t.Fatalf("Stream error = %v, want a stall", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("stall took %s to report", elapsed)
			}
		})
	}
}

func TestSlowButActiveModelStreamIsNotStalled(t *testing.T) {
	shortenStreamIdle(t, 150*time.Millisecond)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Total time well past the idle limit, but no single gap reaches it.
		for range 8 {
			fmt.Fprint(w, ": keep-alive\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := New(server.URL+"/v1", "local-model", "")
	if err != nil {
		t.Fatal(err)
	}
	message, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
	if err != nil || message.Content != "done" {
		t.Fatalf("Stream = %q, %v", message.Content, err)
	}
}

func TestModelClientsBoundTheWaitForResponseHeaders(t *testing.T) {
	if modelTransport.timeout != modelResponseHeaderTimeout {
		t.Fatalf("header timeout = %s, want %s", modelTransport.timeout, modelResponseHeaderTimeout)
	}
	for name, client := range map[string]*http.Client{"model": noRedirectHTTPClient(), "model list": sharedListClient} {
		if client.Transport != modelTransport {
			t.Errorf("%s client does not use the bounded model transport", name)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select { // Accept the request but never answer it.
		case <-req.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()
	routeToServer(t, server)
	client := &http.Client{Transport: newModelTransport(50 * time.Millisecond)}
	resp, err := client.Get("http://models.example.test/v1/chat")
	if err == nil {
		resp.Body.Close()
		t.Fatal("request without response headers did not time out")
	}
	if !errors.Is(err, errResponseHeaderTimeout) || !strings.Contains(err.Error(), "within 50ms") {
		t.Fatalf("error = %v, want a header timeout", err)
	}
}

// routeToServer points http.DefaultTransport at server for every host, so a
// test can use a non-loopback URL while traffic stays on 127.0.0.1.
func routeToServer(t *testing.T, server *httptest.Server) {
	t.Helper()
	previous := http.DefaultTransport
	address := server.Listener.Addr().String()
	var dialer net.Dialer
	http.DefaultTransport = &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}}
	t.Cleanup(func() { http.DefaultTransport = previous })
}

// A local server may send no headers until the model is loaded and the
// prompt evaluated, so loopback hosts have no header limit.
func TestLoopbackModelServersHaveNoHeaderLimit(t *testing.T) {
	const limit = 50 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(4 * limit) // a slow model load
		fmt.Fprint(w, "loaded")
	}))
	defer server.Close()
	client := &http.Client{Transport: newModelTransport(limit)}
	port := server.Listener.Addr().(*net.TCPAddr).Port
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			resp, err := client.Get(fmt.Sprintf("http://%s/v1/chat", net.JoinHostPort(host, strconv.Itoa(port))))
			if err != nil {
				t.Fatalf("slow loopback server hit the header limit: %v", err)
			}
			defer resp.Body.Close()
			if body, err := io.ReadAll(resp.Body); err != nil || string(body) != "loaded" {
				t.Fatalf("body = %q, %v", body, err)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LocalHost", true},
		{"127.0.0.1", true},
		{"127.4.5.6", true},
		{"::1", true},
		{"api.openai.com", false},
		{"10.0.0.2", false},
		{"localhost.example.com", false},
	}
	for _, tc := range cases {
		if got := isLoopbackHost(tc.host); got != tc.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// Process-wide routing installed on http.DefaultTransport after start-up
// (proxies, test reroutes) must still reach model traffic.
func TestModelTransportFollowsTheCurrentDefaultTransport(t *testing.T) {
	routed := false
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		routed = true
		return previous.RoundTrip(req)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()
	resp, err := noRedirectHTTPClient().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !routed {
		t.Fatal("model client bypassed http.DefaultTransport")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
