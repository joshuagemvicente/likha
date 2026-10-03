// Codex wire: encoding of Lisa conversations as OpenAI Responses API requests
// for the ChatGPT Codex backend, and decoding of that backend's SSE stream.
// The chat-completions wire in client.go is untouched: the Codex endpoint
// rejects sampling parameters with HTTP 400 and speaks a different event
// vocabulary, so the request shape, event names, and line limit here are
// local to this file.
package model

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxCodexLineBytes = 1 << 20

type codexInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
}

type codexTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type codexRequest struct {
	Model        string           `json:"model"`
	Instructions string           `json:"instructions,omitempty"`
	Input        []codexInputItem `json:"input"`
	Tools        []codexTool      `json:"tools,omitempty"`
	Stream       bool             `json:"stream"`
}

// BuildCodexRequest maps a Lisa conversation onto an OpenAI Responses API
// request body for the ChatGPT Codex backend. System and developer messages
// become the top-level "instructions" (joined with "\n\n" in order). Tool
// reasoning (Message.Reasoning) is never included. Sampling parameters
// (temperature, top_p, ...) are never emitted: the backend rejects them with
// HTTP 400. No other fields beyond those specified here.
func BuildCodexRequest(modelName string, messages []Message, tools []ToolDefinition) ([]byte, error) {
	var instructions []string
	input := make([]codexInputItem, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "system", "developer":
			if m.Content != "" {
				instructions = append(instructions, m.Content)
			}
		case "user":
			content, err := codexContent("input_text", m.Content)
			if err != nil {
				return nil, err
			}
			input = append(input, codexInputItem{Type: "message", Role: "user", Content: content})
		case "assistant":
			if m.Content != "" {
				content, err := codexContent("output_text", m.Content)
				if err != nil {
					return nil, err
				}
				input = append(input, codexInputItem{Type: "message", Role: "assistant", Content: content})
			}
			for _, call := range m.ToolCalls {
				if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
					return nil, errors.New("invalid assistant tool call")
				}
				input = append(input, codexInputItem{Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: call.Arguments})
			}
		case "tool":
			if m.ToolCallID == "" {
				return nil, errors.New("tool result requires a tool call ID")
			}
			input = append(input, codexInputItem{Type: "function_call_output", CallID: m.ToolCallID, Output: m.Content})
		default:
			return nil, fmt.Errorf("invalid message role %q", m.Role)
		}
	}
	body := codexRequest{Model: modelName, Instructions: strings.Join(instructions, "\n\n"), Input: input, Stream: true}
	for _, t := range tools {
		if t.Name == "" || len(t.Parameters) == 0 || !json.Valid(t.Parameters) {
			return nil, fmt.Errorf("invalid tool definition %q", t.Name)
		}
		body.Tools = append(body.Tools, codexTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode codex request: %w", err)
	}
	return payload, nil
}

// codexContent encodes one text part of an assistant or user message.
func codexContent(textType, text string) (json.RawMessage, error) {
	part := struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: textType, Text: text}
	raw, err := json.Marshal([]any{part})
	if err != nil {
		return nil, fmt.Errorf("encode codex content: %w", err)
	}
	return raw, nil
}

type codexLine struct {
	line string
	err  error
}

// codexResponseOutput is the shape of response.completed's response.output.
type codexResponseOutput struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type codexCallBuilder struct {
	callID, name, arguments strings.Builder
	argumentsDone           string // authoritative full arguments from output_item.done
}

// codexCallTracker assembles streamed function calls by item id, preserving
// first-seen order. A completed output, when present, takes precedence.
type codexCallTracker struct {
	order    []string
	calls    map[string]*codexCallBuilder
	replaced []ToolCall
}

func (t *codexCallTracker) builder(id string) *codexCallBuilder {
	if t.calls == nil {
		t.calls = make(map[string]*codexCallBuilder)
	}
	call := t.calls[id]
	if call == nil {
		call = &codexCallBuilder{}
		t.calls[id] = call
		t.order = append(t.order, id)
	}
	return call
}

func (t *codexCallTracker) add(itemID, callID, name, arguments string) {
	call := t.builder(itemID)
	call.callID.WriteString(callID)
	call.name.WriteString(name)
	call.arguments.WriteString(arguments)
}

func (t *codexCallTracker) appendArguments(itemID, delta string) {
	t.builder(itemID).arguments.WriteString(delta)
}

// setArguments records the authoritative full arguments delivered with
// output_item.done, replacing anything streamed so far for that item.
func (t *codexCallTracker) setArguments(itemID, callID, name, arguments string) {
	call := t.builder(itemID)
	call.argumentsDone = arguments
	if callID != "" {
		call.callID.Reset()
		call.callID.WriteString(callID)
	}
	if name != "" {
		call.name.Reset()
		call.name.WriteString(name)
	}
}

// replace installs the tool calls rebuilt from a terminal response.output.
func (t *codexCallTracker) replace(calls []ToolCall) {
	t.replaced = calls
}

func (t *codexCallTracker) assemble() ([]ToolCall, error) {
	if t.replaced != nil {
		return t.replaced, nil
	}
	calls := make([]ToolCall, 0, len(t.order))
	for _, id := range t.order {
		b := t.calls[id]
		arguments := b.argumentsDone
		if !json.Valid([]byte(arguments)) {
			arguments = b.arguments.String()
		}
		call := ToolCall{ID: b.callID.String(), Name: b.name.String(), Arguments: arguments}
		if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
			return nil, fmt.Errorf("incomplete codex function call %q", id)
		}
		calls = append(calls, call)
	}
	return calls, nil
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

// parseCodexUsage extracts prompt/completion token totals from a Codex
// Responses terminal event's usage object. Both the Responses naming
// (input_tokens / output_tokens) and the chat-completions aliases
// (prompt_tokens / completion_tokens) are accepted. ok is false when usage
// is absent or not an object; zero totals are valid usage.
func parseCodexUsage(raw json.RawMessage) (TokenUsage, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return TokenUsage{}, false
	}
	var usage struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
		Input      *int64 `json:"input_tokens"`
		Output     *int64 `json:"output_tokens"`
	}
	if err := json.Unmarshal(raw, &usage); err != nil {
		return TokenUsage{}, false
	}
	var result TokenUsage
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
	case usage.Output != nil && *usage.Output >= 0:
		result.Completion = *usage.Output
	}
	return result, true
}

// ConsumeCodexStream parses the Codex backend's server-sent-events Responses
// stream, streaming text deltas to onText and reasoning summary deltas to
// onReasoning (nil callbacks are allowed), and reports the token usage
// carried by the terminal event (top-level "usage", or "response.usage";
// input_tokens/prompt_tokens and output_tokens/completion_tokens naming,
// with the alias naming accepted on both). Returns the assembled
// assistant Message (Role "assistant", Content = full text, Reasoning = full
// reasoning, ToolCalls in output order). usage=false means the terminal
// event carried no usage; zero totals are valid usage. The stream is over
// only on a terminal event; a connection that ends without one is an error,
// and a partial turn is never returned as success.
func ConsumeCodexStream(ctx context.Context, r io.Reader, onText func(string), onReasoning func(string)) (Message, TokenUsage, bool, error) {
	result := Message{Role: "assistant"}
	var lastUsage TokenUsage
	var seenUsage bool
	var text, reasoning strings.Builder
	calls := &codexCallTracker{}

	lines := make(chan codexLine, 16)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 4096), maxCodexLineBytes)
		for scanner.Scan() {
			select {
			case lines <- codexLine{line: scanner.Text()}:
			case <-quit:
				return
			}
		}
		err := scanner.Err()
		select {
		case lines <- codexLine{err: err}:
		case <-quit:
		}
		close(lines)
	}()

	var data strings.Builder // current SSE data payload, "" when no frame is open
	eventType := ""
	terminated := false
	flush := func(evType, payload string) (bool, error) {
		if payload == "" {
			if evType == "error" {
				return true, errors.New("codex stream reported an error")
			}
			return false, nil
		}
		if payload == "[DONE]" {
			return true, nil
		}
		if !json.Valid([]byte(payload)) {
			return false, fmt.Errorf("malformed codex stream event: invalid JSON payload for event %q", evType)
		}
		bad := func(err error) error { return fmt.Errorf("malformed codex stream event %q: %w", evType, err) }
		switch evType {
		case "response.output_text.delta":
			var d struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(payload), &d); err != nil {
				return false, bad(err)
			}
			if d.Delta != "" {
				text.WriteString(d.Delta)
				if onText != nil {
					onText(d.Delta)
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			var d struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(payload), &d); err != nil {
				return false, bad(err)
			}
			if d.Delta != "" {
				reasoning.WriteString(d.Delta)
				if onReasoning != nil {
					onReasoning(d.Delta)
				}
			}
		case "response.output_item.added":
			var d struct {
				Item struct {
					ID        string `json:"id"`
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"item"`
			}
			if err := json.Unmarshal([]byte(payload), &d); err != nil {
				return false, bad(err)
			}
			if d.Item.Type == "function_call" {
				calls.add(d.Item.ID, d.Item.CallID, d.Item.Name, d.Item.Arguments)
			}
		case "response.function_call_arguments.delta":
			var d struct {
				ItemID string `json:"item_id"`
				Delta  string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(payload), &d); err != nil {
				return false, bad(err)
			}
			calls.appendArguments(d.ItemID, d.Delta)
		case "response.output_item.done":
			var d struct {
				Item struct {
					ID        string `json:"id"`
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"item"`
			}
			if err := json.Unmarshal([]byte(payload), &d); err != nil {
				return false, bad(err)
			}
			if d.Item.Type == "function_call" {
				calls.setArguments(d.Item.ID, d.Item.CallID, d.Item.Name, d.Item.Arguments)
			}
		case "response.completed", "response.incomplete":
			var d struct {
				Response struct {
					Output []codexResponseOutput `json:"output"`
					Usage  json.RawMessage       `json:"usage"`
				} `json:"response"`
				Usage json.RawMessage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(payload), &d); err != nil {
				return false, bad(err)
			}
			rawUsage := d.Usage
			if len(rawUsage) == 0 || string(rawUsage) == "null" {
				rawUsage = d.Response.Usage
			}
			if usage, ok := parseCodexUsage(rawUsage); ok {
				lastUsage, seenUsage = usage, true
			}
			var stream []ToolCall // function calls taken from the terminal output, in order
			for _, item := range d.Response.Output {
				if item.Type != "function_call" {
					continue // message/reasoning items: text was already streamed
				}
				if item.CallID == "" || item.Name == "" {
					return false, errors.New("codex function_call item is missing a call ID or name")
				}
				if !json.Valid([]byte(item.Arguments)) {
					return false, fmt.Errorf("codex function_call %q has non-JSON arguments", item.CallID)
				}
				stream = append(stream, ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
			}
			if evType == "response.completed" || len(stream) > 0 {
				calls.replace(stream)
			}
			return true, nil
		case "response.failed", "error":
			return false, fmt.Errorf("codex stream error: %s", codexPayloadErrorMessage(payload))
		}
		// Unknown event types are ignored silently: the backend adds events
		// over time.
		return false, nil
	}

	var stopped error
loop:
	for {
		select {
		case <-ctx.Done():
			return Message{}, TokenUsage{}, false, ctx.Err()
		case l, ok := <-lines:
			if !ok {
				break loop
			}
			if l.err != nil {
				stopped = fmt.Errorf("reading codex stream (line limit %d bytes): %w", maxCodexLineBytes, l.err)
				break loop
			}
			line := l.line
			switch {
			case line == "":
				if data.Len() > 0 || eventType != "" {
					terminal, err := flush(eventType, data.String())
					eventType, data = "", strings.Builder{}
					if err != nil {
						return Message{}, TokenUsage{}, false, err
					}
					if terminal {
						terminated = true
						break loop
					}
				}
			case strings.HasPrefix(line, "data:"):
				part := strings.TrimPrefix(line, "data:")
				part = strings.TrimPrefix(part, " ")
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(part)
			case strings.HasPrefix(line, "event:"):
				if data.Len() > 0 || eventType != "" {
					terminal, err := flush(eventType, data.String())
					if err != nil {
						return Message{}, TokenUsage{}, false, err
					}
					if terminal {
						terminated = true
						break loop
					}
					data.Reset()
				}
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			default:
				// SSE comments, "id:", "retry:", and other fields.
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Message{}, TokenUsage{}, false, err
	}
	if stopped != nil {
		return Message{}, TokenUsage{}, false, stopped
	}
	if data.Len() > 0 || eventType != "" {
		terminal, err := flush(eventType, data.String())
		if err != nil {
			return Message{}, TokenUsage{}, false, err
		}
		terminated = terminal
	}
	if !terminated {
		return Message{}, TokenUsage{}, false, errors.New("codex stream ended without a terminal event")
	}
	toolCalls, err := calls.assemble()
	if err != nil {
		return Message{}, TokenUsage{}, false, err
	}
	result.ToolCalls = toolCalls
	result.Content = text.String()
	result.Reasoning = reasoning.String()
	return result, lastUsage, seenUsage, nil
}
