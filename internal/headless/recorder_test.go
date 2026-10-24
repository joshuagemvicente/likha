package headless

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"likha/internal/agent"
	"likha/internal/model"
)

// A long answer streamed as many small deltas is recorded in linear memory:
// appending each delta to the entry string copied the whole answer every
// time, so an 800 KB answer allocated tens of gigabytes and took minutes.
func TestRecorderStreamsLongAnswerInLinearMemory(t *testing.T) {
	client, err := model.New("http://127.0.0.1:1/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecorder(Config{Prompt: "go", Client: client})
	const deltas, size = 10000, 64
	delta := strings.Repeat("x", size)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range deltas {
		rec.observe(agent.TurnEvent{Kind: "text", Text: delta})
	}
	rec.endTurn()
	runtime.ReadMemStats(&after)

	// Quadratic growth allocates about deltas²·size/2 = 3.2 GB here.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 32<<20 {
		t.Fatalf("recording a %d-byte answer allocated %d bytes", deltas*size, allocated)
	}
	if len(rec.entries) != 3 || rec.entries[1].Role != "Assistant" || rec.entries[1].Content != strings.Repeat(delta, deltas) {
		t.Fatalf("entries = %d, want You, the whole answer, Turn", len(rec.entries))
	}
}

// Streamed text before and after a tool call lands in two entries, each
// with exactly its own text.
func TestRecorderSplitsStreamedTextAtToolCalls(t *testing.T) {
	client, err := model.New("http://127.0.0.1:1/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecorder(Config{Prompt: "go", Client: client})
	call := model.ToolCall{ID: "c1", Name: "read", Arguments: `{"path":"a"}`}
	for _, ev := range []agent.TurnEvent{
		{Kind: "text", Text: "Look"},
		{Kind: "text", Text: "ing."},
		{Kind: "tool_start", Text: "Request: read", ToolCall: &call},
		{Kind: "text", Text: "Done"},
		{Kind: "text", Text: "."},
	} {
		rec.observe(ev)
	}
	rec.endTurn()
	var texts []string
	for _, entry := range rec.entries {
		if entry.Role == "Assistant" {
			texts = append(texts, entry.Content)
		}
	}
	if strings.Join(texts, "|") != "Looking.|Done." {
		t.Fatalf("assistant entries = %q, want [Looking. Done.]", texts)
	}
}

// A stop that lands after the run finished (a signal or the time limit
// between "done" and the result) does not turn the completed run into a
// cancelled one or add a stop notice to its transcript.
func TestRecorderStopAfterDoneKeepsTheRunCompleted(t *testing.T) {
	client, err := model.New("http://127.0.0.1:1/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	causes := []struct {
		name  string
		cause error
	}{
		{"interrupt", ErrInterrupted},
		{"time limit", errTimeLimit},
	}
	for _, tc := range causes {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder(Config{Prompt: "go", Client: client})
			history := []model.Message{{Role: "user", Content: "go"}, {Role: "assistant", Content: "All done."}}
			rec.observe(agent.TurnEvent{Kind: "text", Text: "All done."})
			rec.observe(agent.TurnEvent{Kind: "done", History: history})
			ctx, stop := context.WithCancelCause(context.Background())
			stop(tc.cause)

			result := rec.result(ctx)
			if result.Status != statusCompleted || result.Text != "All done." || result.Err != "" {
				t.Fatalf("result = %+v, want completed with the answer", result)
			}
			if notice, stopped := rec.stopNotice(ctx); stopped {
				t.Fatalf("stopNotice = %q for a finished run", notice)
			}
			rec.endTurn()
			for _, entry := range rec.entries {
				if strings.HasPrefix(entry.Content, "Run ") {
					t.Fatalf("finished transcript gained a stop entry: %+v", rec.entries)
				}
			}
		})
	}
}
