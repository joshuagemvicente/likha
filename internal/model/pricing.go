// Session-spend pricing: per-million-token prices in US dollars for the
// status bar's spend segment (spec tui-layout phase 1b, resolved decision 4).
// Mirrors windows.go: exact IDs first, then family prefixes, every entry
// naming its documentation basis. Models with no documented pricing report
// ok=false so the caller hides spend instead of estimating; the ChatGPT
// subscription row prices at exactly 0 (the caller renders $0.00).
package model

// pricingEntry is one documented pair of per-Mtok prices. prefix "" means
// only the exact ID matches.
type pricingEntry struct {
	id         string  // exact model ID
	prefix     string  // family prefix; "" = exact-ID-only entry
	prompt     float64 // US dollars per million prompt tokens
	completion float64 // US dollars per million completion tokens
}

// pricingTable is the curated price catalog, sourced like windows.go.
var pricingTable = []pricingEntry{
	// Exact IDs first.
	{"gpt-4o-mini", "", 0.15, 0.60},       // OpenAI docs: platform.openai.com/docs/models pricing
	{"gpt-4o", "", 2.50, 10.00},           // OpenAI docs: platform.openai.com/docs/models pricing
	{"o3", "", 2.00, 8.00},                // OpenAI docs: platform.openai.com/docs/models pricing
	{"o1", "", 15.00, 60.00},              // OpenAI docs: platform.openai.com/docs/models pricing
	{"deepseek-chat", "", 0.27, 1.10},     // DeepSeek docs: api-docs.deepseek.com pricing
	{"deepseek-reasoner", "", 0.27, 1.10}, // DeepSeek docs: api-docs.deepseek.com pricing
	{"claude-opus-4-1", "", 15, 75},       // Anthropic docs: docs.anthropic.com model comparison pricing
	{"claude-opus-4", "", 15, 75},         // Anthropic docs: docs.anthropic.com model comparison pricing
	{"claude-sonnet-4-5", "", 3, 15},      // Anthropic docs: docs.anthropic.com model comparison pricing
	{"claude-sonnet-4", "", 3, 15},        // Anthropic docs: docs.anthropic.com model comparison pricing
	{"claude-3-7-sonnet", "", 3, 15},      // Anthropic docs: docs.anthropic.com model comparison pricing
	{"claude-haiku-4-5", "", 0.80, 4.00},  // Anthropic docs: docs.anthropic.com model comparison pricing
	// Family prefixes: any model whose ID starts with the prefix shares the
	// family's documented price.
	{prefix: "claude-opus", prompt: 15, completion: 75},          // Anthropic docs pricing
	{prefix: "claude-sonnet", prompt: 3, completion: 15},         // Anthropic docs pricing
	{prefix: "claude-haiku", prompt: 0.80, completion: 4.00},     // Anthropic docs pricing
	{prefix: "claude-3-5-haiku", prompt: 0.80, completion: 4.00}, // Anthropic docs pricing
	{prefix: "deepseek", prompt: 0.27, completion: 1.10},         // DeepSeek docs pricing
	// Undocumented; conservative placeholder. These ride the vendors'
	// published pay-per-token cards or routers, but no per-Mtok price is
	// citable today — placeholder until the live-probe/documentation pass.
	{prefix: "minimax", prompt: 0.30, completion: 1.20},
	{prefix: "kimi", prompt: 0.30, completion: 1.20},
	{prefix: "glm", prompt: 0.30, completion: 1.20},
}

// subscriptionZeroIDs lists the ChatGPT-curated models that bill the user's
// ChatGPT plan (provider.go, AuthOAuth) instead of tokens, so they price at
// exactly 0 with ok=true: a known-known zero, not an unknown. Placeholder
// subject to the live-probe pass like the rest of the fictional-catalog
// rows.
var subscriptionZeroIDs = []string{
	"gpt-5.5",             // provider.go: ChatGPTModels
	"gpt-5.4",             // provider.go: ChatGPTModels
	"gpt-5.4-mini",        // provider.go: ChatGPTModels
	"gpt-5.3-codex",       // provider.go: ChatGPTModels
	"gpt-5.3-codex-spark", // provider.go: ChatGPTModels
	"gpt-6-sol",           // provider.go: ChatGPTModels
	"gpt-6-luna",          // provider.go: ChatGPTModels
}

// TurnCost prices one model turn in US dollars from the documented catalog.
// ok=false means no documented pricing: callers must hide spend rather than
// estimate. Subscription rows (the ChatGPT/Codex provider's models) price
// at exactly 0: the caller renders $0.00, not a hidden segment. Negative
// token counts are treated as 0.
func TurnCost(modelName string, promptTokens, completionTokens int64) (float64, bool) {
	name := catalogSlug(modelName)
	if name == "" {
		return 0, false
	}
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	price := func(entry pricingEntry) (float64, bool) {
		return float64(promptTokens)/1_000_000*entry.prompt +
			float64(completionTokens)/1_000_000*entry.completion, true
	}
	for _, entry := range pricingTable {
		if entry.prefix == "" && entry.id == name {
			return price(entry)
		}
	}
	for _, entry := range pricingTable {
		if entry.prefix != "" && hasCatalogPrefix(name, entry.prefix) {
			return price(entry)
		}
	}
	for _, id := range subscriptionZeroIDs {
		if id == name {
			return 0, true
		}
	}
	return 0, false
}

// hasCatalogPrefix reports whether name starts with prefix, mirroring the
// windows.go matching rule.
func hasCatalogPrefix(name, prefix string) bool {
	return len(name) >= len(prefix) && name[:len(prefix)] == prefix
}
