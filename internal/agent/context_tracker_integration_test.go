package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"likha/internal/model"
	"likha/internal/repository"
)

func TestContextTrackerIntegrationRunTurnKeepsPerRequestUsageFresh(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("context tracker test"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var events []TurnEvent
	var contextsAtRequest []TurnEvent
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[]}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected model endpoint %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}

		// A request must not be sent until RunTurn has emitted this request's
		// approximate count; a preceding provider measurement is not current.
		mu.Lock()
		var lastContext TurnEvent
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Kind == "context" {
				lastContext = events[i]
				break
			}
		}
		contextsAtRequest = append(contextsAtRequest, lastContext)
		mu.Unlock()

		request := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		switch request {
		case 1:
			// Usage attached to the choice-bearing final event.
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"context_call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"note.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":41,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
		case 2:
			// The final usage-only event is another supported OpenAI-compatible
			// stream shape. A tool call keeps this RunTurn active for one more
			// request so the missing-usage case can be checked afterward.
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"context_call_2\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"note.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":52,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
		case 3:
			// No usage is optional telemetry, not a failed response. This request
			// must remain approximate rather than reusing 41 or 52.
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected extra model request %d", request)
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer server.Close()

	client, err := model.New(server.URL+"/v1", "gpt-4o-mini", "")
	if err != nil {
		t.Fatal(err)
	}
	RunTurn(context.Background(), client, repo, root, nil, "check context", nil, nil, func(event TurnEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})

	if got := requests.Load(); got != 3 {
		t.Fatalf("model requests = %d, want 3", got)
	}
	mu.Lock()
	gotEvents := append([]TurnEvent(nil), events...)
	gotContextsAtRequest := append([]TurnEvent(nil), contextsAtRequest...)
	mu.Unlock()
	var finalText string
	for _, event := range gotEvents {
		if event.Kind == "error" {
			t.Fatalf("RunTurn failed despite successful responses: %s", event.Text)
		}
		if event.Kind == "text" {
			finalText += event.Text
		}
	}
	if len(gotEvents) == 0 || gotEvents[len(gotEvents)-1].Kind != "done" {
		t.Fatalf("RunTurn did not finish successfully; final event = %+v", gotEvents[len(gotEvents)-1])
	}
	if len(gotContextsAtRequest) != 3 {
		t.Fatalf("context events observed at requests = %d, want 3: %+v", len(gotContextsAtRequest), gotContextsAtRequest)
	}
	for i, event := range gotContextsAtRequest {
		if event.Kind != "context" || !event.ContextKnown || !event.ContextEstimated || event.ContextTokens <= 0 {
			t.Errorf("request %d was sent without a positive, known approximate context event: %+v", i+1, event)
		}
	}

	var contextEvents []TurnEvent
	for _, event := range gotEvents {
		if event.Kind == "context" {
			contextEvents = append(contextEvents, event)
		}
	}
	if len(contextEvents) != 5 {
		t.Fatalf("context events = %d, want 3 estimates and 2 measurements: %+v", len(contextEvents), contextEvents)
	}
	for i, requestIndex := range []int{0, 2, 4} {
		event := contextEvents[requestIndex]
		if !event.ContextKnown || !event.ContextEstimated || event.ContextTokens <= 0 {
			t.Errorf("context event before request %d is not visibly approximate: %+v", i+1, event)
		}
	}
	for i, wantTokens := range []int64{41, 52} {
		event := contextEvents[2*i+1]
		if !event.ContextKnown || event.ContextEstimated || event.ContextTokens != wantTokens {
			t.Errorf("measured context event %d = %+v, want %d exact provider tokens", i+1, event, wantTokens)
		}
	}
	last := contextEvents[len(contextEvents)-1]
	if !last.ContextEstimated || !last.ContextKnown || last.ContextTokens <= 0 || last.ContextTokens == 41 || last.ContextTokens == 52 {
		t.Errorf("request without usage retained stale measured context: %+v", last)
	}
	if finalText != "finished" {
		t.Errorf("final response text = %q, want %q", finalText, "finished")
	}
}
