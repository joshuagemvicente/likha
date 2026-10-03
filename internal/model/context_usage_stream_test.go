package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestStreamRequestsAndReadsUsage(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		want   TokenUsage
	}{
		{
			name: "usage-only event with aliases",
			events: []string{
				`{"choices":[{"delta":{"content":"answer"}}]}`,
				`{"choices":[],"usage":{"input_tokens":23,"output_tokens":4}}`,
				`[DONE]`,
			},
			want: TokenUsage{Prompt: 23, Completion: 4, PromptSeen: true},
		},
		{
			name: "usage alongside final choice",
			events: []string{
				`{"choices":[{"delta":{"content":"answer"}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":31,"completion_tokens":6}}`,
				`[DONE]`,
			},
			want: TokenUsage{Prompt: 31, Completion: 6, PromptSeen: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					StreamOptions struct {
						IncludeUsage bool `json:"include_usage"`
					} `json:"stream_options"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if !request.StreamOptions.IncludeUsage {
					t.Errorf("stream_options.include_usage = false; request = %+v", request)
				}
				writeModelEvents(w, tc.events...)
			}))
			defer server.Close()

			client := newTestModelClient(t, server.URL)
			answer, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if answer.Content != "answer" {
				t.Fatalf("answer.Content = %q, want answer", answer.Content)
			}
			if got, ok := client.LastTokenUsage(); !ok || got != tc.want {
				t.Fatalf("LastTokenUsage = (%+v, %v), want (%+v, true)", got, ok, tc.want)
			}
		})
	}
}

func TestStreamOptionalUsageAndRequestFreshness(t *testing.T) {
	var requests atomic.Int32
	var client *Client
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch requests.Add(1) {
		case 1:
			writeModelEvents(w,
				`{"choices":[{"delta":{"content":"first"}}]}`,
				`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20}}`,
				`[DONE]`)
		case 2:
			if got, ok := client.LastTokenUsage(); ok || got != (TokenUsage{}) {
				t.Errorf("usage at start of failed request = (%+v, %v), want zero/false", got, ok)
			}
			w.WriteHeader(http.StatusBadGateway)
		case 3:
			writeModelEvents(w,
				`{"choices":[{"delta":{"content":"third"}}],"usage":{"prompt_tokens":"bad","completion_tokens":"bad"}}`,
				`[DONE]`)
		}
	}))
	defer server.Close()
	client = newTestModelClient(t, server.URL)

	for i := 0; i < 3; i++ {
		_, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil, nil, nil)
		if i == 1 {
			if err == nil {
				t.Fatal("second request succeeded; want HTTP failure")
			}
			if got, ok := client.LastTokenUsage(); ok || got != (TokenUsage{}) {
				t.Fatalf("usage after failed request = (%+v, %v), want zero/false", got, ok)
			}
			continue
		}
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if i == 2 {
			if got, ok := client.LastTokenUsage(); ok || got != (TokenUsage{}) {
				t.Fatalf("usage after malformed optional usage = (%+v, %v), want zero/false", got, ok)
			}
		}
	}
}

func TestStreamFallsBackWhenProviderRejectsOptionalUsageRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch requests.Add(1) {
		case 1:
			if _, ok := body["stream_options"]; !ok {
				t.Error("first request did not ask for optional usage")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":{"message":"unsupported stream_options.include_usage"}}`)
		case 2:
			if _, ok := body["stream_options"]; ok {
				t.Error("fallback request still included unsupported stream_options")
			}
			writeModelEvents(w, `{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`, `[DONE]`)
		default:
			t.Errorf("unexpected retry count: %d", requests.Load())
		}
	}))
	defer server.Close()
	client := newTestModelClient(t, server.URL)
	answer, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("stream after telemetry fallback: %v", err)
	}
	if answer.Content != "answer" || requests.Load() != 2 {
		t.Fatalf("answer=%q requests=%d, want answer and one fallback retry", answer.Content, requests.Load())
	}
	if _, ok := client.LastTokenUsage(); ok {
		t.Fatal("fallback response without usage retained telemetry from the rejected attempt")
	}
}

func TestStreamUsageWithoutInputCountIsNotAContextMeasurement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeModelEvents(w,
			`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"completion_tokens":4}}`,
			`[DONE]`)
	}))
	defer server.Close()
	client := newTestModelClient(t, server.URL)
	if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	usage, ok := client.LastTokenUsage()
	if !ok || usage.Completion != 4 || usage.PromptSeen {
		t.Fatalf("LastTokenUsage = (%+v, %t), want completion-only usage", usage, ok)
	}
}

func TestListModelsWithDetailsEnvelopesAndMetadata(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []ModelDetails
	}{
		{
			name: "data envelope",
			body: `{"object":"list","data":[{"id":"window","context_window":65536},{"id":"length","context_length":131072},{"id":"camel","contextWindow":32768},{"id":"nested","limit":{"context":200000}},{"id":"zero","context_length":0},{"id":"negative","context_length":-1},{"id":"malformed","context_window":"large","limit":"not-an-object"}]}`,
			want: []ModelDetails{
				{ID: "window", ContextWindow: 65536, ContextWindowSource: "context_window"},
				{ID: "length", ContextWindow: 131072, ContextWindowSource: "context_length"},
				{ID: "camel", ContextWindow: 32768, ContextWindowSource: "contextWindow"},
				{ID: "nested", ContextWindow: 200000, ContextWindowSource: "limit.context"},
				{ID: "zero"},
				{ID: "negative"},
				{ID: "malformed"},
			},
		},
		{
			name: "top-level array",
			body: `[{"id":"local","limit":{"context":98304}},{"id":"unknown"}]`,
			want: []ModelDetails{{ID: "local", ContextWindow: 98304, ContextWindowSource: "limit.context"}, {ID: "unknown"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()

			got, err := ListModelsWithDetails(context.Background(), server.URL+"/v1", "")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ListModelsWithDetails() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func newTestModelClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	client, err := New(serverURL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeModelEvents(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
	}
}
