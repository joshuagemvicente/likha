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

	"likha/internal/providers"
)

const probeTestKey = "test-key-app-6b0d27f9c3e1"

// probeServer is a chat-completions provider that honors effort: reasoning
// grows with the level and the invalid control level is rejected. status,
// when set, answers every POST with that status instead.
type probeServer struct {
	*httptest.Server
	posts  atomic.Int32
	gets   atomic.Int32
	status int
}

func newProbeServer(t *testing.T, status int) *probeServer {
	t.Helper()
	s := &probeServer{status: status}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *probeServer) endpoint() string { return s.URL + "/v1" }

var probeServerTokens = map[string]int{"": 900, "none": 0, "low": 200, "medium": 900, "high": 2400, "xhigh": 6000}

func (s *probeServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		s.gets.Add(1)
		fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5.5"}]}`)
		return
	}
	s.posts.Add(1)
	if s.status != 0 {
		w.WriteHeader(s.status)
		// A provider that echoes the key must not leak it into any output.
		fmt.Fprintf(w, `{"error":"rejected key %s"}`, probeTestKey)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		ReasoningEffort string `json:"reasoning_effort"`
	}
	_ = json.Unmarshal(raw, &body)
	tokens, known := probeServerTokens[body.ReasoningEffort]
	if !known {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid reasoning_effort"}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"record_answer","arguments":"{\"answer\":440}"}}]}}]}`+"\n\n")
	fmt.Fprintf(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":%d,"completion_tokens_details":{"reasoning_tokens":%d}}}`+"\n\n", tokens+30, tokens)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// probeEnv isolates the state directory and the environment the probe reads.
func probeEnv(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("LIKHA_STATE_DIR", state)
	for _, key := range []string{"LIKHA_PROVIDER", "LIKHA_MODEL", "LIKHA_API_KEY", "LIKHA_ENDPOINT", "LIKHA_OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	return state
}

// seedStoredSetup writes a first-run config and a stored key for another
// provider, so tests can prove the probe neither uses nor changes them.
func seedStoredSetup(t *testing.T, state string) (config, keys []byte) {
	t.Helper()
	if err := providers.SaveStoredConfig(state, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-5.5"}); err != nil {
		t.Fatal(err)
	}
	if err := providers.StoreKey(state, "openrouter", "stored-other-key"); err != nil {
		t.Fatal(err)
	}
	return readFile(t, filepath.Join(state, "config.json")), readFile(t, filepath.Join(state, "providers.json"))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProbeUsageErrors(t *testing.T) {
	repo := t.TempDir()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"probe-wire without probe", []string{"--probe-wire", "top", "--provider", "openai"}, "--probe-wire applies only with --probe-effort"},
		{"probe-levels without probe", []string{"--probe-levels", "low"}, "--probe-levels applies only with --probe-effort"},
		{"yes without probe", []string{"--yes"}, "--yes applies only with --probe-effort"},
		{"with sessions", []string{"--probe-effort", "--sessions", "--provider", "openai", "--model", "gpt-5.5"}, "cannot be combined"},
		{"with resume", []string{"--probe-effort", "--resume", "abc", "--provider", "openai", "--model", "gpt-5.5"}, "cannot be combined"},
		{"with login", []string{"--probe-effort", "--login", "--provider", "openai", "--model", "gpt-5.5"}, "cannot be combined"},
		{"with a repository path", []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", repo}, "takes no repository path"},
		{"missing provider", []string{"--probe-effort", "--model", "gpt-5.5"}, "needs --provider or LIKHA_PROVIDER"},
		{"missing model", []string{"--probe-effort", "--provider", "openai"}, "needs --model or LIKHA_MODEL"},
		{"unknown provider", []string{"--probe-effort", "--provider", "nope", "--model", "m"}, `unknown provider "nope"`},
		{"probe-wire on chatgpt", []string{"--probe-effort", "--provider", "chatgpt", "--model", "gpt-5.5", "--probe-wire", "both"}, "--probe-wire does not apply to chatgpt"},
		{"probe-wire on claude", []string{"--probe-effort", "--provider", "claude", "--model", "claude-opus-5-5", "--probe-wire", "top"}, "--probe-wire does not apply to claude: it has one shape, output_config.effort"},
		{"bad probe-wire", []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", "--probe-wire", "sideways"}, "top, object or both"},
		{"empty probe level", []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", "--probe-levels", "low,,high"}, "empty level"},
		{"missing credential", []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5"}, "requires an API key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := probeEnv(t)
			config, keys := seedStoredSetup(t, state)
			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr); code != 2 {
				t.Fatalf("exit = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if !bytes.Equal(config, readFile(t, filepath.Join(state, "config.json"))) || !bytes.Equal(keys, readFile(t, filepath.Join(state, "providers.json"))) {
				t.Fatal("a refused probe changed stored configuration")
			}
		})
	}
}

// An override on claude would post chat completions, which never carry
// output_config.effort, so the probe refuses it before contacting it.
func TestProbeRefusesClaudeEndpointOverride(t *testing.T) {
	probeEnv(t)
	server := newProbeServer(t, 0)
	var stdout, stderr bytes.Buffer
	args := []string{"--probe-effort", "--provider", "claude", "--model", "claude-opus-5-5", "--endpoint", server.endpoint(), "--api-key", probeTestKey, "--yes"}
	if code := Run(args, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "an endpoint override on claude cannot be probed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if server.posts.Load() != 0 || server.gets.Load() != 0 || stdout.Len() != 0 {
		t.Fatalf("POSTs = %d, GETs = %d, stdout = %q", server.posts.Load(), server.gets.Load(), stdout.String())
	}
}

func TestProbeRunsAndChangesNoConfiguration(t *testing.T) {
	state := probeEnv(t)
	config, keys := seedStoredSetup(t, state)
	server := newProbeServer(t, 0)
	var stdout, stderr bytes.Buffer
	args := []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", "--endpoint", server.endpoint(), "--api-key", probeTestKey, "--yes"}
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d; stderr = %s", code, stderr.String())
	}
	if got := server.posts.Load(); got != 7 {
		t.Fatalf("POSTs = %d, want 7", got)
	}
	if !bytes.Equal(config, readFile(t, filepath.Join(state, "config.json"))) || !bytes.Equal(keys, readFile(t, filepath.Join(state, "providers.json"))) {
		t.Fatal("the probe changed config.json or providers.json")
	}
	if stored, err := providers.StoredKey(state, "openai"); err != nil || stored != "" {
		t.Fatalf("--api-key was stored: %q, %v", stored, err)
	}
	out := stdout.String()
	for _, want := range []string{"Effort probe plan", "Eligible to enable reasoning_effort for openai: yes (endpoint override: not evidence for openai)", "Report: "} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout lacks %q:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	path := strings.TrimPrefix(lines[len(lines)-1], "Report: ")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || filepath.Dir(path) != filepath.Join(state, "effort-probes") {
		t.Fatalf("report %s: %v %v", path, info, err)
	}
	if strings.Contains(out+stderr.String()+string(readFile(t, path)), probeTestKey) {
		t.Fatal("the API key appears in the output or the report")
	}
}

func TestProbeWithoutYesAndTerminalSendsNothing(t *testing.T) {
	probeEnv(t)
	server := newProbeServer(t, 0)
	var stdout, stderr bytes.Buffer
	args := []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", "--endpoint", server.endpoint(), "--api-key", probeTestKey}
	if code := Run(args, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if server.posts.Load() != 0 || !strings.Contains(stderr.String(), "pass --yes to confirm spending") {
		t.Fatalf("POSTs = %d, stderr = %q", server.posts.Load(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Estimated cost: about $") {
		t.Fatalf("the plan was not printed before refusing:\n%s", stdout.String())
	}
}

func TestProbePromptDeclinedOrAccepted(t *testing.T) {
	cases := []struct {
		answer    string
		wantCode  int
		wantPosts int32
	}{
		{"n\n", 1, 0},
		{"\n", 1, 0},
		{"yes\n", 0, 7},
		{"Y\n", 0, 7},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			state := probeEnv(t)
			server := newProbeServer(t, 0)
			isTerminal, stdin := probeIsTerminal, probeStdin
			probeIsTerminal = func(io.Writer) bool { return true }
			probeStdin = strings.NewReader(tc.answer)
			t.Cleanup(func() { probeIsTerminal, probeStdin = isTerminal, stdin })
			var stdout, stderr bytes.Buffer
			args := []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", "--endpoint", server.endpoint(), "--api-key", probeTestKey}
			if code := Run(args, &stdout, &stderr); code != tc.wantCode {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, tc.wantCode, stderr.String())
			}
			if got := server.posts.Load(); got != tc.wantPosts {
				t.Fatalf("POSTs = %d, want %d", got, tc.wantPosts)
			}
			if !strings.Contains(stderr.String(), "Send 7 requests to openai (gpt-5.5)? [y/N]") {
				t.Fatalf("stderr = %q", stderr.String())
			}
			_, err := os.Stat(filepath.Join(state, "effort-probes"))
			if tc.wantPosts == 0 && !os.IsNotExist(err) {
				t.Fatalf("a declined probe wrote a report directory: %v", err)
			}
			if tc.wantPosts == 0 && !strings.Contains(stderr.String(), "Probe cancelled; no model request was sent.") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestProbeUnauthorizedStillWritesReport(t *testing.T) {
	state := probeEnv(t)
	server := newProbeServer(t, http.StatusUnauthorized)
	var stdout, stderr bytes.Buffer
	args := []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", "--endpoint", server.endpoint(), "--api-key", probeTestKey, "--yes"}
	if code := Run(args, &stdout, &stderr); code != 1 {
		t.Fatalf("exit = %d, want 1; stderr = %s", code, stderr.String())
	}
	if server.posts.Load() != 1 {
		t.Fatalf("POSTs = %d, want 1 (a 401 stops the run)", server.posts.Load())
	}
	entries, err := os.ReadDir(filepath.Join(state, "effort-probes"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("reports = %v, %v", entries, err)
	}
	report := readFile(t, filepath.Join(state, "effort-probes", entries[0].Name()))
	if !strings.Contains(string(report), `"cause": "unauthorized"`) {
		t.Fatalf("report does not record the stop:\n%s", report)
	}
	if strings.Contains(string(report)+stdout.String()+stderr.String(), probeTestKey) {
		t.Fatal("the echoed API key leaked")
	}
}

// The probe flags' help text is copied into docs/usage.md; this keeps the
// two identical.
func TestProbeHelpMatchesUsageDoc(t *testing.T) {
	help := usageText()
	start := strings.Index(help, "  --probe-effort")
	end := strings.Index(help, "without a prompt.\n")
	if start < 0 || end < start {
		t.Fatalf("help lacks the probe flags:\n%s", help)
	}
	block := help[start : end+len("without a prompt.\n")]
	for _, flagName := range []string{"--probe-effort", "--probe-wire", "--probe-levels", "--yes"} {
		if !strings.Contains(block, flagName) {
			t.Fatalf("help block lacks %s", flagName)
		}
	}
	doc := readFile(t, filepath.Join("..", "..", "docs", "usage.md"))
	if !strings.Contains(string(doc), block) {
		t.Fatalf("docs/usage.md does not carry the help text verbatim:\n%s", block)
	}
}

func TestProbeNoLevelsExitsBeforeAnyRequest(t *testing.T) {
	probeEnv(t)
	server := newProbeServer(t, 0)
	var stdout, stderr bytes.Buffer
	args := []string{"--probe-effort", "--provider", "dialagram", "--model", "some-model", "--endpoint", server.endpoint(), "--api-key", probeTestKey, "--yes"}
	if code := Run(args, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if server.posts.Load() != 0 || !strings.Contains(stderr.String(), "some-model publishes no effort levels on dialagram; nothing to probe") {
		t.Fatalf("POSTs = %d, stderr = %q", server.posts.Load(), stderr.String())
	}
}

// An endpoint override, including an ambient LIKHA_ENDPOINT, must not
// receive the credential before the plan has shown the override and the
// user has confirmed: the unbilled model-list GET is skipped there.
func TestProbeEndpointOverrideGetsNothingBeforeConsent(t *testing.T) {
	cases := []struct {
		name string
		flag bool // --endpoint instead of LIKHA_ENDPOINT
	}{
		{"LIKHA_ENDPOINT", false},
		{"--endpoint", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probeEnv(t)
			server := newProbeServer(t, 0)
			args := []string{"--probe-effort", "--provider", "openai", "--model", "gpt-5.5", "--api-key", probeTestKey}
			if tc.flag {
				args = append(args, "--endpoint", server.endpoint())
			} else {
				t.Setenv("LIKHA_ENDPOINT", server.endpoint())
			}
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr); code != 2 {
				t.Fatalf("exit = %d, want 2 (no --yes, no terminal); stderr = %s", code, stderr.String())
			}
			if gets, posts := server.gets.Load(), server.posts.Load(); gets != 0 || posts != 0 {
				t.Fatalf("the override host got %d GETs and %d POSTs before consent", gets, posts)
			}
			if !strings.Contains(stderr.String(), "endpoint override: the model list is not fetched before you confirm") {
				t.Fatalf("stderr = %q", stderr.String())
			}
			if !strings.Contains(stdout.String(), "(endpoint override)") || !strings.Contains(stdout.String(), "Levels (catalog)") {
				t.Fatalf("plan = %s", stdout.String())
			}
		})
	}
}
