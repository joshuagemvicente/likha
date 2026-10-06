package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureBody returns a client whose server records each raw request body
// and answers with a minimal successful stream.
func captureBody(t *testing.T, bodies *[]string) *Client {
	t.Helper()
	return localClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		*bodies = append(*bodies, string(raw))
		sendEvents(w, `{"choices":[{"delta":{"content":"ok"}}]}`, "[DONE]")
	})
}

func TestStreamEffortField(t *testing.T) {
	// The exact pre-feature body: Default must reproduce it byte for byte.
	const baseline = `{"model":"local-model","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`
	cases := []struct {
		name  string
		wire  EffortWire
		level string
		want  string
	}{
		{"default sends nothing", EffortTopLevel, "", baseline},
		{"unsupported provider sends nothing", EffortUnsupported, "high", baseline},
		{"top-level field", EffortTopLevel, "high", strings.TrimSuffix(baseline, "}") + `,"reasoning_effort":"high"}`},
		{"reasoning object", EffortObject, "xhigh", strings.TrimSuffix(baseline, "}") + `,"reasoning":{"effort":"xhigh"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []string
			client := captureBody(t, &bodies)
			client.SetEffort(tc.wire, tc.level)
			if _, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 1 || bodies[0] != tc.want {
				t.Fatalf("body = %q\nwant  %q", bodies, tc.want)
			}
		})
	}
}

func TestForkInheritsEffortAndWithoutEffortClearsIt(t *testing.T) {
	var bodies []string
	parent := captureBody(t, &bodies)

	same, err := parent.WithoutEffort()
	if err != nil || same != parent {
		t.Fatalf("WithoutEffort with no effort = %p, %v; want the parent itself", same, err)
	}

	parent.SetEffort(EffortTopLevel, "high")
	child, err := parent.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if child.Effort() != "high" {
		t.Fatalf("fork effort = %q, want high", child.Effort())
	}
	background, err := parent.WithoutEffort()
	if err != nil {
		t.Fatal(err)
	}
	if background == parent || background.Effort() != "" || parent.Effort() != "high" {
		t.Fatalf("WithoutEffort must return a separate client with no effort; got %q, parent %q", background.Effort(), parent.Effort())
	}
	if _, err := background.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bodies[0], "reasoning") {
		t.Fatalf("background request carried effort: %s", bodies[0])
	}
}

func TestRejectedRequestWithEffortIsTyped(t *testing.T) {
	cases := []struct {
		name   string
		status int
		level  string
		typed  bool
	}{
		{"400 with effort", http.StatusBadRequest, "max", true},
		{"422 with effort", http.StatusUnprocessableEntity, "max", true},
		{"400 without effort", http.StatusBadRequest, "", false},
		{"500 with effort", http.StatusInternalServerError, "max", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":{"message":"Unsupported value: 'reasoning_effort'"}}`, tc.status)
			})
			client.SetEffort(EffortTopLevel, tc.level)
			_, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
			if err == nil {
				t.Fatal("rejected request succeeded")
			}
			var rejected *EffortRejectedError
			if got := errors.As(err, &rejected); got != tc.typed {
				t.Fatalf("errors.As EffortRejectedError = %v, want %v (err %v)", got, tc.typed, err)
			}
			if tc.typed && (rejected.Level != tc.level || !strings.Contains(err.Error(), "Unsupported value")) {
				t.Fatalf("rejection = level %q, %v", rejected.Level, err)
			}
		})
	}
}

func TestBuildCodexRequestEffort(t *testing.T) {
	messages := []Message{{Role: "user", Content: "hi"}}
	without, err := BuildCodexRequest("gpt-6.1-sol", "", messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), `"reasoning"`) {
		t.Fatalf("empty effort sent a reasoning field: %s", without)
	}
	with, err := BuildCodexRequest("gpt-6.1-sol", "high", messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(with, &body); err != nil {
		t.Fatal(err)
	}
	if body.Reasoning.Effort != "high" {
		t.Fatalf("reasoning effort = %q in %s", body.Reasoning.Effort, with)
	}
}

func TestEffortLevels(t *testing.T) {
	supported := Provider{Name: "openai", DisplayName: "OpenAI", Effort: EffortTopLevel}
	unsupported := Provider{Name: "openai", DisplayName: "OpenAI"}
	cases := []struct {
		name     string
		provider Provider
		model    string
		live     []string
		want     string
	}{
		{"catalog levels", supported, "gpt-5.5", nil, "none,low,medium,high,xhigh"},
		{"live list wins", supported, "gpt-5.5", []string{"low", "high"}, "low,high"},
		{"unknown model", supported, "gpt-unknown", nil, ""},
		{"no prefix matching", supported, "gpt-5.5-unlisted", nil, ""},
		{"unsupported provider", unsupported, "gpt-5.5", []string{"low"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(EffortLevels(tc.provider, tc.model, tc.live), ","); got != tc.want {
				t.Fatalf("EffortLevels = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffortUnavailableReason(t *testing.T) {
	cases := []struct {
		name     string
		provider Provider
		want     string
	}{
		{"supported", Provider{DisplayName: "OpenAI", Effort: EffortTopLevel}, ""},
		{"unprobed", Provider{DisplayName: "Groq"}, "not yet verified for Groq"},
		{"custom endpoint", Provider{}, "not yet verified for this endpoint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffortUnavailableReason(tc.provider); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
	claude, ok := LookupProvider("claude")
	if !ok || EffortUnavailableReason(claude) != "Anthropic's OpenAI-compatible endpoint ignores effort" {
		t.Fatalf("claude reason = %q", EffortUnavailableReason(claude))
	}
}

func TestDecodeModelDetailsReadsLiveEfforts(t *testing.T) {
	raw := `{"data": [
	  {"id": "openai/gpt-6.1-sol", "context_length": 1050000,
	   "reasoning": {"mandatory": false, "supported_efforts": ["max", "xhigh", "high", "medium", "low"], "default_effort": "medium"}},
	  {"id": "anthropic/claude-sonnet-4.5", "reasoning": {"mandatory": false}},
	  {"id": "bad/levels", "reasoning": {"supported_efforts": ["High", "low"]}},
	  {"id": "odd/default", "reasoning": {"supported_efforts": ["high", "low"], "default_effort": "turbo"}},
	  {"id": "flag/only", "reasoning": true},
	  {"id": "plain/model"}
	]}`
	details, err := decodeModelDetails([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range details {
		got[d.ID] = strings.Join(d.Efforts, ",") + "|" + d.DefaultEffort
	}
	want := map[string]string{
		"openai/gpt-6.1-sol":          "low,medium,high,xhigh,max|medium",
		"anthropic/claude-sonnet-4.5": "|",
		"bad/levels":                  "|",
		"odd/default":                 "low,high|",
		"flag/only":                   "|",
		"plain/model":                 "|",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}
	if details[0].ContextWindow != 1050000 {
		t.Fatalf("context window lost: %+v", details[0])
	}
}

func TestOAuthStreamCarriesAndReportsEffort(t *testing.T) {
	var efforts []string
	reject := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reasoning *struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode responses request: %v", err)
		}
		effort := ""
		if body.Reasoning != nil {
			effort = body.Reasoning.Effort
		}
		efforts = append(efforts, effort)
		if reject {
			http.Error(w, `{"error":{"message":"Unsupported value: 'reasoning.effort'"}}`, http.StatusBadRequest)
			return
		}
		codexSSE(w, "ok")
	}))
	defer server.Close()
	client := newOAuthTestClient(t, server.URL, server.URL+"/v1", validOAuthCredentials())
	messages := []Message{{Role: "user", Content: "hi"}}
	tools := []ToolDefinition{{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}

	if _, err := client.Stream(context.Background(), messages, tools, nil, nil); err != nil {
		t.Fatal(err)
	}
	client.SetEffort(EffortObject, "high")
	if _, err := client.Stream(context.Background(), messages, tools, nil, nil); err != nil {
		t.Fatal(err)
	}
	reject = true
	_, err := client.Stream(context.Background(), messages, tools, nil, nil)
	var rejected *EffortRejectedError
	if !errors.As(err, &rejected) || rejected.Level != "high" {
		t.Fatalf("rejected Responses request = %v, want EffortRejectedError at high", err)
	}
	if strings.Join(efforts, ",") != ",high,high" {
		t.Fatalf("efforts on the wire = %q", efforts)
	}
}
