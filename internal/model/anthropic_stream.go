package model

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// anthropicResponse is one decoded Messages API stream: the assistant
// message, its usage, and the content array to keep for replay.
type anthropicResponse struct {
	message Message
	usage   usageReport
	// blocks is the response's content array in block order, set only when
	// the response is replayable and holds thinking (tasks T12).
	blocks json.RawMessage
}

// anthropicEvent is the union of the documented stream event payloads; the
// decoder dispatches on Type and ignores fields it does not use, including
// ones Anthropic adds later.
type anthropicEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage json.RawMessage `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		Data     string `json:"data"`
		ID       string `json:"id"`
		Name     string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string          `json:"type"`
		Text        string          `json:"text"`
		Thinking    string          `json:"thinking"`
		Signature   string          `json:"signature"`
		PartialJSON string          `json:"partial_json"`
		StopReason  string          `json:"stop_reason"`
		StopDetails json.RawMessage `json:"stop_details"`
	} `json:"delta"`
	Usage json.RawMessage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicBlock is one streamed content block.
type anthropicBlock struct {
	kind      string // text, thinking, redacted_thinking or tool_use
	text      strings.Builder
	signature strings.Builder
	data      string
	id, name  string
	input     strings.Builder
	closed    bool
	shown     bool // a non-empty thinking piece reached onReasoning
}

// anthropicStream is the decoder state of one response.
type anthropicStream struct {
	onText, onReasoning func(string)
	blocks              map[int]*anthropicBlock
	usage               map[string]json.RawMessage
	stopReason          string
	stopDetails         json.RawMessage
	done                bool
	unreplayable        bool // a block Likha cannot send back verbatim was seen
	reasoningShown      bool
}

// consumeAnthropicStream decodes a Messages API event stream (tasks T5–T7).
// A stream that ends before message_stop, an error event, a refusal or an
// incomplete tool call is an error and never yields a message.
func consumeAnthropicStream(ctx context.Context, reader io.Reader, onText, onReasoning func(string)) (anthropicResponse, error) {
	stream := &anthropicStream{
		onText:      onText,
		onReasoning: onReasoning,
		blocks:      make(map[int]*anthropicBlock),
		usage:       make(map[string]json.RawMessage),
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxEventLineBytes)
	var data strings.Builder
	for !stream.done && scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return anthropicResponse{}, err
		}
		line := scanner.Text()
		if line != "" {
			// event: lines name the type the data repeats; only data counts.
			if part, ok := strings.CutPrefix(line, "data:"); ok {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(part, " "))
			}
			continue
		}
		if err := stream.dispatch(data.String()); err != nil {
			return anthropicResponse{}, err
		}
		data.Reset()
	}
	if err := ctx.Err(); err != nil {
		return anthropicResponse{}, err
	}
	if err := scanner.Err(); err != nil {
		return anthropicResponse{}, fmt.Errorf("reading model stream (limit %d bytes, line limit %d bytes): %w", maxResponseBytes, maxEventLineBytes, err)
	}
	if !stream.done && data.Len() > 0 {
		if err := stream.dispatch(data.String()); err != nil {
			return anthropicResponse{}, err
		}
	}
	if !stream.done {
		return anthropicResponse{}, errAnthropicStreamEnded
	}
	return stream.finish()
}

// dispatch handles one event's data payload.
func (s *anthropicStream) dispatch(payload string) error {
	if payload == "" {
		return nil
	}
	var event anthropicEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return fmt.Errorf("malformed model stream event: %w", err)
	}
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			s.mergeUsage(event.Message.Usage)
		}
	case "content_block_start":
		s.startBlock(event)
	case "content_block_delta":
		s.applyDelta(event)
	case "content_block_stop":
		if block := s.blocks[event.Index]; block != nil {
			block.closed = true
		}
	case "message_delta":
		if event.Delta != nil {
			s.stopReason = event.Delta.StopReason
			s.stopDetails = event.Delta.StopDetails
		}
		s.mergeUsage(event.Usage)
	case "message_stop":
		s.done = true
	case "error":
		return streamErrorEvent(event)
	}
	// ping and event types added later are ignored, as Anthropic's
	// versioning policy asks.
	return nil
}

func streamErrorEvent(event anthropicEvent) error {
	apiErr := &anthropicAPIError{Type: "error"}
	if event.Error != nil {
		apiErr.Type = boundText(event.Error.Type)
		apiErr.Message = boundText(event.Error.Message)
	}
	return apiErr
}

func (s *anthropicStream) startBlock(event anthropicEvent) {
	start := event.ContentBlock
	if start == nil {
		return
	}
	block := &anthropicBlock{kind: start.Type, id: start.ID, name: start.Name, data: start.Data}
	switch start.Type {
	case "text", "thinking", "redacted_thinking", "tool_use":
	default:
		// Any other block (for example a server-side fallback) cannot be
		// rebuilt verbatim, so the response keeps no replay blocks.
		s.unreplayable = true
		return
	}
	s.blocks[event.Index] = block
	s.appendText(block, start.Text)
	s.appendThinking(block, start.Thinking)
}

func (s *anthropicStream) applyDelta(event anthropicEvent) {
	block, delta := s.blocks[event.Index], event.Delta
	if block == nil || delta == nil {
		return
	}
	switch delta.Type {
	case "text_delta":
		s.appendText(block, delta.Text)
	case "thinking_delta":
		s.appendThinking(block, delta.Thinking)
	case "signature_delta":
		block.signature.WriteString(delta.Signature)
	case "input_json_delta":
		block.input.WriteString(delta.PartialJSON)
	}
	// citations_delta and later delta types carry nothing Likha shows.
}

func (s *anthropicStream) appendText(block *anthropicBlock, piece string) {
	if block.kind != "text" || piece == "" {
		return
	}
	block.text.WriteString(piece)
	if s.onText != nil {
		s.onText(piece)
	}
}

// appendThinking streams reasoning to the muted view. A second thinking
// block starts after a blank line so the summaries stay apart.
func (s *anthropicStream) appendThinking(block *anthropicBlock, piece string) {
	if block.kind != "thinking" || piece == "" {
		return
	}
	block.text.WriteString(piece)
	if s.onReasoning == nil {
		return
	}
	if !block.shown && s.reasoningShown {
		s.onReasoning("\n\n")
	}
	block.shown, s.reasoningShown = true, true
	s.onReasoning(piece)
}

// mergeUsage records a usage object. message_delta counts are cumulative, so
// a later field replaces the earlier value of the same field.
func (s *anthropicStream) mergeUsage(raw json.RawMessage) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return
	}
	for key, value := range fields {
		if string(value) != "null" {
			s.usage[key] = value
		}
	}
}

// finish applies the completion rules (tasks T6) to a stream that reached
// message_stop.
func (s *anthropicStream) finish() (anthropicResponse, error) {
	switch s.stopReason {
	case "refusal":
		return anthropicResponse{}, refusalError(s.stopDetails)
	case "pause_turn":
		// Only server tools pause a turn, and Likha declares none.
		return anthropicResponse{}, fmt.Errorf("unexpected stop reason %s", s.stopReason)
	}
	indexes := make([]int, 0, len(s.blocks))
	for index := range s.blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	message := Message{Role: "assistant"}
	var text strings.Builder
	var reasoning []string
	hasThinking := false
	for _, index := range indexes {
		block := s.blocks[index]
		switch block.kind {
		case "text":
			text.WriteString(block.text.String())
		case "thinking", "redacted_thinking":
			hasThinking = true
			if block.kind == "thinking" && block.text.Len() > 0 {
				reasoning = append(reasoning, block.text.String())
			}
		case "tool_use":
			call, err := block.toolCall(index)
			if err != nil {
				return anthropicResponse{}, err
			}
			message.ToolCalls = append(message.ToolCalls, call)
		}
	}
	message.Content = text.String()
	message.Reasoning = strings.Join(reasoning, "\n\n")
	response := anthropicResponse{message: message, usage: anthropicUsageReport(s.usage)}
	if hasThinking && s.replayable(indexes) {
		blocks, err := s.contentArray(indexes, message.ToolCalls)
		if err != nil {
			return anthropicResponse{}, err
		}
		response.blocks = blocks
	}
	return response, nil
}

// toolCall turns a tool_use block into a call. Empty input means {};
// anything that is not a JSON object was cut off (for example at
// max_tokens), and no tool may run from it.
func (b *anthropicBlock) toolCall(index int) (ToolCall, error) {
	input := strings.TrimSpace(b.input.String())
	if input == "" {
		input = "{}"
	}
	if b.id == "" || b.name == "" || !isJSONObject([]byte(input)) {
		return ToolCall{}, fmt.Errorf("incomplete structured tool call at index %d", index)
	}
	return ToolCall{ID: b.id, Name: b.name, Arguments: input}, nil
}

// replayable reports whether every thinking block arrived whole: a block cut
// off before its signature, or redacted thinking without data, cannot go
// back, and dropping part of a turn's thinking is rejected.
func (s *anthropicStream) replayable(indexes []int) bool {
	if s.unreplayable {
		return false
	}
	for _, index := range indexes {
		block := s.blocks[index]
		switch block.kind {
		case "thinking":
			if !block.closed || block.signature.Len() == 0 {
				return false
			}
		case "redacted_thinking":
			if !block.closed || block.data == "" {
				return false
			}
		}
	}
	return true
}

// contentArray marshals the response's blocks in order, in the shapes the
// Messages API accepts back. Tool inputs reuse the calls' arguments.
func (s *anthropicStream) contentArray(indexes []int, calls []ToolCall) (json.RawMessage, error) {
	blocks := make([]any, 0, len(indexes))
	call := 0
	for _, index := range indexes {
		block := s.blocks[index]
		switch block.kind {
		case "text":
			blocks = append(blocks, anthropicTextBlock{Type: "text", Text: block.text.String()})
		case "thinking":
			blocks = append(blocks, anthropicThinkingBlock{Type: "thinking", Thinking: block.text.String(), Signature: block.signature.String()})
		case "redacted_thinking":
			blocks = append(blocks, anthropicRedactedBlock{Type: "redacted_thinking", Data: block.data})
		case "tool_use":
			blocks = append(blocks, anthropicToolUseBlock{Type: "tool_use", ID: calls[call].ID, Name: calls[call].Name, Input: json.RawMessage(calls[call].Arguments)})
			call++
		}
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return nil, fmt.Errorf("encode Claude replay blocks: %w", err)
	}
	return raw, nil
}

type anthropicThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type anthropicRedactedBlock struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// refusalError reads stop_details; category and explanation may each be
// null when the refusal maps to no named category.
func refusalError(raw json.RawMessage) *RefusalError {
	var details struct {
		Category    *string `json:"category"`
		Explanation *string `json:"explanation"`
	}
	refusal := &RefusalError{}
	if len(raw) == 0 || json.Unmarshal(raw, &details) != nil {
		return refusal
	}
	if details.Category != nil {
		refusal.Category = boundText(*details.Category)
	}
	if details.Explanation != nil {
		refusal.Explanation = boundText(*details.Explanation)
	}
	return refusal
}

// anthropicUsageReport maps Anthropic usage onto Likha's report (tasks T7).
// input_tokens counts only tokens after the last cache breakpoint, so cache
// reads and writes are added to make the prompt the whole input (spec
// decision 11). thinking_tokens is a documented subset of output_tokens, so
// no additive-reasoning fold applies; it is marked seen only when present,
// because Anthropic may omit it (research.md: thinking-token-count beta).
func anthropicUsageReport(fields map[string]json.RawMessage) usageReport {
	count := func(raw json.RawMessage) (int64, bool) {
		var n int64
		if len(raw) == 0 || json.Unmarshal(raw, &n) != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	input, inputSeen := count(fields["input_tokens"])
	output, outputSeen := count(fields["output_tokens"])
	if !inputSeen && !outputSeen {
		return usageReport{}
	}
	cacheRead, _ := count(fields["cache_read_input_tokens"])
	cacheWrite, _ := count(fields["cache_creation_input_tokens"])
	report := usageReport{
		usage:  TokenUsage{Completion: output, PromptSeen: inputSeen},
		seen:   usageSeen{prompt: inputSeen, completion: outputSeen},
		detail: usageDetail{cacheRead: cacheRead, cacheWrite: cacheWrite},
		ok:     true,
	}
	if inputSeen {
		report.usage.Prompt = input + cacheRead + cacheWrite
	}
	var details map[string]json.RawMessage
	if json.Unmarshal(fields["output_tokens_details"], &details) == nil {
		report.detail.reasoning, report.detail.reasoningSeen = count(details["thinking_tokens"])
	}
	return report
}
