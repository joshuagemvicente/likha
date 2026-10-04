package tui

import tea "github.com/charmbracelet/bubbletea"

// handleComposerSelectionKey runs after modal and popup navigation. Shifted
// vertical movement never recalls history: its only job is extending a range.
func (m *ui) handleComposerSelectionKey(msg tea.KeyMsg) bool {
	if !m.selectionSurfaceVisible() || !m.editable() {
		return false
	}
	key := msg.String()
	caret := min(len(m.input), max(0, m.edit.caret))
	start, end, selected := m.edit.selectionRange(m.input)
	next := caret
	extend := false
	switch key {
	case "shift+left":
		next, extend = max(0, caret-1), true
	case "shift+right":
		next, extend = min(len(m.input), caret+1), true
	case "shift+up", "shift+down":
		direction := 1
		if key == "shift+up" {
			direction = -1
		}
		var moved bool
		next, moved = m.composerSelectionCaretVertical(caret, direction)
		if !moved {
			next = len(m.input)
			if direction < 0 {
				next = 0
			}
		}
		extend = true
	case "left", "right":
		if key == "left" {
			next = max(0, caret-1)
			if selected {
				next = start
			}
		} else {
			next = min(len(m.input), caret+1)
			if selected {
				next = end
			}
		}
	case "up", "down":
		if !selected {
			return false
		}
		next = start
		if key == "down" {
			next = end
		}
	default:
		return false
	}
	m.clearToolEntryFocus()
	m.clearTranscriptSelection()
	if extend {
		m.edit.extendSelection(m.input, next)
		if _, _, ok := m.edit.selectionRange(m.input); !ok {
			m.edit.clearSelection()
		}
	} else {
		m.edit.clearSelection()
		m.edit.caret = next
	}
	if key != "shift+up" && key != "shift+down" {
		m.composerVertical.reset()
	}
	m.edit.lastWasYank = false
	m.caretNote()
	return true
}
