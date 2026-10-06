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

const (
	maxCodexLineBytes       = 1 << 20
	maxResponsesEventBytes  = 1 << 20
	maxResponsesStreamBytes = 16 << 20
)

// ConsumeCodexStream consumes the public SIWC Responses SSE transport. Its
// historical name is retained for callers. Text/reasoning callbacks are
// incremental, but a Message and usage are returned only for response.completed
// with status "completed". Incomplete, failed, malformed and interrupted turns
// return no Message or usage, so partial function calls cannot execute.
//
// Pass the advertised tools as an optional fifth argument to reject any returned
// name outside that registry (an explicitly passed nil slice means no tools).
// Four-argument callers validate names and namespace without registry membership.
// The caller owns r; on cancellation a closable reader is closed to unblock Read.
func ConsumeCodexStream(ctx context.Context, r io.Reader, onText func(string), onReasoning func(string), advertised ...[]ToolDefinition) (Message, TokenUsage, bool, error) {
	message, report, err := consumeCodexStreamDetailed(ctx, r, onText, onReasoning, advertised...)
	if err != nil {
		return Message{}, TokenUsage{}, false, err
	}
	return message, report.usage, report.ok, nil
}

// consumeCodexStreamDetailed preserves the token metadata and per-count seen
// flags needed by StreamUsage and LastRequestUsage.
func consumeCodexStreamDetailed(ctx context.Context, r io.Reader, onText func(string), onReasoning func(string), advertised ...[]ToolDefinition) (Message, usageReport, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, usageReport{}, err
	}
	if len(advertised) > 1 {
		return Message{}, usageReport{}, errors.New("Responses stream accepts only one advertised tool registry")
	}
	var allowed map[string]bool
	if len(advertised) == 1 {
		allowed = make(map[string]bool, len(advertised[0]))
		for _, tool := range advertised[0] {
			if !validResponsesToolName(tool.Name) {
				return Message{}, usageReport{}, fmt.Errorf("invalid advertised tool name %q", tool.Name)
			}
			allowed[tool.Name] = true
		}
	}
	decoder := responsesStreamDecoder{calls: codexCallTracker{allowed: allowed}, onText: onText, onReasoning: onReasoning}
	quit := make(chan struct{})
	defer func() {
		close(quit)
		if ctx.Err() != nil {
			if closer, ok := r.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}()
	lines := readResponsesLines(r, quit)
	var data strings.Builder
	eventType := ""
	hasData := false
	for {
		if err := ctx.Err(); err != nil {
			return Message{}, usageReport{}, err
		}
		select {
		case <-ctx.Done():
			return Message{}, usageReport{}, ctx.Err()
		case l, ok := <-lines:
			if err := ctx.Err(); err != nil {
				return Message{}, usageReport{}, err
			}
			if !ok {
				// SSE dispatches only on a blank line, not EOF. Even a complete
				// JSON completion in an unterminated frame is a truncated stream.
				return Message{}, usageReport{}, errors.New("Responses stream ended without response.completed")
			}
			if l.err != nil {
				return Message{}, usageReport{}, fmt.Errorf("reading Responses stream (limit %d bytes, line limit %d bytes): %w", maxResponsesStreamBytes, maxCodexLineBytes, l.err)
			}
			if l.line == "" {
				if hasData || eventType != "" {
					completed, err := decoder.consume(eventType, data.String())
					if err != nil {
						return Message{}, usageReport{}, err
					}
					if err := ctx.Err(); err != nil {
						return Message{}, usageReport{}, err
					}
					if completed {
						return decoder.message, decoder.usage, nil
					}
				}
				data.Reset()
				eventType, hasData = "", false
				continue
			}
			if strings.HasPrefix(l.line, ":") {
				continue // SSE keepalive comment
			}
			field, value, _ := strings.Cut(l.line, ":")
			value = strings.TrimPrefix(value, " ") // exactly one optional space
			switch field {
			case "data":
				newline := 0
				if hasData {
					newline = 1
				}
				if data.Len()+newline+len(value) > maxResponsesEventBytes {
					return Message{}, usageReport{}, fmt.Errorf("Responses SSE event exceeds %d byte limit", maxResponsesEventBytes)
				}
				if hasData {
					data.WriteByte('\n')
				}
				data.WriteString(value)
				hasData = true
			case "event":
				// Multiple event fields in one frame overwrite, not dispatch.
				eventType = value
			default:
				// id, retry and unknown fields have no inference semantics.
			}
		}
	}
}

type codexLine struct {
	line string
	err  error
}

// The asynchronous reader allows prompt cancellation even for a blocked Reader.
// HTTP callers must bind the body to ctx and close it on return; a nonclosable
// arbitrary Reader cannot itself be forcibly interrupted by this API.
func readResponsesLines(r io.Reader, quit <-chan struct{}) <-chan codexLine {
	lines := make(chan codexLine, 16)
	go func() {
		defer close(lines)
		limited := &io.LimitedReader{R: r, N: maxResponsesStreamBytes + 1}
		scanner := bufio.NewScanner(limited)
		scanner.Buffer(make([]byte, 4096), maxCodexLineBytes)
		var bytes int64
		scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			advance, token, err := splitResponsesLine(data, atEOF)
			bytes += int64(advance)
			return advance, token, err
		})
		send := func(line codexLine) bool {
			select {
			case <-quit:
				return false
			case lines <- line:
				return true
			}
		}
		first := true
		for scanner.Scan() {
			if bytes > maxResponsesStreamBytes {
				send(codexLine{err: errors.New("Responses stream exceeds byte limit")})
				return
			}
			line := scanner.Text()
			if first {
				line = strings.TrimPrefix(line, "\ufeff")
				first = false
			}
			if !send(codexLine{line: line}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			send(codexLine{err: err})
		} else if limited.N == 0 {
			send(codexLine{err: errors.New("Responses stream exceeds byte limit")})
		}
	}()
	return lines
}

// SSE permits LF, CRLF, and CR line endings. Wait across a read boundary so
// the CR in a split CRLF is not mistaken for an extra blank line.
func splitResponsesLine(data []byte, atEOF bool) (int, []byte, error) {
	for i, c := range data {
		switch c {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			advance := i + 1
			if i+1 < len(data) && data[i+1] == '\n' {
				advance++
			}
			return advance, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type responsesStreamDecoder struct {
	text, reasoning strings.Builder
	calls           codexCallTracker
	onText          func(string)
	onReasoning     func(string)
	message         Message
	usage           usageReport
}

func knownResponsesEvent(event string) bool {
	switch event {
	case "response.output_text.delta", "response.refusal.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta",
		"response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done",
		"response.completed", "response.incomplete", "response.failed", "error":
		return true
	}
	return false
}

func (d *responsesStreamDecoder) consume(event, payload string) (bool, error) {
	if strings.TrimSpace(payload) == "[DONE]" {
		return false, errors.New("Responses stream ended with [DONE] before response.completed")
	}
	if event != "" && !knownResponsesEvent(event) {
		return false, nil // future events/keepalives cannot authorize completion
	}
	if payload == "" {
		if event == "error" || event == "response.failed" {
			return false, errors.New("Responses stream reported an error")
		}
		if event != "" {
			return false, fmt.Errorf("malformed Responses stream event %q: missing JSON payload", event)
		}
		return false, nil
	}
	bad := func(err error) error { return fmt.Errorf("malformed Responses stream event %q: %w", event, err) }
	var envelope struct {
		Type string `json:"type"`
	}
	if !strings.HasPrefix(strings.TrimSpace(payload), "{") {
		return false, bad(errors.New("payload must be a JSON object"))
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return false, bad(err)
	}
	if event == "" {
		event = envelope.Type
	} else if envelope.Type != "" && envelope.Type != event {
		return false, bad(fmt.Errorf("event type does not match payload type %q", envelope.Type))
	}
	switch event {
	case "response.output_text.delta", "response.refusal.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		var part struct {
			Delta *string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &part); err != nil {
			return false, bad(err)
		}
		if part.Delta == nil {
			return false, bad(errors.New("missing text delta"))
		}
		if event == "response.output_text.delta" || event == "response.refusal.delta" {
			writeResponsesDelta(&d.text, *part.Delta, d.onText)
		} else {
			writeResponsesDelta(&d.reasoning, *part.Delta, d.onReasoning)
		}
	case "response.output_item.added", "response.output_item.done":
		var part struct {
			Item *codexResponseOutput `json:"item"`
		}
		if err := json.Unmarshal([]byte(payload), &part); err != nil {
			return false, bad(err)
		}
		if part.Item == nil || part.Item.Type == "" {
			return false, bad(errors.New("missing output item or type"))
		}
		if part.Item.Type == "function_call" {
			var err error
			if event == "response.output_item.added" {
				err = d.calls.add(*part.Item)
			} else {
				err = d.calls.done(*part.Item)
			}
			if err != nil {
				return false, bad(err)
			}
		}
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		var part struct {
			ItemID    string  `json:"item_id"`
			Delta     *string `json:"delta"`
			Arguments *string `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(payload), &part); err != nil {
			return false, bad(err)
		}
		var err error
		if event == "response.function_call_arguments.delta" {
			if part.Delta == nil {
				return false, bad(errors.New("missing arguments delta"))
			}
			err = d.calls.appendArguments(part.ItemID, *part.Delta)
		} else {
			if part.Arguments == nil {
				return false, bad(errors.New("missing completed arguments"))
			}
			err = d.calls.setArguments(part.ItemID, *part.Arguments)
		}
		if err != nil {
			return false, bad(err)
		}
	case "response.completed":
		if err := d.completed(payload); err != nil {
			return false, bad(err)
		}
		return true, nil
	case "response.incomplete":
		var part struct {
			Response struct {
				IncompleteDetails struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &part); err != nil {
			return false, bad(err)
		}
		return false, fmt.Errorf("Responses stream incomplete: %s", part.Response.IncompleteDetails.Reason)
	case "response.failed", "error":
		return false, fmt.Errorf("Responses stream error: %s", codexPayloadErrorMessage(payload))
	}
	return false, nil
}

func writeResponsesDelta(builder *strings.Builder, delta string, callback func(string)) {
	if delta != "" {
		builder.WriteString(delta)
		if callback != nil {
			callback(delta)
		}
	}
}

func (d *responsesStreamDecoder) completed(payload string) error {
	var terminal struct {
		Response *struct {
			Status string          `json:"status"`
			Output json.RawMessage `json:"output"`
			Usage  json.RawMessage `json:"usage"`
			Error  json.RawMessage `json:"error"`
		} `json:"response"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &terminal); err != nil {
		return err
	}
	if terminal.Response == nil {
		return errors.New("missing completed response")
	}
	response := terminal.Response
	if response.Status != "completed" {
		return fmt.Errorf("response.completed has invalid completion status %q", response.Status)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return fmt.Errorf("response.completed contains an error: %s", codexPayloadErrorMessage(payload))
	}
	if !strings.HasPrefix(strings.TrimSpace(string(response.Output)), "[") {
		return errors.New("response.completed is missing an output array")
	}
	var output []codexResponseOutput
	if err := json.Unmarshal(response.Output, &output); err != nil {
		return err
	}
	var text, reasoning strings.Builder
	hasFinalText := false
	for _, item := range output {
		if item.Type == "" {
			return errors.New("response.completed output item is missing its type")
		}
		if item.Status != "" && item.Status != "completed" {
			return fmt.Errorf("response.completed output item has invalid completion status %q", item.Status)
		}
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					text.WriteString(part.Text)
					hasFinalText = true
				case "refusal":
					text.WriteString(part.Refusal)
					hasFinalText = true
				}
			}
		case "reasoning":
			for _, part := range item.Summary {
				if part.Type == "summary_text" {
					reasoning.WriteString(part.Text)
				}
			}
		}
	}
	calls, err := d.calls.completed(output)
	if err != nil {
		return err
	}
	if hasFinalText {
		finalText, streamed := text.String(), d.text.String()
		if !strings.HasPrefix(finalText, streamed) {
			return errors.New("response.completed text conflicts with streamed deltas")
		}
		writeResponsesDelta(&d.text, strings.TrimPrefix(finalText, streamed), d.onText)
	}
	if d.reasoning.Len() == 0 {
		writeResponsesDelta(&d.reasoning, reasoning.String(), d.onReasoning)
	}
	rawUsage := terminal.Usage
	if len(rawUsage) == 0 || string(rawUsage) == "null" {
		rawUsage = response.Usage
	}
	d.usage = parseCodexUsageDetailed(rawUsage)
	d.message = Message{Role: "assistant", Content: d.text.String(), Reasoning: d.reasoning.String(), ToolCalls: calls}
	return nil
}
