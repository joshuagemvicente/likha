package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/hooks"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

// writeHooks writes a private hooks.json into stateDir.
func writeHooks(t *testing.T, stateDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(stateDir, hooks.FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// hookShell writes an executable hook script and returns its path.
func hookShell(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return file
}

func hooksJSON(t *testing.T, event, name, command string, extra string) string {
	t.Helper()
	cmd, _ := json.Marshal(command)
	return fmt.Sprintf(`{"hooks":{%q:[{"name":%q,"command":%s%s}]}}`, event, name, cmd, extra)
}

// hookModel serves one read tool call, then a final answer; it counts
// generating requests.
func hookModel(t *testing.T, readPath string) (*model.Client, *atomic.Int32) {
	t.Helper()
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 && readPath != "" {
			arguments, _ := json.Marshal(map[string]string{"path": readPath})
			call := map[string]any{"index": 0, "id": "read_1", "type": "function", "function": map[string]any{"name": "read", "arguments": string(arguments)}}
			data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"All done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "hook-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return client, &count
}

func hookUI(t *testing.T, root, stateDir string, client *model.Client, store *session.Store, snapshot session.Snapshot) *ui {
	t.Helper()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, client, "hook-model", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, stateDir, store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	return m
}

// runHookCmds executes commands that only fire hooks, feeding their
// warnings back through Update.
func runHookCmds(m *ui, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runHookCmds(m, c)
		}
	case hookWarningsMsg:
		m.Update(msg)
	}
}

func hasEntry(m *ui, role, substring string) bool {
	for _, e := range m.entries {
		if e.role == role && strings.Contains(e.content, substring) {
			return true
		}
	}
	return false
}

func TestHooksInvalidFileStartsNoneAndIsReported(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	// A valid pre_tool hook beside an invalid entry: the whole file is refused.
	writeHooks(t, stateDir, `{"hooks":{"pre_tool":[{"command":"touch `+marker+`"}],"run_end":[{"command":""}]}}`)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, count := hookModel(t, "a.txt")
	m := hookUI(t, root, stateDir, client, nil, session.Snapshot{})
	if m.hooks != nil || !hasEntry(m, "Error", "Hooks disabled: ") || !hasEntry(m, "Error", "run_end[0]: command is required") {
		t.Fatalf("launch did not report the refused file: hooks=%v entries=%+v", m.hooks, m.entries)
	}
	m.handleCommand("/hooks")
	if last := lastEntry(m); !strings.Contains(last.content, "Hooks disabled: ") {
		t.Fatalf("/hooks = %q", last.content)
	}
	m.input = []rune("read a.txt")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	if count.Load() != 2 {
		t.Fatalf("model requests = %d, want 2", count.Load())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a hook from a refused file ran")
	}
}

func TestHooksViewStates(t *testing.T) {
	stateDir := t.TempDir()
	m := hookUI(t, t.TempDir(), stateDir, nil, nil, session.Snapshot{})
	m.handleCommand("/hooks")
	if last := lastEntry(m); !strings.Contains(last.content, "No hooks configured. Add them to "+filepath.Join(stateDir, "hooks.json")) {
		t.Fatalf("/hooks without a file = %q", last.content)
	}

	stateDir = t.TempDir()
	writeHooks(t, stateDir, `{"hooks":{"pre_tool":[{"name":"guard","match":["read","edit*"],"command":"true","timeout":5,"on_error":"allow"}]}}`)
	m = hookUI(t, t.TempDir(), stateDir, nil, nil, session.Snapshot{})
	m.handleCommand("/hooks")
	view := lastEntry(m).content
	for _, want := range []string{`pre_tool "guard": true`, "match read, edit*", "timeout 5s", "on_error allow", "last: not run yet"} {
		if !strings.Contains(view, want) {
			t.Fatalf("/hooks lacks %q:\n%s", want, view)
		}
	}
	m.hooks.Fire(t.Context(), hooks.Event{Event: hooks.PreTool, Tool: &hooks.ToolCall{Name: "read"}})
	m.handleCommand("/hooks")
	if view := lastEntry(m).content; !strings.Contains(view, "last: ok at ") {
		t.Fatalf("/hooks after a run:\n%s", view)
	}

	// /hooks reports during a run instead of being refused or queued.
	m.working = true
	m.input = []rune("/hooks")
	if m.queueRunDraft() || len(m.queue) != 0 || len(m.input) != 0 {
		t.Fatalf("/hooks during a run was queued or kept: queue=%v input=%q", m.queue, string(m.input))
	}
	if view := lastEntry(m).content; !strings.Contains(view, `pre_tool "guard"`) {
		t.Fatalf("/hooks during a run = %q", view)
	}
	m.working = false

	if !strings.Contains(commandHelp, "/hooks") {
		t.Fatal("/help does not list /hooks")
	}
	found := false
	for _, item := range commandMatches("hoo") {
		found = found || item.Name == "hooks"
	}
	if !found {
		t.Fatal("the / autocomplete does not offer /hooks")
	}
}

func TestPromptSubmitBlockKeepsDraftAndSendsNothing(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()
	guard := hookShell(t, `case "$(cat)" in *zebra*) echo "no zebras here" >&2; exit 2;; esac`)
	writeHooks(t, stateDir, hooksJSON(t, "prompt_submit", "guard", guard, ""))
	store, err := session.Open(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	client, count := hookModel(t, "")
	m := hookUI(t, root, stateDir, client, store, snapshot)
	m.input = []rune("tell me about the zebra")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	if count.Load() != 0 {
		t.Fatalf("a blocked prompt sent %d model requests", count.Load())
	}
	if got := string(m.input); got != "tell me about the zebra" {
		t.Fatalf("draft = %q, want the blocked prompt back", got)
	}
	if hasEntry(m, "You", "zebra") || len(m.history) != 0 {
		t.Fatalf("blocked prompt left a You row or history: %+v %+v", m.entries, m.history)
	}
	if last := lastEntry(m); last.role != "Error" || last.content != `Prompt blocked by hook "guard": no zebras here` {
		t.Fatalf("last entry = %+v", last)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range saved.History {
		if strings.Contains(msg.Content, "zebra") {
			t.Fatal("the blocked prompt was persisted to the session")
		}
	}

	// A prompt the hook accepts runs normally.
	m.input = []rune("hello there")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	if count.Load() != 1 || !hasEntry(m, "You", "hello there") || !hasEntry(m, "Assistant", "All done") {
		t.Fatalf("accepted prompt did not run: requests=%d entries=%+v", count.Load(), m.entries)
	}
}

func TestSteerBlockedDropsOnlyThatQueuedPrompt(t *testing.T) {
	m := hookUI(t, t.TempDir(), t.TempDir(), nil, nil, session.Snapshot{})
	m.working, m.runID = true, 7
	m.entries = append(m.entries, entry{role: "Queued", content: "first"}, entry{role: "Queued", content: "second"})
	m.queue = []string{"first", "second"}
	m.Update(agent.TurnEvent{Kind: "steer_blocked", RunID: 7, Text: "first", Err: &hooks.BlockError{Hook: "guard", Reason: "nope"}})
	if hasEntry(m, "Queued", "first") || !hasEntry(m, "Queued", "second") {
		t.Fatalf("queued rows = %+v", m.entries)
	}
	if len(m.queue) != 1 || m.queue[0] != "second" {
		t.Fatalf("queue = %v", m.queue)
	}
	if !hasEntry(m, "Error", `Prompt blocked by hook "guard": nope`) {
		t.Fatal("no blocked-prompt notice")
	}
}

func TestPreToolBlockShownAsRefusedToolItem(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	guard := hookShell(t, `case "$(cat)" in *.env*) echo "no dotenv reads" >&2; exit 2;; esac`)
	writeHooks(t, stateDir, hooksJSON(t, "pre_tool", "no-dotenv", guard, `,"match":["read"]`))
	client, _ := hookModel(t, ".env")
	m := hookUI(t, root, stateDir, client, nil, session.Snapshot{})
	m.input = []rune("read the env")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	reviewed := false
	permE2Epump(t, m, func() bool {
		reviewed = reviewed || m.pending != nil
		return !m.working
	})
	if reviewed {
		t.Fatal("a blocked read opened a review")
	}
	found := false
	for _, record := range m.toolRecords {
		if record.Name == "read" && strings.Contains(record.Content, `Blocked by hook "no-dotenv": no dotenv reads`) && !strings.Contains(record.Content, "TOKEN=x") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no blocked read item: %+v", m.toolRecords)
	}
	if !strings.Contains(m.View(), "no-dotenv") {
		t.Fatal("the transcript does not show the blocking hook")
	}
}

func TestObserveHooksRunOffTheUIAndReportFailures(t *testing.T) {
	stateDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "events.jsonl")
	recorder := hookShell(t, `cat >>"`+log+`"; echo >>"`+log+`"`)
	slow := hookShell(t, `cat >/dev/null; sleep 5`)
	cmd, _ := json.Marshal(recorder)
	slowCmd, _ := json.Marshal(slow)
	writeHooks(t, stateDir, fmt.Sprintf(`{"hooks":{
		"run_end":[{"command":%s},{"name":"slow","command":%s,"timeout":1}],
		"attention":[{"command":%s}],
		"post_tool":[{"command":%s}],
		"session_start":[{"command":%s}]
	}}`, cmd, slowCmd, cmd, cmd, cmd))
	root := t.TempDir()
	m := hookUI(t, root, stateDir, nil, nil, session.Snapshot{ID: "sess-9"})

	// Firing never runs a hook on the UI goroutine: the call returns at once.
	start := time.Now()
	var cmds []tea.Cmd
	for _, state := range []struct {
		cancelling bool
		kind, text string
	}{{false, "done", ""}, {false, "error", "boom"}, {true, "error", "context canceled"}} {
		m.cancelling = state.cancelling
		cmds = append(cmds, m.runEndHooks(agent.TurnEvent{Kind: state.kind, Text: state.text}))
	}
	m.cancelling = false
	cmds = append(cmds, m.attentionMoment(hookAttentionReview), m.fireSessionStart(true))
	_, observeCmd := m.Update(agent.TurnEvent{Kind: "hook_observe", Hook: &hooks.Event{Event: hooks.PostTool, SessionID: "sess-9", Repository: root, Agent: "main", Tool: &hooks.ToolCall{Name: "read"}, Result: &hooks.ToolResult{Status: "succeeded"}}})
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("firing observe hooks blocked the UI for %s", elapsed)
	}
	// hook_observe outside a live run is ignored like every stale event.
	if observeCmd != nil {
		t.Fatal("a stale hook_observe event fired")
	}
	// Live: applyTurnEvent returns only the hook command (Update would also
	// batch the next read of the run's event channel).
	observeCmd = m.applyTurnEvent(agent.TurnEvent{Kind: "hook_observe", Hook: &hooks.Event{Event: hooks.PostTool, SessionID: "sess-9", Repository: root, Agent: "main", Tool: &hooks.ToolCall{Name: "read"}, Result: &hooks.ToolResult{Status: "succeeded"}}})
	for _, c := range append(cmds, observeCmd) {
		runHookCmds(m, c)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes, attention []string
	var sawSession, sawPostTool bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev hooks.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("payload %q: %v", line, err)
		}
		if ev.SessionID != "sess-9" || ev.Repository != root || ev.Agent != "main" || ev.Version != 1 {
			t.Fatalf("common fields = %+v", ev)
		}
		switch ev.Event {
		case hooks.RunEnd:
			outcomes = append(outcomes, ev.Outcome+":"+ev.Error)
		case hooks.Attention:
			attention = append(attention, ev.Attention)
		case hooks.SessionStart:
			sawSession = ev.Resumed != nil && *ev.Resumed
		case hooks.PostTool:
			sawPostTool = ev.Tool != nil && ev.Tool.Name == "read"
		}
	}
	if fmt.Sprint(outcomes) != "[completed: failed:boom cancelled:]" {
		t.Fatalf("run_end outcomes = %v", outcomes)
	}
	// Completed and failed runs need attention; a cancelled run does not.
	if fmt.Sprint(attention) != "[run_end run_end review]" {
		t.Fatalf("attention = %v", attention)
	}
	if !sawSession || !sawPostTool {
		t.Fatalf("session_start=%v post_tool=%v", sawSession, sawPostTool)
	}
	if !hasEntry(m, "Likha", `Hook "slow" (run_end) failed: timed out after 1s.`) {
		t.Fatalf("slow hook failure not reported: %+v", m.entries)
	}
}

func TestRepositoryHookFilesAreIgnored(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	config := `{"hooks":{"pre_tool":[{"command":"touch ` + marker + `"}],"PreToolUse":[{"hooks":[{"type":"command","command":"touch ` + marker + `"}]}]}}`
	for _, dir := range []string{".likha", ".claude"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{".likha/hooks.json", ".claude/settings.json", "hooks.json"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, count := hookModel(t, "a.txt")
	m := hookUI(t, root, stateDir, client, nil, session.Snapshot{})
	if m.hooks != nil || m.hooksErr != nil {
		t.Fatalf("repository files configured hooks: %v %v", m.hooks, m.hooksErr)
	}
	m.input = []rune("read a.txt")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	if count.Load() != 2 {
		t.Fatalf("model requests = %d", count.Load())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a repository hook file ran a command")
	}
}

// Deleting the current session continues in a fresh one; its session_start
// reports a new session, not a resumed one.
func TestSessionStartAfterDeletingTheCurrentSessionIsNotResumed(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "events.jsonl")
	writeHooks(t, stateDir, hooksJSON(t, "session_start", "recorder", hookShell(t, `cat >>"`+log+`"; echo >>"`+log+`"`), ""))
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
	_, cmd := m.Update(sessionDeleteRequestedMsg{ID: snapshot.ID, Current: true})
	runHookCmds(m, cmd)
	if m.snapshot.ID == snapshot.ID || m.snapshot.ID == "" {
		t.Fatalf("no fresh session replaced the deleted one: %q", m.snapshot.ID)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var ev hooks.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &ev); err != nil {
		t.Fatalf("session_start payload %q: %v", data, err)
	}
	if ev.SessionID != m.snapshot.ID || ev.Resumed == nil || *ev.Resumed {
		t.Fatalf("session_start = %+v (resumed %v), want the fresh session reported as not resumed", ev, ev.Resumed)
	}
}
