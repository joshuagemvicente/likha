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
	"time"

	"likha/internal/model"
	"likha/internal/repository"
)

// steerRequest is the part of a chat-completions request body the steer-now
// tests inspect.
type steerRequest struct {
	Messages []struct {
		Role      string            `json:"role"`
		Content   string            `json:"content"`
		ToolCalls []json.RawMessage `json:"tool_calls"`
	} `json:"messages"`
}

// steerServer answers the connection check, decodes each model request, and
// hands it to respond with its 1-based request number.
func steerServer(t *testing.T, respond func(n int32, body steerRequest, w http.ResponseWriter, r *http.Request)) (*model.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body steerRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		respond(calls.Add(1), body, w, r)
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

// sendFrame writes one SSE data frame and flushes it to the client.
func sendFrame(w http.ResponseWriter, payload string) {
	fmt.Fprintf(w, "data: %s\n\n", payload)
	w.(http.Flusher).Flush()
}

func steerRepo(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("the answer is 42"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return repo, root
}

func eventKinds(events []TurnEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func indexOfKind(events []TurnEvent, kind string) int {
	for i, ev := range events {
		if ev.Kind == kind {
			return i
		}
	}
	return -1
}

// (a) A steer-now signal mid-stream stops only that request: the run
// continues with a fresh request carrying the partial text and then the
// steered message, with no error or done in between.
func TestSteerNowStopsStreamAndContinuesRun(t *testing.T) {
	repo, root := steerRepo(t)
	steer := make(chan string, 4)
	steerNow := make(chan struct{}, 1)
	client, calls := steerServer(t, func(n int32, body steerRequest, w http.ResponseWriter, r *http.Request) {
		switch n {
		case 1:
			sendFrame(w, `{"choices":[{"delta":{"content":"Partial answer"}}]}`)
			<-r.Context().Done()
		case 2:
			m := body.Messages
			k := len(m)
			if k < 2 || m[k-2].Role != "assistant" || m[k-2].Content != "Partial answer" || m[k-1].Role != "user" || m[k-1].Content != "use tabs" {
				t.Errorf("second request does not end with partial text then steer: %+v", m)
			}
			sendFrame(w, `{"choices":[{"delta":{"content":"Done."},"finish_reason":"stop"}]}`)
			sendFrame(w, `[DONE]`)
		default:
			t.Errorf("unexpected model request %d", n)
		}
	})
	var events []TurnEvent
	sent := false
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, steer, RunOptions{SteerNow: steerNow}, func(ev TurnEvent) {
		events = append(events, ev)
		if ev.Kind == "text" && !sent {
			sent = true
			steer <- "use tabs"
			steerNow <- struct{}{}
		}
	})
	if calls.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", calls.Load())
	}
	interrupted, steered, done := indexOfKind(events, "stream_interrupted"), indexOfKind(events, "steer"), indexOfKind(events, "done")
	if interrupted < 0 || steered < interrupted || done < steered || indexOfKind(events, "error") >= 0 {
		t.Fatalf("event order = %v, want stream_interrupted, steer, done and no error", eventKinds(events))
	}
	ev := events[interrupted]
	if ev.Usage != nil {
		t.Fatalf("stream_interrupted usage = %+v, want nil", ev.Usage)
	}
	last := ev.History[len(ev.History)-1]
	if last.Role != "assistant" || last.Content != "Partial answer" || len(last.ToolCalls) != 0 || last.Reasoning != "" {
		t.Fatalf("interrupted history tail = %+v, want the partial assistant text", last)
	}
	final := events[len(events)-1]
	if final.Kind != "done" || final.History[len(final.History)-1].Content != "Done." {
		t.Fatalf("run did not finish with the fresh answer: %+v", final)
	}
}

// (b) With only reasoning streamed, the stop adds no assistant message: the
// steer follows the last settled message.
func TestSteerNowReasoningOnlyAddsNoAssistantMessage(t *testing.T) {
	repo, root := steerRepo(t)
	steer := make(chan string, 4)
	steerNow := make(chan struct{}, 1)
	client, calls := steerServer(t, func(n int32, body steerRequest, w http.ResponseWriter, r *http.Request) {
		switch n {
		case 1:
			sendFrame(w, `{"choices":[{"delta":{"reasoning_content":"thinking hard"}}]}`)
			<-r.Context().Done()
		case 2:
			m := body.Messages
			k := len(m)
			if k < 2 || m[k-2].Role != "user" || m[k-2].Content != "start" || m[k-1].Role != "user" || m[k-1].Content != "stop thinking" {
				t.Errorf("second request = %+v, want prompt then steer with no assistant message", m)
			}
			for _, msg := range m {
				if strings.Contains(msg.Content, "thinking hard") {
					t.Errorf("partial reasoning reached the provider: %+v", m)
				}
			}
			sendFrame(w, `{"choices":[{"delta":{"content":"OK."},"finish_reason":"stop"}]}`)
			sendFrame(w, `[DONE]`)
		}
	})
	var events []TurnEvent
	sent := false
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, steer, RunOptions{SteerNow: steerNow}, func(ev TurnEvent) {
		events = append(events, ev)
		if ev.Kind == "reasoning" && !sent {
			sent = true
			steer <- "stop thinking"
			steerNow <- struct{}{}
		}
	})
	if calls.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", calls.Load())
	}
	at := indexOfKind(events, "stream_interrupted")
	if at < 0 {
		t.Fatalf("no stream_interrupted event: %v", eventKinds(events))
	}
	for _, msg := range events[at].History {
		if msg.Role == "assistant" {
			t.Fatalf("reasoning-only stop added an assistant message: %+v", events[at].History)
		}
	}
}

// (c) A tool call the model had begun streaming is dropped: it never runs,
// never reaches approval, and is absent from the next request.
func TestSteerNowDropsPartialToolCall(t *testing.T) {
	repo, root := steerRepo(t)
	steer := make(chan string, 4)
	steerNow := make(chan struct{}, 1)
	client, calls := steerServer(t, func(n int32, body steerRequest, w http.ResponseWriter, r *http.Request) {
		switch n {
		case 1:
			sendFrame(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`)
			steer <- "never mind"
			steerNow <- struct{}{}
			<-r.Context().Done()
		case 2:
			for _, msg := range body.Messages {
				if len(msg.ToolCalls) > 0 || msg.Role == "tool" {
					t.Errorf("partial tool call reached the next request: %+v", body.Messages)
				}
			}
			if k := len(body.Messages); k == 0 || body.Messages[k-1].Content != "never mind" {
				t.Errorf("steer missing from next request: %+v", body.Messages)
			}
			sendFrame(w, `{"choices":[{"delta":{"content":"OK."},"finish_reason":"stop"}]}`)
			sendFrame(w, `[DONE]`)
		}
	})
	var events []TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, steer, RunOptions{SteerNow: steerNow}, func(ev TurnEvent) {
		events = append(events, ev)
	})
	if calls.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", calls.Load())
	}
	for _, ev := range events {
		if ev.Kind == "tool_start" || ev.Kind == "tool_result" || ev.Kind == "approval" {
			t.Fatalf("partial tool call executed: %v", eventKinds(events))
		}
	}
	if indexOfKind(events, "stream_interrupted") < 0 || events[len(events)-1].Kind != "done" {
		t.Fatalf("event kinds = %v, want stream_interrupted then done", eventKinds(events))
	}
}

// (d) A signal sent while a tool executes cancels nothing: the tool
// completes, the message is delivered at the boundary, and the stale signal
// does not stop the next request.
func TestSteerNowDuringToolWaitsForBoundary(t *testing.T) {
	repo, root := steerRepo(t)
	steer := make(chan string, 4)
	steerNow := make(chan struct{}, 1)
	client, calls := steerServer(t, func(n int32, body steerRequest, w http.ResponseWriter, r *http.Request) {
		switch n {
		case 1:
			sendFrame(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"known.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
			sendFrame(w, `[DONE]`)
		case 2:
			m := body.Messages
			k := len(m)
			if k < 2 || m[k-2].Role != "tool" || m[k-1].Role != "user" || m[k-1].Content != "after the read" {
				t.Errorf("steer not delivered after the tool result: %+v", m)
			}
			sendFrame(w, `{"choices":[{"delta":{"content":"Read it."}}]}`)
			// A leftover signal would stop the request during this pause.
			time.Sleep(100 * time.Millisecond)
			sendFrame(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
			sendFrame(w, `[DONE]`)
		}
	})
	var events []TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, steer, RunOptions{SteerNow: steerNow}, func(ev TurnEvent) {
		events = append(events, ev)
		if ev.Kind == "tool_start" {
			steer <- "after the read"
			steerNow <- struct{}{}
		}
	})
	if calls.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", calls.Load())
	}
	if at := indexOfKind(events, "stream_interrupted"); at >= 0 {
		t.Fatalf("a steer during a tool stopped a request: %v", eventKinds(events))
	}
	result := events[indexOfKind(events, "tool_result")]
	if result.ToolResult == nil || !strings.Contains(result.ToolResult.Content, "the answer is 42") {
		t.Fatalf("tool did not complete normally: %+v", result.ToolResult)
	}
	final := events[len(events)-1]
	if final.Kind != "done" || final.History[len(final.History)-1].Content != "Read it." {
		t.Fatalf("run did not finish with the full answer: %+v", final)
	}
}

// (e) Run cancellation wins over a steer pressed at the same moment.
func TestSteerNowLosesToRunCancellation(t *testing.T) {
	repo, root := steerRepo(t)
	steer := make(chan string, 4)
	steerNow := make(chan struct{}, 1)
	client, calls := steerServer(t, func(n int32, body steerRequest, w http.ResponseWriter, r *http.Request) {
		sendFrame(w, `{"choices":[{"delta":{"content":"Partial"}}]}`)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []TurnEvent
	sent := false
	RunTurnWithOptions(ctx, client, repo, root, nil, "start", nil, steer, RunOptions{SteerNow: steerNow}, func(ev TurnEvent) {
		events = append(events, ev)
		if ev.Kind == "text" && !sent {
			sent = true
			steer <- "late"
			steerNow <- struct{}{}
			cancel()
		}
	})
	if calls.Load() != 1 {
		t.Fatalf("model requests = %d, want 1", calls.Load())
	}
	if indexOfKind(events, "stream_interrupted") >= 0 || indexOfKind(events, "steer") >= 0 {
		t.Fatalf("cancelled run steered: %v", eventKinds(events))
	}
	if events[len(events)-1].Kind != "error" {
		t.Fatalf("cancelled run ended with %q, want error", events[len(events)-1].Kind)
	}
}

// (f) A signal with nothing queued never stops a response.
func TestSteerNowWithEmptyQueueDoesNotStop(t *testing.T) {
	repo, root := steerRepo(t)
	steer := make(chan string, 4)
	steerNow := make(chan struct{}, 1)
	client, calls := steerServer(t, func(n int32, body steerRequest, w http.ResponseWriter, r *http.Request) {
		sendFrame(w, `{"choices":[{"delta":{"content":"Whole "}}]}`)
		time.Sleep(100 * time.Millisecond)
		sendFrame(w, `{"choices":[{"delta":{"content":"answer."},"finish_reason":"stop"}]}`)
		sendFrame(w, `[DONE]`)
	})
	var events []TurnEvent
	sent := false
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, steer, RunOptions{SteerNow: steerNow}, func(ev TurnEvent) {
		events = append(events, ev)
		if ev.Kind == "text" && !sent {
			sent = true
			steerNow <- struct{}{}
		}
	})
	if calls.Load() != 1 {
		t.Fatalf("model requests = %d, want 1", calls.Load())
	}
	final := events[len(events)-1]
	if indexOfKind(events, "stream_interrupted") >= 0 || final.Kind != "done" || final.History[len(final.History)-1].Content != "Whole answer." {
		t.Fatalf("empty-queue signal stopped the response: %v", eventKinds(events))
	}
}
