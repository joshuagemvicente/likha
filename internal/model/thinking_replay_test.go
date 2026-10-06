package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The mistral row keeps replay off until FR-39's rejection hint exists
// (specs/chunked-content T10): a replayed shape Mistral rejects would
// otherwise fail every later turn with a bare HTTP 400 and no way out named.
// The thinking-chunk wire is exercised through setReplayWire meanwhile.
func TestMistralRowKeepsReplayOff(t *testing.T) {
	cases := []struct {
		name string
		base string
		want ReplayWire
	}{
		{"mistral", "https://api.mistral.ai/v1", ReplayNone},
		{"relay passing chunks through", "https://openrouter.ai/api/v1", ReplayNone},
		{"custom endpoint", "http://127.0.0.1:8080/v1", ReplayNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := New(tc.base, "magistral-medium-latest", "")
			if err != nil {
				t.Fatal(err)
			}
			if client.replayWire != tc.want {
				t.Fatalf("wire = %d, want %d", client.replayWire, tc.want)
			}
		})
	}
	mistral, ok := LookupProvider("mistral")
	if !ok || mistral.Effort != EffortUnsupported {
		t.Fatalf("mistral row effort = %d, want the zero value until its probe passes", mistral.Effort)
	}
}

func TestCaptureMistralThinking(t *testing.T) {
	cases := []struct {
		name          string
		events        []string
		wantThinking  *string // nil: no thinking chunk recorded
		wantSignature string
		wantReasoning string
	}{
		{"documented phases", mistralPhases, strPtr("Let me check. Done."), "", "Let me check. Done."},
		{
			name: "last non-empty string signature kept, odd ones dropped",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"a"}],"signature":"sig-1"}]}}]}`,
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"b"}],"signature":7}]}}]}`,
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[],"signature":""},{"type":"text","text":"x"}]}}]}`,
				`[DONE]`,
			},
			wantThinking: strPtr("ab"), wantSignature: "sig-1", wantReasoning: "ab",
		},
		{
			name:         "numeric signature alone is no signature",
			events:       []string{`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"t"}],"signature":12},{"type":"text","text":"x"}]}}]}`, `[DONE]`},
			wantThinking: strPtr("t"), wantReasoning: "t",
		},
		{
			name: "thinking around answer text joins into one",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"first"}]}]}}]}`,
				`{"choices":[{"delta":{"content":"answer"}}]}`,
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":" second"}]}]}}]}`,
				`[DONE]`,
			},
			wantThinking: strPtr("first second"), wantReasoning: "first second",
		},
		{
			name:         "thinking chunk without text is still recorded",
			events:       []string{`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":null},{"type":"text","text":"x"}]}}]}`, `[DONE]`},
			wantThinking: strPtr(""),
		},
		{
			name: "reasoning fields stay out of the replay copy",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"chunk"}]}],"reasoning":" field"}}]}`,
				`{"choices":[{"delta":{"content":"x"}}]}`,
				`[DONE]`,
			},
			wantThinking: strPtr("chunk"), wantReasoning: "chunk field",
		},
		{"string content only", []string{`{"choices":[{"delta":{"content":"x"}}]}`, `[DONE]`}, nil, "", ""},
		{"reasoning content field only", []string{`{"choices":[{"delta":{"reasoning_content":"r","content":"x"}}]}`, `[DONE]`}, nil, "", "r"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reply, client, err := streamWithWire(t, ReplayThinkingChunk, tc.events...)
			if err != nil {
				t.Fatal(err)
			}
			source := client.ReplaySource()
			if reply.Replay == nil || reply.Replay.Source != source {
				t.Fatalf("replay = %+v, want one issued by %+v", reply.Replay, source)
			}
			if !equalText(reply.Replay.Thinking, tc.wantThinking) {
				t.Errorf("thinking = %v, want %v", deref(reply.Replay.Thinking), deref(tc.wantThinking))
			}
			if reply.Replay.ThinkingSignature != tc.wantSignature {
				t.Errorf("signature = %q, want %q", reply.Replay.ThinkingSignature, tc.wantSignature)
			}
			if reply.Reasoning != tc.wantReasoning {
				t.Errorf("display reasoning = %q, want %q", reply.Reasoning, tc.wantReasoning)
			}
			if reply.Replay.ReasoningContent != nil || reply.Replay.Details != nil || reply.Replay.Blocks != nil {
				t.Errorf("Mistral capture filled another form: %+v", reply.Replay)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestNoThinkingReplayWithoutTheFlag(t *testing.T) {
	reply, _, err := streamWithWire(t, ReplayNone, mistralPhases...)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Replay != nil {
		t.Fatalf("replay = %+v, want none on a row without replay", reply.Replay)
	}
	if reply.Reasoning != "Let me check. Done." || reply.Content != "The answer is 42." {
		t.Fatalf("reply = %+v, want the parsed answer and reasoning", reply)
	}
}

// One replayCollector serves every wire: thinking chunks are recorded only
// under the thinking-chunk wire, and other wires keep their own fields.
func TestOtherWiresIgnoreThinkingChunks(t *testing.T) {
	for _, wire := range []ReplayWire{ReplayReasoningContent, ReplayThoughtSignature, ReplayReasoningDetails} {
		reply, _, err := streamWithWire(t, wire, mistralPhases...)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Replay == nil || reply.Replay.Thinking != nil || reply.Replay.ThinkingSignature != "" {
			t.Errorf("wire %d replay = %+v, want an issuer with no thinking", wire, reply.Replay)
		}
	}
}

func TestMistralThinkingReplayWireShape(t *testing.T) {
	var bodies []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, string(data))
		sendEvents(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	}
	client := localClient(t, handler)
	client.setReplayWire(ReplayThinkingChunk)
	own := client.ReplaySource()
	thinkingReplay := func(t *testing.T, source ReplaySource, thinking, signature string) *ReasoningReplay {
		t.Helper()
		replay, err := NewReasoningReplay(source, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		replay.Thinking, replay.ThinkingSignature = &thinking, signature
		return replay
	}
	plain := `{"role":"assistant","content":"answer \u003cb\u003e"}`
	cases := []struct {
		name    string
		message Message
		client  func(t *testing.T) *Client
		want    string // the encoded assistant message
	}{
		{
			name:    "same endpoint and model",
			message: Message{Role: "assistant", Content: "answer <b>", Reasoning: "display only", Replay: thinkingReplay(t, own, "trace", "")},
			want:    `{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"trace"}]},{"type":"text","text":"answer \u003cb\u003e"}]}`,
		},
		{
			name:    "signature kept",
			message: Message{Role: "assistant", Content: "answer <b>", Replay: thinkingReplay(t, own, "trace", "sig")},
			want:    `{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"trace"}],"signature":"sig"},{"type":"text","text":"answer \u003cb\u003e"}]}`,
		},
		{
			name:    "tool call turn without answer text",
			message: Message{Role: "assistant", Replay: thinkingReplay(t, own, "need a file", ""), ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}},
			want:    `{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"need a file"}]}],"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]}`,
		},
		{
			name:    "issuer sent no thinking",
			message: Message{Role: "assistant", Content: "answer <b>", Replay: &ReasoningReplay{Source: own}},
			want:    plain,
		},
		{
			name:    "different model",
			message: Message{Role: "assistant", Content: "answer <b>", Replay: thinkingReplay(t, ReplaySource{Endpoint: own.Endpoint, Model: "other-model"}, "trace", "")},
			want:    plain,
		},
		{
			name:    "different provider",
			message: Message{Role: "assistant", Content: "answer <b>", Replay: thinkingReplay(t, own, "trace", "")},
			client: func(t *testing.T) *Client {
				other := localClient(t, handler)
				other.setReplayWire(ReplayThinkingChunk)
				return other
			},
			want: plain,
		},
		{
			name:    "row without replay",
			message: Message{Role: "assistant", Content: "answer <b>", Replay: thinkingReplay(t, own, "trace", "")},
			client: func(t *testing.T) *Client {
				same, err := New(strings.TrimSuffix(own.Endpoint, "/chat/completions"), own.Model, "")
				if err != nil {
					t.Fatal(err)
				}
				return same
			},
			want: plain,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bodies = nil
			sender := client
			if tc.client != nil {
				sender = tc.client(t)
			}
			history := []Message{{Role: "user", Content: "q"}, tc.message, {Role: "user", Content: "next"}}
			if _, err := sender.Stream(context.Background(), history, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 1 {
				t.Fatalf("got %d requests, want 1", len(bodies))
			}
			want := `{"model":"` + sender.Model() + `","messages":[{"role":"user","content":"q"},` + tc.want +
				`,{"role":"user","content":"next"}],"stream":true,"stream_options":{"include_usage":true}}`
			if bodies[0] != want {
				t.Fatalf("body =\n%s\nwant\n%s", bodies[0], want)
			}
		})
	}
}

// Thinking that streamed in two chunks around answer text goes back as one
// leading thinking chunk: Mistral rejects more than one.
func TestMistralReplaySendsOneThinkingChunk(t *testing.T) {
	var bodies []string
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, string(data))
		sendEvents(w,
			`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"first"}]}]}}]}`,
			`{"choices":[{"delta":{"content":"answer"}}]}`,
			`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":" second"}]},{"type":"text","text":" more"}]}}]}`,
			`[DONE]`)
	})
	client.setReplayWire(ReplayThinkingChunk)
	history := []Message{{Role: "user", Content: "q"}}
	reply, err := client.Stream(context.Background(), history, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, reply, Message{Role: "user", Content: "next"})
	if _, err := client.Stream(context.Background(), history, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[1]), &request); err != nil {
		t.Fatal(err)
	}
	const want = `[{"type":"thinking","thinking":[{"type":"text","text":"first second"}]},{"type":"text","text":"answer more"}]`
	if got := string(request.Messages[1].Content); got != want {
		t.Fatalf("assistant content = %s, want %s", got, want)
	}
	if strings.Count(bodies[1], `"type":"thinking"`) != 1 {
		t.Fatalf("body holds more than one thinking chunk: %s", bodies[1])
	}
}

func TestThinkingReplayEqualAndUnsignedOnly(t *testing.T) {
	build := func(thinking *string, signature string) *ReasoningReplay {
		replay := mustReplay(t, nil, nil, nil)
		replay.Thinking, replay.ThinkingSignature = thinking, signature
		return replay
	}
	if !build(strPtr("t"), "s").Equal(build(strPtr("t"), "s")) {
		t.Error("equal thinking replays compare unequal")
	}
	for name, other := range map[string]*ReasoningReplay{
		"other text":      build(strPtr("u"), "s"),
		"other signature": build(strPtr("t"), ""),
		"no thinking":     build(nil, "s"),
	} {
		if build(strPtr("t"), "s").Equal(other) {
			t.Errorf("%s compares equal", name)
		}
	}
	if build(strPtr(""), "").Equal(build(nil, "")) {
		t.Error("empty thinking equals no thinking")
	}

	unsigned := build(strPtr("trace"), "").UnsignedOnly()
	if want := build(strPtr("trace"), ""); !unsigned.Equal(want) {
		t.Errorf("UnsignedOnly(unsigned thinking) = %+v, want the thinking kept", unsigned)
	}
	if got := build(strPtr("trace"), "sig").UnsignedOnly(); got != nil {
		t.Errorf("UnsignedOnly(signed thinking) = %+v, want nil", got)
	}
}

func TestThinkingReplayJSONRoundTrip(t *testing.T) {
	replay := mustReplay(t, nil, nil, nil)
	replay.Thinking, replay.ThinkingSignature = strPtr(""), "sig"
	data, err := json.Marshal(Message{Role: "assistant", Content: "a", Replay: replay})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Replay.Equal(replay) {
		t.Fatalf("round trip = %+v, want %+v", decoded.Replay, replay)
	}
}

func TestClientEstimateCountsThinkingReplay(t *testing.T) {
	client, err := New("https://api.mistral.ai/v1", "magistral-medium-latest", "")
	if err != nil {
		t.Fatal(err)
	}
	client.setReplayWire(ReplayThinkingChunk)
	replay, err := NewReasoningReplay(client.ReplaySource(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay.Thinking = strPtr("a long hidden trace")
	history := []Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a", Replay: replay}}
	base, ok := EstimateInputTokens(client.Model(), history, nil)
	if !ok {
		t.Fatal("base estimate unavailable")
	}
	trace, _ := estimateTextTokens("a long hidden trace")
	if got, ok := client.EstimateInputTokens(history, nil); !ok || got != base+trace {
		t.Fatalf("estimate = %d, %v; want %d", got, ok, base+trace)
	}
}

// Without thinking, requestContent must encode exactly as the string field it
// replaced, escapes included, so no other provider's bytes change.
func TestRequestContentEncodesLikeAString(t *testing.T) {
	type before struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	for _, text := range []string{"", "plain", "<b>&\"q\"", "line\nbreak\ttab", "  ", "ünïcødé 日本 🙂", "\x01\x7f", "bad \xff utf8", "a\u2028b\u2029c"} {
		want, err := json.Marshal(before{Role: "assistant", Content: text})
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(requestMessage{Role: "assistant", Content: requestContent{text: text}})
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("content %q encodes as %s, want %s", text, got, want)
		}
	}
}
