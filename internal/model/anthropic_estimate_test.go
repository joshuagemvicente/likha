package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// claudeReplayHistory is a tool round whose assistant turn carries Claude
// content blocks issued by source: two signed thinking blocks around the
// calls, and a redacted block whose opaque data is not text.
func claudeReplayHistory(t *testing.T, source ReplaySource) []Message {
	t.Helper()
	blocks := json.RawMessage(`[{"type":"thinking","thinking":"Plan: read a.","signature":"sig-0"},` +
		`{"type":"text","text":"Reading a."},` +
		`{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a"}},` +
		`{"type":"redacted_thinking","data":"opaque-redacted-data"},` +
		`{"type":"thinking","thinking":"Now b.","signature":"sig-3"},` +
		`{"type":"tool_use","id":"toolu_2","name":"read","input":{"path":"b"}}]`)
	replay, err := NewReasoningReplay(source, nil, nil, blocks)
	if err != nil {
		t.Fatal(err)
	}
	return []Message{
		{Role: "user", Content: "read a and b"},
		{Role: "assistant", Content: "Reading a.", Replay: replay, ToolCalls: []ToolCall{
			{ID: "toolu_1", Name: "read", Arguments: `{"path":"a"}`},
			{ID: "toolu_2", Name: "read", Arguments: `{"path":"b"}`},
		}},
		{Role: "tool", ToolCallID: "toolu_1", Content: "A"},
		{Role: "tool", ToolCallID: "toolu_2", Content: "B"},
	}
}

// The context estimate counts the thinking text of replayed Claude blocks
// exactly when the request would carry them: tools declared (or, at idle, the
// next prompt's request, which declares them), the issuing endpoint and
// model, and blocks that still match the message. Text and tool_use blocks
// repeat Content and ToolCalls, already counted, and signatures and redacted
// data are opaque, so neither adds anything.
func TestClaudeEstimateCountsReplayedThinking(t *testing.T) {
	client, err := New(anthropicBase, "claude-opus-5-5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	own := claudeReplayHistory(t, client.ReplaySource())
	planTokens, _ := estimateTextTokens("Plan: read a.")
	nowTokens, _ := estimateTextTokens("Now b.")
	thinking := planTokens + nowTokens

	otherModel, err := client.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if err := otherModel.SetModel("claude-sonnet-5-5"); err != nil {
		t.Fatal(err)
	}
	edited := claudeReplayHistory(t, client.ReplaySource())
	edited[1].Content = "Reading"
	// The chat-completions route on the same base is a different issuer.
	foreign := claudeReplayHistory(t, ReplaySource{Endpoint: anthropicBase + "/chat/completions", Model: "claude-opus-5-5"})

	tools := []ToolDefinition{readTool}
	cases := []struct {
		name         string
		client       *Client
		history      []Message
		tools        []ToolDefinition
		idle         bool // EstimateHistoryTokens instead of EstimateInputTokens
		wantThinking bool
	}{
		{"own blocks with tools", client, own, tools, false, true},
		{"tool-less request rebuilds without thinking", client, own, nil, false, false},
		{"idle history counts what the next prompt sends", client, own, nil, true, true},
		{"another model", otherModel, own, tools, false, false},
		{"another endpoint", client, foreign, tools, false, false},
		{"edited content", client, edited, tools, false, false},
		{"idle history after the message changed", client, edited, nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, ok := EstimateInputTokens(tc.client.Model(), tc.history, tc.tools)
			if !ok {
				t.Fatal("base estimate unavailable")
			}
			want := base
			if tc.wantThinking {
				want += thinking
			}
			var got int64
			if tc.idle {
				got, ok = tc.client.EstimateHistoryTokens(tc.history)
			} else {
				got, ok = tc.client.EstimateInputTokens(tc.history, tc.tools)
			}
			if !ok || got != want {
				t.Fatalf("estimate = %d, %v; want %d (base %d, thinking %d)", got, ok, want, base, thinking)
			}

			// The estimate follows the body: the request the estimate stands
			// for (with tools at idle) carries thinking exactly when counted.
			bodyTools := tc.tools
			if tc.idle {
				bodyTools = tools
			}
			request, err := buildAnthropicRequest(anthropicInput{
				Model:     tc.client.Model(),
				Messages:  tc.history,
				Tools:     bodyTools,
				MaxTokens: 1000,
				Replay:    tc.client.replayFor,
			})
			if err != nil {
				t.Fatal(err)
			}
			if sent := strings.Contains(string(request.Body), `"thinking":"Plan: read a."`); sent != tc.wantThinking {
				t.Fatalf("body carries thinking = %v, estimate counted it = %v:\n%s", sent, tc.wantThinking, request.Body)
			}
		})
	}
}
