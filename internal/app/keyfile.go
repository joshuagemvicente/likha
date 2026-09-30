package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"lisa/internal/model"
)

// Credentials live only in the private state directory, never in the
// repository and never in the session database. The file holds one
// credential per predefined hosted provider: either a static API key
// (type "api") or an OAuth login (type "oauth").
//
// Schema v2 wraps every entry in an object so OAuth tokens (refresh, access,
// expiry, account id) fit beside plain keys. Files written by older versions
// hold a plain map of API keys and are migrated on read.
func keyFilePath(stateDir string) string {
	return filepath.Join(stateDir, "providers.json")
}

type storedCredential struct {
	Type      string `json:"type"` // "api" or "oauth"; empty (legacy) means "api"
	Key       string `json:"key,omitempty"`
	Refresh   string `json:"refresh,omitempty"`
	Access    string `json:"access,omitempty"`
	Expires   int64  `json:"expires,omitempty"` // Unix milliseconds
	AccountID string `json:"account_id,omitempty"`
}

// readCredentials loads the credential file, migrating the legacy
// map-of-plain-keys schema in memory. A missing file is not an error; a
// corrupt file is.
func readCredentials(stateDir string) (map[string]storedCredential, error) {
	data, err := os.ReadFile(keyFilePath(stateDir))
	if os.IsNotExist(err) {
		return map[string]storedCredential{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading stored API keys: %w", err)
	}
	var creds map[string]storedCredential
	if err := json.Unmarshal(data, &creds); err == nil {
		return creds, nil
	}
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("stored API key file %s is not valid JSON: %w", keyFilePath(stateDir), err)
	}
	creds = make(map[string]storedCredential, len(legacy))
	for name, key := range legacy {
		creds[name] = storedCredential{Type: "api", Key: key}
	}
	return creds, nil
}

// writeCredentials persists the credential map with user-only permissions.
func writeCredentials(stateDir string, creds map[string]storedCredential) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("encoding stored credentials: %w", err)
	}
	if err := os.WriteFile(keyFilePath(stateDir), data, 0600); err != nil {
		return fmt.Errorf("writing stored API key file: %w", err)
	}
	return nil
}

// storedKey returns the stored API key for a provider, or "" when none is
// stored. OAuth credentials carry no static key and return "".
func storedKey(stateDir, provider string) (string, error) {
	creds, err := readCredentials(stateDir)
	if err != nil {
		return "", err
	}
	return creds[provider].Key, nil
}

// storedOAuth returns the stored OAuth login for a provider and whether one
// exists. A missing file is not an error; a corrupt file is.
func storedOAuth(stateDir, provider string) (model.OAuthCredentials, bool, error) {
	creds, err := readCredentials(stateDir)
	if err != nil {
		return model.OAuthCredentials{}, false, err
	}
	entry := creds[provider]
	if entry.Type != "oauth" {
		return model.OAuthCredentials{}, false, nil
	}
	return model.OAuthCredentials{
		Refresh:   entry.Refresh,
		Access:    entry.Access,
		Expires:   entry.Expires,
		AccountID: entry.AccountID,
	}, true, nil
}

// storeKey persists a provider API key with user-only permissions. An empty
// key deletes the stored credential. Other providers' credentials,
// including OAuth logins, are preserved.
func storeKey(stateDir, provider, key string) error {
	creds, err := readCredentials(stateDir)
	if err != nil {
		return err
	}
	if key == "" {
		delete(creds, provider)
	} else {
		creds[provider] = storedCredential{Type: "api", Key: key}
	}
	return writeCredentials(stateDir, creds)
}

// storeOAuth persists an OAuth login with user-only permissions.
func storeOAuth(stateDir, provider string, c model.OAuthCredentials) error {
	creds, err := readCredentials(stateDir)
	if err != nil {
		return err
	}
	creds[provider] = storedCredential{Type: "oauth", Refresh: c.Refresh, Access: c.Access, Expires: c.Expires, AccountID: c.AccountID}
	return writeCredentials(stateDir, creds)
}
