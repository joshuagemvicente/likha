package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"likha/internal/model"
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
	Efforts         map[string]map[string]string          `json:"efforts,omitempty"`
	Variants        map[string]map[string]string          `json:"variants,omitempty"`
	DefaultVariants map[string]map[string]string          `json:"default_variants,omitempty"`
	VariantPresets  map[string]map[string][]model.Variant `json:"variant_presets,omitempty"`
	ProviderOptions map[string]model.Overlay              `json:"provider_options,omitempty"`
	ModelOptions    map[string]map[string]model.Overlay   `json:"model_options,omitempty"`
	extra           map[string]json.RawMessage
	// CommandTrust records, per canonical repository path, the verification
	// checks the user trusted and their fingerprints at that time
	// (specs/command-permissions). It is never read from a repository.
	CommandTrust map[string]StoredCommandTrust `json:"command_trust,omitempty"`
	// Web holds the web-tool settings (specs/web-tools) verbatim. webtools
	// owns their meaning and reads them straight from the file; this field
	// exists so saving a theme or model never drops them.
	Web json.RawMessage `json:"web,omitempty"`
	// AutoCompact turns automatic compaction off when explicitly false
	// (specs/auto-compaction D2). Absent means on, so config.json files
	// stored before the key existed need no rewrite.
	AutoCompact *bool `json:"auto_compact,omitempty"`
	// Attention holds the attention-signal settings (specs/attention-notifications).
	Attention *StoredAttentionConfig `json:"attention,omitempty"`
}

// AutoCompactEnabled reports whether automatic compaction is on.
func (c StoredProviderConfig) AutoCompactEnabled() bool {
	return FlagEnabled(c.AutoCompact)
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
	path := ConfigFilePath(stateDir)
	data, err := readPrivateFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if errors.Is(err, syscall.ELOOP) {
		// Overlay headers in config.json may carry private gateway
		// credentials, so it is opened like a credential file: never through
		// a link whose target someone else could control.
		return cfg, fmt.Errorf("config.json must not be a symlink: %s: %w", path, err)
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
	unlock, err := lockCredentials(context.Background(), stateDir)
	if err != nil {
		return fmt.Errorf("lock configuration: %w", err)
	}
	defer unlock()
	if _, err := LoadStoredConfig(stateDir); err != nil {
		return err
	}
	return saveConfigLocked(stateDir, cfg)
}

func saveConfigLocked(stateDir string, cfg StoredProviderConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding stored provider config: %w", err)
	}
	if err := writePrivateFile(ConfigFilePath(stateDir), data); err != nil {
		return fmt.Errorf("writing stored provider config: %w", err)
	}
	return nil
}

// UpdateStoredConfig locks the whole read-modify-write transaction. A corrupt
// existing file is never replaced with an empty configuration.
func UpdateStoredConfig(ctx context.Context, stateDir string, update func(*StoredProviderConfig) error) error {
	unlock, err := lockCredentials(ctx, stateDir)
	if err != nil {
		return fmt.Errorf("lock configuration: %w", err)
	}
	defer unlock()
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil {
		return err
	}
	if err := update(&cfg); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return saveConfigLocked(stateDir, cfg)
}

// UnmarshalJSON retains unrecognized private settings across later updates.
func (c *StoredProviderConfig) UnmarshalJSON(data []byte) error {
	type plain StoredProviderConfig
	var decoded plain
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("configuration must be a JSON object")
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	// Omitted known fields must not reappear from the preserved extras.
	for key := range fields {
		switch key {
		case "provider", "model", "theme", "composer", "status_line", "context_windows", "efforts", "variants", "default_variants", "variant_presets", "provider_options", "model_options", "command_trust", "web", "auto_compact", "attention":
			delete(fields, key)
		}
	}
	*c = StoredProviderConfig(decoded)
	c.extra = fields
	return nil
}

// MarshalJSON writes known settings and preserves unknown JSON values verbatim.
func (c StoredProviderConfig) MarshalJSON() ([]byte, error) {
	type plain StoredProviderConfig
	raw, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for key, value := range c.extra {
		if _, known := fields[key]; !known {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

// StoredVariant distinguishes a missing preference from explicit Default.
func (c StoredProviderConfig) StoredVariant(provider, id string) (string, bool) {
	choice, ok := c.Variants[provider][id]
	if choice == "default" {
		choice = ""
	}
	return choice, ok
}

// SetStoredVariant records Default explicitly, never as a missing pair.
func (c *StoredProviderConfig) SetStoredVariant(provider, id, choice string) {
	if choice == "" {
		choice = "default"
	}
	if c.Variants == nil {
		c.Variants = make(map[string]map[string]string)
	}
	if c.Variants[provider] == nil {
		c.Variants[provider] = make(map[string]string)
	}
	c.Variants[provider][id] = choice
}

// VariantConfig returns the overlays and ordered definitions for an exact pair.
func (c StoredProviderConfig) VariantConfig(provider, id string) model.VariantConfig {
	return model.VariantConfig{Provider: c.ProviderOptions[provider], Model: c.ModelOptions[provider][id], Presets: c.VariantPresets[provider][id]}
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
