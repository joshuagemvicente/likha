package tui

import (
	"encoding/json"
	"lisa/internal/providers"
	"os"
	"reflect"
	"testing"
)

func TestStoredComposerConfigRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	want := providers.StoredProviderConfig{
		Provider: "openrouter",
		Model:    "openai/gpt-4o-mini",
		Theme:    "habamax",
		Composer: &providers.StoredComposerConfig{Style: "bordered"},
		StatusLine: &providers.StoredStatusLineConfig{
			Folder:  statusFlag(true),
			Branch:  statusFlag(true),
			Version: true,
			Changes: true,
			Staged:  true,
			MCP:     true,
			Minutes: true,
			Tokens:  true,
			Update:  true,
		},
	}
	if err := providers.SaveStoredConfig(stateDir, want); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Composer struct {
			Style string `json:"style"`
		} `json:"composer"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Composer.Style != "bordered" {
		t.Fatalf("persisted composer style = %q, want bordered", stored.Composer.Style)
	}
	got, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != want.Provider || got.Model != want.Model || got.Theme != want.Theme || got.Composer == nil || got.Composer.Style != want.Composer.Style {
		t.Fatalf("config roundtrip = %+v, want %+v", got, want)
	}
	if got.StatusLine == nil || !reflect.DeepEqual(got.StatusLine, want.StatusLine) {
		t.Fatalf("status line roundtrip = %+v, want %+v", got.StatusLine, want.StatusLine)
	}
	info, err := os.Stat(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions = %v, want 0600", info.Mode().Perm())
	}
}

func TestStoredComposerLegacyConfigRemainsOptional(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(providers.ConfigFilePath(stateDir), []byte(`{"provider":"openai","model":"gpt-4o-mini","theme":"default"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" || cfg.Model != "gpt-4o-mini" || cfg.Theme != "default" || cfg.Composer != nil {
		t.Fatalf("legacy config = %+v", cfg)
	}
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["composer"]; exists {
		t.Fatalf("legacy config gained composer field: %s", data)
	}
}

func TestStoredComposerUnknownStylePreservedForUIFallback(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(providers.ConfigFilePath(stateDir), []byte(`{"provider":"openai","model":"gpt-4o-mini","composer":{"style":"future-style"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Composer == nil || cfg.Composer.Style != "future-style" {
		t.Fatalf("unknown composer style was lost: %+v", cfg)
	}
}

func TestStoredStatusLineConfigRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	cfg := providers.StoredProviderConfig{
		Provider: "openrouter",
		Model:    "openai/gpt-4o-mini",
		StatusLine: &providers.StoredStatusLineConfig{
			Folder:  statusFlag(true),
			Version: true,
			Staged:  true,
		},
	}
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	statusLine := fields["status_line"]
	if statusLine == nil {
		t.Fatalf("status_line missing: %s", data)
	}
	var segments map[string]json.RawMessage
	if err := json.Unmarshal(statusLine, &segments); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"folder", "version", "staged"} {
		if string(segments[name]) != "true" {
			t.Fatalf("status_line.%s = %s, want true: %s", name, segments[name], statusLine)
		}
	}
	for _, name := range []string{"branch", "session", "changes", "mcp", "minutes", "tokens", "update"} {
		if _, exists := segments[name]; exists {
			t.Fatalf("disabled status_line.%s persisted: %s", name, statusLine)
		}
	}
	got, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusLine == nil || !reflect.DeepEqual(got.StatusLine, cfg.StatusLine) {
		t.Fatalf("status line roundtrip = %+v, want %+v", got.StatusLine, cfg.StatusLine)
	}
}

func TestStoredStatusLineLegacyConfigStaysNilSafe(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(providers.ConfigFilePath(stateDir), []byte(`{"provider":"openai","model":"gpt-4o-mini","status_line":{"folder":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusLine == nil || cfg.StatusLine.Folder == nil || !*cfg.StatusLine.Folder || cfg.StatusLine.Changes || cfg.StatusLine.Staged || cfg.StatusLine.MCP || cfg.StatusLine.Minutes || cfg.StatusLine.Tokens || cfg.StatusLine.Update {
		t.Fatalf("legacy status line = %+v, want only folder", cfg.StatusLine)
	}
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	var statusLine map[string]json.RawMessage
	if err := json.Unmarshal(data, &statusLine); err != nil {
		t.Fatal(err)
	}
	segments, exists := statusLine["status_line"]
	if !exists {
		t.Fatalf("status_line disappeared: %s", data)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(segments, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 {
		t.Fatalf("legacy status line gained fields on save: %s", segments)
	}
	if string(persisted["folder"]) != "true" {
		t.Fatalf("status line folder lost: %s", segments)
	}
}

func TestStoredStatusLineUnknownFutureKeysIgnored(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(providers.ConfigFilePath(stateDir), []byte(`{"provider":"openai","model":"gpt-4o-mini","status_line":{"folder":true,"mcp":true,"budget":true,"widget":"fancy"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusLine == nil || cfg.StatusLine.Folder == nil || !*cfg.StatusLine.Folder || !cfg.StatusLine.MCP || cfg.StatusLine.Changes || cfg.StatusLine.Update {
		t.Fatalf("status line = %+v, want folder and mcp only", cfg.StatusLine)
	}
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	var segments map[string]json.RawMessage
	if err := json.Unmarshal(fields["status_line"], &segments); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"budget", "widget"} {
		if _, exists := segments[name]; exists {
			t.Fatalf("unknown status line key %q leaked into save: %s", name, fields["status_line"])
		}
	}
}
