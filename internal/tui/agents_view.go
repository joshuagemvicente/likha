package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"likha/internal/explore"
)

// Agent inspection has its own focus and scroll position. It never edits the
// composer, steering queue, main model history, or file-approval state.
type agentInspectionState struct {
	open, details, confirm bool
	cursor, scroll         int // -1 selects the built-in profile; tasks start at 0
	sessionID, taskID      string
	generation             uint64
	reader                 toolOutputInspectionReader
	detail                 agentDetailState

	// Bind confirmation to the original runtime and task run, not whichever
	// task happens to occupy the same list position when Enter arrives.
	confirmRunID   string
	confirmRuntime *explore.Manager
	runtimeOwner   *explore.Manager
	runtimeRunID   string
}

func (m *ui) setAgentOutputReader(reader toolOutputInspectionReader) {
	m.agentInspector.reader = reader
	m.resetAgentDetail()
}

// resetAgentInspection is the session-switch/compaction hook. The attached
// reader enforces session ownership; late detail pages are invalidated here.
func (m *ui) resetAgentInspection() {
	m.resetAgentDetail()
	reader, generation := m.agentInspector.reader, m.agentInspector.generation+1
	m.agentInspector = agentInspectionState{cursor: -1, reader: reader, generation: generation}
	m.layoutWidth = 0
}

// openAgentInspection is the idle /agents hook. Active entry uses a focused
// transcript task instead of interpreting a reserved command as steering.
func (m *ui) openAgentInspection() tea.Cmd {
	if m.working || m.pending != nil || m.mode == modeSetup || m.keyModal.open || m.dialog.open {
		m.status = "Agent profiles are available when no run or review is active"
		return nil
	}
	m.beginAgentInspection()
	return nil
}

// openAgentTaskInspection belongs on a focused task row's Enter/Ctrl+O action.
// It opens the tree with that stable task selected, including during a run.
func (m *ui) openAgentTaskInspection(taskID string) tea.Cmd {
	if m.pending != nil || m.mode == modeSetup || m.keyModal.open || m.dialog.open {
		return nil
	}
	records := m.agentTreeRecords()
	for i, record := range records {
		if record.ID == taskID {
			m.beginAgentInspection()
			m.agentInspector.cursor, m.agentInspector.taskID = i, taskID
			m.revealAgentSelection()
			return nil
		}
	}
	m.status = "No saved child record is available for this task"
	return nil
}

func (m *ui) beginAgentInspection() {
	m.resetAgentInspection()
	state := &m.agentInspector
	if state.reader == nil {
		state.reader = m.toolInspector.reader
	}
	state.open, state.sessionID = true, m.snapshot.ID
	// There is only one historical inspector on screen. Closing the tool
	// inspector changes no transcript focus, draft, or pending approval.
	if m.toolInspector.open {
		m.closeToolInspection()
	}
}

// Newly arrived approvals and normal setup/provider dialogs preempt inspection.
// No inspector action marks reviewSeen or replies to an approval request.
func (m *ui) agentInspectionVisible() bool {
	return m.agentInspector.open && m.agentInspector.sessionID == m.snapshot.ID &&
		m.pending == nil && m.mode != modeSetup && !m.keyModal.open && !m.dialog.open
}

// handleAgentInspection belongs after popup/modal/review handling and before
// tool-row focus or composer submission. A false result must reach the existing
// whole-run cancellation path, even while branch confirmation is open.
func (m *ui) handleAgentInspection(msg tea.KeyMsg) (handled bool, cmd tea.Cmd) {
	key := msg.String()
	if m.agentInspector.open && m.agentInspector.sessionID != m.snapshot.ID {
		m.resetAgentInspection()
	}
	if m.working && (key == "esc" || key == "ctrl+c") {
		return false, nil
	}
	if !m.agentInspectionVisible() || m.mention.open || m.commandPopup.open {
		return false, nil
	}
	if key == "ctrl+c" || key == "ctrl+d" {
		return false, nil
	}
	if key == "esc" {
		m.closeAgentInspection()
		return true, nil
	}
	state := &m.agentInspector
	if state.confirm {
		switch key {
		case "enter":
			return true, m.confirmAgentBranchCancellation()
		case "backspace", "ctrl+h", "alt+left", "left", "c":
			m.dismissAgentConfirmation()
		default:
			m.scrollAgentInspection(key)
		}
		return true, nil
	}
	if key == "c" {
		m.openAgentBranchConfirmation()
		return true, nil
	}
	if state.details {
		switch key {
		case "enter", "backspace", "ctrl+h", "alt+left":
			state.details, state.scroll = false, 0
			m.resetAgentDetail()
			m.revealAgentSelection()
			return true, nil
		}
		if state.taskID != "" {
			if handled, cmd := m.handleAgentDetailKey(msg); handled {
				return true, cmd
			}
		}
		if key == "left" {
			state.details, state.scroll = false, 0
			m.resetAgentDetail()
			m.revealAgentSelection()
			return true, nil
		}
		m.scrollAgentInspection(key)
		return true, nil
	}
	records := m.agentTreeRecords()
	m.syncAgentSelection(records)
	if len(records) == 0 {
		switch key {
		case "enter", "ctrl+o", "backspace", "ctrl+h", "alt+left":
			m.closeAgentInspection()
		default:
			m.scrollAgentInspection(key)
		}
		return true, nil
	}
	switch key {
	case "up", "shift+tab":
		m.selectAgentNode(records, toolWrappedIndex(state.cursor, len(records)+1)-1)
	case "down", "tab":
		m.selectAgentNode(records, toolWrappedIndex(state.cursor+2, len(records)+1)-1)
	case "pgup", "ctrl+p":
		step := min(max(1, m.agentInspectionBodyHeight()/4), len(records))
		m.selectAgentNode(records, max(-1, state.cursor-step))
	case "pgdown", "ctrl+n":
		step := min(max(1, m.agentInspectionBodyHeight()/4), len(records))
		m.selectAgentNode(records, min(len(records)-1, state.cursor+step))
	case "home", "p":
		m.selectAgentNode(records, -1)
	case "end":
		m.selectAgentNode(records, len(records)-1)
	case "enter", "ctrl+o":
		state.details, state.scroll = true, 0
		m.resetAgentDetail()
	case "backspace", "ctrl+h", "alt+left":
		m.closeAgentInspection()
	}
	return true, nil
}

func (m *ui) closeAgentInspection() {
	m.resetAgentDetail()
	m.agentInspector.open, m.agentInspector.details, m.agentInspector.confirm = false, false, false
	m.agentInspector.confirmRuntime = nil
	m.agentInspector.confirmRunID = ""
	m.agentInspector.generation++
	// Transcript scrolling/following and native selection were never changed.
}

// Consume inspector mouse events before transcript drag/scroll handling. Only
// the wheel changes state; ordinary clicks leave native terminal selection alone.
func (m *ui) handleAgentInspectionMouse(msg tea.MouseMsg) bool {
	if !m.agentInspectionVisible() {
		return false
	}
	direction := 0
	switch msg.Type {
	case tea.MouseWheelUp:
		direction = -1
	case tea.MouseWheelDown:
		direction = 1
	}
	if direction == 0 {
		return true
	}
	state := &m.agentInspector
	records := m.agentTreeRecords()
	if !state.details && !state.confirm && len(records) > 0 {
		m.syncAgentSelection(records)
		m.selectAgentNode(records, toolWrappedIndex(state.cursor+1+direction, len(records)+1)-1)
	} else {
		state.scroll += direction * scrollWheelLines
		m.clampAgentInspectionScroll()
	}
	return true
}

func (m *ui) scrollAgentInspection(key string) {
	state := &m.agentInspector
	switch key {
	case "up":
		state.scroll--
	case "down":
		state.scroll++
	case "pgup", "ctrl+p":
		state.scroll -= m.agentInspectionBodyHeight()
	case "pgdown", "ctrl+n":
		state.scroll += m.agentInspectionBodyHeight()
	case "home":
		state.scroll = 0
	case "end":
		state.scroll = len(m.agentInspectionLines())
	}
	m.clampAgentInspectionScroll()
}

func (m *ui) clampAgentInspectionScroll() {
	state := &m.agentInspector
	state.scroll = max(0, min(state.scroll, max(0, len(m.agentInspectionLines())-m.agentInspectionBodyHeight())))
}

type agentTaskIdentity struct {
	runID, taskID string
}

// agentTreeRecords is deterministic preorder: siblings use acceptance time,
// then stable ID. Missing parents and corrupt cycles remain inspectable rather
// than dropping records or looping. Parent edges cannot cross run identities.
func (m *ui) agentTreeRecords() []explore.Record {
	var records []explore.Record
	latest := make(map[agentTaskIdentity]int)
	for _, record := range m.taskRecords {
		if record.ID != "" && record.SessionID == m.snapshot.ID {
			identity := agentTaskIdentity{record.RunID, record.ID}
			if index, exists := latest[identity]; exists {
				if record.Version > records[index].Version ||
					(record.Version == records[index].Version && agentRecordTerminal(record) && !agentRecordTerminal(records[index])) {
					records[index] = record
				}
				continue
			}
			latest[identity] = len(records)
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if !a.AcceptedAt.Equal(b.AcceptedAt) {
			return a.AcceptedAt.Before(b.AcceptedAt)
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.RunID < b.RunID
	})
	byID := make(map[agentTaskIdentity]int, len(records))
	for i, record := range records {
		byID[agentTaskIdentity{record.RunID, record.ID}] = i
	}
	children := make(map[agentTaskIdentity][]int)
	var roots []int
	for i, record := range records {
		parent := agentTaskIdentity{record.RunID, record.ParentID}
		if _, exists := byID[parent]; record.ParentID == "" || !exists {
			roots = append(roots, i)
		} else {
			children[parent] = append(children[parent], i)
		}
	}
	ordered := make([]explore.Record, 0, len(records))
	visited := make([]bool, len(records))
	visit := func(root int) {
		stack := []int{root}
		for len(stack) > 0 {
			index := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if visited[index] {
				continue
			}
			visited[index] = true
			record := records[index]
			ordered = append(ordered, record)
			branch := children[agentTaskIdentity{record.RunID, record.ID}]
			for i := len(branch) - 1; i >= 0; i-- {
				stack = append(stack, branch[i])
			}
		}
	}
	for _, root := range roots {
		visit(root)
	}
	for i := range records {
		if !visited[i] {
			visit(i)
		}
	}
	return ordered
}

func (m *ui) syncAgentSelection(records []explore.Record) {
	state := &m.agentInspector
	if state.taskID == "" {
		state.cursor = -1
		return
	}
	for i, record := range records {
		if record.ID == state.taskID {
			state.cursor = i
			return
		}
	}
	// Never silently replace a detail/confirmation target with another node.
	if !state.details && !state.confirm {
		state.taskID, state.cursor = "", -1
	}
}

func (m *ui) selectAgentNode(records []explore.Record, cursor int) {
	state := &m.agentInspector
	state.cursor = max(-1, min(cursor, len(records)-1))
	state.taskID = ""
	if state.cursor >= 0 {
		state.taskID = records[state.cursor].ID
	}
	state.scroll = 0
	m.resetAgentDetail()
	m.revealAgentSelection()
}

func (m *ui) selectedAgentRecord() (explore.Record, bool) {
	if m.agentInspector.sessionID != m.snapshot.ID || m.agentInspector.taskID == "" {
		return explore.Record{}, false
	}
	var latest explore.Record
	found := false
	for _, record := range m.taskRecords {
		if record.ID == m.agentInspector.taskID && record.SessionID == m.snapshot.ID {
			if !found || record.Version > latest.Version {
				latest, found = record, true
			}
		}
	}
	return latest, found
}

func agentRecordTerminal(record explore.Record) bool {
	switch record.Status {
	case explore.Completed, explore.Limited, explore.Failed, explore.Cancelled, explore.Interrupted:
		return true
	default:
		return false
	}
}

func agentRecordActive(record explore.Record) bool {
	return record.Status == explore.Queued || record.Status == explore.Running || record.Status == explore.Waiting
}

func (m *ui) agentBranchCancellable(record explore.Record) bool {
	if !m.working || m.cancelling || m.taskRuntime == nil || !agentRecordActive(record) {
		return false
	}
	state := &m.agentInspector
	// Runtime identity is immutable. Cache it rather than cloning every child
	// transcript on each render just to check whether a historical row is live.
	if state.runtimeOwner != m.taskRuntime || state.runtimeRunID == "" {
		state.runtimeOwner, state.runtimeRunID = m.taskRuntime, ""
		for _, live := range m.taskRuntime.Snapshot() {
			if live.SessionID == m.snapshot.ID {
				state.runtimeRunID = live.RunID
				break
			}
		}
	}
	return record.RunID != "" && record.RunID == state.runtimeRunID
}

func (m *ui) agentLiveBranchCancellable(record explore.Record) bool {
	if !m.agentBranchCancellable(record) {
		return false
	}
	for _, live := range m.taskRuntime.Snapshot() {
		if live.ID == record.ID && live.RunID == record.RunID && live.SessionID == record.SessionID {
			return agentRecordActive(live)
		}
	}
	return false
}

func (m *ui) openAgentBranchConfirmation() {
	record, ok := m.selectedAgentRecord()
	if !ok {
		m.status = "Select an active task to cancel its branch"
		return
	}
	if !m.agentLiveBranchCancellable(record) {
		m.status = "Cancel branch is disabled for terminal or inactive tasks"
		return
	}
	// A late detail page must not reset confirmation scrolling. Saved output
	// remains on the record and can be opened again after this dedicated action.
	m.resetAgentDetail()
	state := &m.agentInspector
	state.confirm, state.scroll = true, 0
	state.confirmRunID, state.confirmRuntime = record.RunID, m.taskRuntime
}

func (m *ui) dismissAgentConfirmation() {
	state := &m.agentInspector
	state.confirm, state.scroll = false, 0
	state.confirmRunID, state.confirmRuntime = "", nil
	if !state.details {
		m.revealAgentSelection()
	}
}

type agentBranchCancelledMsg struct {
	sessionID string
	runID     uint64
	taskID    string
	err       error
}

func (m *ui) confirmAgentBranchCancellation() tea.Cmd {
	state := &m.agentInspector
	record, ok := m.selectedAgentRecord()
	if !ok || record.RunID != state.confirmRunID || state.confirmRuntime != m.taskRuntime || !m.agentLiveBranchCancellable(record) {
		m.status = "Cancel branch is no longer available; partial task records are preserved"
		m.dismissAgentConfirmation()
		return nil
	}
	// The manager owns cancellation, descendant scope, and exactly one terminal
	// outcome. This dedicated confirmation never uses the file-approval dialog.
	runtime, sessionID, runID := m.taskRuntime, m.snapshot.ID, m.runID
	m.status = "Cancelling explore branch " + toolShortText(record.ID, 64)
	m.dismissAgentConfirmation()
	// Persistence/progress callbacks can wait for the UI event queue. Dispatch
	// cancellation outside Update so that queue keeps draining during the join.
	return func() tea.Msg {
		return agentBranchCancelledMsg{sessionID: sessionID, runID: runID, taskID: record.ID, err: runtime.CancelBranch(record.ID)}
	}
}

func (m *ui) agentDescendants(record explore.Record) int {
	children := make(map[string][]string)
	for _, candidate := range m.agentTreeRecords() {
		if candidate.RunID == record.RunID && candidate.ParentID != "" {
			children[candidate.ParentID] = append(children[candidate.ParentID], candidate.ID)
		}
	}
	seen := map[string]bool{record.ID: true}
	stack := append([]string(nil), children[record.ID]...)
	count := 0
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		count++
		stack = append(stack, children[id]...)
	}
	return count
}

// applyTaskRecord only upserts child inspection data. Callers still own event
// run-generation filtering, main transcript attribution, and session saving.
// Required terminal records from a cancelling run must still reach this hook.
func (m *ui) applyTaskRecord(record explore.Record) {
	if record.ID == "" || record.SessionID != m.snapshot.ID {
		return
	}
	for i, previous := range m.taskRecords {
		if previous.ID != record.ID {
			continue
		}
		if previous.SessionID != record.SessionID || previous.RunID != record.RunID ||
			record.Version < previous.Version ||
			(record.Version == previous.Version && !(agentRecordTerminal(record) && !agentRecordTerminal(previous))) ||
			(agentRecordTerminal(previous) && !agentRecordTerminal(record)) {
			return
		}
		m.taskRecords[i] = record
		return
	}
	m.taskRecords = append(m.taskRecords, record)
}

func (m *ui) agentInspectionLines() []toolInspectionLine {
	if m.agentInspector.confirm {
		return m.agentBranchConfirmationLines()
	}
	if m.agentInspector.details {
		if m.agentInspector.taskID == "" {
			return m.agentProfileLines()
		}
		if record, ok := m.selectedAgentRecord(); ok {
			return m.agentDetailLines(record)
		}
		return m.appendToolInspectionLines(nil, "This task is no longer available in the current session. Back returns to the tree; no task is relaunched.", m.theme.Muted)
	}
	lines, _, _ := m.agentTreeLines()
	return lines
}

func (m *ui) agentProfileLines() []toolInspectionLine {
	var lines []toolInspectionLine
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	provider := m.conn.Provider
	if provider == "" {
		provider = m.conn.ProviderCanonical
	}
	add("Built-in explore profile", m.theme.Title)
	add("Repository-read-only work in a separate child context. Only the built-in explore profile is available.", m.theme.Normal)
	add("Inherited provider: "+agentRecordedText(provider)+" · model: "+agentRecordedText(m.modelName), m.theme.Muted)
	add("No cheaper model or provider fallback.", m.theme.Muted)
	add("", m.theme.Base)
	add("Exact tool ceiling", m.theme.Title)
	add("Depth 1: glob, read, grep, task (explore only).", m.theme.Normal)
	add("Depth 2: glob, read, grep. No task; crafted deeper spawns are refused.", m.theme.Normal)
	add("The effective set is parent capability ∩ user/mode policy ∩ this ceiling. Plan mode permits the same read-only tree.", m.theme.Muted)
	add("Excluded: edits, shell, MCP, web, skills, questions, checklist, and artifact-read tools.", m.theme.Muted)
	add("", m.theme.Base)
	add("Nesting and budgets", m.theme.Title)
	add(fmt.Sprintf("Main depth 0 → explore depth 1 → explore depth 2. Maximum depth: %d.", explore.MaxDepth), m.theme.Normal)
	add(fmt.Sprintf("Shared execution limit: %d children per main run, not per parent. Queued/waiting parents release their execution permit.", explore.MaxExecuting), m.theme.Muted)
	add(fmt.Sprintf("Shared spawn limit: %d accepted children per main run, including nested, completed, failed, limited, and cancelled tasks. Finishing does not restore this budget.", explore.MaxChildren), m.theme.Muted)
	add(fmt.Sprintf("Per-child deadline: %s from acceptance, including queue and nested wait time; the earlier ancestor deadline wins.", explore.ChildTimeout), m.theme.Normal)
	add(fmt.Sprintf("Per-child model request limit: %d, with no extra summarization request after exhaustion.", explore.MaxRequests), m.theme.Muted)
	add("", m.theme.Base)
	add("Hosted provider and child usage", m.theme.Title)
	add("Nested agents use the configured hosted provider and can multiply cost. Time, concurrency, and request limits are not a cost cap.", m.theme.Warning)
	add("Reported child usage is separate from main request context. Missing tokens or pricing stay unknown.", m.theme.Muted)
	add("Inspection reads saved session data locally. It does not launch a task, make a provider call, merge child transcripts into main context, or grant permission.", m.theme.Muted)
	return lines
}

func agentRecordedText(text string) string {
	if strings.TrimSpace(text) == "" {
		return "unknown (not recorded)"
	}
	return text
}

func agentTaskLabel(record explore.Record) string {
	if strings.TrimSpace(record.Description) != "" {
		return record.Description
	}
	return "Explore task"
}

func (m *ui) agentSpawnUsed(runID string) int {
	count, used := 0, 0
	seen := make(map[string]bool)
	for _, record := range m.taskRecords {
		if record.SessionID == m.snapshot.ID && record.RunID == runID {
			if record.ID != "" && !seen[record.ID] {
				count++
				seen[record.ID] = true
			}
			used = max(used, record.SpawnUsed)
		}
	}
	return max(count, used)
}

// The tree uses a compact multi-line record rather than a fixed-column table.
// Every node retains identity and explicit state on narrow terminals.
func (m *ui) agentTreeLines() (lines []toolInspectionLine, selectedStart, selectedEnd int) {
	records := m.agentTreeRecords()
	m.syncAgentSelection(records)
	state := &m.agentInspector
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	if len(records) == 0 {
		add("No explore tasks have been recorded in this session.", m.theme.Title)
		add("Tasks appear when the main model delegates scoped repository exploration. Opening /agents launches nothing.", m.theme.Muted)
		add("", m.theme.Base)
		lines = append(lines, m.agentProfileLines()...)
		add("", m.theme.Base)
		for _, line := range m.profileCatalogLines() {
			add(line, m.theme.Muted)
		}
		return lines, 0, 0
	}
	prefix, style := "  ", m.theme.Muted
	if state.taskID == "" {
		prefix, style = "> ", m.theme.Selected
	}
	add(prefix+"Built-in explore profile · Enter for exact ceiling and budgets", style)
	add(fmt.Sprintf("  Max depth %d · max %d executing · %d accepted/run · %s incl. wait · %d model requests/child", explore.MaxDepth, explore.MaxExecuting, explore.MaxChildren, explore.ChildTimeout, explore.MaxRequests), m.theme.Muted)
	add("  Nested hosted-provider calls can multiply cost; these limits are not a cost cap.", m.theme.Warning)
	if state.taskID == "" {
		selectedEnd = len(lines)
	}
	add("", m.theme.Base)
	for _, line := range m.profileCatalogLines() {
		add(line, m.theme.Muted)
	}
	add("", m.theme.Base)
	add(fmt.Sprintf("Session task tree · %d recorded child task(s)", len(records)), m.theme.Title)
	now := time.Now()
	for _, record := range records {
		prefix, style = "  ", m.theme.Muted
		if record.ID == state.taskID {
			prefix, style = "> ", m.theme.Selected
			selectedStart = len(lines)
		}
		indent := strings.Repeat("  ", max(0, min(record.Depth-1, explore.MaxDepth)))
		label := agentTaskLabel(record)
		if profileLabel := m.agentProfileLabel(record); profileLabel != "explore" {
			label = "[" + profileLabel + "] " + label
		}
		add(prefix+indent+"["+agentRecordedText(string(record.Status))+"] "+toolShortText(label, explore.MaxDescriptionRunes), style)
		parent := record.ParentID
		if parent == "" {
			parent = "main (depth 0)"
		}
		add(fmt.Sprintf("  Task: %s · parent: %s · depth: %d · run: %s", record.ID, parent, record.Depth, agentRecordedText(record.RunID)), m.theme.Muted)
		add("  Provider: "+agentRecordedText(record.Provider)+" · model: "+agentRecordedText(record.Model), m.theme.Muted)
		add(fmt.Sprintf("  Model requests: %d/%d · shared accepted children: %d/%d", record.Rounds, explore.MaxRequests, m.agentSpawnUsed(record.RunID), explore.MaxChildren), m.theme.Muted)
		add("  "+agentTaskTimeSummary(record, now, contentWidth(m.width)-2), m.theme.Muted)
		if record.ID == state.taskID {
			add("  Child usage: "+AgentUsageSummary(record), m.theme.Muted)
			if m.agentBranchCancellable(record) {
				add("  Cancel branch: available · c opens a dedicated confirmation", m.theme.Warning)
			} else {
				add("  Cancel branch: disabled (terminal or inactive task)", m.theme.Muted)
			}
			selectedEnd = len(lines)
		}
		add("", m.theme.Base)
	}
	return lines, selectedStart, selectedEnd
}

func agentTaskTimeSummary(record explore.Record, now time.Time, width int) string {
	text := agentWaitActiveText(record, now, width)
	if !record.AcceptedAt.IsZero() {
		if !record.StartedAt.IsZero() {
			text += " · initial queue: " + agentInspectionDuration(record.StartedAt.Sub(record.AcceptedAt))
		} else if record.Status == explore.Queued {
			text += " · queued since acceptance"
		}
	}
	if record.Status == explore.Waiting {
		text += " · waiting for child now"
	}
	if record.Deadline.IsZero() {
		return text + " · deadline: unknown (not recorded)"
	}
	text += " · deadline: " + record.Deadline.Format("2006-01-02 15:04:05 MST")
	if agentRecordActive(record) {
		if !now.Before(record.Deadline) {
			text += " (elapsed; awaiting runtime outcome)"
		} else {
			text += " (" + agentInspectionDuration(record.Deadline.Sub(now)) + " left)"
		}
	}
	return text
}

// agentWaitActiveText replaces a single elapsed figure with the record's
// cumulative wait (no execution permit: queue and nested child wait) and
// active (holding a permit) totals, as of the record's latest version. When
// both segments do not fit width, the wait segment is dropped first so the
// active figure survives instead of being clipped. Records saved before the
// split was recorded fall back to elapsed time since acceptance.
func agentWaitActiveText(record explore.Record, now time.Time, width int) string {
	if !agentWaitActiveRecorded(record) {
		end := record.FinishedAt
		if end.IsZero() && agentRecordActive(record) {
			end = now
		}
		if record.AcceptedAt.IsZero() || end.IsZero() {
			return "wait/active: unknown (timestamps not recorded)"
		}
		return "wait/active: not recorded · elapsed " + agentInspectionDuration(end.Sub(record.AcceptedAt))
	}
	wait := "wait " + agentInspectionDuration(agentMillis(record.WaitMs))
	active := "active " + agentInspectionDuration(agentMillis(record.ActiveMs))
	if full := wait + " · " + active; width <= 0 || lipgloss.Width(full) <= width {
		return full
	}
	return active
}

// agentWaitActiveRecorded is false only for a record whose timestamps span
// time the totals do not: older records persisted before wait/active
// accounting decode both totals as zero.
func agentWaitActiveRecorded(record explore.Record) bool {
	if record.WaitMs > 0 || record.ActiveMs > 0 {
		return true
	}
	if record.AcceptedAt.IsZero() {
		return false
	}
	return record.FinishedAt.IsZero() || record.FinishedAt.Sub(record.AcceptedAt) < time.Second
}

func agentMillis(ms int64) time.Duration {
	return time.Duration(max(0, ms)) * time.Millisecond
}

func agentInspectionDuration(duration time.Duration) string {
	return max(time.Duration(0), duration).Truncate(time.Second).String()
}

func (m *ui) revealAgentSelection() {
	if m.agentInspector.details || m.agentInspector.confirm {
		return
	}
	_, start, end := m.agentTreeLines()
	state := &m.agentInspector
	body := m.agentInspectionBodyHeight()
	if start < state.scroll || end-start > body {
		state.scroll = start
	} else if end > state.scroll+body {
		state.scroll = min(start, end-body)
	}
}

func (m *ui) agentBranchConfirmationLines() []toolInspectionLine {
	var lines []toolInspectionLine
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	add("Cancel branch", m.theme.Title)
	record, ok := m.selectedAgentRecord()
	if !ok {
		add("This task is no longer available. Back dismisses confirmation.", m.theme.Muted)
		return lines
	}
	add("Target: "+agentTaskLabel(record), m.theme.Normal)
	add("Task: "+record.ID+" · run: "+agentRecordedText(record.RunID), m.theme.Muted)
	add(fmt.Sprintf("Scope: this task and all descendants (%d currently recorded). Other branches may continue.", m.agentDescendants(record)), m.theme.Warning)
	add("The runtime stops pending/running descendants and delivers their terminal results. Partial transcripts and findings remain available; cancelling does not restore spawn budget.", m.theme.Muted)
	add("", m.theme.Base)
	if !m.agentBranchCancellable(record) || record.RunID != m.agentInspector.confirmRunID || m.agentInspector.confirmRuntime != m.taskRuntime {
		add("Cancel branch is disabled: the target is terminal or its runtime is no longer active. Enter/Back returns without cancelling.", m.theme.Muted)
	} else {
		add("Enter confirms Cancel branch. Back dismisses without cancelling.", m.theme.Warning)
	}
	add("This confirmation is not file approval and does not pause deadlines.", m.theme.Muted)
	if m.working {
		add("Esc/Ctrl+C still cancels the whole active run, including every descendant.", m.theme.Warning)
	}
	return lines
}

func (m *ui) agentInspectionFooter() []string {
	state := &m.agentInspector
	var text string
	switch {
	case state.confirm:
		text = "Enter confirm Cancel branch · Back dismiss\n↑/↓ PgUp/PgDn scroll"
	case state.details && state.taskID != "":
		text = "↑/↓ PgUp/PgDn scroll · Enter/Back tree"
		if record, ok := m.selectedAgentRecord(); ok {
			if len(agentDetailArtifacts(record)) > 0 {
				text += "\no/Ctrl+O retained output · ←/→ page"
			}
			if m.agentBranchCancellable(record) {
				text += "\nc Cancel branch (confirmation)"
			} else {
				text += "\nCancel branch disabled"
			}
		}
	case state.details || len(m.agentTreeRecords()) == 0:
		text = "↑/↓ PgUp/PgDn scroll · Home/End\nEnter/Back return"
	default:
		text = "↑/↓ Tab/Shift+Tab focus · Enter/Ctrl+O details\nPgUp/PgDn page · Home profile · Back close"
		if record, ok := m.selectedAgentRecord(); ok {
			if m.agentBranchCancellable(record) {
				text += "\nc Cancel branch (confirmation)"
			} else {
				text += "\nCancel branch disabled"
			}
		}
	}
	if m.working {
		text += "\nEsc/Ctrl+C cancel whole active run"
	} else {
		text += "\nEsc close"
	}
	return wrap(text, contentWidth(m.width))
}

func (m *ui) agentInspectionBodyHeight() int {
	return max(1, m.height-2-len(m.agentInspectionFooter()))
}

// agentInspectionView mirrors the tool inspector's plain, selectable terminal
// surface. All untrusted labels, IDs, provider/model metadata, and detail output
// pass through wrap/fit before theme styling; terminal escapes are never emitted.
func (m *ui) agentInspectionView() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if !m.agentInspectionVisible() {
		return m.mainView()
	}
	lines := m.agentInspectionLines()
	foot := m.agentInspectionFooter()
	body := m.agentInspectionBodyHeight()
	state := &m.agentInspector
	state.scroll = max(0, min(state.scroll, max(0, len(lines)-body)))
	title := "Agent tree and explore profile"
	if state.details {
		title = "Explore profile"
		if state.taskID != "" {
			title = "Child task inspection"
		}
	}
	if state.confirm {
		title = "Confirm Cancel branch"
	}
	title += fmt.Sprintf(" · lines %d–%d/%d", min(len(lines), state.scroll+1), min(len(lines), state.scroll+body), len(lines))
	subtitle := "Local session inspection · no launch or permission grant"
	if state.confirm {
		subtitle = "Scope: task and all descendants"
	}
	rows := make([]string, 0, m.height)
	rows = append(rows, withBase(m.theme.Title, m.theme.Base).Render(fit(title, m.width)))
	rows = append(rows, withBase(m.theme.Muted, m.theme.Base).Render(fit(subtitle, m.width)))
	for i := range body {
		line, style := "", m.theme.Base
		if position := state.scroll + i; position < len(lines) {
			line, style = lines[position].text, lines[position].style
		}
		rows = append(rows, withBase(style, m.theme.Base).Render(fit(line, m.width)))
	}
	for _, line := range foot {
		rows = append(rows, withBase(m.theme.Help, m.theme.Base).Render(fit(line, m.width)))
	}
	return strings.Join(rows[:min(len(rows), m.height)], "\n")
}
