package app

import (
	"context"
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

	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/repository"
	"lisa/internal/session"
)

func TestExpandFileReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "c.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}

	expanded := expandFileReferences("fix @main.go please", repo)
	if !strings.Contains(expanded, "fix @main.go please") || !strings.Contains(expanded, "[Referenced file @main.go]") || !strings.Contains(expanded, "package main") {
		t.Fatalf("expansion missing content: %q", expanded)
	}

	// Trailing punctuation is not part of the path.
	expanded = expandFileReferences("see @main.go.", repo)
	if !strings.Contains(expanded, "package main") {
		t.Fatalf("punctuation broke the reference: %q", expanded)
	}

	// Unresolvable tokens stay literal: no silent failure.
	expanded = expandFileReferences("what is @missing.txt", repo)
	if strings.Contains(expanded, "[Referenced") || !strings.Contains(expanded, "@missing.txt") {
		t.Fatalf("unknown token not left literal: %q", expanded)
	}

	// A folder reference expands to its tree listing, not file content.
	expanded = expandFileReferences("summarize @sub/", repo)
	if !strings.Contains(expanded, "[Referenced folder @sub/]") || !strings.Contains(expanded, "sub/c.txt") {
		t.Fatalf("folder expansion missing listing: %q", expanded)
	}

	// A bare folder name (no trailing slash) also resolves as a folder.
	expanded = expandFileReferences("what does @sub contain", repo)
	if !strings.Contains(expanded, "[Referenced folder @sub]") {
		t.Fatalf("bare folder name not expanded: %q", expanded)
	}

	// No @ in the prompt: untouched.
	if got := expandFileReferences("plain prompt", repo); got != "plain prompt" {
		t.Fatalf("plain prompt changed: %q", got)
	}
}

func TestBuildFileIndexIncludesFoldersAndSkipsHidden(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("x"), 0600)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0600)
	os.MkdirAll(filepath.Join(root, ".git", "objects"), 0700)
	os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref"), 0600)
	os.MkdirAll(filepath.Join(root, "sub"), 0700)
	os.WriteFile(filepath.Join(root, "sub", "c.txt"), []byte("x"), 0600)
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}

	files := buildFileIndex(repo)
	// Depth-first order: a directory is followed immediately by its contents.
	want := []string{"a.txt", "b.txt", "sub/", filepath.Join("sub", "c.txt")}
	if len(files) != len(want) {
		t.Fatalf("index = %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("index = %v, want %v", files, want)
		}
	}
}

func TestMentionPopupCompletes(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("x"), 0600)
	os.WriteFile(filepath.Join(root, "beta.md"), []byte("x"), 0600)
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newUI(root, repo, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Typing @ schedules the index build; the message populates the popup.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	if !m.mention.open {
		t.Fatal("popup did not open on @")
	}
	// The walk cmd is discarded; the index message is fed synchronously.
	m.Update(fileIndexMsg{files: []string{"alpha.txt", "beta.md"}})
	if len(m.mention.matches) != 2 {
		t.Fatalf("matches = %v", m.mention.matches)
	}
	if view := m.View(); !strings.Contains(view, "alpha.txt") {
		t.Fatalf("popup rows missing from view: %q", view)
	}

	// Runes narrow the list; Enter completes into the draft.
	for _, r := range []rune("al") {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.mention.matches) != 1 || m.mention.matches[0] != "alpha.txt" {
		t.Fatalf("matches after query = %v", m.mention.matches)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mention.open {
		t.Fatal("popup stayed open after completion")
	}
	if string(m.input) != "@alpha.txt " {
		t.Fatalf("draft after completion = %q", string(m.input))
	}
}

func TestMentionPopupIgnoredWithoutMatches(t *testing.T) {
	root := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newUI(root, repo, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	m.Update(fileIndexMsg{files: []string{"alpha.txt"}})
	for _, r := range []rune("zzz") {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// No matches: Enter is not claimed by the popup and submits normally.
	if m.mentionActive() {
		t.Fatalf("popup claims input with no matches: %v", m.mention.matches)
	}
}

func TestRunTurnExpandsMentionedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "context.txt"), []byte("secret-of-context"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			if len(body.Messages) == 0 || body.Messages[len(body.Messages)-1].Role != "user" || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "secret-of-context") {
				t.Errorf("referenced file not inlined in user message: %+v", body.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		t.Errorf("unexpected extra model request")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	var events []turnEvent
	runTurn(context.Background(), client, repo, root, nil, "summarize @context.txt", nil, func(ev turnEvent) { events = append(events, ev) })
	for _, ev := range events {
		if ev.kind == "error" {
			t.Fatalf("agent error: %s", ev.text)
		}
	}
}
