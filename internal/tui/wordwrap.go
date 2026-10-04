package tui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// wrapHanging word-wraps transcript prose (spec transcript-redesign §
// Wrapping) into rows that hang under a glyph gutter. Rune handling matches
// wrap(): control and zero-width runes render as their escaped \uXXXX form
// and cell widths come from go-runewidth.
//
// Contract:
//   - width < 1 is treated as 1; negative indents are treated as 0.
//   - The first row carries no indent: the caller has already placed
//     firstIndent cells (the glyph gutter) in front of it, so its text fits
//     width-firstIndent cells. When firstIndent leaves no room for the first
//     piece of text, the first row is "" and the text starts on row two.
//   - Every later row, whether a soft wrap or the row after an explicit
//     '\n', starts with the hang indent: hangIndent spaces, shrunk to
//     width-2 (floor 0) so at least two cells remain and no rune ever needs
//     splitting. Rows with no text are "" (no indent, no trailing spaces).
//   - Rows break at ASCII spaces; the spaces at a break are dropped, all
//     other spaces are kept (no collapsing), and a paragraph's leading
//     spaces stay glued to its first word. Trailing spaces (or a paragraph
//     of only spaces) are kept as far as they fit on their row. A token wider than a full row
//     starts on the current row when its first cell fits there and breaks by
//     cell width, never inside a wide rune. At width 1, where a wide rune
//     cannot fit any row, that rune is shown escaped like a hidden rune.
//   - Every returned row's display width is <= width (the first row's is
//     <= width-firstIndent when firstIndent < width).
//
// At least one row is always returned.
func wrapHanging(text string, width, firstIndent, hangIndent int) []string {
	width = max(width, 1)
	hang := min(max(hangIndent, 0), max(width-2, 0))
	w := &hangWrapper{
		avail:     max(width-max(firstIndent, 0), 0),
		contAvail: width - hang,
		pad:       strings.Repeat(" ", hang),
	}
	for i, para := range strings.Split(text, "\n") {
		if i > 0 {
			w.newRow()
		}
		w.paragraph(para)
	}
	w.newRow()
	return w.rows
}

// wrapCell is one terminal cell group: a visible rune or one character of an
// escaped hidden rune.
type wrapCell struct {
	r     rune
	width int
}

type hangWrapper struct {
	rows      []string
	line      strings.Builder
	cols      int // cells used by text on the current row
	avail     int // text cells available on the current row
	contAvail int // text cells available on continuation rows
	pad       string
}

func (w *hangWrapper) newRow() {
	text := w.line.String()
	if len(w.rows) > 0 && text != "" {
		text = w.pad + text
	}
	w.rows = append(w.rows, text)
	w.line.Reset()
	w.cols = 0
	w.avail = w.contAvail
}

// paragraph lays out one '\n'-free run as space-separated tokens.
func (w *hangWrapper) paragraph(para string) {
	spaces := 0
	var word []wrapCell
	lead := true
	emit := func() {
		w.place(spaces, word, lead)
		lead = false
		spaces = 0
		word = word[:0]
	}
	for _, r := range para {
		if r == ' ' {
			if len(word) > 0 {
				emit()
			}
			spaces++
			continue
		}
		word = w.appendCells(word, r)
	}
	if len(word) > 0 || spaces > 0 {
		emit()
	}
}

// appendCells adds r's cells, escaping it exactly as wrap() does when it is
// hidden, or when it is wider than any row can hold (width 1).
func (w *hangWrapper) appendCells(cells []wrapCell, r rune) []wrapCell {
	size := runewidth.RuneWidth(r)
	if hiddenReviewRune(r) || size > w.contAvail {
		for _, c := range fmt.Sprintf("\\u%04X", r) {
			cells = append(cells, wrapCell{c, 1})
		}
		return cells
	}
	return append(cells, wrapCell{r, size})
}

// place lays out one token: the spaces before it and its word cells. lead
// marks the paragraph's first token, whose spaces are indentation.
func (w *hangWrapper) place(spaces int, word []wrapCell, lead bool) {
	if len(word) == 0 {
		// Trailing spaces, or a paragraph of only spaces: kept as far as
		// they fit, never spilling onto rows of their own.
		w.writeSpaces(min(spaces, max(w.avail-w.cols, 0)))
		return
	}
	if lead && spaces > 0 {
		// Leading indentation travels with the first word.
		glued := make([]wrapCell, 0, spaces+len(word))
		for range spaces {
			glued = append(glued, wrapCell{' ', 1})
		}
		word = append(glued, word...)
		spaces = 0
	}
	size := 0
	for _, c := range word {
		size += c.width
	}
	if w.cols+spaces+size <= w.avail {
		w.writeSpaces(spaces)
		w.write(word)
		return
	}
	if size <= w.contAvail {
		// Fits a fresh row: break here, dropping the separating spaces.
		w.newRow()
		w.write(word)
		return
	}
	// Wider than any row: fill the current row, then break by cell width.
	if w.cols > 0 {
		if w.cols+spaces+word[0].width <= w.avail {
			w.writeSpaces(spaces)
		} else {
			w.newRow()
		}
	}
	for _, c := range word {
		if w.cols+c.width > w.avail {
			w.newRow()
		}
		w.line.WriteRune(c.r)
		w.cols += c.width
	}
}

func (w *hangWrapper) writeSpaces(n int) {
	w.line.WriteString(strings.Repeat(" ", n))
	w.cols += n
}

func (w *hangWrapper) write(word []wrapCell) {
	for _, c := range word {
		w.line.WriteRune(c.r)
		w.cols += c.width
	}
}
