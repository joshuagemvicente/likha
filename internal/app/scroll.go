package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// Continuous scrolling: the transcript is one long document. The viewport
// shows lines [scroll, scroll+body); the newest content is followed while the
// viewport sits at the bottom (chat/browser behavior), and any upward scroll
// anchors the view until the user returns to the bottom. Wheel input moves by
// a few lines per event — line-granular movement, never page jumps.

// scrollWheelLines is how many lines one mouse-wheel notch travels, matching
// the browser's default three-lines-per-notch.
const scrollWheelLines = 3

// scrollbarColumn is the body's last display column when the scrollbar is
// visible; clicks and drags there reposition the viewport.
const scrollbarColumn = 1

// scrollMax is the topmost valid scroll offset for the current content and
// viewport: the number of body lines hidden below the fold.
func (m *ui) scrollMax() int {
	return max(0, len(m.lines)-m.bodyHeight())
}

// clampScroll reconciles the offset with the current content: while
// following, the viewport stays pinned to the newest lines even as the
// transcript grows; otherwise it clamps into range.
func (m *ui) clampScroll() {
	maxScroll := m.scrollMax()
	if m.following {
		m.scroll = maxScroll
		return
	}
	m.scroll = max(0, min(m.scroll, maxScroll))
	if m.scroll >= maxScroll {
		m.following = true
	}
}

// scrollBy moves the viewport n lines (negative = up). Leaving the bottom
// unhooks following, like a browser.
func (m *ui) scrollBy(n int) bool {
	m.scroll += n
	maxScroll := m.scrollMax()
	if m.scroll >= maxScroll {
		m.scroll = maxScroll
		m.following = true
	} else {
		m.scroll = max(0, m.scroll)
		m.following = false
	}
	return true
}

// jumpTop shows the very first line of the session; nothing is clipped above.
func (m *ui) jumpTop() {
	m.scroll = 0
	m.following = false
	m.markSeenFromScroll()
}

// jumpBottom pins the viewport to the newest content, the page -1 of the old
// model: it keeps following as lines stream in.
func (m *ui) jumpBottom() {
	m.scroll = m.scrollMax()
	m.following = true
	m.markSeenFromScroll()
}

// pageUp jumps one viewport up; pageDown one viewport down, returning to the
// bottom when the last page is passed.
func (m *ui) pageUp() {
	m.scrollBy(-m.bodyHeight())
	m.markSeenFromScroll()
}

func (m *ui) pageDown() {
	m.scrollBy(m.bodyHeight())
	m.markSeenFromScroll()
}

// scrollTickMsg is reserved for an animated glide later; the current wheel
// model moves line-granular per event without a redraw ticker.
type scrollTickMsg struct{}

// caretTickMsg toggles the composer's block caret blink phase.
type caretTickMsg struct{}

// caretBlinkInterval matches the classic terminal/hardware caret blink rate.
const caretBlinkInterval = 530 * time.Millisecond

// blinkCaret schedules the next blink tick.
func blinkCaret() tea.Cmd {
	return tea.Tick(caretBlinkInterval, func(time.Time) tea.Msg { return caretTickMsg{} })
}

// caretVisible reports whether the block caret renders at the end of the
// draft: only while the composer is editable. A recent keystroke forces the
// solid phase so the caret never blinks away mid-typing.
func (m *ui) caretVisible() bool {
	if m.working || m.pending != nil || m.keyModal.open || m.dialog.open {
		return false
	}
	return m.caretOn || m.caretTyped
}

// caretNote keystroke hook: the caret renders solid from the first typed
// character until the next blink tick.
func (m *ui) caretNote() {
	m.caretOn = true
	m.caretTyped = true
}

// scrollbarVisible reports whether the body needs a scrollbar: only when
// content overflows the viewport.
func (m *ui) scrollbarVisible(body int) bool {
	return len(m.lines) > body
}

// scrollbarRows renders the one-column scrollbar for the body's right edge:
// a dim track with a thumb sized to the viewport's share of the content and
// positioned by the scroll offset. A empty slice (no scrollbar) is returned
// when the content fits.
func (m *ui) scrollbarRows(body int) []string {
	if !m.scrollbarVisible(body) {
		return nil
	}
	rows := make([]string, 0, body)
	thumb := max(1, body*body/len(m.lines))
	thumb = min(thumb, body)
	travel := max(1, body-thumb)
	pos := 0
	if maxScroll := m.scrollMax(); maxScroll > 0 {
		pos = travel * min(m.scroll, maxScroll) / maxScroll
	}
	track := m.theme.Help.Render("│")
	knob := m.theme.Selected.Render("█")
	for i := range body {
		if i >= pos && i < pos+thumb {
			rows = append(rows, knob)
		} else {
			rows = append(rows, track)
		}
	}
	return rows
}

// spliceScrollbar places the scrollbar column over the already-rendered body
// rows without reflowing them: each row is re-fit one cell narrower and the
// scrollbar column is appended, so no body text is lost.
func (m *ui) spliceScrollbar(rows []string, header int, body int) {
	bar := m.scrollbarRows(body)
	if bar == nil {
		return
	}
	for i := range body {
		row := rows[header+i]
		rows[header+i] = truncateRow(row, m.width-1) + bar[i]
	}
}

// truncateRow cuts a rendered row to n display columns on cell boundaries.
// SGR escape sequences never occupy cells, so their bytes are neither cut
// nor counted toward the width: a styled row measures by its visible text
// alone, and a cut never splits a sequence or misplaces the scrollbar.
func truncateRow(row string, width int) string {
	var buf []byte
	w := 0
	escaping := false
	escStart := 0
	for _, r := range row {
		if escaping {
			buf = append(buf, string(r)...)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\\' {
				escaping = false // final byte of the sequence
			}
			continue
		}
		if r == '\x1b' {
			escaping = true
			escStart = len(buf)
			buf = append(buf, string(r)...)
			continue
		}
		rw := runewidth.RuneWidth(r)
		if w+rw > width {
			// Cell budget exhausted: drop everything after, including any
			// escape sequence already in progress, so a dangling ESC cannot
			// corrupt the following row's styling.
			if escaping {
				buf = buf[:escStart]
			}
			break
		}
		buf = append(buf, string(r)...)
		w += rw
	}
	return string(buf)
}

// updateScrollbarMouse handles clicks and drags on the scrollbar column: the
// thumb jumps to the pointer's vertical position and follows while the button
// is held (motion events). It reports whether the event was consumed.
func (m *ui) updateScrollbarMouse(v tea.MouseMsg) bool {
	if !m.scrollbarVisible(m.bodyHeight()) {
		return false
	}
	if v.X != m.width-1 {
		return false
	}
	drag := v.Type == tea.MouseLeft || v.Type == tea.MouseMotion
	if !drag {
		return false
	}
	body := m.bodyHeight()
	frac := float64(max(0, min(v.Y, body-1))) / float64(max(1, body-1))
	m.scroll = int(float64(m.scrollMax()) * frac)
	if m.scroll >= m.scrollMax() {
		m.scroll = m.scrollMax()
		m.following = true
	} else {
		// Dragging off the bottom unhooks the pinned view, like a wheel.
		m.following = false
	}
	m.markSeenFromScroll()
	return true
}

// markSeenFromScroll marks review pages whose line range lies entirely above
// the viewport's bottom edge as reviewed; with the pending proposal at the
// transcript end, reaching the bottom reviews everything.
func (m *ui) markSeenFromScroll() {
	if m.pending == nil || len(m.reviewSeen) == 0 {
		return
	}
	body := m.bodyHeight()
	if body <= 0 {
		return
	}
	pages := m.pageCount()
	if pages != len(m.reviewSeen) {
		m.reviewSeen = make([]bool, pages)
	}
	seenThrough := m.scroll + body // lines fully above the viewport bottom
	for i := range m.reviewSeen {
		// The last page is usually shorter than a full page: its end is the
		// transcript's end, not pages*body.
		if seenThrough >= min((i+1)*body, len(m.lines)) {
			m.reviewSeen[i] = true
		}
	}
}

// scrollPosition renders the browser-like position indicator used in the
// status hints: a percentage of the transcript scrolled, "top"/"bottom" at
// the extremes.
func (m *ui) scrollPosition() string {
	maxScroll := m.scrollMax()
	if maxScroll == 0 {
		return "top"
	}
	pct := 100 * m.scroll / maxScroll
	switch {
	case m.scroll <= 0:
		return "top"
	case m.scroll >= maxScroll:
		return "bottom"
	default:
		return fmt.Sprintf("%d%%", pct)
	}
}
