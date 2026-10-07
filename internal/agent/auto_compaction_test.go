package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/repository"
	"likha/internal/tools"
)

func TestShouldAutoCompact(t *testing.T) {
	cases := []struct {
		name   string
		tokens int64
		known  bool
		window int64
		want   bool
	}{
		{"below threshold", 84, true, 100, false},
		{"at threshold", 85, true, 100, true},
		{"full window", 100, true, 100, true},
		{"just under in a large window", 169_999, true, 200_000, false},
		{"exactly 85 percent of a large window", 170_000, true, 200_000, true},
		{"unknown estimate", 95, false, 100, false},
		{"unknown window", 95, true, 0, false},
		{"negative window", 95, true, -1, false},
		{"no tokens", 0, true, 100, false},
		{"overflowing estimate", math.MaxInt64, true, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAutoCompact(tc.tokens, tc.known, tc.window); got != tc.want {
				t.Fatalf("shouldAutoCompact(%d, %t, %d) = %t, want %t", tc.tokens, tc.known, tc.window, got, tc.want)
			}
		})
	}
}

// compactionServer plays a chat-completions provider that answers the
// summarize request (no tools, last message is the compact instruction)
// with summary, and every turn request with the next entry of turns.
type compactionServer struct {
	t       *testing.T
	summary string
	// summarizeStatus, when nonzero, fails the summarize request with it.
	summarizeStatus int
	// onSummarize runs when the summarize request arrives, before answering.
	onSummarize func(r *http.Request)
	turns       []string

	mu         sync.Mutex
	summarizes []wireRequest
	requests   []wireRequest
}

type wireMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
	ToolCalls  []struct {
		ID string `json:"id"`
	} `json:"tool_calls"`
}

type wireRequest struct {
	Tools    []json.RawMessage `json:"tools"`
	Messages []wireMessage     `json:"messages"`
	raw      string
}

func (s *compactionServer) handler(w http.ResponseWriter, r *http.Request) {
	if serveModels(s.t, w, r) {
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		s.t.Errorf("read request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body wireRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		s.t.Errorf("decode request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	body.raw = string(raw)
	last := body.Messages[len(body.Messages)-1]
	if len(body.Tools) == 0 && last.Role == "user" && strings.HasPrefix(last.Content, compactInstruction) {
		s.mu.Lock()
		s.summarizes = append(s.summarizes, body)
		s.mu.Unlock()
		if s.onSummarize != nil {
			s.onSummarize(r)
		}
		if s.summarizeStatus != 0 {
			w.WriteHeader(s.summarizeStatus)
			fmt.Fprint(w, `{"error":{"message":"summarizer unavailable"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\n", s.summary)
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":900,\"completion_tokens\":40}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	s.mu.Lock()
	index := len(s.requests)
	s.requests = append(s.requests, body)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	if index >= len(s.turns) {
		s.t.Errorf("unexpected turn request %d", index+1)
		fmt.Fprint(w, sseText("Done."))
		return
	}
	fmt.Fprint(w, s.turns[index])
}

func sseText(text string) string {
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
}

func sseReadCall(id, path string) string {
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"%s\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", id, path)
}

// compactionFixture holds what every automatic-compaction run test shares:
// a repository, the main harness and tool catalog the loop sends, and a
// client pointed at the fake provider.
type compactionFixture struct {
	repo        *repository.Repository
	harness     []model.Message
	definitions []model.ToolDefinition
	client      *model.Client
	server      *compactionServer
}

func newCompactionFixture(t *testing.T, server *compactionServer) *compactionFixture {
	t.Helper()
	root := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	harness, _ := HarnessMessages(repo, root)
	registry, _ := newToolRegistry(repo, repo.Root(), nil, RunOptions{}, func(TurnEvent) {})
	server.t = t
	httpServer := httptest.NewServer(http.HandlerFunc(server.handler))
	t.Cleanup(httpServer.Close)
	client, err := model.New(httpServer.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return &compactionFixture{repo: repo, harness: harness, definitions: registry.Definitions(tools.Scope{Agent: "main"}), client: client, server: server}
}

// estimate is the loop's own estimate of a main-turn request carrying history.
func (f *compactionFixture) estimate(t *testing.T, history []model.Message) int64 {
	t.Helper()
	request := append(append([]model.Message(nil), f.harness...), history...)
	tokens, ok := f.client.EstimateInputTokens(request, f.definitions)
	if !ok {
		t.Fatal("estimate unavailable")
	}
	return tokens
}

// windowFor returns a window at which tokens sits at exactly percent.
func windowFor(tokens int64, percent int64) int64 {
	return tokens * 100 / percent
}

func (f *compactionFixture) run(ctx context.Context, prior []model.Message, prompt string, steer <-chan string, options RunOptions) []TurnEvent {
	// Explore children report task records from their own goroutines.
	var mu sync.Mutex
	var events []TurnEvent
	RunTurnWithOptions(ctx, f.client, f.repo, f.repo.Root(), prior, prompt, nil, steer, options, func(ev TurnEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	})
	mu.Lock()
	defer mu.Unlock()
	return events
}

// bulkyPrior is an earlier exchange large enough that summarizing it brings
// a request well below the trigger.
func bulkyPrior() []model.Message {
	return []model.Message{
		{Role: "user", Content: "Earlier question"},
		{Role: "assistant", Content: strings.Repeat("Long earlier answer about the repository. ", 4000)},
	}
}

func compactionKinds(events []TurnEvent) []string {
	var kinds []string
	for _, ev := range events {
		if ev.Kind == "context" || ev.Kind == "text" || ev.Kind == "reasoning" {
			continue
		}
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func findEvent(events []TurnEvent, kind string) (TurnEvent, bool) {
	for _, ev := range events {
		if ev.Kind == kind {
			return ev, true
		}
	}
	return TurnEvent{}, false
}

func countEvents(events []TurnEvent, kind string) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func TestAutoCompactionCompactsKeepsPromptAndContinues(t *testing.T) {
	server := &compactionServer{summary: "Earlier work: the user asked a question and got a long answer.", turns: []string{sseText("Done.")}}
	f := newCompactionFixture(t, server)
	prior := bulkyPrior()
	prompt := "Now fix the bug"
	full := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	tokens := f.estimate(t, full)
	window := windowFor(tokens, 90)

	events := f.run(context.Background(), prior, prompt, nil, RunOptions{ContextWindow: window, AutoCompact: true})

	// The turn request reports no usage; the summarize call's rides on
	// auto_compacted.
	if got, want := compactionKinds(events), []string{"compacting", "auto_compacted", "done"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event kinds = %v, want %v", got, want)
	}
	compacted, _ := findEvent(events, "auto_compacted")
	if compacted.Text != server.summary || compacted.ContextTokens != tokens || compacted.ContextWindow != window {
		t.Fatalf("auto_compacted = %+v", compacted)
	}
	if compacted.Usage == nil || compacted.Usage.Prompt != 900 || compacted.Usage.Completion != 40 {
		t.Fatalf("auto_compacted usage = %+v, want the summarize request's", compacted.Usage)
	}
	wantHistory := []model.Message{
		{Role: "developer", Content: compactPrefix + server.summary},
		{Role: "user", Content: prompt},
		{Role: "developer", Content: autoCompactNote},
	}
	if !reflect.DeepEqual(compacted.History, wantHistory) {
		t.Fatalf("compacted history = %+v, want %+v", compacted.History, wantHistory)
	}

	if len(server.summarizes) != 1 {
		t.Fatalf("summarize requests = %d, want 1", len(server.summarizes))
	}
	summarize := server.summarizes[0].Messages
	if len(summarize) != len(full)+1 {
		t.Fatalf("summarize request has %d messages, want the full history plus the instruction", len(summarize))
	}
	for i, message := range full {
		if summarize[i].Role != message.Role || summarize[i].Content != message.Content {
			t.Fatalf("summarize message %d = %+v, want %+v", i, summarize[i], message)
		}
	}
	if instruction := summarize[len(summarize)-1].Content; !strings.Contains(instruction, "Emphasize: "+autoCompactFocus+".") {
		t.Fatalf("summarize instruction %q lacks the automatic focus", instruction)
	}

	if len(server.requests) != 1 {
		t.Fatalf("turn requests = %d, want 1", len(server.requests))
	}
	sent := server.requests[0].Messages[len(f.harness):]
	if len(sent) != len(wantHistory) {
		t.Fatalf("turn request history = %+v, want the compacted history", sent)
	}
	for i, message := range wantHistory {
		if sent[i].Role != message.Role || sent[i].Content != message.Content {
			t.Fatalf("turn request message %d = %+v, want %+v", i, sent[i], message)
		}
	}

	// A fresh estimate for the compacted request follows the compaction.
	var estimates []int64
	for _, ev := range events {
		if ev.Kind == "context" && ev.ContextEstimated {
			estimates = append(estimates, ev.ContextTokens)
		}
	}
	if len(estimates) != 2 || estimates[0] != tokens || estimates[1] != f.estimate(t, wantHistory) {
		t.Fatalf("estimates = %v, want the trigger then the compacted request", estimates)
	}
	done, _ := findEvent(events, "done")
	if len(done.History) != len(wantHistory)+1 || done.History[len(wantHistory)].Content != "Done." {
		t.Fatalf("done history = %+v", done.History)
	}
}

func TestAutoCompactionSummarizeSendsNoEffort(t *testing.T) {
	// The main turn runs at a chosen effort; the automatic summarize call is
	// a background request and sends none (FR-37 decision 9).
	server := &compactionServer{summary: "Brief.", turns: []string{sseText("Done.")}}
	f := newCompactionFixture(t, server)
	f.client.SetEffort(model.EffortTopLevel, "high")
	prior := bulkyPrior()
	prompt := "Now fix the bug"
	full := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	window := windowFor(f.estimate(t, full), 90)

	f.run(context.Background(), prior, prompt, nil, RunOptions{ContextWindow: window, AutoCompact: true})

	if len(server.summarizes) != 1 || len(server.requests) != 1 {
		t.Fatalf("summarizes=%d requests=%d, want 1 and 1", len(server.summarizes), len(server.requests))
	}
	if strings.Contains(server.summarizes[0].raw, "reasoning_effort") {
		t.Fatalf("summarize request carried effort: %s", server.summarizes[0].raw[:200])
	}
	if !strings.Contains(server.requests[0].raw, `"reasoning_effort":"high"`) {
		t.Fatal("fixture: the main turn request did not carry the chosen effort")
	}
}

func TestAutoCompactionDoesNotRunWhenOffOrBelowThreshold(t *testing.T) {
	prior := bulkyPrior()
	prompt := "Now fix the bug"
	full := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	cases := []struct {
		name    string
		options func(tokens int64) RunOptions
	}{
		{"84 percent", func(tokens int64) RunOptions {
			return RunOptions{ContextWindow: windowFor(tokens, 84) + 2, AutoCompact: true}
		}},
		{"opted out at 95 percent", func(tokens int64) RunOptions {
			return RunOptions{ContextWindow: windowFor(tokens, 95), AutoCompact: false}
		}},
		{"unknown window", func(int64) RunOptions {
			return RunOptions{ContextWindow: 0, AutoCompact: true}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &compactionServer{summary: "unused", turns: []string{sseText("Done.")}}
			f := newCompactionFixture(t, server)
			options := tc.options(f.estimate(t, full))
			if tc.name == "84 percent" && shouldAutoCompact(f.estimate(t, full), true, options.ContextWindow) {
				t.Fatalf("fixture window %d still triggers", options.ContextWindow)
			}
			events := f.run(context.Background(), prior, prompt, nil, options)
			if len(server.summarizes) != 0 || countEvents(events, "compacting") != 0 || countEvents(events, "auto_compacted") != 0 {
				t.Fatalf("compaction ran: summarizes=%d kinds=%v", len(server.summarizes), compactionKinds(events))
			}
			sent := server.requests[0].Messages[len(f.harness):]
			if len(sent) != len(full) {
				t.Fatalf("request was changed: %d messages, want %d", len(sent), len(full))
			}
			if _, ok := findEvent(events, "done"); !ok {
				t.Fatalf("run did not finish: %v", compactionKinds(events))
			}
		})
	}
}

func TestAutoCompactionStandsDownWhenStillOverThreshold(t *testing.T) {
	// The summary is as large as what it replaced, so the compacted request
	// stays over the trigger: one notice, and no second compaction later in
	// the run even though the next round is still over.
	big := strings.Repeat("Huge summary that does not shrink anything. ", 4000)
	server := &compactionServer{summary: big, turns: []string{sseReadCall("r1", "missing.txt"), sseText("Done.")}}
	f := newCompactionFixture(t, server)
	prior := bulkyPrior()
	prompt := "Keep going"
	full := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	window := windowFor(f.estimate(t, full), 90)

	events := f.run(context.Background(), prior, prompt, nil, RunOptions{ContextWindow: window, AutoCompact: true})

	if n := len(server.summarizes); n != 1 {
		t.Fatalf("summarize requests = %d, want exactly 1", n)
	}
	var notices []string
	for _, ev := range events {
		if ev.Kind == "notice" {
			notices = append(notices, ev.Text)
		}
	}
	if len(notices) != 1 || !strings.HasPrefix(notices[0], "Still at ~") || !strings.Contains(notices[0], "continuing without compacting again in this run. /compact with a focus may help.") {
		t.Fatalf("notices = %q, want one stand-down notice", notices)
	}
	if len(server.requests) != 2 {
		t.Fatalf("turn requests = %d, want 2", len(server.requests))
	}
	if _, ok := findEvent(events, "done"); !ok {
		t.Fatalf("run did not finish: %v", compactionKinds(events))
	}
}

func TestAutoCompactionFailureSendsOriginalRequest(t *testing.T) {
	server := &compactionServer{summarizeStatus: http.StatusInternalServerError, turns: []string{sseReadCall("r1", "missing.txt"), sseText("Done.")}}
	f := newCompactionFixture(t, server)
	prior := bulkyPrior()
	prompt := "Now fix the bug"
	full := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	window := windowFor(f.estimate(t, full), 90)

	events := f.run(context.Background(), prior, prompt, nil, RunOptions{ContextWindow: window, AutoCompact: true})

	if countEvents(events, "auto_compacted") != 0 {
		t.Fatal("a failed summarize must not replace history")
	}
	notice, ok := findEvent(events, "notice")
	if !ok || !strings.HasPrefix(notice.Text, "Automatic compaction failed: ") || !strings.HasSuffix(notice.Text, "Continuing without compacting again in this run.") {
		t.Fatalf("failure notice = %+v", notice)
	}
	if !reflect.DeepEqual(notice.History, full) {
		t.Fatalf("history after failure = %+v, want it unchanged", notice.History)
	}
	sent := server.requests[0].Messages[len(f.harness):]
	if len(sent) != len(full) || sent[len(sent)-1].Content != prompt || sent[1].Content != prior[1].Content {
		t.Fatalf("first turn request is not the original request: %d messages", len(sent))
	}
	// Stood down: the second round is still over the trigger but does not retry.
	if len(server.summarizes) != 1 {
		t.Fatalf("summarize requests = %d, want 1", len(server.summarizes))
	}
	if _, ok := findEvent(events, "done"); !ok {
		t.Fatalf("run did not finish: %v", compactionKinds(events))
	}
}

func TestAutoCompactionNothingToSummarizeSendsUnchanged(t *testing.T) {
	server := &compactionServer{summary: "unused", turns: []string{sseText("Done.")}}
	f := newCompactionFixture(t, server)
	prompt := strings.Repeat("A very long first prompt. ", 4000)
	full := []model.Message{{Role: "user", Content: prompt}}
	window := windowFor(f.estimate(t, full), 90)

	events := f.run(context.Background(), nil, prompt, nil, RunOptions{ContextWindow: window, AutoCompact: true})

	if len(server.summarizes) != 0 {
		t.Fatalf("summarize requests = %d, want none", len(server.summarizes))
	}
	notice, ok := findEvent(events, "notice")
	if !ok || !strings.HasPrefix(notice.Text, "Nothing earlier to summarize at ~90% of the context window") {
		t.Fatalf("notice = %+v", notice)
	}
	sent := server.requests[0].Messages[len(f.harness):]
	if len(sent) != 1 || sent[0].Content != prompt {
		t.Fatalf("request changed: %+v", sent)
	}
}

func TestAutoCompactionCancelLeavesHistoryUncompacted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &compactionServer{summary: "never delivered"}
	server.onSummarize = func(r *http.Request) {
		cancel()
		<-r.Context().Done()
	}
	f := newCompactionFixture(t, server)
	prior := bulkyPrior()
	prompt := "Now fix the bug"
	full := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	window := windowFor(f.estimate(t, full), 90)

	events := f.run(ctx, prior, prompt, nil, RunOptions{ContextWindow: window, AutoCompact: true})

	last := events[len(events)-1]
	if last.Kind != "error" || last.Err == nil || ctx.Err() == nil {
		t.Fatalf("last event = %+v, want the cancelled run's error", last)
	}
	if !reflect.DeepEqual(last.History, full) {
		t.Fatalf("history after cancel = %+v, want it uncompacted", last.History)
	}
	if len(server.requests) != 0 || countEvents(events, "auto_compacted") != 0 {
		t.Fatalf("cancelled compaction still continued: requests=%d kinds=%v", len(server.requests), compactionKinds(events))
	}
}

func TestAutoCompactionDeliversSteeringQueuedDuringCompaction(t *testing.T) {
	steer := make(chan string, 4)
	server := &compactionServer{summary: "Brief.", turns: []string{sseText("Done.")}}
	server.onSummarize = func(*http.Request) { steer <- "also update the docs" }
	f := newCompactionFixture(t, server)
	prior := bulkyPrior()
	prompt := "Now fix the bug"
	full := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	window := windowFor(f.estimate(t, full), 90)

	events := f.run(context.Background(), prior, prompt, steer, RunOptions{ContextWindow: window, AutoCompact: true})

	delivered, ok := findEvent(events, "steer")
	if !ok || delivered.Text != "also update the docs" {
		t.Fatalf("steer event = %+v, want delivery in the same run", delivered)
	}
	sent := server.requests[0].Messages[len(f.harness):]
	want := []string{compactPrefix + "Brief.", prompt, autoCompactNote, "also update the docs"}
	if len(sent) != len(want) {
		t.Fatalf("request = %+v, want %q", sent, want)
	}
	for i, content := range want {
		if sent[i].Content != content {
			t.Fatalf("request message %d = %q, want %q", i, sent[i].Content, content)
		}
	}
}

func TestAutoCompactionKeepsDeliveredSteeringVerbatim(t *testing.T) {
	// The run's prompt and a steering prompt delivered before the trigger
	// both follow the summary unchanged; the run's tool round is summarized.
	steer := make(chan string, 4)
	steer <- "steer before"
	big := strings.Repeat("x", 120_000)
	server := &compactionServer{summary: "Brief.", turns: []string{sseReadCall("r1", "big.txt"), sseText("Done.")}}
	f := newCompactionFixture(t, server)
	if err := os.WriteFile(filepath.Join(f.repo.Root(), "big.txt"), []byte(big), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := "Inspect big.txt"
	// The first request (prompt + steer) is far below the window; after the
	// 120 KB tool result it crosses 85%.
	small := f.estimate(t, []model.Message{{Role: "user", Content: prompt}, {Role: "user", Content: "steer before"}})
	window := small * 3

	events := f.run(context.Background(), nil, prompt, steer, RunOptions{ContextWindow: window, AutoCompact: true})

	compacted, ok := findEvent(events, "auto_compacted")
	if !ok {
		t.Fatalf("no compaction: %v", compactionKinds(events))
	}
	want := []model.Message{
		{Role: "developer", Content: compactPrefix + "Brief."},
		{Role: "user", Content: prompt},
		{Role: "user", Content: "steer before"},
		{Role: "developer", Content: autoCompactNote},
	}
	if !reflect.DeepEqual(compacted.History, want) {
		t.Fatalf("compacted history = %+v, want %+v", compacted.History, want)
	}
	if len(server.requests) != 2 {
		t.Fatalf("turn requests = %d, want 2", len(server.requests))
	}
}

func TestCompactRunLeavesInputUntouched(t *testing.T) {
	server := &compactionServer{summary: "Brief."}
	f := newCompactionFixture(t, server)
	history := []model.Message{
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "current"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a"}`}}},
		{Role: "tool", ToolCallID: "c1", Content: "a"},
		{Role: "user", Content: "steer"},
	}
	before := append([]model.Message(nil), history...)
	replacement, summary, _, _, err := compactRun(context.Background(), f.client, history, 2)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "Brief." {
		t.Fatalf("summary = %q", summary)
	}
	want := []model.Message{
		{Role: "developer", Content: compactPrefix + "Brief."},
		{Role: "user", Content: "current"},
		{Role: "user", Content: "steer"},
		{Role: "developer", Content: autoCompactNote},
	}
	if !reflect.DeepEqual(replacement, want) {
		t.Fatalf("replacement = %+v, want %+v", replacement, want)
	}
	if !reflect.DeepEqual(history, before) {
		t.Fatal("compactRun modified its input")
	}
	if _, _, _, _, err := compactRun(context.Background(), f.client, []model.Message{{Role: "user", Content: "only"}}, 0); !errors.Is(err, errNothingToCompact) {
		t.Fatalf("only kept messages: err = %v, want errNothingToCompact", err)
	}
}

// sseCall streams one tool call with args as its JSON arguments object.
func sseCall(id, name string, args any) string {
	raw, _ := json.Marshal(args)
	argsJSON, _ := json.Marshal(string(raw))
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", id, name, argsJSON)
}

func TestAutoCompactionWaitsForOpenReview(t *testing.T) {
	// The proposed edit itself pushes the conversation past the trigger,
	// so the history is over the threshold while its review is open.
	// Compaction must wait until the review settled and its result is in.
	content := strings.Repeat("z", 120_000)
	server := &compactionServer{summary: "Brief.", turns: []string{
		sseCall("e1", "edit_file", map[string]string{"path": "notes.txt", "content": content}),
		sseText("Done."),
	}}
	f := newCompactionFixture(t, server)
	prompt := "Write the notes"
	window := f.estimate(t, []model.Message{{Role: "user", Content: prompt}}) * 2

	var mu sync.Mutex
	var events []TurnEvent
	RunTurnWithOptions(context.Background(), f.client, f.repo, f.repo.Root(), nil, prompt, nil, nil, RunOptions{ContextWindow: window, AutoCompact: true}, func(ev TurnEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		if ev.Kind == "approval" && ev.Approval != nil {
			ev.Approval.Reply <- false
		}
	})

	approval, result, compacting := indexOfKind(events, "approval"), indexOfKind(events, "tool_result"), indexOfKind(events, "compacting")
	if approval < 0 || result < 0 || compacting < 0 {
		t.Fatalf("missing events: %v", compactionKinds(events))
	}
	if !(approval < result && result < compacting) {
		t.Fatalf("compaction did not wait for the review: approval=%d tool_result=%d compacting=%d", approval, result, compacting)
	}
	if _, ok := findEvent(events, "done"); !ok {
		t.Fatalf("run did not finish: %v", compactionKinds(events))
	}
}

func TestAutoCompactionKeepsInstallPlanGateOpen(t *testing.T) {
	// /install opens edits once a plan exists in the turn. That gate is run
	// state, so an edit proposed after an automatic compaction still reaches
	// review instead of the plan-first refusal.
	server := &compactionServer{summary: "Brief: plan posted, big.txt read.", turns: []string{
		sseCall("p1", "plan_update", map[string]any{"steps": []map[string]string{{"id": "1", "title": "Install the tool", "status": "pending"}}}),
		sseReadCall("r1", "big.txt"),
		sseCall("e1", "edit_file", map[string]string{"path": "notes.txt", "content": "installed"}),
		sseText("Done."),
	}}
	f := newCompactionFixture(t, server)
	if err := os.WriteFile(filepath.Join(f.repo.Root(), "big.txt"), []byte(strings.Repeat("y", 120_000)), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := "Install the tool"
	window := f.estimate(t, []model.Message{{Role: "user", Content: prompt}}) * 2

	var mu sync.Mutex
	var events []TurnEvent
	options := RunOptions{
		ContextWindow: window, AutoCompact: true, InstallMode: true,
		PlanApply: func(context.Context, []tools.PlanStep) error { return nil },
	}
	RunTurnWithOptions(context.Background(), f.client, f.repo, f.repo.Root(), nil, prompt, nil, nil, options, func(ev TurnEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		if ev.Kind == "approval" && ev.Approval != nil {
			ev.Approval.Reply <- false
		}
	})

	compacted, approval := indexOfKind(events, "auto_compacted"), indexOfKind(events, "approval")
	if compacted < 0 {
		t.Fatalf("no compaction: %v", compactionKinds(events))
	}
	if approval < compacted {
		t.Fatalf("edit after compaction did not reach review (approval=%d compacted=%d): %v", approval, compacted, compactionKinds(events))
	}
	if _, ok := findEvent(events, "done"); !ok {
		t.Fatalf("run did not finish: %v", compactionKinds(events))
	}
}

func sseTaskCall(id, prompt string) string {
	args, _ := json.Marshal(map[string]string{"agent": "explore", "description": "look", "prompt": prompt})
	argsJSON, _ := json.Marshal(string(args))
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"task\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", id, argsJSON)
}

func TestAutoCompactionNeverRunsInExploreChildren(t *testing.T) {
	// The child's second request carries a 120 KB tool result, far past the
	// main run's trigger; the main run's own requests stay below it. No
	// summarize request may be sent at all.
	server := &compactionServer{summary: "unused", turns: []string{
		sseTaskCall("t1", "inspect big.txt"),
		sseReadCall("c1", "big.txt"),
		sseText("big.txt is large"),
		sseText("Done."),
	}}
	f := newCompactionFixture(t, server)
	if err := os.WriteFile(filepath.Join(f.repo.Root(), "big.txt"), []byte(strings.Repeat("y", 120_000)), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := "Delegate the inspection"
	window := f.estimate(t, []model.Message{{Role: "user", Content: prompt}}) * 2

	events := f.run(context.Background(), nil, prompt, nil, RunOptions{
		ContextWindow: window, AutoCompact: true,
		TasksEnabled: true, SaveTask: func(explore.Record) error { return nil },
	})

	if _, ok := findEvent(events, "done"); !ok {
		t.Fatalf("run did not finish: %v", compactionKinds(events))
	}
	if len(server.requests) != 4 {
		t.Fatalf("requests = %d, want main, child, child, main", len(server.requests))
	}
	var childMessages []model.Message
	for _, message := range server.requests[2].Messages {
		childMessages = append(childMessages, model.Message{Role: message.Role, Content: message.Content})
	}
	if tokens, ok := f.client.EstimateInputTokens(childMessages, nil); !shouldAutoCompact(tokens, ok, window) {
		t.Fatalf("fixture: the child's request (%d tokens) does not cross the trigger for window %d", tokens, window)
	}
	if len(server.summarizes) != 0 || countEvents(events, "compacting") != 0 {
		t.Fatalf("a child compacted: summarizes=%d", len(server.summarizes))
	}
}
