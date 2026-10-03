package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/mcp"
	"likha/internal/providers"
	"likha/internal/session"
)

// TestMain turns the test binary into the fake MCP server on request, so the
// /mcp TUI surface test can launch a real stdio server cheaply. The same
// hook lives in the mcp package's own tests; each test binary needs its own.
func TestMain(m *testing.M) {
	if os.Getenv("LIKHA_FAKE_MCP") == "1" {
		runAppFakeMcpServe()
		return
	}
	os.Exit(m.Run())
}

func TestMcpStatusSurfacesInTUI(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(stateDir+"/mcp.json", []byte(`{"mcpServers":{"fake":{"command":"`+os.Args[0]+`","env":{"LIKHA_FAKE_MCP":"1"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := mcp.NewMcpManager(stateDir, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	// Start the server eagerly: Status reports stored state, so a never-
	// started server would render "not started" instead of running.
	if tools := manager.Tools("likha/test"); len(tools) != 1 {
		t.Fatalf("server did not start: %+v", tools)
	}

	// /mcp surfaces the running server and its tool through the TUI.
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Mcp: manager}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/mcp")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	lastEntry := m.entries[len(m.entries)-1]
	if !strings.Contains(lastEntry.content, "fake: running") || !strings.Contains(lastEntry.content, "tool: echo") {
		t.Fatalf("/mcp status wrong: %q", lastEntry.content)
	}
}

// runAppFakeMcpServe answers the MCP handshake and an "echo" tool over stdio
// using newline-delimited JSON-RPC. Duplicate of the mcp package harness:
// each test binary re-execs itself, so each needs its own responder.
func runAppFakeMcpServe() {
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
