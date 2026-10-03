package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"likha/internal/model"
	"likha/internal/repository"
)

func TestRunTurnEmitsFreshEstimatedAndMeasuredContextPerRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("tool result content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "next.txt"), []byte("next tool result"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	steer := make(chan string, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected endpoint %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		request := requests.Add(1)
		var body struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch request {
		case 1:
			if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "Find the answer" {
				t.Errorf("first request has unexpected history: %+v", body.Messages)
			}
			steer <- "also consider the tests"
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"read_call\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"note.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":53,\"completion_tokens\":8}}\n\n")
		case 2:
			if len(body.Messages) != 4 ||
				body.Messages[0].Role != "user" || body.Messages[0].Content != "Find the answer" ||
				body.Messages[1].Role != "assistant" || len(body.Messages[1].ToolCalls) != 1 || body.Messages[1].ToolCalls[0].Function.Name != "read" ||
				body.Messages[2].Role != "tool" || body.Messages[2].ToolCallID != "read_call" || body.Messages[2].Content != "tool result content" ||
				body.Messages[3].Role != "user" || body.Messages[3].Content != "also consider the tests" {
				t.Errorf("second request is missing current tool/steering history: %+v", body.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"read_call_2\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"next.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case 3:
			if len(body.Messages) != 6 ||
				body.Messages[0].Role != "user" || body.Messages[0].Content != "Find the answer" ||
				body.Messages[1].Role != "assistant" || len(body.Messages[1].ToolCalls) != 1 || body.Messages[1].ToolCalls[0].Function.Name != "read" ||
				body.Messages[2].Role != "tool" || body.Messages[2].ToolCallID != "read_call" || body.Messages[2].Content != "tool result content" ||
				body.Messages[3].Role != "user" || body.Messages[3].Content != "also consider the tests" ||
				body.Messages[4].Role != "assistant" || len(body.Messages[4].ToolCalls) != 1 || body.Messages[4].ToolCalls[0].Function.Name != "read" ||
				body.Messages[5].Role != "tool" || body.Messages[5].ToolCallID != "read_call_2" || body.Messages[5].Content != "next tool result" {
				t.Errorf("third request is missing the second tool result: %+v", body.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected request %d", request)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
		if request == 1 {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}

	firstHistory := []model.Message{{Role: "user", Content: "Find the answer"}}
	secondHistory := []model.Message{
		{Role: "user", Content: "Find the answer"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "read_call", Name: "read", Arguments: `{"path":"note.txt"}`}}},
		{Role: "tool", ToolCallID: "read_call", Content: "tool result content"},
		{Role: "user", Content: "also consider the tests"},
	}
	thirdHistory := append(append([]model.Message(nil), secondHistory...),
		model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "read_call_2", Name: "read", Arguments: `{"path":"next.txt"}`}}},
		model.Message{Role: "tool", ToolCallID: "read_call_2", Content: "next tool result"},
	)
	wantFirstTokens, wantFirstKnown := model.EstimateInputTokens("", firstHistory, agentTools)
	wantSecondTokens, wantSecondKnown := model.EstimateInputTokens("", secondHistory, agentTools)
	wantThirdTokens, wantThirdKnown := model.EstimateInputTokens("", thirdHistory, agentTools)
	var contextEvents []TurnEvent
	RunTurn(context.Background(), client, repo, root, nil, "Find the answer", nil, steer, func(ev TurnEvent) {
		if ev.Kind == "context" {
			contextEvents = append(contextEvents, ev)
			if ev.ContextEstimated {
				wantRequests := int32(0)
				wantTokens, wantKnown := wantFirstTokens, wantFirstKnown
				if len(contextEvents) == 3 {
					wantRequests = 1
					wantTokens, wantKnown = wantSecondTokens, wantSecondKnown
				} else if len(contextEvents) == 4 {
					wantRequests = 2
					wantTokens, wantKnown = wantThirdTokens, wantThirdKnown
				}
				if got := requests.Load(); got != wantRequests {
					t.Errorf("estimate emitted after %d provider requests, want %d", got, wantRequests)
				}
				if ev.ContextTokens != wantTokens || ev.ContextKnown != wantKnown {
					t.Errorf("estimate event = (%d, known=%t), want (%d, known=%t)", ev.ContextTokens, ev.ContextKnown, wantTokens, wantKnown)
				}
			} else if ev.ContextTokens != 53 || !ev.ContextKnown {
				t.Errorf("measured event = (%d, known=%t), want (53, known=true)", ev.ContextTokens, ev.ContextKnown)
			}
		}
	})

	if requests.Load() != 3 {
		t.Fatalf("provider requests = %d, want 3", requests.Load())
	}
	if len(contextEvents) != 4 {
		t.Fatalf("context events = %+v, want estimate, measured, and two fresh estimates (no later usage)", contextEvents)
	}
	if !contextEvents[0].ContextEstimated || contextEvents[0].ContextTokens != wantFirstTokens || contextEvents[0].ContextKnown != wantFirstKnown {
		t.Errorf("initial estimate = %+v", contextEvents[0])
	}
	if contextEvents[1].ContextEstimated || contextEvents[1].ContextTokens != 53 || !contextEvents[1].ContextKnown {
		t.Errorf("measured replacement = %+v", contextEvents[1])
	}
	if !contextEvents[2].ContextEstimated || contextEvents[2].ContextTokens != wantSecondTokens || contextEvents[2].ContextKnown != wantSecondKnown {
		t.Errorf("fresh second-request estimate = %+v", contextEvents[2])
	}
	if !contextEvents[3].ContextEstimated || contextEvents[3].ContextTokens != wantThirdTokens || contextEvents[3].ContextKnown != wantThirdKnown {
		t.Errorf("fresh third-request estimate = %+v", contextEvents[3])
	}
}
