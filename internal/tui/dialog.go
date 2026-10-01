package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"lisa/internal/model"
	lisaui "lisa/internal/ui"
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
}

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
		m.entries = append(m.entries, entry{role: "Lisa", content: m.conn.Mcp.Status()})
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
		m.previewDialogTheme(m.dialogMatches())
		return m, nil
	case tea.KeyBackspace:
		if runes := []rune(m.dialog.query); len(runes) > 0 {
			m.dialog.query = string(runes[:len(runes)-1])
			m.dialog.cursor = 0
		}
		m.previewDialogTheme(m.dialogMatches())
		return m, nil
	}
	matches := m.dialogMatches()
	switch msg.String() {
	case "up":
		if m.dialog.cursor > 0 {
			m.dialog.cursor--
		}
		m.previewDialogTheme(matches)
	case "down":
		if m.dialog.cursor < len(matches)-1 {
			m.dialog.cursor++
		}
		m.previewDialogTheme(matches)
	case "pgup":
		m.dialog.cursor = max(0, m.dialog.cursor-m.dialogWindow())
		m.previewDialogTheme(matches)
	case "pgdown":
		m.dialog.cursor = min(len(matches)-1, m.dialog.cursor+m.dialogWindow())
		m.previewDialogTheme(matches)
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
			if m.dialog.kind == dialogThemes {
				m.previewTheme(m.themeName)
			}
			m.layoutWidth = 0
			return m, nil
		}
		// Discard: nothing applied; the item list is dropped with the dialog
		// so a later open starts clean and view math never indexes stale rows.
		wasThemes := m.dialog.kind == dialogThemes
		m.dialog = dialogState{}
		m.dialogItems = nil
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
		providerName = m.conn.Provider
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
