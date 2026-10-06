package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"likha/internal/agent"
	"likha/internal/cmdpolicy"
	"likha/internal/model"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

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

	// Sessions dialog delete confirmation (session_delete.go). The dialog
	// only requests deletion; tui.go performs it and reports back.
	deleteConfirm bool   // the confirmation view replaces the session list
	deleteFocus   bool   // true: Delete is focused; Cancel is the default
	deleteBusy    bool   // request in flight; keys are inert until it resolves
	deleteID      string // session the confirmation names
	deleteLabel   string // listing row of that session, shown in the body
	deleteErr     string // last delete failure, shown inside the confirmation
}

func (m *ui) dialogMatches() []int {
	indices := make([]int, 0, len(m.dialogItems))
	q := strings.ToLower(m.dialog.query)
	if m.dialog.kind == dialogModels {
		return m.modelMatches(q)
	}
	for i, item := range m.dialogItems {
		if q == "" || strings.Contains(strings.ToLower(item), q) {
			indices = append(indices, i)
		}
	}
	return indices
}

// modelMatches filters /models rows over model and provider text: a model
// row matches on its id or its section's names, so a provider name narrows
// the list to its section. A section (header included) shows iff at least
// one of its rows matches, so filtering never leaves an orphan header.
func (m *ui) modelMatches(q string) []int {
	indices := make([]int, 0, len(m.dialogModelRows))
	for i, row := range m.dialogModelRows {
		if q == "" ||
			strings.Contains(strings.ToLower(row.model), q) ||
			strings.Contains(strings.ToLower(row.displayName), q) ||
			strings.Contains(strings.ToLower(row.provider.DisplayName), q) ||
			strings.Contains(strings.ToLower(row.provider.Name), q) {
			indices = append(indices, i)
		}
	}
	return indices
}

// modelHeaderAt reports whether a visible (match-space) position starts a
// new provider section, returning the section's display name. Headers are
// non-selectable: the cursor in dialogState indexes match positions, never
// header rows, so navigation and Enter never land on one.
func (m *ui) modelHeaderAt(matches []int, pos int) (string, bool) {
	if m.dialog.kind != dialogModels || pos < 0 || pos >= len(matches) {
		return "", false
	}
	row := m.dialogModelRows[matches[pos]]
	if pos == 0 {
		return row.provider.DisplayName, true
	}
	prev := m.dialogModelRows[matches[pos-1]]
	if prev.provider.Name != row.provider.Name {
		return row.provider.DisplayName, true
	}
	return "", false
}

// openDialog switches the UI to a selection dialog. Themes and composer
// styles start with the cursor on the applied choice.
func (m *ui) openDialog(kind dialogKind) tea.Cmd {
	m.dialog = dialogState{kind: kind, open: true, cursor: 0, loading: false}
	switch kind {
	case dialogThemes:
		m.dialogItems = likhaui.ThemeNames()
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
		// All-provider fetch (specs/all-models): cursor starts on the live
		// model row once the sections arrive. The refusal when nothing is
		// configured stays in handleCommand with zero network traffic.
		return m.startModelsFetch()
	case dialogSessions:
		// Sessions are already local; listing is synchronous.
		m.listSessionsDialog()
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

// listSessionsDialog fills the sessions dialog rows from the store. A list
// error or an empty store closes the dialog with a visible entry instead.
func (m *ui) listSessionsDialog() {
	summaries, err := m.store.List()
	if err != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "List sessions: " + err.Error()})
		m.dialog = dialogState{}
		return
	}
	if len(summaries) == 0 {
		m.entries = append(m.entries, entry{role: "Likha", content: "No saved sessions for this repository yet."})
		m.dialog = dialogState{}
		return
	}
	m.sessionIDs = make([]string, len(summaries))
	m.dialogItems = make([]string, len(summaries))
	for i, item := range summaries {
		m.sessionIDs[i] = item.ID
		m.dialogItems[i] = item.Title + "  " + item.Updated.Local().Format("2006-01-02 15:04")
	}
}

// commandHelp is the text /help prints and unknown-command errors point to.
const commandHelp = "Commands: /compact [focus] summarize the conversation into a compact brief; /sessions [n] list or resume a saved session; /tools inspect tool availability, source, and permissions; /tools clear-output explicitly clear this session's retained output; /models list models from every configured provider; /providers manage a provider's stored key (auth); switching happens through /models; /plan toggle read-only plan mode (edits, commands, and MCP calls refuse without approval flow); /agents inspect the explore profile, task tree, and child transcripts; /todo show the persisted plan checklist; /skills list the discovered global skill catalog; /skill <name> [request] load one skill and start a turn; /quit exit; /help this list. Ctrl+O inspects tool results without losing your draft. Selections open a dialog: ↑/↓ navigate, type to filter, Enter apply, Esc cancel. Unknown /commands are not sent to the model; // sends a literal slash."

// handleCommand dispatches a leading-slash input. Reserved commands act on
// the application and never reach the model; unknown commands restore the
// draft so nothing is lost.
func (m *ui) handleCommand(line string) tea.Cmd {
	m.layoutWidth = 0 // command results change the conversation body
	if strings.HasPrefix(line, "//") {
		// Escaped literal slash: strip one and send as a normal prompt.
		return m.startTurn(line[1:], nil)
	}
	name, arg, _ := strings.Cut(line[1:], " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "quit":
		m.cancelOAuthLogin()
		if m.cancel != nil {
			if m.abandon != nil {
				close(m.abandon)
			}
			m.cancel()
		}
		return tea.Quit
	case "help":
		m.entries = append(m.entries, entry{role: "Likha", content: commandHelp})
		return nil
	case "tools":
		return m.handleToolsCommand(arg)
	case "agents":
		if arg != "" {
			m.entries = append(m.entries, entry{role: "Error", content: "Use /agents to inspect explore tasks; user-authored profiles are not supported."})
			return nil
		}
		return m.openAgentInspection()
	case "plan":
		if arg != "" {
			m.entries = append(m.entries, entry{role: "Error", content: "The /plan command takes no arguments; it toggles read-only plan mode."})
			return nil
		}
		if m.working || m.pending != nil {
			// Reserved commands stay inactive during a run or pending review;
			// the mode changes at turn boundaries only, with a visible note.
			m.entries = append(m.entries, entry{role: "Likha", content: "Plan mode cannot change while a run is active or a review is pending."})
			return nil
		}
		m.planMode = !m.planMode
		if m.planMode {
			m.entries = append(m.entries, entry{role: "Likha", content: "Plan mode enabled: workspace edits, shell commands, and MCP calls refuse without an approval flow until you toggle /plan off."})
		} else {
			m.entries = append(m.entries, entry{role: "Likha", content: "Plan mode disabled: edits and commands propose for approval again as usual."})
		}
		return nil
	case "todo":
		if arg != "" {
			m.entries = append(m.entries, entry{role: "Error", content: "The /todo command takes no arguments; it shows the persisted checklist."})
			return nil
		}
		m.entries = append(m.entries, entry{role: "Likha", content: m.planSummaryView()})
		return nil
	case "skills":
		if arg != "" {
			m.entries = append(m.entries, entry{role: "Error", content: "The /skills command takes no arguments; use /skill <name> [request] to load one."})
			return nil
		}
		m.entries = append(m.entries, entry{role: "Likha", content: m.describeSkills()})
		return nil
	case "skill":
		parts := strings.SplitN(arg, " ", 2)
		name := strings.TrimSpace(parts[0])
		if m.client == nil {
			m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
			return nil
		}
		if name == "" {
			m.entries = append(m.entries, entry{role: "Error", content: "Usage: /skill <name> [request]. /skills lists the discovered catalog."})
			return nil
		}
		body, origin, err := m.loadUserSkill(name)
		if err != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Skill load refused: " + err.Error() + " Nothing was sent to the provider."})
			return nil
		}
		provenance := "Loaded skill '" + name + "' (origin " + origin + "): instruction text reaches the configured provider; runtime policy stays authoritative even if the text demands otherwise."
		m.entries = append(m.entries, entry{role: "Likha", content: provenance})
		request := ""
		if len(parts) > 1 {
			request = strings.TrimSpace(parts[1])
		}
		prompt := "Follow the loaded skill '" + name + "' instructions for this request.\n\nSkill instruction text (user-managed, sent to the provider; untrusted relative to runtime policy):\n\n" + body + "\n\nRequest:\n" + request
		return m.startTurn(prompt, nil)
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
			m.entries = append(m.entries, entry{role: "Likha", content: "Nothing to compact yet."})
			return nil
		}
		return m.startCompaction(arg)
	case "models":
		if m.client == nil {
			m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
			return nil
		}
		// Zero-network refusal when nothing is configured: no stored key
		// or sign-in anywhere and no known live row. Otherwise the dialog
		// lists through the live client as its active section.
		if apis, oauth := m.modelsSources(); len(apis) == 0 && len(oauth) == 0 {
			if _, ok := lookupProviderByBase(m.client.Base(), m.conn.ProviderCanonical); !ok {
				m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
				return nil
			}
		}
		return m.openDialog(dialogModels)
	case "providers":
		return m.handleProvidersCommand(line, arg)
	case "sessions":
		return m.handleSessionsCommand(arg)
	case "themes":
		return m.handleThemesCommand(arg)
	case "mcp":
		m.entries = append(m.entries, entry{role: "Likha", content: m.conn.Mcp.Status()})
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

// resetSessionGrants drops every Approve always grant — commands, edits, and
// MCP server trust — so none carries into another session
// (specs/approve-always).
func (m *ui) resetSessionGrants() {
	m.commandGrants = &cmdpolicy.Grants{}
	m.editGrant = &agent.EditGrant{}
	m.conn.Mcp.ResetTrust()
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
	m.restoreTaskRecords()
	snapshot = m.snapshot
	m.taskRuntime = nil
	m.resetAgentInspection()
	m.closeAsk()
	m.closeConsent()
	m.planMode = false // a resumed session never inherits a live mode
	m.webGrants = nil
	m.resetSessionGrants()
	m.refreshProfileCatalog()
	m.restorePlan()
	m.toolRecords = append([]session.ToolRecord(nil), snapshot.ToolRecords...)
	m.toolInspector = toolInspectionState{}
	m.toolCatalogGen++
	m.history = snapshot.History
	m.recalculateContext(m.history)
	m.freshSession = false // a resumed session never re-generates its name
	m.entries = m.entries[:0]
	for _, saved := range snapshot.Entries {
		m.entries = append(m.entries, entry{role: saved.Role, content: saved.Content})
	}
	m.openOutputStore()
	m.reportInterruptedTasks()
	m.reportUnansweredQuestions()
	m.refreshStatusSessionTitle()
	m.streamBuf.Reset()
	m.streaming = -1
	m.reasoningBuf.Reset()
	m.reasoningStream = -1
	m.resetThoughts() // expansion and measured durations belong to the old view
	m.pending = nil
	m.reviewSeen = nil
	m.reviewFocus = focusApprove
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
	if m.dialog.kind == dialogSessions && m.dialog.deleteConfirm {
		// The confirmation owns every key: nothing reaches the query.
		return m, m.updateSessionDelete(msg)
	}
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		r := msg.Runes
		if msg.Type == tea.KeySpace {
			r = []rune{' '}
		}
		m.dialog.query += string(r)
		m.dialog.cursor = 0 // reset to the first visible match on query change
		if m.dialog.kind == dialogModels {
			m.modelsNavigated = true
		}
		m.previewDialogTheme(m.dialogMatches())
		return m, nil
	case tea.KeyBackspace:
		if runes := []rune(m.dialog.query); len(runes) > 0 {
			m.dialog.query = string(runes[:len(runes)-1])
			m.dialog.cursor = 0
		}
		if m.dialog.kind == dialogModels {
			m.modelsNavigated = true
		}
		m.previewDialogTheme(m.dialogMatches())
		return m, nil
	}
	matches := m.dialogMatches()
	switch msg.String() {
	case "up":
		if m.dialog.kind == dialogModels {
			m.modelsNavigated = true
		}
		if m.dialog.cursor > 0 {
			m.dialog.cursor--
		}
		m.previewDialogTheme(matches)
	case "down":
		if m.dialog.kind == dialogModels {
			m.modelsNavigated = true
		}
		if m.dialog.cursor < len(matches)-1 {
			m.dialog.cursor++
		}
		m.previewDialogTheme(matches)
	case "pgup":
		if m.dialog.kind == dialogModels {
			m.modelsNavigated = true
		}
		m.dialog.cursor = max(0, m.dialog.cursor-m.dialogWindow())
		m.previewDialogTheme(matches)
	case "pgdown":
		if m.dialog.kind == dialogModels {
			m.modelsNavigated = true
		}
		m.dialog.cursor = min(len(matches)-1, m.dialog.cursor+m.dialogWindow())
		m.previewDialogTheme(matches)
	case "enter":
		if len(matches) == 0 {
			// Empty match set: Enter does nothing while nothing is visible.
			return m, nil
		}
		return m, m.confirmDialog(matches)
	case "ctrl+d":
		// Letters extend the query, so delete rides on a control chord.
		if m.dialog.kind == dialogSessions {
			m.openSessionDelete(matches)
		}
		return m, nil
	case "esc":
		if m.dialog.query != "" {
			// Esc clears the query first, then closes on a second Esc.
			m.dialog.query = ""
			m.dialog.cursor = 0
			if m.dialog.kind == dialogModels {
				m.modelsNavigated = true
			}
			if m.dialog.kind == dialogThemes {
				m.previewTheme(m.themeName)
			}
			m.layoutWidth = 0
			return m, nil
		}
		// Discard: nothing applied; the item list is dropped with the dialog
		// so a later open starts clean and view math never indexes stale rows.
		wasThemes := m.dialog.kind == dialogThemes
		wasModels := m.dialog.kind == dialogModels
		m.dialog = dialogState{}
		m.dialogItems = nil
		if wasModels {
			m.dialogModelRows = nil
		}
		if wasThemes {
			m.previewTheme(m.themeName)
		}
		m.layoutWidth = 0
		return m, nil
	}
	return m, nil
}

// previewDialogTheme re-resolves the live styles to the highlighted theme
// candidate without committing: Enter stores, Esc restores. Non-theme
// dialogs and empty match sets are no-ops.
func (m *ui) previewDialogTheme(matches []int) {
	if m.dialog.kind != dialogThemes || m.dialog.cursor >= len(matches) {
		return
	}
	origIndex := matches[m.dialog.cursor]
	if origIndex < 0 || origIndex >= len(m.dialogItems) {
		return
	}
	m.previewTheme(m.dialogItems[origIndex])
}

// dialogWindow is the number of rows the dialog list can show.
func (m *ui) dialogWindow() int {
	return max(1, m.height-10)
}

// confirmDialog applies the highlighted selection and closes the dialog.
// Enter while the models list is still loading does nothing, so the dialog
// cannot be dismissed into an empty selection by accident.
func (m *ui) confirmDialog(matches []int) tea.Cmd {
	if m.dialog.loading && !(m.dialog.kind == dialogModels && len(m.dialogModelRows) > 0) {
		return nil
	}
	if m.dialog.cursor >= len(matches) {
		m.dialog = dialogState{}
		return nil
	}
	origIndex := matches[m.dialog.cursor]
	if m.dialog.kind == dialogModels {
		// Selectable rows only: headers never enter matches, so a header
		// can never be confirmed. Stale-list guard over the model rows.
		if origIndex < 0 || origIndex >= len(m.dialogModelRows) {
			m.dialog = dialogState{}
			m.dialogModelRows = nil
			m.dialogModelsNote = ""
			m.layoutWidth = 0
			return nil
		}
		row := m.dialogModelRows[origIndex]
		m.dialog = dialogState{}
		m.dialogModelRows = nil
		m.dialogModelsNote = ""
		m.layoutWidth = 0
		m.applyModelRow(row)
		// applyModelRow installs metadata for the newly selected provider/model;
		// resolve after that update so the new limit wins over the old model's.
		if m.modelName == row.model && m.isLiveProvider(row.provider) {
			m.resolveContextWindow()
			m.recalculateContext(m.history)
		}
		return nil
	}
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
	case dialogSessions:
		m.dialog = dialogState{}
		m.layoutWidth = 0
		return m.resumeSession(m.sessionIDs[origIndex])
	case dialogProviders:
		// Enter opens the row's auth surface; the dialog stays open beneath
		// (Esc returns to it) and nothing on this surface ever switches the
		// live provider — activation happens through /models.
		m.layoutWidth = 0
		return m.openProviderAuth(model.Providers[origIndex])
	}
	return nil
}

// dialogView renders the shared selection dialog over the dimmed main UI:
// the conversation is drawn first, everything outside the centered selection
// box is dimmed, and the box itself renders at full intensity on top. The
func (m *ui) dialogView() string {
	base := strings.Split(m.mainView(), "\n")
	for i := range base {
		base[i] = dimRowOn(base[i], m.theme.BaseBG())
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
		if m.dialog.deleteConfirm {
			return m.sessionDeleteView(base)
		}
		title, hint = "Session selection", "↑/↓ navigate  PgUp/PgDn page  Enter resume  Ctrl+D delete  Esc cancel"
	case dialogProviders:
		title, hint = "Provider selection", "↑/↓ navigate  PgUp/PgDn page  Enter auth  Esc cancel"
	}
	items := m.dialogItems
	matches := m.dialogMatches()
	if m.dialog.loading && (m.dialog.kind != dialogModels || len(m.dialogModelRows) == 0) {
		// Cold open: the list is still in flight with no rows to filter or
		// label, so matches must not index the stale previous list.
		items = []string{"Fetching model list…"}
		matches = nil
	}
	isModels := m.dialog.kind == dialogModels && (!m.dialog.loading || len(m.dialogModelRows) > 0)
	// The visible rows are the query-filtered subset; the cursor indexes
	// into it, so the highlighted row must be resolved through the matches.
	if m.dialog.query != "" {
		title = "q: \"" + m.dialog.query + "\"" + title
	}
	type visibleRow struct {
		header  string   // non-empty: section header, non-selectable
		label   string   // model rows only
		details []string // model rows: known context and price, right-aligned
		pos     int      // match-space position of the model row
	}
	var visible []visibleRow
	if isModels {
		for pos, orig := range matches {
			if header, ok := m.modelHeaderAt(matches, pos); ok {
				visible = append(visible, visibleRow{header: header})
			}
			visible = append(visible, visibleRow{label: m.modelLabel(orig), details: m.modelDetailParts(orig), pos: pos})
		}
	} else {
		visible = make([]visibleRow, len(matches))
		for i, orig := range matches {
			visible[i] = visibleRow{label: m.dialogLabel(orig, items), pos: i}
		}
	}

	// Box width tracks the widest item so nothing is truncated; padding and
	// borders account for the two-space gutter. Measure by display width,
	// not len(): `len` counts UTF-8 bytes, so a CJK label (3 bytes/cell)
	// inflates the box and a long label can even shift the box off-center.
	boxWidth := runewidth.StringWidth(title)
	for _, row := range visible {
		text := row.label
		if row.header != "" {
			text = row.header
		}
		if w := runewidth.StringWidth(text) + 12; w > boxWidth {
			boxWidth = w
		}
		// A model row also fits its details: marker, label, a two-cell
		// gap, the details, and the same right margin the label keeps.
		if details := modelDetailText(row.details); details != "" {
			if w := runewidth.StringWidth(row.label) + runewidth.StringWidth(details) + 8; w > boxWidth {
				boxWidth = w
			}
		}
	}
	if w := runewidth.StringWidth(hint) + 2; w > boxWidth {
		boxWidth = w
	}
	boxWidth = min(width-4, boxWidth+4)
	inner := boxWidth - 4 // "│ " + content + " │"

	// Every content row is fitted to the inner width BEFORE styling; a style
	// is never applied over an escape sequence and never re-fitted after.
	var content []string
	content = append(content, withBase(m.theme.Title, m.theme.Base).Render(fit(title, inner)))
	content = append(content, m.theme.Base.Render(fit("", inner)))
	if m.dialog.loadErr != "" {
		for _, line := range wrap("Error: "+m.dialog.loadErr, inner) {
			content = append(content, withBase(m.theme.Error, m.theme.Base).Render(fit(line, inner)))
		}
	} else if m.dialog.loading && (m.dialog.kind != dialogModels || len(m.dialogModelRows) == 0) {
		content = append(content, withBase(m.theme.Muted, m.theme.Base).Render(fit("Fetching model list…", inner)))
	} else if len(visible) == 0 {
		// Empty match set under a query: an explicit row instead of a blank
		// box; Enter is a no-op while nothing is selectable.
		content = append(content, withBase(m.theme.Muted, m.theme.Base).Render(fit("No "+m.dialogKindName()+" match \""+m.dialog.query+"\"", inner)))
	} else if !isModels {
		windowed, start := windowList(len(visible), m.dialog.cursor, max(1, height-10))
		for i := start; i < start+windowed; i++ {
			label := visible[i].label
			if i == m.dialog.cursor {
				content = append(content, withBase(m.theme.Selected, m.theme.Base).Render(fit("> "+label, inner)))
			} else {
				content = append(content, m.theme.Base.Render(fit("  "+label, inner)))
			}
		}
	} else {
		if m.dialogModelsNote != "" {
			for _, line := range wrap(m.dialogModelsNote, inner) {
				content = append(content, withBase(m.theme.Muted, m.theme.Base).Render(fit(line, inner)))
			}
		}
		// Headers ride with their rows: find the visible index of the
		// cursor row, window the full row list around it, then back up
		// past a leading detached header so a section header never
		// renders without at least one of its rows.
		at := 0
		for vi, row := range visible {
			if row.header == "" && row.pos == m.dialog.cursor {
				at = vi
				break
			}
		}
		windowed, start := windowList(len(visible), at, max(1, height-10))
		end := min(len(visible), start+windowed)
		for len(visible[start:end]) > 0 && visible[start].header != "" && (start+1 >= end || visible[start+1].header != "") {
			start++
		}
		// Sticky section header: when the window opens mid-section, the
		// section title scrolled out while its rows stay visible — models
		// without their title (the reported shape). Pin the first row's
		// section title above the window. The pinned title occupies one
		// window slot; make room by dropping the tail row (when it is not
		// the cursor), else the leading row while its neighbor shares the
		// same section, else let the box grow by one row — a foreign
		// section's rows must never render under the pinned title.
		pinned := ""
		if start < end && visible[start].header == "" {
			pinned = m.dialogModelRows[matches[visible[start].pos]].provider.DisplayName
		}
		rowsEnd := end
		if pinned != "" {
			if rowsEnd-1 > at {
				rowsEnd--
			} else if start < rowsEnd-1 {
				next := visible[start+1]
				if next.header == "" {
					if q := m.dialogModelRows[matches[next.pos]].provider; q.Name == m.dialogModelRows[matches[visible[start].pos]].provider.Name {
						start++
					}
				}
			}
			content = append(content, withBase(m.theme.Title, m.theme.Base).Render(fit(pinned, inner)))
		}
		for _, row := range visible[start:rowsEnd] {
			if row.header != "" {
				content = append(content, withBase(m.theme.Title, m.theme.Base).Render(fit(row.header, inner)))
				continue
			}
			content = append(content, m.modelDialogRow(row.label, row.details, row.pos == m.dialog.cursor, inner))
		}
	}
	content = append(content, m.theme.Base.Render(fit("", inner)))
	if m.dialog.kind == dialogSessions {
		// The sessions hint carries the delete chord; wrap it at narrow
		// widths so no control is clipped out of view.
		for _, line := range wrapHint(hint, inner) {
			content = append(content, withBase(m.theme.Help, m.theme.Base).Render(fit(line, inner)))
		}
	} else {
		content = append(content, withBase(m.theme.Help, m.theme.Base).Render(fit(hint, inner)))
	}
	return m.overlayDialogBox(base, content, boxWidth)
}

// modelDialogRow renders one /models row exactly inner cells wide: the
// cursor marker and label on the left, the known details right-aligned.
// Details give way from the front (context before price) until the label
// keeps a readable width, so they never push the model name out of view
// and the row never overflows.
func (m *ui) modelDialogRow(label string, details []string, selected bool, inner int) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	need := min(12, runewidth.StringWidth(label))
	right := modelDetailText(details)
	for right != "" && inner-2-runewidth.StringWidth(right)-2 < need {
		details = details[1:]
		right = modelDetailText(details)
	}
	room := inner - 2
	if right != "" {
		room -= runewidth.StringWidth(right) + 2
	}
	left := fit(marker+fitText(label, max(0, room)), inner-runewidth.StringWidth(right))
	if selected {
		return withBase(m.theme.Selected, m.theme.Base).Render(left + right)
	}
	if right == "" {
		return m.theme.Base.Render(left)
	}
	return m.theme.Base.Render(left) + withBase(m.theme.Muted, m.theme.Base).Render(right)
}

// modelDetailText joins a /models row's detail parts: "1M ctx · $4/$20".
func modelDetailText(parts []string) string {
	return strings.Join(parts, " · ")
}

// overlayDialogBox draws the bordered box of pre-fitted content rows centered
// over the dimmed base rows.
func (m *ui) overlayDialogBox(base, content []string, boxWidth int) string {
	width, height := m.width, m.height
	top := max(0, (height-len(content)-2)/2)
	left := max(2, (width-boxWidth)/2)
	// Compose each overlaid row from the dimmed base text left of the box,
	// the full-intensity box row, and the dimmed base text right of it — the
	// background keeps its content instead of collapsing to a solid band.
	for j := 0; j < len(content)+2 && top+j < height; j++ {
		var box string
		switch {
		case j == 0:
			box = withBase(m.theme.Border, m.theme.Base).Render("╭" + strings.Repeat("─", boxWidth-2) + "╮")
		case j == len(content)+1:
			box = withBase(m.theme.Border, m.theme.Base).Render("╰" + strings.Repeat("─", boxWidth-2) + "╯")
		default:
			border := withBase(m.theme.Border, m.theme.Base)
			box = border.Render("│ ") + content[j-1] + border.Render(" │")
		}
		base[top+j] = spliceRowOn(base[top+j], left, boxWidth, box, m.theme.BaseBG())
	}
	return strings.Join(base[:height], "\n")
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
		// /models renders through modelLabel (live pair only), never here.
		return label
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
