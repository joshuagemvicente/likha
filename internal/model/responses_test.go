package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResponsesSIWCRequestContract(t *testing.T) {
	parameters := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer"}},"required":["path"]}`)
	payload, err := BuildCodexRequest("gpt-6.1-sol", []Message{
		{Role: "system", Content: "Use local tools."},
		{Role: "user", Content: "Read a file."},
		{Role: "assistant", Content: "Checking.", Reasoning: "not replayable", ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"a.txt"}`}}},
		{Role: "tool", ToolCallID: "call_1", Content: ""},
		{Role: "developer", Content: "Now summarize."},
		{Role: "user", Content: "What did it say?"},
	}, []ToolDefinition{{Name: "read", Description: "Read a repository file.", Parameters: parameters}})
	if err != nil {
		t.Fatal(err)
	}
	body := codexRequestMap(t, payload)
	if body["model"] != "gpt-6.1-sol" || body["store"] != false || body["stream"] != true || body["instructions"] != "Use local tools." {
		t.Fatalf("SIWC request fields = %s", payload)
	}
	for _, field := range []string{"previous_response_id", "temperature", "top_p", "max_output_tokens", "metadata", "background", "conversation", "max_tool_calls", "moderation", "multi_agent", "prompt", "prompt_cache_retention", "safety_identifier", "top_logprobs", "truncation", "user"} {
		if _, ok := body[field]; ok {
			t.Errorf("unsupported field %q emitted", field)
		}
	}
	items := codexInputMaps(t, body)
	if len(items) != 6 || items[4]["role"] != "developer" || items[5]["role"] != "user" {
		t.Fatalf("full ordered history = %+v", items)
	}
	if items[2]["namespace"] != "likha" || items[2]["name"] != "read" || items[2]["type"] != "function_call" {
		t.Fatalf("namespaced replay = %+v", items[2])
	}
	if output, ok := items[3]["output"]; !ok || output != "" {
		t.Fatalf("empty tool output must be present: %+v", items[3])
	}
	if strings.Contains(string(payload), "not replayable") {
		t.Fatal("display-only reasoning leaked into request")
	}
	topTools := body["tools"].([]any)
	if len(topTools) != 1 {
		t.Fatalf("top-level tools = %+v", topTools)
	}
	namespace := topTools[0].(map[string]any)
	if namespace["type"] != "namespace" || namespace["name"] != "likha" || namespace["description"] == "" {
		t.Fatalf("namespace schema = %+v", namespace)
	}
	tool := namespace["tools"].([]any)[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "read" || tool["strict"] != false {
		t.Fatalf("function schema = %+v", tool)
	}
	gotParameters, _ := json.Marshal(tool["parameters"])
	var got, want any
	_ = json.Unmarshal(gotParameters, &got)
	_ = json.Unmarshal(parameters, &want)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("optional parameter schema changed: %s, want %s", gotJSON, wantJSON)
	}
}

func TestResponsesNoToolsAndNoHistoryAreArrays(t *testing.T) {
	payload, err := BuildCodexRequest("gpt-6.1-sol", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := codexRequestMap(t, payload)
	for _, field := range []string{"tools", "input"} {
		items, ok := body[field].([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("%s = %#v, want []", field, body[field])
		}
	}
}

func TestResponsesOnlyCompletedSucceeds(t *testing.T) {
	cases := []struct {
		name, terminal string
	}{
		{"DONE", "data: [DONE]\n\n"},
		{"EOF", ""},
		{"incomplete", codexFrame("response.incomplete", `{"response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`)},
		{"failed after streaming", codexFrame("response.failed", `{"response":{"status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded"}}}`)},
		{"invalid completed status", codexFrame("response.completed", `{"response":{"status":"in_progress","output":[]}}`)},
		{"missing completed status", codexFrame("response.completed", `{"response":{"output":[]}}`)},
		{"missing response", codexFrame("response.completed", `{}`)},
		{"truncated completed frame", "event: response.completed\ndata: {\"response\":{\"status\":\"completed\",\"output\":[]}}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var chunks []string
			message, usage, ok, err := ConsumeCodexStream(context.Background(), strings.NewReader(
				codexFrame("response.output_text.delta", `{"delta":"partial"}`)+tc.terminal), func(s string) { chunks = append(chunks, s) }, nil)
			if err == nil || message.Role != "" || message.Content != "" || len(message.ToolCalls) != 0 || ok || usage != (TokenUsage{}) {
				t.Fatalf("partial turn accepted: message=%+v usage=%+v ok=%v err=%v", message, usage, ok, err)
			}
			if strings.Join(chunks, "") != "partial" {
				t.Fatalf("incremental text was not delivered: %v", chunks)
			}
		})
	}
}

func TestResponsesCompletedTextWithoutDeltas(t *testing.T) {
	message, _, err := codexRunStream(codexFrame("response.completed", `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-6.1-sol","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello, world!","annotations":[]}]}]}}`))
	if err != nil || message.Content != "Hello, world!" {
		t.Fatalf("completed text = %+v, err=%v", message, err)
	}
}

func TestResponsesCanonicalNamespacedToolTurnAndReplay(t *testing.T) {
	// This follows the public function-calling event/schema examples, with the
	// fixed namespace required by SIWC and the signed-in model used in its docs.
	sse := codexFrame("response.created", `{"type":"response.created","response":{"id":"resp_1","model":"gpt-6.1-sol","status":"in_progress","output":[]}}`) +
		codexFrame("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":"Checking files."}`) +
		codexFrame("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"Checking "}`) +
		codexFrame("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"a.txt."}`) +
		codexFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","namespace":"likha","arguments":"","status":"in_progress"}}`) +
		codexFrame("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"{\"pa"}`) +
		codexFrame("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"th\":\"a.txt\"}"}`) +
		codexFrame("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":2,"arguments":"{\"path\":\"a.txt\"}"}`) +
		codexFrame("response.output_item.done", `{"type":"response.output_item.done","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","namespace":"likha","arguments":"{\"path\":\"a.txt\"}","status":"completed"}}`) +
		codexFrame("response.completed", `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-6.1-sol","status":"completed","error":null,"output":[{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"Checking files."}]},{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Checking a.txt.","annotations":[]}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","namespace":"likha","arguments":"{\"path\":\"a.txt\"}","status":"completed"}],"usage":{"input_tokens":5000,"output_tokens":900,"total_tokens":5900,"input_tokens_details":{"cached_tokens":4096},"output_tokens_details":{"reasoning_tokens":640}}}}`)
	tools := []ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}}
	var text, reason []string
	message, report, err := consumeCodexStreamDetailed(context.Background(), strings.NewReader(sse), func(s string) { text = append(text, s) }, func(s string) { reason = append(reason, s) }, tools)
	if err != nil {
		t.Fatal(err)
	}
	if message.Role != "assistant" || message.Content != "Checking a.txt." || message.Reasoning != "Checking files." || len(message.ToolCalls) != 1 {
		t.Fatalf("canonical response = %+v", message)
	}
	if got := message.ToolCalls[0]; got.ID != "call_1" || got.Name != "read" || got.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("namespace was not mapped back to local registry name: %+v", got)
	}
	if strings.Join(text, "|") != "Checking |a.txt." || strings.Join(reason, "|") != "Checking files." {
		t.Fatalf("incremental callbacks duplicated or lost text: text=%v reasoning=%v", text, reason)
	}
	wantUsage := RequestUsage{Prompt: 5000, Completion: 900, PromptSeen: true, CompletionSeen: true, CacheRead: 4096, Reasoning: 640}
	if !report.ok || report.request() != wantUsage {
		t.Fatalf("usage metadata = %+v, want %+v", report.request(), wantUsage)
	}
	payload, err := BuildCodexRequest("gpt-6.1-sol", []Message{{Role: "user", Content: "Read a.txt."}, message,
		{Role: "tool", ToolCallID: "call_1", Content: "hello\nworld"}, {Role: "user", Content: "Summarize it."}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	items := codexInputMaps(t, codexRequestMap(t, payload))
	if len(items) != 5 || items[2]["namespace"] != "likha" || items[2]["name"] != "read" || items[3]["output"] != "hello\nworld" || items[4]["role"] != "user" {
		t.Fatalf("stateless full-history replay lost calls/results: %s", payload)
	}
}

func responsesCompletedItem(t *testing.T, item map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"id": "resp_1", "model": "gpt-6.1-sol", "status": "completed", "output": []any{item}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return codexFrame("response.completed", string(raw))
}

func TestResponsesUntrustedToolCallsRejected(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"missing call ID", func(item map[string]any) { delete(item, "call_id") }, "missing a call ID"},
		{"blank call ID", func(item map[string]any) { item["call_id"] = " " }, "invalid ID"},
		{"control call ID", func(item map[string]any) { item["call_id"] = "call\n1" }, "invalid ID"},
		{"missing name", func(item map[string]any) { delete(item, "name") }, "missing a call ID or name"},
		{"qualified name", func(item map[string]any) { item["name"] = "likha.read" }, "invalid tool name"},
		{"whitespace name", func(item map[string]any) { item["name"] = " read" }, "invalid tool name"},
		{"unicode name", func(item map[string]any) { item["name"] = "rеad" }, "invalid tool name"},
		{"overlong name", func(item map[string]any) { item["name"] = strings.Repeat("a", 65) }, "invalid tool name"},
		{"unknown name", func(item map[string]any) { item["name"] = "not_advertised" }, "unadvertised tool"},
		{"foreign namespace", func(item map[string]any) { item["namespace"] = "external" }, "unknown namespace"},
		{"namespace injection", func(item map[string]any) { item["namespace"] = "likha\n" }, "unknown namespace"},
		{"invalid item ID", func(item map[string]any) { item["id"] = "fc\n1" }, "invalid item ID"},
		{"incomplete call", func(item map[string]any) { item["status"] = "incomplete" }, "invalid completion status"},
		{"missing arguments", func(item map[string]any) { delete(item, "arguments") }, "non-JSON-object arguments"},
		{"malformed arguments", func(item map[string]any) { item["arguments"] = "{" }, "non-JSON-object arguments"},
		{"array arguments", func(item map[string]any) { item["arguments"] = "[]" }, "non-JSON-object arguments"},
		{"null arguments", func(item map[string]any) { item["arguments"] = "null" }, "non-JSON-object arguments"},
		{"scalar arguments", func(item map[string]any) { item["arguments"] = "1" }, "non-JSON-object arguments"},
		{"object instead of encoded arguments", func(item map[string]any) { item["arguments"] = map[string]any{} }, "malformed Responses stream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "read", "namespace": "likha", "arguments": "{}", "status": "completed"}
			tc.edit(item)
			message, usage, ok, err := ConsumeCodexStream(context.Background(), strings.NewReader(responsesCompletedItem(t, item)), nil, nil, []ToolDefinition{{Name: "read"}})
			codexWantError(t, err, tc.want)
			if message.Role != "" || len(message.ToolCalls) != 0 || ok || usage != (TokenUsage{}) {
				t.Fatalf("untrusted call leaked into result: %+v, %+v, %v", message, usage, ok)
			}
		})
	}
}

func TestResponsesOptionalNamespaceAndMCPRegistryNames(t *testing.T) {
	const name = "mcp__filesystem__read_file__0abc"
	for _, namespace := range []string{"", "likha"} {
		t.Run(namespace, func(t *testing.T) {
			item := map[string]any{"type": "function_call", "call_id": "call_1", "name": name, "arguments": "{}"}
			if namespace != "" {
				item["namespace"] = namespace
			}
			message, _, _, err := ConsumeCodexStream(context.Background(), strings.NewReader(responsesCompletedItem(t, item)), nil, nil, []ToolDefinition{{Name: name}})
			if err != nil || len(message.ToolCalls) != 1 || message.ToolCalls[0].Name != name {
				t.Fatalf("local MCP name changed: %+v, %v", message, err)
			}
			payload, err := BuildCodexRequest("gpt-6.1-sol", []Message{message}, nil)
			if err != nil {
				t.Fatal(err)
			}
			item = codexInputMaps(t, codexRequestMap(t, payload))[0]
			if item["namespace"] != "likha" || item["name"] != name {
				t.Fatalf("namespace not reconstructed on replay: %s", payload)
			}
		})
	}
}

func TestResponsesExplicitEmptyRegistryRejectsTools(t *testing.T) {
	sse := responsesCompletedItem(t, map[string]any{"type": "function_call", "call_id": "call_1", "name": "read", "namespace": "likha", "arguments": "{}"})
	_, _, _, err := ConsumeCodexStream(context.Background(), strings.NewReader(sse), nil, nil, nil)
	codexWantError(t, err, "unadvertised tool")
}

func TestResponsesSSEFraming(t *testing.T) {
	// Data is assembled across all lines and dispatched only at a blank line.
	// event can appear after data; its last value in the frame is authoritative.
	stream := "\ufeff: keepalive\n\n" +
		"event: ping\n\n" +
		"event: response.future_event\ndata: not JSON, deliberately ignored\n\n" +
		"id: event_1\nretry: 1000\nunknown: ignored\n" +
		"event: overridden\ndata:\ndata: {\"type\":\"response.output_text.delta\",\ndata: \"delta\":\"Hello\"}\nevent: response.output_text.delta\n\n" +
		"data: {\"type\":\"response.completed\",\ndata: \"response\":{\"model\":\"gpt-6.1-sol\",\"status\":\"completed\",\"output\":[]}}\n\n"
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(ending, func(t *testing.T) {
			sse := strings.ReplaceAll(stream, "\n", ending)
			message, _, _, err := ConsumeCodexStream(context.Background(), responsesOneByteReader{strings.NewReader(sse)}, nil, nil)
			if err != nil || message.Content != "Hello" {
				t.Fatalf("SSE framing result=%+v err=%v", message, err)
			}
		})
	}
}

type responsesOneByteReader struct{ io.Reader }

func (r responsesOneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestResponsesMalformedFramesRejected(t *testing.T) {
	for name, frame := range map[string]string{
		"invalid JSON":           codexFrame("response.output_text.delta", `{not json}`),
		"scalar payload":         codexFrame("response.output_text.delta", `"text"`),
		"null payload":           codexFrame("response.output_text.delta", `null`),
		"array payload":          codexFrame("response.output_text.delta", `[]`),
		"missing delta":          codexFrame("response.output_text.delta", `{}`),
		"non-string delta":       codexFrame("response.output_text.delta", `{"delta":1}`),
		"mismatched event types": codexFrame("response.completed", `{"type":"response.incomplete","response":{"status":"completed","output":[]}}`),
		"no frame separator":     "event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\nevent: response.completed\ndata: {\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n",
		"missing output":         codexFrame("response.completed", `{"response":{"status":"completed"}}`),
		"null output":            codexFrame("response.completed", `{"response":{"status":"completed","output":null}}`),
		"object output":          codexFrame("response.completed", `{"response":{"status":"completed","output":{}}}`),
		"null output item":       codexFrame("response.completed", `{"response":{"status":"completed","output":[null]}}`),
		"completed error":        codexFrame("response.completed", `{"response":{"status":"completed","output":[],"error":{"message":"failed"}}}`),
		"empty completion":       "event: response.completed\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			message, _, err := codexRunStream(frame)
			codexWantError(t, err, "malformed Responses stream")
			if message.Role != "" || len(message.ToolCalls) != 0 {
				t.Fatalf("malformed frame returned a result: %+v", message)
			}
		})
	}
}

func TestResponsesArgumentEventsValidatedBeforeCompletion(t *testing.T) {
	added := codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","namespace":"likha","arguments":""}}`)
	completed := codexFrame("response.completed", `{"response":{"status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","namespace":"likha","arguments":"{}"}]}}`)
	for name, frame := range map[string]string{
		"missing item ID":       codexFrame("response.function_call_arguments.delta", `{"delta":"{}"}`),
		"unknown item ID":       codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_other","delta":"{}"}`),
		"missing fragment":      codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_1"}`),
		"wrong fragment type":   codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_1","delta":{}}`),
		"missing done args":     codexFrame("response.function_call_arguments.done", `{"item_id":"fc_1"}`),
		"malformed done args":   codexFrame("response.function_call_arguments.done", `{"item_id":"fc_1","arguments":"{broken"}`),
		"nonobject done args":   codexFrame("response.function_call_arguments.done", `{"item_id":"fc_1","arguments":"null"}`),
		"done unknown item":     codexFrame("response.function_call_arguments.done", `{"item_id":"fc_other","arguments":"{}"}`),
		"delta after done":      codexFrame("response.function_call_arguments.done", `{"item_id":"fc_1","arguments":"{}"}`) + codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_1","delta":"{}"}`),
		"done changed identity": codexFrame("response.output_item.done", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_other","name":"read","namespace":"likha","arguments":"{}"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			message, _, err := codexRunStream(added + frame + completed)
			if err == nil || message.Role != "" || len(message.ToolCalls) != 0 {
				t.Fatalf("bad argument event accepted: %+v, %v", message, err)
			}
		})
	}
}

func TestResponsesIncompleteNeverReturnsPartialToolsOrUsage(t *testing.T) {
	partial := codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","namespace":"likha","arguments":""}}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_1","delta":"{}"}`) +
		codexFrame("response.function_call_arguments.done", `{"item_id":"fc_1","arguments":"{}"}`)
	for name, terminal := range map[string]string{
		"EOF":                             "",
		"DONE":                            "data: [DONE]\n\n",
		"incomplete with tool and usage":  codexFrame("response.incomplete", `{"response":{"status":"incomplete","output":[{"type":"function_call","call_id":"call_1","name":"read","namespace":"likha","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":1},"incomplete_details":{"reason":"max_output_tokens"}}}`),
		"failed":                          codexFrame("response.failed", `{"response":{"status":"failed","error":{"code":"subscription_sharing_usage_unavailable","message":"Plan usage unavailable"},"usage":{"input_tokens":10,"output_tokens":1}}}`),
		"error":                           codexFrame("error", `{"type":"error","code":"server_error","message":"Failed after a tool"}`),
		"completed missing streamed tool": codexFrame("response.completed", `{"response":{"status":"completed","output":[]}}`),
	} {
		t.Run(name, func(t *testing.T) {
			message, report, err := consumeCodexStreamDetailed(context.Background(), strings.NewReader(partial+terminal), nil, nil, []ToolDefinition{{Name: "read"}})
			if err == nil || message.Role != "" || len(message.ToolCalls) != 0 || report.ok || report.request() != (RequestUsage{}) {
				t.Fatalf("partial tool/usage leaked: %+v, %+v, %v", message, report, err)
			}
		})
	}
}

func TestResponsesStreamBounds(t *testing.T) {
	t.Run("multiline frame bound", func(t *testing.T) {
		frame := "event: response.completed\n" + strings.Repeat("data: "+strings.Repeat(" ", maxResponsesEventBytes/4)+"\n", 5) + "\n"
		_, _, err := codexRunStream(frame)
		codexWantError(t, err, "SSE event exceeds")
	})
	t.Run("whole stream bound including keepalive", func(t *testing.T) {
		comment := ": " + strings.Repeat("a", 1024) + "\n\n"
		stream := strings.Repeat(comment, maxResponsesStreamBytes/len(comment)+1) +
			codexFrame("response.completed", `{"response":{"status":"completed","output":[]}}`)
		_, _, err := codexRunStream(stream)
		codexWantError(t, err, "byte limit")
	})
}

func TestResponsesCancellationClosesBlockedReader(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	closable := &responsesClosingReader{Reader: reader, closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = io.WriteString(writer, codexFrame("response.output_text.delta", `{"delta":"partial"}`)) }()
	message, _, _, err := ConsumeCodexStream(ctx, closable, func(string) { cancel() }, nil)
	if !errors.Is(err, context.Canceled) || message.Role != "" {
		t.Fatalf("cancellation returned a partial turn: %+v, %v", message, err)
	}
	select {
	case <-closable.closed:
	case <-time.After(time.Second):
		t.Fatal("blocked reader was not closed on cancellation")
	}
}

type responsesClosingReader struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (r *responsesClosingReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return r.Reader.(io.Closer).Close()
}

func TestResponsesCallbacksAreIncremental(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	chunk := make(chan string, 1)
	done := make(chan struct{})
	var message Message
	var err error
	go func() {
		defer close(done)
		message, _, _, err = ConsumeCodexStream(ctx, reader, func(s string) { chunk <- s }, nil)
	}()
	if _, err := io.WriteString(writer, codexFrame("response.output_text.delta", `{"delta":"incremental"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-chunk:
		if got != "incremental" {
			t.Fatalf("chunk = %q", got)
		}
	case <-ctx.Done():
		t.Fatal("text was buffered until completion")
	}
	if _, err := io.WriteString(writer, codexFrame("response.completed", `{"response":{"status":"completed","output":[]}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("completion was not recognized while body remained open")
	}
	if err != nil || message.Content != "incremental" {
		t.Fatalf("stream result=%+v err=%v", message, err)
	}
}

func TestResponsesRequestDefinitionValidation(t *testing.T) {
	for _, parameters := range []string{`null`, `[]`, `true`, `"schema"`, `1`, `{"type":"string"}`, `{"type":["object","null"]}`} {
		t.Run(parameters, func(t *testing.T) {
			_, err := BuildCodexRequest("gpt-6.1-sol", nil, []ToolDefinition{{Name: "read", Parameters: json.RawMessage(parameters)}})
			codexWantError(t, err, "invalid tool definition")
		})
	}
	for _, name := range []string{"", "likha.read", "read\n", "réad", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			_, err := BuildCodexRequest("gpt-6.1-sol", nil, []ToolDefinition{{Name: name, Parameters: json.RawMessage(`{}`)}})
			codexWantError(t, err, "invalid tool definition")
		})
	}
	_, err := BuildCodexRequest("gpt-6.1-sol", nil, []ToolDefinition{{Name: "read", Parameters: json.RawMessage(`{}`)}, {Name: "read", Parameters: json.RawMessage(`{}`)}})
	codexWantError(t, err, "duplicate tool definition")
	_, err = BuildCodexRequest(" ", nil, nil)
	codexWantError(t, err, "model name is required")
}

func TestResponsesRequestPreservesEmptyMessagesAndSystemOrder(t *testing.T) {
	payload, err := BuildCodexRequest("gpt-6.1-sol", []Message{{Role: "system", Content: "first"}, {Role: "user"},
		{Role: "system", Content: "second"}, {Role: "developer"}, {Role: "assistant"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := codexRequestMap(t, payload)
	if body["instructions"] != "first\n\nsecond" {
		t.Fatalf("system instruction order changed: %s", payload)
	}
	items := codexInputMaps(t, body)
	if len(items) != 3 {
		t.Fatalf("empty history messages were dropped: %s", payload)
	}
	for i, role := range []string{"user", "developer", "assistant"} {
		parts := codexContentParts(t, body, i)
		if items[i]["role"] != role || parts[0]["type"] != "input_text" || parts[0]["text"] != "" {
			t.Fatalf("empty message %d = %+v", i, items[i])
		}
	}
}

func TestResponsesAddedCallValidation(t *testing.T) {
	for name, item := range map[string]string{
		"missing item ID":   `{"type":"function_call","call_id":"call_1","name":"read","arguments":""}`,
		"missing call ID":   `{"type":"function_call","id":"fc_1","name":"read","arguments":""}`,
		"missing name":      `{"type":"function_call","id":"fc_1","call_id":"call_1","arguments":""}`,
		"invalid name":      `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"likha.read","arguments":""}`,
		"foreign namespace": `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","namespace":"foreign","arguments":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			message, _, err := codexRunStream(codexFrame("response.output_item.added", `{"item":`+item+`}`) +
				codexFrame("response.completed", `{"response":{"status":"completed","output":[]}}`))
			if err == nil || message.Role != "" {
				t.Fatalf("invalid added tool call accepted: %+v, %v", message, err)
			}
		})
	}
}

func TestResponsesDuplicateAndChangedIdentities(t *testing.T) {
	added := codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`)
	for name, sse := range map[string]string{
		"duplicate streamed item":     added + added,
		"duplicate streamed call ID":  added + codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_2","call_id":"call_1","name":"read","arguments":""}}`),
		"duplicate completed item ID": codexFrame("response.completed", `{"response":{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{}"},{"type":"function_call","id":"fc_1","call_id":"call_2","name":"read","arguments":"{}"}]}}`),
		"duplicate completed call ID": codexFrame("response.completed", `{"response":{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read","arguments":"{}"},{"type":"function_call","call_id":"call_1","name":"read","arguments":"{}"}]}}`),
		"changed completed name":      added + codexFrame("response.completed", `{"response":{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"grep","arguments":"{}"}]}}`),
		"changed completed item ID":   added + codexFrame("response.completed", `{"response":{"status":"completed","output":[{"type":"function_call","id":"fc_2","call_id":"call_1","name":"read","arguments":"{}"}]}}`),
		"changed completed call ID":   added + codexFrame("response.completed", `{"response":{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_2","name":"read","arguments":"{}"}]}}`),
	} {
		t.Run(name, func(t *testing.T) {
			message, _, err := codexRunStream(sse)
			if err == nil || message.Role != "" || len(message.ToolCalls) != 0 {
				t.Fatalf("ambiguous tool identity accepted: %+v, %v", message, err)
			}
		})
	}
}

func TestResponsesInterleavedToolsFollowCompletedOutputOrder(t *testing.T) {
	sse := codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","namespace":"likha","arguments":""}}`) +
		codexFrame("response.output_item.added", `{"item":{"type":"function_call","id":"fc_2","call_id":"call_2","name":"grep","namespace":"likha","arguments":""}}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_1","delta":"{\"path\":"}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_2","delta":"{\"pattern\":\"text\"}"}`) +
		codexFrame("response.function_call_arguments.delta", `{"item_id":"fc_1","delta":"\"a.txt\"}"}`) +
		codexFrame("response.function_call_arguments.done", `{"item_id":"fc_2","arguments":"{\"pattern\":\"text\"}"}`) +
		codexFrame("response.output_item.done", `{"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","namespace":"likha","arguments":"{\"path\":\"a.txt\"}","status":"completed"}}`) +
		codexFrame("response.completed", `{"response":{"model":"gpt-6.1-sol","status":"completed","output":[{"type":"function_call","id":"fc_2","call_id":"call_2","name":"grep","namespace":"likha","arguments":"{\"pattern\":\"text\"}"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","namespace":"likha","arguments":"{\"path\":\"a.txt\"}"}]}}`)
	message, _, _, err := ConsumeCodexStream(context.Background(), strings.NewReader(sse), nil, nil, []ToolDefinition{{Name: "read"}, {Name: "grep"}})
	if err != nil || len(message.ToolCalls) != 2 {
		t.Fatalf("interleaved response=%+v err=%v", message, err)
	}
	if message.ToolCalls[0].ID != "call_2" || message.ToolCalls[0].Arguments != `{"pattern":"text"}` || message.ToolCalls[1].ID != "call_1" || message.ToolCalls[1].Arguments != `{"path":"a.txt"}` {
		t.Fatalf("completed order/arguments changed: %+v", message.ToolCalls)
	}
}

func TestResponsesCompletedOnlySummaryAndRefusal(t *testing.T) {
	sse := codexFrame("response.completed", `{"response":{"model":"gpt-6.1-sol","status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Policy check."}]},{"type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I cannot do that."}]}]}}`)
	message, rec, err := codexRunStream(sse)
	if err != nil || message.Content != "I cannot do that." || message.Reasoning != "Policy check." {
		t.Fatalf("completed-only result=%+v err=%v", message, err)
	}
	if strings.Join(rec.texts(), "") != message.Content || strings.Join(rec.reasons(), "") != message.Reasoning {
		t.Fatal("completed-only text/summary were not delivered through callbacks")
	}
}

func TestResponsesTransportReadError(t *testing.T) {
	want := errors.New("read interrupted")
	reader := io.MultiReader(strings.NewReader(codexFrame("response.output_text.delta", `{"delta":"partial"}`)), responsesErrorReader{want})
	message, _, _, err := ConsumeCodexStream(context.Background(), reader, nil, nil)
	if !errors.Is(err, want) || message.Role != "" {
		t.Fatalf("read error result=%+v err=%v", message, err)
	}
}

type responsesErrorReader struct{ err error }

func (r responsesErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestResponsesCancellationDuringCompletedCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sse := codexFrame("response.completed", `{"response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"text"}]}],"usage":{"input_tokens":10,"output_tokens":1}}}`)
	message, usage, ok, err := ConsumeCodexStream(ctx, strings.NewReader(sse), func(string) { cancel() }, nil)
	if !errors.Is(err, context.Canceled) || message.Role != "" || ok || usage != (TokenUsage{}) {
		t.Fatalf("cancelled completion returned a result: %+v, %+v, %v, %v", message, usage, ok, err)
	}
}

func TestResponsesDetailedPromptOnlyAndZeroUsage(t *testing.T) {
	for name, tc := range map[string]struct {
		usage string
		want  RequestUsage
	}{
		"prompt only":                  {`{"input_tokens":12,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":7}}`, RequestUsage{Prompt: 12, PromptSeen: true, CacheRead: 4, Reasoning: 7}},
		"zero counts":                  {`{"input_tokens":0,"output_tokens":0}`, RequestUsage{PromptSeen: true, CompletionSeen: true}},
		"aliases and billing metadata": {`{"prompt_tokens":20,"completion_tokens":3,"cost":0.001,"prompt_tokens_details":{"cached_tokens":5,"cache_write_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}`, RequestUsage{Prompt: 20, Completion: 3, PromptSeen: true, CompletionSeen: true, CacheRead: 5, CacheWrite: 2, Reasoning: 1, Cost: 0.001, CostSeen: true}},
	} {
		t.Run(name, func(t *testing.T) {
			sse := codexFrame("response.completed", `{"response":{"status":"completed","output":[],"usage":`+tc.usage+`}}`)
			_, report, err := consumeCodexStreamDetailed(context.Background(), strings.NewReader(sse), nil, nil)
			if err != nil || !report.ok || report.request() != tc.want {
				t.Fatalf("usage=%+v ok=%v err=%v, want %+v", report.request(), report.ok, err, tc.want)
			}
		})
	}
}
