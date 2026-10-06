package effortprobe

import (
	"encoding/json"

	"likha/internal/model"
)

// probeTaskVersion changes whenever the messages or the tool change, so
// reports from different tasks are never compared as if equal.
const probeTaskVersion = 1

// expectedAnswer is the sum of the primes below 60.
const expectedAnswer = 440

const answerTool = "record_answer"

// probeMessages is the fixed single-turn task (spec decision 6): it needs
// some reasoning, so levels can differ, and its answer is one short tool call.
func probeMessages() []model.Message {
	return []model.Message{
		{Role: "system", Content: "You are a careful assistant. Report every final answer by calling the record_answer tool exactly once."},
		{Role: "user", Content: "Compute the sum of all prime numbers below 60. Report the sum with the record_answer tool."},
	}
}

// probeTools is the one tool every probe request carries; tools are on every
// real Likha turn and change what some models accept.
func probeTools() []model.ToolDefinition {
	return []model.ToolDefinition{{
		Name:        answerTool,
		Description: "Record the final integer answer.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"answer":{"type":"integer","description":"The final answer."}},"required":["answer"]}`),
	}}
}

// estimatedInputTokens is the cost estimate's input size per request.
func estimatedInputTokens(modelID string) int64 {
	tokens, ok := model.EstimateInputTokens(modelID, probeMessages(), probeTools())
	if !ok {
		return 0
	}
	return tokens
}

// toolAnswer reads the record_answer call from a reply: whether the tool was
// called, and the integer it reported when the arguments carry one.
func toolAnswer(reply model.Message) (called bool, answer *int64) {
	for _, call := range reply.ToolCalls {
		if call.Name != answerTool {
			continue
		}
		var args struct {
			Answer *int64 `json:"answer"`
		}
		if json.Unmarshal([]byte(call.Arguments), &args) != nil {
			return true, nil
		}
		return true, args.Answer
	}
	return false, nil
}
