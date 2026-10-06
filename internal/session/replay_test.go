package session

import (
	"encoding/json"
	"testing"

	"likha/internal/explore"
	"likha/internal/model"
)

func testReplay(t *testing.T, modelID, content, details string) *model.ReasoningReplay {
	t.Helper()
	source := model.ReplaySource{Endpoint: "https://api.deepseek.com/v1/chat/completions", Model: modelID}
	replay, err := model.NewReasoningReplay(source, &content, json.RawMessage(details), nil)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}

func replayConversation(replay *model.ReasoningReplay) []model.Message {
	return []model.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", Reasoning: "display", Replay: replay, ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{}`, ThoughtSignature: "sig<&>"}}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
}

// Replay state and thought signatures survive save and load, and re-saving
// the loaded snapshot (HTML-sensitive characters included) is not counted as
// conversation activity.
func TestReplayRoundTripAndResaveKeepsRecency(t *testing.T) {
	store, _, _ := testStore(t)
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.History = replayConversation(testReplay(t, "deepseek-flash", "<think> & more", `[ {"text": "<a&b>"} ]`))
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved := snapshot.History[1]
	got := loaded.History[1]
	if !got.Replay.Equal(saved.Replay) || got.ToolCalls[0] != saved.ToolCalls[0] {
		t.Fatalf("loaded assistant = %+v, want %+v", got, saved)
	}
	if !sameConversation(snapshot, loaded) {
		t.Fatal("a loaded copy of a saved snapshot compared as a different conversation")
	}
	before, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	after, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(after) != 1 || !before[0].Updated.Equal(after[0].Updated) {
		t.Fatalf("re-saving an unchanged replay history changed recency: before=%v after=%v", before, after)
	}
}

func TestDecodeLegacySnapshotWithoutReplay(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	legacy := []byte(`{"ID":"` + id + `","Root":"/repo","History":[` +
		`{"Role":"user","Content":"go","Reasoning":"","ToolCalls":null,"ToolCallID":""},` +
		`{"Role":"assistant","Content":"","Reasoning":"r","ToolCalls":[{"ID":"c1","Name":"read","Arguments":"{}"}],"ToolCallID":""}` +
		`],"Entries":null}`)
	snapshot, err := decode(id, "/repo", legacy)
	if err != nil {
		t.Fatal(err)
	}
	assistant := snapshot.History[1]
	if assistant.Replay != nil || assistant.ToolCalls[0].ThoughtSignature != "" || assistant.Reasoning != "r" {
		t.Fatalf("legacy assistant = %+v; want its fields and no replay state", assistant)
	}
}

func TestSameConversationComparesReplay(t *testing.T) {
	flash := testReplay(t, "deepseek-flash", "r", "")
	cases := []struct {
		name  string
		left  *model.ReasoningReplay
		right *model.ReasoningReplay
		want  bool
	}{
		{"both absent", nil, nil, true},
		{"shared pointer", flash, flash, true},
		{"equal copies", flash, testReplay(t, "deepseek-flash", "r", ""), true},
		{"added", nil, flash, false},
		{"removed", flash, nil, false},
		{"other issuer", flash, testReplay(t, "deepseek-v4-pro", "r", ""), false},
		{"other content", flash, testReplay(t, "deepseek-flash", "s", ""), false},
		{"other details", flash, testReplay(t, "deepseek-flash", "r", `[1]`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Snapshot{History: replayConversation(tc.left)}
			b := Snapshot{History: replayConversation(tc.right)}
			if got := sameConversation(a, b); got != tc.want {
				t.Fatalf("sameConversation = %v, want %v", got, tc.want)
			}
			ra := explore.Record{History: a.History}
			rb := explore.Record{History: b.History}
			if got := sameTaskRecord(ra, rb); got != tc.want {
				t.Fatalf("sameTaskRecord = %v, want %v", got, tc.want)
			}
		})
	}

	signed := Snapshot{History: replayConversation(nil)}
	unsigned := Snapshot{History: replayConversation(nil)}
	unsigned.History[1].ToolCalls = []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{}`}}
	if sameConversation(signed, unsigned) {
		t.Fatal("histories differing only in a thought signature compared equal")
	}
}

// A tool result carrying replay state is malformed: replay belongs to
// assistant messages, so resume must not repair the group.
func TestRepairTaskHistoryRejectsToolResultWithReplay(t *testing.T) {
	record := explore.Record{ID: "t1", ParentCallID: "c1", Agent: "explore", Description: "d", Prompt: "p", Status: explore.Running}
	call := model.ToolCall{ID: "c1", Name: "task", Arguments: `{"agent":"explore","description":"d","prompt":"p"}`}
	cases := []struct {
		name   string
		replay *model.ReasoningReplay
		want   bool
	}{
		{"plain interrupted result", nil, true},
		{"result carrying replay", testReplay(t, "deepseek-flash", "r", ""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := []model.Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", ToolCalls: []model.ToolCall{call}},
				{Role: "tool", ToolCallID: "c1", Content: interruptedToolText, Replay: tc.replay},
			}
			_, _, repaired := repairTaskHistory(history, []explore.Record{record})
			if repaired != tc.want {
				t.Fatalf("repaired = %v, want %v", repaired, tc.want)
			}
		})
	}
}
