package headless

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"likha/internal/cmdpolicy"
	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/transcript"
)

func runHeadless(t *testing.T, ctx context.Context, cfg Config) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(ctx, cfg, &out, &errOut)
	return code, out.String(), errOut.String()
}

// decodeEvents parses JSON Lines output, failing on any non-JSON line.
func decodeEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("stdout line is not JSON: %q (%v)", scanner.Text(), err)
		}
		events = append(events, event)
	}
	return events
}

func eventsOfType(events []map[string]any, kind string) []map[string]any {
	var matched []map[string]any
	for _, event := range events {
		if event["type"] == kind {
			matched = append(matched, event)
		}
	}
	return matched
}

func TestCompletedRunPrintsOnlyTheAnswer(t *testing.T) {
	fake := newFakeProvider(t, textReply("The README explains Likha."))
	run := newTestRun(t, fake, "summarize README\nplease")
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", code, stderr)
	}
	if stdout != "The README explains Likha.\n" {
		t.Fatalf("stdout = %q, want only the answer", stdout)
	}
	if !strings.Contains(stderr, "headless run in "+run.root) || !strings.Contains(stderr, "completed · session "+run.cfg.Snapshot.ID) {
		t.Fatalf("stderr lacks start or summary:\n%s", stderr)
	}
	if strings.Contains(stderr, "\x1b[") {
		t.Fatalf("stderr contains ANSI escapes:\n%q", stderr)
	}

	saved, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.NamedTitle != "summarize README" || len(saved.History) != 2 || saved.History[1].Content != "The README explains Likha." {
		t.Fatalf("saved session = title %q, history %+v", saved.NamedTitle, saved.History)
	}
	if len(saved.Entries) != 3 || saved.Entries[0].Role != "You" || saved.Entries[1].Role != "Assistant" || saved.Entries[2].Role != "Turn" {
		t.Fatalf("saved entries = %+v", saved.Entries)
	}
}

func TestHeadlessOffersNoInteractiveTools(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("ask1", "ask_user", map[string]any{"question": "Which file?"}),
		textReply("done"))
	run := newTestRun(t, fake, "go")
	code, _, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	names := toolNames(fake.requests()[0])
	for _, hidden := range []string{"ask_user", "web_search", "web_fetch"} {
		if slices.Contains(names, hidden) {
			t.Fatalf("headless run offered %s: %v", hidden, names)
		}
	}
	for _, offered := range []string{"read", "edit_file", "edit", "run_command"} {
		if !slices.Contains(names, offered) {
			t.Fatalf("headless run did not offer %s: %v", offered, names)
		}
	}
	if !strings.Contains(stderr, "tool ask_user: refused") {
		t.Fatalf("a forged ask_user call was not reported refused:\n%s", stderr)
	}
}

func TestRefusedEditLeavesFilesUnchanged(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("e1", "edit_file", map[string]string{"path": "notes.txt", "content": "new\n"}),
		textReply("I would add notes.txt."))
	run := newTestRun(t, fake, "add notes")
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted || stdout != "I would add notes.txt.\n" {
		t.Fatalf("exit = %d stdout = %q\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(run.root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("a refused edit wrote a file: %v", err)
	}
	if !strings.Contains(stderr, "tool edit_file: refused") || !strings.Contains(stderr, NoReviewerReason) || !strings.Contains(stderr, "1 refusal") {
		t.Fatalf("stderr does not report the refusal:\n%s", stderr)
	}
	// The model received the refusal, not an approval or a rejection.
	requests := fake.requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	messages, _ := requests[1]["messages"].([]any)
	last, _ := messages[len(messages)-1].(map[string]any)
	content, _ := last["content"].(string)
	if last["role"] != "tool" || !strings.Contains(content, NoReviewerReason) || !strings.Contains(content, "Describe the change") || strings.Contains(content, "rejected by user") {
		t.Fatalf("model saw %v", last)
	}
}

func TestCommandsReadOnlyRunRestRefused(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("c1", "run_command", map[string]string{"command": "ls"}),
		toolReply("c2", "run_command", map[string]string{"command": "rm build.log"}),
		toolReply("c3", "run_command", map[string]string{"command": "npm install"}),
		textReply("done"))
	run := newTestRun(t, fake, "look around")
	if err := os.WriteFile(filepath.Join(run.root, "build.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	run.cfg.JSON = true
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("--json wrote to stderr:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(run.root, "build.log")); err != nil {
		t.Fatalf("a refused rm ran: %v", err)
	}
	results := eventsOfType(decodeEvents(t, stdout), "tool_result")
	if len(results) != 3 {
		t.Fatalf("tool_result events = %v", results)
	}
	if results[0]["status"] != "ok" || results[0]["auto_approved"] != "read-only inspection" {
		t.Fatalf("ls result = %v", results[0])
	}
	for _, refused := range results[1:] {
		if refused["status"] != "refused" || !strings.Contains(refused["content"].(string), "was not run") {
			t.Fatalf("refused command result = %v", refused)
		}
	}
}

func TestTrustedCheckRunsUntilItsScriptChanges(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("m1", "run_command", map[string]string{"command": "make test"}),
		textReply("checked"),
		toolReply("m2", "run_command", map[string]string{"command": "make test"}),
		textReply("checked again"))
	run := newTestRun(t, fake, "run the tests")
	makefile := filepath.Join(run.root, "Makefile")
	if err := os.WriteFile(makefile, []byte("test:\n\t@echo ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := providers.TrustCommandChecks(run.cfg.StateDir, run.root, cmdpolicy.RepoChecks(run.root)); err != nil {
		t.Fatal(err)
	}
	run.cfg.JSON = true
	code, stdout, _ := runHeadless(t, context.Background(), run.cfg)
	results := eventsOfType(decodeEvents(t, stdout), "tool_result")
	if code != ExitCompleted || len(results) != 1 || results[0]["status"] != "ok" || results[0]["auto_approved"] != "verification check in a trusted repository" {
		t.Fatalf("trusted make test: exit=%d results=%v", code, results)
	}

	if err := os.WriteFile(makefile, []byte("test:\n\t@echo changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Snapshot, run.cfg.Prompt = loaded, "again"
	code, stdout, _ = runHeadless(t, context.Background(), run.cfg)
	results = eventsOfType(decodeEvents(t, stdout), "tool_result")
	if code != ExitCompleted || len(results) != 1 || results[0]["status"] != "refused" {
		t.Fatalf("changed Makefile: exit=%d results=%v; want a refusal", code, results)
	}
}

func TestJSONStreamShape(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("r1", "read", map[string]string{"path": "."}),
		textReply("answer"))
	run := newTestRun(t, fake, "inspect")
	run.cfg.JSON = true
	code, stdout, _ := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d", code)
	}
	events := decodeEvents(t, stdout)
	first, last := events[0], events[len(events)-1]
	if first["type"] != "start" || first["schema"] != float64(SchemaVersion) || first["session_id"] != run.cfg.Snapshot.ID || first["effort"] != "default" {
		t.Fatalf("first event = %v", first)
	}
	if last["type"] != "result" || last["status"] != "completed" || last["text"] != "answer" || last["tool_calls"] != float64(1) || last["requests"] != float64(2) {
		t.Fatalf("last event = %v", last)
	}
	if len(eventsOfType(events, "result")) != 1 || len(eventsOfType(events, "tool_start")) != 1 || len(eventsOfType(events, "usage")) != 2 {
		t.Fatalf("event counts wrong: %v", events)
	}
	if texts := eventsOfType(events, "text"); len(texts) == 0 || texts[0]["delta"] != "answer" {
		t.Fatalf("text events = %v", texts)
	}
}

func TestFailedRunRedactsAndExitsOne(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		fake := newFakeProvider(t, failReply(http.StatusBadRequest))
		run := newTestRun(t, fake, "hello")
		run.cfg.JSON = jsonMode
		code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
		if code != ExitFailed {
			t.Fatalf("json=%v exit = %d, want 1", jsonMode, code)
		}
		if strings.Contains(stdout+stderr, testKey) {
			t.Fatalf("json=%v output contains the API key:\n%s\n%s", jsonMode, stdout, stderr)
		}
		if !jsonMode {
			if stdout != "" || !strings.Contains(stderr, "Test provider: ") || !strings.Contains(stderr, "[redacted]") {
				t.Fatalf("text failure: stdout=%q stderr=%s", stdout, stderr)
			}
			continue
		}
		events := decodeEvents(t, stdout)
		last := events[len(events)-1]
		if last["status"] != "failed" || last["text"] != "" || !strings.Contains(last["error"].(string), "[redacted]") {
			t.Fatalf("json failure result = %v", last)
		}
	}
}

func TestTimeLimitReportsAndExitsOne(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	fake := newFakeProvider(t, hangReply(release))
	run := newTestRun(t, fake, "slow")
	run.cfg.JSON = true
	run.cfg.MaxTime = 300 * time.Millisecond
	start := time.Now()
	code, stdout, _ := runHeadless(t, context.Background(), run.cfg)
	if code != ExitFailed || time.Since(start) > 10*time.Second {
		t.Fatalf("exit = %d after %s", code, time.Since(start))
	}
	events := decodeEvents(t, stdout)
	if last := events[len(events)-1]; last["status"] != "time_limit" {
		t.Fatalf("result = %v", last)
	}
	saved, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatalf("session not saved after time limit: %v", err)
	}
	assertStoppedEntry(t, saved.Entries, "Run stopped: time limit of 300ms reached; no further tools will execute. Approved shell commands may leave detached processes running.")
}

func TestJSONStartReportsTheEffortSent(t *testing.T) {
	fake := newFakeProvider(t, textReply("answer"))
	run := newTestRun(t, fake, "inspect")
	run.cfg.JSON = true
	run.cfg.Client.SetEffort(model.EffortTopLevel, "high")
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	if first := decodeEvents(t, stdout)[0]; first["type"] != "start" || first["effort"] != "high" {
		t.Fatalf("start event = %v, want effort high", first)
	}
}

func TestInterruptReportsCancelledAndSaves(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	fake := newFakeProvider(t, hangReply(release))
	run := newTestRun(t, fake, "slow prompt")
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel(ErrInterrupted)
	}()
	code, stdout, stderr := runHeadless(t, ctx, run.cfg)
	if code != ExitInterrupted || stdout != "" || !strings.Contains(stderr, "cancelled · session") {
		t.Fatalf("exit = %d stdout = %q\n%s", code, stdout, stderr)
	}
	saved, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil || len(saved.History) == 0 || saved.History[0].Content != "slow prompt" {
		t.Fatalf("saved = %+v, %v", saved.History, err)
	}
	// The stored transcript reads as a TUI cancel, not as a red error.
	assertStoppedEntry(t, saved.Entries, cancelledEntry)
}

// assertStoppedEntry checks that a stopped run stored want as a Likha
// notice and no Error entry, ahead of its cancelled turn footer.
func assertStoppedEntry(t *testing.T, entries []session.Entry, want string) {
	t.Helper()
	var notices []string
	for _, entry := range entries {
		switch entry.Role {
		case "Error":
			t.Fatalf("a stopped run stored an error entry %q; entries = %+v", entry.Content, entries)
		case "Likha":
			notices = append(notices, entry.Content)
		}
	}
	if !slices.Contains(notices, want) {
		t.Fatalf("stored notices = %q, want %q", notices, want)
	}
	last := entries[len(entries)-1]
	if turn, ok := transcript.DecodeTurn(last.Content); last.Role != transcript.TurnRole || !ok || !turn.Cancelled {
		t.Fatalf("last entry = %+v, want a cancelled turn footer", last)
	}
}

func TestResumeContinuesSession(t *testing.T) {
	fake := newFakeProvider(t, textReply("first"), textReply("second"))
	run := newTestRun(t, fake, "one")
	if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code != ExitCompleted {
		t.Fatalf("first run exit = %d\n%s", code, stderr)
	}
	loaded, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Snapshot, run.cfg.Prompt = loaded, "two"
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted || stdout != "second\n" {
		t.Fatalf("resume exit = %d stdout = %q\n%s", code, stdout, stderr)
	}
	messages, _ := fake.requests()[1]["messages"].([]any)
	var contents []string
	for _, item := range messages {
		message, _ := item.(map[string]any)
		if content, ok := message["content"].(string); ok && message["role"] != "system" {
			contents = append(contents, content)
		}
	}
	if !slices.Equal(contents, []string{"one", "first", "two"}) {
		t.Fatalf("resumed request carried %v", contents)
	}
	saved, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil || saved.NamedTitle != "one" || len(saved.History) != 4 {
		t.Fatalf("resumed save = title %q, %d messages, %v", saved.NamedTitle, len(saved.History), err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

func TestUnwritableOutputExitsOne(t *testing.T) {
	fake := newFakeProvider(t, textReply("answer"))
	run := newTestRun(t, fake, "hello")
	var stderr bytes.Buffer
	if code := Run(context.Background(), run.cfg, failingWriter{}, &stderr); code != ExitFailed {
		t.Fatalf("exit = %d, want 1 when stdout cannot be written", code)
	}
	if !strings.Contains(stderr.String(), "write output") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSessionTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"first line", "fix the bug\nin main.go", "fix the bug"},
		{"skips blank lines", "\n\n  hello  \n", "hello"},
		{"bounded", strings.Repeat("a", 100), strings.Repeat("a", 79) + "…"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionTitle(tc.in); got != tc.want {
				t.Fatalf("sessionTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A session another process saved under the run fails the save with the
// session named once: the store's error already names it, so wrapping it
// again read "save session X: save session X: …".
func TestConflictingSaveNamesSessionOnce(t *testing.T) {
	fake := newFakeProvider(t, textReply("answer"))
	run := newTestRun(t, fake, "hello")
	id := run.cfg.Snapshot.ID
	other, err := run.store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	other.PromptHistory = append(other.PromptHistory, "from another process")
	if err := run.store.Save(other); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitFailed || !strings.Contains(stderr, "session changed in another process") {
		t.Fatalf("exit = %d, want 1 with the conflict reported\n%s", code, stderr)
	}
	if count := strings.Count(stderr, "save session "+id); count != 1 {
		t.Fatalf("stderr names the failed save %d times, want once:\n%s", count, stderr)
	}
}

// D10: automatic compaction (FR-42) applies to a headless run as in the
// TUI. A resumed run whose first request reaches 85% of the window is
// compacted first; the summarize request counts in the totals, the caller
// gets a notice and its usage, and the session stores the TUI's divider and
// a compacted turn footer.
func TestAutoCompactionInHeadlessRun(t *testing.T) {
	fake := newFakeProvider(t, textReply("first"), textReply("Summary: the user said one."), textReply("second"))
	run := newTestRun(t, fake, "one")
	if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code != ExitCompleted {
		t.Fatalf("first run exit = %d\n%s", code, stderr)
	}
	loaded, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Snapshot, run.cfg.Prompt, run.cfg.JSON = loaded, "two", true
	// Any request with the tool definitions exceeds 85% of this window.
	run.cfg.ContextWindow, run.cfg.AutoCompact = 100, true
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("resumed run exit = %d\n%s", code, stderr)
	}

	if requests := fake.requests(); len(requests) != 3 {
		t.Fatalf("POSTs = %d, want first answer, summary and second answer", len(requests))
	} else if body, _ := json.Marshal(requests[2]); !strings.Contains(string(body), "Summary: the user said one.") {
		t.Fatalf("the request after compaction did not carry the summary: %s", body)
	}
	events := decodeEvents(t, stdout)
	var compacted []string
	for _, notice := range eventsOfType(events, "notice") {
		if text := notice["text"].(string); strings.HasPrefix(text, "Conversation compacted automatically at ~") {
			compacted = append(compacted, text)
		}
	}
	if len(compacted) != 1 || !strings.HasSuffix(compacted[0], "% of the 100 context window.") {
		t.Fatalf("compaction notices = %q, want one naming the trigger", compacted)
	}
	last := events[len(events)-1]
	if len(eventsOfType(events, "usage")) != 2 || last["requests"] != float64(2) || last["prompt_tokens"] != float64(20) || last["text"] != "second" {
		t.Fatalf("summarize request not counted: usage events %d, result %v", len(eventsOfType(events, "usage")), last)
	}

	saved, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) == 0 || !strings.Contains(saved.History[0].Content, "Summary: the user said one.") {
		t.Fatalf("saved history does not start with the summary: %+v", saved.History)
	}
	var divider bool
	for _, entry := range saved.Entries {
		divider = divider || entry.Role == "Likha" && strings.HasPrefix(entry.Content, "Conversation compacted automatically at ~") &&
			strings.HasSuffix(entry.Content, " context window. Summary of earlier turns:\n\nSummary: the user said one.")
	}
	footer := saved.Entries[len(saved.Entries)-1]
	turn, ok := transcript.DecodeTurn(footer.Content)
	if !divider || !ok || !turn.Compacted {
		t.Fatalf("saved entries lack the divider or a compacted footer: %+v", saved.Entries)
	}
}

// failAfterWriter accepts the first n writes and fails every later one, as
// a pipe whose reader went away mid-run.
type failAfterWriter struct{ n int }

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, errors.New("broken pipe")
	}
	w.n--
	return len(p), nil
}

// Nobody reads a run whose output cannot be written, so the first failed
// write stops it like an interrupt: no further request or tool runs, the
// session is saved, and the run exits 1 for output not written.
func TestFailedOutputWriteStopsTheRun(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("r1", "read", map[string]string{"path": "."}),
		textReply("nobody reads this"))
	run := newTestRun(t, fake, "closed output")
	run.cfg.JSON = true
	var stderr bytes.Buffer
	// The start event is written; the first request's usage event is not.
	if code := Run(context.Background(), run.cfg, &failAfterWriter{n: 1}, &stderr); code != ExitFailed {
		t.Fatalf("exit = %d, want 1\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "likha: write output: broken pipe") {
		t.Fatalf("stderr does not report the failed write: %q", stderr.String())
	}
	if requests := len(fake.requests()); requests != 1 {
		t.Fatalf("POSTs = %d, want the run stopped after its first request", requests)
	}
	saved, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil || len(saved.History) == 0 || saved.History[0].Content != "closed output" {
		t.Fatalf("saved = %+v, %v", saved.History, err)
	}
	assertStoppedEntry(t, saved.Entries, "Run stopped: its output could not be written; no further tools will execute. Approved shell commands may leave detached processes running.")
}

// In text mode stderr only carries progress: a run started with stderr
// closed (2>&-) still finishes and prints its answer on stdout.
func TestUnwritableStderrDoesNotStopATextRun(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("r1", "read", map[string]string{"path": "."}),
		textReply("the answer"))
	run := newTestRun(t, fake, "closed stderr")
	var stdout bytes.Buffer
	if code := Run(context.Background(), run.cfg, &stdout, &failAfterWriter{n: 0}); code != ExitCompleted {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s", code, stdout.String())
	}
	if got := stdout.String(); got != "the answer\n" {
		t.Fatalf("stdout = %q, want the answer", got)
	}
	if requests := len(fake.requests()); requests != 2 {
		t.Fatalf("POSTs = %d, want both requests", requests)
	}
}

// Regression: the task tool advertised the built-in review agent, but the
// run registered no runner for it, so every review task was refused as an
// unknown agent. This goes through the production run options.
func TestBuiltinReviewTaskRuns(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("t1", "task", map[string]any{"agent": "review", "description": "review README", "prompt": "Review the README."}),
		textReply("review findings"),
		textReply("done"))
	run := newTestRun(t, fake, "go")
	code, _, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "tool task: ok") {
		t.Fatalf("review task did not run:\n%s", stderr)
	}
	requests := fake.requests()
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want main, review child, main", len(requests))
	}
	child, _ := json.Marshal(requests[1]["messages"])
	if !strings.Contains(string(child), "built-in review agent") {
		t.Fatalf("child request lacks the review instructions:\n%s", child)
	}
	if names := toolNames(requests[1]); !slices.Equal(names, []string{"glob", "grep", "read", "task"}) {
		t.Fatalf("review child tools = %v, want the review ceiling", names)
	}
}

// unfinishedTask saves an explore task record as a run killed before the
// task finished leaves it in the store: accepted, not yet done.
func unfinishedTask(t *testing.T, store *session.Store, sessionID, root string) string {
	t.Helper()
	var records []explore.Record
	manager, err := explore.New(context.Background(), explore.Config{
		SessionID: sessionID, Root: root, Provider: "custom", Model: "test-model",
		Runner: func(context.Context, *explore.Node) explore.Outcome { return explore.Outcome{Findings: "done"} },
		OnUpdate: func(record explore.Record) error {
			records = append(records, record)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Spawn(context.Background(), "", "call_task", explore.Spec{Agent: "explore", Description: "look around", Prompt: "look"}); err != nil {
		t.Fatal(err)
	}
	manager.Wait()
	if len(records) == 0 || records[0].Status != explore.Queued {
		t.Fatalf("first task record = %+v, want it queued", records)
	}
	if err := store.SaveTask(records[0]); err != nil {
		t.Fatal(err)
	}
	return records[0].ID
}

// likha -p --resume reconciles the session as the TUI does on resume: an
// explore task a killed run left unfinished is saved interrupted, and a plan
// step that run left in progress is reported interrupted. A step left in
// progress by a run that completed is not.
func TestResumeReconcilesWorkAnEarlierRunLeft(t *testing.T) {
	for _, tc := range []struct {
		name         string
		earlierEnded func(*session.Store, string) error
		interrupted  bool
	}{
		{"earlier run killed", func(store *session.Store, id string) error {
			return store.MarkRunStarted(id, session.NewRunToken())
		}, true},
		{"earlier run completed", func(store *session.Store, id string) error {
			token := session.NewRunToken()
			if err := store.MarkRunStarted(id, token); err != nil {
				return err
			}
			return store.MarkRunCompleted(id, token)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeProvider(t, textReply("resumed"))
			run := newTestRun(t, fake, "continue")
			id := run.cfg.Snapshot.ID
			taskID := unfinishedTask(t, run.store, id, run.root)
			plan := session.Plan{Steps: []session.PlanStep{{ID: "fix", Title: "Fix the bug", Status: string(session.PlanInProgress)}}, UpdatedAt: time.Now().UTC()}
			if err := run.store.SavePlan(id, plan); err != nil {
				t.Fatal(err)
			}
			if err := tc.earlierEnded(run.store, id); err != nil {
				t.Fatal(err)
			}
			loaded, err := run.store.Load(id)
			if err != nil {
				t.Fatal(err)
			}
			run.cfg.Snapshot, run.cfg.Created = loaded, false
			if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code != ExitCompleted {
				t.Fatalf("resume exit = %d\n%s", code, stderr)
			}

			tasks, err := run.store.Tasks(id)
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 1 || tasks[0].ID != taskID || tasks[0].Status != explore.Interrupted {
				t.Fatalf("stored tasks after resume = %+v, want %s interrupted", tasks, taskID)
			}
			saved, err := run.store.Load(id)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Plan == nil || saved.Plan.Interrupted != tc.interrupted || saved.Plan.Steps[0].Status != string(session.PlanInProgress) {
				t.Fatalf("saved plan = %+v, want the step kept in progress with interrupted %v", saved.Plan, tc.interrupted)
			}
			stored, _, err := run.store.LoadPlan(id)
			if err != nil || stored.Interrupted != tc.interrupted {
				t.Fatalf("stored plan = %+v (err %v), want interrupted %v", stored, err, tc.interrupted)
			}
			if unfinished, err := run.store.RunUnfinished(id); err != nil || unfinished {
				t.Fatalf("run marker after the completed resume = %v (err %v), want cleared", unfinished, err)
			}
		})
	}
}

// sessionWithPlan stores the run's session with one plan step in progress,
// updated at updated, and returns it loaded for a resumed run.
func sessionWithPlan(t *testing.T, run testRun, updated time.Time) session.Snapshot {
	t.Helper()
	id := run.cfg.Snapshot.ID
	plan := session.Plan{Steps: []session.PlanStep{{ID: "fix", Title: "Fix the bug", Status: string(session.PlanInProgress)}}, UpdatedAt: updated}
	snapshot := run.cfg.Snapshot
	snapshot.Plan = &plan
	snapshot.History = []model.Message{{Role: "user", Content: "start"}, {Role: "assistant", Content: "started"}}
	if err := run.store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := run.store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// A plan_update during the run is kept even when the clock stepped back
// since the stored plan was written: the row is compared with the plan
// resume loaded, not by update time.
func TestRunKeepsItsPlanUpdateAfterTheClockSteppedBack(t *testing.T) {
	var run testRun
	updated := []session.PlanStep{{ID: "fix", Title: "Fix the bug", Status: string(session.PlanCompleted)}}
	fake := newFakeProvider(t, func(w http.ResponseWriter) {
		// What plan_update does mid-run, with a clock now behind the row's.
		if err := run.store.SavePlan(run.cfg.Snapshot.ID, session.Plan{Steps: updated, UpdatedAt: time.Now().UTC()}); err != nil {
			t.Errorf("save plan: %v", err)
		}
		textReply("fixed")(w)
	})
	run = newTestRun(t, fake, "fix it")
	run.cfg.Snapshot = sessionWithPlan(t, run, time.Now().UTC().Add(time.Hour))
	if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code != ExitCompleted {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	saved, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Plan == nil || !slices.Equal(saved.Plan.Steps, updated) {
		t.Fatalf("saved plan = %+v, want the run's update %+v", saved.Plan, updated)
	}
	stored, _, err := run.store.LoadPlan(run.cfg.Snapshot.ID)
	if err != nil || !slices.Equal(stored.Steps, updated) {
		t.Fatalf("stored plan = %+v (err %v), want the run's update", stored, err)
	}
}

// A resumed session whose plan row is damaged could not be saved, so the run
// stops with a setup error before sending anything, naming the plan.
func TestDamagedPlanStopsTheResumeBeforeAnyRequest(t *testing.T) {
	fake := newFakeProvider(t, textReply("never sent"))
	run := newTestRun(t, fake, "continue")
	run.cfg.Snapshot = sessionWithPlan(t, run, time.Now().UTC())
	db, err := sql.Open("sqlite", filepath.Join(run.cfg.StateDir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE plans SET payload = X'7B' WHERE session_id = ?`, run.cfg.Snapshot.ID); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitUsage, stdout, stderr)
	}
	if !strings.Contains(stderr, "load plan") || !strings.Contains(stderr, "no request was sent") || strings.Contains(stderr, "explore") {
		t.Fatalf("stderr = %q, want it to name the plan as what failed", stderr)
	}
	if requests := fake.requests(); len(requests) != 0 {
		t.Fatalf("sent %d requests for a session that cannot be saved", len(requests))
	}
}

// A run that is cut short saves its active plan steps marked interrupted at
// once, so a later completed run without a plan_update keeps reporting them.
func TestCancelledRunSavesItsPlanInterrupted(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	fake := newFakeProvider(t, hangReply(release), textReply("answered"))
	run := newTestRun(t, fake, "fix it")
	run.cfg.Snapshot = sessionWithPlan(t, run, time.Now().UTC())
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		for len(fake.requests()) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		cancel(ErrInterrupted)
	}()
	if code, _, stderr := runHeadless(t, ctx, run.cfg); code != ExitInterrupted {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
	stored, _, err := run.store.LoadPlan(run.cfg.Snapshot.ID)
	if err != nil || !stored.Interrupted {
		t.Fatalf("stored plan after the cancelled run = %+v (err %v), want it interrupted", stored, err)
	}

	loaded, err := run.store.Load(run.cfg.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.cfg.Snapshot, run.cfg.Prompt = loaded, "what next"
	if code, _, stderr := runHeadless(t, context.Background(), run.cfg); code != ExitCompleted {
		t.Fatalf("second run exit = %d\n%s", code, stderr)
	}
	resumed, found, err := run.store.ResumablePlan(run.cfg.Snapshot.ID)
	if err != nil || !found || !resumed.Interrupted {
		t.Fatalf("plan after a later completed run = %+v (found %v, err %v), want it still interrupted", resumed, found, err)
	}
}
