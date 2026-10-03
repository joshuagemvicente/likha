package model

import (
	"encoding/json"
	"testing"
)

func TestEstimateInputTokensEmpty(t *testing.T) {
	got, ok := EstimateInputTokens("any-model", nil, nil)
	if !ok || got != 0 {
		t.Fatalf("EstimateInputTokens(empty) = (%d, %t), want (0, true)", got, ok)
	}
}

func TestEstimateInputTokensMixedHistoryAndTools(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "Be brief.", Reasoning: "not sent"},
		{
			Role:      "assistant",
			Content:   "Checking now.",
			Reasoning: "private thought that is not sent",
			ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{"city":"Oslo"}`}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: `{"temperature":12}`},
		{Role: "assistant", Content: "It is 12°C."},
	}
	tools := []ToolDefinition{{
		Name:        "lookup",
		Description: "Look up current weather.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}}

	got, ok := EstimateInputTokens("provider/custom-model", messages, tools)
	if !ok || got != 83 {
		t.Fatalf("EstimateInputTokens(mixed) = (%d, %t), want (83, true)", got, ok)
	}
}

func TestEstimateInputTokensUnicode(t *testing.T) {
	got, ok := EstimateInputTokens("unicode-test", []Message{{Role: "user", Content: "Hi 世界🌍"}}, nil)
	if !ok || got != 9 {
		t.Fatalf("EstimateInputTokens(Unicode) = (%d, %t), want (9, true)", got, ok)
	}
}

func TestEstimateInputTokensIncludesToolPayloads(t *testing.T) {
	base := []Message{{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID: "id", Name: "run", Arguments: `{}`,
		}},
	}}
	withPayload := []Message{{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID: "id", Name: "run", Arguments: `{"query":"find blue whale"}`,
		}},
	}}
	tool := []ToolDefinition{{
		Name:        "run",
		Description: "Search the archive.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
	}}

	baseCount, baseOK := EstimateInputTokens("test", base, nil)
	payloadCount, payloadOK := EstimateInputTokens("test", withPayload, tool)
	if !baseOK || !payloadOK {
		t.Fatal("expected estimates for tool-call histories")
	}
	if baseCount != 13 {
		t.Errorf("base tool-call estimate = %d, want 13", baseCount)
	}
	if payloadCount != 46 {
		t.Errorf("estimate with tool arguments and definition = %d, want 46", payloadCount)
	}
	if payloadCount <= baseCount {
		t.Fatalf("tool payload did not increase estimate: base %d, payload %d", baseCount, payloadCount)
	}
}
