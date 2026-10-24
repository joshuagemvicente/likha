package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"likha/internal/model"
)

// textReplyWithoutUsage streams an answer and ends without a usage object,
// as some OpenAI-compatible servers do.
func textReplyWithoutUsage(text string) reply {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
		fmt.Fprintf(w, "data: %s\n\n", delta)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// resultEventOf runs cfg with --json and returns the final result event.
func resultEventOf(t *testing.T, cfg Config) map[string]any {
	t.Helper()
	cfg.JSON = true
	code, stdout, stderr := runHeadless(t, context.Background(), cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	results := eventsOfType(decodeEvents(t, stdout), "result")
	if len(results) != 1 {
		t.Fatalf("result events = %d, want 1\n%s", len(results), stdout)
	}
	return results[0]
}

// A run that delegates counts every request it caused, as the TUI's session
// totals do: the explore child's request and tokens join the run's totals.
func TestTotalsIncludeExploreChildUsage(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("t1", "task", map[string]string{"agent": "explore", "description": "find main", "prompt": "find the main function"}),
		textReply("child findings"),
		textReply("parent done"))
	run := newTestRun(t, fake, "explore it")
	result := resultEventOf(t, run.cfg)
	if len(fake.requests()) != 3 {
		t.Fatalf("provider requests = %d, want 3 (parent, child, parent)", len(fake.requests()))
	}
	// Every scripted reply reports 10 prompt and 5 completion tokens.
	if result["requests"] != 3.0 || result["prompt_tokens"] != 30.0 || result["completion_tokens"] != 15.0 {
		t.Fatalf("result totals = requests %v, prompt %v, completion %v; want 3, 30, 15 including the child",
			result["requests"], result["prompt_tokens"], result["completion_tokens"])
	}
}

// A request that completed without reporting usage has an unknown cost, so
// the run's spend is not shown as the sum of the other requests alone.
func TestCostUnknownWhenARequestReportsNoUsage(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("r1", "read", map[string]string{"path": "."}),
		textReplyWithoutUsage("done"))
	run := newTestRun(t, fake, "look")
	client, err := model.New(fake.server.URL+"/v1", "gpt-4o", testKey)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Client, run.cfg.Provider = client, "openai"
	result := resultEventOf(t, run.cfg)
	if cost, shown := result["cost"]; shown {
		t.Fatalf("result cost = %v, want none: the second request reported no usage", cost)
	}
	if result["requests"] != 2.0 || result["prompt_tokens"] != 10.0 {
		t.Fatalf("result totals = requests %v, prompt %v; want 2, 10", result["requests"], result["prompt_tokens"])
	}
}

// Every request reporting usage on a priced model keeps the cost known.
func TestCostKnownWhenEveryRequestReportsUsage(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("r1", "read", map[string]string{"path": "."}),
		textReply("done"))
	run := newTestRun(t, fake, "look")
	client, err := model.New(fake.server.URL+"/v1", "gpt-4o", testKey)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Client, run.cfg.Provider = client, "openai"
	result := resultEventOf(t, run.cfg)
	cost, shown := result["cost"].(float64)
	if !shown || cost <= 0 {
		t.Fatalf("result cost = %v, want a known positive cost", result["cost"])
	}
}

// Automatic compaction that finds nothing earlier to summarize sends no
// summarize request, so the run's one POST counts once although the request
// was estimated twice.
func TestNothingToCompactCountsOneRequest(t *testing.T) {
	fake := newFakeProvider(t, textReply("answer"))
	run := newTestRun(t, fake, "only prompt")
	// Any request with the tool definitions exceeds 85% of this window.
	run.cfg.ContextWindow, run.cfg.AutoCompact = 100, true
	result := resultEventOf(t, run.cfg)
	if posts := len(fake.requests()); posts != 1 {
		t.Fatalf("provider requests = %d, want 1", posts)
	}
	if result["requests"] != 1.0 || result["prompt_tokens"] != 10.0 {
		t.Fatalf("result totals = requests %v, prompt %v; want 1, 10", result["requests"], result["prompt_tokens"])
	}
}

// A summarize request that failed without reporting usage still cost
// something unknown, so the run's spend is not shown.
func TestFailedSummarizeWithoutUsageMakesCostUnknown(t *testing.T) {
	fake := newFakeProvider(t, textReply("first"), failReply(http.StatusInternalServerError), textReply("second"))
	run := newTestRun(t, fake, "one")
	client, err := model.New(fake.server.URL+"/v1", "gpt-4o", testKey)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Client, run.cfg.Provider = client, "openai"
	if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code != ExitCompleted {
		t.Fatalf("first run exit = %d\n%s", code, stderr)
	}
	loaded, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Snapshot, run.cfg.Prompt = loaded, "two"
	run.cfg.ContextWindow, run.cfg.AutoCompact = 100, true
	result := resultEventOf(t, run.cfg)
	if posts := len(fake.requests()); posts != 3 {
		t.Fatalf("provider requests = %d, want first answer, failed summarize, second answer", posts)
	}
	if result["requests"] != 2.0 {
		t.Fatalf("result requests = %v, want 2 (failed summarize and answer)", result["requests"])
	}
	if cost, shown := result["cost"]; shown {
		t.Fatalf("result cost = %v, want none: the failed summarize reported no usage", cost)
	}
}
