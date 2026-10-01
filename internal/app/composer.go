package app

import (
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
	if m.pending != nil {
		if m.status == "Review every page before approving" {
			text = m.reviewMark("Read all pages before approving")
		} else {
			text = m.reviewMark("Review before approval")
		}
		placeholder = false
	} else if placeholder {
		text = "Ask Likha… // escapes a slash"
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
		rows = append(rows, m.theme.Border.Render(strings.Repeat("─", width)))
	} else if style == "bordered" {
		rows = append(rows, m.theme.Border.Render("╭"+strings.Repeat("─", width-2)+"╮"))
	}
	for i, line := range input {
		switch style {
		case "bordered":
			inner := withBase(m.theme.Base, m.theme.Base).Render(fit(line, width-4))
			if placeholder {
				inner = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, width-4))
			} else if m.pending != nil {
				inner = withBase(m.theme.Warning, m.theme.Base).Render(fit(line, width-4))
			}
			rows = append(rows, m.theme.Border.Render("│ ")+inner+m.theme.Border.Render(" │"))
		case "chatter":
			prefix := "      "
			if i == 0 {
				prefix = "You › "
			}
			content := withBase(m.theme.Base, m.theme.Base).Render(fit(line, inputWidth))
			if placeholder {
				content = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, inputWidth))
			} else if m.pending != nil {
				content = withBase(m.theme.Warning, m.theme.Base).Render(fit(line, inputWidth))
			}
			rows = append(rows, m.theme.Selected.Render(prefix)+content)
		default:
			content := withBase(m.theme.Base, m.theme.Base).Render(fit(line, width))
			if placeholder {
				content = withBase(m.theme.Muted, m.theme.Base).Render(fit(line, width))
			} else if m.pending != nil {
				content = withBase(m.theme.Warning, m.theme.Base).Render(fit(line, width))
			}
			rows = append(rows, content)
		}
	}
	if style == "bordered" {
		rows = append(rows, m.theme.Border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	}
	return rows
}

// An unsuccessful write leaves both the active appearance and the stored
// choice untouched. A corrupt or unreadable config is never overwritten.
func (m *ui) applyComposerStyle(style string) {
	cfg, err := loadStoredConfig(m.stateDir)
	if err == nil {
		cfg.Composer = &storedComposerConfig{Style: style}
		err = saveStoredConfig(m.stateDir, cfg)
	}
	if err != nil {
		m.status = "Error"
		m.entries = append(m.entries, entry{role: "Error", content: "Store composer style: " + err.Error()})
		m.layoutWidth = 0
		return
	}
	m.composerStyle = style
	m.conn.composerStyle = style
}
