package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tools"
	"likha/internal/webtools"
)

// interruptedNotes counts the conversation events that record a question
// interrupted before an answer.
func interruptedNotes(m *ui) int {
	count := 0
	for _, e := range m.entries {
		if strings.Contains(e.content, "Question interrupted before an answer") {
			count++
		}
	}
	return count
}

// Esc and Ctrl+C on a question during a run both cancel the run and clear
// the question (specs/ask-user): exactly one interrupted note, and a later
// Enter cannot answer the closed question.
func TestAskCancelKeysClearTheQuestion(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlC}, {Type: tea.KeyEsc}} {
		t.Run(key.String(), func(t *testing.T) {
			m := newFlowUI(t)
			beginFlowRun(m)
			reply := openFlowQuestion(t, m)
			m.Update(key)
			if !m.cancelling {
				t.Fatalf("%s did not cancel the run", key)
			}
			if m.askVisible() {
				t.Fatalf("question still answerable after %s", key)
			}
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m.Update(agent.TurnEvent{RunID: 1, Kind: "done", History: m.history})
			if got := interruptedNotes(m); got != 1 {
				t.Fatalf("interrupted note recorded %d times, want 1", got)
			}
			if len(reply) != 0 {
				t.Fatalf("cancelled question sent an answer: %+v", <-reply)
			}
			if hasEntry(m, "Likha", "ask answered") {
				t.Fatal("cancelled question recorded as answered")
			}
		})
	}
}

// Ctrl+C during web consent cancels the run and closes the dialog without
// answering it: the user made no decision, so the waiting web tool ends on
// the run's cancellation instead of a decline, and no later Enter can grant
// the origin.
func TestConsentCtrlCClosesWithoutDeciding(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	reply := openFlowConsent(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // focus Allow
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.cancelling {
		t.Fatal("Ctrl+C did not cancel the run")
	}
	if m.consentVisible() {
		t.Fatal("consent still visible while the run cancels")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-reply:
		t.Fatalf("cancelled consent was answered with %+v; only the run context may end the wait", got)
	default:
	}
	if m.webGranted((&agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com"}).ScopeKey()) {
		t.Fatal("cancelled consent granted the origin")
	}
	if hasEntry(m, "Likha", "Web consent declined") {
		t.Fatal("cancelled consent was recorded as a decline")
	}
}

// A web tool parked on consent when the run is cancelled reports the
// cancellation, never a decline: a decline would reach the model as an
// ordinary tool result claiming the user refused web access. Repeated
// because the old reply-then-cancel order lost the race only sometimes.
func TestConsentCancelEndsToolWithCancellation(t *testing.T) {
	for i := 0; i < 50; i++ {
		m := newFlowUI(t)
		beginFlowRun(m)
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		type outcome struct {
			allowed bool
			err     error
		}
		done := make(chan outcome, 1)
		ask := m.fetchConsent(m.events, m.runID)
		go func() {
			allowed, err := ask(ctx, webtools.ConsentScope{Kind: "fetch-origin", Origin: "example.com", URL: "https://example.com/"})
			done <- outcome{allowed, err}
		}()
		select {
		case event := <-m.events:
			m.Update(event)
		case got := <-done:
			t.Fatalf("consent hook returned before asking: allowed %t err %v", got.allowed, got.err)
		}
		if !m.consentVisible() {
			t.Fatal("consent did not open")
		}
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		got := <-done
		if got.allowed || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("iteration %d: consent after cancel = allowed %t err %v, want the run's cancellation", i, got.allowed, got.err)
		}
	}
}

// The production fetch consent hook carries a reply channel, so Allow
// reaches the waiting tool as a grant instead of failing before the dialog
// opens.
func TestFetchConsentHookAllowGrants(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	type outcome struct {
		allowed bool
		err     error
	}
	done := make(chan outcome, 1)
	ask := m.fetchConsent(m.events, m.runID)
	go func() {
		allowed, err := ask(context.Background(), webtools.ConsentScope{Kind: "fetch-origin", Origin: "example.com", URL: "https://example.com/"})
		done <- outcome{allowed, err}
	}()
	select {
	case event := <-m.events:
		m.Update(event)
	case got := <-done:
		t.Fatalf("consent hook returned before asking: allowed %t err %v", got.allowed, got.err)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-done; !got.allowed || got.err != nil {
		t.Fatalf("Allow = allowed %t err %v, want a grant", got.allowed, got.err)
	}
}

// Consent arrives from inside a running web tool, whose tool_start already
// replaced the Working row; answering keeps the tool's dot ticking instead
// of adding a Working row under the still-running call.
func TestConsentAnswerKeepsLiveToolItem(t *testing.T) {
	m := toolItemTestUI(t, 80)
	m.cancel = func() {}
	m.showActivity()
	startCall(m, "call_web", "web_fetch", `{"url":"https://example.com/"}`)
	if m.hasActivity() {
		t.Fatal("tool_start left the Working row")
	}
	statusBefore := m.status
	openFlowConsent(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.hasActivity() {
		t.Fatal("answering consent added a Working row under the running tool")
	}
	if m.status != statusBefore {
		t.Fatalf("status after consent = %q, want %q", m.status, statusBefore)
	}
	if m.activityArmed != m.activityGeneration+1 {
		t.Fatalf("tool dot clock stopped: armed=%d generation=%d", m.activityArmed, m.activityGeneration)
	}
}

// Tab and Shift+Tab move consent focus between the buttons, as ←/→ do.
func TestConsentTabTogglesFocus(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	reply := openFlowConsent(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !m.consent.allowFocused {
		t.Fatal("Tab from Decline did not focus Allow")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.consent.allowFocused {
		t.Fatal("Shift+Tab from Allow did not focus Decline")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-reply; !got.Allowed {
		t.Fatal("Enter after Tab to Allow declined")
	}
}

// Consent replaces historical inspection surfaces like an approval does, so
// neither inspector stays open, steals the mouse, or hides the dialog.
func TestConsentClosesInspectors(t *testing.T) {
	m := toolItemTestUI(t, 80)
	m.cancel = func() {}
	call := startCall(m, "call_read", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Content: "body"})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.toolInspectionVisible() {
		t.Fatal("tool inspector did not open")
	}
	openFlowConsent(t, m)
	if m.toolInspector.open || m.toolInspectionVisible() {
		t.Fatal("tool inspector stayed open under consent")
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Web consent required") {
		t.Fatalf("consent dialog not shown: %s", view)
	}

	m = newFlowUI(t)
	beginFlowRun(m)
	m.agentInspector = agentInspectionState{open: true, sessionID: m.snapshot.ID, cursor: -1}
	openFlowConsent(t, m)
	if m.agentInspector.open || m.agentInspectionVisible() {
		t.Fatal("agent inspector stayed open under consent")
	}
}

// Below the minimum size the view shows only the enlarge notice, so a blind
// Enter must not decide the consent.
func TestConsentEnterIgnoredBelowMinimumSize(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	reply := openFlowConsent(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.WindowSizeMsg{Width: minWidth - 1, Height: minHeight})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(reply) != 0 || !m.consent.pending {
		t.Fatal("Enter decided consent while the dialog was hidden")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-reply; !got.Allowed {
		t.Fatal("Enter on Allow at full size declined")
	}
}

// A question and a consent never share the screen: whichever arrives second
// is refused (a question as skipped, a consent as unavailable, never as a
// decision), and the first stays pending.
func TestAskAndConsentSerialize(t *testing.T) {
	t.Run("consent while a question is pending", func(t *testing.T) {
		m := newFlowUI(t)
		beginFlowRun(m)
		openFlowQuestion(t, m)
		reply := make(chan agent.ConsentReply, 1)
		m.Update(agent.TurnEvent{RunID: 1, Kind: "consent", Consent: &agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com", URL: "https://example.com/", Reply: reply}})
		select {
		case got := <-reply:
			// The user was never asked, so the tool must fail rather than
			// report a decline (or a grant) the user did not make.
			if got.Allowed || !errors.Is(got.Err, errConsentUnavailable) {
				t.Fatalf("consent behind a question = %+v, want errConsentUnavailable", got)
			}
		default:
			t.Fatal("consent behind a question was left waiting")
		}
		if hasEntry(m, "Likha", "Web consent declined") || m.webGranted((&agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com"}).ScopeKey()) {
			t.Fatal("unasked consent was recorded as a decision")
		}
		if m.consent.pending || !m.askVisible() {
			t.Fatalf("question lost the screen: consent pending=%t ask visible=%t", m.consent.pending, m.askVisible())
		}
	})
	t.Run("question while consent is pending", func(t *testing.T) {
		m := newFlowUI(t)
		beginFlowRun(m)
		openFlowConsent(t, m)
		reply := make(chan tools.AskAnswer, 1)
		m.Update(agent.TurnEvent{RunID: 1, Kind: "ask", Ask: &agent.AskInteraction{
			ID: "ask_1_1", CallID: "call_q", Reply: reply, Request: tools.AskRequest{Question: "Proceed?"},
		}})
		select {
		case answer := <-reply:
			if !answer.Skipped {
				t.Fatalf("refused question got answer %+v, want skipped", answer)
			}
		default:
			t.Fatal("question behind consent was not refused")
		}
		if m.ask.pending || !m.consentVisible() {
			t.Fatalf("consent lost the screen: ask pending=%t consent visible=%t", m.ask.pending, m.consentVisible())
		}
	})
}

// A failed or abandoned key check from /providers <name> <key> returns the
// status to key entry instead of leaving "Checking <Provider>".
func TestProviderKeyCheckStatusSettles(t *testing.T) {
	p, ok := model.LookupProvider("openai")
	if !ok {
		t.Fatal("openai missing from the predefined list")
	}
	t.Run("failed check", func(t *testing.T) {
		m := newFlowUI(t)
		m.applyProviderKeyArgument(p, "sk-bad")
		m.Update(keyCheckMsg{attempt: m.keyModal.checkAttempt, provider: p, key: "sk-bad", err: errors.New("401 unauthorized")})
		if m.status != "Provider key required" {
			t.Fatalf("status after failed check = %q", m.status)
		}
	})
	t.Run("Esc during check", func(t *testing.T) {
		m := newFlowUI(t)
		m.applyProviderKeyArgument(p, "sk-slow")
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if m.keyModal.checking || m.status != "Provider key required" {
			t.Fatalf("Esc during check: checking=%t status=%q", m.keyModal.checking, m.status)
		}
	})
}

// A held batch that cannot start keeps everything where it was: the held
// prompts and their Queued rows stay held and the new draft stays in the
// composer.
func TestHeldBatchStartFailureKeepsDraftAndQueue(t *testing.T) {
	m := newFlowUI(t) // no provider client: every run start fails
	m.queue = []string{"held one", "held two"}
	m.entries = append(m.entries, entry{role: "Queued", content: "held one"}, entry{role: "Queued", content: "held two"})
	typeText(m, "new draft")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := string(m.input); got != "new draft" {
		t.Fatalf("composer = %q, want the new draft", got)
	}
	if len(m.queue) != 2 || m.queue[0] != "held one" || m.queue[1] != "held two" {
		t.Fatalf("held queue = %q, want both prompts", m.queue)
	}
	queued := 0
	for _, e := range m.entries {
		if e.role == "Queued" {
			queued++
		}
	}
	if queued != 2 {
		t.Fatalf("Queued rows = %d, want 2", queued)
	}
	if !hasEntry(m, "Error", "No provider configured") {
		t.Fatal("start failure not reported")
	}
}

// A web tool can queue its consent request just before Ctrl+C lands. The
// tool then ends on the cancellation, so the late request must not open a
// dialog during the cancel, ring the bell, overwrite the Cancelling status,
// or let Allow grant an origin no tool is waiting for.
func TestConsentQueuedBeforeCancelNeverOpens(t *testing.T) {
	m, bell, _ := attentionTestUI(t, providers.StoredAttentionConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	done := make(chan error, 1)
	ask := m.fetchConsent(m.events, m.runID)
	go func() {
		_, err := ask(ctx, webtools.ConsentScope{Kind: "fetch-origin", Origin: "example.com", URL: "https://example.com/"})
		done <- err
	}()
	queued := <-m.events
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("web tool ended with %v, want the run's cancellation", err)
	}
	m.Update(queued)
	if m.consent.pending || m.consentVisible() {
		t.Fatal("a consent queued before the cancel opened during it")
	}
	if m.status != "Cancelling" {
		t.Fatalf("status = %q, want Cancelling", m.status)
	}
	if bell.Len() != 0 {
		t.Fatalf("an unopened consent signalled attention: %q", bell.String())
	}
	if hasEntry(m, "Likha", "Network consent requested") {
		t.Fatal("an unopened consent was recorded as requested")
	}
	select {
	case got := <-queued.Consent.Reply:
		if got.Allowed || !errors.Is(got.Err, errConsentRunEnding) {
			t.Fatalf("late consent answered %+v, want errConsentRunEnding", got)
		}
	default:
		t.Fatal("late consent was left unanswered")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.webGranted((&agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com"}).ScopeKey()) {
		t.Fatal("a consent nobody was shown granted the origin")
	}
}

// The attention hook fires only for a consent dialog that actually opened:
// one refused behind an open question, or arriving while the run cancels,
// needs nobody's attention.
func TestConsentAttentionHookOnlyWhenShown(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, m *ui)
		fires bool
	}{
		{"shown", func(*testing.T, *ui) {}, true},
		{"behind an open question", func(t *testing.T, m *ui) { openFlowQuestion(t, m) }, false},
		{"while the run cancels", func(_ *testing.T, m *ui) { m.cancelling = true }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir, log := t.TempDir(), filepath.Join(t.TempDir(), "events.jsonl")
			writeHooks(t, stateDir, hooksJSON(t, "attention", "recorder", hookShell(t, `cat >>"`+log+`"`), ""))
			m := hookUI(t, t.TempDir(), stateDir, nil, nil, session.Snapshot{})
			beginFlowRun(m)
			tc.setup(t, m)
			cmd := m.applyTurnEvent(agent.TurnEvent{RunID: 1, Kind: "consent", Consent: &agent.ConsentRequest{
				Kind: "fetch-origin", Origin: "example.com", URL: "https://example.com/", Reply: make(chan agent.ConsentReply, 1)}})
			runHookCmds(m, cmd)
			data, err := os.ReadFile(log)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if fired := strings.Contains(string(data), `"review"`); fired != tc.fires {
				t.Fatalf("attention hook fired = %t, want %t (log %q)", fired, tc.fires, data)
			}
		})
	}
}

// A force-stopped run's web tool may still check a grant from its own
// goroutine while the user switches sessions, which clears every grant;
// both sides go through the grants lock (run with -race).
func TestSessionSwitchClearsWebGrantsUnderTheLock(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()
	store, err := session.Open(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := hookUI(t, root, stateDir, nil, store, snapshot)
	key := (&agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com"}).ScopeKey()
	m.grantWeb(key)
	stop, checked := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(checked)
		for {
			select {
			case <-stop:
				return
			default:
				m.webGranted(key)
			}
		}
	}()
	m.resumeSession(snapshot.ID)
	close(stop)
	<-checked
	if m.webGranted(key) {
		t.Fatal("a session switch kept the previous conversation's web grant")
	}
}
