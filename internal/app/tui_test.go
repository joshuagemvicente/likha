package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lisa/internal/model"
	"lisa/internal/session"
)

func TestUIUsesFixedViewportAndPagesCompleteContent(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
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
	for m.page != 0 {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
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
	m := newUI("/sample", nil, nil, "", connection{setup: true}, stateDir, nil, session.Snapshot{})
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
		t.Fatalf("API key rendered in clear text: %q", m.View())
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
	// A single reported model auto-selects; setup completes immediately.
	if m.mode != modeMain || m.modelName != "zeta-model" || m.client == nil {
		t.Fatalf("auto-select did not complete: mode=%q model=%q client=%v", m.mode, m.modelName, m.client)
	}
	if !strings.Contains(m.View(), "Provider: OpenRouter (accepted)") || !strings.Contains(m.View(), "Model: zeta-model") {
		t.Fatalf("main view missing provider/model: %q", m.View())
	}
	cfg, err := loadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openrouter" || cfg.Model != "zeta-model" {
		t.Fatalf("stored config: %+v %v", cfg, err)
	}
	key, err := storedKey(stateDir, "openrouter")
	if err != nil || key != "sk-test-123" {
		t.Fatalf("stored key = %q err=%v", key, err)
	}
}

func TestSetupModelStageListsAndSelects(t *testing.T) {
	stateDir := t.TempDir()
	m := newUI("/sample", nil, nil, "", connection{setup: true}, stateDir, nil, session.Snapshot{})
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
	if m.mode != modeMain || m.modelName != "beta" {
		t.Fatalf("model selection failed: mode=%q model=%q", m.mode, m.modelName)
	}
	cfg, err := loadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openai" || cfg.Model != "beta" {
		t.Fatalf("stored config: %+v %v", cfg, err)
	}
}

func TestSetupCheckFailureShowsClassifiedErrorAndRetries(t *testing.T) {
	m := newUI("/sample", nil, nil, "", connection{setup: true}, t.TempDir(), nil, session.Snapshot{})
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

func TestUIRequiresResizeBeforePrompt(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 15, Height: 5})
	assertViewport(t, m.View(), 15, 5)
	m.input = []rune("hello")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.working || !strings.Contains(m.View(), "Lisa:") {
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
	m := newUI(root, nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", store, snapshot)
	m.working, m.runID = true, 1
	cancelled := false
	m.cancel = func() { cancelled = true }
	m.events = make(chan turnEvent, 2)
	m.entries = append(m.entries, entry{role: "You", content: "check"})
	m.history = []model.Message{{Role: "user", Content: "check"}}
	completed := []model.Message{{Role: "user", Content: "check"}, {Role: "assistant", ToolCalls: []model.ToolCall{{ID: "one"}}}, {Role: "tool", ToolCallID: "one", Content: "Exit status: 0"}}
	m.events <- turnEvent{runID: 1, kind: "tool_result", text: "run_command: Exit status: 0", history: completed}
	m.events <- turnEvent{runID: 1, kind: "error", text: "context canceled", history: completed}
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
	m.Update(turnEvent{runID: 1, kind: "tool_result", text: "obsolete result"})
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
	m := newUI(root, nil, client, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input = []rune("remember me")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not start")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	select {
	case event := <-m.events:
		m.Update(event)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled model request did not deliver terminal event")
	}
	if m.working || m.status != "Cancelled" || len(m.history) != 1 || m.history[0].Content != "remember me" || m.entries[0].content != "remember me" {
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
	m := newUI(root, nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan turnEvent, 1)
	m.history = []model.Message{{Role: "user", Content: "check"}}
	m.entries = []entry{{role: "You", content: "check"}}
	pending := &approvalRequest{Kind: "command", Title: "Shell command", Body: "touch marker", Reply: make(chan bool, 1)}
	m.Update(turnEvent{runID: 1, kind: "approval", approval: pending})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	history := []model.Message{{Role: "user", Content: "check"}, {Role: "assistant", ToolCalls: []model.ToolCall{{ID: "one"}}}, {Role: "tool", ToolCallID: "one", Content: "Error: action not executed; run interrupted"}}
	m.Update(turnEvent{runID: 1, kind: "error", history: history})
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
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("two")})
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("words")})
	if !strings.Contains(m.View(), "Draft: two words") {
		t.Fatalf("prompt lost its space: %q", m.View())
	}
}

func TestUIRequiresFullDiffReviewBeforeApproval(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	request := &approvalRequest{Kind: "edit", Title: "Edit: work.txt", Body: strings.Repeat("-old\n+new\n", 40), Reply: make(chan bool, 1)}
	m.Update(turnEvent{runID: 1, kind: "approval", approval: request})
	if m.pageCount() < 2 || !strings.Contains(m.View(), "Edit: work.txt") {
		t.Fatal("multi-page diff review not displayed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.pending == nil || len(request.Reply) != 0 {
		t.Fatal("unseen diff pages were approved")
	}
	for range m.pageCount() - 1 {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.pending != nil || !<-request.Reply {
		t.Fatal("reviewed diff was not approvable")
	}
}

func TestUIRejectsCommandWithoutReviewingAllPages(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	request := &approvalRequest{Kind: "command", Title: "Shell command", Body: strings.Repeat("dangerous command\n", 50), Reply: make(chan bool, 1)}
	m.Update(turnEvent{runID: 1, kind: "approval", approval: request})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.pending != nil || <-request.Reply {
		t.Fatal("command rejection was not honored immediately")
	}
}

func TestUIReviewKeepsDecisionsAndPositionVisibleAtTerminalSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		for _, kind := range []string{"edit", "command"} {
			t.Run(fmt.Sprintf("%s-%dx%d", kind, size[0], size[1]), func(t *testing.T) {
				m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.working, m.runID = true, 1
				m.cancel = func() {}
				request := &approvalRequest{Kind: kind, Reply: make(chan bool, 1)}
				label := "EDIT REVIEW"
				if kind == "edit" {
					request.Title = "Edit: work.txt"
					request.Body = "--- a/work.txt\n+++ b/work.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n" + strings.Repeat("-older\n+newer\n", 30) + "END OF DIFF"
				} else {
					label = "COMMAND REVIEW"
					request.Title = "Shell command"
					request.Body = "Working directory: /sample\nCommand:\nprintf reviewed\n\n" + strings.Repeat("argument line\n", 30) + "Approved commands can access files outside this repository and use the network."
				}
				m.Update(turnEvent{runID: 1, kind: "approval", approval: request})
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
						visibleBody.WriteString(strings.ReplaceAll(strings.TrimSpace(row), " ", ""))
					}
					assertViewport(t, view, size[0], size[1])
					for _, visible := range []string{label, fmt.Sprintf("Page %d/%d", page+1, m.pageCount()), "Repo: /sample", "Model: local", "Y approve after review", "N reject", "PgUp/PgDn pages", "Ctrl+C cancel"} {
						if !strings.Contains(view, visible) {
							t.Fatalf("review lost %q on page %d: %q", visible, page+1, view)
						}
					}
					if page == 0 {
						first := "Edit: work.txt"
						if kind == "command" {
							first = "WARNING: No filesystem/network sandbox"
							if !strings.Contains(view, "Working directory: /sample") || !strings.Contains(view, "Detached jobs may survive") {
								t.Fatalf("command warning or working directory missing on first page: %q", view)
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
				if kind == "command" && !strings.Contains(reviewed.String(), "printf reviewed") {
					t.Fatal("command was not visible on any review page")
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
	m := newUI("/sample", nil, nil, "local", connection{provider: "Local OpenAI-compatible", verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID = true, 1
	m.cancel = func() {}
	request := &approvalRequest{Kind: "edit", Title: "Edit: work.txt", Body: strings.Repeat("-old\n+new\n", 36), Reply: make(chan bool, 1)}
	m.Update(turnEvent{runID: 1, kind: "approval", approval: request})
	for range m.pageCount() - 1 {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	}
	m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
	assertViewport(t, m.View(), 39, 11)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.pending == nil || len(request.Reply) != 0 {
		t.Fatal("approval was accepted in an undersized viewport")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	assertViewport(t, m.View(), 40, 12)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.pending == nil || len(request.Reply) != 0 || !strings.Contains(m.View(), "Read all pages") || !strings.Contains(m.View(), "Page 1/") {
		t.Fatal("resized diff was approved without reviewing its new pages or lost page position")
	}
	for range m.pageCount() - 1 {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.pending != nil || !<-request.Reply {
		t.Fatal("fully reviewed resized diff was not approvable")
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
