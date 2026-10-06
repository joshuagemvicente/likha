package model

import (
	"encoding/json"
	"net/http"
)

// skipThoughtSignature is Google's documented stand-in for a function call
// whose thought signature is unavailable. Google calls it a last resort that
// lowers quality, so Likha sends it only where a missing signature would be
// a 400 (spec decision 5).
const skipThoughtSignature = "skip_thought_signature_validator"

// requestExtraContent is a tool call's extra_content on the Gemini
// OpenAI-compatible endpoint.
type requestExtraContent struct {
	Google requestGoogleContent `json:"google"`
}

type requestGoogleContent struct {
	ThoughtSignature string `json:"thought_signature"`
}

func thoughtSignatureContent(signature string) *requestExtraContent {
	return &requestExtraContent{Google: requestGoogleContent{ThoughtSignature: signature}}
}

// requestContent is a request message's content: the plain string every
// provider gets, or, when replayEncoder sends Mistral thinking back, a chunk
// list that leads with the reply's thinking (specs/chunked-content M2).
type requestContent struct {
	text     string
	thinking *requestThinkingChunk // nil sends text as a plain string
}

type requestTextChunk struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type requestThinkingChunk struct {
	Type      string             `json:"type"`
	Thinking  []requestTextChunk `json:"thinking"`
	Signature string             `json:"signature,omitempty"`
}

// MarshalJSON writes the plain string unless thinking goes back, so every
// request without Mistral replay is byte-identical to a string field.
func (c requestContent) MarshalJSON() ([]byte, error) {
	if c.thinking == nil {
		return json.Marshal(c.text)
	}
	// Mistral accepts at most one leading thinking chunk, so all of the
	// reply's thinking rides in this one, ahead of the answer text.
	chunks := []any{c.thinking}
	if c.text != "" {
		chunks = append(chunks, requestTextChunk{Type: "text", Text: c.text})
	}
	return json.Marshal(chunks)
}

func thinkingChunk(replay *ReasoningReplay) *requestThinkingChunk {
	return &requestThinkingChunk{
		Type:      "thinking",
		Thinking:  []requestTextChunk{{Type: "text", Text: *replay.Thinking}},
		Signature: replay.ThinkingSignature,
	}
}

// replayEncoder adds replay fields to one request's messages and counts what
// it sent and what was missing, so a rejection can say whether replay may be
// to blame.
type replayEncoder struct {
	client    *Client
	turnStart int  // first message after the last user prompt (Gemini's current turn)
	tools     bool // DeepSeek requires reasoning_content only when tools are sent
	sent      int
	missing   int
}

func (c *Client) newReplayEncoder(messages []Message, tools bool) replayEncoder {
	return replayEncoder{client: c, turnStart: currentTurnStart(messages), tools: tools}
}

// encode fills exactly the replay field of the client's wire on rm, the
// request form of messages[index]. Display reasoning is never read.
func (e *replayEncoder) encode(index int, m Message, rm *requestMessage) {
	c := e.client
	if c.replayWire == ReplayNone || m.Role != "assistant" {
		return
	}
	if c.needsSkipSignature(m, index >= e.turnStart) {
		rm.ToolCalls[0].ExtraContent = thoughtSignatureContent(skipThoughtSignature)
		e.missing++
		return
	}
	replay := c.replayFor(m)
	if replay == nil {
		if c.replayWire == ReplayReasoningContent && e.tools {
			e.missing++
		}
		return
	}
	if fillReplayFields(c.replayWire, replay, m, rm) {
		e.sent++
	}
}

// fillReplayFields copies replay into rm's field for wire and reports whether
// it added anything. Thought signatures go only on the calls that carry one:
// for parallel calls Gemini signs the first call only. Mistral thinking turns
// rm's content into a chunk list; a reply without a thinking chunk keeps its
// plain string.
func fillReplayFields(wire ReplayWire, replay *ReasoningReplay, m Message, rm *requestMessage) bool {
	switch wire {
	case ReplayReasoningContent:
		rm.ReasoningContent = replay.ReasoningContent
		return replay.ReasoningContent != nil
	case ReplayReasoningDetails:
		rm.ReasoningDetails = replay.Details
		return len(replay.Details) > 0
	case ReplayThinkingChunk:
		if replay.Thinking == nil {
			return false
		}
		rm.Content.thinking = thinkingChunk(replay)
		return true
	case ReplayThoughtSignature:
		sent := false
		for i, call := range m.ToolCalls {
			if call.ThoughtSignature != "" {
				rm.ToolCalls[i].ExtraContent = thoughtSignatureContent(call.ThoughtSignature)
				sent = true
			}
		}
		return sent
	}
	return false
}

// rejected wraps a provider error in ReplayRejectedError when the request
// was a 400 and replay may be the cause; otherwise it returns err unchanged.
func (e *replayEncoder) rejected(status int, err error) error {
	if status != http.StatusBadRequest || (e.sent == 0 && e.missing == 0) {
		return err
	}
	return &ReplayRejectedError{Missing: e.missing, Sent: e.sent, Err: err}
}

// currentTurnStart returns the index of the first message after the last
// user prompt. Gemini validates thought signatures only from there on; tool
// results use the tool role, so they never start a turn.
func currentTurnStart(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i + 1
		}
	}
	return 0
}

// needsSkipSignature reports whether m gets Google's skip value on its first
// call: a current-turn assistant message with tool calls whose issuer is not
// this client's model (another model or endpoint, or a session saved before
// replay). Calls this model issued go back as received, signed or not, so a
// Gemini 2.5 loop is unchanged.
func (c *Client) needsSkipSignature(m Message, currentTurn bool) bool {
	return c.replayWire == ReplayThoughtSignature && currentTurn &&
		m.Role == "assistant" && len(m.ToolCalls) > 0 && c.replayFor(m) == nil
}

// PartialAssistant builds the assistant message Steer now keeps from a
// stopped request: the visible text, and on DeepSeek the reasoning text
// streamed so far as unsigned replay state, because DeepSeek rejects later
// tool requests whose earlier turns lack reasoning_content (spec decision 6).
// Signed state cut short would be rejected, so no other wire keeps any.
func (c *Client) PartialAssistant(text, reasoning string) Message {
	message := Message{Role: "assistant", Content: text}
	if c == nil || c.replayWire != ReplayReasoningContent {
		return message
	}
	message.Replay = &ReasoningReplay{Source: c.ReplaySource(), ReasoningContent: &reasoning}
	return message
}
