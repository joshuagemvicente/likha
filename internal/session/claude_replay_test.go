package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"likha/internal/model"
)

type claudeReroute struct {
	target *url.URL
	next   http.RoundTripper
}

func (r claudeReroute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.anthropic.com" {
		return nil, fmt.Errorf("test refused request to %s", req.URL)
	}
	routed := req.Clone(req.Context())
	routed.URL.Scheme, routed.URL.Host, routed.Host = r.target.Scheme, r.target.Host, ""
	return r.next.RoundTrip(routed)
}

// claudeFixtureEvents is a thinking block with characters JSON escapes,
// then a tool call, as the first response of a tool loop.
var claudeFixtureEvents = []string{
	`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
	`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Check <a> & \"b\"."}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig+/="}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\": \"a<b>\"}"}}`,
	`{"type":"content_block_stop","index":1}`,
	`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
	`{"type":"message_stop"}`,
}

// A session holding Claude replay state is saved and resumed, and the next
// request built from the loaded history equals the one built before saving.
func TestClaudeReplaySurvivesSaveAndResume(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range claudeFixtureEvents {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = claudeReroute{target: target, next: previous}
	t.Cleanup(func() { http.DefaultTransport = previous })

	client, err := model.New("https://api.anthropic.com/v1", "claude-opus-5-5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	tools := []model.ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}
	history := []model.Message{{Role: "user", Content: "go"}}
	reply, err := client.Stream(context.Background(), history, tools, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Replay == nil || len(reply.Replay.Blocks) == 0 {
		t.Fatalf("reply kept no replay blocks: %+v", reply.Replay)
	}
	history = append(history, reply, model.Message{Role: "tool", ToolCallID: "toolu_1", Content: "ok"})
	send := func(messages []model.Message) string {
		t.Helper()
		if _, err := client.Stream(context.Background(), messages, tools, nil, nil); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return bodies[len(bodies)-1]
	}
	before := send(history)
	if !strings.Contains(before, `"signature":"sig+/="`) {
		t.Fatalf("pre-save request did not replay the thinking block:\n%s", before)
	}

	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.History = history
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after := send(loaded.History); after != before {
		t.Fatalf("request after resume differs\nbefore %s\n after %s", before, after)
	}
}
