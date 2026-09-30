package model

import "testing"

func TestModelDisplayNameCatalog(t *testing.T) {
	cases := []struct {
		model, want string
	}{
		// Exact IDs.
		{"gpt-5.5", "GPT-5.5"},
		{"gpt-5.4", "GPT-5.4"},
		{"gpt-5.4-mini", "GPT-5.4 mini"},
		{"gpt-5.3-codex-spark", "GPT-5.3 Codex Spark"},
		{"gpt-5.3-codex", "GPT-5.3 Codex"},
		{"gpt-6-sol", "GPT-6 Sol"},
		{"gpt-6-luna", "GPT-6 Luna"},
		{"gpt-4o-mini", "GPT-4o mini"},
		{"deepseek-chat", "DeepSeek Chat"},
		{"deepseek-reasoner", "DeepSeek Reasoner"},
		{"minimax-m3", "MiniMax M3"},
		{"kimi-k3", "Kimi K3"},
		{"glm-5.3-flash", "GLM-5.3 Flash"},
		{"claude-opus-4-1", "Claude Opus 4.1"},
		{"claude-sonnet-4-5", "Claude Sonnet 4.5"},
		{"claude-3-7-sonnet", "Claude 3.7 Sonnet"},
		// Exact IDs win over family prefixes.
		{"gpt-4o", "GPT-4o"},
		// Family prefixes.
		{"gpt-4o-2024-08-06", "GPT-4o"},
		{"claude-opus-4-20250514", "Claude"},
		{"claude-something-new", "Claude"},
		// Compound slugs: one "<publisher>/" prefix is stripped.
		{"openai/gpt-4o-mini", "GPT-4o mini"},
		{"anthropic/claude-sonnet-4-5", "Claude Sonnet 4.5"},
		{" deepseek-chat ", "DeepSeek Chat"},
		// Fallback: unknown slugs stay unchanged (trimmed only).
		{"gpt-9-future", "gpt-9-future"},
		{"bedrock-only-model", "bedrock-only-model"},
		{"anthropic.claude-3-5-haiku-20241022-v1:0", "anthropic.claude-3-5-haiku-20241022-v1:0"},
		// Empty.
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := ModelDisplayName(tc.model); got != tc.want {
			t.Errorf("ModelDisplayName(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}
