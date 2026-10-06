package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const anthropicModelsPage1 = `{"data":[
	{"type":"model","id":"claude-opus-5-5","display_name":"Claude Opus 5.5","max_input_tokens":1000000,"max_tokens":128000,
	 "capabilities":{"thinking":{"supported":true,"types":{"adaptive":{"supported":true},"enabled":{"supported":false}}},
	  "effort":{"supported":true,"low":{"supported":true},"medium":{"supported":true},"high":{"supported":true},"xhigh":{"supported":true},"max":{"supported":true}}}},
	{"type":"model","id":"claude-haiku-4-5","display_name":"Claude Haiku 4.5","max_input_tokens":200000,"max_tokens":64000,
	 "capabilities":{"thinking":{"supported":true,"types":{"adaptive":{"supported":false},"enabled":{"supported":true}}},
	  "effort":{"supported":false,"low":{"supported":false},"medium":{"supported":false},"high":{"supported":false},"xhigh":null,"max":{"supported":false}}}}
],"has_more":true,"first_id":"claude-opus-5-5","last_id":"claude-haiku-4-5"}`

const anthropicModelsPage2 = `{"data":[
	{"type":"model","id":"claude-opus-4-7","display_name":"Claude Opus 4.7","max_input_tokens":0,"max_tokens":null,"capabilities":null}
],"has_more":false,"first_id":"claude-opus-4-7","last_id":"claude-opus-4-7"}`

// anthropicModelsHandler serves the two-page list and checks the native
// headers and the cursor.
func anthropicModelsHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
			t.Errorf("model list headers = %v", r.Header)
		}
		if r.URL.Query().Get("limit") != "1000" {
			t.Errorf("limit = %q", r.URL.Query().Get("limit"))
		}
		switch r.URL.Query().Get("after_id") {
		case "":
			io.WriteString(w, anthropicModelsPage1)
		case "claude-haiku-4-5":
			io.WriteString(w, anthropicModelsPage2)
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("after_id"))
		}
	}
}

func TestListModelsWithDetailsAnthropicPages(t *testing.T) {
	useAnthropicListServer(t, anthropicModelsHandler(t))
	details, err := ListModelsWithDetails(context.Background(), anthropicBase, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelDetails{
		{ID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5", ContextWindow: 1000000, ContextWindowSource: "max_input_tokens", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
		{ID: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", ContextWindow: 200000, ContextWindowSource: "max_input_tokens"},
		{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7"},
	}
	if !reflect.DeepEqual(details, want) {
		t.Fatalf("details\n got %+v\nwant %+v", details, want)
	}
	// The list also teaches request building each model's limits.
	if anthropicMaxTokens("claude-opus-5-5") != 128000 || anthropicThinking("claude-haiku-4-5") != thinkingNone || anthropicThinking("claude-opus-5-5") != thinkingAdaptive {
		t.Fatal("model list did not record output limits and thinking support")
	}
	if info, ok := lookupAnthropicModel("claude-opus-4-7"); !ok || info.adaptiveKnown || anthropicThinking("claude-opus-4-7") != thinkingAdaptive {
		t.Fatalf("null capabilities should fall back to the table: %+v", info)
	}
	ids, err := ListModels(context.Background(), anthropicBase, "test-key")
	if err != nil || !reflect.DeepEqual(ids, []string{"claude-opus-5-5", "claude-haiku-4-5", "claude-opus-4-7"}) {
		t.Fatalf("ListModels = %v, %v", ids, err)
	}
}

func TestAnthropicCheckAndModelsUseNativeList(t *testing.T) {
	client := anthropicTestClient(t, "claude-opus-5-5", anthropicModelsHandler(t))
	if err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info, ok := lookupAnthropicModel("claude-opus-5-5"); !ok || info.maxTokens != 128000 || !info.adaptive {
		t.Fatalf("Check did not record model info: %+v", info)
	}
	useAnthropicListServer(t, anthropicModelsHandler(t))
	fork, err := client.Fork()
	if err != nil {
		t.Fatal(err)
	}
	details, err := fork.Models(context.Background())
	if err != nil || len(details) != 3 {
		t.Fatalf("Models = %v, %v", details, err)
	}
}

func TestAnthropicModelListFailures(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		is      error
		want    string
	}{
		{
			name: "401",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("request-id", "req_list")
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			},
			is:   ErrUnauthorized,
			want: "(request req_list)",
		},
		{
			name: "500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			is: ErrUnexpectedResponse,
		},
		{
			name:    "empty list",
			handler: func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":[],"has_more":false}`) },
			is:      ErrUnexpectedResponse,
			want:    "the model list is empty",
		},
		{
			name:    "not a list",
			handler: func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"models":[]}`) },
			is:      ErrUnexpectedResponse,
		},
		{
			name: "more pages without a cursor",
			handler: func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"data":[{"id":"a"}],"has_more":true}`)
			},
			is:   ErrUnexpectedResponse,
			want: "no last_id",
		},
		{
			name: "endless pages",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"data":[{"id":"m%[1]s"}],"has_more":true,"last_id":"m%[1]s"}`, r.URL.Query().Get("after_id"))
			},
			is:   ErrUnexpectedResponse,
			want: "did not end within 10 pages",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useAnthropicListServer(t, tc.handler)
			_, err := ListModelsWithDetails(context.Background(), anthropicBase, "test-key")
			if !errors.Is(err, tc.is) {
				t.Fatalf("error = %v, want %v", err, tc.is)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q", err, tc.want)
			}
			if tc.is != ErrUnauthorized && errors.Is(err, ErrUnauthorized) {
				t.Fatalf("error %v reports rejected credentials", err)
			}
		})
	}
}
