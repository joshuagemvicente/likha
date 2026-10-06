package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// errMalformedContent marks a streamed delta whose content is neither a
// string, null, nor a well-formed list of typed chunks (specs/chunked-content
// decision 4). consumeStream reports it as a malformed stream event.
var errMalformedContent = errors.New("malformed delta content")

// errUnshowableContent ends a reply whose only content was chunks Likha
// cannot show (specs/chunked-content point 8).
var errUnshowableContent = errors.New("model reply held only content Likha cannot show")

// maxReportedTypeBytes bounds a provider-chosen type name quoted in an error,
// so a hostile or broken stream cannot flood the transcript.
const maxReportedTypeBytes = 32

// contentPieceKind says where one piece of streamed content belongs.
type contentPieceKind uint8

const (
	// pieceAnswer is assistant answer text.
	pieceAnswer contentPieceKind = iota
	// pieceThinking is reasoning text from a "thinking" chunk.
	pieceThinking
	// pieceSkipped is a chunk, or an item inside "thinking", that Likha
	// cannot show; its text is that chunk's or item's type.
	pieceSkipped
)

// contentPiece is one piece of a delta's content, kept in arrival order.
type contentPiece struct {
	kind contentPieceKind
	text string
}

// streamContent is a chat-completions delta's content. Most providers send a
// string; Mistral's reasoning models send a list of typed chunks while they
// think (specs/chunked-content), so both shapes decode into ordered pieces.
type streamContent struct {
	pieces []contentPiece
	// thinking reports that the delta held at least one "thinking" chunk,
	// even one with no text; Mistral replay needs that distinction.
	thinking bool
	// signature is the last non-empty string "signature" of the delta's
	// thinking chunks, kept only for Mistral replay.
	signature string
}

// contentChunk is one element of a list-shaped content. Fields stay raw until
// the type is known, so a skipped chunk's payload is never interpreted and
// cannot fail the turn.
type contentChunk struct {
	Type      json.RawMessage `json:"type"`
	Text      json.RawMessage `json:"text"`
	Thinking  json.RawMessage `json:"thinking"`
	Signature json.RawMessage `json:"signature"`
}

// UnmarshalJSON decodes string, null and chunk-list content into pieces.
func (c *streamContent) UnmarshalJSON(data []byte) error {
	*c = streamContent{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return fmt.Errorf("%w: %v", errMalformedContent, err)
		}
		c.pieces = []contentPiece{{kind: pieceAnswer, text: text}}
		return nil
	case '[':
		return c.decodeChunks(trimmed)
	default:
		return fmt.Errorf("%w: content is not a string or a list", errMalformedContent)
	}
}

// decodeChunks reads list-shaped content, one chunk at a time and in order.
func (c *streamContent) decodeChunks(data []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("%w: %v", errMalformedContent, err)
	}
	for i, item := range items {
		chunk, err := decodeChunkObject(item)
		if err != nil {
			return fmt.Errorf("content chunk %d: %w", i, err)
		}
		if err := c.addChunk(chunk); err != nil {
			return fmt.Errorf("content chunk %d: %w", i, err)
		}
	}
	return nil
}

// addChunk appends the pieces of one top-level chunk.
func (c *streamContent) addChunk(chunk contentChunk) error {
	kind, typed := chunkType(chunk.Type)
	switch {
	case typed && kind == "text":
		return c.addText(pieceAnswer, chunk.Text)
	case typed && kind == "thinking":
		c.thinking = true
		c.keepSignature(chunk.Signature)
		return c.addThinking(chunk.Thinking)
	default:
		c.pieces = append(c.pieces, contentPiece{kind: pieceSkipped, text: clipType(kind)})
		return nil
	}
}

// addThinking appends one reasoning piece per "text" item of a thinking
// chunk. Other items (references, tool references, unknown kinds) carry no
// reasoning text; they become skipped pieces, so a reply made only of them
// ends with the unshowable-content error (spec decision 3) instead of a
// silent empty answer.
func (c *streamContent) addThinking(raw json.RawMessage) error {
	if isAbsent(raw) {
		return nil
	}
	if raw[0] != '[' {
		return fmt.Errorf("%w: thinking is not a list", errMalformedContent)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("%w: %v", errMalformedContent, err)
	}
	for i, item := range items {
		chunk, err := decodeChunkObject(item)
		if err != nil {
			return fmt.Errorf("thinking item %d: %w", i, err)
		}
		if kind, typed := chunkType(chunk.Type); !typed || kind != "text" {
			c.pieces = append(c.pieces, contentPiece{kind: pieceSkipped, text: clipType(kind)})
			continue
		}
		if err := c.addText(pieceThinking, chunk.Text); err != nil {
			return fmt.Errorf("thinking item %d: %w", i, err)
		}
	}
	return nil
}

// addText appends a text field as a piece of kind. A null or absent field
// adds nothing, the same way null content does.
func (c *streamContent) addText(kind contentPieceKind, raw json.RawMessage) error {
	if isAbsent(raw) {
		return nil
	}
	var text string
	if raw[0] != '"' || json.Unmarshal(raw, &text) != nil {
		return fmt.Errorf("%w: text is not a string", errMalformedContent)
	}
	c.pieces = append(c.pieces, contentPiece{kind: kind, text: text})
	return nil
}

// keepSignature records a thinking chunk's signature for replay. Anything
// but a non-empty string counts as no signature and never fails the turn
// (specs/chunked-content decision 7).
func (c *streamContent) keepSignature(raw json.RawMessage) {
	var signature string
	if isAbsent(raw) || json.Unmarshal(raw, &signature) != nil || signature == "" {
		return
	}
	c.signature = signature
}

// decodeChunkObject decodes one list element, which must be a JSON object.
func decodeChunkObject(item json.RawMessage) (contentChunk, error) {
	var chunk contentChunk
	trimmed := bytes.TrimSpace(item)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return chunk, fmt.Errorf("%w: chunk is not an object", errMalformedContent)
	}
	if err := json.Unmarshal(trimmed, &chunk); err != nil {
		return chunk, fmt.Errorf("%w: %v", errMalformedContent, err)
	}
	return chunk, nil
}

// chunkType returns a chunk's type and whether it was a JSON string. A chunk
// without a string type is skipped, never malformed: it carries nothing the
// transcript can show.
func chunkType(raw json.RawMessage) (string, bool) {
	var kind string
	if isAbsent(raw) || json.Unmarshal(raw, &kind) != nil {
		return "", false
	}
	return kind, true
}

// isAbsent reports a field that was missing or JSON null.
func isAbsent(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

// clipType bounds a provider-chosen type name before it is quoted.
func clipType(kind string) string {
	if len(kind) > maxReportedTypeBytes {
		return kind[:maxReportedTypeBytes]
	}
	return kind
}
