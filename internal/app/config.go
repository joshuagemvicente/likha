package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// storedProviderConfig is the first-run setup result: the provider and model
// chosen interactively. It lives beside the API keys in the private state
// directory, never in the repository or session database.
type storedProviderConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func configFilePath(stateDir string) string {
	return filepath.Join(stateDir, "config.json")
}

// loadStoredConfig returns the stored provider configuration, or a zero value
// when none is stored. A missing file is not an error; a corrupt file is.
func loadStoredConfig(stateDir string) (storedProviderConfig, error) {
	var cfg storedProviderConfig
	data, err := os.ReadFile(configFilePath(stateDir))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading stored provider config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("stored provider config %s is not valid JSON: %w", configFilePath(stateDir), err)
	}
	return cfg, nil
}

// saveStoredConfig persists the first-run setup choice with user-only
// permissions.
func saveStoredConfig(stateDir string, cfg storedProviderConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding stored provider config: %w", err)
	}
	if err := os.WriteFile(configFilePath(stateDir), data, 0600); err != nil {
		return fmt.Errorf("writing stored provider config: %w", err)
	}
	return nil
}
