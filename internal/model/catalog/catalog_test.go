package catalog_test

import (
	"testing"

	"likha/internal/model"
	"likha/internal/model/catalog"
)

// knownDefaultGaps lists provider defaults the catalog cannot describe, with
// the reason. A new gap fails the test until it is fixed or listed here.
var knownDefaultGaps = map[string]string{
	"bedrock": "models.dev lists Bedrock Claude Haiku 4.5 rows only; the default anthropic.claude-3-5-haiku-20241022-v1:0 awaits the Bedrock provider probe",
}

func TestEmbeddedCatalogLoads(t *testing.T) {
	f, err := catalog.Default()
	if err != nil {
		t.Fatal(err)
	}
	if f.Schema != catalog.Schema || f.Generated == "" || f.Upstream.Name != catalog.SourceUpstream {
		t.Fatalf("catalog header = %+v %+v", f.Schema, f.Upstream)
	}
}

func TestEveryProviderIsCovered(t *testing.T) {
	f, err := catalog.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range model.Providers {
		cp, ok := f.Providers[p.Name]
		if !ok {
			t.Errorf("provider %s missing from the catalog", p.Name)
			continue
		}
		// Every provider with an upstream mapping has rows; curated-only
		// providers may be empty (dialagram) or curated (chatgpt).
		if cp.Upstream != "" && len(cp.Models) == 0 {
			t.Errorf("provider %s maps to %s but has no models", p.Name, cp.Upstream)
		}
		if p.DefaultModel == "" {
			continue
		}
		_, ok = f.Lookup(p.Name, p.DefaultModel)
		if reason, gap := knownDefaultGaps[p.Name]; gap {
			if ok {
				t.Errorf("%s default %s now has an entry; remove the known gap (%s)", p.Name, p.DefaultModel, reason)
			}
			continue
		}
		if !ok {
			t.Errorf("%s default %s has no catalog entry", p.Name, p.DefaultModel)
		}
	}
}

func TestChatGPTIsSubscriptionBilled(t *testing.T) {
	if catalog.Billing("chatgpt") != catalog.BillingSubscription {
		t.Fatal("chatgpt is not subscription-billed")
	}
}

// Values cross-checked against Anthropic's models overview on 2026-10-04.
func TestClaudeRowsMatchAnthropicDocs(t *testing.T) {
	cases := []struct {
		id              string
		input, output   float64
		context, outMax int64
	}{
		{"claude-opus-5-5", 4, 20, 1000000, 128000},
		{"claude-sonnet-5-5", 2, 10, 1000000, 128000},
		{"claude-fable-5-1", 10, 50, 1000000, 128000},
		{"claude-haiku-4-5", 1, 5, 200000, 64000},
		// "Other Claude models, including Claude Sonnet 4.5, have a
		// 200k-token context window" (context-windows doc, 2026-10-04);
		// models.dev lists 1M, corrected in overrides.json.
		{"claude-sonnet-4-5", 3, 15, 200000, 64000},
		{"claude-sonnet-4-5-20250929", 3, 15, 200000, 64000},
	}
	for _, tc := range cases {
		m, ok := catalog.Lookup("claude", tc.id)
		if !ok || m.Cost == nil {
			t.Errorf("claude/%s missing or unpriced", tc.id)
			continue
		}
		if m.Cost.Input != tc.input || m.Cost.Output != tc.output || m.Context != tc.context || m.Output != tc.outMax {
			t.Errorf("claude/%s = $%g/$%g ctx %d out %d, want $%g/$%g ctx %d out %d",
				tc.id, m.Cost.Input, m.Cost.Output, m.Context, m.Output, tc.input, tc.output, tc.context, tc.outMax)
		}
	}
}

// DeepSeek bills off-peak hours at half the peak card; time-of-day pricing
// is a non-goal, so the catalog keeps the peak card and estimates are an
// upper bound (api-docs.deepseek.com/quick_start/pricing, 2026-10-04).
func TestDeepSeekRowsUsePeakCard(t *testing.T) {
	for id, want := range map[string][3]float64{
		"deepseek-flash":               {0.3, 1.2, 0.006},
		"deepseek-v4-flash":            {0.3, 1.2, 0.006},
		"deepseek-v4-flash-vision-exp": {0.3, 1.2, 0.006},
		"deepseek-v4-pro":              {1.32, 3.96, 0.044},
	} {
		m, ok := catalog.Lookup("deepseek", id)
		if !ok || m.Cost == nil || m.Cost.CacheRead == nil {
			t.Errorf("deepseek/%s missing or unpriced", id)
			continue
		}
		if got := [3]float64{m.Cost.Input, m.Cost.Output, *m.Cost.CacheRead}; got != want || m.Source != catalog.SourceOverride {
			t.Errorf("deepseek/%s = %v (%s), want peak card %v", id, got, m.Source, want)
		}
	}
}

func TestLookupMatchesExactPairOnly(t *testing.T) {
	if _, ok := catalog.Lookup("claude", "  claude-opus-5-5 "); !ok {
		t.Error("surrounding whitespace should be trimmed")
	}
	// The same model ID on another provider never borrows the price.
	if _, ok := catalog.Lookup("openrouter", "claude-opus-5-5"); ok {
		t.Error("openrouter borrowed a claude row")
	}
	for _, id := range []string{"claude-opus", "claude-opus-5-", "anthropic/claude-opus-5-5", "", "gpt-9-future"} {
		if m, ok := catalog.Lookup("claude", id); ok {
			t.Errorf("claude/%q matched %+v; only exact IDs match", id, m)
		}
	}
	if _, ok := catalog.Lookup("nope", "claude-opus-5-5"); ok {
		t.Error("unknown provider matched")
	}
}

func TestRatesForTiers(t *testing.T) {
	m, ok := catalog.Lookup("openai", "gpt-5.5")
	if !ok || m.Cost == nil || len(m.Cost.Tiers) == 0 {
		t.Skip("gpt-5.5 has no tiers in this catalog")
	}
	tier := m.Cost.Tiers[0]
	if got := m.Cost.RatesFor(tier.Above); got.Input != m.Cost.Input {
		t.Errorf("at the threshold the base rate applies, got %v", got.Input)
	}
	if got := m.Cost.RatesFor(tier.Above + 1); got.Input != tier.Input || got.Output != tier.Output {
		t.Errorf("above the threshold the tier applies, got %+v", got)
	}
	cost := catalog.Cost{
		Rates: catalog.Rates{Input: 1, Output: 2},
		Tiers: []catalog.Tier{{Above: 100, Rates: catalog.Rates{Input: 3, Output: 4}}, {Above: 200, Rates: catalog.Rates{Input: 5, Output: 6}}},
	}
	for prompt, want := range map[int64]float64{50: 1, 150: 3, 250: 5} {
		if got := cost.RatesFor(prompt).Input; got != want {
			t.Errorf("RatesFor(%d).Input = %v, want %v", prompt, got, want)
		}
	}
}

func TestPromptLimitPrefersInputLimit(t *testing.T) {
	if got := (catalog.Model{Context: 1000, Input: 800}).PromptLimit(); got != 800 {
		t.Errorf("PromptLimit with input = %d", got)
	}
	if got := (catalog.Model{Context: 1000}).PromptLimit(); got != 1000 {
		t.Errorf("PromptLimit without input = %d", got)
	}
}

func TestParseRejectsInvalidCatalogs(t *testing.T) {
	cases := map[string]string{
		"bad json":         `{`,
		"wrong schema":     `{"schema": 2, "providers": {}}`,
		"missing source":   `{"schema": 1, "providers": {"p": {"models": {"m": {}}}}}`,
		"override no ref":  `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "override", "verified": "2026-10-01"}}}}}`,
		"negative price":   `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "models.dev", "cost": {"input": -1, "output": 1}}}}}}`,
		"negative limit":   `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "models.dev", "context": -1}}}}}`,
		"unsorted tiers":   `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "models.dev", "cost": {"input": 1, "output": 1, "tiers": [{"above": 200, "input": 1, "output": 1}, {"above": 100, "input": 1, "output": 1}]}}}}}}`,
		"unknown billing":  `{"schema": 1, "providers": {"p": {"billing": "free", "models": {}}}}`,
		"negative cache":   `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "models.dev", "cost": {"input": 1, "output": 1, "cache_read": -0.1}}}}}}`,
		"zero tier above":  `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "models.dev", "cost": {"input": 1, "output": 1, "tiers": [{"above": 0, "input": 1, "output": 1}]}}}}}}`,
		"unknown source":   `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "guess"}}}}}`,
		"override no date": `{"schema": 1, "providers": {"p": {"models": {"m": {"source": "override", "ref": "x"}}}}}`,
	}
	for name, raw := range cases {
		if _, err := catalog.Parse([]byte(raw)); err == nil {
			t.Errorf("%s: Parse accepted it", name)
		}
	}
}
