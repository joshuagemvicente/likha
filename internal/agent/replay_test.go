package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/profiles"
	"likha/internal/tools"
)

// replayRequest is the replay-relevant part of one model request as the
// test server decoded it.
type replayRequest struct {
	raw      string
	Messages []struct {
		Role             string      `json:"role"`
		Content          wireContent `json:"content"`
		ReasoningContent *string     `json:"reasoning_content"`
		ToolCalls        []struct {
			ID           string `json:"id"`
			ExtraContent *struct {
				Google struct {
					ThoughtSignature string `json:"thought_signature"`
				} `json:"google"`
			} `json:"extra_content"`
		} `json:"tool_calls"`
	} `json:"messages"`
}

// prompted reports whether a user message of the request contains content,
// which tells a child's requests from the parent's.
func (r replayRequest) prompted(content string) bool {
	for _, m := range r.Messages {
		if m.Role == "user" && strings.Contains(m.Content.text, content) {
			return true
		}
	}
	return false
}

// wireContent is a request message's content: a plain string, or the chunk
// list Mistral replay sends (a leading thinking chunk, then a text chunk).
type wireContent struct {
	text     string   // the string, or the text chunks joined
	thinking []string // each thinking chunk's text, in order
}

func (c *wireContent) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &c.text); err == nil {
		return nil
	}
	var chunks []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking []struct {
			Text string `json:"text"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(data, &chunks); err != nil {
		return fmt.Errorf("content is neither a string nor a chunk list: %w", err)
	}
	for _, chunk := range chunks {
		switch chunk.Type {
		case "text":
			c.text += chunk.Text
		case "thinking":
			var trace strings.Builder
			for _, item := range chunk.Thinking {
				trace.WriteString(item.Text)
			}
			c.thinking = append(c.thinking, trace.String())
		default:
			return fmt.Errorf("unexpected content chunk type %q", chunk.Type)
		}
	}
	return nil
}

// providerServer plays the shipped provider row named provider: the row's
// base URL points at a local server for the test, so model.New resolves the
// row's real replay wire and every request goes through real capture and
// encoding. respond gets each model request with its 1-based number.
func providerServer(t *testing.T, provider string, respond func(n int, request replayRequest, w http.ResponseWriter, r *http.Request)) *model.Client {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read model request: %v", err)
			return
		}
		request := replayRequest{raw: string(data)}
		if err := json.Unmarshal(data, &request); err != nil {
			t.Errorf("decode model request: %v", err)
			return
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		respond(n, request, w, r)
	}))
	t.Cleanup(server.Close)
	row := providerRow(t, provider)
	previous := model.Providers[row].BaseURL
	model.Providers[row].BaseURL = server.URL + "/v1"
	t.Cleanup(func() { model.Providers[row].BaseURL = previous })
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func providerRow(t *testing.T, name string) int {
	t.Helper()
	for i, p := range model.Providers {
		if p.Name == name {
			return i
		}
	}
	t.Fatalf("no provider row %q", name)
	return -1
}

// replayDelta is the state each provider attaches to a streamed reply: the
// reasoning text for DeepSeek, a thought signature on the first call for
// Gemini.
type replayDelta struct {
	provider  string
	reasoning string // DeepSeek reasoning_content
	signature string // Gemini thought signature
}

func (d replayDelta) reasoningFrame() string {
	if d.reasoning == "" {
		return ""
	}
	text, _ := json.Marshal(d.reasoning)
	return `{"choices":[{"delta":{"reasoning_content":` + string(text) + `}}]}`
}

// toolCall renders one streamed call, signed when the delta has a signature
// and signed is true.
func (d replayDelta) toolCall(index int, id, path string, signed bool) string {
	call := `{"index":` + strconv.Itoa(index) + `,"id":"` + id + `","type":"function","function":{"name":"read","arguments":"{\"path\":\"` + path + `\"}"}`
	if signed && d.signature != "" {
		call += `,"extra_content":{"google":{"thought_signature":"` + d.signature + `"}}`
	}
	return call + `}`
}

// A DeepSeek tool loop through the real main loop: the second request
// carries the first reply's reasoning_content on that assistant message,
// once, and the finished history keeps it for the next turn.
func TestMainLoopReplaysDeepSeekReasoning(t *testing.T) {
	repo, root := steerRepo(t)
	delta := replayDelta{provider: "deepseek", reasoning: "read known.txt first"}
	client := providerServer(t, delta.provider, func(n int, request replayRequest, w http.ResponseWriter, _ *http.Request) {
		switch n {
		case 1:
			sendFrame(w, delta.reasoningFrame())
			sendFrame(w, `{"choices":[{"delta":{"tool_calls":[`+delta.toolCall(0, "c1", "known.txt", true)+`]}}]}`)
		case 2:
			assistant := request.Messages[len(request.Messages)-2]
			if assistant.Role != "assistant" || assistant.ReasoningContent == nil || *assistant.ReasoningContent != delta.reasoning {
				t.Errorf("second request assistant = %+v, want the first reply's reasoning_content", assistant)
			}
			if got := strings.Count(request.raw, delta.reasoning); got != 1 {
				t.Errorf("reasoning appears %d times in the second request, want once: %s", got, request.raw)
			}
			sendFrame(w, `{"choices":[{"delta":{"content":"42"}}]}`)
		default:
			t.Errorf("unexpected model request %d", n)
		}
		sendFrame(w, `[DONE]`)
	})
	var final *TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, nil, RunOptions{}, func(ev TurnEvent) {
		if ev.Kind == "done" || ev.Kind == "error" {
			final = &ev
		}
	})
	if final == nil || final.Kind != "done" {
		t.Fatalf("final event = %+v, want done", final)
	}
	first := final.History[1]
	if first.Replay == nil || first.Replay.ReasoningContent == nil || *first.Replay.ReasoningContent != delta.reasoning || first.Replay.Source != client.ReplaySource() {
		t.Fatalf("history keeps replay %+v, want the reasoning from %+v", first.Replay, client.ReplaySource())
	}
}

// A Mistral tool loop through the real main loop and the shipped mistral row
// (specs/chunked-content T10): the second request carries the first reply's
// thinking as one leading thinking chunk, and Mistral rejecting that request
// is a ReplayRejectedError counting it as sent, which the interface turns
// into the /compact hint.
func TestMainLoopReplaysMistralThinkingAndTypesItsRejection(t *testing.T) {
	const trace = "read known.txt first"
	repo, root := steerRepo(t)
	client := providerServer(t, "mistral", func(n int, request replayRequest, w http.ResponseWriter, _ *http.Request) {
		switch n {
		case 1:
			sendFrame(w, `{"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"`+trace+`"}]}]}}]}`)
			sendFrame(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"known.txt\"}"}}]}}]}`)
			sendFrame(w, `[DONE]`)
		case 2:
			assistant := request.Messages[len(request.Messages)-2]
			if assistant.Role != "assistant" || len(assistant.Content.thinking) != 1 || assistant.Content.thinking[0] != trace {
				t.Errorf("second request assistant = %+v, want one leading thinking chunk holding %q", assistant, trace)
			}
			if got := strings.Count(request.raw, trace); got != 1 {
				t.Errorf("thinking appears %d times in the second request, want once: %s", got, request.raw)
			}
			http.Error(w, `{"error":{"message":"invalid thinking chunk"}}`, http.StatusBadRequest)
		default:
			t.Errorf("unexpected model request %d", n)
		}
	})
	var final *TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, nil, RunOptions{}, func(ev TurnEvent) {
		if ev.Kind == "done" || ev.Kind == "error" {
			final = &ev
		}
	})
	if final == nil || final.Kind != "error" {
		t.Fatalf("final event = %+v, want the rejection", final)
	}
	var rejected *model.ReplayRejectedError
	if !errors.As(final.Err, &rejected) || rejected.Sent != 1 || rejected.Missing != 0 {
		t.Fatalf("error = %v (%+v), want a ReplayRejectedError with one turn sent", final.Err, rejected)
	}
	first := final.History[len(final.History)-2]
	if first.Replay == nil || first.Replay.Thinking == nil || *first.Replay.Thinking != trace || first.Replay.Source != client.ReplaySource() {
		t.Fatalf("history keeps replay %+v, want the thinking from %+v", first.Replay, client.ReplaySource())
	}
}

// A malformed batch is dropped from the main loop's history. The assistant
// message keeps its unsigned DeepSeek text, so a later tool request still
// carries it, and no signed Gemini state for calls it no longer has.
func TestMainLoopMalformedBatchKeepsOnlyUnsignedReplay(t *testing.T) {
	cases := []struct {
		delta      replayDelta
		wantReplay func(*model.Client) *model.ReasoningReplay
	}{
		{replayDelta{provider: "deepseek", reasoning: "r"}, func(c *model.Client) *model.ReasoningReplay {
			text := "r"
			return &model.ReasoningReplay{Source: c.ReplaySource(), ReasoningContent: &text}
		}},
		{replayDelta{provider: "gemini", signature: "sig"}, func(*model.Client) *model.ReasoningReplay { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.delta.provider, func(t *testing.T) {
			repo, root := steerRepo(t)
			client := providerServer(t, tc.delta.provider, func(n int, _ replayRequest, w http.ResponseWriter, _ *http.Request) {
				if n != 1 {
					t.Errorf("unexpected model request %d", n)
					return
				}
				if frame := tc.delta.reasoningFrame(); frame != "" {
					sendFrame(w, frame)
				}
				sendFrame(w, `{"choices":[{"delta":{"content":"a","tool_calls":[`+
					tc.delta.toolCall(0, "dup", "known.txt", true)+`,`+tc.delta.toolCall(1, "dup", "known.txt", false)+`]},"finish_reason":"tool_calls"}]}`)
				sendFrame(w, `[DONE]`)
			})
			var failed *TurnEvent
			RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, nil, RunOptions{}, func(ev TurnEvent) {
				if ev.Kind == "error" {
					failed = &ev
				}
			})
			if failed == nil {
				t.Fatal("no error event, want the malformed batch reported")
			}
			last := failed.History[len(failed.History)-1]
			if last.Role != "assistant" || last.Content != "a" || last.ToolCalls != nil {
				t.Fatalf("history tail = %+v, want the assistant text without its malformed calls", last)
			}
			if want := tc.wantReplay(client); !last.Replay.Equal(want) {
				t.Fatalf("replay = %+v, want %+v", last.Replay, want)
			}
		})
	}
}

// Explore and profile children clip an oversize reply's content for their
// bounded transcript. A child's second request carries its own first reply's
// state: DeepSeek text whether clipped or not; a Gemini signature only when
// unclipped, and the skip value once a clip dropped it, because the call is
// in the child's current turn.
func TestChildLoopReplaysOwnStateAndDropsSignedStateWhenClipped(t *testing.T) {
	sizes := []struct {
		name    string
		content string
		clipped bool
	}{
		{"at the limit", strings.Repeat("x", exploreStreamBytes), false},
		{"over the limit", strings.Repeat("x", exploreStreamBytes+1), true},
	}
	deltas := []replayDelta{
		{provider: "deepseek", reasoning: "child plan"},
		{provider: "gemini", signature: "child-sig"},
	}
	for _, agentName := range []string{"explore", "reviewer"} {
		for _, delta := range deltas {
			for _, size := range sizes {
				t.Run(agentName+" "+delta.provider+" "+size.name, func(t *testing.T) {
					second := runChildTask(t, agentName, delta, size.content)
					reply := second.Messages[len(second.Messages)-2]
					if reply.Role != "assistant" || len(reply.Content.text) != exploreStreamBytes || len(reply.ToolCalls) != 1 {
						t.Fatalf("second request's reply = %d bytes, %d calls; want the clipped reply with its call", len(reply.Content.text), len(reply.ToolCalls))
					}
					signature := ""
					if extra := reply.ToolCalls[0].ExtraContent; extra != nil {
						signature = extra.Google.ThoughtSignature
					}
					switch {
					case delta.provider == "deepseek":
						if reply.ReasoningContent == nil || *reply.ReasoningContent != delta.reasoning || signature != "" {
							t.Fatalf("reply reasoning_content = %v, signature %q; want the child's text only", reply.ReasoningContent, signature)
						}
					case size.clipped:
						if signature != "skip_thought_signature_validator" {
							t.Fatalf("clipped reply signature = %q, want the skip value", signature)
						}
					default:
						if signature != delta.signature {
							t.Fatalf("unclipped reply signature = %q, want %q", signature, delta.signature)
						}
					}
				})
			}
		}
	}
}

// runChildTask spawns one child task named agentName on provider whose first
// reply is content plus a read call and whose second reply ends the task. It
// returns the child's second request.
func runChildTask(t *testing.T, agentName string, delta replayDelta, content string) replayRequest {
	t.Helper()
	repo, _ := steerRepo(t)
	contentJSON, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	// The handler runs on the server's goroutine; the lock hands its copy
	// of the request to the test.
	var mu sync.Mutex
	var second replayRequest
	client := providerServer(t, delta.provider, func(n int, request replayRequest, w http.ResponseWriter, _ *http.Request) {
		switch n {
		case 1:
			if frame := delta.reasoningFrame(); frame != "" {
				sendFrame(w, frame)
			}
			sendFrame(w, `{"choices":[{"delta":{"content":`+string(contentJSON)+`,"tool_calls":[`+delta.toolCall(0, "c1", "known.txt", true)+`]},"finish_reason":"tool_calls"}]}`)
		case 2:
			mu.Lock()
			second = request
			mu.Unlock()
			sendFrame(w, `{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected model request %d", n)
		}
		sendFrame(w, `[DONE]`)
	})
	scope := tools.Scope{Agent: "explore", Allowed: map[string]bool{"read": true}}
	config := explore.Config{
		Root: repo.Root(), Provider: delta.provider, Model: client.Model(), Scope: scope,
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
	if outcome.Status != explore.Completed {
		t.Fatalf("child outcome = %s (%s), want completed", outcome.Status, outcome.Reason)
	}
	mu.Lock()
	defer mu.Unlock()
	if second.raw == "" {
		t.Fatal("the child sent no second request")
	}
	return second
}

// A parent and the explore child it spawns keep their own DeepSeek state:
// the child's second request carries its own reasoning and none of the
// parent's, and the parent's next request none of the child's.
func TestChildAndParentNeverShareReplay(t *testing.T) {
	repo, root := steerRepo(t)
	const parentPlan, childPlan = "parent plan", "child plan"
	const childPrompt = "find the answer"
	parent := replayDelta{provider: "deepseek", reasoning: parentPlan}
	child := replayDelta{provider: "deepseek", reasoning: childPlan}
	var childSeen, parentSeen atomic.Bool
	client := providerServer(t, "deepseek", func(_ int, request replayRequest, w http.ResponseWriter, _ *http.Request) {
		isChild := request.prompted(childPrompt)
		hasAssistant := false
		for _, m := range request.Messages {
			hasAssistant = hasAssistant || m.Role == "assistant"
		}
		switch {
		case isChild && !hasAssistant:
			sendFrame(w, child.reasoningFrame())
			sendFrame(w, `{"choices":[{"delta":{"tool_calls":[`+child.toolCall(0, "k1", "known.txt", false)+`]}}]}`)
		case isChild:
			childSeen.Store(true)
			if !strings.Contains(request.raw, childPlan) || strings.Contains(request.raw, parentPlan) {
				t.Errorf("child request = %s, want its own reasoning only", request.raw)
			}
			sendFrame(w, `{"choices":[{"delta":{"content":"the answer is 42"}}]}`)
		case !hasAssistant:
			sendFrame(w, parent.reasoningFrame())
			args, _ := json.Marshal(map[string]string{"agent": "explore", "description": "look", "prompt": childPrompt})
			argsJSON, _ := json.Marshal(string(args))
			sendFrame(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"task","arguments":`+string(argsJSON)+`}}]}}]}`)
		default:
			parentSeen.Store(true)
			if !strings.Contains(request.raw, parentPlan) || strings.Contains(request.raw, childPlan) {
				t.Errorf("parent request = %s, want its own reasoning only", request.raw)
			}
			sendFrame(w, `{"choices":[{"delta":{"content":"done"}}]}`)
		}
		sendFrame(w, `[DONE]`)
	})
	options := RunOptions{TasksEnabled: true, SaveTask: func(explore.Record) error { return nil }}
	var final *TurnEvent
	RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, nil, options, func(ev TurnEvent) {
		if ev.Kind == "done" || ev.Kind == "error" {
			final = &ev
		}
	})
	if final == nil || final.Kind != "done" {
		t.Fatalf("final event = %+v, want done", final)
	}
	if !childSeen.Load() || !parentSeen.Load() {
		t.Fatalf("child second request seen %v, parent second request seen %v; want both", childSeen.Load(), parentSeen.Load())
	}
}

// Compaction and session naming are tool-less background requests and send
// no replay state (spec decision 8 as amended): a provider that rejected the
// session's replay state cannot reject the summary meant to recover from it,
// so their 400 is a plain provider error without the /compact hint. The
// compacted history keeps no replay state either.
func TestBackgroundRequestsSendNoReplay(t *testing.T) {
	requests := []struct {
		name string
		run  func(client *model.Client, history []model.Message) error
	}{
		{"compaction", func(client *model.Client, history []model.Message) error {
			replacement, _, _, _, err := CompactHistoryUsage(context.Background(), client, history, "", nil)
			for _, m := range replacement {
				if m.Replay != nil {
					t.Errorf("compacted history carries replay state: %+v", m)
				}
			}
			return err
		}},
		{"session naming", func(client *model.Client, history []model.Message) error {
			_, _, _, err := GenerateSessionNameUsage(context.Background(), client, history)
			return err
		}},
	}
	for _, provider := range []string{"deepseek", "gemini", "mistral"} {
		for _, request := range requests {
			t.Run(provider+" "+request.name, func(t *testing.T) {
				var mu sync.Mutex
				var bodies []string
				reject := false
				client := providerServer(t, provider, func(_ int, r replayRequest, w http.ResponseWriter, _ *http.Request) {
					mu.Lock()
					bodies = append(bodies, r.raw)
					rejectNow := reject
					mu.Unlock()
					if rejectNow {
						http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
						return
					}
					sendFrame(w, `{"choices":[{"delta":{"content":"Fix the parser"}}]}`)
					sendFrame(w, `[DONE]`)
				})
				history := ownReplayHistory(t, client, provider)
				if err := request.run(client, history); err != nil {
					t.Fatalf("%s: %v", request.name, err)
				}
				mu.Lock()
				reject = true
				mu.Unlock()
				err := request.run(client, history)
				var rejected *model.ReplayRejectedError
				if err == nil || errors.As(err, &rejected) {
					t.Fatalf("400 error = %v, want a plain provider error", err)
				}
				mu.Lock()
				defer mu.Unlock()
				for _, body := range bodies {
					for _, field := range []string{"reasoning_content", "extra_content", "thought_signature", `"thinking"`, earlierThinking} {
						if strings.Contains(body, field) {
							t.Fatalf("%s request sends %s: %s", request.name, field, body)
						}
					}
				}
			})
		}
	}
}

// earlierThinking is the Mistral thinking ownReplayHistory records.
const earlierThinking = "earlier thinking"

// ownReplayHistory is one finished tool turn that client issued, carrying
// the provider's own replay state: reasoning text on DeepSeek, a thought
// signature on Gemini, a thinking chunk's text on Mistral.
func ownReplayHistory(t *testing.T, client *model.Client, provider string) []model.Message {
	t.Helper()
	var reasoning *string
	signature := ""
	switch provider {
	case "deepseek":
		text := "earlier reasoning"
		reasoning = &text
	case "gemini":
		signature = "sig"
	}
	replay, err := model.NewReasoningReplay(client.ReplaySource(), reasoning, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if provider == "mistral" {
		thinking := earlierThinking
		replay.Thinking = &thinking
	}
	return []model.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", Replay: replay, ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a.go"}`, ThoughtSignature: signature}}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
		{Role: "assistant", Content: "answer", Replay: replay},
	}
}

// Steer now keeps the visible text of a stopped request and, on DeepSeek,
// the reasoning streamed so far as replay text, so the next tool request
// still carries reasoning_content on every earlier turn. Gemini keeps no
// signed state, and a stop before any text keeps no message on either.
func TestSteerNowKeepsOnlyUnsignedPartialReasoning(t *testing.T) {
	const reasoning = "half a thought"
	cases := []struct {
		provider      string
		text          string // "" stops after reasoning only
		wantReasoning bool
	}{
		{"deepseek", "Partial", true},
		{"gemini", "Partial", false},
		{"deepseek", "", false},
		{"gemini", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.provider+" text "+strconv.Quote(tc.text), func(t *testing.T) {
			repo, root := steerRepo(t)
			steer := make(chan string, 1)
			steerNow := make(chan struct{}, 1)
			delta := replayDelta{provider: tc.provider, reasoning: reasoning}
			var mu sync.Mutex
			var second replayRequest
			client := providerServer(t, tc.provider, func(n int, request replayRequest, w http.ResponseWriter, r *http.Request) {
				if n == 1 {
					sendFrame(w, delta.reasoningFrame())
					if tc.text != "" {
						sendFrame(w, `{"choices":[{"delta":{"content":"`+tc.text+`"}}]}`)
					}
					<-r.Context().Done()
					return
				}
				mu.Lock()
				second = request
				mu.Unlock()
				sendFrame(w, `{"choices":[{"delta":{"content":"OK."}}]}`)
				sendFrame(w, `[DONE]`)
			})
			trigger := "text"
			if tc.text == "" {
				trigger = "reasoning"
			}
			var interrupted *TurnEvent
			sent := false
			RunTurnWithOptions(context.Background(), client, repo, root, nil, "start", nil, steer, RunOptions{SteerNow: steerNow}, func(ev TurnEvent) {
				if ev.Kind == trigger && !sent {
					sent = true
					steer <- "change course"
					steerNow <- struct{}{}
				}
				if ev.Kind == "stream_interrupted" {
					interrupted = &ev
				}
			})
			if interrupted == nil {
				t.Fatal("no stream_interrupted event")
			}
			var kept *model.Message
			for i, m := range interrupted.History {
				if m.Role == "assistant" {
					kept = &interrupted.History[i]
				}
			}
			if tc.text == "" {
				if kept != nil {
					t.Fatalf("a stop before any text kept %+v", kept)
				}
				return
			}
			if kept == nil || kept.Content != tc.text || kept.Reasoning != "" {
				t.Fatalf("kept message = %+v, want the visible text only", kept)
			}
			mu.Lock()
			defer mu.Unlock()
			resent := second.Messages[len(second.Messages)-2]
			if resent.Role != "assistant" || resent.Content.text != tc.text {
				t.Fatalf("resent message = %+v, want the partial assistant text", resent)
			}
			got := resent.ReasoningContent
			if tc.wantReasoning != (got != nil) || (got != nil && *got != reasoning) {
				t.Fatalf("resent reasoning_content = %v, want it sent: %v", got, tc.wantReasoning)
			}
		})
	}
}
