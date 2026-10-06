package explore_test

import (
	"context"
	"math"
	"testing"

	"likha/internal/explore"
	"likha/internal/model"
)

// recorded is one model request as the child loop reports it: the client's
// LastRequest breakdown and whether the response carried usage at all.
type recorded struct {
	usage    model.RequestUsage
	reported bool
}

func full(prompt, completion int64) model.RequestUsage {
	return model.RequestUsage{Prompt: prompt, Completion: completion, PromptSeen: true, CompletionSeen: true}
}

// runTask spawns one explore task on provider/modelID whose runner records
// the given requests in order, and returns the settled task usage.
func runTask(t *testing.T, provider, modelID string, requests ...recorded) explore.Usage {
	t.Helper()
	manager, err := explore.New(context.Background(), explore.Config{
		Root: t.TempDir(), Provider: provider, Model: modelID,
		Runner: func(_ context.Context, n *explore.Node) explore.Outcome {
			for _, r := range requests {
				if err := n.BeginRequest(); err != nil {
					t.Errorf("BeginRequest: %v", err)
					return explore.Outcome{Status: explore.Failed}
				}
				n.RecordRequest(r.usage, r.reported)
				// A second report for the same request is ignored.
				n.RecordRequest(full(1e9, 1e9), true)
			}
			return explore.Outcome{Findings: "done"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Wait()
	outcome, err := manager.Spawn(context.Background(), "", "call_1", explore.Spec{Agent: "explore", Description: "usage", Prompt: "measure"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != explore.Completed {
		t.Fatalf("task status = %s (%s), want completed", outcome.Status, outcome.Reason)
	}
	records := manager.Snapshot()
	if len(records) != 1 || records[0].Usage != outcome.Usage {
		t.Fatalf("record usage %+v differs from outcome usage %+v", records, outcome.Usage)
	}
	return outcome.Usage
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRecordRequestPricesWithTheTaskProvider(t *testing.T) {
	// claude-opus-5-5 on the claude provider: $4 input, $20 output per 1M.
	usage := runTask(t, "claude", "claude-opus-5-5",
		recorded{full(1_000_000, 0), true},
		recorded{full(0, 1_000_000), true})
	if !usage.CostKnown || !near(usage.Cost, 24) || !usage.CostEstimated {
		t.Fatalf("usage = %+v, want known estimated $24", usage)
	}
	if usage.PromptTokens != 1_000_000 || usage.CompletionTokens != 1_000_000 || usage.ReportedRequests != 2 || usage.UnknownRequests != 0 {
		t.Fatalf("token totals = %+v", usage)
	}
}

func TestRecordRequestNeverBorrowsAnotherProvidersPrice(t *testing.T) {
	for _, tc := range []struct{ provider, model string }{
		{"openrouter", "claude-opus-5-5"}, // first-party ID on a router
		{"", "claude-opus-5-5"},           // custom endpoint
		{"claude", "claude-opus"},         // family prefix
	} {
		usage := runTask(t, tc.provider, tc.model, recorded{full(1000, 1000), true})
		if usage.CostKnown || usage.CostEstimated {
			t.Errorf("%q/%q usage = %+v, want unknown cost", tc.provider, tc.model, usage)
		}
	}
}

func TestRecordRequestReportedCostIsExact(t *testing.T) {
	reported := full(1_000_000, 0)
	reported.Cost, reported.CostSeen = 0.0421, true
	usage := runTask(t, "openrouter", "openai/gpt-4o-mini", recorded{reported, true})
	if !usage.CostKnown || usage.CostEstimated || usage.Cost != 0.0421 {
		t.Fatalf("usage = %+v, want exact reported $0.0421", usage)
	}

	// One estimate among exact requests marks the whole task estimated.
	usage = runTask(t, "openrouter", "openai/gpt-4o-mini",
		recorded{reported, true},
		recorded{full(1_000_000, 0), true}) // catalog: $0.15 input
	if !usage.CostKnown || !usage.CostEstimated || !near(usage.Cost, 0.0421+0.15) {
		t.Fatalf("mixed usage = %+v, want estimated $0.1921", usage)
	}
}

func TestRecordRequestSubscriptionCostIsUnknownWithoutReport(t *testing.T) {
	usage := runTask(t, "chatgpt", "gpt-5.5",
		recorded{full(5000, 900), true},
		recorded{model.RequestUsage{}, false}) // no usage in the response
	if usage.CostKnown || usage.CostEstimated {
		t.Fatalf("usage = %+v, want unknown cost (plan identity is not a charge)", usage)
	}
	if usage.UnknownRequests != 1 || usage.PromptKnown {
		t.Fatalf("token accounting = %+v, want the unreported request counted unknown", usage)
	}

	// A provider-reported charge is kept exactly, including a zero.
	reported := full(5000, 900)
	reported.Cost, reported.CostSeen = 0.021, true
	usage = runTask(t, "chatgpt", "gpt-5.5", recorded{reported, true})
	if !usage.CostKnown || usage.CostEstimated || usage.Cost != 0.021 {
		t.Fatalf("usage = %+v, want exact reported $0.021", usage)
	}
}

func TestRecordRequestUnreportedHidesCost(t *testing.T) {
	usage := runTask(t, "claude", "claude-opus-5-5",
		recorded{full(1_000_000, 0), true},
		recorded{full(1_000_000, 0), false}) // counts present but not reported
	if usage.CostKnown {
		t.Fatalf("usage = %+v, want unknown cost after an unreported request", usage)
	}
	if usage.PromptTokens != 1_000_000 {
		t.Fatalf("unreported counts were summed: %+v", usage)
	}

	promptOnly := model.RequestUsage{Prompt: 1000, PromptSeen: true}
	usage = runTask(t, "claude", "claude-opus-5-5", recorded{promptOnly, true})
	if usage.CostKnown || usage.CompletionKnown {
		t.Fatalf("prompt-only usage = %+v, want unknown cost and completion", usage)
	}
}

func TestRecordRequestSumsCacheAndReasoningTokens(t *testing.T) {
	first := full(1_000_000, 100_000)
	first.CacheRead, first.CacheWrite, first.Reasoning = 600_000, 100_000, 40_000
	second := full(10_000, 2_000)
	second.CacheRead, second.Reasoning = 8_000, 500
	ignored := full(1, 1)
	ignored.CacheRead, ignored.CacheWrite, ignored.Reasoning = 999, 999, 999
	usage := runTask(t, "claude", "claude-opus-5-5",
		recorded{first, true}, recorded{second, true}, recorded{ignored, false})
	if usage.CacheReadTokens != 608_000 || usage.CacheWriteTokens != 100_000 || usage.ReasoningTokens != 40_500 {
		t.Fatalf("breakdown = %+v, want reported requests only", usage)
	}

	// Cache rates apply: $4 input, $0.20 cache read, $5 cache write, $20 output.
	usage = runTask(t, "claude", "claude-opus-5-5", recorded{first, true})
	if want := 0.3*4 + 0.6*0.2 + 0.1*5 + 0.1*20; !usage.CostKnown || !near(usage.Cost, want) {
		t.Fatalf("cached cost = %+v, want %v", usage, want)
	}
}
