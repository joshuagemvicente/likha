package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// BuildCodexRequest helpers.

func codexRequestMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return body
}

func codexInputMaps(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["input"].([]any)
	if !ok {
		t.Fatal("request has no input array")
	}
	items := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		items = append(items, e.(map[string]any))
	}
	return items
}

func codexContentParts(t *testing.T, body map[string]any, index int) []map[string]any {
	t.Helper()
	raw, ok := codexInputMaps(t, body)[index]["content"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("input[%d] has no content parts", index)
	}
	parts := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		parts = append(parts, e.(map[string]any))
	}
	return parts
}

func codexWantError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestBuildCodexRequestFullConversation(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "first system block"},
		{Role: "developer", Content: "second developer block"},
		{Role: "user", Content: "list things"},
		{Role: "assistant", Content: "done", Reasoning: "secret reasoning that must never be sent", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "list_files", Arguments: `{"path":"/tmp"}`},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "a.txt\nb.txt"},
	}
	tools := []ToolDefinition{{
		Name:        "list_files",
		Description: "list directory contents",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}}
	payload, err := BuildCodexRequest("codex-mini", messages, tools)
	if err != nil {
		t.Fatalf("BuildCodexRequest: %v", err)
	}
	body := codexRequestMap(t, payload)
	if body["model"] != "codex-mini" {
		t.Errorf("model = %v, want codex-mini", body["model"])
	}
	if body["instructions"] != "first system block\n\nsecond developer block" {
		t.Errorf("instructions = %v, want system+developer joined", body["instructions"])
	}
	for _, forbidden := range []string{"temperature", "top_p", "store", "messages", "presence_penalty", "frequency_penalty"} {
		if _, present := body[forbidden]; present {
			t.Errorf("request contains forbidden field %q", forbidden)
		}
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	items := codexInputMaps(t, body)
	if len(items) != 4 {
		t.Fatalf("got %d input items, want 4", len(items))
	}
	parts := codexContentParts(t, body, 0)
	if items[0]["type"] != "message" || items[0]["role"] != "user" {
		t.Errorf("item0 = %v, want user message", items[0])
	}
	if len(parts) != 1 || parts[0]["type"] != "input_text" || parts[0]["text"] != "list things" {
		t.Errorf("user content = %v, want single input_text part", parts)
	}
	if items[1]["type"] != "message" || items[1]["role"] != "assistant" {
		t.Errorf("item1 = %v, want assistant message", items[1])
	}
	parts = codexContentParts(t, body, 1)
	if len(parts) != 1 || parts[0]["type"] != "output_text" || parts[0]["text"] != "done" {
		t.Errorf("assistant content = %v, want single output_text part", parts)
	}
	if items[2]["type"] != "function_call" || items[2]["call_id"] != "call_1" || items[2]["name"] != "list_files" || items[2]["arguments"] != `{"path":"/tmp"}` {
		t.Errorf("item2 = %v, want function_call", items[2])
	}
	if items[3]["type"] != "function_call_output" || items[3]["call_id"] != "call_1" || items[3]["output"] != "a.txt\nb.txt" {
		t.Errorf("item3 = %v, want function_call_output", items[3])
	}
	rawTools, ok := body["tools"].([]any)
	if !ok || len(rawTools) != 1 {
		t.Fatalf("tools = %v, want one entry", body["tools"])
	}
	tool := rawTools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "list_files" || tool["description"] != "list directory contents" {
		t.Errorf("tool = %v, want function definition", tool)
	}
	if params, ok := tool["parameters"].(map[string]any); !ok || params["type"] != "object" {
		t.Errorf("tool parameters = %v, want object schema", tool["parameters"])
	}
}

func TestBuildCodexRequestAssistantCallsBeforeMessage(t *testing.T) {
	// Tool calls come before or after the message item exactly as they appear
	// in the slice order; here the calls precede the content.
	messages := []Message{
		{Role: "assistant", Content: "here", ToolCalls: []ToolCall{
			{ID: "c1", Name: "a", Arguments: "1"},
			{ID: "c2", Name: "b", Arguments: "2"},
		}},
	}
	payload, err := BuildCodexRequest("m", messages, nil)
	if err != nil {
		t.Fatalf("BuildCodexRequest: %v", err)
	}
	payload2, err := BuildCodexRequest("m", []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildCodexRequest: %v", err)
	}
	if req := codexRequestMap(t, payload2); req["model"] != "m" {
		t.Errorf("model = %v, want m", req["model"])
	}
	items := codexInputMaps(t, codexRequestMap(t, payload))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0]["type"] != "message" || items[0]["role"] != "assistant" {
		t.Errorf("item0 = %v, want assistant message", items[0])
	}
	parts := codexContentParts(t, codexRequestMap(t, payload), 0)
	if parts[0]["type"] != "output_text" || parts[0]["text"] != "here" {
		t.Errorf("assistant content = %v, want output_text", parts)
	}
	if items[1]["call_id"] != "c1" || items[2]["call_id"] != "c2" {
		t.Errorf("call order = %v,%v, want c1,c2", items[1], items[2])
	}
}

func TestBuildCodexRequestOmissions(t *testing.T) {
	// No system/developer: instructions omitted entirely.
	// No tools: "tools" omitted entirely.
	payload, err := BuildCodexRequest("m", []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildCodexRequest: %v", err)
	}
	body := codexRequestMap(t, payload)
	for _, absent := range []string{"instructions", "tools"} {
		if _, present := body[absent]; present {
			t.Errorf("request must omit %q when nothing to send, got %v", absent, body[absent])
		}
	}
	if body["model"] != "m" || body["stream"] != true {
		t.Errorf("base fields = %v", body)
	}
	items := codexInputMaps(t, body)
	if len(items) != 1 {
		t.Fatalf("items = %v, want one", items)
	}
	if items[0]["type"] != "message" || items[0]["role"] != "user" {
		t.Errorf("item = %v, want user message", items[0])
	}
}

func TestBuildCodexRequestValidation(t *testing.T) {
	cases := []struct {
		name     string
		messages []Message
		tools    []ToolDefinition
		want     string
	}{
		{"bad role", []Message{{Role: "moderator", Content: "x"}}, nil, `invalid message role "moderator"`},
		{"tool without call id", []Message{{Role: "tool", Content: "out"}}, nil, "tool result requires a tool call ID"},
		{"tool call missing id", []Message{{Role: "assistant", ToolCalls: []ToolCall{{Name: "a", Arguments: "1"}}}}, nil, "invalid assistant tool call"},
		{"tool call missing name", []Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Arguments: "1"}}}}, nil, "invalid assistant tool call"},
		{"tool call bad arguments", []Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Name: "a", Arguments: "{oops"}}}}, nil, "invalid assistant tool call"},
		{"tool definition empty parameters", nil, []ToolDefinition{{Name: "x"}}, `invalid tool definition "x"`},
		{"tool definition bad parameters", nil, []ToolDefinition{{Name: "x", Parameters: json.RawMessage(`nope`)}}, `invalid tool definition "x"`},
		{"tool definition missing name", nil, []ToolDefinition{{Parameters: json.RawMessage(`{}`)}}, `invalid tool definition ""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildCodexRequest("m", tc.messages, tc.tools)
			codexWantError(t, err, tc.want)
		})
	}
	if _, err := BuildCodexRequest("m", nil, []ToolDefinition{{Name: "x", Parameters: json.RawMessage(`{}`)}}); err != nil {
		t.Errorf("empty parameters object {} must be valid: %v", err)
	}
}

func TestBuildCodexRequestJSONReversible(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first\nmulti-line"},
	}
	tools := []ToolDefinition{{Name: "echo", Parameters: json.RawMessage(`{"type":"object"}`)}}
	payload, err := BuildCodexRequest("model-x", messages, tools)
	if err != nil {
		t.Fatalf("BuildCodexRequest: %v", err)
	}
	back := codexRequestMap(t, payload)
	if back["model"] != "model-x" {
		t.Errorf("model = %v, want model-x", back["model"])
	}
	if _, present := back["instructions"]; present {
		t.Errorf("instructions must be omitted with no system messages")
	}
	items := codexInputMaps(t, back)
	if items[0]["type"] != "message" || items[0]["role"] != "user" {
		t.Errorf("item = %v", items[0])
	}
}

// ConsumeCodexStream helpers.

type codexRecorder struct {
	mu        sync.Mutex
	text      []string
	reason    []string
	usage     TokenUsage
	usageSeen bool
}

// usage reports the usage the last ConsumeCodexStream saw.
func (r *codexRecorder) reportedUsage() (TokenUsage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage, r.usageSeen
}

func (r *codexRecorder) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.text...)
}

func (r *codexRecorder) reasons() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reason...)
}

func codexRunStream(sse string) (Message, *codexRecorder, error) {
	rec := &codexRecorder{}
	msg, usage, usageSeen, err := ConsumeCodexStream(context.Background(), strings.NewReader(sse),
		func(s string) { rec.mu.Lock(); rec.text = append(rec.text, s); rec.mu.Unlock() },
		func(s string) { rec.mu.Lock(); rec.reason = append(rec.reason, s); rec.mu.Unlock() })
	rec.mu.Lock()
	rec.usage, rec.usageSeen = usage, usageSeen
	rec.mu.Unlock()
	return msg, rec, err
}

func codexFrame(event, payload string) string {
	return "event: " + event + "\ndata: " + payload + "\n\n"
}

func TestConsumeCodexStreamTextAndReasoning(t *testing.T) {
	sse := codexFrame("response.created", `{"response":{"id":"r"}}`) +
		codexFrame("response.reasoning_summary_text.delta", `{"delta":"think "}`) +
		codexFrame("response.reasoning_text.delta", `{"delta":"more think"}`) +
		codexFrame("response.output_text.delta", `{"delta":"Hello"}`) +
		codexFrame("response.output_text.delta", `{"delta":" world"}`) +
		codexFrame("response.completed", `{"response":{"output":[]}}`)
	msg, rec, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want assistant", msg.Role)
	}
	if msg.Content != "Hello world" {
		t.Errorf("content = %q, want Hello world", msg.Content)
	}
	if msg.Reasoning != "think more think" {
		t.Errorf("reasoning = %q, want think more think", msg.Reasoning)
	}
	if got, want := rec.texts(), []string{"Hello", " world"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("text deltas = %q, want %q", got, want)
	}
	if got, want := rec.reasons(), []string{"think ", "more think"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("reasoning deltas = %q, want %q", got, want)
	}
}

func TestConsumeCodexStreamFunctionCall(t *testing.T) {
	// added → delta → done → completed: done and completed are authoritative.
	sse := codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"list_files","arguments":""}}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"item_1","delta":"{\"pa"}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"item_1","delta":"th\":\"drift\"}"}`) +
		codexFrame("response.output_item.done", `{"item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"list_files","arguments":"{\"path\":\"a\"}"}}`) +
		codexFrame("response.completed", `{"response":{"output":[{"type":"function_call","call_id":"call_1","name":"list_files","arguments":"{\"path\":\"a\"}"},{"type":"message","content":[{"type":"output_text","text":"x"}]}]}}`)
	msg, _, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want one", msg.ToolCalls)
	}
	call := msg.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "list_files" || call.Arguments != `{"path":"a"}` {
		t.Errorf("tool call = %+v, want authoritative completed arguments", call)
	}
}

func TestConsumeCodexStreamFunctionCallStreamedOnly(t *testing.T) {
	// added → delta → completed (no done event): accumulated arguments win.
	sse := codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"i1","call_id":"c1","name":"f","arguments":"{\"a"}}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"i1","delta":"\":1}"}`) +
		codexFrame("response.completed", `{"response":{"output":[{"type":"function_call","call_id":"c1","name":"f","arguments":"{\"a\":1}"}]}}`)
	msg, _, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "c1" || msg.ToolCalls[0].Arguments != `{"a":1}` {
		t.Errorf("tool calls = %+v, want streamed+assembled call", msg.ToolCalls)
	}
}

func TestConsumeCodexStreamFunctionCallFromCompleted(t *testing.T) {
	// No streaming fragments at all: completed output is the only source and
	// must keep output order.
	sse := codexFrame("response.output_text.delta", `{"delta":"answer"}`) +
		codexFrame("response.completed", `{"response":{"output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]},{"type":"function_call","call_id":"c2","name":"second","arguments":"{}"},{"type":"function_call","call_id":"c1","name":"first","arguments":"[]"}]}}`)
	msg, _, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want two", msg.ToolCalls)
	}
	if msg.ToolCalls[0].ID != "c2" || msg.ToolCalls[1].ID != "c1" {
		t.Errorf("order = %s,%s, want c2,c1 (output order)", msg.ToolCalls[0].ID, msg.ToolCalls[1].ID)
	}
	if msg.ToolCalls[1].Arguments != "[]" {
		t.Errorf("arguments = %q, want []", msg.ToolCalls[1].Arguments)
	}
	if msg.Content != "answer" {
		t.Errorf("content = %q, want answer", msg.Content)
	}
}

func TestConsumeCodexStreamCompletedInvalidCall(t *testing.T) {
	cases := []struct {
		name string
		sse  string
		want string
	}{
		{"non-JSON arguments", codexFrame("response.completed", `{"response":{"output":[{"type":"function_call","call_id":"c","name":"f","arguments":"garbage"}]}}`), "non-JSON arguments"},
		{"missing name", codexFrame("response.completed", `{"response":{"output":[{"type":"function_call","call_id":"c","arguments":"1"}]}}`), "missing a call ID or name"},
		{"missing call id", codexFrame("response.completed", `{"response":{"output":[{"type":"function_call","name":"f","arguments":"1"}]}}`), "missing a call ID or name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := codexRunStream(tc.sse)
			codexWantError(t, err, tc.want)
		})
	}
}

func TestConsumeCodexStreamUsage(t *testing.T) {
	cases := []struct {
		name     string
		terminal string
		want     TokenUsage
		wantSeen bool
	}{
		{
			name:     "response.usage on completed",
			terminal: `{"response":{"output":[],"usage":{"input_tokens":110,"output_tokens":25}}}`,
			want:     TokenUsage{Prompt: 110, Completion: 25},
			wantSeen: true,
		},
		{
			name:     "top-level usage on completed",
			terminal: `{"response":{"output":[]},"usage":{"input_tokens":50,"output_tokens":8}}`,
			want:     TokenUsage{Prompt: 50, Completion: 8},
			wantSeen: true,
		},
		{
			name:     "top-level usage wins over response.usage",
			terminal: `{"response":{"output":[],"usage":{"input_tokens":1,"output_tokens":1}},"usage":{"input_tokens":2,"output_tokens":2}}`,
			want:     TokenUsage{Prompt: 2, Completion: 2},
			wantSeen: true,
		},
		{
			name:     "chat-completions alias naming accepted",
			terminal: `{"response":{"output":[],"usage":{"prompt_tokens":33,"completion_tokens":44}}}`,
			want:     TokenUsage{Prompt: 33, Completion: 44},
			wantSeen: true,
		},
		{
			name:     "zero totals are valid usage",
			terminal: `{"response":{"output":[],"usage":{"input_tokens":0,"output_tokens":0}}}`,
			wantSeen: true,
		},
		{
			name:     "usage on incomplete terminal",
			terminal: `{"response":{"output":[],"status":"incomplete","usage":{"input_tokens":12,"output_tokens":3}}}`,
			want:     TokenUsage{Prompt: 12, Completion: 3},
			wantSeen: true,
		},
		{
			name:     "no usage on completed means no usage",
			terminal: `{"response":{"output":[]}}`,
			wantSeen: false,
		},
		{
			name:     "null usage means no usage",
			terminal: `{"response":{"output":[],"usage":null}}`,
			wantSeen: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sse := codexFrame("response.output_text.delta", `{"delta":"hi"}`) +
				codexFrame("response.completed", tc.terminal)
			msg, rec, err := codexRunStream(sse)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != "hi" {
				t.Fatalf("content = %q, want hi", msg.Content)
			}
			usage, ok := rec.reportedUsage()
			if ok != tc.wantSeen || usage != tc.want {
				t.Fatalf("usage = (%+v, %v), want (%+v, %v)", usage, ok, tc.want, tc.wantSeen)
			}
		})
	}
}

func TestConsumeCodexStreamUsageIgnoredBeforeTerminal(t *testing.T) {
	// Only the terminal event's usage counts; usage-shaped payloads on
	// earlier events are ignored, and a stream that never terminates reports
	// no usage even after emitting a usage object.
	sse := codexFrame("response.created", `{"response":{"id":"r","usage":{"input_tokens":9,"output_tokens":9}}}`)
	_, rec, err := codexRunStream(sse)
	if err == nil {
		t.Fatal("stream without a terminal event must fail")
	}
	if usage, ok := rec.reportedUsage(); ok || usage != (TokenUsage{}) {
		t.Fatalf("usage = (%+v, %v), want zero/false", usage, ok)
	}
}

func TestConsumeCodexStreamDone(t *testing.T) {
	sse := codexFrame("response.output_text.delta", `{"delta":"hi"}`) + "data: [DONE]\n\n"
	msg, _, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: [DONE] must terminate successfully: %v", err)
	}
	if msg.Content != "hi" {
		t.Errorf("content = %q, want hi", msg.Content)
	}
}

func TestConsumeCodexStreamErrorEvent(t *testing.T) {
	sse := codexFrame("error", `{"code":"server_error","message":"boom"}`)
	_, _, err := codexRunStream(sse)
	codexWantError(t, err, "boom")
}

func TestConsumeCodexStreamResponseFailed(t *testing.T) {
	cases := []struct {
		name string
		sse  string
		want string
	}{
		{"nested response error", codexFrame("response.failed", `{"response":{"error":{"code":"timeout","message":"took too long"}}}`), "took too long"},
		{"bare error object", codexFrame("response.failed", `{"error":{"message":"model dropped"}}`), "model dropped"},
		{"string error", codexFrame("response.failed", `{"error":"plain failure"}`), "plain failure"},
		{"payload message", codexFrame("response.failed", `{"message":"direct message"}`), "direct message"},
		{"payload code only", codexFrame("response.failed", `{"code":"timeout_only"}`), "timeout_only"},
		{"error event nested", codexFrame("error", `{"response":{"error":{"code":"bad","message":"very bad"}}}`), "very bad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := codexRunStream(tc.sse)
			codexWantError(t, err, tc.want)
		})
	}
}

func TestConsumeCodexStreamEarlyEOF(t *testing.T) {
	sse := codexFrame("response.output_text.delta", `{"delta":"partial"}`) // no terminal
	_, _, err := codexRunStream(sse)
	codexWantError(t, err, "codex stream ended without a terminal event")
}

func TestConsumeCodexStreamEarlyEOFIncompleteSilence(t *testing.T) {
	// Nothing streamed at all, connection closes: still an error, never a
	// successful empty turn.
	_, _, err := codexRunStream(codexFrame("response.created", `{"response":{}}`))
	codexWantError(t, err, "codex stream ended without a terminal event")
}

func TestConsumeCodexStreamIncompleteTerminal(t *testing.T) {
	sse := codexFrame("response.output_text.delta", `{"delta":"partial answer"}`) +
		codexFrame("response.incomplete", `{"response":{"output":[],"status":"incomplete"}}`)
	msg, _, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: response.incomplete must terminate successfully: %v", err)
	}
	if msg.Content != "partial answer" {
		t.Errorf("content = %q, want partial answer", msg.Content)
	}
}

func TestConsumeCodexStreamMalformedPayload(t *testing.T) {
	sse := codexFrame("response.output_text.delta", `{not json}`)
	_, _, err := codexRunStream(sse)
	codexWantError(t, err, "malformed codex stream")
}

func TestConsumeCodexStreamMalformedFunctionPayload(t *testing.T) {
	sse := codexFrame("response.function_call_arguments.delta", `{"item_id":not-a-json}`)
	_, _, err := codexRunStream(sse)
	codexWantError(t, err, "malformed codex stream")
}

func TestConsumeCodexStreamContextCancellation(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		for {
			err := context.DeadlineExceeded // placeholder write
			_ = err
			pw.Write([]byte(codexFrame("response.output_text.delta", `{"delta":"x"}`)))
			time.Sleep(20 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := ConsumeCodexStream(ctx, pr, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	pw.Close()
}

func TestConsumeCodexStreamOversizedLine(t *testing.T) {
	// >1 MiB single line: clean failure, not unbounded buffering.
	sse := codexFrame("response.output_text.delta", `{"delta":"`+strings.Repeat("a", 1100*1024)+`"}`) +
		codexFrame("response.completed", `{"response":{"output":[]}}`)
	_, _, err := codexRunStream(sse)
	codexWantError(t, err, "reading codex stream")
}

func TestConsumeCodexStreamUnknownEventsIgnored(t *testing.T) {
	sse := codexFrame("response.some_future_event", `{"mystery":{"nested":"value"}}`) +
		codexFrame("response.created", `{"response":{"id":"r"}}`) +
		codexFrame("response.output_text.delta", `{"delta":"ok"}`) +
		codexFrame("response.completed", `{"response":{"output":[]}}`)
	msg, _, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if msg.Content != "ok" {
		t.Errorf("content = %q, want ok", msg.Content)
	}
}

func TestConsumeCodexStreamErrorEventWithoutData(t *testing.T) {
	// A named error event with no payload must fail, not silently pass.
	sse := "event: error\n\n"
	_, _, err := codexRunStream(sse)
	codexWantError(t, err, "codex stream reported an error")
}

func TestConsumeCodexStreamBlankContentOnlyEvent(t *testing.T) {
	// comment lines and unknown fields are ignored
	sse := ": keepalive\n\n" +
		"id: 1\nretry: 100\n" +
		codexFrame("response.output_text.delta", `{"delta":"still alive"}`) +
		codexFrame("response.completed", `{"response":{"output":[]}}`)
	msg, _, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if msg.Content != "still alive" {
		t.Errorf("content = %q, want still alive", msg.Content)
	}
}

func TestConsumeCodexStreamNilCallbacks(t *testing.T) {
	sse := codexFrame("response.output_text.delta", `{"delta":"quiet"}`) +
		codexFrame("response.reasoning_text.delta", `{"delta":"deep"}`) +
		codexFrame("response.completed", `{"response":{"output":[]}}`)
	msg, _, _, err := ConsumeCodexStream(context.Background(), strings.NewReader(sse), nil, nil)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if msg.Content != "quiet" || msg.Reasoning != "deep" {
		t.Errorf("message = %+v, want quiet/deep", msg)
	}
}

func TestConsumeCodexStreamRealWorldTurn(t *testing.T) {
	// One whole turn: reasoning summary, text, two function calls where the
	// second arrives with its name filled only at completed time.
	sse := codexFrame("response.created", `{"response":{"id":"resp_1","model":"codex-mini"}}`) +
		codexFrame("response.output_item.added", `{"item":{"type":"reasoning","id":"rs_1"}}`) +
		codexFrame("response.reasoning_summary_text.delta", `{"delta":"checking "}`) +
		codexFrame("response.reasoning_summary_text.delta", `{"delta":"files"}`) +
		codexFrame("response.output_item.added", `{"item":{"type":"message","id":"msg_1"}}`) +
		codexFrame("response.output_text.delta", `{"delta":"Found "}`) +
		codexFrame("response.output_text.delta", `{"delta":"three files"}`) +
		codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_z9","name":"read_file","arguments":""}}`) +
		codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_2","call_id":"call_k1","name":"list_dir","arguments":"{}"}}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_1","delta":"{\"path\":\"/x\"}"}`) +
		codexFrame("response.output_item.done", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_z9","name":"read_file","arguments":"{\"path\":\"/x\"}"}}`) +
		codexFrame("response.completed", `{"response":{"output":[{"type":"reasoning","id":"rs_1"},{"type":"message","id":"msg_1"},{"type":"function_call","call_id":"call_z9","name":"read_file","arguments":"{\"path\":\"/x\"}"},{"type":"function_call","call_id":"call_k1","name":"list_dir","arguments":"{}"}]}}`)
	msg, rec, err := codexRunStream(sse)
	if err != nil {
		t.Fatalf("ConsumeCodexStream: %v", err)
	}
	if msg.Content != "Found three files" {
		t.Errorf("content = %q, want Found three files", msg.Content)
	}
	if msg.Reasoning != "checking files" {
		t.Errorf("reasoning = %q, want checking files", msg.Reasoning)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want two", msg.ToolCalls)
	}
	if msg.ToolCalls[0].ID != "call_z9" || msg.ToolCalls[0].Name != "read_file" || msg.ToolCalls[0].Arguments != `{"path":"/x"}` {
		t.Errorf("tool call 0 = %+v, want read_file with path /x", msg.ToolCalls[0])
	}
	if msg.ToolCalls[1].ID != "call_k1" || msg.ToolCalls[1].Name != "list_dir" || msg.ToolCalls[1].Arguments != `{}` {
		t.Errorf("tool call 1 = %+v, want list_dir with {}", msg.ToolCalls[1])
	}
	if got, want := rec.texts(), []string{"Found ", "three files"}; strings.Join(got, "") != strings.Join(want, "") {
		t.Errorf("text deltas = %q, want %q", got, want)
	}
	if got, want := rec.reasons(), []string{"checking ", "files"}; strings.Join(got, "") != strings.Join(want, "") {
		t.Errorf("reasoning deltas = %q, want %q", got, want)
	}
}

func TestCodexStreamReadCapturedDump(t *testing.T) {
	// A captured session dump, when provided via the environment, must parse
	// end to end. Skipping otherwise keeps the suite hermetic.
	path := os.Getenv("LISA_CODEX_DUMP")
	if path == "" {
		t.Skip("no LISA_CODEX_DUMP provided")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if _, _, _, err := ConsumeCodexStream(context.Background(), strings.NewReader(string(data)), nil, nil); err != nil {
		t.Fatalf("ConsumeCodexStream on captured dump: %v", err)
	}
}
