package integration_test

import (
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
	"sync/atomic"
	"testing"
)

const probeKey = "test-key-integration-93ad41c7"

// effortServer honors reasoning_effort for gpt-5.5's levels and rejects the
// probe's invalid control level, counting every POST.
func effortServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var posts atomic.Int32
	tokens := map[string]int{"": 900, "none": 0, "low": 200, "medium": 900, "high": 2400, "xhigh": 6000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5.5"}]}`)
			return
		}
		posts.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		_ = json.Unmarshal(raw, &body)
		n, ok := tokens[body.ReasoningEffort]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"invalid effort for key %s"}`, probeKey)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"record_answer","arguments":"{\"answer\":440}"}}]}}]}`+"\n\n")
		fmt.Fprintf(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":%d,"completion_tokens_details":{"reasoning_tokens":%d}}}`+"\n\n", n+30, n)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, &posts
}

func probeCommand(state string, args ...string) *exec.Cmd {
	cmd := exec.Command("go", append([]string{"run", "./cmd/likha"}, args...)...)
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "LIKHA_PROVIDER=", "LIKHA_MODEL=", "LIKHA_API_KEY=", "LIKHA_ENDPOINT=",
		"LIKHA_OPENAI_API_KEY=", "LIKHA_STATE_DIR="+state)
	return cmd
}

func TestEffortProbeEndToEnd(t *testing.T) {
	server, posts := effortServer(t)
	state := t.TempDir()
	cmd := probeCommand(state, "--probe-effort", "--provider", "openai", "--endpoint", server.URL+"/v1",
		"--model", "gpt-5.5", "--api-key", probeKey, "--yes")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe failed: %v\n%s", err, out)
	}
	if got := posts.Load(); got != 7 {
		t.Fatalf("POSTs = %d, want 7\n%s", got, out)
	}
	reports, err := filepath.Glob(filepath.Join(state, "effort-probes", "*-openai-gpt-5.5.json"))
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %v, %v", reports, err)
	}
	info, err := os.Stat(reports[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %v, %v", info, err)
	}
	report, err := os.ReadFile(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), probeKey) || strings.Contains(string(report), probeKey) {
		t.Fatal("the API key appears in the output or the report")
	}
	if !strings.Contains(string(out), "Eligible to enable reasoning_effort for openai: yes") {
		t.Fatalf("output lacks the eligibility line:\n%s", out)
	}
}

func TestEffortProbeWithoutYesSendsNothing(t *testing.T) {
	server, posts := effortServer(t)
	cmd := probeCommand(t.TempDir(), "--probe-effort", "--provider", "openai", "--endpoint", server.URL+"/v1",
		"--model", "gpt-5.5", "--api-key", probeKey)
	out, err := cmd.CombinedOutput()
	// go run exits 1 for any failing program and reports the program's own
	// code as "exit status N" on stderr.
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !strings.Contains(string(out), "exit status 2") {
		t.Fatalf("exit = %v, want the program's exit status 2\n%s", err, out)
	}
	if posts.Load() != 0 || !strings.Contains(string(out), "pass --yes to confirm spending") {
		t.Fatalf("POSTs = %d\n%s", posts.Load(), out)
	}
}
