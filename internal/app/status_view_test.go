package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/agent"
	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
)

// statusFlag builds an explicit optional-segment value for config literals;
// the zero value of providers.StoredStatusLineConfig leaves Folder/Branch default-on.
func statusFlag(on bool) *bool { return &on }

// driveTurn runs the working loop until the turn ends, chaining every
// returned command back into the model (as the Bubble Tea runtime does), so
// the auto-naming command returned with the done event actually executes.
func driveTurn(m *ui) {
	for {
		_, cmd := m.Update(waitEvent(m.events)())
		for cmd != nil {
			msg := cmd()
			_, cmd = m.Update(msg)
		}
		if !m.working {
			return
		}
	}
}

func TestHeaderCollapsesToFiftySixColumns(t *testing.T) {
	for _, width := range []int{40, 55, 56, 80, 120} {
		m := newUI("/home/me/repo-x", nil, nil, "local", providers.Connection{Provider: "Local", Verified: true}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		if width >= 56 {
			if lines := m.header(); len(lines) != 0 {
				t.Fatalf("%d columns kept a header: %q", width, lines)
			}
		} else {
			lines := m.header()
			if len(lines) != 1 || lines[0] != "Lisa · repo-x" {
				t.Fatalf("%d columns header = %q", width, lines)
			}
		}
		// Zero-line header must keep the layout sound: a full-height frame
		// with a positive body and page math that never goes negative.
		view := m.View()
		assertViewport(t, view, width, 24)
		if m.bodyHeight() < 1 || m.pageCount() < 1 {
			t.Fatalf("%d columns broke layout math: body=%d pages=%d", width, m.bodyHeight(), m.pageCount())
		}
	}
	// A long repository basename below 56 columns re-wraps into the body
	// instead of clipping.
	m := newUI("/somewhere/very-long-repository-name-for-wrapping", nil, nil, "local", providers.Connection{Provider: "Local", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	assertViewport(t, m.View(), 40, 24)
}

func TestLogoEntryIsPureLogo(t *testing.T) {
	m := newUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if len(m.entries) == 0 || m.entries[0].role != "Logo" {
		t.Fatalf("fresh session missing the logo entry: %+v", m.entries)
	}
	if m.entries[0].content != logo {
		t.Fatalf("logo entry is not the bare logo: %q", m.entries[0].content)
	}
	if strings.Contains(m.View(), "Repository:") {
		t.Fatal("logo block rendered a repository line")
	}
}

func TestLisaMarkPlacementAndRetirement(t *testing.T) {
	newAt := func(width int) *ui {
		m := newUI("/sample", nil, nil, "local", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m.entries = append(m.entries, entry{role: "Assistant", content: "a visible answer"})
		return m
	}
	row := stripANSI(newAt(90).statusLineRows(1, 1)[0])
	if !strings.HasSuffix(row, "Lisa") {
		t.Fatalf("wide row does not end with the mark: %q", row)
	}
	// The mark sits flush right, after the page hint, separated by spaces.
	if strings.HasSuffix(row, " Lisa") && !strings.Contains(row, " · Lisa") && !strings.Contains(row, "  Lisa") && !strings.Contains(row, "· Lisa") {
		t.Fatalf("mark not separated from the hint: %q", row)
	}
	for _, width := range []int{40, 69} {
		m := newAt(width)
		joined := stripANSI(strings.Join(m.statusLineRows(1, 1), "\n"))
		if strings.Contains(joined, "Lisa") {
			t.Fatalf("%d columns rendered the mark: %q", width, joined)
		}
		// The left side never leads with a wordmark at any width.
		left := stripANSI(m.statusLineRows(1, 1)[0])
		if strings.HasPrefix(left, "Lisa") {
			t.Fatalf("%d columns left side leads with the wordmark: %q", width, left)
		}
	}
}

func TestContextSegmentFormats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answered\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":64000,\"completion_tokens\":100}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI("/sample", nil, client, "gpt-4o", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.startTurn("first question")
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m)

	// Wide row: percentage plus the compact used/total pair.
	if got := stripANSI(m.contextSegment().text); got != "ctx 50% · 64k/128k" {
		t.Fatalf("wide ctx = %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if got := stripANSI(m.contextSegment().text); got != "ctx 50%" {
		t.Fatalf("narrow ctx = %q", got)
	}
	// Usage seen but an unknown window: still no fabricated denominator.
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.modelName = "model-alpha"
	if got := stripANSI(m.contextSegment().text); got != "ctx —" {
		t.Fatalf("unknown-window ctx = %q", got)
	}
	// Nothing measured at all.
	m2 := statusTestUI(t, "minimal", 100, 24, providers.StoredStatusLineConfig{})
	if got := stripANSI(m2.contextSegment().text); got != "ctx —" {
		t.Fatalf("unmeasured ctx = %q", got)
	}
}

func TestHumanTokens(t *testing.T) {
	cases := map[int64]string{
		999: "999", 1000: "1k", 68432: "68.4k", 128000: "128k",
		999_999: "1M", 1_000_000: "1M", 1_500_000: "1.5M", 12_000_000: "12M",
	}
	for n, want := range cases {
		if got := humanTokens(n); got != want {
			t.Fatalf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestStatusShowsModelDisplayName(t *testing.T) {
	m := statusTestUI(t, "minimal", 200, 24, providers.StoredStatusLineConfig{})
	m.modelName = "gpt-4o"
	if row := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(row, "GPT-4o") {
		t.Fatalf("mapped slug did not render its display name: %q", row)
	}
	m.modelName = "totally-unknown-model"
	if row := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(row, "totally-unknown-model") || strings.Contains(row, "GPT-4o") {
		t.Fatalf("unknown slug must fall back unchanged: %q", row)
	}
}

func TestEnvSegmentText(t *testing.T) {
	labels := map[string]string{"darwin": "macOS", "linux": "Linux", "windows": "Windows", "freebsd": "freebsd"}
	for goos, want := range labels {
		if got := envLabel(goos); got != want {
			t.Fatalf("envLabel(%q) = %q, want %q", goos, got, want)
		}
	}
	// arch passes through: arm64/amd64 as-is, anything else raw.
	if arch := runtime.GOARCH; !strings.HasSuffix(envSegment(), " "+arch) {
		t.Fatalf("env segment = %q, want arch %q", envSegment(), arch)
	}
}

func TestGitSegmentsRenderFromGitState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	write("tracked.txt", "one\n")
	run("add", ".")
	run("commit", "-qm", "initial")
	// Upstream so a local commit later renders the ahead arrow.
	origin := filepath.Join(t.TempDir(), "origin.git")
	run("init", "-q", "--bare", origin)
	run("remote", "add", "origin", origin)
	run("push", "-q", "-u", "origin", "main")

	// Worktree edit and an untracked file: dirty and untracked segments.
	write("tracked.txt", "one\ntwo\n")
	write("scratch.txt", "new\n")
	m := newUI(repo, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true, StatusLine: providers.StoredStatusLineConfig{Changes: true, Staged: true}}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	row := stripANSI(m.statusLineRows(1, 1)[0])
	if !strings.Contains(row, "main") || !strings.Contains(row, "1 changed") || !strings.Contains(row, "1 untracked") {
		t.Fatalf("dirty/untracked/branch segments missing: %q", row)
	}
	if strings.Contains(row, "staged") || strings.Contains(row, "↓") || strings.Contains(row, "↑") {
		t.Fatalf("zero-state git segments must hide: %q", row)
	}

	// Staged change plus one local commit ahead of upstream: ↑1.
	run("add", "tracked.txt")
	run("commit", "-qm", "second")
	write("extra.txt", "staged\n")
	run("add", "extra.txt")
	write("tracked.txt", "one\ntwo\nthree\n")
	m.working, m.runID = true, 1
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "tool_result", Text: "listed", History: nil})
	row = stripANSI(m.statusLineRows(1, 1)[0])
	if !strings.Contains(row, "1 staged") || !strings.Contains(row, "1 changed") || !strings.Contains(row, "1 untracked") || !strings.Contains(row, "↑1") {
		t.Fatalf("staged/dirty/untracked/ahead segments missing: %q", row)
	}
}

func TestSpendSegmentPricing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answered\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":40000,\"completion_tokens\":1000}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	newSession := func(modelName, canonical string) *ui {
		c, err := model.New(server.URL+"/v1", modelName, "")
		if err != nil {
			t.Fatal(err)
		}
		m := newUI("/sample", nil, c, modelName, providers.Connection{Provider: "OpenAI", ProviderCanonical: canonical, Verified: true}, t.TempDir(), nil, session.Snapshot{})
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
		return m
	}
	// Documented pricing accumulates: 40k prompt + 1k completion on gpt-4o
	// is $0.10 + $0.01 = $0.11 (cents precision under $1).
	m := newSession("gpt-4o", "openai")
	m.startTurn("first question")
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m)
	if !m.spendKnown || m.spendSegment() != "$0.11" {
		t.Fatalf("priced turn spend = %q (known=%t)", m.spendSegment(), m.spendKnown)
	}
	if row := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(row, "$0.11") {
		t.Fatalf("spend segment missing from the row: %q", row)
	}

	// The subscription row renders the known zero.
	m = newSession("gpt-5.5", "chatgpt")
	m.startTurn("first question")
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m)
	if !m.spendKnown || m.spendSegment() != "$0.00" {
		t.Fatalf("subscription spend = %q (known=%t)", m.spendSegment(), m.spendKnown)
	}

	// Unknown pricing keeps the segment hidden.
	m = newSession("mystery-model", "openai")
	m.startTurn("first question")
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m)
	if m.spendKnown || m.spendSegment() != "" {
		t.Fatalf("unknown pricing fabricated spend: %q (known=%t)", m.spendSegment(), m.spendKnown)
	}
}

func TestStatusLineConfigPointerDefaults(t *testing.T) {
	stateDir := t.TempDir()
	// Explicit false disables; a missing key keeps the new default-on.
	if err := os.WriteFile(providers.ConfigFilePath(stateDir), []byte(`{"provider":"openai","model":"gpt-4o-mini","status_line":{"folder":false,"session":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusLine == nil || cfg.StatusLine.Folder == nil || *cfg.StatusLine.Folder {
		t.Fatalf("explicit false not loaded: %+v", cfg.StatusLine)
	}
	if providers.FlagEnabled(cfg.StatusLine.Folder) {
		t.Fatal("explicit false did not disable folder")
	}
	if !providers.FlagEnabled(cfg.StatusLine.Branch) {
		t.Fatal("absent branch key must default on")
	}
	if !cfg.StatusLine.Session {
		t.Fatal("session bool lost")
	}
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"folder":false`) {
		t.Fatalf("explicit false not persisted: %s", data)
	}
	if strings.Contains(string(data), `"branch"`) {
		t.Fatalf("default-on branch key must stay absent from the JSON: %s", data)
	}
}

// namingFixture serves real turns plus auto-naming calls, counting the
// naming requests by their instruction marker.
func namingFixture(t *testing.T, nameContent string, failNaming bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var naming atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Generate a session name") {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"work complete\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		naming.Add(1)
		if failNaming {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\""+nameContent+"\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, &naming
}

func TestAutoSessionNameAppliesAndPersists(t *testing.T) {
	server, naming := namingFixture(t, "Fix Parser Bug", false)
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	client, err := model.New(server.URL+"/v1", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI(root, nil, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true, StatusLine: providers.StoredStatusLineConfig{Session: true}}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.startTurn("I want you to fix the parser bug")
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m)
	if m.working {
		t.Fatal("turn never completed")
	}
	// The fresh-session naming ran exactly once.
	if got := naming.Load(); got != 1 {
		t.Fatalf("naming requests = %d, want 1", got)
	}
	if m.statusTitle != "Fix Parser Bug" || m.snapshot.NamedTitle != "Fix Parser Bug" {
		t.Fatalf("name not applied: title=%q named=%q", m.statusTitle, m.snapshot.NamedTitle)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NamedTitle != "Fix Parser Bug" {
		t.Fatalf("persisted snapshot lost the name: %+v", loaded)
	}
	summaries, err := store.List()
	if err != nil || len(summaries) != 1 || summaries[0].Title != "Fix Parser Bug" {
		t.Fatalf("store list = %+v err=%v", summaries, err)
	}

	// The second turn triggers no new naming request.
	m.startTurn("second question")
	driveTurn(m)
	if got := naming.Load(); got != 1 {
		t.Fatalf("second turn re-ran naming: %d requests", got)
	}
}

func TestAutoSessionNameFailureStaysSilent(t *testing.T) {
	server, naming := namingFixture(t, "ignored", true)
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	client, err := model.New(server.URL+"/v1", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI(root, nil, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true, StatusLine: providers.StoredStatusLineConfig{Session: true}}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.startTurn("first prompt")
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m)
	if got := naming.Load(); got != 1 {
		t.Fatalf("naming requests = %d, want 1", got)
	}
	if m.snapshot.NamedTitle != "" {
		t.Fatalf("failed naming applied a name: %q", m.snapshot.NamedTitle)
	}
	if m.statusTitle != "first prompt" {
		t.Fatalf("derived title lost: %q", m.statusTitle)
	}
	for _, e := range m.entries {
		if e.role == "Error" {
			t.Fatalf("naming failure surfaced an error entry: %+v", m.entries)
		}
	}
}

func TestAutoSessionNameSkipsResumedSessions(t *testing.T) {
	server, naming := namingFixture(t, "Fix Parser Bug", false)
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Entries = []session.Entry{{Role: "user", Content: "an earlier conversation"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	client, err := model.New(server.URL+"/v1", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI(root, nil, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, t.TempDir(), store, snapshot)
	if m.freshSession {
		t.Fatal("a snapshot with entries must not count as fresh")
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.startTurn("follow-up prompt")
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m)
	if got := naming.Load(); got != 0 {
		t.Fatalf("resumed session triggered %d naming requests", got)
	}
}

func TestAutoSessionNameExitBeforeCompletionPersistsNothing(t *testing.T) {
	server, naming := namingFixture(t, "Fix Parser Bug", false)
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	client, err := model.New(server.URL+"/v1", "local", "")
	if err != nil {
		t.Fatal(err)
	}
	m := newUI(root, nil, client, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, t.TempDir(), store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.startTurn("first prompt")
	defer close(m.abandon)
	for m.working {
		// Deliver the turn's events but drop the naming command: an exit
		// before the goroutine's result lands.
		_, cmd := m.Update(waitEvent(m.events)())
		_ = cmd
	}
	if got := naming.Load(); got != 0 {
		t.Fatalf("naming ran without its command being delivered: %d", got)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NamedTitle != "" {
		t.Fatalf("exit before completion persisted a name: %q", loaded.NamedTitle)
	}
	if len(loaded.History) == 0 {
		t.Fatal("exit before completion lost the completed turn")
	}
}
