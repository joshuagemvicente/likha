package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"likha/internal/agent"
	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/profiles"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
	"likha/internal/tooloutput"
	"likha/internal/tools"
	"likha/internal/transcript"
	likhaui "likha/internal/ui"
	"likha/internal/update"
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

// Review decision focus (FR-22): the index of the focused button on the
// decision bar. A new approval always starts on Approve.
const focusApprove = 0
const focusDecline = 1

// workingLabel is the fixed generic activity text. It never claims to reveal
// model reasoning; spinner frames remain literal ASCII in every terminal.
const workingLabel = "Working…"

var workingSpinnerFrames = []string{"|", "/", "-", "\\"}

// contentWidth is the transcript's wrap width: the viewport minus two
// padding columns. Popups and the composer keep the full width. The
// transcript wraps to the full content width at every terminal size — no
// capped measure — so text reaches the right edge on wide terminals instead
// of stopping short of it.
func contentWidth(termWidth int) int {
	return max(1, termWidth-2)
}

type ui struct {
	root, modelName    string
	conn               providers.Connection
	stateDir           string
	repo               *repository.Repository
	client             *model.Client
	theme              likhaui.Theme
	glyphs             likhaui.Glyphs
	blocks             likhaui.BlockGlyphs // transcript block glyphs; ASCII under --ascii / LIKHA_ASCII
	themeName          string
	composerStyle      string
	width, height      int
	statusLineOpts     providers.StoredStatusLineConfig
	statusFolder       string
	statusTitle        string
	entries            []entry
	input              []rune
	edit               editState // composer cursor + kill ring (specs/tool-rendering-terminal-keys)
	composerVertical   composerVerticalMover
	selection          textSelectionState // transient mouse selection; never persisted or sent to the model
	clipboard          clipboardState     // asynchronous system clipboard writes and transient feedback
	escPrefix          bool               // armed after ESC: Return inside the decay window inserts a newline
	lines              []string
	lineStyles         []lipgloss.Style
	lineSpans          [][]likhaui.Swatch
	lineActivity       []bool                             // marks the ephemeral activity row for render-time sweep styling
	lineRuns           [][]lineRun                        // styled runs per tool and markdown row (status dot, bold name, markdown spans); nil elsewhere
	entryLines         []int                              // first layout line of each entry, recorded by rebuild for focus/scroll math
	markdownCache      map[markdownCacheKey]markdownEntry // assistant rows from the last layout, reused while unchanged
	highlightCache     map[highlightKey]highlightResult   // code highlights from the last layout, reused across widths and themes
	highlightPending   bool                               // the last layout left code plain for want of highlight budget
	streamBuf          strings.Builder
	reasoningBuf       strings.Builder
	reasoningStream    int
	layoutWidth        int
	scroll             int  // top body line of the viewport; scrollMax() pins it to the newest content
	following          bool // true: the viewport follows new lines like a chat/browser at the bottom
	caretOn            bool // blink phase of the composer's block caret
	caretTyped         bool // recent keystroke: caret renders solid until the blink resumes
	streaming          int
	activity           int    // entries index of ephemeral Working row; -1 when absent
	activityFrame      int    // spinner and sweep frame advanced by activity ticks
	activityGeneration uint64 // invalidates ticks from an earlier activity interval
	working            bool
	cancelling         bool
	planMode           bool // live per-session read-only state; never persisted
	runID              uint64
	cancel             context.CancelFunc
	events             chan agent.TurnEvent
	abandon            chan struct{}
	history            []model.Message
	promptHistory      promptHistory
	queue              []string       // steering prompts typed while a run is active; UI mirror of the delivery channel
	steer              chan string    // the active turn's steering input; nil while idle
	priorLen           int            // m.history length at the active run's start (turn or compaction); reconcile baseline
	turnSent           map[string]int // steer deliveries the UI processed this turn, by text
	store              *session.Store
	snapshot           session.Snapshot
	pending            *agent.ApprovalRequest
	reviewSeen         []bool
	reviewFocus        int // focused decision-bar button while a review is pending (focusApprove/focusDecline)
	status             string
	mode               string
	setup              setupState
	oauth              oauthFlowState
	lastModels         []string     // numbered list shown by /models
	sessionIDs         []string     // ids from the last /sessions listing
	mention            mentionState // @file completion popup state
	commandPopup       commandState // /command completion popup state
	dialogItems        []string     // display rows for the open dialog (theme names, session titles; /models uses dialogModelRows)
	dialog             dialogState
	dialogModelRows    []modelsRow              // parallel selectable rows for the /models dialog; headers render from sections, never rows
	dialogModelCounts  map[string]int           // model id → occurrence count across sections; >1 rows render the id with its provider name
	dialogModelsNote   string                   // muted unreachable-provider note for the /models dialog; "" when every fetch succeeded
	modelsCache        modelsCache              // in-memory last-good sections (specs/models-perf); dies with the process
	modelsGen          uint64                   // /models open generation; per-section arrivals carry it, stale ones drop
	modelsPending      int                      // outstanding per-provider fetches in the current open
	modelsNavigated    bool                     // user navigated/typed in this open; arrivals must not yank the cursor after
	modelsArrived      map[string]modelsSection // arrived sections by provider canonical Name
	modelsTargetOrder  []string                 // target provider Names in final display order
	modelsFailures     []string                 // failed provider display names this open
	modelsErrSample    string                   // first fetch error text this open
	keyModal           keyState                 // /providers: provider auth overlay (key-entry modal and auth-state view)

	// Session measurements and identity for the status line (spec §4).
	contextTokens         int64     // input-context estimate or provider-reported value
	contextSeen           bool      // context usage has a usable value
	contextEstimated      bool      // value is approximate rather than provider-reported
	contextWindow         int64     // selected model's resolved context limit
	contextWindowKnown    bool      // selected model's context limit is documented
	started               time.Time // session start; drives the minutes segment
	usagePrompt           int64     // cumulative prompt tokens across completed turns
	usageCompletion       int64     // cumulative completion tokens across completed turns
	lastPromptTokens      int64     // prompt tokens of the last completed turn (ctx %)
	usageSeen             bool      // provider usage reported at least once this session
	updateVersion         string    // newer release tag when known; empty = none
	git                   gitState  // last successful git status read; zero value (and !gitStatusOK) on failure
	gitOK                 bool      // last git status call succeeded; false hides every git segment
	spend                 float64   // accumulated session cost in US dollars
	spendKnown            bool      // pricing seen for at least one request (subscription rows included)
	spendEstimated        bool      // at least one priced request was a catalog estimate: spend renders "~$x.xx"
	runProvider           string    // canonical provider of the active run's client, captured at run start
	runModel              string    // model of the active run's client, captured at run start
	freshSession          bool      // started with no stored entries; gates the auto-naming run
	nameTried             bool      // the one auto-naming attempt already launched
	toolCatalog           []tools.CatalogEntry
	toolCatalogGen        uint64
	toolInspector         toolInspectionState
	toolRecords           []session.ToolRecord
	liveToolCalls         map[string]bool // this run's started calls still awaiting a result
	runToolCalls          map[string]bool // every call this run showed as a tool item
	approvalCall          string          // live call whose record reads "awaiting approval"
	activityArmed         uint64          // activityGeneration+1 of the tick in flight; 0 = none
	outputs               *tooloutput.Store
	agentInspector        agentInspectionState
	taskRecords           []explore.Record
	profileCatalog        profiles.Catalog // frozen at run boundaries/session switches; read-only in agents_profiles.go
	reportedProfileErrors string
	taskRuntime           *explore.Manager
	ask                   askState
	askSequence           uint64 // delivery identity counter under m.events broker
	consent               consentState
	reportedSkillErrors   string
	webGrants             map[string]bool // conversation-scoped web consents
	webConfigState        string          // last loaded web config; a change clears webGrants
	webConfigSeen         bool
	grantsMu              sync.Mutex // serializes UI writes with tool-goroutine reads

	// Explore/agent child usage already added to the token totals and spend:
	// task ID → the newest counted record version and its usage sums.
	taskUsageCounted map[string]countedTaskUsage

	// Edit diffs (spec transcript-redesign § Diffs). approvedEdits holds the
	// reviewed diff of each edit this run approved, by call ID, until the
	// call's result decides whether it was applied. editDiffCache keeps the
	// edit item diffs of the last layout, reused while unchanged;
	// editDiffNext collects the ones the layout in progress uses (nil
	// outside rebuild).
	approvedEdits map[string]string
	editDiffCache map[editDiffKey]transcript.DiffSummary
	editDiffNext  map[editDiffKey]transcript.DiffSummary

	// Reasoning blocks and turn footers (spec transcript-redesign §
	// Reasoning, § Turn footer; thought_items.go). Blocks key on their
	// Reasoning ordinal. thoughtDurations holds the durations measured in
	// this view; thoughtExpanded the markers expanded inline. Neither is
	// persisted: durations reach the session inside each Turn entry.
	clock            func() time.Time // nil: time.Now
	reasoningStarted time.Time        // open block's start; zero when none
	thoughtDurations map[int]time.Duration
	thoughtExpanded  map[int]bool
	toolExpanded     map[string]bool // tool items expanded inline, by call ID; never persisted
	turnStarted      time.Time       // current user turn's start; zero outside one (and for compaction)
	turnModel        string          // model the current user turn runs on
	turnThoughtBase  int             // reasoning ordinal of the current turn's first block
	turnToolItems    int             // top-level tool items the current turn opened
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
	dark := likhaui.HasDarkBackground()
	theme := likhaui.Resolve(themeName, dark)
	glyphs := likhaui.AsciiGlyphs()
	if conn.Nerd {
		glyphs = likhaui.NerdGlyphs()
	}
	m := &ui{root: root, repo: repo, client: client, modelName: name, conn: conn, stateDir: stateDir, store: store, snapshot: snapshot, history: snapshot.History, promptHistory: newPromptHistory(restoredPromptHistory(snapshot)), following: true, caretOn: true, streaming: -1, activity: -1, status: "Connected", mode: modeMain, theme: theme, glyphs: glyphs, blocks: likhaui.BlockGlyphSet(conn.ASCII), themeName: themeName, composerStyle: validComposerStyle(conn.ComposerStyle), statusLineOpts: conn.StatusLine, started: time.Now(), freshSession: len(snapshot.Entries) == 0}
	m.restoreTaskRecords()
	m.restorePlan()
	m.refreshProfileCatalog()
	m.reportUnansweredQuestions()
	m.reportSkillCatalogIssue()
	snapshot = m.snapshot
	m.resolveContextWindow()
	if len(m.history) > 0 {
		m.recalculateContext(m.history)
	}
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
		for i, p := range model.Providers {
			if p.Name == conn.ProviderCanonical {
				m.setup.cursor = i
				if p.Auth == model.AuthOAuth {
					m.setup.stage = setupLogin
				}
				break
			}
		}
		m.status = "First-run setup"
		return m
	}
	if conn.Err != nil {
		m.status = "Not connected"
		m.entries = append(m.entries, entry{role: "Error", content: "Startup connection check failed: " + conn.Err.Error()})
	}
	entryBase := len(m.entries)
	for _, saved := range snapshot.Entries {
		m.entries = append(m.entries, entry{role: saved.Role, content: saved.Content})
	}
	m.toolRecords = append([]session.ToolRecord(nil), snapshot.ToolRecords...)
	for i := range m.toolRecords {
		m.toolRecords[i].EntryIndex += entryBase
	}
	m.openOutputStore()
	m.reportInterruptedTasks()
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
	var login tea.Cmd
	if m.mode == modeSetup && m.setup.stage == setupLogin && !m.setup.checking {
		login = m.startOAuthLogin(model.Providers[m.setup.cursor])
	}
	shouldCheck, mark, err := update.Prepared(m.stateDir)
	if err != nil || !shouldCheck {
		// No update check due: only the caret blink loop runs.
		return tea.Batch(blinkCaret(), login)
	}
	return tea.Batch(blinkCaret(), login, func() tea.Msg {
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

func (m *ui) hasActivity() bool {
	for _, e := range m.entries {
		if e.role == "Working" {
			return true
		}
	}
	return false
}

// showActivity appends one temporary row while a turn is active. Scroll
// position is owned by submit/review interactions, never by animation.
func (m *ui) showActivity() {
	if !m.working || m.pending != nil {
		return
	}
	if m.hasActivity() {
		for i := range m.entries {
			if m.entries[i].role == "Working" {
				m.activity = i
				return
			}
		}
	}
	m.entries = append(m.entries, entry{role: "Working", content: workingLabel})
	m.activity = len(m.entries) - 1
	m.activityFrame = 0
	m.activityGeneration++
	m.layoutWidth = 0
}

// hideActivity removes the temporary row and repairs stream-buffer indices.
// Incrementing the generation invalidates any timer command already in flight.
func (m *ui) hideActivity() {
	idx := m.activity
	if idx < 0 {
		return
	}
	m.activityGeneration++
	if idx >= len(m.entries) || m.entries[idx].role != "Working" {
		idx = -1
		for i := range m.entries {
			if m.entries[i].role == "Working" {
				idx = i
				break
			}
		}
	}
	if idx >= 0 {
		m.entries = append(m.entries[:idx], m.entries[idx+1:]...)
		m.adjustToolRecordsAfterRemoval(idx)
		if m.streaming > idx {
			m.streaming--
		}
		if m.reasoningStream > idx {
			m.reasoningStream--
		}
		m.layoutWidth = 0
	}
	m.activity = -1
}

func (m *ui) activityTickCmd() tea.Cmd {
	m.activityArmed = m.activityGeneration + 1
	return activityTick(m.runID, m.activityGeneration)
}

// armActivityTick starts the clock for tool dots or model status unless a tick
// for the current generation is already in flight. hideActivity bumps the
// generation, so the Working row's retired clock never counts.
func (m *ui) armActivityTick() tea.Cmd {
	if !m.working || m.pending != nil || m.activityArmed == m.activityGeneration+1 || !m.toolBlinking() && !m.statusWorking() {
		return nil
	}
	return m.activityTickCmd()
}

// startTurn submits a user prompt to the model and enters the working state.
// queued pre-fills the fresh steering channel so a turn flushed from the
// previous run's leftovers delivers its remaining messages in order.
func (m *ui) startTurn(prompt string, queued []string) tea.Cmd {
	steer := make(chan string, 64)
	for _, q := range queued {
		select {
		case steer <- q:
		default:
		}
	}
	m.queue = append([]string(nil), queued...)
	m.steer = steer
	if m.client == nil {
		m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup before prompting."})
		m.input = []rune(prompt)
		m.edit.endCaret(m.input)
		return nil
	}
	prior := m.history
	m.priorLen = len(prior)
	m.turnSent = make(map[string]int)
	m.entries = append(m.entries, entry{role: "You", content: prompt})
	m.history = append(m.history, model.Message{Role: "user", Content: prompt})
	m.refreshStatusSessionTitle()
	m.persist()
	m.streamBuf.Reset()
	m.streaming = -1
	m.closeReasoning()
	m.beginTurnFooter()
	m.status = "Waiting for model"
	m.working = true
	m.jumpBottom()
	m.layoutWidth = 0
	m.showActivity()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan agent.TurnEvent, 64)
	m.abandon = make(chan struct{})
	m.runID++
	m.runProvider, m.runModel = m.conn.ProviderCanonical, m.modelName
	m.taskRuntime = nil
	m.liveToolCalls, m.runToolCalls, m.approvalCall = nil, nil, ""
	m.approvedEdits = nil
	m.resetAgentInspection()
	runID := m.runID
	events := m.events
	abandon := m.abandon
	client, repo, root, mcp := m.client, m.repo, m.root, m.conn.Mcp
	options := m.toolRunOptions(m.runID)
	go func() {
		defer close(events)
		agent.RunTurnWithOptions(ctx, client, repo, root, prior, prompt, mcp, steer, options, func(ev agent.TurnEvent) {
			ev.RunID = runID
			// Steer deliveries join terminal events in the non-droppable
			// path: the engine has already appended the message to its
			// history, so the UI must account for it before the run ends.
			// Usage events too: a cancel must not lose a request that
			// already completed and was billed.
			if ev.Kind == "done" || ev.Kind == "error" || ev.Kind == "tool_result" || ev.Kind == "steer" || ev.Kind == "stream_interrupted" || ev.Kind == "notice" || ev.Kind == "task" || ev.Kind == "task_runtime" || ev.Kind == "tool_checkpoint" || ev.Kind == "usage" {
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
	return tea.Batch(waitEvent(events), m.activityTickCmd())
}

// applyTurnEvent folds one event of the live run into the view. The caller
// owns re-arming the event read; a returned command is extra work only.
func (m *ui) applyTurnEvent(v agent.TurnEvent) tea.Cmd {
	// Every completed model request of the run carries its own usage: the
	// loop's "usage" event after each round, and a compaction's terminal
	// event for its one summarize call. Each event is applied exactly once.
	if v.Usage != nil {
		provider, modelID := m.runProvider, m.runModel
		if modelID == "" {
			provider, modelID = m.conn.ProviderCanonical, m.modelName
		}
		m.addRequestUsage(provider, modelID, *v.Usage)
		if v.Kind == "usage" && v.Usage.PromptSeen {
			m.lastPromptTokens = v.Usage.Prompt
		}
	}
	switch v.Kind {
	case "task_runtime":
		m.taskRuntime = v.TaskRuntime
	case "task":
		if v.Task != nil {
			m.addTaskUsage(*v.Task)
			m.acceptTaskRecord(*v.Task)
		}
	case "tool_checkpoint":
		m.history = v.History
		m.persist()
	case "ask":
		m.clearTextSelection()
		// One blocking ask_user broker waits on the reply channel; this
		// opens the interactive question. The reply has room for one
		// send, exactly once, and stale duplicates are dropped here.
		// The UI keeps draining events while the question is open: the
		// run must still deliver its tool_result, text and terminal
		// event after the answer, or after a cancel.
		m.hideActivity()
		if v.Ask == nil {
			return nil
		}
		ask := v.Ask
		m.openAskQuestion(ask.Request, ask.ID, ask.CallID, func(answer tools.AskAnswer) {
			select {
			case ask.Reply <- answer:
			default:
			}
		})
		m.markAwaitingAnswer(ask.CallID)
		return nil
	case "consent":
		m.clearTextSelection()
		m.openConsent(v.Consent)
		return nil
	case "context":
		// Each stream replaces the displayed context state. An unknown
		// measurement intentionally clears any value from the prior turn.
		m.contextTokens = v.ContextTokens
		m.contextSeen = v.ContextKnown
		m.contextEstimated = v.ContextEstimated
	case "reasoning":
		// Thinking output from a reasoning model; rendered muted and
		// closed as soon as the first real content delta arrives. Deltas
		// after content started are dropped so thinking cannot be
		// mistaken for the answer.
		m.hideActivity()
		if m.streaming >= 0 {
			return nil
		}
		if m.reasoningStream < 0 {
			m.openReasoning()
		}
		m.reasoningBuf.WriteString(v.Text)
		m.entries[m.reasoningStream].content = m.reasoningBuf.String()
	case "text":
		m.hideActivity()
		m.closeReasoning() // content after thinking closes the reasoning stream
		if m.streaming < 0 {
			m.entries = append(m.entries, entry{role: "Assistant"})
			m.streaming = len(m.entries) - 1
		}
		m.streamBuf.WriteString(v.Text)
		m.entries[m.streaming].content = m.streamBuf.String()
	case "approval":
		m.clearTextSelection()
		m.hideActivity()
		m.closeReasoning()
		if m.cancelling {
			break
		}
		m.pending = v.Approval
		m.markAwaitingApproval()
		m.resetToolInspection()
		m.resetAgentInspection()
		m.scroll = 0 // review starts at the top of the proposal screen
		m.following = false
		m.layoutWidth = 0
		m.reviewSeen = make([]bool, m.pageCount())
		m.reviewFocus = focusApprove
		m.markSeenFromScroll()
		m.status = "Review " + m.pending.Kind + " before approval"
	case "tool_start", "tool_result":
		m.hideActivity()
		m.streamBuf.Reset()
		m.streaming = -1
		// A tool call ends the reasoning block too: reasoning after it
		// opens a new block below the tool item.
		m.closeReasoning()
		if v.Kind == "tool_start" {
			m.startToolItem(v.Text, v.ToolCall)
		} else if v.ToolCall != nil && v.ToolResult != nil {
			m.recordToolResult(*v.ToolCall, *v.ToolResult, v.Text)
		} else {
			m.entries = append(m.entries, entry{role: "Tool", content: v.Text})
		}
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
			if m.working && !m.cancelling && m.pending == nil {
				m.showActivity()
				return m.activityTickCmd()
			}
		}
		if v.Kind == "tool_start" && m.working && m.pending == nil {
			// The running dot blinks on the activity tick, which would
			// otherwise stop with the Working row it just replaced.
			if tick := m.armActivityTick(); tick != nil {
				m.layoutWidth = 0
				return tick
			}
		}
	case "notice":
		m.entries = append(m.entries, entry{role: "Likha", content: v.Text})
		if v.History != nil {
			m.history = v.History
		}
		m.persist()
	case "steer":
		m.hideActivity()
		// A queued message reached the model: adopt the engine's history
		// and flip its Queued row to You. A message from a sent batch
		// has no row yet, so append one to keep the transcript in step
		// with the history it just produced (spec FR-21).
		m.history = v.History
		// The delivery opens a new provider round: close any open
		// assistant/reasoning stream so the next round's output starts
		// fresh entries instead of appending to the previous answer.
		m.streaming = -1
		m.streamBuf.Reset()
		m.closeReasoning()
		if !m.flipQueuedRow(v.Text) {
			m.entries = append(m.entries, entry{role: "You", content: v.Text})
		}
		if len(m.queue) > 0 {
			m.queue = m.queue[1:]
		}
		if m.turnSent == nil {
			m.turnSent = make(map[string]int)
		}
		m.turnSent[v.Text]++
		m.persist()
		if m.working && !m.cancelling && m.pending == nil {
			m.showActivity()
			return m.activityTickCmd()
		}
	case "compacted":
		m.hideActivity()
		m.closeReasoning()
		m.resetToolInspection()
		m.resetAgentInspection()
		// The single summarize call finished: swap the summarized turns
		// for the brief and mark the point in the conversation.
		m.history = v.History
		m.recalculateContext(m.history)
		m.entries = append(m.entries, entry{role: "Likha", content: "Conversation compacted. Summary of earlier turns:\n\n" + v.Text})
		m.status = "Ready"
		m.working = false
		if m.cancel != nil {
			m.cancel()
		}
		m.cancel = nil
		m.persist()
		m.layoutWidth = 0
		// A finished compaction holds any queue for an explicit Enter.
		m.steer = nil
		return nil
	case "done", "error":
		if cmd := m.finishRun(v); cmd != nil {
			m.layoutWidth = 0
			return cmd
		}
	}
	return nil
}

// finishRun settles the active run on its terminal event: the run state
// clears, a cancelled run gets its notice and footer, and the queue is held
// for an explicit Enter. forceStopRun reuses it for a run that never
// delivered one.
func (m *ui) finishRun(v agent.TurnEvent) tea.Cmd {
	m.hideActivity()
	m.closeReasoning()
	m.pending = nil
	m.reviewFocus = focusApprove
	m.history = v.History
	m.working = false
	m.taskRuntime = nil
	// An unanswered question dies with the run context; record the
	// interruption once and never reopen or replay it.
	m.askInterruptedNote()
	m.closeConsent()
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	if m.streaming >= 0 && v.Kind == "error" {
		m.entries = append(m.entries[:m.streaming], m.entries[m.streaming+1:]...)
		m.adjustToolRecordsAfterRemoval(m.streaming)
	}
	m.streaming = -1
	if m.cancelling {
		m.recordUnexecutedCalls(v.History)
	}
	turnTools, turnCancelled := m.turnToolItems, m.cancelling
	m.liveToolCalls, m.runToolCalls, m.approvalCall = nil, nil, ""
	m.approvedEdits = nil
	if m.cancelling {
		m.status = "Cancelled"
		m.entries = append(m.entries, entry{role: "Likha", content: "Run cancelled; no further tools will execute. Approved shell commands may leave detached processes running."})
	} else if v.Kind == "error" {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: v.Text})
	} else {
		m.status = "Ready"
	}
	// The footer closes every finished user turn, failed ones
	// included, below the run's last notice.
	m.appendTurnFooter(turnTools, turnCancelled)
	m.cancelling = false
	// A run end (done, error, cancel) holds the queue: reconcile the
	// backstop against the adopted history, then leave the remaining
	// messages queued for an explicit Enter (FR-21).
	m.reconcileQueue()
	m.persist()
	m.refreshStatusSessionTitle()
	m.steer = nil
	return m.autoNameAfterFirstTurn(v.Kind)
}

// addRequestUsage adds one completed model request to the session token
// totals and spend (specs/model-metadata § Lookup). The request is priced
// with the provider and model it was sent to: a provider-reported cost is
// exact, a subscription is an exact $0, a catalog price is an estimate that
// marks spend with "~", and an unknown price leaves spend as it was.
func (m *ui) addRequestUsage(provider, modelID string, u model.RequestUsage) {
	m.usagePrompt += u.Prompt
	m.usageCompletion += u.Completion
	m.usageSeen = true
	cost, source := model.RequestCost(provider, modelID, u)
	if !source.Known() {
		return
	}
	m.spend += cost
	m.spendKnown = true
	if source == model.CostEstimated {
		m.spendEstimated = true
	}
}

// countedTaskUsage is the part of a task record's usage already added to the
// session totals.
type countedTaskUsage struct {
	version uint64
	usage   explore.Usage
}

// addTaskUsage adds the model requests an explore/agent child made since its
// last counted record to the session token totals and spend, so a run that
// delegates counts every request it caused (specs/model-metadata § Lookup).
// The node prices each request with the task's own provider and model when
// it completes; its record carries the running sums, so only the growth
// since the newest counted version is added and no request is counted
// twice. A record restored from storage is the baseline, never new usage.
// Requests whose cost is unknown leave spend as it was, as on the main loop.
func (m *ui) addTaskUsage(record explore.Record) {
	if record.SessionID != m.snapshot.ID || record.ID == "" {
		return
	}
	prev, counted := m.taskUsageCounted[record.ID]
	if !counted {
		for _, known := range m.taskRecords {
			if known.ID == record.ID {
				prev, counted = countedTaskUsage{version: known.Version, usage: known.Usage}, true
				break
			}
		}
	}
	if counted && record.Version <= prev.version {
		return
	}
	if m.taskUsageCounted == nil {
		m.taskUsageCounted = make(map[string]countedTaskUsage)
	}
	m.taskUsageCounted[record.ID] = countedTaskUsage{version: record.Version, usage: record.Usage}
	next := record.Usage
	prompt := max(next.PromptTokens-prev.usage.PromptTokens, 0)
	completion := max(next.CompletionTokens-prev.usage.CompletionTokens, 0)
	if prompt > 0 || completion > 0 {
		m.usagePrompt += prompt
		m.usageCompletion += completion
		m.usageSeen = true
	}
	switch cost := next.Cost - prev.usage.Cost; {
	case cost > 0:
		m.spend += cost
	case next.CostKnown && record.Rounds > 0:
		// Every request so far is priced, at $0 (a subscription child).
	default:
		return
	}
	m.spendKnown = true
	m.spendEstimated = m.spendEstimated || next.CostEstimated
}

// forceStopRun ends a cancelled run that never delivered its terminal
// event. Closing abandon releases the run goroutine's undroppable sends, the
// run settles as cancelled with the queue held, and its late events are
// dropped by the run-ID guard because the UI is no longer working.
func (m *ui) forceStopRun() tea.Cmd {
	if m.abandon != nil {
		close(m.abandon)
		m.abandon = nil
	}
	m.entries = append(m.entries, entry{role: "Likha", content: "The run did not stop in time; it was detached."})
	return m.finishRun(agent.TurnEvent{Kind: "error", RunID: m.runID, History: m.history})
}

// enqueue records a steering prompt typed while a run is active: a visible
// Queued row, the UI FIFO, and a non-blocking hand-off to the running turn.
// A nil or full channel leaves the entry only in m.queue, where the turn-end
// flush picks it up (spec FR-21).
func (m *ui) enqueue(text string) {
	m.entries = append(m.entries, entry{role: "Queued", content: text})
	m.queue = append(m.queue, text)
	if m.steer != nil {
		select {
		case m.steer <- text:
		default:
		}
	}
	m.input = nil
	m.edit.endCaret(nil)
	m.layoutWidth = 0
}

// sendHeldQueue starts a turn from the messages held when a previous run
// ended: the head becomes the prompt and the rest pre-fill the fresh
// steering channel, so the whole batch reaches the model in order. Only an
// explicit Enter calls it — a run end never auto-starts a turn (FR-21).
func (m *ui) sendHeldQueue() tea.Cmd {
	if len(m.queue) == 0 {
		return nil
	}
	kept := m.entries[:0]
	indices := make(map[int]int)
	for index, e := range m.entries {
		if e.role != "Queued" {
			indices[index] = len(kept)
			kept = append(kept, e)
		}
	}
	m.remapToolRecords(indices)
	m.entries = kept
	head, rest := m.queue[0], m.queue[1:]
	m.queue = nil
	return m.startTurn(head, rest)
}

// flipQueuedRow turns the first queued row with the given content into a
// delivered You row. Reports whether a row matched.
func (m *ui) flipQueuedRow(text string) bool {
	for i := range m.entries {
		if m.entries[i].role == "Queued" && m.entries[i].content == text {
			m.entries[i].role = "You"
			return true
		}
	}
	return false
}

// reconcileQueue is the terminal backstop for a steer delivery the UI never
// saw: when this turn's history holds more user messages than its own prompt
// plus the steer events the UI processed, the oldest queued messages were
// received anyway. Flip and drop them so a held batch is never sent twice.
func (m *ui) reconcileQueue() {
	if len(m.queue) == 0 || m.priorLen > len(m.history) {
		return
	}
	users := 0
	for _, msg := range m.history[m.priorLen:] {
		if msg.Role == "user" {
			users++
		}
	}
	delivered := 0
	for _, n := range m.turnSent {
		delivered += n
	}
	missing := users - 1 - delivered // minus the turn's own prompt
	if missing <= 0 {
		return
	}
	for i := 0; i < missing && i < len(m.queue); i++ {
		m.flipQueuedRow(m.queue[i])
	}
	if missing >= len(m.queue) {
		m.queue = nil
		return
	}
	m.queue = m.queue[missing:]
}

// startCompaction summarizes the conversation so far with one model call and
// replaces the summarized turns with the returned brief. It mirrors
// startTurn's run machinery: cancellable context, an events channel drained
// by waitEvent, and the shared working state. No text streams into the
// conversation while it runs; the deliverable is a single agent.TurnEvent.
func (m *ui) startCompaction(focus string) tea.Cmd {
	m.status = "Compacting…"
	m.working = true
	m.turnStarted = time.Time{} // a compaction is not a user turn: no footer
	m.jumpBottom()
	m.layoutWidth = 0
	m.showActivity()
	m.input = nil
	m.edit.endCaret(m.input)
	// The run starts at the current history: a cancelled compaction carries
	// no turn of its own, so the end-of-run backstops (unexecuted calls,
	// queue reconcile) must not revisit the previous turn.
	m.priorLen = len(m.history)
	m.turnSent = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan agent.TurnEvent, 64)
	m.abandon = make(chan struct{})
	m.runID++
	m.runProvider, m.runModel = m.conn.ProviderCanonical, m.modelName
	runID := m.runID
	events := m.events
	abandon := m.abandon
	client, history := m.client, m.history
	go func() {
		defer close(events)
		newHistory, summary, usage, usageOK, err := agent.CompactHistoryUsage(ctx, client, history, focus, nil)
		ev := agent.TurnEvent{RunID: runID}
		if usageOK {
			// The summarize request completed and was billed even when its
			// summary is rejected; the terminal event carries its usage.
			ev.Usage = &usage
		}
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
	return tea.Batch(waitEvent(events), m.activityTickCmd())
}

// previewTheme re-resolves the live styles to a candidate family without
// touching the committed name, config, or transcript: Esc restores.
func (m *ui) previewTheme(themeName string) {
	m.theme = likhaui.Resolve(themeName, likhaui.HasDarkBackground())
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
	m.entries = append(m.entries, entry{role: "Likha", content: "Theme set to " + themeName + "; stored for later runs."})
}

// handleThemesCommand lists the predefined themes or applies one by number
// or name. A bare /themes opens the selection dialog; a number or name applies
// directly. The applied theme is stored for later runs.
func (m *ui) handleThemesCommand(arg string) tea.Cmd {
	m.layoutWidth = 0
	names := likhaui.ThemeNames()
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
	if _, ok := likhaui.Named(themeName, true); !ok {
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
		m.entries = append(m.entries, entry{role: "Likha", content: "The provider reports no models."})
		return
	}
	m.lastModels = msg.models
	var b strings.Builder
	b.WriteString("Provider models (the /models dialog applies the highlighted model with Enter):")
	for i, id := range msg.models {
		fmt.Fprintf(&b, "\n%d. %s", i+1, id)
	}
	m.entries = append(m.entries, entry{role: "Likha", content: b.String()})
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
	m.resolveContextWindow()
	if cfg, err := providers.LoadStoredConfig(m.stateDir); err == nil && cfg.Provider != "" {
		cfg.Model = id
		if err := providers.SaveStoredConfig(m.stateDir, cfg); err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Store model: " + err.Error()})
			return
		}
	}
	m.entries = append(m.entries, entry{role: "Likha", content: "Model switched to " + id + " and stored for later runs."})
}

// resolveContextWindow refreshes the active model's context limit from the
// selected provider's metadata, a user override, and the documented catalog.
func (m *ui) resolveContextWindow() {
	var metadata, override int64
	if m.conn.ContextWindows != nil {
		metadata = m.conn.ContextWindows[m.modelName]
	}
	if m.conn.ContextWindowOverrides != nil {
		override = m.conn.ContextWindowOverrides[m.conn.ProviderCanonical][m.modelName]
	}
	m.contextWindow, m.contextWindowKnown = model.ResolveContextWindow(m.conn.ProviderCanonical, m.modelName, override, metadata)
}

// recalculateContext recomputes a best-effort estimate for restored or
// rewritten history. It is deliberately marked estimated: no actual provider
// request (and therefore no actual tool payload) exists at these lifecycle
// boundaries.
func (m *ui) recalculateContext(messages []model.Message) {
	tokens, ok := model.EstimateInputTokens(m.modelName, messages, nil)
	m.contextTokens = tokens
	m.contextSeen = ok
	m.contextEstimated = ok
}

func (m *ui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	sessionBefore := m.snapshot.ID
	defer func() {
		// A selection belongs only to the main surface and the session that
		// displayed it. Opening a review/overlay must never leave stale copy
		// targets behind, especially behind an authentication form.
		if m.snapshot.ID != sessionBefore {
			m.clearTextSelection()
			m.clearClipboardNotice()
		} else if !m.selectionSurfaceVisible() {
			// Reviews forbid text selection, but their scrollbar still owns
			// the entire press/drag/release gesture across Update and View.
			m.clearUnavailableTextSelection()
			m.clearClipboardNotice()
		}
	}()
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.clearTextSelection()
		m.composerVertical.reset()
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
	case clipboardResultMsg:
		return m, m.handleClipboardResult(v)
	case clipboardNoticeExpiredMsg:
		m.handleClipboardNoticeExpired(v)
		return m, nil
	case updateAvailableMsg:
		if v.version != "" {
			m.updateVersion = v.version
		}
		return m, nil
	case toolsCatalogMsg:
		if v.generation != m.toolCatalogGen || v.sessionID != m.snapshot.ID || v.runID != m.runID || m.working || m.mode != modeMain || m.pending != nil || m.dialog.open || m.keyModal.open {
			return m, nil
		}
		m.toolCatalog = v.entries
		m.status = "Ready"
		return m, m.openToolCatalog()
	case toolInspectionOutputMsg:
		m.handleToolInspectionOutput(v)
		return m, nil
	case agentDetailOutputMsg:
		m.handleAgentDetailOutput(v)
		return m, nil
	case sessionDeleteRequestedMsg:
		// Deletion never races a run or a pending review; the dialog shows
		// the same reason, this re-check covers a run that started since.
		if reason := m.sessionDeleteBlocked(); reason != "" {
			return m, m.finishSessionDelete(v, errors.New(reason))
		}
		if m.store == nil {
			return m, m.finishSessionDelete(v, errors.New("no session store available"))
		}
		if v.Current {
			// Detach the live output store before its files are removed.
			m.outputs = nil
			m.setToolOutputStore(nil)
		}
		if err := m.store.Delete(v.ID); err != nil {
			if v.Current {
				m.openOutputStore()
			}
			return m, m.finishSessionDelete(v, err)
		}
		// The row is gone, so the delete completes (a current session still
		// moves to a fresh one); leftover private output is reported, never
		// hidden.
		var rmErr error
		if m.stateDir != "" {
			rmErr = tooloutput.RemoveSession(m.stateDir, v.ID)
		}
		cmd := m.finishSessionDelete(v, nil)
		if rmErr != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Session deleted, but its private tool output could not be removed: " + rmErr.Error()})
			m.status = "Session output cleanup failed"
		}
		m.layoutWidth = 0
		return m, cmd
	case agentBranchCancelledMsg:
		if v.sessionID != m.snapshot.ID || v.runID != m.runID || !m.working || m.cancelling {
			return m, nil
		}
		if v.err != nil {
			m.status = "Cancel branch unavailable: " + toolShortText(v.err.Error(), 160)
		} else {
			m.status = "Cancel requested for task " + toolShortText(v.taskID, 64)
		}
		return m, nil
	case agent.TurnEvent:
		if !m.working || v.RunID != m.runID {
			return m, nil
		}
		cmd := m.applyTurnEvent(v)
		m.layoutWidth = 0
		// Every event of the live run schedules the next read here, so no
		// branch can strand the run's channel: an open question, a dropped
		// delta, or a cancel all keep draining until the terminal event.
		if m.working && v.RunID == m.runID {
			// A transcript handoff retires its old clock, but the model's
			// bottom status keeps animating throughout streamed output.
			cmd = tea.Batch(cmd, m.armActivityTick(), waitEvent(m.events))
		}
		return m, cmd
	case tea.MouseMsg:
		if m.mode == modeSetup {
			return m, nil
		}
		if m.handleAgentInspectionMouse(v) || m.handleToolInspectionMouse(v) {
			return m, nil
		}
		if m.handleTextSelectionMouse(v) {
			return m, nil
		}
		// Main-surface mouse events must not scroll content behind overlays.
		if m.keyModal.open || m.dialog.open || m.askVisible() || m.consentVisible() {
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
		// The naming request is billed whenever it completed, even when
		// its name is rejected or arrives too late to apply.
		if v.usageOK {
			m.addRequestUsage(v.provider, v.model, v.usage)
			m.layoutWidth = 0
		}
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
		if m.highlightPending {
			// Code left plain by the layout's highlight budget: lay out
			// again to highlight the next batch.
			m.layoutWidth = 0
		}
		return m, blinkCaret()
	case activityTickMsg:
		// Ticks are scoped to both the run and the current activity interval.
		// Handoffs, re-arms, cancellation, and reviews invalidate old clocks.
		if v.runID != m.runID || v.generation != m.activityGeneration || !m.working || m.pending != nil || !m.hasActivity() && !m.toolBlinking() && !m.statusWorking() {
			if m.activityArmed == v.generation+1 {
				m.activityArmed = 0 // this clock stopped; a later handoff may re-arm
			}
			return m, nil
		}
		m.activityFrame++
		m.layoutWidth = 0
		return m, m.activityTickCmd()
	case cancelWatchdogMsg:
		if m.working && m.cancelling && v.runID == m.runID {
			return m, m.forceStopRun()
		}
		return m, nil
	case escDecayMsg:
		// The Alt-prefix decay expired: this was a bare ESC, not the
		// Alt+Return prefix — clear the draft as ESC did before the decay
		// window existed (spec open item 2).
		if m.escPrefix {
			m.escPrefix = false
			if !m.working && m.pending == nil {
				switch {
				case len(m.input) > 0:
					m.input = nil
					m.edit.endCaret(nil)
					m.promptHistory.resetNavigation()
					m.layoutWidth = 0
				case len(m.queue) > 0:
					// A bare ESC with nothing else to clear drops the held
					// queue: rows and their texts go together (FR-21).
					n := len(m.queue)
					m.queue = nil
					kept := m.entries[:0]
					indices := make(map[int]int)
					for index, e := range m.entries {
						if e.role != "Queued" {
							indices[index] = len(kept)
							kept = append(kept, e)
						}
					}
					m.remapToolRecords(indices)
					note := "Queued messages cleared."
					if n == 1 {
						note = "Queued message cleared."
					}
					m.entries = append(kept, entry{role: "Likha", content: note})
					m.layoutWidth = 0
				}
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
			// Back on the key field with the key kept: fix it and Enter
			// retries, Esc picks another provider.
			m.setup.err = v.err.Error()
			m.setup.stage = setupKey
			return m, nil
		}
		m.setup.err = ""
		m.setup.models = v.models
		m.setup.contextWindows = v.contextWindows
		if len(v.models) == 1 {
			return m, m.finishSetup(v.models[0])
		}
		m.setup.stage = setupModel
		m.setup.filter = nil
		m.setup.modelCursor = setupDefaultModel(model.Providers[m.setup.cursor], v.models)
		return m, nil
	case oauthLoginMsg:
		return m, m.handleOAuthLogin(v)
	case oauthProgressMsg:
		if v.attempt != m.oauth.id || !m.oauthWaiting() {
			return m, nil
		}
		m.oauth.progress = v.text
		return m, waitOAuthEvent(m.oauth.events)
	case oauthBrowserErrorMsg:
		if v.attempt != m.oauth.id || !m.oauthWaiting() {
			return m, nil
		}
		m.oauth.manualURL = manualOAuthURL(v.url)
		m.oauth.progress = "Couldn't open the browser automatically. Open the link below to continue."
		return m, waitOAuthEvent(m.oauth.events)
	case oauthLogoutMsg:
		m.handleOAuthLogout(v)
		return m, nil
	case setupTickMsg:
		return m, m.handleSetupTick()
	case tea.KeyMsg:
		m.endTextSelectionDrag()
		inputBeforeKey := string(m.input)
		caretBeforeKey := m.edit.caret
		defer func() {
			if string(m.input) != inputBeforeKey {
				m.composerVertical.reset()
			}
			if string(m.input) != inputBeforeKey || m.edit.caret != caretBeforeKey {
				m.clearTranscriptSelection()
			}
		}()
		if m.mode == modeSetup {
			return m.updateSetup(v)
		}
		// A pending model question claims keys first: answer input must never
		// leak into the composer or popups, and Esc/Ctrl+C still fall through
		// to run cancellation (handleAskKey returns false for ctrl+c).
		if handled, cmd := m.handleAskKey(v); handled {
			m.layoutWidth = 0
			return m, cmd
		}
		// Web consent likewise intercepts keys, with Esc declining only the
		// consent and Ctrl+C falling through to run cancellation.
		if handled, cmd := m.handleConsentKey(v); handled {
			m.layoutWidth = 0
			return m, cmd
		}
		if m.selectionSurfaceVisible() {
			if v.String() == "alt+c" {
				return m, m.copyText(m.selectedText())
			}
			if v.String() == "esc" && m.textSelectionActive() {
				m.escPrefix = false
				m.clearTextSelection()
				return m, nil
			}
			// An existing input range owns ordinary arrows before completion
			// popups. Once collapsed, the next arrow resumes popup navigation.
			if _, _, selected := m.edit.selectionRange(m.input); selected {
				switch v.String() {
				case "left", "right", "up", "down":
					m.handleComposerSelectionKey(v)
					m.layoutWidth = 0
					return m, nil
				}
			}
			if v.String() == "up" || v.String() == "down" {
				// Completion navigation should remain visible after returning
				// from an output selection's frozen popup geometry.
				m.clearTranscriptSelection()
			}
		}
		// The @ completion popup claims navigation and completion keys only
		// while it has rows; everything else keeps editing the draft.
		if m.mention.open {
			before := string(m.input)
			if claimed, cmd := m.updateMention(v); claimed {
				if string(m.input) != before {
					m.promptHistory.resetNavigation()
				}
				return m, cmd
			}
		}
		// The / command popup mirrors it; the two popups are never open
		// together, and Esc/Ctrl+C must still reach a running turn.
		if m.commandPopup.open {
			before := string(m.input)
			if claimed, cmd := m.updateCommand(v); claimed {
				if string(m.input) != before {
					m.promptHistory.resetNavigation()
				}
				return m, cmd
			}
		}
		if m.keyModal.open {
			// The key modal takes precedence over the provider dialog beneath
			// it; Esc returns to that dialog with nothing changed.
			return m, m.updateKeyModal(v)
		}
		if m.dialog.open {
			snapshotID := m.snapshot.ID
			updated, cmd := m.updateDialog(v)
			if m.snapshot.ID != snapshotID {
				m.promptHistory = newPromptHistory(restoredPromptHistory(m.snapshot))
				m.composerVertical.reset()
			}
			return updated, cmd
		}
		// Keyboard actions that replace or reflow the transcript release its
		// frozen selection before inspection, expansion, or submission runs.
		switch v.String() {
		case "enter", "tab", "shift+tab", "ctrl+o", "ctrl+g":
			if m.selectionSurfaceVisible() {
				m.clearTranscriptSelection()
			}
		}
		if m.pending == nil {
			if handled, cmd := m.handleAgentInspection(v); handled {
				return m, cmd
			}
			if handled, cmd := m.inspectFocusedTask(v); handled {
				return m, cmd
			}
			if handled, cmd := m.handleToolInspection(v); handled {
				return m, cmd
			}
		}
		if v.String() == "ctrl+g" {
			if !m.working && m.pending == nil {
				return m, m.openDialog(dialogComposer)
			}
			return m, nil
		}
		if m.pending != nil {
			// The review gate owns the decision: arrows/Tab move the focus,
			// Enter confirms the focused button, and every other key —
			// letters included — stays inert (FR-22).
			switch v.String() {
			case "left", "shift+tab":
				m.reviewFocus = focusApprove
				return m, nil
			case "right", "tab":
				m.reviewFocus = focusDecline
				return m, nil
			case "enter":
				if m.reviewFocus == focusApprove {
					if m.width < minWidth || m.height < minHeight {
						return m, nil
					}
					if !m.reviewReady() {
						m.status = reviewGateStatus
						return m, nil
					}
					m.rememberApprovedEdit()
					m.pending.Reply <- true
					m.status = "Executing approved " + m.pending.Kind
					m.pending = nil
					m.reviewSeen = nil
					m.reviewFocus = focusApprove
					m.jumpBottom()
					m.layoutWidth = 0
					return m, m.resumeAfterApproval()
				}
				m.pending.Reply <- false
				m.status = "Rejected"
				m.pending = nil
				m.reviewSeen = nil
				m.reviewFocus = focusApprove
				m.jumpBottom()
				m.layoutWidth = 0
				return m, m.resumeAfterApproval()
			}
		}
		if m.handleComposerSelectionKey(v) {
			m.layoutWidth = 0
			return m, nil
		}
		// Composer arrows are history/caret navigation only after overlays
		// have declined them. Popups, the review gate, dialogs, and key modal
		// therefore retain ownership of their navigation keys.
		if (v.String() == "up" || v.String() == "down") && m.editable() {
			return m, m.navigatePromptHistory(v.String() == "up")
		}
		// The Alt-prefix decay window only carries Return; any other chord
		// is processed as its own key and cancels the window (the ESC is
		// dropped, never queued).
		if m.escPrefix && v.String() != "enter" {
			m.escPrefix = false
		}
		switch v.String() {
		case "ctrl+c", "esc":
			m.toolCatalogGen++
			if m.working {
				m.clearTextSelection()
				if m.cancelling {
					// The run has not acknowledged the first cancel; a
					// second press stops waiting for it.
					return m, m.forceStopRun()
				}
				// Cancel-run stays immediate: ESC during a run never arms
				// the newline prefix, so it carries no ambiguity.
				m.cancel()
				m.pending = nil
				m.resetToolInspection()
				m.resetAgentInspection()
				m.reviewSeen = nil
				m.reviewFocus = focusApprove
				m.cancelling = true
				m.status = "Cancelling"
				m.layoutWidth = 0
				return m, cancelWatchdog(m.runID)
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
				if deleteBack(&m.input, &m.edit) {
					m.promptHistory.resetNavigation()
				}
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "delete":
			if m.editable() && deleteForward(&m.input, &m.edit) {
				m.caretNote()
				m.promptHistory.resetNavigation()
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
				m.promptHistory.resetNavigation()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+delete", "alt+delete", "alt+d":
			if m.editable() && killWordForward(&m.input, &m.edit) {
				m.caretNote()
				m.promptHistory.resetNavigation()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "alt+b":
			if m.editable() {
				m.caretNote()
				moveWordBack(m.input, &m.edit)
				m.composerVertical.reset()
			}
		case "alt+f":
			if m.editable() {
				m.caretNote()
				moveWordForward(m.input, &m.edit)
				m.composerVertical.reset()
			}
		case "ctrl+u":
			if m.editable() && killToStart(&m.input, &m.edit) {
				m.caretNote()
				m.promptHistory.resetNavigation()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+k":
			if m.editable() && killToEnd(&m.input, &m.edit) {
				m.caretNote()
				m.promptHistory.resetNavigation()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+y":
			if m.editable() && yank(&m.input, &m.edit) {
				m.caretNote()
				m.promptHistory.resetNavigation()
				if cmd := m.syncPopups(); cmd != nil {
					m.layoutWidth = 0
					return m, cmd
				}
			}
		case "ctrl+t":
			if m.editable() && transpose(&m.input, &m.edit) {
				m.caretNote()
				m.promptHistory.resetNavigation()
			}
		case "ctrl+j", "alt+enter", "ctrl+enter", "shift+enter":
			// Newline insertion (the Return-modifier family). ctrl+enter
			// reaches most terminals as the LF byte, reported "ctrl+j";
			// alt+enter arrives ESC-combined or through the ESC decay
			// window on "enter" below (probe table in tasks.md).
			if m.editable() && !m.mention.open && !m.commandPopup.open {
				m.caretNote()
				insertNewline(&m.input, &m.edit)
				m.promptHistory.resetNavigation()
			}
		case "enter":
			if m.escPrefix {
				m.escPrefix = false
				if m.editable() && !m.mention.open && !m.commandPopup.open {
					m.caretNote()
					insertNewline(&m.input, &m.edit)
					m.promptHistory.resetNavigation()
					break
				}
			}
			// Typed newlines flatten to spaces on submit, exactly as
			// bracketed-paste newlines do (user-confirmed decision).
			prompt := strings.TrimSpace(strings.ReplaceAll(string(m.input), "\n", " "))
			if m.working {
				// Live composer (FR-21): Enter turns the draft into a
				// steering prompt. A leading / is refused with the draft
				// kept, while // queues the literal remainder. These paths
				// carry no min-size guard.
				switch {
				case prompt == "":
					return m, nil
				case strings.HasPrefix(prompt, "//"):
					if rest := prompt[2:]; rest != "" {
						m.promptHistory.append(rest)
						m.enqueue(rest)
						m.persist()
					}
					return m, nil
				case strings.HasPrefix(prompt, "/"):
					m.entries = append(m.entries, entry{role: "Error", content: "Commands are inactive while a run is active. Esc cancels the run; // queues a literal slash."})
					m.layoutWidth = 0
					return m, nil
				default:
					m.promptHistory.append(prompt)
					m.enqueue(prompt)
					m.persist()
					return m, nil
				}
			}
			if m.width < minWidth || m.height < minHeight {
				return m, nil
			}
			if prompt == "" {
				// Enter on an empty draft sends the held queue (FR-21):
				// a run end never auto-starts it, this is the only send.
				if len(m.queue) > 0 {
					return m, m.sendHeldQueue()
				}
				return m, nil
			}
			submitted := prompt
			if strings.HasPrefix(submitted, "//") {
				// handleCommand strips one slash before dispatching an escaped
				// literal prompt; history stores the exact queued text.
				submitted = submitted[1:]
			}
			record := false
			switch {
			case strings.HasPrefix(prompt, "//"):
				record = m.client != nil
			case strings.HasPrefix(prompt, "/"):
				record = recognizedCommand(prompt)
			default:
				record = m.client != nil
			}
			if record {
				m.promptHistory.append(submitted)
			}
			m.input = nil
			m.edit.endCaret(nil)
			if strings.HasPrefix(prompt, "/") {
				snapshotID := m.snapshot.ID
				if record && !strings.HasPrefix(prompt, "//") {
					// Preserve the accepted command in its originating session even
					// when it immediately resumes another session.
					m.persist()
				}
				cmd := m.handleCommand(prompt)
				if m.snapshot.ID != snapshotID {
					m.promptHistory = newPromptHistory(restoredPromptHistory(m.snapshot))
				} else {
					m.persist()
				}
				return m, cmd
			}
			return m, m.startTurn(prompt, nil)
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
					if len(runes) > 0 {
						m.promptHistory.resetNavigation()
					}
				} else if v.Type == tea.KeySpace {
					m.caretNote()
					insertRunes(&m.input, &m.edit, []rune{' '})
					m.promptHistory.resetNavigation()
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

// editable reports whether the composer accepts edits: not while an approval
// is pending. The composer stays live while a turn streams (FR-21).
func (m *ui) editable() bool {
	return m.pending == nil
}

// recognizedCommand reports whether an idle slash submission dispatches to a
// reserved application command. Unknown slash input is restored in the
// composer and must not become history.
func recognizedCommand(prompt string) bool {
	name, _, _ := strings.Cut(strings.TrimPrefix(prompt, "/"), " ")
	for _, command := range commands {
		if name == command.Name {
			return true
		}
	}
	return false
}

// composerInputWidth is the usable width of the text area, matching the
// composer style's insets so vertical caret moves track the rendered wraps.
func (m *ui) composerInputWidth() int {
	_, _, width := m.composerLayout()
	return width
}

// navigatePromptHistory gives the composer the usual two-step edge behavior:
// the first edge press moves to the row boundary, and the next recalls a
// prompt. Within a wrapped draft, it moves vertically without touching history.
func (m *ui) navigatePromptHistory(up bool) tea.Cmd {
	width := m.composerInputWidth()
	caret := min(len(m.input), max(0, m.edit.caret))
	row := composerVisualRow(m.input, caret, width)
	firstRow := composerVisualRow(m.input, 0, width)
	lastRow := composerVisualRow(m.input, len(m.input), width)

	if up {
		if row > firstRow {
			if next, ok := m.composerVertical.move(m.input, caret, width, -1); ok {
				m.edit.caret = next
				m.caretNote()
				m.layoutWidth = 0
			}
			return nil
		}
		if caret != 0 {
			m.edit.caret = 0
			m.composerVertical.reset()
			m.caretNote()
			m.layoutWidth = 0
			return nil
		}
		recalled, ok := m.promptHistory.previous(m.input)
		if !ok {
			return nil
		}
		m.input = append([]rune(nil), recalled...)
	} else {
		if row < lastRow {
			if next, ok := m.composerVertical.move(m.input, caret, width, 1); ok {
				m.edit.caret = next
				m.caretNote()
				m.layoutWidth = 0
			}
			return nil
		}
		if caret != len(m.input) {
			m.edit.caret = len(m.input)
			m.composerVertical.reset()
			m.caretNote()
			m.layoutWidth = 0
			return nil
		}
		recalled, ok := m.promptHistory.next()
		if !ok {
			return nil
		}
		m.input = append([]rune(nil), recalled...)
	}
	m.edit.endCaret(m.input)
	m.composerVertical.reset()
	m.caretNote()
	m.layoutWidth = 0
	return m.syncPopups()
}

// syncPopups re-filters the @-mention and /-command popups after a draft
// edit; the popups are never open together, and an @ anywhere wins.
// Returns the background command for a freshly started mention index walk.
func (m *ui) syncPopups() tea.Cmd {
	if m.repo != nil {
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
	if _, active := commandQuery(string(m.input)); active && !strings.Contains(string(m.input), "@") {
		m.commandPopup.open = true
		m.syncCommand()
		m.mention.open = false
	} else {
		m.commandPopup.open = false
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

// cancelWatchdogWindow is how long a cancelled run may take to deliver its
// terminal event before the UI stops waiting and detaches it.
const cancelWatchdogWindow = 3 * time.Second

type cancelWatchdogMsg struct{ runID uint64 }

func cancelWatchdog(runID uint64) tea.Cmd {
	return tea.Tick(cancelWatchdogWindow, func(time.Time) tea.Msg { return cancelWatchdogMsg{runID: runID} })
}

// nameGeneratedMsg carries the outcome of the one auto-naming call. An error
// or empty name keeps the derived title; nothing is ever surfaced.
type nameGeneratedMsg struct {
	name string
	err  error
	// The naming request's usage, priced with the provider and model the
	// call was made with; usageOK is false when the request never completed
	// or reported no usage.
	usage    model.RequestUsage
	usageOK  bool
	provider string
	model    string
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
	provider, modelID := m.conn.ProviderCanonical, m.modelName
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		name, usage, usageOK, err := agent.GenerateSessionNameUsage(ctx, client, history)
		return nameGeneratedMsg{name: name, err: err, usage: usage, usageOK: usageOK, provider: provider, model: modelID}
	}
}

func (m *ui) persist() {
	m.snapshot.PromptHistory = m.promptHistory.all()
	m.snapshot.Tasks = append([]explore.Record(nil), m.taskRecords...)
	if m.store == nil {
		return
	}
	m.snapshot.History = m.history
	m.snapshot.Entries = make([]session.Entry, 0, len(m.entries))
	indices := make(map[int]int)
	for index, current := range m.entries {
		if current.role == "Logo" || current.role == "Queued" || current.role == "Working" {
			continue // per-run furniture: startup art, undelivered queue rows, and ephemeral activity are never stored
		}
		indices[index] = len(m.snapshot.Entries)
		m.snapshot.Entries = append(m.snapshot.Entries, session.Entry{Role: current.role, Content: current.content})
	}
	m.snapshot.ToolRecords = nil
	for _, record := range m.toolRecords {
		if index, ok := indices[record.EntryIndex]; ok {
			record.EntryIndex = index
			m.snapshot.ToolRecords = append(m.snapshot.ToolRecords, record)
		}
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
