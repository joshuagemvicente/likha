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
	"testing"

	"likha/internal/model"
)

type variantLoopTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (v variantLoopTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host, copy.Host = v.target.Scheme, v.target.Host, ""
	return v.next.RoundTrip(copy)
}

func TestVariantsFullToolRoundTripOnShippedAdapters(t *testing.T) {
	for _, tc := range []struct{ provider, id, variant, replay string }{
		{"openai", "gpt-5.5", "high", `"encrypted_content":"opaque-state"`},
		{"claude", "claude-haiku-4-5", "high", `"signature":"sig-main"`},
		{"claude", "claude-opus-5-5", "high", `"signature":"sig-main"`},
		{"openrouter", "openai/gpt-5.5", "high", `"reasoning_details"`},
		{"gemini", "gemini-3.1-pro-preview", "high", `"thought_signature":"sig-main"`},
		{"deepseek", "deepseek-v4-flash", "high", `"reasoning_content":"private-state"`},
	} {
		t.Run(tc.provider+"/"+tc.id, func(t *testing.T) {
			repo, root := steerRepo(t)
			var bodies []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					fmt.Fprintf(w, `{"data":[{"id":%q}],"has_more":false}`, tc.id)
					return
				}
				data, err := io.ReadAll(req.Body)
				if err != nil {
					t.Error(err)
				}
				bodies = append(bodies, string(data))
				w.Header().Set("Content-Type", "text/event-stream")
				first := len(bodies) == 1
				switch tc.provider {
				case "claude":
					if first {
						claudeToolTurn(w, "sig-main", "toolu_1", "known.txt")
					} else {
						claudeTextTurn(w, "done")
					}
				case "openai":
					output := `[{"type":"message","id":"msg_2","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}]`
					if first {
						output = `[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-state","summary":[]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","namespace":"likha","arguments":"{\"path\":\"known.txt\"}"}]`
					}
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":%s}}\n\n", output)
				default:
					if !first {
						sendEvents(w, `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`, `[DONE]`)
						return
					}
					delta := map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "read", "arguments": `{"path":"known.txt"}`}}}}
					switch tc.provider {
					case "deepseek":
						delta["reasoning_content"] = "private-state"
					case "openrouter":
						delta["reasoning_details"] = []any{map[string]any{"type": "reasoning.encrypted", "data": "private-state", "id": "rs_1", "format": "openai-responses-v1", "index": 0}}
					case "gemini":
						delta["tool_calls"].([]any)[0].(map[string]any)["extra_content"] = map[string]any{"google": map[string]any{"thought_signature": "sig-main"}}
					}
					payload, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}})
					if err != nil {
						t.Error(err)
					}
					sendEvents(w, string(payload), `[DONE]`)
				}
			}))
			defer server.Close()
			p, _ := model.LookupProvider(tc.provider)
			composer, err := model.New(p.BaseURL, tc.id, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			if err := composer.SetVariant(p, nil, model.VariantConfig{}, tc.variant); err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client, err := composer.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper { return variantLoopTransport{target, next} })
			if err != nil {
				t.Fatal(err)
			}
			toolCalls, completed := 0, false
			RunTurnWithOptions(context.Background(), client, repo, root, nil, "read known.txt", nil, nil, RunOptions{}, func(ev TurnEvent) {
				if ev.Kind == "tool_result" {
					toolCalls++
				}
				if ev.Kind == "error" {
					t.Errorf("loop failed: %s", ev.Text)
				}
				if ev.Kind == "done" {
					completed = true
				}
				if ev.Kind == "tool_start" {
					if err := composer.SetVariant(p, nil, model.VariantConfig{}, ""); err != nil {
						t.Error(err)
					}
				}
			})
			if !completed || toolCalls != 1 || len(bodies) != 2 {
				t.Fatalf("completed=%v, tool results=%d, POSTs=%d", completed, toolCalls, len(bodies))
			}
			if !strings.Contains(bodies[1], tc.replay) || !strings.Contains(bodies[1], "42") {
				t.Fatalf("missing replay or actual tool output in follow-up: %s", bodies[1])
			}
			if client.Variant() != tc.variant || composer.Variant() != "" {
				t.Fatal("active run followed composer change")
			}
		})
	}
}
