package providers

import (
	"os"
	"strings"
	"testing"
)

func TestAutoCompactConfigDefaultsOnAndRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		file string
		want bool
	}{
		{"key absent", `{"provider":"openai","model":"gpt-5"}`, true},
		{"explicit true", `{"provider":"openai","model":"gpt-5","auto_compact":true}`, true},
		{"explicit false", `{"provider":"openai","model":"gpt-5","auto_compact":false}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(ConfigFilePath(dir), []byte(tc.file), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadStoredConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.AutoCompactEnabled(); got != tc.want {
				t.Fatalf("AutoCompactEnabled() = %t, want %t", got, tc.want)
			}
			// Saving another preference (as /themes does) keeps the setting.
			cfg.Theme = "nord"
			if err := SaveStoredConfig(dir, cfg); err != nil {
				t.Fatal(err)
			}
			reloaded, err := LoadStoredConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := reloaded.AutoCompactEnabled(); got != tc.want {
				t.Fatalf("after save AutoCompactEnabled() = %t, want %t", got, tc.want)
			}
			data, err := os.ReadFile(ConfigFilePath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "key absent" && strings.Contains(string(data), "auto_compact") {
				t.Fatalf("saving added the key to a file that never had it: %s", data)
			}
		})
	}
}

func TestAutoCompactEnabledOnZeroConfig(t *testing.T) {
	if !(StoredProviderConfig{}).AutoCompactEnabled() {
		t.Fatal("zero config must leave automatic compaction on")
	}
}
