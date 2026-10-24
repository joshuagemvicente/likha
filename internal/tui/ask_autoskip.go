package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/tools"
)

// Opt-in ask_user auto-skip (specs/attention-notifications, FR-41): with a
// limit set, a question left untouched that long closes with the existing
// skip refusal, marked as timed out. It never picks an option, and approvals
// and consent never time out.

// askTimerInterval is how often the countdown advances and repaints.
const askTimerInterval = time.Second

// askTimerMsg advances the auto-skip countdown of the question it was armed
// for. A tick whose generation no longer matches (answered, skipped,
// cancelled, or replaced by a newer question) is ignored.
type askTimerMsg struct{ generation uint64 }

func askTimerTick(generation uint64) tea.Cmd {
	return tea.Tick(askTimerInterval, func(time.Time) tea.Msg { return askTimerMsg{generation: generation} })
}

// armAskTimer starts the countdown for a question that just opened, when a
// limit is set; it returns the first tick or nil.
func (m *ui) armAskTimer() tea.Cmd {
	if m.attention.askSkip <= 0 || !m.ask.pending || m.ask.responded {
		return nil
	}
	m.ask.skipLeft = m.attention.askSkip
	m.ask.skipLast = m.now()
	return askTimerTick(m.ask.generation)
}

// stopAskTimer cancels the countdown: the user pressed a key in the dialog,
// so they are present and the question waits like one without a limit (D8).
func (m *ui) stopAskTimer() {
	m.ask.skipLeft = 0
}

// handleAskTimer counts down only while the dialog is on screen (spec 15),
// then delivers the timed-out skip exactly once through answerAsk.
func (m *ui) handleAskTimer(v askTimerMsg) tea.Cmd {
	if !m.ask.pending || m.ask.responded || v.generation != m.ask.generation || m.ask.skipLeft <= 0 {
		return nil
	}
	now := m.now()
	if m.askVisible() {
		m.ask.skipLeft -= now.Sub(m.ask.skipLast)
	}
	m.ask.skipLast = now
	if m.ask.skipLeft > 0 {
		m.layoutWidth = 0
		return askTimerTick(v.generation)
	}
	return m.autoSkipAsk()
}

// autoSkipAsk closes the question as skipped because no answer arrived in
// time. A questionnaire is skipped as a whole (D9): any key would have
// stopped the timer, so no page has a recorded answer.
func (m *ui) autoSkipAsk() tea.Cmd {
	minutes := int(m.attention.askSkip / time.Minute)
	note := fmt.Sprintf("question skipped (timed out after %d min)", minutes)
	return m.answerAsk(note, tools.AskAnswer{Skipped: true, TimedOutMinutes: minutes})
}

// askCountdownLine is the muted dialog line while auto-skip is armed; "" when
// it is not. Remaining time rounds up to whole seconds.
func (m *ui) askCountdownLine() string {
	if m.ask.skipLeft <= 0 {
		return ""
	}
	seconds := int((m.ask.skipLeft + time.Second - 1) / time.Second)
	return fmt.Sprintf("Auto-skips in %d:%02d unless you answer · any key keeps it open", seconds/60, seconds%60)
}
