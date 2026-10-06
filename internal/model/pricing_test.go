package model

import (
	"math"
	"testing"
)

// near compares dollar amounts computed in floating point.
func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func full(prompt, completion int64) RequestUsage {
	return RequestUsage{Prompt: prompt, Completion: completion, PromptSeen: true, CompletionSeen: true}
}

// Prices cross-checked against Anthropic's models overview and the
// models.dev catalog on 2026-10-04 (specs/model-metadata).
func TestRequestCostUsesProviderScopedCatalog(t *testing.T) {
	cases := []struct {
		provider, model string
		usage           RequestUsage
		want            float64
	}{
		{"claude", "claude-opus-5-5", full(1_000_000, 0), 4},
		{"claude", "claude-opus-5-5", full(0, 1_000_000), 20},
		{"claude", "claude-haiku-4-5", full(1_000_000, 1_000_000), 6},
		{"openai", "gpt-4o-mini", full(1_000_000, 0), 0.15},
		{"openai", "gpt-4o", full(100_000, 100_000), 1.25},
	}
	for _, tc := range cases {
		got, source := RequestCost(tc.provider, tc.model, tc.usage)
		if source != CostEstimated || !near(got, tc.want) {
			t.Errorf("%s/%s %+v = (%v, %v), want (%v, estimated)", tc.provider, tc.model, tc.usage, got, source, tc.want)
		}
	}
}

func TestRequestCostNeverBorrowsOrGuesses(t *testing.T) {
	for _, tc := range []struct{ provider, model string }{
		{"", "gpt-4o"},                          // custom endpoint
		{"openrouter", "claude-opus-5-5"},       // first-party ID on a router
		{"claude", "claude-opus"},               // family prefix
		{"claude", "anthropic/claude-opus-5-5"}, // vendor-prefixed slug
		{"opencode-go", "kimi-k3-unlisted"},     // former placeholder family
		{"openai", "gpt-9-future"},
		{"dialagram", "anything"},
	} {
		if got, source := RequestCost(tc.provider, tc.model, full(1000, 1000)); source.Known() {
			t.Errorf("%s/%s priced at %v (%v); want unknown", tc.provider, tc.model, got, source)
		}
	}
}

func TestRequestCostPricesCacheAndReasoningTokens(t *testing.T) {
	// claude-opus-5-5: $4 input, $0.20 cache read, $5 cache write, $20 output.
	u := full(1_000_000, 0)
	u.CacheRead = 600_000
	u.CacheWrite = 100_000
	got, _ := RequestCost("claude", "claude-opus-5-5", u)
	if want := 0.3*4 + 0.6*0.2 + 0.1*5; !near(got, want) {
		t.Errorf("cached request = %v, want %v", got, want)
	}
	// gpt-4o-mini has cache_read $0.075 and no cache_write: writes price at input.
	u = full(1_000_000, 0)
	u.CacheWrite = 500_000
	got, _ = RequestCost("openai", "gpt-4o-mini", u)
	if want := 0.15; !near(got, want) {
		t.Errorf("cache writes without a rate = %v, want input price %v", got, want)
	}
	// Reasoning tokens without a separate rate price at the output rate.
	u = full(0, 1_000_000)
	u.Reasoning = 400_000
	got, _ = RequestCost("claude", "claude-opus-5-5", u)
	if !near(got, 20) {
		t.Errorf("reasoning without a rate = %v, want 20", got)
	}
	// Subsets larger than their totals are clamped, never over-priced.
	u = full(1000, 1000)
	u.CacheRead, u.CacheWrite, u.Reasoning = 5000, 5000, 5000
	got, _ = RequestCost("claude", "claude-opus-5-5", u)
	if want := (1000*0.2 + 1000*20) / 1e6; !near(got, want) {
		t.Errorf("clamped request = %v, want %v", got, want)
	}
}

func TestRequestCostAppliesTiers(t *testing.T) {
	// openai gpt-5.5: $5/$30 base, $10/$45 above 272K prompt tokens.
	if got, _ := RequestCost("openai", "gpt-5.5", full(272_000, 0)); !near(got, 0.272*5) {
		t.Errorf("at the threshold = %v, want base rate", got)
	}
	if got, _ := RequestCost("openai", "gpt-5.5", full(300_000, 100_000)); !near(got, 0.3*10+0.1*45) {
		t.Errorf("above the threshold = %v, want tier rate", got)
	}
}

func TestRequestCostPrefersReportedCost(t *testing.T) {
	u := full(1_000_000, 1_000_000)
	u.Cost, u.CostSeen = 0.0123, true
	if got, source := RequestCost("openrouter", "anything/at-all", u); source != CostReported || got != 0.0123 || !source.Exact() {
		t.Errorf("reported cost = (%v, %v), want (0.0123, reported)", got, source)
	}
	u.Cost = math.NaN()
	if _, source := RequestCost("openrouter", "anything/at-all", u); source == CostReported {
		t.Error("a NaN reported cost was trusted")
	}
}

func TestRequestCostSubscriptionDoesNotGuessCharges(t *testing.T) {
	for _, id := range []string{"not-in-the-list", "gpt-5.5"} {
		got, source := RequestCost("chatgpt", id, full(1234567, 987654))
		if got != 0 || source.Known() {
			t.Errorf("chatgpt/%s = (%v, %v), want unknown without a reported charge", id, got, source)
		}
		usage := full(1000, 100)
		usage.Cost, usage.CostSeen = 0.25, true
		if got, source := RequestCost("chatgpt", id, usage); got != 0.25 || source != CostReported {
			t.Errorf("chatgpt/%s dropped reported credits: (%v, %v)", id, got, source)
		}
		usage.Cost = 0
		if got, source := RequestCost("chatgpt", id, usage); got != 0 || source != CostReported {
			t.Errorf("chatgpt/%s dropped reported zero: (%v, %v)", id, got, source)
		}
	}
}

func TestRequestCostNeedsBothCounts(t *testing.T) {
	u := full(1000, 1000)
	u.CompletionSeen = false
	if _, source := RequestCost("claude", "claude-opus-5-5", u); source.Known() {
		t.Error("a prompt-only report was priced")
	}
	u = full(-5, -10)
	if got, source := RequestCost("claude", "claude-opus-5-5", u); source != CostEstimated || got != 0 {
		t.Errorf("negative counts = (%v, %v), want (0, estimated)", got, source)
	}
}

func TestModelPrice(t *testing.T) {
	if in, out, ok := ModelPrice("claude", "claude-opus-5-5"); !ok || in != 4 || out != 20 {
		t.Errorf("ModelPrice(claude-opus-5-5) = %v %v %v", in, out, ok)
	}
	if _, _, ok := ModelPrice("chatgpt", "gpt-5.5"); ok {
		t.Error("subscription models have no per-token price to show")
	}
	if _, _, ok := ModelPrice("openrouter", "claude-opus-5-5"); ok {
		t.Error("ModelPrice borrowed across providers")
	}
}
