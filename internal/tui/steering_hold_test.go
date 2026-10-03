package tui

// Regression tests for the steering-hold semantics (FR-21, user-confirmed
// 2026-10-03): a run end holds the queue, Enter sends it, an idle Esc clears
// it, steer deliveries open fresh transcript bubbles, and a dropped steer
// event can never cause a duplicate send.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
)

// An error holds the queue exactly like a cancel: rows and texts survive,
// no turn starts, and Enter sends the held message.
func TestSteerHoldErrorKeepsQueueUntilEnter(t *testing.T) {
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 1)
	m.entries = append(m.entries, entry{role: "Queued", content: "held"})
	m.queue = []string{"held"}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "error", Text: "boom",
		History: []model.Message{{Role: "user", Content: "first"}}})
	if m.working {
		t.Fatal("error left the run working")
	}
	if len(m.queue) != 1 || m.queue[0] != "held" {
		t.Fatalf("error did not hold the queue: %v", m.queue)
	}
	held := false
	for _, e := range m.entries {
		if e.role == "Queued" && e.content == "held" {
			held = true
		}
	}
	if !held {
		t.Fatalf("queued row lost on error: %+v", m.entries)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := string(m.input); got != "held" {
		t.Fatalf("held send prompt = %q, want %q", got, "held")
	}
}

// A bare Esc with an empty draft clears the held queue with a visible note.
func TestSteerHoldIdleEscClearsQueue(t *testing.T) {
	m := newKeysTestUI(t)
	m.entries = append(m.entries, entry{role: "Queued", content: "held"})
	m.queue = []string{"held"}
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m.Update(escDecayMsg{})
	if len(m.queue) != 0 {
		t.Fatalf("esc did not clear the held queue: %v", m.queue)
	}
	for _, e := range m.entries {
		if e.role == "Queued" {
			t.Fatalf("queued row survived esc: %+v", m.entries)
		}
	}
	noted := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "cleared") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("no clear note appended: %+v", m.entries)
	}
}

// The terminal reconcile backstop: a message the engine received (it is in
// the adopted history) but whose steer event the UI never saw is flipped and
// removed from the queue, so Enter cannot send it twice.
func TestSteerReconcileDroppedEventPreventsDuplicate(t *testing.T) {
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 1)
	m.priorLen = 0
	m.turnSent = map[string]int{}
	m.entries = append(m.entries, entry{role: "Queued", content: "lost event"})
	m.queue = []string{"lost event"}
	hist := []model.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "lost event"},
	}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "done", History: hist})
	if len(m.queue) != 0 {
		t.Fatalf("reconcile left the delivered message queued: %v", m.queue)
	}
	flipped := false
	for _, e := range m.entries {
		if e.role == "You" && e.content == "lost event" {
			flipped = true
		}
	}
	if !flipped {
		t.Fatalf("reconciled row not flipped: %+v", m.entries)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.queue) != 0 || len(m.input) != 0 {
		t.Fatalf("duplicate send after reconcile: queue=%v input=%q", m.queue, string(m.input))
	}
}

// The held queue is visible while idle: status hints name the count and
// Enter, and the composer placeholder explains the send/clear controls.
func TestSteerHeldQueueHints(t *testing.T) {
	m := newKeysTestUI(t)
	m.entries = append(m.entries, entry{role: "Queued", content: "held"})
	m.queue = []string{"held"}
	wide := m.statusHints(1, 1, false)
	if !strings.Contains(wide[0], "1 queued") || !strings.Contains(wide[0], "Enter send") {
		t.Fatalf("wide held hint = %q", wide[0])
	}
	narrow := m.statusHints(1, 1, true)
	if !strings.Contains(narrow[0], "1Q") {
		t.Fatalf("narrow held hint = %q", narrow[0])
	}
	bar := stripANSI(strings.Join(m.composerLines(), "\n"))
	if !strings.Contains(bar, "Enter sends the queued message") {
		t.Fatalf("held placeholder = %q", bar)
	}
}

func steerHoldHasAssistant(m *ui) bool {
	for _, e := range m.entries {
		if e.role == "Assistant" {
			return true
		}
	}
	return false
}

// A steer delivered at the post-response boundary opens a fresh assistant
// bubble: the second answer must not be glued to the first, and it must
// render after the queued prompt it answers.
func TestSteerTranscriptFreshBubbleAfterBoundaryDelivery(t *testing.T) {
	requests := make(chan steerE2Ereq, 8)
	release := make(chan struct{})
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
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer one\"}}]}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer two\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := steerE2EnewUI(t, "/sample", nil, client)

	steerE2Esubmit(m, "first")
	for !steerHoldHasAssistant(m) {
		msg := steerE2Eevent(t, m)
		if msg == nil {
			t.Fatal("run ended before the first answer streamed")
		}
		m.Update(msg)
	}
	steerE2Esubmit(m, "second")
	close(release)
	steerE2Edrive(t, m)

	reqs := steerE2Edrain(requests)
	if len(reqs) != 2 {
		t.Fatalf("model requests = %d, want 2", len(reqs))
	}
	last := reqs[1].Messages
	if n := len(last); n < 2 || last[n-2].Content != "answer one" || last[n-1].Role != "user" || last[n-1].Content != "second" {
		t.Fatalf("round 2 request wrong: %+v", last)
	}
	var assistants []string
	secondIdx, secondAnswerIdx := -1, -1
	for i, e := range m.entries {
		if e.role == "Assistant" {
			assistants = append(assistants, e.content)
		}
		if e.role == "You" && e.content == "second" {
			secondIdx = i
		}
		if e.role == "Assistant" && strings.Contains(e.content, "answer two") {
			secondAnswerIdx = i
		}
	}
	if len(assistants) != 2 {
		t.Fatalf("assistant bubbles = %q, want two fresh entries", assistants)
	}
	if secondIdx < 0 || secondAnswerIdx < secondIdx {
		t.Fatalf("second answer not in its own bubble after the queued prompt: %+v", m.entries)
	}
}

// A prompt queued before the first provider call merges into that request
// (user-confirmed): the model sees both messages in one turn, and the queued
// row flips at delivery.
func TestSteerMergeIntoFirstRequestPinned(t *testing.T) {
	requests := make(chan steerE2Ereq, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			<-release // stall the connection check so the second prompt queues pre-drain
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		var body steerE2Ereq
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"one answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := steerE2EnewUI(t, "/sample", nil, client)

	steerE2Esubmit(m, "first")
	steerE2Esubmit(m, "second")
	close(release)
	steerE2Edrive(t, m)

	reqs := steerE2Edrain(requests)
	if len(reqs) != 1 {
		t.Fatalf("model requests = %d, want 1 (merge is intended)", len(reqs))
	}
	last := reqs[0].Messages
	if n := len(last); n < 2 || last[n-2].Content != "first" || last[n-1].Role != "user" || last[n-1].Content != "second" {
		t.Fatalf("merged request = %+v, want first then second", last)
	}
	if len(m.queue) != 0 {
		t.Fatalf("queue after merge = %v", m.queue)
	}
	flipped := false
	for _, e := range m.entries {
		if e.role == "You" && e.content == "second" {
			flipped = true
		}
	}
	if !flipped {
		t.Fatalf("queued row not flipped at delivery: %+v", m.entries)
	}
}
