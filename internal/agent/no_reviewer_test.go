package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"likha/internal/actions"
	"likha/internal/mcp"
	"likha/internal/tools"
)

const headlessReason = "no reviewer is present in headless mode"

// refusingReviewer fails the test if any review event reaches it.
func refusingReviewer(t *testing.T) *fakeReviewer {
	t.Helper()
	return &fakeReviewer{approve: true}
}

func assertNoReviewerRefusal(t *testing.T, name string, result tools.Result, err error, reviewer *fakeReviewer, kind string) {
	t.Helper()
	if len(reviewer.requests) != 0 {
		t.Fatalf("%s: emitted %d approval events; a no-reviewer run must emit none", name, len(reviewer.requests))
	}
	if result.Status != tools.Refused {
		t.Fatalf("%s: status = %q, want refused", name, result.Status)
	}
	var refusal *NoReviewerError
	if !errors.As(err, &refusal) || refusal.Kind != kind {
		t.Fatalf("%s: err = %v, want a *NoReviewerError of kind %q", name, err, kind)
	}
	message := modelError(err)
	if !strings.Contains(message, headlessReason) || strings.Contains(message, "rejected by user") {
		t.Fatalf("%s: model sees %q; want the headless reason and no user rejection", name, message)
	}
}

func TestNoReviewerRefusesEdits(t *testing.T) {
	reviewer := refusingReviewer(t)
	registry, root := initRegistry(t, RunOptions{NoReviewer: headlessReason}, reviewer)

	result, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "new.txt", "hello\n")))
	assertNoReviewerRefusal(t, "edit_file", result, err, reviewer, "edit")
	if !strings.Contains(modelError(err), "Describe the change in your answer") {
		t.Fatalf("edit_file refusal does not ask for a description: %q", modelError(err))
	}
	if _, statErr := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("edit_file wrote a file without a reviewer: %v", statErr)
	}

	args, marshalErr := json.Marshal(map[string]any{"operations": []actions.EditOperation{{Kind: "create", Path: "batch.txt", Content: "x\n"}}})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	result, err = registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit", string(args)))
	assertNoReviewerRefusal(t, "edit", result, err, reviewer, "edit")
	if _, statErr := os.Stat(filepath.Join(root, "batch.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("edit wrote a file without a reviewer: %v", statErr)
	}
}

func TestNoReviewerKeepsSessionEditGrant(t *testing.T) {
	// A headless run never holds a grant, but a refusal must not depend on
	// that: the grant path skips the review and is the user's own decision.
	grant := &EditGrant{}
	grant.Allow()
	reviewer := refusingReviewer(t)
	registry, root := initRegistry(t, RunOptions{NoReviewer: headlessReason, EditGrant: grant}, reviewer)
	result, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "granted.txt", "ok\n")))
	if err != nil || result.Status != tools.Succeeded {
		t.Fatalf("granted edit: status=%q err=%v; a session grant still applies", result.Status, err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "granted.txt")); string(content) != "ok\n" {
		t.Fatalf("granted edit wrote %q", content)
	}
}

func TestNoReviewerCommandTiers(t *testing.T) {
	trusted := func(check, fingerprint string) bool { return true }
	cases := []struct {
		name     string
		command  string
		trusted  func(string, string) bool
		wantAuto bool
		wantTier string // empty when the command is not refused for lack of a reviewer
	}{
		{"read-only runs", "git status", nil, true, ""},
		{"trusted check runs", "npm test", trusted, true, ""},
		{"untrusted check refused", "npm test", nil, false, "verification check not covered by repository trust"},
		{"ask tier refused", "npm install", nil, false, "asks before running"},
		{"always-ask refused", "rm build.log", nil, false, "always asks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviewer := refusingReviewer(t)
			options := RunOptions{NoReviewer: headlessReason, CommandTrusted: tc.trusted}
			auto, err := authorize(t, commandRepo(t), tc.command, options, reviewer)
			if len(reviewer.requests) != 0 {
				t.Fatalf("%s emitted an approval event", tc.command)
			}
			if tc.wantAuto {
				if err != nil || auto == "" {
					t.Fatalf("%s: auto=%q err=%v; want it to run unprompted", tc.command, auto, err)
				}
				return
			}
			var refusal *NoReviewerError
			if !errors.As(err, &refusal) || refusal.Kind != "command" {
				t.Fatalf("%s: err=%v, want a *NoReviewerError", tc.command, err)
			}
			if !strings.Contains(err.Error(), tc.wantTier) || !strings.Contains(err.Error(), "was not run") || !strings.Contains(err.Error(), headlessReason) {
				t.Fatalf("%s: refusal %q does not name the tier %q and the reason", tc.command, err, tc.wantTier)
			}
		})
	}
}

func TestNoReviewerRefusedTierStaysRefused(t *testing.T) {
	reviewer := refusingReviewer(t)
	_, err := authorize(t, commandRepo(t), "rm -rf /", RunOptions{NoReviewer: headlessReason}, reviewer)
	var refusal *NoReviewerError
	if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "never runs it") {
		t.Fatalf("rm -rf /: err=%v; the refuse tier keeps its own refusal", err)
	}
}

func TestNoReviewerRefusesUntrustedMCP(t *testing.T) {
	servers := mcp.NewMcpManagerForTest(
		map[string]mcp.ServerConfig{"docs": {Command: "unused"}},
		map[string]*mcp.Client{"docs": {Tools: []mcp.Tool{{Name: "search", InputSchema: json.RawMessage(`{"type":"object"}`)}}}},
		map[string]bool{}, nil)
	reviewer := refusingReviewer(t)
	registry, _ := newToolRegistry(nil, t.TempDir(), servers, RunOptions{NoReviewer: headlessReason}, reviewer.emit)
	var name string
	for _, definition := range registry.Definitions(mainScope(tools.Scope{})) {
		if strings.Contains(definition.Name, "docs") && strings.Contains(definition.Name, "search") {
			name = definition.Name
		}
	}
	if name == "" {
		t.Fatal("the MCP tool was not registered")
	}
	result, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall(name, `{}`))
	if len(reviewer.requests) != 0 {
		t.Fatal("an untrusted MCP call emitted an approval event")
	}
	var refusal *NoReviewerError
	if result.Status != tools.Refused || !errors.As(err, &refusal) || refusal.Kind != "mcp" || !strings.Contains(refusal.Error(), "MCP server docs") {
		t.Fatalf("MCP call %s: status=%q cause=%q err=%v; want a no-reviewer refusal naming the server", name, result.Status, modelError(err), err)
	}
}

func TestReviewWithReviewerStillAsks(t *testing.T) {
	reviewer := &fakeReviewer{approve: false}
	_, err := authorize(t, commandRepo(t), "npm install", RunOptions{}, reviewer)
	if len(reviewer.requests) != 1 || err == nil || !strings.Contains(err.Error(), "rejected by user") {
		t.Fatalf("interactive review changed: prompts=%d err=%v", len(reviewer.requests), err)
	}
}
