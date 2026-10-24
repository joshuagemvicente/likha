package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

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
	once    map[string]bool // onceKey(server, tool) → one approved call pending
	failed  map[string]string
	// starting holds one channel per server whose handshake is in flight,
	// closed when it ends; the handshake itself runs without mu.
	starting map[string]chan struct{}
	stopped  bool // Stop ran: no further starts, late clients are closed
	// lifetime bounds every server start; Stop cancels it so a handshake in
	// flight ends at once instead of holding its server for CallTimeout.
	// Server starts have no caller context of their own (CatalogTools is
	// called while building the tool list), so the manager owns this one.
	lifetime       context.Context
	cancelLifetime context.CancelFunc
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
	decide := func(server, tool, arguments string) Decision {
		if approve != nil && approve(server, tool, arguments) {
			return ApprovedAlways
		}
		return Declined
	}
	if err := m.AuthorizeTool(ctx, server, tool.Name, string(arguments), decide); err != nil {
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
		fmt.Fprintf(&b, "No MCP servers configured. Edit %s (same shape as Claude Desktop: {\"mcpServers\": {\"name\": {\"command\": ..., \"args\": [...], \"env\": {...}}}}, or for a Streamable HTTP server {\"name\": {\"url\": \"https://...\", \"headers\": {\"Authorization\": \"Bearer ${TOKEN}\"}}}); new servers appear on next launch.", m.path)
		return b.String()
	}
	fmt.Fprintf(&b, "Configured MCP servers (edit %s):", m.path)
	for name, cfg := range m.servers {
		label, failure := name, "crashed"
		if cfg.Remote() {
			// Only the origin: the path, query and header values stay private.
			label, failure = fmt.Sprintf("%s (http %s)", name, origin(cfg.URL)), "failed"
		}
		switch {
		case m.failed[name] != "":
			fmt.Fprintf(&b, "\n- %s: %s (%s)", label, failure, m.failed[name])
		case m.clients[name] != nil:
			fmt.Fprintf(&b, "\n- %s: running", label)
			for _, tool := range m.clients[name].Tools {
				fmt.Fprintf(&b, "\n    tool: %s", tool.Name)
			}
		default:
			fmt.Fprintf(&b, "\n- %s: not started (starts on first prompt)", label)
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

// Origin returns scheme://host[:port] for an HTTP server, or "" for a stdio
// or unknown server. Reviews show it so the user sees arguments leave the
// machine.
func (m *McpManager) Origin(server string) string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.servers[server]
	if !ok || !cfg.Remote() {
		return ""
	}
	return origin(cfg.URL)
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

// Stop kills every running server, ends any handshake in flight, and fails
// pending calls. Called on exit; servers must not outlive Likha.
//
// Servers close concurrently, one goroutine each, and Stop waits for all of
// them: a server that ignores SIGTERM takes stdioStopGrace to kill, so
// closing in turn would grow with the server count and outlast the exit
// grace, leaving later servers unreaped. A start in flight is cancelled and
// awaited too, bounded by startCleanupBound, so its cleanup runs before the
// process exits. mu is not held while waiting: finishStart needs it.
func (m *McpManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.stopped = true
	if m.cancelLifetime != nil {
		m.cancelLifetime()
	}
	clients := make(map[string]*Client, len(m.clients))
	for name, client := range m.clients {
		clients[name] = client
	}
	starting := make([]chan struct{}, 0, len(m.starting))
	for _, done := range m.starting {
		starting = append(starting, done)
	}
	m.mu.Unlock()

	type closeResult struct {
		name string
		err  error
	}
	results := make(chan closeResult, len(clients))
	var wg sync.WaitGroup
	for name, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- closeResult{name: name, err: client.Close()}
		}()
	}
	awaitStarts(starting, startCleanupBound())
	wg.Wait()
	close(results)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed == nil {
		m.failed = make(map[string]string)
	}
	for result := range results {
		if result.err != nil {
			m.failed[result.name] = result.err.Error()
		}
	}
}

// StopBound is the longest Stop waits. Servers close concurrently, each
// within stdioStopGrace (stdio) or closeTimeout (HTTP), while Stop awaits
// in-flight starts for at most startCleanupBound, which covers either. The
// app's shutdown grace must exceed it so a signalled exit still reaps every
// server.
func StopBound() time.Duration {
	return startCleanupBound()
}

// startCleanupBound is how long Stop waits for cancelled starts to clean up:
// a stdio server gets stdioStopGrace before its group is killed, and an HTTP
// session's DELETE is bounded by closeTimeout.
func startCleanupBound() time.Duration {
	return stdioStopGrace + closeTimeout
}

// awaitStarts waits until every in-flight start has finished, or bound has
// passed.
func awaitStarts(starting []chan struct{}, bound time.Duration) {
	if len(starting) == 0 {
		return
	}
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for _, done := range starting {
		select {
		case <-done:
		case <-deadline.C:
			return
		}
	}
}
