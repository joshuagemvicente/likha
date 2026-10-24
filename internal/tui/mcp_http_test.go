package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/mcp"
	"likha/internal/providers"
	"likha/internal/session"
)

const tuiMcpToken = "tui-mcp-secret-0123456789"

// startHTTPMcpServer serves the MCP handshake and an "echo" tool over
// Streamable HTTP with JSON responses, refusing requests without the token.
func startHTTPMcpServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+tuiMcpToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&message) != nil || len(message.ID) == 0 {
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
			result = `{"content":[{"type":"text","text":"echoed"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, message.ID, result)
	}))
	t.Cleanup(server.Close)
	return server
}

func httpMcpStateDir(t *testing.T, url string) string {
	t.Helper()
	t.Setenv("LIKHA_TEST_TUI_MCP_TOKEN", tuiMcpToken)
	stateDir := t.TempDir()
	config := fmt.Sprintf(`{"mcpServers":{"docs":{"type":"http","url":%q,"headers":{"Authorization":"Bearer ${LIKHA_TEST_TUI_MCP_TOKEN}"}}}}`, url+"/mcp?key=private")
	if err := os.WriteFile(stateDir+"/mcp.json", []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return stateDir
}

func TestMcpHTTPStatusSurfacesInTUI(t *testing.T) {
	server := startHTTPMcpServer(t)
	stateDir := httpMcpStateDir(t, server.URL)
	manager, err := mcp.NewMcpManager(stateDir, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	if tools := manager.Tools("likha/test"); len(tools) != 1 {
		t.Fatalf("server did not start: %+v (%s)", tools, manager.Status())
	}

	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Mcp: manager}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/mcp")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	status := m.entries[len(m.entries)-1].content
	if !strings.Contains(status, "docs (http "+server.URL+"): running") || !strings.Contains(status, "tool: echo") {
		t.Fatalf("/mcp status wrong: %q", status)
	}
	view := stripANSI(m.View())
	for _, private := range []string{tuiMcpToken, "key=private", server.URL + "/mcp"} {
		if strings.Contains(status, private) || strings.Contains(view, private) {
			t.Fatalf("/mcp or the view shows %q:\n%s\n%s", private, status, view)
		}
	}
}

func TestHTTPMcpReviewFitsNarrowTerminals(t *testing.T) {
	body := "MCP server: docs\nRemote MCP server: https://mcp.example.com (the arguments are sent over the network)\nTool: echo\nArguments: {}"
	for _, width := range []int{40, 48, 80} {
		m := newKeysTestUI(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m.working, m.runID = true, 1
		m.events = make(chan agent.TurnEvent, 1)
		request := &agent.ApprovalRequest{Kind: "mcp", Title: "MCP tool", Body: body, Remember: agent.RememberServer, Reply: make(chan bool, 1)}
		m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
		view := stripANSI(m.View())
		if !strings.Contains(view, "Remote MCP server") {
			t.Fatalf("%d cols: origin line missing:\n%s", width, view)
		}
		assertViewport(t, m.View(), width, 24)
	}
}

func TestHTTPMcpTrustClearsOnSessionSwitch(t *testing.T) {
	server := startHTTPMcpServer(t)
	stateDir := httpMcpStateDir(t, server.URL)
	manager, err := mcp.NewMcpManager(stateDir, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	manager.Tools("likha/test")
	always := func(string, string, string) mcp.Decision { return mcp.ApprovedAlways }
	if err := manager.AuthorizeTool(context.Background(), "docs", "echo", "{}", always); err != nil || !manager.Trusted("docs") {
		t.Fatalf("Approve always did not trust the HTTP server: %v", err)
	}
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Mcp: manager}, stateDir, store, first)
	m.resumeSession(next.ID)
	if manager.Trusted("docs") {
		t.Fatal("HTTP MCP server trust carried into another session")
	}
}
