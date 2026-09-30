package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// commandItem is one reserved slash command offered by the completion popup.
type commandItem struct {
	Name, Description string
}

// reservedCommands are the commands handleCommand recognizes. The popup
// mirrors them so entering "@/…" style tokens is not needed: "/" itself is
// enough. Descriptions restate commandHelp in one line each.
var commands = []commandItem{
	{"compact", "summarize the conversation so far into a compact brief, /compact [focus] steers it"},
	{"help", "list the reserved commands"},
	{"mcp", "show the connected MCP servers or how to configure them"},
	{"models", "switch the model for this session"},
	{"providers", "select the provider for this session (a prompt asks for the API key when none is stored)"},
	{"quit", "exit"},
	{"sessions", "list or resume a saved session"},
	{"themes", "list or apply a color theme"},
}

// commandQuery reports whether the draft is inside a slash-command token:
// it starts with "/" and carries no space yet, so the popup closes the
// moment arguments begin. The empty query right after "/" matches
// everything.
func commandQuery(input string) (string, bool) {
	if input == "" || input[0] != '/' {
		return "", false
	}
	if strings.ContainsAny(input, " \t\n") {
		return "", false
	}
	return input[1:], true
}

// commandMatches returns the reserved commands visible for the query:
// case-insensitive substring match on the name, capped small.
func commandMatches(query string) []commandItem {
	q := strings.ToLower(query)
	var out []commandItem
	for _, c := range commands {
		if strings.Contains(strings.ToLower(c.Name), q) {
			out = append(out, c)
			if len(out) >= 8 {
				break
			}
		}
	}
	return out
}

// completeCommand rewites the draft to the chosen command plus a trailing
// space, ready for arguments or send.
func completeCommand(choice string) string {
	return "/" + choice + " "
}

// commandState is the / completion popup: matches the visible rows for the
// current draft, cursor the selection.
type commandState struct {
	open    bool
	matches []commandItem
	cursor  int
}

// syncCommand re-filters matches from the current draft. The cursor resets
// to the first visible row on any query change.
func (m *ui) syncCommand() {
	query, _ := commandQuery(string(m.input))
	m.commandPopup.matches = commandMatches(query)
	if m.commandPopup.cursor >= len(m.commandPopup.matches) {
		m.commandPopup.cursor = 0
	}
}

// commandActive reports whether the popup is open with rows to show.
func (m *ui) commandActive() bool {
	return m.commandPopup.open && len(m.commandPopup.matches) > 0
}

// updateCommand handles keys while the / popup is open. It reports whether
// the key was consumed; callers fall through to normal input handling when
// the popup does not claim the key. Enter completes only when the query is
// not already an exact command name: the fully typed "/compact" plus Enter
// must send, while "/comp" plus Enter completes.
func (m *ui) updateCommand(v tea.KeyMsg) (bool, tea.Cmd) {
	switch v.String() {
	case "up":
		if m.commandActive() {
			m.commandPopup.cursor = max(0, m.commandPopup.cursor-1)
			m.layoutWidth = 0
			return true, nil
		}
	case "down":
		if m.commandActive() {
			m.commandPopup.cursor = min(len(m.commandPopup.matches)-1, m.commandPopup.cursor+1)
			m.layoutWidth = 0
			return true, nil
		}
	case "tab":
		if m.commandActive() {
			m.input = []rune(completeCommand(m.commandPopup.matches[m.commandPopup.cursor].Name))
			m.edit.endCaret(m.input)
			m.commandPopup.open = false
			m.layoutWidth = 0
			return true, nil
		}
	case "esc":
		m.commandPopup.open = false
		m.layoutWidth = 0
		return true, nil
	case "enter":
		if m.commandActive() {
			query, _ := commandQuery(string(m.input))
			for _, c := range m.commandPopup.matches {
				if strings.EqualFold(c.Name, query) {
					// Exact name: the draft is sent as-is; close the popup
					// so it cannot claim later keys with a stale query.
					m.commandPopup.open = false
					m.layoutWidth = 0
					return false, nil
				}
			}
			m.input = []rune(completeCommand(m.commandPopup.matches[m.commandPopup.cursor].Name))
			m.edit.endCaret(m.input)
			m.commandPopup.open = false
			m.layoutWidth = 0
			return true, nil
		}
	}
	return false, nil
}

// commandLines renders as many matches as fit without hiding the
// transcript, editable draft, or fixed status rows.
func (m *ui) commandLines() []string {
	if !m.commandPopup.open {
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
	if len(m.commandPopup.matches) == 0 {
		query, _ := commandQuery(string(m.input))
		if query != "" {
			return []string{m.theme.Help.Render(fit("/ no matching commands", m.width))}
		}
		return nil
	}
	visible := min(8, len(m.commandPopup.matches), available)
	rows := make([]string, 0, visible+1)
	for i, c := range m.commandPopup.matches[:visible] {
		row := "/" + c.Name + " — " + c.Description
		if i == m.commandPopup.cursor {
			rows = append(rows, m.theme.Selected.Render(fit("> "+row, m.width)))
		} else {
			rows = append(rows, m.theme.Help.Render(fit("  "+row, m.width)))
		}
	}
	// Shared popup hint, only while the popup budget has a spare row.
	if len(rows) < available {
		rows = append(rows, m.theme.Help.Render(fit("  ↑/↓ select  Tab complete  Esc dismiss", m.width)))
	}
	return rows
}
