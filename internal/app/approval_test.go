package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"lisa/internal/model"
	"lisa/internal/repository"
)

// An actual model-protocol tool request enters the agent loop; approval is
// given or denied only after the test observes the proposed action on disk.
func runApprovalScenario(t *testing.T, root, name, arguments string, decide func(*approvalRequest)) string {
	t.Helper()
	var calls atomic.Int32
	var outcome string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			call := map[string]any{"index": 0, "id": "action_1", "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}
			chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}, "finish_reason": "tool_calls"}}}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			return
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode follow-up: %v", err)
			return
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" {
			t.Errorf("last message is not tool outcome: %+v", last)
		}
		outcome = last.Content
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Acknowledged\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "local-test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var events []turnEvent
	runTurn(context.Background(), client, repo, root, nil, "make a change", nil, func(ev turnEvent) {
		events = append(events, ev)
		if ev.kind == "approval" {
			decide(ev.approval)
		}
	})
	if calls.Load() != 2 || len(events) == 0 || events[len(events)-1].kind != "done" {
		t.Fatalf("conversation did not complete: calls = %d, events = %+v", calls.Load(), events)
	}
	return outcome
}

func TestAgentEditRequiresApprovalAndReportsRejection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "work.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := `{"path":"work.txt","content":"after\n"}`
	outcome := runApprovalScenario(t, root, "edit_file", args, func(request *approvalRequest) {
		if request.Kind != "edit" || !strings.Contains(request.Body, "after") {
			t.Fatalf("diff missing proposed edit: %+v", request)
		}
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "before\n" {
			t.Fatalf("edit applied before approval: %q, %v", content, err)
		}
		request.Reply <- false
	})
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "before\n" || !strings.Contains(outcome, "rejected") {
		t.Fatalf("rejection changed file or misreported outcome: file = %q, outcome = %q, err = %v", content, outcome, err)
	}
}

func TestAgentApprovedEditAppliesExactlyReviewedChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "work.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	outcome := runApprovalScenario(t, root, "edit_file", `{"path":"work.txt","content":"after\n"}`, func(request *approvalRequest) {
		if !strings.Contains(request.Body, "-before") || !strings.Contains(request.Body, "+after") {
			t.Fatalf("review missing changed lines: %q", request.Body)
		}
		request.Reply <- true
	})
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "after\n" || !strings.Contains(outcome, "Applied edit") {
		t.Fatalf("approved edit failed: file = %q, outcome = %q, err = %v", content, outcome, err)
	}
}

func TestAgentCommandRequiresApprovalAndReportsExit(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ran.txt")
	for _, approved := range []bool{false, true} {
		outcome := runApprovalScenario(t, root, "run_command", `{"command":"printf executed > ran.txt"}`, func(request *approvalRequest) {
			if request.Kind != "command" || !strings.Contains(request.Body, root) || !strings.Contains(request.Body, "printf executed > ran.txt") {
				t.Fatalf("command or cwd missing from review: %+v", request)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("command ran before approval")
			}
			request.Reply <- approved
		})
		data, err := os.ReadFile(marker)
		if approved {
			if err != nil || string(data) != "executed" || !strings.Contains(outcome, "Exit status: 0") {
				t.Fatalf("approved command: file = %q, outcome = %q, err = %v", data, outcome, err)
			}
		} else if !os.IsNotExist(err) || !strings.Contains(outcome, "rejected") {
			t.Fatalf("rejected command: file = %q, outcome = %q, err = %v", data, outcome, err)
		}
	}
}
