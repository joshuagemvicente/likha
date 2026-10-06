package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Every frame has the same cell width. Only this bottom-status marker moves;
// the Working label, editable input, and transcript layout remain still.
var statusSpinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *ui) statusWorking() bool {
	return m.working && !m.cancelling && m.pending == nil && m.status == "Waiting for model"
}

func (m *ui) statusSpinner() string {
	if m.conn.ASCII {
		return activitySpinner(m.activityFrame)
	}
	return statusSpinnerFrames[m.activityFrame%len(statusSpinnerFrames)]
}

// renderStatusHint runs after width fitting, so ANSI never enters the footer's
// measurements. Only the spinner is accented; other hints are untouched.
func (m *ui) renderStatusHint(text string, style lipgloss.Style) string {
	if !m.statusWorking() || !strings.HasPrefix(text, m.statusState()) {
		return style.Render(text)
	}
	runes := []rune(text)
	var out strings.Builder
	out.WriteString(m.theme.Accent.Render(string(runes[:1])))
	out.WriteString(style.Render(string(runes[1:])))
	return out.String()
}
