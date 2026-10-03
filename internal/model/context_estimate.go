package model

import (
	"unicode"
	"unicode/utf8"
)

// EstimateInputTokens returns a local approximation of the input tokens Likha
// sends in messages and tool definitions. Reasoning is excluded because it is
// not sent back to the provider. The estimate is model-independent: this
// package has no model-specific tokenizer. The boolean is false only if the
// estimate cannot be represented as an int64; an empty input is a usable zero
// estimate.
func EstimateInputTokens(modelID string, messages []Message, tools []ToolDefinition) (int64, bool) {
	_ = modelID // Reserved for a model-specific tokenizer if one is added later.

	var total int64
	add := func(tokens int64) bool {
		if tokens < 0 || total > int64(^uint64(0)>>1)-tokens {
			return false
		}
		total += tokens
		return true
	}
	addText := func(text string) bool {
		tokens, ok := estimateTextTokens(text)
		return ok && add(tokens)
	}

	for _, message := range messages {
		// Account for message framing in addition to the role and payload text.
		if !add(4) || !addText(message.Role) || !addText(message.Content) || !addText(message.ToolCallID) {
			return 0, false
		}
		for _, call := range message.ToolCalls {
			// Function-call framing and all call fields are input, including the
			// JSON arguments. Message.Reasoning is deliberately not counted.
			if !add(3) || !addText(call.ID) || !addText(call.Name) || !addText(call.Arguments) {
				return 0, false
			}
		}
	}

	for _, tool := range tools {
		// Tool definitions are part of the prompt even though they are not
		// conversation messages.
		if !add(6) || !addText(tool.Name) || !addText(tool.Description) || !addText(string(tool.Parameters)) {
			return 0, false
		}
	}

	return total, true
}

// estimateTextTokens approximates tokenizer pieces by counting ASCII bytes in
// groups of four and each non-whitespace Unicode rune as one piece. This keeps
// common Latin text near typical token densities while avoiding byte-based
// undercounts for CJK and emoji. It intentionally favors a simple, stable
// estimate over pretending to reproduce any provider tokenizer.
func estimateTextTokens(text string) (int64, bool) {
	var tokens int64
	var asciiBytes int64
	flushASCII := func() bool {
		pieces := asciiBytes / 4
		if asciiBytes%4 != 0 {
			pieces++
		}
		if tokens > int64(^uint64(0)>>1)-pieces {
			return false
		}
		tokens += pieces
		asciiBytes = 0
		return true
	}

	for _, r := range text {
		if r < utf8.RuneSelf {
			asciiBytes++
			continue
		}
		if !flushASCII() {
			return 0, false
		}
		if !unicode.IsSpace(r) {
			if tokens == int64(^uint64(0)>>1) {
				return 0, false
			}
			tokens++
		}
	}
	if !flushASCII() {
		return 0, false
	}
	return tokens, true
}
