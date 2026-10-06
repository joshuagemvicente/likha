// Package webtools holds configuration, consent vocabulary, and the search
// provider clients for the optional web tools. It intentionally depends on the
// standard library only, so internal/tools can import it to wrap search as a
// builtin tool without creating an import cycle.
package webtools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// configFileName is the main likha config file; web settings live under
	// its top-level "web" key beside the provider, model, and theme.
	configFileName = "config.json"
	// legacyConfigFileName held the web settings before they moved into
	// config.json. It is read only while config.json has no "web" key;
	// app startup folds it into config.json (MigrateLegacyConfig).
	legacyConfigFileName = "tools.json"
	keyFileName          = "tool-keys.json"
	// braveBackend is the Brave search backend name; the full supported list
	// lives in SupportedBackends (provider.go).
	braveBackend = "brave"

	// SearchKeyEnv is the Brave environment variable that wins over
	// tool-keys.json; the other key-bearing backends have their own below.
	SearchKeyEnv = "BRAVE_SEARCH_API_KEY"
	TavilyKeyEnv = "TAVILY_API_KEY"
	ExaKeyEnv    = "EXA_API_KEY"
)

// Config is the nonsecret web-tool enablement loaded from the "web" key of
// <stateDir>/config.json. Neither credentials nor environment presence
// enables a tool by itself; both tools are on unless the file turns them off.
type Config struct {
	Search SearchConfig `json:"search"`
	Fetch  FetchConfig  `json:"fetch"`
}

type SearchConfig struct {
	Enabled bool   `json:"enabled"`
	Backend string `json:"backend"`
}

type FetchConfig struct {
	Enabled bool `json:"enabled"`
}

// DefaultConfig is the configuration when nothing is stored: keyless
// DuckDuckGo search and public HTTPS fetch both on. Each still asks for
// consent on first use in a conversation.
func DefaultConfig() Config {
	return Config{
		Search: SearchConfig{Enabled: true, Backend: DefaultBackend},
		Fetch:  FetchConfig{Enabled: true},
	}
}

// LoadConfig reads the "web" key of <stateDir>/config.json, shaped
// {"web":{"search":{"enabled":true,"backend":"brave"},"fetch":{"enabled":true}}}.
// Every field is optional and an absent one keeps DefaultConfig: search and
// fetch on, backend DuckDuckGo. Only an explicit "enabled": false turns a
// tool off. While config.json has no "web" key, a legacy <stateDir>/tools.json
// of the same shape is read instead. Malformed JSON or an unsupported backend
// returns an error naming the file and the problem; the caller disables both
// web tools and reports it.
func LoadConfig(stateDir string) (Config, error) {
	config := DefaultConfig()
	if strings.TrimSpace(stateDir) == "" {
		return config, errors.New("a state directory is required to load " + configFileName)
	}
	web, file, err := readWebSection(stateDir)
	if err != nil || web == nil {
		return config, err
	}
	if raw, exists := web["search"]; exists {
		section, ok := raw.(map[string]any)
		if !ok {
			return config, fmt.Errorf("%s: web.search must be a JSON object", file)
		}
		if config.Search.Enabled, err = boolFieldDefault(section, "enabled", true, file+": web.search.enabled"); err != nil {
			return config, err
		}
		backend, err := stringField(section, "backend", file+": web.search.backend")
		if err != nil {
			return config, err
		}
		if backend != "" {
			if !supportedBackend(backend) {
				return config, fmt.Errorf("%s: web.search.backend %q is not supported; supported backends are: %s",
					file, backend, strings.Join(SupportedBackends, ", "))
			}
			config.Search.Backend = backend
		}
	}
	if raw, exists := web["fetch"]; exists {
		section, ok := raw.(map[string]any)
		if !ok {
			return config, fmt.Errorf("%s: web.fetch must be a JSON object", file)
		}
		if config.Fetch.Enabled, err = boolFieldDefault(section, "enabled", true, file+": web.fetch.enabled"); err != nil {
			return config, err
		}
	}
	return config, nil
}

// readWebSection returns the "web" object and the file it came from:
// config.json when it has a "web" key, otherwise a legacy tools.json. A nil
// object with a nil error means nothing is configured.
func readWebSection(stateDir string) (map[string]any, string, error) {
	for _, file := range []string{configFileName, legacyConfigFileName} {
		data, err := os.ReadFile(filepath.Join(stateDir, file))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, file, fmt.Errorf("read %s: %w", file, err)
		}
		root, err := decodeJSONObject(data, file)
		if err != nil {
			return nil, file, err
		}
		web, present, err := objectField(root, "web", file)
		if err != nil {
			return nil, file, err
		}
		if present {
			return web, file, nil
		}
	}
	return nil, "", nil
}

// LegacyWebSection reads the "web" object of a legacy <stateDir>/tools.json
// as raw JSON for folding into config.json. It returns nil with a nil error
// when the file is absent or has no "web" key, and an error when the file
// is unreadable or malformed (it is then left in place and LoadConfig keeps
// reporting the problem).
func LegacyWebSection(stateDir string) (json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, legacyConfigFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", legacyConfigFileName, err)
	}
	root, err := decodeJSONObject(data, legacyConfigFileName)
	if err != nil {
		return nil, err
	}
	if _, present, err := objectField(root, "web", legacyConfigFileName); err != nil || !present {
		return nil, err
	}
	var raw struct {
		Web json.RawMessage `json:"web"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s must contain one JSON object", legacyConfigFileName)
	}
	return raw.Web, nil
}

// LegacyConfigPath is the legacy tools.json path, removed after its "web"
// section is folded into config.json.
func LegacyConfigPath(stateDir string) string {
	return filepath.Join(stateDir, legacyConfigFileName)
}

// searchKeyEnv maps each key-bearing search backend to the environment
// variable that wins over tool-keys.json. DuckDuckGo is absent: it needs no
// credential and LoadSearchKey returns "" for it.
var searchKeyEnv = map[string]string{
	braveBackend:  SearchKeyEnv,
	tavilyBackend: TavilyKeyEnv,
	exaBackend:    ExaKeyEnv,
}

// LoadSearchKey resolves the search credential for one backend: the backend's
// environment variable (BRAVE_SEARCH_API_KEY, TAVILY_API_KEY, or EXA_API_KEY)
// wins over the private <stateDir>/tool-keys.json entry keyed by the backend
// name ("brave", "tavily", or "exa"). DuckDuckGo needs no key and returns ""
// with a nil error, as does a missing key source for the other backends. An
// unknown backend returns an error naming it and the supported list. Never
// reads a repository .env, never logs or returns the key itself in errors,
// and never relaxes or chown-corrects permissions: the file must be 0600 in a
// 0700 directory, and a world/group-readable or writable location fails with
// an error asking the user to fix it manually.
func LoadSearchKey(stateDir, backend string) (string, error) {
	if !supportedBackend(backend) {
		return "", unsupportedBackendError(backend)
	}
	if backend == duckduckgoBackend {
		return "", nil
	}
	envVar := searchKeyEnv[backend]
	if key := strings.TrimSpace(os.Getenv(envVar)); key != "" {
		if !validKey(key) {
			return "", errors.New(envVar + " contains control or invalid characters")
		}
		return key, nil
	}
	if strings.TrimSpace(stateDir) == "" {
		return "", nil
	}
	path := filepath.Join(stateDir, keyFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", keyFileName, err)
	}
	if err := checkPrivatePermissions(path, keyFileName); err != nil {
		return "", err
	}
	root, err := decodeJSONObject(data, keyFileName)
	if err != nil {
		return "", err
	}
	raw, exists := root[backend]
	if !exists {
		return "", nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s: %q must be a JSON string", keyFileName, backend)
	}
	key := strings.TrimSpace(text)
	if key == "" {
		return "", nil
	}
	if !validKey(key) {
		return "", fmt.Errorf("%s: the %q entry contains control or invalid characters", keyFileName, backend)
	}
	return key, nil
}

// LoadBraveSearchKey resolves the Brave credential for callers that have not
// yet moved to the per-backend LoadSearchKey; it is a transitional shim and
// can be removed once no caller needs it.
func LoadBraveSearchKey(stateDir string) (string, error) {
	return LoadSearchKey(stateDir, braveBackend)
}

func validKey(key string) bool {
	if !utf8.ValidString(key) {
		return false
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// checkPrivatePermissions reports a usable problem without printing file
// contents. The user fixes permissions manually; nothing is chmod'ed here.
func checkPrivatePermissions(path, name string) error {
	if stat, err := os.Stat(path); err != nil {
		return fmt.Errorf("stat %s: %w", name, err)
	} else if stat.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be mode 0600; the file is readable or writable by group or others, fix its permissions manually", name)
	}
	dir := filepath.Dir(path)
	if stat, err := os.Stat(dir); err != nil {
		return fmt.Errorf("stat the directory holding %s: %w", name, err)
	} else if !stat.IsDir() {
		return fmt.Errorf("the directory holding %s is not a directory", name)
	} else if stat.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("the directory holding %s must be mode 0700; fix its permissions manually", name)
	}
	return nil
}

func objectField(root map[string]any, name, file string) (map[string]any, bool, error) {
	raw, exists := root[name]
	if !exists {
		return nil, false, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("%s: %q must be a JSON object", file, name)
	}
	return object, true, nil
}

func boolField(object map[string]any, name, path string) (bool, error) {
	raw, exists := object[name]
	if !exists {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a JSON boolean", path)
	}
	return value, nil
}

// boolFieldDefault reads an optional boolean, returning fallback when the
// field is absent.
func boolFieldDefault(object map[string]any, name string, fallback bool, path string) (bool, error) {
	if _, exists := object[name]; !exists {
		return fallback, nil
	}
	return boolField(object, name, path)
}

func stringField(object map[string]any, name, path string) (string, error) {
	raw, exists := object[name]
	if !exists {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a JSON string", path)
	}
	return value, nil
}

const jsonDepthLimit = 16

// decodeJSONObject parses exactly one UTF-8 JSON object, rejecting duplicate
// fields and trailing values. It never includes the raw parse error (which can
// echo adjacent file bytes) in its message.
func decodeJSONObject(data []byte, name string) (map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s must contain valid UTF-8 JSON; rewrite the file manually", name)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	root, err := decodeJSONValue(decoder, name, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s must contain exactly one JSON value", name)
	}
	object, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must contain one JSON object", name)
	}
	return object, nil
}

func decodeJSONValue(decoder *json.Decoder, name string, depth int) (any, error) {
	if depth > jsonDepthLimit {
		return nil, fmt.Errorf("%s nests JSON too deeply", name)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("%s is not valid JSON; rewrite the file manually", name)
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok {
					return nil, fmt.Errorf("%s has a malformed JSON object key", name)
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("%s contains duplicate JSON field %q", name, key)
				}
				value, err := decodeJSONValue(decoder, name, depth+1)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			if closer, err := decoder.Token(); err != nil || closer != json.Delim('}') {
				return nil, fmt.Errorf("%s has a malformed JSON object", name)
			}
			return object, nil
		case '[':
			values := make([]any, 0)
			for decoder.More() {
				value, err := decodeJSONValue(decoder, name, depth+1)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			}
			if closer, err := decoder.Token(); err != nil || closer != json.Delim(']') {
				return nil, fmt.Errorf("%s has a malformed JSON array", name)
			}
			return values, nil
		default:
			return nil, fmt.Errorf("%s has a malformed JSON value", name)
		}
	default:
		return token, nil
	}
}
