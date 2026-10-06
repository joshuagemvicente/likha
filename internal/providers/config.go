package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type StoredComposerConfig struct {
	Style string `json:"style"`
}

type StoredStatusLineConfig struct {
	// Folder and Branch are default-ON (spec tui-layout 1a.4): the pointer
	// nil state is the new default, so config.json files stored before this
	// field existed get both segments without a rewrite. An explicit false
	// still disables. Every consumer resolves these through flagEnabled.
	Folder  *bool `json:"folder,omitempty"`
	Branch  *bool `json:"branch,omitempty"`
	Session bool  `json:"session,omitempty"`
	Version bool  `json:"version,omitempty"`
	Changes bool  `json:"changes,omitempty"`
	Staged  bool  `json:"staged,omitempty"`
	MCP     bool  `json:"mcp,omitempty"`
	Minutes bool  `json:"minutes,omitempty"`
	Tokens  bool  `json:"tokens,omitempty"`
	Update  bool  `json:"update,omitempty"`
}

// StoredProviderConfig holds the selected provider, model, theme, composer, and
// status line preferences in the private state directory, never in the
// repository or session database.
type StoredProviderConfig struct {
	Provider       string                      `json:"provider"`
	Model          string                      `json:"model"`
	Theme          string                      `json:"theme,omitempty"`
	Composer       *StoredComposerConfig       `json:"composer,omitempty"`
	StatusLine     *StoredStatusLineConfig     `json:"status_line,omitempty"`
	ContextWindows map[string]map[string]int64 `json:"context_windows,omitempty"`
	// CommandTrust records, per canonical repository path, the verification
	// checks the user trusted and their fingerprints at that time
	// (specs/command-permissions). It is never read from a repository.
	CommandTrust map[string]StoredCommandTrust `json:"command_trust,omitempty"`
}

// StoredCommandTrust maps a check key ("script:test", "go", "make") to the
// fingerprint it had when the user trusted the repository.
type StoredCommandTrust struct {
	Checks map[string]string `json:"checks"`
}

// flagEnabled resolves an optional-segment pointer: nil (the key was absent
// from config.json) means default-on for Folder and Branch; an explicit
// value wins.
func FlagEnabled(f *bool) bool {
	return f == nil || *f
}

func ConfigFilePath(stateDir string) string {
	return filepath.Join(stateDir, "config.json")
}

// loadStoredConfig returns the stored configuration, or a zero value when none
// is stored. A missing file is not an error; a corrupt file is.
func LoadStoredConfig(stateDir string) (StoredProviderConfig, error) {
	var cfg StoredProviderConfig
	data, err := os.ReadFile(ConfigFilePath(stateDir))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading stored provider config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("stored provider config %s is not valid JSON: %w", ConfigFilePath(stateDir), err)
	}
	return cfg, nil
}

// saveStoredConfig persists the configuration with user-only permissions.
func SaveStoredConfig(stateDir string, cfg StoredProviderConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding stored provider config: %w", err)
	}
	if err := os.WriteFile(ConfigFilePath(stateDir), data, 0600); err != nil {
		return fmt.Errorf("writing stored provider config: %w", err)
	}
	return nil
}
