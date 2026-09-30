// Model display-name catalog: curated, human-readable names for the models
// Lisa may connect to, maintained beside the context-window table in
// windows.go (spec tui-layout phase 1b, resolved decision 3). Display-only:
// slugs remain canonical in /models, stored config, and API calls. This is a
// naming table, not a documentation source — entries whose slug is not yet
// publicly confirmed keep the sanitized catalog slug until the live-probe
// pass.
package model

import "strings"

// displayNameEntry is one curated display name. prefix "" means only the
// exact ID matches.
type displayNameEntry struct {
	id     string // exact model ID
	prefix string // family prefix; "" = exact-ID-only entry
	name   string // display name shown in the status bar
}

// displayNames is the curated catalog: provider defaults and catalog IDs of
// provider.go, exact IDs first, then family prefixes.
var displayNames = []displayNameEntry{
	// Exact IDs first.
	{"gpt-5.5", "", "GPT-5.5"},                         // provider.go: ChatGPTModels (subscription row)
	{"gpt-5.4", "", "GPT-5.4"},                         // provider.go: ChatGPTModels
	{"gpt-5.4-mini", "", "GPT-5.4 mini"},               // provider.go: ChatGPTModels
	{"gpt-5.3-codex-spark", "", "GPT-5.3 Codex Spark"}, // provider.go: ChatGPTModels
	{"gpt-5.3-codex", "", "GPT-5.3 Codex"},             // provider.go: opencode-zen default
	{"gpt-6-sol", "", "GPT-6 Sol"},                     // provider.go: ChatGPTModels (placeholder name pending release docs)
	{"gpt-6-luna", "", "GPT-6 Luna"},                   // provider.go: ChatGPTModels (placeholder name pending release docs)
	{"gpt-4o-mini", "", "GPT-4o mini"},                 // provider.go: openai default; OpenAI docs model list
	{"glm-5.3-flash", "", "GLM-5.3 Flash"},             // provider.go: opencode-go default (placeholder name pending live probe)
	{"deepseek-chat", "", "DeepSeek Chat"},             // DeepSeek docs: api-docs.deepseek.com model list
	{"deepseek-reasoner", "", "DeepSeek Reasoner"},     // DeepSeek docs: api-docs.deepseek.com model list
	{"minimax-m3", "", "MiniMax M3"},                   // provider.go predecessor catalogs (placeholder name pending live probe)
	{"kimi-k3", "", "Kimi K3"},                         // Moonshot model list (placeholder name pending live probe)
	{"claude-opus-4-1", "", "Claude Opus 4.1"},         // Anthropic docs: docs.anthropic.com model comparison
	{"claude-opus-4", "", "Claude Opus 4"},             // Anthropic docs: docs.anthropic.com model comparison
	{"claude-sonnet-4-5", "", "Claude Sonnet 4.5"},     // Anthropic docs: docs.anthropic.com model comparison
	{"claude-sonnet-4", "", "Claude Sonnet 4"},         // Anthropic docs: docs.anthropic.com model comparison
	{"claude-haiku-4-5", "", "Claude Haiku 4.5"},       // Anthropic docs: docs.anthropic.com model comparison
	{"claude-3-7-sonnet", "", "Claude 3.7 Sonnet"},     // Anthropic docs: docs.anthropic.com model comparison
	// Family prefixes: any model whose ID starts with the prefix shares the
	// family's display name (gpt-4o-2024-08-06 → "GPT-4o").
	{prefix: "gpt-4o", name: "GPT-4o"},  // OpenAI docs model list
	{prefix: "claude-", name: "Claude"}, // Anthropic docs model list
}

// catalogSlug trims surrounding whitespace and strips one
// "<publisher>/" prefix (OpenRouter compound slugs) so
// "openai/gpt-4o-mini" resolves to its base model ID. Empty input → "".
// Shared by the display-name and pricing catalogs.
func catalogSlug(modelName string) string {
	name := strings.TrimSpace(modelName)
	if name == "" {
		return ""
	}
	if i := strings.IndexByte(name, '/'); i > 0 && i+1 < len(name) {
		name = name[i+1:]
	}
	return name
}

// ModelDisplayName returns the curated display name for modelName, falling
// back to the unchanged slug when the catalog has no entry. Display-only:
// slugs remain canonical in /models, stored config, and API calls.
// OpenRouter-style compound slugs resolve to the base model's name; empty
// input returns "".
func ModelDisplayName(modelName string) string {
	name := catalogSlug(modelName)
	if name == "" {
		return ""
	}
	for _, entry := range displayNames {
		if entry.prefix == "" && entry.id == name {
			return entry.name
		}
	}
	for _, entry := range displayNames {
		if entry.prefix != "" && strings.HasPrefix(name, entry.prefix) {
			return entry.name
		}
	}
	return name
}
