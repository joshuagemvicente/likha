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
	"testing"
)

type variantTestTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (v variantTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host = v.target.Scheme, v.target.Host
	return v.next.RoundTrip(copy)
}

func TestShippedVariantAdaptersSendDocumentedFields(t *testing.T) {
	for _, tc := range []struct{ provider, id, variant, fragment, route string }{
		{"openai", "gpt-5.5", "high", `"reasoning":{"effort":"high"}`, "/v1/responses"},
		{"claude", "claude-opus-5-5", "high", `"output_config":{"effort":"high"}`, "/v1/messages"},
		{"claude", "claude-haiku-4-5", "high", `"thinking":{"budget_tokens":16000,"type":"enabled"}`, "/v1/messages"},
		{"openrouter", "openai/gpt-5.5", "high", `"reasoning":{"effort":"high"}`, "/api/v1/chat/completions"},
		{"gemini", "gemini-2.5-flash", "none", `"extra_body":{"google":{"thinking_config":{"thinking_budget":0}}}`, "/v1beta/openai/chat/completions"},
		{"deepseek", "deepseek-v4-flash", "none", `"thinking":{"type":"disabled"}`, "/v1/chat/completions"},
		{"groq", "openai/gpt-oss-120b", "high", `"reasoning_effort":"high"`, "/openai/v1/chat/completions"},
	} {
		t.Run(tc.provider+"/"+tc.id+"#"+tc.variant, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				data, err := io.ReadAll(req.Body)
				if err != nil {
					t.Error(err)
				}
				if req.URL.Path != tc.route || !strings.Contains(string(data), tc.fragment) {
					t.Errorf("wire %s: %s", req.URL.Path, data)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				switch tc.provider {
				case "openai":
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
				case "claude":
					fmt.Fprint(w, sseFrames(evMessageStart, evTextStart(0), evText(0, "done"), evStop(0), evMessageDelta("end_turn"), evMessageStop))
				default:
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			p, _ := LookupProvider(tc.provider)
			client, err := New(p.BaseURL, tc.id, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			if err := client.SetVariant(p, nil, VariantConfig{}, tc.variant); err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.http.Transport = variantTestTransport{target, http.DefaultTransport}
			if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVariantRejectionNoDowngradeAndDefaultBaseline(t *testing.T) {
	p, _ := LookupProvider("openai")
	client, err := New(p.BaseURL, "gpt-5.5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	config := VariantConfig{Provider: Overlay{Settings: map[string]any{"reasoningEffort": "low"}}}
	if err := client.SetVariant(p, nil, config, "high"); err != nil {
		t.Fatal(err)
	}
	fork, err := client.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetVariant(p, nil, config, ""); err != nil {
		t.Fatal(err)
	}
	defaultBody, err := client.variantPayload([]byte(`{"model":"gpt-5.5"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(defaultBody), `"effort":"low"`) || fork.Variant() != "high" {
		t.Fatal("Default dropped baseline or mutated fork")
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"unrelated bad request"}}`)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	fork.http.Transport = variantTestTransport{target, http.DefaultTransport}
	_, err = fork.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
	var rejected *VariantRejectedError
	if !errors.As(err, &rejected) || rejected.Variant != "high" || requests != 1 {
		t.Fatalf("rejection %v, requests=%d", err, requests)
	}
}

func TestConfiguredVariantOrderingBoundsAndProtectedBodies(t *testing.T) {
	p, _ := LookupProvider("claude")
	client, err := New(p.BaseURL, "claude-haiku-4-5", "unused")
	if err != nil {
		t.Fatal(err)
	}
	config := VariantConfig{Model: Overlay{Settings: map[string]any{"maxOutputTokens": int64(4096)}}}
	if err := client.SetVariant(p, nil, config, "max"); err != nil {
		t.Fatal(err)
	}
	body, err := client.variantPayload([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"budget_tokens":4095`) {
		t.Fatalf("budget exceeded baseline: %s", body)
	}
	openai, _ := LookupProvider("openai")
	choices, err := ModelVariants(openai, "gpt-5.5", nil, []Variant{{ID: "high"}, {ID: "low"}})
	if err != nil || len(choices) != 2 || choices[0].ID != "high" || choices[1].ID != "low" {
		t.Fatalf("configured order: %+v, %v", choices, err)
	}
	for _, raw := range []string{`{"tools":[]}`, `{"store":true}`, `{"reasoning_effort":"high"}`} {
		var fields map[string]any
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatal(err)
		}
		if _, err := ModelVariants(openai, "gpt-5.5", nil, []Variant{{ID: "bad", Overlay: Overlay{Body: fields}}}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
