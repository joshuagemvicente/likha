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
	}, []ToolDefinition{{Name: "list", Description: "List files", Parameters: json.RawMessage(`{"type":"object"}`)}}, func(chunk string) { chunks = append(chunks, chunk) })
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
	answer, err := client.Stream(context.Background(), nil, nil, nil)
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
	answer, err := client.Stream(context.Background(), nil, nil, nil)
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
			_, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "test"}}, nil, nil)
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
	})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(chunks, []string{"first"}) || answer.Role != "" {
		t.Fatalf("answer=%+v, chunks=%v, error=%v", answer, chunks, err)
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
	_, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "private"}}, nil, nil)
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
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil); err != nil {
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
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil); err != nil {
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
	if _, err := plain.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil); err != nil {
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
