package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/mcp"
	"lisa/internal/session"
)

// TestMain turns the test binary into the fake MCP server on request, so the
// manager tests can launch a real stdio server cheaply.
func TestMain(m *testing.M) {
	if os.Getenv("LISA_FAKE_MCP") == "1" {
		fakeMcpServe()
		return
	}
	os.Exit(m.Run())
}

// fakeMcpServe answers the MCP handshake and an "echo" tool over stdio using
// newline-delimited JSON-RPC.
func fakeMcpServe() {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		write := func(result string) {
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":%s}\n", req.ID, result)
			writer.Flush()
		}
		switch req.Method {
		case "initialize":
			write(`{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}`)
		case "tools/list":
			write(`{"tools":[{"name":"echo","description":"Echo back","inputSchema":{"type":"object"}}]}`)
		case "tools/call":
			var params struct {
				Arguments json.RawMessage `json:"arguments"`
			}
			json.Unmarshal(req.Params, &params)
			content, _ := json.Marshal([]map[string]string{{"type": "text", "text": "echo:" + string(params.Arguments)}})
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":%s}}\n", req.ID, content)
			writer.Flush()
		}
	}
}

func TestMcpTrustGateAndStatus(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(stateDir+"/mcp.json", []byte(`{"mcpServers":{"fake":{"command":"`+os.Args[0]+`","env":{"LISA_FAKE_MCP":"1"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := newMcpManager(stateDir, "lisa/test")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	// The server's tool joins the built-in set.
	tools := manager.Tools("lisa/test")
	found := false
	for _, tool := range tools {
		if tool.Name == "echo" && strings.Contains(tool.Description, "fake") {
			found = true
		}
	}
	if !found {
		t.Fatalf("mcp tool missing from tool surface: %+v", tools)
	}

	// First call triggers the trust prompt. Rejection: no side effect, no trust.
	rejected := false
	result, _, err := manager.Call(context.Background(), "echo", json.RawMessage(`{"x":1}`), func(server, tool, arguments string) bool {
		rejected = true
		if server != "fake" || tool != "echo" || !strings.Contains(arguments, `"x":1`) {
			t.Fatalf("trust prompt identity wrong: %s %s %s", server, tool, arguments)
		}
		return false
	})
	if !rejected || err == nil || !strings.Contains(err.Error(), "rejected by user") {
		t.Fatalf("rejection path: result=%q err=%v", result, err)
	}
	if manager.Trusted("fake") {
		t.Fatal("rejection granted trust")
	}

	// Approval: the call runs; the second call runs without a prompt.
	var prompted int
	result, _, err = manager.Call(context.Background(), "echo", json.RawMessage(`{"x":2}`), func(string, string, string) bool {
		prompted++
		return true
	})
	if err != nil || !strings.Contains(result, `echo:{"x":2}`) {
		t.Fatalf("approved call: result=%q err=%v", result, err)
	}
	if !manager.Trusted("fake") || prompted != 1 {
		t.Fatalf("trust not recorded: trusted=%t prompted=%d", manager.Trusted("fake"), prompted)
	}
	if _, _, err := manager.Call(context.Background(), "echo", json.RawMessage(`{"x":3}`), func(string, string, string) bool {
		prompted++
		return true
	}); err != nil {
		t.Fatalf("trusted call failed: %v", err)
	}
	if prompted != 1 {
		t.Fatalf("trusted server prompted again: %d", prompted)
	}

	// /mcp surfaces the running server and its tool through the TUI.
	m := newUI("/sample", nil, nil, "", connection{provider: "OpenAI", verified: true, mcp: manager}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/mcp")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	lastEntry := m.entries[len(m.entries)-1]
	if !strings.Contains(lastEntry.content, "fake: running") || !strings.Contains(lastEntry.content, "tool: echo") {
		t.Fatalf("/mcp status wrong: %q", lastEntry.content)
	}
}

func TestMcpCrashSurfacesAsFailure(t *testing.T) {
	// A server whose command exits immediately.
	cfg := mcp.ServerConfig{Command: "sh", Args: []string{"-c", "exit 0"}}
	manager := &mcpManager{servers: map[string]mcp.ServerConfig{"dead": cfg}, clients: map[string]*mcp.Client{}, trusted: map[string]bool{}, failed: map[string]string{}}
	if tools := manager.Tools("lisa/test"); len(tools) != 0 {
		t.Fatalf("crashed server contributed tools: %+v", tools)
	}
	if !strings.Contains(manager.Status(), "crashed") {
		t.Fatalf("status missing crash: %q", manager.Status())
	}
}

func TestMcpUnknownToolWithoutManager(t *testing.T) {
	var manager *mcpManager
	_, _, err := manager.Call(context.Background(), "anything", json.RawMessage(`{}`), func(string, string, string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "unsupported tool") {
		t.Fatalf("nil manager error = %v", err)
	}
}

func TestMcpSummaryStates(t *testing.T) {
	var nilManager *mcpManager
	if got := nilManager.Summary(); got != "" {
		t.Fatalf("nil manager summary = %q", got)
	}
	empty := &mcpManager{servers: map[string]mcp.ServerConfig{}, clients: map[string]*mcp.Client{}, trusted: map[string]bool{}, failed: map[string]string{}}
	if got := empty.Summary(); got != "" {
		t.Fatalf("unconfigured manager summary = %q", got)
	}
	noneStarted := &mcpManager{
		servers: map[string]mcp.ServerConfig{"a": {Command: "a"}, "b": {Command: "b"}},
		clients: map[string]*mcp.Client{}, trusted: map[string]bool{}, failed: map[string]string{},
	}
	if got := noneStarted.Summary(); got != "mcp 0/2" {
		t.Fatalf("none started summary = %q", got)
	}
	oneRunning := &mcpManager{
		servers: map[string]mcp.ServerConfig{"a": {Command: "a"}, "b": {Command: "b"}},
		clients: map[string]*mcp.Client{"a": {}}, trusted: map[string]bool{}, failed: map[string]string{},
	}
	if got := oneRunning.Summary(); got != "mcp 1/2" {
		t.Fatalf("one running summary = %q", got)
	}
	oneCrashed := &mcpManager{
		servers: map[string]mcp.ServerConfig{"a": {Command: "a"}, "b": {Command: "b"}},
		clients: map[string]*mcp.Client{"a": {}}, trusted: map[string]bool{}, failed: map[string]string{"b": "exit status 1"},
	}
	if got := oneCrashed.Summary(); got != "mcp 1/2 crashed" {
		t.Fatalf("one crashed summary = %q", got)
	}
	allCrashed := &mcpManager{
		servers: map[string]mcp.ServerConfig{"a": {Command: "a"}, "b": {Command: "b"}},
		clients: map[string]*mcp.Client{}, trusted: map[string]bool{}, failed: map[string]string{"a": "exit status 1", "b": "exit status 1"},
	}
	if got := allCrashed.Summary(); got != "mcp 0/2 crashed" {
		t.Fatalf("all crashed summary = %q", got)
	}
}
