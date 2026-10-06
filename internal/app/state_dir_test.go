package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"likha/internal/providers"
	"likha/internal/webtools"
)

func TestDefaultStateDirUsesXDGOrDotConfig(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows keeps os.UserConfigDir")
	}
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if dir, err := defaultStateDir(); err != nil || dir != filepath.Join(xdg, "likha") {
		t.Fatalf("dir = %q, err = %v", dir, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/ignored")
	if dir, err := defaultStateDir(); err != nil || dir != filepath.Join(home, ".config", "likha") {
		t.Fatalf("dir = %q, err = %v", dir, err)
	}
}

func TestResolveStateDirHonorsOverride(t *testing.T) {
	override := t.TempDir()
	t.Setenv("LIKHA_STATE_DIR", override)
	if dir, err := resolveStateDir(&bytes.Buffer{}); err != nil || dir != override {
		t.Fatalf("dir = %q, err = %v", dir, err)
	}
}

func TestMigrateLegacyStateDirMovesWholeDirectory(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "Application Support", "likha")
	dir := filepath.Join(base, ".config", "likha")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.json", "sessions.sqlite", "sessions.sqlite-wal"} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer
	if got := migrateLegacyStateDir(dir, legacy, &stderr); got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy dir still exists: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("moved dir = %v, %v; want mode 0700", info, err)
	}
	for _, name := range []string{"config.json", "sessions.sqlite", "sessions.sqlite-wal"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != name {
			t.Fatalf("%s = %q, %v", name, data, err)
		}
	}
	if !strings.Contains(stderr.String(), "moved settings and sessions") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMigrateLegacyStateDirNeverMerges(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "legacy")
	dir := filepath.Join(base, "new")
	for _, d := range []string{legacy, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer
	if got := migrateLegacyStateDir(dir, legacy, &stderr); got != dir {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy dir should be left alone: %v", err)
	}
	if !strings.Contains(stderr.String(), "is ignored") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	stderr.Reset()
	if got := migrateLegacyStateDir(dir, filepath.Join(base, "missing"), &stderr); got != dir || stderr.Len() != 0 {
		t.Fatalf("no legacy: got %q, stderr %q", got, stderr.String())
	}
}

func TestMigrateToolsConfigFoldsIntoConfigJSON(t *testing.T) {
	dir := t.TempDir()
	if err := providers.SaveStoredConfig(dir, providers.StoredProviderConfig{Provider: "openai", Model: "gpt", Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools.json"), []byte(`{"web":{"fetch":{"enabled":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	migrateToolsConfig(dir, &stderr)
	if _, err := os.Stat(filepath.Join(dir, "tools.json")); !os.IsNotExist(err) {
		t.Fatalf("tools.json should be removed: %v", err)
	}
	cfg, err := providers.LoadStoredConfig(dir)
	if err != nil || cfg.Provider != "openai" || cfg.Theme != "dark" || string(cfg.Web) != `{"fetch":{"enabled":false}}` {
		t.Fatalf("cfg = %+v (web %s), err = %v", cfg, cfg.Web, err)
	}
	config, err := webtools.LoadConfig(dir)
	if err != nil || config.Fetch.Enabled || !config.Search.Enabled {
		t.Fatalf("web config = %+v, err = %v; want the explicit fetch off kept", config, err)
	}
	// Saving another preference must keep the web settings.
	cfg.Theme = "light"
	if err := providers.SaveStoredConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if config, err := webtools.LoadConfig(dir); err != nil || config.Fetch.Enabled {
		t.Fatalf("web settings lost after save: %+v, %v", config, err)
	}
}

func TestMigrateToolsConfigLeavesMalformedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tools.json"), []byte(`{"web":`), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateToolsConfig(dir, &bytes.Buffer{})
	if _, err := os.Stat(filepath.Join(dir, "tools.json")); err != nil {
		t.Fatalf("malformed tools.json should stay: %v", err)
	}
	if _, err := webtools.LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "tools.json") {
		t.Fatalf("LoadConfig should keep reporting tools.json: %v", err)
	}
}
