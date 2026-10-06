package explore

import (
	"testing"

	"likha/internal/model"
)

// Task records never persist replay state: cloning a child history for its
// record strips it without touching the live history.
func TestCloneMessagesStripsReplayState(t *testing.T) {
	text := "r"
	replay, err := model.NewReasoningReplay(model.ReplaySource{Endpoint: "https://x.example/v1/chat/completions", Model: "m"}, &text, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	live := []model.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "a", Reasoning: "display", Replay: replay, ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{}`, ThoughtSignature: "sig"}}},
	}
	cloned := cloneMessages(live)
	want := model.Message{Role: "assistant", Content: "a", Reasoning: "display", ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}}
	got := cloned[1]
	if got.Replay != nil || len(got.ToolCalls) != 1 || got.ToolCalls[0] != want.ToolCalls[0] || got.Content != want.Content || got.Reasoning != want.Reasoning {
		t.Fatalf("cloned assistant = %+v, want %+v", got, want)
	}
	if live[1].Replay != replay || live[1].ToolCalls[0].ThoughtSignature != "sig" {
		t.Fatal("cloning stripped replay state from the live history")
	}
	if cloneMessages(nil) != nil {
		t.Fatal("cloneMessages(nil) is not nil")
	}
	record := cloneRecord(Record{History: live})
	if record.History[1].Replay != nil || record.History[1].ToolCalls[0].ThoughtSignature != "" {
		t.Fatal("cloneRecord kept replay state")
	}
}
