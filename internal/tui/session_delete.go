package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// sessionDeleteRequestedMsg asks tui.go to delete one stored session. The
// /sessions dialog never deletes anything itself: it emits this after the
// second explicit confirmation and waits (deleteBusy) for finishSessionDelete.
// Current reports that the session is the one loaded in this UI.
type sessionDeleteRequestedMsg struct {
	ID      string
	Current bool
}

// sessionDeleteLoss names everything a delete removes, in the order the
// confirmation lists it.
const sessionDeleteLoss = "its conversation, retained tool-output artifacts, edit journals, task records, and the saved plan"

// sessionDeleteBlocked returns why a delete cannot run now, or "". The
// confirmation shows the reason live and tui.go rechecks it before calling
// the store, since a run may start between the request and its handling.
func (m *ui) sessionDeleteBlocked() string {
	if m.working {
		return "Cannot delete while a run is active."
	}
	if m.pending != nil {
		return "Cannot delete while a review is pending."
	}
	return ""
}

// openSessionDelete switches the sessions dialog to the confirmation for the
// highlighted row. Cancel starts focused, so a stray Enter never deletes.
func (m *ui) openSessionDelete(matches []int) {
	if m.dialog.cursor < 0 || m.dialog.cursor >= len(matches) {
		return
	}
	orig := matches[m.dialog.cursor]
	if orig < 0 || orig >= len(m.sessionIDs) || orig >= len(m.dialogItems) {
		return
	}
	m.dialog.deleteConfirm = true
	m.dialog.deleteFocus = false
	m.dialog.deleteBusy = false
	m.dialog.deleteID = m.sessionIDs[orig]
	m.dialog.deleteLabel = m.dialogItems[orig]
	m.dialog.deleteErr = ""
	m.layoutWidth = 0
}

// closeSessionDelete returns to the session list with nothing changed.
func (m *ui) closeSessionDelete() {
	m.dialog.deleteConfirm = false
	m.dialog.deleteFocus = false
	m.dialog.deleteBusy = false
	m.dialog.deleteID = ""
	m.dialog.deleteLabel = ""
	m.dialog.deleteErr = ""
	m.layoutWidth = 0
}

// updateSessionDelete handles keys in the confirmation: arrows and Tab move
// focus between Cancel and Delete, Enter activates the focused button, and
// Esc backs out to the list. Every key is inert while a request is in flight.
func (m *ui) updateSessionDelete(msg tea.KeyMsg) tea.Cmd {
	if m.dialog.deleteBusy {
		return nil
	}
	switch msg.String() {
	case "left", "shift+tab":
		m.dialog.deleteFocus = false
	case "right", "tab":
		m.dialog.deleteFocus = true
	case "esc":
		m.closeSessionDelete()
	case "enter":
		if !m.dialog.deleteFocus {
			m.closeSessionDelete()
			return nil
		}
		if m.sessionDeleteBlocked() != "" {
			// The reason is already on screen; Delete stays inert.
			return nil
		}
		m.dialog.deleteBusy = true
		m.dialog.deleteErr = ""
		request := sessionDeleteRequestedMsg{ID: m.dialog.deleteID, Current: m.dialog.deleteID == m.snapshot.ID}
		return func() tea.Msg { return request }
	}
	return nil
}

// finishSessionDelete applies the outcome tui.go reports for a request. A
// failure stays visible inside the confirmation and as an Error entry. A
// success replaces a deleted current session with a fresh one, then
// refreshes the list in place (the dialog closes if nothing is left).
func (m *ui) finishSessionDelete(msg sessionDeleteRequestedMsg, err error) tea.Cmd {
	matching := m.dialog.open && m.dialog.kind == dialogSessions && m.dialog.deleteConfirm && m.dialog.deleteID == msg.ID
	label := msg.ID
	if matching && m.dialog.deleteLabel != "" {
		label = m.dialog.deleteLabel
	}
	m.layoutWidth = 0
	if err != nil {
		if matching {
			m.dialog.deleteBusy = false
			m.dialog.deleteErr = err.Error()
		}
		m.entries = append(m.entries, entry{role: "Error", content: "Delete session: " + err.Error()})
		m.status = "Session delete failed"
		return nil
	}
	var cmd tea.Cmd
	if msg.Current {
		// The loaded session no longer exists; continue in a fresh one so
		// nothing later saves into the deleted id.
		fresh, createErr := m.store.Create()
		if createErr != nil {
			m.entries = append(m.entries, entry{role: "Error", content: "Start a fresh session: " + createErr.Error()})
		} else {
			cmd = m.resumeSession(fresh.ID)
			m.freshSession = true
			m.nameTried = false
			m.promptHistory = newPromptHistory(restoredPromptHistory(m.snapshot))
			m.composerVertical.reset()
		}
	}
	m.entries = append(m.entries, entry{role: "Likha", content: "Deleted session " + label + " with " + sessionDeleteLoss + "."})
	m.status = "Deleted session"
	if m.dialog.open && m.dialog.kind == dialogSessions {
		m.refreshSessionsDialog()
	}
	return cmd
}

// refreshSessionsDialog re-lists sessions after a delete, keeping the query
// and clamping the cursor onto the shortened match set.
func (m *ui) refreshSessionsDialog() {
	query, cursor := m.dialog.query, m.dialog.cursor
	m.dialog = dialogState{kind: dialogSessions, open: true, query: query}
	m.listSessionsDialog()
	if !m.dialog.open {
		m.dialogItems = nil
		return
	}
	m.dialog.cursor = max(0, min(cursor, len(m.dialogMatches())-1))
}

// sessionDeleteView renders the confirmation in the dialog box. Title,
// buttons, and hint always fit: at small heights the explanatory body is
// shortened first, so 60x16 never clips a control.
func (m *ui) sessionDeleteView(base []string) string {
	boxWidth := min(m.width-4, 64)
	inner := boxWidth - 4
	muted := withBase(m.theme.Muted, m.theme.Base)
	errStyle := withBase(m.theme.Error, m.theme.Base)
	blank := m.theme.Base.Render(fit("", inner))

	type styledLine struct {
		text  string
		muted bool
	}
	var body []styledLine
	for _, line := range wrapWords("Delete \""+m.dialog.deleteLabel+"\"? This permanently removes "+sessionDeleteLoss+". It cannot be undone.", inner) {
		body = append(body, styledLine{text: line})
	}
	if m.dialog.deleteID == m.snapshot.ID {
		for _, line := range wrapWords("This is the current session; a fresh session replaces it.", inner) {
			body = append(body, styledLine{text: line, muted: true})
		}
	}
	var notes []string
	if reason := m.sessionDeleteBlocked(); reason != "" {
		for _, line := range wrapWords(reason, inner) {
			notes = append(notes, errStyle.Render(fit(line, inner)))
		}
	}
	if m.dialog.deleteErr != "" {
		for _, line := range wrapWords("Delete failed: "+m.dialog.deleteErr, inner) {
			notes = append(notes, errStyle.Render(fit(line, inner)))
		}
	}
	if m.dialog.deleteBusy {
		notes = append(notes, muted.Render(fit("Deleting…", inner)))
	}
	var hint []string
	for _, line := range wrapHint("←/→ choose  Enter confirm  Esc back", inner) {
		hint = append(hint, withBase(m.theme.Help, m.theme.Base).Render(fit(line, inner)))
	}

	// Fixed rows: title, blank, blank, buttons, blank, hint; the border adds
	// two more. Notes outrank the body when the height runs short.
	room := max(0, m.height-2-5-len(hint))
	if len(notes) > room {
		notes = notes[len(notes)-room:]
	}
	room -= len(notes)
	if len(body) > room {
		body = body[:room]
		if room > 0 {
			last := &body[room-1]
			last.text = strings.TrimRight(fit(last.text, inner-1), " ") + "…"
		}
	}

	content := []string{withBase(m.theme.Title, m.theme.Base).Render(fit("Delete session", inner)), blank}
	for _, line := range body {
		style := m.theme.Base
		if line.muted {
			style = muted
		}
		content = append(content, style.Render(fit(line.text, inner)))
	}
	content = append(content, notes...)
	content = append(content, blank, m.sessionDeleteButtons(inner), blank)
	content = append(content, hint...)
	return m.overlayDialogBox(base, content, boxWidth)
}

// sessionDeleteButtons renders Cancel and Delete on one row; the focused
// button uses the Selected style and a cursor marker.
func (m *ui) sessionDeleteButtons(inner int) string {
	button := func(label string, focused bool) (string, int) {
		text := "  " + label + "  "
		if focused {
			text = "> " + label + "  "
			return withBase(m.theme.Selected, m.theme.Base).Render(text), runewidth.StringWidth(text)
		}
		return m.theme.Base.Render(text), runewidth.StringWidth(text)
	}
	cancel, cw := button("Cancel", !m.dialog.deleteFocus)
	del, dw := button("Delete", m.dialog.deleteFocus)
	if cw+dw > inner {
		// Too narrow for both: show only the focused button.
		focused := "> Cancel"
		if m.dialog.deleteFocus {
			focused = "> Delete"
		}
		return withBase(m.theme.Selected, m.theme.Base).Render(fit(focused, inner))
	}
	return cancel + del + m.theme.Base.Render(fit("", inner-cw-dw))
}

// wrapHint packs a hint's double-space separated groups onto lines no wider
// than width, so a key binding is never split or clipped.
func wrapHint(hint string, width int) []string {
	var lines []string
	line := ""
	for _, group := range strings.Split(hint, "  ") {
		if group == "" {
			continue
		}
		if line != "" && runewidth.StringWidth(line+"  "+group) > width {
			lines = append(lines, line)
			line = ""
		}
		if line == "" {
			line = group
		} else {
			line += "  " + group
		}
	}
	return append(lines, line)
}

// wrapWords breaks text at spaces to the given width, falling back to the
// rune wrap for a single word wider than a line.
func wrapWords(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && runewidth.StringWidth(line+" "+word) > width {
			lines = append(lines, line)
			line = ""
		}
		if line == "" {
			if runewidth.StringWidth(word) > width {
				parts := wrap(word, width)
				lines = append(lines, parts[:len(parts)-1]...)
				line = parts[len(parts)-1]
				continue
			}
			line = word
		} else {
			line += " " + word
		}
	}
	return append(lines, line)
}
