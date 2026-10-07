package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ServerConfig mirrors one mcpServers entry: either a stdio server command
// plus environment, or a Streamable HTTP server URL plus static headers.
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Type    string            `json:"type,omitempty"`
}

// Remote reports whether the entry names a Streamable HTTP server.
func (c ServerConfig) Remote() bool {
	return strings.TrimSpace(c.URL) != ""
}

// userConfig is the on-disk shape of mcp.json — a Claude-Desktop-shaped
// mcpServers map so configs from other harnesses can be pasted verbatim.
type userConfig struct {
	McpServers map[string]ServerConfig `json:"mcpServers"`
}

// EnsureSkeleton creates the state directory (0700) and an empty mcp.json
// skeleton if either is missing, so the user always has a file to edit —
// like other harnesses that scaffold their config on first run. An existing
// mcp.json is never overwritten. Returns the file path.
func EnsureSkeleton(stateDir string) (string, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", fmt.Errorf("create state directory: %w", err)
	}
	path := filepath.Join(stateDir, "mcp.json")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect mcp.json: %w", err)
	}
	skeleton := []byte("{\n  \"mcpServers\": {}\n}\n")
	if err := os.WriteFile(path, skeleton, 0o600); err != nil {
		return "", fmt.Errorf("write mcp.json: %w", err)
	}
	return path, nil
}

// LoadConfig reads <stateDir>/mcp.json. A missing file is not an error (the
// feature is simply unused); a corrupt or wrong-shaped file is.
func LoadConfig(stateDir string) (map[string]ServerConfig, error) {
	path := filepath.Join(stateDir, "mcp.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading mcp.json: %w", err)
	}
	var cfg userConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("mcp.json at %s is not valid JSON: %w", path, err)
	}
	if len(cfg.McpServers) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(cfg.McpServers))
	for name := range cfg.McpServers {
		names = append(names, name)
	}
	sort.Strings(names) // report the same first problem on every launch
	for _, name := range names {
		if err := validateServer(name, cfg.McpServers[name]); err != nil {
			return nil, fmt.Errorf("mcp.json: %w", err)
		}
	}
	return cfg.McpServers, nil
}

// validateServer checks one entry's shape. stdio entries keep their
// historical rule (a non-empty command); fields they never used, such as
// "type" or "headers", stay ignored as they always were, so pasted Claude
// Code entries with "type": "stdio" still load.
func validateServer(name string, cfg ServerConfig) error {
	hasCommand := strings.TrimSpace(cfg.Command) != ""
	if name == "" || !hasCommand && !cfg.Remote() {
		return fmt.Errorf("server %q must have a command or a url", name)
	}
	if hasCommand && cfg.Remote() {
		return fmt.Errorf("server %q has both a command and a url; use one", name)
	}
	if !cfg.Remote() {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "http", "streamable-http":
	case "sse":
		return fmt.Errorf("server %q: legacy HTTP+SSE servers are not supported; use the server's Streamable HTTP endpoint", name)
	default:
		return fmt.Errorf("server %q: unsupported type %q for a url server (use \"http\")", name, cfg.Type)
	}
	if len(cfg.Args) > 0 || len(cfg.Env) > 0 {
		return fmt.Errorf("server %q: args and env apply only to stdio servers", name)
	}
	if err := validateURL(cfg.URL); err != nil {
		return fmt.Errorf("server %q: %w", name, err)
	}
	for header, value := range cfg.Headers {
		if !validHeaderName(header) {
			return fmt.Errorf("server %q: invalid header name %q", name, header)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("server %q: header %s contains a line break", name, header)
		}
		for _, ref := range envReferences(value) {
			if providerKeyVariable(ref) {
				return fmt.Errorf("server %q: header %s may not use %s; Likha's provider keys never reach MCP servers", name, header, ref)
			}
		}
	}
	return nil
}

// validateURL allows https anywhere and plain http only to loopback, so a
// bearer token never crosses the network unencrypted.
func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("url is not valid: %w", err)
	}
	if u.Host == "" || u.Hostname() == "" {
		return errors.New("url must be absolute, like https://host/mcp")
	}
	if u.User != nil {
		return errors.New("url must not contain a user name or password; use headers")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopbackHost(u.Hostname()) {
			return nil
		}
		return errors.New("plain http is only allowed for localhost; use https")
	default:
		return fmt.Errorf("url scheme %q is not supported; use https", u.Scheme)
	}
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// validHeaderName accepts an RFC 9110 token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// envReference matches ${NAME}; any other text, including a lone "${", is
// literal.
var envReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func envReferences(value string) []string {
	var names []string
	for _, match := range envReference.FindAllStringSubmatch(value, -1) {
		names = append(names, match[1])
	}
	return names
}

// providerKeyVariable mirrors actions.CommandEnv: LIKHA_API_KEY and every
// LIKHA_<NAME>_API_KEY are Likha's provider credentials.
func providerKeyVariable(name string) bool {
	return name == "LIKHA_API_KEY" || strings.HasPrefix(name, "LIKHA_") && strings.HasSuffix(name, "_API_KEY")
}

// expandHeaders replaces each ${NAME} with the variable's value. It returns
// the expanded headers and every value that must be redacted from output,
// or an error naming the first unset or empty variable.
func expandHeaders(headers map[string]string, lookup func(string) (string, bool)) (map[string]string, []string, error) {
	expanded := make(map[string]string, len(headers))
	var secrets []string
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var missing string
		value := envReference.ReplaceAllStringFunc(headers[name], func(ref string) string {
			variable := envReference.FindStringSubmatch(ref)[1]
			got, ok := lookup(variable)
			if !ok || got == "" {
				if missing == "" {
					missing = variable
				}
				return ""
			}
			secrets = append(secrets, got)
			return got
		})
		if missing != "" {
			return nil, nil, fmt.Errorf("environment variable %s is not set", missing)
		}
		expanded[name] = value
		secrets = append(secrets, value)
	}
	return expanded, secrets, nil
}
