package agent

import (
	"context"
	"net/http"
	"testing"
)

// thinkingThenText is a Mistral-style reply: a list delta with a thinking
// chunk and a text chunk, then a plain string delta.
func thinkingThenText(answerStart, answerEnd string) []string {
	return []string{
		`{"choices":[{"delta":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"SECRET-TRACE weighing the history"}]},{"type":"text","text":"` + answerStart + `"}]}}]}`,
		`{"choices":[{"delta":{"content":"` + answerEnd + `"}}]}`,
		`[DONE]`,
	}
}

// Compaction passes no reasoning callback and keeps only Content, so thinking
// chunks never reach the summary (specs/chunked-content point 7).
func TestCompactHistoryIgnoresThinkingChunks(t *testing.T) {
	client := compactTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		compactFixture(t, thinkingThenText("User wants the parser offset bug fixed ", "and tests added.")...)(w, r)
	})
	var streamed string
	replacement, summary, err := CompactHistory(context.Background(), client, sampleHistory(), "", func(s string) { streamed += s })
	if err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	replacementAsserts(t, replacement, summary)
	if streamed != streamedSummary {
		t.Errorf("streamed text = %q, want only the answer %q", streamed, streamedSummary)
	}
}

func TestGenerateSessionNameIgnoresThinkingChunks(t *testing.T) {
	client := nameTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		nameFixture(t, thinkingThenText("Parser ", "offset fix")...)(w, r)
	})
	name, err := GenerateSessionName(context.Background(), client, sampleHistory())
	if err != nil {
		t.Fatalf("GenerateSessionName: %v", err)
	}
	if name != "Parser offset fix" {
		t.Fatalf("name = %q, want the answer text alone", name)
	}
}
