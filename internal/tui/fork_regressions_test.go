package tui

// Regressions from running every turn on a forked client (model variants):
// state the composer client used to collect now lives on the run's fork, and
// forks share the composer's ChatGPT session.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

// seededSnapshot is an existing session: its stored entry keeps auto-naming
// from adding requests the tests do not count.
func seededSnapshot(id string) session.Snapshot {
	return session.Snapshot{ID: id, Entries: []session.Entry{{Role: "You", Content: "seed"}}}
}

// The Codex rate window arrives on the run's fork. The status line must show
// it during and after the run, not only for requests the composer made.
func TestRateLimitSegmentFollowsForkedRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list"}]}`)
			return
		}
		w.Header().Set("x-codex-primary-used-percent", "34")
		w.Header().Set("x-codex-primary-window-minutes", "300")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	creds := oauthUICredentials(t, state, "issued-rate-run")
	signedIn, err := model.NewOAuth(model.ChatGPTResource, "gpt-5.5", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		t.Fatal(err)
	}
	// The public resource URL stays intact; only the transport reaches the
	// local fixture, and every run fork inherits it.
	client, err := signedIn.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper { return variantUITransport{target, next} })
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "gpt-5.5", providers.Connection{Provider: "ChatGPT (Plus/Pro)", ProviderCanonical: "chatgpt", Verified: true}, state, nil, seededSnapshot("rate-run"))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	steerE2Esubmit(m, "hello")
	steerE2Edrive(t, m)
	if hasEntry(m, "Error", "") {
		t.Fatalf("run failed: %+v", m.entries)
	}
	if client.Usage() != "" {
		t.Fatal("fixture invalid: the composer client saw the run's headers")
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "34%") {
		t.Fatalf("rate window missing after the run: %q", view)
	}
}

// Submitting a new prompt while earlier prompts are held sends the held
// prompts and leaves no Queued row behind. The new prompt stays in the
// composer: queued behind the batch it would be screened as a steering
// prompt and dropped, not restored, if a hook blocked it.
func TestNewPromptSendsHeldQueueAndKeepsDraft(t *testing.T) {
	requests := make(chan steerE2Ereq, 8)
	round1 := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body steerE2Ereq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if first {
			close(round1)
			<-r.Context().Done() // held open until Esc cancels the run
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := steerE2EnewUI(t, "/sample", nil, client)

	steerE2Esubmit(m, "ask one")
	steerE2Ewait(t, round1, "the first model request")
	steerE2Esubmit(m, "queued-one")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	steerE2Edrive(t, m)
	steerE2Edrain(requests)
	if len(m.queue) != 1 {
		t.Fatalf("cancel did not hold the queue: %v", m.queue)
	}

	steerE2Esubmit(m, "brand new")
	if !m.working {
		t.Fatalf("new prompt did not start a turn: %+v", m.entries)
	}
	steerE2Edrive(t, m)
	var users []string
	for _, req := range steerE2Edrain(requests) {
		users = users[:0]
		for _, msg := range req.Messages {
			if msg.Role == "user" {
				users = append(users, msg.Content)
			}
		}
	}
	if n := len(users); n == 0 || users[n-1] != "queued-one" {
		t.Fatalf("last request users = %q, want the held queued-one last", users)
	}
	if got := string(m.input); got != "brand new" {
		t.Fatalf("composer = %q, want the new prompt kept", got)
	}
	if !hasEntry(m, "Likha", heldQueueSentNote) {
		t.Fatalf("no note that the new prompt stayed in the composer: %+v", m.entries)
	}
	if len(m.queue) != 0 || len(m.queueSelections) != 0 {
		t.Fatalf("queue survived the send: %v", m.queue)
	}
	if hasEntry(m, "Queued", "") {
		t.Fatalf("orphaned Queued row: %+v", m.entries)
	}
}

// A command that starts its own turn cannot run past held prompts: it is
// refused visibly and the held queue stays intact and clearable.
func TestTurnCommandRefusedWhileQueueHeld(t *testing.T) {
	// An unroutable local endpoint: a regression fails locally, never online.
	client, err := model.New("http://127.0.0.1:1/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"/init cover the docs", "/install ripgrep", "/skill demo tidy up"} {
		t.Run(command, func(t *testing.T) {
			m := steerE2EnewUI(t, "/sample", nil, client)
			writeDemoSkill(t, m.stateDir)
			m.entries = append(m.entries, entry{role: "Queued", content: "held"})
			m.queue = []string{"held"}
			steerE2Esubmit(m, command)
			if m.working || len(m.queue) != 1 || !hasEntry(m, "Error", "Queued prompts are held") {
				t.Fatalf("%s ran past the held queue: working=%t queue=%v", command, m.working, m.queue)
			}
			if got := string(m.input); got != command {
				t.Fatalf("composer = %q after the refusal, want %q kept", got, command)
			}
			if hasEntry(m, "Likha", "Loaded skill") {
				t.Fatalf("a refused /skill claimed its text reaches the provider: %+v", m.entries)
			}
		})
	}
}

// writeDemoSkill installs a valid global skill named demo.
func writeDemoSkill(t *testing.T, stateDir string) {
	t.Helper()
	dir := filepath.Join(stateDir, "skills", "demo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	text := "---\nname: demo\ndescription: A demo skill.\n---\nTidy the repository.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// openAIRoutedClient is an API-key OpenAI client whose requests reach server.
func openAIRoutedClient(t *testing.T, server *httptest.Server, id string) *model.Client {
	t.Helper()
	p, _ := model.LookupProvider("openai")
	client, err := model.New(p.BaseURL, id, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err = client.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper { return variantUITransport{target, next} })
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// responsesServer answers every Responses request with one completed message
// and records the request bodies.
func responsesServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			fmt.Fprint(w, `{"data":[{"id":"gpt-5.5"}]}`)
			return
		}
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

// Replacing a ChatGPT composer client ends the OAuth session its forks
// share. Held prompts captured on it must still send after the swap, with a
// visible note naming the selection they now use.
func TestHeldQueueSurvivesChatGPTClientReplacement(t *testing.T) {
	server, bodies := responsesServer(t)
	state := t.TempDir()
	creds := oauthUICredentials(t, state, "issued-held-swap")
	oauth, err := model.NewOAuth(model.ChatGPTResource, "gpt-5.5", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, oauth, "gpt-5.5", providers.Connection{Provider: "ChatGPT (Plus/Pro)", ProviderCanonical: "chatgpt", Verified: true}, state, nil, seededSnapshot("held-swap"))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.enqueue("held work") // an idle enqueue is exactly a held prompt
	if len(m.queueSelections) != 1 || m.queueSelections[0].client == nil {
		t.Fatal("fixture invalid: held prompt captured no selection")
	}

	p, _ := model.LookupProvider("openai")
	next := openAIRoutedClient(t, server, "gpt-5.5")
	m.Update(selectionRestoredMsg{sessionID: m.snapshot.ID, revision: m.selectionRevision, client: next, provider: p, selection: model.Selection{Provider: p.Name, Model: "gpt-5.5"}})
	if m.client != next {
		t.Fatal("fixture invalid: restore did not replace the composer client")
	}
	if !hasEntry(m, "Likha", "will run with openai/gpt-5.5") {
		t.Fatalf("no visible note about the re-captured queue: %+v", m.entries)
	}

	d := newStrictDriver(t, m)
	d.key(key("enter"))
	d.until(func() bool { return !m.working }, "held run")
	if hasEntry(m, "Error", "") {
		t.Fatalf("held prompt failed after the swap: %+v", m.entries)
	}
	if got := bodies(); len(got) != 1 || !strings.Contains(got[0], "held work") {
		t.Fatalf("held prompt never reached the new client: %q", got)
	}
}

// A session restore that lands after a turn committed its selection cannot
// replace the selection the turn just ran with.
func TestLateRestoreCannotReplaceSelectionCommittedByTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "test-model", "")
	if err != nil {
		t.Fatal(err)
	}
	m := steerE2EnewUI(t, "/sample", nil, client)
	p, _ := model.LookupProvider("openai")
	other, err := model.New(p.BaseURL, "gpt-5.5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	// Issued before the turn starts, as a resume's restore would be.
	late := selectionRestoredMsg{sessionID: m.snapshot.ID, revision: m.selectionRevision, client: other, provider: p, selection: model.Selection{Provider: p.Name, Model: "gpt-5.5"}}

	steerE2Esubmit(m, "run first")
	steerE2Edrive(t, m)
	m.Update(late)
	if m.client != client || m.modelName != "local" {
		t.Fatalf("late restore replaced the committed selection with %s", m.modelName)
	}
}

// A billed naming request still counts after the user switched sessions;
// only its name is not applied.
func TestLateNamingUsageCountsAfterSessionSwitch(t *testing.T) {
	m := NewUI("/sample", nil, nil, "gpt-4o", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, t.TempDir(), nil, seededSnapshot("current"))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(nameGeneratedMsg{sessionID: "switched-away", name: "Old name", usageOK: true, provider: "openai", model: "gpt-4o",
		usage: model.RequestUsage{Prompt: 1000, Completion: 10, PromptSeen: true, CompletionSeen: true}})
	if m.usagePrompt != 1000 || m.usageCompletion != 10 {
		t.Fatalf("naming usage dropped: prompt=%d completion=%d", m.usagePrompt, m.usageCompletion)
	}
	if m.snapshot.NamedTitle != "" {
		t.Fatalf("name applied to the wrong session: %q", m.snapshot.NamedTitle)
	}
}

// Naming is silent: a client that cannot produce the naming request leaves
// the derived title without an Error row.
func TestSessionNamingClientFailureIsSilent(t *testing.T) {
	state := t.TempDir()
	creds := oauthUICredentials(t, state, "issued-naming-silent")
	client, err := model.NewOAuth(model.ChatGPTResource, "gpt-5.5", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "gpt-5.5", providers.Connection{Provider: "ChatGPT (Plus/Pro)", ProviderCanonical: "chatgpt", Verified: true}, state, nil, session.Snapshot{ID: "fresh"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	client.InvalidateOAuth()
	m.working, m.runID, m.cancel = true, 1, func() {}
	m.events = make(chan agent.TurnEvent, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "done", History: []model.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok"}}})
	if !m.nameTried {
		t.Fatal("fixture invalid: naming was not attempted")
	}
	if hasEntry(m, "Error", "") {
		t.Fatalf("naming failure surfaced: %+v", m.entries)
	}
}

// A re-login whose variant cannot be applied changes nothing on disk and
// releases the candidate's ChatGPT session.
func TestChatGPTReloginVariantFailureKeepsStoredAccount(t *testing.T) {
	dir := t.TempDir()
	active := oauthUICredentials(t, dir, "issued-relogin-active")
	other := oauthUICredentials(t, dir, "issued-relogin-other")
	for _, c := range []model.OAuthCredentials{other, active} {
		if err := providers.StoreOAuth(dir, "chatgpt", c); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{active.ClientID, other.ClientID} {
		if err := acknowledgeChatGPTPlan(dir, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := providers.UpdateStoredConfig(context.Background(), dir, func(cfg *providers.StoredProviderConfig) error {
		cfg.Provider, cfg.Model = "chatgpt", "real-model"
		cfg.DefaultVariants = map[string]map[string]string{"chatgpt": {"real-model": "missing-variant"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	live, err := model.NewOAuth(model.ChatGPTResource, "real-model", active.Issuer, active.ClientID, active)
	if err != nil {
		t.Fatal(err)
	}
	p := mustChatGPTProvider(t)
	m := NewUI("/sample", nil, live, "real-model", providers.Connection{Provider: p.DisplayName, ProviderCanonical: p.Name, Verified: true}, dir, nil, seededSnapshot("relogin"))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	stubOAuthModels(t, []model.ModelDetails{{ID: "real-model"}}, nil)
	m.openProviderAuth(p)
	for i := 0; i < len(m.keyModal.accounts) && m.keyModal.accounts[m.keyModal.accountCursor].ClientID != other.ClientID; i++ {
		m.updateKeyModal(key("down"))
	}
	if m.keyModal.accounts[m.keyModal.accountCursor].ClientID != other.ClientID {
		t.Fatal("fixture invalid: could not select the other account")
	}
	result := runOAuthFlow(t, m, m.updateKeyModal(key("enter")))
	if result.client == nil || result.err != nil {
		t.Fatalf("fixture invalid: flow produced no candidate: %v", result.err)
	}
	if !strings.Contains(m.keyModal.err, "Select variant") {
		t.Fatalf("variant failure not reported: %q", m.keyModal.err)
	}
	if _, selected, err := providers.OAuthAccounts(dir, "chatgpt"); err != nil || selected != active.ClientID {
		t.Fatalf("failed re-login switched the stored account to %q (%v)", selected, err)
	}
	if m.client != live || !clientUsable(live) {
		t.Fatal("failed re-login disturbed the live client")
	}
	if clientUsable(result.client) {
		t.Fatal("failed re-login left the candidate session running")
	}
}

// A /models switch whose variant cannot be applied releases the candidate.
func TestModelSwitchVariantFailureReleasesCandidate(t *testing.T) {
	dir := t.TempDir()
	creds := oauthUICredentials(t, dir, "issued-switch-candidate")
	if err := providers.UpdateStoredConfig(context.Background(), dir, func(cfg *providers.StoredProviderConfig) error {
		cfg.DefaultVariants = map[string]map[string]string{"chatgpt": {"real-model": "missing-variant"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := variantTestUI(t, "gpt-5.5")
	m.stateDir = dir
	current := m.client
	candidate, err := model.NewOAuth(model.ChatGPTResource, "real-model", creds.Issuer, creds.ClientID, creds)
	if err != nil {
		t.Fatal(err)
	}
	m.activateModelClient(mustChatGPTProvider(t), candidate, "real-model", liveEffort{}, nil)
	if m.client != current || !hasEntry(m, "Error", "Select model") {
		t.Fatal("failed switch replaced the live client or stayed silent")
	}
	if clientUsable(candidate) {
		t.Fatal("failed switch left the candidate session running")
	}
}
