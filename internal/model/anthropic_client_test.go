package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const anthropicBase = "https://api.anthropic.com/v1"

// anthropicReroute sends requests for the real Anthropic origin to a test
// server, so a client built from the claude row's base URL runs its real
// routing, headers and decoding against a fixture.
type anthropicReroute struct {
	t      *testing.T
	target *url.URL
	next   http.RoundTripper
}

func (r anthropicReroute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "api.anthropic.com" {
		r.t.Errorf("request left the Anthropic origin: %s", req.URL)
		return nil, fmt.Errorf("fixture refused %s", req.URL)
	}
	routed := req.Clone(req.Context())
	routed.URL.Scheme, routed.URL.Host, routed.Host = r.target.Scheme, r.target.Host, ""
	return r.next.RoundTrip(routed)
}

func newAnthropicReroute(t *testing.T, handler http.Handler) anthropicReroute {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return anthropicReroute{t: t, target: target, next: http.DefaultTransport}
}

// anthropicTestClient builds a client for the claude row whose transport
// reaches handler. The live model-info cache is reset around the test.
func anthropicTestClient(t *testing.T, modelID string, handler http.HandlerFunc) *Client {
	t.Helper()
	resetAnthropicModels(t)
	client, err := New(anthropicBase, modelID, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: newAnthropicReroute(t, handler), CheckRedirect: noRedirectHTTPClient().CheckRedirect}
	return client
}

// useAnthropicListServer points the package-level model-list client at
// handler for the package functions ListModels and ListModelsWithDetails.
func useAnthropicListServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	resetAnthropicModels(t)
	previous := sharedListClient.Transport
	sharedListClient.Transport = newAnthropicReroute(t, handler)
	t.Cleanup(func() { sharedListClient.Transport = previous })
}

func resetAnthropicModels(t *testing.T) {
	t.Helper()
	clear := func() {
		anthropicModels.mu.Lock()
		anthropicModels.byID = make(map[string]anthropicModelInfo)
		anthropicModels.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func writeSSE(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("request-id", "req_stream")
	io.WriteString(w, sseFrames(events...))
}

func textResponse(text string) []string {
	return []string{evMessageStart, evTextStart(0), evText(0, text), evStop(0), evMessageDelta("end_turn"), evMessageStop}
}

// bodyRecorder keeps every request body and header set a fixture receives.
type bodyRecorder struct {
	mu      sync.Mutex
	bodies  []string
	headers []http.Header
}

func (r *bodyRecorder) record(t *testing.T, req *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Errorf("read request body: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, string(body))
	r.headers = append(r.headers, req.Header.Clone())
	return string(body)
}

func (r *bodyRecorder) body(t *testing.T, n int) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) <= n {
		t.Fatalf("recorded %d requests, want more than %d", len(r.bodies), n)
	}
	return r.bodies[n]
}

func TestWireFor(t *testing.T) {
	cases := []struct {
		base string
		want ProviderWire
	}{
		{anthropicBase, WireAnthropic},
		{anthropicBase + "/", WireAnthropic},
		{"https://api.openai.com/v1", WireChat},
		{"https://openrouter.ai/api/v1", WireChat},
		{"https://api.anthropic.com/v1beta", WireChat},
		{"https://proxy.example/v1", WireChat},
		{"http://127.0.0.1:8080/v1", WireChat},
	}
	for _, tc := range cases {
		if got := wireFor(tc.base); got != tc.want {
			t.Errorf("wireFor(%q) = %v, want %v", tc.base, got, tc.want)
		}
	}
	for _, p := range Providers {
		if want := WireChat; p.Name != "claude" && p.Wire != want {
			t.Errorf("provider %s wire = %v, want chat", p.Name, p.Wire)
		}
	}
}

func TestNewRoutesClaudeRowToMessages(t *testing.T) {
	claude, err := New(anthropicBase, "claude-opus-5-5", "k")
	if err != nil {
		t.Fatal(err)
	}
	if claude.url != anthropicBase+"/messages" || !claude.nativeAnthropic() {
		t.Fatalf("claude url = %q native = %v", claude.url, claude.nativeAnthropic())
	}
	if claude.ReplaySource().Endpoint != anthropicBase+"/messages" || claude.replayWire != ReplayAnthropicBlocks {
		t.Fatalf("claude replay source %+v wire %v", claude.ReplaySource(), claude.replayWire)
	}
	custom, err := New("https://proxy.example/v1", "claude-opus-5-5", "k")
	if err != nil {
		t.Fatal(err)
	}
	if custom.url != "https://proxy.example/v1/chat/completions" || custom.nativeAnthropic() {
		t.Fatalf("custom endpoint url = %q native = %v", custom.url, custom.nativeAnthropic())
	}
}

func TestAnthropicStreamRequestAndResponse(t *testing.T) {
	var recorder bodyRecorder
	client := anthropicTestClient(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		recorder.record(t, r)
		writeSSE(w, interleavedStream()...)
	})
	var reasoning strings.Builder
	message, usage, ok, err := client.StreamUsage(context.Background(), []Message{{Role: "system", Content: "harness"}, {Role: "user", Content: "go"}},
		[]ToolDefinition{readTool}, nil, func(s string) { reasoning.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	header := recorder.headers[0]
	checks := map[string]string{
		"x-api-key":         "test-key",
		"anthropic-version": "2023-06-01",
		"anthropic-beta":    anthropicThinkingBeta,
		"Content-Type":      "application/json",
		"Accept":            "text/event-stream",
		"User-Agent":        UserAgent,
		"Authorization":     "",
	}
	for name, want := range checks {
		if got := header.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
	var body struct {
		MaxTokens int64           `json:"max_tokens"`
		Stream    bool            `json:"stream"`
		Thinking  json.RawMessage `json:"thinking"`
	}
	if err := json.Unmarshal([]byte(recorder.body(t, 0)), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Stream || body.MaxTokens != 128000 || string(body.Thinking) != `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}` {
		t.Fatalf("body = %s", recorder.body(t, 0))
	}
	assertNoForbiddenKeys(t, []byte(recorder.body(t, 0)))
	if reasoning.String() != "Plan: read a.\n\nNow b." || message.Reasoning != reasoning.String() || len(message.ToolCalls) != 2 {
		t.Fatalf("message = %+v, streamed reasoning %q", message, reasoning.String())
	}
	if !ok || usage.Prompt != 9450 || usage.CacheRead != 9000 || usage.CacheWrite != 400 || usage.Completion != 300 {
		t.Fatalf("usage = %+v ok=%v", usage, ok)
	}
	if last, ok := client.LastRequest(); !ok || last != usage {
		t.Fatalf("LastRequest = %+v, want %+v", last, usage)
	}
	want, err := NewReasoningReplay(client.ReplaySource(), nil, nil, json.RawMessage(interleavedBlocks))
	if err != nil {
		t.Fatal(err)
	}
	if !message.Replay.Equal(want) {
		t.Fatalf("replay = %+v, want %+v", message.Replay, want)
	}
}

func TestAnthropicHaikuSendsNoThinking(t *testing.T) {
	var recorder bodyRecorder
	client := anthropicTestClient(t, "claude-haiku-4-5", func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		writeSSE(w, textResponse("hi")...)
	})
	message, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "go"}}, []ToolDefinition{readTool}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recorder.body(t, 0), `"thinking"`) || recorder.headers[0].Get("anthropic-beta") != "" {
		t.Fatalf("haiku request carried thinking: %s beta %q", recorder.body(t, 0), recorder.headers[0].Get("anthropic-beta"))
	}
	if !strings.Contains(recorder.body(t, 0), `"max_tokens":64000`) {
		t.Fatalf("haiku max_tokens not from the catalog: %s", recorder.body(t, 0))
	}
	// A text-only response records its issuer and no blocks.
	if message.Replay == nil || message.Replay.Source != client.ReplaySource() || message.Replay.Blocks != nil {
		t.Fatalf("replay = %+v", message.Replay)
	}
}

func TestAnthropicThinkingAndMaxTokens(t *testing.T) {
	resetAnthropicModels(t)
	rememberAnthropicModels(map[string]anthropicModelInfo{
		"claude-live-adaptive": {maxTokens: 32000, adaptive: true, adaptiveKnown: true},
		"claude-opus-5-5":      {maxTokens: 50000, adaptive: false, adaptiveKnown: true},
		"claude-sonnet-5-5":    {}, // capabilities null: the table decides
	})
	cases := []struct {
		model     string
		thinking  anthropicThinkingMode
		maxTokens int64
	}{
		{"claude-live-adaptive", thinkingAdaptive, 32000},
		{"claude-opus-5-5", thinkingNone, 50000},
		{"claude-sonnet-5-5", thinkingAdaptive, 128000},
		{"claude-fable-5-1", thinkingAdaptive, 128000},
		{"claude-opus-4-7", thinkingAdaptive, 128000},
		{"claude-haiku-4-5", thinkingNone, 64000},
		{"claude-unknown-9", thinkingNone, defaultAnthropicMaxTokens},
	}
	for _, tc := range cases {
		if got := anthropicThinking(tc.model); got != tc.thinking {
			t.Errorf("anthropicThinking(%s) = %v, want %v", tc.model, got, tc.thinking)
		}
		if got := anthropicMaxTokens(tc.model); got != tc.maxTokens {
			t.Errorf("anthropicMaxTokens(%s) = %d, want %d", tc.model, got, tc.maxTokens)
		}
	}
	if claudeAdaptiveModels["claude-haiku-4-5"] {
		t.Fatal("haiku must not be in the adaptive table")
	}
}

// The second request of a tool loop sends the first response's blocks back
// byte for byte; another model, an edited message, and a tool-less request
// rebuild the turn without thinking.
func TestAnthropicReplayRoundTrip(t *testing.T) {
	var recorder bodyRecorder
	client := anthropicTestClient(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		recorder.mu.Lock()
		n := len(recorder.bodies)
		recorder.mu.Unlock()
		if n == 1 {
			writeSSE(w, interleavedStream()...)
			return
		}
		writeSSE(w, textResponse("done")...)
	})
	tools := []ToolDefinition{readTool}
	history := []Message{{Role: "user", Content: "go"}}
	reply, err := client.Stream(context.Background(), history, tools, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, reply,
		Message{Role: "tool", ToolCallID: "toolu_1", Content: "A"},
		Message{Role: "tool", ToolCallID: "toolu_2", Content: "B"})
	send := func(c *Client, messages []Message, tools []ToolDefinition) string {
		t.Helper()
		if _, err := c.Stream(context.Background(), messages, tools, nil, nil); err != nil {
			t.Fatal(err)
		}
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		return recorder.bodies[len(recorder.bodies)-1]
	}
	replayed := `{"role":"assistant","content":` + interleavedBlocks + `}`
	if body := send(client, history, tools); !strings.Contains(body, replayed) {
		t.Fatalf("same-model request did not replay the blocks verbatim:\n%s", body)
	}
	rebuilt := `{"role":"assistant","content":[{"type":"text","text":"Reading a."},{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a"}},{"type":"tool_use","id":"toolu_2","name":"read","input":{"path":"b"}}]}`

	other, err := client.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if err := other.SetModel("claude-sonnet-5-5"); err != nil {
		t.Fatal(err)
	}
	if body := send(other, history, tools); !strings.Contains(body, rebuilt) || strings.Contains(body, "sig-0") {
		t.Fatalf("another model received thinking:\n%s", body)
	}

	edited := append([]Message(nil), history...)
	edited[1].Content = "Reading"
	if body := send(client, edited, tools); strings.Contains(body, "sig-0") {
		t.Fatalf("an edited message kept its thinking:\n%s", body)
	}

	if body := send(client, append(append([]Message(nil), history...), Message{Role: "user", Content: "summarize"}), nil); strings.Contains(body, "sig-0") || strings.Contains(body, "cache_control") || strings.Contains(body, `"tool_use"`) {
		t.Fatalf("a tool-less request carried thinking, caching or tool blocks:\n%s", body)
	}
	if strings.Contains(recorder.body(t, 1), "Plan: read a.\\n\\nNow b.") {
		t.Fatal("display reasoning reached a request body")
	}
}

// Claude can stream an empty or whitespace-only text block between thinking
// and a tool call. The next request replays the thinking and the call but not
// that block, which Claude rejects in a request.
func TestAnthropicReplayLeavesOutWhitespaceText(t *testing.T) {
	cases := []struct {
		name string
		text []string // text deltas of the block between thinking and tool_use
	}{
		{"empty text block", nil},
		{"blank lines", []string{"\n\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{evMessageStart, evThinkingStart(0), evThinking(0, "Read a."), evSignature(0, "sig-ws"), evStop(0), evTextStart(1)}
			for _, piece := range tc.text {
				events = append(events, evText(1, piece))
			}
			events = append(events, evStop(1), evToolStart(2, "toolu_1", "read"), evInput(2, `{"path":"a"}`), evStop(2), evMessageDelta("tool_use"), evMessageStop)
			var recorder bodyRecorder
			client := anthropicTestClient(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request) {
				recorder.record(t, r)
				writeSSE(w, events...)
			})
			history := []Message{{Role: "user", Content: "go"}}
			reply, err := client.Stream(context.Background(), history, []ToolDefinition{readTool}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			history = append(history, reply, Message{Role: "tool", ToolCallID: "toolu_1", Content: "A"})
			if _, err := client.Stream(context.Background(), history, []ToolDefinition{readTool}, nil, nil); err != nil {
				t.Fatal(err)
			}
			want := `{"role":"assistant","content":[{"type":"thinking","thinking":"Read a.","signature":"sig-ws"},{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a"}}]}`
			if body := recorder.body(t, 1); !strings.Contains(body, want) {
				t.Fatalf("second body does not replay thinking without the blank text block:\n%s", body)
			}
		})
	}
}

// A response cut off inside a thinking block keeps no blocks, so the next
// request sends the turn without thinking.
func TestAnthropicTruncatedThinkingIsNotReplayed(t *testing.T) {
	var recorder bodyRecorder
	client := anthropicTestClient(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request) {
		recorder.record(t, r)
		writeSSE(w, evMessageStart, evTextStart(0), evText(0, "partial"), evStop(0), evThinkingStart(1), evThinking(1, "cut"), evMessageDelta("max_tokens"), evMessageStop)
	})
	reply, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "go"}}, []ToolDefinition{readTool}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Replay == nil || reply.Replay.Blocks != nil {
		t.Fatalf("truncated response replay = %+v, want Source only", reply.Replay)
	}
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "go"}, reply, {Role: "user", Content: "continue"}}, []ToolDefinition{readTool}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if body := recorder.body(t, 1); !strings.Contains(body, `{"role":"assistant","content":[{"type":"text","text":"partial"}]}`) || strings.Contains(body, `"type":"thinking"`) {
		t.Fatalf("second body = %s", body)
	}
}

func TestAnthropicEffortWire(t *testing.T) {
	cases := []struct {
		name    string
		wire    EffortWire
		level   string
		wantCfg bool
	}{
		{"anthropic wire with a level", EffortAnthropic, "high", true},
		{"unsupported wire", EffortUnsupported, "high", false},
		{"anthropic wire at Default", EffortAnthropic, "", false},
		{"chat wire kind on the native client", EffortTopLevel, "high", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var recorder bodyRecorder
			client := anthropicTestClient(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request) {
				recorder.record(t, r)
				writeSSE(w, textResponse("ok")...)
			})
			client.SetEffort(tc.wire, tc.level)
			if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "go"}}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			body := recorder.body(t, 0)
			has := strings.Contains(body, `"output_config":{"effort":"high"}`)
			if has != tc.wantCfg || !tc.wantCfg && strings.Contains(body, "output_config") {
				t.Fatalf("body = %s, want output_config %v", body, tc.wantCfg)
			}
			assertNoForbiddenKeys(t, []byte(body))
		})
	}
}

func TestAnthropicRequestErrors(t *testing.T) {
	replayState := func(t *testing.T, c *Client) []Message {
		replay, err := NewReasoningReplay(c.ReplaySource(), nil, nil, json.RawMessage(`[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"c1","name":"read","input":{}}]`))
		if err != nil {
			t.Fatal(err)
		}
		return []Message{
			{Role: "user", Content: "go"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}}, Replay: replay},
			{Role: "tool", ToolCallID: "c1", Content: "x"},
		}
	}
	jsonError := func(errType, message string) string {
		return `{"type":"error","error":{"type":"` + errType + `","message":"` + message + `"},"request_id":"req_body"}`
	}
	cases := []struct {
		name        string
		status      int
		body        string
		header      map[string]string
		effort      string
		replay      bool
		contentType string
		events      []string
		wantPrefix  string
		check       func(t *testing.T, err error, apiErr *anthropicAPIError)
	}{
		{name: "401", status: 401, body: jsonError("authentication_error", "invalid x-api-key"),
			wantPrefix: "model endpoint rejected the credentials: model HTTP 401 Unauthorized: authentication_error: invalid x-api-key",
			check: func(t *testing.T, err error, _ *anthropicAPIError) {
				if !errors.Is(err, ErrUnauthorized) {
					t.Fatalf("401 is not ErrUnauthorized: %v", err)
				}
			}},
		{name: "403", status: 403, body: jsonError("permission_error", "no"),
			check: func(t *testing.T, err error, _ *anthropicAPIError) {
				if !errors.Is(err, ErrUnauthorized) {
					t.Fatalf("403 is not ErrUnauthorized: %v", err)
				}
			}},
		{name: "400 plain", status: 400, body: jsonError("invalid_request_error", "bad"),
			wantPrefix: "model HTTP 400 Bad Request: invalid_request_error: bad",
			check: func(t *testing.T, err error, apiErr *anthropicAPIError) {
				var effort *EffortRejectedError
				var replay *ReplayRejectedError
				if errors.As(err, &effort) || errors.As(err, &replay) {
					t.Fatalf("plain 400 carries a hint: %v", err)
				}
			}},
		{name: "400 with effort", status: 400, body: jsonError("invalid_request_error", "bad"), effort: "max",
			check: func(t *testing.T, err error, _ *anthropicAPIError) {
				var effort *EffortRejectedError
				var replay *ReplayRejectedError
				if !errors.As(err, &effort) || effort.Level != "max" || errors.As(err, &replay) {
					t.Fatalf("400 with effort = %v", err)
				}
			}},
		{name: "400 with replayed thinking", status: 400, body: jsonError("invalid_request_error", "thinking blocks cannot be modified"), replay: true,
			check: func(t *testing.T, err error, _ *anthropicAPIError) {
				var effort *EffortRejectedError
				var replay *ReplayRejectedError
				if !errors.As(err, &replay) || replay.Sent != 1 || errors.As(err, &effort) {
					t.Fatalf("400 with replay = %v", err)
				}
			}},
		{name: "400 with effort and replayed thinking", status: 400, body: jsonError("invalid_request_error", "bad"), effort: "high", replay: true,
			check: func(t *testing.T, err error, _ *anthropicAPIError) {
				var effort *EffortRejectedError
				var replay *ReplayRejectedError
				if !errors.As(err, &effort) || !errors.As(err, &replay) {
					t.Fatalf("400 with both = %v", err)
				}
			}},
		{name: "404", status: 404, body: jsonError("not_found_error", "model: nope"), wantPrefix: "model HTTP 404 Not Found: not_found_error: model: nope"},
		{name: "413 non-JSON body", status: 413, body: "<html>Request Entity Too Large</html>", wantPrefix: "model HTTP 413 Request Entity Too Large: <html>Request Entity Too Large</html>",
			check: func(t *testing.T, _ error, apiErr *anthropicAPIError) {
				if apiErr.Type != "" {
					t.Fatalf("413 = %+v", apiErr)
				}
			}},
		{name: "429 rate limit", status: 429, body: jsonError("rate_limit_error", "slow down"), header: map[string]string{"retry-after": "7"},
			wantPrefix: "model HTTP 429 Too Many Requests: rate_limit_error: slow down"},
		{name: "429 spend cap", status: 429, body: `{"type":"error","error":{"type":"rate_limit_error","message":"cap","details":{"error_code":"enforced_spend_limit_reached"}}}`,
			wantPrefix: "model HTTP 429 Too Many Requests: " + spendCapNote + "rate_limit_error: cap"},
		{name: "500", status: 500, body: jsonError("api_error", "boom"), wantPrefix: "model HTTP 500 Internal Server Error: api_error: boom"},
		{name: "529", status: 529, body: jsonError("overloaded_error", "Overloaded"), wantPrefix: "model HTTP 529"},
		{name: "200 with a JSON body", status: 200, contentType: "application/json", body: `{"type":"message"}`, wantPrefix: `model returned incompatible content type "application/json" (expected SSE)`},
		{name: "in-stream overloaded_error", status: 200, events: []string{evMessageStart, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
			wantPrefix: "model stream error: overloaded_error: Overloaded"},
		{name: "in-stream invalid_request_error", status: 200, events: []string{evMessageStart, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`},
			wantPrefix: "model stream error: invalid_request_error: bad"},
		{name: "EOF before message_stop", status: 200, events: []string{evMessageStart, evTextStart(0), evText(0, "half")}, wantPrefix: "model stream ended before message_stop"},
		{name: "refusal", status: 200, events: []string{evMessageStart, evTextStart(0), evText(0, "Sure"), evStop(0),
			`{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"Not this."}}}`, evMessageStop},
			wantPrefix: "Claude declined the request (category: cyber).\nNot this."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			client := anthropicTestClient(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				io.Copy(io.Discard, r.Body)
				w.Header().Set("request-id", "req_header")
				for name, value := range tc.header {
					w.Header().Set(name, value)
				}
				if tc.events != nil {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, sseFrames(tc.events...))
					return
				}
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			if tc.effort != "" {
				client.SetEffort(EffortAnthropic, tc.effort)
			}
			messages := []Message{{Role: "user", Content: "go"}}
			if tc.replay {
				messages = replayState(t, client)
			}
			message, err := client.Stream(context.Background(), messages, []ToolDefinition{readTool}, nil, nil)
			if err == nil {
				t.Fatalf("request succeeded with %+v", message)
			}
			if message.Role != "" || message.Content != "" || message.Replay != nil {
				t.Fatalf("failed request returned a message: %+v", message)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want exactly one (no retry inside the client)", requests.Load())
			}
			if !strings.Contains(err.Error(), "(request req_header)") {
				t.Fatalf("error %q does not name the request ID", err)
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("error = %q, want prefix %q", err, tc.wantPrefix)
			}
			var apiErr *anthropicAPIError
			hasAPIErr := errors.As(err, &apiErr)
			if tc.check != nil {
				tc.check(t, err, apiErr)
			}
			if tc.status != 200 && (!hasAPIErr || apiErr.Status != tc.status) {
				t.Fatalf("error %v does not carry status %d", err, tc.status)
			}
		})
	}
}

func TestAnthropicForkKeepsRouteAndTaskGateCountsMessages(t *testing.T) {
	var posts, lists atomic.Int32
	parent := anthropicTestClient(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			lists.Add(1)
			io.WriteString(w, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5"}],"has_more":false,"last_id":"claude-opus-5-5"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
			posts.Add(1)
			writeSSE(w, textResponse("ok")...)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	fork, err := parent.Fork()
	if err != nil || fork.url != parent.url || !fork.nativeAnthropic() {
		t.Fatalf("fork = %+v err %v", fork, err)
	}
	var reservations atomic.Int32
	task, err := parent.ForkForTask(func(context.Context) error { reservations.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Stream(context.Background(), []Message{{Role: "user", Content: "go"}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if reservations.Load() != 1 || posts.Load() != 1 || lists.Load() != 1 {
		t.Fatalf("reservations=%d posts=%d lists=%d, want 1 each", reservations.Load(), posts.Load(), lists.Load())
	}
}
