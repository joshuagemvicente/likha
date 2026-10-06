package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

func TestInstallRefusalsNeverReachTheModel(t *testing.T) {
	server, requests, _ := initServer(t)

	refused := func(t *testing.T, m *ui, label, want string) {
		t.Helper()
		last := lastEntry(m)
		if m.installRun || last.role != "Error" || !strings.Contains(last.content, want) {
			t.Fatalf("%s refusal wrong: install=%t last=%+v", label, m.installRun, last)
		}
	}

	// Empty and whitespace-only requests show the usage line, through the
	// composer and through handleCommand.
	m := newInitUI(t, server.URL)
	sendRunes(m, "/install")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	refused(t, m, "empty", installUsage)
	if m.working || len(m.history) != 0 || lastEntry(m).content != "Usage: /install <what to install>" {
		t.Fatalf("empty refusal wrong: working=%t history=%+v last=%+v", m.working, m.history, lastEntry(m))
	}
	if cmd := m.handleCommand("/install    "); cmd != nil || m.working || len(m.history) != 0 {
		t.Fatalf("whitespace request started a turn: working=%t history=%+v", m.working, m.history)
	}
	refused(t, m, "whitespace", installUsage)

	// No provider configured.
	m = NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	sendRunes(m, "/install nvm posix")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	refused(t, m, "no-provider", "No provider configured")
	if m.working || len(m.history) != 0 {
		t.Fatalf("no-provider refusal started a turn: history=%+v", m.history)
	}

	// Plan mode on: refused with the /plan hint.
	m = newInitUI(t, server.URL)
	m.planMode = true
	sendRunes(m, "/install nvm posix")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	refused(t, m, "plan-mode", "turn off /plan to run /install")
	if m.working || len(m.history) != 0 {
		t.Fatalf("plan-mode refusal started a turn: history=%+v", m.history)
	}

	// A run is active: refused without touching the run.
	m = newInitUI(t, server.URL)
	m.working, m.runID = true, 7
	if cmd := m.handleCommand("/install nvm posix"); cmd != nil {
		t.Fatal("refused /install returned a command")
	}
	refused(t, m, "active-run", "A run is active")
	if m.runID != 7 || len(m.history) != 0 {
		t.Fatalf("active-run refusal changed the run: runID=%d history=%+v", m.runID, m.history)
	}

	// A review is pending.
	m = newInitUI(t, server.URL)
	m.working, m.runID = true, 3
	m.pending = &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "touch marker", Reply: make(chan bool, 1)}
	if cmd := m.handleCommand("/install nvm posix"); cmd != nil {
		t.Fatal("refused /install returned a command")
	}
	refused(t, m, "pending-review", "A review is pending")
	if m.runID != 3 {
		t.Fatalf("pending-review refusal changed the run: runID=%d", m.runID)
	}

	// A question is pending (ask dialog open).
	m = newInitUI(t, server.URL)
	m.working, m.runID = true, 5
	m.ask.pending = true
	if cmd := m.handleCommand("/install nvm posix"); cmd != nil {
		t.Fatal("refused /install returned a command")
	}
	refused(t, m, "pending-question", "A question is pending")
	if m.runID != 5 {
		t.Fatalf("pending-question refusal changed the run: runID=%d", m.runID)
	}

	if n := requests.Load(); n != 0 {
		t.Fatalf("refusals reached the model %d times", n)
	}
}

func TestInstallStartsOneTurnWithInstallModeForThatTurnOnly(t *testing.T) {
	server, requests, lastUsers := initServer(t)
	m := newInitUI(t, server.URL)
	m.statusLineOpts.Session = true
	m.history = []model.Message{{Role: "user", Content: "earlier question"}, {Role: "assistant", Content: "earlier answer"}}
	m.entries = append(m.entries, entry{role: "You", content: "earlier question"}, entry{role: "Assistant", content: "earlier answer"})
	const request = "nvm posix"
	prompt := agent.InstallPrompt(request)

	sendRunes(m, "/install "+request)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.working || !m.installRun || cmd == nil {
		t.Fatalf("/install did not start a turn: working=%t install=%t cmd=%v", m.working, m.installRun, cmd)
	}
	defer close(m.abandon)
	// The transcript shows only what was typed; the model and saved history
	// get the generated prompt, after the earlier conversation.
	var you []string
	for _, e := range m.entries {
		if e.role == "You" {
			you = append(you, e.content)
		}
	}
	if len(you) != 2 || you[1] != "/install "+request {
		t.Fatalf("You rows = %q, want the short /install line last", you)
	}
	if n := len(m.history); n != 3 || m.history[2].Role != "user" || m.history[2].Content != prompt || m.history[0].Content != "earlier question" {
		t.Fatalf("history after /install = %+v", m.history)
	}
	if prompt == "/install "+request || !strings.Contains(prompt, request) {
		t.Fatalf("install prompt %q is not the full prompt carrying the request", prompt)
	}
	options := m.toolRunOptions(m.runID)
	if !options.InstallMode || options.InitMode {
		t.Fatalf("the /install run's options: install=%t init=%t", options.InstallMode, options.InitMode)
	}

	driveTurn(m)
	if requests.Load() != 1 {
		t.Fatalf("/install made %d model requests, want 1", requests.Load())
	}
	if users := lastUsers(); len(users) != 1 || users[0] != prompt {
		t.Fatalf("model received %q, want the generated install prompt", users)
	}
	sawUser := false
	for _, message := range m.history {
		if message.Role == "user" && message.Content == prompt {
			sawUser = true
		}
	}
	if !sawUser {
		t.Fatalf("finished history lost the install prompt: %+v", m.history)
	}
	for _, e := range m.entries {
		if e.content == initNoProposalNote {
			t.Fatal("an /install run appended the /init note")
		}
	}

	// The mode was for that run only: the next prompt is a normal turn.
	if m.installRun || m.toolRunOptions(m.runID).InstallMode {
		t.Fatal("InstallMode outlived the /install run")
	}
	m.startTurn("what next?", nil)
	if m.installRun || m.toolRunOptions(m.runID).InstallMode {
		t.Fatal("a normal turn after /install ran in install mode")
	}
	driveTurn(m)
	if users := lastUsers(); len(users) != 2 || users[1] != "what next?" {
		t.Fatalf("second turn sent %q", users)
	}
}

// fakeInstallRun puts the UI into a live /install run without a goroutine,
// so the test feeds the run's events itself.
func fakeInstallRun(m *ui) {
	m.working, m.runID = true, 43
	m.installRun = true
	m.events = make(chan agent.TurnEvent, 4)
	m.cancel = func() {}
	m.history = []model.Message{{Role: "user", Content: agent.InstallPrompt("nvm posix")}}
}

func TestInstallModeClearsOnErrorAndCancel(t *testing.T) {
	// An errored run clears the flag.
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	fakeInstallRun(m)
	m.Update(agent.TurnEvent{Kind: "error", RunID: 43, Text: "provider failed", History: m.history})
	if m.installRun || m.working || m.toolRunOptions(m.runID).InstallMode {
		t.Fatalf("errored /install run kept install mode: install=%t working=%t", m.installRun, m.working)
	}

	// A cancelled run clears the flag too.
	m = NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	fakeInstallRun(m)
	m.cancelling = true
	m.Update(agent.TurnEvent{Kind: "done", RunID: 43, History: m.history})
	if m.installRun || m.working || m.toolRunOptions(m.runID).InstallMode {
		t.Fatalf("cancelled /install run kept install mode: install=%t working=%t", m.installRun, m.working)
	}
}

func TestEscapedInstallSendsLiteralPrompt(t *testing.T) {
	server, requests, lastUsers := initServer(t)
	m := newInitUI(t, server.URL)
	sendRunes(m, "//install x")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.working || m.installRun || cmd == nil {
		t.Fatalf("//install did not start a normal turn: working=%t install=%t", m.working, m.installRun)
	}
	defer close(m.abandon)
	if m.toolRunOptions(m.runID).InstallMode {
		t.Fatal("//install ran in install mode")
	}
	if len(m.history) != 1 || m.history[0].Content != "/install x" {
		t.Fatalf("history = %+v, want the literal /install x", m.history)
	}
	driveTurn(m)
	if requests.Load() != 1 {
		t.Fatalf("//install made %d model requests, want 1", requests.Load())
	}
	if users := lastUsers(); len(users) != 1 || users[0] != "/install x" {
		t.Fatalf("model received %q, want the literal /install x", users)
	}
}

func TestInstallListedInHelpAndCompletion(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.handleCommand("/help")
	if last := lastEntry(m); !strings.Contains(last.content, "/install <request>") || !strings.Contains(last.content, "run each step with approval") {
		t.Fatalf("/help lacks /install: %q", last.content)
	}
	matches := commandMatches("ins")
	if len(matches) != 1 || matches[0].Name != "install" || !strings.Contains(matches[0].Description, "/install <request>") {
		t.Fatalf("completion for ins = %+v", matches)
	}
	if !recognizedCommand("/install nvm posix") {
		t.Fatal("/install is not a recognized command")
	}
}
