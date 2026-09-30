package app

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"lisa/internal/mcp"
	"lisa/internal/model"
	"lisa/internal/session"
)

func statusTestUI(t *testing.T, style string, width, height int, options storedStatusLineConfig) *ui {
	t.Helper()
	m := newUI("/sample", nil, nil, "model-alpha", connection{provider: "Local", verified: true, composerStyle: style, statusLine: options}, t.TempDir(), nil, session.Snapshot{ID: "sample-session"})
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func TestStatusViewportAcrossComposerStyles(t *testing.T) {
	for _, style := range composerStyles {
		for _, size := range [][2]int{{40, 12}, {80, 24}, {100, 24}} {
			m := statusTestUI(t, style, size[0], size[1], storedStatusLineConfig{})
			m.entries = append(m.entries, entry{role: "Assistant", content: "A visible transcript answer"})
			view := m.View()
			assertViewport(t, view, size[0], size[1])
			if !strings.Contains(view, "A visible transcript answer") {
				t.Fatalf("%s at %dx%d lost transcript", style, size[0], size[1])
			}
			status := m.statusLineRows(1, 1)
			if len(status) != m.statusLineHeight() || !strings.Contains(stripANSI(status[0]), "Local · model-alpha · ctx —") {
				t.Fatalf("%s at %dx%d status = %q", style, size[0], size[1], status)
			}
			// The Lisa mark moved to the far right end of the row (after the
			// page hint) and only renders at 70 columns and up.
			if size[0] >= 70 && !strings.HasSuffix(stripANSI(status[0]), "Lisa") {
				t.Fatalf("%s at %dx%d lost the bottom-right mark: %q", style, size[0], size[1], status[0])
			}
			if size[0] < 70 && strings.Contains(stripANSI(strings.Join(status, "\n")), "Lisa") {
				t.Fatalf("%s at %dx%d kept the mark narrow: %q", style, size[0], size[1], status)
			}
			if !strings.Contains(stripANSI(status[len(status)-1]), "Page 1/1") {
				t.Fatalf("page controls lost for %s at %dx%d", style, size[0], size[1])
			}
		}
	}
}

func TestStatusOptionalOrderAndNarrowControls(t *testing.T) {
	allOn := storedStatusLineConfig{
		Branch: statusFlag(true), Changes: true, Staged: true, MCP: true,
		Session: true, Minutes: true, Tokens: true, Version: true, Update: true,
	}
	m := statusTestUI(t, "minimal", 200, 24, allOn)
	m.statusFolder = "~/work"
	m.git, m.gitOK = gitState{Branch: "feature", Staged: 1, Dirty: 3}, true
	m.statusTitle = "First prompt"
	m.started = time.Now().Add(-90 * time.Second)
	m.usageSeen = true
	m.usagePrompt, m.usageCompletion = 1200, 300
	m.lastPromptTokens = 1200
	m.updateVersion = "v0.2.0"
	m.conn.mcp = deterministicMcp()
	row := stripANSI(m.statusLineRows(1, 1)[0])
	previous := -1
	for _, text := range []string{
		"Local · model-alpha",
		"ctx —",
		"~/work",
		"feature",
		"1 staged",
		"3 changed",
		"mcp 1/2 crashed",
		"First prompt",
		envSegment(),
		"minutes 1m",
		"tokens 1500",
		"v" + strings.TrimPrefix(Version, "v"),
		"update → v0.2.0",
	} {
		index := strings.Index(row, text)
		if index <= previous {
			t.Fatalf("field %q missing or out of order: %q", text, row)
		}
		previous = index
	}
	// The Lisa mark closes the row at the far right end.
	if !strings.HasSuffix(row, "Lisa") {
		t.Fatalf("bottom-right mark missing: %q", row)
	}
	plain := statusTestUI(t, "minimal", 100, 24, storedStatusLineConfig{})
	defaultRow := stripANSI(plain.statusLineRows(1, 1)[0])
	for _, future := range []string{"cost ", "tokens ", "minutes ", "update ", " changed", " staged"} {
		if strings.Contains(defaultRow, future) {
			t.Fatalf("disabled or future field %q visible: %q", future, defaultRow)
		}
	}
	if strings.Contains(defaultRow, "~/work") || strings.Contains(defaultRow, "First prompt") {
		t.Fatalf("disabled or future fields visible: %q", defaultRow)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	assertViewport(t, m.View(), 40, 12)
	rows := m.statusLineRows(1, 1)
	if strings.Contains(stripANSI(rows[0]), "Lisa") {
		t.Fatalf("narrow layout kept the wordmark: %q", rows[0])
	}
	if !strings.Contains(stripANSI(rows[1]), "Page 1/1") || !strings.Contains(stripANSI(rows[1]), "PgUp/PgDn") || strings.Contains(stripANSI(rows[0]), "First prompt") {
		t.Fatalf("narrow line did not hide optional fields before controls: %q", rows)
	}
	m.working = true
	m.status = "Cancelling"
	if last := stripANSI(m.statusLineRows(1, 1)[1]); !strings.Contains(last, "Cancelling") || !strings.Contains(last, "^C") {
		t.Fatalf("cancellation controls lost: %q", last)
	}
}

func TestStatusLongDraftAndMentionPopupKeepTranscriptVisible(t *testing.T) {
	for _, style := range composerStyles {
		m := statusTestUI(t, style, 40, 12, storedStatusLineConfig{})
		m.entries = append(m.entries, entry{role: "Assistant", content: "visible answer"})
		m.input = []rune(strings.Repeat("draft ", 120) + "@file")
		m.mention = mentionState{open: true, matches: []string{"file-one", "file-two", "file-three", "file-four", "file-five"}}
		view := m.View()
		assertViewport(t, view, 40, 12)
		if m.bodyHeight() < 1 || !strings.Contains(view, "visible answer") || !strings.Contains(view, "@file") || !strings.Contains(view, "file-one") || !strings.Contains(view, "Page 1/1") {
			t.Fatalf("%s lost transcript, draft, popup, or status: %q", style, view)
		}
		m.mention.cursor = 4
		view = m.View()
		assertViewport(t, view, 40, 12)
		if !strings.Contains(view, "> file-five") {
			t.Fatalf("%s hid the selected completion behind the narrow popup window: %q", style, view)
		}
	}
}

func TestStatusReviewPagesRemainReachableAfterResize(t *testing.T) {
	m := statusTestUI(t, "bordered", 100, 24, storedStatusLineConfig{})
	m.working, m.runID = true, 1
	m.events = make(chan turnEvent, 1)
	request := &approvalRequest{Kind: "command", Title: "Review command", Body: strings.Repeat("a review line\n", 70), Reply: make(chan bool, 1)}
	m.Update(turnEvent{runID: 1, kind: "approval", approval: request})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	pages := m.pageCount()
	if pages < 2 || pages != len(m.reviewSeen) || m.bodyHeight() < 1 {
		t.Fatalf("review boundaries wrong: pages=%d seen=%d body=%d", pages, len(m.reviewSeen), m.bodyHeight())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(request.Reply) != 0 || m.pending == nil {
		t.Fatal("review gate skipped unread pages")
	}
	for page := range pages {
		last := stripANSI(strings.Split(m.View(), "\n")[11])
		if !strings.Contains(last, "Y/N") || !strings.Contains(last, "PgUp/PgDn") || !strings.Contains(last, "Page ") {
			t.Fatalf("review controls hidden on page %d: %q", page, last)
		}
		assertViewport(t, m.View(), 40, 12)
		m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(request.Reply) != 1 || !<-request.Reply {
		t.Fatal("approval remained gated after reviewing all pages")
	}
}

func TestStatusTitleUpdatesOnPromptSubmission(t *testing.T) {
	client, err := model.New("http://127.0.0.1:1/v1", "local", "test")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI(t.TempDir(), nil, client, "local", connection{provider: "Local", statusLine: storedStatusLineConfig{Session: true}}, t.TempDir(), nil, session.Snapshot{ID: "fresh-session"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.startTurn("First real prompt")
	defer m.cancel()
	defer close(m.abandon)
	if m.statusTitle != "First real prompt" || !strings.Contains(stripANSI(m.statusLineRows(1, 1)[0]), "First real prompt") {
		t.Fatalf("submitted prompt did not replace session fallback: %q", m.statusTitle)
	}
}

func TestStatusSessionTitleAndBranchRefresh(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/first")
	run("commit", "-qm", "initial", "--allow-empty")
	options := storedStatusLineConfig{Branch: statusFlag(true), Session: true}
	m := newUI(root, nil, nil, "local", connection{provider: "Local", statusLine: options}, t.TempDir(), nil, session.Snapshot{ID: "new-session-id"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if !m.gitOK || m.git.Branch != "first" || !strings.Contains(stripANSI(m.statusLineRows(1, 1)[0]), "first") {
		t.Fatalf("configured git branch not displayed: git=%+v ok=%t", m.git, m.gitOK)
	}
	m.history = append(m.history, model.Message{Role: "user", Content: "First question"})
	m.refreshStatusSessionTitle()
	if m.statusTitle != "First question" {
		t.Fatalf("first prompt not reflected before turn completion: %q", m.statusTitle)
	}
	m.working, m.runID = true, 1
	m.events = make(chan turnEvent, 1)
	m.cancel = func() {}
	run("symbolic-ref", "HEAD", "refs/heads/next")
	m.Update(turnEvent{runID: 1, kind: "tool_result", text: "done", history: m.history})
	// An unborn branch renders "No commits yet on next"; the segment shows
	// the bare name.
	if !strings.Contains(m.git.Branch, "next") || !strings.Contains(stripANSI(m.statusLineRows(1, 1)[0]), "next") {
		t.Fatalf("branch did not refresh after tool result: %q", m.git.Branch)
	}
	m.Update(turnEvent{runID: 1, kind: "done", history: m.history})
	if m.statusTitle != "First question" {
		t.Fatalf("completed turn lost title: %q", m.statusTitle)
	}
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	other.Entries = []session.Entry{{Role: "user", Content: "Resumed question"}}
	if err := store.Save(other); err != nil {
		t.Fatal(err)
	}
	m.store = store
	m.resumeSession(other.ID)
	if m.statusTitle != "Resumed question" {
		t.Fatalf("resume displayed stale session: %q", m.statusTitle)
	}
	// Detached HEAD renders the short SHA as the branch text.
	run("commit", "-qm", "on-next", "--allow-empty")
	run("checkout", "-q", "--detach")
	m.working, m.runID = true, 2
	m.events = make(chan turnEvent, 1)
	m.cancel = func() {}
	m.Update(turnEvent{runID: 2, kind: "tool_result", history: m.history})
	if !m.git.Detached || len(m.git.Branch) != 7 || !strings.Contains(stripANSI(m.statusLineRows(1, 1)[0]), m.git.Branch) {
		t.Fatalf("detached HEAD lost its short SHA: git=%+v", m.git)
	}
}

func deterministicMcp() *mcpManager {
	return &mcpManager{
		servers: map[string]mcp.ServerConfig{"a": {Command: "a"}, "b": {Command: "b"}},
		clients: map[string]*mcp.Client{"a": {}},
		trusted: map[string]bool{},
		failed:  map[string]string{"b": "exit status 1"},
	}
}

func TestStatusCtxPercentAndWarningThreshold(t *testing.T) {
	m := statusTestUI(t, "minimal", 100, 24, storedStatusLineConfig{})
	m.modelName = "gpt-4o" // known catalog window: 128000
	m.usageSeen = true
	for _, tc := range []struct {
		tokens int64
		text   string
	}{
		{64000, "ctx 50%"},
		{102400, "ctx 80%"}, // 80% lands exactly on the warning threshold
	} {
		m.lastPromptTokens = tc.tokens
		if got := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(got, tc.text) {
			t.Fatalf("%d/128000 tokens missing %q: %q", tc.tokens, tc.text, got)
		}
	}
	m.lastPromptTokens = 64000
	if warning := m.contextSegment(); warning.style.Render(warning.text) != m.theme.Normal.Render(warning.text) {
		t.Fatalf("50%% context must not use the Warning role: %q", warning.text)
	}
	m.lastPromptTokens = 102400
	if warning := m.contextSegment(); warning.style.Render(warning.text) != m.theme.Warning.Render(warning.text) {
		t.Fatalf("80%% context lost its Warning role: %q", warning.text)
	}
	// Below the threshold the measured percentage keeps the muted styling; an
	// unknown model window or unseen usage falls back to the placeholder.
	m.lastPromptTokens = 64000
	m.modelName = "model-alpha"
	if got := m.contextSegment(); got.text != "ctx —" {
		t.Fatalf("unknown model window rendered %q", got.text)
	}
}

func TestStatusSegmentsHonorTogglesAndMeasurements(t *testing.T) {
	cases := []struct {
		option  storedStatusLineConfig
		off     storedStatusLineConfig // Folder/Branch default-on: an explicit false is the off state
		present string
	}{
		{storedStatusLineConfig{Folder: statusFlag(true)}, storedStatusLineConfig{Folder: statusFlag(false)}, "~/work"},
		{storedStatusLineConfig{Branch: statusFlag(true)}, storedStatusLineConfig{Branch: statusFlag(false)}, "feat"},
		{storedStatusLineConfig{Changes: true}, storedStatusLineConfig{Changes: false}, "3 changed"},
		{storedStatusLineConfig{Staged: true}, storedStatusLineConfig{Staged: false}, "1 staged"},
		{storedStatusLineConfig{MCP: true}, storedStatusLineConfig{MCP: false}, "mcp 1/2"},
		{storedStatusLineConfig{Session: true}, storedStatusLineConfig{Session: false}, "T"},
		{storedStatusLineConfig{Minutes: true}, storedStatusLineConfig{Minutes: false}, "minutes 1m"},
		{storedStatusLineConfig{Tokens: true}, storedStatusLineConfig{Tokens: false}, "tokens 1500"},
		{storedStatusLineConfig{Version: true}, storedStatusLineConfig{Version: false}, "v" + strings.TrimPrefix(Version, "v")},
		{storedStatusLineConfig{Update: true}, storedStatusLineConfig{Update: false}, "update → v0.2.0"},
	}
	fresh := func() *ui {
		m := statusTestUI(t, "minimal", 200, 24, storedStatusLineConfig{})
		m.started = time.Now().Add(-90 * time.Second)
		m.usageSeen = true
		m.usagePrompt, m.usageCompletion = 1200, 300
		m.git, m.gitOK = gitState{Branch: "feat", Staged: 1, Dirty: 3}, true
		m.updateVersion = "v0.2.0"
		m.statusFolder, m.statusTitle = "~/work", "T"
		m.conn.mcp = deterministicMcp()
		return m
	}
	for i, tc := range cases {
		m := fresh()
		m.statusLineOpts = tc.off
		if row := stripANSI(m.statusLineRows(1, 1)[0]); strings.Contains(row, tc.present) {
			t.Fatalf("case %d rendered %q with its toggle off: %q", i, tc.present, row)
		}
		m = fresh()
		m.statusLineOpts = tc.option
		if row := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(row, tc.present) {
			t.Fatalf("case %d lost %q with its toggle on: %q", i, tc.present, row)
		}
	}
	// No measured value may be fabricated: unseen usage renders the ctx
	// placeholder and never a token count, and an unknown newer release
	// renders no update notice.
	m := fresh()
	m.usageSeen = false
	m.updateVersion = ""
	m.statusLineOpts = storedStatusLineConfig{Tokens: true, Update: true}
	row := stripANSI(m.statusLineRows(1, 1)[0])
	if !strings.Contains(row, "ctx —") || strings.Contains(row, "tokens ") || strings.Contains(row, "update ") {
		t.Fatalf("unmeasured data fabricated a segment: %q", row)
	}
	// Zero counts hide their segments rather than reporting "0 changed".
	m = fresh()
	m.git = gitState{Branch: "feat"}
	m.statusLineOpts = storedStatusLineConfig{Changes: true, Staged: true}
	if row := stripANSI(m.statusLineRows(1, 1)[0]); strings.Contains(row, "changed") || strings.Contains(row, "staged") {
		t.Fatalf("empty git state rendered a segment: %q", row)
	}
	// A failed git read hides every git segment, including the branch.
	m = fresh()
	m.git, m.gitOK = gitState{}, false
	m.statusLineOpts = storedStatusLineConfig{Changes: true, Staged: true}
	if row := stripANSI(m.statusLineRows(1, 1)[0]); strings.Contains(row, "feat") || strings.Contains(row, "changed") {
		t.Fatalf("failed git read kept segments: %q", row)
	}
}
