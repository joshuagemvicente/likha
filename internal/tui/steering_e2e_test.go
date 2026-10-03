package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/repository"
	"lisa/internal/session"
)

// steerE2Emsg is the subset of a provider message body these end-to-end
// steering tests assert against.
type steerE2Emsg struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
}

// steerE2Ereq is one decoded /v1/chat/completions request.
type steerE2Ereq struct {
	Messages []steerE2Emsg `json:"messages"`
}

// steerE2Edrain empties the buffered request log the test server fills.
func steerE2Edrain(ch chan steerE2Ereq) []steerE2Ereq {
	var out []steerE2Ereq
	for len(ch) > 0 {
		out = append(out, <-ch)
	}
	return out
}

// steerE2Ewait blocks until ch fires, failing after a generous timeout so a
// wiring regression surfaces as a failure rather than a hang.
func steerE2Ewait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// steerE2Eevent receives one turn event with a timeout, so a stalled run
// fails the test instead of blocking forever.
func steerE2Eevent(t *testing.T, m *ui) tea.Msg {
	t.Helper()
	select {
	case ev, ok := <-m.events:
		if !ok {
			if m.working {
				t.Fatal("event stream closed while the run was still working")
			}
			return nil
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for a turn event (working=%t)", m.working)
		return nil
	}
}

// steerE2Edrive runs the working loop until the turn — and any turn flushed
// from its leftovers — ends, chaining every command back into the model
// exactly as the Bubble Tea runtime does.
func steerE2Edrive(t *testing.T, m *ui) {
	t.Helper()
	for {
		_, cmd := m.Update(steerE2Eevent(t, m))
		for cmd != nil {
			msg := cmd()
			_, cmd = m.Update(msg)
		}
		if !m.working {
			return
		}
	}
}

// steerE2EnewUI builds the main-screen UI these tests drive. The one stored
// entry keeps the session non-fresh so the unrelated auto-naming call never
// reaches the test server and perturbs the request log.
func steerE2EnewUI(t *testing.T, root string, repo *repository.Repository, client *model.Client) *ui {
	t.Helper()
	m := NewUI(root, repo, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, t.TempDir(), nil, session.Snapshot{ID: "steer-e2e", Entries: []session.Entry{{Role: "You", Content: "seed"}}})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	return m
}

// steerE2Esubmit types a draft and presses Enter, taking the real Update path
// a user would.
func steerE2Esubmit(m *ui, text string) {
	sendRunes(m, text)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

// A prompt queued while round 1 streams a tool call reaches the model on the
// next request, as a user message after the tool result, and its transcript
// row flips from Queued to You at delivery.
func TestSteerE2EMidStreamQueuedMessageReachesNextRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("the answer is 42"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}

	const queued = "steer me midstream"
	requests := make(chan steerE2Ereq, 8)
	round1 := make(chan struct{})
	release := make(chan struct{})
	releaseRound1 := func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body steerE2Ereq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			close(round1)
			// Hold round 1 open so the draft is queued while it streams.
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"known.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Second answer.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer releaseRound1()

	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := steerE2EnewUI(t, root, repo, client)

	steerE2Esubmit(m, "Find the answer")
	if !m.working {
		t.Fatal("submission did not start a run")
	}
	steerE2Ewait(t, round1, "the first model request")

	steerE2Esubmit(m, queued)
	if len(m.queue) != 1 || m.queue[0] != queued {
		t.Fatalf("queue = %v, want [%q]", m.queue, queued)
	}
	if len(m.input) != 0 {
		t.Fatalf("queued submission kept the draft: %q", string(m.input))
	}
	queuedRow := len(m.entries) - 1
	if m.entries[queuedRow].role != "Queued" || m.entries[queuedRow].content != queued {
		t.Fatalf("queued row = %+v", m.entries[queuedRow])
	}
	releaseRound1()
	steerE2Edrive(t, m)

	reqs := steerE2Edrain(requests)
	if len(reqs) != 2 {
		t.Fatalf("model requests = %d, want 2", len(reqs))
	}
	last := reqs[1].Messages
	if n := len(last); n < 2 || last[n-1].Role != "user" || last[n-1].Content != queued || last[n-2].Role != "tool" {
		t.Fatalf("round-2 request did not carry the queued message after the tool result: %+v", last)
	}
	delivered := false
	for _, entry := range m.entries {
		if entry.content == queued && entry.role == "You" {
			delivered = true
			break
		}
	}
	if !delivered {
		t.Fatalf("queued row did not flip to You: %+v", m.entries)
	}
	for _, e := range m.entries {
		if e.role == "Queued" {
			t.Fatalf("stale Queued row after delivery: %+v", e)
		}
	}
	if len(m.queue) != 0 {
		t.Fatalf("queue not drained after delivery: %v", m.queue)
	}
	if m.working {
		t.Fatal("run stayed working after the queued message was delivered")
	}
}

// A prompt queued during the final no-tool response keeps the run alive: the
// next provider request carries it and done only follows delivery.
func TestSteerE2EQueuedDuringFinalResponseContinuesRun(t *testing.T) {
	const queued = "one more thing"

	requests := make(chan steerE2Ereq, 8)
	round1 := make(chan struct{})
	release := make(chan struct{})
	releaseRound1 := func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body steerE2Ereq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			close(round1)
			// Hold the final, tool-free response open until the queue is set.
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"First answer.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Second answer.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer releaseRound1()

	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := steerE2EnewUI(t, "/sample", nil, client)

	steerE2Esubmit(m, "Start the turn")
	if !m.working {
		t.Fatal("submission did not start a run")
	}
	steerE2Ewait(t, round1, "the first model request")

	steerE2Esubmit(m, queued)
	if len(m.queue) != 1 || m.queue[0] != queued {
		t.Fatalf("queue = %v, want [%q]", m.queue, queued)
	}
	releaseRound1()
	steerE2Edrive(t, m)

	reqs := steerE2Edrain(requests)
	if len(reqs) != 2 {
		t.Fatalf("model requests = %d, want 2 (the queued message must continue the run)", len(reqs))
	}
	last := reqs[1].Messages
	if n := len(last); n == 0 || last[n-1].Role != "user" || last[n-1].Content != queued {
		t.Fatalf("continuation request did not end with the queued message: %+v", last)
	}
	if m.working {
		t.Fatal("done arrived before the queued message was delivered")
	}
	if m.status != "Ready" {
		t.Fatalf("run ended in status %q, want Ready (done)", m.status)
	}
	if len(m.queue) != 0 {
		t.Fatalf("queue not drained after delivery: %v", m.queue)
	}
	for _, e := range m.entries {
		if e.role == "Queued" {
			t.Fatalf("stale Queued row after delivery: %+v", e)
		}
	}
}

// Queueing then cancelling with Esc holds the queued text: no new request
// runs until Enter sends the held batch as the next turn's prompt.
func TestSteerE2EQueuedMessageAfterEscHoldsUntilEnter(t *testing.T) {
	const queued = "carry me past the cancel"

	requests := make(chan steerE2Ereq, 8)
	round1 := make(chan struct{})
	release := make(chan struct{})
	releaseRound1 := func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body steerE2Ereq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			close(round1)
			// Hold round 1 open until Esc cancels the run.
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Ran after the cancel.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer releaseRound1()

	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := steerE2EnewUI(t, "/sample", nil, client)

	steerE2Esubmit(m, "ask one")
	if !m.working {
		t.Fatal("submission did not start a run")
	}
	steerE2Ewait(t, round1, "the first model request")

	steerE2Esubmit(m, queued)
	if len(m.queue) != 1 || m.queue[0] != queued {
		t.Fatalf("queue = %v, want [%q]", m.queue, queued)
	}
	// Esc cancels; nothing else is pressed.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	steerE2Edrive(t, m)

	if reqs := steerE2Edrain(requests); len(reqs) != 1 {
		t.Fatalf("model requests after cancel = %d, want 1 (the queue must hold)", len(reqs))
	}
	if m.working {
		t.Fatal("cancel left the run working")
	}
	if len(m.queue) != 1 || m.queue[0] != queued {
		t.Fatalf("queue after cancel = %v, want [%q] held", m.queue, queued)
	}
	held := false
	for _, e := range m.entries {
		if e.role == "Queued" && e.content == queued {
			held = true
		}
	}
	if !held {
		t.Fatalf("queued row did not survive the cancel: %+v", m.entries)
	}

	// Enter sends the held batch; the queued text becomes the next prompt.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	steerE2Edrive(t, m)
	afterEnter := steerE2Edrain(requests)
	if len(afterEnter) != 1 {
		t.Fatalf("model requests after Enter = %d, want 1 (the held batch must send)", len(afterEnter))
	}
	last := afterEnter[0].Messages
	if n := len(last); n == 0 || last[n-1].Role != "user" || last[n-1].Content != queued {
		t.Fatalf("held turn did not carry the queued text as its prompt: %+v", last)
	}
	if m.working {
		t.Fatal("held turn never completed")
	}
	if len(m.queue) != 0 {
		t.Fatalf("queue survived the send: %v", m.queue)
	}
	for _, e := range m.entries {
		if e.role == "Queued" {
			t.Fatalf("stale Queued row after the send: %+v", e)
		}
	}
}
