package app

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lisa/internal/model"
)

func TestResolveRootFollowsSelectedRepository(t *testing.T) {
	repo := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-repository")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	root, err := resolveRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Fatalf("selected repository = %q, want %q", root, want)
	}
}

func TestRunRejectsInvalidRepository(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"regular file", []string{file}},
		{"missing directory", []string{filepath.Join(t.TempDir(), "absent")}},
		{"extra path", []string{t.TempDir(), t.TempDir()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr); code == 0 {
				t.Fatalf("accepted invalid repository: %v", tc.args)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunReportsMissingModel(t *testing.T) {
	t.Setenv("LISA_MODEL", "")
	t.Setenv("LISA_ENDPOINT", "")
	t.Setenv("LISA_STATE_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	// A closed port keeps the test independent of any real model server; with
	// no provider named, the custom endpoint's discovery fails and names the
	// missing configuration.
	if code := Run([]string{"--endpoint", "http://127.0.0.1:9/v1", t.TempDir()}, &stdout, &stderr); code == 0 {
		t.Fatal("accepted missing model configuration")
	}
	if !strings.Contains(stderr.String(), "model is required") || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunWithoutConfigurationExplainsSetup(t *testing.T) {
	for _, key := range []string{"LISA_MODEL", "LISA_PROVIDER", "LISA_API_KEY", "LISA_ENDPOINT"} {
		t.Setenv(key, "")
	}
	t.Setenv("LISA_STATE_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := Run([]string{t.TempDir()}, &stdout, &stderr); code == 0 {
		t.Fatal("started without provider configuration in a non-interactive terminal")
	}
	if !strings.Contains(stderr.String(), "no provider configured") || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestResolveProviderSelectsEndpointsAndKeys(t *testing.T) {
	t.Setenv("LISA_ENDPOINT", "")
	if _, err := resolveProvider("", "", "", false, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no provider configured") {
		t.Fatalf("no-provider error = %v", err)
	}
	res, err := resolveProvider("", "https://gateway.example.net/v1", "", false, t.TempDir())
	if err != nil || res.endpoint != "https://gateway.example.net/v1" || res.verified || res.display != "Custom endpoint" {
		t.Fatalf("custom endpoint = %+v err=%v", res, err)
	}
	// Every listed API-key provider resolves to its own base URL, display
	// name, and the flagged key; the OAuth row is covered separately because
	// keys do not apply to it.
	for _, p := range model.Providers {
		if p.Auth == model.AuthOAuth {
			continue
		}
		res, err := resolveProvider(p.Name, "", "sk-test", false, t.TempDir())
		if err != nil || res.endpoint != p.BaseURL || !res.verified || res.display != p.DisplayName || res.key != "sk-test" {
			t.Fatalf("provider %s: %+v err=%v", p.Name, res, err)
		}
	}
}

func TestResolveProviderStoredOAuth(t *testing.T) {
	stateDir := t.TempDir()
	// Without a stored login, resolution names the remedy.
	if _, err := resolveProvider("chatgpt", "", "", false, stateDir); err == nil || !strings.Contains(err.Error(), "--device-login") {
		t.Fatalf("missing login error = %v", err)
	}
	want := model.OAuthCredentials{Refresh: "refresh-token", Access: "access-token", Expires: 12345, AccountID: "acc_123"}
	if err := storeOAuth(stateDir, "chatgpt", want); err != nil {
		t.Fatal(err)
	}
	res, err := resolveProvider("chatgpt", "", "", false, stateDir)
	if err != nil {
		t.Fatalf("stored login: %v", err)
	}
	if !res.oauth || !res.verified || res.endpoint != "https://chatgpt.com/backend-api/codex" || res.display != "ChatGPT (Plus/Pro)" || res.key != "" {
		t.Fatalf("stored login resolution = %+v", res)
	}
	if res.creds != want {
		t.Fatalf("credentials did not round-trip: %+v want %+v", res.creds, want)
	}
	// A --endpoint override still wins for OAuth providers; NewOAuth
	// validates it later.
	res, err = resolveProvider("chatgpt", "https://proxy.example.net/v1", "", false, stateDir)
	if err != nil || !res.oauth || res.endpoint != "https://proxy.example.net/v1" {
		t.Fatalf("endpoint override = %+v err=%v", res, err)
	}
}

func TestResolveProviderCustomEndpointUnchanged(t *testing.T) {
	t.Setenv("LISA_ENDPOINT", "")
	res, err := resolveProvider("", "https://custom.example.net/v1", "sk-x", true, t.TempDir())
	if err != nil || res.endpoint != "https://custom.example.net/v1" || res.verified || res.display != "Custom endpoint" || res.key != "" || res.oauth {
		t.Fatalf("custom endpoint = %+v err=%v", res, err)
	}
}

func TestResolveProviderRequiresAndStoresHostedKeys(t *testing.T) {
	t.Setenv("LISA_API_KEY", "")
	stateDir := t.TempDir()
	if _, err := resolveProvider("openrouter", "", "", false, stateDir); err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("missing key error = %v", err)
	}
	res, err := resolveProvider("openrouter", "", "sk-env", false, stateDir)
	if err != nil || res.endpoint != "https://openrouter.ai/api/v1" || res.key != "sk-env" {
		t.Fatalf("env key: %+v err=%v", res, err)
	}
	stored, err := storedKey(stateDir, "openrouter")
	if err != nil || stored != "" {
		t.Fatalf("non-persisted key was stored: %q %v", stored, err)
	}
	if res, err := resolveProvider("openrouter", "", "sk-flag", true, stateDir); err != nil || res.key != "sk-flag" {
		t.Fatalf("flag key: %+v %v", res, err)
	}
	stored, err = storedKey(stateDir, "openrouter")
	if err != nil || stored != "sk-flag" {
		t.Fatalf("key not stored: %q %v", stored, err)
	}
	info, err := os.Stat(filepath.Join(stateDir, "providers.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("stored key permissions: %v %v", info, err)
	}
	if res, err = resolveProvider("openrouter", "", "", false, stateDir); err != nil || res.key != "sk-flag" {
		t.Fatalf("stored key not used: %+v %v", res, err)
	}
}

func TestResolveModelExplicitBeatsDiscovery(t *testing.T) {
	custom := model.Provider{DisplayName: "Custom endpoint"}
	got, err := resolveModel("  qwen3:4b ", custom, "http://127.0.0.1:9/v1", "")
	if err != nil || got != "qwen3:4b" {
		t.Fatalf("explicit model = %q err=%v", got, err)
	}
}

func TestResolveModelUsesHostedDefault(t *testing.T) {
	p, ok := model.LookupProvider("openrouter")
	if !ok {
		t.Fatal("openrouter missing from table")
	}
	got, err := resolveModel("", p, p.BaseURL, "sk-key")
	if err != nil || got != p.DefaultModel {
		t.Fatalf("hosted default = %q err=%v", got, err)
	}
	hostless := model.Provider{Name: "x", DisplayName: "X", Hosted: true}
	if _, err := resolveModel("", hostless, hostless.BaseURL, "k"); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("hosted without default: err=%v", err)
	}
}

func TestResolveModelDiscoversFirstEndpointModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"smoke-model"},{"id":"other"}]}`)
	}))
	defer server.Close()
	custom := model.Provider{DisplayName: "Custom endpoint"}
	got, err := resolveModel("", custom, server.URL+"/v1", "")
	if err != nil || got != "smoke-model" {
		t.Fatalf("discovered model = %q err=%v", got, err)
	}
}

func TestResolveModelErrorsWithoutEndpointModels(t *testing.T) {
	custom := model.Provider{DisplayName: "Custom endpoint"}
	if _, err := resolveModel("", custom, "http://127.0.0.1:1/v1", ""); err == nil || !strings.Contains(err.Error(), "model list failed") {
		t.Fatalf("unreachable discovery error = %v", err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[]}`)
	}))
	defer empty.Close()
	if _, err := resolveModel("", custom, empty.URL+"/v1", ""); err == nil || !strings.Contains(err.Error(), "reports no models") {
		t.Fatalf("empty list error = %v", err)
	}
}

func TestStoredConfigRoundTripAndCorruption(t *testing.T) {
	stateDir := t.TempDir()
	cfg, err := loadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "" || cfg.Model != "" {
		t.Fatalf("empty state: %+v %v", cfg, err)
	}
	if err := saveStoredConfig(stateDir, storedProviderConfig{Provider: "openrouter", Model: "openai/gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(stateDir, "config.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v %v", info, err)
	}
	cfg, err = loadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openrouter" || cfg.Model != "openai/gpt-4o-mini" {
		t.Fatalf("round trip: %+v %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadStoredConfig(stateDir); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("corrupt config error = %v", err)
	}
}

func TestRunHelpDoesNotRequireRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help failed: exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: lisa") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	for _, expected := range []string{"--provider", "--api-key", "--model", "--endpoint", "--sessions", "--resume", "--version", "LISA_STATE_DIR", "Providers", "BYOK"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("help is missing %q: %q", expected, stdout.String())
		}
	}
}
