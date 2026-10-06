package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Usage-detail parsing against the documented provider shapes listed in
// specs/model-metadata/context.md § Usage-field documentation.

func TestParseFinalUsageDetailedProviderShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want RequestUsage
	}{
		{
			// openai-python CompletionUsage: cached and cache-write tokens are
			// present in the prompt; reasoning tokens count toward completion.
			name: "openai prompt and completion details",
			raw: `{"prompt_tokens":2006,"completion_tokens":300,"total_tokens":2306,
				"prompt_tokens_details":{"cached_tokens":1920,"cache_write_tokens":64,"audio_tokens":0},
				"completion_tokens_details":{"reasoning_tokens":192,"audio_tokens":0,"accepted_prediction_tokens":0}}`,
			want: RequestUsage{Prompt: 2006, Completion: 300, PromptSeen: true, CompletionSeen: true,
				CacheRead: 1920, CacheWrite: 64, Reasoning: 192},
		},
		{
			// OpenRouter usage accounting: OpenAI fields plus a top-level cost
			// in US dollars (credits). The documented example also carries
			// cost_details.upstream_inference_cost, the upstream provider's
			// charge on a BYOK request, which the user pays on top of
			// OpenRouter's fee: the request costs 0.95 + 19.
			name: "openrouter reported cost with BYOK upstream charge",
			raw: `{"prompt_tokens":194,"completion_tokens":2,"total_tokens":196,"cost":0.95,
				"is_byok":false,"cost_details":{"upstream_inference_cost":19},
				"prompt_tokens_details":{"cached_tokens":0},
				"completion_tokens_details":{"reasoning_tokens":0}}`,
			want: RequestUsage{Prompt: 194, Completion: 2, PromptSeen: true, CompletionSeen: true,
				Cost: 19.95, CostSeen: true},
		},
		{
			name: "openrouter non-BYOK upstream cost is null or zero",
			raw: `{"prompt_tokens":194,"completion_tokens":2,"cost":0.95,
				"cost_details":{"upstream_inference_cost":null}}`,
			want: RequestUsage{Prompt: 194, Completion: 2, PromptSeen: true, CompletionSeen: true,
				Cost: 0.95, CostSeen: true},
		},
		{
			name: "openrouter malformed upstream cost is ignored",
			raw: `{"prompt_tokens":194,"completion_tokens":2,"cost":0.95,
				"cost_details":{"upstream_inference_cost":"19"}}`,
			want: RequestUsage{Prompt: 194, Completion: 2, PromptSeen: true, CompletionSeen: true,
				Cost: 0.95, CostSeen: true},
		},
		{
			name: "upstream cost without a reported cost is not a cost",
			raw:  `{"prompt_tokens":194,"completion_tokens":2,"cost_details":{"upstream_inference_cost":19}}`,
			want: RequestUsage{Prompt: 194, Completion: 2, PromptSeen: true, CompletionSeen: true},
		},
		{
			// xAI's documented chat-completions example (docs.x.ai
			// openapi.json): reasoning is counted on top of completion,
			// 32 + 9 + 94 = 135, so completion becomes 9 + 94.
			name: "xai reasoning on top of completion",
			raw: `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,
				"prompt_tokens_details":{"text_tokens":32,"audio_tokens":0,"image_tokens":0,"cached_tokens":6},
				"completion_tokens_details":{"reasoning_tokens":94,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0},
				"num_sources_used":0}`,
			want: RequestUsage{Prompt: 32, Completion: 103, PromptSeen: true, CompletionSeen: true,
				CacheRead: 6, Reasoning: 94},
		},
		{
			name: "reasoning above completion is additive without total_tokens",
			raw:  `{"prompt_tokens":32,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":94}}`,
			want: RequestUsage{Prompt: 32, Completion: 103, PromptSeen: true, CompletionSeen: true, Reasoning: 94},
		},
		{
			// total = prompt + completion: reasoning is inside completion.
			name: "reasoning inside completion stays a subset",
			raw:  `{"prompt_tokens":32,"completion_tokens":100,"total_tokens":132,"completion_tokens_details":{"reasoning_tokens":94}}`,
			want: RequestUsage{Prompt: 32, Completion: 100, PromptSeen: true, CompletionSeen: true, Reasoning: 94},
		},
		{
			name: "xai cost_in_usd_ticks is the exact charge",
			raw: `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,"cost_in_usd_ticks":12500000000,
				"completion_tokens_details":{"reasoning_tokens":94}}`,
			want: RequestUsage{Prompt: 32, Completion: 103, PromptSeen: true, CompletionSeen: true,
				Reasoning: 94, Cost: 1.25, CostSeen: true},
		},
		{
			name: "cost wins over cost_in_usd_ticks",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":0.5,"cost_in_usd_ticks":12500000000}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true, Cost: 0.5, CostSeen: true},
		},
		{
			name: "malformed cost_in_usd_ticks is ignored",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost_in_usd_ticks":-5}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "null cost_in_usd_ticks is not a reported zero",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost_in_usd_ticks":null}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "openrouter zero cost is a reported zero",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":0}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true, CostSeen: true},
		},
		{
			// DeepSeek: prompt_tokens = hit + miss; the hit count is the
			// cache read when the OpenAI detail object is absent.
			name: "deepseek top-level cache hit tokens",
			raw: `{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,
				"prompt_cache_hit_tokens":800,"prompt_cache_miss_tokens":200}`,
			want: RequestUsage{Prompt: 1000, Completion: 50, PromptSeen: true, CompletionSeen: true, CacheRead: 800},
		},
		{
			name: "openai cached_tokens wins over deepseek hit tokens",
			raw: `{"prompt_tokens":1000,"completion_tokens":50,
				"prompt_cache_hit_tokens":800,"prompt_cache_miss_tokens":200,
				"prompt_tokens_details":{"cached_tokens":700}}`,
			want: RequestUsage{Prompt: 1000, Completion: 50, PromptSeen: true, CompletionSeen: true, CacheRead: 700},
		},
		{
			name: "deepseek hit tokens used when cached_tokens is malformed",
			raw: `{"prompt_tokens":1000,"completion_tokens":50,
				"prompt_cache_hit_tokens":800,"prompt_tokens_details":{"cached_tokens":"700"}}`,
			want: RequestUsage{Prompt: 1000, Completion: 50, PromptSeen: true, CompletionSeen: true, CacheRead: 800},
		},
		{
			// Anthropic's OpenAI-compatible endpoint returns empty detail
			// objects (prompt caching unsupported).
			name: "anthropic compat empty detail objects",
			raw: `{"prompt_tokens":40,"completion_tokens":12,"total_tokens":52,
				"prompt_tokens_details":{},"completion_tokens_details":{}}`,
			want: RequestUsage{Prompt: 40, Completion: 12, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "null detail objects",
			raw:  `{"prompt_tokens":40,"completion_tokens":12,"prompt_tokens_details":null,"completion_tokens_details":null}`,
			want: RequestUsage{Prompt: 40, Completion: 12, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "malformed negative and string detail counts are ignored",
			raw: `{"prompt_tokens":100,"completion_tokens":20,
				"prompt_tokens_details":{"cached_tokens":-5,"cache_write_tokens":"9"},
				"completion_tokens_details":{"reasoning_tokens":1.5},
				"prompt_cache_hit_tokens":"bad"}`,
			want: RequestUsage{Prompt: 100, Completion: 20, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "detail objects of the wrong type are ignored",
			raw: `{"prompt_tokens":100,"completion_tokens":20,
				"prompt_tokens_details":[1,2],"completion_tokens_details":"x"}`,
			want: RequestUsage{Prompt: 100, Completion: 20, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "negative cost is ignored",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":-0.5}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "string cost is ignored",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":"0.5"}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "NaN-like cost strings are ignored",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":"NaN"}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "out-of-range cost is ignored",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":1e999}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "null cost is not a reported zero",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":null}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "object cost is ignored",
			raw:  `{"prompt_tokens":10,"completion_tokens":1,"cost":{"total":0.5}}`,
			want: RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true},
		},
		{
			// Details never invent a completion count.
			name: "prompt-only usage with reasoning detail keeps completion unseen",
			raw: `{"prompt_tokens":500,"prompt_tokens_details":{"cached_tokens":100},
				"completion_tokens_details":{"reasoning_tokens":40},"cost":0.01}`,
			want: RequestUsage{Prompt: 500, PromptSeen: true, CacheRead: 100, Reasoning: 40, Cost: 0.01, CostSeen: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := parseFinalUsageDetailed(json.RawMessage(tc.raw))
			if !report.ok {
				t.Fatalf("report.ok = false for %s", tc.raw)
			}
			if got := report.request(); got != tc.want {
				t.Fatalf("request() = %+v\nwant        %+v", got, tc.want)
			}
		})
	}
}

func TestParseFinalUsageDetailedWithoutUsage(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `"usage"`, `{"prompt_tokens":"bad"}`} {
		report := parseFinalUsageDetailed(json.RawMessage(raw))
		if report.ok || report.request() != (RequestUsage{}) {
			t.Errorf("%q: report = %+v, want no usage", raw, report)
		}
	}
}

func TestParseCodexUsageDetailedResponsesShape(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want RequestUsage
	}{
		{
			// OpenAI Responses usage object.
			name: "responses input and output details",
			raw: `{"input_tokens":5000,"output_tokens":900,"total_tokens":5900,
				"input_tokens_details":{"cached_tokens":4096},
				"output_tokens_details":{"reasoning_tokens":640}}`,
			want: RequestUsage{Prompt: 5000, Completion: 900, PromptSeen: true, CompletionSeen: true,
				CacheRead: 4096, Reasoning: 640},
		},
		{
			name: "responses without details",
			raw:  `{"input_tokens":12,"output_tokens":3}`,
			want: RequestUsage{Prompt: 12, Completion: 3, PromptSeen: true, CompletionSeen: true},
		},
		{
			name: "malformed responses details are ignored",
			raw: `{"input_tokens":12,"output_tokens":3,
				"input_tokens_details":{"cached_tokens":-1},"output_tokens_details":{"reasoning_tokens":"x"}}`,
			want: RequestUsage{Prompt: 12, Completion: 3, PromptSeen: true, CompletionSeen: true},
		},
		{
			// xAI's documented Responses example: 32 + 9 + 110 = 151.
			name: "xai responses reasoning on top of output",
			raw: `{"input_tokens":32,"input_tokens_details":{"cached_tokens":8},"output_tokens":9,
				"output_tokens_details":{"reasoning_tokens":110},"total_tokens":151}`,
			want: RequestUsage{Prompt: 32, Completion: 119, PromptSeen: true, CompletionSeen: true,
				CacheRead: 8, Reasoning: 110},
		},
		{
			name: "input-only responses usage keeps completion unseen",
			raw:  `{"input_tokens":12,"output_tokens_details":{"reasoning_tokens":7}}`,
			want: RequestUsage{Prompt: 12, PromptSeen: true, Reasoning: 7},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := parseCodexUsageDetailed(json.RawMessage(tc.raw))
			if !report.ok {
				t.Fatalf("report.ok = false for %s", tc.raw)
			}
			if got := report.request(); got != tc.want {
				t.Fatalf("request() = %+v\nwant        %+v", got, tc.want)
			}
		})
	}
}

var hello = []Message{{Role: "user", Content: "hello"}}

func TestStreamUsageReturnsRequestBreakdown(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		sendEvents(w,
			`{"choices":[{"delta":{"role":"assistant","content":"hi"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":2006,"completion_tokens":300,"cost":0.0123,`+
				`"prompt_tokens_details":{"cached_tokens":1920,"cache_write_tokens":64},`+
				`"completion_tokens_details":{"reasoning_tokens":192}}}`,
			`[DONE]`)
	})
	answer, usage, ok, err := client.StreamUsage(context.Background(), hello, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Content != "hi" {
		t.Fatalf("answer = %+v", answer)
	}
	want := RequestUsage{Prompt: 2006, Completion: 300, PromptSeen: true, CompletionSeen: true,
		CacheRead: 1920, CacheWrite: 64, Reasoning: 192, Cost: 0.0123, CostSeen: true}
	if !ok || usage != want {
		t.Fatalf("StreamUsage usage = (%+v, %v), want (%+v, true)", usage, ok, want)
	}
	last, lastOK := client.LastRequest()
	if !lastOK || last != usage {
		t.Fatalf("LastRequest = (%+v, %v), want StreamUsage's (%+v, true)", last, lastOK, usage)
	}
	// The legacy accessors still see the same request.
	if tokens, ok := client.LastTokenUsage(); !ok || tokens != (TokenUsage{Prompt: 2006, Completion: 300, PromptSeen: true}) {
		t.Fatalf("LastTokenUsage = (%+v, %v)", tokens, ok)
	}
}

func TestStreamUsageWithoutUsageChunk(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		sendEvents(w, `{"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}`, `[DONE]`)
	})
	answer, usage, ok, err := client.StreamUsage(context.Background(), hello, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Content != "hi" || ok || usage != (RequestUsage{}) {
		t.Fatalf("StreamUsage = (%q, %+v, %v), want answer with no usage", answer.Content, usage, ok)
	}
	if last, ok := client.LastRequest(); ok || last != (RequestUsage{}) {
		t.Fatalf("LastRequest = (%+v, %v), want none", last, ok)
	}
}

func TestStreamUsageErrorReturnsNoUsage(t *testing.T) {
	t.Run("HTTP failure", func(t *testing.T) {
		client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		_, usage, ok, err := client.StreamUsage(context.Background(), hello, nil, nil, nil)
		if err == nil || ok || usage != (RequestUsage{}) {
			t.Fatalf("StreamUsage = (%+v, %v, %v), want error and no usage", usage, ok, err)
		}
	})
	t.Run("stream ends before DONE after usage", func(t *testing.T) {
		// Usage arrived but the turn is partial: a failed request reports no
		// usage rather than a priced half-answer.
		client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w,
				`{"choices":[{"delta":{"content":"hi"}}]}`,
				`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.5}}`)
		})
		_, usage, ok, err := client.StreamUsage(context.Background(), hello, nil, nil, nil)
		if err == nil || ok || usage != (RequestUsage{}) {
			t.Fatalf("StreamUsage = (%+v, %v, %v), want error and no usage", usage, ok, err)
		}
		if last, ok := client.LastRequest(); ok {
			t.Fatalf("LastRequest after failure = %+v, want none", last)
		}
	})
	t.Run("invalid request", func(t *testing.T) {
		client := localClient(t, func(http.ResponseWriter, *http.Request) { t.Error("no request expected") })
		_, usage, ok, err := client.StreamUsage(context.Background(), []Message{{Role: "bogus"}}, nil, nil, nil)
		if err == nil || ok || usage != (RequestUsage{}) {
			t.Fatalf("StreamUsage = (%+v, %v, %v), want error and no usage", usage, ok, err)
		}
	})
}

func TestStreamUnchangedAndLastRequestTracksLatest(t *testing.T) {
	var requests atomic.Int32
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n := requests.Add(1)
		sendEvents(w,
			fmt.Sprintf(`{"choices":[{"delta":{"content":"r%d"}}]}`, n),
			fmt.Sprintf(`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d}}}`, 100*n, 10*n, 50*n),
			`[DONE]`)
	})
	answer, err := client.Stream(context.Background(), hello, nil, nil, nil)
	if err != nil || answer.Content != "r1" {
		t.Fatalf("Stream = (%+v, %v)", answer, err)
	}
	if last, ok := client.LastRequest(); !ok || last.Prompt != 100 || last.CacheRead != 50 {
		t.Fatalf("LastRequest after Stream = (%+v, %v)", last, ok)
	}
	_, usage, ok, err := client.StreamUsage(context.Background(), hello, nil, nil, nil)
	if err != nil || !ok {
		t.Fatalf("StreamUsage = (%+v, %v, %v)", usage, ok, err)
	}
	want := RequestUsage{Prompt: 200, Completion: 20, PromptSeen: true, CompletionSeen: true, CacheRead: 100}
	if usage != want {
		t.Fatalf("second request usage = %+v, want %+v (per request, never cumulative)", usage, want)
	}
	if last, ok := client.LastRequest(); !ok || last != usage {
		t.Fatalf("LastRequest = (%+v, %v), want (%+v, true)", last, ok, usage)
	}
}

func TestStreamUsageCodexWire(t *testing.T) {
	server := newCodexUsageServer(t, `{"response":{"status":"completed","output":[],"usage":{"input_tokens":5000,"output_tokens":900,`+
		`"input_tokens_details":{"cached_tokens":4096},"output_tokens_details":{"reasoning_tokens":640}}}}`)
	client := newOAuthTestClient(t, server, server+"/v1", validOAuthCredentials())
	answer, usage, ok, err := client.StreamUsage(context.Background(), hello, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := RequestUsage{Prompt: 5000, Completion: 900, PromptSeen: true, CompletionSeen: true, CacheRead: 4096, Reasoning: 640}
	if answer.Content != "ok" || !ok || usage != want {
		t.Fatalf("StreamUsage = (%q, %+v, %v), want (ok, %+v, true)", answer.Content, usage, ok, want)
	}
	if last, ok := client.LastRequest(); !ok || last != usage {
		t.Fatalf("LastRequest = (%+v, %v)", last, ok)
	}
}

func newCodexUsageServer(t *testing.T, terminal string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n")
		fmt.Fprint(w, `data: {"delta":"ok"}`+"\n\n")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, "data: "+terminal+"\n\n")
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// RequestCost on parsed usage: the provider's reported charge wins over the
// catalog, and cache hits lower an estimate.
func TestRequestCostOnParsedUsage(t *testing.T) {
	openrouter := parseFinalUsageDetailed(json.RawMessage(
		`{"prompt_tokens":1000000,"completion_tokens":0,"cost":0.0421,"prompt_tokens_details":{"cached_tokens":0}}`)).request()
	cost, source := RequestCost("openrouter", "openai/gpt-4o-mini", openrouter)
	if source != CostReported || !source.Exact() || cost != 0.0421 {
		t.Fatalf("openrouter reported = (%v, %v), want (0.0421, reported)", cost, source)
	}
	// Without the reported figure the same tokens are a catalog estimate.
	openrouter.CostSeen, openrouter.Cost = false, 0
	cost, source = RequestCost("openrouter", "openai/gpt-4o-mini", openrouter)
	if source != CostEstimated || source.Exact() || !near(cost, 0.15) {
		t.Fatalf("openrouter estimate = (%v, %v), want (0.15, estimated)", cost, source)
	}

	cached := parseFinalUsageDetailed(json.RawMessage(
		`{"prompt_tokens":1000000,"completion_tokens":100000,
		"prompt_tokens_details":{"cached_tokens":800000},
		"completion_tokens_details":{"reasoning_tokens":0}}`)).request()
	uncached := cached
	uncached.CacheRead = 0
	withHits, source := RequestCost("openai", "gpt-4o-mini", cached)
	if source != CostEstimated {
		t.Fatalf("cached source = %v, want estimated", source)
	}
	withoutHits, _ := RequestCost("openai", "gpt-4o-mini", uncached)
	// gpt-4o-mini: $0.15 input, $0.075 cache read, $0.60 output.
	if want := 0.2*0.15 + 0.8*0.075 + 0.1*0.6; !near(withHits, want) {
		t.Fatalf("cached estimate = %v, want %v", withHits, want)
	}
	if !(withHits < withoutHits) || !near(withoutHits, 0.15+0.06) {
		t.Fatalf("cached %v should be below uncached %v (want 0.21)", withHits, withoutHits)
	}

	// xAI's documented example prices all 94 reasoning tokens, not only
	// the 9 completion tokens. grok-4.3: $1.25 input, $0.20 cache read,
	// $2.50 output: 26*1.25 + 6*0.2 + 103*2.5 per 1M.
	xai := parseFinalUsageDetailed(json.RawMessage(
		`{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,
		"prompt_tokens_details":{"cached_tokens":6},"completion_tokens_details":{"reasoning_tokens":94}}`)).request()
	if cost, source := RequestCost("xai", "grok-4.3", xai); source != CostEstimated || !near(cost, (26*1.25+6*0.2+103*2.5)/1_000_000) {
		t.Fatalf("xai reasoning estimate = (%v, %v), want (0.0002912, estimated)", cost, source)
	}

	// A prompt-only report is never estimated.
	partial := parseFinalUsageDetailed(json.RawMessage(`{"prompt_tokens":1000}`)).request()
	if _, source := RequestCost("openai", "gpt-4o-mini", partial); source.Known() {
		t.Fatalf("prompt-only usage priced (%v); want unknown", source)
	}
}
