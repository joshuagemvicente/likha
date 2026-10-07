package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoredAttentionConfigLoads(t *testing.T) {
	cases := []struct {
		name        string
		attention   string // the "attention" value in config.json; "" omits the key
		wantBell    bool
		wantDesktop bool
		wantMinutes int
		wantValid   bool
	}{
		{"absent section", "", true, true, 0, true},
		{"empty section", `{}`, true, true, 0, true},
		{"bell off", `{"bell":false}`, false, true, 0, true},
		{"desktop off", `{"desktop":false}`, true, false, 0, true},
		{"both explicitly on", `{"bell":true,"desktop":true}`, true, true, 0, true},
		{"zero minutes is off", `{"ask_skip_after_minutes":0}`, true, true, 0, true},
		{"one minute", `{"ask_skip_after_minutes":1}`, true, true, 1, true},
		{"upper bound", `{"ask_skip_after_minutes":120}`, true, true, 120, true},
		{"above range", `{"ask_skip_after_minutes":121}`, true, true, 0, false},
		{"negative", `{"ask_skip_after_minutes":-1}`, true, true, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			body := `{"provider":"openai","model":"gpt-5.5"`
			if tc.attention != "" {
				body += `,"attention":` + tc.attention
			}
			body += "}"
			if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadStoredConfig(stateDir)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var attention StoredAttentionConfig
			if cfg.Attention != nil {
				attention = *cfg.Attention
			}
			if got := FlagEnabled(attention.Bell); got != tc.wantBell {
				t.Errorf("bell = %v, want %v", got, tc.wantBell)
			}
			if got := FlagEnabled(attention.Desktop); got != tc.wantDesktop {
				t.Errorf("desktop = %v, want %v", got, tc.wantDesktop)
			}
			minutes, ok := attention.AskSkipLimit()
			if minutes != tc.wantMinutes || ok != tc.wantValid {
				t.Errorf("AskSkipLimit() = (%d, %v), want (%d, %v)", minutes, ok, tc.wantMinutes, tc.wantValid)
			}
		})
	}
}

// Saving another setting (a theme switch) must keep the attention section
// exactly as stored.
func TestStoredAttentionSurvivesThemeSave(t *testing.T) {
	stateDir := t.TempDir()
	body := `{"provider":"openai","model":"gpt-5.5","attention":{"bell":false,"ask_skip_after_minutes":15}}`
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Theme = "tokyo"
	if err := SaveStoredConfig(stateDir, cfg); err != nil {
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
	if got, want := string(raw["attention"]), `{"bell":false,"ask_skip_after_minutes":15}`; got != want {
		t.Fatalf("attention after a theme save = %s, want %s", got, want)
	}
}

// A config.json written before this feature has no attention key and gains
// none on save.
func TestStoredConfigWithoutAttentionStaysWithout(t *testing.T) {
	stateDir := t.TempDir()
	if err := SaveStoredConfig(stateDir, StoredProviderConfig{Provider: "openai", Model: "gpt-5.5"}); err != nil {
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
	if _, ok := raw["attention"]; ok {
		t.Fatalf("a config without attention gained the key: %s", data)
	}
}
