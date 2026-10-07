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
	// Efforts records the reasoning-effort level chosen per provider and model
	// (specs/model-effort). A missing pair means Default: no effort is sent.
	Efforts map[string]map[string]string `json:"efforts,omitempty"`
	// CommandTrust records, per canonical repository path, the verification
	// checks the user trusted and their fingerprints at that time
	// (specs/command-permissions). It is never read from a repository.
	CommandTrust map[string]StoredCommandTrust `json:"command_trust,omitempty"`
	// Web holds the web-tool settings (specs/web-tools) verbatim. webtools
	// owns their meaning and reads them straight from the file; this field
	// exists so saving a theme or model never drops them.
	Web json.RawMessage `json:"web,omitempty"`
	// Attention holds the attention-signal settings (specs/attention-notifications).
	Attention *StoredAttentionConfig `json:"attention,omitempty"`
}

// StoredAttentionConfig controls the terminal bell, the OSC 9 desktop
// notification, and the opt-in ask_user auto-skip. Bell and Desktop are
// default-on pointers resolved with FlagEnabled; AskSkipAfterMinutes 0 (or
// absent) leaves questions waiting indefinitely.
type StoredAttentionConfig struct {
	Bell                *bool `json:"bell,omitempty"`
	Desktop             *bool `json:"desktop,omitempty"`
	AskSkipAfterMinutes int   `json:"ask_skip_after_minutes,omitempty"`
}

// Attention auto-skip bounds, in whole minutes (specs/attention-notifications D2).
const (
	MinAskSkipMinutes = 1
	MaxAskSkipMinutes = 120
)

// AskSkipLimit resolves the auto-skip setting. ok is false when the stored
// value is outside 1–120; the caller then treats auto-skip as off and tells
// the user, because an out-of-range value is a typo, not a reason to refuse
// to start.
func (c StoredAttentionConfig) AskSkipLimit() (minutes int, ok bool) {
	switch {
	case c.AskSkipAfterMinutes == 0:
		return 0, true
	case c.AskSkipAfterMinutes < MinAskSkipMinutes || c.AskSkipAfterMinutes > MaxAskSkipMinutes:
		return 0, false
	default:
		return c.AskSkipAfterMinutes, true
	}
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

// StoredEffort returns the effort level stored for the provider and model,
// or "" (Default) when none is stored.
func (c StoredProviderConfig) StoredEffort(provider, modelID string) string {
	return c.Efforts[provider][modelID]
}

// SetStoredEffort records level for the provider and model. An empty level
// (Default) removes the entry, and empty provider maps are dropped, so
// config.json never holds a pair that means "send nothing".
func (c *StoredProviderConfig) SetStoredEffort(provider, modelID, level string) {
	if level == "" {
		delete(c.Efforts[provider], modelID)
		if len(c.Efforts[provider]) == 0 {
			delete(c.Efforts, provider)
		}
		return
	}
	if c.Efforts == nil {
		c.Efforts = map[string]map[string]string{}
	}
	if c.Efforts[provider] == nil {
		c.Efforts[provider] = map[string]string{}
	}
	c.Efforts[provider][modelID] = level
}
