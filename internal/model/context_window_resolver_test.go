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
			name:    "documented catalog fallback",
			modelID: "gpt-4o",
			want:    128000,
			wantOK:  true,
		},
		{
			name:    "unknown model stays unknown",
			modelID: "gpt-5.5",
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

func TestResolveContextWindowWithEmptyOrWhitespaceProviderID(t *testing.T) {
	for _, providerID := range []string{"", " \t "} {
		if got, ok := ResolveContextWindow(providerID, "gpt-4o", 0, 0); !ok || got != 128000 {
			t.Errorf("catalog resolution with provider ID %q = (%d, %v), want (128000, true)", providerID, got, ok)
		}
	}
}
