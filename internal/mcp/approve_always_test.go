package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func startedTestManager(t *testing.T) *McpManager {
	t.Helper()
	stateDir := t.TempDir()
	if err := writeTestMcpConfig(t, stateDir); err != nil {
		t.Fatal(err)
	}
	manager, err := NewMcpManager(stateDir, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	manager.Tools("likha/test") // starts the fake server
	return manager
}

func TestMcpApprovedOnceRunsOneCall(t *testing.T) {
	manager := startedTestManager(t)
	ctx := context.Background()
	prompts := 0
	once := func(string, string, string) Decision { prompts++; return ApprovedOnce }
	if err := manager.AuthorizeTool(ctx, "fake", "echo", `{"x":1}`, once); err != nil {
		t.Fatal(err)
	}
	if manager.Trusted("fake") {
		t.Fatal("Approve (once) trusted the server")
	}
	if result, _, err := manager.CallTool(ctx, "fake", "echo", json.RawMessage(`{"x":1}`)); err != nil || !strings.Contains(result, `{"x":1}`) {
		t.Fatalf("approved-once call: %q %v", result, err)
	}
	if _, _, err := manager.CallTool(ctx, "fake", "echo", json.RawMessage(`{"x":2}`)); err == nil {
		t.Fatal("a single-use approval ran twice")
	}
	if err := manager.AuthorizeTool(ctx, "fake", "echo", `{"x":3}`, once); err != nil || prompts != 2 {
		t.Fatalf("the next call did not ask again: prompts=%d err=%v", prompts, err)
	}
}

func TestMcpApprovedAlwaysTrustsUntilReset(t *testing.T) {
	manager := startedTestManager(t)
	ctx := context.Background()
	prompts := 0
	always := func(string, string, string) Decision { prompts++; return ApprovedAlways }
	for range 2 {
		if err := manager.AuthorizeTool(ctx, "fake", "echo", "{}", always); err != nil {
			t.Fatal(err)
		}
	}
	if prompts != 1 || !manager.Trusted("fake") {
		t.Fatalf("Approve always: prompts=%d trusted=%t", prompts, manager.Trusted("fake"))
	}
	manager.ResetTrust()
	if manager.Trusted("fake") {
		t.Fatal("ResetTrust kept the server trusted")
	}
	if err := manager.AuthorizeTool(ctx, "fake", "echo", "{}", always); err != nil || prompts != 2 {
		t.Fatalf("after ResetTrust: prompts=%d err=%v", prompts, err)
	}
	var nilManager *McpManager
	nilManager.ResetTrust()
}

func TestMcpResetTrustClearsSingleUseApproval(t *testing.T) {
	manager := startedTestManager(t)
	ctx := context.Background()
	if err := manager.AuthorizeTool(ctx, "fake", "echo", "{}", func(string, string, string) Decision { return ApprovedOnce }); err != nil {
		t.Fatal(err)
	}
	manager.ResetTrust()
	if _, _, err := manager.CallTool(ctx, "fake", "echo", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a single-use approval survived ResetTrust")
	}
}

func TestMcpDeclineAndCancelGrantNothing(t *testing.T) {
	manager := startedTestManager(t)
	if err := manager.AuthorizeTool(context.Background(), "fake", "echo", "{}", func(string, string, string) Decision { return Declined }); err == nil || !strings.Contains(err.Error(), "rejected by user") {
		t.Fatalf("decline err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := manager.AuthorizeTool(ctx, "fake", "echo", "{}", func(string, string, string) Decision {
		cancel()
		return ApprovedAlways
	})
	if err == nil || manager.Trusted("fake") {
		t.Fatalf("cancelled review: err=%v trusted=%t", err, manager.Trusted("fake"))
	}
	if _, _, err := manager.CallTool(context.Background(), "fake", "echo", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a cancelled review left a runnable call")
	}
}
