package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lisa/internal/model"
)

func TestResolveProviderSelectsEndpointsAndKeys(t *testing.T) {
	t.Setenv("LISA_ENDPOINT", "")
	if _, err := ResolveProvider("", "", "", false, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no provider configured") {
		t.Fatalf("no-provider error = %v", err)
	}
	res, err := ResolveProvider("", "https://gateway.example.net/v1", "", false, t.TempDir())
	if err != nil || res.Endpoint != "https://gateway.example.net/v1" || res.Verified || res.Display != "Custom endpoint" {
		t.Fatalf("custom endpoint = %+v err=%v", res, err)
	}
	// Every listed API-key provider resolves to its own base URL, display
	// name, and the flagged key; the OAuth row is covered separately because
	// keys do not apply to it.
	for _, p := range model.Providers {
		if p.Auth == model.AuthOAuth {
			continue
		}
		res, err := ResolveProvider(p.Name, "", "sk-test", false, t.TempDir())
		if err != nil || res.Endpoint != p.BaseURL || !res.Verified || res.Display != p.DisplayName || res.Key != "sk-test" {
			t.Fatalf("provider %s: %+v err=%v", p.Name, res, err)
		}
	}
}

func TestResolveProviderStoredOAuth(t *testing.T) {
	stateDir := t.TempDir()
	// Without a stored login, resolution names the remedy.
	if _, err := ResolveProvider("chatgpt", "", "", false, stateDir); err == nil || !strings.Contains(err.Error(), "--device-login") {
		t.Fatalf("missing login error = %v", err)
	}
	want := model.OAuthCredentials{Refresh: "refresh-token", Access: "access-token", Expires: 12345, AccountID: "acc_123"}
	if err := StoreOAuth(stateDir, "chatgpt", want); err != nil {
		t.Fatal(err)
	}
	res, err := ResolveProvider("chatgpt", "", "", false, stateDir)
	if err != nil {
		t.Fatalf("stored login: %v", err)
	}
	if !res.OAuth || !res.Verified || res.Endpoint != "https://chatgpt.com/backend-api/codex" || res.Display != "ChatGPT (Plus/Pro)" || res.Key != "" {
		t.Fatalf("stored login resolution = %+v", res)
	}
	if res.Creds != want {
		t.Fatalf("credentials did not round-trip: %+v want %+v", res.Creds, want)
	}
	// A --endpoint override still wins for OAuth providers; NewOAuth
	// validates it later.
	res, err = ResolveProvider("chatgpt", "https://proxy.example.net/v1", "", false, stateDir)
	if err != nil || !res.OAuth || res.Endpoint != "https://proxy.example.net/v1" {
		t.Fatalf("endpoint override = %+v err=%v", res, err)
	}
}

func TestResolveProviderCustomEndpointUnchanged(t *testing.T) {
	t.Setenv("LISA_ENDPOINT", "")
	res, err := ResolveProvider("", "https://custom.example.net/v1", "sk-x", true, t.TempDir())
	if err != nil || res.Endpoint != "https://custom.example.net/v1" || res.Verified || res.Display != "Custom endpoint" || res.Key != "" || res.OAuth {
		t.Fatalf("custom endpoint = %+v err=%v", res, err)
	}
}

func TestResolveProviderRequiresAndStoresHostedKeys(t *testing.T) {
	t.Setenv("LISA_API_KEY", "")
	stateDir := t.TempDir()
	if _, err := ResolveProvider("openrouter", "", "", false, stateDir); err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("missing key error = %v", err)
	}
	res, err := ResolveProvider("openrouter", "", "sk-env", false, stateDir)
	if err != nil || res.Endpoint != "https://openrouter.ai/api/v1" || res.Key != "sk-env" {
		t.Fatalf("env key: %+v err=%v", res, err)
	}
	stored, err := StoredKey(stateDir, "openrouter")
	if err != nil || stored != "" {
		t.Fatalf("non-persisted key was stored: %q %v", stored, err)
	}
	if res, err := ResolveProvider("openrouter", "", "sk-flag", true, stateDir); err != nil || res.Key != "sk-flag" {
		t.Fatalf("flag key: %+v %v", res, err)
	}
	stored, err = StoredKey(stateDir, "openrouter")
	if err != nil || stored != "sk-flag" {
		t.Fatalf("key not stored: %q %v", stored, err)
	}
	info, err := os.Stat(filepath.Join(stateDir, "providers.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("stored key permissions: %v %v", info, err)
	}
	if res, err = ResolveProvider("openrouter", "", "", false, stateDir); err != nil || res.Key != "sk-flag" {
		t.Fatalf("stored key not used: %+v %v", res, err)
	}
}

func TestStoredConfigRoundTripAndCorruption(t *testing.T) {
	stateDir := t.TempDir()
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "" || cfg.Model != "" {
		t.Fatalf("empty state: %+v %v", cfg, err)
	}
	if err := SaveStoredConfig(stateDir, StoredProviderConfig{Provider: "openrouter", Model: "openai/gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(stateDir, "config.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v %v", info, err)
	}
	cfg, err = LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openrouter" || cfg.Model != "openai/gpt-4o-mini" {
		t.Fatalf("round trip: %+v %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStoredConfig(stateDir); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("corrupt config error = %v", err)
	}
}

// TestProvidersSwitchModelID verifies the landing-model resolution: the
// provider's documented default wins over the reported list, the first
// reported id is used when there is no default, and the result is empty when
// neither exists.
func TestProvidersSwitchModelID(t *testing.T) {
	withDefault := model.Provider{Name: "with-default", DefaultModel: "default-model"}
	if got := SwitchModelID(withDefault, []string{"reported-1", "reported-2"}); got != "default-model" {
		t.Fatalf("SwitchModelID with default = %q, want default-model", got)
	}
	noDefault := model.Provider{Name: "no-default"}
	if got := SwitchModelID(noDefault, []string{"reported-1", "reported-2"}); got != "reported-1" {
		t.Fatalf("SwitchModelID without default = %q, want the first reported id", got)
	}
	if got := SwitchModelID(noDefault, nil); got != "" {
		t.Fatalf("SwitchModelID with neither = %q, want empty", got)
	}
}
