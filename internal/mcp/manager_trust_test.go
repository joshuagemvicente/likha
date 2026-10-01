package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The fake-server harness lives in mcp_test.go's TestMain (LISA_FAKE_MCP);
// these tests exercise the manager against it.
func writeTestMcpConfig(t *testing.T, stateDir string) error {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return os.WriteFile(stateDir+"/mcp.json", []byte(`{"mcpServers":{"fake":{"command":"`+exe+`","env":{"LISA_FAKE_MCP":"1"}}}}`), 0600)
}
func TestMcpTrustGateAndStatus(t *testing.T) {
	stateDir := t.TempDir()
	if err := writeTestMcpConfig(t, stateDir); err != nil {
		t.Fatal(err)
	}
	manager, err := NewMcpManager(stateDir, "lisa/test")
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
	if err != nil || !strings.Contains(result, `{"x":2}`) {
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
}

func TestMcpCrashSurfacesAsFailure(t *testing.T) {
	// A server whose command exits immediately.
	manager := NewMcpManagerForTest(
		map[string]ServerConfig{"dead": {Command: "sh", Args: []string{"-c", "exit 0"}}},
		map[string]*Client{}, map[string]bool{}, map[string]string{},
	)
	if tools := manager.Tools("lisa/test"); len(tools) != 0 {
		t.Fatalf("crashed server contributed tools: %+v", tools)
	}
	if !strings.Contains(manager.Status(), "crashed") {
		t.Fatalf("status missing crash: %q", manager.Status())
	}
}

func TestMcpUnknownToolWithoutManager(t *testing.T) {
	var manager *McpManager
	_, _, err := manager.Call(context.Background(), "anything", json.RawMessage(`{}`), func(string, string, string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "unsupported tool") {
		t.Fatalf("nil manager error = %v", err)
	}
}

func TestMcpSummaryStates(t *testing.T) {
	var nilManager *McpManager
	if got := nilManager.Summary(); got != "" {
		t.Fatalf("nil manager summary = %q", got)
	}
	empty := NewMcpManagerForTest(map[string]ServerConfig{}, map[string]*Client{}, map[string]bool{}, map[string]string{})
	if got := empty.Summary(); got != "" {
		t.Fatalf("unconfigured manager summary = %q", got)
	}
	servers := map[string]ServerConfig{"a": {Command: "a"}, "b": {Command: "b"}}
	noneStarted := NewMcpManagerForTest(servers, map[string]*Client{}, map[string]bool{}, map[string]string{})
	if got := noneStarted.Summary(); got != "mcp 0/2" {
		t.Fatalf("none started summary = %q", got)
	}
	oneRunning := NewMcpManagerForTest(servers, map[string]*Client{"a": {}}, map[string]bool{}, map[string]string{})
	if got := oneRunning.Summary(); got != "mcp 1/2" {
		t.Fatalf("one running summary = %q", got)
	}
	oneCrashed := NewMcpManagerForTest(servers, map[string]*Client{"a": {}}, map[string]bool{}, map[string]string{"b": "exit status 1"})
	if got := oneCrashed.Summary(); got != "mcp 1/2 crashed" {
		t.Fatalf("one crashed summary = %q", got)
	}
	allCrashed := NewMcpManagerForTest(servers, map[string]*Client{}, map[string]bool{}, map[string]string{"a": "exit status 1", "b": "exit status 1"})
	if got := allCrashed.Summary(); got != "mcp 0/2 crashed" {
		t.Fatalf("all crashed summary = %q", got)
	}
}
