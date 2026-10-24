package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/providers"
)

// Attention signals (specs/attention-notifications, FR-41): a terminal bell
// and an OSC 9 desktop notification when Likha needs the user or has
// finished. Signals only write to the terminal; they never change the
// screen, the transcript, the draft or the steering queue.

// attentionEvent is what a signal announces. Its text is generic on purpose
// (D6): desktop notification history can be seen by others, so it never
// carries a path, a command, a question or model output.
type attentionEvent uint8

const (
	attentionApproval attentionEvent = iota
	attentionQuestion
	attentionRunFinished
	attentionRunFailed
)

func (e attentionEvent) text() string {
	switch e {
	case attentionApproval:
		return "approval needed"
	case attentionQuestion:
		return "question waiting"
	case attentionRunFinished:
		return "run finished"
	default:
		return "run failed"
	}
}

// attentionRunMinimum is the shortest run whose end signals (D4): a shorter
// run ends while the user is still watching.
const attentionRunMinimum = 10 * time.Second

// attentionTextLimit bounds the notification text after cleaning.
const attentionTextLimit = 128

// terminalFocus is what the terminal last reported about focus (D3).
type terminalFocus uint8

const (
	focusUnknown terminalFocus = iota // never reported: signals fire
	focusFocused                      // the user is looking: signals stay quiet
	focusBlurred
)

// attentionState holds the resolved settings and the terminal writer.
type attentionState struct {
	out     io.Writer // nil disables every signal
	bell    bool
	desktop bool
	folder  string // repository folder name, never a full path
	tmux    bool   // wrap OSC 9 for tmux passthrough (D7)
	focus   terminalFocus
	// askSkip is the opt-in ask_user auto-skip limit; 0 means off.
	askSkip time.Duration
	// runStarted is the active run's start, for the run-end minimum (D4).
	runStarted time.Time
}

// newAttentionState resolves the stored settings. An out-of-range auto-skip
// value turns auto-skip off; invalid reports it so the UI can say so.
func newAttentionState(cfg providers.StoredAttentionConfig, out io.Writer, root string, tmux bool) (state attentionState, invalid bool) {
	minutes, ok := cfg.AskSkipLimit()
	return attentionState{
		out:     out,
		bell:    providers.FlagEnabled(cfg.Bell),
		desktop: providers.FlagEnabled(cfg.Desktop),
		folder:  filepath.Base(root),
		tmux:    tmux,
		askSkip: time.Duration(minutes) * time.Minute,
	}, !ok
}

// insideTmux reports whether Likha runs inside tmux, whose OSC 9 needs the
// passthrough wrapper (D7).
func insideTmux() bool { return os.Getenv("TMUX") != "" }

// attentionSkipInvalidNote tells the user an out-of-range auto-skip value
// was ignored.
var attentionSkipInvalidNote = fmt.Sprintf("config.json attention.ask_skip_after_minutes must be a whole number from %d to %d; question auto-skip is off.", providers.MinAskSkipMinutes, providers.MaxAskSkipMinutes)

// sequence builds the bytes for one signal: BEL and/or the OSC 9
// notification, or nil when both are off.
func (a attentionState) sequence(event attentionEvent) []byte {
	var b strings.Builder
	if a.bell {
		b.WriteByte('\a')
	}
	if a.desktop {
		osc := "\x1b]9;" + attentionText(a.folder, event) + "\a"
		if a.tmux {
			// tmux forwards a DCS-wrapped sequence to the outer terminal only
			// with allow-passthrough on; every ESC inside is doubled.
			osc = "\x1bPtmux;" + strings.ReplaceAll(osc, "\x1b", "\x1b\x1b") + "\x1b\\"
		}
		b.WriteString(osc)
	}
	if b.Len() == 0 {
		return nil
	}
	return []byte(b.String())
}

// attentionText is the cleaned notification text: control characters (C0,
// DEL and C1) are dropped so the folder name cannot end or extend the escape
// sequence, and the result is bounded to attentionTextLimit bytes.
func attentionText(folder string, event attentionEvent) string {
	text := "Likha · " + folder + ": " + event.text()
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, ""))
	if len(cleaned) <= attentionTextLimit {
		return cleaned
	}
	cut := attentionTextLimit
	for cut > 0 && !utf8.RuneStart(cleaned[cut]) {
		cut--
	}
	return cleaned[:cut]
}

// signalAttention writes one signal unless signals are off or the terminal
// says the user is looking at it.
func (m *ui) signalAttention(event attentionEvent) {
	a := m.attention
	if a.out == nil || a.focus == focusFocused {
		return
	}
	seq := a.sequence(event)
	if seq == nil {
		return
	}
	// Best effort by design: a terminal that cannot take the write cannot
	// show an error about it either, and the run must not depend on it.
	_, _ = a.out.Write(seq)
}

// markRunStarted records the start of a run for the run-end minimum.
func (m *ui) markRunStarted() {
	m.attention.runStarted = m.now()
}

// signalRunEnd signals a finished or failed run that lasted long enough. A
// cancelled run is not signalled (D5): the user pressed Esc, so they are
// present.
func (m *ui) signalRunEnd(failed, cancelled bool) {
	started := m.attention.runStarted
	m.attention.runStarted = time.Time{}
	if cancelled || started.IsZero() || m.now().Sub(started) < attentionRunMinimum {
		return
	}
	if failed {
		m.signalAttention(attentionRunFailed)
		return
	}
	m.signalAttention(attentionRunFinished)
}

// handleFocus records the terminal's focus reports (D3).
func (m *ui) handleFocus(msg tea.Msg) {
	switch msg.(type) {
	case tea.FocusMsg:
		m.attention.focus = focusFocused
	case tea.BlurMsg:
		m.attention.focus = focusBlurred
	}
}
