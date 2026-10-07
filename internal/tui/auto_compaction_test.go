package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
	"likha/internal/transcript"
)

// autoCompactProvider plays a chat-completions provider for automatic
// compaction runs: a tool-less request ending in the compact instruction is
// the summarize call, anything else is a turn request answered "Done.".
type autoCompactProvider struct {
	t *testing.T
	// release, when set, holds the summarize answer until it is closed.
	release chan struct{}
	// blockUntilCancelled makes the summarize call wait for the client to go.
	blockUntilCancelled bool

	mu         sync.Mutex
	summarizes int
	turns      [][]string // message contents of each turn request
}

func (p *autoCompactProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/models" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[]}`)
		return
	}
	var body struct {
		Tools    json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		p.t.Errorf("decode request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	last := body.Messages[len(body.Messages)-1]
	w.Header().Set("Content-Type", "text/event-stream")
	if len(body.Tools) == 0 && last.Role == "user" && strings.HasPrefix(last.Content, "Summarize the conversation above") {
		p.mu.Lock()
		p.summarizes++
		p.mu.Unlock()
		if p.blockUntilCancelled {
			<-r.Context().Done()
			return
		}
		if p.release != nil {
			<-p.release
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Earlier: a long answer about the repo.\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":900,\"completion_tokens\":40}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	var contents []string
	for _, message := range body.Messages {
		contents = append(contents, message.Content)
	}
	p.mu.Lock()
	p.turns = append(p.turns, contents)
	p.mu.Unlock()
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

type autoCompactRig struct {
	m        *ui
	store    *session.Store
	snapshot session.Snapshot
	provider *autoCompactProvider
	client   *model.Client
	root     string
	repo     *repository.Repository
	stateDir string
	prior    []model.Message
}

// newAutoCompactRig starts a UI whose stored conversation is big enough that
// the next request reaches the whole known window, so it triggers.
func newAutoCompactRig(t *testing.T, provider *autoCompactProvider, configJSON string) *autoCompactRig {
	t.Helper()
	provider.t = t
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	if configJSON != "" {
		if err := os.WriteFile(providers.ConfigFilePath(stateDir), []byte(configJSON), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewUI(root, repo, client, "test-model", providers.Connection{Provider: "OpenAI", Verified: true}, stateDir, store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	prior := []model.Message{
		{Role: "user", Content: "Earlier question"},
		{Role: "assistant", Content: strings.Repeat("Long earlier answer about the repository. ", 4000)},
	}
	m.history = append([]model.Message(nil), prior...)
	m.entries = append(m.entries, entry{role: "You", content: "Earlier question"}, entry{role: "Assistant", content: "Long earlier answer"})
	tokens, ok := client.EstimateHistoryTokens(prior)
	if !ok {
		t.Fatal("estimate unavailable")
	}
	m.contextWindow, m.contextWindowKnown = tokens, true
	return &autoCompactRig{m: m, store: store, snapshot: snapshot, provider: provider, client: client, root: root, repo: repo, stateDir: stateDir, prior: prior}
}

// drive submits prompt and feeds the run's events to the UI until the run
// ends. after runs once per event, after the UI applied it.
func (r *autoCompactRig) drive(t *testing.T, prompt string, after func(agent.TurnEvent)) {
	t.Helper()
	sendRunes(r.m, prompt)
	r.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !r.m.working {
		t.Fatalf("run did not start: entries=%+v", r.m.entries)
	}
	for r.m.working {
		select {
		case ev, ok := <-r.m.events:
			if !ok {
				t.Fatal("events closed while the UI still works")
			}
			r.m.Update(ev)
			if after != nil {
				after(ev)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("run did not finish")
		}
	}
}

func (r *autoCompactRig) entryContaining(text string) (entry, bool) {
	for _, e := range r.m.entries {
		if strings.Contains(e.content, text) {
			return e, true
		}
	}
	return entry{}, false
}

func lastTurnFooter(t *testing.T, entries []entry) transcript.TurnInfo {
	t.Helper()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].role == transcript.TurnRole {
			info, ok := transcript.DecodeTurn(entries[i].content)
			if !ok {
				t.Fatalf("bad footer %q", entries[i].content)
			}
			return info
		}
	}
	t.Fatal("no turn footer")
	return transcript.TurnInfo{}
}

func TestAutoCompactionRunCompactsContinuesAndPersists(t *testing.T) {
	provider := &autoCompactProvider{}
	rig := newAutoCompactRig(t, provider, "")
	var statuses []string
	rig.drive(t, "Now fix the bug", func(ev agent.TurnEvent) {
		if ev.Kind == "compacting" {
			statuses = append(statuses, rig.m.status)
			if !rig.m.working {
				t.Error("compaction ended the run")
			}
		}
	})

	if len(statuses) != 1 || statuses[0] != "Compacting…" {
		t.Fatalf("status during compaction = %q, want Compacting…", statuses)
	}
	if provider.summarizes != 1 || len(provider.turns) != 1 {
		t.Fatalf("summarizes=%d turns=%d, want 1 and 1", provider.summarizes, len(provider.turns))
	}
	if rig.m.status != "Ready" {
		t.Fatalf("status = %q, want Ready", rig.m.status)
	}
	divider, ok := rig.entryContaining("Conversation compacted automatically at ~")
	if !ok || divider.role != "Likha" || !strings.Contains(divider.content, "context window. Summary of earlier turns:\n\nEarlier: a long answer about the repo.") {
		t.Fatalf("divider = %+v", divider)
	}
	// The earlier transcript stays on screen; only what is sent changed.
	if _, ok := rig.entryContaining("Long earlier answer"); !ok {
		t.Fatal("earlier transcript entry was removed")
	}
	if footer := lastTurnFooter(t, rig.m.entries); !footer.Compacted {
		t.Fatalf("footer = %+v, want compacted", footer)
	}
	// The turn request carried the summary, then the prompt verbatim.
	sent := strings.Join(provider.turns[0], "\n")
	if !strings.Contains(sent, "Earlier: a long answer about the repo.") || !strings.Contains(sent, "Now fix the bug") || strings.Contains(sent, "Long earlier answer about the repository.") {
		t.Fatalf("turn request did not carry the compacted conversation: %q", sent)
	}
	// The summarize call's usage is in session spend.
	if rig.m.usagePrompt != 900 || rig.m.usageCompletion != 40 {
		t.Fatalf("usage = %d/%d, want the summarize call's 900/40", rig.m.usagePrompt, rig.m.usageCompletion)
	}
	// The context segment was recalculated for the compacted conversation.
	if rig.m.contextTokens >= rig.m.contextWindow*85/100 {
		t.Fatalf("context = %d of %d, want the compacted size", rig.m.contextTokens, rig.m.contextWindow)
	}

	saved, err := rig.store.Load(rig.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 4 || saved.History[0].Role != "developer" || saved.History[1].Content != "Now fix the bug" || saved.History[3].Content != "Done." {
		t.Fatalf("persisted history = %+v", saved.History)
	}
	resumed := NewUI(rig.root, rig.repo, rig.client, "test-model", providers.Connection{Provider: "OpenAI", Verified: true}, rig.stateDir, rig.store, saved)
	if !historyEqual(resumed.history, saved.History) {
		t.Fatalf("resumed history = %+v", resumed.history)
	}
	found := false
	for _, e := range resumed.entries {
		found = found || strings.Contains(e.content, "Conversation compacted automatically at ~")
	}
	if !found {
		t.Fatal("resumed session lost the automatic divider")
	}
}

func TestAutoCompactionDeliversPromptQueuedDuringCompaction(t *testing.T) {
	provider := &autoCompactProvider{release: make(chan struct{})}
	rig := newAutoCompactRig(t, provider, "")
	released := false
	rig.drive(t, "Now fix the bug", func(ev agent.TurnEvent) {
		if ev.Kind == "compacting" && !released {
			sendRunes(rig.m, "also update the docs")
			rig.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if len(rig.m.queue) != 1 {
				t.Errorf("queue = %v, want the prompt queued during compaction", rig.m.queue)
			}
			released = true
			close(provider.release)
		}
	})

	if !released {
		t.Fatal("compaction never started")
	}
	if len(provider.turns) != 1 {
		t.Fatalf("turn requests = %d, want 1", len(provider.turns))
	}
	if sent := strings.Join(provider.turns[0], "\n"); !strings.Contains(sent, "also update the docs") {
		t.Fatalf("queued prompt was not delivered in the same run: %q", sent)
	}
	if len(rig.m.queue) != 0 {
		t.Fatalf("queue = %v, want empty", rig.m.queue)
	}
	for _, e := range rig.m.entries {
		if e.role == "Queued" {
			t.Fatalf("stray queued row: %+v", e)
		}
	}
	if e, ok := rig.entryContaining("also update the docs"); !ok || e.role != "You" {
		t.Fatalf("delivered prompt row = %+v", e)
	}
}

func TestAutoCompactionEscCancelsRunUncompacted(t *testing.T) {
	provider := &autoCompactProvider{blockUntilCancelled: true}
	rig := newAutoCompactRig(t, provider, "")
	rig.drive(t, "Now fix the bug", func(ev agent.TurnEvent) {
		if ev.Kind == "compacting" {
			rig.m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		}
	})

	if rig.m.status != "Cancelled" {
		t.Fatalf("status = %q, want Cancelled", rig.m.status)
	}
	want := append(append([]model.Message(nil), rig.prior...), model.Message{Role: "user", Content: "Now fix the bug"})
	if !historyEqual(rig.m.history, want) {
		t.Fatalf("history = %+v, want it uncompacted", rig.m.history)
	}
	if _, ok := rig.entryContaining("Conversation compacted automatically"); ok {
		t.Fatal("a cancelled compaction left a divider")
	}
	if len(provider.turns) != 0 {
		t.Fatalf("turn requests = %d after cancel, want 0", len(provider.turns))
	}
}

func TestAutoCompactionOptOutSendsUncompacted(t *testing.T) {
	provider := &autoCompactProvider{}
	rig := newAutoCompactRig(t, provider, `{"provider":"openai","model":"test-model","auto_compact":false}`)
	rig.drive(t, "Now fix the bug", nil)

	if provider.summarizes != 0 {
		t.Fatalf("summarizes = %d with auto_compact false", provider.summarizes)
	}
	if len(provider.turns) != 1 || !strings.Contains(strings.Join(provider.turns[0], "\n"), "Long earlier answer about the repository.") {
		t.Fatal("the request was not sent with the full conversation")
	}
	if footer := lastTurnFooter(t, rig.m.entries); footer.Compacted {
		t.Fatalf("footer = %+v, want not compacted", footer)
	}

	// The opt-out leaves manual /compact working as before.
	sendRunes(rig.m, "/compact")
	rig.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !rig.m.working || rig.m.status != "Compacting…" {
		t.Fatalf("/compact did not start: working=%t status=%q", rig.m.working, rig.m.status)
	}
	rig.m.Update(<-rig.m.events)
	if provider.summarizes != 1 || rig.m.working {
		t.Fatalf("/compact: summarizes=%d working=%t", provider.summarizes, rig.m.working)
	}
	if _, ok := rig.entryContaining("Conversation compacted. Summary of earlier turns:"); !ok {
		t.Fatal("/compact divider missing")
	}
}

func TestAutoCompactionNeedsKnownWindow(t *testing.T) {
	provider := &autoCompactProvider{}
	rig := newAutoCompactRig(t, provider, "")
	rig.m.contextWindow, rig.m.contextWindowKnown = 0, false
	rig.drive(t, "Now fix the bug", nil)

	if provider.summarizes != 0 || len(provider.turns) != 1 {
		t.Fatalf("summarizes=%d turns=%d, want 0 and 1", provider.summarizes, len(provider.turns))
	}
}

func TestAutoCompactDivider(t *testing.T) {
	cases := []struct {
		name   string
		tokens int64
		window int64
		want   string
	}{
		{"spec example", 172_000, 200_000, "Conversation compacted automatically at ~86% of the 200k context window. Summary of earlier turns:"},
		{"million window", 900_000, 1_000_000, "Conversation compacted automatically at ~90% of the 1M context window. Summary of earlier turns:"},
		{"no window", 10, 0, "Conversation compacted automatically. Summary of earlier turns:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoCompactDivider(tc.tokens, tc.window); got != tc.want {
				t.Fatalf("autoCompactDivider = %q, want %q", got, tc.want)
			}
		})
	}
}
