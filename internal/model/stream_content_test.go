package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func answer(text string) contentPiece    { return contentPiece{kind: pieceAnswer, text: text} }
func thought(text string) contentPiece   { return contentPiece{kind: pieceThinking, text: text} }
func skippedAs(kind string) contentPiece { return contentPiece{kind: pieceSkipped, text: kind} }

func TestStreamContentUnmarshal(t *testing.T) {
	cases := []struct {
		name      string
		delta     string // the delta object holding (or lacking) "content"
		want      []contentPiece
		thinking  bool
		signature string
	}{
		{"null", `{"content":null}`, nil, false, ""},
		{"absent", `{}`, nil, false, ""},
		{"empty string", `{"content":""}`, []contentPiece{answer("")}, false, ""},
		{"string", `{"content":"hi"}`, []contentPiece{answer("hi")}, false, ""},
		{"docs thinking then text",
			`{"content":[{"type":"thinking","thinking":[{"type":"text","text":"Let me think."}]},{"type":"text","text":"42"}]}`,
			[]contentPiece{thought("Let me think."), answer("42")}, true, ""},
		{"transition keeps thinking ahead of text",
			`{"content":[{"type":"thinking","thinking":[{"type":"text","text":" done"}],"closed":true},{"type":"text","text":"The"}]}`,
			[]contentPiece{thought(" done"), answer("The")}, true, ""},
		{"references inside thinking are skipped",
			`{"content":[{"type":"thinking","thinking":[{"type":"reference","reference_ids":[1]},{"type":"text","text":"why"},{"type":"tool_reference","tool":"x"},{"text":"untyped"},{"type":7,"text":"numeric type"}]}]}`,
			[]contentPiece{skippedAs("reference"), thought("why"), skippedAs("tool_reference"), skippedAs(""), skippedAs("")}, true, ""},
		{"several thinking items in order",
			`{"content":[{"type":"thinking","thinking":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`,
			[]contentPiece{thought("a"), thought("b")}, true, ""},
		{"image_url skipped", `{"content":[{"type":"image_url","image_url":{"url":"x"}}]}`, []contentPiece{skippedAs("image_url")}, false, ""},
		{"reference skipped", `{"content":[{"type":"reference","reference_ids":[1,2]}]}`, []contentPiece{skippedAs("reference")}, false, ""},
		{"future kind skipped with odd fields", `{"content":[{"type":"future_kind","text":5,"thinking":"x"}]}`, []contentPiece{skippedAs("future_kind")}, false, ""},
		{"type-less chunk skipped", `{"content":[{"text":"no type"}]}`, []contentPiece{skippedAs("")}, false, ""},
		{"non-string type skipped", `{"content":[{"type":3}]}`, []contentPiece{skippedAs("")}, false, ""},
		{"long type clipped", `{"content":[{"type":"` + strings.Repeat("k", 40) + `"}]}`, []contentPiece{skippedAs(strings.Repeat("k", 32))}, false, ""},
		{"null text adds nothing", `{"content":[{"type":"text","text":null}]}`, nil, false, ""},
		{"absent text adds nothing", `{"content":[{"type":"text"}]}`, nil, false, ""},
		{"null thinking adds nothing", `{"content":[{"type":"thinking","thinking":null}]}`, nil, true, ""},
		{"absent thinking adds nothing", `{"content":[{"type":"thinking"}]}`, nil, true, ""},
		{"null text inside thinking adds nothing", `{"content":[{"type":"thinking","thinking":[{"type":"text","text":null}]}]}`, nil, true, ""},
		{"empty list", `{"content":[]}`, nil, false, ""},
		{"signature kept", `{"content":[{"type":"thinking","thinking":[],"signature":"sig"}]}`, nil, true, "sig"},
		{"last non-empty signature wins",
			`{"content":[{"type":"thinking","signature":"a"},{"type":"thinking","signature":""},{"type":"thinking","signature":null},{"type":"thinking","signature":9}]}`,
			nil, true, "a"},
		{"numeric signature ignored", `{"content":[{"type":"thinking","thinking":[{"type":"text","text":"t"}],"signature":12}]}`, []contentPiece{thought("t")}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var delta struct {
				Content streamContent `json:"content"`
			}
			if err := json.Unmarshal([]byte(tc.delta), &delta); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got := delta.Content
			if !reflect.DeepEqual(got.pieces, tc.want) {
				t.Errorf("pieces = %#v, want %#v", got.pieces, tc.want)
			}
			if got.thinking != tc.thinking || got.signature != tc.signature {
				t.Errorf("thinking, signature = %v, %q; want %v, %q", got.thinking, got.signature, tc.thinking, tc.signature)
			}
		})
	}
}

// brokenContents are the shapes of spec decision 4: broken streams, not
// unknown features.
var brokenContents = []struct {
	name    string
	content string
}{
	{"number", `5`},
	{"boolean", `true`},
	{"object", `{"type":"text","text":"x"}`},
	{"string list item", `["hi"]`},
	{"null list item", `[null]`},
	{"numeric text", `[{"type":"text","text":5}]`},
	{"object text", `[{"type":"text","text":{"a":1}}]`},
	{"numeric text inside thinking", `[{"type":"thinking","thinking":[{"type":"text","text":5}]}]`},
	{"string thinking", `[{"type":"thinking","thinking":"x"}]`},
	{"object thinking", `[{"type":"thinking","thinking":{"type":"text"}}]`},
	{"string item inside thinking", `[{"type":"thinking","thinking":["x"]}]`},
}

func TestStreamContentUnmarshalRejectsBrokenShapes(t *testing.T) {
	for _, tc := range brokenContents {
		t.Run(tc.name, func(t *testing.T) {
			var content streamContent
			err := json.Unmarshal([]byte(tc.content), &content)
			if !errors.Is(err, errMalformedContent) {
				t.Fatalf("Unmarshal(%s) error = %v, want errMalformedContent", tc.content, err)
			}
		})
	}
}

func TestStreamFailsOnBrokenContent(t *testing.T) {
	for _, tc := range brokenContents {
		t.Run(tc.name, func(t *testing.T) {
			client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
				sendEvents(w, `{"choices":[{"delta":{"content":"partial "}}]}`, `{"choices":[{"delta":{"content":`+tc.content+`}}]}`, `[DONE]`)
			})
			reply, err := client.Stream(context.Background(), []Message{{Role: "user", Content: "q"}}, nil, nil, nil)
			if !errors.Is(err, errMalformedContent) {
				t.Fatalf("error = %v, want errMalformedContent", err)
			}
			if !strings.HasPrefix(err.Error(), "malformed model stream event: ") {
				t.Errorf("error = %q, want the existing malformed-stream wording", err)
			}
			if reply.Role != "" || reply.Content != "" {
				t.Errorf("a broken reply returned a message: %+v", reply)
			}
		})
	}
}

// streamEvent records one callback call so a test can assert arrival order
// across the text and reasoning streams.
type streamEvent struct {
	kind, text string
}

func recordStream(t *testing.T, client *Client, history []Message, tools []ToolDefinition) (Message, []streamEvent, error) {
	t.Helper()
	var events []streamEvent
	reply, err := client.Stream(context.Background(), history, tools,
		func(s string) { events = append(events, streamEvent{"text", s}) },
		func(s string) { events = append(events, streamEvent{"reasoning", s}) })
	return reply, events, err
}

// mistralPhases is Mistral's documented reasoning stream: list deltas with a
// thinking chunk, one transition list, then plain string deltas.
var mistralPhases = []string{
	`{"choices":[{"delta":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"Let me "}]}]}}]}`,
	`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"check."}]}]}}]}`,
	`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":" Done."}],"closed":true},{"type":"text","text":"The "}]}}]}`,
	`{"choices":[{"delta":{"content":"answer"}}]}`,
	`{"choices":[{"delta":{"content":" is 42."},"finish_reason":"stop"}]}`,
	`[DONE]`,
}

func TestStreamMistralThinkingPhases(t *testing.T) {
	client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
		sendEvents(w, mistralPhases...)
	})
	reply, events, err := recordStream(t, client, []Message{{Role: "user", Content: "q"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []streamEvent{
		{"reasoning", "Let me "}, {"reasoning", "check."}, {"reasoning", " Done."},
		{"text", "The "}, {"text", "answer"}, {"text", " is 42."},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	want := Message{Role: "assistant", Content: "The answer is 42.", Reasoning: "Let me check. Done."}
	if !reflect.DeepEqual(reply, want) {
		t.Fatalf("reply = %+v, want %+v", reply, want)
	}
}

func TestStreamChunkedContent(t *testing.T) {
	cases := []struct {
		name       string
		events     []string
		wantEvents []streamEvent
		want       Message
	}{
		{
			name: "thinking chunks join without separator and empty text fires nothing",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"to"}]}]}}]}`,
				`{"choices":[{"delta":{"content":[{"type":"text","text":""},{"type":"thinking","thinking":[{"type":"text","text":""}]}]}}]}`,
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"ken"}]}]}}]}`,
				`{"choices":[{"delta":{"content":"ok"}}]}`,
				`[DONE]`,
			},
			wantEvents: []streamEvent{{"reasoning", "to"}, {"reasoning", "ken"}, {"text", "ok"}},
			want:       Message{Role: "assistant", Content: "ok", Reasoning: "token"},
		},
		{
			name: "tool calls after list thinking stay complete",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"need a file"}]}]}}]}`,
				`{"choices":[{"delta":{"content":"","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			},
			wantEvents: []streamEvent{{"reasoning", "need a file"}},
			want: Message{Role: "assistant", Reasoning: "need a file", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read", Arguments: `{"path":"a.go"}`},
			}},
		},
		{
			name: "skipped chunks beside text leave the text",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"image_url","image_url":"x"},{"type":"reference","reference_ids":[1]},{"type":"future_kind"},{"no":"type"},{"type":"text","text":"seen"}]}}]}`,
				`[DONE]`,
			},
			wantEvents: []streamEvent{{"text", "seen"}},
			want:       Message{Role: "assistant", Content: "seen"},
		},
		{
			name: "reasoning field after content in the same delta",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"chunk "}]}],"reasoning_content":"field"}}]}`,
				`{"choices":[{"delta":{"content":"a"}}]}`,
				`[DONE]`,
			},
			wantEvents: []streamEvent{{"reasoning", "chunk "}, {"reasoning", "field"}, {"text", "a"}},
			want:       Message{Role: "assistant", Content: "a", Reasoning: "chunk field"},
		},
		{
			name: "thinking after answer text streams in place",
			events: []string{
				`{"choices":[{"delta":{"content":"a"}}]}`,
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"late"}]},{"type":"text","text":"b"}]}}]}`,
				`[DONE]`,
			},
			wantEvents: []streamEvent{{"text", "a"}, {"reasoning", "late"}, {"text", "b"}},
			want:       Message{Role: "assistant", Content: "ab", Reasoning: "late"},
		},
		{
			name:       "empty string deltas stay an empty answer",
			events:     []string{`{"choices":[{"delta":{"content":""}}]}`, `{"choices":[{"delta":{"content":""}}]}`, `[DONE]`},
			wantEvents: nil,
			want:       Message{Role: "assistant"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
				sendEvents(w, tc.events...)
			})
			reply, events, err := recordStream(t, client, []Message{{Role: "user", Content: "q"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(events, tc.wantEvents) {
				t.Errorf("events = %v, want %v", events, tc.wantEvents)
			}
			if !reflect.DeepEqual(reply, tc.want) {
				t.Errorf("reply = %+v, want %+v", reply, tc.want)
			}
		})
	}
}

// Spec decision 3 counts both top-level chunks and items inside "thinking"
// as content Likha cannot show; a reply made only of them is an error.
func TestStreamOnlyUnshowableContentFails(t *testing.T) {
	cases := []struct {
		name     string
		events   []string
		wantType string
	}{
		{
			name: "top-level chunks",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"image_url","image_url":"x"}]}}]}`,
				`{"choices":[{"delta":{"content":[{"type":"reference","reference_ids":[1]}]}}]}`,
				`{"choices":[{"delta":{"content":""}}]}`,
				`[DONE]`,
			},
			wantType: `"image_url"`,
		},
		{
			name: "items inside thinking",
			events: []string{
				`{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"reference","reference_ids":[1]},{"type":"tool_reference","tool":"x"}]}]}}]}`,
				`[DONE]`,
			},
			wantType: `"reference"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := localClient(t, func(w http.ResponseWriter, _ *http.Request) {
				sendEvents(w, tc.events...)
			})
			client.setReplayWire(ReplayThinkingChunk) // no replay state may outlive the error
			reply, events, err := recordStream(t, client, []Message{{Role: "user", Content: "q"}}, nil)
			if !errors.Is(err, errUnshowableContent) {
				t.Fatalf("error = %v, want errUnshowableContent", err)
			}
			if !strings.Contains(err.Error(), tc.wantType) {
				t.Errorf("error %q does not name the first skipped type %s", err, tc.wantType)
			}
			if reply.Role != "" || reply.Replay != nil || len(events) != 0 {
				t.Errorf("reply = %+v, events = %v; want no message and no output", reply, events)
			}
		})
	}
}

// A reply parsed from chunks goes back exactly as a string reply would: the
// answer as string content and no thinking, on a provider without replay.
func TestRequestBodyAfterChunkedReplyIsUnchanged(t *testing.T) {
	var bodies []string
	client := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, string(data))
		sendEvents(w, mistralPhases...)
	})
	history := []Message{{Role: "user", Content: "q"}}
	reply, err := client.Stream(context.Background(), history, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, reply, Message{Role: "user", Content: "next"})
	if _, err := client.Stream(context.Background(), history, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	const golden = `{"model":"local-model","messages":[` +
		`{"role":"user","content":"q"},` +
		`{"role":"assistant","content":"The answer is 42."},` +
		`{"role":"user","content":"next"}],` +
		`"stream":true,"stream_options":{"include_usage":true}}`
	if len(bodies) != 2 || bodies[1] != golden {
		t.Fatalf("second body =\n%s\nwant\n%s", bodies[len(bodies)-1], golden)
	}
}
