package agent

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

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/tools"
)

const claudeBase = "https://api.anthropic.com/v1"

// claudeServer plays Anthropic's Messages API for a client built from the
// claude row's real base URL. It reroutes the default transport to a local
// server for the test, so the client runs its real wire selection.
type claudeServer struct {
	mu     sync.Mutex
	bodies []string
}

func (s *claudeServer) body(t *testing.T, n int) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) <= n {
		t.Fatalf("server saw %d model requests, want more than %d", len(s.bodies), n)
	}
	return s.bodies[n]
}

// newClaudeServer answers the model list and hands each Messages request to
// respond with its 1-based number, returning a claude client.
func newClaudeServer(t *testing.T, respond func(n int, w http.ResponseWriter)) (*model.Client, *claudeServer) {
	t.Helper()
	state := &claudeServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5"}],"has_more":false}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		state.mu.Lock()
		state.bodies = append(state.bodies, string(raw))
		n := len(state.bodies)
		state.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		respond(n, w)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = claudeReroute{target: target, next: previous}
	t.Cleanup(func() { http.DefaultTransport = previous })
	client, err := model.New(claudeBase, "claude-opus-5-5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	return client, state
}

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

// claudeEvents writes Messages API events as server-sent events.
func claudeEvents(w http.ResponseWriter, events ...string) {
	for _, event := range events {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &head)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, event)
	}
}

// claudeToolTurn is a thinking block, then a read call of path.
func claudeToolTurn(w http.ResponseWriter, signature, id, path string) {
	claudeEvents(w,
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Read it."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"`+signature+`"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"`+id+`","name":"read","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"`+path+`\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
		`{"type":"message_stop"}`,
	)
}

func claudeTextTurn(w http.ResponseWriter, text string) {
	quoted, _ := json.Marshal(text)
	claudeEvents(w,
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(quoted)+`}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	)
}

func replayedTurn(signature, id, path string) string {
	return `{"role":"assistant","content":[{"type":"thinking","thinking":"Read it.","signature":"` + signature + `"},` +
		`{"type":"tool_use","id":"` + id + `","name":"read","input":{"path":"` + path + `"}}]}`
}

// The main loop's second request sends the first response's content blocks
// back verbatim, thinking and signature included.
func TestClaudeMainLoopReplaysThinkingAcrossToolCalls(t *testing.T) {
	repo, root := steerRepo(t)
	client, server := newClaudeServer(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			claudeToolTurn(w, "sig-main", "toolu_1", "known.txt")
			return
		}
		claudeTextTurn(w, "The answer is 42.")
	})
	var failed *TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "what is in known.txt?", nil, nil, RunOptions{}, func(ev TurnEvent) {
		if ev.Kind == "error" {
			failed = &ev
		}
	})
	if failed != nil {
		t.Fatalf("turn failed: %s", failed.Text)
	}
	second := server.body(t, 1)
	if !strings.Contains(second, replayedTurn("sig-main", "toolu_1", "known.txt")) {
		t.Fatalf("second request did not replay the first response's blocks:\n%s", second)
	}
	if !strings.Contains(second, `{"type":"tool_result","tool_use_id":"toolu_1","content":`) {
		t.Fatalf("second request lacks the tool result:\n%s", second)
	}
}

// historyWithClaudeReplay is a finished tool turn whose assistant message
// holds replay blocks issued by client.
func historyWithClaudeReplay(t *testing.T, client *model.Client) []model.Message {
	t.Helper()
	replay, err := model.NewReasoningReplay(client.ReplaySource(), nil, nil,
		json.RawMessage(`[{"type":"thinking","thinking":"Read it.","signature":"sig-old"},{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	return []model.Message{
		{Role: "user", Content: "read a"},
		{Role: "assistant", Replay: replay, ToolCalls: []model.ToolCall{{ID: "toolu_1", Name: "read", Arguments: `{"path":"a"}`}}},
		{Role: "tool", ToolCallID: "toolu_1", Content: "contents of a"},
		{Role: "assistant", Content: "a holds the contents."},
	}
}

// assertBackgroundBody checks a compaction or naming request: tool history
// as text, and no tools, caching, effort or thinking blocks.
func assertBackgroundBody(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{`"tools"`, `"cache_control"`, `"output_config"`, `"type":"thinking"`, `"tool_use"`, `"tool_result"`, "sig-old"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("background request carries %s:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, `[tool call read {\"path\":\"a\"}]`) || !strings.Contains(body, `[tool result toolu_1]\ncontents of a`) {
		t.Fatalf("background request lacks tool history as text:\n%s", body)
	}
}

func TestClaudeCompactionAndNamingSendNoReplayOrCaching(t *testing.T) {
	client, server := newClaudeServer(t, func(n int, w http.ResponseWriter) {
		claudeTextTurn(w, "Read file a")
	})
	client.SetEffort(model.EffortAnthropic, "high")
	history := historyWithClaudeReplay(t, client)
	if _, _, err := CompactHistory(context.Background(), client, history, "", nil); err != nil {
		t.Fatalf("compaction: %v", err)
	}
	assertBackgroundBody(t, server.body(t, 0))
	if _, err := GenerateSessionName(context.Background(), client, history); err != nil {
		t.Fatalf("naming: %v", err)
	}
	assertBackgroundBody(t, server.body(t, 1))
}

// An explore child forks the parent's client; its second request replays
// the child's own first response and nothing from the parent's history.
func TestClaudeExploreChildReplaysOnlyItsOwnBlocks(t *testing.T) {
	repo, _ := steerRepo(t)
	client, server := newClaudeServer(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			claudeToolTurn(w, "sig-child", "toolu_c1", "known.txt")
			return
		}
		claudeTextTurn(w, "done")
	})
	scope := tools.Scope{Agent: "explore", Allowed: map[string]bool{"read": true}}
	manager, err := explore.New(context.Background(), explore.Config{
		Root: repo.Root(), Provider: "claude", Model: client.Model(), Scope: scope,
		Runner: ExploreRunner(client, repo, nil, scope, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Wait()
	outcome, err := manager.Spawn(context.Background(), "", "call_1", explore.Spec{Agent: "explore", Description: "look", Prompt: "find the answer"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if outcome.Status != explore.Completed {
		t.Fatalf("child outcome = %s (%s)", outcome.Status, outcome.Reason)
	}
	second := server.body(t, 1)
	if !strings.Contains(second, replayedTurn("sig-child", "toolu_c1", "known.txt")) {
		t.Fatalf("child did not replay its own blocks:\n%s", second)
	}
	if strings.Count(second, `"signature"`) != 1 {
		t.Fatalf("child request replayed blocks it did not issue:\n%s", second)
	}
}
