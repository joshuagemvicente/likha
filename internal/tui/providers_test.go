package tui

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

// modelsListBody is the canned /models payload the fake endpoints serve.
const modelsListBody = `{"object":"list","data":[{"id":"alpha"},{"id":"beta"}]}`

// newProvidersServer starts a fake OpenAI-compatible endpoint serving the
// model list on its /models route. With failing set, /models returns 500 so
// the connection check fails without any real network traffic.
func newProvidersServer(t *testing.T, failing bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"boom"}}`)
			return
		}
		fmt.Fprint(w, modelsListBody)
	}))
}

// newProvidersUI builds a main-mode UI connected to endpoint, started on the
// OpenAI provider so the providers dialog cursor has an active row to find.
func newProvidersUI(t *testing.T, stateDir, endpoint string) *ui {
	t.Helper()
	client, err := model.New(endpoint, "start-model", "")
	if err != nil {
		t.Fatal(err)
	}
	return NewUI("/sample", nil, client, "start-model", providers.Connection{Provider: "OpenAI", Verified: true}, stateDir, nil, session.Snapshot{})
}

// runCommand types a prompt line and submits it with Enter. Any draft left
// by a previous command is discarded first.
func runCommand(m *ui, line string) tea.Cmd {
	m.input = nil
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(line)})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return cmd
}

// findEntry reports the newest entry whose content contains needle.
func findEntry(m *ui, needle string) (entry, bool) {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if strings.Contains(m.entries[i].content, needle) {
			return m.entries[i], true
		}
	}
	return entry{}, false
}

// TestProvidersDialogBareCommand mirrors the themes dialog: a bare /providers
// opens the selection dialog, configured providers show a check, unconfigured
// ones show "not configured", and the cursor starts on the active provider.
func TestProvidersDialogBareCommand(t *testing.T) {
	stateDir := t.TempDir()
	if err := providers.StoreKey(stateDir, "opencode-go", "k"); err != nil {
		t.Fatal(err)
	}
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	runCommand(m, "/providers")

	if !m.dialog.open || m.dialog.kind != dialogProviders {
		t.Fatalf("dialog did not open: %+v", m.dialog)
	}
	if len(m.dialogItems) != len(model.Providers) {
		t.Fatalf("dialog rows = %d, want %d", len(m.dialogItems), len(model.Providers))
	}
	var goRow, openAIRow bool
	for _, item := range m.dialogItems {
		if strings.HasPrefix(item, "✔ Opencode Go (opencode-go)") {
			goRow = true
		}
		if strings.HasPrefix(item, "OpenAI (openai)") && strings.Contains(item, "— not configured") {
			openAIRow = true
		}
	}
	if !goRow || !openAIRow {
		t.Fatalf("rows not rendered as configured: %+v", m.dialogItems)
	}
	// The session runs on OpenAI, so the cursor starts on its row.
	if m.dialog.cursor != 0 {
		t.Fatalf("cursor = %d, want the OpenAI row 0", m.dialog.cursor)
	}
	if !strings.Contains(m.View(), "Provider selection") {
		t.Fatalf("view missing dialog title: %q", m.View())
	}
}

// TestProvidersDialogFilter verifies the shared query behavior: typing
// filters the rows, the first Esc clears the query and keeps the dialog
// open, and the second Esc closes it.
func TestProvidersDialogFilter(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	runCommand(m, "/providers")
	if len(m.dialogMatches()) != len(model.Providers) {
		t.Fatalf("empty query should show all rows, got %d", len(m.dialogMatches()))
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("open")})
	if m.dialog.query != "open" {
		t.Fatalf("query = %q, want open", m.dialog.query)
	}
	matches := m.dialogMatches()
	if len(matches) == 0 || len(matches) >= len(model.Providers) {
		t.Fatalf("filter matches = %d, want a strict subset", len(matches))
	}
	view := m.View()
	if !strings.Contains(view, "Opencode") {
		t.Fatalf("view dropped a matching row: %q", view)
	}
	if strings.Contains(view, "Bedrock") {
		t.Fatalf("view still shows a filtered-out row: %q", view)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dialog.open || m.dialog.query != "" {
		t.Fatalf("first Esc: open=%t query=%q", m.dialog.open, m.dialog.query)
	}
	if len(m.dialogMatches()) != len(model.Providers) {
		t.Fatal("cleared query should show all rows again")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("second Esc did not close the dialog")
	}
	if len(m.input) != 0 {
		t.Fatalf("dialog leaked into the prompt draft: %q", string(m.input))
	}
}

// TestProvidersDialogNoMatches verifies the empty match set: the view says so
// and Enter is a no-op — the dialog neither closes nor re-selects anything.
func TestProvidersDialogNoMatches(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	runCommand(m, "/providers")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})
	if len(m.dialogMatches()) != 0 {
		t.Fatalf("matches for a junk query: %+v", m.dialogMatches())
	}
	if !strings.Contains(m.View(), "No providers match") {
		t.Fatalf("view missing empty-match row: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dialog.open {
		t.Fatal("Enter closed the dialog with nothing selectable")
	}
	// Esc still exits: first clears the query, second closes.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dialog.open || m.dialog.query != "" {
		t.Fatalf("Esc did not clear the junk query: %+v", m.dialog)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("Esc did not close the dialog after clearing")
	}
}

// TestProvidersEnterOpensAuthWithoutSwitching drives the core contract: Enter
// on a configured API-key row opens the auth-state view and swaps nothing —
// the client, model name, connection, and stored files are untouched, and no
// network request is made (the fake endpoint would 404 any non-/models call,
// and the auth path never dials at all).
func TestProvidersEnterOpensAuthWithoutSwitching(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	if err := providers.StoreKey(stateDir, "openai", "test-key"); err != nil {
		t.Fatal(err)
	}
	providersBefore, err := os.ReadFile(providers.KeyFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	runCommand(m, "/providers")
	// The cursor starts on the live OpenAI row; Enter must only auth.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter on a configured row returned a command")
	}
	if !m.keyModal.open || !m.keyModal.auth || m.keyModal.provider.Name != "openai" {
		t.Fatalf("auth-state view not opened for openai: %+v", m.keyModal)
	}
	if !strings.Contains(m.View(), "Auth for OpenAI") ||
		!strings.Contains(m.View(), "stored in the private state directory (providers.json)") {
		t.Fatalf("auth view missing provider/source: %q", m.View())
	}
	if m.client != prevClient {
		t.Fatal("Enter replaced the live client")
	}
	if m.modelName != "start-model" {
		t.Fatalf("model changed to %q", m.modelName)
	}
	if m.conn.Provider != "OpenAI" {
		t.Fatalf("connection changed to %q", m.conn.Provider)
	}
	if !m.dialog.open {
		t.Fatal("Enter closed the providers dialog")
	}
	providersAfter, err := os.ReadFile(providers.KeyFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(providersBefore, providersAfter) {
		t.Fatal("providers.json was rewritten by navigation alone")
	}
}

// TestProvidersAuthReplaceKeepsOldKeyOnFailure drives key replacement: the
// auth-state view's Enter re-opens the key modal, and a failed check keeps
// the OLD key stored, shows the error, and leaves the live session alone.
func TestProvidersAuthReplaceKeepsOldKeyOnFailure(t *testing.T) {
	stateDir := t.TempDir()
	bad := newProvidersServer(t, true)
	defer bad.Close()
	if err := providers.StoreKey(stateDir, "custom", "old-key"); err != nil {
		t.Fatal(err)
	}
	m := newProvidersUI(t, stateDir, "https://example.invalid/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	if cmd := m.openProviderAuth(providers.CustomEndpointTarget(bad.URL + "/v1")); cmd != nil {
		t.Fatal("auth view returned a command")
	}
	if !m.keyModal.open || !m.keyModal.auth {
		t.Fatalf("auth-state view not opened: %+v", m.keyModal)
	}
	// Enter replaces: the key modal opens over the auth view.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.keyModal.auth || !m.keyModal.open || m.keyModal.checking {
		t.Fatalf("Enter did not open the key modal: %+v", m.keyModal)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new-key")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter with a key did not start the connection check")
	}
	msg := cmd()
	v, ok := msg.(keyCheckMsg)
	if !ok || v.err == nil {
		t.Fatalf("expected a check failure, got %+v", msg)
	}
	m.Update(msg)

	if m.keyModal.err == "" {
		t.Fatalf("modal error not shown: %+v", m.keyModal)
	}
	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "old-key" {
		t.Fatalf("failed replace clobbered the stored key: %q %v", key, err)
	}
	if m.client != prevClient || m.modelName != "start-model" {
		t.Fatalf("failed replace changed the session: client=%v model=%q", m.client, m.modelName)
	}
}

// TestProvidersKeyModalSuccess verifies the key modal for an unconfigured
// provider: an empty Enter does nothing, a typed key checks the connection,
// and a passing check stores the key and returns to the providers dialog —
// the live provider and model stay untouched.
func TestProvidersKeyModalSuccess(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	if cmd := m.openProviderAuth(providers.CustomEndpointTarget(server.URL + "/v1")); cmd != nil {
		t.Fatal("unconfigured provider returned a command instead of the key modal")
	}
	if !m.keyModal.open || m.keyModal.auth {
		t.Fatalf("key modal did not open: %+v", m.keyModal)
	}
	if !strings.Contains(m.View(), "API key for Custom endpoint") {
		t.Fatalf("view missing key modal title: %q", m.View())
	}

	// Enter with an empty input is a no-op.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.keyModal.checking || !m.keyModal.open {
		t.Fatalf("empty Enter started a check: cmd=%v modal=%+v", cmd, m.keyModal)
	}

	// Typing updates the masked input.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("test-key")})
	if len(m.keyModal.input) != len("test-key") {
		t.Fatalf("input = %q", string(m.keyModal.input))
	}
	if !strings.Contains(m.View(), strings.Repeat("•", len("test-key"))) {
		t.Fatalf("view does not mask the input: %q", m.View())
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter with a key did not start the connection check")
	}
	msg := cmd()
	v, ok := msg.(keyCheckMsg)
	if !ok || v.err != nil {
		t.Fatalf("key check result = %+v", msg)
	}
	m.Update(msg)

	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "test-key" {
		t.Fatalf("stored key = %q %v, want test-key", key, err)
	}
	if m.keyModal.open {
		t.Fatalf("key modal stayed open after success: %+v", m.keyModal)
	}
	// Direct form: the dialog opens on success so the stored row is visible.
	if !m.dialog.open || m.dialog.kind != dialogProviders {
		t.Fatalf("dialog did not open after storing: %+v", m.dialog)
	}
	if m.client != prevClient || m.modelName != "start-model" {
		t.Fatalf("storing a key switched the session: client=%v model=%q", m.client, m.modelName)
	}
	if _, ok := findEntry(m, "Stored the API key for Custom endpoint."); !ok {
		t.Fatalf("confirmation entry missing: %+v", m.entries)
	}
}

// TestProvidersKeyStoreRefreshesDialogRow verifies the dialog refresh: with
// the providers dialog open beneath a checking modal, a passing check stores
// the key, closes the modal, and the row's ✔ appears without disturbing the
// open dialog state.
func TestProvidersKeyStoreRefreshesDialogRow(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	runCommand(m, "/providers")
	if !m.dialog.open {
		t.Fatal("providers dialog did not open")
	}
	// Seed the modal into its checking state for the openai row; the check
	// result is then synthesized (the real endpoint is never dialed).
	p, ok := model.LookupProvider("openai")
	if !ok {
		t.Fatal("openai missing from the predefined list")
	}
	m.keyModal = keyState{open: true, provider: p, checking: true}
	m.handleKeyCheckMsg(keyCheckMsg{provider: p, key: "fresh-key"})

	if m.keyModal.open {
		t.Fatalf("modal stayed open after storing: %+v", m.keyModal)
	}
	if key, err := providers.StoredKey(stateDir, "openai"); err != nil || key != "fresh-key" {
		t.Fatalf("stored key = %q %v, want fresh-key", key, err)
	}
	if !m.dialog.open || m.dialog.kind != dialogProviders {
		t.Fatalf("dialog did not stay open: %+v", m.dialog)
	}
	var openAIRow bool
	for _, item := range m.dialogItems {
		if strings.HasPrefix(item, "✔ OpenAI (openai)") {
			openAIRow = true
		}
	}
	if !openAIRow {
		t.Fatalf("row did not refresh to ✔: %+v", m.dialogItems)
	}
	if m.client != prevClient || m.modelName != "start-model" {
		t.Fatalf("storing a key switched the session: client=%v model=%q", m.client, m.modelName)
	}
}

// TestProvidersKeyModalFailure verifies the failing check: the modal stays
// open showing the error, nothing is stored, the client is untouched, and the
// provider dialog is still open underneath.
func TestProvidersKeyModalFailure(t *testing.T) {
	stateDir := t.TempDir()
	bad := newProvidersServer(t, true)
	defer bad.Close()
	m := newProvidersUI(t, stateDir, bad.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Open the dialog first so the key modal stacks on top of it.
	runCommand(m, "/providers")
	if !m.dialog.open {
		t.Fatal("providers dialog did not open")
	}
	prevClient := m.client
	if cmd := m.openProviderAuth(providers.CustomEndpointTarget(bad.URL + "/v1")); cmd != nil {
		t.Fatal("unconfigured provider returned a command instead of the key modal")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("test-key")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter with a key did not start the connection check")
	}
	msg := cmd()
	v, ok := msg.(keyCheckMsg)
	if !ok || v.err == nil {
		t.Fatalf("expected a check failure, got %+v", msg)
	}
	m.Update(msg)

	if m.keyModal.err == "" {
		t.Fatalf("modal error not set: %+v", m.keyModal)
	}
	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "" {
		t.Fatalf("failed check stored a key: %q %v", key, err)
	}
	if m.client != prevClient {
		t.Fatal("failed check replaced the live client")
	}
	if !m.dialog.open {
		t.Fatal("provider dialog did not stay open beneath the modal")
	}

	// Esc leaves the failed modal with nothing stored.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.keyModal.open {
		t.Fatal("Esc did not close the key modal")
	}
	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "" {
		t.Fatalf("Esc stored a key: %q %v", key, err)
	}
}

// TestProvidersKeyModalEscKeepsDialog verifies that Esc in the key modal
// opened from the provider dialog returns to the open dialog with nothing
// changed and nothing stored.
func TestProvidersKeyModalEscKeepsDialog(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	runCommand(m, "/providers")
	if !m.dialog.open {
		t.Fatal("providers dialog did not open")
	}
	prevClient := m.client
	if cmd := m.openProviderAuth(providers.CustomEndpointTarget(server.URL + "/v1")); cmd != nil {
		t.Fatal("unconfigured provider returned a command")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("partial")})
	if !m.keyModal.open || !m.dialog.open {
		t.Fatalf("modal/dialog stack wrong: modal=%+v dialog=%+v", m.keyModal, m.dialog)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.keyModal.open {
		t.Fatal("Esc did not close the key modal")
	}
	if !m.dialog.open {
		t.Fatal("Esc closed the provider dialog underneath")
	}
	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "" {
		t.Fatalf("Esc stored a key: %q %v", key, err)
	}
	if m.client != prevClient {
		t.Fatal("Esc swapped the client")
	}
	// Esc again discards the dialog itself.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("Esc did not close the dialog afterwards")
	}
}

// TestProvidersAuthEscReturnsToDialog verifies the auth-state view's Esc:
// it closes only the overlay; the dialog beneath stays open, and from the
// direct form (no dialog) it returns to the main view.
func TestProvidersAuthEscReturnsToDialog(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	if err := providers.StoreKey(stateDir, "openai", "test-key"); err != nil {
		t.Fatal(err)
	}
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	runCommand(m, "/providers")
	// Enter on the live OpenAI row (cursor starts there) opens the auth
	// view while the dialog stays open beneath it.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatalf("dialog Enter returned a command: %v", cmd)
	}
	if !m.keyModal.open || !m.keyModal.auth || !m.dialog.open {
		t.Fatalf("auth view over dialog wrong: modal=%+v dialog=%+v", m.keyModal, m.dialog)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.keyModal.open {
		t.Fatal("Esc did not close the auth view")
	}
	if !m.dialog.open {
		t.Fatal("Esc closed the providers dialog beneath the auth view")
	}
	// Esc again discards the dialog itself.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog.open {
		t.Fatal("Esc did not close the dialog afterwards")
	}

	// Direct form: the auth view overlays the main view; Esc returns there.
	m2 := newProvidersUI(t, stateDir, server.URL+"/v1")
	m2.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	runCommand(m2, "/providers openai")
	if !m2.keyModal.open || !m2.keyModal.auth || m2.dialog.open {
		t.Fatalf("direct auth view wrong: modal=%+v dialog=%+v", m2.keyModal, m2.dialog)
	}
	m2.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m2.keyModal.open || m2.dialog.open {
		t.Fatalf("Esc did not return to the main view: modal=%+v dialog=%+v", m2.keyModal, m2.dialog)
	}
}

// TestProvidersDirectArguments covers /providers <n> and /providers <name>:
// an unconfigured provider opens the key modal, a configured one the
// auth-state view, and bad arguments restore the draft with an Error entry.
// None of these dial the network.
func TestProvidersDirectArguments(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()

	// Number on an unconfigured provider: the key modal opens.
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	runCommand(m, "/providers 1")
	if !m.keyModal.open || m.keyModal.auth || m.keyModal.provider.Name != "openai" {
		t.Fatalf("key modal not opened for /providers 1: %+v", m.keyModal)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Number on a configured provider: the auth-state view opens; nothing
	// dials the real OpenAI endpoint.
	if err := providers.StoreKey(stateDir, "openai", "k"); err != nil {
		t.Fatal(err)
	}
	runCommand(m, "/providers 1")
	if !m.keyModal.open || !m.keyModal.auth || m.keyModal.provider.Name != "openai" {
		t.Fatalf("auth view not opened for /providers 1: %+v", m.keyModal)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Name form resolves through LookupProvider: configured → auth view.
	m2 := newProvidersUI(t, stateDir, server.URL+"/v1")
	m2.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	runCommand(m2, "/providers openai")
	if !m2.keyModal.open || !m2.keyModal.auth {
		t.Fatalf("auth view not opened for /providers openai: %+v", m2.keyModal)
	}

	// Out-of-range number: draft restored, Error entry appended.
	m3 := newProvidersUI(t, stateDir, server.URL+"/v1")
	m3.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	runCommand(m3, "/providers 99")
	if string(m3.input) != "/providers 99" {
		t.Fatalf("draft = %q, want the typed line back", string(m3.input))
	}
	if e, ok := findEntry(m3, "Usage: /providers"); !ok || e.role != "Error" {
		t.Fatalf("usage error entry missing: %+v", m3.entries)
	}

	// Unknown name: draft restored, Error entry appended.
	runCommand(m3, "/providers nope")
	if string(m3.input) != "/providers nope" {
		t.Fatalf("draft = %q, want the typed line back", string(m3.input))
	}
	if e, ok := findEntry(m3, "Unknown provider nope"); !ok || e.role != "Error" {
		t.Fatalf("unknown-provider entry missing: %+v", m3.entries)
	}
}

// TestProvidersKeyArgumentStoresAfterCheck drives the flag-style trailing key
// argument against a dialable fake endpoint: a passing check stores the key
// and lands on the providers dialog; nothing switches. Failure stores nothing.
func TestProvidersKeyArgumentStoresAfterCheck(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	p := providers.CustomEndpointTarget(server.URL + "/v1")
	cmd := m.applyProviderKeyArgument(p, "flag-key")
	if cmd == nil || !m.keyModal.open || !m.keyModal.checking {
		t.Fatalf("key argument did not start the check: cmd=%v modal=%+v", cmd, m.keyModal)
	}
	msg := cmd()
	v, ok := msg.(keyCheckMsg)
	if !ok || v.err != nil {
		t.Fatalf("check result = %+v", msg)
	}
	m.Update(msg)

	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "flag-key" {
		t.Fatalf("stored key = %q %v, want flag-key", key, err)
	}
	if m.client != prevClient || m.modelName != "start-model" {
		t.Fatalf("key argument switched the session: client=%v model=%q", m.client, m.modelName)
	}
	if !m.dialog.open || m.dialog.kind != dialogProviders {
		t.Fatalf("dialog did not open after storing: %+v", m.dialog)
	}

	// A failing check stores nothing and leaves the error visible.
	bad := newProvidersServer(t, true)
	defer bad.Close()
	m2 := newProvidersUI(t, stateDir, server.URL+"/v1")
	m2.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	cmd = m2.applyProviderKeyArgument(providers.CustomEndpointTarget(bad.URL+"/v1"), "bad-key")
	msg = cmd()
	v, ok = msg.(keyCheckMsg)
	if !ok || v.err == nil {
		t.Fatalf("expected a check failure, got %+v", msg)
	}
	m2.Update(msg)
	if m2.keyModal.err == "" {
		t.Fatalf("failure error not visible: %+v", m2.keyModal)
	}
	if key, err := providers.StoredKey(stateDir, "custom"); err != nil || key != "flag-key" {
		t.Fatalf("failed argument clobbered the stored key: %q %v", key, err)
	}
}

// TestModelCommandRemoved is the regression for the removed typed /model
// command: it is rejected as an unknown command, the draft is restored, and
// the live provider is left untouched.
func TestModelCommandRemoved(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	for _, line := range []string{"/model 2", "/model gpt-4o-mini"} {
		runCommand(m, line)
		e, ok := findEntry(m, "Unknown command /model")
		if !ok || e.role != "Error" {
			t.Fatalf("%s: unknown-command entry missing: %+v", line, m.entries)
		}
		if string(m.input) != line {
			t.Fatalf("%s: draft = %q, want the typed line back", line, string(m.input))
		}
		if m.client != prevClient {
			t.Fatalf("%s: client was replaced", line)
		}
		if m.modelName != "start-model" {
			t.Fatalf("%s: model changed to %q", line, m.modelName)
		}
	}
}

// TestCustomEndpointGate pins the build-time gate: the custom endpoint target
// exists, but with customEndpointsEnabled=false nothing in the dialog or the
// direct form ever references it — no row, no auth surface, no switch.
func TestCustomEndpointGate(t *testing.T) {
	if customEndpointsEnabled {
		t.Fatal("customEndpointsEnabled should be false until the feature ships")
	}
	p := providers.CustomEndpointTarget("https://example.invalid/v1")
	if p.Name != "custom" || p.DisplayName != "Custom endpoint" {
		t.Fatalf("custom endpoint target = %+v", p)
	}
	if _, ok := model.LookupProvider(p.Name); ok {
		t.Fatal("custom endpoint must not be part of the predefined list")
	}

	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	runCommand(m, "/providers")
	if len(m.dialogItems) != len(model.Providers) {
		t.Fatalf("dialog rows = %d, want %d predefined providers", len(m.dialogItems), len(model.Providers))
	}
	for _, item := range m.dialogItems {
		if strings.Contains(item, "Custom endpoint") {
			t.Fatalf("custom endpoint surfaced in the dialog: %q", item)
		}
	}
}

// storedTestOAuth writes a signed-in OAuth login with a future expiry into
// the private state dir for a test.
func storedTestOAuth(t *testing.T, stateDir string) model.OAuthCredentials {
	t.Helper()
	creds := oauthUICredentials(t, stateDir, "issued-provider-test")
	if err := providers.StoreOAuth(stateDir, "chatgpt", creds); err != nil {
		t.Fatal(err)
	}
	return creds
}

// TestProvidersChatgptStateNotSignedIn drives the keyless path: the chatgpt
// row immediately starts browser authorization; a second Enter is unnecessary
// and the unrelated live provider stays untouched.
func TestProvidersChatgptStateNotSignedIn(t *testing.T) {
	stateDir := t.TempDir()
	m := newProvidersUI(t, stateDir, "https://example.invalid/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	if cmd := runCommand(m, "/providers chatgpt"); cmd == nil {
		t.Fatal("chatgpt auth view did not auto-launch browser authorization")
	}
	if !m.keyModal.open || !m.keyModal.auth || m.keyModal.provider.Name != "chatgpt" {
		t.Fatalf("auth view not opened for chatgpt: %+v", m.keyModal)
	}
	view := m.View()
	if !strings.Contains(view, "Opening your browser") || strings.Contains(view, "--device-login") {
		t.Fatalf("auth view missing browser progress: %q", view)
	}
	if strings.Contains(view, "Signed in") {
		t.Fatalf("unsigned-in chatgpt shows a signed-in state: %q", view)
	}
	// Enter is a no-op: no sign-in here, no key modal.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.keyModal.checking {
		t.Fatalf("Enter on unsigned-in chatgpt did something: cmd=%v modal=%+v", cmd, m.keyModal)
	}
	if m.client != prevClient || m.modelName != "start-model" {
		t.Fatalf("chatgpt auth view changed the session: client=%v model=%q", m.client, m.modelName)
	}
	m.cancelOAuthLogin()
}

// TestProvidersChatgptStateSignedIn drives the signed-in path: with a stored
// login the chatgpt auth view reports the sign-in, Enter does nothing, and
// neither config.json nor providers.json is touched.
func TestProvidersChatgptStateSignedIn(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close() // unused by the auth view; nothing may dial it
	storedTestOAuth(t, stateDir)

	cfgBefore := providers.StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini"}
	if err := providers.SaveStoredConfig(stateDir, cfgBefore); err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	providersBefore, err := os.ReadFile(providers.KeyFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	if cmd := runCommand(m, "/providers chatgpt"); cmd != nil {
		t.Fatal("chatgpt auth view returned a command")
	}
	if !m.keyModal.open || !m.keyModal.auth || !m.keyModal.signedIn {
		t.Fatalf("signed-in auth view not opened: %+v", m.keyModal)
	}
	if !strings.Contains(m.View(), "[active]") || !strings.Contains(m.View(), "user@example.test") {
		t.Fatalf("auth view missing the signed-in state: %q", m.View())
	}
	// Enter does nothing even when signed in: the modal is for keys only.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("Enter on signed-in chatgpt returned a command: %v", cmd)
	}
	if m.client != prevClient || m.modelName != "start-model" || m.conn.Provider != "OpenAI" {
		t.Fatalf("chatgpt auth view switched the session: client=%v model=%q conn=%q", m.client, m.modelName, m.conn.Provider)
	}
	configAfter, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configBefore, configAfter) {
		t.Fatalf("config.json changed: %s -> %s", configBefore, configAfter)
	}
	providersAfter, err := os.ReadFile(providers.KeyFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(providersBefore, providersAfter) {
		t.Fatal("providers.json was rewritten by the auth view")
	}
	if _, ok, err := providers.StoredOAuth(stateDir, "chatgpt"); err != nil || !ok {
		t.Fatalf("stored login lost: ok=%t err=%v", ok, err)
	}
}

// TestProvidersEnvKeySource verifies the env-var source: with no stored key
// but the provider's dedicated environment variable set, the auth view
// reports the env source and Enter opens the replace modal — nothing is
// stored by navigation alone.
func TestProvidersEnvKeySource(t *testing.T) {
	t.Setenv("LIKHA_OPENAI_API_KEY", "env-key")
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if cmd := runCommand(m, "/providers 1"); cmd != nil {
		t.Fatal("env-sourced auth view returned a command")
	}
	if !m.keyModal.open || !m.keyModal.auth {
		t.Fatalf("auth view not opened for the env-sourced provider: %+v", m.keyModal)
	}
	if !strings.Contains(m.View(), "provided via environment variable LIKHA_OPENAI_API_KEY") {
		t.Fatalf("auth view missing the env source: %q", m.View())
	}
	if key, err := providers.StoredKey(stateDir, "openai"); err != nil || key != "" {
		t.Fatalf("navigation stored the env key: %q %v", key, err)
	}
	// Enter replaces: the key modal opens (its check is never started here).
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.keyModal.auth || !m.keyModal.open || m.keyModal.checking {
		t.Fatalf("Enter did not open the replace modal: %+v", m.keyModal)
	}
}

// TestProvidersDialogOAuthRow verifies the dialog states of the chatgpt row:
// a stored login shows the signed-in check, no login shows the not-signed-in
// suffix, and the row never says "not configured".
func TestProvidersDialogOAuthRow(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	runCommand(m, "/providers")
	chatgptRow := func(items []string) string {
		for _, item := range items {
			if strings.Contains(item, "ChatGPT (Plus/Pro)") {
				return item
			}
		}
		return ""
	}
	if row := chatgptRow(m.dialogItems); row == "" || !strings.Contains(row, "browser sign-in required") || strings.Contains(row, "✔") {
		t.Fatalf("unsigned-in chatgpt row = %q", row)
	}

	storedTestOAuth(t, stateDir)
	// The open dialog swallows keystrokes as a filter query, so close it
	// before re-running /providers: two Esc presses (query is empty).
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	runCommand(m, "/providers")
	if row := chatgptRow(m.dialogItems); !strings.HasPrefix(row, "✔ ChatGPT (Plus/Pro)") || strings.Contains(row, "not signed in") {
		t.Fatalf("signed-in chatgpt row = %q", row)
	}
}
