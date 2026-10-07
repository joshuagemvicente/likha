package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/headless"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

// A session saved by a headless run (specs/headless-print D3) resumes in the
// TUI with its prompt, its refused edit and its answer visible.
func TestHeadlessSessionResumesInTUI(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"e1","type":"function","function":{"name":"edit_file","arguments":"{\"path\":\"a.txt\",\"content\":\"x\"}"}}]}}]}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Headless answer text"}}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(state, root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := headless.Run(context.Background(), headless.Config{
		Prompt: "please edit", Root: root, Repo: repo, Client: client, Store: store,
		Snapshot: snapshot, StateDir: state, Display: "Test",
	}, &stdout, &stderr)
	if code != headless.ExitCompleted {
		t.Fatalf("headless exit = %d\n%s", code, stderr.String())
	}

	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Test", Verified: true}, "", store, saved)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m.layoutWidth = 0
	m.rebuild()
	view := strings.Join(m.lines, "\n")
	for _, want := range []string{"please edit", "Headless answer text", "headless mode"} {
		if !strings.Contains(view, want) {
			t.Fatalf("resumed view is missing %q:\n%s", want, view)
		}
	}
}
