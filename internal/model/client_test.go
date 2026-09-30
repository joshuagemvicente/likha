package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func localClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(server.URL+"/v1", "local-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func sendEvents(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	for _, event := range events {
		fmt.Fprintf(w, "data: %s\n\n", event)
		w.(http.Flusher).Flush()
	}
}

func TestStreamTextAndIndexedToolCalls(t *testing.T) {
	var request struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("unexpected request %s %s, accept %q", r.Method, r.URL.Path, r.Header.Get("Accept"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		sendEvents(w,
			`{"choices":[{"delta":{"role":"assistant","content":"Found "}}]}`,
			`{"choices":[{"delta":{"content":"files","tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"read","arguments":"{\"path\":\""}},{"index":0,"id":"call_a","type":"function","function":{"name":"list","arguments":"{\"path\":\""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"a.go\"}"}},{"index":0,"function":{"arguments":".\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		)
	})
	var chunks []string
	response, err := client.Stream(context.Background(), []Message{
		{Role: "user", Content: "Find files"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "prior", Name: "search", Arguments: `{"query":"x"}`}}},
		{Role: "tool", ToolCallID: "prior", Content: "search result"},
	}, []ToolDefinition{{Name: "list", Description: "List files", Parameters: json.RawMessage(`{"type":"object"}`)}}, func(chunk string) { chunks = append(chunks, chunk) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chunks, []string{"Found ", "files"}) {
		t.Fatalf("text chunks: %#v", chunks)
	}
	want := Message{Role: "assistant", Content: "Found files", ToolCalls: []ToolCall{
		{ID: "call_a", Name: "list", Arguments: `{"path":"."}`},
		{ID: "call_b", Name: "read", Arguments: `{"path":"a.go"}`},
	}}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("response: %+v, want %+v", response, want)
	}
	if request.Model != "local-model" || !request.Stream || len(request.Tools) != 1 || request.Tools[0].Type != "function" || request.Tools[0].Function.Name != "list" || string(request.Tools[0].Function.Parameters) != `{"type":"object"}` {
		t.Errorf("model or tool request not preserved: %+v", request)
	}
	if len(request.Messages) != 3 || request.Messages[1].ToolCalls[0].ID != "prior" || request.Messages[1].ToolCalls[0].Type != "function" || request.Messages[1].ToolCalls[0].Function.Name != "search" || request.Messages[1].ToolCalls[0].Function.Arguments != `{"query":"x"}` || request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "prior" || request.Messages[2].Content != "search result" {
		t.Errorf("follow-up tool messages not preserved: %+v", request.Messages)
	}
}

func TestStreamSplitSSEEventAndToolMetadata(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keepalive\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_\",\"function\":{\"name\":\"re\",\"arguments\":\"{\\\"path\\\":\\\"\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"one\",\"function\":{\"name\":\"ad\",\"arguments\":\"x\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\n")
		fmt.Fprint(w, "data: \"usage\":null}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"total_tokens\":10}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	answer, err := client.Stream(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Message{Role: "assistant", Content: "ok", ToolCalls: []ToolCall{{ID: "call_one", Name: "read", Arguments: `{"path":"x"}`}}}
	if !reflect.DeepEqual(answer, want) {
		t.Fatalf("answer = %+v; want %+v", answer, want)
	}
}

func TestStreamAcceptsDoneWithoutTrailingBlankLine(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]")
	})
	answer, err := client.Stream(context.Background(), nil, nil, nil, nil)
	if err != nil || answer.Content != "hello" {
		t.Fatalf("answer=%+v, error=%v", answer, err)
	}
}

func TestStreamFailures(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"server status", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "model not loaded", http.StatusServiceUnavailable)
		}, "503"},
		{"not a stream", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[]}`)
		}, "incompatible content type"},
		{"malformed event", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"choices":`)
		}, "malformed"},
		{"missing structured index", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"choices":[{"delta":{"tool_calls":[{"id":"x","function":{"name":"read","arguments":"{}"}}]}}]}`, `[DONE]`)
		}, "indexed"},
		{"incomplete tool call", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"read","arguments":"{"}}]}}]}`, `[DONE]`)
		}, "incomplete structured tool call"},
		{"premature end", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"choices":[{"delta":{"content":"partial"}}]}`)
		}, "before [DONE]"},
		{"empty done", func(w http.ResponseWriter, _ *http.Request) { sendEvents(w, `[DONE]`) }, "no assistant response"},
		{"server error event", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"error":{"message":"model failed"}}`)
		}, "model failed"},
		{"line too large", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, `{"choices":[{"delta":{"content":"`+strings.Repeat("a", maxEventLineBytes)+`"}}]}`, `[DONE]`)
		}, "line limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := localClient(t, tc.handler)
			_, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "test"}}, nil, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStreamCancellation(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		sendEvents(w, `{"choices":[{"delta":{"content":"first"}}]}`)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var chunks []string
	answer, err := client.Stream(ctx, []Message{{Role: "user", Content: "test"}}, nil, func(s string) {
		chunks = append(chunks, s)
		cancel()
	}, nil)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(chunks, []string{"first"}) || answer.Role != "" {
		t.Fatalf("answer=%+v, chunks=%v, error=%v", answer, chunks, err)
	}
}

func TestLastTokenUsageChatWire(t *testing.T) {
	cases := []struct {
		name     string
		events   []string
		want     TokenUsage
		wantSeen bool
	}{
		{
			name: "usage-only final event with canonical names",
			events: []string{
				`{"choices":[{"delta":{"role":"assistant","content":"hi"}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":34}}`,
				`[DONE]`,
			},
			want:     TokenUsage{Prompt: 120, Completion: 34},
			wantSeen: true,
		},
		{
			name: "input/output aliases accepted",
			events: []string{
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`{"choices":[],"usage":{"input_tokens":9,"output_tokens":3}}`,
				`[DONE]`,
			},
			want:     TokenUsage{Prompt: 9, Completion: 3},
			wantSeen: true,
		},
		{
			name: "canonical names win over aliases when both present",
			events: []string{
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`{"choices":[],"usage":{"prompt_tokens":7,"input_tokens":99,"completion_tokens":2,"output_tokens":88}}`,
				`[DONE]`,
			},
			want:     TokenUsage{Prompt: 7, Completion: 2},
			wantSeen: true,
		},
		{
			name: "zero totals are valid usage",
			events: []string{
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0}}`,
				`[DONE]`,
			},
			want:     TokenUsage{},
			wantSeen: true,
		},
		{
			name: "usage object present without recognized fields still counts",
			events: []string{
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`{"choices":[],"usage":{"total_tokens":10}}`,
				`[DONE]`,
			},
			want:     TokenUsage{},
			wantSeen: true,
		},
		{
			name: "last usage event wins",
			events: []string{
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
				`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":6}}`,
				`[DONE]`,
			},
			want:     TokenUsage{Prompt: 5, Completion: 6},
			wantSeen: true,
		},
		{
			name: "no usage event means no usage",
			events: []string{
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`[DONE]`,
			},
			wantSeen: false,
		},
		{
			name: "null usage means no usage",
			events: []string{
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`{"choices":[],"usage":null}`,
				`[DONE]`,
			},
			wantSeen: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
				sendEvents(w, tc.events...)
			})
			answer, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if answer.Content != "hi" {
				t.Fatalf("answer = %+v", answer)
			}
			usage, ok := client.LastTokenUsage()
			if ok != tc.wantSeen || usage != tc.want {
				t.Fatalf("LastTokenUsage = (%+v, %v), want (%+v, %v)", usage, ok, tc.want, tc.wantSeen)
			}
		})
	}
}

func TestLastTokenUsagePerResponseNotCumulative(t *testing.T) {
	var turn int
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		turn++
		if turn == 1 {
			sendEvents(w,
				`{"choices":[{"delta":{"content":"one"}}]}`,
				`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
				`[DONE]`)
			return
		}
		sendEvents(w,
			`{"choices":[{"delta":{"content":"two"}}]}`,
			`[DONE]`)
	})
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "a"}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if usage, ok := client.LastTokenUsage(); !ok || usage != (TokenUsage{Prompt: 10, Completion: 5}) {
		t.Fatalf("first turn usage = (%+v, %v)", usage, ok)
	}
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "b"}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// The second response carried no usage: the counts must reset to the
	// last response alone, never accumulate across turns.
	if usage, ok := client.LastTokenUsage(); ok || usage != (TokenUsage{}) {
		t.Fatalf("second turn usage = (%+v, %v), want zero/false", usage, ok)
	}
}

func TestLastTokenUsageBeforeAnyStream(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request expected")
	})
	if usage, ok := client.LastTokenUsage(); ok || usage != (TokenUsage{}) {
		t.Fatalf("usage = (%+v, %v), want zero/false before any stream", usage, ok)
	}
}

func TestContextWindowCatalog(t *testing.T) {
	cases := []struct {
		model  string
		want   int64
		wantOK bool
	}{
		{"gpt-4o", 128000, true},
		{"gpt-4o-mini", 128000, true},
		{"gpt-4o-2024-11-20", 128000, true},
		{"gpt-4.1", 1000000, true},
		{"gpt-4.1-mini", 1000000, true},
		{"gpt-4.1-nano-2025-04-14", 1000000, true},
		{"o1", 200000, true},
		{"o1-mini", 128000, true},
		{"o1-preview", 128000, true},
		{"o3", 200000, true},
		{"o3-mini", 200000, true},
		{"o3-2025-04-16", 200000, true},
		{"o4-mini", 200000, true},
		{"deepseek-chat", 128000, true},
		{"deepseek-reasoner", 128000, true},
		{"claude-sonnet-4", 200000, true},
		{"claude-opus-4-1", 200000, true},
		{"claude-3-7-sonnet-20250219", 200000, true},
		{"claude-haiku-4-5", 200000, true},
		// ChatGPT-curated gpt-5.x ids have no publicly documented window.
		{"gpt-5.5", 0, false},
		{"gpt-5", 0, false},
		{"gpt-3.7", 0, false},
		{"", 0, false},
		{"  ", 0, false},
		{"some-unknown-model", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			window, ok := ContextWindow(tc.model)
			if ok != tc.wantOK || window != tc.want {
				t.Fatalf("ContextWindow(%q) = (%d, %v), want (%d, %v)", tc.model, window, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestNewAcceptsListedAndLocalEndpoints(t *testing.T) {
	for _, endpoint := range append([]string{
		"https://openrouter.ai/api/v1", "https://dialagram.me/router/v1",
		"https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1",
	}, ProviderBaseURLs()...) {
		if _, err := New(endpoint, "local", "key"); err != nil {
			t.Errorf("rejected provider endpoint %q: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"http://[::1]:8080/v1/", "http://127.0.0.2:8080/v1"} {
		if _, err := New(endpoint, "local", ""); err != nil {
			t.Errorf("rejected loopback endpoint %q: %v", endpoint, err)
		}
	}
	if _, err := New("http://localhost:8080/v1", " ", ""); err == nil {
		t.Fatal("accepted empty model name")
	}
}

func TestNewRejectsInsecureAndMalformedEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com/v1", "http://localhost:1234/",
		"http://localhost:1234/v1?key=secret",
		"http://user@localhost:1234/v1", "http://localhost.evil.test/v1",
		"https://openrouter.ai/api/v1?tracking=1", "https://user@example.com/v1",
		"ftp://example.com/v1", "https://example.com/",
	} {
		if _, err := New(endpoint, "local", ""); err == nil {
			t.Errorf("accepted invalid endpoint %q", endpoint)
		}
	}
}

func TestStreamDoesNotFollowRedirect(t *testing.T) {
	var leaked bool
	remote := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
	defer remote.Close()
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, remote.URL, http.StatusTemporaryRedirect)
	})
	_, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "private"}}, nil, nil, nil)
	if err == nil || leaked {
		t.Fatalf("redirect error = %v, sent remotely = %t", err, leaked)
	}
}

func TestStreamSendsBearerKeyAndCheckClassifiesFailures(t *testing.T) {
	var sawAuth, sawCheckAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models":
			sawCheckAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"m"}]}`)
		case r.URL.Path == "/v1/chat/completions":
			sawAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := New(server.URL+"/v1", "local", "sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if sawCheckAuth != "Bearer sk-secret" {
		t.Fatalf("check authorization = %q", sawCheckAuth)
	}
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if sawAuth != "Bearer sk-secret" {
		t.Fatalf("stream authorization = %q", sawAuth)
	}
	if err := client.EnsureConnected(context.Background()); err != nil {
		t.Fatalf("connected client re-checked: %v", err)
	}
}

func TestStreamSendsSessionHeaderAndAgentUserAgent(t *testing.T) {
	var sawSession, sawUA string
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		sawSession = r.Header.Get("x-opencode-session")
		sawUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	})
	client.SetSessionHeader("x-opencode-session")
	client.SetSession("ses_abc123")
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if sawSession != "ses_abc123" {
		t.Fatalf("session header = %q", sawSession)
	}
	if sawUA != UserAgent || !strings.HasPrefix(sawUA, "lisa/") {
		t.Fatalf("user agent = %q, want %q", sawUA, UserAgent)
	}
	// A client without a session header must not send an empty one.
	plain := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-opencode-session") != "" {
			t.Errorf("session header sent without configuration")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	})
	if _, err := plain.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
		t.Fatalf("plain Stream: %v", err)
	}
}

func TestCheckFailureClasses(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    error
	}{
		{"unreachable", func(http.ResponseWriter, *http.Request) {}, ErrUnreachable},
		{"revoked key", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}, ErrUnauthorized},
		{"forbidden", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}, ErrUnauthorized},
		{"server error", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}, ErrUnexpectedResponse},
		{"wrong shape", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"unexpected":true}`)
		}, ErrUnexpectedResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var client *Client
			if tc.want == ErrUnreachable {
				c, err := New("http://127.0.0.1:1/v1", "local", "")
				if err != nil {
					t.Fatal(err)
				}
				client = c
			} else {
				server := httptest.NewServer(tc.handler)
				defer server.Close()
				c, err := New(server.URL+"/v1", "local", "key")
				if err != nil {
					t.Fatal(err)
				}
				client = c
			}
			if err := client.Check(context.Background()); !errors.Is(err, tc.want) {
				t.Fatalf("Check error = %v, want %v", err, tc.want)
			}
		})
	}
}

// oauthRecorder counts token-endpoint and chat hits across handler
// invocations, safe for concurrent requests.
type oauthRecorder struct {
	mu     sync.Mutex
	tokens int
	chats  int
	auths  []string
}

func (r *oauthRecorder) hitToken() {
	r.mu.Lock()
	r.tokens++
	r.mu.Unlock()
}

func (r *oauthRecorder) hitChat(auth string) {
	r.mu.Lock()
	r.chats++
	r.auths = append(r.auths, auth)
	r.mu.Unlock()
}

func (r *oauthRecorder) tokenCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens
}

func (r *oauthRecorder) chatCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chats
}

func (r *oauthRecorder) chatAuths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.auths...)
}

// writeToken answers a token endpoint request with a fresh token set. The
// access token carries no account claims, so a refreshed login keeps the
// stored account ID.
func writeToken(w http.ResponseWriter, access string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"refresh-new","expires_in":3600}`, access)
}

// codexOK writes a minimal terminal Codex SSE stream answering "ok".
func codexOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: response.output_text.delta\n")
	fmt.Fprint(w, "data: {\"delta\":\"ok\"}\n\n")
	fmt.Fprint(w, "event: response.completed\n")
	fmt.Fprint(w, `data: {"response":{"output":[]}}`+"\n\n")
}

// codexSSE writes text deltas followed by one streamed function call and the
// terminal event.
func codexSSE(w http.ResponseWriter, deltas ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, delta := range deltas {
		fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"delta\":%q}\n\n", delta)
	}
	fmt.Fprint(w, "event: response.output_item.added\n")
	fmt.Fprint(w, `data: {"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":""}}`+"\n\n")
	fmt.Fprint(w, "event: response.function_call_arguments.delta\n")
	fmt.Fprint(w, `data: {"item_id":"fc_1","delta":"{\"path\":"}`+"\n\n")
	fmt.Fprint(w, "event: response.function_call_arguments.delta\n")
	fmt.Fprint(w, `data: {"item_id":"fc_1","delta":"\"a.go\"}"}`+"\n\n")
	fmt.Fprint(w, "event: response.completed\n")
	fmt.Fprint(w, `data: {"response":{"output":[]}}`+"\n\n")
}

func newOAuthTestClient(t *testing.T, issuer, base string, creds OAuthCredentials) *Client {
	t.Helper()
	client, err := NewOAuth(base, "gpt-5.5", issuer, "client_test", creds)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func validOAuthCredentials() OAuthCredentials {
	return OAuthCredentials{
		Refresh:   "refresh-old",
		Access:    "access-old",
		Expires:   time.Now().Add(time.Hour).UnixMilli(),
		AccountID: "acct_123",
	}
}

func expiredOAuthCredentials() OAuthCredentials {
	creds := validOAuthCredentials()
	creds.Expires = time.Now().Add(-time.Minute).UnixMilli()
	return creds
}

func TestOAuthStreamSendsCodexHeadersAndParsesSSE(t *testing.T) {
	var sawAuth, sawAccount, sawSession, sawOriginator, sawUA, sawPath, sawModel, sawInstructions string
	var sawTemperature bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			t.Errorf("token endpoint hit with a valid stored token")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		sawAccount = r.Header.Get("ChatGPT-Account-Id")
		sawSession = r.Header.Get("session-id")
		sawOriginator = r.Header.Get("originator")
		sawUA = r.Header.Get("User-Agent")
		var envelope map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Errorf("decode codex request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, ok := envelope["temperature"]; ok {
			sawTemperature = true
		}
		sawModel = string(envelope["model"])
		sawInstructions = string(envelope["instructions"])
		codexSSE(w, "Hello ")
	}))
	defer server.Close()
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	client.SetSessionHeader("session-id")
	client.SetSession("ses_1")
	var chunks []string
	answer, err := client.Stream(context.Background(), []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hi"},
	}, []ToolDefinition{{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)}},
		func(chunk string) { chunks = append(chunks, chunk) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Message{Role: "assistant", Content: "Hello ", ToolCalls: []ToolCall{
		{ID: "call_1", Name: "read", Arguments: `{"path":"a.go"}`},
	}}
	if !reflect.DeepEqual(answer, want) {
		t.Fatalf("answer = %+v, want %+v", answer, want)
	}
	if !reflect.DeepEqual(chunks, []string{"Hello "}) {
		t.Fatalf("chunks: %#v", chunks)
	}
	if sawAuth != "Bearer access-old" || sawAccount != "acct_123" || sawSession != "ses_1" {
		t.Fatalf("auth %q, account %q, session %q", sawAuth, sawAccount, sawSession)
	}
	if sawOriginator != ChatGPTOriginator || sawUA != UserAgent {
		t.Fatalf("originator %q, user agent %q", sawOriginator, sawUA)
	}
	if !strings.HasSuffix(sawPath, "/responses") || strings.Contains(sawPath, "/chat/completions") {
		t.Fatalf("codex path = %q", sawPath)
	}
	if sawTemperature {
		t.Fatalf("codex request carried temperature")
	}
	if sawModel != `"gpt-5.5"` || sawInstructions != `"be brief"` {
		t.Fatalf("model %s, instructions %s", sawModel, sawInstructions)
	}
	if client.APIKey() != "" {
		t.Fatalf("oauth client reported an API key")
	}
}

func TestOAuthRefreshesExpiredTokenAndCallsSaver(t *testing.T) {
	var rec oauthRecorder
	var grantType, clientID, refreshToken string
	var saved OAuthCredentials
	var saveCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/token":
			rec.hitToken()
			grantType, clientID, refreshToken = r.FormValue("grant_type"), r.FormValue("client_id"), r.FormValue("refresh_token")
			writeToken(w, "access-new")
		case strings.HasSuffix(r.URL.Path, "/responses"):
			rec.hitChat(r.Header.Get("Authorization"))
			codexOK(w)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	oldExpires := expiredOAuthCredentials().Expires
	client.SetOAuthSaver(func(c OAuthCredentials) error {
		saveCalls++
		saved = c
		return nil
	})
	answer, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Content != "ok" {
		t.Fatalf("answer = %+v", answer)
	}
	if grantType != "refresh_token" || clientID != "client_test" || refreshToken != "refresh-old" {
		t.Fatalf("token request: grant %q, client %q, refresh %q", grantType, clientID, refreshToken)
	}
	if auths := rec.chatAuths(); len(auths) != 1 || auths[0] != "Bearer access-new" {
		t.Fatalf("chat authorizations: %#v", auths)
	}
	if saveCalls != 1 {
		t.Fatalf("saver calls = %d, want 1", saveCalls)
	}
	if saved.Access != "access-new" || saved.Refresh != "refresh-new" || saved.AccountID != "acct_123" {
		t.Fatalf("saved credentials: %+v", saved)
	}
	if saved.Expires <= oldExpires {
		t.Fatalf("saved expiry %d not after old expiry %d", saved.Expires, oldExpires)
	}
}

func TestOAuthUnauthorizedForcesRefreshAndRetries(t *testing.T) {
	t.Run("recovers after refresh", func(t *testing.T) {
		var rec oauthRecorder
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/oauth/token":
				rec.hitToken()
				writeToken(w, "access-new")
			case strings.HasSuffix(r.URL.Path, "/responses"):
				rec.hitChat(r.Header.Get("Authorization"))
				if rec.chatCount() == 1 {
					http.Error(w, "stale token", http.StatusUnauthorized)
					return
				}
				codexOK(w)
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()
		client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
		answer, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if answer.Content != "ok" {
			t.Fatalf("answer = %+v", answer)
		}
		if rec.tokenCount() != 1 {
			t.Fatalf("token endpoint hits = %d, want 1", rec.tokenCount())
		}
		if rec.chatCount() != 2 {
			t.Fatalf("chat requests = %d, want 2", rec.chatCount())
		}
		auths := rec.chatAuths()
		if auths[0] != "Bearer access-old" || auths[1] != "Bearer access-new" {
			t.Fatalf("chat authorizations: %#v", auths)
		}
	})
	t.Run("second 401 is unauthorized", func(t *testing.T) {
		var rec oauthRecorder
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/oauth/token":
				rec.hitToken()
				writeToken(w, "access-new")
			case strings.HasSuffix(r.URL.Path, "/responses"):
				rec.hitChat(r.Header.Get("Authorization"))
				http.Error(w, "revoked", http.StatusUnauthorized)
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()
		client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
		_, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
		if rec.tokenCount() != 1 {
			t.Fatalf("token endpoint hits = %d, want 1", rec.tokenCount())
		}
		if rec.chatCount() != 2 {
			t.Fatalf("chat requests = %d, want 2", rec.chatCount())
		}
	})
}

func TestOAuthCheckValidatesLoginWithoutModelList(t *testing.T) {
	t.Run("hits only the token endpoint", func(t *testing.T) {
		var rec oauthRecorder
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/oauth/token" {
				rec.hitToken()
				writeToken(w, "access-new")
				return
			}
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()
		client := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
		if err := client.Check(context.Background()); err != nil {
			t.Fatalf("Check: %v", err)
		}
		if rec.tokenCount() != 1 {
			t.Fatalf("token endpoint hits = %d, want 1", rec.tokenCount())
		}
	})
	t.Run("unreachable token endpoint", func(t *testing.T) {
		client := newOAuthTestClient(t, "http://127.0.0.1:1", "http://127.0.0.1:1/v1", expiredOAuthCredentials())
		err := client.Check(context.Background())
		if err == nil {
			t.Fatal("Check on unreachable token endpoint succeeded")
		}
		if !strings.Contains(err.Error(), "token request") || !strings.Contains(err.Error(), "sign in again") {
			t.Fatalf("error = %v, want a clear token-request failure", err)
		}
	})
}

func TestLastTokenUsageCodexWire(t *testing.T) {
	cases := []struct {
		name     string
		terminal string
		want     TokenUsage
		wantSeen bool
	}{
		{
			name:     "usage in completed event",
			terminal: `{"response":{"output":[],"usage":{"input_tokens":110,"output_tokens":25}}}`,
			want:     TokenUsage{Prompt: 110, Completion: 25},
			wantSeen: true,
		},
		{
			name:     "alias naming in completed event",
			terminal: `{"response":{"output":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}}`,
			want:     TokenUsage{Prompt: 7, Completion: 2},
			wantSeen: true,
		},
		{
			name:     "no usage in completed event",
			terminal: `{"response":{"output":[]}}`,
			wantSeen: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.output_text.delta\n")
				fmt.Fprint(w, `data: {"delta":"ok"}`+"\n\n")
				fmt.Fprint(w, "event: response.completed\n")
				fmt.Fprint(w, "data: "+tc.terminal+"\n\n")
			}))
			defer server.Close()
			client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
			if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			usage, ok := client.LastTokenUsage()
			if ok != tc.wantSeen || usage != tc.want {
				t.Fatalf("LastTokenUsage = (%+v, %v), want (%+v, %v)", usage, ok, tc.want, tc.wantSeen)
			}
		})
	}
}

func TestUsageFromCodexRateLimitHeaders(t *testing.T) {
	t.Run("percent and window", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/responses") {
				t.Errorf("unexpected path %q", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("x-codex-primary-used-percent", "34")
			w.Header().Set("x-codex-primary-window-minutes", "300")
			w.Header().Set("x-codex-primary-reset-at", "2026-09-29T18:00:00Z")
			codexOK(w)
		}))
		defer server.Close()
		client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
		if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if got := client.Usage(); got != "34% of 5h window used" {
			t.Fatalf("Usage() = %q", got)
		}
	})
	t.Run("percent only", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-codex-primary-used-percent", "12")
			codexOK(w)
		}))
		defer server.Close()
		client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
		if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if got := client.Usage(); got != "12% used" {
			t.Fatalf("Usage() = %q", got)
		}
	})
	t.Run("no headers", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			codexOK(w)
		}))
		defer server.Close()
		client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
		if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if got := client.Usage(); got != "" {
			t.Fatalf("Usage() = %q, want empty", got)
		}
	})
}

func TestOAuthConcurrentStreamsShareOneRefresh(t *testing.T) {
	var rec oauthRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/token":
			rec.hitToken()
			time.Sleep(50 * time.Millisecond)
			writeToken(w, "access-new")
		case strings.HasSuffix(r.URL.Path, "/responses"):
			auth := r.Header.Get("Authorization")
			rec.hitChat(auth)
			if auth != "Bearer access-new" {
				t.Errorf("chat authorization = %q, want the refreshed token", auth)
			}
			codexOK(w)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", expiredOAuthCredentials())
	const n = 5
	results := make([]Message, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
		}()
	}
	wg.Wait()
	if rec.tokenCount() != 1 {
		t.Fatalf("token endpoint hits = %d, want exactly 1", rec.tokenCount())
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("stream %d: %v", i, errs[i])
		}
		if results[i].Content != "ok" {
			t.Fatalf("stream %d answer = %+v", i, results[i])
		}
	}
}
