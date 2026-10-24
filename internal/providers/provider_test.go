package providers

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"likha/internal/model"
)

func TestResolveProviderSelectsEndpointsAndKeys(t *testing.T) {
	t.Setenv("LIKHA_ENDPOINT", "")
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
	t.Setenv("LIKHA_ENDPOINT", "")
	t.Setenv("LIKHA_API_KEY", "")
	t.Setenv("LIKHA_CHATGPT_API_KEY", "")
	stateDir := t.TempDir()
	// Without a stored login, resolution names the remedy.
	if _, err := ResolveProvider("chatgpt", "", "", false, stateDir); !errors.Is(err, ErrOAuthLoginRequired) || !strings.Contains(err.Error(), "/providers") || strings.Contains(err.Error(), "--device-login") {
		t.Fatalf("missing login error = %v", err)
	}
	want := oauthFixture(t, stateDir, "oaiapp_resolution")
	if err := StoreOAuth(stateDir, "chatgpt", want); err != nil {
		t.Fatal(err)
	}
	res, err := ResolveProvider("chatgpt", "", "", false, stateDir)
	if err != nil {
		t.Fatalf("stored login: %v", err)
	}
	if !res.OAuth || !res.Verified || res.Endpoint != model.ChatGPTResource || res.Display != "ChatGPT (Plus/Pro)" || res.Key != "" {
		t.Fatalf("stored login resolution = %+v", res)
	}
	if !reflect.DeepEqual(res.Creds, want) {
		t.Fatalf("credentials did not round-trip: %+v want %+v", res.Creds, want)
	}
	// Even a proxy must not receive the ChatGPT plan credential.
	if _, err := ResolveProvider("chatgpt", "https://proxy.example.net/v1", "", false, stateDir); err == nil || !strings.Contains(err.Error(), "only https://api.openai.com/v1") {
		t.Fatalf("endpoint override error = %v", err)
	}
}

func TestResolveProviderOAuthRejectsKeysAndCustomEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, key, env, value, remedy string
	}{
		{name: "flag key", key: "fixture-key", remedy: "not an API key"},
		{name: "general env key", env: "LIKHA_API_KEY", value: "fixture-key", remedy: "not an API key"},
		{name: "provider env key", env: "LIKHA_CHATGPT_API_KEY", value: "fixture-key", remedy: "not an API key"},
		{name: "private endpoint", endpoint: "https://chatgpt.com/backend-api/codex", remedy: "only https://api.openai.com/v1"},
		{name: "http endpoint", endpoint: "http://api.openai.com/v1", remedy: "only https://api.openai.com/v1"},
		{name: "env endpoint", env: "LIKHA_ENDPOINT", value: "https://fixture.invalid/v1", remedy: "LIKHA_ENDPOINT"},
		{name: "env cannot hide behind flag", endpoint: model.ChatGPTResource, env: "LIKHA_ENDPOINT", value: "https://fixture.invalid/v1", remedy: "LIKHA_ENDPOINT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LIKHA_ENDPOINT", "")
			t.Setenv("LIKHA_API_KEY", "")
			t.Setenv("LIKHA_CHATGPT_API_KEY", "")
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			if _, err := ResolveProvider("chatgpt", tc.endpoint, tc.key, false, t.TempDir()); err == nil || !strings.Contains(err.Error(), tc.remedy) {
				t.Fatalf("unsafe OAuth configuration error = %v", err)
			}
		})
	}
}

func TestResolveProviderCustomEndpointUnchanged(t *testing.T) {
	t.Setenv("LIKHA_ENDPOINT", "")
	res, err := ResolveProvider("", "https://custom.example.net/v1", "sk-x", true, t.TempDir())
	if err != nil || res.Endpoint != "https://custom.example.net/v1" || res.Verified || res.Display != "Custom endpoint" || res.Key != "" || res.OAuth {
		t.Fatalf("custom endpoint = %+v err=%v", res, err)
	}
}

func TestResolveProviderRequiresAndStoresHostedKeys(t *testing.T) {
	t.Setenv("LIKHA_API_KEY", "")
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

// Keys pasted from a clipboard or a .env file often carry a trailing newline
// or spaces; sent verbatim they fail as 401 or an invalid header, and a
// blank variable must not shadow a later source in the documented order.
func TestResolveProviderTrimsKeysAndSkipsBlankSources(t *testing.T) {
	cases := []struct {
		name, flag, providerEnv, stored, want string
	}{
		{name: "flag newline", flag: "sk-flag\n", want: "sk-flag"},
		{name: "blank flag falls through to provider env", flag: " \t", providerEnv: "sk-env", want: "sk-env"},
		{name: "provider env spaces", providerEnv: "  sk-env \r\n", want: "sk-env"},
		{name: "blank provider env falls through to store", providerEnv: "\n", stored: "sk-stored", want: "sk-stored"},
		{name: "stored newline", stored: "sk-stored\n", want: "sk-stored"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LIKHA_OPENROUTER_API_KEY", tc.providerEnv)
			stateDir := t.TempDir()
			if tc.stored != "" {
				if err := WriteCredentials(stateDir, map[string]StoredCredential{"openrouter": {Type: "api", Key: tc.stored}}); err != nil {
					t.Fatal(err)
				}
			}
			res, err := ResolveProvider("openrouter", "", tc.flag, false, stateDir)
			if err != nil {
				t.Fatalf("ResolveProvider: %v", err)
			}
			if res.Key != tc.want {
				t.Fatalf("key = %q, want %q", res.Key, tc.want)
			}
		})
	}
}

func TestResolveProviderBlankKeysAreMissing(t *testing.T) {
	t.Setenv("LIKHA_OPENROUTER_API_KEY", "  ")
	_, err := ResolveProvider("openrouter", "", "\n", true, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("blank keys error = %v, want requires an API key", err)
	}
}

// Only a key passed on the command line is stored; a key resolved from the
// environment or the store stays where it came from.
func TestResolveProviderPersistsOnlyTheFlagKey(t *testing.T) {
	t.Setenv("LIKHA_OPENROUTER_API_KEY", "sk-env")
	stateDir := t.TempDir()
	if _, err := ResolveProvider("openrouter", "", " ", true, stateDir); err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	stored, err := StoredKey(stateDir, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if stored != "" {
		t.Fatalf("environment key was stored as %q", stored)
	}
	if _, err := ResolveProvider("openrouter", "", " sk-flag\n", true, stateDir); err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	if stored, err = StoredKey(stateDir, "openrouter"); err != nil || stored != "sk-flag" {
		t.Fatalf("stored flag key = %q, %v; want sk-flag", stored, err)
	}
}

// v1-spec §3: an endpoint outside the accepted list runs with a visible
// unverified warning, including a listed provider pointed elsewhere.
func TestResolveProviderEndpointOverrideIsUnverified(t *testing.T) {
	t.Setenv("LIKHA_ENDPOINT", "")
	cases := []struct {
		name, endpoint string
		verified       bool
	}{
		{name: "provider base", endpoint: "", verified: true},
		{name: "same base spelled out", endpoint: "https://openrouter.ai/api/v1", verified: true},
		{name: "same base with trailing slash", endpoint: "https://openrouter.ai/api/v1/", verified: true},
		{name: "other host", endpoint: "https://gateway.example.net/v1", verified: false},
		{name: "other path on provider host", endpoint: "https://openrouter.ai/api/v2", verified: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ResolveProvider("openrouter", tc.endpoint, "sk-test", false, t.TempDir())
			if err != nil {
				t.Fatalf("ResolveProvider: %v", err)
			}
			if res.Verified != tc.verified {
				t.Fatalf("Verified = %v, want %v (endpoint %q)", res.Verified, tc.verified, res.Endpoint)
			}
		})
	}
}

// A provider's documented endpoint in another region is the same accepted
// provider, so it carries no unverified marker; anything else still does.
func TestEndpointVerified(t *testing.T) {
	bedrock, ok := model.LookupProvider("bedrock")
	if !ok {
		t.Fatal("bedrock missing from the predefined list")
	}
	openrouter, ok := model.LookupProvider("openrouter")
	if !ok {
		t.Fatal("openrouter missing from the predefined list")
	}
	cases := []struct {
		name     string
		provider model.Provider
		endpoint string
		want     bool
	}{
		{"base URL", bedrock, bedrock.BaseURL, true},
		{"base URL with trailing slash", bedrock, bedrock.BaseURL + "/", true},
		{"other Bedrock region", bedrock, "https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1", true},
		{"GovCloud region", bedrock, "https://bedrock-runtime.us-gov-west-1.amazonaws.com/openai/v1/", true},
		{"uppercase host", bedrock, "https://BEDROCK-RUNTIME.ap-southeast-2.amazonaws.com/openai/v1", true},
		{"regional host, other path", bedrock, "https://bedrock-runtime.eu-west-1.amazonaws.com/v2", false},
		{"plain http", bedrock, "http://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1", false},
		{"look-alike host", bedrock, "https://bedrock-runtime.eu-west-1.amazonaws.com.evil.example/openai/v1", false},
		{"not a region", bedrock, "https://bedrock-runtime.evil.amazonaws.com/openai/v1", false},
		{"user info", bedrock, "https://user@bedrock-runtime.eu-west-1.amazonaws.com/openai/v1", false},
		{"explicit port", bedrock, "https://bedrock-runtime.eu-west-1.amazonaws.com:8443/openai/v1", false},
		{"query", openrouter, "https://openrouter.ai/api/v1?x=1", false},
		{"no regional pattern elsewhere", openrouter, "https://eu.openrouter.ai/api/v1", false},
		{"unparseable", openrouter, "https://openrouter.ai/api/v1%zz", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EndpointVerified(tc.provider, tc.endpoint); got != tc.want {
				t.Fatalf("EndpointVerified(%s, %q) = %t, want %t", tc.provider.Name, tc.endpoint, got, tc.want)
			}
		})
	}
	res, err := ResolveProvider("bedrock", "https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1", "sk-test", false, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveProvider: %v", err)
	}
	if !res.Verified {
		t.Fatal("a Bedrock regional endpoint resolved as unverified")
	}
}

// A stored key reaches every caller trimmed, so a key saved with a pasted
// newline works in /models and /providers switches too, and a blank one
// reads as no key at all.
func TestStoredKeyTrimsWhitespace(t *testing.T) {
	cases := []struct {
		name, stored, want string
	}{
		{"trailing newline", "sk-x\n", "sk-x"},
		{"surrounding spaces", "  sk-x \t", "sk-x"},
		{"blank", " \r\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			if err := WriteCredentials(stateDir, map[string]StoredCredential{"openrouter": {Type: "api", Key: tc.stored}}); err != nil {
				t.Fatal(err)
			}
			got, err := StoredKey(stateDir, "openrouter")
			if err != nil {
				t.Fatalf("StoredKey: %v", err)
			}
			if got != tc.want {
				t.Fatalf("StoredKey = %q, want %q", got, tc.want)
			}
		})
	}
}
