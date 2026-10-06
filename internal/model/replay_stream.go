package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// replayCapture tells consumeStream which replay form to collect and which
// issuer to record. The zero value collects nothing, so a provider without
// replay decodes its stream exactly as before this feature.
type replayCapture struct {
	wire   ReplayWire
	source ReplaySource
}

// replayCollector accumulates one stream's replay state. Tool-call thought
// signatures live on streamedToolCall, because they belong to a call index.
type replayCollector struct {
	capture              replayCapture
	reasoningContent     strings.Builder
	reasoningContentSeen bool
	details              []json.RawMessage
	// Mistral thinking chunks (specs/chunked-content M2), kept apart from
	// display reasoning, which also takes "reasoning" fields and which
	// explore and profile children clip.
	thinking          strings.Builder
	thinkingSeen      bool
	thinkingSignature string
}

// addDelta records one delta's replay parts for the captured wire. A
// reasoning_content field that is present but empty still counts as sent, so
// an empty string goes back rather than nothing.
func (r *replayCollector) addDelta(reasoningContent *string, details json.RawMessage) error {
	switch r.capture.wire {
	case ReplayReasoningContent:
		if reasoningContent != nil {
			r.reasoningContentSeen = true
			r.reasoningContent.WriteString(*reasoningContent)
		}
	case ReplayReasoningDetails:
		if isAbsentJSON(details) {
			return nil
		}
		var elements []json.RawMessage
		if err := json.Unmarshal(details, &elements); err != nil {
			return fmt.Errorf("malformed reasoning_details: %w", err)
		}
		r.details = append(r.details, elements...)
	}
	return nil
}

// addContent records one delta's Mistral thinking chunks under the
// thinking-chunk wire. A thinking chunk without text still counts, so the
// reply records "" rather than nothing; the last non-empty signature wins.
func (r *replayCollector) addContent(content streamContent) {
	if r.capture.wire != ReplayThinkingChunk || !content.thinking {
		return
	}
	r.thinkingSeen = true
	for _, piece := range content.pieces {
		if piece.kind == pieceThinking {
			r.thinking.WriteString(piece.text)
		}
	}
	if content.signature != "" {
		r.thinkingSignature = content.signature
	}
}

// thoughtSignature reads a tool-call fragment's
// extra_content.google.thought_signature, or "" when the fragment has none.
// Only the Gemini wire looks at it; other providers' extra_content stays
// undecoded so its shape can never fail their streams.
func (r *replayCollector) thoughtSignature(extraContent json.RawMessage) (string, error) {
	if r.capture.wire != ReplayThoughtSignature || isAbsentJSON(extraContent) {
		return "", nil
	}
	var extra struct {
		Google *struct {
			ThoughtSignature string `json:"thought_signature"`
		} `json:"google"`
	}
	if err := json.Unmarshal(extraContent, &extra); err != nil {
		return "", fmt.Errorf("malformed tool-call extra_content: %w", err)
	}
	if extra.Google == nil {
		return "", nil
	}
	return extra.Google.ThoughtSignature, nil
}

// finish builds the completed response's replay state. Every completed
// response on a replay provider records its issuer, even when it carried no
// state (spec decision 9); a provider without replay gets nil.
func (r *replayCollector) finish() (*ReasoningReplay, error) {
	if r.capture.wire == ReplayNone {
		return nil, nil
	}
	var reasoningContent *string
	if r.reasoningContentSeen {
		text := r.reasoningContent.String()
		reasoningContent = &text
	}
	var details json.RawMessage
	if len(r.details) > 0 {
		assembled, err := assembleReasoningDetails(r.details)
		if err != nil {
			return nil, err
		}
		details = assembled
	}
	replay, err := NewReasoningReplay(r.capture.source, reasoningContent, details, nil)
	if err != nil {
		return nil, fmt.Errorf("capture replay state: %w", err)
	}
	if r.thinkingSeen {
		// The value is still private to this function, so filling it here
		// does not break the immutability of shared replay values.
		text := r.thinking.String()
		replay.Thinking = &text
		replay.ThinkingSignature = r.thinkingSignature
	}
	return replay, nil
}

// isAbsentJSON reports whether a raw field was missing or JSON null.
func isAbsentJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// reasoningDetailText names the reasoning_details fields that stream in
// pieces; every other field takes its last non-empty value.
var reasoningDetailText = map[string]bool{"text": true, "summary": true, "data": true}

// detailObject is one reasoning_details object being assembled from stream
// fragments.
type detailObject struct {
	fields map[string]json.RawMessage
	raw    json.RawMessage // set for elements kept as they arrived
}

// assembleReasoningDetails turns OpenRouter's streamed reasoning_details
// elements into the provider's complete objects, in arrival order.
// Consecutive objects with the same type and index, and no conflicting
// non-empty id, are fragments of one object: their text, summary and data
// strings concatenate and every other field keeps its last non-empty value.
// Elements without an index, or that are not objects, are kept as they
// arrived. Whether OpenRouter wants index stripped and whether data is split
// across fragments are unverified until the user's probe (tasks T7, T18).
func assembleReasoningDetails(elements []json.RawMessage) (json.RawMessage, error) {
	var objects []*detailObject
	var open *detailObject // the object later fragments may continue
	for i, element := range elements {
		var fields map[string]json.RawMessage
		if json.Unmarshal(element, &fields) != nil || fields == nil || isAbsentJSON(fields["index"]) {
			objects = append(objects, &detailObject{raw: element})
			open = nil
			continue
		}
		if open != nil && continuesDetail(open.fields, fields) {
			if err := mergeDetail(open.fields, fields); err != nil {
				return nil, fmt.Errorf("reasoning_details element %d: %w", i, err)
			}
			continue
		}
		open = &detailObject{fields: fields}
		objects = append(objects, open)
	}
	assembled := make([]json.RawMessage, 0, len(objects))
	for _, object := range objects {
		if object.raw != nil {
			assembled = append(assembled, object.raw)
			continue
		}
		encoded, err := json.Marshal(object.fields)
		if err != nil {
			return nil, fmt.Errorf("encode reasoning_details: %w", err)
		}
		assembled = append(assembled, encoded)
	}
	encoded, err := json.Marshal(assembled)
	if err != nil {
		return nil, fmt.Errorf("encode reasoning_details: %w", err)
	}
	return encoded, nil
}

// continuesDetail reports whether next is a fragment of the object current
// is assembling.
func continuesDetail(current, next map[string]json.RawMessage) bool {
	if !sameJSON(current["type"], next["type"]) || !sameJSON(current["index"], next["index"]) {
		return false
	}
	currentID, nextID := current["id"], next["id"]
	return isEmptyDetailValue(currentID) || isEmptyDetailValue(nextID) || sameJSON(currentID, nextID)
}

// mergeDetail folds one fragment into the object being assembled.
func mergeDetail(into, fragment map[string]json.RawMessage) error {
	for key, value := range fragment {
		if !reasoningDetailText[key] {
			if !isEmptyDetailValue(value) {
				into[key] = value
			}
			continue
		}
		joined, err := concatJSONStrings(into[key], value)
		if err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
		into[key] = joined
	}
	return nil
}

// concatJSONStrings appends the JSON string next to the JSON string
// current; either may be absent or null.
func concatJSONStrings(current, next json.RawMessage) (json.RawMessage, error) {
	if isAbsentJSON(next) {
		return current, nil
	}
	var head, tail string
	if !isAbsentJSON(current) {
		if err := json.Unmarshal(current, &head); err != nil {
			return nil, errors.New("streamed piece is not a string")
		}
	}
	if err := json.Unmarshal(next, &tail); err != nil {
		return nil, errors.New("streamed piece is not a string")
	}
	return json.Marshal(head + tail)
}

// isEmptyDetailValue reports whether a field carries nothing: absent, null
// or the empty string.
func isEmptyDetailValue(raw json.RawMessage) bool {
	return isAbsentJSON(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte(`""`))
}

// sameJSON compares two raw scalar values as written, ignoring surrounding
// whitespace.
func sameJSON(a, b json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}
