package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"likha/internal/cmdpolicy"
	"likha/internal/model"
)

func toolCall(name, arguments string) model.ToolCall {
	return model.ToolCall{ID: "call-" + name, Name: name, Arguments: arguments}
}

// fakeReviewer answers approval events the way the TUI would: approve or
// decline, optionally choosing the Remember button.
type fakeReviewer struct {
	approve, remember bool
	requests          []*ApprovalRequest
	notices           []string
}

func (f *fakeReviewer) emit(ev TurnEvent) {
	switch ev.Kind {
	case "approval":
		f.requests = append(f.requests, ev.Approval)
		if f.approve && f.remember && ev.Approval.Remember != "" {
			ev.Approval.Remembered = true
		}
		ev.Approval.Reply <- f.approve
	case "notice":
		f.notices = append(f.notices, ev.Text)
	}
}

func commandRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"vitest run"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func authorize(t *testing.T, root, command string, options RunOptions, reviewer *fakeReviewer) (string, error) {
	t.Helper()
	auto := ""
	err := authorizeCommand(context.Background(), root, command, options, reviewer.emit, &auto)
	return auto, err
}

func TestAuthorizeReadOnlyRunsWithoutPrompt(t *testing.T) {
	reviewer := &fakeReviewer{}
	auto, err := authorize(t, commandRepo(t), "git status", RunOptions{}, reviewer)
	if err != nil || auto == "" || len(reviewer.requests) != 0 {
		t.Fatalf("git status: auto=%q err=%v prompts=%d", auto, err, len(reviewer.requests))
	}
}

func TestAuthorizeRefusesWithoutPrompt(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	_, err := authorize(t, commandRepo(t), "rm -rf /", RunOptions{}, reviewer)
	if err == nil || !strings.Contains(err.Error(), "refused") || len(reviewer.requests) != 0 {
		t.Fatalf("rm -rf /: err=%v prompts=%d", err, len(reviewer.requests))
	}
}

func TestAuthorizeAlwaysAskNeverOffersRemember(t *testing.T) {
	grants := &cmdpolicy.Grants{}
	reviewer := &fakeReviewer{approve: true, remember: true}
	options := RunOptions{CommandGrants: grants}
	for range 2 {
		auto, err := authorize(t, commandRepo(t), "rm build.log", options, reviewer)
		if err != nil || auto != "" {
			t.Fatalf("rm: auto=%q err=%v", auto, err)
		}
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("rm prompted %d times, want every time", len(reviewer.requests))
	}
	request := reviewer.requests[0]
	if request.Remember != "" || !strings.Contains(request.Warning, "deletes files") {
		t.Fatalf("always-ask request = %+v", request)
	}
	if grants.Allowed("rm build.log") {
		t.Fatal("an always-ask command was remembered")
	}
}

func TestAuthorizeApproveAlwaysCoversCommandPrefix(t *testing.T) {
	grants := &cmdpolicy.Grants{}
	reviewer := &fakeReviewer{approve: true, remember: true}
	options := RunOptions{CommandGrants: grants}
	root := commandRepo(t)
	if _, err := authorize(t, root, "npm install", options, reviewer); err != nil {
		t.Fatal(err)
	}
	request := reviewer.requests[0]
	if request.Remember != RememberSession {
		t.Fatalf("ask request did not offer Approve always: %+v", request)
	}
	if !strings.Contains(request.Body, `"Approve always" runs commands starting with "npm install" without asking for the rest of this session.`) {
		t.Fatalf("review body does not name the grant scope:\n%s", request.Body)
	}
	for _, command := range []string{"npm install", "npm install left-pad"} {
		auto, err := authorize(t, root, command, options, reviewer)
		if err != nil || auto != AutoApprovalReason || len(reviewer.requests) != 1 {
			t.Fatalf("%s: auto=%q err=%v prompts=%d", command, auto, err, len(reviewer.requests))
		}
	}
	if _, err := authorize(t, root, "npm ci", options, reviewer); err != nil || len(reviewer.requests) != 2 {
		t.Fatal("a different subcommand reused the grant")
	}
}

func TestAuthorizeGrantNeverCoversDangerousCommands(t *testing.T) {
	grants := &cmdpolicy.Grants{}
	options := RunOptions{CommandGrants: grants}
	root := commandRepo(t)
	grants.Allow("git commit -m x")
	grants.Allow("npm test")
	reviewer := &fakeReviewer{approve: true}
	auto, err := authorize(t, root, "git push origin main", options, reviewer)
	if err != nil || auto != "" || len(reviewer.requests) != 1 || !strings.HasPrefix(reviewer.requests[0].Warning, "Always asks:") {
		t.Fatalf("git push under a git commit grant: auto=%q err=%v prompts=%d", auto, err, len(reviewer.requests))
	}
	reviewer = &fakeReviewer{approve: true}
	auto, _ = authorize(t, root, "npm test; rm -rf .", options, reviewer)
	if auto != "" {
		t.Fatalf("a compound command ran under a prefix grant: auto=%q", auto)
	}
	reviewer = &fakeReviewer{approve: true}
	if _, err := authorize(t, root, "python a.py", options, reviewer); err != nil || len(reviewer.requests) != 1 {
		t.Fatal("interpreter command did not prompt")
	}
	if !strings.Contains(reviewer.requests[0].Body, `"Approve always" runs this exact command`) {
		t.Fatalf("interpreter review body:\n%s", reviewer.requests[0].Body)
	}
}

func TestAuthorizeDeclineReturnsRejection(t *testing.T) {
	reviewer := &fakeReviewer{approve: false}
	_, err := authorize(t, commandRepo(t), "npm install", RunOptions{CommandGrants: &cmdpolicy.Grants{}}, reviewer)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("declined command err = %v", err)
	}
}

func TestAuthorizeVerificationTrustFlow(t *testing.T) {
	root := commandRepo(t)
	trusted := map[string]string{}
	options := RunOptions{
		CommandGrants: &cmdpolicy.Grants{},
		CommandTrusted: func(check, fingerprint string) bool {
			recorded, ok := trusted[check]
			return ok && recorded == fingerprint
		},
		TrustChecks: func(checks map[string]string) error {
			for key, value := range checks {
				trusted[key] = value
			}
			return nil
		},
	}
	reviewer := &fakeReviewer{approve: true, remember: true}

	// Untrusted: prompts once and offers the trust button.
	auto, err := authorize(t, root, "npm test", options, reviewer)
	if err != nil || auto != "" || len(reviewer.requests) != 1 {
		t.Fatalf("first npm test: auto=%q err=%v prompts=%d", auto, err, len(reviewer.requests))
	}
	if request := reviewer.requests[0]; request.Remember != RememberTrust || !strings.Contains(request.Body, "vitest run") {
		t.Fatalf("verification request = %+v", request)
	}
	// Trusted: runs without a prompt, including other spellings and checks.
	for _, command := range []string{"npm test", "npm run test", "go test ./..."} {
		auto, err := authorize(t, root, command, options, reviewer)
		if err != nil || auto == "" {
			t.Fatalf("%s after trust: auto=%q err=%v", command, auto, err)
		}
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("trusted checks prompted: %d prompts", len(reviewer.requests))
	}
	// Changing the script revokes the trust for that check.
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"node evil.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if auto, _ := authorize(t, root, "npm test", options, reviewer); auto != "" || len(reviewer.requests) != 2 {
		t.Fatalf("changed script ran without a prompt: auto=%q prompts=%d", auto, len(reviewer.requests))
	}
}

func TestAuthorizeVerificationWithoutTrustHooksPrompts(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	auto, err := authorize(t, commandRepo(t), "npm test", RunOptions{}, reviewer)
	if err != nil || auto != "" || len(reviewer.requests) != 1 || reviewer.requests[0].Remember != "" {
		t.Fatalf("npm test without hooks: auto=%q err=%v requests=%+v", auto, err, reviewer.requests)
	}
}

func TestRunCommandLabelsAutoApprovedResult(t *testing.T) {
	root := commandRepo(t)
	registry, _ := newToolRegistry(nil, root, nil, RunOptions{}, (&fakeReviewer{}).emit)
	result, err := registry.Invoke(context.Background(), mainScope(RunOptions{}.Scope), toolCall("run_command", `{"command":"echo hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(result.Content, "\n", 3)
	if len(lines) < 3 || lines[0] != "Exit status: 0" || !strings.HasPrefix(lines[1], CommandApprovalPrefix+"auto-approved") || !strings.Contains(lines[2], "hello") {
		t.Fatalf("auto-approved result = %q", result.Content)
	}
}

func TestPlanModeStillRefusesReadOnlyCommands(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, _ := newToolRegistry(nil, commandRepo(t), nil, RunOptions{PlanMode: true}, reviewer.emit)
	_, err := registry.Invoke(context.Background(), mainScope(RunOptions{}.Scope), toolCall("run_command", `{"command":"git status"}`))
	if err == nil || !strings.Contains(err.Error(), "plan mode") || len(reviewer.requests) != 0 {
		t.Fatalf("plan mode git status: err=%v prompts=%d", err, len(reviewer.requests))
	}
}

func TestAuthorizeVerificationSessionGrantWithoutTrustStorage(t *testing.T) {
	root := commandRepo(t)
	options := RunOptions{CommandGrants: &cmdpolicy.Grants{}}
	reviewer := &fakeReviewer{approve: true, remember: true}
	if _, err := authorize(t, root, "npm test", options, reviewer); err != nil {
		t.Fatal(err)
	}
	if reviewer.requests[0].Remember != RememberSession {
		t.Fatalf("without trust storage the check offered %q", reviewer.requests[0].Remember)
	}
	if auto, err := authorize(t, root, "npm test", options, reviewer); err != nil || auto == "" || len(reviewer.requests) != 1 {
		t.Fatalf("session-granted check prompted again: auto=%q err=%v", auto, err)
	}
}
