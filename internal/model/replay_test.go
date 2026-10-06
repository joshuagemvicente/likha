package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ToolCall must stay comparable: sessions and task records compare calls with
// == and slices.Equal. This line stops compiling if a non-comparable field is
// added.
var _ = ToolCall{} == ToolCall{}

func strPtr(s string) *string { return &s }

var testSource = ReplaySource{Endpoint: "https://api.deepseek.com/v1/chat/completions", Model: "deepseek-flash"}

func TestNewReasoningReplayCanonicalizesRawJSON(t *testing.T) {
	cases := []struct {
		name        string
		details     string
		blocks      string
		wantDetails string
		wantBlocks  string
	}{
		{"absent parts", "", "", "", ""},
		{"null means absent", "null", " null\n", "", ""},
		{"whitespace means absent", "  ", "\t", "", ""},
		{"compacted", "[ {\"type\" : \"reasoning.text\", \"text\": \"a b\"} ]", "[{ \"type\":\"thinking\" }]", `[{"type":"reasoning.text","text":"a b"}]`, `[{"type":"thinking"}]`},
		{"html characters escaped as a saved session holds them", `[{"text":"<a&b>"}]`, `[{"thinking":"x<y"}]`, `[{"text":"\u003ca\u0026b\u003e"}]`, `[{"thinking":"x\u003cy"}]`},
		{"already canonical kept", `[{"text":"\u003c"}]`, "", `[{"text":"\u003c"}]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay, err := NewReasoningReplay(testSource, nil, json.RawMessage(tc.details), json.RawMessage(tc.blocks))
			if err != nil {
				t.Fatalf("NewReasoningReplay: %v", err)
			}
			if got := string(replay.Details); got != tc.wantDetails {
				t.Errorf("Details = %q, want %q", got, tc.wantDetails)
			}
			if got := string(replay.Blocks); got != tc.wantBlocks {
				t.Errorf("Blocks = %q, want %q", got, tc.wantBlocks)
			}
			if tc.wantDetails == "" && replay.Details != nil {
				t.Errorf("Details = %#v, want nil", replay.Details)
			}
			if tc.wantBlocks == "" && replay.Blocks != nil {
				t.Errorf("Blocks = %#v, want nil", replay.Blocks)
			}
		})
	}
}

func TestNewReasoningReplayRejects(t *testing.T) {
	cases := []struct {
		name    string
		source  ReplaySource
		details string
		blocks  string
	}{
		{"missing endpoint", ReplaySource{Model: "m"}, "", ""},
		{"missing model", ReplaySource{Endpoint: "https://x/v1/chat/completions"}, "", ""},
		{"invalid details", testSource, "[{", ""},
		{"invalid blocks", testSource, "", "{\"a\":}"},
		{"trailing garbage", testSource, "[] []", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay, err := NewReasoningReplay(tc.source, nil, json.RawMessage(tc.details), json.RawMessage(tc.blocks))
			if err == nil || replay != nil {
				t.Fatalf("NewReasoningReplay = %+v, %v; want nil and an error", replay, err)
			}
		})
	}
}

func TestNewReasoningReplayCopiesReasoningContent(t *testing.T) {
	text := "thinking"
	replay, err := NewReasoningReplay(testSource, &text, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	text = "changed"
	if replay.ReasoningContent == nil || *replay.ReasoningContent != "thinking" {
		t.Fatalf("ReasoningContent = %v, want a private copy of %q", replay.ReasoningContent, "thinking")
	}
	empty, err := NewReasoningReplay(testSource, strPtr(""), nil, nil)
	if err != nil || empty.ReasoningContent == nil || *empty.ReasoningContent != "" {
		t.Fatalf("empty reasoning content = %+v, %v; want a pointer to \"\"", empty, err)
	}
}

func TestReasoningReplayEqual(t *testing.T) {
	mustReplay := func(content *string, details string) *ReasoningReplay {
		t.Helper()
		replay, err := NewReasoningReplay(testSource, content, json.RawMessage(details), nil)
		if err != nil {
			t.Fatal(err)
		}
		return replay
	}
	base := mustReplay(strPtr("r"), `[{"text":"<x>"}]`)
	otherModel := *base
	otherModel.Source.Model = "deepseek-v4-pro"
	cases := []struct {
		name string
		a, b *ReasoningReplay
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil and value", nil, base, false},
		{"value and nil", base, nil, false},
		{"issuer only and nil", &ReasoningReplay{Source: testSource}, nil, false},
		{"same pointer", base, base, true},
		{"equal copies", base, mustReplay(strPtr("r"), `[ {"text": "<x>"} ]`), true},
		{"other model", base, &otherModel, false},
		{"absent and empty content", mustReplay(nil, ""), mustReplay(strPtr(""), ""), false},
		{"different content", mustReplay(strPtr("a"), ""), mustReplay(strPtr("b"), ""), false},
		{"different details", mustReplay(nil, `[1]`), mustReplay(nil, `[2]`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equal(tc.b); got != tc.want {
				t.Fatalf("Equal = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMessageJSONWithoutReplayIsUnchanged(t *testing.T) {
	message := Message{Role: "assistant", Content: "hi", Reasoning: "r", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	const golden = `{"Role":"assistant","Content":"hi","Reasoning":"r","ToolCalls":[{"ID":"c1","Name":"read","Arguments":"{}"}],"ToolCallID":""}`
	if string(data) != golden {
		t.Fatalf("encoding = %s, want %s", data, golden)
	}
}

func TestMessageJSONRoundTripsReplay(t *testing.T) {
	replay, err := NewReasoningReplay(testSource, strPtr(""), json.RawMessage(`[{"text":"<a>"}]`), json.RawMessage(`[{"type":"thinking","signature":"s&"}]`))
	if err != nil {
		t.Fatal(err)
	}
	message := Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Name: "n", Arguments: `{}`, ThoughtSignature: "sig"}}, Replay: replay}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Replay.Equal(message.Replay) || decoded.ToolCalls[0] != message.ToolCalls[0] {
		t.Fatalf("round trip = %+v, want %+v", decoded, message)
	}
	if decoded.Replay.ReasoningContent == nil || *decoded.Replay.ReasoningContent != "" {
		t.Fatal("empty reasoning content did not survive as a pointer to \"\"")
	}

	var legacy Message
	if err := json.Unmarshal([]byte(`{"Role":"assistant","Content":"x","Reasoning":"","ToolCalls":[{"ID":"c","Name":"n","Arguments":"{}"}],"ToolCallID":""}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Replay != nil || legacy.ToolCalls[0].ThoughtSignature != "" {
		t.Fatalf("legacy message decoded with replay state: %+v", legacy)
	}
}

func TestReplayWireModelBound(t *testing.T) {
	cases := []struct {
		wire ReplayWire
		want bool
	}{
		{ReplayReasoningContent, false},
		{ReplayThoughtSignature, true},
		{ReplayReasoningDetails, true},
		{ReplayAnthropicBlocks, true},
	}
	for _, tc := range cases {
		if got := tc.wire.modelBound(); got != tc.want {
			t.Errorf("ReplayWire(%d).modelBound() = %v, want %v", tc.wire, got, tc.want)
		}
	}
}

func TestReplayForMatchesIssuer(t *testing.T) {
	client, err := New("https://api.deepseek.com/v1", "deepseek-flash", "")
	if err != nil {
		t.Fatal(err)
	}
	source := client.ReplaySource()
	if source.Endpoint != "https://api.deepseek.com/v1/chat/completions" || source.Model != "deepseek-flash" {
		t.Fatalf("ReplaySource = %+v, want the request URL and model", source)
	}
	own := &ReasoningReplay{Source: source}
	otherModel := &ReasoningReplay{Source: ReplaySource{Endpoint: source.Endpoint, Model: "deepseek-v4-pro"}}
	otherEndpoint := &ReasoningReplay{Source: ReplaySource{Endpoint: "https://api.deepseek.com/v1/messages", Model: source.Model}}
	cases := []struct {
		name   string
		wire   ReplayWire
		role   string
		replay *ReasoningReplay
		want   *ReasoningReplay
	}{
		{"none sends nothing", ReplayNone, "assistant", own, nil},
		{"own issuer", ReplayThoughtSignature, "assistant", own, own},
		{"no replay", ReplayThoughtSignature, "assistant", nil, nil},
		{"not an assistant", ReplayThoughtSignature, "tool", own, nil},
		{"signed state from another model", ReplayThoughtSignature, "assistant", otherModel, nil},
		{"details from another model", ReplayReasoningDetails, "assistant", otherModel, nil},
		{"reasoning text from another model on the endpoint", ReplayReasoningContent, "assistant", otherModel, otherModel},
		{"reasoning text from another endpoint", ReplayReasoningContent, "assistant", otherEndpoint, nil},
		{"signed state from another endpoint", ReplayThoughtSignature, "assistant", otherEndpoint, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client.setReplayWire(tc.wire)
			if got := client.replayFor(Message{Role: tc.role, Replay: tc.replay}); got != tc.want {
				t.Fatalf("replayFor = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReplayWireResolvedFromProviderRow(t *testing.T) {
	rows := []Provider{
		{Name: "a", BaseURL: "https://a.example/v1", Replay: ReplayReasoningContent},
		{Name: "b", BaseURL: "https://b.example/v1"},
	}
	cases := []struct {
		name string
		base string
		want ReplayWire
	}{
		{"row with replay", "https://a.example/v1", ReplayReasoningContent},
		{"row without replay", "https://b.example/v1", ReplayNone},
		{"custom endpoint", "https://custom.example/v1", ReplayNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := replayWireFor(tc.base, rows); got != tc.want {
				t.Fatalf("replayWireFor(%q) = %d, want %d", tc.base, got, tc.want)
			}
		})
	}

	for _, p := range Providers {
		if p.Auth == AuthOAuth {
			continue
		}
		client, err := New(p.BaseURL, "m", "")
		if err != nil {
			t.Fatalf("New(%s): %v", p.Name, err)
		}
		if client.replayWire != p.Replay {
			t.Errorf("%s client wire = %d, want row's %d", p.Name, client.replayWire, p.Replay)
		}
	}
	local, err := New("http://127.0.0.1:1/v1", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	if local.replayWire != ReplayNone {
		t.Errorf("custom endpoint wire = %d, want ReplayNone", local.replayWire)
	}
}

func TestProviderRowsSharingBaseShareReplay(t *testing.T) {
	byBase := make(map[string]Provider)
	for _, p := range Providers {
		if first, ok := byBase[p.BaseURL]; ok && first.Replay != p.Replay {
			t.Errorf("%s and %s share %s but carry replay %d and %d", first.Name, p.Name, p.BaseURL, first.Replay, p.Replay)
		}
		byBase[p.BaseURL] = p
	}
}

func TestForkCopiesReplayWire(t *testing.T) {
	parent := localClient(t, func(http.ResponseWriter, *http.Request) {})
	parent.setReplayWire(ReplayThoughtSignature)
	parent.SetEffort(EffortTopLevel, "high")
	fork, err := parent.Fork()
	if err != nil {
		t.Fatal(err)
	}
	task, err := parent.ForkForTask(func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	withoutEffort, err := parent.WithoutEffort()
	if err != nil {
		t.Fatal(err)
	}
	if withoutEffort == parent {
		t.Fatal("WithoutEffort returned the parent although effort was set")
	}
	for name, child := range map[string]*Client{"Fork": fork, "ForkForTask": task, "WithoutEffort": withoutEffort} {
		if child.replayWire != ReplayThoughtSignature {
			t.Errorf("%s wire = %d, want the parent's", name, child.replayWire)
		}
		if child.ReplaySource() != parent.ReplaySource() {
			t.Errorf("%s source = %+v, want %+v", name, child.ReplaySource(), parent.ReplaySource())
		}
	}
}

// Background requests fork a client without replay; the parent keeps its
// wire, and a client that has none is reused.
func TestWithoutReplay(t *testing.T) {
	parent, err := New("https://api.deepseek.com/v1", "deepseek-flash", "")
	if err != nil {
		t.Fatal(err)
	}
	background, err := parent.WithoutReplay()
	if err != nil {
		t.Fatal(err)
	}
	if background == parent || background.replayWire != ReplayNone {
		t.Fatalf("WithoutReplay() = same %v, wire %d; want a fork with ReplayNone", background == parent, background.replayWire)
	}
	if parent.replayWire != ReplayReasoningContent {
		t.Fatalf("parent wire = %d, want unchanged", parent.replayWire)
	}
	again, err := background.WithoutReplay()
	if err != nil || again != background {
		t.Fatalf("WithoutReplay() on a client without replay = %p, %v; want the same client", again, err)
	}
}

// replayHistory holds every replay form, issued both by the test client and by
// other issuers, so a body test covers matching and mismatched sources.
func replayHistory(t *testing.T, own ReplaySource) []Message {
	t.Helper()
	foreign := ReplaySource{Endpoint: "https://elsewhere.example/v1/chat/completions", Model: "other"}
	var history []Message
	for _, source := range []ReplaySource{own, foreign} {
		replay, err := NewReasoningReplay(source, strPtr("hidden <reasoning>"), json.RawMessage(`[{"type":"reasoning.text","text":"t"}]`), json.RawMessage(`[{"type":"thinking"}]`))
		if err != nil {
			t.Fatal(err)
		}
		history = append(history,
			Message{Role: "user", Content: "go"},
			Message{Role: "assistant", Reasoning: "display", Replay: replay, ToolCalls: []ToolCall{{ID: "c-" + source.Model, Name: "read", Arguments: `{}`, ThoughtSignature: "sig"}}},
			Message{Role: "tool", ToolCallID: "c-" + source.Model, Content: "ok"},
		)
	}
	return history
}

func TestRequestBodyUnchangedByReplayState(t *testing.T) {
	var bodies []string
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, string(data))
		sendEvents(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	})
	// The pre-feature encoding of replayHistory: no reasoning, no replay
	// fields, no extra_content on tool calls.
	const golden = `{"model":"local-model","messages":[` +
		`{"role":"user","content":"go"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"c-local-model","type":"function","function":{"name":"read","arguments":"{}"}}]},` +
		`{"role":"tool","content":"ok","tool_call_id":"c-local-model"},` +
		`{"role":"user","content":"go"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"c-other","type":"function","function":{"name":"read","arguments":"{}"}}]},` +
		`{"role":"tool","content":"ok","tool_call_id":"c-other"}],` +
		`"stream":true,"stream_options":{"include_usage":true}}`
	// A client without replay sends none, whatever replay state the history
	// holds and whoever issued it.
	if _, err := client.Stream(context.Background(), replayHistory(t, client.ReplaySource()), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || bodies[0] != golden {
		t.Fatalf("body =\n%s\nwant\n%s", strings.Join(bodies, "\n"), golden)
	}
}

func TestClientEstimateCountsOnlySentReplay(t *testing.T) {
	client, err := New("https://api.deepseek.com/v1", "deepseek-flash", "")
	if err != nil {
		t.Fatal(err)
	}
	history := replayHistory(t, client.ReplaySource())
	base, ok := EstimateInputTokens(client.Model(), history, nil)
	if !ok {
		t.Fatal("base estimate unavailable")
	}
	ownText, _ := estimateTextTokens("hidden <reasoning>")
	ownDetails, _ := estimateTextTokens(string(history[1].Replay.Details))
	ownSignature, _ := estimateTextTokens("sig")
	// The foreign call sits in the current turn, so Gemini gets the skip value.
	skipValue, _ := estimateTextTokens(skipThoughtSignature)
	cases := []struct {
		name   string
		client *Client
		wire   ReplayWire
		want   int64
	}{
		{"nil client", nil, ReplayNone, base},
		{"provider without replay", client, ReplayNone, base},
		{"reasoning text from own endpoint", client, ReplayReasoningContent, base + ownText},
		{"details from own model only", client, ReplayReasoningDetails, base + ownDetails},
		{"own signatures and the skip value", client, ReplayThoughtSignature, base + ownSignature + skipValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.client != nil {
				tc.client.setReplayWire(tc.wire)
			}
			got, ok := tc.client.EstimateInputTokens(history, nil)
			if !ok || got != tc.want {
				t.Fatalf("estimate = %d, %v; want %d", got, ok, tc.want)
			}
		})
	}
}

// The idle estimate is for the history a new prompt will follow: own
// signatures are still sent, but the prompt starts a new Gemini turn, so no
// call gets the skip value.
func TestClientHistoryEstimateCountsNoSkipValue(t *testing.T) {
	client, err := New("https://generativelanguage.googleapis.com/v1beta/openai", "local-model", "")
	if err != nil {
		t.Fatal(err)
	}
	history := replayHistory(t, client.ReplaySource())
	base, ok := EstimateInputTokens(client.Model(), history, nil)
	if !ok {
		t.Fatal("base estimate unavailable")
	}
	ownSignature, _ := estimateTextTokens("sig")
	skipValue, _ := estimateTextTokens(skipThoughtSignature)
	if got, ok := client.EstimateHistoryTokens(history); !ok || got != base+ownSignature {
		t.Fatalf("history estimate = %d, %v; want %d", got, ok, base+ownSignature)
	}
	if got, ok := client.EstimateInputTokens(history, nil); !ok || got != base+ownSignature+skipValue {
		t.Fatalf("request estimate = %d, %v; want %d", got, ok, base+ownSignature+skipValue)
	}
}

// UnsignedOnly keeps what stays valid after the message changes: unsigned
// reasoning text. Signed or encrypted parts go, and nothing left means nil,
// never a Source-only value that would claim the issuer sent nothing.
func TestReasoningReplayUnsignedOnly(t *testing.T) {
	details := json.RawMessage(`[{"type":"reasoning.encrypted","data":"x"}]`)
	blocks := json.RawMessage(`[{"type":"thinking","thinking":"t","signature":"s"}]`)
	cases := []struct {
		name             string
		reasoningContent *string
		details, blocks  json.RawMessage
		want             *string // nil: UnsignedOnly returns nil
	}{
		{"text only", strPtr("think"), nil, nil, strPtr("think")},
		{"empty text is still text", strPtr(""), nil, nil, strPtr("")},
		{"text with signed parts keeps the text", strPtr("think"), details, blocks, strPtr("think")},
		{"signed parts only", nil, details, blocks, nil},
		{"issuer sent nothing", nil, nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay := mustReplay(t, tc.reasoningContent, tc.details, tc.blocks)
			got := replay.UnsignedOnly()
			if tc.want == nil {
				if got != nil {
					t.Fatalf("UnsignedOnly() = %+v, want nil", got)
				}
				return
			}
			if want := mustReplay(t, tc.want, nil, nil); !got.Equal(want) {
				t.Fatalf("UnsignedOnly() = %+v, want %+v", got, want)
			}
			if !replay.Equal(mustReplay(t, tc.reasoningContent, tc.details, tc.blocks)) {
				t.Fatal("UnsignedOnly modified the receiver")
			}
		})
	}
	var absent *ReasoningReplay
	if absent.UnsignedOnly() != nil {
		t.Fatal("nil.UnsignedOnly() != nil")
	}
}

func mustReplay(t *testing.T, reasoningContent *string, details, blocks json.RawMessage) *ReasoningReplay {
	t.Helper()
	replay, err := NewReasoningReplay(testSource, reasoningContent, details, blocks)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}
