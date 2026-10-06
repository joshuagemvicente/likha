package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// sentMessage is the replay-relevant part of one request message as the
// server decoded it.
type sentMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent *string         `json:"reasoning_content"`
	ReasoningDetails json.RawMessage `json:"reasoning_details"`
	ToolCalls        []struct {
		ID           string `json:"id"`
		ExtraContent *struct {
			Google struct {
				ThoughtSignature string `json:"thought_signature"`
			} `json:"google"`
		} `json:"extra_content"`
	} `json:"tool_calls"`
}

// signatures lists each call's sent thought signature, "" for a call sent
// without extra_content.
func (m sentMessage) signatures() []string {
	var out []string
	for _, call := range m.ToolCalls {
		signature := ""
		if call.ExtraContent != nil {
			signature = call.ExtraContent.Google.ThoughtSignature
		}
		out = append(out, signature)
	}
	return out
}

// recordingServer serves respond and records every request body.
type recordingServer struct {
	mu     sync.Mutex
	bodies []string
}

func (s *recordingServer) body(t *testing.T, n int) (string, []sentMessage) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) <= n {
		t.Fatalf("server saw %d requests, want more than %d", len(s.bodies), n)
	}
	var request struct {
		Messages []sentMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(s.bodies[n]), &request); err != nil {
		t.Fatalf("decode request %d: %v", n, err)
	}
	return s.bodies[n], request.Messages
}

func recordingClient(t *testing.T, wire ReplayWire, respond func(n int, w http.ResponseWriter)) (*Client, *recordingServer) {
	t.Helper()
	server := &recordingServer{}
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		server.mu.Lock()
		server.bodies = append(server.bodies, string(data))
		n := len(server.bodies)
		server.mu.Unlock()
		respond(n, w)
	})
	client.setReplayWire(wire)
	return client, server
}

func okResponse(_ int, w http.ResponseWriter) {
	sendEvents(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
}

// sendHistory builds a history from issuers around the client: its own
// model, another model on its endpoint, and another endpoint, in a previous
// turn and in the current one.
func sendHistory(t *testing.T, own ReplaySource) []Message {
	t.Helper()
	otherModel := ReplaySource{Endpoint: own.Endpoint, Model: "other-model"}
	otherEndpoint := ReplaySource{Endpoint: "https://elsewhere.example/v1/chat/completions", Model: own.Model}
	full := func(source ReplaySource, text string) *ReasoningReplay {
		replay, err := NewReasoningReplay(source, strPtr(text), json.RawMessage(`[{"type":"reasoning.text","text":"`+text+`"}]`), nil)
		if err != nil {
			t.Fatal(err)
		}
		return replay
	}
	assistant := func(id string, replay *ReasoningReplay, signatures ...string) []Message {
		message := Message{Role: "assistant", Reasoning: "display " + id, Replay: replay}
		var results []Message
		for i, signature := range signatures {
			callID := id + "-" + strconv.Itoa(i)
			message.ToolCalls = append(message.ToolCalls, ToolCall{ID: callID, Name: "read", Arguments: `{}`, ThoughtSignature: signature})
			results = append(results, Message{Role: "tool", ToolCallID: callID, Content: "ok"})
		}
		return append([]Message{message}, results...)
	}
	var history []Message
	history = append(history, Message{Role: "user", Content: "first"})
	history = append(history, assistant("own-prev", full(own, "own"), "sig-own")...)
	history = append(history, assistant("model-prev", full(otherModel, "model"), "sig-model")...)
	history = append(history, assistant("endpoint-prev", full(otherEndpoint, "endpoint"), "sig-endpoint")...)
	history = append(history, Message{Role: "user", Content: "second"})
	history = append(history, assistant("own-unsigned", &ReasoningReplay{Source: own}, "")...)
	history = append(history, assistant("model-now", full(otherModel, "model-now"), "sig-a", "")...)
	history = append(history, assistant("legacy-now", nil, "", "")...)
	return history
}

// wantSent is what one assistant message should carry on the wire.
type wantSent struct {
	reasoningContent *string
	details          string
	signatures       []string
}

func TestRequestSendsReplayOnlyToItsIssuer(t *testing.T) {
	cases := []struct {
		name string
		wire ReplayWire
		want []wantSent // per assistant message of sendHistory, in order
	}{
		{
			"DeepSeek reasoning text goes to its endpoint, any model",
			ReplayReasoningContent,
			[]wantSent{
				{reasoningContent: strPtr("own"), signatures: []string{""}},
				{reasoningContent: strPtr("model"), signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{""}},
				{reasoningContent: strPtr("model-now"), signatures: []string{"", ""}},
				{signatures: []string{"", ""}},
			},
		},
		{
			"OpenRouter details go to their model only",
			ReplayReasoningDetails,
			[]wantSent{
				{details: `[{"type":"reasoning.text","text":"own"}]`, signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{"", ""}},
				{signatures: []string{"", ""}},
			},
		},
		{
			"Gemini signatures go to their model; foreign current-turn calls get the skip value",
			ReplayThoughtSignature,
			[]wantSent{
				{signatures: []string{"sig-own"}},
				{signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{skipThoughtSignature, ""}},
				{signatures: []string{skipThoughtSignature, ""}},
			},
		},
		{
			"no replay wire sends nothing",
			ReplayNone,
			[]wantSent{
				{signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{""}},
				{signatures: []string{"", ""}},
				{signatures: []string{"", ""}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := recordingClient(t, tc.wire, okResponse)
			if _, err := client.Stream(context.Background(), sendHistory(t, client.ReplaySource()), testTools, nil, nil); err != nil {
				t.Fatalf("Stream: %v", err)
			}
			raw, messages := server.body(t, 0)
			if strings.Contains(raw, "display ") {
				t.Fatalf("display reasoning reached the provider: %s", raw)
			}
			var assistants []sentMessage
			for _, m := range messages {
				if m.Role == "assistant" {
					assistants = append(assistants, m)
				}
			}
			if len(assistants) != len(tc.want) {
				t.Fatalf("assistant messages = %d, want %d", len(assistants), len(tc.want))
			}
			for i, want := range tc.want {
				assertSent(t, i, assistants[i], want)
			}
		})
	}
}

var testTools = []ToolDefinition{{Name: "read", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}

func assertSent(t *testing.T, index int, got sentMessage, want wantSent) {
	t.Helper()
	if (got.ReasoningContent == nil) != (want.reasoningContent == nil) ||
		(got.ReasoningContent != nil && *got.ReasoningContent != *want.reasoningContent) {
		t.Errorf("assistant %d reasoning_content = %v, want %v", index, got.ReasoningContent, want.reasoningContent)
	}
	if string(got.ReasoningDetails) != want.details {
		t.Errorf("assistant %d reasoning_details = %s, want %q", index, got.ReasoningDetails, want.details)
	}
	if strings.Join(got.signatures(), ",") != strings.Join(want.signatures, ",") {
		t.Errorf("assistant %d signatures = %q, want %q", index, got.signatures(), want.signatures)
	}
}

// An empty captured reasoning_content goes back as "", not as an absent
// field: DeepSeek sent the field, so it expects it.
func TestRequestSendsEmptyReasoningContent(t *testing.T) {
	client, server := recordingClient(t, ReplayReasoningContent, okResponse)
	replay, err := NewReasoningReplay(client.ReplaySource(), strPtr(""), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history := []Message{{Role: "user", Content: "go"}, {Role: "assistant", Content: "hi", Replay: replay}, {Role: "user", Content: "more"}}
	if _, err := client.Stream(context.Background(), history, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := server.body(t, 0)
	if !strings.Contains(raw, `{"role":"assistant","content":"hi","reasoning_content":""}`) {
		t.Fatalf("body = %s, want an empty reasoning_content", raw)
	}
}

// A DeepSeek tool loop: the second request carries the first response's
// reasoning_content on that assistant message, even after a switch to the
// endpoint's other model, and the text appears nowhere else.
func TestDeepSeekLoopReplaysReasoningContent(t *testing.T) {
	client, server := recordingClient(t, ReplayReasoningContent, func(n int, w http.ResponseWriter) {
		if n == 1 {
			sendEvents(w,
				`{"choices":[{"delta":{"reasoning_content":"need the file"}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
				`[DONE]`)
			return
		}
		okResponse(n, w)
	})
	history := []Message{{Role: "user", Content: "go"}}
	first, err := client.Stream(context.Background(), history, testTools, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, first, Message{Role: "tool", ToolCallID: "c1", Content: "data"})
	for i, modelID := range []string{"local-model", "deepseek-v4-pro"} {
		if err := client.SetModel(modelID); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Stream(context.Background(), history, testTools, nil, nil); err != nil {
			t.Fatal(err)
		}
		raw, messages := server.body(t, i+1)
		if got := messages[1].ReasoningContent; got == nil || *got != "need the file" {
			t.Fatalf("%s: assistant reasoning_content = %v, want the first response's", modelID, got)
		}
		if strings.Count(raw, "need the file") != 1 {
			t.Fatalf("%s: reasoning appears %d times in %s, want once", modelID, strings.Count(raw, "need the file"), raw)
		}
	}
}

// A Gemini tool loop: each call's signature goes back at
// tool_calls[].extra_content.google.thought_signature; a parallel call that
// came unsigned goes back without extra_content.
func TestGeminiLoopReplaysThoughtSignatures(t *testing.T) {
	client, server := recordingClient(t, ReplayThoughtSignature, func(n int, w http.ResponseWriter) {
		if n == 1 {
			sendEvents(w,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"},"extra_content":{"google":{"thought_signature":"opaque=="}}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
				`[DONE]`)
			return
		}
		okResponse(n, w)
	})
	history := []Message{{Role: "user", Content: "go"}}
	first, err := client.Stream(context.Background(), history, testTools, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, first,
		Message{Role: "tool", ToolCallID: "c1", Content: "a"},
		Message{Role: "tool", ToolCallID: "c2", Content: "b"})
	if _, err := client.Stream(context.Background(), history, testTools, nil, nil); err != nil {
		t.Fatal(err)
	}
	raw, messages := server.body(t, 1)
	if got := strings.Join(messages[1].signatures(), ","); got != "opaque==," {
		t.Fatalf("signatures = %q, want the first call's only", got)
	}
	if strings.Count(raw, "extra_content") != 1 {
		t.Fatalf("body = %s, want extra_content on the signed call only", raw)
	}
}

// A 400 names replay as a possible cause only when the request lacked
// required state or carried some, and never on a provider without replay.
func TestReplayRejectedError(t *testing.T) {
	own := func(client *Client) []Message {
		replay, err := NewReasoningReplay(client.ReplaySource(), strPtr("mine"), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return []Message{{Role: "user", Content: "go"}, {Role: "assistant", Content: "a", Replay: replay}, {Role: "user", Content: "more"}}
	}
	ownThinking := func(client *Client) []Message {
		replay, err := NewReasoningReplay(client.ReplaySource(), nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		replay.Thinking = strPtr("trace")
		return []Message{{Role: "user", Content: "go"}, {Role: "assistant", Content: "a", Replay: replay}, {Role: "user", Content: "more"}}
	}
	foreignToolTurn := func(*Client) []Message {
		return []Message{
			{Role: "user", Content: "go"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}},
			{Role: "tool", ToolCallID: "c1", Content: "ok"},
		}
	}
	cases := []struct {
		name        string
		wire        ReplayWire
		status      int
		history     func(*Client) []Message
		tools       []ToolDefinition
		effort      string
		wantMissing int
		wantSent    int
		wantReplay  bool
		wantEffort  bool
	}{
		{"DeepSeek turn from another issuer", ReplayReasoningContent, http.StatusBadRequest, foreignToolTurn, testTools, "", 1, 0, true, false},
		{"own replay sent", ReplayReasoningContent, http.StatusBadRequest, own, testTools, "", 0, 1, true, false},
		{"Mistral thinking chunk sent", ReplayThinkingChunk, http.StatusBadRequest, ownThinking, nil, "", 0, 1, true, false},
		{"Mistral reply without thinking sends nothing", ReplayThinkingChunk, http.StatusBadRequest, own, nil, "", 0, 0, false, false},
		{"Gemini skip value", ReplayThoughtSignature, http.StatusBadRequest, foreignToolTurn, testTools, "", 1, 0, true, false},
		{"effort and replay", ReplayReasoningContent, http.StatusBadRequest, own, testTools, "high", 0, 1, true, true},
		{"provider without replay", ReplayNone, http.StatusBadRequest, own, testTools, "", 0, 0, false, false},
		{"DeepSeek without tools needs no state", ReplayReasoningContent, http.StatusBadRequest, foreignToolTurn, nil, "", 0, 0, false, false},
		{"Gemini previous-turn call needs no state", ReplayThoughtSignature, http.StatusBadRequest, func(c *Client) []Message {
			return append(foreignToolTurn(c), Message{Role: "user", Content: "next"})
		}, testTools, "", 0, 0, false, false},
		{"not a 400", ReplayReasoningContent, http.StatusUnprocessableEntity, own, testTools, "", 0, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := recordingClient(t, tc.wire, func(_ int, w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				io.WriteString(w, `{"error":{"message":"bad request"}}`)
			})
			client.SetEffort(EffortTopLevel, tc.effort)
			_, err := client.Stream(context.Background(), tc.history(client), tc.tools, nil, nil)
			if err == nil {
				t.Fatal("Stream succeeded, want the provider's rejection")
			}
			if !strings.Contains(err.Error(), "bad request") {
				t.Fatalf("error = %v, want the provider's message", err)
			}
			var rejected *ReplayRejectedError
			if got := errors.As(err, &rejected); got != tc.wantReplay {
				t.Fatalf("errors.As(ReplayRejectedError) = %v, want %v (%v)", got, tc.wantReplay, err)
			}
			if tc.wantReplay && (rejected.Missing != tc.wantMissing || rejected.Sent != tc.wantSent) {
				t.Fatalf("rejected = missing %d sent %d, want %d and %d", rejected.Missing, rejected.Sent, tc.wantMissing, tc.wantSent)
			}
			var effort *EffortRejectedError
			if got := errors.As(err, &effort); got != tc.wantEffort {
				t.Fatalf("errors.As(EffortRejectedError) = %v, want %v", got, tc.wantEffort)
			}
		})
	}
}

func TestPartialAssistantKeepsOnlyUnsignedReasoning(t *testing.T) {
	client := localClient(t, func(http.ResponseWriter, *http.Request) {})
	cases := []struct {
		name   string
		client *Client
		wire   ReplayWire
		want   *ReasoningReplay
	}{
		{"DeepSeek keeps the reasoning text", client, ReplayReasoningContent, &ReasoningReplay{Source: client.ReplaySource(), ReasoningContent: strPtr("half a thou")}},
		{"Gemini keeps nothing signed", client, ReplayThoughtSignature, nil},
		{"OpenRouter keeps nothing signed", client, ReplayReasoningDetails, nil},
		{"Mistral keeps no thinking", client, ReplayThinkingChunk, nil},
		{"no replay", client, ReplayNone, nil},
		{"nil client", nil, ReplayNone, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.client != nil {
				tc.client.setReplayWire(tc.wire)
			}
			got := tc.client.PartialAssistant("partial", "half a thou")
			if got.Role != "assistant" || got.Content != "partial" || got.Reasoning != "" || !got.Replay.Equal(tc.want) {
				t.Fatalf("PartialAssistant = %+v (replay %+v), want text with replay %+v", got, got.Replay, tc.want)
			}
		})
	}
}

func TestShippedProviderReplayWires(t *testing.T) {
	want := map[string]ReplayWire{"deepseek": ReplayReasoningContent, "gemini": ReplayThoughtSignature}
	for _, p := range Providers {
		if p.Replay != want[p.Name] {
			t.Errorf("%s replay = %d, want %d", p.Name, p.Replay, want[p.Name])
		}
	}
}
