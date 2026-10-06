// Public Responses wire for Sign in with ChatGPT plan usage. The historical
// Codex function names remain for callers, but the payload targets the public
// /v1/responses API, not a private Codex backend. See the SIWC preview limits:
// https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Namespace metadata is separate from the registry name in the public schema.
// Reconstruct it when replaying ToolCall, whose shared shape has no namespace.
const responsesToolNamespace = "likha"

type codexInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    *string         `json:"output,omitempty"`
}

type codexTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type responsesNamespace struct {
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Tools       []codexTool `json:"tools"`
}

type codexRequest struct {
	Model        string               `json:"model"`
	Instructions string               `json:"instructions,omitempty"`
	Input        []codexInputItem     `json:"input"`
	Tools        []responsesNamespace `json:"tools"`
	Store        bool                 `json:"store"`
	Stream       bool                 `json:"stream"`
}

// BuildCodexRequest maps a Likha conversation onto an OpenAI Responses API
// request for public Sign in with ChatGPT inference. System messages become
// instructions; developer messages retain their place in the input history.
// Full text/tool history is replayed with store:false and stream:true. Tools
// use one fixed namespace, while local names and schemas remain unchanged.
// Display-only reasoning and unsupported request parameters are never sent.
func BuildCodexRequest(modelName string, messages []Message, tools []ToolDefinition) ([]byte, error) {
	if strings.TrimSpace(modelName) == "" {
		return nil, errors.New("model name is required")
	}
	var instructions []string
	input := make([]codexInputItem, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "system":
			if m.Content != "" {
				instructions = append(instructions, m.Content)
			}
		case "user", "developer":
			content, err := codexContent("input_text", m.Content)
			if err != nil {
				return nil, err
			}
			input = append(input, codexInputItem{Type: "message", Role: m.Role, Content: content})
		case "assistant":
			if m.Content != "" || len(m.ToolCalls) == 0 {
				// EasyInputMessage accepts input_text for every role, including
				// assistant. output_text is a response item and would require
				// response-only message fields (id, status, annotations).
				content, err := codexContent("input_text", m.Content)
				if err != nil {
					return nil, err
				}
				input = append(input, codexInputItem{Type: "message", Role: "assistant", Content: content})
			}
			for _, call := range m.ToolCalls {
				if err := validateResponsesCall(call.ID, call.Name, responsesToolNamespace, call.Arguments, nil); err != nil {
					return nil, fmt.Errorf("invalid assistant tool call: %w", err)
				}
				input = append(input, codexInputItem{Type: "function_call", CallID: call.ID, Name: call.Name, Namespace: responsesToolNamespace, Arguments: call.Arguments})
			}
		case "tool":
			if !validResponsesID(m.ToolCallID) {
				return nil, errors.New("tool result requires a tool call ID")
			}
			output := m.Content
			input = append(input, codexInputItem{Type: "function_call_output", CallID: m.ToolCallID, Output: &output})
		default:
			return nil, fmt.Errorf("invalid message role %q", m.Role)
		}
	}
	body := codexRequest{Model: modelName, Instructions: strings.Join(instructions, "\n\n"), Input: input, Tools: []responsesNamespace{}, Store: false, Stream: true}
	functions := make([]codexTool, 0, len(tools))
	seen := make(map[string]bool, len(tools))
	for _, t := range tools {
		if !validResponsesToolName(t.Name) || !responsesParametersObject(t.Parameters) {
			return nil, fmt.Errorf("invalid tool definition %q", t.Name)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("duplicate tool definition %q", t.Name)
		}
		seen[t.Name] = true
		// Responses may normalize schemas into strict mode by default.
		// Explicit false preserves Likha's existing optional parameters.
		functions = append(functions, codexTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Parameters, Strict: false})
	}
	if len(functions) > 0 {
		body.Tools = append(body.Tools, responsesNamespace{Type: "namespace", Name: responsesToolNamespace, Description: "Likha's locally executed tools.", Tools: functions})
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode Responses request: %w", err)
	}
	return payload, nil
}

// codexContent encodes one text part of a replayed message.
func codexContent(textType, text string) (json.RawMessage, error) {
	part := struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: textType, Text: text}
	raw, err := json.Marshal([]any{part})
	if err != nil {
		return nil, fmt.Errorf("encode Responses content: %w", err)
	}
	return raw, nil
}

// codexPayloadErrorMessage extracts a human-readable message from a failed
// or error event payload: {"response":{"error":{...}}}, {"error":{...}},
// {"error":"..."}, or {"code","message"}.
func codexPayloadErrorMessage(payload string) string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err == nil {
		if response, ok := doc["response"].(map[string]any); ok {
			if e, ok := response["error"].(map[string]any); ok {
				return codexErrorMapMessage(e)
			}
		}
		if e, ok := doc["error"].(map[string]any); ok {
			return codexErrorMapMessage(e)
		}
		if e, ok := doc["error"].(string); ok {
			return e
		}
		if msg, ok := doc["message"].(string); ok {
			return msg
		}
		if code, ok := doc["code"].(string); ok {
			return code
		}
	}
	return strings.TrimSpace(payload)
}

func codexErrorMapMessage(e map[string]any) string {
	code, _ := e["code"].(string)
	msg, _ := e["message"].(string)
	switch {
	case code != "" && msg != "":
		return code + ": " + msg
	case msg != "":
		return msg
	default:
		return code
	}
}

// parseCodexUsage extracts prompt/completion token totals from a public
// Responses terminal event's usage object. Both the Responses naming
// (input_tokens / output_tokens) and the chat-completions aliases
// (prompt_tokens / completion_tokens) are accepted. ok is false when usage
// is absent or not an object; zero totals are valid usage.
func parseCodexUsage(raw json.RawMessage) (TokenUsage, bool) {
	report := parseCodexUsageDetailed(raw)
	return report.usage, report.ok
}

// parseCodexUsageDetailed is parseCodexUsage plus which of the two counts the
// usage object actually named, so a prompt-only report is not mistaken for a
// completion total of zero.
func parseCodexUsageDetailed(raw json.RawMessage) usageReport {
	if len(raw) == 0 || string(raw) == "null" {
		return usageReport{}
	}
	var usage struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
		Input      *int64 `json:"input_tokens"`
		Output     *int64 `json:"output_tokens"`
	}
	if err := json.Unmarshal(raw, &usage); err != nil {
		return usageReport{}
	}
	var result TokenUsage
	var seen usageSeen
	switch {
	case usage.Prompt != nil && *usage.Prompt >= 0:
		result.Prompt = *usage.Prompt
		result.PromptSeen = true
	case usage.Input != nil && *usage.Input >= 0:
		result.Prompt = *usage.Input
		result.PromptSeen = true
	}
	switch {
	case usage.Completion != nil && *usage.Completion >= 0:
		result.Completion = *usage.Completion
		seen.completion = true
	case usage.Output != nil && *usage.Output >= 0:
		result.Completion = *usage.Output
		seen.completion = true
	}
	seen.prompt = result.PromptSeen
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields) // the struct decode above already succeeded
	return usageReport{usage: result, seen: seen, detail: parseUsageDetail(fields), ok: true}.withAdditiveReasoning()
}
