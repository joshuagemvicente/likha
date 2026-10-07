package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"likha/internal/cmdpolicy"
	"likha/internal/explore"
	"likha/internal/hooks"
	"likha/internal/model"
	"likha/internal/repository"
	"likha/internal/tools"
)

// hookScript writes an executable hook script and returns its path.
func hookScript(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return file
}

func hookRunner(hs ...hooks.Hook) *hooks.Runner {
	for i := range hs {
		if hs[i].Timeout == 0 {
			hs[i].Timeout = 5 * time.Second
		}
	}
	return hooks.NewRunner(hooks.Config{Path: "hooks.json", Hooks: hs})
}

// modelScript serves chat-completion frames per request number (1-based) and
// records every request body.
type modelScript struct {
	mu     sync.Mutex
	bodies []string
}

func (s *modelScript) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func scriptedClient(t *testing.T, reply func(n int) string) (*model.Client, *modelScript) {
	t.Helper()
	script := &modelScript{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		script.mu.Lock()
		script.bodies = append(script.bodies, string(body))
		n := len(script.bodies)
		script.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", reply(n))
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "local-test", "")
	if err != nil {
		t.Fatal(err)
	}
	return client, script
}

func toolCallFrame(id, name, arguments string) string {
	call := map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}, "finish_reason": "tool_calls"}}})
	return string(data)
}

const doneFrame = `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`

// oneToolTurn runs a turn whose model makes one tool call and then stops. It
// returns the run's events and the tool message the model received.
func oneToolTurn(t *testing.T, root, name, arguments string, options RunOptions, decide func(*ApprovalRequest)) ([]TurnEvent, string) {
	t.Helper()
	client, script := scriptedClient(t, func(n int) string {
		if n == 1 {
			return toolCallFrame("call_1", name, arguments)
		}
		return doneFrame
	})
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var events []TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "go", nil, nil, options, func(ev TurnEvent) {
		events = append(events, ev)
		if ev.Kind == "approval" && decide != nil {
			decide(ev.Approval)
		}
	})
	if len(events) == 0 || events[len(events)-1].Kind != "done" {
		t.Fatalf("run did not complete: %+v", events)
	}
	requests := script.requests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(requests[1]), &body); err != nil {
		t.Fatal(err)
	}
	last := body.Messages[len(body.Messages)-1]
	if last.Role != "tool" {
		t.Fatalf("last message = %+v, want the tool outcome", last)
	}
	return events, last.Content
}

func countKind(events []TurnEvent, kind string) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func TestPreToolHookBlocksCommandBeforeReview(t *testing.T) {
	root := t.TempDir()
	guard := hookScript(t, `cat >/dev/null; echo "commands are off" >&2; exit 2`)
	options := RunOptions{Hooks: hookRunner(hooks.Hook{Event: hooks.PreTool, Name: "guard", Match: []string{"run_command"}, Command: guard})}
	events, outcome := oneToolTurn(t, root, "run_command", `{"command":"printf x > ran.txt"}`, options, func(*ApprovalRequest) {
		t.Fatal("a blocked call opened a review")
	})
	if countKind(events, "approval") != 0 {
		t.Fatal("a blocked call opened a review")
	}
	if !strings.Contains(outcome, `"status":"refused"`) || !strings.Contains(outcome, `Blocked by hook \"guard\": commands are off`) {
		t.Fatalf("model outcome = %q", outcome)
	}
	if _, err := os.Stat(filepath.Join(root, "ran.txt")); !os.IsNotExist(err) {
		t.Fatal("the blocked command ran")
	}
}

func TestPreToolHookBlocksSessionGrantedCommand(t *testing.T) {
	const command = "printf x > ran.txt"
	grants := &cmdpolicy.Grants{}
	grants.Allow(command)
	// Control: without hooks the grant runs the command with no review.
	root := t.TempDir()
	events, _ := oneToolTurn(t, root, "run_command", `{"command":"`+command+`"}`, RunOptions{CommandGrants: grants}, nil)
	if countKind(events, "approval") != 0 {
		t.Fatal("control: the session grant did not apply")
	}
	if _, err := os.Stat(filepath.Join(root, "ran.txt")); err != nil {
		t.Fatalf("control: granted command did not run: %v", err)
	}
	root = t.TempDir()
	guard := hookScript(t, `cat >/dev/null; exit 2`)
	options := RunOptions{CommandGrants: grants, Hooks: hookRunner(hooks.Hook{Event: hooks.PreTool, Name: "guard", Command: guard})}
	_, outcome := oneToolTurn(t, root, "run_command", `{"command":"`+command+`"}`, options, nil)
	if !strings.Contains(outcome, `Blocked by hook \"guard\"`) {
		t.Fatalf("model outcome = %q", outcome)
	}
	if _, err := os.Stat(filepath.Join(root, "ran.txt")); !os.IsNotExist(err) {
		t.Fatal("a session grant ran a command a hook blocked")
	}
}

func TestPreToolHookCannotApprove(t *testing.T) {
	root := t.TempDir()
	approver := hookScript(t, `cat >/dev/null; echo '{"decision":"allow"}'`)
	options := RunOptions{Hooks: hookRunner(hooks.Hook{Event: hooks.PreTool, Name: "approver", Command: approver})}
	reviewed := 0
	events, outcome := oneToolTurn(t, root, "run_command", `{"command":"printf x > ran.txt"}`, options, func(request *ApprovalRequest) {
		reviewed++
		request.Reply <- false
	})
	if reviewed != 1 || !strings.Contains(outcome, "rejected") {
		t.Fatalf("an allow decision skipped the review: reviews=%d outcome=%q", reviewed, outcome)
	}
	warned := false
	for _, ev := range events {
		warned = warned || ev.Kind == "hook" && strings.Contains(ev.Text, "hooks cannot approve")
	}
	if !warned {
		t.Fatal("no warning that hooks cannot approve")
	}
}

func TestPostToolHookEventCarriesResult(t *testing.T) {
	root := t.TempDir()
	options := RunOptions{Hooks: hookRunner(hooks.Hook{Event: hooks.PostTool, Name: "log", Command: "true"})}
	events, _ := oneToolTurn(t, root, "run_command", `{"command":"exit 3"}`, options, func(request *ApprovalRequest) {
		request.Reply <- true
	})
	var observed *hooks.Event
	for _, ev := range events {
		if ev.Kind == "hook_observe" {
			observed = ev.Hook
		}
	}
	if observed == nil || observed.Event != hooks.PostTool || observed.Agent != "main" || observed.Tool.Name != "run_command" {
		t.Fatalf("post_tool event = %+v", observed)
	}
	if observed.Result == nil || observed.Result.Status != string(tools.Failed) || observed.Result.ExitCode == nil || *observed.Result.ExitCode != 3 {
		t.Fatalf("post_tool result = %+v", observed.Result)
	}
	if string(observed.Tool.Arguments) != `{"command":"exit 3"}` || observed.Tool.Source != "builtin" || observed.Tool.Effects[0] != "exec" {
		t.Fatalf("post_tool tool = %+v", observed.Tool)
	}
}

func TestNoHooksLeavesRequestsUnchanged(t *testing.T) {
	run := func(options RunOptions) []string {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		client, script := scriptedClient(t, func(n int) string {
			if n == 1 {
				return toolCallFrame("call_1", "read", `{"path":"a.txt"}`)
			}
			return doneFrame
		})
		repo, err := repository.New(root)
		if err != nil {
			t.Fatal(err)
		}
		var kinds []string
		RunTurnWithOptions(context.Background(), client, repo, root, nil, "go", nil, nil, options, func(ev TurnEvent) { kinds = append(kinds, ev.Kind) })
		second := script.requests()[1]
		// Repository paths differ per temp dir; compare the tool exchange.
		return append(kinds, second[strings.Index(second, `"role":"tool"`):])
	}
	plain := run(RunOptions{})
	empty := run(RunOptions{Hooks: hooks.NewRunner(hooks.Config{})})
	if fmt.Sprint(plain) != fmt.Sprint(empty) {
		t.Fatalf("an empty hook config changed the run:\n%v\n%v", plain, empty)
	}
}

func TestSteeringPromptBlockedAtDelivery(t *testing.T) {
	root := t.TempDir()
	guard := hookScript(t, `case "$(cat)" in *zebra*) echo "no zebras" >&2; exit 2;; esac`)
	options := RunOptions{SessionID: "s1", Hooks: hookRunner(hooks.Hook{Event: hooks.PromptSubmit, Name: "guard", Command: guard})}
	client, script := scriptedClient(t, func(int) string { return doneFrame })
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	steer := make(chan string, 4)
	steer <- "my zebra token"
	steer <- "also check the docs"
	var events []TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "go", nil, steer, options, func(ev TurnEvent) { events = append(events, ev) })
	var blocked, delivered []string
	for _, ev := range events {
		switch ev.Kind {
		case "steer_blocked":
			var block *hooks.BlockError
			if !errors.As(ev.Err, &block) || block.PromptMessage() != `Prompt blocked by hook "guard": no zebras` {
				t.Fatalf("steer_blocked err = %v", ev.Err)
			}
			blocked = append(blocked, ev.Text)
		case "steer":
			delivered = append(delivered, ev.Text)
		}
	}
	if fmt.Sprint(blocked) != "[my zebra token]" || fmt.Sprint(delivered) != "[also check the docs]" {
		t.Fatalf("blocked=%v delivered=%v", blocked, delivered)
	}
	for _, body := range script.requests() {
		if strings.Contains(body, "zebra") {
			t.Fatal("a blocked steering prompt reached the model")
		}
	}
	if events[len(events)-1].Kind != "done" {
		t.Fatalf("last event = %s", events[len(events)-1].Kind)
	}
}

func TestAllSteeringBlockedEndsRunWithoutRequest(t *testing.T) {
	root := t.TempDir()
	guard := hookScript(t, `cat >/dev/null; exit 2`)
	options := RunOptions{Hooks: hookRunner(hooks.Hook{Event: hooks.PromptSubmit, Name: "guard", Command: guard})}
	// The first request answers; the steering prompt queued meanwhile is
	// blocked, so no second request may follow.
	steer := make(chan string, 1)
	client, script := scriptedClient(t, func(n int) string {
		if n == 1 {
			steer <- "later"
		}
		return doneFrame
	})
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var last TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "go", nil, steer, options, func(ev TurnEvent) { last = ev })
	if last.Kind != "done" || len(script.requests()) != 1 {
		t.Fatalf("last=%s requests=%d, want done after one request", last.Kind, len(script.requests()))
	}
}

func TestExploreChildReadBlockedAndNamed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(t.TempDir(), "payload.json")
	guard := hookScript(t, `cat >"`+payload+`"; grep -q '\.env' "`+payload+`" && { echo "no dotenv" >&2; exit 2; }; exit 0`)
	runner := hookRunner(hooks.Hook{Event: hooks.PreTool, Name: "no-dotenv", Match: []string{"read"}, Command: guard})
	client, script := scriptedClient(t, func(n int) string {
		if n == 1 {
			return toolCallFrame("k1", "read", `{"path":".env"}`)
		}
		return doneFrame
	})
	var mu sync.Mutex
	var emitted []TurnEvent
	gate := newHookGate(runner, "s1", root, func(ev TurnEvent) {
		mu.Lock()
		defer mu.Unlock()
		emitted = append(emitted, ev)
	})
	scope := tools.Scope{Agent: "explore", Allowed: map[string]bool{"read": true}}
	manager, err := explore.New(context.Background(), explore.Config{
		Root: repo.Root(), Provider: "local", Model: client.Model(), Scope: scope,
		Runner: ExploreRunner(client, repo, nil, scope, nil, gate),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Wait()
	outcome, err := manager.Spawn(context.Background(), "", "call_1", explore.Spec{Agent: "explore", Description: "look", Prompt: "read the env"})
	if err != nil || outcome.Status != explore.Completed {
		t.Fatalf("spawn: %v %+v", err, outcome)
	}
	requests := script.requests()
	// The refusal is a JSON envelope inside the JSON request body, so its
	// quotes arrive escaped twice.
	if len(requests) != 2 || !strings.Contains(requests[1], `Blocked by hook \\\"no-dotenv\\\": no dotenv`) || strings.Contains(requests[1], "TOKEN=x") {
		t.Fatalf("child's second request does not carry the block:\n%s", requests[len(requests)-1])
	}
	data, err := os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	var ev hooks.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Agent != "explore" || ev.SessionID != "s1" || ev.Tool.Name != "read" {
		t.Fatalf("child payload = %+v", ev)
	}
}

func TestCommandExitCode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Exit status: 0\nok", "0"},
		{"Exit status: 127\n", "127"},
		{"command_refused: nope", "<nil>"},
		{"", "<nil>"},
	}
	for _, tc := range cases {
		got := "<nil>"
		if code := commandExitCode(tc.in); code != nil {
			got = fmt.Sprint(*code)
		}
		if got != tc.want {
			t.Fatalf("commandExitCode(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
