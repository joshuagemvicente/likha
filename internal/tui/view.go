package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	likhaui "likha/internal/ui"
)

// entry is one transcript row: assistant prose, tool activity, or an error.
type entry struct{ role, content string }

func (m *ui) markReviewPage() {
	m.markSeenFromScroll()
}

// header is the chrome above the transcript (spec tui-layout 1a): empty at
// 56 columns and up — the ASCII logo on page 1 plus the status bar's Folder
// and Branch segments carry identity — and a single compact line on the
// narrowest terminals, where the logo is skipped. Every len(m.header())
// consumer (bodyHeight, composer limit, popup budgets, rebuild fallbacks)
// already tolerates the zero-line form.
func (m *ui) header() []string {
	if m.width >= 56 {
		return nil
	}
	return []string{"Likha · " + filepath.Base(m.root)}
}

func (m *ui) bodyHeight() int {
	return max(1, m.height-len(m.header())-len(m.composerLines())-max(len(m.mentionLines()), len(m.commandLines()))-m.statusLineHeight())
}

func (m *ui) pageCount() int {
	m.rebuild()
	body := m.bodyHeight()
	if body <= 0 {
		return 1
	}
	return max(1, (len(m.lines)+body-1)/body)
}

// rebuild lays out content only when the viewport or content changes. The
// screen is never scrolled; each frame selects a discrete page of these lines.
// lineStyles mirrors m.lines so body lines can carry entry-level styling
// (reasoning output renders muted).
func (m *ui) rebuild() {
	if m.layoutWidth == m.width && m.lines != nil {
		return
	}
	plain := lipgloss.NewStyle()
	m.lines = m.lines[:0]
	m.lineStyles = m.lineStyles[:0]
	m.lineSpans = m.lineSpans[:0]
	m.lineActivity = m.lineActivity[:0]
	m.lineRuns = m.lineRuns[:0]
	m.entryLines = nil
	// add appends display lines, the style each one renders with, and the
	// inline color spans painted over it at render time. Logo art and the
	// review proposal body skip detection: spans there would paint ASCII
	// art and diff content, not model-emitted literals.
	add := func(lines []string, style lipgloss.Style) {
		for _, line := range lines {
			m.lines = append(m.lines, line)
			m.lineStyles = append(m.lineStyles, style)
			m.lineSpans = append(m.lineSpans, nil)
			m.lineActivity = append(m.lineActivity, false)
			m.lineRuns = append(m.lineRuns, nil)
		}
	}
	addSwatches := func(lines []string, style lipgloss.Style) {
		for _, line := range lines {
			m.lines = append(m.lines, line)
			m.lineStyles = append(m.lineStyles, style)
			m.lineSpans = append(m.lineSpans, likhaui.FindSwatches(line))
			m.lineActivity = append(m.lineActivity, false)
			m.lineRuns = append(m.lineRuns, nil)
		}
	}
	// Tool rows carry styled runs (status dot, bold name) instead of
	// swatches: color literals in tool output are data, not prose.
	// Assistant markdown rows use the same path; their prose swatches are
	// already merged into the runs (markdownToRows).
	addTool := func(rows []toolRow) {
		for _, row := range rows {
			m.lines = append(m.lines, row.text)
			m.lineStyles = append(m.lineStyles, row.style)
			m.lineSpans = append(m.lineSpans, nil)
			m.lineActivity = append(m.lineActivity, false)
			m.lineRuns = append(m.lineRuns, row.runs)
		}
	}
	width := contentWidth(m.width)
	if m.pending != nil {
		add(wrap(m.pending.Title, width), plain)
		if m.pending.Kind == "command" {
			add([]string{"WARNING: No filesystem/network sandbox"}, m.theme.Warning)
			add([]string{"WARNING: Detached jobs may survive"}, m.theme.Warning)
		}
		add([]string{""}, plain)
		add(wrap(m.pending.Body, width), plain)
	} else {
		// Narrow terminals keep the compact header line and re-wrap its
		// overflow into the body; at 56 columns and up there is nothing
		// header-shaped to wrap.
		for _, line := range m.header() {
			if runewidth.StringWidth(line) > m.width {
				add(wrap(line, width), plain)
			}
		}
		first := false
		m.entryLines = make([]int, len(m.entries))
		md := m.newMarkdownLayout()
		plan := m.toolItemPlan()
		focused, hasFocus := m.focusedToolItem(plan)
		// Lay assistant answers out newest first, so the highlight budget
		// goes to the answers nearest the screen; the loop below then finds
		// every one of them in this layout's cache.
		for index := len(m.entries) - 1; index >= 0; index-- {
			if e := m.entries[index]; e.role == "Assistant" && !plan.absorbed[index] {
				glyph, _, _ := m.transcriptBlock(e)
				m.assistantRows(md, e.content, glyph, width, index == m.streaming)
			}
		}
		for index, e := range m.entries {
			if plan.absorbed[index] {
				// A legacy result renders inside its request's item.
				m.entryLines[index] = m.entryLines[plan.owner[index]]
				continue
			}
			if e.role == "Logo" {
				// Startup block: raw pre-formatted logo lines, never
				// re-wrapped. The block is fixed-width ASCII, but a line wider
				// than the viewport would still push its tail off-screen (raw
				// lines skip fit()), so over-wide lines hard-wrap to content
				// width. A no-op for the current logo; it keeps the one raw
				// render path provably safe if the art is ever retraced.
				m.entryLines[index] = len(m.lines)
				if m.width < 56 {
					continue
				}
				if first {
					add([]string{""}, plain)
				}
				first = true
				m.entryLines[index] = len(m.lines)
				for _, line := range strings.Split(e.content, "\n") {
					if runewidth.StringWidth(line) <= width {
						add([]string{line}, withBase(m.theme.Title, m.theme.Base))
						continue
					}
					add(wrap(line, width), withBase(m.theme.Title, m.theme.Base))
				}
				continue
			}
			if first {
				add([]string{""}, withBase(plain, m.theme.Base))
			}
			first = true
			m.entryLines[index] = len(m.lines)
			if e.role == "Working" {
				plainText := activitySpinner(m.activityFrame) + " " + workingLabel
				for i, line := range wrap(plainText, width) {
					m.lines = append(m.lines, line)
					m.lineStyles = append(m.lineStyles, m.theme.Muted)
					m.lineSpans = append(m.lineSpans, nil)
					m.lineActivity = append(m.lineActivity, i == 0)
					m.lineRuns = append(m.lineRuns, nil)
				}
				continue
			}
			if e.role == "Tool" {
				addTool(m.toolItemRows(index, plan, hasFocus && focused == index, width))
				continue
			}
			// Glyph-language blocks (spec transcript-redesign § Glyph
			// language): a glyph in a two-cell gutter carries the block kind,
			// prose word-wraps under the text column, and only user prompts
			// sit on a band, padded by one band row above and below.
			glyph, style, band := m.transcriptBlock(e)
			if e.role == "Assistant" {
				// Assistant text, streaming included, renders as markdown
				// (spec transcript-redesign § Assistant text).
				addTool(m.assistantRows(md, e.content, glyph, width, index == m.streaming))
				continue
			}
			content := e.content
			if e.role == "Queued" {
				// The user's words, not yet sent: the word says so without
				// relying on the muted color.
				content = "queued · " + content
			}
			if band {
				add([]string{""}, style)
			}
			// Inline swatches: color literals paint their own background
			// (with a contrast-picked foreground) over whatever band would
			// apply, so "#4493f8" previews the color in place. Spans are
			// detected per wrapped line, so no swatch ever crosses a line
			// boundary: widths pre- and post-swatch are identical because the
			// decoration is visual only — no text is added, removed, or
			// reordered, only SGR spans wrap existing cells.
			gutter := glyph + " "
			indent := runewidth.StringWidth(gutter)
			rows := wrapHanging(content, width, indent, indent)
			rows[0] = gutter + rows[0]
			addSwatches(rows, style)
			if band {
				add([]string{""}, style)
			}
		}
		// Keep only this layout's rendered entries and code highlights:
		// edits, theme changes, and resizes leave nothing stale behind.
		m.commitMarkdownLayout(md)
		m.layoutWidth = m.width
	}
}

// transcriptBlock maps an entry to its block in the glyph language (spec
// transcript-redesign § Glyph language, § Backgrounds): the gutter glyph,
// the style every row of the block renders with, and whether the block sits
// on the user band. Meaning rides on the glyph, never on color alone; only
// user prompts carry a band, and everything else sits on the base canvas.
// Tool entries lay out as tool items instead (toolItemRows).
func (m *ui) transcriptBlock(e entry) (glyph string, style lipgloss.Style, band bool) {
	g := m.blocks
	switch e.role {
	case "You":
		return g.User, withBase(m.theme.Normal, m.theme.BgUser), true
	case "Queued":
		// Queued prompts keep the user band but read muted: they are the
		// user's words, not yet sent.
		return g.User, withBase(m.theme.Muted, m.theme.BgUser), true
	case "Assistant":
		return g.Assistant, withBase(m.theme.Normal, m.theme.Base), false
	case "Reasoning":
		// The quietest layer: foreground-muted only, no band.
		return g.Thought, m.theme.Muted, false
	case "Error":
		return g.Error, withBase(m.theme.Error, m.theme.Base), false
	default:
		// Likha notices, legacy Agent lines, and any future prose role read
		// as muted notices on the canvas.
		return g.Notice, withBase(m.theme.Muted, m.theme.Base), false
	}
}

func (m *ui) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return m.frame([]string{"Likha: enlarge terminal to at least 40 columns by 12 rows"})
	}
	if m.mode == modeSetup {
		return m.setupView()
	}
	if m.keyModal.open {
		return m.keyModalView()
	}
	if m.consentVisible() {
		return m.consentView()
	}
	if m.askVisible() {
		return m.askView()
	}
	if m.dialog.open {
		return m.dialogView()
	}
	if m.agentInspectionVisible() {
		return m.agentInspectionView()
	}
	if m.toolInspectionVisible() {
		return m.toolInspectionView()
	}
	return m.mainView()
}

// mainView renders the conversation: header, scrolled body with a scrollbar,
// composer, status line. The viewport is bodyHeight() lines of m.lines
// starting at m.scroll; the newest content follows while the viewport sits at
// the bottom.
func (m *ui) mainView() string {
	m.rebuild()
	header := m.header()
	composer := m.composerLines()
	mention := m.mentionLines()
	command := m.commandLines()
	body := max(1, m.height-len(header)-len(composer)-len(mention)-len(command)-m.statusLineHeight())
	m.clampScroll()
	pages := max(1, (len(m.lines)+body-1)/body)
	// The indicator names the page containing the viewport's bottom edge, so
	// the pinned (bottom) view always reads as the last page.
	page := (m.scroll + body - 1) / body
	page = max(0, min(pages-1, page))
	rows := make([]string, 0, m.height)
	for i, line := range header {
		if i == 0 {
			rows = append(rows, withBase(m.theme.Title, m.theme.Base).Render(fit(line, m.width)))
		} else {
			rows = append(rows, m.theme.Base.Render(fit(line, m.width)))
		}
	}
	start := m.scroll
	blinkOff := m.toolBlinkOff()
	for i := range body {
		line := ""
		style := m.theme.Base
		var spans []likhaui.Swatch
		var runs []lineRun
		if start+i < len(m.lines) {
			line = m.lines[start+i]
			if start+i < len(m.lineStyles) {
				style = withBase(m.lineStyles[start+i], m.theme.Base)
			}
			if start+i < len(m.lineSpans) {
				spans = m.lineSpans[start+i]
			}
			if start+i < len(m.lineRuns) {
				runs = m.lineRuns[start+i]
			}
		}
		// Style the padded line: foreground on padding spaces is invisible,
		// while the band background paints the full row; spans repaint only
		// the literal's own cells on top. lineSpans rides parallel to lines
		// (same indices, cleared with them in rebuild) so stale swatches
		// can never paint a reused row.
		fitted := fit(line, m.width)
		if start+i < len(m.lineActivity) && m.lineActivity[start+i] {
			muted := withBase(m.theme.Muted, m.theme.Base)
			accent := withBase(m.theme.Title, m.theme.Base)
			rows = append(rows, renderActivityRow(fitted, line, m.activityFrame, muted, accent))
			continue
		}
		if len(runs) > 0 {
			rows = append(rows, renderRuns(style, runs, fitted, blinkOff))
			continue
		}
		rows = append(rows, renderSwatches(style, spans, fitted))
	}
	m.spliceScrollbar(rows, len(header), body)
	rows = append(rows, mention...)
	rows = append(rows, command...)
	rows = append(rows, composer...)
	for _, row := range m.statusLineRows(page+1, pages) {
		rows = append(rows, m.renderStatusCanvas(row))
	}
	return strings.Join(rows, "\n")
}

func activitySpinner(frame int) string {
	return workingSpinnerFrames[frame%len(workingSpinnerFrames)]
}

// renderActivityRow applies the muted base and one moving accent cell without
// changing visible text or cell widths. Spinner and sweep share the frame.
func renderActivityRow(fitted, plainLine string, frame int, muted, accent lipgloss.Style) string {
	runes := []rune(fitted)
	plainCount := len([]rune(plainLine))
	labelCount := len([]rune(workingLabel))
	if len(runes) == 0 || labelCount == 0 {
		return muted.Render(fitted)
	}
	head := frame % labelCount
	if head < 0 {
		head += labelCount
	}
	headIdx := 2 + head // spinner and space precede the label
	styleAt := func(i int) bool {
		return i < plainCount && (i == 0 || i == headIdx)
	}
	var out strings.Builder
	start := 0
	current := styleAt(0)
	flush := func(end int, useAccent bool) {
		if end <= start {
			return
		}
		style := muted
		if useAccent {
			style = accent
		}
		out.WriteString(style.Render(string(runes[start:end])))
		start = end
	}
	for i := 1; i < len(runes); i++ {
		if next := styleAt(i); next != current {
			flush(i, current)
			current = next
		}
	}
	flush(len(runes), current)
	return out.String()
}

// renderSwatches styles a fitted row with the band style, then repaints each
// detected color span on its own swatch background. Spans are rune offsets
// into the pre-fit line; fit only appends padding (never reorders), so they
// address the padded row unchanged. Zero spans render the band style alone.
// The swatch shows the literal's own runes (decoration only), so padding
// and wrapping are unaffected.
func renderSwatches(band lipgloss.Style, spans []likhaui.Swatch, fitted string) string {
	if len(spans) == 0 {
		return band.Render(fitted)
	}
	runes := []rune(fitted)
	var out strings.Builder
	pos := 0
	emit := func(end int, style lipgloss.Style) {
		if end > pos {
			out.WriteString(style.Render(string(runes[pos:end])))
			pos = end
		}
	}
	for _, sp := range spans {
		if sp.Start < pos || sp.Start >= sp.End || sp.End > len(runes) {
			continue
		}
		emit(sp.Start, band)
		out.WriteString(likhaui.SwatchStyle(sp.Hex).Render(string(runes[sp.Start:sp.End])))
		pos = sp.End
	}
	emit(len(runes), band)
	return out.String()
}

// withBase layers a role's foreground attributes over the theme canvas
// background: canvas roles (Base, BgUser, BgTool, BgModel) set bg only,
// foreground roles set fg/bold only, so the union paints the row's text in
// the role color on the intended background. A role that already carries
// its own band background keeps it; Base fills in only behind background-
// free roles. When the theme leaves the terminal default (default family),
// the role renders unchanged.
func withBase(role, base lipgloss.Style) lipgloss.Style {
	if bg := role.GetBackground(); bg != nil {
		if _, isNoColor := bg.(lipgloss.NoColor); !isNoColor {
			return role
		}
	}
	if bg := base.GetBackground(); bg != nil {
		if _, isNoColor := bg.(lipgloss.NoColor); !isNoColor {
			return role.Background(bg)
		}
	}
	return role
}

// dimRow wraps one rendered row in ANSI faint, re-applying the attribute after
// every embedded reset so the dim survives nested color spans. Callers pass
// the canvas bg so the dimmed surround keeps the theme background: the bg
// opens first and is re-applied after each reset, exactly like PaintRow.
func dimRow(row string) string {
	return dimRowOn(row, "")
}

func dimRowOn(row, bg string) string {
	open := ""
	if r, g, b, ok := likhaui.ParseHex(bg); ok {
		open = "\x1b[48;2;" + likhaui.Itoa(r) + ";" + likhaui.Itoa(g) + ";" + likhaui.Itoa(b) + "m"
		// Empty padding needs no dim: paint the canvas bg alone so blank
		// surround rows keep the background without a faint span.
		if strings.TrimSpace(stripANSI(row)) == "" {
			return open + row + "\x1b[0m"
		}
	}
	dimmed := strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m\x1b[2m"+open)
	return "\x1b[2m" + open + strings.TrimSuffix(dimmed, "\x1b[2m"+open) + "\x1b[0m"
}

// spliceRow replaces the display-cell range [left, left+boxWidth) of a plain
// row with the given rendered box segment; the surrounding text stays dimmed
// like the rest of the background. Callers pass the already-rendered row and
// the canvas bg: the surround keeps the theme background (dimmed), the box
// renders at full intensity, and widths measure on the ANSI-stripped row.
func spliceRow(rendered string, left, boxWidth int, box string) string {
	return spliceRowOn(rendered, left, boxWidth, box, "")
}

func spliceRowOn(rendered string, left, boxWidth int, box, bg string) string {
	plain := stripANSI(rendered)
	l, rest := splitAtWidth(plain, left)
	_, right := splitAtWidth(rest, boxWidth)
	return dimRowOn(l, bg) + box + dimRowOn(right, bg)
}

// stripANSI removes SGR escape sequences, leaving the plain text of a
// rendered row. Styles never alter visible widths, so the result stays
// width-exact.
func stripANSI(s string) string {
	var b strings.Builder
	escaping := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			escaping = true
		case escaping:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\\' {
				escaping = false // final byte of the sequence
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitAtWidth splits plain text at a display-column boundary without
// cutting a wide rune.
func splitAtWidth(s string, col int) (left, right string) {
	if col <= 0 {
		return "", s
	}
	width := 0
	for i, r := range s {
		if width >= col {
			return s[:i], s[i:]
		}
		width += runewidth.RuneWidth(r)
	}
	return s, ""
}

func (m *ui) frame(content []string) string {
	rows := make([]string, m.height)
	for i := range rows {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		// The canvas background paints every row, including filler: with no
		// content the Base style emits the bg fill alone.
		rows[i] = m.theme.Base.Render(fit(line, m.width))
	}
	return strings.Join(rows, "\n")
}

// wrap makes untrusted control and zero-width characters visible before
// breaking text into display-width-bounded lines.
func wrap(text string, width int) []string {
	var lines []string
	var line strings.Builder
	columns := 0
	flush := func() {
		lines = append(lines, line.String())
		line.Reset()
		columns = 0
	}
	for _, r := range text {
		if r == '\n' {
			flush()
			continue
		}
		if hiddenReviewRune(r) {
			escaped := fmt.Sprintf("\\u%04X", r)
			for _, c := range escaped {
				if columns+1 > width && columns > 0 {
					flush()
				}
				line.WriteRune(c)
				columns++
			}
			continue
		}
		size := runewidth.RuneWidth(r)
		if columns+size > width && columns > 0 {
			flush()
		}
		line.WriteRune(r)
		columns += size
	}
	flush()
	return lines
}

func hiddenReviewRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || runewidth.RuneWidth(r) == 0
}

func fit(text string, width int) string {
	var b strings.Builder
	columns := 0
	for _, r := range text {
		if hiddenReviewRune(r) {
			for _, c := range fmt.Sprintf("\\u%04X", r) {
				if columns+1 > width {
					break
				}
				b.WriteRune(c)
				columns++
			}
			continue
		}
		size := runewidth.RuneWidth(r)
		if columns+size > width {
			break
		}
		b.WriteRune(r)
		columns += size
	}
	if columns < width {
		b.WriteString(strings.Repeat(" ", width-columns))
	}
	return b.String()
}
