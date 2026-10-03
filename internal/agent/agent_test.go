package agent

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
	var events []TurnEvent
	RunTurn(context.Background(), client, repo, root, nil, "Find the answer", nil, nil, func(ev TurnEvent) { events = append(events, ev) })
	if got := calls.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2", got)
	}
	var text, tools string
	for _, ev := range events {
		if ev.Kind == "error" {
			t.Fatalf("agent error: %s", ev.Text)
		}
		if ev.Kind == "text" {
			text += ev.Text
		}
		if ev.Kind == "tool_result" {
			tools += ev.Text
		}
	}
	if text != "The answer is 42." || !strings.Contains(tools, "the answer is 42") || events[len(events)-1].Kind != "done" {
		t.Fatalf("text = %q, tools = %q, last event = %q", text, tools, events[len(events)-1].Kind)
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
	var events []TurnEvent
	RunTurn(context.Background(), client, repo, root, nil, "Read outside", nil, nil, func(ev TurnEvent) { events = append(events, ev) })
	if calls.Load() != 2 || events[len(events)-1].Kind != "done" {
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
			_, err = dispatchTool(context.Background(), nil, t.TempDir(), model.ToolCall{Name: "run_command", Arguments: string(args)}, nil, func(ev TurnEvent) {
				if ev.Kind == "approval" {
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
	var terminal TurnEvent
	RunTurn(ctx, client, repo, root, nil, "do it", nil, nil, func(ev TurnEvent) {
		if ev.Kind == "approval" {
			cancel()
		}
		if ev.Kind == "error" || ev.Kind == "done" {
			terminal = ev
		}
	})
	if terminal.Kind != "error" || calls.Load() != 1 || len(terminal.History) != 3 || terminal.History[2].Content != "Error: action not executed; run interrupted" {
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
	var result, terminal TurnEvent
	RunTurn(ctx, client, repo, root, nil, "edit", nil, nil, func(ev TurnEvent) {
		if ev.Kind == "approval" {
			ev.Approval.Reply <- true
		}
		if ev.Kind == "tool_result" {
			result = ev
			cancel()
		}
		if ev.Kind == "error" || ev.Kind == "done" {
			terminal = ev
		}
	})
	if result.Kind != "tool_result" || result.History[2].Content != "Applied edit to ready.txt" || terminal.Kind != "error" || terminal.History[2].Content != result.History[2].Content || calls.Load() != 1 {
		t.Fatalf("completed action lost on cancellation: result=%+v terminal=%+v calls=%d", result, terminal, calls.Load())
	}
	data, err := os.ReadFile(filepath.Join(root, "ready.txt"))
	if err != nil || string(data) != "after" {
		t.Fatalf("approved edit was not applied: %q %v", data, err)
	}
}

// A message queued while a tool-calling round streams is delivered at the next
// round boundary: after that round's tool result and before the next request,
// with its steer event preceding the following round's first text.
func TestSteerMessageDeliveredAfterToolResultBeforeNextRound(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("the answer is 42"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	steer := make(chan string, 4)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			// Queued while the first round streams; delivery waits for the
			// round boundary after the tool result.
			steer <- "also check the tests"
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"known.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			n := len(body.Messages)
			if n < 4 || body.Messages[n-1].Role != "user" || body.Messages[n-1].Content != "also check the tests" || body.Messages[n-2].Role != "tool" {
				t.Errorf("steered message missing after tool result: %+v", body.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Checked.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected extra model request")
		}
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	var events []TurnEvent
	RunTurn(context.Background(), client, repo, root, nil, "Find the answer", nil, steer, func(ev TurnEvent) { events = append(events, ev) })
	if calls.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", calls.Load())
	}
	steerIndex, textIndex := -1, -1
	for i, ev := range events {
		if ev.Kind == "steer" {
			if steerIndex == -1 {
				steerIndex = i
			}
			if ev.Text != "also check the tests" {
				t.Fatalf("steer event text = %q, want raw message", ev.Text)
			}
			if len(ev.History) == 0 || ev.History[len(ev.History)-1].Role != "user" || ev.History[len(ev.History)-1].Content != "also check the tests" {
				t.Fatalf("steer history missing delivered message: %+v", ev.History)
			}
		}
		// Round 1 carries no text, so the first text event is round 2's.
		if ev.Kind == "text" && textIndex == -1 {
			textIndex = i
		}
	}
	if steerIndex == -1 || textIndex == -1 || steerIndex > textIndex {
		t.Fatalf("steer event (index %d) did not precede round 2 text (index %d): %+v", steerIndex, textIndex, events)
	}
	if events[len(events)-1].Kind != "done" {
		t.Fatalf("last event = %q, want done", events[len(events)-1].Kind)
	}
}

// A message queued while a no-tool-call response settles keeps the run alive:
// the queue is drained before "done", a further request is issued, and "done"
// arrives only once the queue is empty.
func TestSteerQueuedDuringIdleResponseContinuesRun(t *testing.T) {
	root := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	steer := make(chan string, 4)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			steer <- "one more thing"
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"First.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			n := len(body.Messages)
			if n < 3 || body.Messages[n-1].Role != "user" || body.Messages[n-1].Content != "one more thing" {
				t.Errorf("queued message missing from continued round: %+v", body.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Second.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected extra model request")
		}
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	var events []TurnEvent
	RunTurn(context.Background(), client, repo, root, nil, "start", nil, steer, func(ev TurnEvent) { events = append(events, ev) })
	if calls.Load() != 2 {
		t.Fatalf("model requests = %d, want 2 (run continued after queued message)", calls.Load())
	}
	if len(events) == 0 || events[len(events)-1].Kind != "done" {
		t.Fatalf("run did not finish with done: %+v", events)
	}
	steerIndex, doneIndex, doneCount := -1, -1, 0
	for i, ev := range events {
		if ev.Kind == "steer" {
			steerIndex = i
		}
		if ev.Kind == "done" {
			doneIndex = i
			doneCount++
		}
	}
	if steerIndex == -1 || doneIndex == -1 || steerIndex > doneIndex || doneCount != 1 {
		t.Fatalf("done emitted before the queue emptied: steer=%d done=%d count=%d events=%+v", steerIndex, doneIndex, doneCount, events)
	}
}

// Cancellation never drains the steering queue: the run ends with an error and
// the queued message is neither delivered nor recorded.
func TestSteerCancelledRunEmitsErrorWithoutDelivery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("the answer is 42"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	steer := make(chan string, 4)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"known.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []TurnEvent
	RunTurn(ctx, client, repo, root, nil, "start", nil, steer, func(ev TurnEvent) {
		events = append(events, ev)
		if ev.Kind == "tool_result" {
			// Queue a message and cancel at the same settlement point: the
			// cancel path must not drain it.
			steer <- "late message"
			cancel()
		}
	})
	if calls.Load() != 1 {
		t.Fatalf("model requests = %d, want 1", calls.Load())
	}
	var terminal *TurnEvent
	for i := range events {
		if events[i].Kind == "steer" {
			t.Fatalf("cancelled run delivered a steer event: %+v", events[i])
		}
		if events[i].Kind == "error" || events[i].Kind == "done" {
			terminal = &events[i]
		}
	}
	if terminal == nil || terminal.Kind != "error" {
		t.Fatalf("cancelled run did not end with error: %+v", events)
	}
	for _, msg := range terminal.History {
		if strings.Contains(msg.Content, "late message") {
			t.Fatalf("queued message leaked into cancelled history: %+v", terminal.History)
		}
	}
}
