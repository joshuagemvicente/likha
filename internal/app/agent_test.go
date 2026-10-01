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

// serveModels answers the connection-check path used by client.EnsureConnected.
func serveModels(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.URL.Path != "/v1/models" {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"object":"list","data":[]}`)
	return true
}

func TestAgentReadsFileAndReportsResultToModel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("the answer is 42"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected endpoint %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"known.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		if len(body.Messages) < 3 || body.Messages[len(body.Messages)-1].Role != "tool" || body.Messages[len(body.Messages)-1].ToolCallID != "call_1" || body.Messages[len(body.Messages)-1].Content != "the answer is 42" {
			t.Errorf("tool result not returned to model: %+v", body.Messages)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"The answer is 42.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	var events []turnEvent
	runTurn(context.Background(), client, repo, root, nil, "Find the answer", nil, func(ev turnEvent) { events = append(events, ev) })
	if got := calls.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2", got)
	}
	var text, tools string
	for _, ev := range events {
		if ev.kind == "error" {
			t.Fatalf("agent error: %s", ev.text)
		}
		if ev.kind == "text" {
			text += ev.text
		}
		if ev.kind == "tool_result" {
			tools += ev.text
		}
	}
	if text != "The answer is 42." || !strings.Contains(tools, "the answer is 42") || events[len(events)-1].kind != "done" {
		t.Fatalf("text = %q, tools = %q, last event = %q", text, tools, events[len(events)-1].kind)
	}
}

func TestAgentReportsRejectedReadWithoutLeakingFile(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("private value"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_escape\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"../secret.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
		}
		result := body.Messages[len(body.Messages)-1].Content
		if strings.Contains(result, "private value") || !strings.Contains(result, "Error:") {
			t.Errorf("unsafe tool result: %q", result)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Could not read it.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	var events []turnEvent
	runTurn(context.Background(), client, repo, root, nil, "Read outside", nil, func(ev turnEvent) { events = append(events, ev) })
	if calls.Load() != 2 || events[len(events)-1].kind != "done" {
		t.Fatalf("agent did not recover from rejected read: %+v", events)
	}
}

func TestRunCommandRejectsHiddenReviewCharactersBeforeApproval(t *testing.T) {
	for _, command := range []string{"printf ok\rhidden", "printf ok\tescaped", "printf ok\nhidden", "printf ok\x1b[2J", "printf ok\u202ehidden", "printf ok\u200bhidden", "printf ok\u0301hidden"} {
		t.Run(fmt.Sprintf("%q", command), func(t *testing.T) {
			var approvals int
			args, err := json.Marshal(map[string]string{"command": command})
			if err != nil {
				t.Fatal(err)
			}
			_, err = dispatchTool(context.Background(), nil, t.TempDir(), model.ToolCall{Name: "run_command", Arguments: string(args)}, nil, func(ev turnEvent) {
				if ev.kind == "approval" {
					approvals++
				}
			})
			if err == nil || !strings.Contains(err.Error(), "rejects invisible") || approvals != 0 {
				t.Fatalf("unsafe command reached review: approvals=%d error=%v", approvals, err)
			}
		})
	}
}

func TestCancelledPendingCommandDoesNotExecuteOrRecordCompletion(t *testing.T) {
	root := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"pending\",\"type\":\"function\",\"function\":{\"name\":\"run_command\",\"arguments\":\"{\\\"command\\\":\\\"touch marker\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var terminal turnEvent
	runTurn(ctx, client, repo, root, nil, "do it", nil, func(ev turnEvent) {
		if ev.kind == "approval" {
			cancel()
		}
		if ev.kind == "error" || ev.kind == "done" {
			terminal = ev
		}
	})
	if terminal.kind != "error" || calls.Load() != 1 || len(terminal.history) != 3 || terminal.history[2].Content != "Error: action not executed; run interrupted" {
		t.Fatalf("cancelled approval recorded incorrect outcome: event=%+v requests=%d", terminal, calls.Load())
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); !os.IsNotExist(err) {
		t.Fatalf("pending approval executed command: %v", err)
	}
}

func TestCancelledAfterCompletedToolStillReportsOutcome(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ready.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"complete\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":\"{\\\"path\\\":\\\"ready.txt\\\",\\\"content\\\":\\\"after\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var result, terminal turnEvent
	runTurn(ctx, client, repo, root, nil, "edit", nil, func(ev turnEvent) {
		if ev.kind == "approval" {
			ev.approval.Reply <- true
		}
		if ev.kind == "tool_result" {
			result = ev
			cancel()
		}
		if ev.kind == "error" || ev.kind == "done" {
			terminal = ev
		}
	})
	if result.kind != "tool_result" || result.history[2].Content != "Applied edit to ready.txt" || terminal.kind != "error" || terminal.history[2].Content != result.history[2].Content || calls.Load() != 1 {
		t.Fatalf("completed action lost on cancellation: result=%+v terminal=%+v calls=%d", result, terminal, calls.Load())
	}
	data, err := os.ReadFile(filepath.Join(root, "ready.txt"))
	if err != nil || string(data) != "after" {
		t.Fatalf("approved edit was not applied: %q %v", data, err)
	}
}
