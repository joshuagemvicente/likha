package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const headlessTestKey = "sk-app-headless-456"

// headlessProvider answers the model list and every POST with one text
// reply naming the last user message, and counts requests.
type headlessProvider struct {
	server      *httptest.Server
	gets, posts atomic.Int32
	lastPrompt  atomic.Value
}

func newHeadlessProvider(t *testing.T, listStatus int) *headlessProvider {
	t.Helper()
	fake := &headlessProvider{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fake.gets.Add(1)
			if listStatus != http.StatusOK {
				w.WriteHeader(listStatus)
				fmt.Fprintf(w, `{"error":{"message":"bad key %s"}}`, headlessTestKey)
				return
			}
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		fake.posts.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.Unmarshal(raw, &body)
		for _, message := range body.Messages {
			if message.Role == "user" {
				fake.lastPrompt.Store(message.Content)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"pong"}}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *headlessProvider) prompt() string {
	value, _ := f.lastPrompt.Load().(string)
	return value
}

// headlessEnv isolates the run from the developer's environment.
func headlessEnv(t *testing.T) string {
	t.Helper()
	for _, key := range []string{"LIKHA_MODEL", "LIKHA_PROVIDER", "LIKHA_API_KEY", "LIKHA_ENDPOINT", "LIKHA_THEME"} {
		t.Setenv(key, "")
	}
	state := t.TempDir()
	t.Setenv("LIKHA_STATE_DIR", state)
	return state
}

func runApp(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestHeadlessUsageErrorsSendNothing(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty prompt", []string{"-p", "  "}, "-p needs a prompt"},
		{"slash command", []string{"-p", "/compact"}, "slash commands are interactive only"},
		{"with sessions", []string{"-p", "hi", "--sessions"}, "-p cannot be combined with --sessions"},
		{"with login", []string{"-p", "hi", "--login"}, "-p cannot be combined with --login"},
		{"with probe", []string{"-p", "hi", "--probe-effort"}, "-p cannot be combined with --probe-effort"},
		{"json without p", []string{"--json"}, "--json applies only with -p"},
		{"max-time without p", []string{"--max-time", "1m"}, "--max-time applies only with -p"},
		{"negative max-time", []string{"-p", "hi", "--max-time", "-1s"}, "--max-time cannot be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headlessEnv(t)
			fake := newHeadlessProvider(t, http.StatusOK)
			args := append([]string{"--endpoint", fake.server.URL + "/v1", "--model", "test-model"}, tc.args...)
			code, stdout, stderr := runApp(append(args, t.TempDir())...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit = %d stdout = %q stderr = %q; want 2 and %q", code, stdout, stderr, tc.want)
			}
			if fake.gets.Load()+fake.posts.Load() != 0 {
				t.Fatal("a usage error contacted the provider")
			}
		})
	}
}

func TestHeadlessRunsWithoutTerminal(t *testing.T) {
	headlessEnv(t)
	fake := newHeadlessProvider(t, http.StatusOK)
	code, stdout, stderr := runApp("--endpoint", fake.server.URL+"/v1", "--model", "test-model", "-p", "ping", t.TempDir())
	if code != 0 || stdout != "pong\n" {
		t.Fatalf("exit = %d stdout = %q\n%s", code, stdout, stderr)
	}
	if fake.prompt() != "ping" || fake.posts.Load() != 1 {
		t.Fatalf("provider saw prompt %q in %d POSTs", fake.prompt(), fake.posts.Load())
	}
}

func TestHeadlessReadsPromptFromStdin(t *testing.T) {
	headlessEnv(t)
	fake := newHeadlessProvider(t, http.StatusOK)
	previous := headlessStdin
	t.Cleanup(func() { headlessStdin = previous })

	headlessStdin = strings.NewReader("hello from stdin\n")
	code, stdout, stderr := runApp("--endpoint", fake.server.URL+"/v1", "--model", "test-model", "-p", "-", t.TempDir())
	if code != 0 || stdout != "pong\n" || fake.prompt() != "hello from stdin" {
		t.Fatalf("exit = %d stdout = %q prompt = %q\n%s", code, stdout, fake.prompt(), stderr)
	}

	headlessStdin = strings.NewReader(strings.Repeat("x", maxStdinPrompt+1))
	code, _, stderr = runApp("--endpoint", fake.server.URL+"/v1", "--model", "test-model", "-p", "-", t.TempDir())
	if code != 2 || !strings.Contains(stderr, "exceeds 256 KiB") {
		t.Fatalf("oversized stdin: exit = %d stderr = %q", code, stderr)
	}
}

func TestHeadlessWithoutProviderExitsTwo(t *testing.T) {
	headlessEnv(t)
	code, stdout, stderr := runApp("-p", "hi", t.TempDir())
	if code != 2 || stdout != "" || !strings.Contains(stderr, "no provider configured") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
}

func TestHeadlessChatGPTWithoutSignInExitsTwo(t *testing.T) {
	headlessEnv(t)
	code, stdout, stderr := runApp("--provider", "chatgpt", "-p", "hi", t.TempDir())
	if code != 2 || stdout != "" || !strings.Contains(stderr, "likha --provider chatgpt --login") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
}

func TestHeadlessConnectionFailureExitsOneRedacted(t *testing.T) {
	headlessEnv(t)
	fake := newHeadlessProvider(t, http.StatusUnauthorized)
	code, stdout, stderr := runApp("--endpoint", fake.server.URL+"/v1", "--model", "test-model", "--api-key", headlessTestKey, "-p", "hi", t.TempDir())
	if code != 1 || stdout != "" || !strings.Contains(stderr, "connection check failed") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
	if strings.Contains(stderr, headlessTestKey) {
		t.Fatalf("stderr contains the API key: %q", stderr)
	}
	if fake.posts.Load() != 0 {
		t.Fatal("a failed connection check still sent the prompt")
	}
}

func TestHeadlessLeavesMCPStopped(t *testing.T) {
	state := headlessEnv(t)
	config := `{"mcpServers":{"docs":{"command":"/nonexistent/likha-mcp-server"}}}`
	if err := os.WriteFile(filepath.Join(state, "mcp.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := newHeadlessProvider(t, http.StatusOK)
	code, stdout, stderr := runApp("--endpoint", fake.server.URL+"/v1", "--model", "test-model", "-p", "hi", t.TempDir())
	if code != 0 || stdout != "pong\n" || !strings.Contains(stderr, "1 configured MCP server(s) are not started in headless runs") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
}

func TestHelpListsHeadlessFlags(t *testing.T) {
	code, stdout, _ := runApp("--help")
	if code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	for _, want := range []string{"-p PROMPT", "--json", "--max-time D", "Without -p, Likha needs an"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help is missing %q", want)
		}
	}
}

func TestInteractiveLaunchStillNeedsTerminal(t *testing.T) {
	headlessEnv(t)
	fake := newHeadlessProvider(t, http.StatusOK)
	code, _, stderr := runApp("--endpoint", fake.server.URL+"/v1", "--model", "test-model", t.TempDir())
	if code != 2 || !strings.Contains(stderr, "interactive terminal required") {
		t.Fatalf("exit = %d stderr = %q; a launch without -p must keep requiring a terminal", code, stderr)
	}
	if fake.posts.Load() != 0 {
		t.Fatal("an interactive launch without a terminal sent a prompt")
	}
}
