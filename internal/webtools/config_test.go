package webtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStateFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigDefaultsOnWithoutWebSettings(t *testing.T) {
	for name, content := range map[string]string{
		"no file":        "",
		"no web key":     `{"provider":"openai","model":"gpt"}`,
		"empty web":      `{"web":{}}`,
		"empty sections": `{"web":{"search":{},"fetch":{}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if content != "" {
				writeStateFile(t, dir, "config.json", content)
			}
			config, err := LoadConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if config != DefaultConfig() || config.Search.Backend != duckduckgoBackend || !config.Search.Enabled || !config.Fetch.Enabled {
				t.Fatalf("config = %+v, want search (duckduckgo) and fetch on", config)
			}
		})
	}
}

func TestLoadConfigExplicitValues(t *testing.T) {
	dir := t.TempDir()
	writeStateFile(t, dir, "config.json", `{"provider":"x","web":{"search":{"enabled":false,"backend":"brave"},"fetch":{"enabled":false}}}`)
	config, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Search: SearchConfig{Enabled: false, Backend: "brave"}, Fetch: FetchConfig{Enabled: false}}
	if config != want {
		t.Fatalf("config = %+v, want %+v", config, want)
	}
}

func TestLoadConfigErrorsNameTheFile(t *testing.T) {
	dir := t.TempDir()
	writeStateFile(t, dir, "config.json", `{"web":{"search":{"backend":"bing"}}}`)
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "config.json") || !strings.Contains(err.Error(), "bing") {
		t.Fatalf("err = %v", err)
	}
	writeStateFile(t, dir, "config.json", `{"web":{"fetch":{"enabled":"yes"}}}`)
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "web.fetch.enabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadConfigReadsLegacyToolsJSONUntilConfigHasWeb(t *testing.T) {
	dir := t.TempDir()
	writeStateFile(t, dir, "tools.json", `{"web":{"fetch":{"enabled":false}}}`)
	config, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if config.Fetch.Enabled || !config.Search.Enabled {
		t.Fatalf("legacy config = %+v, want fetch off, search on", config)
	}
	writeStateFile(t, dir, "config.json", `{"web":{}}`)
	if config, err = LoadConfig(dir); err != nil || !config.Fetch.Enabled {
		t.Fatalf("config.json web should win over tools.json: %+v, %v", config, err)
	}
}

func TestLegacyWebSection(t *testing.T) {
	dir := t.TempDir()
	if raw, err := LegacyWebSection(dir); raw != nil || err != nil {
		t.Fatalf("absent tools.json = %s, %v", raw, err)
	}
	writeStateFile(t, dir, "tools.json", `{"web":{"search":{"enabled":false}}}`)
	raw, err := LegacyWebSection(dir)
	if err != nil || string(raw) != `{"search":{"enabled":false}}` {
		t.Fatalf("raw = %s, err = %v", raw, err)
	}
	writeStateFile(t, dir, "tools.json", `{"web":`)
	if _, err := LegacyWebSection(dir); err == nil {
		t.Fatal("malformed tools.json should error")
	}
}
