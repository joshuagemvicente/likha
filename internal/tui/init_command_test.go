package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

// initServer answers every chat request with one plain assistant reply (no
// tool call, so no AGENTS.md proposal) and records each request's last user
// message.
func initServer(t *testing.T) (*httptest.Server, *atomic.Int32, func() []string) {
	t.Helper()
	var requests atomic.Int32
	var mu sync.Mutex
	var lastUsers []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		requests.Add(1)
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode chat request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for i := len(body.Messages) - 1; i >= 0; i-- {
			if body.Messages[i].Role == "user" {
				mu.Lock()
				lastUsers = append(lastUsers, body.Messages[i].Content)
				mu.Unlock()
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"survey notes only\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, &requests, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lastUsers...)
	}
}

func newInitUI(t *testing.T, serverURL string) *ui {
	t.Helper()
	client, err := model.New(serverURL+"/v1", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "gpt-4o", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.nameTried = true // keep the auto-naming request out of the request count
	return m
}

func lastEntry(m *ui) entry {
	return m.entries[len(m.entries)-1]
}

func TestInitRefusalsNeverReachTheModel(t *testing.T) {
	server, requests, _ := initServer(t)

	// No provider configured: refused through the composer, no turn.
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	sendRunes(m, "/init")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if last := lastEntry(m); m.working || m.initRun || last.role != "Error" || !strings.Contains(last.content, "No provider configured") || len(m.history) != 0 {
		t.Fatalf("no-provider refusal wrong: working=%t init=%t last=%+v history=%+v", m.working, m.initRun, last, m.history)
	}

	// Plan mode on: refused with the /plan hint.
	m = newInitUI(t, server.URL)
	m.planMode = true
	sendRunes(m, "/init keep it short")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if last := lastEntry(m); m.working || m.initRun || last.role != "Error" || !strings.Contains(last.content, "turn off /plan to run /init") || len(m.history) != 0 {
		t.Fatalf("plan-mode refusal wrong: working=%t init=%t last=%+v history=%+v", m.working, m.initRun, last, m.history)
	}

	// A run is active: handleCommand refuses without touching the run.
	m = newInitUI(t, server.URL)
	m.working, m.runID = true, 7
	if cmd := m.handleCommand("/init"); cmd != nil {
		t.Fatal("refused /init returned a command")
	}
	if last := lastEntry(m); m.initRun || last.role != "Error" || !strings.Contains(last.content, "A run is active") || m.runID != 7 || len(m.history) != 0 {
		t.Fatalf("active-run refusal wrong: init=%t runID=%d last=%+v", m.initRun, m.runID, last)
	}

	// A review is pending: refused with the review wording.
	m = newInitUI(t, server.URL)
	m.working, m.runID = true, 3
	m.pending = &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "touch marker", Reply: make(chan bool, 1)}
	if cmd := m.handleCommand("/init"); cmd != nil {
		t.Fatal("refused /init returned a command")
	}
	if last := lastEntry(m); m.initRun || last.role != "Error" || !strings.Contains(last.content, "A review is pending") || m.runID != 3 {
		t.Fatalf("pending-review refusal wrong: init=%t runID=%d last=%+v", m.initRun, m.runID, last)
	}

	if n := requests.Load(); n != 0 {
		t.Fatalf("refusals reached the model %d times", n)
	}
}

func TestInitStartsOneLimitedTurnAndNotesMissingProposal(t *testing.T) {
	server, requests, lastUsers := initServer(t)
	m := newInitUI(t, server.URL)
	m.statusLineOpts.Session = true
	m.history = []model.Message{{Role: "user", Content: "earlier question"}, {Role: "assistant", Content: "earlier answer"}}
	m.entries = append(m.entries, entry{role: "You", content: "earlier question"}, entry{role: "Assistant", content: "earlier answer"})
	const guidance = "mention the release script"
	prompt := agent.InitPrompt(guidance)

	sendRunes(m, "/init "+guidance)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.working || !m.initRun || cmd == nil {
		t.Fatalf("/init did not start a turn: working=%t init=%t cmd=%v", m.working, m.initRun, cmd)
	}
	defer close(m.abandon)
	// The transcript shows the short command; the model and saved history
	// get the generated prompt, after the visible earlier conversation.
	var you []string
	for _, e := range m.entries {
		if e.role == "You" {
			you = append(you, e.content)
		}
	}
	if len(you) != 2 || you[1] != "/init "+guidance {
		t.Fatalf("You rows = %q, want the short /init line last", you)
	}
	if n := len(m.history); n != 3 || m.history[2].Role != "user" || m.history[2].Content != prompt || m.history[0].Content != "earlier question" {
		t.Fatalf("history after /init = %+v", m.history)
	}
	if !m.toolRunOptions(m.runID).InitMode {
		t.Fatal("the /init run's options lack InitMode")
	}
	if m.statusTitle != "earlier question" {
		t.Fatalf("status title = %q", m.statusTitle)
	}

	driveTurn(m)
	if requests.Load() != 1 {
		t.Fatalf("/init made %d model requests, want 1", requests.Load())
	}
	if users := lastUsers(); len(users) != 1 || users[0] != prompt {
		t.Fatalf("model received %q, want the generated init prompt", users)
	}
	sawUser := false
	for _, message := range m.history {
		if message.Role == "user" && message.Content == prompt {
			sawUser = true
		}
	}
	if !sawUser {
		t.Fatalf("finished history lost the init prompt: %+v", m.history)
	}
	notes := 0
	for _, e := range m.entries {
		if e.role == "Likha" && e.content == initNoProposalNote {
			notes++
		}
	}
	if notes != 1 || m.initRun || m.initProposed {
		t.Fatalf("no-proposal note count=%d init=%t proposed=%t entries=%+v", notes, m.initRun, m.initProposed, m.entries)
	}

	// The limit was for that run only: the next prompt is a normal turn.
	if m.toolRunOptions(m.runID).InitMode {
		t.Fatal("InitMode outlived the /init run")
	}
	m.startTurn("what next?", nil)
	if m.initRun || m.toolRunOptions(m.runID).InitMode {
		t.Fatal("a normal turn after /init ran in init mode")
	}
	driveTurn(m)
	for _, e := range m.entries[len(m.entries)-4:] {
		if e.content == initNoProposalNote {
			t.Fatalf("a normal turn appended the /init note: %+v", m.entries)
		}
	}
}

func TestInitWithoutGuidanceShowsBareCommandAndShortTitle(t *testing.T) {
	server, _, _ := initServer(t)
	m := newInitUI(t, server.URL)
	m.statusLineOpts.Session = true
	m.handleCommand("/init")
	if !m.working || !m.initRun {
		t.Fatalf("/init did not start: working=%t init=%t", m.working, m.initRun)
	}
	defer close(m.abandon)
	var you []string
	for _, e := range m.entries {
		if e.role == "You" {
			you = append(you, e.content)
		}
	}
	if len(you) != 1 || you[0] != "/init" {
		t.Fatalf("You rows = %q, want just /init", you)
	}
	if len(m.history) != 1 || m.history[0].Content != agent.InitPrompt("") {
		t.Fatalf("history = %+v", m.history)
	}
	// The first prompt titles the session by the short command, not the
	// generated survey prompt.
	if m.statusTitle != "/init" {
		t.Fatalf("status title = %q, want /init", m.statusTitle)
	}
	driveTurn(m)
}

// fakeInitRun puts the UI into a live /init run without a goroutine, so the
// test feeds the run's events itself.
func fakeInitRun(m *ui) {
	m.working, m.runID = true, 41
	m.initRun, m.initProposed = true, false
	m.events = make(chan agent.TurnEvent, 4)
	m.cancel = func() {}
	m.history = []model.Message{{Role: "user", Content: agent.InitPrompt("")}}
}

func TestInitProposalSuppressesTheNote(t *testing.T) {
	for _, title := range []string{"Edit: AGENTS.md", "Edit: ./AGENTS.md"} {
		m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		fakeInitRun(m)
		reply := make(chan bool, 1)
		m.Update(agent.TurnEvent{Kind: "approval", RunID: 41, Approval: &agent.ApprovalRequest{Kind: "edit", Title: title, Body: "+# AGENTS.md\n", Reply: reply}})
		if m.pending == nil || !m.initProposed {
			t.Fatalf("%s: proposal not tracked: pending=%v proposed=%t", title, m.pending, m.initProposed)
		}
		// The user rejects; the run then finishes normally.
		m.Update(agent.TurnEvent{Kind: "done", RunID: 41, History: m.history})
		for _, e := range m.entries {
			if e.content == initNoProposalNote {
				t.Fatalf("%s: note shown after a proposal: %+v", title, m.entries)
			}
		}
		if m.initRun || m.initProposed || m.working {
			t.Fatalf("%s: init state not cleared: init=%t proposed=%t working=%t", title, m.initRun, m.initProposed, m.working)
		}
	}
}

func TestInitNoteOnErrorAndOtherFileProposal(t *testing.T) {
	// A review for another file is not an AGENTS.md proposal.
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	fakeInitRun(m)
	m.Update(agent.TurnEvent{Kind: "approval", RunID: 41, Approval: &agent.ApprovalRequest{Kind: "edit", Title: "Edit: docs/AGENTS.md", Body: "+x\n", Reply: make(chan bool, 1)}})
	if m.initProposed {
		t.Fatal("a nested AGENTS.md review counted as the root proposal")
	}
	m.Update(agent.TurnEvent{Kind: "error", RunID: 41, Text: "provider failed", History: m.history})
	found := false
	for _, e := range m.entries {
		if e.role == "Likha" && e.content == initNoProposalNote {
			found = true
		}
	}
	if !found || m.initRun {
		t.Fatalf("errored /init run without a proposal lacks the note: init=%t entries=%+v", m.initRun, m.entries)
	}

	// A normal run never gets the note.
	m = NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	fakeInitRun(m)
	m.initRun = false
	m.Update(agent.TurnEvent{Kind: "done", RunID: 41, History: m.history})
	for _, e := range m.entries {
		if e.content == initNoProposalNote {
			t.Fatal("a normal run appended the /init note")
		}
	}
}

func TestIsRootAgentsEdit(t *testing.T) {
	cases := []struct {
		request *agent.ApprovalRequest
		want    bool
	}{
		{nil, false},
		{&agent.ApprovalRequest{Kind: "edit", Title: "Edit: AGENTS.md"}, true},
		{&agent.ApprovalRequest{Kind: "edit", Title: "Edit: ./AGENTS.md"}, true},
		{&agent.ApprovalRequest{Kind: "edit", Title: "Edit: sub/AGENTS.md"}, false},
		{&agent.ApprovalRequest{Kind: "edit", Title: "Edit: agents.md"}, false},
		{&agent.ApprovalRequest{Kind: "command", Title: "Edit: AGENTS.md"}, false},
		{&agent.ApprovalRequest{Kind: "edit", Title: "AGENTS.md"}, false},
	}
	for _, c := range cases {
		if got := isRootAgentsEdit(c.request); got != c.want {
			t.Errorf("isRootAgentsEdit(%+v) = %t, want %t", c.request, got, c.want)
		}
	}
}

func TestInitListedInHelpAndCompletion(t *testing.T) {
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.handleCommand("/help")
	if last := lastEntry(m); !strings.Contains(last.content, "/init [guidance]") || !strings.Contains(last.content, "AGENTS.md") {
		t.Fatalf("/help lacks /init: %q", last.content)
	}
	matches := commandMatches("ini")
	if len(matches) != 1 || matches[0].Name != "init" || matches[0].Description == "" {
		t.Fatalf("completion for ini = %+v", matches)
	}
	if !recognizedCommand("/init some guidance") {
		t.Fatal("/init is not a recognized command")
	}
}
