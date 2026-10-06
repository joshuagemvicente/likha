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

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

// TestCommandPermissionsE2E drives a real run through Update: the first
// verification check asks with "Trust repo checks"; after trust, a second
// spelling of the same check runs without a prompt and is labelled; rm still
// asks with no remember option, and declining it never deletes the file.
func TestCommandPermissionsE2E(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"echo checks-ran"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := []string{"npm test", "npm run test", "rm notes.txt"}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		n := int(count.Add(1))
		if n <= len(commands) {
			arguments, _ := json.Marshal(map[string]string{"command": commands[n-1]})
			call := map[string]any{"index": 0, "id": fmt.Sprintf("cmd_%d", n), "type": "function", "function": map[string]any{"name": "run_command", "arguments": string(arguments)}}
			chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}, "finish_reason": "tool_calls"}}}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"All done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "e2e-model", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, client, "e2e-model", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m.input = []rune("run the checks")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})

	// 1. npm test in an untrusted repository: a review offering trust.
	permE2Epump(t, m, func() bool { return m.pending != nil })
	if m.pending.Remember != agent.RememberTrust || !strings.Contains(m.pending.Body, "echo checks-ran") {
		t.Fatalf("first check review = %+v", m.pending)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Trust repo checks
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// 2. npm run test now runs without a review; 3. rm asks again.
	permE2Epump(t, m, func() bool { return m.pending != nil || !m.working })
	if m.pending == nil {
		t.Fatal("rm ran without a review")
	}
	if m.pending.Remember != "" || !strings.Contains(m.pending.Warning, "deletes files") {
		t.Fatalf("rm review = %+v", m.pending)
	}
	if count.Load() != 3 {
		t.Fatalf("model requests before rm review = %d, want 3 (npm run test needed no review)", count.Load())
	}
	autoLabelled := false
	for _, record := range m.toolRecords {
		// The label is checked, not the script output: npm may be absent.
		if record.Name == "run_command" && strings.Contains(record.Content, agent.CommandApprovalPrefix+"auto-approved") {
			autoLabelled = true
		}
	}
	if !autoLabelled {
		t.Fatalf("auto-approved check missing its label: %+v", m.toolRecords)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Decline
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	if data, err := os.ReadFile(filepath.Join(root, "notes.txt")); err != nil || string(data) != "keep me\n" {
		t.Fatalf("declined rm touched the file: %q, %v", data, err)
	}

	// The trust was stored in the private config, not the repository.
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || len(cfg.CommandTrust[providers.CanonicalRepoPath(root)].Checks) == 0 {
		t.Fatalf("trust record = %+v, %v", cfg.CommandTrust, err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 2 {
		t.Fatalf("the run wrote into the repository: %v", entries)
	}
}
