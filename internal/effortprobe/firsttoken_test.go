package effortprobe

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestTokenWatchMarksTheFirstOutputEvent(t *testing.T) {
	const output = "OUTPUT"
	cases := []struct {
		name   string
		stream string // OUTPUT marks the event whose arrival is the first token
	}{
		{"chat text", `data: {"choices":[{"delta":{"role":"assistant","content":""}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"content":"Hi` + output + `"}}]}` + "\n\n"},
		{"chat reasoning", `data: {"choices":[{"delta":{"reasoning_content":"` + output + `"}}]}` + "\n\n"},
		{"chat tool call only", ": keepalive\n\n" + `data: {"choices":[{"delta":{"role":"assistant"}}]}` + "\r\n\r\n" +
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"` + output + `"}]}}]}` + "\n\n"},
		{"responses event line", "event: response.created\ndata: {\"response\":{}}\n\n" +
			"event: response.output_item.added\ndata: {\"item\":{\"type\":\"reasoning\"}}\n\n" +
			"event: response.function_call_arguments.delta\ndata: {\"delta\":\"" + output + "\"}\n\n"},
		{"responses type in payload", `data: {"type":"response.in_progress"}` + "\n\n" +
			`data: {"type":"response.output_item.added","item":{"type":"function_call","name":"` + output + `"}}` + "\n\n"},
		{"no output", `data: {"choices":[],"usage":{"completion_tokens":3}}` + "\n\ndata: [DONE]\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var read strings.Builder
			marks, markedAt := 0, -1
			watch := &tokenWatch{ReadCloser: io.NopCloser(iotest.OneByteReader(strings.NewReader(tc.stream))), mark: func() {
				marks++
				markedAt = read.Len() + 1 // the byte this Read returns is not yet in read
			}}
			buf := make([]byte, 1)
			for {
				n, err := watch.Read(buf)
				read.Write(buf[:n])
				if err != nil {
					break
				}
			}
			if read.String() != tc.stream {
				t.Fatalf("the body changed:\n%q\nwant\n%q", read.String(), tc.stream)
			}
			want := strings.Index(tc.stream, output)
			if want < 0 {
				if marks != 0 {
					t.Fatalf("marked %d times for a stream without output", marks)
				}
				return
			}
			// The event ends at the blank line after OUTPUT.
			end := want + strings.Index(tc.stream[want:], "\n\n") + 2
			if marks != 1 || markedAt != end {
				t.Fatalf("marks = %d at byte %d, want 1 at byte %d", marks, markedAt, end)
			}
		})
	}
}
