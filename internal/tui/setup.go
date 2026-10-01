package tui

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/model"
	"lisa/internal/providers"
	lisaui "lisa/internal/ui"
)

// First-run setup stages.
const (
	setupProvider = iota
	setupLogin
	setupKey
	setupChecking
	setupModel
	setupTheme
)

const (
	modeMain  = "main"
	modeSetup = "setup"
)

type setupState struct {
	stage       int
	cursor      int
	keyInput    []rune
	creds       model.OAuthCredentials // OAuth providers: the browser-login token set
	models      []string
	modelCursor int
	err         string
	checking    bool
}

// updateSetup drives the first-run setup stages. It runs while ui.mode is
// modeSetup and switches to the conversation view on completion.
func (m *ui) updateSetup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+d":
		return m, tea.Quit
	case "esc":
		switch m.setup.stage {
		case setupTheme:
			m.setup.stage = setupModel
			m.setup.err = ""
		case setupModel:
			if model.Providers[m.setup.cursor].Auth == model.AuthOAuth {
				// OAuth providers skip the key stage; back goes to the login.
				m.setup.stage = setupLogin
			} else {
				m.setup.stage = setupKey
			}
			m.setup.err, m.setup.models, m.setup.modelCursor = "", nil, 0
		case setupChecking:
			// A late check result is ignored once the stage moves on.
			m.setup.stage = setupKey
			m.setup.checking = false
		case setupLogin:
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
		switch msg.String() {
		case "up":
			if m.setup.cursor > 0 {
				m.setup.cursor--
			}
		case "down":
			if m.setup.cursor < len(model.Providers)-1 {
				m.setup.cursor++
			}
		case "enter":
			if model.Providers[m.setup.cursor].Auth == model.AuthOAuth {
				m.setup.stage = setupLogin
				m.setup.err, m.setup.checking = "", false
			} else {
				m.setup.stage = setupKey
				m.setup.keyInput = nil
			}
		}
	case setupLogin:
		switch msg.String() {
		case "enter":
			if !m.setup.checking {
				return m, m.startOAuthLogin(model.Providers[m.setup.cursor])
			}
		}
	case setupKey:
		switch msg.Type {
		case tea.KeyBackspace:
			if len(m.setup.keyInput) > 0 {
				m.setup.keyInput = m.setup.keyInput[:len(m.setup.keyInput)-1]
			}
		case tea.KeyRunes, tea.KeySpace:
			r := msg.Runes
			if msg.Type == tea.KeySpace {
				r = []rune{' '}
			}
			m.setup.keyInput = append(m.setup.keyInput, r...)
		case tea.KeyEnter:
			if key := strings.TrimSpace(string(m.setup.keyInput)); key != "" {
				p := model.Providers[m.setup.cursor]
				return m, m.startSetupCheck(p, key)
			}
		}
	case setupChecking:
		// Waiting for the connection check; only quit and esc are handled above.
	case setupTheme:
		switch msg.String() {
		case "up":
			if m.setup.cursor > 0 {
				m.setup.cursor--
			}
			m.previewSetupTheme()
		case "down":
			if m.setup.cursor < len(lisaui.ThemeNames())-1 {
				m.setup.cursor++
			}
			m.previewSetupTheme()
		case "enter":
			return m, m.applySetupTheme()
		}
	case setupModel:
		switch msg.String() {
		case "up":
			if m.setup.modelCursor > 0 {
				m.setup.modelCursor--
			}
		case "down":
			if m.setup.modelCursor < len(m.setup.models)-1 {
				m.setup.modelCursor++
			}
		case "pgup":
			m.setup.modelCursor = max(0, m.setup.modelCursor-m.setupWindow())
		case "pgdown":
			m.setup.modelCursor = min(len(m.setup.models)-1, m.setup.modelCursor+m.setupWindow())
		case "enter":
			if m.setup.modelCursor < len(m.setup.models) {
				return m, m.finishSetup(m.setup.models[m.setup.modelCursor])
			}
		}
	}
	return m, nil
}

func (m *ui) setupWindow() int {
	return max(1, m.height-6-3) // setupView body minus title, blank, and hint.
}

// startSetupCheck probes the provider's model list with the entered key.
func (m *ui) startSetupCheck(p model.Provider, key string) tea.Cmd {
	m.setup.stage = setupChecking
	m.setup.err, m.setup.checking = "", true
	m.setup.models = nil
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ids, err := model.ListModels(ctx, p.BaseURL, key)
		return setupCheckMsg{provider: p, key: key, models: ids, err: err}
	}
}

type setupCheckMsg struct {
	provider model.Provider
	key      string
	models   []string
	err      error
}

// openBrowser opens url in the default browser; tests replace it.
var openBrowser = func(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// startOAuthLogin opens the browser for the ChatGPT account login and waits
// for the loopback callback. The stage stays on setupLogin with checking set
// while the browser flow runs; the five-minute context bounds the wait.
func (m *ui) startOAuthLogin(p model.Provider) tea.Cmd {
	m.setup.checking = true
	m.setup.err = ""
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ts, err := model.BrowserLogin(ctx, model.ChatGPTIssuer, model.ChatGPTClientID, model.ChatGPTCallbackPort, openBrowser, nil)
		return oauthLoginMsg{creds: ts.Credentials(), err: err}
	}
}

type oauthLoginMsg struct {
	creds model.OAuthCredentials
	err   error
}

// finishSetup builds the client from the chosen provider/model, stores the
// credential (API key or OAuth login) and choice, and moves to the theme
// stage of setup.
func (m *ui) finishSetup(modelID string) tea.Cmd {
	p := model.Providers[m.setup.cursor]
	var client *model.Client
	var err error
	if p.Auth == model.AuthOAuth {
		client, err = model.NewOAuth(p.BaseURL, modelID, model.ChatGPTIssuer, model.ChatGPTClientID, m.setup.creds)
	} else {
		client, err = model.New(p.BaseURL, modelID, strings.TrimSpace(string(m.setup.keyInput)))
	}
	if err != nil {
		m.setup.err = err.Error()
		return nil
	}
	if p.Auth == model.AuthOAuth {
		// Store the fresh login immediately; access tokens then expire and
		// the client refreshes them through this saver.
		if err := providers.StoreOAuth(m.stateDir, p.Name, m.setup.creds); err != nil {
			m.setup.err = err.Error()
			return nil
		}
		client.SetOAuthSaver(func(c model.OAuthCredentials) error {
			return providers.StoreOAuth(m.stateDir, p.Name, c)
		})
	} else if key := strings.TrimSpace(string(m.setup.keyInput)); key != "" {
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
	m.conn = providers.Connection{Provider: p.DisplayName, ProviderCanonical: p.Name, Verified: true, Theme: m.themeName, Nerd: m.conn.Nerd, ComposerStyle: m.composerStyle}
	m.setup.stage = setupTheme
	m.setup.cursor = 0
	return nil
}

// previewSetupTheme re-resolves the live styles to the highlighted setup
// candidate without committing; Enter stores via applySetupTheme.
func (m *ui) previewSetupTheme() {
	names := lisaui.ThemeNames()
	if m.setup.cursor < len(names) {
		m.previewTheme(names[m.setup.cursor])
	}
}

// applySetupTheme resolves the highlighted theme, stores it with the setup
// choice, and completes first-run setup into the conversation view.
func (m *ui) applySetupTheme() tea.Cmd {
	names := lisaui.ThemeNames()
	if m.setup.cursor >= len(names) {
		return nil
	}
	m.themeName = names[m.setup.cursor]
	m.theme = lisaui.Resolve(m.themeName, lisaui.HasDarkBackground())
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
	m.layoutWidth = 0
	return nil
}

func (m *ui) setupView() string {
	body := max(1, m.height-6) // compact header, blank separator, three footer rows.
	rows := make([]string, 0, m.height)
	rows = append(rows, m.theme.Title.Render(fit("Lisa  |  First-run setup  |  Repo: "+m.root, m.width)))
	rows = append(rows, fit("", m.width))
	content := m.setupLines(body)
	for _, line := range content {
		rows = append(rows, fit(line, m.width))
	}
	rows = append(rows, strings.Repeat("─", m.width))
	status := "SETUP"
	if m.setup.checking {
		if m.setup.stage == setupLogin {
			status = "SETUP  |  Waiting for browser sign-in…"
		} else {
			status = "SETUP  |  Checking provider…"
		}
	} else if m.setup.err != "" {
		if m.setup.stage == setupLogin {
			status = "SETUP  |  Sign-in failed"
		} else {
			status = "SETUP  |  Check failed"
		}
	}
	rows = append(rows, m.theme.Help.Render(fit(status, m.width)))
	rows = append(rows, fit(m.setupActions(), m.width))
	rows = append(rows, fit("Esc back  Ctrl+C quit", m.width))
	return m.frame(rows)
}

// setupLines builds the stage body, windowing long model lists to the visible
// body height with the cursor kept on screen.
func (m *ui) setupLines(body int) []string {
	var lines []string
	switch m.setup.stage {
	case setupProvider:
		lines = append(lines, "Choose a model provider (BYOK; Lisa hosts no models):", "")
		windowed, start := windowList(len(model.Providers), m.setup.cursor, body-len(lines))
		for i := start; i < start+windowed; i++ {
			p := model.Providers[i]
			marker, indent := "  ", "  "
			if i == m.setup.cursor {
				marker, indent = "> ", "     "
			}
			lines = append(lines, marker+p.DisplayName)
			lines = append(lines, indent+p.BaseURL)
		}
	case setupKey:
		lines = append(lines, "Provider: "+model.Providers[m.setup.cursor].DisplayName, "")
		masked := strings.Repeat("•", len(m.setup.keyInput))
		if len(m.setup.keyInput) == 0 {
			masked = "(paste your API key, then Enter)"
		}
		lines = append(lines, "API key: "+masked)
		lines = append(lines, "")
		lines = append(lines, "The key is stored only in the private state directory.")
	case setupChecking:
		lines = append(lines, "Provider: "+model.Providers[m.setup.cursor].DisplayName, "")
		lines = append(lines, "Checking connection and model list…")
	case setupLogin:
		lines = append(lines, "Provider: "+model.Providers[m.setup.cursor].DisplayName, "")
		lines = append(lines, "Lisa opens your browser to sign in with your ChatGPT account.")
		lines = append(lines, "Your browser shows a code page; approve access to continue.")
		lines = append(lines, "")
		lines = append(lines, "Press Enter to open the browser")
	case setupTheme:
		names := lisaui.ThemeNames()
		lines = append(lines, "Choose a color theme (applies immediately, stored for next runs):", "")
		windowed, start := windowList(len(names), m.setup.cursor, body-len(lines))
		for i := start; i < start+windowed; i++ {
			marker := "  "
			if i == m.setup.cursor {
				marker = "> "
			}
			label := names[i]
			if label == m.themeName {
				label += "  (current)"
			}
			lines = append(lines, marker+label)
		}
	case setupModel:
		lines = append(lines, "Choose a model:", "")
		windowed, start := windowList(len(m.setup.models), m.setup.modelCursor, body-len(lines))
		for i := start; i < start+windowed; i++ {
			marker := "  "
			if i == m.setup.modelCursor {
				marker = "> "
			}
			lines = append(lines, marker+m.setup.models[i])
		}
	}
	if m.setup.err != "" {
		lines = append(lines, "")
		lines = append(lines, wrap("Error: "+m.setup.err, max(1, m.width-2))...)
	}
	return lines
}

func (m *ui) setupActions() string {
	switch m.setup.stage {
	case setupProvider:
		return "↑/↓ choose  Enter select"
	case setupKey:
		return "Type/paste key  Enter check  Esc back"
	case setupChecking:
		return "Checking…  Esc back"
	case setupLogin:
		return "Enter open browser  Esc back"
	case setupTheme:
		return "↑/↓ choose  Enter select"
	case setupModel:
		return "↑/↓ choose  PgUp/PgDn page  Enter select"
	}
	return ""
}
