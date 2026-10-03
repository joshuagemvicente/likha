package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"likha/internal/session"
	"likha/internal/tooloutput"
	"likha/internal/tools"
)

type toolInspectionKind uint8

const (
	toolInspectionCatalog toolInspectionKind = iota
	toolInspectionResult
)

// toolOutputInspectionPage is a local, session-owned page, not a model message.
// A coordinator can adapt its output store with setToolOutputReader. The reader
// must enforce the supplied session identity and must never regenerate output.
type toolOutputInspectionPage = tooloutput.Page

type toolOutputInspectionReader func(sessionID, artifactID string, offset, limit int) (toolOutputInspectionPage, error)

type toolInspectionState struct {
	open      bool
	kind      toolInspectionKind
	cursor    int
	query     string
	details   bool
	scroll    int
	sessionID string

	// Focus is separate from the composer and survives closing an inspector.
	// Call identity follows metadata when ephemeral transcript rows disappear.
	focused      bool
	focusedEntry int
	focusedCall  string

	reader        toolOutputInspectionReader
	generation    uint64
	page          toolOutputInspectionPage
	pageSet       bool
	pageOffset    int
	requestOffset int
	pageSteps     []int
	pageIndex     int
	pageErr       string
	loading       bool
}

// toolInspectionOutputMsg must be routed from ui.Update to
// handleToolInspectionOutput. Late pages cannot replace another tool/session.
type toolInspectionOutputMsg struct {
	generation uint64
	sessionID  string
	artifactID string
	callID     string
	entryIndex int
	offset     int
	page       toolOutputInspectionPage
	err        error
}

func (m *ui) setToolOutputReader(reader toolOutputInspectionReader) {
	m.toolInspector.reader = reader
	m.resetToolOutputPage()
}

// setToolOutputStore binds the current session's store without requiring another
// ui field. Call again after a session switch, or with nil if storage is absent.
func (m *ui) setToolOutputStore(outputs *tooloutput.Store) {
	if outputs == nil {
		m.setToolOutputReader(nil)
		return
	}
	owner := m.snapshot.ID
	m.setToolOutputReader(func(sessionID, artifactID string, offset, limit int) (toolOutputInspectionPage, error) {
		if sessionID != owner {
			return toolOutputInspectionPage{}, fmt.Errorf("retained output store is not attached to the current session")
		}
		return outputs.Read(artifactID, offset, limit)
	})
}

// resetToolInspection is the session-switch/compaction hook. Readers remain
// attached, but an old focused row or page must not follow a new transcript.
func (m *ui) resetToolInspection() {
	reader, generation := m.toolInspector.reader, m.toolInspector.generation+1
	m.toolInspector = toolInspectionState{reader: reader, generation: generation}
	m.layoutWidth = 0
}

func (m *ui) openToolCatalog() tea.Cmd {
	if m.working || m.pending != nil || m.mode == modeSetup || m.keyModal.open || m.dialog.open {
		m.status = "Tool catalog is available when no run or review is active"
		return nil
	}
	reader, generation := m.toolInspector.reader, m.toolInspector.generation+1
	m.toolInspector = toolInspectionState{open: true, kind: toolInspectionCatalog, sessionID: m.snapshot.ID, reader: reader, generation: generation}
	return nil
}

func (m *ui) openToolResultInspection() tea.Cmd {
	if m.pending != nil || m.mode == modeSetup || m.keyModal.open || m.dialog.open {
		return nil
	}
	indices := m.inspectableToolEntries()
	if len(indices) == 0 {
		m.status = "No tool results to inspect"
		return nil
	}
	index, ok := m.focusedToolEntry()
	if !ok {
		index = m.firstVisibleToolEntry(indices)
	}
	m.setToolEntryFocus(index)
	m.toolInspector.open = true
	m.toolInspector.kind = toolInspectionResult
	m.toolInspector.details = true
	m.toolInspector.scroll = 0
	m.resetToolOutputPage()
	return m.loadToolOutputPage(1)
}

// toolInspectionVisible also keeps a newly arrived approval above historical
// output. Inspection never marks reviewSeen or replies to an approval request.
func (m *ui) toolInspectionVisible() bool {
	return m.toolInspector.open && m.toolInspector.sessionID == m.snapshot.ID && m.pending == nil && m.mode != modeSetup && !m.keyModal.open && !m.dialog.open
}

// handleToolInspection belongs after popup/modal/review key handling and before
// composer submission. In particular, false lets the existing run-cancellation
// path own Esc/Ctrl+C; callers must not unconditionally call updateToolInspection.
func (m *ui) handleToolInspection(msg tea.KeyMsg) (handled bool, cmd tea.Cmd) {
	key := msg.String()
	if m.toolInspector.sessionID != m.snapshot.ID {
		m.resetToolInspection()
	}
	if m.working && (key == "esc" || key == "ctrl+c") {
		return false, nil
	}
	if m.pending != nil || m.mode == modeSetup || m.keyModal.open || m.dialog.open || m.mention.open || m.commandPopup.open {
		return false, nil
	}
	if key == "ctrl+c" || key == "ctrl+d" {
		return false, nil // retain existing idle exit and active abandon behavior
	}
	if m.toolInspector.open {
		_, cmd = m.updateToolInspection(msg)
		return true, cmd
	}
	switch key {
	case "tab", "shift+tab":
		indices := m.inspectableToolEntries()
		if len(indices) == 0 {
			return false, nil
		}
		direction := 1
		if key == "shift+tab" {
			direction = -1
		}
		m.cycleToolEntryFocus(indices, direction, true)
		return true, nil
	case "enter", "ctrl+o":
		if _, ok := m.focusedToolEntry(); ok {
			return true, m.openToolResultInspection()
		}
		if key == "ctrl+o" && len(m.inspectableToolEntries()) > 0 {
			return true, m.openToolResultInspection()
		}
	case "backspace", "ctrl+h", "alt+left", "esc":
		if m.toolInspector.focused {
			m.clearToolEntryFocus()
			return true, nil
		}
	default:
		// Returning to editing releases row focus; the original key still
		// reaches the composer. No draft, caret, queue, or history is changed.
		if m.toolInspector.focused {
			m.clearToolEntryFocus()
		}
	}
	return false, nil
}

func (m *ui) updateToolInspection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if !m.toolInspectionVisible() || (m.working && (key == "esc" || key == "ctrl+c")) {
		return m, nil
	}
	if key == "esc" {
		m.closeToolInspection()
		return m, nil
	}
	state := &m.toolInspector
	if state.kind == toolInspectionResult {
		switch key {
		case "enter", "ctrl+o", "backspace", "ctrl+h", "alt+left":
			m.closeToolInspection()
		case "tab", "shift+tab":
			direction := 1
			if key == "shift+tab" {
				direction = -1
			}
			m.cycleToolEntryFocus(m.inspectableToolEntries(), direction, false)
			state.scroll = 0
			m.resetToolOutputPage()
			return m, m.loadToolOutputPage(1)
		case "left":
			if state.pageSet && state.pageIndex > 0 && !state.loading {
				return m, m.loadToolOutputPage(state.pageSteps[state.pageIndex-1])
			}
		case "right":
			if !state.loading {
				if state.pageErr != "" && state.requestOffset > 0 {
					return m, m.loadToolOutputPage(state.requestOffset)
				}
				if !state.pageSet {
					return m, m.loadToolOutputPage(1)
				}
				if state.page.NextOffset > state.pageOffset {
					return m, m.loadToolOutputPage(state.page.NextOffset)
				}
			}
		default:
			m.scrollToolInspection(key)
		}
		return m, nil
	}
	if state.details {
		switch key {
		case "enter", "ctrl+o", "backspace", "ctrl+h", "alt+left", "left":
			state.details = false
			state.scroll = 0
		case "tab", "shift+tab":
			direction := 1
			if key == "shift+tab" {
				direction = -1
			}
			state.cursor = toolWrappedIndex(state.cursor+direction, len(m.toolCatalogMatches()))
			state.scroll = 0
		default:
			m.scrollToolInspection(key)
		}
		return m, nil
	}
	matches := m.toolCatalogMatches()
	switch key {
	case "up", "shift+tab":
		state.cursor = toolWrappedIndex(state.cursor-1, len(matches))
	case "down", "tab":
		state.cursor = toolWrappedIndex(state.cursor+1, len(matches))
	case "pgup", "ctrl+p":
		step := min(max(1, m.toolInspectionBodyHeight()/3), max(1, len(matches)-1))
		state.cursor = toolWrappedIndex(state.cursor-step, len(matches))
	case "pgdown", "ctrl+n":
		step := min(max(1, m.toolInspectionBodyHeight()/3), max(1, len(matches)-1))
		state.cursor = toolWrappedIndex(state.cursor+step, len(matches))
	case "home":
		state.cursor = 0
	case "end":
		state.cursor = max(0, len(matches)-1)
	case "enter", "ctrl+o":
		if len(matches) > 0 {
			state.details = true
			state.scroll = 0
		}
	case "backspace", "ctrl+h":
		if query := []rune(state.query); len(query) > 0 {
			state.query = string(query[:len(query)-1])
			state.cursor, state.scroll = 0, 0
		} else {
			m.closeToolInspection()
		}
	case "alt+left":
		m.closeToolInspection()
	case "ctrl+u":
		state.query = ""
		state.cursor, state.scroll = 0, 0
	default:
		if msg.Type == tea.KeyRunes && !msg.Alt {
			state.query += strings.ReplaceAll(string(msg.Runes), "\n", " ")
			state.cursor, state.scroll = 0, 0
		} else if msg.Type == tea.KeySpace {
			state.query += " "
			state.cursor, state.scroll = 0, 0
		}
	}
	return m, nil
}

func (m *ui) closeToolInspection() {
	m.toolInspector.open = false
	m.toolInspector.generation++
	m.toolInspector.loading = false
	// Transcript scrolling/following were never changed by the inspector.
	// Draft and held steering prompts therefore remain exactly where they were.
}

// handleToolInspectionMouse belongs before main transcript mouse handling.
// Ignoring non-wheel events leaves native terminal text selection unchanged;
// historical inspection never drags the transcript or approval scrollbar.
func (m *ui) handleToolInspectionMouse(msg tea.MouseMsg) bool {
	if !m.toolInspectionVisible() {
		return false
	}
	direction := 0
	switch msg.Type {
	case tea.MouseWheelUp:
		direction = -1
	case tea.MouseWheelDown:
		direction = 1
	}
	if direction != 0 {
		state := &m.toolInspector
		if state.kind == toolInspectionCatalog && !state.details {
			state.cursor = toolWrappedIndex(state.cursor+direction, len(m.toolCatalogMatches()))
		} else {
			state.scroll += direction * scrollWheelLines
			state.scroll = max(0, min(state.scroll, max(0, len(m.toolInspectionLines())-m.toolInspectionBodyHeight())))
		}
	}
	return true
}

func (m *ui) scrollToolInspection(key string) {
	state := &m.toolInspector
	switch key {
	case "up":
		state.scroll--
	case "down":
		state.scroll++
	case "pgup", "ctrl+p":
		state.scroll -= m.toolInspectionBodyHeight()
	case "pgdown", "ctrl+n":
		state.scroll += m.toolInspectionBodyHeight()
	case "home":
		state.scroll = 0
	case "end":
		state.scroll = len(m.toolInspectionLines())
	}
	state.scroll = max(0, min(state.scroll, max(0, len(m.toolInspectionLines())-m.toolInspectionBodyHeight())))
}

func toolWrappedIndex(index, count int) int {
	if count <= 0 {
		return 0
	}
	return ((index % count) + count) % count
}

func (m *ui) toolCatalogMatches() []int {
	var indices []int
	query := strings.ToLower(m.toolInspector.query)
	for i, tool := range m.toolCatalog {
		text := strings.Join([]string{tool.Name, toolHumanTitle(tool.Name, tool.Source), tool.Description,
			toolSourceLabel(tool.Source), tool.Target, tool.Reason, toolEffectsLabel(tool.Effects), toolPermissionLabel(tool)}, " ")
		if query == "" || strings.Contains(strings.ToLower(text), query) {
			indices = append(indices, i)
		}
	}
	return indices
}

type toolInspectionLine struct {
	text  string
	style lipgloss.Style
}

func (m *ui) appendToolInspectionLines(lines []toolInspectionLine, text string, style lipgloss.Style) []toolInspectionLine {
	for _, line := range wrap(text, contentWidth(m.width)) {
		lines = append(lines, toolInspectionLine{text: line, style: style})
	}
	return lines
}

func (m *ui) toolInspectionLines() []toolInspectionLine {
	if m.toolInspector.kind == toolInspectionResult {
		return m.toolResultInspectionLines()
	}
	matches := m.toolCatalogMatches()
	if len(matches) == 0 {
		text := "No tools configured. Only installed tools and known unavailable backends appear here."
		if m.toolInspector.query != "" {
			text = "No tools match " + fmt.Sprintf("%q", m.toolInspector.query) + ". Backspace edits the filter; Ctrl+U clears it."
		}
		return m.appendToolInspectionLines(nil, text, m.theme.Muted)
	}
	cursor := max(0, min(m.toolInspector.cursor, len(matches)-1))
	if m.toolInspector.details {
		return m.toolCatalogDetailLines(m.toolCatalog[matches[cursor]])
	}
	var lines []toolInspectionLine
	selectedStart, selectedEnd := 0, 0
	for position, index := range matches {
		tool := m.toolCatalog[index]
		prefix, style := "  ", m.theme.Muted
		if position == cursor {
			prefix, style = "> ", m.theme.Selected
			selectedStart = len(lines)
		}
		label := tool.Name
		if title := toolHumanTitle(tool.Name, tool.Source); title != tool.Name {
			label += " — " + title
		}
		lines = m.appendToolInspectionLines(lines, prefix+label, style)
		availability := "available"
		if !tool.Available {
			availability = "unavailable"
		}
		lines = m.appendToolInspectionLines(lines, "  "+toolSourceLabel(tool.Source)+" · "+availability, m.theme.Muted)
		if !tool.Available {
			reason := tool.Reason
			if reason == "" {
				reason = "No availability reason was supplied."
			}
			lines = m.appendToolInspectionLines(lines, "  "+reason, m.theme.Muted)
		}
		if position == cursor {
			selectedEnd = len(lines)
		}
		lines = m.appendToolInspectionLines(lines, "", m.theme.Base)
	}
	body := m.toolInspectionBodyHeight()
	if selectedStart < m.toolInspector.scroll || selectedEnd-selectedStart > body {
		m.toolInspector.scroll = selectedStart
	} else if selectedEnd > m.toolInspector.scroll+body {
		m.toolInspector.scroll = min(selectedStart, selectedEnd-body)
	}
	return lines
}

func (m *ui) toolCatalogDetailLines(tool tools.CatalogEntry) []toolInspectionLine {
	var lines []toolInspectionLine
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	add(toolHumanTitle(tool.Name, tool.Source), m.theme.Title)
	add("Model name: "+tool.Name, m.theme.Normal)
	add("Source: "+toolSourceLabel(tool.Source), m.theme.Muted)
	if tool.Available {
		add("Availability: available in this catalog", m.theme.Muted)
	} else {
		add("Availability: unavailable", m.theme.Muted)
	}
	if tool.Reason != "" {
		add("Eligibility / reason: "+tool.Reason, m.theme.Muted)
	} else if tool.Available {
		add("Eligibility: current main-agent catalog; mode and agent limits are enforced again at dispatch.", m.theme.Muted)
	} else {
		add("Eligibility / reason: not supplied", m.theme.Muted)
	}
	add("", m.theme.Base)
	description := tool.Description
	if description == "" {
		description = "No description supplied."
	}
	add(description, m.theme.Normal)
	add("", m.theme.Base)
	add("Effects: "+toolEffectsLabel(tool.Effects), m.theme.Muted)
	target := tool.Target
	if target == "" {
		target = "Not declared; actual targets are checked at dispatch."
	}
	add("Target scope: "+target, m.theme.Muted)
	add("Permissions: "+toolPermissionLabel(tool), m.theme.Normal)
	if tool.ParallelSafe {
		add("Concurrency: declared parallel-safe; approval and mutation barriers still apply.", m.theme.Muted)
	} else {
		add("Concurrency: not declared parallel-safe", m.theme.Muted)
	}
	if tool.Interactive {
		add("Interaction: may request user input or approval", m.theme.Muted)
	} else {
		add("Interaction: no interactive handler declared", m.theme.Muted)
	}
	add("", m.theme.Base)
	add("Inspecting this catalog grants no permission and does not run a tool.", m.theme.Muted)
	return lines
}

func toolHumanTitle(name string, source tools.Source) string {
	if source.Kind == "mcp" {
		if source.Tool != "" {
			return source.Tool
		}
		return name
	}
	switch name {
	case "glob":
		return "Find repository paths"
	case "read":
		return "Read repository files"
	case "grep":
		return "Search repository content"
	case "edit_file", "edit":
		return "Propose repository edits"
	case "run_command":
		return "Run an approved command"
	case "read_output":
		return "Read retained session output"
	case "task":
		return "Explore a repository task"
	case "ask_user":
		return "Ask a structured question"
	case "plan_update":
		return "Update the session checklist"
	case "skill":
		return "Load skill instructions"
	case "web_search":
		return "Search the web"
	case "web_fetch":
		return "Fetch a public HTTPS page"
	default:
		return name
	}
}

func toolSourceLabel(source tools.Source) string {
	switch source.Kind {
	case "builtin", "built-in", "likha":
		return "Likha built-in"
	case "mcp":
		server := source.Server
		if server == "" {
			server = "unknown server"
		}
		label := "MCP · " + server
		if source.Tool != "" {
			label += " / " + source.Tool
		}
		return label
	case "":
		return "Unknown source (legacy metadata)"
	default:
		return source.Kind + " · " + source.Server + " / " + source.Tool
	}
}

func toolEffectsLabel(effects []tools.Effect) string {
	if len(effects) == 0 {
		return "unknown / undeclared; not an inferred read permission"
	}
	labels := make([]string, 0, len(effects))
	for _, effect := range effects {
		switch effect {
		case tools.Read:
			labels = append(labels, "read")
		case tools.Write:
			labels = append(labels, "write")
		case tools.Exec:
			labels = append(labels, "execute")
		case tools.Network:
			labels = append(labels, "network")
		default:
			labels = append(labels, string(effect)+" (unknown)")
		}
	}
	return strings.Join(labels, ", ")
}

func toolPermissionLabel(tool tools.CatalogEntry) string {
	if tool.Source.Kind == "mcp" {
		return "Trust approval for the displayed MCP server, remembered until app relaunch. MCP effects are not inferred as repository-read permission."
	}
	if tool.Source.Kind != "builtin" && tool.Source.Kind != "built-in" && tool.Source.Kind != "likha" {
		return "Not declared for this source; inspection grants no permission."
	}
	if tool.Name == "read_output" {
		return "Current session's artifact IDs only, not host paths or URLs. Reading a page does not approve or repeat its generating action."
	}
	var permissions []string
	for _, effect := range tool.Effects {
		switch effect {
		case tools.Read:
			permissions = append(permissions, "reads within the declared scope; existing path restrictions apply")
		case tools.Write:
			permissions = append(permissions, "repository changes require complete diff review and approval")
		case tools.Exec:
			permissions = append(permissions, "approval for the exact command and working directory; no filesystem/network sandbox, and detached jobs may survive cancellation")
		case tools.Network:
			permissions = append(permissions, "separate consent for the backend/destination; conversation-scoped grants")
		default:
			permissions = append(permissions, "unknown effect; no read permission inferred")
		}
	}
	if len(permissions) == 0 {
		return "No effect-based permission declared; inspection grants no permission."
	}
	return strings.Join(permissions, "; ") + "."
}

// RenderToolSummary is content only: rebuild retains the existing muted Tool
// band and prefix. Execution status is deliberately separate from clipping.
func RenderToolSummary(record session.ToolRecord) string {
	name := toolShortText(record.Name, 48)
	if name == "" {
		name = "tool"
	}
	source := toolShortText(toolSourceLabel(tools.Source{Kind: record.SourceKind, Server: record.Server, Tool: record.SourceTool}), 64)
	status := toolShortText(record.Status, 32)
	if status == "" {
		status = "unknown status"
	}
	parts := []string{name}
	if record.Status == string(tools.Refused) && strings.Contains(record.Content, "rejected by user") {
		// Keep the user's decision early and intact instead of burying it
		// after a long target/source where wrapping can obscure the outcome.
		parts = append(parts, "rejected by user")
	}
	parts = append(parts, status, "["+source+"]")
	if target := toolArgumentSummary(record.Arguments); target != "" {
		parts = append(parts, target)
	}
	if preview := toolShortText(record.Content, 96); preview != "" {
		parts = append(parts, preview)
	} else {
		parts = append(parts, "no textual output")
	}
	if record.Truncated {
		parts = append(parts, "preview clipped")
	}
	if record.ArtifactID != "" {
		parts = append(parts, "retained output")
	}
	if len(record.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", len(record.Warnings)))
	}
	return strings.Join(parts, " · ")
}

func toolArgumentSummary(arguments string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		return toolShortText(arguments, 72)
	}
	for _, key := range []string{"command", "path", "file_path", "paths", "pattern", "artifact_id", "query", "url"} {
		if value, ok := fields[key]; ok {
			var text string
			if json.Unmarshal(value, &text) != nil {
				text = string(value)
			}
			return key + ": " + toolShortText(text, 72)
		}
	}
	return toolShortText(arguments, 72)
}

// toolShortText makes control characters visible before bounding display cells.
// It never strips an escape prefix and accidentally executes its remaining tail.
func toolShortText(text string, width int) string {
	var plain strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			plain.WriteByte(' ')
		case hiddenReviewRune(r):
			fmt.Fprintf(&plain, "\\u%04X", r)
		default:
			plain.WriteRune(r)
		}
	}
	text = strings.Join(strings.Fields(plain.String()), " ")
	if runewidth.StringWidth(text) <= width {
		return text
	}
	left, _ := splitAtWidth(text, max(0, width-1))
	// splitAtWidth can include a wide rune crossing the boundary; fit cannot.
	return strings.TrimRight(fit(left, max(0, width-1)), " ") + "…"
}

func (m *ui) toolRecordAt(index int) (session.ToolRecord, bool) {
	if index < 0 || index >= len(m.entries) || m.entries[index].role != "Tool" {
		return session.ToolRecord{}, false
	}
	for i := len(m.toolRecords) - 1; i >= 0; i-- {
		if m.toolRecords[i].EntryIndex == index {
			return m.toolRecords[i], true
		}
	}
	return session.ToolRecord{}, false
}

// toolEntryContent leaves all legacy rows byte-for-byte intact. Only attributed
// results use compact summaries; the inspector still has their full Content.
func (m *ui) toolEntryContent(index int, fallback string) string {
	if record, ok := m.toolRecordAt(index); ok {
		return RenderToolSummary(record)
	}
	return fallback
}

func (m *ui) inspectableToolEntries() []int {
	var indices []int
	for i, entry := range m.entries {
		if entry.role == "Tool" {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *ui) focusedToolEntry() (int, bool) {
	state := &m.toolInspector
	if !state.focused || state.sessionID != m.snapshot.ID {
		return 0, false
	}
	if state.focusedCall != "" {
		for _, record := range m.toolRecords {
			if record.CallID == state.focusedCall && record.EntryIndex >= 0 && record.EntryIndex < len(m.entries) && m.entries[record.EntryIndex].role == "Tool" {
				return record.EntryIndex, true
			}
		}
		return 0, false
	}
	index := state.focusedEntry
	return index, index >= 0 && index < len(m.entries) && m.entries[index].role == "Tool"
}

// toolEntryFocused is a leaf hook for rebuild's selected-row treatment. Keeping
// it separate from toolEntryContent preserves legacy Tool content and styling.
func (m *ui) toolEntryFocused(index int) bool {
	focused, ok := m.focusedToolEntry()
	return ok && focused == index
}

func (m *ui) setToolEntryFocus(index int) {
	m.toolInspector.sessionID = m.snapshot.ID
	m.toolInspector.focused = true
	m.toolInspector.focusedEntry = index
	m.toolInspector.focusedCall = ""
	if record, ok := m.toolRecordAt(index); ok {
		m.toolInspector.focusedCall = record.CallID
	}
	m.layoutWidth = 0
}

func (m *ui) clearToolEntryFocus() {
	m.toolInspector.focused = false
	m.toolInspector.focusedCall = ""
	m.layoutWidth = 0
}

func (m *ui) cycleToolEntryFocus(indices []int, direction int, reveal bool) {
	if len(indices) == 0 {
		m.clearToolEntryFocus()
		return
	}
	position := -1
	if index, ok := m.focusedToolEntry(); ok {
		for i, candidate := range indices {
			if index == candidate {
				position = i
				break
			}
		}
	}
	if position < 0 {
		index := m.firstVisibleToolEntry(indices)
		for i, candidate := range indices {
			if candidate == index {
				position = i
				break
			}
		}
	} else {
		position = toolWrappedIndex(position+direction, len(indices))
	}
	index := indices[max(0, position)]
	m.setToolEntryFocus(index)
	if reveal {
		m.rebuild()
		start := m.toolEntryStartLine(index)
		if start < m.scroll || start >= m.scroll+m.bodyHeight() {
			m.scroll = max(0, min(start, m.scrollMax()))
			m.following = false
		}
	}
}

func (m *ui) firstVisibleToolEntry(indices []int) int {
	m.rebuild()
	m.clampScroll()
	for _, index := range indices {
		start := m.toolEntryStartLine(index)
		if start >= m.scroll && start < m.scroll+m.bodyHeight() {
			return index
		}
	}
	return indices[len(indices)-1]
}

// This mirrors rebuild's plain line accounting only; it never touches approval
// coverage. Coordinator additions to transcript layout should keep it in sync.
func (m *ui) toolEntryStartLine(index int) int {
	width, count := contentWidth(m.width), 0
	for _, header := range m.header() {
		if runewidth.StringWidth(header) > m.width {
			count += len(wrap(header, width))
		}
	}
	first := false
	for i, entry := range m.entries {
		if entry.role == "Logo" && m.width < 56 {
			continue
		}
		if first {
			count++
		}
		first = true
		if i == index {
			return count
		}
		switch entry.role {
		case "Logo":
			for _, line := range strings.Split(entry.content, "\n") {
				count += len(wrap(line, width))
			}
		case "Working":
			count += len(wrap(activitySpinner(m.activityFrame)+" "+workingLabel, width))
		case "Tool":
			count += len(wrap(entry.role+": "+m.toolEntryContent(i, entry.content), width))
		default:
			count += len(wrap(entry.role+": "+entry.content, width))
		}
	}
	return count
}

func (m *ui) toolResultInspectionLines() []toolInspectionLine {
	var lines []toolInspectionLine
	add := func(text string, style lipgloss.Style) { lines = m.appendToolInspectionLines(lines, text, style) }
	index, ok := m.focusedToolEntry()
	if !ok {
		add("This tool row is no longer in the current transcript. Back closes inspection; no action is replayed.", m.theme.Muted)
		return lines
	}
	record, attributed := m.toolRecordAt(index)
	if !attributed {
		add("Legacy tool output", m.theme.Title)
		add("Status and source were not recorded. This text is historical, not proof of a new execution or permission grant.", m.theme.Muted)
		add("", m.theme.Base)
		add(m.entries[index].content, m.theme.Muted)
		return lines
	}
	add(record.Name, m.theme.Title)
	add("Call: "+record.CallID, m.theme.Muted)
	add("Source: "+toolSourceLabel(tools.Source{Kind: record.SourceKind, Server: record.Server, Tool: record.SourceTool}), m.theme.Muted)
	status := record.Status
	if status == "" {
		status = "unknown (not recorded)"
	}
	add("Result status: "+status, m.theme.Muted)
	if record.Name == "run_command" {
		// The runner records this literal outcome before captured text. Keep
		// it reachable even when an artifact page replaces the inline preview.
		outcome, _, _ := strings.Cut(record.Content, "\n")
		if strings.HasPrefix(outcome, "Exit status: ") {
			add("Command outcome: "+outcome, m.theme.Muted)
		}
	}
	if record.Truncated {
		add("Preview completeness: clipped / incomplete; this does not change a recorded command exit status.", m.theme.Warning)
	} else {
		add("Preview completeness: not marked truncated", m.theme.Muted)
	}
	for _, warning := range record.Warnings {
		add("Warning: "+warning, m.theme.Warning)
	}
	if record.ArtifactID != "" {
		add("Output reference: "+record.ArtifactID+" (session-owned ID, not a file path)", m.theme.Muted)
		add("Retained output is private local session storage and may contain secrets. Inspection adds nothing to model context; requested tool previews/pages may reach the configured provider.", m.theme.Muted)
	}
	if record.NextOffset > 0 {
		add(fmt.Sprintf("Result continuation: offset %d", record.NextOffset), m.theme.Muted)
	}
	if record.Cursor != "" {
		add("Result cursor: "+record.Cursor, m.theme.Muted)
	}
	if record.Arguments != "" {
		add("", m.theme.Base)
		add("Arguments", m.theme.Title)
		var formatted bytes.Buffer
		if json.Indent(&formatted, []byte(record.Arguments), "", "  ") == nil {
			add(formatted.String(), m.theme.Muted)
		} else {
			add(record.Arguments, m.theme.Muted)
		}
	}
	add("", m.theme.Base)
	state := &m.toolInspector
	if state.loading {
		add("Reading retained output locally…", m.theme.Muted)
	}
	if state.pageErr != "" {
		add("Retained output unavailable: "+state.pageErr, m.theme.Error)
		add("The transcript preview remains usable. No tool is rerun. → retries the local read.", m.theme.Muted)
	}
	if state.pageSet {
		add(fmt.Sprintf("Retained output · page %d · offset %d", state.pageIndex+1, state.pageOffset), m.theme.Title)
		if state.page.Truncated {
			add("Retained page is marked truncated; check continuation and warnings for capture limits.", m.theme.Warning)
		}
		for _, warning := range state.page.Warnings {
			add("Warning: "+warning, m.theme.Warning)
		}
		if state.page.NextOffset > state.pageOffset {
			add(fmt.Sprintf("More retained output at offset %d; → reads the next local page.", state.page.NextOffset), m.theme.Muted)
		} else {
			add("End of retained output; this does not change the execution status or capture completeness.", m.theme.Muted)
		}
		add(state.page.Content, m.theme.Muted)
	} else {
		add("Result preview", m.theme.Title)
		if record.Content == "" {
			add("No textual output was recorded.", m.theme.Muted)
		} else {
			add(record.Content, m.theme.Muted)
		}
	}
	return lines
}

func (m *ui) resetToolOutputPage() {
	state := &m.toolInspector
	state.generation++
	state.page = toolOutputInspectionPage{}
	state.pageSet, state.loading = false, false
	state.pageOffset, state.pageIndex, state.requestOffset = 0, 0, 0
	state.pageSteps, state.pageErr = nil, ""
}

func (m *ui) loadToolOutputPage(offset int) tea.Cmd {
	state := &m.toolInspector
	index, ok := m.focusedToolEntry()
	if !ok || !state.open || state.kind != toolInspectionResult {
		return nil
	}
	record, ok := m.toolRecordAt(index)
	if !ok || record.ArtifactID == "" || state.loading {
		return nil
	}
	offset = max(1, offset)
	state.requestOffset = offset
	if state.reader == nil {
		state.pageErr = "session output storage is not attached; the saved preview is still available"
		return nil
	}
	state.generation++
	state.loading, state.pageErr = true, ""
	generation, reader, sessionID := state.generation, state.reader, m.snapshot.ID
	return func() tea.Msg {
		page, err := reader(sessionID, record.ArtifactID, offset, 128)
		return toolInspectionOutputMsg{generation: generation, sessionID: sessionID, artifactID: record.ArtifactID,
			callID: record.CallID, entryIndex: index, offset: offset, page: page, err: err}
	}
}

func (m *ui) handleToolInspectionOutput(msg toolInspectionOutputMsg) {
	state := &m.toolInspector
	if !state.open || state.kind != toolInspectionResult || msg.generation != state.generation || msg.sessionID != m.snapshot.ID {
		return
	}
	index, ok := m.focusedToolEntry()
	if !ok {
		return
	}
	record, ok := m.toolRecordAt(index)
	if !ok || record.ArtifactID != msg.artifactID || record.CallID != msg.callID || (record.CallID == "" && index != msg.entryIndex) {
		return
	}
	state.loading = false
	if msg.err != nil {
		state.pageErr = msg.err.Error()
		return // preserve either the inline preview or the last readable page
	}
	step := -1
	for i, offset := range state.pageSteps {
		if offset == msg.offset {
			step = i
			break
		}
	}
	if step < 0 {
		if state.pageSet {
			state.pageSteps = state.pageSteps[:state.pageIndex+1]
		}
		state.pageSteps = append(state.pageSteps, msg.offset)
		step = len(state.pageSteps) - 1
	}
	state.page, state.pageSet, state.pageErr = msg.page, true, ""
	state.pageOffset, state.pageIndex, state.scroll = msg.offset, step, 0
}

func (m *ui) toolInspectionFooter() []string {
	var text string
	state := &m.toolInspector
	if state.kind == toolInspectionCatalog {
		if state.details {
			text = "↑/↓ PgUp/PgDn scroll · Tab next tool\nEnter/Back list · Esc close"
		} else {
			text = "↑/↓ navigate · PgUp/PgDn page\nType to filter · Enter details\nBackspace edit filter · Esc close"
		}
	} else {
		text = "↑/↓ PgUp/PgDn scroll\nTab/Shift+Tab tool · Enter/Ctrl+O/Back close"
		if index, ok := m.focusedToolEntry(); ok {
			if record, ok := m.toolRecordAt(index); ok && record.ArtifactID != "" {
				text += "\n←/→ retained output page"
			}
		}
		if m.working {
			text += "\nEsc/Ctrl+C cancel active run"
		} else {
			text += "\nEsc close"
		}
	}
	return wrap(text, contentWidth(m.width))
}

func (m *ui) toolInspectionBodyHeight() int {
	return max(1, m.height-2-len(m.toolInspectionFooter()))
}

// toolInspectionView is full-screen plain, selectable terminal text. It fits
// before styling and routes every untrusted string through wrap/fit, including
// provenance, filters, arguments, warnings, errors, and artifact pages.
func (m *ui) toolInspectionView() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if !m.toolInspectionVisible() {
		return m.mainView()
	}
	lines := m.toolInspectionLines()
	foot := m.toolInspectionFooter()
	body := m.toolInspectionBodyHeight()
	state := &m.toolInspector
	state.scroll = max(0, min(state.scroll, max(0, len(lines)-body)))
	title := "Tool catalog"
	if state.kind == toolInspectionResult {
		title = "Tool output inspection"
		if m.width < 60 {
			title = "Tool output"
		}
	}
	title += fmt.Sprintf(" · lines %d–%d/%d", min(len(lines), state.scroll+1), min(len(lines), state.scroll+body), len(lines))
	subtitle := "Local inspection only · no action or permission grant"
	if state.kind == toolInspectionCatalog && !state.details {
		subtitle = "Filter: " + state.query
		if state.query == "" {
			subtitle += "(type to filter)"
		}
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

// toolInspectionHelp is the coordinator's main-view footer/help hook. Only a
// focused row claims Enter, so the normal composer and steering flow stay live.
func (m *ui) toolInspectionHelp() string {
	indices := m.inspectableToolEntries()
	if len(indices) == 0 || m.pending != nil {
		return ""
	}
	if index, ok := m.focusedToolEntry(); ok {
		position := 0
		for i, candidate := range indices {
			if candidate == index {
				position = i + 1
				break
			}
		}
		return fmt.Sprintf("Tool %d/%d focused · Tab/Shift+Tab tool · Enter/Ctrl+O inspect · Back unfocus", position, len(indices))
	}
	return "Tab/Shift+Tab focus tool · Ctrl+O inspect"
}
