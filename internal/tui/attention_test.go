package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/hooks"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tools"
)

// Attention signals and the opt-in ask_user auto-skip
// (specs/attention-notifications, FR-41).

const (
	testBell    = "\a"
	testOSCRepo = "\x1b]9;Likha · repo: "
)

func testOSC(event string) string { return testOSCRepo + event + "\a" }

func boolPtr(v bool) *bool { return &v }

// attentionTestUI builds a UI rooted at /work/repo whose signals go to the
// returned buffer, with a settable clock and a run in progress.
func attentionTestUI(t *testing.T, cfg providers.StoredAttentionConfig) (*ui, *bytes.Buffer, *testClock) {
	t.Helper()
	out := &bytes.Buffer{}
	m := NewUI("/work/repo", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Attention: cfg, AttentionOut: out}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	clock := &testClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	m.clock = clock.now
	m.working, m.runID = true, 1
	m.reasoningStream = -1
	m.events = make(chan agent.TurnEvent, 64)
	m.cancel = func() {}
	return m, out, clock
}

func TestAttentionSequence(t *testing.T) {
	cases := []struct {
		name          string
		bell, desktop bool
		tmux          bool
		folder        string
		event         attentionEvent
		want          string
	}{
		{"approval, both", true, true, false, "repo", attentionApproval, testBell + testOSC("approval needed")},
		{"question, both", true, true, false, "repo", attentionQuestion, testBell + testOSC("question waiting")},
		{"finished, both", true, true, false, "repo", attentionRunFinished, testBell + testOSC("run finished")},
		{"failed, both", true, true, false, "repo", attentionRunFailed, testBell + testOSC("run failed")},
		{"bell only", true, false, false, "repo", attentionApproval, testBell},
		{"desktop only", false, true, false, "repo", attentionApproval, testOSC("approval needed")},
		{"both off", false, false, false, "repo", attentionApproval, ""},
		{"tmux wraps OSC 9, bell plain", true, true, true, "repo", attentionRunFinished,
			testBell + "\x1bPtmux;\x1b\x1b]9;Likha · repo: run finished\a\x1b\\"},
		{"control bytes in the folder are dropped", false, true, false, "re\x1b]9;x\apo\u009b;", attentionApproval,
			"\x1b]9;Likha · re]9;xpo;: approval needed\a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := attentionState{bell: tc.bell, desktop: tc.desktop, tmux: tc.tmux, folder: tc.folder}
			if got := string(state.sequence(tc.event)); got != tc.want {
				t.Fatalf("sequence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAttentionTextIsBounded(t *testing.T) {
	folder := strings.Repeat("é", 200)
	got := attentionText(folder, attentionQuestion)
	if len(got) > attentionTextLimit || !strings.HasPrefix(got, "Likha · ") {
		t.Fatalf("text = %d bytes %q, want at most %d starting with Likha", len(got), got, attentionTextLimit)
	}
	if !strings.HasSuffix(got, "é") {
		t.Fatalf("text was cut inside a character: %q", got[len(got)-4:])
	}
}

func TestAttentionUsesFolderNameNotPath(t *testing.T) {
	state, invalid := newAttentionState(providers.StoredAttentionConfig{}, &bytes.Buffer{}, "/home/someone/secret/projects/repo", false)
	if invalid || state.folder != "repo" || !state.bell || !state.desktop || state.askSkip != 0 {
		t.Fatalf("defaults = %+v invalid %v", state, invalid)
	}
}

// Every approval kind and web consent signal once when they open, and the
// signal never carries the proposal's paths or commands (D6).
func TestAttentionSignalsWhenReviewsOpen(t *testing.T) {
	for _, kind := range []string{"edit", "command", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			m, out, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
			request := &agent.ApprovalRequest{Kind: kind, Title: "rm -rf /secret/path", Body: "--- a/secret.go\n+++ b/secret.go\n", Reply: make(chan bool, 1)}
			m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
			if got, want := out.String(), testBell+testOSC("approval needed"); got != want {
				t.Fatalf("signal = %q, want %q", got, want)
			}
			// Repaints and resizes while the same review stays open repeat nothing.
			m.View()
			m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
			m.View()
			if got, want := out.String(), testBell+testOSC("approval needed"); got != want {
				t.Fatalf("signal repeated: %q", got)
			}
			if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "rm -rf") {
				t.Fatalf("signal leaked proposal text: %q", out.String())
			}
		})
	}
	t.Run("web consent", func(t *testing.T) {
		m, out, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
		m.Update(agent.TurnEvent{RunID: 1, Kind: "consent", Consent: &agent.ConsentRequest{Kind: "fetch-origin", Origin: "secret.example.com", Reply: make(chan agent.ConsentReply, 1)}})
		if got, want := out.String(), testBell+testOSC("approval needed"); got != want {
			t.Fatalf("signal = %q, want %q", got, want)
		}
	})
}

// A cancelling run skips the review, so it sends no signal either.
func TestAttentionNoSignalForReviewSkippedByCancel(t *testing.T) {
	m, out, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
	m.cancelling = true
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "edit", Reply: make(chan bool, 1)}})
	if out.Len() != 0 {
		t.Fatalf("signal for a review that never opened: %q", out.String())
	}
}

func TestAttentionQuestionSignalsOnceAcrossPages(t *testing.T) {
	m, out, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
	reply := make(chan tools.AskAnswer, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "ask", Ask: &agent.AskInteraction{
		ID: "ask_1_1", CallID: "call_q", Reply: reply,
		Request: tools.AskRequest{Questions: []tools.AskQuestion{{Question: "Which database?"}, {Question: "Which port?"}}},
	}})
	want := testBell + testOSC("question waiting")
	if out.String() != want {
		t.Fatalf("signal = %q, want %q", out.String(), want)
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	askKey(m, tea.KeyMsg{Type: tea.KeyLeft})
	askType(m, "pg")
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if out.String() != want {
		t.Fatalf("page moves or answers signalled again: %q", out.String())
	}
	if strings.Contains(out.String(), "database") {
		t.Fatalf("signal leaked the question: %q", out.String())
	}
}

// A question dropped because another interaction is open never shows, so it
// does not signal.
func TestAttentionDroppedQuestionDoesNotSignal(t *testing.T) {
	m, out, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
	m.dialog.open = true
	replies := &askReplies{}
	m.openAskQuestion(tools.AskRequest{Question: "Proceed?"}, "ask_1", "call_1", replies.reply)
	if out.Len() != 0 || len(replies.got) != 1 {
		t.Fatalf("signal %q replies %d", out.String(), len(replies.got))
	}
}

func TestAttentionRunEnd(t *testing.T) {
	cases := []struct {
		name      string
		duration  time.Duration
		kind      string
		cancelled bool
		want      string
	}{
		{"short run finishes quietly", 3 * time.Second, "done", false, ""},
		{"just under the minimum", attentionRunMinimum - time.Millisecond, "done", false, ""},
		{"long run finished", 12 * time.Second, "done", false, testBell + testOSC("run finished")},
		{"long run failed", 12 * time.Second, "error", false, testBell + testOSC("run failed")},
		{"cancelled run is quiet", 12 * time.Second, "done", true, ""},
		{"short failed run is quiet", 3 * time.Second, "error", false, ""},
		{"long compaction finished", 12 * time.Second, "compacted", false, testBell + testOSC("run finished")},
		{"short compaction is quiet", 3 * time.Second, "compacted", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, out, clock := attentionTestUI(t, providers.StoredAttentionConfig{})
			m.markRunStarted()
			clock.advance(tc.duration)
			m.cancelling = tc.cancelled
			m.Update(agent.TurnEvent{RunID: 1, Kind: tc.kind, Text: "model failed", History: m.history})
			if out.String() != tc.want {
				t.Fatalf("signal = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestAttentionSettingsSilenceTheirOwnSignal(t *testing.T) {
	cases := []struct {
		name string
		cfg  providers.StoredAttentionConfig
		want string
	}{
		{"bell off", providers.StoredAttentionConfig{Bell: boolPtr(false)}, testOSC("approval needed")},
		{"desktop off", providers.StoredAttentionConfig{Desktop: boolPtr(false)}, testBell},
		{"both off", providers.StoredAttentionConfig{Bell: boolPtr(false), Desktop: boolPtr(false)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, out, _ := attentionTestUI(t, tc.cfg)
			m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "edit", Reply: make(chan bool, 1)}})
			if out.String() != tc.want {
				t.Fatalf("signal = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestAttentionFocusSuppressesSignals(t *testing.T) {
	m, out, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
	m.signalAttention(attentionApproval) // focus never reported: signal
	if out.Len() == 0 {
		t.Fatal("unknown focus suppressed the signal")
	}
	out.Reset()
	m.Update(tea.FocusMsg{})
	m.signalAttention(attentionApproval)
	if out.Len() != 0 {
		t.Fatalf("signal while focused: %q", out.String())
	}
	m.Update(tea.BlurMsg{})
	m.signalAttention(attentionApproval)
	if out.String() != testBell+testOSC("approval needed") {
		t.Fatalf("signal after blur = %q", out.String())
	}
}

// Signals write to the terminal only: the screen, transcript, draft and
// steering queue stay exactly as they were.
func TestAttentionSignalChangesNothingOnScreen(t *testing.T) {
	m, out, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
	m.input = []rune("draft text")
	m.queue = []string{"queued"}
	before := m.View()
	entries := len(m.entries)
	m.signalAttention(attentionRunFinished)
	if out.Len() == 0 {
		t.Fatal("no signal written")
	}
	if m.View() != before || len(m.entries) != entries || string(m.input) != "draft text" || len(m.queue) != 1 {
		t.Fatal("a signal changed the screen, transcript, draft or queue")
	}
}

func TestAttentionNilWriterIsSilent(t *testing.T) {
	m := toolItemTestUI(t, 80)
	m.signalAttention(attentionApproval) // must not panic with no terminal writer
}

func TestAttentionInvalidSkipLimitNotice(t *testing.T) {
	m, _, _ := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 121})
	if m.attention.askSkip != 0 {
		t.Fatalf("out-of-range limit armed auto-skip: %v", m.attention.askSkip)
	}
	found := false
	for _, e := range m.entries {
		if e.content == attentionSkipInvalidNote {
			found = true
		}
	}
	if !found {
		t.Fatal("no notice for an out-of-range auto-skip value")
	}
}

// openTimedAsk opens a single question through the ask event with a
// one-minute auto-skip and returns its reply channel.
func openTimedAsk(t *testing.T, m *ui) chan tools.AskAnswer {
	t.Helper()
	startCall(m, "call_q", "ask_user", `{"question":"Proceed?"}`)
	reply := make(chan tools.AskAnswer, 2)
	_, cmd := m.Update(agent.TurnEvent{RunID: 1, Kind: "ask", Ask: &agent.AskInteraction{
		ID: "ask_1_1", CallID: "call_q", Reply: reply, Request: tools.AskRequest{Question: "Proceed?", Options: []string{"yes", "no"}},
	}})
	if cmd == nil || m.ask.skipLeft != time.Minute {
		t.Fatalf("auto-skip not armed: left %v", m.ask.skipLeft)
	}
	return reply
}

func tickAsk(m *ui) { m.Update(askTimerMsg{generation: m.ask.generation}) }

func TestAskAutoSkipClosesAnUntouchedQuestion(t *testing.T) {
	m, _, clock := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 1})
	reply := openTimedAsk(t, m)
	if view := askViewText(m); !strings.Contains(view, "Auto-skips in 1:00 unless you answer · any key keeps it open") {
		t.Fatalf("countdown line missing:\n%s", view)
	}
	clock.advance(30 * time.Second)
	tickAsk(m)
	if !m.ask.pending {
		t.Fatal("question closed before the limit")
	}
	if view := askViewText(m); !strings.Contains(view, "Auto-skips in 0:30") {
		t.Fatalf("countdown did not advance:\n%s", view)
	}
	clock.advance(30 * time.Second)
	generation := m.ask.generation
	tickAsk(m)
	if m.ask.pending {
		t.Fatal("question still open at the limit")
	}
	select {
	case got := <-reply:
		if !got.Skipped || got.TimedOutMinutes != 1 || got.Text != "" {
			t.Fatalf("reply = %+v, want a timed-out skip", got)
		}
	default:
		t.Fatal("no reply delivered")
	}
	if note := askLastNote(m); note != "question skipped (timed out after 1 min)" {
		t.Fatalf("transcript note = %q", note)
	}
	// A late tick for the closed question delivers nothing more.
	m.Update(askTimerMsg{generation: generation})
	clock.advance(time.Minute)
	m.Update(askTimerMsg{generation: generation})
	if len(reply) != 0 {
		t.Fatalf("second delivery: %+v", <-reply)
	}
}

func TestAskAutoSkipQuestionnaireSkipsWhole(t *testing.T) {
	m, _, clock := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 2})
	reply := make(chan tools.AskAnswer, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "ask", Ask: &agent.AskInteraction{
		ID: "ask_1_1", CallID: "call_q", Reply: reply,
		Request: tools.AskRequest{Questions: []tools.AskQuestion{{Question: "Which database?"}, {Question: "Which port?"}}},
	}})
	clock.advance(2 * time.Minute)
	tickAsk(m)
	got := <-reply
	if !got.Skipped || got.TimedOutMinutes != 2 || len(got.Answers) != 0 {
		t.Fatalf("questionnaire reply = %+v, want one whole timed-out skip", got)
	}
	if note := askLastNote(m); note != "question skipped (timed out after 2 min)" {
		t.Fatalf("transcript note = %q", note)
	}
}

func TestAskAutoSkipFirstKeyStopsCountdown(t *testing.T) {
	m, _, clock := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 1})
	reply := openTimedAsk(t, m)
	askKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(askViewText(m), "Auto-skips in") {
		t.Fatal("countdown line still shown after a key")
	}
	clock.advance(5 * time.Minute)
	tickAsk(m)
	if !m.ask.pending || len(reply) != 0 {
		t.Fatal("question timed out after the user pressed a key")
	}
}

func TestAskAutoSkipEscCancelsWithoutLateSkip(t *testing.T) {
	m, _, clock := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 1})
	reply := openTimedAsk(t, m)
	generation := m.ask.generation
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.ask.pending {
		t.Fatal("Esc left the question open")
	}
	clock.advance(2 * time.Minute)
	m.Update(askTimerMsg{generation: generation})
	if len(reply) != 0 {
		t.Fatalf("late skip delivered after Esc: %+v", <-reply)
	}
}

// The countdown runs only while the dialog is on screen (spec 15).
func TestAskAutoSkipPausesWhileHidden(t *testing.T) {
	m, _, clock := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 1})
	reply := openTimedAsk(t, m)
	m.dialog.open = true // another overlay hides the question
	clock.advance(10 * time.Minute)
	tickAsk(m)
	if !m.ask.pending || m.ask.skipLeft != time.Minute {
		t.Fatalf("hidden question counted down: pending %v left %v", m.ask.pending, m.ask.skipLeft)
	}
	m.dialog.open = false
	clock.advance(59 * time.Second)
	tickAsk(m)
	if !m.ask.pending {
		t.Fatal("question closed early after reappearing")
	}
	clock.advance(time.Second)
	tickAsk(m)
	if m.ask.pending || len(reply) != 1 {
		t.Fatalf("question did not time out once visible: pending %v replies %d", m.ask.pending, len(reply))
	}
}

func TestAskWithoutLimitWaits(t *testing.T) {
	m, _, clock := attentionTestUI(t, providers.StoredAttentionConfig{})
	startCall(m, "call_q", "ask_user", `{"question":"Proceed?"}`)
	reply := make(chan tools.AskAnswer, 1)
	_, cmd := m.Update(agent.TurnEvent{RunID: 1, Kind: "ask", Ask: &agent.AskInteraction{ID: "ask_1_1", CallID: "call_q", Reply: reply, Request: tools.AskRequest{Question: "Proceed?"}}})
	if m.ask.skipLeft != 0 || strings.Contains(askViewText(m), "Auto-skips") {
		t.Fatal("auto-skip armed without a limit")
	}
	_ = cmd
	clock.advance(24 * time.Hour)
	tickAsk(m)
	if !m.ask.pending || len(reply) != 0 {
		t.Fatal("a question without a limit closed")
	}
}

func TestAskCountdownLineFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{80, 40} {
		m, _, _ := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 1})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		openTimedAsk(t, m)
		view := askViewText(m)
		if !strings.Contains(view, "Auto-skips in") {
			t.Fatalf("width %d: countdown missing:\n%s", width, view)
		}
		for i, row := range strings.Split(view, "\n") {
			if plainWidth(row) > width {
				t.Fatalf("width %d: row %d overflows (%d): %q", width, i, plainWidth(row), row)
			}
		}
	}
}

// Approvals and consent never close on a timer, whatever the auto-skip limit.
func TestAttentionApprovalsNeverTimeOut(t *testing.T) {
	m, _, clock := attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 1})
	request := &agent.ApprovalRequest{Kind: "command", Title: "go test", Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	clock.advance(time.Hour)
	tickAsk(m)
	m.Update(askTimerMsg{generation: m.ask.generation + 1})
	if m.pending != request || len(request.Reply) != 0 {
		t.Fatal("approval review closed or answered on a timer")
	}

	m, _, clock = attentionTestUI(t, providers.StoredAttentionConfig{AskSkipAfterMinutes: 1})
	consent := &agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com", Reply: make(chan agent.ConsentReply, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "consent", Consent: consent})
	clock.advance(time.Hour)
	tickAsk(m)
	if !m.consent.pending || len(consent.Reply) != 0 {
		t.Fatal("consent closed or answered on a timer")
	}
}

// Resume treats a timed-out question as skipped: no interruption note, and
// the dialog does not reopen (FR-30).
func TestAskTimedOutQuestionResumesAsSkipped(t *testing.T) {
	snapshot := session.Snapshot{Entries: []session.Entry{
		{Role: "You", Content: "ship it"},
		{Role: "Likha", Content: "Question from the model:\nProceed?"},
		{Role: "Likha", Content: "question skipped (timed out after 5 min)"},
	}}
	m := NewUI("/work/repo", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, snapshot)
	if m.ask.pending {
		t.Fatal("resume reopened the question")
	}
	for _, e := range m.entries {
		if strings.Contains(e.content, "interrupted before an answer") {
			t.Fatalf("resume reported a timed-out question as interrupted: %q", e.content)
		}
	}
}

// Each moment that needs the user signals the terminal once and fires the
// attention hook once (FR-41 with FR-45): neither channel repeats or
// replaces the other.
func TestAttentionMomentsSignalAndFireHookOnce(t *testing.T) {
	stateDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "attention.jsonl")
	recorder, _ := json.Marshal(hookShell(t, `cat >>"`+log+`"; echo >>"`+log+`"`))
	writeHooks(t, stateDir, fmt.Sprintf(`{"hooks":{"attention":[{"command":%s}]}}`, recorder))
	out := &bytes.Buffer{}
	m := NewUI("/work/repo", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, AttentionOut: out}, stateDir, nil, session.Snapshot{ID: "sess-a"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID = true, 1
	m.cancel = func() {}

	steps := []struct {
		event  agent.TurnEvent
		signal string
	}{
		{agent.TurnEvent{RunID: 1, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "edit", Reply: make(chan bool, 1)}}, "approval needed"},
		{agent.TurnEvent{RunID: 1, Kind: "consent", Consent: &agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com", Reply: make(chan agent.ConsentReply, 1)}}, "approval needed"},
		{agent.TurnEvent{RunID: 1, Kind: "ask", Ask: &agent.AskInteraction{ID: "ask_1", CallID: "call_q", Reply: make(chan tools.AskAnswer, 1), Request: tools.AskRequest{Question: "Proceed?"}}}, "question waiting"},
	}
	for _, step := range steps {
		// Each interaction opens alone, as it does in a run.
		m.pending, m.consent, m.ask = nil, consentState{}, askState{}
		out.Reset()
		runHookCmds(m, m.applyTurnEvent(step.event))
		if got, want := out.String(), testBell+testOSC(step.signal); got != want {
			t.Fatalf("%s: signal = %q, want %q", step.event.Kind, got, want)
		}
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev hooks.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("payload %q: %v", line, err)
		}
		kinds = append(kinds, ev.Attention)
	}
	if got := fmt.Sprint(kinds); got != "[review review question]" {
		t.Fatalf("attention hook kinds = %s, want [review review question]", got)
	}
}
