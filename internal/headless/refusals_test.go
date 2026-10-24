package headless

import (
	"context"
	"strings"
	"testing"
)

// toolMessage returns the content the model received for callID.
func toolMessage(t *testing.T, body map[string]any, callID string) string {
	t.Helper()
	messages, _ := body["messages"].([]any)
	for _, item := range messages {
		message, _ := item.(map[string]any)
		if message["role"] == "tool" && message["tool_call_id"] == callID {
			content, _ := message["content"].(string)
			return content
		}
	}
	t.Fatalf("no tool message for %s in %v", callID, messages)
	return ""
}

// A forged call to a tool that needs a person (a question or web consent)
// is refused with the headless reason, not a configuration guess such as
// "web search is turned off" (specs/headless-print point 8).
func TestForgedInteractiveCallsNameHeadlessMode(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"ask_user", map[string]any{"question": "Which file?"}},
		{"web_search", map[string]any{"query": "likha"}},
		{"web_fetch", map[string]any{"url": "https://example.com/"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeProvider(t, toolReply("f1", tc.name, tc.args), textReply("done"))
			run := newTestRun(t, fake, "go")
			code, _, stderr := runHeadless(t, context.Background(), run.cfg)
			if code != ExitCompleted {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "tool "+tc.name+": refused: ") || !strings.Contains(stderr, "1 refusal") {
				t.Fatalf("stderr does not report the refusal:\n%s", stderr)
			}
			requests := fake.requests()
			if len(requests) != 2 {
				t.Fatalf("requests = %d, want 2", len(requests))
			}
			content := toolMessage(t, requests[1], "f1")
			if !strings.Contains(content, NoReviewerReason) || !strings.Contains(content, `"status":"refused"`) {
				t.Fatalf("model saw %q, want a refusal naming %q", content, NoReviewerReason)
			}
			if strings.Contains(content, "config.json") || strings.Contains(content, "not wired") {
				t.Fatalf("model saw a misleading reason: %q", content)
			}
		})
	}
}
