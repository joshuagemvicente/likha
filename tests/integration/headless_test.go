package integration_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const headlessKey = "sk-integration-headless-7f3e"

// scriptedProvider is an OpenAI-compatible fake that answers each POST with
// the next scripted step. A step is "text:<answer>", "edit:<path>",
// "fail" (HTTP 400 echoing the key) or "hang" (no response until the
// client gives up).
type scriptedProvider struct {
	server *httptest.Server
	mu     sync.Mutex
	steps  []string
	posts  int
	// hung is closed when a "hang" step starts.
	hung chan struct{}
}

func newScriptedProvider(t *testing.T, steps ...string) *scriptedProvider {
	t.Helper()
	fake := &scriptedProvider{steps: steps, hung: make(chan struct{})}
	release := make(chan struct{})
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		fake.mu.Lock()
		fake.posts++
		step := "fail"
		if len(fake.steps) > 0 {
			step, fake.steps = fake.steps[0], fake.steps[1:]
		}
		fake.mu.Unlock()
		switch {
		case step == "hang":
			close(fake.hung)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		case step == "fail":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":{"message":"rejected key %s"}}`, headlessKey)
		case strings.HasPrefix(step, "edit:"):
			args, _ := json.Marshal(map[string]string{"path": strings.TrimPrefix(step, "edit:"), "content": "changed\n"})
			call, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "id": "e1", "type": "function", "function": map[string]any{"name": "edit_file", "arguments": string(args)}},
			}}}}})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", call)
		default:
			delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": strings.TrimPrefix(step, "text:")}}}})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", delta)
		}
	}))
	t.Cleanup(func() {
		close(release)
		fake.server.Close()
	})
	return fake
}

func (f *scriptedProvider) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

// headlessArgs points a run at the fake with a model and key.
func (f *scriptedProvider) headlessArgs(extra ...string) []string {
	return append([]string{"--endpoint", f.server.URL + "/v1", "--model", "test-model", "--api-key", headlessKey}, extra...)
}

func runLikha(t *testing.T, cmd *exec.Cmd) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run likha: %v", err)
	}
	return code, out.String(), errOut.String()
}

func jsonEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("stdout line is not JSON: %q", scanner.Text())
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		t.Fatal("no JSON events on stdout")
	}
	return events
}

func TestHeadlessPrintsAnswerOnly(t *testing.T) {
	fake := newScriptedProvider(t, "text:Likha is a coding agent.")
	state, repo := t.TempDir(), t.TempDir()
	code, stdout, stderr := runLikha(t, likhaCommand(state, append(fake.headlessArgs("-p", "summarize README"), repo)...))
	if code != 0 || stdout != "Likha is a coding agent.\n" {
		t.Fatalf("exit = %d stdout = %q\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "completed · session ") || strings.Contains(stdout+stderr, headlessKey) {
		t.Fatalf("stderr summary missing or key leaked:\n%s", stderr)
	}
	code, list, listErr := runLikha(t, likhaCommand(state, "--sessions", repo))
	if code != 0 || !strings.Contains(list, `"summarize README"`) {
		t.Fatalf("--sessions = %d %q\n%s", code, list, listErr)
	}
}

func TestHeadlessRefusedEditKeepsFiles(t *testing.T) {
	fake := newScriptedProvider(t, "edit:README.md", "text:I would change README.md.")
	state, repo := t.TempDir(), t.TempDir()
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runLikha(t, likhaCommand(state, append(fake.headlessArgs("--json", "-p", "edit it"), repo)...))
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, stdout, stderr)
	}
	if content, _ := os.ReadFile(readme); string(content) != "original\n" {
		t.Fatalf("README changed to %q without a reviewer", content)
	}
	events := jsonEvents(t, stdout)
	if events[0]["type"] != "start" || events[0]["schema"] != float64(1) {
		t.Fatalf("first event = %v", events[0])
	}
	last := events[len(events)-1]
	if last["type"] != "result" || last["status"] != "completed" || last["refusals"] != float64(1) || last["text"] != "I would change README.md." {
		t.Fatalf("result = %v", last)
	}
	var refused bool
	for _, event := range events {
		if event["type"] == "tool_result" && event["status"] == "refused" && strings.Contains(event["content"].(string), "headless mode") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("no refused tool_result naming headless mode: %v", events)
	}
}

func TestHeadlessUnconfiguredExitsTwo(t *testing.T) {
	code, stdout, stderr := runLikha(t, likhaCommand(t.TempDir(), "-p", "hi", t.TempDir()))
	if code != 2 || stdout != "" || !strings.Contains(stderr, "no provider configured") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
}

func TestHeadlessProviderErrorExitsOneRedacted(t *testing.T) {
	fake := newScriptedProvider(t, "fail")
	code, stdout, stderr := runLikha(t, likhaCommand(t.TempDir(), append(fake.headlessArgs("-p", "hi"), t.TempDir())...))
	if code != 1 || stdout != "" {
		t.Fatalf("exit = %d stdout = %q\n%s", code, stdout, stderr)
	}
	if strings.Contains(stderr, headlessKey) || !strings.Contains(stderr, "[redacted]") {
		t.Fatalf("stderr leaked or did not redact the key:\n%s", stderr)
	}
}

func TestHeadlessMaxTimeExitsOne(t *testing.T) {
	fake := newScriptedProvider(t, "hang")
	code, stdout, stderr := runLikha(t, likhaCommand(t.TempDir(), append(fake.headlessArgs("--json", "--max-time", "2s", "-p", "slow"), t.TempDir())...))
	if code != 1 {
		t.Fatalf("exit = %d\n%s\n%s", code, stdout, stderr)
	}
	events := jsonEvents(t, stdout)
	if last := events[len(events)-1]; last["type"] != "result" || last["status"] != "time_limit" {
		t.Fatalf("result = %v", last)
	}
}

func TestHeadlessResumeContinuesSession(t *testing.T) {
	fake := newScriptedProvider(t, "text:first answer", "text:second answer")
	state, repo := t.TempDir(), t.TempDir()
	code, stdout, stderr := runLikha(t, likhaCommand(state, append(fake.headlessArgs("--json", "-p", "first question"), repo)...))
	if code != 0 {
		t.Fatalf("first run exit = %d\n%s", code, stderr)
	}
	sessionID, _ := jsonEvents(t, stdout)[0]["session_id"].(string)
	code, stdout, stderr = runLikha(t, likhaCommand(state, append(fake.headlessArgs("--resume", sessionID, "-p", "second question"), repo)...))
	if code != 0 || stdout != "second answer\n" || !strings.Contains(stderr, "session "+sessionID) {
		t.Fatalf("resume exit = %d stdout = %q\n%s", code, stdout, stderr)
	}
	code, list, _ := runLikha(t, likhaCommand(state, "--sessions", repo))
	if code != 0 || strings.Count(list, "\n") != 1 || !strings.HasPrefix(list, sessionID) {
		t.Fatalf("--sessions after resume = %q", list)
	}
}

func TestHeadlessInterruptExits130(t *testing.T) {
	fake := newScriptedProvider(t, "hang")
	state, repo := t.TempDir(), t.TempDir()
	cmd := likhaCommand(state, append(fake.headlessArgs("--json", "-p", "slow"), repo)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.hung:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the run never reached the model request\n%s", errOut.String())
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 {
		t.Fatalf("exit = %v, want 130\n%s", err, errOut.String())
	}
	events := jsonEvents(t, out.String())
	last := events[len(events)-1]
	if last["status"] != "cancelled" {
		t.Fatalf("result = %v", last)
	}
	code, list, _ := runLikha(t, likhaCommand(state, "--sessions", repo))
	if sessionID, _ := last["session_id"].(string); code != 0 || sessionID == "" || !strings.Contains(list, sessionID) {
		t.Fatalf("interrupted session not saved: %q", list)
	}
}

// writeHooksFile writes a private hooks.json into the state directory.
func writeHooksFile(t *testing.T, state, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, "hooks.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessHooksBlockPromptAndTool(t *testing.T) {
	t.Run("prompt_submit block exits one before any request", func(t *testing.T) {
		fake := newScriptedProvider(t, "text:never sent")
		state, repo := t.TempDir(), t.TempDir()
		writeHooksFile(t, state, `{"hooks":{"prompt_submit":[{"name":"guard","command":"echo '{\"decision\":\"block\",\"reason\":\"not today\"}'"}]}}`)
		code, stdout, stderr := runLikha(t, likhaCommand(state, append(fake.headlessArgs("-p", "do it"), repo)...))
		if code != 1 || stdout != "" || !strings.Contains(stderr, `Prompt blocked by hook "guard": not today`) {
			t.Fatalf("exit = %d stdout = %q\n%s", code, stdout, stderr)
		}
		if fake.postCount() != 0 {
			t.Fatalf("a blocked prompt sent %d model requests", fake.postCount())
		}
	})
	t.Run("pre_tool block is a refusal and the file is untouched", func(t *testing.T) {
		fake := newScriptedProvider(t, "edit:README.md", "text:Blocked, so I describe the change.")
		state, repo := t.TempDir(), t.TempDir()
		readme := filepath.Join(repo, "README.md")
		if err := os.WriteFile(readme, []byte("original\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ended := filepath.Join(t.TempDir(), "run_end.json")
		writeHooksFile(t, state, `{"hooks":{
			"pre_tool":[{"name":"no-edits","match":["edit_file"],"command":"echo '{\"decision\":\"block\",\"reason\":\"read-only day\"}'"}],
			"run_end":[{"name":"end","command":"cat > '`+ended+`'"}]}}`)
		code, stdout, stderr := runLikha(t, likhaCommand(state, append(fake.headlessArgs("--json", "-p", "edit it"), repo)...))
		if code != 0 {
			t.Fatalf("exit = %d\n%s\n%s", code, stdout, stderr)
		}
		if content, _ := os.ReadFile(readme); string(content) != "original\n" {
			t.Fatalf("README changed to %q despite the block", content)
		}
		var blocked bool
		for _, event := range jsonEvents(t, stdout) {
			if event["type"] == "tool_result" && event["status"] == "refused" && strings.Contains(event["content"].(string), `Blocked by hook "no-edits": read-only day`) {
				blocked = true
			}
		}
		if !blocked {
			t.Fatalf("no refused tool_result naming the hook:\n%s", stdout)
		}
		data, err := os.ReadFile(ended)
		if err != nil || !strings.Contains(string(data), `"outcome":"completed"`) {
			t.Fatalf("run_end payload = %q (%v)", data, err)
		}
	})
}
