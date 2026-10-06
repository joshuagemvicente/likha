package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// sseFrames renders Messages API events as the wire does: an event line
// naming the payload's type, then its data line.
func sseFrames(events ...string) string {
	var b strings.Builder
	for _, event := range events {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &head)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", head.Type, event)
	}
	return b.String()
}

const (
	evMessageStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5","usage":{"input_tokens":50,"cache_creation_input_tokens":400,"cache_read_input_tokens":9000,"output_tokens":1}}}`
	evMessageStop  = `{"type":"message_stop"}`
	evPing         = `{"type":"ping"}`
)

func evTextStart(index int) string {
	return fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index)
}

func evText(index int, text string) string {
	quoted, _ := json.Marshal(text)
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, index, quoted)
}

func evThinkingStart(index int) string {
	return fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"thinking","thinking":""}}`, index)
}

func evThinking(index int, text string) string {
	quoted, _ := json.Marshal(text)
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"thinking_delta","thinking":%s}}`, index, quoted)
}

func evSignature(index int, signature string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":%q}}`, index, signature)
}

func evToolStart(index int, id, name string) string {
	return fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, index, id, name)
}

func evInput(index int, partial string) string {
	quoted, _ := json.Marshal(partial)
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`, index, quoted)
}

func evStop(index int) string {
	return fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index)
}

func evMessageDelta(stopReason string) string {
	return fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_details":null},"usage":{"output_tokens":300}}`, stopReason)
}

// interleavedStream is two thinking blocks around two tool calls, with text
// between, as adaptive thinking streams a tool turn.
func interleavedStream() []string {
	return []string{
		evMessageStart, evPing,
		evThinkingStart(0), evThinking(0, "Plan: "), evThinking(0, "read a."), evSignature(0, "sig-0"), evStop(0),
		evTextStart(1), evText(1, "Reading a."), evStop(1),
		evToolStart(2, "toolu_1", "read"), evInput(2, ""), evInput(2, `{"path":`), evInput(2, `"a"}`), evStop(2),
		evThinkingStart(3), evThinking(3, "Now b."), evSignature(3, "sig-3"), evStop(3),
		evToolStart(4, "toolu_2", "read"), evInput(4, `{"path":"b"}`), evStop(4),
		evMessageDelta("tool_use"), evMessageStop,
	}
}

const interleavedBlocks = `[{"type":"thinking","thinking":"Plan: read a.","signature":"sig-0"},` +
	`{"type":"text","text":"Reading a."},` +
	`{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a"}},` +
	`{"type":"thinking","thinking":"Now b.","signature":"sig-3"},` +
	`{"type":"tool_use","id":"toolu_2","name":"read","input":{"path":"b"}}]`

func decodeStream(t *testing.T, events ...string) (anthropicResponse, []string, []string, error) {
	t.Helper()
	var text, reasoning []string
	response, err := consumeAnthropicStream(context.Background(), strings.NewReader(sseFrames(events...)),
		func(s string) { text = append(text, s) },
		func(s string) { reasoning = append(reasoning, s) })
	return response, text, reasoning, err
}

func TestConsumeAnthropicStreamInterleaved(t *testing.T) {
	response, text, reasoning, err := decodeStream(t, interleavedStream()...)
	if err != nil {
		t.Fatal(err)
	}
	wantReasoning := []string{"Plan: ", "read a.", "\n\n", "Now b."}
	if !reflect.DeepEqual(reasoning, wantReasoning) || !reflect.DeepEqual(text, []string{"Reading a."}) {
		t.Fatalf("streamed text %q reasoning %q, want %q", text, reasoning, wantReasoning)
	}
	message := response.message
	wantCalls := []ToolCall{{ID: "toolu_1", Name: "read", Arguments: `{"path":"a"}`}, {ID: "toolu_2", Name: "read", Arguments: `{"path":"b"}`}}
	if message.Content != "Reading a." || message.Reasoning != "Plan: read a.\n\nNow b." || !reflect.DeepEqual(message.ToolCalls, wantCalls) {
		t.Fatalf("message = %+v", message)
	}
	if string(response.blocks) != interleavedBlocks {
		t.Fatalf("blocks\n got %s\nwant %s", response.blocks, interleavedBlocks)
	}
}

func TestConsumeAnthropicStreamShapes(t *testing.T) {
	cases := []struct {
		name          string
		events        []string
		wantContent   string
		wantReasoning string
		wantCalls     []ToolCall
		wantBlocks    string
	}{
		{
			name:        "text only, with ping and an unknown event",
			events:      []string{evMessageStart, evPing, `{"type":"future_event","x":1}`, evTextStart(0), evText(0, "Hel"), evText(0, "lo"), evStop(0), evMessageDelta("end_turn"), evMessageStop},
			wantContent: "Hello",
		},
		{
			name:          "thinking, signature and text",
			events:        []string{evMessageStart, evThinkingStart(0), evThinking(0, "hmm"), evSignature(0, "s"), evStop(0), evTextStart(1), evText(1, "ok"), evStop(1), evMessageDelta("end_turn"), evMessageStop},
			wantContent:   "ok",
			wantReasoning: "hmm",
			wantBlocks:    `[{"type":"thinking","thinking":"hmm","signature":"s"},{"type":"text","text":"ok"}]`,
		},
		{
			name: "redacted thinking is kept but never shown",
			events: []string{evMessageStart, `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"enc"}}`, evStop(0),
				evTextStart(1), evText(1, "ok"), evStop(1), evMessageDelta("end_turn"), evMessageStop},
			wantContent: "ok",
			wantBlocks:  `[{"type":"redacted_thinking","data":"enc"},{"type":"text","text":"ok"}]`,
		},
		{
			name:        "tool input with an empty first fragment",
			events:      []string{evMessageStart, evToolStart(0, "t1", "list"), evInput(0, ""), evStop(0), evMessageDelta("tool_use"), evMessageStop},
			wantCalls:   []ToolCall{{ID: "t1", Name: "list", Arguments: "{}"}},
			wantContent: "",
		},
		{
			name:        "omitted display streams an empty thinking block",
			events:      []string{evMessageStart, evThinkingStart(0), evThinking(0, ""), evSignature(0, "s"), evStop(0), evTextStart(1), evText(1, "ok"), evStop(1), evMessageDelta("end_turn"), evMessageStop},
			wantContent: "ok",
			wantBlocks:  `[{"type":"thinking","thinking":"","signature":"s"},{"type":"text","text":"ok"}]`,
		},
		{
			name: "a fallback block makes the response unreplayable",
			events: []string{evMessageStart, evThinkingStart(0), evThinking(0, "hmm"), evSignature(0, "s"), evStop(0),
				`{"type":"content_block_start","index":1,"content_block":{"type":"fallback"}}`, evStop(1),
				evTextStart(2), evText(2, "ok"), evStop(2), evMessageDelta("end_turn"), evMessageStop},
			wantContent:   "ok",
			wantReasoning: "hmm",
		},
		{
			name:          "max_tokens inside a thinking block keeps the text and no blocks",
			events:        []string{evMessageStart, evTextStart(0), evText(0, "partial"), evStop(0), evThinkingStart(1), evThinking(1, "cut"), evMessageDelta("max_tokens"), evMessageStop},
			wantContent:   "partial",
			wantReasoning: "cut",
		},
		{
			name:        "context window exceeded returns the message",
			events:      []string{evMessageStart, evTextStart(0), evText(0, "long"), evStop(0), evMessageDelta("model_context_window_exceeded"), evMessageStop},
			wantContent: "long",
		},
		{
			name:        "unknown stop reason returns the message",
			events:      []string{evMessageStart, evTextStart(0), evText(0, "x"), evStop(0), evMessageDelta("brand_new_reason"), evMessageStop},
			wantContent: "x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, _, _, err := decodeStream(t, tc.events...)
			if err != nil {
				t.Fatal(err)
			}
			m := response.message
			if m.Role != "assistant" || m.Content != tc.wantContent || m.Reasoning != tc.wantReasoning || !reflect.DeepEqual(m.ToolCalls, tc.wantCalls) {
				t.Fatalf("message = %+v", m)
			}
			if string(response.blocks) != tc.wantBlocks {
				t.Fatalf("blocks = %s, want %s", response.blocks, tc.wantBlocks)
			}
		})
	}
}

func TestConsumeAnthropicStreamFailures(t *testing.T) {
	refusal := func(details string) string {
		return `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":` + details + `},"usage":{"output_tokens":3}}`
	}
	cases := []struct {
		name    string
		events  []string
		wantErr string
		check   func(t *testing.T, err error)
	}{
		{
			name:    "EOF after a text delta",
			events:  []string{evMessageStart, evTextStart(0), evText(0, "half")},
			wantErr: "model stream ended before message_stop",
			check: func(t *testing.T, err error) {
				if !errors.Is(err, errAnthropicStreamEnded) {
					t.Fatalf("error %v is not errAnthropicStreamEnded", err)
				}
			},
		},
		{
			name:    "mid-stream overloaded error",
			events:  []string{evMessageStart, evTextStart(0), evText(0, "x"), `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
			wantErr: "model stream error: overloaded_error: Overloaded",
			check: func(t *testing.T, err error) {
				var apiErr *anthropicAPIError
				if !errors.As(err, &apiErr) || apiErr.Status != 0 || apiErr.Type != "overloaded_error" {
					t.Fatalf("error %v is not an in-stream overloaded API error", err)
				}
			},
		},
		{
			name:    "refusal with category and explanation",
			events:  []string{evMessageStart, evTextStart(0), evText(0, "Sure, here"), evStop(0), refusal(`{"type":"refusal","category":"cyber","explanation":"This looks like malware."}`), evMessageStop},
			wantErr: "Claude declined the request (category: cyber).\nThis looks like malware.",
			check: func(t *testing.T, err error) {
				var refused *RefusalError
				if !errors.As(err, &refused) || refused.Category != "cyber" {
					t.Fatalf("error %v is not a cyber refusal", err)
				}
			},
		},
		{
			name:    "refusal without category",
			events:  []string{evMessageStart, refusal(`{"type":"refusal","category":null,"explanation":null}`), evMessageStop},
			wantErr: "Claude declined the request.",
		},
		{
			name:    "pause_turn",
			events:  []string{evMessageStart, evMessageDelta("pause_turn"), evMessageStop},
			wantErr: "unexpected stop reason pause_turn",
		},
		{
			name:    "max_tokens with a half-written tool input",
			events:  []string{evMessageStart, evToolStart(0, "t1", "write"), evInput(0, `{"path":"a","content":"x`), evMessageDelta("max_tokens"), evMessageStop},
			wantErr: "incomplete structured tool call at index 0",
		},
		{
			name:    "malformed event",
			events:  []string{`{"type":`},
			wantErr: "malformed model stream event",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, _, _, err := decodeStream(t, tc.events...)
			if err == nil {
				t.Fatalf("decode succeeded with %+v", response.message)
			}
			if !strings.HasPrefix(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want prefix %q", err, tc.wantErr)
			}
			if response.message.Role != "" || response.message.Content != "" {
				t.Fatalf("a failed stream returned a message: %+v", response.message)
			}
			if tc.check != nil {
				tc.check(t, err)
			}
		})
	}
}

func TestConsumeAnthropicStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := consumeAnthropicStream(ctx, strings.NewReader(sseFrames(evMessageStart, evMessageStop)), nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestAnthropicUsageReport(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		want   RequestUsage
		ok     bool
	}{
		{
			name:   "cache reads and writes are part of the prompt",
			events: []string{evMessageStart, evMessageDelta("end_turn"), evMessageStop},
			want:   RequestUsage{Prompt: 9450, Completion: 300, PromptSeen: true, CompletionSeen: true, CacheRead: 9000, CacheWrite: 400},
			ok:     true,
		},
		{
			name: "message_delta adds cache fields and thinking tokens",
			events: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":12,"cache_read_input_tokens":100,"cache_creation_input_tokens":null,"output_tokens":40,"output_tokens_details":{"thinking_tokens":25}}}`,
				evMessageStop,
			},
			want: RequestUsage{Prompt: 112, Completion: 40, PromptSeen: true, CompletionSeen: true, CacheRead: 100, Reasoning: 25, ReasoningSeen: true},
			ok:   true,
		},
		{
			name: "reported zero thinking tokens still count as seen",
			events: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5,"output_tokens_details":{"thinking_tokens":0}}}`,
				evMessageStop,
			},
			want: RequestUsage{Prompt: 10, Completion: 5, PromptSeen: true, CompletionSeen: true, ReasoningSeen: true},
			ok:   true,
		},
		{
			name: "absent thinking tokens are not reported",
			events: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5,"output_tokens_details":{}}}`,
				evMessageStop,
			},
			want: RequestUsage{Prompt: 10, Completion: 5, PromptSeen: true, CompletionSeen: true},
			ok:   true,
		},
		{
			name:   "usage-less stream",
			events: []string{`{"type":"message_start","message":{}}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, evMessageStop},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, _, _, err := decodeStream(t, tc.events...)
			if err != nil {
				t.Fatal(err)
			}
			if response.usage.ok != tc.ok {
				t.Fatalf("usage ok = %v, want %v", response.usage.ok, tc.ok)
			}
			if got := response.usage.request(); tc.ok && got != tc.want {
				t.Fatalf("usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}
