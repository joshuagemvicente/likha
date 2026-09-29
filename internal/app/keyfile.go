package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// API keys live only in the private state directory, never in the repository
// and never in the session database. The file holds one key per predefined
// hosted provider.
func keyFilePath(stateDir string) string {
	return filepath.Join(stateDir, "providers.json")
}

// storedKey returns the stored API key for a provider, or "" when none is
// stored. A missing file is not an error; a corrupt file is.
func storedKey(stateDir, provider string) (string, error) {
	data, err := os.ReadFile(keyFilePath(stateDir))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading stored API keys: %w", err)
	}
	var keys map[string]string
	if err := json.Unmarshal(data, &keys); err != nil {
		return "", fmt.Errorf("stored API key file %s is not valid JSON: %w", keyFilePath(stateDir), err)
	}
	return keys[provider], nil
}

// storeKey persists a provider API key with user-only permissions.
func storeKey(stateDir, provider, key string) error {
	path := keyFilePath(stateDir)
	keys := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &keys); err != nil {
			return fmt.Errorf("stored API key file %s is not valid JSON: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading stored API keys: %w", err)
	}
	if key == "" {
		delete(keys, provider)
	} else {
		keys[provider] = key
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("encoding stored API keys: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing stored API key file: %w", err)
	}
	return nil
}
