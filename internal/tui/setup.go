package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
	likhaui "likha/internal/ui"
)

// First-run setup stages.
const (
	setupProvider = iota
	setupLogin
	setupKey
	setupChecking
	setupModel
	setupTheme
	setupPlanNotice
)

const (
	modeMain  = "main"
	modeSetup = "setup"
)

type setupState struct {
	stage          int
	cursor         int    // provider index into model.Providers
	themeCursor    int    // theme index into likhaui.ThemeNames
	filter         []rune // type-to-filter query on the provider and model lists
	keyInput       []rune
	envKey         bool                   // the checked key came from the provider's KeyEnv, not the input
	creds          model.OAuthCredentials // OAuth providers: the browser-login token set
	models         []string
	modelNames     map[string]string
	contextWindows map[string]int64
	modelCursor    int // model index into models (not into the filtered rows)
	err            string
	checking       bool
	spin           int  // spinner frame while checking or waiting for sign-in
	ticking        bool // a setupTickMsg loop is armed
}

// updateSetup drives the first-run setup stages. It runs while ui.mode is
// modeSetup and switches to the conversation view on completion.
func (m *ui) updateSetup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.cancelOAuthLogin()
		return m, tea.Quit
	case "ctrl+d":
		m.cancelOAuthLogin()
		return m, tea.Quit
	case "esc":
		if len(m.setup.filter) > 0 && (m.setup.stage == setupProvider || m.setup.stage == setupModel) {
			// Esc clears an active filter before it goes back.
			m.setFilter(nil)
			return m, nil
		}
		switch m.setup.stage {
		case setupTheme:
			// Back to the model list; the committed theme replaces the
			// previewed one.
			m.previewTheme(m.themeName)
			m.setup.stage = setupModel
			m.setup.err = ""
		case setupModel:
			if model.Providers[m.setup.cursor].Auth == model.AuthOAuth {
				m.setup.stage = setupProvider
			} else {
				m.setup.stage = setupKey
			}
			m.setup.err, m.setup.models, m.setup.modelCursor, m.setup.filter = "", nil, 0, nil
		case setupChecking:
			// A late check result is ignored once the stage moves on.
			m.setup.stage = setupKey
			m.setup.checking = false
		case setupLogin, setupPlanNotice:
			m.cancelOAuthLogin()
			m.setup.stage = setupProvider
			m.setup.err, m.setup.checking = "", false
		case setupKey:
			m.setup.stage = setupProvider
			m.setup.err, m.setup.keyInput = "", nil
		}
		return m, nil
	}
	switch m.setup.stage {
	case setupProvider:
		matches := setupProviderMatches(string(m.setup.filter))
		switch msg.String() {
		case "up":
			m.setup.cursor = stepMatch(matches, m.setup.cursor, -1)
		case "down":
			m.setup.cursor = stepMatch(matches, m.setup.cursor, 1)
		case "pgup":
			m.setup.cursor = stepMatch(matches, m.setup.cursor, -m.setupWindow())
		case "pgdown":
			m.setup.cursor = stepMatch(matches, m.setup.cursor, m.setupWindow())
		case "home":
			m.setup.cursor = stepMatch(matches, m.setup.cursor, -len(matches))
		case "end":
			m.setup.cursor = stepMatch(matches, m.setup.cursor, len(matches))
		case "enter":
			if indexOf(matches, m.setup.cursor) < 0 {
				return m, nil
			}
			m.setup.contextWindows = nil
			m.setup.filter = nil
			if model.Providers[m.setup.cursor].Auth == model.AuthOAuth {
				m.setup.stage = setupLogin
				m.setup.err, m.setup.checking = "", false
				return m, m.startOAuthLogin(model.Providers[m.setup.cursor])
			} else {
				m.setup.stage = setupKey
				m.setup.keyInput = nil
				m.setup.err = ""
			}
		default:
			m.editFilter(msg)
		}
	case setupLogin:
		switch msg.String() {
		case "enter":
			if !m.setup.checking {
				return m, m.startOAuthLogin(model.Providers[m.setup.cursor])
			}
		}
	case setupPlanNotice:
		if msg.String() == "enter" {
			if err := acknowledgeChatGPTPlan(m.stateDir, m.setup.creds.ClientID); err != nil {
				m.setup.err = "Store plan acknowledgement: " + err.Error()
				return m, nil
			}
			m.setup.err = ""
			return m, m.continueOAuthSetup()
		}
	case setupKey:
		switch msg.Type {
		case tea.KeyBackspace:
			if len(m.setup.keyInput) > 0 {
				m.setup.keyInput = m.setup.keyInput[:len(m.setup.keyInput)-1]
			}
			m.setup.err = ""
		case tea.KeyCtrlU:
			m.setup.keyInput = nil
			m.setup.err = ""
		case tea.KeyRunes, tea.KeySpace:
			r := msg.Runes
			if msg.Type == tea.KeySpace {
				r = []rune{' '}
			}
			m.setup.keyInput = append(m.setup.keyInput, r...)
			m.setup.err = ""
			m.caretNote()
		case tea.KeyEnter:
			p := model.Providers[m.setup.cursor]
			if key, fromEnv := m.setupAPIKey(); key != "" {
				return m, m.startSetupCheck(p, key, fromEnv)
			}
		}
	case setupChecking:
		// Waiting for the connection check; only quit and esc are handled above.
	case setupTheme:
		names := likhaui.ThemeNames()
		switch msg.String() {
		case "up":
			if m.setup.themeCursor > 0 {
				m.setup.themeCursor--
			}
			m.previewSetupTheme()
		case "down":
			if m.setup.themeCursor < len(names)-1 {
				m.setup.themeCursor++
			}
			m.previewSetupTheme()
		case "home":
			m.setup.themeCursor = 0
			m.previewSetupTheme()
		case "end":
			m.setup.themeCursor = len(names) - 1
			m.previewSetupTheme()
		case "enter":
			return m, m.applySetupTheme()
		}
	case setupModel:
		matches := m.setupModelMatches()
		switch msg.String() {
		case "up":
			m.setup.modelCursor = stepMatch(matches, m.setup.modelCursor, -1)
		case "down":
			m.setup.modelCursor = stepMatch(matches, m.setup.modelCursor, 1)
		case "pgup":
			m.setup.modelCursor = stepMatch(matches, m.setup.modelCursor, -m.setupWindow())
		case "pgdown":
			m.setup.modelCursor = stepMatch(matches, m.setup.modelCursor, m.setupWindow())
		case "home":
			m.setup.modelCursor = stepMatch(matches, m.setup.modelCursor, -len(matches))
		case "end":
			m.setup.modelCursor = stepMatch(matches, m.setup.modelCursor, len(matches))
		case "enter":
			if indexOf(matches, m.setup.modelCursor) >= 0 {
				return m, m.finishSetup(m.setup.models[m.setup.modelCursor])
			}
		default:
			m.editFilter(msg)
		}
	}
	return m, nil
}

// editFilter applies typing and backspace to the list filter, then keeps the
// cursor on a visible row.
func (m *ui) editFilter(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyBackspace:
		if len(m.setup.filter) > 0 {
			m.setFilter(m.setup.filter[:len(m.setup.filter)-1])
		}
	case tea.KeyCtrlU:
		m.setFilter(nil)
	case tea.KeyRunes, tea.KeySpace:
		r := msg.Runes
		if msg.Type == tea.KeySpace {
			r = []rune{' '}
		}
		m.setFilter(append(m.setup.filter, r...))
	}
}

// setFilter replaces the filter and moves the cursor to the first match when
// the current row is filtered out.
func (m *ui) setFilter(filter []rune) {
	m.setup.filter = filter
	switch m.setup.stage {
	case setupProvider:
		if matches := setupProviderMatches(string(filter)); len(matches) > 0 && indexOf(matches, m.setup.cursor) < 0 {
			m.setup.cursor = matches[0]
		}
	case setupModel:
		if matches := m.setupModelMatches(); len(matches) > 0 && indexOf(matches, m.setup.modelCursor) < 0 {
			m.setup.modelCursor = matches[0]
		}
	}
}

// setupProviderMatches lists the provider indexes whose display name,
// canonical name, or host contains the query (case-insensitive).
func setupProviderMatches(query string) []int {
	query = strings.ToLower(strings.TrimSpace(query))
	matches := make([]int, 0, len(model.Providers))
	for i, p := range model.Providers {
		hay := strings.ToLower(p.DisplayName + " " + p.Name + " " + providerHost(p.BaseURL))
		if query == "" || strings.Contains(hay, query) {
			matches = append(matches, i)
		}
	}
	return matches
}

// setupModelMatches lists the model indexes whose ID contains the query.
func (m *ui) setupModelMatches() []int {
	query := strings.ToLower(strings.TrimSpace(string(m.setup.filter)))
	matches := make([]int, 0, len(m.setup.models))
	for i, id := range m.setup.models {
		if query == "" || strings.Contains(strings.ToLower(id+" "+m.setup.modelNames[id]), query) {
			matches = append(matches, i)
		}
	}
	return matches
}

// stepMatch moves cursor by delta rows within matches, clamped to the ends.
// A cursor outside matches lands on the first match.
func stepMatch(matches []int, cursor, delta int) int {
	if len(matches) == 0 {
		return cursor
	}
	pos := -1
	for i, idx := range matches {
		if idx == cursor {
			pos = i
			break
		}
	}
	if pos < 0 {
		return matches[0]
	}
	return matches[min(len(matches)-1, max(0, pos+delta))]
}

// providerHost returns the host of a base URL for compact display.
func providerHost(baseURL string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return host
}

// setupAPIKey resolves the key the key stage checks: the typed key, else the
// provider's KeyEnv value. fromEnv keys are never written to providers.json;
// later runs read them from the environment like any configured provider.
func (m *ui) setupAPIKey() (key string, fromEnv bool) {
	if key := strings.TrimSpace(string(m.setup.keyInput)); key != "" {
		return key, false
	}
	if env := model.Providers[m.setup.cursor].KeyEnv; env != "" {
		if key := strings.TrimSpace(os.Getenv(env)); key != "" {
			return key, true
		}
	}
	return "", false
}

// setupDefaultModel returns the index of the provider's default model in
// models, or 0 when the provider names none or the list lacks it.
func setupDefaultModel(p model.Provider, models []string) int {
	for i, id := range models {
		if p.DefaultModel != "" && id == p.DefaultModel {
			return i
		}
	}
	return 0
}

// setupEnvKeySet reports whether provider p's KeyEnv holds a key.
func setupEnvKeySet(p model.Provider) bool {
	return p.KeyEnv != "" && strings.TrimSpace(os.Getenv(p.KeyEnv)) != ""
}

// setupTickMsg advances the setup spinner while a check or sign-in runs.
type setupTickMsg struct{}

func setupTick() tea.Cmd {
	return tea.Tick(activityTickInterval, func(time.Time) tea.Msg { return setupTickMsg{} })
}

// armSetupTick starts the spinner loop unless one is already running.
func (m *ui) armSetupTick() tea.Cmd {
	if m.setup.ticking {
		return nil
	}
	m.setup.ticking = true
	return setupTick()
}

// handleSetupTick advances the spinner and re-arms while something is in
// flight; the loop ends on the first tick after the wait is over.
func (m *ui) handleSetupTick() tea.Cmd {
	if m.mode != modeSetup || !m.setup.checking {
		m.setup.ticking = false
		return nil
	}
	m.setup.spin++
	return setupTick()
}

// setupWindow is the number of list rows the current stage shows; PgUp/PgDn
// move by one window.
func (m *ui) setupWindow() int {
	return max(1, m.setupListRows())
}

// startSetupCheck probes the provider's model list with the entered key.
func (m *ui) startSetupCheck(p model.Provider, key string, fromEnv bool) tea.Cmd {
	m.setup.stage = setupChecking
	m.setup.err, m.setup.checking = "", true
	m.setup.envKey = fromEnv
	m.setup.models = nil
	m.setup.contextWindows = nil
	observe := m.conn.ModelMetadataObserver
	return tea.Batch(m.armSetupTick(), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start := time.Now()
		details, err := model.ListModelsWithDetails(ctx, p.BaseURL, key)
		if observe != nil {
			observe(p.Name, "first-run setup /models", time.Since(start), details, err)
		}
		ids := make([]string, 0, len(details))
		windows := make(map[string]int64)
		for _, detail := range details {
			ids = append(ids, detail.ID)
			if detail.ContextWindow > 0 {
				windows[detail.ID] = detail.ContextWindow
			}
		}
		return setupCheckMsg{provider: p, key: key, models: ids, contextWindows: windows, err: err}
	})
}

type setupCheckMsg struct {
	provider       model.Provider
	key            string
	models         []string
	contextWindows map[string]int64
	err            error
}

// openBrowser opens url in the default browser; tests replace it.
var openBrowser = func(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}

// startOAuthLogin is returned by the first provider selection (or Init for
// explicit ChatGPT startup), never by a second browser-launch screen.
func (m *ui) startOAuthLogin(p model.Provider) tea.Cmd {
	registration, err := selectedOAuthRegistration(m.stateDir, p.Name)
	if err != nil {
		m.setup.err = "Read ChatGPT accounts: " + err.Error()
		return nil
	}
	return m.startOAuthFlow(p, registration, oauthSetup, false)
}

// finishSetup builds the client from the chosen provider/model, stores the
// credential (API key or OAuth login) and choice, and moves to the theme
// stage of setup.
func (m *ui) finishSetup(modelID string) tea.Cmd {
	p := model.Providers[m.setup.cursor]
	var client *model.Client
	var err error
	if p.Auth == model.AuthOAuth {
		creds, signedIn, loadErr := providers.StoredOAuth(m.stateDir, p.Name)
		if loadErr != nil {
			m.setup.err = loadErr.Error()
			return nil
		}
		if !signedIn || creds.ClientID != m.setup.creds.ClientID {
			m.setup.err = "ChatGPT sign-in changed or was signed out. Reconnect before continuing."
			return nil
		}
		client, err = model.NewOAuth(p.BaseURL, modelID, creds.Issuer, creds.ClientID, creds)
	} else {
		key, _ := m.setupAPIKey()
		client, err = model.New(p.BaseURL, modelID, key)
	}
	if err != nil {
		m.setup.err = err.Error()
		return nil
	}
	if p.Auth == model.AuthOAuth {
		bindOAuthStorage(client, m.stateDir, p.Name)
	} else if key, fromEnv := m.setupAPIKey(); key != "" && !fromEnv {
		// A key from the environment stays there; only a typed key is stored.
		if err := providers.StoreKey(m.stateDir, p.Name, key); err != nil {
			m.setup.err = err.Error()
			return nil
		}
	}
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err != nil {
		m.setup.err = err.Error()
		return nil
	}
	cfg.Provider, cfg.Model, cfg.Theme = p.Name, modelID, m.themeName
	if err := providers.SaveStoredConfig(m.stateDir, cfg); err != nil {
		m.setup.err = err.Error()
		return nil
	}
	m.client = client
	if p.SessionHeader != "" {
		// Providers such as OpenCode route per conversation; the snapshot was
		// created before the TUI started.
		client.SetSessionHeader(p.SessionHeader)
		client.SetSession(m.snapshot.ID)
	}
	m.modelName = modelID
	// Change only the connection identity; keep MCP and unrelated preferences.
	m.conn.Provider, m.conn.ProviderCanonical, m.conn.Verified = p.DisplayName, p.Name, true
	m.conn.Err, m.conn.Setup = nil, false
	m.conn.Theme, m.conn.ComposerStyle = m.themeName, m.composerStyle
	m.conn.ContextWindows = m.setup.contextWindows
	m.resolveContextWindow()
	m.setup.stage = setupTheme
	m.setup.filter = nil
	m.setup.themeCursor = 0
	for i, name := range likhaui.ThemeNames() {
		if name == m.themeName {
			m.setup.themeCursor = i
		}
	}
	return nil
}

// previewSetupTheme re-resolves the live styles to the highlighted setup
// candidate without committing; Enter stores via applySetupTheme.
func (m *ui) previewSetupTheme() {
	names := likhaui.ThemeNames()
	if m.setup.themeCursor < len(names) {
		m.previewTheme(names[m.setup.themeCursor])
	}
}

// applySetupTheme resolves the highlighted theme, stores it with the setup
// choice, and completes first-run setup into the conversation view.
func (m *ui) applySetupTheme() tea.Cmd {
	names := likhaui.ThemeNames()
	if m.setup.themeCursor >= len(names) {
		return nil
	}
	m.themeName = names[m.setup.themeCursor]
	m.theme = likhaui.Resolve(m.themeName, likhaui.HasDarkBackground())
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err == nil && cfg.Provider != "" {
		cfg.Theme = m.themeName
		if err := providers.SaveStoredConfig(m.stateDir, cfg); err != nil {
			m.setup.err = err.Error()
			return nil
		}
	}
	m.mode = modeMain
	m.status = "Connected"
	m.entries = append(m.entries, entry{role: "Likha", content: "Connected to " + m.conn.Provider + " · " + m.modelName + ". Change these later with /providers, /models, and /themes."})
	m.layoutWidth = 0
	return nil
}
