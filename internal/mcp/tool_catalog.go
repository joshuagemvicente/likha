package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"likha/internal/model"
)

// CatalogTool preserves the exact MCP identity alongside its provider-safe name.
// Aliases contain only unambiguous legacy names outside the built-in namespace.
type CatalogTool struct {
	Definition   model.ToolDefinition
	SourceServer string
	OriginalName string
	Aliases      []string
}

// CatalogTools starts the configured stdio servers using the existing discovery
// handshake. Discovery runs before TOFU approval: it is not a process sandbox.
// Duplicate server/tool identities and qualified-name collisions are omitted,
// never resolved by map iteration order. Schemas retain their original bytes;
// the registry is responsible for schema validation and run/agent eligibility.
func (m *McpManager) CatalogTools(userAgent string) []CatalogTool {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clients == nil {
		m.clients = make(map[string]*Client)
	}
	if m.failed == nil {
		m.failed = make(map[string]string)
	}
	servers := make([]string, 0, len(m.servers))
	for server := range m.servers {
		servers = append(servers, server)
	}
	sort.Strings(servers)
	for _, server := range servers {
		if m.clients[server] != nil {
			continue
		}
		if _, failed := m.failed[server]; failed {
			continue // relaunch-only restart, as in the legacy discovery path
		}
		client, err := Start(server, m.servers[server], userAgent)
		if err != nil {
			m.failed[server] = err.Error()
			continue
		}
		m.clients[server] = client
	}
	return m.catalogToolsLocked(servers)
}

// catalogToolsLocked does not start servers or acquire the manager mutex.
func (m *McpManager) catalogToolsLocked(servers []string) []CatalogTool {
	var candidates []CatalogTool
	rawCounts := make(map[string]int)
	qualifiedCounts := make(map[string]int)
	for _, server := range servers {
		client := m.clients[server]
		if client == nil {
			continue
		}
		tools := append([]Tool(nil), client.Tools...)
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		for _, tool := range tools {
			rawCounts[tool.Name]++
			name := qualifiedToolName(server, tool.Name)
			qualifiedCounts[name]++
			candidates = append(candidates, CatalogTool{
				Definition: model.ToolDefinition{
					Name:        name,
					Description: fmt.Sprintf("MCP tool from server %q. %s", server, tool.Description),
					Parameters:  append(json.RawMessage(nil), tool.InputSchema...),
				},
				SourceServer: server,
				OriginalName: tool.Name,
			})
		}
	}
	var catalog []CatalogTool
	for _, candidate := range candidates {
		if candidate.OriginalName == "" || qualifiedCounts[candidate.Definition.Name] != 1 {
			continue
		}
		if rawCounts[candidate.OriginalName] == 1 && !reservedToolName(candidate.OriginalName) && qualifiedCounts[candidate.OriginalName] == 0 {
			candidate.Aliases = []string{candidate.OriginalName}
		}
		catalog = append(catalog, candidate)
	}
	return catalog
}

// qualifiedToolName is independent of discovery order and of other servers.
// Length-delimited original bytes distinguish punctuation, Unicode, truncation,
// and separator lookalikes; the catalog still rejects any digest collision.
func qualifiedToolName(server, tool string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%d:%s", len(server), server, len(tool), tool)))
	return fmt.Sprintf("mcp__%s__%s__%x", toolNamePart(server, "server", 12), toolNamePart(tool, "tool", 19), digest[:12])
}

func toolNamePart(value, fallback string, limit int) string {
	var b strings.Builder
	for _, char := range value {
		if b.Len() == limit {
			break
		}
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			b.WriteByte(byte(char))
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

func reservedToolName(name string) bool {
	switch name {
	case "glob", "read", "grep", "edit_file", "edit", "run_command", "read_output",
		"task", "ask_user", "plan_update", "skill", "web_search", "web_fetch":
		return true
	default:
		return false
	}
}

// lookupToolLocked requires one exact tool/schema registration on the named
// server. Even identical duplicate definitions are ambiguous and refused.
func (m *McpManager) lookupToolLocked(server, name string) (*Client, Tool, error) {
	client := m.clients[server]
	if client == nil || name == "" {
		return nil, Tool{}, fmt.Errorf("unsupported MCP tool %q from server %q", name, server)
	}
	var found Tool
	count := 0
	for _, tool := range client.Tools {
		if tool.Name == name {
			found = tool
			count++
		}
	}
	if count > 1 {
		return nil, Tool{}, fmt.Errorf("ambiguous MCP tool %q from server %q: duplicate registrations", name, server)
	}
	if count == 0 {
		return nil, Tool{}, fmt.Errorf("unsupported MCP tool %q from server %q", name, server)
	}
	client.mu.Lock()
	closed := client.closed
	client.mu.Unlock()
	if closed {
		return nil, Tool{}, fmt.Errorf("MCP server %q is stopped", server)
	}
	return client, found, nil
}

// Decision is the user's answer to a first-call MCP review
// (specs/approve-always).
type Decision int

const (
	Declined       Decision = iota
	ApprovedOnce            // run this call; the server stays untrusted
	ApprovedAlways          // run this call and trust the server for the session
)

// ResetTrust forgets every server trust and single-call approval (session
// switch, deletion of the active session). Safe on a nil manager.
func (m *McpManager) ResetTrust() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trusted = make(map[string]bool)
	m.once = nil
}

// onceKey identifies one single-use approval: one exact (server, tool).
func onceKey(server, tool string) string { return server + "\x00" + tool }

// AuthorizeTool consults the session's server trust. An untrusted server
// asks; ApprovedAlways trusts the server for the session, ApprovedOnce
// grants one call of this exact tool that CallTool consumes. The callback
// sees original identities and runs outside the manager mutex. Cancellation
// while awaiting approval never creates a late grant.
func (m *McpManager) AuthorizeTool(ctx context.Context, server, tool, arguments string, approve func(server, tool, arguments string) Decision) error {
	if m == nil {
		return fmt.Errorf("unsupported MCP tool %q from server %q", tool, server)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	_, _, err := m.lookupToolLocked(server, tool)
	trusted := m.trusted[server]
	// A stale single-use approval never carries into a new review.
	delete(m.once, onceKey(server, tool))
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if trusted {
		return nil
	}
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		arguments = "{}"
	}
	decision := Declined
	if approve != nil {
		decision = approve(server, tool, arguments)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if decision != ApprovedOnce && decision != ApprovedAlways {
		return fmt.Errorf("MCP tool %q from server %q rejected by user", tool, server)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, _, err := m.lookupToolLocked(server, tool); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if decision == ApprovedOnce {
		if m.once == nil {
			m.once = make(map[string]bool)
		}
		m.once[onceKey(server, tool)] = true
		return nil
	}
	if m.trusted == nil {
		m.trusted = make(map[string]bool)
	}
	m.trusted[server] = true
	return nil
}

// CallTool dispatches only an exact discovered identity on an approved server
// or with a pending single-use approval; it cannot grant trust. isError retains MCP's tool-level error flag. The 60 s
// deadline bounds local waiting, not remote effects: cancellation does not undo
// server work or promise that it stops.
func (m *McpManager) CallTool(ctx context.Context, server, tool string, args json.RawMessage) (string, bool, error) {
	if m == nil {
		return "", false, fmt.Errorf("unsupported MCP tool %q from server %q", tool, server)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	m.mu.Lock()
	client, definition, err := m.lookupToolLocked(server, tool)
	trusted := m.trusted[server]
	if err == nil && !trusted && m.once[onceKey(server, tool)] {
		// One approved call: consumed before it runs, so it never repeats.
		delete(m.once, onceKey(server, tool))
		trusted = true
	}
	m.mu.Unlock()
	if err != nil {
		return "", false, err
	}
	if !trusted {
		return "", false, fmt.Errorf("MCP server %q is not approved for this session", server)
	}
	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	if err := callCtx.Err(); err != nil {
		return "", false, err
	}
	return client.Call(callCtx, definition.Name, args)
}
