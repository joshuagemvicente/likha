package model

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// streamWithWire streams one response of events from a local server through
// a client resolved to wire.
func streamWithWire(t *testing.T, wire ReplayWire, events ...string) (Message, *Client, error) {
	t.Helper()
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		sendEvents(w, events...)
	})
	client.setReplayWire(wire)
	message, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "go"}}, nil, nil, nil)
	return message, client, err
}

// Every replay field a provider might send, on a stream whose extra_content
// and reasoning_details are malformed for their wires. A client without
// replay must decode it exactly as before this feature.
var everyReplayFieldEvents = []string{
	`{"choices":[{"delta":{"reasoning_content":"think","reasoning_details":{"not":"an array"}}}]}`,
	`{"choices":[{"delta":{"content":"hi","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"},"extra_content":"not an object"}]}}]}`,
	`[DONE]`,
}

func TestStreamWithoutReplayCapturesNothing(t *testing.T) {
	message, _, err := streamWithWire(t, ReplayNone, everyReplayFieldEvents...)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if message.Replay != nil || message.ToolCalls[0].ThoughtSignature != "" {
		t.Fatalf("message = %+v, want no replay state", message)
	}
	if message.Reasoning != "think" || message.Content != "hi" {
		t.Fatalf("display fields = %q / %q, want think / hi", message.Reasoning, message.Content)
	}
}

func TestStreamRecordsIssuerWithoutState(t *testing.T) {
	for _, wire := range []ReplayWire{ReplayReasoningContent, ReplayThoughtSignature, ReplayReasoningDetails} {
		message, client, err := streamWithWire(t, wire, `{"choices":[{"delta":{"content":"plain"}}]}`, `[DONE]`)
		if err != nil {
			t.Fatalf("wire %d: Stream: %v", wire, err)
		}
		want := &ReasoningReplay{Source: client.ReplaySource()}
		if !message.Replay.Equal(want) {
			t.Fatalf("wire %d: replay = %+v, want only the issuer %+v", wire, message.Replay, want)
		}
	}
}

func TestStreamCapturesDeepSeekReasoningContent(t *testing.T) {
	cases := []struct {
		name          string
		events        []string
		wantReplay    *string // nil: ReasoningContent absent
		wantReasoning string  // display reasoning
	}{
		{
			"pieces concatenate",
			[]string{
				`{"choices":[{"delta":{"reasoning_content":"plan "}}]}`,
				`{"choices":[{"delta":{"reasoning_content":"the read","content":null}}]}`,
				`{"choices":[{"delta":{"content":"","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`,
				`[DONE]`,
			},
			strPtr("plan the read"), "plan the read",
		},
		{
			"present but empty",
			[]string{`{"choices":[{"delta":{"reasoning_content":"","content":"ok"}}]}`, `[DONE]`},
			strPtr(""), "",
		},
		{
			"only display reasoning",
			[]string{`{"choices":[{"delta":{"reasoning":"shown","content":"ok"}}]}`, `[DONE]`},
			nil, "shown",
		},
		{
			"display prefers reasoning, replay keeps reasoning_content",
			[]string{`{"choices":[{"delta":{"reasoning":"shown","reasoning_content":"sent","content":"ok"}}]}`, `[DONE]`},
			strPtr("sent"), "shown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message, client, err := streamWithWire(t, ReplayReasoningContent, tc.events...)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			want, err := NewReasoningReplay(client.ReplaySource(), tc.wantReplay, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !message.Replay.Equal(want) {
				t.Fatalf("replay = %+v, want %+v", message.Replay, want)
			}
			if message.Reasoning != tc.wantReasoning {
				t.Fatalf("display reasoning = %q, want %q", message.Reasoning, tc.wantReasoning)
			}
		})
	}
}

func TestStreamCapturesGeminiThoughtSignatures(t *testing.T) {
	call := func(index int, id, extra string) string {
		fragment := `{"index":` + strconv.Itoa(index)
		if id != "" {
			fragment += `,"id":"` + id + `","type":"function","function":{"name":"read","arguments":"{}"}`
		}
		if extra != "" {
			fragment += `,"extra_content":` + extra
		}
		return `{"choices":[{"delta":{"tool_calls":[` + fragment + `}]}}]}`
	}
	signed := func(signature string) string { return `{"google":{"thought_signature":"` + signature + `"}}` }
	cases := []struct {
		name   string
		events []string
		want   []string
	}{
		{"single signed call", []string{call(0, "c1", signed("s1"))}, []string{"s1"}},
		{"parallel calls, first signed", []string{call(0, "c1", signed("s1")), call(1, "c2", "")}, []string{"s1", ""}},
		{"signature on a later fragment", []string{call(0, "c1", ""), call(0, "", signed("late"))}, []string{"late"}},
		{"repeated signature is not doubled", []string{call(0, "c1", signed("s1")), call(0, "", signed("s1"))}, []string{"s1"}},
		{"empty repeat keeps the signature", []string{call(0, "c1", signed("s1")), call(0, "", signed(""))}, []string{"s1"}},
		{"other extra_content carries no signature", []string{call(0, "c1", `{"vendor":{}}`)}, []string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message, client, err := streamWithWire(t, ReplayThoughtSignature, append(tc.events, `[DONE]`)...)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if len(message.ToolCalls) != len(tc.want) {
				t.Fatalf("calls = %+v, want %d", message.ToolCalls, len(tc.want))
			}
			for i, want := range tc.want {
				if got := message.ToolCalls[i].ThoughtSignature; got != want {
					t.Errorf("call %d signature = %q, want %q", i, got, want)
				}
			}
			if !message.Replay.Equal(&ReasoningReplay{Source: client.ReplaySource()}) {
				t.Fatalf("replay = %+v, want the issuer", message.Replay)
			}
		})
	}
}

func TestStreamRejectsMalformedReplayFieldsOnTheirWire(t *testing.T) {
	cases := []struct {
		name string
		wire ReplayWire
	}{
		{"Gemini extra_content", ReplayThoughtSignature},
		{"OpenRouter reasoning_details", ReplayReasoningDetails},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message, _, err := streamWithWire(t, tc.wire, everyReplayFieldEvents...)
			if err == nil || message.Replay != nil {
				t.Fatalf("Stream = %+v, %v; want an error and no replay", message, err)
			}
		})
	}
}

func TestStreamAssemblesOpenRouterReasoningDetails(t *testing.T) {
	message, client, err := streamWithWire(t, ReplayReasoningDetails,
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"id":"r1","format":"anthropic-claude-v1","text":"Let me "}]}}]}`,
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"text":"think","vendor_extra":{"k":1}}]}}]}`,
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"signature":"sig-1"}]}}]}`,
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.encrypted","index":1,"data":"opaque"}],"content":"done"}}]}`,
		`[DONE]`,
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	const want = `[{"format":"anthropic-claude-v1","id":"r1","index":0,"signature":"sig-1","text":"Let me think","type":"reasoning.text","vendor_extra":{"k":1}},` +
		`{"data":"opaque","index":1,"type":"reasoning.encrypted"}]`
	if message.Replay == nil || string(message.Replay.Details) != want {
		t.Fatalf("details = %+v, want %s", message.Replay, want)
	}
	if message.Replay.Source != client.ReplaySource() || message.Replay.ReasoningContent != nil {
		t.Fatalf("replay = %+v, want only the issuer and details", message.Replay)
	}
}

func TestAssembleReasoningDetails(t *testing.T) {
	cases := []struct {
		name     string
		elements []string
		want     string
		wantErr  bool
	}{
		{
			"conflicting ids stay apart",
			[]string{`{"type":"reasoning.text","index":0,"id":"a","text":"x"}`, `{"type":"reasoning.text","index":0,"id":"b","text":"y"}`},
			`[{"id":"a","index":0,"text":"x","type":"reasoning.text"},{"id":"b","index":0,"text":"y","type":"reasoning.text"}]`, false,
		},
		{
			"different types stay apart",
			[]string{`{"type":"reasoning.summary","index":0,"summary":"s"}`, `{"type":"reasoning.text","index":0,"text":"t"}`},
			`[{"index":0,"summary":"s","type":"reasoning.summary"},{"index":0,"text":"t","type":"reasoning.text"}]`, false,
		},
		{
			"elements without index kept as they are",
			[]string{`{"type":"reasoning.text", "text":"a"}`, `{"type":"reasoning.text", "text":"b"}`},
			`[{"type":"reasoning.text","text":"a"},{"type":"reasoning.text","text":"b"}]`, false,
		},
		{
			"non-objects kept as they are",
			[]string{`"loose"`, `{"type":"reasoning.text","index":0,"text":"a"}`},
			`["loose",{"index":0,"text":"a","type":"reasoning.text"}]`, false,
		},
		{
			"a fragment after an unindexed element starts a new object",
			[]string{`{"type":"t","index":0,"text":"a"}`, `{"type":"t","text":"x"}`, `{"type":"t","index":0,"text":"b"}`},
			`[{"index":0,"text":"a","type":"t"},{"type":"t","text":"x"},{"index":0,"text":"b","type":"t"}]`, false,
		},
		{
			"streamed text that is not a string",
			[]string{`{"type":"t","index":0,"text":"a"}`, `{"type":"t","index":0,"text":5}`},
			"", true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			elements := make([]json.RawMessage, 0, len(tc.elements))
			for _, element := range tc.elements {
				elements = append(elements, json.RawMessage(element))
			}
			got, err := assembleReasoningDetails(elements)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("assembleReasoningDetails = %s, want an error", got)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Fatalf("assembleReasoningDetails = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

// A failed, cancelled or incomplete stream keeps no replay state, on any
// wire, so a retried or abandoned attempt can never leave some behind.
func TestStreamFailureKeepsNoReplay(t *testing.T) {
	stateful := `{"choices":[{"delta":{"reasoning_content":"r","reasoning_details":[{"type":"t","index":0,"text":"x"}],` +
		`"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{}"},"extra_content":{"google":{"thought_signature":"s"}}}]}}]}`
	failures := []struct {
		name    string
		respond func(w http.ResponseWriter, r *http.Request)
		cancel  bool
	}{
		{"ends before DONE", func(w http.ResponseWriter, _ *http.Request) { sendEvents(w, stateful) }, false},
		{"stream error", func(w http.ResponseWriter, _ *http.Request) {
			sendEvents(w, stateful, `{"error":{"message":"overloaded"}}`)
		}, false},
		{"cancelled mid-stream", func(w http.ResponseWriter, r *http.Request) {
			sendEvents(w, stateful, `{"choices":[{"delta":{"content":"x"}}]}`)
			<-r.Context().Done()
		}, true},
	}
	wires := []ReplayWire{ReplayReasoningContent, ReplayThoughtSignature, ReplayReasoningDetails}
	for _, failure := range failures {
		for _, wire := range wires {
			client := localClient(t, failure.respond)
			client.setReplayWire(wire)
			ctx, cancel := context.WithCancel(context.Background())
			onText := func(string) {
				if failure.cancel {
					cancel()
				}
			}
			message, err := client.Stream(ctx, []Message{{Role: "user", Content: "go"}}, nil, onText, nil)
			cancel()
			if err == nil || message.Replay != nil || len(message.ToolCalls) != 0 {
				t.Fatalf("%s on wire %d: Stream = %+v, %v; want an error and no replay", failure.name, wire, message, err)
			}
		}
	}
}
