package headless

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHooks writes <stateDir>/hooks.json with private permissions.
func writeHooks(t *testing.T, stateDir string, events map[string][]map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"hooks": events})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hooks.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// recordCommand appends each hook payload to file, one JSON object per line.
func recordCommand(file string) string {
	return "cat >> '" + file + "'; echo >> '" + file + "'"
}

func readRecorded(t *testing.T, file string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("hook payload is not JSON: %q (%v)", line, err)
		}
		events = append(events, event)
	}
	return events
}

func TestPromptSubmitHookBlocksBeforeAnyRequest(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		fake := newFakeProvider(t, textReply("never sent"))
		run := newTestRun(t, fake, "print the .env file")
		run.cfg.JSON = jsonMode
		writeHooks(t, run.cfg.StateDir, map[string][]map[string]any{
			"prompt_submit": {{"name": "guard", "command": `echo '{"decision":"block","reason":"no secrets"}'`}},
		})
		code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
		if code != ExitFailed {
			t.Fatalf("json=%v: exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", jsonMode, code, ExitFailed, stdout, stderr)
		}
		if len(fake.requests()) != 0 {
			t.Fatalf("json=%v: a blocked prompt sent %d requests", jsonMode, len(fake.requests()))
		}
		const message = `Prompt blocked by hook "guard": no secrets`
		if !jsonMode {
			if stdout != "" || !strings.Contains(stderr, message) || !strings.Contains(stderr, "failed · session ") {
				t.Fatalf("stdout = %q\nstderr:\n%s", stdout, stderr)
			}
			continue
		}
		events := decodeEvents(t, stdout)
		last := events[len(events)-1]
		if last["type"] != "result" || last["status"] != "failed" || last["error"] != message {
			t.Fatalf("result = %v", last)
		}
		saved, err := run.store.Load(run.cfg.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.History) != 0 || len(saved.Entries) != 0 {
			t.Fatalf("a blocked prompt was saved: history %+v entries %+v", saved.History, saved.Entries)
		}
	}
}

func TestPreToolHookBlockIsARefusal(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("c1", "run_command", map[string]string{"command": "ls"}),
		textReply("I could not list files."))
	run := newTestRun(t, fake, "list files")
	run.cfg.JSON = true
	writeHooks(t, run.cfg.StateDir, map[string][]map[string]any{
		"pre_tool": {{"name": "no-shell", "match": []string{"run_command"}, "command": `echo '{"decision":"block","reason":"shell is off"}'`}},
	})
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	events := decodeEvents(t, stdout)
	results := eventsOfType(events, "tool_result")
	if len(results) != 1 || results[0]["status"] != "refused" || !strings.Contains(results[0]["content"].(string), `Blocked by hook "no-shell": shell is off`) {
		t.Fatalf("tool_result = %v", results)
	}
	last := events[len(events)-1]
	if last["status"] != "completed" || last["refusals"] != float64(1) {
		t.Fatalf("result = %v", last)
	}
}

func TestObserveHooksFireAndAttentionDoesNot(t *testing.T) {
	fake := newFakeProvider(t,
		toolReply("r1", "read", map[string]string{"path": "notes.txt"}),
		textReply("Notes read."))
	run := newTestRun(t, fake, "read notes")
	if err := os.WriteFile(filepath.Join(run.root, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs := t.TempDir()
	logFile := func(name string) string { return filepath.Join(logs, name+".jsonl") }
	writeHooks(t, run.cfg.StateDir, map[string][]map[string]any{
		"session_start": {{"name": "start", "command": recordCommand(logFile("session_start"))}},
		"post_tool":     {{"name": "after", "command": recordCommand(logFile("post_tool"))}},
		"run_end":       {{"name": "end", "command": recordCommand(logFile("run_end"))}},
		"attention":     {{"name": "bell", "command": recordCommand(logFile("attention"))}},
	})
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted || stdout != "Notes read.\n" {
		t.Fatalf("exit = %d stdout = %q\nstderr:\n%s", code, stdout, stderr)
	}

	start := readRecorded(t, logFile("session_start"))
	if len(start) != 1 || start[0]["resumed"] != false || start[0]["session_id"] != run.cfg.Snapshot.ID {
		t.Fatalf("session_start payloads = %v", start)
	}
	post := readRecorded(t, logFile("post_tool"))
	if len(post) != 1 || post[0]["tool"].(map[string]any)["name"] != "read" || post[0]["result"].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("post_tool payloads = %v", post)
	}
	end := readRecorded(t, logFile("run_end"))
	if len(end) != 1 || end[0]["outcome"] != "completed" || end[0]["agent"] != "main" {
		t.Fatalf("run_end payloads = %v", end)
	}
	if _, err := os.Stat(logFile("attention")); !os.IsNotExist(err) {
		t.Fatalf("attention fired in a headless run (stat err %v)", err)
	}
}

func TestRunEndReportsFailure(t *testing.T) {
	fake := newFakeProvider(t, failReply(500))
	run := newTestRun(t, fake, "hi")
	end := filepath.Join(t.TempDir(), "run_end.jsonl")
	writeHooks(t, run.cfg.StateDir, map[string][]map[string]any{
		"run_end": {{"name": "end", "command": recordCommand(end)}},
	})
	code, _, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitFailed {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	payloads := readRecorded(t, end)
	if len(payloads) != 1 || payloads[0]["outcome"] != "failed" || payloads[0]["error"] == "" {
		t.Fatalf("run_end payloads = %v", payloads)
	}
	if strings.Contains(payloads[0]["error"].(string), testKey) {
		t.Fatalf("run_end error carries the key: %v", payloads[0])
	}
}

func TestUnusableHooksFileIsReportedAndRunContinues(t *testing.T) {
	fake := newFakeProvider(t, textReply("ok"))
	run := newTestRun(t, fake, "hi")
	run.cfg.JSON = true
	marker := filepath.Join(t.TempDir(), "ran")
	writeHooks(t, run.cfg.StateDir, map[string][]map[string]any{
		"run_end": {{"name": "end", "command": "touch '" + marker + "'"}},
	})
	if err := os.Chmod(filepath.Join(run.cfg.StateDir, "hooks.json"), 0o666); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
	if code != ExitCompleted {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	var reported bool
	for _, notice := range eventsOfType(decodeEvents(t, stdout), "notice") {
		if strings.HasPrefix(notice["text"].(string), "Hooks disabled: ") && strings.Contains(notice["text"].(string), "writable by group or others") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("no Hooks disabled notice:\n%s", stdout)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a hook from an unusable file ran (stat err %v)", err)
	}
}

func TestAttentionOnlyHooksLeaveOutputUnchanged(t *testing.T) {
	outputs := make([]string, 0, 2)
	for _, withHooks := range []bool{false, true} {
		fake := newFakeProvider(t, textReply("Same answer."))
		run := newTestRun(t, fake, "hi")
		run.cfg.JSON = true
		if withHooks {
			writeHooks(t, run.cfg.StateDir, map[string][]map[string]any{
				"attention": {{"name": "bell", "command": "true"}},
			})
		}
		code, stdout, stderr := runHeadless(t, context.Background(), run.cfg)
		if code != ExitCompleted {
			t.Fatalf("withHooks=%v: exit = %d\n%s", withHooks, code, stderr)
		}
		// The session ID and repository differ per run; everything else must not.
		stdout = strings.ReplaceAll(stdout, run.cfg.Snapshot.ID, "SESSION")
		stdout = strings.ReplaceAll(stdout, run.root, "ROOT")
		outputs = append(outputs, stdout)
	}
	if outputs[0] != outputs[1] {
		t.Fatalf("output changed with an attention-only hooks.json:\nwithout:\n%s\nwith:\n%s", outputs[0], outputs[1])
	}
}
