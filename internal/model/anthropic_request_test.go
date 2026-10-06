package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var readTool = ToolDefinition{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}

// buildBody builds a request and returns its body as a string.
func buildBody(t *testing.T, in anthropicInput) string {
	t.Helper()
	if in.Model == "" {
		in.Model = "claude-opus-5-5"
	}
	if in.MaxTokens == 0 {
		in.MaxTokens = 128000
	}
	request, err := buildAnthropicRequest(in)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	return string(request.Body)
}

// The acceptance conversation: harness system, AGENTS.md developer note,
// user, assistant with two tool calls, both results and a steer.
func TestBuildAnthropicRequestAcceptanceConversation(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "harness"},
		{Role: "developer", Content: "agents.md"},
		{Role: "user", Content: "fix it"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "c1", Name: "read", Arguments: `{"path":"a"}`},
			{ID: "c2", Name: "read", Arguments: `{"path":"b"}`},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "A"},
		{Role: "tool", ToolCallID: "c2", Content: "B"},
		{Role: "user", Content: "steer"},
	}
	got := buildBody(t, anthropicInput{Messages: messages, Tools: []ToolDefinition{readTool}})
	want := `{"model":"claude-opus-5-5","max_tokens":128000,` +
		`"system":[{"type":"text","text":"harness"},{"type":"text","text":"agents.md"}],` +
		`"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"fix it"}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"read","input":{"path":"a"}},{"type":"tool_use","id":"c2","name":"read","input":{"path":"b"}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"A"},{"type":"tool_result","tool_use_id":"c2","content":"B"},{"type":"text","text":"steer"}]}],` +
		`"tools":[{"name":"read","description":"Read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],` +
		`"cache_control":{"type":"ephemeral"},"stream":true}`
	if got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestBuildAnthropicRequestMapping(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "read", Arguments: `{"path":"a"}`}
	cases := []struct {
		name     string
		messages []Message
		noTools  bool
		want     string // the system and messages fields of the body
	}{
		{
			name: "harness, AGENTS.md and compaction summary are hoisted",
			messages: []Message{
				{Role: "system", Content: "harness"},
				{Role: "developer", Content: "agents"},
				{Role: "developer", Content: "Conversation summary (compacted)."},
				{Role: "user", Content: "go"},
			},
			want: `"system":[{"type":"text","text":"harness"},{"type":"text","text":"agents"},{"type":"text","text":"Conversation summary (compacted)."}],"messages":[{"role":"user","content":[{"type":"text","text":"go"}]}]`,
		},
		{
			name: "empty system content is skipped",
			messages: []Message{
				{Role: "system", Content: "harness"},
				{Role: "developer", Content: "  "},
				{Role: "user", Content: "go"},
			},
			want: `"system":[{"type":"text","text":"harness"}],"messages":[{"role":"user","content":[{"type":"text","text":"go"}]}]`,
		},
		{
			name: "checkpoint developer note lands after tool results",
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", ToolCalls: []ToolCall{call}},
				{Role: "tool", ToolCallID: "c1", Content: "A"},
				{Role: "developer", Content: "checkpoint"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"read","input":{"path":"a"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"A"},{"type":"text","text":"checkpoint"}]}]`,
		},
		{
			name: "results move before text placed earlier in the same turn",
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", ToolCalls: []ToolCall{call}},
				{Role: "developer", Content: "note"},
				{Role: "tool", ToolCallID: "c1", Content: "A"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"read","input":{"path":"a"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"A"},{"type":"text","text":"note"}]}]`,
		},
		{
			name: "empty tool output omits content and empty arguments become an object",
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "Looking.", ToolCalls: []ToolCall{{ID: "c1", Name: "list"}}},
				{Role: "tool", ToolCallID: "c1"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"text","text":"Looking."},{"type":"tool_use","id":"c1","name":"list","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1"}]}]`,
		},
		{
			name: "whitespace user and empty assistant are left out and neighbours merge",
			messages: []Message{
				{Role: "user", Content: "one"},
				{Role: "assistant", Content: " "},
				{Role: "user", Content: "\n\t"},
				{Role: "user", Content: "two"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}]`,
		},
		{
			name: "consecutive assistant messages form one turn",
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "partial"},
				{Role: "assistant", Content: "more"},
				{Role: "user", Content: "steer"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"text","text":"partial"},{"type":"text","text":"more"}]},` +
				`{"role":"user","content":[{"type":"text","text":"steer"}]}]`,
		},
		{
			name:    "without tools, tool history is text",
			noTools: true,
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "Reading.", ToolCalls: []ToolCall{call}},
				{Role: "tool", ToolCallID: "c1", Content: "A"},
				{Role: "user", Content: "summarize"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"text","text":"Reading."},{"type":"text","text":"[tool call read {\"path\":\"a\"}]"}]},` +
				`{"role":"user","content":[{"type":"text","text":"[tool result c1]\nA"},{"type":"text","text":"summarize"}]}]`,
		},
		{
			name: "IDs another wire issued are mapped onto Claude's pattern, call and result alike",
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", ToolCalls: []ToolCall{
					{ID: "functions.read:0", Name: "read", Arguments: `{"path":"a"}`},
					{ID: "functions.read:1", Name: "read", Arguments: `{"path":"b"}`},
				}},
				{Role: "tool", ToolCallID: "functions.read:0", Content: "A"},
				{Role: "tool", ToolCallID: "functions.read:1", Content: "B"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"functions_read_0_c216cab2","name":"read","input":{"path":"a"}},{"type":"tool_use","id":"functions_read_1_08bea971","name":"read","input":{"path":"b"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"functions_read_0_c216cab2","content":"A"},{"type":"tool_result","tool_use_id":"functions_read_1_08bea971","content":"B"}]}]`,
		},
		{
			name: "null arguments are sent as an empty object",
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "list", Arguments: "null"}}},
				{Role: "tool", ToolCallID: "c1", Content: "A"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"list","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"A"}]}]`,
		},
		{
			name:    "without tools, arguments that are not an object are shown as stored",
			noTools: true,
			messages: []Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "list", Arguments: "[1]"}, {ID: "c2", Name: "list", Arguments: "null"}}},
				{Role: "tool", ToolCallID: "c1", Content: "A"},
				{Role: "user", Content: "summarize"},
			},
			want: `"messages":[{"role":"user","content":[{"type":"text","text":"go"}]},` +
				`{"role":"assistant","content":[{"type":"text","text":"[tool call list [1]]"},{"type":"text","text":"[tool call list null]"}]},` +
				`{"role":"user","content":[{"type":"text","text":"[tool result c1]\nA"},{"type":"text","text":"summarize"}]}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := anthropicInput{Messages: tc.messages, Tools: []ToolDefinition{readTool}}
			if tc.noTools {
				in.Tools = nil
			}
			got := buildBody(t, in)
			if !strings.Contains(got, `,`+tc.want+`,`) {
				t.Fatalf("body %s\nlacks %s", got, tc.want)
			}
		})
	}
}

func TestBuildAnthropicRequestRejects(t *testing.T) {
	user := Message{Role: "user", Content: "go"}
	cases := []struct {
		name     string
		messages []Message
		tools    []ToolDefinition
		want     string
		is       error
	}{
		{name: "trailing assistant", messages: []Message{user, {Role: "assistant", Content: "prefill"}}, is: errClaudeEndsWithAssistant},
		{name: "nothing to send", messages: []Message{{Role: "system", Content: "harness"}, {Role: "user", Content: " "}}, is: errClaudeEndsWithAssistant},
		{name: "unknown role", messages: []Message{{Role: "robot", Content: "x"}}, want: `invalid message role "robot"`},
		{name: "tool result without id", messages: []Message{user, {Role: "tool", Content: "x"}}, want: "tool result requires a tool call ID"},
		{name: "array arguments", messages: []Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Name: "read", Arguments: `[1]`}}}, user}, tools: []ToolDefinition{readTool},
			want: "invalid assistant tool call read (c): Claude needs tool arguments as a JSON object; /compact continues from a summary"},
		{name: "call without id", messages: []Message{{Role: "assistant", ToolCalls: []ToolCall{{Name: "read", Arguments: `{}`}}}, user}, tools: []ToolDefinition{readTool}, want: "invalid assistant tool call"},
		{name: "invalid tool name", messages: []Message{user}, tools: []ToolDefinition{{Name: "mcp.server/tool", Parameters: json.RawMessage(`{}`)}}, want: `invalid tool definition "mcp.server/tool"`},
		{name: "overlong tool name", messages: []Message{user}, tools: []ToolDefinition{{Name: strings.Repeat("a", 129), Parameters: json.RawMessage(`{}`)}}, want: `invalid tool definition "aaaa`},
		{name: "duplicate tool name", messages: []Message{user}, tools: []ToolDefinition{readTool, readTool}, want: `invalid tool definition "read": duplicate tool name`},
		{name: "schema not an object", messages: []Message{user}, tools: []ToolDefinition{{Name: "read", Parameters: json.RawMessage(`[]`)}}, want: `invalid tool definition "read": input schema must be a JSON object`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildAnthropicRequest(anthropicInput{Model: "m", MaxTokens: 1, Messages: tc.messages, Tools: tc.tools})
			if err == nil {
				t.Fatal("build succeeded, want an error")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("error = %v, want %v", err, tc.is)
			}
			if tc.want != "" && !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("error = %q, want prefix %q", err, tc.want)
			}
		})
	}
	if errClaudeEndsWithAssistant.Error() != "Claude request would end with an assistant turn; send a prompt to continue." {
		t.Fatalf("prefill error wording = %q", errClaudeEndsWithAssistant)
	}
}

func TestBuildAnthropicRequestTopLevelFields(t *testing.T) {
	prompt := []Message{{Role: "user", Content: "hi"}}
	const thinking = `"thinking":{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`
	cases := []struct {
		name     string
		in       anthropicInput
		want     string
		wantBeta string
	}{
		{
			name: "adaptive with tools",
			in:   anthropicInput{Messages: prompt, Tools: []ToolDefinition{readTool}, Thinking: thinkingAdaptive},
			want: `{"model":"claude-opus-5-5","max_tokens":128000,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],` +
				`"tools":[{"name":"read","description":"Read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],` +
				thinking + `,"cache_control":{"type":"ephemeral"},"stream":true}`,
			wantBeta: anthropicThinkingBeta,
		},
		{
			name: "no thinking without tools",
			in:   anthropicInput{Messages: prompt},
			want: `{"model":"claude-opus-5-5","max_tokens":128000,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"stream":true}`,
		},
		{
			name: "effort set",
			in:   anthropicInput{Messages: prompt, Effort: "high"},
			want: `{"model":"claude-opus-5-5","max_tokens":128000,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"output_config":{"effort":"high"},"stream":true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Model, tc.in.MaxTokens = "claude-opus-5-5", 128000
			request, err := buildAnthropicRequest(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(request.Body) != tc.want || request.Beta != tc.wantBeta || request.Effort != tc.in.Effort {
				t.Fatalf("body %s beta %q effort %q\nwant %s beta %q", request.Body, request.Beta, request.Effort, tc.want, tc.wantBeta)
			}
			assertNoForbiddenKeys(t, request.Body)
		})
	}
}

// assertNoForbiddenKeys scans every object in body for keys Likha never
// sends to Claude (spec point 4).
func assertNoForbiddenKeys(t *testing.T, body []byte) {
	t.Helper()
	var walk func(value any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				switch key {
				case "temperature", "top_p", "top_k", "tool_choice", "stop_sequences", "metadata":
					t.Fatalf("body carries forbidden key %q: %s", key, body)
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	walk(decoded)
}

func TestReplayBlocksMatching(t *testing.T) {
	blocks := json.RawMessage(`[{"type":"thinking","thinking":"t","signature":"s"},{"type":"text","text":"Let me "},{"type":"text","text":"look."},{"type":"tool_use","id":"c1","name":"read","input":{"path":"a"}}]`)
	base := Message{Role: "assistant", Content: "Let me look.", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: `{ "path": "a" }`}}}
	cases := []struct {
		name   string
		mutate func(m *Message)
		raw    json.RawMessage
		want   bool
	}{
		{name: "matching message, whitespace in arguments ignored", want: true},
		{name: "clipped content", mutate: func(m *Message) { m.Content = "Let me" }},
		{name: "dropped calls", mutate: func(m *Message) { m.ToolCalls = nil }},
		{name: "extra call", mutate: func(m *Message) { m.ToolCalls = append(m.ToolCalls, ToolCall{ID: "c2", Name: "read", Arguments: `{}`}) }},
		{name: "changed arguments", mutate: func(m *Message) { m.ToolCalls = []ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"b"}`}} }},
		{name: "renamed call", mutate: func(m *Message) { m.ToolCalls = []ToolCall{{ID: "c1", Name: "list", Arguments: `{"path":"a"}`}} }},
		{name: "unknown block type", raw: json.RawMessage(`[{"type":"fallback"}]`)},
		{name: "not an array", raw: json.RawMessage(`{"type":"text"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.ToolCalls = append([]ToolCall(nil), base.ToolCalls...)
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			raw := blocks
			if tc.raw != nil {
				raw = tc.raw
			}
			got, ok := replayBlocksMatching(raw, m)
			if ok != tc.want {
				t.Fatalf("matching = %v, want %v", ok, tc.want)
			}
			if ok && len(got) != 4 {
				t.Fatalf("blocks = %d, want 4", len(got))
			}
		})
	}
}
