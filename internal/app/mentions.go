package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"lisa/internal/agent"
)

// mentionState is the @ completion popup: fileIndex is the cached walk,
// matches the visible rows for the current query, cursor the selection.
type mentionState struct {
	open    bool
	index   []string
	matches []string
	cursor  int
	err     string
}

// fileIndexMsg delivers the built file index to the UI.
type fileIndexMsg struct {
	files []string
	err   error
}

// startMention opens the popup and builds the index in the background; an
// already-built index is reused immediately.
func (m *ui) startMention() tea.Cmd {
	if m.mention.index != nil {
		m.mention.open = true
		m.syncMention()
		return nil
	}
	m.mention.open = true
	repo := m.repo
	return func() tea.Msg {
		return fileIndexMsg{files: agent.BuildFileIndex(repo)}
	}
}

// syncMention re-filters matches from the current draft. The cursor resets to
// the first visible row on any query change.
func (m *ui) syncMention() {
	query, _ := agent.MentionQuery(string(m.input))
	m.mention.matches = agent.MentionMatches(m.mention.index, query)
	if m.mention.cursor >= len(m.mention.matches) {
		m.mention.cursor = 0
	}
}

// mentionActive reports whether the popup is open with rows to show.
func (m *ui) mentionActive() bool {
	return m.mention.open && len(m.mention.matches) > 0
}

// updateMention handles keys while the @ popup is open. It reports whether
// the key was consumed; callers fall through to normal input handling when
// the popup does not claim the key.
func (m *ui) updateMention(v tea.KeyMsg) (bool, tea.Cmd) {
	switch v.String() {
	case "up":
		if m.mentionActive() {
			m.mention.cursor = max(0, m.mention.cursor-1)
			return true, nil
		}
	case "down":
		if m.mentionActive() {
			m.mention.cursor = min(len(m.mention.matches)-1, m.mention.cursor+1)
			return true, nil
		}
	case "tab":
		if m.mentionActive() {
			m.input = []rune(agent.CompleteMention(string(m.input), m.mention.matches[m.mention.cursor]))
			m.edit.endCaret(m.input)
			m.mention.open = false
			m.layoutWidth = 0
			return true, nil
		}
	case "esc":
		m.mention.open = false
		m.mention.err = ""
		m.layoutWidth = 0
		return true, nil
	case "enter":
		if m.mentionActive() {
			m.input = []rune(agent.CompleteMention(string(m.input), m.mention.matches[m.mention.cursor]))
			m.edit.endCaret(m.input)
			m.mention.open = false
			m.layoutWidth = 0
			return true, nil
		}
	}
	return false, nil
}

// mentionLines renders as many matches as fit without hiding the transcript,
// editable draft, or fixed status rows.
func (m *ui) mentionLines() []string {
	if !m.mention.open {
		return nil
	}
	style := m.composerStyle
	if m.width <= minWidth && (style == "bordered" || style == "chatter") {
		style = "minimal"
	}
	fixed := 1 // one blank line above every composer
	if style == "minimal" {
		fixed = 2
	} else if style == "bordered" {
		fixed = 3
	}
	available := max(0, m.height-len(m.header())-m.statusLineHeight()-fixed-1-1)
	if available == 0 {
		return nil
	}
	if m.mention.err != "" {
		return []string{m.theme.Warning.Render(fit("@ "+m.mention.err, m.width))}
	}
	if len(m.mention.matches) == 0 {
		return []string{m.theme.Help.Render(fit("@ no matching files", m.width))}
	}
	visible := min(5, len(m.mention.matches), available)
	start := max(0, min(m.mention.cursor-visible+1, len(m.mention.matches)-visible))
	rows := make([]string, 0, visible+1)
	for i := start; i < start+visible; i++ {
		match := m.mention.matches[i]
		if i == m.mention.cursor {
			rows = append(rows, m.theme.Selected.Render(fit("> "+match, m.width)))
		} else {
			rows = append(rows, m.theme.Help.Render(fit("  "+match, m.width)))
		}
	}
	// Shared popup hint, only while the popup budget has a spare row: the
	// same keys drive the / command popup.
	if len(rows) < available {
		rows = append(rows, m.theme.Help.Render(fit("  ↑/↓ select  Tab complete  Esc dismiss", m.width)))
	}
	return rows
}
