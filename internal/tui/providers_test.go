package tui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
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

// TestProvidersSwitchActivatesSession drives the success path: an
// already-configured provider verifies against its endpoint and then swaps
// the live client, model, and status — without touching config.json or
// providers.json.
func TestProvidersSwitchActivatesSession(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	if err := providers.StoreKey(stateDir, "custom", "test-key"); err != nil {
		t.Fatal(err)
	}
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

	cmd := m.applyProviderDirect(providers.CustomEndpointTarget(server.URL + "/v1"))
	if cmd == nil {
		t.Fatal("configured provider did not start a switch")
	}
	if m.status != "Checking Custom endpoint" {
		t.Fatalf("status = %q, want the checking state", m.status)
	}
	msg := cmd()
	v, ok := msg.(providerSwitchMsg)
	if !ok || v.err != nil {
		t.Fatalf("switch result = %+v", msg)
	}
	m.Update(msg)

	if m.modelName != "alpha" {
		t.Fatalf("model = %q, want the first reported id alpha", m.modelName)
	}
	if m.client == nil {
		t.Fatal("client was not swapped in")
	}
	if m.status != "Connected" {
		t.Fatalf("status = %q, want Connected", m.status)
	}
	if m.conn.Provider != "Custom endpoint" {
		t.Fatalf("connection provider = %q, want Custom endpoint", m.conn.Provider)
	}
	if _, ok := findEntry(m, "Provider switched to Custom endpoint for this session."); !ok {
		t.Fatalf("confirmation entry missing: %+v", m.entries)
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
		t.Fatal("providers.json was rewritten by a session switch")
	}
	if cfg, err := providers.LoadStoredConfig(stateDir); err != nil || cfg != cfgBefore {
		t.Fatalf("stored config changed: %+v %v", cfg, err)
	}
}

// TestProvidersSwitchVerificationFailure verifies the failure path: a failed
// connection check leaves the previous provider active and appends an Error
// entry describing the failed switch.
func TestProvidersSwitchVerificationFailure(t *testing.T) {
	stateDir := t.TempDir()
	good := newProvidersServer(t, false)
	defer good.Close()
	bad := newProvidersServer(t, true)
	defer bad.Close()
	if err := providers.StoreKey(stateDir, "custom", "test-key"); err != nil {
		t.Fatal(err)
	}
	m := newProvidersUI(t, stateDir, good.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	cmd := m.applyProviderDirect(providers.CustomEndpointTarget(bad.URL + "/v1"))
	if cmd == nil {
		t.Fatal("configured provider did not start a switch")
	}
	msg := cmd()
	v, ok := msg.(providerSwitchMsg)
	if !ok || v.err == nil {
		t.Fatalf("expected a verification failure, got %+v", msg)
	}
	m.Update(msg)

	if m.client != prevClient {
		t.Fatal("failed switch replaced the live client")
	}
	if m.modelName != "start-model" {
		t.Fatalf("model changed to %q on failure", m.modelName)
	}
	if !strings.Contains(m.status, "Error") {
		t.Fatalf("status = %q, want Error", m.status)
	}
	e, ok := findEntry(m, "Switch to Custom endpoint failed")
	if !ok || e.role != "Error" {
		t.Fatalf("failure entry missing: %+v", m.entries)
	}
}

// TestProvidersKeyModalSuccess verifies the key modal for an unconfigured
// provider: an empty Enter does nothing, a typed key checks the connection,
// and the key persists only once the check passes, after which the provider
// activates.
func TestProvidersKeyModalSuccess(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if cmd := m.applyProviderDirect(providers.CustomEndpointTarget(server.URL + "/v1")); cmd != nil {
		t.Fatal("unconfigured provider returned a switch command instead of the key modal")
	}
	if !m.keyModal.open {
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
	if m.client == nil || m.modelName != "alpha" || m.status != "Connected" {
		t.Fatalf("provider not activated: model=%q status=%q", m.modelName, m.status)
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
	if cmd := m.applyProviderDirect(providers.CustomEndpointTarget(bad.URL + "/v1")); cmd != nil {
		t.Fatal("unconfigured provider returned a switch command instead of the key modal")
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
	if cmd := m.applyProviderDirect(providers.CustomEndpointTarget(server.URL + "/v1")); cmd != nil {
		t.Fatal("unconfigured provider returned a switch command")
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

// TestProvidersDirectArguments covers /providers <n> and /providers <name>:
// a configured provider starts the async switch, an unconfigured one opens
// the key modal, and bad arguments restore the draft with an Error entry.
func TestProvidersDirectArguments(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close()

	// Number on an unconfigured provider: the key modal opens.
	m := newProvidersUI(t, stateDir, server.URL+"/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	runCommand(m, "/providers 1")
	if !m.keyModal.open || m.keyModal.provider.Name != "openai" {
		t.Fatalf("key modal not opened for /providers 1: %+v", m.keyModal)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Number on a configured provider: the switch command starts. The
	// command verifies against the endpoint asynchronously; it is never
	// invoked here (the real OpenAI endpoint must not be contacted).
	if err := providers.StoreKey(stateDir, "openai", "k"); err != nil {
		t.Fatal(err)
	}
	cmd := runCommand(m, "/providers 1")
	if cmd == nil {
		t.Fatal("/providers 1 on a configured provider did not start a switch")
	}
	if m.keyModal.open {
		t.Fatalf("key modal open for a configured provider: %+v", m.keyModal)
	}
	if !strings.HasPrefix(m.status, "Checking OpenAI") {
		t.Fatalf("status = %q, want the checking state", m.status)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // esc during checking cancels nothing here; safe no-op

	// Name form resolves through LookupProvider; configured → switch path.
	m2 := newProvidersUI(t, stateDir, server.URL+"/v1")
	m2.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd := runCommand(m2, "/providers openai"); cmd == nil {
		t.Fatal("/providers openai did not start a switch")
	}
	if m2.keyModal.open {
		t.Fatalf("key modal open for a configured provider: %+v", m2.keyModal)
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

// TestCustomEndpointGate pins the build-time gate: the custom endpoint
// target exists and flows through the switch machinery, but the dialog only
// ever lists the predefined providers.
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
	creds := model.OAuthCredentials{
		Refresh:   "refresh-token",
		Access:    "access-token",
		Expires:   time.Now().Add(time.Hour).UnixMilli(),
		AccountID: "acct_test",
	}
	if err := providers.StoreOAuth(stateDir, "chatgpt", creds); err != nil {
		t.Fatal(err)
	}
	return creds
}

// TestProvidersChatgptNoStoredLogin drives the keyless path: /providers
// chatgpt without a stored login appends the sign-in guidance as an Error
// entry, never opens the API-key modal (the switcher cannot sign in), and
// leaves the live client untouched.
func TestProvidersChatgptNoStoredLogin(t *testing.T) {
	stateDir := t.TempDir()
	m := newProvidersUI(t, stateDir, "https://example.invalid/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	if cmd := runCommand(m, "/providers chatgpt"); cmd != nil {
		t.Fatal("no-stored-login chatgpt returned a command instead of an error entry")
	}
	if m.keyModal.open {
		t.Fatal("API-key modal opened for an OAuth provider")
	}
	e, ok := findEntry(m, "ChatGPT (Plus/Pro) has no stored sign-in; sign in during first-run setup or run lisa --provider chatgpt --device-login")
	if !ok || e.role != "Error" {
		t.Fatalf("sign-in guidance entry missing: %+v", m.entries)
	}
	if m.client != prevClient || m.modelName != "start-model" {
		t.Fatalf("no-login path changed the session: client=%v model=%q", m.client, m.modelName)
	}
}

// TestProvidersChatgptSwitchActivated drives the OAuth success path: with a
// stored login the switch verifies the login (offline while the token is
// unexpired), swaps in an OAuth client on the documented default model, and
// touches neither config.json nor providers.json.
func TestProvidersChatgptSwitchActivated(t *testing.T) {
	stateDir := t.TempDir()
	server := newProvidersServer(t, false)
	defer server.Close() // unused by the OAuth path; nothing may dial it
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

	cmd := runCommand(m, "/providers chatgpt")
	if cmd == nil {
		t.Fatal("signed-in chatgpt did not start a switch")
	}
	if m.keyModal.open {
		t.Fatal("API-key modal opened for an OAuth provider")
	}
	if !strings.HasPrefix(m.status, "Checking ChatGPT (Plus/Pro)") {
		t.Fatalf("status = %q, want the checking state", m.status)
	}
	msg := cmd()
	v, ok := msg.(providerSwitchMsg)
	if !ok || v.err != nil {
		t.Fatalf("switch result = %+v", msg)
	}
	m.Update(msg)

	if m.conn.Provider != "ChatGPT (Plus/Pro)" {
		t.Fatalf("connection provider = %q, want ChatGPT (Plus/Pro)", m.conn.Provider)
	}
	if m.modelName != "gpt-5.5" {
		t.Fatalf("model = %q, want the provider default gpt-5.5", m.modelName)
	}
	if m.client == nil {
		t.Fatal("client was not swapped in")
	}
	// OAuth clients hold no static key and Codex responses report no
	// x-codex-primary-* headers yet, so both stay empty.
	if m.client.APIKey() != "" || m.client.Usage() != "" {
		t.Fatalf("activated client is not an OAuth client: key=%q usage=%q", m.client.APIKey(), m.client.Usage())
	}
	if m.status != "Connected" {
		t.Fatalf("status = %q, want Connected", m.status)
	}
	if _, ok := findEntry(m, "Provider switched to ChatGPT (Plus/Pro) for this session."); !ok {
		t.Fatalf("confirmation entry missing: %+v", m.entries)
	}
	// Re-checking the activated client must succeed offline: the stored
	// token is unexpired, so no refresh and no network round trip happen.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.client.Check(ctx); err != nil {
		t.Fatalf("activated OAuth client failed an offline check: %v", err)
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
		t.Fatal("providers.json was rewritten by a session switch")
	}
	if cfg, err := providers.LoadStoredConfig(stateDir); err != nil || cfg != cfgBefore {
		t.Fatalf("stored config changed: %+v %v", cfg, err)
	}
	if _, ok, err := providers.StoredOAuth(stateDir, "chatgpt"); err != nil || !ok {
		t.Fatalf("stored login lost during the switch: ok=%t err=%v", ok, err)
	}
}

// TestProvidersChatgptSwitchCheckFailure verifies the OAuth failure path: an
// expired login forces a refresh attempt, which the unreachable endpoint
// rejects, leaving the previous provider active with an Error entry.
func TestProvidersChatgptSwitchCheckFailure(t *testing.T) {
	stateDir := t.TempDir()
	creds := model.OAuthCredentials{
		Refresh: "refresh-token",
		Access:  "access-token",
		Expires: time.Now().Add(-time.Hour).UnixMilli(),
	}
	if err := providers.StoreOAuth(stateDir, "chatgpt", creds); err != nil {
		t.Fatal(err)
	}
	m := newProvidersUI(t, stateDir, "https://example.invalid/v1")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	prevClient := m.client

	cmd := runCommand(m, "/providers chatgpt")
	if cmd == nil {
		t.Fatal("signed-in chatgpt did not start a switch")
	}
	msg := cmd()
	v, ok := msg.(providerSwitchMsg)
	if !ok || v.err == nil {
		t.Fatalf("expected a verification failure for the stale login, got %+v", msg)
	}
	m.Update(msg)

	if m.client != prevClient {
		t.Fatal("failed OAuth switch replaced the live client")
	}
	if m.modelName != "start-model" {
		t.Fatalf("model changed to %q on failure", m.modelName)
	}
	e, ok := findEntry(m, "Switch to ChatGPT (Plus/Pro) failed")
	if !ok || e.role != "Error" {
		t.Fatalf("failure entry missing: %+v", m.entries)
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
	if row := chatgptRow(m.dialogItems); row == "" || !strings.Contains(row, "not signed in (first-run setup or --device-login)") || strings.Contains(row, "✔") {
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
