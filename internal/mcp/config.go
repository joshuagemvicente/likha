package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ServerConfig mirrors one mcpServers entry: a stdio server command plus
// environment.
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
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
	for name, server := range cfg.McpServers {
		if name == "" || strings.TrimSpace(server.Command) == "" {
			return nil, fmt.Errorf("mcp.json: server %q must have a command", name)
		}
	}
	return cfg.McpServers, nil
}
