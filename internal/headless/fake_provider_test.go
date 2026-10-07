package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"likha/internal/model"
	"likha/internal/repository"
	"likha/internal/session"
)

const testKey = "sk-headless-secret-123"

// reply writes one streamed chat-completions response.
type reply func(w http.ResponseWriter)

func textReply(text string) reply {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
		fmt.Fprintf(w, "data: %s\n\n", delta)
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func toolReply(id, name string, arguments any) reply {
	return func(w http.ResponseWriter) {
		args, _ := json.Marshal(arguments)
		w.Header().Set("Content-Type", "text/event-stream")
		delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}},
		}}}}})
		fmt.Fprintf(w, "data: %s\n\n", delta)
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// failReply answers with an HTTP error whose body echoes the API key.
func failReply(status int) reply {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"message":"upstream broke for key %s"}}`, testKey)
	}
}

// hangReply blocks until the request is cancelled.
func hangReply(release <-chan struct{}) reply {
	return func(w http.ResponseWriter) {
		<-release
	}
}

// fakeProvider serves a model list and answers each POST with the next
// scripted reply; it records request bodies.
type fakeProvider struct {
	server  *httptest.Server
	mu      sync.Mutex
	replies []reply
	bodies  []map[string]any
}

func newFakeProvider(t *testing.T, replies ...reply) *fakeProvider {
	t.Helper()
	fake := &fakeProvider{replies: replies}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		fake.mu.Lock()
		fake.bodies = append(fake.bodies, body)
		var next reply
		if len(fake.replies) > 0 {
			next, fake.replies = fake.replies[0], fake.replies[1:]
		}
		fake.mu.Unlock()
		if next == nil {
			failReply(http.StatusInternalServerError)(w)
			return
		}
		next(w)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeProvider) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.bodies...)
}

// toolNames lists the tool definitions one request offered.
func toolNames(body map[string]any) []string {
	var names []string
	list, _ := body["tools"].([]any)
	for _, item := range list {
		tool, _ := item.(map[string]any)
		function, _ := tool["function"].(map[string]any)
		if name, ok := function["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// testRun is a ready headless configuration against a fake provider.
type testRun struct {
	cfg   Config
	root  string
	store *session.Store
}

func newTestRun(t *testing.T, fake *fakeProvider, prompt string) testRun {
	t.Helper()
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
	t.Cleanup(func() { store.Close() })
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	client, err := model.New(fake.server.URL+"/v1", "test-model", testKey)
	if err != nil {
		t.Fatal(err)
	}
	return testRun{root: root, store: store, cfg: Config{
		Prompt: prompt, Root: root, Repo: repo, Client: client, Store: store, Snapshot: snapshot,
		StateDir: state, Provider: "custom", Display: "Test provider", Secrets: []string{testKey},
	}}
}
