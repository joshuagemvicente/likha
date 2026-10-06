package model

import "testing"

func TestResolveContextWindowPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		modelID  string
		override int64
		metadata int64
		want     int64
		wantOK   bool
	}{
		{
			name:     "user override wins",
			modelID:  "gpt-4o",
			override: 64000,
			metadata: 32000,
			want:     64000,
			wantOK:   true,
		},
		{
			name:     "provider metadata wins over catalog",
			modelID:  "gpt-4o",
			metadata: 32000,
			want:     32000,
			wantOK:   true,
		},
		{
			name:    "catalog fallback for the exact pair",
			modelID: "gpt-4o",
			want:    128000,
			wantOK:  true,
		},
		{
			name:    "catalog input limit is the prompt budget",
			modelID: "gpt-5.5",
			want:    922000,
			wantOK:  true,
		},
		{
			name:    "unknown model stays unknown",
			modelID: "gpt-9-future",
		},
		{
			name:    "no prefix matching",
			modelID: "gpt-4o-2099-01-01",
		},
		{
			name:    "empty model ID stays unknown",
			modelID: "",
		},
		{
			name:    "whitespace model ID stays unknown",
			modelID: " \t ",
		},
		{
			name:    "outer model ID whitespace is trimmed",
			modelID: "  gpt-4o  ",
			want:    128000,
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveContextWindow("openai", tt.modelID, tt.override, tt.metadata)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ResolveContextWindow(%q, %q, %d, %d) = (%d, %v), want (%d, %v)",
					"openai", tt.modelID, tt.override, tt.metadata, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestResolveContextWindowIgnoresNonPositiveLimits(t *testing.T) {
	tests := []struct {
		name     string
		modelID  string
		override int64
		metadata int64
		want     int64
		wantOK   bool
	}{
		{
			name:     "negative override falls through to metadata",
			modelID:  "unknown-model",
			override: -1,
			metadata: 24000,
			want:     24000,
			wantOK:   true,
		},
		{
			name:     "zero override falls through to metadata",
			modelID:  "unknown-model",
			metadata: 24000,
			want:     24000,
			wantOK:   true,
		},
		{
			name:     "negative metadata falls through to catalog",
			modelID:  "gpt-4o",
			override: -1,
			metadata: -2,
			want:     128000,
			wantOK:   true,
		},
		{
			name:     "non-positive values do not invent unknown limit",
			modelID:  "unknown-model",
			override: 0,
			metadata: -2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveContextWindow("openai", tt.modelID, tt.override, tt.metadata)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ResolveContextWindow(%q, %q, %d, %d) = (%d, %v), want (%d, %v)",
					"openai", tt.modelID, tt.override, tt.metadata, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// A custom endpoint (no canonical provider) never borrows a catalog row
// from a predefined provider (specs/model-metadata: exact pair only).
func TestResolveContextWindowWithEmptyOrWhitespaceProviderID(t *testing.T) {
	for _, providerID := range []string{"", " \t "} {
		if got, ok := ResolveContextWindow(providerID, "gpt-4o", 0, 0); ok {
			t.Errorf("provider ID %q borrowed a catalog window %d", providerID, got)
		}
		if got, ok := ResolveContextWindow(providerID, "gpt-4o", 0, 32000); !ok || got != 32000 {
			t.Errorf("provider metadata with provider ID %q = (%d, %v), want (32000, true)", providerID, got, ok)
		}
	}
	// The same model on another provider resolves from that provider's row.
	if got, ok := ResolveContextWindow(" claude ", " claude-opus-5-5 ", 0, 0); !ok || got != 1000000 {
		t.Errorf("claude/claude-opus-5-5 = (%d, %v), want (1000000, true)", got, ok)
	}
	// Anthropic documents 200K for Sonnet 4.5 (models.dev says 1M; the
	// override corrects it), so compaction triggers before the API's limit.
	for _, id := range []string{"claude-sonnet-4-5", "claude-sonnet-4-5-20250929"} {
		if got, ok := ResolveContextWindow("claude", id, 0, 0); !ok || got != 200000 {
			t.Errorf("claude/%s = (%d, %v), want (200000, true)", id, got, ok)
		}
	}
}
