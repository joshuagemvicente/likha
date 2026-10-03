package tui

import (
	"lisa/internal/providers"
	"strings"

	"github.com/mattn/go-runewidth"
)

var composerStyles = []string{"minimal", "bordered", "borderless", "chatter"}

func validComposerStyle(style string) string {
	for _, available := range composerStyles {
		if style == available {
			return style
		}
	}
	return "minimal"
}

// composerLines reserves its own viewport rows; a long draft shows its editable
// tail without displacing the transcript, mention popup, or status line.
func (m *ui) composerLines() []string {
	if m.pending != nil {
		// A pending review replaces the composer with the decision bar; the
		// draft, caret, and borders do not render.
		return m.reviewActionLines()
	}
	width := m.width
	style := m.composerStyle
	if width <= minWidth && (style == "bordered" || style == "chatter") {
		style = "minimal"
	}
	if style == "" {
		style = "minimal"
	}
	fixed := 1 // borderless/chatter: separator above input
	if style == "minimal" {
		fixed = 2 // separator and rule above input
	} else if style == "bordered" {
		fixed = 3 // separator and top/bottom edges around input
	}
	inputWidth := width
	if style == "bordered" {
		inputWidth -= 4 // │ + one space of inset on each side
	}
	if style == "chatter" {
		inputWidth -= runewidth.StringWidth("You › ")
	}
	inputWidth = max(1, inputWidth)
	text := string(m.input)
	placeholder := len(m.input) == 0
	caret := ""
	if m.caretVisible() {
		caret = "█"
	}
	if placeholder {
		text = "Ask Likha… // escapes a slash"
		if m.working {
			// A run is active but editable (no review pending): the draft is
			// a steering prompt queued for the next provider-call boundary.
			text = "Type to queue… (Enter queues, Esc cancels)"
		} else if n := len(m.queue); n > 0 {
			// A run ended with messages held: Enter sends them (FR-21).
			text = "Enter sends the queued message… (Esc clears)"
			if n > 1 {
				text = "Enter sends the queued messages… (Esc clears)"
			}
		}
		if caret != "" {
			// An empty draft renders the caret before the placeholder, like
			// a browser's placeholder with a focused empty input.
			text = caret + text
			caret = ""
		}
	}
	if caret != "" {
		// The block caret renders at the insertion point (FR-17), which a
		// multi-line draft may sit inside any wrapped row of.
		c := min(len(m.input), max(0, m.edit.caret))
		text = string(m.input[:c]) + caret + string(m.input[c:])
	}
	input := wrap(text, inputWidth)
	limit := max(1, m.height-len(m.header())-m.statusLineHeight()-len(m.mentionLines())-len(m.commandLines())-1-fixed)
	if len(input) > limit {
		input = input[len(input)-limit:]
	}
	rows := make([]string, 0, len(input)+fixed)
	gap := m.theme.Base.Render(fit("", width))
	rows = append(rows, gap)
	if style == "minimal" {
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render(strings.Repeat("─", width)))
	} else if style == "bordered" {
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render("╭"+strings.Repeat("─", width-2)+"╮"))
	}
	for i, line := range input {
		switch style {
		case "bordered":
			inner := withBase(m.theme.Normal, m.theme.Base).Render(fit(line, width-4))
			if placeholder {
				inner = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, width-4))
			}
			border := withBase(m.theme.Border, m.theme.Base)
			rows = append(rows, border.Render("│ ")+inner+border.Render(" │"))
		case "chatter":
			prefix := "      "
			if i == 0 {
				prefix = "You › "
			}
			content := withBase(m.theme.Normal, m.theme.Base).Render(fit(line, inputWidth))
			if placeholder {
				content = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, inputWidth))
			}
			rows = append(rows, withBase(m.theme.Selected, m.theme.Base).Render(prefix)+content)
		default:
			content := withBase(m.theme.Normal, m.theme.Base).Render(fit(line, width))
			if placeholder {
				content = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, width))
			}
			rows = append(rows, content)
		}
	}
	if style == "bordered" {
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render("╰"+strings.Repeat("─", width-2)+"╯"))
	}
	return rows
}

// reviewActionLines renders the pinned decision bar that replaces the
// composer while a review is pending (FR-22): two labelled buttons with
// exactly one focus marker, then the key hint. It reuses the composer's
// reserved rows, so the layout budget is unchanged. Approve stays muted and
// cannot be confirmed until every review page has been seen; Decline is
// always available.
func (m *ui) reviewActionLines() []string {
	ready := m.reviewReady()
	approveStyle := withBase(m.theme.Muted, m.theme.Base)
	approve := approveStyle.Render("  [ Approve ]")
	if m.reviewFocus == focusApprove {
		style := m.theme.Muted
		if ready {
			style = m.theme.Selected
		}
		approve = withBase(style, m.theme.Base).Render("> [ Approve ]")
	}
	decline := approveStyle.Render("  [ Decline ]")
	if m.reviewFocus == focusDecline {
		decline = withBase(m.theme.Selected, m.theme.Base).Render("> [ Decline ]")
	}
	hint := "←/→ choose · Enter confirm · Esc cancel run"
	if !ready {
		hint = reviewGateStatus + " · ←/→ choose · Esc cancel run"
	}
	if m.width < 50 {
		// The narrow bar keeps the gate message: it is the only surface that
		// explains why Approve is muted.
		hint = "←/→ Enter Esc"
		if !ready {
			hint = reviewGateStatus
		}
	}
	// Each button reserves a two-column marker slot ("  ", or "> " when
	// focused); a two-space gap between the cells leaves the four-space
	// separation the spec shows. fit measures plain text only, so pad the
	// styled row to the full width explicitly.
	buttons := approve + m.theme.Base.Render("  ") + decline
	if w := runewidth.StringWidth(stripANSI(buttons)); w < m.width {
		buttons += m.theme.Base.Render(strings.Repeat(" ", m.width-w))
	}
	return []string{buttons, withBase(m.theme.Muted, m.theme.Base).Render(fit(hint, m.width))}
}

// An unsuccessful write leaves both the active appearance and the stored
// choice untouched. A corrupt or unreadable config is never overwritten.
func (m *ui) applyComposerStyle(style string) {
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err == nil {
		cfg.Composer = &providers.StoredComposerConfig{Style: style}
		err = providers.SaveStoredConfig(m.stateDir, cfg)
	}
	if err != nil {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: "Store composer style: " + err.Error()})
		m.layoutWidth = 0
		return
	}
	m.composerStyle = style
	m.conn.ComposerStyle = style
}
