package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"lisa/internal/model"
)

func nameTestClient(t *testing.T, handler http.HandlerFunc) *model.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "local-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// nameFixture streams the given delta events then [DONE], rejecting extra
// POSTs (naming must be exactly one model round-trip).
func nameFixture(t *testing.T, events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %q", r.Method)
		}
		sendEvents(w, events...)
	}
}

// nameBody builds a single assistant delta carrying the given raw content.
func nameBody(content string) string {
	encoded, _ := json.Marshal(content)
	return `{"choices":[{"delta":{"role":"assistant","content":` + string(encoded) + `}}]}`
}

func TestGenerateSessionNameSuccess(t *testing.T) {
	var posts int
	var body []byte
	client := nameTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		body = buf
		posts++
		if posts > 1 {
			t.Errorf("naming made %d POSTs, want exactly one", posts)
		}
		nameFixture(t, nameBody("  \"Feature scaffolding\".\n"), `[DONE]`)(w, r)
	})
	history := sampleHistory()
	before := append([]model.Message(nil), history...)

	name, err := GenerateSessionName(context.Background(), client, history)
	if err != nil {
		t.Fatalf("generateSessionName: %v", err)
	}
	if name != "Feature scaffolding" {
		t.Errorf("name = %q, want %q", name, "Feature scaffolding")
	}

	// One POST, no tools field, verbatim history + the naming instruction last.
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools *json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.Tools != nil {
		t.Errorf("request carries a tools field, want none")
	}
	if len(req.Messages) != len(history)+1 {
		t.Fatalf("message count = %d, want %d", len(req.Messages), len(history)+1)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		t.Errorf("last message role = %q, want user", last.Role)
	}
	for _, fragment := range []string{"session name", "2", "6", "trailing period"} {
		if !strings.Contains(last.Content, fragment) {
			t.Errorf("instruction %q missing %q", last.Content, fragment)
		}
	}
	for i, msg := range history {
		if req.Messages[i].Role != msg.Role || req.Messages[i].Content != msg.Content {
			t.Errorf("message[%d] = %s/%q, want %s/%q (must be verbatim)",
				i, req.Messages[i].Role, req.Messages[i].Content, msg.Role, msg.Content)
		}
	}
	if !reflect.DeepEqual(history, before) {
		t.Errorf("input history mutated: got %+v, want %+v", history, before)
	}
}

func TestGenerateSessionNameSanitize(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"double quotes", `"Feature scaffolding"`, "Feature scaffolding"},
		{"single quotes", "'Fix parser offset bug'", "Fix parser offset bug"},
		{"internal newlines", "Add\nsession\nname\tlogic", "Add session name logic"},
		{"trailing period", "Feature scaffolding.", "Feature scaffolding"},
		{"extra whitespace", "  feature   scaffolding  ", "feature scaffolding"},
		{"five words kept", "one two three four five", "one two three four five"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := nameTestClient(t, nameFixture(t, nameBody(tc.raw), `[DONE]`))
			got, err := GenerateSessionName(context.Background(), client, sampleHistory())
			if err != nil {
				t.Fatalf("generateSessionName: %v", err)
			}
			if got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGenerateSessionNameSanitizeRejections(t *testing.T) {
	hist := sampleHistory()
	cases := []struct {
		name string
		raw  string
	}{
		{"seven words", "one two three four five six seven"},
		{"one word", "Refactoring"},
		{"sixty one runes", "Very long test case name that runs on past the sixty rune limit yes"},
		{"quote stripped to one word", `"Refactoring"`},
		{"empty raw", "   \n  "},
		{"empty quotes", `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts int
			client := nameTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				posts++
				if posts > 1 {
					t.Errorf("naming made %d POSTs, want one", posts)
				}
				nameFixture(t, nameBody(tc.raw), `[DONE]`)(w, r)
			})
			before := append([]model.Message(nil), hist...)
			_, err := GenerateSessionName(context.Background(), client, hist)
			if err == nil {
				t.Fatalf("raw output %q: want error", tc.raw)
			}
			if !reflect.DeepEqual(hist, before) {
				t.Errorf("input history mutated: got %+v, want %+v", hist, before)
			}
		})
	}
}

func TestGenerateSessionNameValidationErrors(t *testing.T) {
	hist := []model.Message{{Role: "user", Content: "fix the parser bug"}}

	if name, err := GenerateSessionName(context.Background(), nil, sampleHistory()); err == nil {
		t.Fatalf("nil client: want error, got name %q", name)
	}
	if name, err := GenerateSessionName(context.Background(), nil, nil); err == nil {
		t.Fatalf("nil client and empty History: want error, got name %q", name)
	}
	if name, err := GenerateSessionName(context.Background(), nil, hist); err == nil {
		t.Fatalf("history without an assistant message: want error, got name %q", name)
	}
}

func TestGenerateSessionNameStreamErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler func(w http.ResponseWriter, _ *http.Request)
		check   func(t *testing.T, err error)
	}{
		{"non-2xx", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(502)
		}, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "502") {
				t.Errorf("err = %v, want HTTP 502 failure", err)
			}
		}},
		{"error event", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"error":{"message":"model failed"}}`, `[DONE]`)
		}, func(t *testing.T, err error) {
			if err == nil {
				t.Errorf("err = nil, want model failure")
			}
		}},
		{"done before text", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `[DONE]`)
		}, func(t *testing.T, err error) {
			if err == nil {
				t.Errorf("err = nil, want failure for empty stream")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := nameTestClient(t, tc.handler)
			hist := sampleHistory()
			before := append([]model.Message(nil), hist...)
			_, err := GenerateSessionName(context.Background(), client, hist)
			tc.check(t, err)
			if !reflect.DeepEqual(hist, before) {
				t.Errorf("input history mutated: got %+v, want %+v", hist, before)
			}
		})
	}
}

func TestGenerateSessionNameContextCancelled(t *testing.T) {
	// The server blocks until the request context is done, simulating a
	// mid-stream cancellation.
	client := nameTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	hist := sampleHistory()
	before := append([]model.Message(nil), hist...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GenerateSessionName(ctx, client, hist); err == nil {
		t.Fatal("cancelled context: want error")
	} else if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context cancel") {
		t.Errorf("err = %v, want a context cancellation error", err)
	}
	if !reflect.DeepEqual(hist, before) {
		t.Errorf("input history mutated: got %+v, want %+v", hist, before)
	}
}
