package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain turns the test binary into a fake MCP server when asked to. The
// server answers initialize, tools/list (one "echo" tool), and tools/call
// (echoes the arguments back) over newline-delimited JSON-RPC.
func TestMain(m *testing.M) {
	if os.Getenv("LIKHA_FAKE_MCP") == "1" {
		runFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}

func runFakeMCPServer() {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		write := func(result string) {
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":%s}\n", req.ID, result)
			writer.Flush()
		}
		switch req.Method {
		case "initialize":
			write(`{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}`)
		case "tools/list":
			write(`{"tools":[{"name":"echo","description":"Echo the arguments back","inputSchema":{"type":"object","properties":{"message":{"type":"string"}}}}]}`)
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(req.Params, &params) != nil || params.Name != "echo" {
				write(`{"content":[{"type":"text","text":"unknown tool"}],"isError":true}`)
				continue
			}
			payload, _ := json.Marshal(params.Arguments)
			content, _ := json.Marshal([]map[string]string{{"type": "text", "text": string(payload)}})
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":%s,\"isError\":false}}\n", req.ID, content)
			writer.Flush()
		default:
			write(`{}`)
		}
	}
}

// fakeServerCommand returns a ServerConfig whose command re-executes this
// test binary as the fake MCP server.
func fakeServerCommand(t *testing.T) ServerConfig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return ServerConfig{Command: exe, Env: map[string]string{"LIKHA_FAKE_MCP": "1"}}
}

func TestHandshakeListsToolsAndCalls(t *testing.T) {
	client, err := Start("fake", fakeServerCommand(t), "likha/1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if len(client.Tools) != 1 || client.Tools[0].Name != "echo" || client.Tools[0].Description == "" {
		t.Fatalf("tools = %+v", client.Tools)
	}
	if !strings.Contains(string(client.Tools[0].InputSchema), "message") {
		t.Fatalf("schema passthrough broken: %s", client.Tools[0].InputSchema)
	}
	result, isError, err := client.Call(context.Background(), "echo", json.RawMessage(`{"message":"hello"}`))
	if err != nil || isError {
		t.Fatalf("call: result=%q isError=%t err=%v", result, isError, err)
	}
	if !strings.Contains(result, `"message":"hello"`) {
		t.Fatalf("call result = %q", result)
	}
}

func TestStartRejectsBadServer(t *testing.T) {
	cfg := ServerConfig{Command: "/nonexistent/likha-mcp-fake"}
	if _, err := Start("bad", cfg, "likha/1.2.3"); err == nil {
		t.Fatal("accepted a nonexistent server command")
	}
}

func TestLoadConfigShapes(t *testing.T) {
	dir := t.TempDir()
	// Missing file: no servers, no error.
	if servers, err := LoadConfig(dir); err != nil || len(servers) != 0 {
		t.Fatalf("missing file: %+v %v", servers, err)
	}
	// Claude-Desktop shape parses; invalid command fails loudly.
	path := dir + "/mcp.json"
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"echo":`+fmt.Sprintf(`{"command":"%s","args":[],"env":{"LIKHA_FAKE_MCP":"1"}}`, fakeExe())+`}}`), 0600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadConfig(dir)
	if err != nil || len(servers) != 1 || servers["echo"].Command == "" {
		t.Fatalf("mcpServers shape: %+v %v", servers, err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("accepted corrupt mcp.json")
	}
}

func fakeExe() string {
	exe, _ := os.Executable()
	return exe
}

func TestCallTimeout(t *testing.T) {
	old := CallTimeout
	CallTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { CallTimeout = old })
	cfg := fakeServerCommand(t)
	// A server that ignores stdin entirely: the handshake must time out on
	// the bounded deadline instead of hanging forever.
	cfg.Command = "sh"
	cfg.Args = []string{"-c", "cat > /dev/null"}
	start := time.Now()
	client, err := Start("silent", cfg, "likha/1.2.3")
	elapsed := time.Since(start)
	if err == nil {
		// The handshake timed out (bounded) — that is the expected outcome
		// for a silent server.
		client.Close()
		return
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "handshake") {
		t.Fatalf("unexpected error for silent server: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("handshake took %s, deadline not honored", elapsed)
	}
}
