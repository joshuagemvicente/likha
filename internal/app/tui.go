package app

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"lisa/internal/model"
	"lisa/internal/repository"
	"lisa/internal/session"
	"lisa/internal/update"
	lisaui "lisa/ui"
)

// minWidth is the smallest terminal the conversation view will render;
// smaller viewports get an inline hint instead of broken layout.
const minWidth, minHeight = 40, 12

// contentWidth is the wrap width for conversation content: the full
// viewport minus padding, capped past 140 columns so wide terminals keep a
// readable measure (tui-layout phase 2). Popups and the composer keep the
// full width.
func contentWidth(termWidth int) int {
	w := termWidth - 2
	if termWidth > 140 {
		w = min(w, 120)
	}
	return max(1, w)
}

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

type entry struct{ role, content string }

type ui struct {
	root, modelName string
	conn            connection
	stateDir        string
	repo            *repository.Repository
	client          *model.Client
	theme           lisaui.Theme
	glyphs          lisaui.Glyphs
	themeName       string
	composerStyle   string
	width, height   int
	statusLineOpts  storedStatusLineConfig
	statusFolder    string
	statusTitle     string
	entries         []entry
	input           []rune
	edit            editState // composer cursor + kill ring (specs/tool-rendering-terminal-keys)
	escPrefix       bool      // armed after ESC: Return inside the decay window inserts a newline
	lines           []string
	lineStyles      []lipgloss.Style
	streamBuf       strings.Builder
	reasoningBuf    strings.Builder
	reasoningStream int
	layoutWidth     int
	scroll          int  // top body line of the viewport; scrollMax() pins it to the newest content
	following       bool // true: the viewport follows new lines like a chat/browser at the bottom
	caretOn         bool // blink phase of the composer's block caret
	caretTyped      bool // recent keystroke: caret renders solid until the blink resumes
	streaming       int
	working         bool
	cancelling      bool
	runID           uint64
	cancel          context.CancelFunc
	events          chan turnEvent
	abandon         chan struct{}
	history         []model.Message
	store           *session.Store
	snapshot        session.Snapshot
	pending         *approvalRequest
	reviewSeen      []bool
	status          string
	mode            string
	setup           setupState
	lastModels      []string     // numbered list shown by /models
	sessionIDs      []string     // ids from the last /sessions listing
	mention         mentionState // @file completion popup state
	commandPopup    commandState // /command completion popup state
	dialogItems     []string     // display rows for the open dialog (theme names, model IDs, session titles)
	dialog          dialogState
	keyModal        keyState // /providers: masked API-key entry for an unconfigured provider

	// Session measurements and identity for the status line (spec §4).
	started          time.Time // session start; drives the minutes segment
	usagePrompt      int64     // cumulative prompt tokens across completed turns
	usageCompletion  int64     // cumulative completion tokens across completed turns
	lastPromptTokens int64     // prompt tokens of the last completed turn (ctx %)
	usageSeen        bool      // provider usage reported at least once this session
	updateVersion    string    // newer release tag when known; empty = none
	git              gitState  // last successful git status read; zero value (and !gitStatusOK) on failure
	gitOK            bool      // last git status call succeeded; false hides every git segment
	spend            float64   // accumulated session cost in US dollars
	spendKnown       bool      // pricing seen for at least one turn (subscription rows included)
	freshSession     bool      // started with no stored entries; gates the auto-naming run
	nameTried        bool      // the one auto-naming attempt already launched
}

// dialogKind identifies which selection dialog is open.
type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogThemes
	dialogModels
	dialogSessions
	dialogProviders
	dialogComposer
)

// dialogState drives the shared selection modal used by /themes, /models,
// /sessions, /providers, and the composer shortcut. One cursor and one open
// flag serve every dialog; the kind selects the item source, the Enter action,
// and the rendered columns. query filters the visible rows for every kind.
type dialogState struct {
	kind    dialogKind
	open    bool
	cursor  int
	query   string // case-insensitive substring filter over the row text
	loadErr string // models dialog: connection/list error surfaced inside the dialog
	loading bool   // models dialog: the list is still being fetched
}

// dialogMatches returns the indices of dialogItems visible under the query:
// a case-insensitive substring match on the display text, which for
// providers embeds both the display name and the canonical name, and for
// models the model id. An empty query matches everything. The cursor
// indexes into this filtered list, so any query change moves the cursor to
// the first visible match (filtering never re-selects the active provider).
func (m *ui) dialogMatches() []int {
	indices := make([]int, 0, len(m.dialogItems))
	q := strings.ToLower(m.dialog.query)
	for i, item := range m.dialogItems {
		if q == "" || strings.Contains(strings.ToLower(item), q) {
			indices = append(indices, i)
		}
	}
	return indices
}

// openDialog switches the UI to a selection dialog. Themes and composer
// styles start with the cursor on the applied choice.
func (m *ui) openDialog(kind dialogKind) tea.Cmd {
	m.dialog = dialogState{kind: kind, open: true, cursor: 0, loading: false}
	switch kind {
	case dialogThemes:
		m.dialogItems = lisaui.ThemeNames()
		for i, name := range m.dialogItems {
			if name == m.themeName {
				m.dialog.cursor = i
				break
			}
		}
		return nil
	case dialogComposer:
		m.dialogItems = composerStyles
		for i, style := range m.dialogItems {
			if style == m.composerStyle {
				m.dialog.cursor = i
				break
			}
		}
		return nil
	case dialogModels:
		// Cursor starts on the live model once the list arrives.
		m.dialog.loading = true
		m.status = "Listing models"
		m.layoutWidth = 0
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ids, err := model.ListModels(ctx, m.client.Base(), m.client.APIKey())
			return modelsListMsg{models: ids, err: err}
		}
	case dialogSessions:
		// Sessions are already local; listing is synchronous.
		summaries, err := m.store.List()
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "List sessions: " + err.Error()})
			m.dialog = dialogState{}
			return nil
		}
		if len(summaries) == 0 {
			m.entries = append(m.entries, entry{role: "Lisa", content: "No saved sessions for this repository yet."})
			m.dialog = dialogState{}
			return nil
		}
		m.sessionIDs = make([]string, len(summaries))
		m.dialogItems = make([]string, len(summaries))
		for i, item := range summaries {
			m.sessionIDs[i] = item.ID
			m.dialogItems[i] = item.Title + "  " + item.Updated.Local().Format("2006-01-02 15:04")
		}
		return nil
	case dialogProviders:
		// Providers are predefined; the configured state comes from the
		// private state directory. The cursor starts on the active provider,
		// mirroring the themes dialog's start-on-applied behavior.
		items, active := m.providersDialogItems()
		m.dialogItems = items
		if activeIndex := active; activeIndex >= 0 {
			m.dialog.cursor = activeIndex
		}
		return nil
	}
	return nil
}

func newUI(root string, repo *repository.Repository, client *model.Client, name string, conn connection, stateDir string, store *session.Store, snapshot session.Snapshot) *ui {
	themeName := conn.theme
	dark := lisaui.HasDarkBackground()
	theme := lisaui.Resolve(themeName, dark)
	glyphs := lisaui.AsciiGlyphs()
	if conn.nerd {
		glyphs = lisaui.NerdGlyphs()
	}
	m := &ui{root: root, repo: repo, client: client, modelName: name, conn: conn, stateDir: stateDir, store: store, snapshot: snapshot, history: snapshot.History, following: true, caretOn: true, streaming: -1, status: "Connected", mode: modeMain, theme: theme, glyphs: glyphs, themeName: themeName, composerStyle: validComposerStyle(conn.composerStyle), statusLineOpts: conn.statusLine, started: time.Now(), freshSession: len(snapshot.Entries) == 0}
	if flagEnabled(m.statusLineOpts.Folder) {
		m.statusFolder = statusFolder(root)
	}
	if m.statusLineOpts.Changes || m.statusLineOpts.Staged || flagEnabled(m.statusLineOpts.Branch) {
		// Session-start read per spec §8: refreshed again after tool
		// results, but an idle user should see the state immediately. One
		// bounded call feeds branch, counts and ahead/behind; ok=false on
		// any failure hides every git segment.
		m.git, m.gitOK = gitStatus(root)
	}
	if m.statusLineOpts.Session {
		m.statusTitle = statusSessionTitle(snapshot)
	}
	// FR-12: a fresh session opens with the startup logo block as its first
	// transcript entry; it scrolls away naturally as the transcript grows
	// (spec §2.1). Resumed sessions never redraw it — persist excludes the
	// role so the block is never stored. rebuild skips it below minWidth 56,
	// where the compact header line and status line carry identity instead.
	if m.freshSession {
		m.entries = append(m.entries, entry{role: "Logo", content: logo})
	}
	if conn.setup {
		m.mode = modeSetup
		m.setup.stage = setupProvider
		m.status = "First-run setup"
		return m
	}
	if conn.err != nil {
		m.status = "Not connected"
		m.entries = append(m.entries, entry{role: "Error", content: "Startup connection check failed: " + conn.err.Error()})
	}
	for _, saved := range snapshot.Entries {
		m.entries = append(m.entries, entry{role: saved.Role, content: saved.Content})
	}
	if len(snapshot.Entries) > 0 && conn.err == nil {
		m.status = "Resumed session"
	}
	return m
}

// updateAvailableMsg carries a newer release tag discovered by the throttled
// startup check. An empty version is a no-op.
type updateAvailableMsg struct{ version string }

// Init runs the release check at most once per day per machine (update spec):
// when update.Prepared says a check is due, Latest is fetched under a short
// timeout and compared against the running version. Every failure — fetch,
// parse, throttle bookkeeping — is silent; the UI simply shows no notice.
func (m *ui) Init() tea.Cmd {
	shouldCheck, mark, err := update.Prepared(m.stateDir)
	if err != nil || !shouldCheck {
		// No update check due: only the caret blink loop runs.
		return blinkCaret()
	}
	return tea.Batch(blinkCaret(), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		latest, err := update.Latest(ctx)
		if err != nil {
			return nil
		}
		_ = mark() // the fetch completed; throttle the next check regardless of the comparison
		if !update.Newer(Version, latest) {
			return nil
		}
		return updateAvailableMsg{version: latest}
	})
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
		case "down":
			if m.setup.cursor < len(lisaui.ThemeNames())-1 {
				m.setup.cursor++
			}
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
		if err := storeOAuth(m.stateDir, p.Name, m.setup.creds); err != nil {
			m.setup.err = err.Error()
			return nil
		}
		client.SetOAuthSaver(func(c model.OAuthCredentials) error {
			return storeOAuth(m.stateDir, p.Name, c)
		})
	} else if key := strings.TrimSpace(string(m.setup.keyInput)); key != "" {
		if err := storeKey(m.stateDir, p.Name, key); err != nil {
			m.setup.err = err.Error()
			return nil
		}
	}
	cfg, err := loadStoredConfig(m.stateDir)
	if err != nil {
		m.setup.err = err.Error()
		return nil
	}
	cfg.Provider, cfg.Model, cfg.Theme = p.Name, modelID, m.themeName
	if err := saveStoredConfig(m.stateDir, cfg); err != nil {
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
	m.conn = connection{provider: p.DisplayName, providerCanonical: p.Name, verified: true, theme: m.themeName, nerd: m.conn.nerd, composerStyle: m.composerStyle}
	m.setup.stage = setupTheme
	m.setup.cursor = 0
	return nil
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
	cfg, err := loadStoredConfig(m.stateDir)
	if err == nil && cfg.Provider != "" {
		cfg.Theme = m.themeName
		if err := saveStoredConfig(m.stateDir, cfg); err != nil {
			m.setup.err = err.Error()
			return nil
		}
	}
	m.mode = modeMain
	m.status = "Connected"
	m.layoutWidth = 0
	return nil
}

func waitEvent(events <-chan turnEvent) tea.Cmd {
	return func() tea.Msg { return <-events }
}

// startTurn submits a user prompt to the model and enters the working state.
func (m *ui) startTurn(prompt string) tea.Cmd {
	if m.client == nil {
		m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup before prompting."})
		m.input = []rune(prompt)
		m.edit.endCaret(m.input)
		return nil
	}
	prior := m.history
	m.entries = append(m.entries, entry{role: "You", content: prompt})
	m.history = append(m.history, model.Message{Role: "user", Content: prompt})
	m.refreshStatusSessionTitle()
	m.persist()
	m.streamBuf.Reset()
	m.streaming = -1
	m.reasoningBuf.Reset()
	m.reasoningStream = -1
	m.status = "Waiting for model"
	m.working = true
	m.jumpBottom()
	m.layoutWidth = 0
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan turnEvent, 64)
	m.abandon = make(chan struct{})
	m.runID++
	runID := m.runID
	events := m.events
	abandon := m.abandon
	client, repo, root, mcp := m.client, m.repo, m.root, m.conn.mcp
	go func() {
		defer close(events)
		runTurn(ctx, client, repo, root, prior, prompt, mcp, func(ev turnEvent) {
			ev.runID = runID
			if ev.kind == "done" || ev.kind == "error" || ev.kind == "tool_result" {
				select {
				case events <- ev:
				case <-abandon:
				}
				return
			}
			select {
			case events <- ev:
			case <-ctx.Done():
			case <-abandon:
			}
		})
	}()
	return waitEvent(events)
}

// startCompaction summarizes the conversation so far with one model call and
// replaces the summarized turns with the returned brief. It mirrors
// startTurn's run machinery: cancellable context, an events channel drained
// by waitEvent, and the shared working state. No text streams into the
// conversation while it runs; the deliverable is a single turnEvent.
func (m *ui) startCompaction(focus string) tea.Cmd {
	m.status = "Compacting…"
	m.working = true
	m.jumpBottom()
	m.layoutWidth = 0
	m.input = nil
	m.edit.endCaret(m.input)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan turnEvent, 64)
	m.abandon = make(chan struct{})
	m.runID++
	runID := m.runID
	events := m.events
	abandon := m.abandon
	client, history := m.client, m.history
	go func() {
		defer close(events)
		newHistory, summary, err := compactHistory(ctx, client, history, focus, nil)
		ev := turnEvent{runID: runID}
		if err != nil {
			// The failed call never touched the caller's history; carrying
			// it in the event leaves the error handler's history swap a
			// no-op.
			ev.kind, ev.text, ev.history = "error", err.Error(), history
		} else {
			ev.kind, ev.text, ev.history = "compacted", summary, newHistory
		}
		select {
		case events <- ev:
		case <-abandon:
		}
	}()
	return waitEvent(events)
}

// commandHelp is the text /help prints and unknown-command errors point to.
const commandHelp = "Commands: /compact [focus] summarize the conversation into a compact brief; /sessions [n] list or resume a saved session; /models list provider models; /providers select the provider for this session (a prompt asks for the API key when none is stored); /quit exit; /help this list. Selections open a dialog: ↑/↓ navigate, type to filter, Enter apply, Esc cancel. Unknown /commands are not sent to the model; // sends a literal slash."

// handleCommand dispatches a leading-slash input. Reserved commands act on
// the application and never reach the model; unknown commands restore the
// draft so nothing is lost.
func (m *ui) handleCommand(line string) tea.Cmd {
	m.layoutWidth = 0 // command results change the conversation body
	if strings.HasPrefix(line, "//") {
		// Escaped literal slash: strip one and send as a normal prompt.
		return m.startTurn(line[1:])
	}
	name, arg, _ := strings.Cut(line[1:], " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "quit":
		if m.cancel != nil {
			if m.abandon != nil {
				close(m.abandon)
			}
			m.cancel()
		}
		return tea.Quit
	case "help":
		m.entries = append(m.entries, entry{role: "Lisa", content: commandHelp})
		return nil
	case "compact":
		// One model call replaces the summarized past. Refusals are visible
		// entries and never reach the network; nothing changes on failure.
		if m.client == nil {
			m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
			return nil
		}
		if m.pending != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "A review is pending; resolve it before compacting."})
			return nil
		}
		if len(m.history) == 0 {
			m.entries = append(m.entries, entry{role: "Lisa", content: "Nothing to compact yet."})
			return nil
		}
		return m.startCompaction(arg)
	case "models":
		if m.client == nil {
			m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
			return nil
		}
		return m.openDialog(dialogModels)
	case "providers":
		return m.handleProvidersCommand(line, arg)
	case "sessions":
		return m.handleSessionsCommand(arg)
	case "themes":
		return m.handleThemesCommand(arg)
	case "mcp":
		m.entries = append(m.entries, entry{role: "Lisa", content: m.conn.mcp.Status()})
		return nil
	default:
		m.input = []rune(line)
		m.edit.endCaret(m.input)
		m.entries = append(m.entries, entry{role: "Error", content: "Unknown command /" + name + "; not sent to the model. " + commandHelp})
		return nil
	}
}

func (m *ui) handleSessionsCommand(arg string) tea.Cmd {
	if m.store == nil {
		m.entries = append(m.entries, entry{role: "Error", content: "No session store available."})
		return nil
	}
	if arg == "" {
		// Open the selection dialog; sessions are listed synchronously.
		return m.openDialog(dialogSessions)
	}
	index, err := strconv.Atoi(arg)
	if err != nil || index < 1 || index > len(m.sessionIDs) {
		m.input = []rune("/sessions " + arg)
		m.edit.endCaret(m.input)
		m.entries = append(m.entries, entry{role: "Error", content: "Run /sessions first to list sessions, then /sessions <n> with a number from that list."})
		return nil
	}
	return m.resumeSession(m.sessionIDs[index-1])
}

// resumeSession swaps history and entries for the completed snapshot. A
// stored snapshot never contains a pending approval, so nothing replays.
func (m *ui) resumeSession(id string) tea.Cmd {
	snapshot, err := m.store.Load(id)
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "Resume session: " + err.Error()})
		return nil
	}
	m.snapshot = snapshot
	m.history = snapshot.History
	m.freshSession = false // a resumed session never re-generates its name
	m.entries = m.entries[:0]
	for _, saved := range snapshot.Entries {
		m.entries = append(m.entries, entry{role: saved.Role, content: saved.Content})
	}
	m.refreshStatusSessionTitle()
	m.streamBuf.Reset()
	m.streaming = -1
	m.reasoningBuf.Reset()
	m.reasoningStream = -1
	m.pending = nil
	m.reviewSeen = nil
	m.jumpBottom()
	m.layoutWidth = 0
	m.status = "Resumed session"
	return nil
}

// updateDialog handles keys while a selection dialog is open. The cursor
// is the selected state over the filtered rows; printable keys extend the
// query and Backspace deletes (spec: the query applies to every dialog
// kind); Enter applies, Esc clears the query first and then discards.
func (m *ui) updateDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		r := msg.Runes
		if msg.Type == tea.KeySpace {
			r = []rune{' '}
		}
		m.dialog.query += string(r)
		m.dialog.cursor = 0 // reset to the first visible match on query change
		return m, nil
	case tea.KeyBackspace:
		if runes := []rune(m.dialog.query); len(runes) > 0 {
			m.dialog.query = string(runes[:len(runes)-1])
			m.dialog.cursor = 0
		}
		return m, nil
	}
	matches := m.dialogMatches()
	switch msg.String() {
	case "up":
		if m.dialog.cursor > 0 {
			m.dialog.cursor--
		}
	case "down":
		if m.dialog.cursor < len(matches)-1 {
			m.dialog.cursor++
		}
	case "pgup":
		m.dialog.cursor = max(0, m.dialog.cursor-m.dialogWindow())
	case "pgdown":
		m.dialog.cursor = min(len(matches)-1, m.dialog.cursor+m.dialogWindow())
	case "enter":
		if len(matches) == 0 {
			// Empty match set: Enter does nothing while nothing is visible.
			return m, nil
		}
		return m, m.confirmDialog(matches)
	case "esc":
		if m.dialog.query != "" {
			// Esc clears the query first, then closes on a second Esc.
			m.dialog.query = ""
			m.dialog.cursor = 0
			m.layoutWidth = 0
			return m, nil
		}
		// Discard: nothing applied; the item list is dropped with the dialog
		// so a later open starts clean and view math never indexes stale rows.
		m.dialog = dialogState{}
		m.dialogItems = nil
		m.layoutWidth = 0
		return m, nil
	}
	return m, nil
}

// dialogWindow is the number of rows the dialog list can show.
func (m *ui) dialogWindow() int {
	return max(1, m.height-10)
}

// confirmDialog applies the highlighted selection and closes the dialog.
// Enter while the models list is still loading does nothing, so the dialog
// cannot be dismissed into an empty selection by accident.
func (m *ui) confirmDialog(matches []int) tea.Cmd {
	if m.dialog.loading {
		return nil
	}
	if m.dialog.cursor >= len(matches) {
		m.dialog = dialogState{}
		return nil
	}
	origIndex := matches[m.dialog.cursor]
	// Guard the parallel arrays: matches were computed for the list seen at
	// keypress time; a late refresh could have shortened it before Enter.
	if origIndex < 0 || origIndex >= len(m.dialogItems) ||
		m.dialog.kind == dialogSessions && origIndex >= len(m.sessionIDs) ||
		m.dialog.kind == dialogProviders && origIndex >= len(model.Providers) {
		m.dialog = dialogState{}
		m.dialogItems = nil
		m.layoutWidth = 0
		return nil
	}
	id := m.dialogItems[origIndex]
	switch m.dialog.kind {
	case dialogThemes:
		m.dialog = dialogState{}
		m.layoutWidth = 0
		m.applyTheme(id)
	case dialogComposer:
		m.dialog = dialogState{}
		m.dialogItems = nil
		m.layoutWidth = 0
		m.applyComposerStyle(id)
	case dialogModels:
		m.dialog = dialogState{}
		m.layoutWidth = 0
		m.applyModel(id)
	case dialogSessions:
		m.dialog = dialogState{}
		m.layoutWidth = 0
		return m.resumeSession(m.sessionIDs[origIndex])
	case dialogProviders:
		m.dialog = dialogState{}
		m.layoutWidth = 0
		return m.applyProviderDirect(model.Providers[origIndex])
	}
	return nil
}

// applyTheme switches the live theme and stores it for later runs. Does not
// close the modal by itself.
func (m *ui) applyTheme(themeName string) {
	m.themeName = themeName
	m.theme = lisaui.Resolve(themeName, lisaui.HasDarkBackground())
	if cfg, err := loadStoredConfig(m.stateDir); err == nil && cfg.Provider != "" {
		cfg.Theme = themeName
		if err := saveStoredConfig(m.stateDir, cfg); err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Store theme: " + err.Error()})
			return
		}
	}
	m.entries = append(m.entries, entry{role: "Lisa", content: "Theme set to " + themeName + "; stored for later runs."})
}

// handleThemesCommand lists the predefined themes or applies one by number
// or name. A bare /themes opens the selection dialog; a number or name applies
// directly. The applied theme is stored for later runs.
func (m *ui) handleThemesCommand(arg string) tea.Cmd {
	m.layoutWidth = 0
	names := lisaui.ThemeNames()
	if arg == "" {
		// Open the selection dialog; the cursor starts on the applied theme.
		return m.openDialog(dialogThemes)
	}
	themeName := arg
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(names) {
			m.input = []rune("/themes " + arg)
			m.edit.endCaret(m.input)
			m.entries = append(m.entries, entry{role: "Error", content: "Run /themes first, then /themes <n> with a number from that list."})
			return nil
		}
		themeName = names[n-1]
	}
	if _, ok := lisaui.Named(themeName, true); !ok {
		m.input = []rune("/themes " + arg)
		m.edit.endCaret(m.input)
		m.entries = append(m.entries, entry{role: "Error", content: "Unknown theme " + themeName + "; run /themes for the list."})
		return nil
	}
	m.applyTheme(themeName)
	return nil
}

// modelsListMsg carries the provider's model list for the /models dialog.
type modelsListMsg struct {
	models []string
	err    error
}

func (m *ui) handleModelsResult(msg modelsListMsg) {
	m.layoutWidth = 0
	if m.dialog.open && m.dialog.kind == dialogModels {
		// The dialog asked for this list; populate it in place. A late result
		// from an earlier open refreshes the open dialog too (idempotent) —
		// without this it would fall through to the text dump behind the box.
		m.dialog.loading = false
		if msg.err != nil {
			m.dialog.loadErr = msg.err.Error()
			m.dialogItems = nil
			return
		}
		if len(msg.models) == 0 {
			m.dialog.loadErr = "The provider reports no models."
			m.dialogItems = nil
			return
		}
		m.lastModels = msg.models
		m.dialogItems = msg.models
		// Reset first: a shrunken list must not leave the cursor out of range
		// when the live model is absent from it.
		m.dialog.cursor = 0
		for i, id := range msg.models {
			if id == m.modelName {
				m.dialog.cursor = i
				break
			}
		}
		return
	}
	if msg.err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "List models: " + msg.err.Error()})
		return
	}
	if len(msg.models) == 0 {
		m.entries = append(m.entries, entry{role: "Lisa", content: "The provider reports no models."})
		return
	}
	m.lastModels = msg.models
	var b strings.Builder
	b.WriteString("Provider models (the /models dialog applies the highlighted model with Enter):")
	for i, id := range msg.models {
		fmt.Fprintf(&b, "\n%d. %s", i+1, id)
	}
	m.entries = append(m.entries, entry{role: "Lisa", content: b.String()})
}

// applyModel switches the live client and stores the choice in config.json
// for future runs. Reached through the models dialog; there is no typed
// /model command (spec §7: dialog navigation selects; typed commands never
// index).
func (m *ui) applyModel(id string) {
	if err := m.client.SetModel(id); err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: err.Error()})
		return
	}
	m.modelName = id
	if cfg, err := loadStoredConfig(m.stateDir); err == nil && cfg.Provider != "" {
		cfg.Model = id
		if err := saveStoredConfig(m.stateDir, cfg); err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Store model: " + err.Error()})
			return
		}
	}
	m.entries = append(m.entries, entry{role: "Lisa", content: "Model switched to " + id + " and stored for later runs."})
}

func (m *ui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.layoutWidth = 0
		if m.pending != nil {
			// The proposal re-lays out: review restarts at its top under the
			// new layout, preserving the re-review-on-resize contract.
			m.scroll = 0
			m.following = false
			m.reviewSeen = make([]bool, m.pageCount())
			m.markSeenFromScroll()
		}
		return m, nil
	case updateAvailableMsg:
		if v.version != "" {
			m.updateVersion = v.version
		}
		return m, nil
	case turnEvent:
		if !m.working || v.runID != m.runID {
			return m, nil
		}
		switch v.kind {
		case "reasoning":
			// Thinking output from a reasoning model; rendered muted and
			// closed as soon as the first real content delta arrives. Deltas
			// after content started are dropped so thinking cannot be
			// mistaken for the answer.
			if m.streaming >= 0 {
				return m, nil
			}
			if m.reasoningStream < 0 {
				m.entries = append(m.entries, entry{role: "Reasoning"})
				m.reasoningStream = len(m.entries) - 1
			}
			m.reasoningBuf.WriteString(v.text)
			m.entries[m.reasoningStream].content = m.reasoningBuf.String()
		case "text":
			m.reasoningStream = -1 // content after thinking closes the reasoning stream
			if m.streaming < 0 {
				m.entries = append(m.entries, entry{role: "Assistant"})
				m.streaming = len(m.entries) - 1
			}
			m.streamBuf.WriteString(v.text)
			m.entries[m.streaming].content = m.streamBuf.String()
		case "approval":
			if m.cancelling {
				break
			}
			m.pending = v.approval
			m.scroll = 0 // review starts at the top of the proposal screen
			m.following = false
			m.layoutWidth = 0
			m.reviewSeen = make([]bool, m.pageCount())
			m.markSeenFromScroll()
			m.status = "Review " + m.pending.Kind + " before approval"
		case "tool_start", "tool_result":
			m.streamBuf.Reset()
			m.streaming = -1
			m.entries = append(m.entries, entry{role: "Tool", content: v.text})
			if m.cancelling {
				m.status = "Cancelling"
			} else if v.kind == "tool_start" {
				m.status = "Reading repository"
			} else {
				m.status = "Waiting for model"
			}
			if v.kind == "tool_result" {
				if m.statusLineOpts.Changes || m.statusLineOpts.Staged || flagEnabled(m.statusLineOpts.Branch) {
					m.git, m.gitOK = gitStatus(m.root)
				}
				m.history = v.history
				m.persist()
			}
		case "compacted":
			// The single summarize call finished: swap the summarized turns
			// for the brief and mark the point in the conversation.
			m.history = v.history
			m.entries = append(m.entries, entry{role: "Lisa", content: "Conversation compacted. Summary of earlier turns:\n\n" + v.text})
			m.status = "Ready"
			m.working = false
			if m.cancel != nil {
				m.cancel()
			}
			m.cancel = nil
			m.persist()
			m.layoutWidth = 0
			return m, nil
		case "done", "error":
			m.pending = nil
			m.history = v.history
			m.working = false
			if m.cancel != nil {
				m.cancel()
			}
			m.cancel = nil
			if m.streaming >= 0 && v.kind == "error" {
				m.entries = append(m.entries[:m.streaming], m.entries[m.streaming+1:]...)
			}
			m.streaming = -1
			if m.cancelling {
				for _, message := range v.history {
					if message.Role == "tool" && message.Content == "Error: action not executed; run interrupted" {
						m.entries = append(m.entries, entry{role: "Tool", content: message.Content})
					}
				}
			}
			if m.cancelling {
				m.status = "Cancelled"
				m.entries = append(m.entries, entry{role: "Lisa", content: "Run cancelled; no further tools will execute. Approved shell commands may leave detached processes running."})
			} else if v.kind == "error" {
				m.status = "Error"
				m.entries = append(m.entries, entry{role: "Error", content: v.text})
			} else {
				m.status = "Ready"
			}
			if v.kind == "done" && m.client != nil {
				// Provider-reported usage of the just-finished response;
				// absent usage keeps the ctx/tokens segments at their fallback.
				if usage, ok := m.client.LastTokenUsage(); ok {
					m.usagePrompt += usage.Prompt
					m.usageCompletion += usage.Completion
					m.lastPromptTokens = usage.Prompt
					m.usageSeen = true
					// Session spend (spec tui-layout 1b.6): the subscription
					// row is a known $0.00, priced models accumulate, and a
					// model without documented pricing keeps the segment
					// hidden rather than estimating.
					if m.conn.providerCanonical == "chatgpt" {
						m.spendKnown = true
					} else if cost, priced := model.TurnCost(m.modelName, usage.Prompt, usage.Completion); priced {
						m.spend += cost
						m.spendKnown = true
					}
				}
			}
			m.cancelling = false
			m.persist()
			m.refreshStatusSessionTitle()
			if cmd := m.autoNameAfterFirstTurn(v.kind); cmd != nil {
				m.layoutWidth = 0
				return m, cmd
			}
		}
		m.layoutWidth = 0
		if m.working {
			return m, waitEvent(m.events)
		}
		return m, nil
	case tea.MouseMsg:
		if m.mode == modeSetup {
			return m, nil
		}
		// The scrollbar column claims clicks and drags; anywhere else, the
		// wheel scrolls line-granular like a browser.
		if m.updateScrollbarMouse(v) {
			return m, nil
		}
		switch v.Type {
		case tea.MouseWheelUp:
			m.scrollBy(-scrollWheelLines)
			m.markSeenFromScroll()
		case tea.MouseWheelDown:
			m.scrollBy(scrollWheelLines)
			m.markSeenFromScroll()
		}
		return m, nil
	case modelsListMsg:
		if m.mode != modeMain {
			return m, nil
		}
		m.handleModelsResult(v)
		return m, nil
	case nameGeneratedMsg:
		// Auto-naming (spec tui-layout 1c): a failure or a late result after
		// a name was applied stays silent — the derived title remains and no
		// error entry is ever shown.
		if m.mode != modeMain || m.snapshot.NamedTitle != "" {
			return m, nil
		}
		if v.err == nil && v.name != "" {
			m.snapshot.NamedTitle = v.name
			m.statusTitle = v.name
			m.layoutWidth = 0
			m.persist()
		}
		return m, nil
	case caretTickMsg:
		// Solid while the user is typing; blink once idle. The loop runs
		// regardless of mode so it never needs re-arming.
		if m.caretTyped {
			m.caretOn = true
			m.caretTyped = false
		} else {
			m.caretOn = !m.caretOn
		}
		return m, blinkCaret()
	case escDecayMsg:
		// The Alt-prefix decay expired: this was a bare ESC, not the
		// Alt+Return prefix — clear the draft as ESC did before the decay
		// window existed (spec open item 2).
		if m.escPrefix {
			m.escPrefix = false
			if !m.working && m.pending == nil && len(m.input) > 0 {
				m.input = nil
				m.edit.endCaret(nil)
				m.layoutWidth = 0
			}
		}
		return m, nil
	case fileIndexMsg:
		// The @ popup's background walk finished; populate and re-filter
		// against the current draft.
		m.mention.index = v.files
		if v.err != nil {
			m.mention.err = v.err.Error()
		}
		if m.mention.open {
			m.syncMention()
		}
		return m, nil
	case keyCheckMsg:
		if m.mode != modeMain || !m.keyModal.open {
			// Late result after the modal closed: ignore.
			return m, nil
		}
		m.handleKeyCheckMsg(v)
		return m, nil
	case providerSwitchMsg:
		if m.mode != modeMain || m.keyModal.open || m.dialog.open {
			// Late result after esc closed the flow: ignore.
			return m, nil
		}
		return m, m.handleProviderSwitchMsg(v)
	case setupCheckMsg:
		if m.mode != modeSetup || m.setup.stage != setupChecking {
			return m, nil
		}
		m.setup.checking = false
		if v.err != nil {
			m.setup.err = v.err.Error()
			return m, nil
		}
		m.setup.err = ""
		m.setup.models = v.models
		if len(v.models) == 1 {
			return m, m.finishSetup(v.models[0])
		}
		m.setup.stage = setupModel
		m.setup.modelCursor = 0
		return m, nil
	case oauthLoginMsg:
		if m.mode != modeSetup || m.setup.stage != setupLogin || !m.setup.checking {
			// A late login result is ignored once the stage moves on.
			return m, nil
		}
		m.setup.checking = false
		if v.err != nil {
			m.setup.err = fmt.Sprintf("ChatGPT sign-in failed: %v — press Enter to retry", v.err)
			return m, nil
		}
		// The Codex backend has no OpenAI-shaped model list; setup offers the
		// curated ChatGPT models directly.
		m.setup.err = ""
		m.setup.creds = v.creds
		m.setup.models = model.ChatGPTModels
		m.setup.modelCursor = 0
		m.setup.stage = setupModel
		return m, nil
	case tea.KeyMsg:
		if m.mode == modeSetup {
			return m.updateSetup(v)
		}
		// The @ completion popup claims navigation and completion keys only
		// while it has rows; everything else keeps editing the draft.
		if m.mention.open {
			if claimed, cmd := m.updateMention(v); claimed {
				return m, cmd
			}
		}
		// The / command popup mirrors it; the two popups are never open
		// together, and Esc/Ctrl+C must still reach a running turn.
		if m.commandPopup.open && !m.working {
			if claimed, cmd := m.updateCommand(v); claimed {
				return m, cmd
			}
		}
		if m.keyModal.open {
			// The key modal takes precedence over the provider dialog beneath
			// it; Esc returns to that dialog with nothing changed.
			return m, m.updateKeyModal(v)
		}
		if m.dialog.open {
			return m.updateDialog(v)
		}
		if v.String() == "ctrl+g" {
			if !m.working && m.pending == nil {
				return m, m.openDialog(dialogComposer)
			}
			return m, nil
		}
		if m.pending != nil {
			switch v.String() {
			case "y", "Y":
				if m.width < minWidth || m.height < minHeight {
					return m, nil
				}
				for _, seen := range m.reviewSeen {
					if !seen {
						m.status = "Review every page before approving"
						return m, nil
					}
				}
				m.pending.Reply <- true
				m.status = "Executing approved " + m.pending.Kind
				m.pending = nil
				m.reviewSeen = nil
				m.jumpBottom()
				m.layoutWidth = 0
				return m, nil
			case "n", "N":
				m.pending.Reply <- false
				m.status = "Rejected"
				m.pending = nil
				m.reviewSeen = nil
				m.jumpBottom()
				m.layoutWidth = 0
				return m, nil
			}
		}
		// The Alt-prefix decay window only carries Return; any other chord
		// is processed as its own key and cancels the window (the ESC is
		// dropped, never queued).
		if m.escPrefix && v.String() != "enter" {
			m.escPrefix = false
		}
		switch v.String() {
		case "ctrl+c", "esc":
			if m.working {
				// Cancel-run stays immediate: while a turn runs, editing is
				// inert, so ESC carries no newline-prefix ambiguity.
				m.cancel()
				m.pending = nil
				m.reviewSeen = nil
				m.cancelling = true
				m.status = "Cancelling"
				m.layoutWidth = 0
				return m, nil
			}
			if v.String() == "ctrl+c" {
				return m, tea.Quit
			}
			// Idle ESC arms the Alt-prefix decay window: a Return arriving
			// before the expiry inserts a newline (Esc-prefix Return, the
			// terminal encoding of Alt+Return); expiry clears the draft as
			// bare ESC always has. Popups claim ESC earlier, so this never
			// delays popup dismissal.
			m.escPrefix = true
			return m, escDecay()
		case "ctrl+d":
			if m.cancel != nil {
				if m.abandon != nil {
					close(m.abandon)
				}
				m.cancel()
			}
			return m, tea.Quit
		case "pgup", "ctrl+p":
			m.pageUp()
		case "pgdown", "ctrl+n":
			m.pageDown()
		case "home":
			// Jump to the very top of the session.
			m.jumpTop()
		case "end":
			// Jump to the very last chat: follow the newest content.
			m.jumpBottom()
		case "backspace", "ctrl+h":
			if m.editable() && len(m.input) > 0 {
				m.caretNote()
				deleteBack(&m.input, &m.edit)
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+w", "ctrl+backspace", "alt+backspace":
			// Ctrl+Backspace delivers ctrl+w's byte on most terminals
			// (probe table in tasks.md), so it needs no distinct chord.
			if m.editable() && killWordBack(&m.input, &m.edit) {
				m.caretNote()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+delete", "alt+delete", "alt+d":
			if m.editable() && killWordForward(&m.input, &m.edit) {
				m.caretNote()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "alt+b":
			if m.editable() {
				m.caretNote()
				moveWordBack(m.input, &m.edit)
			}
		case "alt+f":
			if m.editable() {
				m.caretNote()
				moveWordForward(m.input, &m.edit)
			}
		case "ctrl+u":
			if m.editable() && killToStart(&m.input, &m.edit) {
				m.caretNote()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+k":
			if m.editable() && killToEnd(&m.input, &m.edit) {
				m.caretNote()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+y":
			if m.editable() && yank(&m.input, &m.edit) {
				m.caretNote()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+t":
			if m.editable() && transpose(&m.input, &m.edit) {
				m.caretNote()
			}
		case "ctrl+j", "alt+enter", "ctrl+enter", "shift+enter":
			// Newline insertion (the Return-modifier family). ctrl+enter
			// reaches most terminals as the LF byte, reported "ctrl+j";
			// alt+enter arrives ESC-combined or through the ESC decay
			// window on "enter" below (probe table in tasks.md).
			if m.editable() && !m.mention.open && !m.commandPopup.open {
				m.caretNote()
				insertNewline(&m.input, &m.edit)
			}
		case "enter":
			if m.escPrefix {
				m.escPrefix = false
				if m.editable() && !m.mention.open && !m.commandPopup.open {
					m.caretNote()
					insertNewline(&m.input, &m.edit)
					break
				}
			}
			if m.working || m.width < minWidth || m.height < minHeight {
				return m, nil
			}
			// Typed newlines flatten to spaces on submit, exactly as
			// bracketed-paste newlines do (user-confirmed decision).
			prompt := strings.TrimSpace(strings.ReplaceAll(string(m.input), "\n", " "))
			if prompt == "" {
				return m, nil
			}
			m.input = nil
			m.edit.endCaret(nil)
			if strings.HasPrefix(prompt, "/") {
				return m, m.handleCommand(prompt)
			}
			return m, m.startTurn(prompt)
		default:
			if m.editable() {
				if v.Type == tea.KeyRunes {
					runes := v.Runes
					if v.Paste {
						// Bracketed paste inserts at the caret; pasted
						// newlines become spaces (paste rule).
						runes = []rune(strings.ReplaceAll(string(v.Runes), "\n", " "))
					}
					m.caretNote() // solid caret while typing
					insertRunes(&m.input, &m.edit, runes)
				} else if v.Type == tea.KeySpace {
					m.caretNote()
					insertRunes(&m.input, &m.edit, []rune{' '})
				}
			}
			if cmd := m.syncPopups(); cmd != nil {
				m.layoutWidth = 0
				return m, cmd
			}
		}
		m.layoutWidth = 0
	}
	return m, nil
}

// editable reports whether the composer accepts edits: not while a turn
// streams or an approval is pending.
func (m *ui) editable() bool {
	return !m.working && m.pending == nil
}

// syncPopups re-filters the @-mention and /-command popups after a draft
// edit; the popups are never open together, and an @ anywhere wins.
// Returns the background command for a freshly started mention index walk.
func (m *ui) syncPopups() tea.Cmd {
	if !m.working && m.repo != nil {
		if _, active := mentionQuery(string(m.input)); active {
			m.commandPopup.open = false // the popups never coexist
			if cmd := m.startMention(); cmd != nil {
				return cmd
			}
			m.syncMention()
			return nil
		}
		m.mention.open = false
	}
	// A typed / opens the command popup while no argument has begun.
	if !m.working {
		if _, active := commandQuery(string(m.input)); active && !strings.Contains(string(m.input), "@") {
			m.commandPopup.open = true
			m.syncCommand()
			m.mention.open = false
		} else {
			m.commandPopup.open = false
		}
	}
	return nil
}

// escDecay is the Alt-prefix decay window: ESC held for this long means a
// bare ESC (clears the draft); Return inside the window means Alt+Return.
const escDecayWindow = 50 * time.Millisecond

type escDecayMsg struct{}

func escDecay() tea.Cmd {
	return tea.Tick(escDecayWindow, func(time.Time) tea.Msg { return escDecayMsg{} })
}

// nameGeneratedMsg carries the outcome of the one auto-naming call. An error
// or empty name keeps the derived title; nothing is ever surfaced.
type nameGeneratedMsg struct {
	name string
	err  error
}

// autoNameAfterFirstTurn names a fresh session after its first COMPLETED
// turn (spec tui-layout 1c): exactly one fire-and-forget call per run,
// resumed sessions never re-generate, and the result is dropped naturally
// when the user exits before it lands. The 20-second context bounds the
// call; a failure keeps the derived title silently.
func (m *ui) autoNameAfterFirstTurn(kind string) tea.Cmd {
	if kind != "done" || !m.freshSession || m.nameTried || m.snapshot.NamedTitle != "" || m.client == nil {
		return nil
	}
	m.nameTried = true
	client, history := m.client, m.history
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		name, err := generateSessionName(ctx, client, history)
		return nameGeneratedMsg{name: name, err: err}
	}
}

func (m *ui) persist() {
	if m.store == nil {
		return
	}
	m.snapshot.History = m.history
	m.snapshot.Entries = make([]session.Entry, 0, len(m.entries))
	for _, current := range m.entries {
		if current.role == "Logo" {
			continue // startup block is per-run furniture, never stored
		}
		m.snapshot.Entries = append(m.snapshot.Entries, session.Entry{Role: current.role, Content: current.content})
	}
	if err := m.store.Save(m.snapshot); err != nil {
		m.status = "Session save failed"
		m.entries = append(m.entries, entry{role: "Error", content: "Session was not saved: " + err.Error()})
	}
}

func (m *ui) markReviewPage() {
	m.markSeenFromScroll()
}

// header is the chrome above the transcript (spec tui-layout 1a): empty at
// 56 columns and up — the ASCII logo on page 1 plus the status bar's Folder
// and Branch segments carry identity — and a single compact line on the
// narrowest terminals, where the logo is skipped. Every len(m.header())
// consumer (bodyHeight, composer limit, popup budgets, rebuild fallbacks)
// already tolerates the zero-line form.
func (m *ui) header() []string {
	if m.width >= 56 {
		return nil
	}
	return []string{"Lisa · " + filepath.Base(m.root)}
}

func (m *ui) bodyHeight() int {
	return max(1, m.height-len(m.header())-len(m.composerLines())-max(len(m.mentionLines()), len(m.commandLines()))-m.statusLineHeight())
}

func (m *ui) pageCount() int {
	m.rebuild()
	body := m.bodyHeight()
	if body <= 0 {
		return 1
	}
	return max(1, (len(m.lines)+body-1)/body)
}

// rebuild lays out content only when the viewport or content changes. The
// screen is never scrolled; each frame selects a discrete page of these lines.
// lineStyles mirrors m.lines so body lines can carry entry-level styling
// (reasoning output renders muted).
func (m *ui) rebuild() {
	if m.layoutWidth == m.width && m.lines != nil {
		return
	}
	plain := lipgloss.NewStyle()
	m.lines = m.lines[:0]
	m.lineStyles = m.lineStyles[:0]
	// add appends display lines and the style each one renders with.
	add := func(lines []string, style lipgloss.Style) {
		for _, line := range lines {
			m.lines = append(m.lines, line)
			m.lineStyles = append(m.lineStyles, style)
		}
	}
	width := contentWidth(m.width)
	if m.pending != nil {
		add(wrap(m.pending.Title, width), plain)
		if m.pending.Kind == "command" {
			add([]string{"WARNING: No filesystem/network sandbox"}, m.theme.Warning)
			add([]string{"WARNING: Detached jobs may survive"}, m.theme.Warning)
		}
		add([]string{""}, plain)
		add(wrap(m.pending.Body, width), plain)
	} else {
		// Narrow terminals keep the compact header line and re-wrap its
		// overflow into the body; at 56 columns and up there is nothing
		// header-shaped to wrap.
		for _, line := range m.header() {
			if runewidth.StringWidth(line) > m.width {
				add(wrap(line, width), plain)
			}
		}
		first := false
		for _, e := range m.entries {
			if e.role == "Logo" {
				// Startup block: raw pre-formatted logo lines, never
				// re-wrapped. The block is fixed-width ASCII, but a line wider
				// than the viewport would still push its tail off-screen (raw
				// lines skip fit()), so over-wide lines hard-wrap to content
				// width. A no-op for the current logo; it keeps the one raw
				// render path provably safe if the art is ever retraced.
				if m.width < 56 {
					continue
				}
				if first {
					add([]string{""}, plain)
				}
				first = true
				for _, line := range strings.Split(e.content, "\n") {
					if runewidth.StringWidth(line) <= width {
						add([]string{line}, m.theme.Title)
						continue
					}
					add(wrap(line, width), m.theme.Title)
				}
				continue
			}
			if first {
				add([]string{""}, plain)
			}
			first = true
			style := plain
			if e.role == "Reasoning" || e.role == "Tool" {
				// Reasoning output (FR-15) and tool activity render muted:
				// machinery, not assistant content.
				style = m.theme.Muted
			}
			add(wrap(e.role+": "+e.content, width), style)
		}
	}
	m.layoutWidth = m.width
}

func (m *ui) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return m.frame([]string{"Lisa: enlarge terminal to at least 40 columns by 12 rows"})
	}
	if m.mode == modeSetup {
		return m.setupView()
	}
	if m.keyModal.open {
		return m.keyModalView()
	}
	if m.dialog.open {
		return m.dialogView()
	}
	return m.mainView()
}

// mainView renders the conversation: header, scrolled body with a scrollbar,
// composer, status line. The viewport is bodyHeight() lines of m.lines
// starting at m.scroll; the newest content follows while the viewport sits at
// the bottom.
func (m *ui) mainView() string {
	m.rebuild()
	header := m.header()
	composer := m.composerLines()
	mention := m.mentionLines()
	command := m.commandLines()
	body := max(1, m.height-len(header)-len(composer)-len(mention)-len(command)-m.statusLineHeight())
	m.clampScroll()
	pages := max(1, (len(m.lines)+body-1)/body)
	// The indicator names the page containing the viewport's bottom edge, so
	// the pinned (bottom) view always reads as the last page.
	page := (m.scroll + body - 1) / body
	page = max(0, min(pages-1, page))
	rows := make([]string, 0, m.height)
	for i, line := range header {
		if i == 0 {
			rows = append(rows, m.theme.Title.Render(fit(line, m.width)))
		} else {
			rows = append(rows, fit(line, m.width))
		}
	}
	start := m.scroll
	for i := range body {
		line := ""
		style := lipgloss.NewStyle()
		if start+i < len(m.lines) {
			line = m.lines[start+i]
			if start+i < len(m.lineStyles) {
				style = m.lineStyles[start+i]
			}
		}
		// Style the padded line: foreground on padding spaces is invisible.
		rows = append(rows, style.Render(fit(line, m.width)))
	}
	m.spliceScrollbar(rows, len(header), body)
	rows = append(rows, mention...)
	rows = append(rows, command...)
	rows = append(rows, composer...)
	rows = append(rows, m.statusLineRows(page+1, pages)...)
	return strings.Join(rows, "\n")
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

// windowList keeps the cursor visible in a body-height window.
func windowList(total, cursor, visible int) (count, start int) {
	visible = max(1, visible)
	start = max(0, min(cursor-visible/2, total-visible))
	count = min(total-start, visible)
	return count, start
}

// dialogView renders the shared selection dialog over the dimmed main UI:
// the conversation is drawn first, everything outside the centered selection
// box is dimmed, and the box itself renders at full intensity on top. The
// base rows around the box are preserved — only the box's own region is
// overlaid, so the conversation stays visible to its left and right.
func (m *ui) dialogView() string {
	base := strings.Split(m.mainView(), "\n")
	for i := range base {
		base[i] = dimRow(base[i])
	}
	width, height := m.width, m.height
	var title, hint string
	switch m.dialog.kind {
	case dialogThemes:
		title, hint = "Theme selection", "↑/↓ navigate  Enter apply  Esc cancel"
	case dialogComposer:
		title, hint = "Composer selection", "↑/↓ navigate  Enter apply  Esc cancel"
	case dialogModels:
		title, hint = "Model selection", "↑/↓ navigate  PgUp/PgDn page  Enter switch  Esc cancel"
	case dialogSessions:
		title, hint = "Session selection", "↑/↓ navigate  PgUp/PgDn page  Enter resume  Esc cancel"
	case dialogProviders:
		title, hint = "Provider selection", "↑/↓ navigate  PgUp/PgDn page  Enter select  Esc cancel"
	}
	items := m.dialogItems
	matches := m.dialogMatches()
	if m.dialog.loading {
		// The list is still in flight: no per-item labels exist to filter or
		// label, so matches must not index the stale previous list.
		items = []string{"Fetching model list…"}
		matches = nil
	}
	// The visible rows are the query-filtered subset; the cursor indexes
	// into it, so the highlighted row must be resolved through the matches.
	if m.dialog.query != "" {
		title = "q: \"" + m.dialog.query + "\"" + title
	}
	visibleItems := make([]string, len(matches))
	for i, orig := range matches {
		visibleItems[i] = m.dialogLabel(orig, items)
	}
	// Model rows carry the provider name right-aligned on the same row —
	// model left, provider right, like a flex justify-between container.
	providerName := ""
	if m.dialog.kind == dialogModels && !m.dialog.loading && m.dialog.loadErr == "" {
		providerName = m.conn.provider
	}
	providerW := runewidth.StringWidth(providerName)

	// Box width tracks the widest item so nothing is truncated; padding and
	// borders account for the two-space gutter. Measure by display width,
	// not len(): `len` counts UTF-8 bytes, so a CJK label (3 bytes/cell)
	// inflates the box and a long label can even shift the box off-center.
	boxWidth := runewidth.StringWidth(title)
	for _, item := range visibleItems {
		w := runewidth.StringWidth(item) + 12
		if providerName != "" {
			w += providerW + 2
		}
		if w > boxWidth {
			boxWidth = w
		}
	}
	if w := runewidth.StringWidth(hint) + 2; w > boxWidth {
		boxWidth = w
	}
	boxWidth = min(width-4, boxWidth+4)
	inner := boxWidth - 4 // "│ " + content + " │"
	if providerName != "" && inner < providerW+10 {
		// Too narrow to fit model and provider meaningfully: drop the column.
		providerName = ""
		providerW = 0
	}

	// Every content row is fitted to the inner width BEFORE styling; a style
	// is never applied over an escape sequence and never re-fitted after.
	var content []string
	content = append(content, m.theme.Title.Render(fit(title, inner)))
	content = append(content, fit("", inner))
	if m.dialog.loadErr != "" {
		for _, line := range wrap("Error: "+m.dialog.loadErr, inner) {
			content = append(content, m.theme.Error.Render(fit(line, inner)))
		}
	} else if m.dialog.loading {
		content = append(content, m.theme.Muted.Render(fit("Fetching model list…", inner)))
	} else if len(visibleItems) == 0 {
		// Empty match set under a query: an explicit row instead of a blank
		// box; Enter is a no-op while nothing is selectable.
		content = append(content, m.theme.Muted.Render(fit("No "+m.dialogKindName()+" match \""+m.dialog.query+"\"", inner)))
	} else {
		visible := max(1, height-10)
		windowed, start := windowList(len(visibleItems), m.dialog.cursor, visible)
		for i := start; i < start+windowed; i++ {
			label := visibleItems[i]
			if providerName != "" {
				// Model left, provider right: the left segment is fitted to
				// leave exactly the two-space gap plus the provider, so the
				// row totals the inner width and the provider is flush right.
				marker := "  "
				if i == m.dialog.cursor {
					marker = "> "
				}
				left := fit(marker+label, inner-providerW-2)
				if i == m.dialog.cursor {
					content = append(content, m.theme.Selected.Render(left)+m.theme.Muted.Render("  "+providerName))
				} else {
					content = append(content, left+m.theme.Muted.Render("  "+providerName))
				}
				continue
			}
			if i == m.dialog.cursor {
				content = append(content, m.theme.Selected.Render(fit("> "+label, inner)))
			} else {
				content = append(content, fit("  "+label, inner))
			}
		}
	}
	content = append(content, fit("", inner))
	content = append(content, m.theme.Help.Render(fit(hint, inner)))

	top := max(0, (height-len(content)-2)/2)
	left := max(2, (width-boxWidth)/2)
	// Compose each overlaid row from the dimmed base text left of the box,
	// the full-intensity box row, and the dimmed base text right of it — the
	// background keeps its content instead of collapsing to a solid band.
	for j := 0; j < len(content)+2 && top+j < height; j++ {
		var box string
		switch {
		case j == 0:
			box = m.theme.Border.Render("╭" + strings.Repeat("─", boxWidth-2) + "╮")
		case j == len(content)+1:
			box = m.theme.Border.Render("╰" + strings.Repeat("─", boxWidth-2) + "╯")
		default:
			box = m.theme.Border.Render("│ ") + content[j-1] + m.theme.Border.Render(" │")
		}
		base[top+j] = spliceRow(stripANSI(base[top+j]), left, boxWidth, box)
	}
	return strings.Join(base[:height], "\n")
}

// dimRow wraps one rendered row in ANSI faint, re-applying the attribute after
// every embedded reset so the dim survives nested color spans.
func dimRow(row string) string {
	dimmed := strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m\x1b[2m")
	return "\x1b[2m" + strings.TrimSuffix(dimmed, "\x1b[2m") + "\x1b[0m"
}

// spliceRow replaces the display-cell range [left, left+boxWidth) of a plain
// row with the given rendered box segment; the surrounding text stays dimmed
// like the rest of the background.
func spliceRow(plain string, left, boxWidth int, box string) string {
	l, rest := splitAtWidth(plain, left)
	_, right := splitAtWidth(rest, boxWidth)
	return dimRow(l) + box + dimRow(right)
}

// stripANSI removes SGR escape sequences, leaving the plain text of a
// rendered row. Styles never alter visible widths, so the result stays
// width-exact.
func stripANSI(s string) string {
	var b strings.Builder
	escaping := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			escaping = true
		case escaping:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\\' {
				escaping = false // final byte of the sequence
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitAtWidth splits plain text at a display-column boundary without
// cutting a wide rune.
func splitAtWidth(s string, col int) (left, right string) {
	if col <= 0 {
		return "", s
	}
	width := 0
	for i, r := range s {
		if width >= col {
			return s[:i], s[i:]
		}
		width += runewidth.RuneWidth(r)
	}
	return s, ""
}

// dialogLabel renders one dialog row with dialog-specific context markers.
func (m *ui) dialogLabel(i int, items []string) string {
	label := items[i]
	switch m.dialog.kind {
	case dialogThemes:
		if label == m.themeName {
			label += " (current)"
		}
	case dialogComposer:
		if label == m.composerStyle {
			label += " (current)"
		}
	case dialogModels:
		if label == m.modelName {
			label += " (current)"
		}
	}
	return label
}

// dialogKindName names the row kind for the empty-match message.
func (m *ui) dialogKindName() string {
	switch m.dialog.kind {
	case dialogThemes:
		return "themes"
	case dialogComposer:
		return "composer styles"
	case dialogModels:
		return "models"
	case dialogSessions:
		return "sessions"
	case dialogProviders:
		return "providers"
	}
	return "items"
}

func (m *ui) frame(content []string) string {
	rows := make([]string, m.height)
	for i := range rows {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		rows[i] = fit(line, m.width)
	}
	return strings.Join(rows, "\n")
}

// wrap makes untrusted control and zero-width characters visible before
// breaking text into display-width-bounded lines.
func wrap(text string, width int) []string {
	var lines []string
	var line strings.Builder
	columns := 0
	flush := func() {
		lines = append(lines, line.String())
		line.Reset()
		columns = 0
	}
	for _, r := range text {
		if r == '\n' {
			flush()
			continue
		}
		if hiddenReviewRune(r) {
			escaped := fmt.Sprintf("\\u%04X", r)
			for _, c := range escaped {
				if columns+1 > width && columns > 0 {
					flush()
				}
				line.WriteRune(c)
				columns++
			}
			continue
		}
		size := runewidth.RuneWidth(r)
		if columns+size > width && columns > 0 {
			flush()
		}
		line.WriteRune(r)
		columns += size
	}
	flush()
	return lines
}

func hiddenReviewRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || runewidth.RuneWidth(r) == 0
}

func fit(text string, width int) string {
	var b strings.Builder
	columns := 0
	for _, r := range text {
		if hiddenReviewRune(r) {
			for _, c := range fmt.Sprintf("\\u%04X", r) {
				if columns+1 > width {
					break
				}
				b.WriteRune(c)
				columns++
			}
			continue
		}
		size := runewidth.RuneWidth(r)
		if columns+size > width {
			break
		}
		b.WriteRune(r)
		columns += size
	}
	if columns < width {
		b.WriteString(strings.Repeat(" ", width-columns))
	}
	return b.String()
}
