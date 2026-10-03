package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoredConfigContextWindowsRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	want := map[string]map[string]int64{
		"openai": {
			"gpt-4.1": 1000000,
			"gpt-4o":  128000,
		},
		"openrouter": {
			"anthropic/claude-sonnet-4-5": 200000,
		},
	}
	input := StoredProviderConfig{
		Provider:       "openai",
		Model:          "gpt-4.1",
		ContextWindows: want,
	}

	if err := SaveStoredConfig(stateDir, input); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(stateDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var nested map[string]map[string]int64
	if err := json.Unmarshal(raw["context_windows"], &nested); err != nil {
		t.Fatalf("decode nested context_windows: %v", err)
	}
	if !reflect.DeepEqual(nested, want) {
		t.Fatalf("saved context_windows = %#v, want %#v", nested, want)
	}

	got, err := LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ContextWindows, want) {
		t.Fatalf("round-trip context_windows = %#v, want %#v", got.ContextWindows, want)
	}
}

func TestStoredConfigWithoutContextWindowsKeepsExistingDefaults(t *testing.T) {
	stateDir := t.TempDir()
	legacyConfig := `{"provider":"openai","model":"gpt-4o","status_line":{}}`
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte(legacyConfig), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextWindows != nil {
		t.Fatalf("absent context_windows = %#v, want nil", cfg.ContextWindows)
	}
	if cfg.Provider != "openai" || cfg.Model != "gpt-4o" {
		t.Fatalf("existing provider/model defaults changed: %+v", cfg)
	}
	if cfg.StatusLine == nil || !FlagEnabled(cfg.StatusLine.Folder) || !FlagEnabled(cfg.StatusLine.Branch) {
		t.Fatalf("default-on status line fields changed: %+v", cfg.StatusLine)
	}
}
