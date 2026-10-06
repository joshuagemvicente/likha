package tui

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
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/transcript"
)

// steerNowUI is a working main-screen UI with live steer channels, as
// startTurnDisplay leaves them, without a running engine.
func steerNowUI(t *testing.T) *ui {
	t.Helper()
	m := newKeysTestUI(t)
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 4)
	m.steer = make(chan string, 64)
	m.steerNow = make(chan struct{}, 1)
	m.status = "Waiting for model"
	return m
}

func steerSignalled(m *ui) bool {
	select {
	case <-m.steerNow:
		return true
	default:
		return false
	}
}

// Ctrl+Enter (the LF byte, reported ctrl+j) with a draft during a run queues
// the draft, hands it to the engine, and signals steer now.
func TestSteerNowQueuesDraftAndSignals(t *testing.T) {
	m := steerNowUI(t)
	m.history = []model.Message{{Role: "user", Content: "prior"}}
	m.input = []rune("go left\ninstead")
	m.edit.endCaret(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if len(m.input) != 0 {
		t.Fatalf("draft not cleared: %q", string(m.input))
	}
	if len(m.queue) != 1 || m.queue[0] != "go left instead" {
		t.Fatalf("queue = %v, want the flattened draft", m.queue)
	}
	if last := m.entries[len(m.entries)-1]; last.role != "Queued" || last.content != "go left instead" {
		t.Fatalf("queued row = %+v", last)
	}
	if got := <-m.steer; got != "go left instead" {
		t.Fatalf("steer channel carried %q", got)
	}
	if !steerSignalled(m) {
		t.Fatal("steer-now signal not sent")
	}
	if m.status != "Steering…" || !m.steering {
		t.Fatalf("status = %q steering=%t, want Steering…", m.status, m.steering)
	}
	if len(m.history) != 1 {
		t.Fatalf("steer changed history before delivery: %+v", m.history)
	}
	if hist := m.promptHistory.all(); len(hist) == 0 || hist[len(hist)-1] != "go left instead" {
		t.Fatalf("prompt history = %v", hist)
	}
}

// Repeated presses while a stop is under way never block and leave one
// pending signal.
func TestSteerNowRepeatedPressesShareOneSignal(t *testing.T) {
	m := steerNowUI(t)
	for _, text := range []string{"one", "two"} {
		sendRunes(m, text)
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	}
	if len(m.queue) != 2 {
		t.Fatalf("queue = %v", m.queue)
	}
	if !steerSignalled(m) || steerSignalled(m) {
		t.Fatal("want exactly one buffered steer-now signal")
	}
}

// Alt+Return stays the newline key inside a running draft.
func TestSteerNowAltEnterInsertsNewlineWhileWorking(t *testing.T) {
	m := steerNowUI(t)
	sendRunes(m, "a")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if string(m.input) != "a\n" {
		t.Fatalf("alt+enter while working = %q", string(m.input))
	}
	if len(m.queue) != 0 || steerSignalled(m) {
		t.Fatal("alt+enter steered")
	}
}

// While idle the LF family still inserts a newline.
func TestSteerNowIdleCtrlJInsertsNewline(t *testing.T) {
	m := newKeysTestUI(t)
	sendRunes(m, "a")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if string(m.input) != "a\n" || len(m.queue) != 0 {
		t.Fatalf("idle ctrl+j = %q queue=%v", string(m.input), m.queue)
	}
}

// A refused slash command keeps the draft and steers nothing; // steers the
// literal remainder.
func TestSteerNowDraftRules(t *testing.T) {
	m := steerNowUI(t)
	m.input = []rune("/compact") // set directly: typing / opens the command popup
	m.edit.endCaret(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if string(m.input) != "/compact" || len(m.queue) != 0 || steerSignalled(m) {
		t.Fatalf("refusal: draft=%q queue=%v", string(m.input), m.queue)
	}
	if last := m.entries[len(m.entries)-1]; last.role != "Error" || last.content != runDraftCommandRefusal {
		t.Fatalf("refusal entry = %+v", last)
	}
	if m.status == "Steering…" {
		t.Fatal("refused draft set the steering status")
	}
	m.input = []rune("//x")
	m.edit.endCaret(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if len(m.queue) != 1 || m.queue[0] != "x" || !steerSignalled(m) {
		t.Fatalf("//x steered %v", m.queue)
	}
}

// An empty draft with a queue only signals; empty with an empty queue is a
// no-op.
func TestSteerNowEmptyDraft(t *testing.T) {
	m := steerNowUI(t)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if steerSignalled(m) || m.status != "Waiting for model" || len(m.input) != 0 {
		t.Fatalf("empty draft, empty queue: status=%q input=%q", m.status, string(m.input))
	}
	m.entries = append(m.entries, entry{role: "Queued", content: "held"})
	m.queue = []string{"held"}
	rows := len(m.entries)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if !steerSignalled(m) {
		t.Fatal("empty draft with a queue did not signal")
	}
	if len(m.entries) != rows || len(m.queue) != 1 {
		t.Fatalf("empty-draft steer added rows or queue items: %v", m.queue)
	}
}

// The chord is inert while an approval is pending or a popup is open.
func TestSteerNowInertWhileReviewingOrPopupOpen(t *testing.T) {
	m := steerNowUI(t)
	tuiKeyApproval(m, "command", "Shell command", "touch marker")
	m.input = []rune("steer")
	m.edit.endCaret(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if len(m.queue) != 0 || steerSignalled(m) || string(m.input) != "steer" {
		t.Fatalf("reviewing: queue=%v input=%q", m.queue, string(m.input))
	}

	m = steerNowUI(t)
	sendRunes(m, "@fi")
	m.mention.open, m.mention.matches = true, []string{"file-a"}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if len(m.queue) != 0 || steerSignalled(m) {
		t.Fatalf("popup open steered: %v", m.queue)
	}
}

// A press while a tool executes queues and names the wait; the signal is
// still sent (the engine discards it at the boundary).
func TestSteerNowWhileToolRunsNamesTheWait(t *testing.T) {
	m := steerNowUI(t)
	m.liveToolCalls = map[string]bool{"call_1": true}
	m.status = "Reading repository"
	sendRunes(m, "after the tool")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.status != "Steer waits for the running tool" {
		t.Fatalf("status = %q", m.status)
	}
	if len(m.queue) != 1 || m.entries[len(m.entries)-1].role != "Queued" {
		t.Fatalf("queue = %v", m.queue)
	}
}

// During compaction (no steer channels) and while cancelling, the message is
// queued and held, and nothing signals.
func TestSteerNowCompactionAndCancellingHold(t *testing.T) {
	m := steerNowUI(t)
	m.steer, m.steerNow = nil, nil
	m.status = "Compacting…"
	sendRunes(m, "later")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if len(m.queue) != 1 || m.status != "Compacting…" || m.steering {
		t.Fatalf("compaction: queue=%v status=%q", m.queue, m.status)
	}

	m = steerNowUI(t)
	m.cancelling = true
	m.status = "Cancelling"
	sendRunes(m, "held")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if len(m.queue) != 1 || steerSignalled(m) || m.status != "Cancelling" {
		t.Fatalf("cancelling: queue=%v status=%q", m.queue, m.status)
	}
}

// The steering status lasts until the fresh request produces output.
func TestSteerNowStatusEndsOnOutput(t *testing.T) {
	for _, kind := range []string{"text", "reasoning"} {
		m := steerNowUI(t)
		sendRunes(m, "steer")
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
		m.Update(agent.TurnEvent{RunID: 1, Kind: kind, Text: "x"})
		if m.status != "Waiting for model" || m.steering {
			t.Fatalf("%s: status = %q steering=%t", kind, m.status, m.steering)
		}
	}
}

// stream_interrupted keeps the partial answer, appends the note, closes the
// stream, and the following steer event and text open fresh entries.
func TestStreamInterruptedKeepsPartialAndAppendsNote(t *testing.T) {
	m := steerNowUI(t)
	m.spendKnown = true
	m.history = []model.Message{{Role: "user", Content: "first"}}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "text", Text: "Partial ans"})
	m.entries = append(m.entries, entry{role: "Queued", content: "steer"})
	m.queue = []string{"steer"}
	interrupted := []model.Message{{Role: "user", Content: "first"}, {Role: "assistant", Content: "Partial ans"}}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "stream_interrupted", History: interrupted})
	if len(m.history) != 2 || m.history[1].Content != "Partial ans" {
		t.Fatalf("history not adopted: %+v", m.history)
	}
	if !m.spendEstimated {
		t.Fatal("unreported usage did not mark spend approximate")
	}
	if m.streaming != -1 || m.streamBuf.Len() != 0 {
		t.Fatal("stream state not reset")
	}
	steered := append(append([]model.Message(nil), interrupted...), model.Message{Role: "user", Content: "steer"})
	m.Update(agent.TurnEvent{RunID: 1, Kind: "steer", Text: "steer", History: steered})
	m.Update(agent.TurnEvent{RunID: 1, Kind: "text", Text: "Fresh"})
	var got []entry
	for _, e := range m.entries {
		if e.role == "Assistant" || e.role == "Likha" || e.role == "You" || e.role == "Queued" {
			got = append(got, e)
		}
	}
	want := []entry{{"Assistant", "Partial ans"}, {"Likha", streamInterruptedNote}, {"You", "steer"}, {"Assistant", "Fresh"}}
	if len(got) < len(want) {
		t.Fatalf("entries = %+v", got)
	}
	got = got[len(got)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries tail = %+v, want %+v", got, want)
		}
	}
}

// An interrupted stream that produced no visible text leaves no empty
// bubble; reported usage leaves spend exactness alone.
func TestStreamInterruptedDropsEmptyBubble(t *testing.T) {
	m := steerNowUI(t)
	m.entries = append(m.entries, entry{role: "Assistant"})
	m.streaming = len(m.entries) - 1
	before := len(m.entries)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "stream_interrupted", History: m.history, Usage: &model.RequestUsage{Prompt: 10, PromptSeen: true}})
	if len(m.entries) != before {
		t.Fatalf("entries = %d, want %d (empty bubble swapped for the note)", len(m.entries), before)
	}
	if last := m.entries[len(m.entries)-1]; last.role != "Likha" || last.content != streamInterruptedNote {
		t.Fatalf("last entry = %+v", last)
	}
	for _, e := range m.entries {
		if e.role == "Assistant" && e.content == "" {
			t.Fatal("empty assistant bubble survived")
		}
	}
	if m.spendEstimated {
		t.Fatal("reported usage marked spend approximate")
	}
}

// The run options carry the turn's steer-now channel.
func TestSteerNowChannelReachesRunOptions(t *testing.T) {
	m := steerNowUI(t)
	if m.toolRunOptions(1).SteerNow == nil {
		t.Fatal("run options lack the steer-now channel")
	}
}

// End to end against the real engine: a steer-now during a streaming answer
// stops it, the next request carries the partial answer then the steer, the
// transcript reads partial → note → You → fresh answer, one turn footer
// closes the run, and the store holds the partial text and the note.
func TestSteerNowE2EStopsStreamAndContinuesRun(t *testing.T) {
	const steer = "actually, answer in French"
	requests := make(chan steerE2Ereq, 8)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body steerE2Ereq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Partial answer\"}}]}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done() // never finishes on its own
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Réponse.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	// Registered before the UI's cancel cleanup so it runs after it: the
	// held round-1 handler only returns once the run context is cancelled.
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Entries = []session.Entry{{Role: "You", Content: "seed"}}
	m := NewUI(root, nil, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})

	steerE2Esubmit(m, "Explain")
	// Apply events directly (no command chaining: a chained waitEvent would
	// swallow events) until the partial answer is on screen.
	for streamed := false; !streamed; {
		m.Update(steerE2Eevent(t, m))
		for _, e := range m.entries {
			streamed = streamed || e.role == "Assistant" && e.content == "Partial answer"
		}
	}
	sendRunes(m, steer)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	steerE2Edrive(t, m)

	reqs := steerE2Edrain(requests)
	if len(reqs) != 2 {
		t.Fatalf("model requests = %d, want 2", len(reqs))
	}
	last := reqs[1].Messages
	n := len(last)
	if n < 2 || last[n-2].Role != "assistant" || last[n-2].Content != "Partial answer" || last[n-1].Role != "user" || last[n-1].Content != steer {
		t.Fatalf("second request tail = %+v", last)
	}
	if m.status != "Ready" {
		t.Fatalf("status = %q, want Ready", m.status)
	}
	var tail []entry
	footers := 0
	for _, e := range m.entries {
		switch e.role {
		case transcript.TurnRole:
			footers++
		case "Assistant", "Likha", "You", "Queued":
			tail = append(tail, e)
		}
	}
	want := []entry{{"Assistant", "Partial answer"}, {"Likha", streamInterruptedNote}, {"You", steer}, {"Assistant", "Réponse."}}
	if len(tail) < len(want) {
		t.Fatalf("entries = %+v", tail)
	}
	tail = tail[len(tail)-len(want):]
	for i := range want {
		if tail[i] != want[i] {
			t.Fatalf("transcript tail = %+v, want %+v", tail, want)
		}
	}
	if footers != 1 {
		t.Fatalf("turn footers = %d, want 1", footers)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundPartial, foundNote := false, false
	for _, e := range saved.Entries {
		foundPartial = foundPartial || e.Role == "Assistant" && e.Content == "Partial answer"
		foundNote = foundNote || e.Role == "Likha" && e.Content == streamInterruptedNote
	}
	if !foundPartial || !foundNote {
		t.Fatalf("stored entries lack the partial answer or note: %+v", saved.Entries)
	}
}

// The working placeholder names both run-draft keys and never overflows.
func TestSteerNowWorkingPlaceholder(t *testing.T) {
	for _, width := range []int{40, 56, 80} {
		m := overflowUI(t, "rounded", width, 24)
		m.caretOn = false
		m.working = true
		rows := m.composerLines()
		for _, row := range rows {
			if w := len([]rune(stripANSI(row))); w > width {
				t.Fatalf("%d columns: row overflows (%d): %q", width, w, stripANSI(row))
			}
		}
		if width == 80 && !strings.Contains(stripANSI(strings.Join(rows, "\n")), "Enter queues · Ctrl+Enter steers now") {
			t.Fatalf("80 columns placeholder lacks the keys: %q", stripANSI(strings.Join(rows, "\n")))
		}
	}
}
