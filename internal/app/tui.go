package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"lisa/internal/model"
	"lisa/internal/repository"
	"lisa/internal/session"
)

const minWidth, minHeight = 40, 12

var (
	headingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	statusStyle  = lipgloss.NewStyle().Bold(true)
)

// First-run setup stages.
const (
	setupProvider = iota
	setupKey
	setupChecking
	setupModel
)

const (
	modeMain  = "main"
	modeSetup = "setup"
)

type setupState struct {
	stage       int
	cursor      int
	keyInput    []rune
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
	width, height   int
	entries         []entry
	input           []rune
	lines           []string
	streamBuf       strings.Builder
	layoutWidth     int
	page            int // -1 follows the newest page; other values are fixed page numbers.
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
}

func newUI(root string, repo *repository.Repository, client *model.Client, name string, conn connection, stateDir string, store *session.Store, snapshot session.Snapshot) *ui {
	m := &ui{root: root, repo: repo, client: client, modelName: name, conn: conn, stateDir: stateDir, store: store, snapshot: snapshot, history: snapshot.History, page: -1, streaming: -1, status: "Connected"}
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
	if len(m.entries) > 0 && conn.err == nil {
		m.status = "Resumed session"
	}
	return m
}

func (m *ui) Init() tea.Cmd { return nil }

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
		case setupModel:
			m.setup.stage = setupKey
			m.setup.err, m.setup.models, m.setup.modelCursor = "", nil, 0
		case setupChecking:
			// A late check result is ignored once the stage moves on.
			m.setup.stage = setupKey
			m.setup.checking = false
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
			m.setup.stage = setupKey
			m.setup.keyInput = nil
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
	body := m.bodyHeight() - 3 // title, blank, hint rows around the list.
	return max(1, body)
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

// finishSetup builds the client from the chosen provider/model, stores the
// key and choice, and switches to the conversation view.
func (m *ui) finishSetup(modelID string) tea.Cmd {
	p := model.Providers[m.setup.cursor]
	key := strings.TrimSpace(string(m.setup.keyInput))
	client, err := model.New(p.BaseURL, modelID, key)
	if err != nil {
		m.setup.err = err.Error()
		return nil
	}
	if key != "" && p.Hosted {
		if err := storeKey(m.stateDir, p.Name, key); err != nil {
			m.setup.err = err.Error()
			return nil
		}
	}
	if err := saveStoredConfig(m.stateDir, storedProviderConfig{Provider: p.Name, Model: modelID}); err != nil {
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
	m.conn = connection{provider: p.DisplayName, verified: true}
	m.mode = modeMain
	m.status = "Connected"
	m.layoutWidth = 0
	return nil
}

func waitEvent(events <-chan turnEvent) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func (m *ui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.layoutWidth = 0
		if m.pending != nil {
			m.page = 0
			m.reviewSeen = make([]bool, m.pageCount())
			if m.width >= minWidth && m.height >= minHeight {
				m.reviewSeen[0] = true
			}
		}
		return m, nil
	case turnEvent:
		if !m.working || v.runID != m.runID {
			return m, nil
		}
		switch v.kind {
		case "text":
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
			m.page = 0
			m.layoutWidth = 0
			m.reviewSeen = make([]bool, m.pageCount())
			if m.width >= minWidth && m.height >= minHeight {
				m.reviewSeen[0] = true
			}
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
				m.history = v.history
				m.persist()
			}
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
			m.cancelling = false
			m.persist()
		}
		m.layoutWidth = 0
		if m.working {
			return m, waitEvent(m.events)
		}
		return m, nil
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
	case tea.KeyMsg:
		if m.mode == modeSetup {
			return m.updateSetup(v)
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
				m.page = -1
				m.layoutWidth = 0
				return m, nil
			case "n", "N":
				m.pending.Reply <- false
				m.status = "Rejected"
				m.pending = nil
				m.reviewSeen = nil
				m.page = -1
				m.layoutWidth = 0
				return m, nil
			}
		}
		switch v.String() {
		case "ctrl+c", "esc":
			if m.working {
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
			m.input = nil
		case "ctrl+d":
			if m.cancel != nil {
				if m.abandon != nil {
					close(m.abandon)
				}
				m.cancel()
			}
			return m, tea.Quit
		case "pgup", "ctrl+p":
			pages := m.pageCount()
			if m.page < 0 {
				m.page = pages - 1
			}
			if m.page > 0 {
				m.page--
			}
			m.markReviewPage()
		case "pgdown", "ctrl+n":
			if m.page >= 0 {
				m.page++
				if m.page >= m.pageCount()-1 {
					m.page = -1
				}
			}
			m.markReviewPage()
		case "backspace", "ctrl+h":
			if !m.working && len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case "enter":
			if m.working || m.width < minWidth || m.height < minHeight {
				return m, nil
			}
			prompt := strings.TrimSpace(string(m.input))
			if prompt == "" {
				return m, nil
			}
			m.input = nil
			prior := m.history
			m.entries = append(m.entries, entry{role: "You", content: prompt})
			m.history = append(m.history, model.Message{Role: "user", Content: prompt})
			m.persist()
			m.streamBuf.Reset()
			m.streaming = -1
			m.status = "Waiting for model"
			m.working = true
			m.page = -1
			m.layoutWidth = 0
			ctx, cancel := context.WithCancel(context.Background())
			m.cancel = cancel
			m.events = make(chan turnEvent, 64)
			m.abandon = make(chan struct{})
			m.runID++
			runID := m.runID
			events := m.events
			abandon := m.abandon
			go func() {
				defer close(events)
				runTurn(ctx, m.client, m.repo, m.root, prior, prompt, func(ev turnEvent) {
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
			return m, waitEvent(events)
		default:
			if !m.working && m.pending == nil {
				if v.Type == tea.KeyRunes {
					m.input = append(m.input, v.Runes...)
				} else if v.Type == tea.KeySpace {
					m.input = append(m.input, ' ')
				}
			}
		}
		m.layoutWidth = 0
	}
	return m, nil
}

func (m *ui) persist() {
	if m.store == nil {
		return
	}
	m.snapshot.History = m.history
	m.snapshot.Entries = make([]session.Entry, len(m.entries))
	for i, current := range m.entries {
		m.snapshot.Entries[i] = session.Entry{Role: current.role, Content: current.content}
	}
	if err := m.store.Save(m.snapshot); err != nil {
		m.status = "Session save failed"
		m.entries = append(m.entries, entry{role: "Error", content: "Session was not saved: " + err.Error()})
	}
}

func (m *ui) markReviewPage() {
	if m.pending == nil || m.width < minWidth || m.height < minHeight {
		return
	}
	page := m.page
	if page < 0 {
		page = len(m.reviewSeen) - 1
	}
	if page >= 0 && page < len(m.reviewSeen) {
		m.reviewSeen[page] = true
	}
}

func (m *ui) providerLine() string {
	state := "accepted"
	if !m.conn.verified {
		state = "unverified"
	}
	return "Provider: " + m.conn.provider + " (" + state + ")"
}

func (m *ui) header() []string {
	if m.pending == nil && m.width >= 56 && m.height >= 19 {
		parts := strings.Split(logo, "\n")
		return append(parts, "Repository: "+m.root, m.providerLine(), "Model: "+m.modelName)
	}
	return []string{"Lisa  |  Repo: " + m.root, m.providerLine(), "Model: " + m.modelName}
}

func (m *ui) bodyHeight() int {
	if m.pending == nil && m.width >= 56 && m.height >= 19 {
		return m.height - 12 // five logo rows, repository/provider/model, four footer rows.
	}
	return m.height - 7 // compact header and four footer rows.
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
func (m *ui) rebuild() {
	if m.layoutWidth == m.width && m.lines != nil {
		return
	}
	m.lines = m.lines[:0]
	width := max(1, m.width-2)
	if m.pending != nil {
		m.lines = append(m.lines, wrap(m.pending.Title, width)...)
		if m.pending.Kind == "command" {
			m.lines = append(m.lines, "WARNING: No filesystem/network sandbox")
			m.lines = append(m.lines, "WARNING: Detached jobs may survive")
		}
		m.lines = append(m.lines, "")
		m.lines = append(m.lines, wrap(m.pending.Body, width)...)
	} else {
		header := m.header()
		if runewidth.StringWidth(header[len(header)-3]) > m.width {
			m.lines = append(m.lines, wrap("Repository: "+m.root, width)...)
		}
		if runewidth.StringWidth(header[len(header)-2]) > m.width {
			m.lines = append(m.lines, wrap(m.providerLine(), width)...)
		}
		if runewidth.StringWidth(header[len(header)-1]) > m.width {
			m.lines = append(m.lines, wrap("Model: "+m.modelName, width)...)
		}
		for _, e := range m.entries {
			m.lines = append(m.lines, wrap(e.role+": "+e.content, width)...)
			m.lines = append(m.lines, "")
		}
		if len(m.input) > 0 {
			m.lines = append(m.lines, wrap("Draft: "+string(m.input), width)...)
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
	m.rebuild()
	header := m.header()
	body := m.bodyHeight()
	pages := m.pageCount()
	page := m.page
	if page < 0 || page >= pages {
		page = pages - 1
	}
	rows := make([]string, 0, m.height)
	for i, line := range header {
		if i == 0 || (len(header) > 2 && i < 5) {
			rows = append(rows, headingStyle.Render(fit(line, m.width)))
		} else {
			rows = append(rows, fit(line, m.width))
		}
	}
	start := page * body
	for i := range body {
		line := ""
		if start+i < len(m.lines) {
			line = m.lines[start+i]
		}
		rows = append(rows, fit(line, m.width))
	}
	rows = append(rows, strings.Repeat("─", m.width))
	status := fmt.Sprintf("%s  |  Page %d/%d", m.status, page+1, pages)
	actions := "Type prompt  Enter send  Ctrl+D quit"
	navigation := "PgUp/PgDn pages  Ctrl+C cancel/quit"
	if m.pending != nil {
		kind := "EDIT REVIEW"
		if m.pending.Kind == "command" {
			kind = "COMMAND REVIEW"
		}
		status = fmt.Sprintf("%s  |  Page %d/%d", kind, page+1, pages)
		if m.status == "Review every page before approving" {
			label := strings.TrimSuffix(kind, " REVIEW")
			status = fmt.Sprintf("%s | Page %d/%d | Read all pages", label, page+1, pages)
		}
		actions = "Y approve after review  N reject"
		navigation = "PgUp/PgDn pages  Esc/Ctrl+C cancel"
	}
	rows = append(rows, statusStyle.Render(fit(status, m.width)))
	rows = append(rows, fit(actions, m.width))
	rows = append(rows, fit(navigation, m.width))
	return strings.Join(rows, "\n")
}

func (m *ui) setupView() string {
	body := max(1, m.height-6) // compact header, blank separator, three footer rows.
	rows := make([]string, 0, m.height)
	rows = append(rows, headingStyle.Render(fit("Lisa  |  First-run setup  |  Repo: "+m.root, m.width)))
	rows = append(rows, fit("", m.width))
	content := m.setupLines(body)
	for _, line := range content {
		rows = append(rows, fit(line, m.width))
	}
	rows = append(rows, strings.Repeat("─", m.width))
	status := "SETUP"
	if m.setup.checking {
		status = "SETUP  |  Checking provider…"
	} else if m.setup.err != "" {
		status = "SETUP  |  Check failed"
	}
	rows = append(rows, statusStyle.Render(fit(status, m.width)))
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
