package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ReplayWire is how a provider wants its own reasoning state back on later
// requests (specs/reasoning-replay). The zero value captures nothing and
// sends nothing, so a provider row without replay keeps today's requests
// byte for byte.
type ReplayWire uint8

const (
	// ReplayNone captures and sends no replay state.
	ReplayNone ReplayWire = iota
	// ReplayReasoningContent sends chat-completions "reasoning_content" on
	// earlier assistant messages (DeepSeek thinking mode).
	ReplayReasoningContent
	// ReplayThoughtSignature sends each tool call's
	// extra_content.google.thought_signature (Gemini 3).
	ReplayThoughtSignature
	// ReplayReasoningDetails sends OpenRouter's "reasoning_details" array.
	ReplayReasoningDetails
	// ReplayAnthropicBlocks sends Anthropic content blocks; only the native
	// Claude client uses it.
	ReplayAnthropicBlocks
	// ReplayThinkingChunk sends Mistral thinking back as one leading
	// "thinking" chunk inside a list-shaped content (specs/chunked-content
	// M2).
	ReplayThinkingChunk
)

// modelBound reports whether state of this form goes back only to the model
// that issued it. Unsigned reasoning text is plain context on its endpoint,
// so it survives a switch between that endpoint's models; signed or encrypted
// state is validated against the issuing model (spec decision 2).
func (w ReplayWire) modelBound() bool {
	return w != ReplayReasoningContent
}

// ReplaySource identifies the client that issued replay state.
type ReplaySource struct {
	// Endpoint is the issuing client's full request URL, not its base URL:
	// openai and chatgpt share a base but use different request routes, and
	// the native Claude client posts to /v1/messages on the same base as the
	// compatibility route.
	Endpoint string
	Model    string // the issuing client's model ID
}

// ReasoningReplay is provider-issued reasoning state that goes back
// unchanged, and only to a matching issuer. It is never displayed; display
// reasoning lives in Message.Reasoning and is never sent.
//
// A non-nil value with only Source set means the issuer sent nothing, which
// tells "this model issued no state" apart from "another model or an older
// Likha wrote this message".
//
// Values are immutable once built: history copies share the pointer, so
// nothing may modify one in place. Build them with NewReasoningReplay so raw
// JSON parts are in canonical form and a persisted copy compares Equal.
type ReasoningReplay struct {
	Source ReplaySource
	// ReasoningContent is chat reasoning_content (DeepSeek). A pointer, so
	// "the provider sent an empty string" (send "" back) differs from "the
	// provider sent no field" (send nothing).
	ReasoningContent *string `json:",omitempty"`
	// Details is OpenRouter's reasoning_details array, assembled from the
	// stream.
	Details json.RawMessage `json:",omitempty"`
	// Blocks is the Anthropic assistant turn's full content array as
	// returned (thinking, redacted_thinking, text, tool_use, in order), so
	// interleaved thinking keeps its position. claude-native owns how it is
	// produced and sent.
	Blocks json.RawMessage `json:",omitempty"`
	// Thinking is Mistral's thinking-chunk text for the whole reply, joined
	// in arrival order and never clipped. Nil means the reply held no
	// thinking chunk; a pointer to "" means it held one without text.
	Thinking *string `json:",omitempty"`
	// ThinkingSignature is the last non-empty signature of those chunks.
	ThinkingSignature string `json:",omitempty"`
}

// NewReasoningReplay builds replay state issued by source. Details and Blocks
// must be valid JSON; empty or JSON null means the part is absent. They are
// stored as json.Marshal writes them (compact, HTML-escaped), the same form a
// saved session holds, so a value read back from a session is Equal to the
// one that was saved and a re-save does not count as a change.
func NewReasoningReplay(source ReplaySource, reasoningContent *string, details, blocks json.RawMessage) (*ReasoningReplay, error) {
	if source.Endpoint == "" || source.Model == "" {
		return nil, errors.New("reasoning replay: issuing endpoint and model are required")
	}
	canonicalDetails, err := canonicalReplayJSON(details)
	if err != nil {
		return nil, fmt.Errorf("reasoning replay details: %w", err)
	}
	canonicalBlocks, err := canonicalReplayJSON(blocks)
	if err != nil {
		return nil, fmt.Errorf("reasoning replay blocks: %w", err)
	}
	replay := &ReasoningReplay{Source: source, Details: canonicalDetails, Blocks: canonicalBlocks}
	if reasoningContent != nil {
		// Copy so the caller's later writes cannot reach the immutable value.
		text := *reasoningContent
		replay.ReasoningContent = &text
	}
	return replay, nil
}

// canonicalReplayJSON validates raw and returns the bytes json.Marshal would
// write for it, or nil when raw is empty or null.
func canonicalReplayJSON(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if !json.Valid(trimmed) {
		return nil, errors.New("invalid JSON")
	}
	canonical, err := json.Marshal(json.RawMessage(trimmed))
	if err != nil {
		return nil, fmt.Errorf("canonicalize JSON: %w", err)
	}
	return canonical, nil
}

// Equal reports whether r and other hold the same issuer and state. Two nil
// values are equal; a nil and a non-nil value are not, because "no replay"
// and "the issuer sent nothing" mean different things.
func (r *ReasoningReplay) Equal(other *ReasoningReplay) bool {
	if r == nil || other == nil {
		return r == other
	}
	if r.Source != other.Source {
		return false
	}
	if (r.ReasoningContent == nil) != (other.ReasoningContent == nil) {
		return false
	}
	if r.ReasoningContent != nil && *r.ReasoningContent != *other.ReasoningContent {
		return false
	}
	if !equalText(r.Thinking, other.Thinking) || r.ThinkingSignature != other.ThinkingSignature {
		return false
	}
	return bytes.Equal(r.Details, other.Details) && bytes.Equal(r.Blocks, other.Blocks)
}

// equalText compares optional texts by value; nil equals only nil.
func equalText(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// UnsignedOnly returns the part of r that stays valid after the message it
// rides on changes, as when the agent clips its content or drops a malformed
// tool-call batch. Unsigned reasoning text is plain context, so it survives,
// and DeepSeek rejects later tool requests whose earlier turns lack it.
// Signed or encrypted parts (Details, Blocks, and the tool-call signatures,
// which the caller clears) describe the exact turn the issuer returned, so
// they go. It returns nil when nothing unsigned remains rather than a
// Source-only value, which would claim the issuer sent nothing and so keep
// Gemini's skip value off an unsigned call (tasks T11). Mistral thinking is
// unsigned text only while no signature came with it; a signed trace
// describes the exact turn, so it goes like the other signed forms.
func (r *ReasoningReplay) UnsignedOnly() *ReasoningReplay {
	if r == nil {
		return nil
	}
	// Sharing the string pointers is safe: replay values are immutable.
	kept := &ReasoningReplay{Source: r.Source, ReasoningContent: r.ReasoningContent}
	if r.ThinkingSignature == "" {
		kept.Thinking = r.Thinking
	}
	if kept.ReasoningContent == nil && kept.Thinking == nil {
		return nil
	}
	return kept
}

// ReplaySource identifies this client as an issuer of replay state. It is
// read per request, so a SetModel switch takes effect on the next request.
func (c *Client) ReplaySource() ReplaySource {
	if c == nil {
		return ReplaySource{}
	}
	return ReplaySource{Endpoint: c.url, Model: c.model}
}

// replayFor returns the replay state of m that this client may send, or nil.
// State goes only to its issuer: the same endpoint, and for model-bound
// forms the same model. A client whose provider records no replay sends
// none, whatever the history holds. Request builders fill only the field of
// c.replayWire from the result.
func (c *Client) replayFor(m Message) *ReasoningReplay {
	if c == nil || c.replayWire == ReplayNone || m.Role != "assistant" || m.Replay == nil {
		return nil
	}
	source := c.ReplaySource()
	if m.Replay.Source.Endpoint != source.Endpoint {
		return nil
	}
	if c.replayWire.modelBound() && m.Replay.Source.Model != source.Model {
		return nil
	}
	return m.Replay
}

// replaySentText returns the replay text a request on wire carries for an
// assistant message whose state passed replayFor. Anthropic blocks are left
// to the native Claude client, whose blocks replace the message's content
// and tool calls rather than adding to them.
func replaySentText(wire ReplayWire, replay *ReasoningReplay, m Message) []string {
	switch wire {
	case ReplayReasoningContent:
		if replay.ReasoningContent != nil {
			return []string{*replay.ReasoningContent}
		}
	case ReplayReasoningDetails:
		if len(replay.Details) != 0 {
			return []string{string(replay.Details)}
		}
	case ReplayThinkingChunk:
		if replay.Thinking != nil {
			return []string{*replay.Thinking, replay.ThinkingSignature}
		}
	case ReplayThoughtSignature:
		var signatures []string
		for _, call := range m.ToolCalls {
			if call.ThoughtSignature != "" {
				signatures = append(signatures, call.ThoughtSignature)
			}
		}
		return signatures
	}
	return nil
}

// replayWireFor returns the replay wire of the provider row whose base URL is
// base. Rows that share a base share a wire, so row order cannot matter.
// Custom endpoints match no row and get ReplayNone.
func replayWireFor(base string, rows []Provider) ReplayWire {
	for _, p := range rows {
		if p.BaseURL == base {
			return p.Replay
		}
	}
	return ReplayNone
}

// setReplayWire overrides the resolved wire. Tests use it to exercise forms
// that no shipped provider row enables yet.
func (c *Client) setReplayWire(wire ReplayWire) {
	c.replayWire = wire
}
