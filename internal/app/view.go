package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	lisaui "lisa/internal/ui"
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
	return []string{"Lisa · " + filepath.Base(m.root)}
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
	// add appends display lines and the style each one renders with.
	add := func(lines []string, style lipgloss.Style) {
		for _, line := range lines {
			m.lines = append(m.lines, line)
			m.lineStyles = append(m.lineStyles, style)
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
		for _, e := range m.entries {
			if e.role == "Logo" {
				// Startup block: raw pre-formatted logo lines, never
				// re-wrapped. The block is fixed-width ASCII, but a line wider
				// than the viewport would still push its tail off-screen (raw
				// lines skip fit()), so over-wide lines hard-wrap to content
				// width. A no-op for the current logo; it keeps the one raw
				// render path provably safe if the art is ever retraced.
				if m.width < 56 {
					continue
				}
				if first {
					add([]string{""}, plain)
				}
				first = true
				for _, line := range strings.Split(e.content, "\n") {
					if runewidth.StringWidth(line) <= width {
						add([]string{line}, m.theme.Title)
						continue
					}
					add(wrap(line, width), m.theme.Title)
				}
				continue
			}
			if first {
				add([]string{""}, plain)
			}
			first = true
			style := plain
			switch {
			case e.role == "Reasoning" || e.role == "Tool":
				// Reasoning output (FR-15) and tool activity render muted:
				// machinery, not assistant content.
				style = m.theme.Muted
			case e.role == "Error":
				// Conversation prose stays uncolored except error entries
				// (themes spec); failures are the one colored role.
				style = m.theme.Error
			}
			add(wrap(e.role+": "+e.content, width), style)
		}
	}
	m.layoutWidth = m.width
}

func (m *ui) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return m.frame([]string{"Lisa: enlarge terminal to at least 40 columns by 12 rows"})
	}
	if m.mode == modeSetup {
		return m.setupView()
	}
	if m.keyModal.open {
		return m.keyModalView()
	}
	if m.dialog.open {
		return m.dialogView()
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
			rows = append(rows, m.theme.Title.Render(fit(line, m.width)))
		} else {
			rows = append(rows, fit(line, m.width))
		}
	}
	start := m.scroll
	for i := range body {
		line := ""
		style := m.theme.Base
		if start+i < len(m.lines) {
			line = m.lines[start+i]
			if start+i < len(m.lineStyles) {
				style = withBase(m.lineStyles[start+i], m.theme.Base)
			}
		}
		// Style the padded line: foreground on padding spaces is invisible,
		// while the Base background paints the full row. Roles never carry
		// their own background, so Base is the only SGR 48 emitter here.
		rows = append(rows, style.Render(fit(line, m.width)))
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

// withBase layers a role's foreground attributes over the theme canvas
// background: roles set fg/bold only, Base sets bg only, so the union paints
// the row's text in the role color on the theme background. When the theme
// leaves the terminal default (default family), the role renders unchanged.
func withBase(role, base lipgloss.Style) lipgloss.Style {
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
	if r, g, b, ok := lisaui.ParseHex(bg); ok {
		open = "\x1b[48;2;" + lisaui.Itoa(r) + ";" + lisaui.Itoa(g) + ";" + lisaui.Itoa(b) + "m"
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
