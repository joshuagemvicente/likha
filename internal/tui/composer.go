package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"likha/internal/agent"
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

// composerGlyph retains the source rune of each safe display glyph. An escape
// can span rows, but all of its glyphs still select and copy as one source rune.
// The synthetic caret and placeholder use source -1 and are never selected.
type composerGlyph struct {
	text   string
	source int
	width  int
}

type composerInsertion struct {
	column int
	caret  int
}

type composerTextRow struct {
	glyphs  []composerGlyph
	points  []composerInsertion
	columns int
}

type composerDraftLayout struct {
	rows      []composerTextRow
	positions []composerInsertionPosition
}

type composerInsertionPosition = composerVisualPosition

// layoutComposerDraft follows wrap's escaping and soft-wrap rules, keeping
// insertion points alongside the display glyphs rather than reverse-mapping
// the rendered (and possibly styled) strings for mouse input.
func layoutComposerDraft(input []rune, width, caret int, showCaret bool) composerDraftLayout {
	width = max(1, width)
	caret = min(len(input), max(0, caret))
	layout := composerDraftLayout{
		rows:      []composerTextRow{{}},
		positions: make([]composerInsertionPosition, len(input)+1),
	}
	row := 0
	point := func(index, column int) {
		layout.rows[row].points = append(layout.rows[row].points, composerInsertion{column: column, caret: index})
	}
	flush := func() {
		layout.rows = append(layout.rows, composerTextRow{})
		row++
	}
	prepare := func(size, index int, boundary bool) {
		if layout.rows[row].columns+size > width && layout.rows[row].columns > 0 {
			if boundary {
				point(index, layout.rows[row].columns)
			}
			flush()
		}
	}
	appendGlyph := func(text string, source, size int) {
		line := &layout.rows[row]
		line.glyphs = append(line.glyphs, composerGlyph{text: text, source: source, width: size})
		line.columns += size
	}
	point(0, 0)
	var caretPosition composerInsertionPosition
	for i := 0; i <= len(input); i++ {
		if showCaret && i == caret {
			prepare(1, i, true)
			caretPosition = composerInsertionPosition{row: row, column: layout.rows[row].columns}
			point(i, layout.rows[row].columns)
			appendGlyph("█", -1, 1)
			point(i, layout.rows[row].columns)
		}
		if i == len(input) {
			layout.positions[i] = composerInsertionPosition{row: row, column: layout.rows[row].columns}
			point(i, layout.rows[row].columns)
			break
		}
		r := input[i]
		if r == '\n' {
			layout.positions[i] = composerInsertionPosition{row: row, column: layout.rows[row].columns}
			point(i, layout.rows[row].columns)
			flush()
			point(i+1, 0)
			continue
		}
		if hiddenReviewRune(r) {
			escaped := fmt.Sprintf("\\u%04X", r)
			prepare(1, i, true)
			start := composerInsertionPosition{row: row, column: layout.rows[row].columns}
			layout.positions[i] = start
			for _, c := range escaped {
				prepare(1, i, false)
				appendGlyph(string(c), i, 1)
			}
			// A row in the middle of an escape has no insertion point inside
			// the source rune. Virtual endpoints outside that row let a click
			// still choose the nearer side of the complete escaped rune.
			for part := start.row; part <= row; part++ {
				column := start.column - (part-start.row)*width
				layout.rows[part].points = append(layout.rows[part].points,
					composerInsertion{column: column, caret: i},
					composerInsertion{column: column + len(escaped), caret: i + 1})
			}
			continue
		}
		size := runewidth.RuneWidth(r)
		prepare(size, i, true)
		layout.positions[i] = composerInsertionPosition{row: row, column: layout.rows[row].columns}
		point(i, layout.rows[row].columns)
		appendGlyph(string(r), i, size)
		point(i+1, layout.rows[row].columns)
	}
	if showCaret {
		layout.positions[caret] = caretPosition
	}
	return layout
}

type composerViewport struct {
	style             string
	fixed, width      int
	left, textOffset  int
	hidden, popupRows int
	rows              []composerTextRow
	placeholder       bool
}

// composerTextViewport is shared by rendering and hit-testing. In particular,
// a selection (including a newly started, empty drag) never inserts a blinking
// display cell that could change the soft wraps underneath the mouse.
func (m *ui) composerTextViewport() composerViewport {
	if view, frozen := m.frozenComposerViewport(); frozen {
		return view
	}
	style, fixed, width := m.composerLayout()
	view := composerViewport{style: style, fixed: fixed, width: width, textOffset: 1, placeholder: len(m.input) == 0}
	switch style {
	case "rounded":
		view.left, view.textOffset = 2+runewidth.StringWidth(composerPrompt), 2
	case "bordered":
		view.left, view.textOffset = 2, 2
	case "minimal":
		view.textOffset = 2
	case "chatter":
		view.left = runewidth.StringWidth("You › ")
	}
	input := m.input
	caret := min(len(input), max(0, m.edit.caret))
	if view.placeholder {
		text := "Ask Likha… // escapes a slash"
		if m.working {
			text = "Type to queue… (Enter queues, Esc cancels)"
		} else if n := len(m.queue); n > 0 {
			text = "Enter sends the queued message… (Esc clears)"
			if n > 1 {
				text = "Enter sends the queued messages… (Esc clears)"
			}
		}
		input = []rune(text)
	}
	layout := layoutComposerDraft(input, width, caret, m.caretVisible() && !m.edit.selecting)
	if view.placeholder {
		for i := range layout.rows {
			for j := range layout.rows[i].glyphs {
				layout.rows[i].glyphs[j].source = -1
			}
			for j := range layout.rows[i].points {
				layout.rows[i].points[j].caret = 0
			}
		}
	}
	view.popupRows = len(m.mentionLines()) + len(m.commandLines())
	limit := max(1, m.height-len(m.header())-m.statusLineHeight()-view.popupRows-1-fixed)
	tail := max(0, len(layout.rows)-limit)
	view.hidden = tail
	if !view.placeholder && m.edit.selecting {
		caretRow := layout.positions[caret].row
		anchor := min(len(m.input), max(0, m.edit.selectionAnchor))
		anchorRow := layout.positions[anchor].row
		if anchorRow < tail {
			view.hidden = max(0, anchorRow-limit/2)
		}
		// Keep the window stable around the drag's anchor until the active
		// end leaves it; keyboard selection can then reach either hidden end.
		if caretRow < view.hidden {
			view.hidden = caretRow
		} else if caretRow >= view.hidden+limit {
			view.hidden = caretRow - limit + 1
		}
		view.hidden = min(tail, view.hidden)
	}
	view.rows = layout.rows[view.hidden:min(len(layout.rows), view.hidden+limit)]
	return view
}

// composerSelectionCaretVertical moves through the marker-free rows actually
// shown during keyboard selection. Reusing the mover retains the preferred
// display column across short rows without its normal editor's marker geometry.
func (m *ui) composerSelectionCaretVertical(caret, direction int) (int, bool) {
	layout := layoutComposerDraft(m.input, m.composerInputWidth(), caret, false)
	return m.composerVertical.moveVisual(m.input, caret, direction, layout.positions)
}

func renderComposerTextRow(row composerTextRow, width int, normal, selected lipgloss.Style, start, end int, selecting bool) string {
	var out, run strings.Builder
	columns := 0
	active := false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		style := normal
		if active {
			style = selected
		}
		out.WriteString(style.Render(run.String()))
		run.Reset()
	}
	for _, glyph := range row.glyphs {
		if columns+glyph.width > width {
			break
		}
		next := selecting && glyph.source >= start && glyph.source < end
		if next != active {
			flush()
			active = next
		}
		run.WriteString(glyph.text)
		columns += glyph.width
	}
	if active {
		flush()
		active = false
	}
	run.WriteString(strings.Repeat(" ", max(0, width-columns)))
	flush()
	return out.String()
}

// composerInputVisible mirrors the main-view guards; review bars and overlays
// must not expose the hidden draft to mouse selection.
func (m *ui) composerInputVisible() bool {
	return m.width >= minWidth && m.height >= minHeight && m.mode != modeSetup && m.pending == nil &&
		!m.keyModal.open && !m.consentVisible() && !m.askVisible() && !m.dialog.open &&
		!m.agentInspectionVisible() && !m.toolInspectionVisible()
}

func (m *ui) composerViewportBounds(view composerViewport) (left, top, width, rows int) {
	header := len(m.header())
	body := max(1, m.height-header-len(view.rows)-view.fixed-view.popupRows-m.statusLineHeight())
	return view.left, header + body + view.popupRows + view.textOffset, view.width, len(view.rows)
}

// composerTextBounds returns the screen-coordinate text area, excluding the
// separator, border, prompt/prefix, and status bar. Rows count only visible
// wrapped input rows (or placeholder rows for the empty draft).
func (m *ui) composerTextBounds() (left, top, width, rows int) {
	if !m.composerInputVisible() {
		return 0, 0, 0, 0
	}
	return m.composerViewportBounds(m.composerTextViewport())
}

// composerCaretAt chooses the nearest source insertion point on a visible
// input row. Horizontal coordinates may extend beyond the input area during
// a drag; vertical coordinates outside its visible rows are not input hits.
func (m *ui) composerCaretAt(x, y int) (int, bool) {
	if !m.composerInputVisible() {
		return 0, false
	}
	view := m.composerTextViewport()
	left, top, width, rows := m.composerViewportBounds(view)
	row := y - top
	if row < 0 || row >= rows {
		return 0, false
	}
	column := min(width, max(0, x-left))
	best, distance, bestColumn := 0, int(^uint(0)>>1), int(^uint(0)>>1)
	for _, point := range view.rows[row].points {
		delta := point.column - column
		if delta < 0 {
			delta = -delta
		}
		if delta < distance || (delta == distance && point.column < bestColumn) {
			best, distance, bestColumn = point.caret, delta, point.column
		}
	}
	return best, true
}

// composerLines reserves its own viewport rows; a long draft shows its editable
// tail, or the active selection end, without displacing the other screen rows.
func (m *ui) composerLines() []string {
	if m.pending != nil {
		return m.reviewActionLines()
	}
	width := m.width
	view := m.composerTextViewport()
	style, fixed, inputWidth := view.style, view.fixed, view.width
	start, end, selecting := m.edit.selectionRange(m.input)
	normal := withBase(m.theme.Normal, m.theme.Base)
	if view.placeholder {
		normal = withBase(m.theme.Muted, m.theme.Base)
	}
	selected := textSelectionStyle(m.theme)
	rows := make([]string, 0, len(view.rows)+fixed)
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
	for i, line := range view.rows {
		content := renderComposerTextRow(line, inputWidth, normal, selected, start, end, selecting)
		switch style {
		case "bordered":
			border := withBase(m.theme.Border, m.theme.Base)
			rows = append(rows, border.Render(vertical+" ")+content+border.Render(" "+vertical))
		case "rounded":
			// The prompt glyph marks the draft's first row; wrapped rows
			// continue under the text column, inside the box.
			prompt := strings.Repeat(" ", runewidth.StringWidth(composerPrompt))
			if i == 0 && view.hidden == 0 {
				prompt = composerPrompt
			}
			border := withBase(m.theme.Border, m.theme.Base)
			rows = append(rows, border.Render(vertical+" ")+withBase(m.theme.Normal, m.theme.Base).Render(prompt)+content+border.Render(" "+vertical))
		case "chatter":
			prefix := "      "
			if i == 0 {
				prefix = "You › "
			}
			rows = append(rows, withBase(m.theme.Selected, m.theme.Base).Render(prefix)+content)
		default:
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

// reviewButton is one decision-bar action. Gated actions (Approve and the
// remember button) stay muted and unconfirmable until the review is read.
type reviewButton struct {
	focus         int
	label, narrow string
	gated         bool
}

// reviewButtons lists the decision bar left to right: Approve, the optional
// remember action the request offers (specs/command-permissions), Decline.
func (m *ui) reviewButtons() []reviewButton {
	buttons := []reviewButton{{focus: focusApprove, label: "Approve", narrow: "Approve", gated: true}}
	if m.pending != nil {
		switch m.pending.Remember {
		case agent.RememberSession, agent.RememberEdits, agent.RememberServer:
			buttons = append(buttons, reviewButton{focus: focusRemember, label: "Approve always", narrow: "Always", gated: true})
		case agent.RememberTrust:
			buttons = append(buttons, reviewButton{focus: focusRemember, label: "Trust repo checks", narrow: "Trust", gated: true})
		}
	}
	return append(buttons, reviewButton{focus: focusDecline, label: "Decline", narrow: "Decline"})
}

// reviewBarWidth is the wide decision bar's width: each button is a
// two-column marker slot plus "[ label ]", with a two-space gap between.
func reviewBarWidth(buttons []reviewButton) int {
	width := 0
	for i, button := range buttons {
		if i > 0 {
			width += 2
		}
		width += 2 + runewidth.StringWidth("[ "+button.label+" ]")
	}
	return width
}

// moveReviewFocus steps the focus across the visible buttons, stopping at
// either end.
func (m *ui) moveReviewFocus(step int) {
	buttons := m.reviewButtons()
	index := 0
	for i, button := range buttons {
		if button.focus == m.reviewFocus {
			index = i
		}
	}
	index = max(0, min(len(buttons)-1, index+step))
	m.reviewFocus = buttons[index].focus
}

// reviewActionLines renders the pinned decision bar that replaces the
// composer while a review is pending (FR-22): labelled buttons with exactly
// one focus marker, then the key hint. It reuses the composer's reserved
// rows, so the layout budget is unchanged. Approve (and a remember button)
// stays muted and cannot be confirmed until every review page has been
// seen; Decline is always available.
func (m *ui) reviewActionLines() []string {
	ready := m.reviewReady()
	buttons := m.reviewButtons()
	// Three buttons use the compact bracket form when the wide row does not
	// fit with three columns spare: below 53 columns with Approve always
	// (50 cells), below 56 with Trust repo checks (53 cells). The compact
	// form fits the 40-column minimum.
	compact := len(buttons) > 2 && m.width < reviewBarWidth(buttons)+3
	var row strings.Builder
	for i, button := range buttons {
		if i > 0 {
			row.WriteString(m.theme.Base.Render("  "))
		}
		label := "[ " + button.label + " ]"
		if compact {
			label = "[" + button.narrow + "]"
		}
		style, marker := m.theme.Muted, "  "
		if button.focus == m.reviewFocus {
			marker = "> "
			if !button.gated || ready {
				style = m.theme.Selected
			}
		}
		row.WriteString(withBase(style, m.theme.Base).Render(marker + label))
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
	bar := row.String()
	if w := runewidth.StringWidth(stripANSI(bar)); w < m.width {
		bar += m.theme.Base.Render(strings.Repeat(" ", m.width-w))
	}
	return []string{bar, withBase(m.theme.Muted, m.theme.Base).Render(fit(hint, m.width))}
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
