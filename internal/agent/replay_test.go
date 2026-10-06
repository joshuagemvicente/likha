package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/profiles"
	"likha/internal/tools"
)

// agentTestReplay holds both kinds of state: unsigned DeepSeek text, which
// survives a change to its message, and signed details, which do not.
func agentTestReplay(t *testing.T) *model.ReasoningReplay {
	t.Helper()
	text := "r"
	details := json.RawMessage(`[{"type":"reasoning.encrypted","data":"x"}]`)
	replay, err := model.NewReasoningReplay(model.ReplaySource{Endpoint: "https://x.example/v1/chat/completions", Model: "m"}, &text, details, nil)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}

// replayCapture stands in for provider capture through replyHook: every reply
// with tool calls gets replay and a signature on each call. It also records
// each request the loops send. Children call the hook from their own
// goroutines, hence the lock.
type replayCapture struct {
	mu       sync.Mutex
	requests [][]model.Message
}

func captureReplay(t *testing.T, replay *model.ReasoningReplay) *replayCapture {
	t.Helper()
	capture := &replayCapture{}
	previous := replyHook
	t.Cleanup(func() { replyHook = previous })
	replyHook = func(request []model.Message, reply model.Message) model.Message {
		capture.mu.Lock()
		capture.requests = append(capture.requests, append([]model.Message(nil), request...))
		capture.mu.Unlock()
		if len(reply.ToolCalls) == 0 {
			return reply
		}
		reply.Replay = replay
		calls := append([]model.ToolCall(nil), reply.ToolCalls...)
		for i := range calls {
			calls[i].ThoughtSignature = "sig"
		}
		reply.ToolCalls = calls
		return reply
	}
	return capture
}

func (c *replayCapture) request(t *testing.T, n int) []model.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) <= n {
		t.Fatalf("loop sent %d requests, want more than %d", len(c.requests), n)
	}
	return c.requests[n]
}

// assertUnsignedReplay checks that a changed assistant message kept only its
// unsigned replay text and no tool-call signature.
func assertUnsignedReplay(t *testing.T, got model.Message, captured *model.ReasoningReplay) {
	t.Helper()
	want := captured.UnsignedOnly()
	if want == nil || !got.Replay.Equal(want) {
		t.Fatalf("replay = %+v, want only the unsigned text %+v", got.Replay, want)
	}
	for _, call := range got.ToolCalls {
		if call.ThoughtSignature != "" {
			t.Fatalf("call %s kept signature %q after its message changed", call.ID, call.ThoughtSignature)
		}
	}
}

// A malformed batch is dropped from the main loop's history; the assistant
// message stays with its unsigned replay text, so a later DeepSeek tool
// request still carries it, but no signed state for calls it no longer has.
func TestMainLoopMalformedBatchKeepsOnlyUnsignedReplay(t *testing.T) {
	repo, root := steerRepo(t)
	replay := agentTestReplay(t)
	captureReplay(t, replay)
	client, calls := steerServer(t, func(n int32, _ steerRequest, w http.ResponseWriter, _ *http.Request) {
		if n != 1 {
			t.Errorf("unexpected model request %d", n)
			return
		}
		sendFrame(w, `{"choices":[{"delta":{"content":"a","tool_calls":[`+
			`{"index":0,"id":"dup","type":"function","function":{"name":"read","arguments":"{\"path\":\"known.txt\"}"}},`+
			`{"index":1,"id":"dup","type":"function","function":{"name":"read","arguments":"{\"path\":\"known.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
		sendFrame(w, `[DONE]`)
	})
	var failed *TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, nil, RunOptions{}, func(ev TurnEvent) {
		if ev.Kind == "error" {
			failed = &ev
		}
	})
	if calls.Load() != 1 || failed == nil {
		t.Fatalf("requests = %d, error event = %v; want one request and an error", calls.Load(), failed)
	}
	last := failed.History[len(failed.History)-1]
	if last.Role != "assistant" || last.Content != "a" || last.ToolCalls != nil {
		t.Fatalf("history tail = %+v, want the assistant text without its malformed calls", last)
	}
	assertUnsignedReplay(t, last, replay)
}

// Explore and profile children clip an oversize reply's content for their
// bounded transcript. The clipped turn keeps its unsigned replay text on the
// next request and loses signed state; an unclipped turn keeps everything.
func TestChildLoopClippedReplyKeepsOnlyUnsignedReplay(t *testing.T) {
	agents := []string{"explore", "reviewer"}
	sizes := []struct {
		name    string
		content string
		clipped bool
	}{
		{"at the limit", strings.Repeat("x", exploreStreamBytes), false},
		{"over the limit", strings.Repeat("x", exploreStreamBytes+1), true},
	}
	for _, agentName := range agents {
		for _, size := range sizes {
			t.Run(agentName+" "+size.name, func(t *testing.T) {
				replay := agentTestReplay(t)
				capture := captureReplay(t, replay)
				outcome := runChildTask(t, agentName, size.content)
				if outcome.Status != explore.Completed {
					t.Fatalf("child outcome = %s (%s), want completed", outcome.Status, outcome.Reason)
				}
				var reply *model.Message
				for _, m := range capture.request(t, 1) {
					if m.Role == "assistant" && len(m.ToolCalls) > 0 {
						reply = &m
					}
				}
				if reply == nil {
					t.Fatal("second child request lacks the first reply")
				}
				if len(reply.Content) != exploreStreamBytes {
					t.Fatalf("resent content = %d bytes, want %d", len(reply.Content), exploreStreamBytes)
				}
				if !size.clipped {
					if !reply.Replay.Equal(replay) || reply.ToolCalls[0].ThoughtSignature != "sig" {
						t.Fatalf("unclipped reply lost replay state: %+v", reply)
					}
					return
				}
				assertUnsignedReplay(t, *reply, replay)
			})
		}
	}
}

// runChildTask spawns one child task named agentName whose first reply is
// content plus a read call and whose second reply ends the task.
func runChildTask(t *testing.T, agentName, content string) explore.Outcome {
	t.Helper()
	repo, _ := steerRepo(t)
	contentJSON, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := steerServer(t, func(n int32, _ steerRequest, w http.ResponseWriter, _ *http.Request) {
		switch n {
		case 1:
			sendFrame(w, `{"choices":[{"delta":{"content":`+string(contentJSON)+`,"tool_calls":[`+
				`{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"known.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
		case 2:
			sendFrame(w, `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected model request %d", n)
		}
		sendFrame(w, `[DONE]`)
	})
	scope := tools.Scope{Agent: "explore", Allowed: map[string]bool{"read": true}}
	config := explore.Config{
		Root: repo.Root(), Provider: "test", Model: client.Model(), Scope: scope,
		Runner: ExploreRunner(client, repo, nil, scope, nil),
	}
	if agentName != "explore" {
		profile := profiles.Profile{Name: agentName, Description: "reviews", Origin: "global"}
		config.Runners = map[string]explore.Runner{
			agentName: ProfileRunner(client, repo, nil, scope, nil, profile, "Review the code."),
		}
	}
	manager, err := explore.New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Wait()
	outcome, err := manager.Spawn(context.Background(), "", "call_1", explore.Spec{Agent: agentName, Description: "look", Prompt: "find the answer"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return outcome
}
