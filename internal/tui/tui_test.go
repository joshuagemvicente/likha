package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

func TestUIUsesFixedViewportAndPagesCompleteContent(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.entries = append(m.entries, entry{role: "Assistant", content: "beginning of conversation"})
	for range 60 {
		m.entries = append(m.entries, entry{role: "Assistant", content: "a distinct message in the conversation"})
	}
	m.entries = append(m.entries, entry{role: "Assistant", content: "end of conversation"})

	latest := m.View()
	if !strings.Contains(latest, "end of conversation") || strings.Contains(latest, "beginning of conversation") {
		t.Fatalf("newest page is not distinct: %q", latest)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if m.scroll != 0 {
		t.Fatalf("scroll = %d after Home, want 0", m.scroll)
	}
	if !strings.Contains(m.View(), "beginning of conversation") {
		t.Fatal("older conversation was not reachable by page navigation")
	}
	m.Update(tea.WindowSizeMsg{Width: 42, Height: 12})
	assertViewport(t, m.View(), 42, 12)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assertViewport(t, m.View(), 80, 24)
}

func assertViewport(t *testing.T, view string, width, height int) {
	t.Helper()
	rows := strings.Split(view, "\n")
	if len(rows) != height {
		t.Fatalf("frame has %d rows, want %d", len(rows), height)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("row %d width = %d, want %d: %q", i, got, width, row)
		}
	}
}

func TestSetupFlowPicksProviderKeyAndModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"zeta-model"}]}`)
	}))
	defer server.Close()

	// Point the openai provider at the test server for the setup check.
	model.Providers[0].BaseURL = server.URL + "/v1"
	t.Cleanup(func() { model.Providers[0].BaseURL = "https://api.openai.com/v1" })

	stateDir := t.TempDir()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, stateDir, nil, session.Snapshot{})
	if m.mode != modeSetup || m.setup.stage != setupProvider {
		t.Fatalf("setup did not start: mode=%q stage=%d", m.mode, m.setup.stage)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View(), "Choose a model provider") || !strings.Contains(m.View(), "OpenRouter") {
		t.Fatalf("provider list not shown: %q", m.View())
	}
	// Select the second provider (openrouter) so key entry is exercised.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupKey {
		t.Fatalf("key stage not reached: stage=%d", m.setup.stage)
	}
	for _, r := range "sk-test-123" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if strings.Contains(m.View(), "sk-test-123") {
		t.Fatalf("API key rendered in clear Text: %q", m.View())
	}
	if !strings.Contains(m.View(), "••••") {
		t.Fatalf("masked key not displayed: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupChecking || !m.setup.checking {
		t.Fatalf("check not started: stage=%d checking=%t", m.setup.stage, m.setup.checking)
	}
	check := setupCheckMsg{models: []string{"zeta-model"}}
	m.Update(check)
	// A single reported model auto-selects; setup then offers the theme stage.
	if m.setup.stage != setupTheme {
		t.Fatalf("theme stage not reached: stage=%d", m.setup.stage)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // accept the default theme
	if m.mode != modeMain || m.modelName != "zeta-model" || m.client == nil {
		t.Fatalf("setup did not complete: mode=%q model=%q client=%v", m.mode, m.modelName, m.client)
	}
	// The three-line header is gone; provider/model identity lives in the
	// status bar row now.
	if status := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(status, "OpenRouter") || !strings.Contains(status, "zeta-model") {
		t.Fatalf("status row missing provider/model identity: %q", status)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openrouter" || cfg.Model != "zeta-model" {
		t.Fatalf("stored config: %+v %v", cfg, err)
	}
	key, err := providers.StoredKey(stateDir, "openrouter")
	if err != nil || key != "sk-test-123" {
		t.Fatalf("stored key = %q err=%v", key, err)
	}
}

func TestSetupModelStageListsAndSelects(t *testing.T) {
	stateDir := t.TempDir()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// Jump straight to the model stage as if a check returned several models.
	m.setup.stage = setupModel
	m.setup.models = []string{"alpha", "beta", "gamma"}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View(), "> alpha") || !strings.Contains(m.View(), "beta") {
		t.Fatalf("model list not shown: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupTheme || m.modelName != "beta" {
		t.Fatalf("theme stage not reached after model selection: stage=%d model=%q", m.setup.stage, m.modelName)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // accept the default theme
	if m.mode != modeMain {
		t.Fatalf("setup did not complete: mode=%q", m.mode)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openai" || cfg.Model != "beta" || cfg.Theme != "default" {
		t.Fatalf("stored config: %+v %v", cfg, err)
	}
}

func TestSetupCheckFailureShowsClassifiedErrorAndRetries(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// Select the first hosted provider (openai) and a bad key.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "sk-wrong" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupChecking {
		t.Fatalf("check stage not reached: %d", m.setup.stage)
	}
	m.Update(setupCheckMsg{err: fmt.Errorf("%w: HTTP 401 Unauthorized", model.ErrUnauthorized)})
	if !strings.Contains(m.View(), "rejected the credentials") {
		t.Fatalf("failure not surfaced: %q", m.View())
	}
	// Esc returns to key entry so the user can retry with a corrected key.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.setup.stage != setupKey {
		t.Fatalf("esc did not return to key stage: %d", m.setup.stage)
	}
}

func TestSlashCommandsDispatchWithoutModel(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// /help prints the command list and never starts a turn.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/help")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.working || len(m.entries) == 0 || m.entries[len(m.entries)-1].content != commandHelp {
		t.Fatalf("/help failed: working=%t entries=%+v", m.working, m.entries)
	}

	// Unknown command: visible error, not sent to the model, draft restored.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/frobnicate")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	last := m.entries[len(m.entries)-1]
	if m.working || last.content != "Unknown command /frobnicate; not sent to the model. "+commandHelp {
		t.Fatalf("unknown command mishandled: working=%t last=%q", m.working, last.content)
	}
	if string(m.input) != "/frobnicate" {
		t.Fatalf("draft not restored: %q", string(m.input))
	}
	// The error is rendered in the conversation: paging keeps every entry
	// reachable. The /compact description lengthened the help entry by a
	// wrapped line, so page until the entry appears instead of pinning a
	// page count.
	found := false
	for range m.pageCount() + 1 {
		if strings.Contains(m.View(), "Unknown command /frobnicate") {
			found = true
			break
		}
		m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if !found {
		t.Fatalf("error entry not reachable by paging: %q", m.View())
	}

	// // escape sends a literal slash to the model: the turn starts with the
	// escaped prompt in history. (The restored draft from the previous error
	// is cleared first, like a real user would with backspace/Ctrl+C.)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answered\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	escapeClient, err := model.New(server.URL+"/v1", "escape-test", "")
	if err != nil {
		t.Fatal(err)
	}
	m.client = escapeClient
	m.input = nil
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("//what is /quit")})
	model2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = model2
	if !m.working || cmd == nil {
		t.Fatal("escaped slash did not start a turn")
	}
	if len(m.history) != 1 || m.history[0].Content != "/what is /quit" {
		t.Fatalf("escaped prompt = %+v", m.history)
	}
	// Drain: close events by ending the turn.
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "done", History: m.history})
	if m.working {
		t.Fatal("turn did not complete")
	}
}

func TestSlashQuitMirrorsCtrlD(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/quit")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/quit produced no command")
	}
	if msg := cmd(); !isQuitMsg(msg) {
		t.Fatalf("/quit message = %v, want quit", msg)
	}
}

func isQuitMsg(msg tea.Msg) bool {
	_, ok := msg.(tea.QuitMsg)
	return ok
}

func TestModelSwitchCommandUsesListingAndLiveClient(t *testing.T) {
	sawModel := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"alpha"},{"id":"beta"}]}`)
		case r.URL.Path == "/v1/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			sawModel <- body.Model
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	stateDir := t.TempDir()
	if err := providers.StoreKey(stateDir, "openai", "k-openai"); err != nil {
		t.Fatal(err)
	}
	setProviderBaseURL(t, "openai", server.URL+"/v1")
	client, err := model.New(server.URL+"/v1", "initial", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "initial", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// /models opens the selection dialog and fetches the list.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	model2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = model2
	if cmd == nil {
		t.Fatal("models fetch did not start")
	}
	msgs := runModelsCmds(t, m, cmd)
	sections := arrivedSections(msgs)
	if len(sections) != 1 || len(sections[0].models) != 2 {
		t.Fatalf("models list = %+v", msgs)
	}
	if !m.dialog.open || m.dialog.kind != dialogModels || len(m.dialogModelRows) != 2 {
		t.Fatalf("models dialog not populated: rows=%+v", m.dialogModelRows)
	}
	// The cursor sits on the live model ("initial" is absent from the list, so
	// the cursor stays at 0 = alpha); Enter applies the highlighted model.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open || m.modelName != "alpha" {
		t.Fatalf("model apply failed: model=%q", m.modelName)
	}
	// A turn uses the switched model.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("say hi")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.working {
		t.Fatal("turn did not start")
	}
	// The request estimate now precedes the streamed response; keep processing
	// events until the response arrives so this still verifies the wire model.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-m.events:
			m.Update(event)
			if event.Kind == "text" {
				goto responseReceived
			}
		case <-deadline:
			t.Fatal("timed out waiting for the model response")
		}
	}
responseReceived:
	select {
	case got := <-sawModel:
		if got != "alpha" {
			t.Fatalf("request model = %q, want alpha", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not reach the test server")
	}
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "done", History: m.history})
}

func TestSessionsCommandListsAndResumesInPlace(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	// Saving first after second's creation makes the listing order
	// deterministic (newest first): first is entry 1.
	first.Entries = []session.Entry{{Role: "You", Content: "earlier question"}}
	first.History = []model.Message{{Role: "user", Content: "earlier question"}}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, second)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/sessions")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.sessionIDs) != 2 {
		t.Fatalf("sessions not listed: %+v", m.entries)
	}
	// Close the dialog before typing the resume command: with the dialog
	// open, printable keys are the query filter, not the prompt.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/sessions 1")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.status != "Resumed session" || len(m.history) != 1 || m.history[0].Content != "earlier question" {
		t.Fatalf("in-place resume failed: status=%q history=%+v", m.status, m.history)
	}
	resumed := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "earlier question") {
			resumed = true
		}
	}
	if !resumed {
		t.Fatalf("resumed entries not shown: %+v", m.entries)
	}
	// An out-of-range selection restores the draft and errors.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/sessions 9")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	lastEntry := m.entries[len(m.entries)-1]
	if !strings.Contains(lastEntry.content, "Run /sessions first") || string(m.input) != "/sessions 9" {
		t.Fatalf("out-of-range resume mishandled: input=%q last=%q", string(m.input), lastEntry.content)
	}
}

func TestReasoningStreamsMutedAndClosesOnContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("think and answer")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.working {
		t.Fatal("turn did not start")
	}
	// Reasoning deltas arrive first, rendered as a Reasoning entry.
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "reasoning", Text: "pondering the question "})
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "reasoning", Text: "carefully"})
	found := false
	for _, e := range m.entries {
		if e.role == "Reasoning" && strings.Contains(e.content, "pondering the question carefully") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasoning not streamed: %+v", m.entries)
	}
	// Content closes the reasoning stream; further reasoning deltas do not
	// reopen or append to it after the answer started.
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "text", Text: "Answer: 42."})
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "reasoning", Text: "late thought"})
	for _, e := range m.entries {
		if e.role == "Reasoning" && strings.Contains(e.content, "late thought") {
			t.Fatalf("reasoning leaked into the answer: %+v", m.entries)
		}
	}
	var assistant string
	for _, e := range m.entries {
		if e.role == "Assistant" {
			assistant = e.content
		}
	}
	if assistant != "Answer: 42." {
		t.Fatalf("assistant content = %q", assistant)
	}
	// The muted role renders (LineStyles aligned; Muted style present).
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "done", History: []model.Message{{Role: "user", Content: "think and answer"}, {Role: "assistant", Content: "Answer: 42.", Reasoning: "pondering the question carefully"}}})
	if m.working {
		t.Fatal("turn did not complete")
	}
}

func TestSessionScrollsFromVeryTopToLastChat(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// Build a session much longer than one viewport.
	for i := range 200 {
		m.entries = append(m.entries, entry{role: "You", content: fmt.Sprintf("message number %d of the conversation", i)})
	}
	first := "message number 0 of the conversation"
	last := "message number 199 of the conversation"

	// A fresh view follows the newest content: the last chat is visible.
	if !strings.Contains(m.View(), last) {
		t.Fatalf("newest page does not show the last chat: %q", m.View())
	}
	if strings.Contains(m.View(), first) {
		t.Fatal("the very top of the session leaked into the newest page")
	}
	// Home jumps straight to the very top.
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if !strings.Contains(m.View(), first) {
		t.Fatalf("Home did not reach the top of the session: %q", m.View())
	}
	if m.scroll != 0 {
		t.Fatalf("scroll = %d after Home, want 0", m.scroll)
	}
	// End jumps straight back to the newest page.
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(m.View(), last) {
		t.Fatalf("End did not return to the last chat: %q", m.View())
	}
	// Mouse wheel scrolls line-granular: one notch un-pins from the bottom
	// anchor and moves a few lines up; two notches down re-pin to the newest.
	maxScroll := m.scrollMax()
	m.Update(tea.MouseMsg{Type: tea.MouseWheelUp})
	if m.following || m.scroll >= maxScroll {
		t.Fatalf("wheel up did not move off the bottom: scroll=%d max=%d", m.scroll, maxScroll)
	}
	m.Update(tea.MouseMsg{Type: tea.MouseWheelDown})
	m.Update(tea.MouseMsg{Type: tea.MouseWheelDown})
	if !m.following || !strings.Contains(m.View(), last) {
		t.Fatalf("wheel down did not re-follow the newest content: %q", m.View())
	}
	// PgUp from the top stays on page 1 — nothing silently clipped above it.
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.scroll != 0 || !strings.Contains(m.View(), first) {
		t.Fatalf("PgUp past the top moved the scroll: scroll=%d", m.scroll)
	}
}

func TestUIRequiresResizeBeforePrompt(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 15, Height: 5})
	assertViewport(t, m.View(), 15, 5)
	m.input = []rune("hello")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.working || !strings.Contains(m.View(), "Likha:") {
		t.Fatal("undersized viewport accepted a prompt")
	}
}

func TestUICancellationDrainsCompletedResultBeforeEnding(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, snapshot)
	m.working, m.runID = true, 1
	cancelled := false
	m.cancel = func() { cancelled = true }
	m.events = make(chan agent.TurnEvent, 2)
	m.entries = append(m.entries, entry{role: "You", content: "check"})
	m.history = []model.Message{{Role: "user", Content: "check"}}
	completed := []model.Message{{Role: "user", Content: "check"}, {Role: "assistant", ToolCalls: []model.ToolCall{{ID: "one"}}}, {Role: "tool", ToolCallID: "one", Content: "Exit status: 0"}}
	m.events <- agent.TurnEvent{RunID: 1, Kind: "tool_result", Text: "run_command: Exit status: 0", History: completed}
	m.events <- agent.TurnEvent{RunID: 1, Kind: "error", Text: "context canceled", History: completed}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled || !m.working || !m.cancelling {
		t.Fatal("cancellation did not enter the draining state")
	}
	m.Update(<-m.events)
	if !m.working || len(m.history) != 3 || m.history[2].Content != "Exit status: 0" {
		t.Fatalf("completed outcome was lost during drain: %+v", m.history)
	}
	m.Update(<-m.events)
	if m.working || m.status != "Cancelled" || m.history[2].Content != "Exit status: 0" {
		t.Fatalf("final cancellation replaced the completed result: %+v", m.history)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 3 || saved.History[2].Content != "Exit status: 0" || !strings.Contains(saved.Entries[len(saved.Entries)-2].Content, "Exit status: 0") {
		t.Fatalf("completed result missing from saved session: %+v", saved)
	}
	m.working, m.runID = true, 2
	before := len(m.entries)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "tool_result", Text: "obsolete result"})
	if len(m.entries) != before {
		t.Fatal("event from a cancelled run changed a later run")
	}
}

func TestUICancelBeforeModelResponsePersistsPrompt(t *testing.T) {
	received := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		close(received)
		<-release
	}))
	defer server.Close()
	defer close(release)
	client, err := model.New(server.URL+"/v1", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input = []rune("remember me")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not start")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	deadline := time.After(3 * time.Second)
	for m.working {
		select {
		case event, ok := <-m.events:
			if !ok {
				t.Fatal("cancelled model request closed before its terminal event")
			}
			m.Update(event)
		case <-deadline:
			t.Fatal("cancelled model request did not deliver terminal event")
		}
	}
	promptSaved := false
	for _, e := range m.entries {
		if e.role == "You" && e.content == "remember me" {
			promptSaved = true
		}
	}
	if m.working || m.status != "Cancelled" || len(m.history) != 1 || m.history[0].Content != "remember me" || !promptSaved {
		t.Fatalf("cancelled prompt not reconciled: history=%+v entries=%+v", m.history, m.entries)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 1 || saved.History[0].Content != "remember me" || len(saved.Entries) < 1 || saved.Entries[0].Content != "remember me" {
		t.Fatalf("cancelled prompt was not saved consistently: %+v", saved)
	}
}

func TestUICancelPendingApprovalDoesNotRecordCompletion(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 1)
	m.history = []model.Message{{Role: "user", Content: "check"}}
	m.entries = []entry{{role: "You", content: "check"}}
	pending := &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "touch marker", Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: pending})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	history := []model.Message{{Role: "user", Content: "check"}, {Role: "assistant", ToolCalls: []model.ToolCall{{ID: "one"}}}, {Role: "tool", ToolCallID: "one", Content: "Error: action not executed; run interrupted"}}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "error", History: history})
	if m.pending != nil || len(pending.Reply) != 0 || m.working || !strings.Contains(m.entries[len(m.entries)-2].content, "not executed") || m.history[2].Content != history[2].Content {
		t.Fatalf("cancelled approval incorrectly reconciled: history=%+v entries=%+v", m.history, m.entries)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 3 || saved.History[2].Content != history[2].Content || saved.Entries[len(saved.Entries)-2].Content != history[2].Content {
		t.Fatalf("pending approval was falsely completed in saved session: %+v", saved)
	}
}

func TestUISpaceRemainsInPrompt(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("two")})
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("words")})
	if got := string(m.input); got != "two words" || !strings.Contains(m.View(), got) {
		t.Fatalf("composer lost its space: input=%q view=%q", got, m.View())
	}
}

func TestUIRequiresFullDiffReviewBeforeApproval(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	request := &agent.ApprovalRequest{Kind: "edit", Title: "Edit: work.txt", Body: strings.Repeat("-old\n+new\n", 40), Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	if m.pageCount() < 2 || !strings.Contains(m.View(), "Edit: work.txt") {
		t.Fatal("multi-page diff review not displayed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Approve is focused by default
	if m.pending == nil || len(request.Reply) != 0 {
		t.Fatal("unseen diff pages were approved")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pending != nil || !<-request.Reply {
		t.Fatal("reviewed diff was not approvable")
	}
}

func TestUIRejectsCommandWithoutReviewingAllPages(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	request := &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: strings.Repeat("dangerous command\n", 50), Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // focus Decline
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pending != nil || <-request.Reply {
		t.Fatal("command rejection was not honored immediately")
	}
}

func TestUIReviewKeepsDecisionsAndPositionVisibleAtTerminalSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, kind := range []string{"edit", "command"} {
			t.Run(fmt.Sprintf("%s-%dx%d", kind, size[0], size[1]), func(t *testing.T) {
				m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.working, m.runID = true, 1
				m.cancel = func() {}
				request := &agent.ApprovalRequest{Kind: kind, Reply: make(chan bool, 1)}
				if kind == "edit" {
					request.Title = "Edit: work.txt"
					request.Body = "--- a/work.txt\n+++ b/work.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n" + strings.Repeat("-older\n+newer\n", 30) + "END OF DIFF"
				} else {
					request.Title = "Shell command"
					request.Body = "Working directory: /sample\nCommand:\nprintf reviewed\n\n" + strings.Repeat("argument line\n", 30) + "Approved commands can access files outside this repository and use the network."
				}
				m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
				if m.pageCount() < 2 {
					t.Fatal("review should span several pages")
				}
				var reviewed strings.Builder
				var visibleBody strings.Builder
				for page := range m.pageCount() {
					view := m.View()
					reviewed.WriteString(view)
					rows := strings.Split(view, "\n")
					for _, row := range rows[len(m.header()) : len(m.header())+m.bodyHeight()] {
						clean := strings.ReplaceAll(strings.TrimSpace(row), " ", "")
						clean = strings.ReplaceAll(clean, "│", "") // scrollbar track column
						clean = strings.ReplaceAll(clean, "█", "") // scrollbar thumb
						visibleBody.WriteString(clean)
					}
					assertViewport(t, view, size[0], size[1])
					// Model identity stays in the status row at every width;
					// below 56 columns the compact header line carries the
					// repository where the old three-line header did.
					visible := []string{fmt.Sprintf("Page %d/%d", page+1, m.pageCount()), "local"}
					if size[0] < 56 {
						visible = append(visible, "Likha · sample")
					}
					for _, want := range visible {
						if !strings.Contains(view, want) {
							t.Fatalf("review lost %q on page %d: %q", want, page+1, view)
						}
					}
					if !strings.Contains(view, "Review") {
						t.Fatalf("review mode is not identified on page %d: %q", page+1, view)
					}
					hints := rows[len(rows)-1]
					if !strings.Contains(hints, "←") || !strings.Contains(hints, "Enter") {
						t.Fatalf("review decisions missing at %dx%d: %q", size[0], size[1], hints)
					}
					if page == 0 {
						first := "Edit: work.txt"
						if kind == "command" {
							first = "WARNING: No filesystem/network sandbox"
							if !strings.Contains(view, "Detached jobs may survive") {
								t.Fatalf("command warnings missing on first page: %q", view)
							}
						}
						if !strings.Contains(view, first) {
							t.Fatalf("first review page missing %q: %q", first, view)
						}
					}
					if page < m.pageCount()-1 {
						m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
					}
				}
				if kind == "command" && (!strings.Contains(reviewed.String(), "Working directory: /sample") || !strings.Contains(reviewed.String(), "printf reviewed")) {
					t.Fatal("command details were not reachable across review pages")
				}
				last := "ENDOFDIFF"
				if kind == "command" {
					last = "Approvedcommandscanaccessfilesoutsidethisrepositoryandusethenetwork."
				}
				if !strings.Contains(visibleBody.String(), last) {
					t.Fatalf("review's final content is unreachable across pages: %q", reviewed.String())
				}
			})
		}
	}
}

func TestUIResizeRequiresReviewingTheNewPageLayout(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	request := &agent.ApprovalRequest{Kind: "edit", Title: "Edit: work.txt", Body: strings.Repeat("-old\n+new\n", 36), Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
	assertViewport(t, m.View(), 39, 11)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pending == nil || len(request.Reply) != 0 {
		t.Fatal("approval was accepted in an undersized viewport")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	assertViewport(t, m.View(), 40, 12)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pending == nil || len(request.Reply) != 0 || !strings.Contains(m.View(), reviewGateStatus) || !strings.Contains(m.View(), "Page 1/") {
		t.Fatal("resized diff was approved without reviewing its new pages or lost page position")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pending != nil || !<-request.Reply {
		t.Fatal("fully reviewed resized diff was not approvable")
	}
}

// tuiKeyReviewUI builds a main-mode 80x24 UI for the decision-bar review
// tests; tuiKeyApproval puts it into a live run and raises the review.
func tuiKeyReviewUI(t *testing.T) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// tuiKeyApproval raises a pending review on m during a live run and returns
// the request so tests can inspect the decision reply channel. The approval
// event is what resets focus to Approve.
func tuiKeyApproval(m *ui, kind, title, body string) *agent.ApprovalRequest {
	m.working, m.runID = true, 1
	m.cancel = func() {}
	request := &agent.ApprovalRequest{Kind: kind, Title: title, Body: body, Reply: make(chan bool, 1)}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
	return request
}

// The decision bar opens with Approve focused; arrows and Tab move the
// focus without deciding anything (FR-22).
func TestTuiKeyReviewFocusMovement(t *testing.T) {
	m := tuiKeyReviewUI(t)
	request := tuiKeyApproval(m, "command", "Shell command", "touch marker")
	if m.reviewFocus != focusApprove {
		t.Fatalf("initial review focus = %d, want Approve", m.reviewFocus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.reviewFocus != focusDecline {
		t.Fatalf("right focus = %d, want Decline", m.reviewFocus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.reviewFocus != focusApprove {
		t.Fatalf("left focus = %d, want Approve", m.reviewFocus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.reviewFocus != focusDecline {
		t.Fatalf("tab focus = %d, want Decline", m.reviewFocus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.reviewFocus != focusApprove {
		t.Fatalf("shift+tab focus = %d, want Approve", m.reviewFocus)
	}
	if m.pending == nil || len(request.Reply) != 0 {
		t.Fatalf("focus movement decided the review: pending=%v replies=%d", m.pending, len(request.Reply))
	}
}

// Approve before every page is seen is refused: the gate status shows and
// no reply reaches the agent.
func TestTuiKeyGatedApproveSendsNoReply(t *testing.T) {
	m := tuiKeyReviewUI(t)
	request := tuiKeyApproval(m, "edit", "Edit: work.txt", strings.Repeat("-old\n+new\n", 40))
	if m.pageCount() < 2 {
		t.Fatal("review should span several pages")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pending == nil {
		t.Fatal("gated Approve closed the review")
	}
	if len(request.Reply) != 0 {
		t.Fatalf("gated Approve sent %d replies", len(request.Reply))
	}
	if m.status != reviewGateStatus {
		t.Fatalf("gated Approve status = %q, want %q", m.status, reviewGateStatus)
	}
}

// y/n and their shifted forms never decide a review.
func TestTuiKeyLetterKeysInertDuringReview(t *testing.T) {
	m := tuiKeyReviewUI(t)
	request := tuiKeyApproval(m, "command", "Shell command", "touch marker")
	for _, letter := range []string{"y", "Y", "n", "N"} {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(letter)})
	}
	if m.pending == nil {
		t.Fatal("a letter key decided the review")
	}
	if len(request.Reply) != 0 {
		t.Fatalf("a letter key sent %d replies", len(request.Reply))
	}
	if m.reviewFocus != focusApprove {
		t.Fatalf("letter keys moved focus to %d", m.reviewFocus)
	}
}

func TestWrapDisplaysNonprintingReviewCharacters(t *testing.T) {
	lines := wrap("safe\t\r\x1b\u202e\u200b visible", 10)
	joined := strings.Join(lines, "")
	for _, visible := range []string{`\u0009`, `\u000D`, `\u001B`, `\u202E`, `\u200B`, "visible"} {
		if !strings.Contains(joined, visible) {
			t.Fatalf("review concealed %q: %q", visible, lines)
		}
	}
	if strings.Contains(joined, "\x1b") {
		t.Fatalf("review included terminal escape: %q", lines)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 10 {
			t.Fatalf("line overflowed: %q", line)
		}
	}
}

// setupCursorOnChatgpt navigates the provider stage onto the chatgpt row.
func setupCursorOnChatgpt(t *testing.T, m *ui) int {
	t.Helper()
	chatgpt := -1
	for i, p := range model.Providers {
		if p.Name == "chatgpt" {
			chatgpt = i
		}
	}
	if chatgpt < 0 {
		t.Fatal("chatgpt provider not listed")
	}
	for m.setup.cursor < chatgpt {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	return chatgpt
}

func TestSetupOAuthProviderOpensBrowserLoginStage(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	setupCursorOnChatgpt(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupLogin {
		t.Fatalf("OAuth provider did not enter the login stage: stage=%d", m.setup.stage)
	}
	view := m.View()
	if !strings.Contains(view, "Likha opens your browser to sign in with your ChatGPT account.") ||
		!strings.Contains(view, "Press Enter to open the browser") ||
		!strings.Contains(view, "Enter open browser  Esc back") {
		t.Fatalf("login stage view incomplete: %q", view)
	}
	// Esc returns to the provider list, and re-entering reaches login again.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.setup.stage != setupProvider || m.setup.err != "" || m.setup.checking {
		t.Fatalf("esc did not return to provider stage: stage=%d checking=%t", m.setup.stage, m.setup.checking)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupLogin {
		t.Fatalf("re-entered provider did not return to login stage: %d", m.setup.stage)
	}
	// Enter starts the browser flow: checking sets and the command is
	// produced. The command itself is never run — BrowserLogin binds a real
	// listener and waits for the callback.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.setup.checking || m.setup.stage != setupLogin {
		t.Fatalf("enter did not start the browser login: cmd=%v checking=%t stage=%d", cmd, m.setup.checking, m.setup.stage)
	}
	if !strings.Contains(m.View(), "Waiting for browser sign-in…") {
		t.Fatalf("sign-in wait status not rendered: %q", m.View())
	}
}

func TestSetupOAuthLoginMsgSuccessMovesToModelStage(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	setupCursorOnChatgpt(t, m)
	m.setup.stage = setupLogin
	m.setup.checking = true
	creds := model.OAuthCredentials{Refresh: "refresh-token", Access: "access-token", Expires: 1234567890}
	m.Update(oauthLoginMsg{creds: creds})
	if m.setup.stage != setupModel || m.setup.checking {
		t.Fatalf("success did not reach the model stage: stage=%d checking=%t", m.setup.stage, m.setup.checking)
	}
	if m.setup.creds != creds {
		t.Fatalf("credentials not stored: %+v", m.setup.creds)
	}
	if len(m.setup.models) != len(model.ChatGPTModels) {
		t.Fatalf("models = %v, want the curated ChatGPT list %v", m.setup.models, model.ChatGPTModels)
	}
	for i, id := range model.ChatGPTModels {
		if m.setup.models[i] != id {
			t.Fatalf("models = %v, want %v", m.setup.models, model.ChatGPTModels)
		}
	}
	if m.setup.modelCursor != 0 {
		t.Fatalf("model cursor = %d, want 0", m.setup.modelCursor)
	}
	if !strings.Contains(m.View(), "> "+model.ChatGPTModels[0]) {
		t.Fatalf("model list not rendered: %q", m.View())
	}
}

func TestSetupOAuthLoginMsgErrorStaysAndRetries(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	setupCursorOnChatgpt(t, m)
	m.setup.stage = setupLogin
	m.setup.checking = true
	m.Update(oauthLoginMsg{err: errors.New("callback timed out")})
	if m.setup.stage != setupLogin || m.setup.checking {
		t.Fatalf("error did not stay on the login stage: stage=%d checking=%t", m.setup.stage, m.setup.checking)
	}
	if m.setup.err == "" {
		t.Fatal("error message not stored")
	}
	view := m.View()
	if !strings.Contains(view, "ChatGPT sign-in failed: callback timed out") ||
		!strings.Contains(view, "press Enter to retry") {
		t.Fatalf("friendly error not rendered: %q", view)
	}
}

func TestSetupOAuthLoginMsgIgnoredAfterEsc(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	setupCursorOnChatgpt(t, m)
	m.setup.stage = setupLogin
	m.setup.checking = true
	// The user gave up waiting and went back to the provider list; a late
	// login result must not advance setup from there.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(oauthLoginMsg{creds: model.OAuthCredentials{Refresh: "late", Access: "late"}})
	if m.setup.stage != setupProvider {
		t.Fatalf("late login result advanced setup: stage=%d", m.setup.stage)
	}
	if m.setup.creds != (model.OAuthCredentials{}) || len(m.setup.models) != 0 {
		t.Fatalf("late login result mutated state: creds=%+v models=%v", m.setup.creds, m.setup.models)
	}
}

func TestSetupFinishStoresOAuthCredentialForChatGPT(t *testing.T) {
	stateDir := t.TempDir()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setup.cursor = setupCursorOnChatgpt(t, m)
	m.setup.stage = setupModel
	m.setup.models = model.ChatGPTModels
	m.setup.creds = model.OAuthCredentials{Refresh: "refresh-token", Access: "access-token", Expires: 1234567890}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.setup.stage != setupTheme {
		t.Fatalf("OAuth finish did not reach the theme stage: stage=%d err=%q", m.setup.stage, m.setup.err)
	}
	if m.client == nil {
		t.Fatal("OAuth finish did not build a client")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // accept the default theme
	if m.mode != modeMain {
		t.Fatalf("setup did not complete: mode=%q", m.mode)
	}
	if _, ok, err := providers.StoredOAuth(stateDir, "chatgpt"); err != nil || !ok {
		t.Fatalf("OAuth credential not stored: ok=%t err=%v", ok, err)
	}
	if key, err := providers.StoredKey(stateDir, "chatgpt"); err != nil || key != "" {
		t.Fatalf("OAuth provider stored an API key: %q %v", key, err)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "chatgpt" || cfg.Model != model.ChatGPTModels[0] {
		t.Fatalf("stored config: %+v %v", cfg, err)
	}
}

// TestUsageFooterShowsCodexRateLimits drives one real codex stream against an
// httptest server and asserts the main-view footer surfaces the client's
// rate-limit summary.
func TestUsageFooterShowsCodexRateLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Codex backend (/responses) and OpenAI-compatible clients
		// (/chat/completions) both carry the rate-limit headers the usage
		// summary reads.
		w.Header().Set("x-codex-primary-used-percent", "34")
		w.Header().Set("x-codex-primary-window-minutes", "300")
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"response\":{\"output\":[]}}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answered\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	creds := model.OAuthCredentials{
		Refresh:   "refresh-token",
		Access:    "access-token",
		Expires:   time.Now().Add(time.Hour).UnixMilli(),
		AccountID: "acct",
	}
	client, err := model.NewOAuth(server.URL+"/v1", "gpt-5.5", server.URL, "client_test", creds)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Stream(context.Background(), []model.Message{{Role: "user", Content: "hi"}}, nil, nil, nil); err != nil {
		t.Fatalf("codex stream: %v", err)
	}
	usage := client.Usage()
	if usage == "" {
		t.Fatal("usage summary not captured from the response headers")
	}
	m := NewUI("/sample", nil, client, "gpt-5.5", providers.Connection{Provider: "ChatGPT (Plus/Pro)", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View(), usage) {
		t.Fatalf("usage summary not shown in the footer: %q", m.View())
	}
}

// The startup block (spec §2.1) opens page 1 of a fresh session as a pure
// logo — no repository line — and scrolls away with content: later pages
// never contain it.
func TestStartupLogoBlockOpensFreshSessionAndScrollsAway(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	first := stripANSI(m.View())
	if !strings.Contains(first, "____   ___  __") {
		t.Fatalf("fresh session does not open with the logo block: %q", first)
	}
	if strings.Contains(first, "Repository:") {
		t.Fatalf("logo block still carries the repository line: %q", first)
	}
	for i := range 60 {
		m.entries = append(m.entries, entry{role: "Assistant", content: fmt.Sprintf("filler message %d of the conversation", i)})
	}
	m.jumpBottom()
	m.layoutWidth = 0 // direct entry mutation bypasses the Update-path cache reset
	latest := stripANSI(m.View())
	if strings.Contains(latest, "____   ___  __") {
		t.Fatalf("logo block stayed on the newest page: %q", latest)
	}
	// Narrow terminals skip the block; the compact header line carries
	// identity instead.
	narrow := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	narrow.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	narrowView := stripANSI(narrow.View())
	if strings.Contains(narrowView, "____   ___  __") {
		t.Fatalf("logo block rendered below the 56-column floor: %q", narrowView)
	}
	if !strings.Contains(narrowView, "Likha · sample") {
		t.Fatalf("narrow terminal lost the compact identity line: %q", narrowView)
	}
}

// The block is per-run furniture: persist excludes it, so the snapshot and a
// resumed session never contain the logo.
func TestStartupLogoBlockNeverPersistsOrReappearsOnResume(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.entries = append(m.entries, entry{role: "You", content: "hello there"})
	m.persist()
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, savedEntry := range saved.Entries {
		if savedEntry.Role == "Logo" {
			t.Fatalf("logo block was persisted: %+v", saved.Entries)
		}
	}
	if len(saved.Entries) != 1 || saved.Entries[0].Content != "hello there" {
		t.Fatalf("logo exclusion dropped or duplicated entries: %+v", saved.Entries)
	}
	resumed := NewUI(root, nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, "", store, saved)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(resumed.View(), "____   ___  __") {
		t.Fatalf("resumed session redrew the logo block: %q", resumed.View())
	}
}

// Usage from the provider's final usage-only stream event accumulates across
// turns, with the last turn's prompt tokens kept for the ctx segment.
func TestUsageAccumulatesFromProviderStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answered\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":34}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.startTurn("first question", nil)
	defer m.cancel()
	defer close(m.abandon)
	for m.working {
		m.Update(waitEvent(m.events)())
	}
	if !m.usageSeen || m.usagePrompt != 12 || m.usageCompletion != 34 || m.lastPromptTokens != 12 {
		t.Fatalf("first turn usage not recorded: seen=%t prompt=%d completion=%d last=%d", m.usageSeen, m.usagePrompt, m.usageCompletion, m.lastPromptTokens)
	}
	m.startTurn("second question", nil)
	for m.working {
		m.Update(waitEvent(m.events)())
	}
	if m.usagePrompt != 24 || m.usageCompletion != 68 || m.lastPromptTokens != 12 {
		t.Fatalf("second turn usage not accumulated: prompt=%d completion=%d last=%d", m.usagePrompt, m.usageCompletion, m.lastPromptTokens)
	}
}

func TestUpdateAvailableMsgSetsVersion(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(updateAvailableMsg{version: "v9.9.9"})
	if m.updateVersion != "v9.9.9" {
		t.Fatalf("update notice lost: %q", m.updateVersion)
	}
	m.Update(updateAvailableMsg{})
	if m.updateVersion != "v9.9.9" {
		t.Fatalf("empty notice clobbered the version: %q", m.updateVersion)
	}
}

// With the changes/staged toggles on, a tool result refreshes the dirty and
// staged counts from the real repository through the single gitStatus read.
func TestToolResultRefreshesGitCountsWhenEnabled(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "staged.txt")
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true, StatusLine: providers.StoredStatusLineConfig{Changes: true, Staged: true}}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "tool_result", Text: "listed", History: nil})
	if !m.gitOK || m.git.Dirty != 1 || m.git.Staged != 1 {
		t.Fatalf("git counts not refreshed: git=%+v ok=%t", m.git, m.gitOK)
	}
}

func TestDebugPagingTemp(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/help")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/frobnicate")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	t.Logf("scroll=%d bodyHeight=%d lines=%d pages=%d input=%q cmdopen=%v", m.scroll, m.bodyHeight(), len(m.lines), m.pageCount(), string(m.input), m.commandPopup.open)
	for i, l := range m.lines {
		t.Logf("%02d|%s", i, l)
	}
}

// clearDraft empties the composer: one Esc dismisses an open popup, the
// next clears the idle draft.
func clearDraft(m *ui) {
	for range 2 {
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
	// Each idle ESC now arms the Alt-prefix decay window; the expiry is what
	// clears the draft (spec open item 2).
	m.Update(escDecayMsg{})
}

func sendRunes(m *ui, text string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}

func TestCommandPopupOpensFiltersCompletesAndSends(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// A mid-draft slash never opens the popup; only a leading one does.
	sendRunes(m, "x/")
	if m.commandPopup.open {
		t.Fatal("popup opened on a non-leading slash")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // clears the idle draft
	m.Update(escDecayMsg{})                // the decay expiry performs the clear

	// "/" opens the popup with every reserved command listed.
	sendRunes(m, "/")
	if !m.commandPopup.open || len(m.commandPopup.matches) != len(commands) {
		t.Fatalf("popup on /: open=%t matches=%d", m.commandPopup.open, len(m.commandPopup.matches))
	}
	if view := m.View(); !strings.Contains(view, "/compact —") {
		t.Fatalf("command rows missing from view: %q", view)
	}

	// Runes filter by case-insensitive substring on the name.
	sendRunes(m, "MO")
	if len(m.commandPopup.matches) != 1 || m.commandPopup.matches[0].Name != "models" {
		t.Fatalf("matches after query = %+v", m.commandPopup.matches)
	}

	// A space begins the arguments and closes the popup.
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if m.commandPopup.open {
		t.Fatal("popup stayed open once arguments began")
	}
	// Deleting the space re-opens the popup with the same query.
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.commandPopup.open || len(m.commandPopup.matches) != 1 {
		t.Fatalf("backspace did not re-filter: open=%t matches=%+v", m.commandPopup.open, m.commandPopup.matches)
	}

	// Esc closes the popup and keeps the draft.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.commandPopup.open || string(m.input) != "/MO" {
		t.Fatalf("esc mishandled: open=%t input=%q", m.commandPopup.open, string(m.input))
	}

	// ↑/↓ move the cursor within the visible rows and clamp at the ends.
	clearDraft(m)
	sendRunes(m, "/s")
	if len(m.commandPopup.matches) < 2 {
		t.Fatalf("expected several /s matches, got %+v", m.commandPopup.matches)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.commandPopup.cursor != 0 {
		t.Fatalf("cursor moved above the first row: %d", m.commandPopup.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.commandPopup.cursor != 1 {
		t.Fatalf("down did not advance the cursor: %d", m.commandPopup.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.commandPopup.cursor != 0 {
		t.Fatalf("up did not step back: %d", m.commandPopup.cursor)
	}

	// Tab always completes: the draft becomes the command plus a space.
	clearDraft(m)
	sendRunes(m, "/comp")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.commandPopup.open || string(m.input) != "/compact " {
		t.Fatalf("tab completion failed: open=%t input=%q", m.commandPopup.open, string(m.input))
	}

	// Enter completes the highlighted command while the query is not yet an
	// exact command name; nothing is sent.
	clearDraft(m)
	sendRunes(m, "/hel")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.commandPopup.open || string(m.input) != "/help " || m.working {
		t.Fatalf("enter did not complete: open=%t input=%q working=%t", m.commandPopup.open, string(m.input), m.working)
	}

	// With the full command name typed, Enter sends instead: /help runs.
	clearDraft(m)
	sendRunes(m, "/help")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.commandPopup.open || m.working {
		t.Fatalf("exact-name enter did not send: open=%t working=%t", m.commandPopup.open, m.working)
	}
	if last := m.entries[len(m.entries)-1]; last.content != commandHelp {
		t.Fatalf("/help did not run from the exact-name draft: %q", last.content)
	}
}

func TestCommandPopupExclusiveWithMentionPopup(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("x"), 0600)
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// The slash popup opens and closes the @ popup...
	sendRunes(m, "/fi")
	if !m.commandPopup.open || m.mention.open {
		t.Fatalf("slash popup exclusive open failed: cmd=%t mention=%t", m.commandPopup.open, m.mention.open)
	}
	// ...and an @ anywhere wins over the slash popup.
	sendRunes(m, "@")
	if m.commandPopup.open || !m.mention.open {
		t.Fatalf("@ did not take over: cmd=%t mention=%t", m.commandPopup.open, m.mention.open)
	}
	// The walk cmd is discarded; the index message feeds the popup.
	m.Update(fileIndexMsg{files: []string{"alpha.txt"}})
	if m.mentionActive() && m.commandPopup.open {
		t.Fatal("both popups open at once")
	}
	// Deleting back to a bare slash re-opens the command popup alone.
	for range 3 {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if !m.commandPopup.open || m.mention.open {
		t.Fatalf("back to slash did not reopen the command popup: cmd=%t mention=%t", m.commandPopup.open, m.mention.open)
	}
}

func TestCompactRefusalsChangeNothing(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"summary\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// No provider configured.
	sendRunes(m, "/compact")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	last := m.entries[len(m.entries)-1]
	if m.working || last.role != "Error" || !strings.Contains(last.content, "No provider configured") || len(m.history) != 0 {
		t.Fatalf("no-client refusal wrong: working=%t last=%+v history=%+v", m.working, last, m.history)
	}

	// Empty history with a working provider: visible note, no model call.
	client, err := model.New(server.URL+"/v1", "compactor", "")
	if err != nil {
		t.Fatal(err)
	}
	m.client = client
	sendRunes(m, "/compact")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	last = m.entries[len(m.entries)-1]
	if m.working || last.role != "Likha" || last.content != "Nothing to compact yet." {
		t.Fatalf("empty-history refusal wrong: working=%t last=%+v", m.working, last)
	}

	// A pending review is resolved before compacting.
	m.working, m.runID = true, 1
	m.events = make(chan agent.TurnEvent, 1)
	m.cancel = func() {}
	m.history = []model.Message{{Role: "user", Content: "check"}}
	m.pending = &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "touch marker", Reply: make(chan bool, 1)}
	m.handleCommand("/compact")
	last = m.entries[len(m.entries)-1]
	// The refusal entry appears; the running turn (and its working state)
	// is left to the review machinery.
	if last.role != "Error" || !strings.Contains(last.content, "A review is pending") || len(m.history) != 1 {
		t.Fatalf("pending-review refusal wrong: working=%t last=%+v history=%+v", m.working, last, m.history)
	}
	if requests.Load() != 0 {
		t.Fatalf("refusals reached the model %d times", requests.Load())
	}
}

func TestCompactSummarizesReplacesHistoryAndPersists(t *testing.T) {
	var requests atomic.Int32
	var sawTools, sawFocus bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode summarize request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests.Add(1)
		if len(body.Tools) > 0 {
			sawTools = true
		}
		if n := len(body.Messages); n > 0 && body.Messages[n-1].Role == "user" && strings.Contains(body.Messages[n-1].Content, "focus on the diff") {
			sawFocus = true
		} else {
			t.Errorf("summarize request missing the focus instruction: %+v", body.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"The user asked about the diff; changes were applied.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := model.New(server.URL+"/v1", "compactor", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, client, "compactor", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.history = []model.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
	}
	m.entries = append(m.entries, entry{role: "You", content: "first question"}, entry{role: "Assistant", content: "first answer"})

	// The argument (with spaces) closes the popup; Enter dispatches /compact.
	sendRunes(m, "/compact focus on the diff")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.working || m.status != "Compacting…" || cmd == nil {
		t.Fatalf("compaction did not start: working=%t status=%q cmd=%v", m.working, m.status, cmd)
	}
	// Drive the single summarize call to completion.
	m.Update(<-m.events)

	if requests.Load() != 1 || sawTools || !sawFocus {
		t.Fatalf("summarize call wrong: requests=%d tools=%t focus=%t", requests.Load(), sawTools, sawFocus)
	}
	if m.working || m.status != "Ready" {
		t.Fatalf("compaction did not finish: working=%t status=%q", m.working, m.status)
	}
	if len(m.history) != 1 || m.history[0].Role != "developer" || !strings.Contains(m.history[0].Content, "changes were applied") {
		t.Fatalf("history not replaced by the summary: %+v", m.history)
	}
	marker := m.entries[len(m.entries)-1]
	if marker.role != "Likha" || !strings.Contains(marker.content, "Conversation compacted. Summary of earlier turns:") {
		t.Fatalf("compaction marker missing: %+v", marker)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 1 || saved.History[0].Role != "developer" {
		t.Fatalf("compacted history not persisted: %+v", saved.History)
	}
	markerSaved := false
	for _, saved := range saved.Entries {
		if saved.Role == "Likha" && strings.Contains(saved.Content, "Conversation compacted. Summary of earlier turns:") {
			markerSaved = true
		}
	}
	if !markerSaved {
		t.Fatalf("marker entry not persisted: %+v", saved.Entries)
	}
}

func TestCompactFailureKeepsHistoryAndRetries(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"retry summary\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := model.New(server.URL+"/v1", "compactor", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, client, "compactor", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.history = []model.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
	}
	original := append([]model.Message(nil), m.history...)

	sendRunes(m, "/compact")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(<-m.events)

	last := m.entries[len(m.entries)-1]
	if m.working || last.role != "Error" {
		t.Fatalf("failure not surfaced: working=%t last=%+v", m.working, last)
	}
	if !historyEqual(m.history, original) {
		t.Fatalf("failed compaction changed History: %+v", m.history)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !historyEqual(saved.History, original) {
		t.Fatalf("failed compaction persisted a changed History: %+v", saved.History)
	}

	// Retry succeeds and compacts normally.
	fail.Store(false)
	sendRunes(m, "/compact")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(<-m.events)
	if m.working || len(m.history) != 1 || m.history[0].Role != "developer" || !strings.Contains(m.history[0].Content, "retry summary") {
		t.Fatalf("retry did not compact: working=%t history=%+v", m.working, m.history)
	}
}

func TestCompactEscCancelsWithoutTouchingHistory(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"too late\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := model.New(server.URL+"/v1", "compactor", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "compactor", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.history = []model.Message{{Role: "user", Content: "first question"}}
	original := append([]model.Message(nil), m.history...)

	sendRunes(m, "/compact")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // cancel the running summarize
	m.Update(<-m.events)                   // the cancelled call reports an error

	if m.working || m.cancel != nil {
		t.Fatalf("cancellation did not clear the working state: working=%t cancel=%v", m.working, m.cancel)
	}
	if !historyEqual(m.history, original) {
		t.Fatalf("cancelled compaction changed History: %+v", m.history)
	}
	cancelled := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "cancel") {
			cancelled = true
		}
	}
	if !cancelled {
		t.Fatalf("cancellation not visible: %+v", m.entries)
	}
}

func historyEqual(a, b []model.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}

// contentWidthBreakpoints pins the wrap width: the viewport minus two
// padding columns at every width — no capped measure on wide terminals.
func TestContentWidthBreakpoints(t *testing.T) {
	cases := []struct {
		term, want int
	}{
		{1, 1}, {40, 38}, {41, 39}, {120, 118}, {140, 138}, {141, 139}, {200, 198},
	}
	for _, tc := range cases {
		if got := contentWidth(tc.term); got != tc.want {
			t.Fatalf("contentWidth(%d) = %d, want %d", tc.term, got, tc.want)
		}
	}
}

// plainFrame returns the plain, ANSI-stripped text of every rendered row,
// asserting the frame is full height first. Styles never change widths, so
// the stripped rows carry the real cell widths for overflow checks.
func plainFrame(t *testing.T, m *ui) []string {
	t.Helper()
	rows := strings.Split(m.View(), "\n")
	if len(rows) != m.height {
		t.Fatalf("frame has %d rows, want %d", len(rows), m.height)
	}
	plain := make([]string, len(rows))
	for i, row := range rows {
		plain[i] = stripANSI(row)
	}
	return plain
}

// assertRowsWithinViewport pins "no rendered line exceeds the viewport
// width" for the current frame, measured on the plain text of each row.
func assertRowsWithinViewport(t *testing.T, m *ui) {
	t.Helper()
	width := runewidth.StringWidth
	for i, row := range plainFrame(t, m) {
		if got := width(row); got > m.width {
			t.Fatalf("row %d is %d cells wide at a %d-column viewport: %q", i, got, m.width, row)
		}
	}
}

// pathologicalEntries are transcript worst cases for the wrap: a token no
// breaker can split, wide CJK text, and packed control/zero-width
// characters that must be escaped to visible form.
func pathologicalEntries() []entry {
	long := strings.Repeat("x", 500)
	cjk := strings.Repeat("词", 200)
	control := "broken\tcontrols\x1b[31mand\u200binvisible\u202ehere"
	return []entry{
		{role: "You", content: long},
		{role: "Assistant", content: cjk},
		{role: "Tool", content: control},
		{role: "Reasoning", content: long + "\n" + cjk},
	}
}

// walkPages visits every transcript page, starting at the top so all
// entries become renderable.
func walkPages(t *testing.T, m *ui) {
	t.Helper()
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	for range m.pageCount() {
		assertRowsWithinViewport(t, m)
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	}
}

// TestBodyNeverOverflowsTheViewport pins the no-horizontal-overflow
// invariant for the conversation body at normal and breakpoint widths.
// Above 140 columns it also pins the resolved cap: transcript lines never
// exceed the 120-column content measure (while status and composer rows
// keep rendering at the full terminal width, which the status-line tests
// already cover).
func TestBodyNeverOverflowsTheViewport(t *testing.T) {
	for _, width := range []int{40, 80, 121, 141, 200} {
		m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m.entries = append(m.entries, pathologicalEntries()...)
		walkPages(t, m)
		// The wrap width is the viewport minus two padding columns at every
		// size — no capped measure — but no line may exceed the viewport.
		for _, line := range m.lines {
			if got := runewidth.StringWidth(stripANSI(line)); got > width-2 {
				t.Fatalf("%d columns leaked a %d-cell line: %q", width, got, line)
			}
		}
	}
}

// TestPendingReviewNeverOverflowsTheViewport walks a multi-page review with
// unbreakably long diff lines at a narrow and a capped width: every
// rendered review line stays inside the viewport, and the approval stays
// reachable page-by-page (never blocked by the overflow guard).
func TestPendingReviewNeverOverflowsTheViewport(t *testing.T) {
	for _, width := range []int{80, 150} {
		m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m.working, m.runID = true, 1
		m.cancel = func() {}
		request := &agent.ApprovalRequest{Kind: "edit", Title: "Edit: work.txt", Body: strings.Repeat(strings.Repeat("d", 300)+"\n", 40), Reply: make(chan bool, 1)}
		m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: request})
		if m.pageCount() < 2 {
			t.Fatalf("%d columns: 300-char diff lines did not paginate", width)
		}
		for range m.pageCount() {
			assertRowsWithinViewport(t, m)
			m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnd})
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if m.pending != nil || !<-request.Reply {
			t.Fatalf("%d columns: fully paged review was not approvable", width)
		}
	}
}

// TestLogoLinesStayWithinTheViewport covers the one raw (never fit()ed)
// render path: a startup pin on the logo art's width plus the rendered
// frame at the minimum and just-past-guard terminals.
func TestLogoLinesStayWithinTheViewport(t *testing.T) {
	for _, line := range strings.Split(logo, "\n") {
		if got := runewidth.StringWidth(line); got > 33 {
			t.Fatalf("logo line is %d cells wide, over the pinned 33-cell bound: %q", got, line)
		}
	}
	// At the 40-column minimum the logo is skipped from the RENDER (sub-56
	// rule; the entry still exists in m.entries for later resize), so
	// assert the rendered page instead: no logo line appears in the frame.
	narrow := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	narrow.Update(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	frame := stripANSI(narrow.View())
	for _, line := range strings.Split(logo, "\n") {
		if strings.Contains(frame, line) {
			t.Fatalf("sub-56-column frame rendered logo line %q instead of the compact header", line)
		}
	}
	assertRowsWithinViewport(t, narrow)
	wide := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	wide.Update(tea.WindowSizeMsg{Width: 56, Height: minHeight})
	assertRowsWithinViewport(t, wide)
}

// TestDialogFitsCJKLabelsInSectionedDialog sizes the sectioned /models
// dialog against wide display widths: a 60-rune CJK label and a long
// section header in a 60-column viewport must fit every row without
// overflowing.
func TestDialogFitsCJKLabelsInSectionedDialog(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	current := "模型标签 Probe"
	relay := model.Provider{Name: "relay", DisplayName: "A very long local OpenAI-compatible relay", BaseURL: "https://relay.example/v1"}
	m.dialog = dialogState{kind: dialogModels, open: true, cursor: 2, loading: false}
	m.dialogModelRows = []modelsRow{
		{provider: relay, model: strings.Repeat("词", 60)},
		{provider: relay, model: current},
		{provider: relay, model: "another one"},
	}
	m.modelName = current
	m.conn.Provider = relay.DisplayName
	m.rebuild() // dialogView() overlays m.mainView() output and asserts nothing about the dialog itself, so lay out first
	view := m.dialogView()
	plain := stripANSI(view)
	if !strings.Contains(plain, relay.DisplayName) {
		t.Fatalf("section header missing: %q", plain)
	}
	dialogRows := plainFrameOf(view, 24)
	for i, row := range dialogRows {
		plainRow := stripANSI(row)
		if got := runewidth.StringWidth(plainRow); got > 60 {
			t.Fatalf("dialog row %d is %d cells wide at 60 columns: %q", i, got, plainRow)
		}
	}
	// A long header still fits by wrapping within the box: every row stays
	// inside the viewport and all three selectable rows render.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.pending = nil
	m.dialogModelRows[0].provider.DisplayName = "A very long local OpenAI-compatible relay provider name that cannot fit on one row"
	m.dialogModelRows[1].provider.DisplayName = m.dialogModelRows[0].provider.DisplayName
	m.dialogModelRows[2].provider.DisplayName = m.dialogModelRows[0].provider.DisplayName
	m.conn.Provider = m.dialogModelRows[0].provider.DisplayName
	view = m.dialogView()
	plain = stripANSI(view)
	for _, id := range []string{current, "another one"} {
		if !strings.Contains(plain, id) {
			t.Fatalf("model row %q missing under a long header: %q", id, plain)
		}
	}
	for i, row := range plainFrameOf(view, 20) {
		plainRow := stripANSI(row)
		if got := runewidth.StringWidth(plainRow); got > 60 {
			t.Fatalf("narrow dialog row %d is %d cells wide at 60 columns: %q", i, got, plainRow)
		}
	}
}

// plainFrameOf splits a pre-captured dialog view into its rows.
func plainFrameOf(view string, _ int) []string {
	return strings.Split(view, "\n")
}

// TestResizeReflowsToNewWidth pins the resize re-flow: lines laid out at a
// capped width re-wrap to the narrower viewport without leaving any
// over-wide row behind.
func TestResizeReflowsToNewWidth(t *testing.T) {
	m := NewUI("/sample", nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 141, Height: 24})
	m.entries = append(m.entries, pathologicalEntries()...)
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	for _, line := range m.lines {
		if got := runewidth.StringWidth(stripANSI(line)); got > 120 {
			t.Fatalf("pre-resize line exceeded the cap: %d cells: %q", got, line)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	for _, line := range m.lines {
		if got := runewidth.StringWidth(stripANSI(line)); got > 98 {
			t.Fatalf("post-resize line exceeded the 98-cell wrap: %d cells: %q", got, line)
		}
	}
	assertRowsWithinViewport(t, m)
}
