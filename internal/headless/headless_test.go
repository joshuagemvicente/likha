package headless

import (
	"bufio"
	"bytes"
	"context"
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
	"likha/internal/providers"
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
	if len(saved.Entries) != 2 || saved.Entries[0].Role != "You" || saved.Entries[1].Role != "Assistant" {
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
	if _, err := run.store.Load(run.cfg.Snapshot.ID); err != nil {
		t.Fatalf("session not saved after time limit: %v", err)
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
