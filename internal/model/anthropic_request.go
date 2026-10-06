package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// anthropicThinkingBeta is the beta that allows thinking.block_binding.
// Every adaptive-thinking request sends it with prefix_mismatch_behavior
// "drop_block" (spec decision 7).
const anthropicThinkingBeta = "thinking-binding-controls-2026-08-01"

// anthropicThinkingMode is the thinking configuration a request carries.
type anthropicThinkingMode uint8

const (
	// thinkingNone sends no thinking field, which never causes a 400.
	thinkingNone anthropicThinkingMode = iota
	// thinkingAdaptive sends adaptive thinking with summarized display.
	thinkingAdaptive
)

// anthropicInput is everything buildAnthropicRequest needs; the builder
// reads no client state, so request bodies are testable as pure values.
type anthropicInput struct {
	Model     string
	Messages  []Message
	Tools     []ToolDefinition
	Thinking  anthropicThinkingMode
	MaxTokens int64
	Effort    string // sent as output_config.effort when non-empty
	// Replay returns the replay state of an assistant message that this
	// request may send back (the client's replayFor gate); nil sends none.
	Replay func(Message) *ReasoningReplay
}

// anthropicRequest is a built request: the body plus what the body carried,
// which decides the request header and how a 400 is reported.
type anthropicRequest struct {
	Body     []byte
	Beta     string // anthropic-beta header value; "" sends none
	Replayed int    // assistant turns sent as replayed content blocks
	Effort   string // effort level the body carries; "" when none
}

type anthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type anthropicToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicThinkingParam struct {
	Type         string `json:"type"`
	Display      string `json:"display"`
	BlockBinding struct {
		PrefixMismatchBehavior string `json:"prefix_mismatch_behavior"`
	} `json:"block_binding"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

// anthropicMessage holds content blocks as values: block structs for rebuilt
// content and json.RawMessage for replayed blocks, which encode verbatim.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

// anthropicBody fixes the order of top-level fields (tasks T4). It has no
// sampling, tool_choice, stop_sequences or metadata field, so none can ever
// be sent (spec point 4).
type anthropicBody struct {
	Model        string                  `json:"model"`
	MaxTokens    int64                   `json:"max_tokens"`
	System       []anthropicTextBlock    `json:"system,omitempty"`
	Messages     []anthropicMessage      `json:"messages"`
	Tools        []anthropicTool         `json:"tools,omitempty"`
	Thinking     *anthropicThinkingParam `json:"thinking,omitempty"`
	OutputConfig *anthropicOutputConfig  `json:"output_config,omitempty"`
	CacheControl *anthropicCacheControl  `json:"cache_control,omitempty"`
	Stream       bool                    `json:"stream"`
}

// buildAnthropicRequest maps Likha's conversation and tools onto a Messages
// API request body (specs/claude-native tasks T2–T4, T13).
func buildAnthropicRequest(in anthropicInput) (anthropicRequest, error) {
	tools, err := anthropicTools(in.Tools)
	if err != nil {
		return anthropicRequest{}, err
	}
	conversation := anthropicConversation{withTools: len(tools) > 0, replay: in.Replay}
	if err := conversation.add(in.Messages); err != nil {
		return anthropicRequest{}, err
	}
	messages := conversation.messages()
	if len(messages) == 0 || messages[len(messages)-1].Role == "assistant" {
		return anthropicRequest{}, errClaudeEndsWithAssistant
	}
	body := anthropicBody{
		Model:     in.Model,
		MaxTokens: in.MaxTokens,
		System:    conversation.system,
		Messages:  messages,
		Tools:     tools,
		Stream:    true,
	}
	request := anthropicRequest{Replayed: conversation.replayed}
	if in.Thinking == thinkingAdaptive {
		thinking := &anthropicThinkingParam{Type: "adaptive", Display: "summarized"}
		thinking.BlockBinding.PrefixMismatchBehavior = "drop_block"
		body.Thinking = thinking
		request.Beta = anthropicThinkingBeta
	}
	if in.Effort != "" {
		body.OutputConfig = &anthropicOutputConfig{Effort: in.Effort}
		request.Effort = in.Effort
	}
	if len(tools) > 0 {
		// Tool-less requests (compaction, naming) never share the main
		// prefix, so a cache write there would cost 1.25x for nothing.
		body.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}
	request.Body, err = json.Marshal(body)
	if err != nil {
		return anthropicRequest{}, fmt.Errorf("encode Claude request: %w", err)
	}
	return request, nil
}

var anthropicToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// anthropicTools validates and maps tool definitions. Claude rejects names
// outside its pattern and duplicate names, so both fail before sending and
// name the tool (spec point 5).
func anthropicTools(defs []ToolDefinition) ([]anthropicTool, error) {
	tools := make([]anthropicTool, 0, len(defs))
	seen := make(map[string]bool, len(defs))
	for _, def := range defs {
		if !anthropicToolName.MatchString(def.Name) {
			return nil, fmt.Errorf("invalid tool definition %q: Claude tool names use only A-Z, a-z, 0-9, _ and - (1-128 characters)", def.Name)
		}
		if seen[def.Name] {
			return nil, fmt.Errorf("invalid tool definition %q: duplicate tool name", def.Name)
		}
		seen[def.Name] = true
		if !isJSONObject(def.Parameters) {
			return nil, fmt.Errorf("invalid tool definition %q: input schema must be a JSON object", def.Name)
		}
		tools = append(tools, anthropicTool{Name: def.Name, Description: def.Description, InputSchema: def.Parameters})
	}
	return tools, nil
}

// isJSONObject reports whether raw is one valid JSON object.
func isJSONObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

// anthropicTurn is one message under construction. User turns keep tool
// results and text apart so results can go first (Anthropic requires it).
type anthropicTurn struct {
	role    string
	results []any
	blocks  []any
}

// anthropicConversation accumulates the top-level system prompt and the
// merged message list.
type anthropicConversation struct {
	withTools bool
	replay    func(Message) *ReasoningReplay
	system    []anthropicTextBlock
	turns     []*anthropicTurn
	replayed  int
}

func (c *anthropicConversation) add(messages []Message) error {
	leading := true
	for _, m := range messages {
		isSystem := m.Role == "system" || m.Role == "developer"
		if leading && isSystem {
			// A stable top-level system prompt keeps the prompt cache and
			// replayed thinking valid (spec decision 3).
			if hasText(m.Content) {
				c.system = append(c.system, anthropicTextBlock{Type: "text", Text: m.Content})
			}
			continue
		}
		leading = false
		if err := c.addMessage(m, isSystem); err != nil {
			return err
		}
	}
	return nil
}

func (c *anthropicConversation) addMessage(m Message, isSystem bool) error {
	switch {
	case isSystem || m.Role == "user":
		// A later developer note (the tool-round checkpoint) is user-turn
		// text at its position.
		if hasText(m.Content) {
			c.appendBlocks("user", textBlock(m.Content))
		}
		return nil
	case m.Role == "tool":
		return c.addToolResult(m)
	case m.Role == "assistant":
		return c.addAssistant(m)
	default:
		return fmt.Errorf("invalid message role %q", m.Role)
	}
}

func (c *anthropicConversation) addToolResult(m Message) error {
	if m.ToolCallID == "" {
		return errors.New("tool result requires a tool call ID")
	}
	if !c.withTools {
		// Tool-less requests carry tool history as plain text (spec
		// decision 6).
		c.appendBlocks("user", textBlock("[tool result "+m.ToolCallID+"]\n"+m.Content))
		return nil
	}
	result := anthropicToolResultBlock{Type: "tool_result", ToolUseID: anthropicToolID(m.ToolCallID)}
	if hasText(m.Content) {
		result.Content = m.Content
	}
	turn := c.turn("user")
	turn.results = append(turn.results, result)
	return nil
}

// appendBlocks adds blocks to the current turn of role.
func (c *anthropicConversation) appendBlocks(role string, blocks ...any) {
	turn := c.turn(role)
	turn.blocks = append(turn.blocks, blocks...)
}

func (c *anthropicConversation) addAssistant(m Message) error {
	if !c.withTools {
		c.addAssistantText(m)
		return nil
	}
	inputs := make([]json.RawMessage, len(m.ToolCalls))
	for i, call := range m.ToolCalls {
		input, err := toolCallInput(call)
		if err != nil {
			return err
		}
		inputs[i] = input
	}
	if blocks := c.acceptedReplay(m); blocks != nil {
		c.appendBlocks("assistant", blocks...)
		c.replayed++
		return nil
	}
	var blocks []any
	if hasText(m.Content) {
		blocks = append(blocks, textBlock(m.Content))
	}
	for i, call := range m.ToolCalls {
		blocks = append(blocks, anthropicToolUseBlock{Type: "tool_use", ID: anthropicToolID(call.ID), Name: call.Name, Input: inputs[i]})
	}
	if len(blocks) == 0 {
		// Nothing to send; the message stays in history (spec point 3).
		return nil
	}
	c.appendBlocks("assistant", blocks...)
	return nil
}

// addAssistantText sends an assistant turn to a tool-less request as text
// lines (spec decision 6). Arguments are shown as stored: the text needs no
// structured input, so history another wire wrote with arguments that are
// not an object still reaches /compact and session naming.
func (c *anthropicConversation) addAssistantText(m Message) {
	var blocks []any
	if hasText(m.Content) {
		blocks = append(blocks, textBlock(m.Content))
	}
	for _, call := range m.ToolCalls {
		arguments := call.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		blocks = append(blocks, textBlock("[tool call "+call.Name+" "+arguments+"]"))
	}
	if len(blocks) > 0 {
		c.appendBlocks("assistant", blocks...)
	}
}

// acceptedReplay returns the stored content blocks of m when this request
// may send them back verbatim (tasks T13): tools are declared, the client's
// gate accepts the state, and the blocks still describe m.
func (c *anthropicConversation) acceptedReplay(m Message) []any {
	if !c.withTools || c.replay == nil {
		return nil
	}
	replay := c.replay(m)
	if replay == nil || len(replay.Blocks) == 0 {
		return nil
	}
	blocks, ok := replayBlocksMatching(replay.Blocks, m)
	if !ok {
		return nil
	}
	return blocks
}

// replayBlocksMatching splits a stored content array and checks it against
// the message: its text blocks concatenate to Content and its tool_use
// blocks equal ToolCalls in order. An agent that clipped the text or dropped
// calls changed the turn, so its blocks no longer apply.
func replayBlocksMatching(raw json.RawMessage, m Message) ([]any, bool) {
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
		return nil, false
	}
	verbatim := make([]any, 0, len(blocks))
	var text strings.Builder
	calls := 0
	for _, block := range blocks {
		var fields struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(block, &fields) != nil {
			return nil, false
		}
		switch fields.Type {
		case "text":
			text.WriteString(fields.Text)
			if !hasText(fields.Text) {
				// Claude can stream an empty or whitespace-only text block
				// but rejects one in a request; it still counts toward
				// Content, so the check below sees the turn unchanged.
				continue
			}
		case "tool_use":
			if calls >= len(m.ToolCalls) || !sameToolUse(m.ToolCalls[calls], fields.ID, fields.Name, fields.Input) {
				return nil, false
			}
			calls++
		case "thinking", "redacted_thinking":
		default:
			return nil, false
		}
		verbatim = append(verbatim, block)
	}
	if text.String() != m.Content || calls != len(m.ToolCalls) {
		return nil, false
	}
	return verbatim, true
}

// sameToolUse compares a stored tool_use block with a tool call, arguments in
// the canonical form both are stored in.
func sameToolUse(call ToolCall, id, name string, input json.RawMessage) bool {
	if call.ID != id || call.Name != name {
		return false
	}
	want, err := toolCallInput(call)
	if err != nil {
		return false
	}
	canonicalWant, err := json.Marshal(want)
	if err != nil {
		return false
	}
	got, err := json.Marshal(input)
	return err == nil && bytes.Equal(got, canonicalWant)
}

// toolCallInput returns a call's arguments as a tool_use input: a JSON
// object, with empty or null arguments meaning {}. Chat wires store any valid
// JSON, and null decodes into a tool's arguments exactly as {} does, so the
// call keeps its meaning. Other non-objects cannot be sent as structured
// input; the error names the call and the way out, since every later request
// repeats the same history.
func toolCallInput(call ToolCall) (json.RawMessage, error) {
	if call.ID == "" || call.Name == "" {
		return nil, errors.New("invalid assistant tool call: a call without an ID or name")
	}
	arguments := strings.TrimSpace(call.Arguments)
	if arguments == "" || arguments == "null" {
		return json.RawMessage(`{}`), nil
	}
	if !isJSONObject([]byte(arguments)) {
		return nil, fmt.Errorf("invalid assistant tool call %s (%s): Claude needs tool arguments as a JSON object; /compact continues from a summary", call.Name, call.ID)
	}
	return json.RawMessage(call.Arguments), nil
}

var anthropicToolIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// anthropicToolID maps a stored tool call ID onto the pattern Claude accepts
// for tool_use.id and tool_result.tool_use_id. Other wires issue IDs such as
// "functions.bash:0"; those get their other characters replaced and a short
// hash of the original, so the mapping is the same for a call and its result
// and two IDs that differ only in replaced characters stay distinct. IDs
// Claude issued already match and pass through unchanged.
func anthropicToolID(id string) string {
	if anthropicToolIDPattern.MatchString(id) {
		return id
	}
	sanitized := strings.Map(func(r rune) rune {
		if r < utf8.RuneSelf && (r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return r
		}
		return '_'
	}, id)
	sum := sha256.Sum256([]byte(id))
	return sanitized + "_" + hex.EncodeToString(sum[:4])
}

// turn returns the last turn when it has role, else opens a new one, so
// consecutive messages from one side form one turn.
func (c *anthropicConversation) turn(role string) *anthropicTurn {
	if n := len(c.turns); n > 0 && c.turns[n-1].role == role {
		return c.turns[n-1]
	}
	turn := &anthropicTurn{role: role}
	c.turns = append(c.turns, turn)
	return turn
}

// messages returns the turns with tool results first in each user turn. A
// turn is opened only to receive a block, so none is empty.
func (c *anthropicConversation) messages() []anthropicMessage {
	messages := make([]anthropicMessage, 0, len(c.turns))
	for _, turn := range c.turns {
		content := append(append([]any(nil), turn.results...), turn.blocks...)
		messages = append(messages, anthropicMessage{Role: turn.role, Content: content})
	}
	return messages
}

// hasText reports whether text has something besides whitespace; Claude
// rejects whitespace-only text blocks.
func hasText(text string) bool {
	return strings.TrimSpace(text) != ""
}

func textBlock(text string) anthropicTextBlock {
	return anthropicTextBlock{Type: "text", Text: text}
}
