package model

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// observingTransport records the path and body of each request it forwards,
// reading the body from GetBody so the forwarded request keeps its own.
type observingTransport struct {
	next   http.RoundTripper
	mu     sync.Mutex
	paths  []string
	bodies []string
}

func (o *observingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := ""
	if req.GetBody != nil {
		copied, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(copied)
		if err != nil {
			return nil, err
		}
		body = string(raw)
	}
	o.mu.Lock()
	o.paths = append(o.paths, req.URL.Path)
	o.bodies = append(o.bodies, body)
	o.mu.Unlock()
	return o.next.RoundTrip(req)
}

func (o *observingTransport) seen() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.paths...)
}

func TestForkWithTransportSeesEveryChatAttemptAndKeepsParent(t *testing.T) {
	var posts atomic.Int32
	var bodies []string
	var mu sync.Mutex
	parent := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		if posts.Add(1) == 1 {
			// The first attempt is refused for stream_options, which makes
			// Stream send a second attempt without it.
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"unknown field stream_options"}`)
			return
		}
		sendEvents(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	})
	parentTransport := parent.http.Transport
	observer := &observingTransport{}
	child, err := parent.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper {
		observer.next = next
		return observer
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := child.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := observer.seen(); len(got) != 2 || got[0] != "/v1/chat/completions" || got[1] != "/v1/chat/completions" {
		t.Fatalf("wrapper saw %v, want two chat attempts", got)
	}
	mu.Lock()
	received := append([]string(nil), bodies...)
	mu.Unlock()
	for i, body := range received {
		// The server received exactly what the wrapper read: reading a copy
		// left the forwarded body intact.
		if body == "" || body != observer.bodies[i] {
			t.Fatalf("attempt %d: server body %q, wrapper body %q", i, body, observer.bodies[i])
		}
	}
	if parent.http.Transport != parentTransport || child.http == parent.http {
		t.Fatal("ForkWithTransport changed the parent's HTTP client")
	}
	if _, err := parent.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := observer.seen(); len(got) != 2 {
		t.Fatalf("parent request passed through the fork's wrapper: %v", got)
	}
}

func TestForkWithTransportSeesCodexRetryButNotRefresh(t *testing.T) {
	var tokens, responses atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/oauth/token":
			writeToken(w, fmt.Sprintf("token-%d", tokens.Add(1)))
		case "/v1/responses":
			if responses.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			codexOK(w)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	parent := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	observer := &observingTransport{}
	child, err := parent.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper {
		observer.next = next
		return observer
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := child.Stream(context.Background(), hello, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := observer.seen()
	if len(got) != 2 || responses.Load() != 2 || tokens.Load() != 2 {
		t.Fatalf("wrapper saw %v; responses=%d tokens=%d", got, responses.Load(), tokens.Load())
	}
	for _, path := range got {
		if strings.Contains(path, "oauth") {
			t.Fatalf("OAuth refresh passed through the fork's wrapper: %v", got)
		}
	}
}

func TestForkWithTransportRejectsMissingWrapper(t *testing.T) {
	parent := localClient(t, func(http.ResponseWriter, *http.Request) {})
	if _, err := parent.ForkWithTransport(nil); err == nil {
		t.Fatal("nil wrapper accepted")
	}
	if _, err := parent.ForkWithTransport(func(http.RoundTripper) http.RoundTripper { return nil }); err == nil {
		t.Fatal("wrapper returning nil accepted")
	}
}
