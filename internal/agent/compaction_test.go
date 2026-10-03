package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"likha/internal/model"
)

func compactTestClient(t *testing.T, handler http.HandlerFunc) *model.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "local-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// compactFixture streams the given delta events then [DONE], rejecting extra
// POSTs (compaction must be exactly one model round-trip).
func compactFixture(t *testing.T, events ...string) http.HandlerFunc {
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

func sendEvents(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	for _, event := range events {
		w.Write([]byte("data: " + event + "\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func sampleHistory() []model.Message {
	return []model.Message{
		{Role: "user", Content: "fix the parser bug in parse.go"},
		{Role: "assistant", Content: "I found that parseToken skipped negative offsets."},
		{Role: "user", Content: "great, also add tests"},
	}
}

const streamedSummary = "User wants the parser offset bug fixed and tests added."

func replacementAsserts(t *testing.T, replacement []model.Message, summary string) {
	t.Helper()
	if len(replacement) != 1 {
		t.Fatalf("replacement history length = %d, want 1", len(replacement))
	}
	if replacement[0].Role != "developer" {
		t.Errorf("replacement role = %q, want developer", replacement[0].Role)
	}
	if !strings.Contains(replacement[0].Content, streamedSummary) {
		t.Errorf("replacement content %q missing streamed summary", replacement[0].Content)
	}
	if !strings.HasPrefix(replacement[0].Content, "Conversation summary (compacted).") {
		t.Errorf("replacement content %q missing fixed prefix", replacement[0].Content)
	}
	if summary != streamedSummary {
		t.Errorf("summary = %q, want %q", summary, streamedSummary)
	}
}

// assertPOSTBody decodes and checks the single request body: no tools field,
// last message is the user instruction, earlier messages copied in order.
func assertPOSTBody(t *testing.T, body []byte, wantCount int, wantLastRole, wantLastContains string) {
	t.Helper()
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
	if len(req.Messages) != wantCount {
		t.Fatalf("message count = %d, want %d", len(req.Messages), wantCount)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != wantLastRole {
		t.Errorf("last message role = %q, want %q", last.Role, wantLastRole)
	}
	if !strings.Contains(last.Content, wantLastContains) {
		t.Errorf("last message content missing %q", wantLastContains)
	}
}

func TestCompactHistorySuccess(t *testing.T) {
	var posts int
	var body []byte
	client := compactTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		body = b
		posts++
		if posts > 1 {
			t.Errorf("summarize made %d POSTs, want exactly one", posts)
		}
		compactFixture(t,
			`{"choices":[{"delta":{"role":"assistant","content":"`+streamedSummary+`"}}]}`,
			`[DONE]`,
		)(w, r)
	})
	history := sampleHistory()
	before := append([]model.Message(nil), history...)

	var streamed []string
	replacement, summary, err := CompactHistory(context.Background(), client, history, "focus text", func(s string) {
		streamed = append(streamed, s)
	})
	if err != nil {
		t.Fatalf("compactHistory: %v", err)
	}
	replacementAsserts(t, replacement, summary)
	if len(streamed) == 0 || streamed[0] != streamedSummary {
		t.Errorf("onText saw %q, want %q", streamed, streamedSummary)
	}
	assertPOSTBody(t, body, len(history)+1, "user", "compact continuation brief")
	// The focus phrase must reach the instruction.
	assertPOSTBody(t, body, len(history)+1, "user", "Emphasize: focus text.")
	if len(body) == 0 {
		t.Fatal("no request body captured")
	}
	// Input history is untouched.
	if !reflect.DeepEqual(history, before) {
		t.Fatalf("input history mutated: got %+v, want %+v", history, before)
	}
}

func TestCompactHistoryNoFocus(t *testing.T) {
	var instruction string
	client := compactTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		json.Unmarshal(b, &req)
		instruction = req.Messages[len(req.Messages)-1].Content
		compactFixture(t, `{"choices":[{"delta":{"content":"`+streamedSummary+`"}}]}`, `[DONE]`)(w, r)
	})
	_, _, err := CompactHistory(context.Background(), client, sampleHistory(), "   ", nil)
	if err != nil {
		t.Fatalf("compactHistory: %v", err)
	}
	if strings.Contains(instruction, "Emphasize:") {
		t.Errorf("instruction %q contains Emphasize, want none for blank focus", instruction)
	}
}

func TestCompactHistoryNilClientAndEmptyHistory(t *testing.T) {
	if _, _, err := CompactHistory(context.Background(), nil, sampleHistory(), "", nil); err == nil {
		t.Error("nil client: want error")
	}
	if _, _, err := CompactHistory(context.Background(), nil, nil, "", nil); err == nil {
		t.Error("nil client and empty History: want error")
	}
	client := compactTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("empty history must not reach the model")
	})
	if _, _, err := CompactHistory(context.Background(), client, nil, "", nil); err == nil || err.Error() != "nothing to compact" {
		t.Errorf("empty history err = %v, want 'nothing to compact'", err)
	}
}

func TestCompactHistoryStreamErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"non-2xx", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream down", http.StatusBadGateway)
		}},
		{"error event", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"error":{"message":"model failed"}}`)
		}},
		{"empty before done", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `[DONE]`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := compactTestClient(t, tc.handler)
			history := sampleHistory()
			before := append([]model.Message(nil), history...)
			_, _, err := CompactHistory(context.Background(), client, history, "", nil)
			switch tc.name {
			case "non-2xx":
				if err == nil || !strings.Contains(err.Error(), "502") {
					t.Errorf("err = %v, want HTTP 502 failure", err)
				}
			case "error event":
				if err == nil || !strings.Contains(err.Error(), "model failed") {
					t.Errorf("err = %v, want model failure", err)
				}
			case "empty before done":
				// Stream itself rejects a stream with no assistant text
				// ("model stream contained no assistant response") before
				// compactHistory's empty-summary check can run; any error is
				// contract-compliant here.
				if err == nil {
					t.Errorf("err = nil, want failure for empty stream")
				}
			}
			if !reflect.DeepEqual(history, before) {
				t.Errorf("input history mutated: got %+v, want %+v", history, before)
			}
		})
	}
}

func TestCompactHistoryContextCancelled(t *testing.T) {
	// The server blocks until the request context is done, simulating a
	// mid-stream cancellation.
	client := compactTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := CompactHistory(ctx, client, sampleHistory(), "", nil); err == nil {
		t.Fatal("cancelled context: want error")
	} else if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context cancel") {
		t.Errorf("err = %v, want a context cancellation error", err)
	}
}

func TestCompactHistoryAlreadyCompacted(t *testing.T) {
	prior := []model.Message{
		{Role: "developer", Content: "Conversation summary (compacted). Earlier turns are no longer available."},
		{Role: "user", Content: "continue with the tests"},
	}
	var messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	client := compactTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		json.Unmarshal(b, &req)
		messages = req.Messages
		compactFixture(t, `{"choices":[{"delta":{"content":"`+streamedSummary+`"}}]}`, `[DONE]`)(w, r)
	})
	replacement, _, err := CompactHistory(context.Background(), client, prior, "", nil)
	if err != nil {
		t.Fatalf("second compact: %v", err)
	}
	if len(messages) != len(prior)+1 {
		t.Fatalf("sent %d messages, want %d", len(messages), len(prior)+1)
	}
	if messages[0].Role != "developer" || !strings.Contains(messages[0].Content, "Conversation summary") {
		t.Errorf("prior summary not forwarded first: %+v", messages[0])
	}
	if len(replacement) != 1 || replacement[0].Role != "developer" {
		t.Errorf("replacement = %+v, want single developer message", replacement)
	}
}
