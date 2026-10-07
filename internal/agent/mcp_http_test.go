package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"likha/internal/mcp"
	"likha/internal/model"
)

// httpMCPServer is a minimal Streamable HTTP MCP server (JSON responses)
// with one "echo" tool; it counts tools/call requests.
type httpMCPServer struct {
	mu    sync.Mutex
	calls int
}

func (s *httpMCPServer) toolCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *httpMCPServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil || len(message.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result := `{}`
	switch message.Method {
	case "initialize":
		result = `{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}`
	case "tools/list":
		result = `{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`
	case "tools/call":
		s.mu.Lock()
		s.calls++
		s.mu.Unlock()
		result = `{"content":[{"type":"text","text":"echoed"}]}`
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, message.ID, result)
}

func httpMCPManager(t *testing.T) (*mcp.McpManager, *httpMCPServer, string) {
	t.Helper()
	fake := &httpMCPServer{}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)
	manager := mcp.NewMcpManagerForTest(map[string]mcp.ServerConfig{"docs": {URL: server.URL + "/mcp?key=private"}}, map[string]*mcp.Client{}, map[string]bool{}, map[string]string{})
	t.Cleanup(manager.Stop)
	return manager, fake, server.URL
}

func canonicalMCPName(t *testing.T, manager *mcp.McpManager) string {
	t.Helper()
	catalog := manager.CatalogTools(model.UserAgent)
	if len(catalog) != 1 {
		t.Fatalf("catalog = %+v", catalog)
	}
	return catalog[0].Definition.Name
}

func TestHTTPMCPReviewShowsOriginOnly(t *testing.T) {
	manager, fake, origin := httpMCPManager(t)
	reviewer := &fakeReviewer{approve: true}
	registry, _ := newToolRegistry(nil, t.TempDir(), manager, RunOptions{}, reviewer.emit)
	result, err := registry.Invoke(context.Background(), mainScope(RunOptions{}.Scope), toolCall(canonicalMCPName(t, manager), `{}`))
	if err != nil || !strings.Contains(result.Content, "echoed") {
		t.Fatalf("approved HTTP MCP call: %q %v", result.Content, err)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("reviews = %d, want 1", len(reviewer.requests))
	}
	body := reviewer.requests[0].Body
	if !strings.Contains(body, "Remote MCP server: "+origin+" (the arguments are sent over the network)") {
		t.Fatalf("review body lacks the origin line: %q", body)
	}
	if strings.Contains(body, "/mcp") || strings.Contains(body, "key=private") {
		t.Fatalf("review body shows the URL path or query: %q", body)
	}
	if fake.toolCalls() != 1 {
		t.Fatalf("tools/call requests = %d", fake.toolCalls())
	}
}

func TestStdioMCPReviewHasNoOriginLine(t *testing.T) {
	manager := mcp.NewMcpManagerForTest(map[string]mcp.ServerConfig{"local": {Command: "npx"}}, nil, nil, nil)
	if got := manager.Origin("local"); got != "" {
		t.Fatalf("stdio Origin = %q, want empty so the review is unchanged", got)
	}
}

func TestPlanModeRefusesHTTPMCPWithoutARequest(t *testing.T) {
	manager, fake, _ := httpMCPManager(t)
	reviewer := &fakeReviewer{approve: true}
	registry, _ := newToolRegistry(nil, t.TempDir(), manager, RunOptions{PlanMode: true}, reviewer.emit)
	_, err := registry.Invoke(context.Background(), mainScope(RunOptions{}.Scope), toolCall(canonicalMCPName(t, manager), `{}`))
	if err == nil || !strings.Contains(err.Error(), "plan mode") {
		t.Fatalf("plan mode HTTP MCP call: err=%v", err)
	}
	if len(reviewer.requests) != 0 || fake.toolCalls() != 0 {
		t.Fatalf("plan mode opened %d reviews and sent %d tools/call requests", len(reviewer.requests), fake.toolCalls())
	}
}
