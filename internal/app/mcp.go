package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"lisa/internal/mcp"
	"lisa/internal/model"
)

// mcpManager owns the user's configured MCP servers: lazy startup, the merged
// tool surface handed to the model, trust-on-first-use tracking, and cleanup.
// A nil manager means the feature is unused and everything is inert.
type mcpManager struct {
	mu      sync.Mutex
	path    string
	servers map[string]mcp.ServerConfig
	clients map[string]*mcp.Client
	trusted map[string]bool // server name → approved for the session
	failed  map[string]string
}

func newMcpManager(stateDir, userAgent string) (*mcpManager, error) {
	path, err := mcp.EnsureSkeleton(stateDir)
	if err != nil {
		return nil, err
	}
	servers, err := mcp.LoadConfig(stateDir)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return &mcpManager{path: path, clients: map[string]*mcp.Client{}, trusted: map[string]bool{}, failed: map[string]string{}}, nil
	}
	return &mcpManager{
		path:    path,
		servers: servers,
		clients: map[string]*mcp.Client{},
		trusted: map[string]bool{},
		failed:  map[string]string{},
	}, nil
}

// ToolDefinition is the model-facing shape of one MCP tool.
type toolDefinition = model.ToolDefinition

// toolSource maps a model-facing tool name to its owning server.
func (m *mcpManager) lookup(name string) (server string, client *mcp.Client, tool mcp.Tool, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for serverName, client := range m.clients {
		for _, tool := range client.Tools {
			if tool.Name == name {
				return serverName, client, tool, true
			}
		}
	}
	return "", nil, mcp.Tool{}, false
}

// Tools returns every MCP tool as model definitions, starting servers lazily
// on first use. Servers that fail to start are recorded and skipped.
func (m *mcpManager) Tools(userAgent string) []model.ToolDefinition {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var tools []model.ToolDefinition
	for name, cfg := range m.servers {
		client, running := m.clients[name]
		if !running {
			if reason, crashed := m.failed[name]; crashed {
				_ = reason // stays dead for the session (relaunch-only restart)
				continue
			}
			started, err := mcp.Start(name, cfg, userAgent)
			if err != nil {
				m.failed[name] = err.Error()
				continue
			}
			m.clients[name] = started
			client = started
		}
		for _, tool := range client.Tools {
			tools = append(tools, model.ToolDefinition{
				Name:        tool.Name,
				Description: fmt.Sprintf("MCP tool from server %q. %s", name, tool.Description),
				Parameters:  tool.InputSchema,
			})
		}
	}
	return tools
}

// Call executes one MCP tool with a 60 s deadline. trusted reports whether
// the server was already approved this session; approve is the callback the
// TUI uses to surface the trust-on-first-use prompt.
func (m *mcpManager) Call(ctx context.Context, name string, arguments json.RawMessage, approve func(server, tool string, arguments string) bool) (string, bool, error) {
	if m == nil {
		return "", false, fmt.Errorf("unsupported tool %q", name)
	}
	server, client, tool, ok := m.lookup(name)
	if !ok {
		return "", false, fmt.Errorf("unsupported tool %q", name)
	}
	m.mu.Lock()
	trusted := m.trusted[server]
	m.mu.Unlock()
	if !trusted {
		args := strings.TrimSpace(string(arguments))
		if args == "" {
			args = "{}"
		}
		if !approve(server, tool.Name, args) {
			return "", false, fmt.Errorf("MCP tool %q from server %q rejected by user", tool.Name, server)
		}
		m.mu.Lock()
		m.trusted[server] = true
		m.mu.Unlock()
	}
	callCtx, cancel := context.WithTimeout(ctx, mcp.CallTimeout)
	defer cancel()
	text, isError, err := client.Call(callCtx, tool.Name, arguments)
	if err != nil {
		return "", false, err
	}
	return text, isError, nil
}

// Status renders the /mcp view: each server, its state, and its tools, plus
// where to edit the configuration.
func (m *mcpManager) Status() string {
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
func (m *mcpManager) Summary() string {
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
func (m *mcpManager) Trusted(name string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.trusted[name]
}

// Stop kills every running server and fails pending calls. Called on exit;
// servers must not outlive Lisa.
func (m *mcpManager) Stop() {
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
