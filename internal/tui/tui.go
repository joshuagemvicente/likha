package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"

	"lisa/internal/agent"
	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/repository"
	"lisa/internal/session"
	lisaui "lisa/internal/ui"
	"lisa/internal/update"
)

// Version is set from the release tag when building distribution binaries.
var Version = "dev"

// const logo = ` _     ___ ____    _
// | |   |_ _/ ___|  / \
// | |    | |\___ \ / _ \
// | |___ | | ___) / ___ \
// |_____|___|____/_/   \_\`

const logo = ` ____   ___  __ ___ __ __  _____ 
/  _/  /___\|  |  //  |  \/  _  \
|  |---|   ||  _ < |  _  ||  _  |
\_____/\___/|__|__\\__|__/\__|__/`

// minWidth is the smallest terminal the conversation view will render;
// smaller viewports get an inline hint instead of broken layout.
const minWidth, minHeight = 40, 12

// contentWidth is the transcript's wrap width: the viewport minus two
// padding columns. Popups and the composer keep the full width. The
// transcript wraps to the full content width at every terminal size — no
// capped measure — so text reaches the right edge on wide terminals instead
// of stopping short of it.
func contentWidth(termWidth int) int {
	return max(1, termWidth-2)
}

type ui struct {
	root, modelName   string
	conn              providers.Connection
	stateDir          string
	repo              *repository.Repository
	client            *model.Client
	theme             lisaui.Theme
	glyphs            lisaui.Glyphs
	themeName         string
	composerStyle     string
	width, height     int
	statusLineOpts    providers.StoredStatusLineConfig
	statusFolder      string
	statusTitle       string
	entries           []entry
	input             []rune
	edit              editState // composer cursor + kill ring (specs/tool-rendering-terminal-keys)
	escPrefix         bool      // armed after ESC: Return inside the decay window inserts a newline
	lines             []string
	lineStyles        []lipgloss.Style
	lineSpans         [][]lisaui.Swatch
	streamBuf         strings.Builder
	reasoningBuf      strings.Builder
	reasoningStream   int
	layoutWidth       int
	scroll            int  // top body line of the viewport; scrollMax() pins it to the newest content
	following         bool // true: the viewport follows new lines like a chat/browser at the bottom
	caretOn           bool // blink phase of the composer's block caret
	caretTyped        bool // recent keystroke: caret renders solid until the blink resumes
	streaming         int
	working           bool
	cancelling        bool
	runID             uint64
	cancel            context.CancelFunc
	events            chan agent.TurnEvent
	abandon           chan struct{}
	history           []model.Message
	store             *session.Store
	snapshot          session.Snapshot
	pending           *agent.ApprovalRequest
	reviewSeen        []bool
	status            string
	mode              string
	setup             setupState
	lastModels        []string     // numbered list shown by /models
	sessionIDs        []string     // ids from the last /sessions listing
	mention           mentionState // @file completion popup state
	commandPopup      commandState // /command completion popup state
	dialogItems       []string     // display rows for the open dialog (theme names, session titles; /models uses dialogModelRows)
	dialog            dialogState
	dialogModelRows   []modelsRow              // parallel selectable rows for the /models dialog; headers render from sections, never rows
	dialogModelCounts map[string]int           // model id → occurrence count across sections; >1 rows render the id with its provider name
	dialogModelsNote  string                   // muted unreachable-provider note for the /models dialog; "" when every fetch succeeded
	modelsCache       modelsCache              // in-memory last-good sections (specs/models-perf); dies with the process
	modelsGen         uint64                   // /models open generation; per-section arrivals carry it, stale ones drop
	modelsPending     int                      // outstanding per-provider fetches in the current open
	modelsNavigated   bool                     // user navigated/typed in this open; arrivals must not yank the cursor after
	modelsArrived     map[string]modelsSection // arrived sections by provider canonical Name
	modelsTargetOrder []string                 // target provider Names in final display order
	modelsFailures    []string                 // failed provider display names this open
	modelsErrSample   string                   // first fetch error text this open
	keyModal          keyState                 // /providers: provider auth overlay (key-entry modal and auth-state view)

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

// dialogMatches documents the shared selection-dialog filter: a
// case-insensitive substring match on the display text, which for providers
// embeds both the display name and the canonical name. The /models dialog
// filters its selectable rows over model id plus provider names instead
// (see modelMatches). An empty query matches everything. The cursor indexes
// into this filtered list, so any query change moves the cursor to the
// first visible match (filtering never re-selects the active row).

func NewUI(root string, repo *repository.Repository, client *model.Client, name string, conn providers.Connection, stateDir string, store *session.Store, snapshot session.Snapshot) *ui {
	themeName := conn.Theme
	dark := lisaui.HasDarkBackground()
	theme := lisaui.Resolve(themeName, dark)
	glyphs := lisaui.AsciiGlyphs()
	if conn.Nerd {
		glyphs = lisaui.NerdGlyphs()
	}
	m := &ui{root: root, repo: repo, client: client, modelName: name, conn: conn, stateDir: stateDir, store: store, snapshot: snapshot, history: snapshot.History, following: true, caretOn: true, streaming: -1, status: "Connected", mode: modeMain, theme: theme, glyphs: glyphs, themeName: themeName, composerStyle: validComposerStyle(conn.ComposerStyle), statusLineOpts: conn.StatusLine, started: time.Now(), freshSession: len(snapshot.Entries) == 0}
	if providers.FlagEnabled(m.statusLineOpts.Folder) {
		m.statusFolder = statusFolder(root)
	}
	if m.statusLineOpts.Changes || m.statusLineOpts.Staged || providers.FlagEnabled(m.statusLineOpts.Branch) {
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
	if conn.Setup {
		m.mode = modeSetup
		m.setup.stage = setupProvider
		m.status = "First-run setup"
		return m
	}
	if conn.Err != nil {
		m.status = "Not connected"
		m.entries = append(m.entries, entry{role: "Error", content: "Startup connection check failed: " + conn.Err.Error()})
	}
	for _, saved := range snapshot.Entries {
		m.entries = append(m.entries, entry{role: saved.Role, content: saved.Content})
	}
	if len(snapshot.Entries) > 0 && conn.Err == nil {
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

func waitEvent(events <-chan agent.TurnEvent) tea.Cmd {
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
	m.events = make(chan agent.TurnEvent, 64)
	m.abandon = make(chan struct{})
	m.runID++
	runID := m.runID
	events := m.events
	abandon := m.abandon
	client, repo, root, mcp := m.client, m.repo, m.root, m.conn.Mcp
	go func() {
		defer close(events)
		agent.RunTurn(ctx, client, repo, root, prior, prompt, mcp, func(ev agent.TurnEvent) {
			ev.RunID = runID
			if ev.Kind == "done" || ev.Kind == "error" || ev.Kind == "tool_result" {
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
// conversation while it runs; the deliverable is a single agent.TurnEvent.
func (m *ui) startCompaction(focus string) tea.Cmd {
	m.status = "Compacting…"
	m.working = true
	m.jumpBottom()
	m.layoutWidth = 0
	m.input = nil
	m.edit.endCaret(m.input)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan agent.TurnEvent, 64)
	m.abandon = make(chan struct{})
	m.runID++
	runID := m.runID
	events := m.events
	abandon := m.abandon
	client, history := m.client, m.history
	go func() {
		defer close(events)
		newHistory, summary, err := agent.CompactHistory(ctx, client, history, focus, nil)
		ev := agent.TurnEvent{RunID: runID}
		if err != nil {
			// The failed call never touched the caller's history; carrying
			// it in the event leaves the error handler's history swap a
			// no-op.
			ev.Kind, ev.Text, ev.History = "error", err.Error(), history
		} else {
			ev.Kind, ev.Text, ev.History = "compacted", summary, newHistory
		}
		select {
		case events <- ev:
		case <-abandon:
		}
	}()
	return waitEvent(events)
}

// previewTheme re-resolves the live styles to a candidate family without
// touching the committed name, config, or transcript: Esc restores.
func (m *ui) previewTheme(themeName string) {
	m.theme = lisaui.Resolve(themeName, lisaui.HasDarkBackground())
	m.layoutWidth = 0
}

// applyTheme switches the live theme and stores it for later runs. Does not
// close the modal by itself.
func (m *ui) applyTheme(themeName string) {
	m.themeName = themeName
	m.previewTheme(themeName)
	if cfg, err := providers.LoadStoredConfig(m.stateDir); err == nil && cfg.Provider != "" {
		cfg.Theme = themeName
		if err := providers.SaveStoredConfig(m.stateDir, cfg); err != nil {
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

// modelsListMsg carries one provider's model list for the setup-adjacent
// paths. The /models dialog itself consumes per-section modelsSectionMsg
// values instead (specs/models-perf).
type modelsListMsg struct {
	models []string
	err    error
}

func (m *ui) handleModelsResult(msg modelsListMsg) {
	m.layoutWidth = 0
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
	if cfg, err := providers.LoadStoredConfig(m.stateDir); err == nil && cfg.Provider != "" {
		cfg.Model = id
		if err := providers.SaveStoredConfig(m.stateDir, cfg); err != nil {
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
	case agent.TurnEvent:
		if !m.working || v.RunID != m.runID {
			return m, nil
		}
		switch v.Kind {
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
			m.reasoningBuf.WriteString(v.Text)
			m.entries[m.reasoningStream].content = m.reasoningBuf.String()
		case "text":
			m.reasoningStream = -1 // content after thinking closes the reasoning stream
			if m.streaming < 0 {
				m.entries = append(m.entries, entry{role: "Assistant"})
				m.streaming = len(m.entries) - 1
			}
			m.streamBuf.WriteString(v.Text)
			m.entries[m.streaming].content = m.streamBuf.String()
		case "approval":
			if m.cancelling {
				break
			}
			m.pending = v.Approval
			m.scroll = 0 // review starts at the top of the proposal screen
			m.following = false
			m.layoutWidth = 0
			m.reviewSeen = make([]bool, m.pageCount())
			m.markSeenFromScroll()
			m.status = "Review " + m.pending.Kind + " before approval"
		case "tool_start", "tool_result":
			m.streamBuf.Reset()
			m.streaming = -1
			m.entries = append(m.entries, entry{role: "Tool", content: v.Text})
			if m.cancelling {
				m.status = "Cancelling"
			} else if v.Kind == "tool_start" {
				m.status = "Reading repository"
			} else {
				m.status = "Waiting for model"
			}
			if v.Kind == "tool_result" {
				if m.statusLineOpts.Changes || m.statusLineOpts.Staged || providers.FlagEnabled(m.statusLineOpts.Branch) {
					m.git, m.gitOK = gitStatus(m.root)
				}
				m.history = v.History
				m.persist()
			}
		case "compacted":
			// The single summarize call finished: swap the summarized turns
			// for the brief and mark the point in the conversation.
			m.history = v.History
			m.entries = append(m.entries, entry{role: "Lisa", content: "Conversation compacted. Summary of earlier turns:\n\n" + v.Text})
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
			m.history = v.History
			m.working = false
			if m.cancel != nil {
				m.cancel()
			}
			m.cancel = nil
			if m.streaming >= 0 && v.Kind == "error" {
				m.entries = append(m.entries[:m.streaming], m.entries[m.streaming+1:]...)
			}
			m.streaming = -1
			if m.cancelling {
				for _, message := range v.History {
					if message.Role == "tool" && message.Content == "Error: action not executed; run interrupted" {
						m.entries = append(m.entries, entry{role: "Tool", content: message.Content})
					}
				}
			}
			if m.cancelling {
				m.status = "Cancelled"
				m.entries = append(m.entries, entry{role: "Lisa", content: "Run cancelled; no further tools will execute. Approved shell commands may leave detached processes running."})
			} else if v.Kind == "error" {
				m.status = "Error"
				m.entries = append(m.entries, entry{role: "Error", content: v.Text})
			} else {
				m.status = "Ready"
			}
			if v.Kind == "done" && m.client != nil {
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
					if m.conn.ProviderCanonical == "chatgpt" {
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
			if cmd := m.autoNameAfterFirstTurn(v.Kind); cmd != nil {
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
	case modelsSectionMsg:
		if m.mode != modeMain {
			return m, nil
		}
		m.handleModelsSection(v)
		return m, nil
	case modelsProviderFailedMsg:
		if m.mode != modeMain {
			return m, nil
		}
		m.handleModelsProviderFailed(v)
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
		if _, active := agent.MentionQuery(string(m.input)); active {
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
		name, err := agent.GenerateSessionName(ctx, client, history)
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

func windowList(total, cursor, visible int) (count, start int) {
	visible = max(1, visible)
	start = max(0, min(cursor-visible/2, total-visible))
	count = min(total-start, visible)
	return count, start
}
