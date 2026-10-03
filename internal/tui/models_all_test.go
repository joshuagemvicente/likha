package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
)

// fakeProviderRow registers a canned model list for one provider row: the
// seam the dialog uses, so tests never touch the fixed Providers URLs or
// the network. It restores the previous seam on cleanup.
func fakeProviderRow(t *testing.T, ids []string, err error) {
	t.Helper()
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		return ids, err
	}
	t.Cleanup(func() { listModelsFunc = prev })
}

// storedProviderKey writes one API-key credential into the state dir.
func storedProviderKey(t *testing.T, stateDir, name, key string) {
	t.Helper()
	if err := providers.StoreKey(stateDir, name, key); err != nil {
		t.Fatal(err)
	}
}

// setProviderBaseURL points one predefined row at a fake server for the
// test duration. The dialog resolves targets by table URL, so without this
// the fixed Providers URLs would hit the real network.
func setProviderBaseURL(t *testing.T, name, url string) string {
	t.Helper()
	for i, p := range model.Providers {
		if p.Name == name {
			prev := model.Providers[i].BaseURL
			model.Providers[i].BaseURL = url
			t.Cleanup(func() { model.Providers[i].BaseURL = prev })
			return prev
		}
	}
	t.Fatalf("unknown provider %q", name)
	return ""
}

// modelsTestUI builds a /models dialog host with a live client.
func modelsTestUI(t *testing.T, client *model.Client, name string, conn providers.Connection, stateDir string) *ui {
	t.Helper()
	m := NewUI("/sample", nil, client, name, conn, stateDir, nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// runModelsCmds executes a /models fetch command under the per-section
// protocol: a tea.BatchMsg runs each sub-command in order, any other
// message runs directly. Every non-nil arrival feeds back through m.Update
// and returns for section/failure assertions.
func runModelsCmds(t *testing.T, m *ui, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("models fetch did not start")
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var fed []tea.Msg
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			subMsg := sub()
			if subMsg == nil {
				continue
			}
			m.Update(subMsg)
			fed = append(fed, subMsg)
		}
		return fed
	}
	m.Update(msg)
	return []tea.Msg{msg}
}

// openModels opens /models and feeds every per-section arrival back in.
func openModels(t *testing.T, m *ui) []tea.Msg {
	t.Helper()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return runModelsCmds(t, m, cmd)
}

// TestBuildModelsTargetsActiveProviderFirstPreservesEveryTarget pins the
// active-provider reorder when it sits between other configured providers.
// The old nested append reused the target slice's backing array and could
// replace the active provider with the provider after it.
func TestBuildModelsTargetsActiveProviderFirstPreservesEveryTarget(t *testing.T) {
	stateDir := t.TempDir()
	var configured []model.Provider
	for _, p := range model.Providers {
		if p.Auth != model.AuthAPIKey {
			continue
		}
		storedProviderKey(t, stateDir, p.Name, "test-key")
		configured = append(configured, p)
	}
	if len(configured) < 3 {
		t.Skip("need at least three API-key providers to exercise a middle active target")
	}

	active := configured[len(configured)/2]
	targets := buildModelsTargets(stateDir, active.BaseURL, active.Name, "active-test-key", active.DisplayName)
	got := make([]string, 0, len(targets))
	for _, target := range targets {
		got = append(got, target.provider.Name)
	}
	want := []string{active.Name}
	for _, p := range configured {
		if p.Name != active.Name {
			want = append(want, p.Name)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("target order = %v, want %v", got, want)
	}
}

// arrivedSections collects the per-section arrivals fed by runModelsCmds.
func arrivedSections(msgs []tea.Msg) []modelsSection {
	var out []modelsSection
	for _, msg := range msgs {
		if s, ok := msg.(modelsSectionMsg); ok {
			out = append(out, s.section)
		}
	}
	return out
}

// arrivedFailures collects the per-provider failure display names fed by
// runModelsCmds.
func arrivedFailures(msgs []tea.Msg) []string {
	var out []string
	for _, msg := range msgs {
		if f, ok := msg.(modelsProviderFailedMsg); ok {
			out = append(out, f.displayName)
		}
	}
	return out
}

// TestAllModelsActiveSectionFirst verifies ordering (active provider first,
// then predefined order) plus one header per section.
func TestAllModelsActiveSectionFirst(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		switch {
		case strings.Contains(base, "openrouter"):
			return []string{"router-a", "router-b"}, nil
		case strings.Contains(base, "openai.com"):
			return []string{"o-a"}, nil
		default:
			return nil, fmt.Errorf("unexpected base %q", base)
		}
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://openrouter.ai/api/v1", "router-a", "k-router")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "router-a", providers.Connection{Provider: "OpenRouter", ProviderCanonical: "openrouter", Verified: true}, stateDir)
	msgs := openModels(t, m)
	sections := arrivedSections(msgs)
	if len(sections) != 2 {
		t.Fatalf("sections = %+v", msgs)
	}
	if sections[0].provider.Name != "openrouter" || sections[1].provider.Name != "openai" {
		t.Fatalf("section order = %q, %q", sections[0].provider.Name, sections[1].provider.Name)
	}
	if len(m.dialogModelRows) != 3 {
		t.Fatalf("rows = %+v", m.dialogModelRows)
	}
	view := m.View()
	for _, header := range []string{"OpenRouter", "OpenAI"} {
		if !strings.Contains(stripANSI(view), header) {
			t.Fatalf("header %q missing: %q", header, view)
		}
	}
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (live router-a)", m.dialog.cursor)
	}
}

// TestAllModelsProviderQueryFiltersToSection verifies a provider name
// narrows the list to its section with no orphan header.
func TestAllModelsProviderQueryFiltersToSection(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "openrouter") {
			return []string{"router-a"}, nil
		}
		return []string{"o-a"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "o-a", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-a", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	openModels(t, m)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("openrouter")})
	matches := m.dialogMatches()
	if len(matches) != 1 || m.dialogModelRows[matches[0]].model != "router-a" {
		t.Fatalf("filtered matches = %+v", matches)
	}
	view := m.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "OpenRouter") || strings.Contains(plain, "\n") && countHeaderLines(plain, "OpenAI") > 0 {
		t.Fatalf("orphan header rendered: %q", view)
	}
}

// countHeaderLines counts dialog rows exactly naming the header.
func countHeaderLines(view, header string) int {
	n := 0
	for _, line := range strings.Split(view, "\n") {
		plain := strings.TrimSpace(stripANSI(line))
		plain = strings.Trim(plain, "│ ")
		if plain == header {
			n++
		}
	}
	return n
}

// TestAllModelsPartialFailureNamesUnreachable verifies a failed provider
// contributes no rows but is named in the muted note while others list.
func TestAllModelsPartialFailureNamesUnreachable(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "openrouter") {
			return nil, fmt.Errorf("boom")
		}
		return []string{"o-a"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "o-a", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-a", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	msgs := openModels(t, m)
	sections := arrivedSections(msgs)
	failures := arrivedFailures(msgs)
	if len(sections) != 1 || len(failures) != 1 || failures[0] != "OpenRouter" {
		t.Fatalf("fetch = %+v", msgs)
	}
	if len(m.dialogModelRows) != 1 || m.dialog.loadErr != "" {
		t.Fatalf("rows=%+v loadErr=%q", m.dialogModelRows, m.dialog.loadErr)
	}
	if !strings.Contains(m.View(), "Unreachable: OpenRouter") {
		t.Fatalf("muted note missing: %q", m.View())
	}
}

// TestAllModelsUnconfiguredProvidersAbsent verifies providers without a
// stored key never appear, and no fetch runs for them.
func TestAllModelsUnconfiguredProvidersAbsent(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	var saw []string
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		saw = append(saw, base)
		return []string{"o-a"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "o-a", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-a", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	openModels(t, m)
	for _, base := range saw {
		if strings.Contains(base, "openrouter") {
			t.Fatalf("unconfigured provider fetched: %v", saw)
		}
	}
	if strings.Contains(m.View(), "OpenRouter") {
		t.Fatalf("unconfigured provider rendered: %q", m.View())
	}
}

// TestAllModelsChatGPTCuratedListNoNetwork verifies the signed-in chatgpt
// row contributes the curated list without any HTTP traffic.
func TestAllModelsChatGPTCuratedListNoNetwork(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	if err := providers.StoreOAuth(stateDir, "chatgpt", model.OAuthCredentials{Refresh: "r", Access: "a", Expires: 9999999999999}); err != nil {
		t.Fatal(err)
	}
	fetched := false
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "chatgpt.com") {
			fetched = true
		}
		return []string{"o-a"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "o-a", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-a", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	msgs := openModels(t, m)
	sections := arrivedSections(msgs)
	if fetched {
		t.Fatal("chatgpt row fetched over the network")
	}
	found := false
	for _, s := range sections {
		if s.provider.Name == "chatgpt" {
			found = true
			if len(s.models) != len(model.ChatGPTModels) {
				t.Fatalf("chatgpt models = %v", s.models)
			}
		}
	}
	if !found {
		t.Fatalf("chatgpt section missing: %+v", sections)
	}
}

// TestAllModelsCurrentMarksLivePairOnly verifies the (current) marker
// follows the live provider+model pair: a bare id match under another
// provider does not carry it.
func TestAllModelsCurrentMarksLivePairOnly(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		return []string{"shared"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "shared", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "shared", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	openModels(t, m)
	if len(m.dialogModelRows) != 2 {
		t.Fatalf("rows = %+v", m.dialogModelRows)
	}
	view := stripANSI(m.View())
	// Both providers report "shared"; colliding ids carry their provider
	// name, and only the live pair carries (current).
	if got := strings.Count(view, "shared · OpenAI (current)"); got != 1 {
		t.Fatalf("(current) marker wrong: %d, label = %q", got, view)
	}
	if strings.Count(view, "shared · OpenRouter (current)") != 0 {
		t.Fatalf("foreign pair carried the marker: %q", view)
	}
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (live pair)", m.dialog.cursor)
	}
	// The foreign row with the same id applies the other provider.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.conn.ProviderCanonical != "openrouter" || m.modelName != "shared" {
		t.Fatalf("session = %q on %q", m.modelName, m.conn.ProviderCanonical)
	}
}

// TestAllModelsCrossProviderSwitch verifies Enter on a foreign row moves
// the session to that provider+model, stores the pair, and the next turn
// uses it.
func TestAllModelsCrossProviderSwitch(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	if err := providers.SaveStoredConfig(stateDir, providers.StoredProviderConfig{Provider: "openai", Model: "o-a"}); err != nil {
		t.Fatal(err)
	}
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		if strings.Contains(base, "openrouter") {
			return []string{"router-a"}, nil
		}
		return []string{"o-a"}, nil
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://api.openai.com/v1", "o-a", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-a", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	openModels(t, m)
	// Move to the foreign row and apply.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open {
		t.Fatal("dialog did not close on Enter")
	}
	if m.modelName != "router-a" || m.conn.ProviderCanonical != "openrouter" {
		t.Fatalf("session = %q on %q", m.modelName, m.conn.ProviderCanonical)
	}
	if m.client.Base() != "https://openrouter.ai/api/v1" {
		t.Fatalf("client base = %q", m.client.Base())
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openrouter" || cfg.Model != "router-a" {
		t.Fatalf("stored pair: %+v %v", cfg, err)
	}
	last := m.entries[len(m.entries)-1]
	if last.role != "Lisa" || !strings.Contains(last.content, "router-a") || !strings.Contains(last.content, "OpenRouter") {
		t.Fatalf("switch entry = %+v", last)
	}
}

// TestAllModelsNoConfiguredProviderRefuses verifies zero network traffic
// when nothing is configured.
func TestAllModelsNoConfiguredProviderRefuses(t *testing.T) {
	stateDir := t.TempDir()
	fakeProviderRow(t, []string{"x"}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("network traffic with no configured provider")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	// No canonical row: the live client is unknown and nothing is stored,
	// so the dispatch refuses before any fetch starts.
	m := modelsTestUI(t, client, "x", providers.Connection{Provider: "Mystery", Verified: true}, stateDir)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/models")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog.open {
		t.Fatal("dialog opened with no configured provider")
	}
	if last := m.entries[len(m.entries)-1]; last.role != "Error" || !strings.Contains(last.content, "No provider configured") {
		t.Fatalf("refusal = %+v", last)
	}
}

// TestAllModelsActivationFailureKeepsSession verifies a failed foreign
// activation closes the dialog with a visible error and keeps the old pair.
func TestAllModelsActivationFailureKeepsSession(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	// A stored key that makes model.New reject the URL is impossible via
	// the seam, so drive the confirm path directly with a bad-base row.
	client, err := model.New("https://api.openai.com/v1", "o-a", "k-openai")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "o-a", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, stateDir)
	bad := model.Provider{Name: "openrouter", DisplayName: "OpenRouter", BaseURL: "://bad"}
	m.dialog = dialogState{kind: dialogModels, open: true}
	m.dialogModelRows = []modelsRow{{provider: bad, model: "router-a"}}
	m.confirmDialog(m.dialogMatches())
	if m.dialog.open {
		t.Fatal("dialog did not close on failed activation")
	}
	if m.dialogModelRows != nil {
		t.Fatalf("stale rows kept: %+v", m.dialogModelRows)
	}
	if m.modelName != "o-a" || m.client.Base() != "https://api.openai.com/v1" {
		t.Fatalf("session changed to %q on %q", m.modelName, m.client.Base())
	}
	last := m.entries[len(m.entries)-1]
	if last.role != "Error" || !strings.Contains(last.content, "OpenRouter") {
		t.Fatalf("error entry = %+v", last)
	}
}

// TestAllModelsSectionDeduplicatesRepeatedIds verifies a provider that
// re-lists an id renders it once per section in first-occurrence order,
// while ids shared across providers keep their per-provider labels.
func TestAllModelsSectionDeduplicatesRepeatedIds(t *testing.T) {
	stateDir := t.TempDir()
	storedProviderKey(t, stateDir, "openai", "k-openai")
	storedProviderKey(t, stateDir, "openrouter", "k-router")
	prev := listModelsFunc
	listModelsFunc = func(ctx context.Context, base, apiKey string) ([]string, error) {
		switch {
		case strings.Contains(base, "openai.com"):
			// alpha echoes through the catalog: one section, three mentions.
			return []string{"alpha", "beta", "alpha", "gamma", "alpha"}, nil
		case strings.Contains(base, "openrouter"):
			// Shares beta and gamma with the openai section.
			return []string{"beta", "gamma", "router-a"}, nil
		default:
			return nil, fmt.Errorf("unexpected base %q", base)
		}
	}
	t.Cleanup(func() { listModelsFunc = prev })
	client, err := model.New("https://openrouter.ai/api/v1", "beta", "k-router")
	if err != nil {
		t.Fatal(err)
	}
	m := modelsTestUI(t, client, "beta", providers.Connection{Provider: "OpenRouter", ProviderCanonical: "openrouter", Verified: true}, stateDir)
	if msgs := openModels(t, m); len(arrivedSections(msgs)) != 2 {
		t.Fatalf("sections = %+v", msgs)
	}
	if len(m.dialogModelRows) != 6 {
		t.Fatalf("rows = %+v", m.dialogModelRows)
	}
	var openaiIDs []string
	for _, r := range m.dialogModelRows {
		if r.provider.Name == "openai" {
			openaiIDs = append(openaiIDs, r.model)
		}
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(openaiIDs) != len(want) {
		t.Fatalf("openai ids = %v, want %v", openaiIDs, want)
	}
	for i := range want {
		if openaiIDs[i] != want[i] {
			t.Fatalf("openai ids = %v, want %v", openaiIDs, want)
		}
	}
	// Deduped rows drive the counts: alpha lists once within its section, so
	// it renders bare; beta and gamma list once per provider, so they carry
	// their provider labels.
	if m.dialogModelCounts["alpha"] != 1 || m.dialogModelCounts["beta"] != 2 {
		t.Fatalf("counts = %v", m.dialogModelCounts)
	}
	view := stripANSI(m.View())
	for _, label := range []string{
		"alpha",
		"beta · OpenRouter (current)",
		"beta · OpenAI",
		"gamma · OpenRouter",
		"gamma · OpenAI",
		"router-a",
	} {
		if !strings.Contains(view, label) {
			t.Fatalf("label %q missing: %q", label, view)
		}
	}
	if strings.Contains(view, "alpha · ") {
		t.Fatalf("intra-section duplicate labeled: %q", view)
	}
}
