package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/tools"
)

// strictDriver runs the UI the way the Bubble Tea runtime does: only the
// commands Update returns are executed, and their messages are fed back in
// arrival order. Unlike steerE2Edrive it never reads m.events itself, so a
// handler that forgets to schedule the next event read stalls the test.
type strictDriver struct {
	t    *testing.T
	m    *ui
	msgs chan tea.Msg
}

func newStrictDriver(t *testing.T, m *ui) *strictDriver {
	return &strictDriver{t: t, m: m, msgs: make(chan tea.Msg, 1024)}
}

func (d *strictDriver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if msg := cmd(); msg != nil {
			d.msgs <- msg
		}
	}()
}

// send delivers one message through Update and runs what it returns.
func (d *strictDriver) send(msg tea.Msg) {
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			d.run(cmd)
		}
		return
	}
	_, cmd := d.m.Update(msg)
	d.run(cmd)
}

func (d *strictDriver) key(k tea.KeyMsg) { d.send(k) }

// until pumps returned messages until cond holds, failing on a stall.
func (d *strictDriver) until(cond func() bool, what string) {
	d.t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case msg := <-d.msgs:
			d.send(msg)
		case <-deadline:
			d.t.Fatalf("stalled waiting for %s (working=%t cancelling=%t status=%q)", what, d.m.working, d.m.cancelling, d.m.status)
		}
	}
}

const askE2ECallID = "ask_call_1"

// askE2EServer answers round 1 with an ask_user call and round 2 with
// round2; started is closed when round 2's request arrives.
func askE2EServer(t *testing.T, round2 func(w http.ResponseWriter, r *http.Request)) (*model.Client, <-chan struct{}) {
	t.Helper()
	return askE2EServerArgs(t, `{"question":"What should the audit focus on?","options":["Comprehensive","Security only"]}`, round2)
}

// askE2EServerArgs is askE2EServer with the round 1 ask_user arguments given.
func askE2EServerArgs(t *testing.T, arguments string, round2 func(w http.ResponseWriter, r *http.Request)) (*model.Client, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			call := map[string]any{"index": 0, "id": askE2ECallID, "type": "function", "function": map[string]any{
				"name":      "ask_user",
				"arguments": arguments,
			}}
			chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}, "finish_reason": "tool_calls"}}}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			return
		}
		close(started)
		round2(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client, started
}

func askE2Erecord(m *ui) (status string, ok bool) {
	for _, r := range m.toolRecords {
		if r.CallID == askE2ECallID {
			return r.Status, true
		}
	}
	return "", false
}

// The reported bug: after the answer, the Ask item stayed Running and the
// rest of the run never reached the screen.
func TestAskE2EAnswerResumesRun(t *testing.T) {
	client, _ := askE2EServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Audit finished.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	m := steerE2EnewUI(t, "/sample", nil, client)
	d := newStrictDriver(t, m)
	sendRunes(m, "audit the codebase")
	d.key(tea.KeyMsg{Type: tea.KeyEnter})

	d.until(func() bool { return m.ask.pending }, "the question to open")
	if status, _ := askE2Erecord(m); status != "awaiting answer" {
		t.Fatalf("ask item status while asking = %q, want awaiting answer", status)
	}

	d.key(tea.KeyMsg{Type: tea.KeyEnter}) // first option
	working := false
	for _, e := range m.entries {
		working = working || e.role == "Working"
	}
	if !working {
		t.Fatal("no Working row after the answer was sent")
	}

	d.until(func() bool { return !m.working }, "the run to finish after the answer")
	record := m.toolRecords[0]
	if summary, _, _ := toolSummary(record, "…", toolPreviewLines); summary != "Answered: Comprehensive" {
		t.Fatalf("ask item summary = %q (status %q), want Answered: Comprehensive", summary, record.Status)
	}
	if !permE2EhasAssistant(m, "Audit finished.") {
		t.Fatalf("round 2 text never reached the transcript: %+v", m.entries)
	}
}

// A questionnaire call runs through the real ask_user tool: the recommended
// option starts focused, a skipped page is reported, and round 2 receives one
// per-question result against the original call.
func TestAskE2EQuestionnaireAnswersReachModel(t *testing.T) {
	args := `{"questions":[` +
		`{"question":"Install method?","options":[{"label":"Homebrew"},{"label":"Official script","description":"Upstream installer","recommended":true}]},` +
		`{"question":"Default Node version?","options":[{"label":"LTS","recommended":true},{"label":"Latest"}]}]}`
	bodies := make(chan string, 1)
	client, _ := askE2EServerArgs(t, args, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Plan ready.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	m := steerE2EnewUI(t, "/sample", nil, client)
	d := newStrictDriver(t, m)
	sendRunes(m, "install nvm")
	d.key(tea.KeyMsg{Type: tea.KeyEnter})

	d.until(func() bool { return m.ask.pending }, "the questionnaire to open")
	d.key(tea.KeyMsg{Type: tea.KeyEnter}) // page 1: the focused recommended option
	d.key(tea.KeyMsg{Type: tea.KeyCtrlS}) // page 2: skip, which submits
	d.until(func() bool { return !m.working }, "the run to finish after the questionnaire")

	var body string
	select {
	case body = <-bodies:
	default:
		t.Fatal("round 2 request never arrived")
	}
	var request struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatalf("round 2 body: %v", err)
	}
	var result string
	for _, msg := range request.Messages {
		if msg.Role == "tool" && msg.ToolCallID == askE2ECallID {
			result = fmt.Sprint(msg.Content)
		}
	}
	for _, want := range []string{`"answers"`, `"answer":"Official script"`, `"choice":true`, `"question":"Default Node version?","skipped":true`} {
		if !strings.Contains(result, want) {
			t.Fatalf("tool result for %s missing %s:\n%s", askE2ECallID, want, result)
		}
	}
	if !permE2EhasAssistant(m, "Plan ready.") {
		t.Fatalf("round 2 text never reached the transcript: %+v", m.entries)
	}
}

// Esc after an answer, with a message queued, ends the run instead of
// leaving the status at "Cancelling · 1 queued".
func TestAskE2EEscAfterAnswerCancels(t *testing.T) {
	// A slow model: round 2 streams nothing. release frees the handler so
	// the server can close once the test is done.
	release := make(chan struct{})
	client, started := askE2EServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	m := steerE2EnewUI(t, "/sample", nil, client)
	d := newStrictDriver(t, m)
	sendRunes(m, "audit the codebase")
	d.key(tea.KeyMsg{Type: tea.KeyEnter})
	d.until(func() bool { return m.ask.pending }, "the question to open")
	d.key(tea.KeyMsg{Type: tea.KeyEnter})
	steerE2Ewait(t, started, "the round 2 request")

	sendRunes(m, "Is it running?")
	d.key(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.queue) != 1 {
		t.Fatalf("queue = %v, want the typed message", m.queue)
	}
	d.key(tea.KeyMsg{Type: tea.KeyEsc})
	d.until(func() bool { return !m.working }, "the cancel to finish")
	if m.cancelling || m.status != "Cancelled" {
		t.Fatalf("after cancel: cancelling=%t status=%q", m.cancelling, m.status)
	}
	if len(m.queue) != 1 {
		t.Fatalf("queue after cancel = %v, want it held", m.queue)
	}
}

// Esc while the question is still open cancels the run too.
func TestAskE2EEscWhileAskingCancels(t *testing.T) {
	client, _ := askE2EServer(t, func(w http.ResponseWriter, r *http.Request) {})
	m := steerE2EnewUI(t, "/sample", nil, client)
	d := newStrictDriver(t, m)
	sendRunes(m, "audit the codebase")
	d.key(tea.KeyMsg{Type: tea.KeyEnter})
	d.until(func() bool { return m.ask.pending }, "the question to open")
	d.key(tea.KeyMsg{Type: tea.KeyCtrlC})
	d.until(func() bool { return !m.working }, "the cancel to finish")
	if m.status != "Cancelled" || m.ask.pending {
		t.Fatalf("after cancel: status=%q ask pending=%t", m.status, m.ask.pending)
	}
}

// stuckRunUI is a UI mid-run whose run never answers: nothing sends on its
// events channel, so only a forced stop can end it.
func stuckRunUI(t *testing.T) (*ui, chan struct{}) {
	t.Helper()
	m := toolItemTestUI(t, 80)
	m.runID = 7
	m.events = make(chan agent.TurnEvent)
	abandon := make(chan struct{})
	m.abandon = abandon
	m.cancel = func() {}
	m.queue = []string{"held"}
	return m, abandon
}

func TestCancelSecondEscForceStopsRun(t *testing.T) {
	m, abandon := stuckRunUI(t)
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Fatal("first Esc armed no watchdog")
	}
	if !m.working || !m.cancelling {
		t.Fatalf("first Esc: working=%t cancelling=%t", m.working, m.cancelling)
	}
	if hints := m.statusHints(1, 1, false); !strings.Contains(hints[0], "Esc again to force stop") {
		t.Fatalf("cancelling hint = %q", hints[0])
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.working || m.cancelling || m.status != "Cancelled" {
		t.Fatalf("second Esc: working=%t cancelling=%t status=%q", m.working, m.cancelling, m.status)
	}
	select {
	case <-abandon:
	default:
		t.Fatal("force stop did not release the run goroutine")
	}
	if len(m.queue) != 1 {
		t.Fatalf("queue = %v, want it held", m.queue)
	}
	// A late event from the detached run changes nothing.
	before := len(m.entries)
	m.Update(agent.TurnEvent{RunID: 7, Kind: "text", Text: "late"})
	if len(m.entries) != before {
		t.Fatal("a late event from the detached run reached the transcript")
	}
}

func TestCancelWatchdogForceStopsRun(t *testing.T) {
	m, _ := stuckRunUI(t)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m.Update(cancelWatchdogMsg{runID: 6}) // another run's watchdog
	if !m.working {
		t.Fatal("a stale watchdog stopped the current run")
	}
	m.Update(cancelWatchdogMsg{runID: 7})
	if m.working || m.status != "Cancelled" {
		t.Fatalf("watchdog: working=%t status=%q", m.working, m.status)
	}
}

// A consent request keeps the event read alive like ask does.
func TestConsentEventKeepsReadingEvents(t *testing.T) {
	m := toolItemTestUI(t, 80)
	d := newStrictDriver(t, m)
	d.send(agent.TurnEvent{RunID: m.runID, Kind: "consent", Consent: &agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com", Reply: make(chan bool, 1)}})
	m.events <- agent.TurnEvent{RunID: m.runID, Kind: "done", History: m.history}
	d.until(func() bool { return !m.working }, "the done event after a consent request")
}

func TestEnterTogglesToolOutputInline(t *testing.T) {
	m := toolItemTestUI(t, 80)
	call := startCall(m, "c1", "run_command", `{"command":"ls"}`)
	var out strings.Builder
	out.WriteString("Exit status: 0\n")
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&out, "row %d\n", i)
	}
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: out.String()})
	endRun(m)
	index := toolEntryIndices(m)[0]
	previewRows := func() int {
		n := 0
		for _, row := range itemRows(m, index) {
			if strings.HasPrefix(row, "     row ") {
				n++
			}
		}
		return n
	}
	if n := previewRows(); n != 3 {
		t.Fatalf("collapsed rows = %d, want 3", n)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if _, ok := m.focusedToolEntry(); !ok {
		t.Fatal("Tab did not focus the tool item")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.toolInspector.open {
		t.Fatal("Enter opened the inspector instead of expanding inline")
	}
	rows := itemRows(m, index)
	if n := previewRows(); n != 20 || rows[len(rows)-1] != "     (enter to collapse)" {
		t.Fatalf("expanded rows %q", rows)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if n := previewRows(); n != 3 {
		t.Fatalf("collapsed again rows = %d, want 3", n)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.toolInspector.open {
		t.Fatal("Ctrl+O no longer opens the inspector")
	}
}
