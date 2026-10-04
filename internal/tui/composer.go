package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"

	"likha/internal/providers"
	likhaui "likha/internal/ui"
)

// composerStyles lists the chooser's styles; the first entry is the default
// for users with no saved choice (transcript-redesign § Composer).
var composerStyles = []string{"rounded", "minimal", "bordered", "borderless", "chatter"}

func validComposerStyle(style string) string {
	for _, available := range composerStyles {
		if style == available {
			return style
		}
	}
	return composerStyles[0]
}

// composerPrompt is the rounded style's prompt glyph; wrapped rows indent by
// its width so continuation text lines up under the first row's text.
const composerPrompt = "> "

// roundedBoxRunes returns the composer's corners and edges (the rounded and
// bordered boxes and the minimal rule) from the block glyph set, the plain
// set when ascii is set.
func roundedBoxRunes(ascii bool) (topLeft, topRight, bottomLeft, bottomRight, horizontal, vertical string) {
	g := likhaui.BlockGlyphSet(ascii)
	return g.TopLeft, g.TopRight, g.BottomLeft, g.BottomRight, g.Horizontal, g.Vertical
}

// composerASCII reports whether the composer draws the ASCII box: the
// --ascii / LIKHA_ASCII block-glyph selection carried on the connection.
func (m *ui) composerASCII() bool {
	return m.conn.ASCII
}

// composerLayout resolves the style the composer actually draws at the
// current width, the rows it reserves besides the input (fixed), and the
// width left for input text. Narrow terminals degrade the boxed and prefixed
// styles to minimal without changing the saved selection.
func (m *ui) composerLayout() (style string, fixed, inputWidth int) {
	style = m.composerStyle
	if style == "" {
		style = composerStyles[0]
	}
	if m.width <= minWidth && (style == "rounded" || style == "bordered" || style == "chatter") {
		style = "minimal"
	}
	inputWidth = m.width
	switch style {
	case "minimal":
		fixed = 2 // separator and rule above input
	case "bordered":
		fixed = 3       // separator and top/bottom edges around input
		inputWidth -= 4 // │ + one space of inset on each side
	case "rounded":
		fixed = 3                                               // separator and top/bottom edges around input
		inputWidth -= 4 + runewidth.StringWidth(composerPrompt) // edges, insets, and prompt
	case "chatter":
		fixed = 1 // separator above input
		inputWidth -= runewidth.StringWidth("You › ")
	default:
		fixed = 1 // borderless: separator above input
	}
	return style, fixed, max(1, inputWidth)
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
	style, fixed, inputWidth := m.composerLayout()
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
	hidden := 0 // leading wrapped rows scrolled off above the editable tail
	if len(input) > limit {
		hidden = len(input) - limit
		input = input[hidden:]
	}
	rows := make([]string, 0, len(input)+fixed)
	gap := m.theme.Base.Render(fit("", width))
	rows = append(rows, gap)
	topLeft, topRight, bottomLeft, bottomRight, horizontal, vertical := roundedBoxRunes(m.composerASCII())
	if style == "minimal" {
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render(strings.Repeat(horizontal, width)))
	} else if style == "bordered" {
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render(topLeft+strings.Repeat(horizontal, width-2)+topRight))
	} else if style == "rounded" {
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render(topLeft+strings.Repeat(horizontal, width-2)+topRight))
	}
	for i, line := range input {
		switch style {
		case "bordered":
			inner := withBase(m.theme.Normal, m.theme.Base).Render(fit(line, width-4))
			if placeholder {
				inner = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, width-4))
			}
			border := withBase(m.theme.Border, m.theme.Base)
			rows = append(rows, border.Render(vertical+" ")+inner+border.Render(" "+vertical))
		case "rounded":
			// The prompt glyph marks the draft's first row; wrapped rows
			// continue under the text column, inside the box.
			prompt := strings.Repeat(" ", runewidth.StringWidth(composerPrompt))
			if i == 0 && hidden == 0 {
				prompt = composerPrompt
			}
			inner := withBase(m.theme.Normal, m.theme.Base).Render(fit(line, inputWidth))
			if placeholder {
				inner = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, inputWidth))
			}
			border := withBase(m.theme.Border, m.theme.Base)
			rows = append(rows, border.Render(vertical+" ")+withBase(m.theme.Normal, m.theme.Base).Render(prompt)+inner+border.Render(" "+vertical))
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
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render(bottomLeft+strings.Repeat(horizontal, width-2)+bottomRight))
	} else if style == "rounded" {
		rows = append(rows, withBase(m.theme.Border, m.theme.Base).Render(bottomLeft+strings.Repeat(horizontal, width-2)+bottomRight))
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
