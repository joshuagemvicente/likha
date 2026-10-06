package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// replayCapture tells consumeStream which replay form the provider uses and
// which client issued the response (specs/reasoning-replay T4). ReplayNone
// captures nothing, so the result is exactly what it was before replay.
type replayCapture struct {
	wire   ReplayWire
	source ReplaySource
}

// replayFor builds the replay state of one completed response. Every
// response on a provider that records replay carries its issuer, even with
// no state (reasoning-replay decision 9).
func (rc replayCapture) replayFor(thinking *chunkThinking) (*ReasoningReplay, error) {
	if rc.wire == ReplayNone {
		return nil, nil
	}
	replay, err := NewReasoningReplay(rc.source, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("capture replay state: %w", err)
	}
	if rc.wire == ReplayThinkingChunk && thinking.seen {
		// The value is still private to this function, so filling it here
		// does not break the immutability of shared replay values.
		text := thinking.text.String()
		replay.Thinking = &text
		replay.ThinkingSignature = thinking.signature
	}
	return replay, nil
}

// chunkThinking collects one reply's Mistral thinking chunks for replay. It is
// kept apart from display reasoning, which also takes "reasoning" fields and
// which explore and profile children clip.
type chunkThinking struct {
	seen      bool
	text      strings.Builder
	signature string
}

// add records the thinking chunks of one delta's content.
func (t *chunkThinking) add(content streamContent) {
	if !content.thinking {
		return
	}
	t.seen = true
	for _, piece := range content.pieces {
		if piece.kind == pieceThinking {
			t.text.WriteString(piece.text)
		}
	}
	if content.signature != "" {
		t.signature = content.signature
	}
}

// requestContent is a request message's content: the plain string every
// provider gets, or, for Mistral thinking replay, a chunk list that leads
// with the reply's thinking.
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

// contentFor encodes m's content for this client. Only an assistant
// message whose Mistral thinking this client issued goes out as a chunk list;
// every other message keeps its plain string content.
func (c *Client) contentFor(m Message) requestContent {
	content := requestContent{text: m.Content}
	if c.replayWire != ReplayThinkingChunk {
		return content
	}
	replay := c.replayFor(m)
	if replay == nil || replay.Thinking == nil {
		return content
	}
	content.thinking = &requestThinkingChunk{
		Type:      "thinking",
		Thinking:  []requestTextChunk{{Type: "text", Text: *replay.Thinking}},
		Signature: replay.ThinkingSignature,
	}
	return content
}
