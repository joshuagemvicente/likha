package tui

// Model-question interaction for the ask_user tool (specs/ask-user). This
// file owns askState, the askPromptMsg/askClosedMsg message types, and all
// rendering/key handling for the pending question. The coordinator owns the
// `ask askState` field on ui, the Update routing plus View branch (snippet
// in the phase report), and the reply channel that askPromptMsg carries.
//
// Invariants held here:
//   - The interaction serializes with approval dialogs: askVisible() is
//     false while an approval, dialog, key modal, or setup is active, and
//     openAskQuestion refuses (skipped reply) if one is somehow open.
//   - The composer draft, caret state, and queued steering prompts are
//     never touched: every chord is swallowed by handleAskKey while the
//     question is pending, so typed answer text can never become a
//     steering message or clear the held queue (FR-30/FR-21).
//   - Answers are exactly-once: responded flips true and the dialog closes
//     before the reply callback is invoked, and askClosedMsg routing
//     compares generations so a late close can never clobber a newer
//     question.
//   - The transcript records the full question, the answer or skip, and
//     interruptions as conversation events, persisted like any other entry.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"likha/internal/tools"
)

// askHint is the always-pinned key help at the bottom of the dialog.
const askHint = "↑/↓ select  Tab free text  Enter submit  Ctrl+S skip  Esc cancel run"

// askDefaultSkipHint informs the user where typed answer text travels
// (FR-30). It renders muted inside the dialog and never implies that an
// answer grants edit, command, MCP trust, or network permission.
const askDefaultSkipHint = "Typed answer text reaches your configured provider."

// Spec bounds (specs/ask-user): a 4 KiB question, at most eight options of
// 120 characters, and an 8 KiB free-text answer. The tool worker validates
// calls before opening the interaction; these clamps are a rendering
// backstop, they never replace that validation.
const (
	askUserQuestionBytes = 4096
	askUserOptionRunes   = 120
	askUserAnswerBytes   = 8192
	askUserMaxOptions    = 8
)

// askMode mirrors where the selection rests: on a numbered choice
// (askChoice) or on the always-present free-text row (askText). The
// free-text row is the last selectable row, so mode and selected stay
// synced through syncMode().
type askMode uint8

const (
	askChoice askMode = iota
	askText
)

// askState is the whole pending ask_user interaction. One question is
// pending at a time; count lives in the coordinator.
type askState struct {
	pending    bool   // a question is on screen awaiting an answer
	id         string // coordinator's delivery identity
	callID     string // original tool call ID the answer must attach to
	question   string
	options    []string              // selectable choices; the free-text row follows them
	selected   int                   // row index in [0 .. len(options)]: last is free text
	text       string                // free-text answer being typed; end-anchored editing
	mode       askMode               // mirror of selected position; see syncMode
	generation uint64                // bumped on open and close; askClosedMsg routing compares it
	responded  bool                  // set once just before close+reply; guards exactly-once delivery
	skipHint   string                // muted note shown inside the dialog (e.g. provider warning)
	committed  func(tools.AskAnswer) // coordinator-supplied reply callback
	scroll     int                   // vertical page offset when the body is tall
}

// rowCount counts selectable rows: every numbered choice plus the
// always-present free-text row.
func (a *askState) rowCount() int { return len(a.options) + 1 }

// freeRow is the index of the free-text row: always the last one.
func (a *askState) freeRow() int { return len(a.options) }

// textMode reports whether the selection rests on the free-text row, which
// is where typed answer text is edited.
func (a *askState) textMode() bool { return a.selected >= len(a.options) }

// syncMode keeps the mode mirror aligned with the selection after every
// selected/mode-affecting change.
func (a *askState) syncMode() {
	if a.textMode() {
		a.mode = askText
		return
	}
	a.mode = askChoice
}

// askPromptMsg opens a question. Routing (coordinator):
//
//	case askPromptMsg:
//	    m.openAskQuestion(tools.AskRequest{Question: v.question, Options: v.options}, v.id, v.callID, v.reply)
//	    return m, nil
type askPromptMsg struct {
	id, callID string
	question   string
	options    []string
	generation uint64 // pairing bookkeeping alongside askClosedMsg; the UI bumps its own generation in openAskQuestion
	reply      func(tools.AskAnswer)
}

// askClosedMsg closes the pending question. Routing (coordinator):
//
//	case askClosedMsg:
//	    if m.ask.pending && m.ask.committed != nil && v.generation == m.ask.generation {
//	        m.closeAsk()
//	    }
//	    return m, nil
//
// The generation compare makes a late close for an already-replaced or
// already-delivered question a no-op. closeAsk() itself is idempotent, so
// a coordinator that delivers closes strictly before opening the next
// question may also route this case straight to m.closeAsk().
type askClosedMsg struct{ generation uint64 }

// askVisible reports whether the question dialog owns the screen. It
// serializes with every other overlay: approval reviews, selection dialogs,
// the provider key modal, and first-run setup.
func (m *ui) askVisible() bool {
	return m.ask.pending && !m.ask.responded &&
		m.pending == nil && !m.keyModal.open && !m.dialog.open &&
		m.mode != modeSetup
}

// openAskQuestion stores the interaction, records the complete question as
// a conversation event, and shows the status note. A non-askable surface is
// a defensive backstop (the coordinator serializes deliveries): the
// question is dropped with a skipped reply so the model call resolves
// instead of hanging.
func (m *ui) openAskQuestion(rec tools.AskRequest, id, callID string, reply func(tools.AskAnswer)) {
	if rec.Question == "" || reply == nil ||
		m.mode == modeSetup || m.pending != nil || m.keyModal.open || m.dialog.open || m.ask.pending {
		m.entries = append(m.entries, entry{role: "Error", content: "Question dropped: another interaction is active or the call was malformed; no answer was sent."})
		m.layoutWidth = 0
		if reply != nil {
			reply(tools.AskAnswer{Skipped: true})
		}
		m.persist()
		return
	}
	// The question dialog replaces historical inspection surfaces exactly
	// like an arriving approval does (see the approval case in Update).
	m.resetToolInspection()
	m.resetAgentInspection()
	m.ask.generation++
	m.ask = askState{
		pending:    true,
		id:         id,
		callID:     callID,
		question:   askTruncateBytes(rec.Question, askUserQuestionBytes),
		options:    askClampOptions(rec.Options),
		selected:   0,
		generation: m.ask.generation,
		skipHint:   askDefaultSkipHint,
		committed:  reply,
	}
	m.ask.syncMode()
	m.entries = append(m.entries, entry{role: "Likha", content: "Question from the model:\n" + m.ask.question})
	m.jumpBottom()
	m.layoutWidth = 0
	m.persist()
	m.status = "Question pending: answer, skip, or cancel"
}

// askClampOptions bounds the choices to the spec limits: at most eight
// entries of 120 characters, with safe truncation as a backstop even
// though the tool worker already validated the call.
func askClampOptions(options []string) []string {
	clamped := make([]string, 0, min(len(options), askUserMaxOptions))
	for _, option := range options {
		if len(clamped) == askUserMaxOptions {
			break
		}
		clamped = append(clamped, askTruncateRunes(option, askUserOptionRunes))
	}
	return clamped
}

// askTruncateBytes cuts text at a rune boundary once limit bytes are
// reached (the spec bounds are in bytes).
func askTruncateBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	size := 0
	for i, r := range text {
		width := utf8.RuneLen(r)
		if width < 0 {
			width = 1
		}
		if size+width > limit {
			return text[:i]
		}
		size += width
	}
	return text
}

// askTruncateRunes caps text at limit characters.
func askTruncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	var b strings.Builder
	count := 0
	for _, r := range text {
		if count == limit {
			break
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}

// closeAsk clears the pending state idempotently. A zero-value ask is
// never pending, so repeated calls are no-ops, and the generation bump
// anneals any askClosedMsg that would otherwise race a replacement
// question. The composer draft, caret, focus, and queued steering prompts
// were never touched while the question was pending — handleAskKey
// swallowed every chord — so there is nothing to restore here
// (spec FR-30/FR-21).
func (m *ui) closeAsk() {
	if !m.ask.pending {
		return
	}
	m.ask.generation++
	m.ask = askState{generation: m.ask.generation}
}

// answerAsk records the outcome as a conversation event, closes the dialog
// with exactly-once semantics, and only then invokes the reply. respond
// happens synchronously inside Update (mirroring the approval Reply send),
// so an unanswered model call blocks the UI here by design.
func (m *ui) answerAsk(note string, answer tools.AskAnswer) {
	reply := m.ask.committed
	m.entries = append(m.entries, entry{role: "Likha", content: note})
	m.jumpBottom()
	m.layoutWidth = 0
	m.persist()
	m.ask.responded = true
	m.closeAsk()
	if answer.Skipped {
		m.status = "Question skipped"
	} else {
		m.status = "Answer sent"
	}
	if reply != nil {
		reply(answer)
	}
}

// askInterruptedNote records an unanswered request as interrupted (FR-30):
// the coordinator calls it on resume/cancel; no answer is replayed and the
// dialog is not reopened later.
func (m *ui) askInterruptedNote() {
	if !m.ask.pending || m.ask.responded {
		return
	}
	m.entries = append(m.entries, entry{role: "Likha", content: "Question interrupted before an answer; no response was sent."})
	m.jumpBottom()
	m.layoutWidth = 0
	m.persist()
	m.closeAsk()
}

// moveAskSelection cycles the selection across choices plus the free-text
// row, wrapping in both directions like tool inspection cursors.
func (m *ui) moveAskSelection(delta int) {
	m.ask.selected = toolWrappedIndex(m.ask.selected+delta, m.ask.rowCount())
	m.ask.syncMode()
}

// appendAskText extends the free-text answer. Every rune is dropped once
// the spec's byte bound is reached; the text never touches the composer.
func (m *ui) appendAskText(runes []rune) {
	for _, r := range runes {
		if len(m.ask.text)+utf8.RuneLen(r) > askUserAnswerBytes {
			return
		}
		m.ask.text += string(r)
	}
}

// handleAskKey intercepts keys ONLY while askVisible(): the call site lives
// before popup/modal/review handling in Update. ctrl+c is never
// intercepted so run cancellation keeps working through it (FR-30; the
// same rule handleToolInspection applies). Esc closes the dialog and keeps
// being handled by the coordinator: with a run active its Esc path
// cancels the run (spec: Esc cancels the active run and clears the
// pending question), while idle there is nothing to cancel so the chord is
// swallowed to protect the draft and popups.
func (m *ui) handleAskKey(msg tea.KeyMsg) (handled bool, cmd tea.Cmd) {
	if !m.askVisible() {
		return false, nil
	}
	if m.ask.responded {
		return false, nil // already delivered; the dialog is closed anyway
	}
	switch msg.String() {
	case "ctrl+c":
		// Never intercepted: cancellation (or idle quit) stays live.
		return false, nil
	case "esc":
		m.closeAsk()
		return !m.working, nil
	case "up":
		m.moveAskSelection(-1)
	case "down":
		m.moveAskSelection(1)
	case "pgup", "ctrl+p":
		m.ask.scroll -= max(1, m.height-10)
		m.ask.scroll = max(0, m.ask.scroll)
	case "pgdown", "ctrl+n":
		m.ask.scroll += max(1, m.height-10)
	case "tab", "shift+tab":
		// Toggle between the choices and the free-text row. Without
		// options the selection already rests on free text.
		if m.ask.textMode() {
			if len(m.ask.options) > 0 {
				m.ask.selected = 0
				m.ask.syncMode()
			}
			break
		}
		m.ask.selected = m.ask.freeRow()
		m.ask.syncMode()
	case "enter":
		if m.ask.textMode() {
			if strings.TrimSpace(m.ask.text) == "" {
				// An empty text answer is not an answer; nothing submits.
				return true, nil
			}
			m.answerAsk("ask answered (text): "+m.ask.text, tools.AskAnswer{Text: m.ask.text})
			return true, nil
		}
		if m.ask.selected < 0 || m.ask.selected >= len(m.ask.options) {
			return true, nil
		}
		chosen := m.ask.options[m.ask.selected]
		m.answerAsk("ask answered (choice): "+chosen, tools.AskAnswer{Text: chosen})
	case "ctrl+s":
		// Skip is a refused/unanswered result, never an empty success.
		m.answerAsk("question skipped", tools.AskAnswer{Skipped: true})
		return true, nil
	case "backspace", "ctrl+h":
		if m.ask.textMode() {
			if runes := []rune(m.ask.text); len(runes) > 0 {
				m.ask.text = string(runes[:len(runes)-1])
			}
		}
	case "ctrl+u":
		if m.ask.textMode() {
			m.ask.text = ""
		}
	case "ctrl+j", "alt+enter", "ctrl+enter", "shift+enter":
		// The newline family (same tokens the composer accepts) extends
		// the free-text answer; submission flattening matches the composer.
		if m.ask.textMode() {
			m.appendAskText([]rune{'\n'})
		}
	default:
		switch {
		case msg.Type == tea.KeyRunes && !msg.Alt:
			runes := msg.Runes
			if msg.Paste {
				// Bracketed paste flattens newlines to spaces exactly like
				// composer paste (user-confirmed paste rule).
				runes = []rune(strings.ReplaceAll(string(runes), "\n", " "))
			}
			if m.ask.textMode() {
				m.appendAskText(runes)
				break
			}
			// A single digit jumps to that numbered row without submitting.
			if len(runes) == 1 && runes[0] >= '1' && runes[0] <= '0'+askUserMaxOptions {
				row := int(runes[0] - '1')
				if row < len(m.ask.options) {
					m.ask.selected = row
					m.ask.syncMode()
				}
			}
		case msg.Type == tea.KeySpace && m.ask.textMode():
			m.appendAskText([]rune{' '})
		}
		// Everything else inside the dialog is swallowed (see the return
		// below), so no stray key can reach the composer or steering queue.
	}
	return true, nil
}

// askLine is one rendered dialog body row. Styles are applied at render
// time over fit() output, never over a wrapped escape sequence.
type askLine struct {
	text  string // unstyled; fit() runs at render time
	style lipgloss.Style
}

// askView renders the pending question as the shared centered modal: the
// conversation is drawn first, the surround is dimmed, and the bordered
// box renders at full intensity (same frame conventions as dialogView).
// The question is wrapped completely — no truncation — to the box width;
// a tall body pages vertically and follows the selection.
func (m *ui) askView() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if !m.askVisible() {
		return m.mainView()
	}
	base := strings.Split(m.mainView(), "\n")
	for i := range base {
		base[i] = dimRowOn(base[i], m.theme.BaseBG())
	}
	width, height := m.width, m.height

	title := "Model question"
	source := "ask_user"
	if m.ask.callID != "" {
		source += " · call " + toolShortText(m.ask.callID, 64)
	}

	// Box width: fit the chrome exactly, let the longest option grow the
	// box up to the terminal, and keep a floor so wrapping always has
	// room. The question then wraps to the final inner width.
	candidate := runewidth.StringWidth(title) + 2
	candidate = max(candidate, runewidth.StringWidth(source)+2)
	candidate = max(candidate, runewidth.StringWidth(askHint)+2)
	if m.ask.skipHint != "" {
		candidate = max(candidate, runewidth.StringWidth(m.ask.skipHint)+8)
	}
	for i, option := range m.ask.options {
		w := runewidth.StringWidth(fmt.Sprintf("%d. %s", i+1, option)) + 6
		candidate = max(candidate, min(w, width-8))
	}
	boxWidth := max(24, min(width-4, candidate+4))
	inner := boxWidth - 4

	// Pinned budget inside the borders: title, source, blank above the
	// body; blank plus hint below. The body window gets what remains.
	pinnedTop, pinnedBottom := 3, 2
	bodyWindow := max(1, height-2-pinnedTop-pinnedBottom)

	// Build the paged body: question, choices, free-text row, typed text.
	var body []askLine
	anchor := -1
	plain := lipgloss.NewStyle()
	add := func(text string, style lipgloss.Style) {
		for _, line := range wrap(text, max(1, inner-2)) {
			body = append(body, askLine{text: line, style: style})
		}
	}
	addSelection := func(prefix, text string, style, contStyle lipgloss.Style, row int, anchorLast bool) {
		start := len(body)
		// Wrap so both the prefixed first line and the "  "-prefixed
		// continuation stay inside the box: the shared width subtracts the
		// widest prefix, then fit() pads but never trims (view.go's fit).
		lines := wrap(text, max(1, inner-max(runewidth.StringWidth(prefix), 2)))
		for i, line := range lines {
			styled := style
			if i > 0 {
				styled = contStyle
			}
			lead := "  "
			if i == 0 {
				lead = prefix
			}
			body = append(body, askLine{text: lead + line, style: styled})
		}
		if row == m.ask.selected {
			if anchorLast {
				anchor = len(body) - 1
			} else {
				anchor = start
			}
		}
	}
	add(m.ask.question, plain)
	add("", m.theme.Base)
	for i, option := range m.ask.options {
		if i == m.ask.selected {
			addSelection(fmt.Sprintf("> %d. ", i+1), option, withBase(m.theme.Selected, m.theme.Base), withBase(m.theme.Selected, m.theme.Base), i, false)
			continue
		}
		addSelection(fmt.Sprintf("  %d. ", i+1), option, m.theme.Base, m.theme.Base, i, false)
	}
	if m.ask.textMode() {
		// Typed answer rendering: prompt line plus wrapped continuation,
		// block caret blinking like the composer's.
		caret := ""
		if m.caretOn {
			caret = "▌"
		}
		addSelection("> ", m.ask.text+caret, withBase(m.theme.Selected, m.theme.Base), m.theme.Base, m.ask.freeRow(), true)
	} else {
		addSelection("  ", "Free text", m.theme.Base, m.theme.Base, m.ask.freeRow(), false)
	}

	// Clamp the page offset, then keep the selected row visible. When the
	// body fits, the offset is forced to the top.
	total := len(body)
	if anchor < 0 {
		anchor = m.ask.scroll
	}
	m.ask.scroll = max(0, min(m.ask.scroll, max(0, total-1)))
	if anchor < m.ask.scroll {
		m.ask.scroll = anchor
	} else if anchor >= m.ask.scroll+bodyWindow {
		m.ask.scroll = anchor - bodyWindow + 1
	}
	m.ask.scroll = max(0, min(m.ask.scroll, max(0, total-bodyWindow)))
	start := m.ask.scroll
	end := min(total, start+bodyWindow)
	if end-start < total {
		title += fmt.Sprintf(" · lines %d–%d/%d", start+1, end, total)
	}

	// Pinned chrome and body, everything fitted and styled up front.
	rendered := make([]string, 0, 2+pinnedTop+(end-start)+pinnedBottom)
	rendered = append(rendered, withBase(m.theme.Title, m.theme.Base).Render(fit(title, inner)))
	rendered = append(rendered, withBase(m.theme.Muted, m.theme.Base).Render(fit(source, inner)))
	rendered = append(rendered, m.theme.Base.Render(fit("", inner)))
	if total == 0 {
		rendered = append(rendered, m.theme.Muted.Render(fit("The question is empty.", inner)))
	}
	for i := start; i < end; i++ {
		rendered = append(rendered, body[i].style.Render(fit(body[i].text, inner)))
	}
	rendered = append(rendered, m.theme.Base.Render(fit("", inner)))
	if m.ask.skipHint != "" {
		for _, line := range wrap(m.ask.skipHint, inner) {
			rendered = append(rendered, withBase(m.theme.Muted, m.theme.Base).Render(fit(line, inner)))
		}
	} else {
		rendered = append(rendered, m.theme.Base.Render(fit("", inner)))
	}
	for _, line := range wrap(askHint, inner) {
		rendered = append(rendered, withBase(m.theme.Help, m.theme.Base).Render(fit(line, inner)))
	}

	boxHeight := len(rendered)
	top := max(0, (height-boxHeight-2)/2)
	left := max(2, (width-boxWidth)/2)
	for j := 0; j < boxHeight+2 && top+j < height; j++ {
		var box string
		switch {
		case j == 0:
			box = withBase(m.theme.Border, m.theme.Base).Render("╭" + strings.Repeat("─", boxWidth-2) + "╮")
		case j == boxHeight+1:
			box = withBase(m.theme.Border, m.theme.Base).Render("╰" + strings.Repeat("─", boxWidth-2) + "╯")
		default:
			border := withBase(m.theme.Border, m.theme.Base)
			box = border.Render("│ ") + rendered[j-1] + border.Render(" │")
		}
		base[top+j] = spliceRowOn(base[top+j], left, boxWidth, box, m.theme.BaseBG())
	}
	return strings.Join(base[:height], "\n")
}
