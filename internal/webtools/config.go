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
	configFileName = "tools.json"
	keyFileName    = "tool-keys.json"
	// braveBackend is the Brave search backend name; the full supported list
	// lives in SupportedBackends (provider.go).
	braveBackend = "brave"

	// SearchKeyEnv is the Brave environment variable that wins over
	// tool-keys.json; the other key-bearing backends have their own below.
	SearchKeyEnv = "BRAVE_SEARCH_API_KEY"
	TavilyKeyEnv = "TAVILY_API_KEY"
	ExaKeyEnv    = "EXA_API_KEY"
)

// Config is the nonsecret web-tool enablement loaded from
// <stateDir>/tools.json. Neither credentials nor environment presence enables
// a tool by itself.
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

// LoadConfig reads <stateDir>/tools.json shaped
// {"web":{"search":{"enabled":true,"backend":"brave"},"fetch":{"enabled":true}}}.
// A missing file returns the zero Config with a nil error (unconfigured, tools
// disabled). Enabled search with no backend uses DefaultBackend (keyless
// DuckDuckGo). Malformed JSON or an unsupported backend returns an error
// naming the problem; the caller disables both web tools and reports it.
func LoadConfig(stateDir string) (Config, error) {
	var config Config
	if strings.TrimSpace(stateDir) == "" {
		return config, errors.New("a state directory is required to load " + configFileName)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, configFileName))
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("read %s: %w", configFileName, err)
	}
	root, err := decodeJSONObject(data, configFileName)
	if err != nil {
		return config, err
	}
	web, present, err := objectField(root, "web", configFileName)
	if err != nil {
		return config, err
	}
	if !present {
		return config, nil
	}
	if raw, exists := web["search"]; exists {
		section, ok := raw.(map[string]any)
		if !ok {
			return config, fmt.Errorf("%s: web.search must be a JSON object", configFileName)
		}
		if config.Search.Enabled, err = boolField(section, "enabled", configFileName+": web.search.enabled"); err != nil {
			return config, err
		}
		config.Search.Backend, err = stringField(section, "backend", configFileName+": web.search.backend")
		if err != nil {
			return config, err
		}
	}
	if raw, exists := web["fetch"]; exists {
		section, ok := raw.(map[string]any)
		if !ok {
			return config, fmt.Errorf("%s: web.fetch must be a JSON object", configFileName)
		}
		if config.Fetch.Enabled, err = boolField(section, "enabled", configFileName+": web.fetch.enabled"); err != nil {
			return config, err
		}
	}
	if config.Search.Backend == "" {
		if config.Search.Enabled {
			// Search works without a key unless the user opts into a keyed
			// backend: an enabled section with no backend means DuckDuckGo.
			config.Search.Backend = DefaultBackend
		}
	} else if !supportedBackend(config.Search.Backend) {
		return config, fmt.Errorf("%s: web.search.backend %q is not supported; supported backends are: %s",
			configFileName, config.Search.Backend, strings.Join(SupportedBackends, ", "))
	}
	return config, nil
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
