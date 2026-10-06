package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoredEffortRoundTripAndDefaultRemoves(t *testing.T) {
	stateDir := t.TempDir()
	cfg := StoredProviderConfig{Provider: "openai", Model: "gpt-5.5", Theme: "tokyo"}
	cfg.SetStoredEffort("openai", "gpt-5.5", "high")
	cfg.SetStoredEffort("openrouter", "openai/gpt-6.1-sol", "max")
	if err := SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.StoredEffort("openai", "gpt-5.5"); got != "high" {
		t.Fatalf("openai/gpt-5.5 = %q, want high", got)
	}
	if got := loaded.StoredEffort("openai", "gpt-4o"); got != "" {
		t.Fatalf("unstored pair = %q, want Default", got)
	}
	if loaded.Theme != "tokyo" {
		t.Fatalf("saving efforts lost the theme: %+v", loaded)
	}

	loaded.SetStoredEffort("openrouter", "openai/gpt-6.1-sol", "")
	loaded.SetStoredEffort("openai", "gpt-5.5", "")
	if err := SaveStoredConfig(stateDir, loaded); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "efforts") {
		t.Fatalf("Default left an efforts entry: %s", data)
	}
}

func TestStoredEffortOnZeroConfig(t *testing.T) {
	var cfg StoredProviderConfig
	if cfg.StoredEffort("openai", "gpt-5.5") != "" {
		t.Fatal("zero config reported a stored effort")
	}
	cfg.SetStoredEffort("openai", "gpt-5.5", "") // removing from nil maps must not panic
	if cfg.Efforts != nil {
		t.Fatalf("Default created an efforts map: %v", cfg.Efforts)
	}
}
