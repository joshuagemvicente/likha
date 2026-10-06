package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"likha/internal/model"
)

// Credentials live only in the private state directory, never in the
// repository or the session database. OAuth entries retain separate issued
// client registrations; flat OAuth token fields are legacy Codex data only.
func KeyFilePath(stateDir string) string {
	return filepath.Join(stateDir, "providers.json")
}

type StoredCredential struct {
	Type      string `json:"type"` // "api" or "oauth"; empty (legacy) means "api"
	Key       string `json:"key,omitempty"`
	Refresh   string `json:"refresh,omitempty"`
	Access    string `json:"access,omitempty"`
	Expires   int64  `json:"expires,omitempty"` // Unix milliseconds
	AccountID string `json:"account_id,omitempty"`

	ActiveClientID string                            `json:"active_client_id,omitempty"`
	Accounts       map[string]model.OAuthCredentials `json:"accounts,omitempty"`
}

// ReadCredentials migrates old plain API keys in memory. Missing storage is
// empty; malformed storage is an error and is never replaced automatically.
func ReadCredentials(stateDir string) (map[string]StoredCredential, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, fmt.Errorf("private credential state directory is required")
	}
	data, err := readPrivateFile(KeyFilePath(stateDir))
	if os.IsNotExist(err) {
		return map[string]StoredCredential{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading stored credentials: %w", err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("stored credential file %s is not valid JSON: %w", KeyFilePath(stateDir), err)
	}
	if entries == nil {
		return nil, fmt.Errorf("stored credential file %s must contain an object", KeyFilePath(stateDir))
	}
	creds := make(map[string]StoredCredential, len(entries))
	for name, raw := range entries {
		raw = bytes.TrimSpace(raw)
		var key string
		if len(raw) > 0 && raw[0] == '"' {
			if err := json.Unmarshal(raw, &key); err != nil {
				return nil, fmt.Errorf("invalid stored API key for provider %s: %w", name, err)
			}
			creds[name] = StoredCredential{Type: "api", Key: key}
			continue
		}
		var entry StoredCredential
		if len(raw) == 0 || raw[0] != '{' {
			return nil, fmt.Errorf("invalid stored credential for provider %s: expected an object or an API key string", name)
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, fmt.Errorf("invalid stored credential for provider %s: %w", name, err)
		}
		if err := validateOAuthEntry(entry); err != nil {
			return nil, fmt.Errorf("invalid stored credential for provider %s: %w", name, err)
		}
		creds[name] = entry
	}
	return creds, nil
}

// WriteCredentials atomically replaces a complete credential map. Callers
// updating one provider should use StoreKey or the OAuth lifecycle functions,
// which also hold the lock while reloading the other entries.
func WriteCredentials(stateDir string, creds map[string]StoredCredential) error {
	unlock, err := lockCredentials(context.Background(), stateDir)
	if err != nil {
		return err
	}
	defer unlock()
	return writeCredentials(stateDir, creds)
}

// writeCredentials is called only while the credential lock is held.
func writeCredentials(stateDir string, creds map[string]StoredCredential) error {
	if creds == nil {
		creds = map[string]StoredCredential{}
	}
	for name, entry := range creds {
		if err := validateOAuthEntry(entry); err != nil {
			return fmt.Errorf("invalid stored credential for provider %s: %w", name, err)
		}
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("encoding stored credentials: %w", err)
	}
	if err := writePrivateFile(KeyFilePath(stateDir), data); err != nil {
		return fmt.Errorf("writing stored credential file: %w", err)
	}
	return nil
}

// StoredKey never treats an OAuth (or unknown-type) entry as an API key,
// including malformed old entries that happen to carry a key field.
func StoredKey(stateDir, provider string) (string, error) {
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return "", err
	}
	entry := creds[provider]
	if entry.Type != "" && entry.Type != "api" {
		return "", nil
	}
	return entry.Key, nil
}

// StoredOAuth returns the active, renewable registration. Legacy Codex tokens
// and retained signed-out registrations are never considered signed in.
func StoredOAuth(stateDir, provider string) (model.OAuthCredentials, bool, error) {
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return model.OAuthCredentials{}, false, err
	}
	entry := creds[provider]
	if entry.Type != "oauth" || entry.ActiveClientID == "" {
		return model.OAuthCredentials{}, false, nil
	}
	c := entry.Accounts[entry.ActiveClientID]
	if !oauthSignedIn(c) {
		return model.OAuthCredentials{}, false, nil
	}
	if err := checkOAuthHost(stateDir, c.HostID); err != nil {
		return model.OAuthCredentials{}, false, err
	}
	return c, true, nil
}

// StoreKey updates one API-key entry without racing other credential writes.
// It cannot delete or replace OAuth account registrations.
func StoreKey(stateDir, provider, key string) error {
	unlock, err := lockCredentials(context.Background(), stateDir)
	if err != nil {
		return err
	}
	defer unlock()
	creds, err := ReadCredentials(stateDir)
	if err != nil {
		return err
	}
	if creds[provider].Type == "oauth" {
		return fmt.Errorf("provider %s uses account sign-in; use account sign-out instead of replacing its API key", provider)
	}
	if key == "" {
		delete(creds, provider)
	} else {
		creds[provider] = StoredCredential{Type: "api", Key: key}
	}
	return writeCredentials(stateDir, creds)
}
