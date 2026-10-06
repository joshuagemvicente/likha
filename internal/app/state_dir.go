package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"likha/internal/providers"
	"likha/internal/webtools"
)

// resolveStateDir picks Likha's private state directory: LIKHA_STATE_DIR
// when set, otherwise $XDG_CONFIG_HOME/likha or ~/.config/likha on every Unix
// including macOS (os.UserConfigDir on Windows). Before 2026-10-05 macOS used
// ~/Library/Application Support/likha; a directory left there is moved once
// to the new location. Notices go to stderr.
func resolveStateDir(stderr io.Writer) (string, error) {
	if dir := os.Getenv("LIKHA_STATE_DIR"); dir != "" {
		return dir, nil
	}
	dir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return migrateLegacyStateDir(dir, legacyStateDir(), stderr), nil
}

func defaultStateDir() (string, error) {
	if runtime.GOOS == "windows" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(configDir, "likha"), nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "likha"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "likha"), nil
}

// legacyStateDir is the pre-2026-10-05 macOS location, or "" elsewhere.
func legacyStateDir() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "likha")
}

// migrateLegacyStateDir moves legacy to dir in one rename when legacy exists
// and dir does not, and returns the directory to use. The whole directory
// moves together, so the sessions database keeps its -wal/-shm files and
// every file keeps its permissions. When both exist, dir wins and legacy is
// left untouched with a notice; the two are never merged. When the move
// fails, Likha keeps using legacy for this run so no session or key is
// hidden.
func migrateLegacyStateDir(dir, legacy string, stderr io.Writer) string {
	if legacy == "" || filepath.Clean(legacy) == filepath.Clean(dir) {
		return dir
	}
	if _, err := os.Lstat(legacy); err != nil {
		return dir
	}
	if _, err := os.Lstat(dir); err == nil {
		fmt.Fprintf(stderr, "likha: using %s; the old state directory %s is ignored (move what you need, then delete it)\n", dir, legacy)
		return dir
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "likha: cannot check %s (%v); using %s\n", dir, err, legacy)
		return legacy
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		fmt.Fprintf(stderr, "likha: cannot create %s (%v); still using %s\n", filepath.Dir(dir), err, legacy)
		return legacy
	}
	if err := os.Rename(legacy, dir); err != nil {
		fmt.Fprintf(stderr, "likha: cannot move %s to %s (%v); still using %s\n", legacy, dir, err, legacy)
		return legacy
	}
	fmt.Fprintf(stderr, "likha: moved settings and sessions from %s to %s\n", legacy, dir)
	return dir
}

// migrateToolsConfig folds a legacy tools.json "web" section into
// config.json and removes tools.json. Explicit values (including
// "enabled": false) are copied verbatim. A malformed tools.json is left in
// place so the web tools keep reporting it; a tools.json beside a config.json
// that already has web settings is left alone with a notice.
func migrateToolsConfig(stateDir string, stderr io.Writer) {
	web, err := webtools.LegacyWebSection(stateDir)
	if err != nil || web == nil {
		return
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		return
	}
	legacy := webtools.LegacyConfigPath(stateDir)
	if cfg.Web != nil {
		fmt.Fprintf(stderr, "likha: %s is ignored because config.json already has web settings; delete it\n", legacy)
		return
	}
	cfg.Web = web
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		fmt.Fprintf(stderr, "likha: cannot move web settings into config.json: %v\n", err)
		return
	}
	if err := os.Remove(legacy); err != nil {
		fmt.Fprintf(stderr, "likha: web settings now live in config.json; remove %s yourself (%v)\n", legacy, err)
		return
	}
	fmt.Fprintln(stderr, "likha: moved web settings from tools.json into config.json")
}
