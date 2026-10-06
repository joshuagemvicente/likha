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
	claudeCallTurn(w, signature, id, "read", `{"path":"`+path+`"}`)
}

// claudeCallTurn is a signed thinking block, then one call of name with
// input (a JSON object).
func claudeCallTurn(w http.ResponseWriter, signature, id, name, input string) {
	partial, _ := json.Marshal(input)
	claudeEvents(w,
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Read it."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"`+signature+`"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"`+id+`","name":"`+name+`","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+string(partial)+`}}`,
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

// A main run whose own turn holds replayed thinking (sig-parent) spawns an
// explore child. The child's requests replay the child's own first response
// and nothing from the parent; the parent's next request still replays its
// own turn, so the parent state was there to leak.
func TestClaudeExploreChildReplaysOnlyItsOwnBlocks(t *testing.T) {
	repo, root := steerRepo(t)
	const childPrompt = "find the answer in known.txt"
	client, server := newClaudeServer(t, func(n int, w http.ResponseWriter) {
		switch n {
		case 1:
			claudeCallTurn(w, "sig-parent", "toolu_p1", "task", `{"agent":"explore","description":"look","prompt":"`+childPrompt+`"}`)
		case 2:
			claudeToolTurn(w, "sig-child", "toolu_c1", "known.txt")
		default:
			claudeTextTurn(w, "done")
		}
	})
	options := RunOptions{TasksEnabled: true, SaveTask: func(explore.Record) error { return nil }}
	var failed *TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "ask a child", nil, nil, options, func(ev TurnEvent) {
		if ev.Kind == "error" {
			failed = &ev
		}
	})
	if failed != nil {
		t.Fatalf("turn failed: %s", failed.Text)
	}
	firstChild, secondChild, parentAfter := server.body(t, 1), server.body(t, 2), server.body(t, 3)
	if !strings.Contains(firstChild, childPrompt) || !strings.Contains(secondChild, childPrompt) {
		t.Fatalf("requests 2 and 3 are not the child's:\n%s\n%s", firstChild, secondChild)
	}
	for i, body := range []string{firstChild, secondChild} {
		if strings.Contains(body, "sig-parent") || strings.Contains(body, "toolu_p1") {
			t.Fatalf("child request %d carries the parent's blocks:\n%s", i+1, body)
		}
	}
	if !strings.Contains(secondChild, replayedTurn("sig-child", "toolu_c1", "known.txt")) {
		t.Fatalf("child did not replay its own blocks:\n%s", secondChild)
	}
	if !strings.Contains(parentAfter, `"signature":"sig-parent"`) || strings.Contains(parentAfter, "sig-child") {
		t.Fatalf("parent's next request does not replay only its own turn:\n%s", parentAfter)
	}
}
