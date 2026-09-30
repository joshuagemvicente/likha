package model

import "testing"

func TestTurnCostCatalog(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		promptC float64 // expected cost for 1M prompt tokens, 0 completion
	}{
		// Exact IDs.
		{name: "gpt-4o-mini", model: "gpt-4o-mini", promptC: 0.15},
		{name: "gpt-4o", model: "gpt-4o", promptC: 2.50},
		{name: "o3", model: "o3", promptC: 2.00},
		{name: "o1", model: "o1", promptC: 15.00},
		{name: "deepseek-chat", model: "deepseek-chat", promptC: 0.27},
		{name: "claude opus", model: "claude-opus-4-1", promptC: 15},
		{name: "claude sonnet", model: "claude-sonnet-4-5", promptC: 3},
		{name: "claude haiku", model: "claude-haiku-4-5", promptC: 0.80},
		// Family prefixes.
		{name: "claude-3-5-haiku", model: "claude-3-5-haiku-20241022", promptC: 0.80},
		{name: "deepseek prefix", model: "deepseek-coder", promptC: 0.27},
		{name: "kimi prefix", model: "kimi-k3", promptC: 0.30},
		{name: "minimax prefix", model: "minimax-m3", promptC: 0.30},
		{name: "glm prefix", model: "glm-5.3-flash", promptC: 0.30},
		// Compound slugs.
		{name: "openrouter gpt-4o-mini", model: "openai/gpt-4o-mini", promptC: 0.15},
		{name: "openrouter claude", model: "anthropic/claude-sonnet-4-5", promptC: 3},
	}
	for _, tc := range cases {
		cost, ok := TurnCost(tc.model, 1000000, 0)
		if !ok {
			t.Errorf("%s: TurnCost(%q, ...) reported no pricing", tc.name, tc.model)
			continue
		}
		if cost != tc.promptC {
			t.Errorf("%s: TurnCost(%q, 1000000, 0) = %v, want %v", tc.name, tc.model, cost, tc.promptC)
		}
	}
}

func TestTurnCostCompletionPricing(t *testing.T) {
	if cost, ok := TurnCost("gpt-4o", 0, 100000); !ok || cost != 1.00 {
		t.Fatalf("gpt-4o completion cost = %v, ok=%v, want 1.00", cost, ok)
	}
	if cost, ok := TurnCost("gpt-4o", 100000, 100000); !ok || cost != 1.25 {
		t.Fatalf("gpt-4o combined cost = %v, ok=%v, want 1.25", cost, ok)
	}
}

func TestTurnCostSubscriptionZero(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.3-codex-spark", "gpt-6-sol", "gpt-6-luna"} {
		cost, ok := TurnCost(model, 1234567, 987654)
		if !ok || cost != 0 {
			t.Errorf("subscription model %q = (%v, %v), want (0, true)", model, cost, ok)
		}
	}
}

func TestTurnCostUnknownHides(t *testing.T) {
	for _, model := range []string{"", "   ", "gpt-9-future", "o4-mini", "o1-mini", "total-guess", "openai/", "openrouter/unknown-model"} {
		if cost, ok := TurnCost(model, 100, 100); ok {
			t.Errorf("unknown model %q reported pricing %v; spend must hide instead", model, cost)
		}
	}
}

func TestTurnCostNegativeTokensClamp(t *testing.T) {
	if cost, ok := TurnCost("gpt-4o", -5, -10); !ok || cost != 0 {
		t.Fatalf("negative tokens = %v, ok=%v, want 0", cost, ok)
	}
	if cost, ok := TurnCost("gpt-4o", -5, 100000); !ok || cost != 1.00 {
		t.Fatalf("negative prompt = %v, ok=%v, want 1.00", cost, ok)
	}
	if cost, ok := TurnCost("gpt-4o", 100000, -1); !ok || cost != 0.25 {
		t.Fatalf("negative completion = %v, ok=%v, want 0.25", cost, ok)
	}
}
