// Context-window catalog: statically documented context-window sizes for
// models Likha may connect to. Only PUBLICLY DOCUMENTED numbers appear here,
// each with a comment naming its documentation basis; unknown model IDs (for
// example ChatGPT-curated gpt-5.x endpoints, whose window is not publicly
// documented) resolve to ok=false so the UI renders "ctx —" instead of a
// fabricated size. Chat-completions usage plumbing lives in client.go; the
// shared TokenUsage totals type is also defined here.
package model

import "strings"

// TokenUsage holds the final prompt/completion token totals of one
// response, as reported by the provider in the stream's usage event.
type TokenUsage struct {
	Prompt     int64
	Completion int64
	PromptSeen bool // true only when the response reported an input/prompt count
}

// contextWindowEntry is one documented context-window size. prefix "" means
// only the exact ID matches.
type contextWindowEntry struct {
	id     string // exact model ID, or family prefix when suffixEmpty
	prefix string // family prefix; "" = exact-ID-only entry
	window int64
}

// contextWindows is the static, documentation-backed catalog. Exact IDs are
// matched first, then family prefixes.
var contextWindows = []contextWindowEntry{
	// Exact IDs first.
	{"deepseek-chat", "", 128000},     // DeepSeek docs: api-docs.deepseek.com, 128K context
	{"deepseek-reasoner", "", 128000}, // DeepSeek docs: api-docs.deepseek.com, 128K context
	{"claude-opus-4-1", "", 200000},   // Anthropic docs: docs.anthropic.com model comparison, 200K
	{"claude-opus-4", "", 200000},     // Anthropic docs: docs.anthropic.com model comparison, 200K
	{"claude-sonnet-4-5", "", 200000}, // Anthropic docs: docs.anthropic.com model comparison, 200K
	{"claude-sonnet-4", "", 200000},   // Anthropic docs: docs.anthropic.com model comparison, 200K
	{"claude-haiku-4-5", "", 200000},  // Anthropic docs: docs.anthropic.com model comparison, 200K
	{"claude-3-7-sonnet", "", 200000}, // Anthropic docs: docs.anthropic.com model comparison, 200K
	{"o1-mini", "", 128000},           // OpenAI docs: platform.openai.com/docs/models, o1-mini 128K
	{"o1-preview", "", 128000},        // OpenAI docs: platform.openai.com/docs/models, o1-preview 128K
	// Family prefixes: any model whose ID starts with the prefix.
	{prefix: "gpt-4o", window: 128000},            // OpenAI docs: platform.openai.com/docs/models, 128K
	{prefix: "gpt-4.1", window: 1000000},          // OpenAI docs: platform.openai.com/docs/models, 1M
	{prefix: "o1", window: 200000},                // OpenAI docs: platform.openai.com/docs/models, 200K
	{prefix: "o3", window: 200000},                // OpenAI docs: platform.openai.com/docs/models, 200K
	{prefix: "o4-mini", window: 200000},           // OpenAI docs: platform.openai.com/docs/models, 200K
	{prefix: "claude-", window: 200000},           // Anthropic docs: docs.anthropic.com model comparison, 200K
	{prefix: "deepseek-chat", window: 128000},     // DeepSeek docs: api-docs.deepseek.com, 128K
	{prefix: "deepseek-reasoner", window: 128000}, // DeepSeek docs: api-docs.deepseek.com, 128K
}

// ContextWindow returns the documented context-window size for modelName:
// exact model IDs first, then documented family prefixes. Documented
// publicly only; unknown model IDs (including ChatGPT-curated gpt-5.x
// endpoints, whose context window is not publicly documented) report
// ok=false.
func ContextWindow(modelName string) (int64, bool) {
	name := strings.TrimSpace(modelName)
	if name == "" {
		return 0, false
	}
	for _, entry := range contextWindows {
		if entry.prefix == "" && entry.id == name {
			return entry.window, true
		}
	}
	for _, entry := range contextWindows {
		if entry.prefix != "" && strings.HasPrefix(name, entry.prefix) {
			return entry.window, true
		}
	}
	return 0, false
}

// ResolveContextWindow resolves a context limit for the selected provider and
// model. A positive user override takes precedence over positive provider
// metadata, followed by Likha's documented model catalog. Provider-specific
// values are supplied by the caller; the static catalog is keyed by model ID,
// so providerID does not affect the catalog fallback.
func ResolveContextWindow(providerID, modelID string, override, metadata int64) (int64, bool) {
	if override > 0 {
		return override, true
	}
	if metadata > 0 {
		return metadata, true
	}
	return ContextWindow(modelID)
}
