package tui

import (
	"reflect"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"

	"likha/internal/markdown"
	"likha/internal/transcript"
	likhaui "likha/internal/ui"
)

type textSelectionSurface uint8

const (
	selectionNone textSelectionSurface = iota
	selectionTranscript
	selectionComposer
)

// Points address rune boundaries in laid-out rows, never terminal padding.
type textSelectionPoint struct{ row, rune int }

type textSelectionState struct {
	surface           textSelectionSurface
	dragging          bool
	scrollbarDragging bool
	anchor, caret     textSelectionPoint
	snapshot          *textSelectionSnapshot
	composerGesture   *textSelectionComposerGesture
}

type textSelectionComposerGesture struct {
	viewport      composerViewport
	width, height int
	body          int
	header        []string
	mention       []string
	command       []string
}

// A selection owns the displayed document, not the entries that the agent is
// still updating. In particular, nested spans and shared run styles must not
// alias the next layout. The body geometry and animation phase stay fixed too.
type textSelectionSnapshot struct {
	lines         []string
	lineStyles    []lipgloss.Style
	lineSpans     [][]likhaui.Swatch
	lineActivity  []bool
	lineRuns      [][]lineRun
	plain         []string
	width, height int
	body          int
	activityFrame int
	blinkOff      bool
	header        []string
	composer      []string
	mention       []string
	command       []string
	composerView  composerViewport
}

// mainScreenVisible also permits the main-screen approval bar (scrolling a
// review is useful), whereas selectionSurfaceVisible excludes that form.
func (m *ui) mainScreenVisible() bool {
	return m.mode == modeMain && m.width >= minWidth && m.height >= minHeight &&
		!m.keyModal.open && !m.dialog.open && !m.askVisible() && !m.consentVisible() &&
		!m.toolInspectionVisible() && !m.agentInspectionVisible()
}

func (m *ui) selectionSurfaceVisible() bool {
	return m.mainScreenVisible() && m.pending == nil
}

func (m *ui) transcriptSelectionFrozen() bool {
	s := m.selection.snapshot
	return s != nil && m.selectionSurfaceVisible() && s.width == m.width && s.height == m.height
}

// frozenComposerViewport is the composer's shared render/hit-test contract.
// A gesture uses exactly the insertion points seen before its first press;
// an output snapshot retains the whole main frame's original input geometry.
func (m *ui) frozenComposerViewport() (composerViewport, bool) {
	if m.composerSelectionGestureFrozen() {
		return m.selection.composerGesture.viewport, true
	}
	if m.transcriptSelectionFrozen() {
		return m.selection.snapshot.composerView, true
	}
	return composerViewport{}, false
}

func (m *ui) composerSelectionGestureFrozen() bool {
	gesture := m.selection.composerGesture
	return gesture != nil && m.selection.dragging && m.selection.surface == selectionComposer &&
		m.selectionSurfaceVisible() && gesture.width == m.width && gesture.height == m.height
}

func (m *ui) captureComposerSelectionGesture() *textSelectionComposerGesture {
	gesture := &textSelectionComposerGesture{
		viewport: copyComposerViewport(m.composerTextViewport(), true), width: m.width, height: m.height, body: m.bodyHeight(),
	}
	var header, mention, command []string
	if m.transcriptSelectionFrozen() {
		s := m.selection.snapshot
		header, mention, command = s.header, s.mention, s.command
	} else if m.composerSelectionGestureFrozen() {
		old := m.selection.composerGesture
		header, mention, command = old.header, old.mention, old.command
	} else {
		header, mention, command = m.header(), m.mentionLines(), m.commandLines()
	}
	gesture.header, gesture.mention, gesture.command = append([]string(nil), header...), append([]string(nil), mention...), append([]string(nil), command...)
	return gesture
}

func copyComposerViewport(view composerViewport, hideMarker bool) composerViewport {
	view.rows = append([]composerTextRow(nil), view.rows...)
	for i := range view.rows {
		row := &view.rows[i]
		row.glyphs = append([]composerGlyph(nil), row.glyphs...)
		row.points = append([]composerInsertion(nil), row.points...)
		if hideMarker {
			for j := range row.glyphs {
				glyph := &row.glyphs[j]
				if glyph.source == -1 && glyph.text == "█" {
					glyph.text = strings.Repeat(" ", glyph.width)
				}
			}
		}
	}
	return view
}

// Keyboard input and mouse releases terminate a captured gesture without
// discarding its nonempty source range or output snapshot.
func (m *ui) endTextSelectionDrag() {
	m.selection.dragging = false
	m.selection.composerGesture = nil
	if m.selection.snapshot != nil && m.selection.anchor == m.selection.caret {
		m.clearTranscriptSelection() // an empty click cannot freeze indefinitely
	}
}

func (m *ui) clearTranscriptSelection() {
	if m.selection.snapshot != nil {
		// Even if ingestion did not invalidate the layout, it must be fresh
		// when releasing a snapshot (animation and highlight budgets included).
		m.layoutWidth = 0
	}
	m.selection.snapshot = nil
	m.selection.anchor, m.selection.caret = textSelectionPoint{}, textSelectionPoint{}
	if m.selection.surface == selectionTranscript {
		m.selection.surface = selectionNone
		m.selection.dragging = false
	}
}

func (m *ui) clearTextSelection() {
	m.clearTranscriptSelection()
	m.edit.clearSelection()
	m.selection = textSelectionState{}
}

func (m *ui) clearUnavailableTextSelection() {
	// An approval hides both text surfaces but still uses the main body's
	// scrollbar. Rendering it must not cancel that scrollbar's mouse capture.
	scrollbarDragging := m.mainScreenVisible() && m.selection.scrollbarDragging
	m.clearTextSelection()
	m.selection.scrollbarDragging = scrollbarDragging
}

func pointBefore(a, b textSelectionPoint) bool {
	return a.row < b.row || a.row == b.row && a.rune < b.rune
}

func (m *ui) transcriptSelectionRange() (start, end textSelectionPoint, ok bool) {
	if !m.transcriptSelectionFrozen() || m.selection.anchor == m.selection.caret {
		return start, end, false
	}
	start, end = m.selection.anchor, m.selection.caret
	if pointBefore(end, start) {
		start, end = end, start
	}
	return start, end, true
}

func (m *ui) textSelectionActive() bool {
	if !m.selectionSurfaceVisible() {
		return false
	}
	if _, _, ok := m.edit.selectionRange(m.input); ok {
		return true
	}
	_, _, ok := m.transcriptSelectionRange()
	return ok
}

func (m *ui) selectedText() string {
	if !m.selectionSurfaceVisible() {
		return ""
	}
	if _, _, ok := m.edit.selectionRange(m.input); ok {
		return m.edit.selectedText(m.input)
	}
	start, end, ok := m.transcriptSelectionRange()
	if !ok {
		return ""
	}
	var out strings.Builder
	for row := start.row; row <= end.row; row++ {
		text := []rune(m.selection.snapshot.plain[row])
		left, right := 0, len(text)
		if row == start.row {
			left = start.rune
		}
		if row == end.row {
			right = end.rune
		}
		if row > start.row {
			out.WriteByte('\n')
		}
		out.WriteString(string(text[left:right]))
	}
	return out.String()
}

// textSelectionStyle is deliberately independent of syntax, swatch, band,
// and faint styles. ANSI/default terminals can customize every palette slot,
// so reverse video on their own defaults is safer than guessed black/white.
func textSelectionStyle(theme likhaui.Theme) lipgloss.Style {
	if theme.Terminal16 || lipgloss.ColorProfile() == termenv.ANSI || !selectionColorSet(theme.Selected.GetForeground()) || !selectionColorSet(theme.Selected.GetBackground()) {
		return lipgloss.NewStyle().Reverse(true)
	}
	return lipgloss.NewStyle().Foreground(theme.Selected.GetForeground()).Background(theme.Selected.GetBackground())
}

func selectionColorSet(color lipgloss.TerminalColor) bool {
	if color == nil {
		return false
	}
	_, none := color.(lipgloss.NoColor)
	return !none
}

// Prefer current Action/Button fields. Type-only messages remain supported
// for older callers, but never override an explicitly supplied modern action.
func selectionMouseActionButton(v tea.MouseMsg) (tea.MouseAction, tea.MouseButton) {
	if v.Action != tea.MouseActionPress || v.Button != tea.MouseButtonNone {
		return v.Action, v.Button
	}
	switch v.Type {
	case tea.MouseLeft:
		return tea.MouseActionPress, tea.MouseButtonLeft
	case tea.MouseRelease:
		return tea.MouseActionRelease, tea.MouseButtonNone
	case tea.MouseMotion:
		return tea.MouseActionMotion, tea.MouseButtonNone
	case tea.MouseWheelUp:
		return tea.MouseActionPress, tea.MouseButtonWheelUp
	case tea.MouseWheelDown:
		return tea.MouseActionPress, tea.MouseButtonWheelDown
	case tea.MouseWheelLeft:
		return tea.MouseActionPress, tea.MouseButtonWheelLeft
	case tea.MouseWheelRight:
		return tea.MouseActionPress, tea.MouseButtonWheelRight
	case tea.MouseRight:
		return tea.MouseActionPress, tea.MouseButtonRight
	case tea.MouseMiddle:
		return tea.MouseActionPress, tea.MouseButtonMiddle
	}
	return v.Action, v.Button
}

func selectionMouseDrag(v tea.MouseMsg, action tea.MouseAction, button tea.MouseButton) bool {
	return action == tea.MouseActionMotion && (button == tea.MouseButtonLeft || button == tea.MouseButtonNone && v.Type == tea.MouseMotion)
}

// handleTextSelectionMouse never submits a draft or writes the clipboard.
// Wheel events belong to the scrolling handler even during a captured drag.
func (m *ui) handleTextSelectionMouse(v tea.MouseMsg) bool {
	if !m.selectionSurfaceVisible() {
		m.clearUnavailableTextSelection()
		return false
	}
	if m.selection.scrollbarDragging {
		return false
	}
	action, button := selectionMouseActionButton(v)
	if button >= tea.MouseButtonWheelUp && button <= tea.MouseButtonWheelRight {
		return false
	}
	if m.selection.dragging {
		if action == tea.MouseActionRelease && (button == tea.MouseButtonNone || button == tea.MouseButtonLeft) {
			m.endTextSelectionDrag()
			if !m.textSelectionActive() {
				m.clearTextSelection()
			}
			return true
		}
		if selectionMouseDrag(v, action, button) {
			m.extendTextSelection(v.X, v.Y)
			return true
		}
		// Hover-only all-motion reports cannot keep a lost release captured.
		if action == tea.MouseActionMotion && button == tea.MouseButtonNone {
			m.endTextSelectionDrag()
			if !m.textSelectionActive() {
				m.clearTextSelection()
			}
		}
	}
	if action != tea.MouseActionPress || button != tea.MouseButtonLeft {
		return false
	}
	m.rebuild()
	m.clampScroll()
	bodyTop, body := len(m.header()), m.bodyHeight()
	if v.Y >= bodyTop && v.Y < bodyTop+body && v.X == m.width-1 && m.scrollbarVisible(body) {
		return false // a scrollbar press does not clear a retained selection
	}
	left, top, width, rows := m.composerTextBounds()
	if v.X >= left && v.X < left+width && v.Y >= top && v.Y < top+rows {
		if caret, ok := m.composerCaretAt(v.X, v.Y); ok {
			// Capture before moving the caret or clearing an output snapshot:
			// those operations otherwise hide/reposition the visible marker.
			gesture := m.captureComposerSelectionGesture()
			m.clearTextSelection()
			m.selection.composerGesture = gesture
			m.selection.surface, m.selection.dragging = selectionComposer, true
			m.edit.setSelection(m.input, caret, caret)
			m.composerVertical.reset()
			m.caretNote()
			return true
		}
	}
	if v.X >= 0 && v.X < m.width && v.Y >= bodyTop && v.Y < bodyTop+body {
		row := m.scroll + v.Y - bodyTop
		plain := m.transcriptSelectionPlain(row)
		if strings.TrimSpace(plain) != "" && v.X < runewidth.StringWidth(plain) {
			m.freezeTranscriptSelection()
			// Code/diff panels contain rectangle padding in the layout
			// itself. Snapshot provenance removes it before accepting a hit.
			if v.X >= runewidth.StringWidth(m.selection.snapshot.plain[row]) {
				m.clearTextSelection()
				return true
			}
			point := m.transcriptSelectionAt(v.X, v.Y)
			m.edit.clearSelection()
			m.selection.composerGesture = nil
			m.selection.anchor, m.selection.caret = point, point
			m.selection.surface, m.selection.dragging = selectionTranscript, true
			// Remove any previous composer highlight, but retain the copied
			// source positions, rows, and marker geometry from before press.
			m.selection.snapshot.composer = append([]string(nil), m.composerLines()...)
			return true
		}
	}
	// Padding, separators, borders, popups, header, and status are not text
	// surfaces. In particular a blank click must never retain a snapshot.
	m.clearTextSelection()
	return true
}

func (m *ui) extendTextSelection(x, y int) {
	switch m.selection.surface {
	case selectionComposer:
		_, top, _, rows := m.composerTextBounds()
		if rows > 0 {
			if caret, ok := m.composerCaretAt(x, max(top, min(y, top+rows-1))); ok {
				m.edit.extendSelection(m.input, caret)
				m.composerVertical.reset()
				m.caretNote()
			}
		}
	case selectionTranscript:
		m.selection.caret = m.transcriptSelectionAt(x, y)
	}
}

func (m *ui) transcriptSelectionPlain(row int) string {
	if m.transcriptSelectionFrozen() {
		if row >= 0 && row < len(m.selection.snapshot.plain) {
			return m.selection.snapshot.plain[row]
		}
		return ""
	}
	if row < 0 || row >= len(m.lines) {
		return ""
	}
	width := m.width
	if m.scrollbarVisible(m.bodyHeight()) {
		width--
	}
	return selectionPlainRow(m.lines[row], width, m.lineRunsAt(row), m.toolBlinkOff())
}

func (m *ui) lineRunsAt(row int) []lineRun {
	if row >= 0 && row < len(m.lineRuns) {
		return m.lineRuns[row]
	}
	return nil
}

func selectionPlainRow(line string, width int, runs []lineRun, blinkOff bool) string {
	plain := fitText(line, width)
	if blinkOff {
		runes := []rune(plain)
		for _, run := range runs {
			if run.blink && run.start >= 0 && run.end > run.start && run.end <= len(runes) {
				blank := strings.Repeat(" ", runewidth.StringWidth(string(runes[run.start:run.end])))
				plain = string(runes[:run.start]) + blank + string(runes[run.end:])
				runes = []rune(plain)
			}
		}
	}
	return plain
}

func (m *ui) freezeTranscriptSelection() {
	if m.transcriptSelectionFrozen() {
		return
	}
	header, mention, command := m.header(), m.mentionLines(), m.commandLines()
	if m.composerSelectionGestureFrozen() {
		gesture := m.selection.composerGesture
		header, mention, command = gesture.header, gesture.mention, gesture.command
	}
	s := &textSelectionSnapshot{
		lines: append([]string(nil), m.lines...), lineActivity: append([]bool(nil), m.lineActivity...),
		width: m.width, height: m.height, body: m.bodyHeight(), activityFrame: m.activityFrame, blinkOff: m.toolBlinkOff(),
		header: append([]string(nil), header...), composer: append([]string(nil), m.composerLines()...),
		mention: append([]string(nil), mention...), command: append([]string(nil), command...),
		composerView: copyComposerViewport(m.composerTextViewport(), false),
	}
	for _, style := range m.lineStyles {
		s.lineStyles = append(s.lineStyles, style.Copy())
	}
	for _, spans := range m.lineSpans {
		s.lineSpans = append(s.lineSpans, append([]likhaui.Swatch(nil), spans...))
	}
	// Preserve sharing within the snapshot without sharing mutable pointers
	// with markdown/tool caches or allocating a full style for every token.
	styles := make(map[*lipgloss.Style]*lipgloss.Style)
	for _, runs := range m.lineRuns {
		copied := append([]lineRun(nil), runs...)
		for i := range copied {
			if original := copied[i].style; original != nil {
				if styles[original] == nil {
					style := original.Copy()
					styles[original] = &style
				}
				copied[i].style = styles[original]
			}
		}
		s.lineRuns = append(s.lineRuns, copied)
	}
	width := m.width
	if len(s.lines) > s.body {
		width--
	}
	for row, line := range s.lines {
		var runs []lineRun
		if row < len(s.lineRuns) {
			runs = s.lineRuns[row]
		}
		s.plain = append(s.plain, selectionPlainRow(line, width, runs, s.blinkOff))
	}
	m.trimSelectionPanelPadding(s, width)
	m.selection.snapshot = s
}

// Code and edit panels pad their rows before fit sees them. Recover source
// boundaries once, at snapshot creation, rather than trimming whitespace (which
// destroys real code), or adding another parse to every streaming frame.
func (m *ui) trimSelectionPanelPadding(s *textSelectionSnapshot, width int) {
	entryEnd := func(index, start int) int {
		for _, next := range m.entryLines[index+1:] {
			if next > start {
				return min(next, len(s.lines))
			}
		}
		return len(s.lines)
	}
	for index, e := range m.entries {
		if index >= len(m.entryLines) || e.role != "Assistant" && e.role != "Reasoning" {
			continue
		}
		start := m.entryLines[index]
		end := entryEnd(index, start)
		gutter := m.blocks.Assistant + " "
		if e.role == "Reasoning" {
			if m.reasoningLive(index) || !m.thoughtExpanded[m.reasoningOrdinal(index)] {
				continue
			}
			start++ // the Thought marker is not part of the markdown body
			gutter = "  "
		}
		hasCode := false
		for row := start; row < end && row < len(s.lineRuns); row++ {
			for _, run := range s.lineRuns[row] {
				if run.style != nil && isCodePanel(*run.style, m.theme.BgCode.GetBackground()) {
					hasCode = true
					break
				}
			}
		}
		if !hasCode {
			continue
		}
		// Artificial styles mark all source code cells, including tabs and
		// trailing spaces. Panel filler retains the distinct base style.
		rows := markdown.Render(e.content, markdown.Options{
			Width: max(1, contentWidth(m.width)-runewidth.StringWidth(gutter)), Glyphs: markdownGlyphs(m.conn.ASCII),
			Styles: markdown.Styles{CodeBlock: lipgloss.NewStyle().Faint(true), Code: map[markdown.CodeKind]lipgloss.Style{markdown.Plain: lipgloss.NewStyle().Blink(true)}},
			Highlight: func(_ string, code string) ([][]markdown.CodeSegment, bool) {
				var marked [][]markdown.CodeSegment
				for _, line := range strings.Split(code, "\n") {
					marked = append(marked, []markdown.CodeSegment{{Text: line, Kind: markdown.Plain}})
				}
				return marked, true
			},
		})
		for i, row := range rows {
			at := start + i
			if at >= end || !row.Base.GetFaint() {
				continue
			}
			prefix := strings.Repeat(" ", runewidth.StringWidth(gutter))
			if i == 0 {
				prefix = gutter
			}
			// Parsing has bounded fallbacks; never apply metadata if the
			// independently rendered row differs from the displayed layout.
			if prefix+row.Text != s.lines[at] {
				continue
			}
			sourceEnd := 0
			for _, run := range row.Runs {
				if run.Style.GetBlink() {
					sourceEnd = max(sourceEnd, run.End)
				}
			}
			s.plain[at] = fitText(prefix+string([]rune(row.Text)[:sourceEnd]), width)
		}
	}
	for _, record := range m.toolRecords {
		index := record.EntryIndex
		if index < 0 || index >= len(m.entryLines) {
			continue
		}
		diff, ok := m.editDiffCache[editDiffKey{name: record.Name, arguments: record.Arguments, diff: record.Diff}]
		if !ok {
			continue
		}
		start := m.entryLines[index]
		end, ordinal := entryEnd(index, start), 0
		for at := start; at < end && at < len(s.lineRuns) && ordinal < len(diff.Lines); at++ {
			runs := s.lineRuns[at]
			if len(runs) != 1 || runs[0].style == nil {
				continue
			}
			bg := runs[0].style.GetBackground()
			if !reflect.DeepEqual(bg, m.theme.BgDiffAdd.GetBackground()) && !reflect.DeepEqual(bg, m.theme.BgDiffRemove.GetBackground()) {
				continue
			}
			runes := []rune(s.lines[at])
			off := runs[0].start
			if off < 0 || off > len(runes) {
				continue
			}
			room := runewidth.StringWidth(string(runes[off:]))
			text := transcript.FormatDiffLine(diff.Lines[ordinal], diff.NumberWidth(), room, m.blocks.Ellipsis)
			ordinal++
			if fit(text, room) == string(runes[off:]) {
				s.plain[at] = fitText(string(runes[:off])+text, width)
			}
		}
	}
}

func (m *ui) transcriptSelectionAt(x, y int) textSelectionPoint {
	s := m.selection.snapshot
	top := len(m.header())
	if y < top {
		y, x = top, 0
	} else if y >= top+s.body {
		y, x = top+s.body-1, s.width
	}
	row := m.scroll + y - top
	if row >= len(s.plain) {
		row = len(s.plain) - 1
		x = s.width
	}
	row = max(0, row)
	return textSelectionPoint{row: row, rune: selectionRuneAtCell(s.plain[row], x)}
}

// Snap an interior cell of a wide glyph to the nearest insertion boundary.
// Both endpoints use the same mapping, so backwards drags are symmetric.
func selectionRuneAtCell(text string, cell int) int {
	column, offset := 0, 0
	clusters := uniseg.NewGraphemes(text)
	for clusters.Next() {
		cluster := clusters.Str()
		width, count := runewidth.StringWidth(cluster), utf8.RuneCountInString(cluster)
		if cell <= column {
			return offset
		}
		if cell < column+width {
			if cell-column < column+width-cell {
				return offset
			}
			return offset + count
		}
		column += width
		offset += count
	}
	return offset
}

func (m *ui) highlightTranscriptSelection(row int, rendered string) string {
	start, end, ok := m.transcriptSelectionRange()
	if !ok || row < start.row || row > end.row {
		return rendered
	}
	text := []rune(m.selection.snapshot.plain[row])
	left, right := 0, len(text)
	if row == start.row {
		left = start.rune
	}
	if row == end.row {
		right = end.rune
	}
	if left >= right {
		return rendered
	}
	return highlightSelectionCells(rendered, runewidth.StringWidth(string(text[:left])), runewidth.StringWidth(string(text[:right])), textSelectionStyle(m.theme))
}

// Repaint only selected visible cells after the normal row renderer. ANSI
// cuts preserve the surrounding styles; explicit resets isolate reverse video
// from dim/bold/code styles and prevent it escaping into padding or chrome.
func highlightSelectionCells(rendered string, left, right int, style lipgloss.Style) string {
	plain := ansi.Strip(ansi.Cut(rendered, left, right))
	selected := style.Render(plain)
	if plain == "" || selected == plain {
		return rendered // honor the no-color profile without introducing escapes
	}
	return ansi.Cut(rendered, 0, left) + "\x1b[0m" + selected + "\x1b[0m" + ansi.Cut(rendered, right, ansi.StringWidth(rendered)) + "\x1b[0m"
}
