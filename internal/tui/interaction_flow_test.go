package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
	"likha/internal/tools"
)

// newFlowUI builds an idle UI over a repository holding alpha.txt, so the
// @ completion popup has a row to offer.
func newFlowUI(t *testing.T) *ui {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// beginFlowRun puts the UI into a live run without a provider.
func beginFlowRun(m *ui) {
	m.working, m.runID = true, 1
	m.reasoningStream = -1
	m.events = make(chan agent.TurnEvent, 64)
	m.cancel = func() {}
	m.status = "Waiting for model"
}

func typeText(m *ui, text string) {
	for _, r := range text {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// openMentionPopup types "see @al" and delivers the file index so the @
// popup is open with the alpha.txt row.
func openMentionPopup(t *testing.T, m *ui) {
	t.Helper()
	typeText(m, "see @al")
	m.Update(fileIndexMsg{files: []string{"alpha.txt"}})
	if !m.mention.open || len(m.mention.matches) != 1 {
		t.Fatalf("mention popup not open: open=%t matches=%v", m.mention.open, m.mention.matches)
	}
}

// An approval arriving while a completion popup is open owns Tab and Enter:
// Tab moves the review focus instead of completing the draft, and Enter then
// confirms the focused Decline instead of approving.
func TestApprovalTakesKeysFromOpenPopups(t *testing.T) {
	cases := []struct {
		name  string
		open  func(t *testing.T, m *ui)
		draft string
	}{
		{"mention popup", openMentionPopup, "see @al"},
		{"command popup", func(t *testing.T, m *ui) {
			typeText(m, "/co")
			if !m.commandPopup.open {
				t.Fatal("command popup not open")
			}
		}, "/co"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFlowUI(t)
			beginFlowRun(m)
			tc.open(t, m)
			reply := make(chan bool, 1)
			m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "command", Reply: reply}})
			if m.mention.open || m.commandPopup.open {
				t.Fatal("approval left a completion popup open")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyTab})
			if m.reviewFocus != focusDecline {
				t.Fatalf("Tab did not move the review focus: focus=%d", m.reviewFocus)
			}
			if got := string(m.input); got != tc.draft {
				t.Fatalf("Tab edited the draft behind the review: %q", got)
			}
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			select {
			case approved := <-reply:
				if approved {
					t.Fatal("Enter approved with Decline focused")
				}
			default:
				t.Fatal("Enter did not decide the review")
			}
		})
	}
}

// Even if a popup is somehow open under a pending review, the review keeps
// Tab (defense in depth behind closing popups when the review opens).
func TestPendingReviewIgnoresStalePopup(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	openMentionPopup(t, m)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "command", Reply: make(chan bool, 1)}})
	m.mention.open = true
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.reviewFocus != focusDecline || string(m.input) != "see @al" {
		t.Fatalf("stale popup claimed Tab: focus=%d draft=%q", m.reviewFocus, string(m.input))
	}
}

func openFlowQuestion(t *testing.T, m *ui) chan tools.AskAnswer {
	t.Helper()
	startCall(m, "call_q", "ask_user", `{"question":"Proceed?"}`)
	reply := make(chan tools.AskAnswer, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "ask", Ask: &agent.AskInteraction{
		ID: "ask_1_1", CallID: "call_q", Reply: reply, Request: tools.AskRequest{Question: "Proceed?", Options: []string{"yes", "no"}},
	}})
	if !m.askVisible() {
		t.Fatal("question did not open")
	}
	return reply
}

// Esc on a question during a run cancels the run even when a completion
// popup was open as the question arrived, and records the interruption.
func TestAskEscCancelsRunPastOpenPopup(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	openMentionPopup(t, m)
	openFlowQuestion(t, m)
	if m.mention.open {
		t.Fatal("question left the mention popup open")
	}
	// Esc must not depend on falling through the popups: even a stale open
	// popup cannot swallow it.
	m.mention.open = true
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.cancelling || m.status != "Cancelling" {
		t.Fatalf("Esc did not cancel the run: cancelling=%t status=%q", m.cancelling, m.status)
	}
	if m.ask.pending {
		t.Fatal("question still pending after Esc")
	}
	if !hasEntry(m, "Likha", "Question interrupted before an answer") {
		t.Fatal("Esc did not record the interrupted question")
	}
}

// Esc records the interrupted question exactly as Ctrl+C does, once.
func TestAskEscRecordsInterruptedQuestion(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	reply := openFlowQuestion(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(agent.TurnEvent{RunID: 1, Kind: "done", History: m.history})
	count := 0
	for _, e := range m.entries {
		if strings.Contains(e.content, "Question interrupted before an answer") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("interrupted note recorded %d times, want 1", count)
	}
	if len(reply) != 0 {
		t.Fatalf("Esc sent an answer: %+v", <-reply)
	}
}

// A compaction summary that lands after the user cancelled keeps the old
// conversation and ends the cancel, so the next run's reviews still open.
func TestCancelledCompactionKeepsHistoryAndClearsCancel(t *testing.T) {
	m := newFlowUI(t)
	old := []model.Message{{Role: "user", Content: "first"}, {Role: "assistant", Content: "reply"}}
	m.history = append([]model.Message(nil), old...)
	beginFlowRun(m)
	m.status = "Compacting…"
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.cancelling {
		t.Fatal("Esc did not cancel the compaction")
	}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "compacted", Text: "brief", History: []model.Message{{Role: "user", Content: "brief"}}})
	if !reflect.DeepEqual(m.history, old) {
		t.Fatalf("cancelled compaction replaced the history: %+v", m.history)
	}
	if m.cancelling || m.working || m.status != "Cancelled" {
		t.Fatalf("cancelled compaction did not settle: cancelling=%t working=%t status=%q", m.cancelling, m.working, m.status)
	}
	if hasEntry(m, "Likha", "Conversation compacted") {
		t.Fatal("cancelled compaction announced a summary")
	}
	m.working, m.runID = true, 2
	m.Update(agent.TurnEvent{RunID: 2, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "edit", Reply: make(chan bool, 1)}})
	if m.pending == nil {
		t.Fatal("next run's approval was dropped")
	}
}

func openFlowConsent(t *testing.T, m *ui) chan agent.ConsentReply {
	t.Helper()
	reply := make(chan agent.ConsentReply, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "consent", Consent: &agent.ConsentRequest{Kind: "fetch-origin", Origin: "example.com", URL: "https://example.com/", Reply: reply}})
	if !m.consentVisible() {
		t.Fatal("consent did not open")
	}
	return reply
}

// Web consent has no letter shortcuts (FR-22): typed text is swallowed, a
// stray Enter declines because focus starts on Decline, and Allow takes an
// explicit focus move.
func TestConsentDecidesOnlyThroughFocusedButtons(t *testing.T) {
	t.Run("letters never decide", func(t *testing.T) {
		m := newFlowUI(t)
		beginFlowRun(m)
		reply := openFlowConsent(t, m)
		typeText(m, "say yes and allow")
		if len(reply) != 0 || !m.consent.pending {
			t.Fatal("a letter answered the consent")
		}
		if len(m.input) != 0 {
			t.Fatalf("consent keys leaked into the composer: %q", string(m.input))
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if got := <-reply; got.Allowed {
			t.Fatal("Enter on the initial focus allowed the request")
		}
	})
	t.Run("focus Allow then Enter", func(t *testing.T) {
		m := newFlowUI(t)
		beginFlowRun(m)
		reply := openFlowConsent(t, m)
		m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		if !strings.Contains(stripANSI(m.View()), "> [ Allow ]") {
			t.Fatalf("Allow focus not shown: %q", stripANSI(m.View()))
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if got := <-reply; !got.Allowed {
			t.Fatal("Enter on Allow declined")
		}
	})
	t.Run("Esc declines", func(t *testing.T) {
		m := newFlowUI(t)
		beginFlowRun(m)
		reply := openFlowConsent(t, m)
		m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if got := <-reply; got.Allowed {
			t.Fatal("Esc allowed the request")
		}
		if m.cancelling {
			t.Fatal("Esc on consent cancelled the run")
		}
	})
}

// Answering consent hands the screen back to the run: the status the run
// showed returns and so does the Working row it showed. Consent from inside
// a running web tool keeps the tool item instead
// (TestConsentAnswerKeepsLiveToolItem).
func TestConsentAnswerRestoresWorkingState(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	m.showActivity()
	openFlowConsent(t, m)
	if m.hasActivity() {
		t.Fatal("Working row shown under the consent dialog")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.status != "Waiting for model" {
		t.Fatalf("status after consent = %q, want the run's status", m.status)
	}
	if !m.hasActivity() || cmd == nil {
		t.Fatalf("Working row not restored: activity=%t cmd=%v", m.hasActivity(), cmd != nil)
	}
}

// Ctrl+Y after an Up caret move inserts at the caret instead of replacing
// runes before it as if the previous yank were still adjacent.
func TestYankAfterVerticalCaretMoveInserts(t *testing.T) {
	m := newFlowUI(t)
	typeText(m, "0123456789")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeText(m, "abc XYZ")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	killed := string(m.edit.ring[0])
	base := string(m.input)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.edit.caret <= len([]rune(killed)) {
		t.Fatalf("Up left the caret at %d; the test needs it past the yank length", m.edit.caret)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	got := string(m.input)
	if strings.Count(got, killed) != 2 || strings.ReplaceAll(got, killed, "") != strings.ReplaceAll(base, killed, "") {
		t.Fatalf("yank after Up lost text: base %q, killed %q, got %q", base, killed, got)
	}
}

// Esc on /models leaves no "Listing models" status behind, and neither does
// picking the model that is already live.
func TestModelsDialogCloseSettlesStatus(t *testing.T) {
	t.Run("Esc", func(t *testing.T) {
		m := newFlowUI(t)
		m.dialog = dialogState{kind: dialogModels, open: true}
		m.status = modelsListingStatus
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if m.dialog.open || m.status != "Ready" {
			t.Fatalf("Esc left status %q (dialog open %t)", m.status, m.dialog.open)
		}
	})
	t.Run("current model", func(t *testing.T) {
		server := newProvidersServer(t, false)
		defer server.Close()
		m := newProvidersUI(t, t.TempDir(), server.URL+"/v1")
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		p, ok := model.LookupProvider("openai")
		if !ok {
			t.Fatal("openai missing from the predefined list")
		}
		m.dialog = dialogState{kind: dialogModels, open: true}
		m.dialogModelRows = []modelsRow{{provider: p, model: m.modelName}}
		m.status = modelsListingStatus
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if m.status == modelsListingStatus {
			t.Fatalf("picking the live model left status %q", m.status)
		}
	})
}

func newCustomKeyModal(t *testing.T, stateDir string, server *httptest.Server) (*ui, model.Provider) {
	t.Helper()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	p := providers.CustomEndpointTarget(server.URL + "/v1")
	m.openProviderAuth(p)
	if !m.keyModal.open || m.keyModal.auth {
		t.Fatalf("key modal not open: %+v", m.keyModal)
	}
	return m, p
}

// Esc on the key modal restores the idle status instead of leaving the
// modal's prompt behind.
func TestKeyModalEscSettlesStatus(t *testing.T) {
	server := newProvidersServer(t, false)
	defer server.Close()
	m, _ := newCustomKeyModal(t, t.TempDir(), server)
	if m.status != "Provider key required" {
		t.Fatalf("modal status = %q", m.status)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.keyModal.open || m.status != "Ready" {
		t.Fatalf("Esc left status %q (modal open %t)", m.status, m.keyModal.open)
	}
}

// Ctrl+C and Ctrl+D quit from the API-key modal as from every other surface.
func TestKeyModalQuitKeys(t *testing.T) {
	server := newProvidersServer(t, false)
	defer server.Close()
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyCtrlD} {
		m, _ := newCustomKeyModal(t, t.TempDir(), server)
		_, cmd := m.Update(tea.KeyMsg{Type: key})
		if cmd == nil {
			t.Fatalf("%v did nothing in the key modal", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%v did not quit from the key modal", key)
		}
	}
}

// A late check for a key abandoned with Esc never stores that key over the
// one typed since; the newer check decides.
func TestKeyModalIgnoresStaleCheck(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m, _ := newCustomKeyModal(t, stateDir, server)
	typeText(m, "key-A")
	_, cmdA := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	staleA := cmdA()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // abandon the wait, keep editing
	for range len("key-A") {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeText(m, "key-B")
	_, cmdB := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(staleA)
	if key, _ := providers.StoredKey(stateDir, "custom"); key != "" {
		t.Fatalf("stale check stored %q", key)
	}
	if !m.keyModal.open || !m.keyModal.checking {
		t.Fatalf("stale check settled the modal: %+v", m.keyModal)
	}
	m.Update(cmdB())
	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "key-B" {
		t.Fatalf("stored key = %q %v, want key-B", key, err)
	}
}

// setupCheckFrom runs a setup check command and returns its result.
func setupCheckFrom(t *testing.T, cmd tea.Cmd) setupCheckMsg {
	t.Helper()
	msg := cmd()
	if v, ok := msg.(setupCheckMsg); ok {
		return v
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if v, ok := sub().(setupCheckMsg); ok {
				return v
			}
		}
	}
	t.Fatalf("no setup check in %T", msg)
	return setupCheckMsg{}
}

// First-run setup ignores the result of a check abandoned with Esc: it
// neither advances nor stores the key typed afterwards on that result.
func TestSetupIgnoresStaleConnectionCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"alpha"}]}`)
	}))
	defer server.Close()
	previous := model.Providers[0].BaseURL
	model.Providers[0].BaseURL = server.URL + "/v1"
	t.Cleanup(func() { model.Providers[0].BaseURL = previous })

	stateDir := t.TempDir()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // first provider
	typeText(m, "sk-A")
	_, cmdA := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	staleA := setupCheckFrom(t, cmdA)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	typeText(m, "sk-B")
	_, cmdB := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(staleA)
	if m.setup.stage != setupChecking {
		t.Fatalf("stale check advanced setup: stage=%d", m.setup.stage)
	}
	if key, _ := providers.StoredKey(stateDir, model.Providers[0].Name); key != "" {
		t.Fatalf("stale check stored %q", key)
	}
	m.Update(setupCheckFrom(t, cmdB))
	if m.setup.stage != setupTheme {
		t.Fatalf("current check did not finish setup: stage=%d err=%q", m.setup.stage, m.setup.err)
	}
	if key, err := providers.StoredKey(stateDir, model.Providers[0].Name); err != nil || key != "sk-B" {
		t.Fatalf("stored key = %q %v, want sk-B", key, err)
	}
}

// Recalling a slash command from history opens no command popup, so Up
// keeps walking history.
func TestHistoryRecallOfCommandKeepsArrows(t *testing.T) {
	m := newPromptHistoryKeysUI(t, []string{"older", "/compact"})
	pressComposerKey(m, tea.KeyUp)
	if string(m.input) != "/compact" {
		t.Fatalf("recalled %q, want /compact", string(m.input))
	}
	if m.commandPopup.open {
		t.Fatal("history recall opened the command popup")
	}
	pressComposerKey(m, tea.KeyUp) // caret to the start
	pressComposerKey(m, tea.KeyUp)
	if string(m.input) != "older" {
		t.Fatalf("Up after recall = %q, want older", string(m.input))
	}
}
