package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"likha/internal/model"
)

// McpManager owns the user's configured MCP servers: lazy startup, the merged
// tool surface handed to the model, trust-on-first-use tracking, and cleanup.
// A nil manager means the feature is unused and everything is inert.
type McpManager struct {
	mu      sync.Mutex
	path    string
	servers map[string]ServerConfig
	clients map[string]*Client
	trusted map[string]bool // server name → approved for the session
	failed  map[string]string
}

func NewMcpManager(stateDir, userAgent string) (*McpManager, error) {
	path, err := EnsureSkeleton(stateDir)
	if err != nil {
		return nil, err
	}
	servers, err := LoadConfig(stateDir)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return &McpManager{path: path, clients: map[string]*Client{}, trusted: map[string]bool{}, failed: map[string]string{}}, nil
	}
	return &McpManager{
		path:    path,
		servers: servers,
		clients: map[string]*Client{},
		trusted: map[string]bool{},
		failed:  map[string]string{},
	}, nil
}

// NewMcpManagerForTest builds a manager with fixed server/client state for
// status-line and summary assertions. Production code uses NewMcpManager.
func NewMcpManagerForTest(servers map[string]ServerConfig, clients map[string]*Client, trusted map[string]bool, failed map[string]string) *McpManager {
	return &McpManager{servers: servers, clients: clients, trusted: trusted, failed: failed}
}

// lookup resolves a legacy raw name only when it has one owner and cannot
// shadow a built-in or a qualified catalog name.
func (m *McpManager) lookup(name string) (server string, client *Client, tool Tool, ok bool) {
	if m == nil || reservedToolName(name) || name == "" {
		return "", nil, Tool{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for serverName, client := range m.clients {
		if client == nil {
			continue
		}
		for _, candidate := range client.Tools {
			if qualifiedToolName(serverName, candidate.Name) == name {
				return "", nil, Tool{}, false
			}
			if candidate.Name == name {
				if ok {
					return "", nil, Tool{}, false
				}
				server, tool, ok = serverName, candidate, true
			}
		}
	}
	return server, m.clients[server], tool, ok
}

// Tools preserves the legacy raw-name model surface for existing callers.
// Ambiguous names and built-in collisions are omitted rather than dispatched
// arbitrarily. New registry callers use CatalogTools for qualified identities.
func (m *McpManager) Tools(userAgent string) []model.ToolDefinition {
	var tools []model.ToolDefinition
	for _, tool := range m.CatalogTools(userAgent) {
		if len(tool.Aliases) == 0 {
			continue
		}
		definition := tool.Definition
		definition.Name = tool.Aliases[0]
		tools = append(tools, definition)
	}
	return tools
}

// Call preserves legacy raw-name dispatch through the common server trust
// adapter. The bool is MCP's tool-level isError flag, not the trust state.
func (m *McpManager) Call(ctx context.Context, name string, arguments json.RawMessage, approve func(server, tool string, arguments string) bool) (string, bool, error) {
	server, _, tool, ok := m.lookup(name)
	if !ok {
		return "", false, fmt.Errorf("unsupported tool %q", name)
	}
	if err := m.AuthorizeTool(ctx, server, tool.Name, string(arguments), approve); err != nil {
		return "", false, err
	}
	return m.CallTool(ctx, server, tool.Name, arguments)
}

// Status renders the /mcp view: each server, its state, and its tools, plus
// where to edit the configuration.
func (m *McpManager) Status() string {
	if m == nil {
		return "No MCP servers configured; mcp.json unavailable."
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	if len(m.servers) == 0 {
		fmt.Fprintf(&b, "No MCP servers configured. Edit %s (same shape as Claude Desktop: {\"mcpServers\": {\"name\": {\"command\": ..., \"args\": [...], \"env\": {...}}}}); new servers appear on next launch.", m.path)
		return b.String()
	}
	fmt.Fprintf(&b, "Configured MCP servers (edit %s):", m.path)
	for name := range m.servers {
		switch {
		case m.failed[name] != "":
			fmt.Fprintf(&b, "\n- %s: crashed (%s)", name, m.failed[name])
		case m.clients[name] != nil:
			fmt.Fprintf(&b, "\n- %s: running", name)
			for _, tool := range m.clients[name].Tools {
				fmt.Fprintf(&b, "\n    tool: %s", tool.Name)
			}
		default:
			fmt.Fprintf(&b, "\n- %s: not started (starts on first prompt)", name)
		}
	}
	return b.String()
}

// Summary condenses manager state to one status-line segment: "mcp 2/3"
// (running/configured), with " crashed" appended when any configured server
// failed to start. With no servers configured it returns "" so the segment
// hides.
func (m *McpManager) Summary() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	configured := len(m.servers)
	if configured == 0 {
		return ""
	}
	running, crashed := 0, 0
	for name := range m.servers {
		if m.clients[name] != nil {
			running++
		}
		if _, failed := m.failed[name]; failed {
			crashed++
		}
	}
	summary := fmt.Sprintf("mcp %d/%d", running, configured)
	if crashed > 0 {
		summary += " crashed"
	}
	return summary
}

// Trusted reports whether a server has been approved for this session.
func (m *McpManager) Trusted(name string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.trusted[name]
}

// Stop kills every running server and fails pending calls. Called on exit;
// servers must not outlive Likha.
func (m *McpManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			m.failed[name] = err.Error()
		}
	}
}
