package headless

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"likha/internal/model"
)

// rerouteTransport sends every request for the public ChatGPT resource to a
// local test server: the OAuth client refuses any other endpoint.
type rerouteTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (rt rerouteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	routed := req.Clone(req.Context())
	routed.URL.Scheme, routed.URL.Host, routed.Host = rt.target.Scheme, rt.target.Host, ""
	return rt.next.RoundTrip(routed)
}

// rotatingSignIn is a signed-in ChatGPT client whose first token access
// rotates every token, and whose fake Responses endpoint echoes the bearer
// token each request carried, split across two text deltas.
func rotatingSignIn(t *testing.T) (client *model.Client, initial []string, rotated string) {
	t.Helper()
	const rotatedAccess = "access-rotated-6f0c1d2e3b4a"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list"}]}`)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "text/event-stream")
		half := len(token) / 2
		for _, delta := range []string{"the token is " + token[:half], token[half:] + " today"} {
			fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"delta\":%q}\n\n", delta)
		}
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, `data: {"response":{"status":"completed","output":[]}}`+"\n\n")
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	creds := model.OAuthCredentials{
		Access: "access-initial-9a8b7c6d5e4f", Refresh: "refresh-initial-1a2b3c4d5e6f", IDToken: "id-initial-0f1e2d3c4b5a",
		Expires: time.Now().Add(time.Hour).UnixMilli(), Issuer: model.ChatGPTIssuer, Subject: "rotation-subject",
		ClientID: "issued-client-rotation", HostID: "urn:uuid:33333333-3333-4333-8333-333333333333", TokenType: "Bearer",
		Scopes: []string{"openid", model.ChatGPTPlanScope},
	}
	base, err := model.NewOAuth(model.ChatGPTResource, "gpt-5.5", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		t.Fatal(err)
	}
	var rotations atomic.Int32
	base.SetOAuthRefresher(func(_ context.Context, current model.OAuthCredentials, _ bool) (model.OAuthCredentials, error) {
		if rotations.Add(1) == 1 {
			current.Access, current.Refresh, current.IDToken = rotatedAccess, "refresh-rotated-7e6d5c4b3a29", "id-rotated-8d7c6b5a4f3e"
		}
		return current, nil
	})
	client, err = base.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper {
		return rerouteTransport{target: target, next: next}
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, []string{creds.Access, creds.Refresh, creds.IDToken}, rotatedAccess
}

// A sign-in that refreshes in the middle of a run rotates its tokens after
// the run's secrets were listed. The new token is redacted all the same,
// wherever the model echoes it.
func TestRotatedSignInTokenIsRedacted(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		run := newTestRun(t, newFakeProvider(t), "what is the token?")
		client, initial, rotated := rotatingSignIn(t)
		run.cfg.Client, run.cfg.Secrets, run.cfg.Provider, run.cfg.JSON = client, initial, "openai", jsonMode
		code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
		if code != ExitCompleted {
			t.Fatalf("json %v: exit = %d\nstdout:\n%s\nstderr:\n%s", jsonMode, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "the token is ") || !strings.Contains(stdout, " today") {
			t.Fatalf("json %v: the answer is missing:\n%s", jsonMode, stdout)
		}
		if strings.Contains(stdout+stderr, rotated) || strings.Contains(stdout+stderr, rotated[:len(rotated)/2]) {
			t.Fatalf("json %v: the rotated token showed:\nstdout:\n%s\nstderr:\n%s", jsonMode, stdout, stderr)
		}
		saved, err := run.store.Load(run.cfg.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.History) != 2 || !strings.Contains(saved.History[1].Content, rotated) {
			t.Fatalf("json %v: the model did not echo the rotated token: %+v", jsonMode, saved.History)
		}
	}
}
