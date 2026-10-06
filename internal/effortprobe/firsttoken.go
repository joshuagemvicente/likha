package effortprobe

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// tokenWatch passes a streamed response body through unchanged and calls
// mark once, at the first SSE event that carries model output: text,
// reasoning or a tool call. The model package's callbacks report only text
// and reasoning, and a probe answer is often a bare tool call with hidden
// reasoning, so the first token is read from the wire (spec point 11).
type tokenWatch struct {
	io.ReadCloser
	mark  func()
	seen  bool
	line  []byte // the current line, until its newline arrives
	event string // the current event's "event:" field
	data  []byte // the current event's "data:" lines, joined by newlines
}

func (w *tokenWatch) Read(p []byte) (int, error) {
	n, err := w.ReadCloser.Read(p)
	if !w.seen {
		w.scan(p[:n])
	}
	return n, err
}

// scan splits chunk into lines; a line split across reads is kept until its
// newline arrives.
func (w *tokenWatch) scan(chunk []byte) {
	for !w.seen {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			w.line = append(w.line, chunk...)
			return
		}
		w.line = append(w.line, chunk[:i]...)
		w.field(bytes.TrimSuffix(w.line, []byte("\r")))
		w.line = w.line[:0]
		chunk = chunk[i+1:]
	}
}

// field applies one SSE line. A blank line ends the event; comment lines
// (keepalives) and unknown fields are ignored.
func (w *tokenWatch) field(line []byte) {
	if len(line) == 0 {
		if len(w.data) > 0 && carriesOutput(w.event, w.data) {
			w.seen = true
			w.mark()
		}
		w.event, w.data = "", w.data[:0]
		return
	}
	name, value, _ := bytes.Cut(line, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" "))
	switch string(name) {
	case "event":
		w.event = string(value)
	case "data":
		if len(w.data) > 0 {
			w.data = append(w.data, '\n')
		}
		w.data = append(w.data, value...)
	}
}

// carriesOutput reports whether one SSE event holds model output, on the
// chat-completions or the Responses wire. Role-only chunks, usage, [DONE]
// and lifecycle events hold none.
func carriesOutput(event string, data []byte) bool {
	var payload struct {
		Type  string          `json:"type"`
		Delta json.RawMessage `json:"delta"`
		Item  *struct {
			Type string `json:"type"`
		} `json:"item"`
		Choices []struct {
			Delta struct {
				Content          json.RawMessage `json:"content"`
				Reasoning        json.RawMessage `json:"reasoning"`
				ReasoningContent json.RawMessage `json:"reasoning_content"`
				ToolCalls        json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return false
	}
	if event == "" {
		event = payload.Type
	}
	if strings.HasPrefix(event, "response.") {
		switch {
		case strings.HasSuffix(event, ".delta"):
			return hasValue(payload.Delta)
		case event == "response.output_item.added", event == "response.output_item.done":
			return payload.Item != nil && payload.Item.Type == "function_call"
		}
		return false
	}
	for _, choice := range payload.Choices {
		d := choice.Delta
		if hasValue(d.Content) || hasValue(d.Reasoning) || hasValue(d.ReasoningContent) || hasValue(d.ToolCalls) {
			return true
		}
	}
	return false
}

// hasValue reports a JSON value that is present and not empty.
func hasValue(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", `""`, "[]", "{}":
		return false
	}
	return true
}
