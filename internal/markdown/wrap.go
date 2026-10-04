package markdown

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// cell is one terminal cell group of a laid-out row: a visible rune or one
// character of an escaped hidden rune. style indexes renderer.styles. brk
// marks a hard line break inside an inline cell stream; it never reaches a
// row.
type cell struct {
	r     rune
	w     int
	style int
	brk   bool
}

// brow is a block-level row under construction: its cells and the style
// index painting every cell whose own style equals base.
type brow struct {
	cells []cell
	base  int
}

// hidden reports runes that render escaped. It mirrors internal/tui's
// hiddenReviewRune so transcript text and markdown escape the same runes.
func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || runewidth.RuneWidth(r) == 0
}

// escapeCells appends r in its escaped \uXXXX form, one cell per character,
// as internal/tui's fit and wrap do.
func escapeCells(dst []cell, r rune, style int) []cell {
	for _, c := range fmt.Sprintf("\\u%04X", r) {
		dst = append(dst, cell{r: c, w: 1, style: style})
	}
	return dst
}

// proseCells appends s as prose cells: tabs and newlines become spaces and
// hidden runes are escaped, so no escape sequence from content survives.
func proseCells(dst []cell, s string, style int) []cell {
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n':
			dst = append(dst, cell{r: ' ', w: 1, style: style})
		case hidden(r):
			dst = escapeCells(dst, r, style)
		default:
			dst = append(dst, cell{r: r, w: runewidth.RuneWidth(r), style: style})
		}
	}
	return dst
}

// fitWide escapes every rune wider than width (a wide rune at width 1), so
// each cell fits an empty row and hard breaks always make progress.
func fitWide(cells []cell, width int) []cell {
	for _, c := range cells {
		if c.w > width {
			out := make([]cell, 0, len(cells)+6)
			for _, c := range cells {
				if c.w > width {
					out = escapeCells(out, c.r, c.style)
					continue
				}
				out = append(out, c)
			}
			return out
		}
	}
	return cells
}

func cellsWidth(cells []cell) int {
	n := 0
	for _, c := range cells {
		n += c.w
	}
	return n
}

func trimTrailingSpaces(cells []cell) []cell {
	for len(cells) > 0 && cells[len(cells)-1].r == ' ' {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// wrapCells word-wraps an inline cell stream into rows of at most width
// cells. Rows break at spaces, dropping the spaces at a soft break; a word
// wider than a row starts on the current row when its first cell fits there
// and breaks by cell width, never inside a wide rune. Leading spaces after a
// hard break are kept as far as the first word allows; trailing spaces are
// trimmed. At least one row is returned.
func wrapCells(cells []cell, width, base int) []brow {
	width = max(width, 1)
	cells = fitWide(cells, width)
	var rows []brow
	var cur []cell
	cols := 0
	flush := func() {
		rows = append(rows, brow{cells: trimTrailingSpaces(cur), base: base})
		cur = nil
		cols = 0
	}
	put := func(cs []cell) {
		cur = append(cur, cs...)
		cols += cellsWidth(cs)
	}
	lineStart := true
	for i := 0; i < len(cells); {
		if cells[i].brk {
			flush()
			lineStart = true
			i++
			continue
		}
		j := i
		for j < len(cells) && cells[j].r == ' ' && !cells[j].brk {
			j++
		}
		k := j
		for k < len(cells) && cells[k].r != ' ' && !cells[k].brk {
			k++
		}
		spaces, word := cells[i:j], cells[j:k]
		i = k
		if len(word) == 0 {
			continue // trailing spaces before a break or the end
		}
		if cols == 0 && !lineStart {
			spaces = nil
		}
		lineStart = false
		size := cellsWidth(word)
		switch {
		case cols+len(spaces)+size <= width:
			put(spaces)
			put(word)
		case size <= width:
			if cols > 0 {
				flush()
			}
			put(word)
		default:
			if cols+len(spaces)+word[0].w <= width {
				put(spaces)
			} else if cols > 0 {
				flush()
			}
			for _, c := range word {
				if cols+c.w > width {
					flush()
				}
				put([]cell{c})
			}
		}
	}
	flush()
	return rows
}

// hardBreak splits one unwrapped line of cells into rows of at most width
// cells without word wrapping, padding each row with pad-styled spaces to
// exactly width cells.
func hardBreak(line []cell, width, base, pad int) []brow {
	width = max(width, 1)
	line = fitWide(line, width)
	rows := make([]brow, 0, cellsWidth(line)/width+1)
	cur := make([]cell, 0, width)
	cols := 0
	flush := func() {
		for ; cols < width; cols++ {
			cur = append(cur, cell{r: ' ', w: 1, style: pad})
		}
		rows = append(rows, brow{cells: cur, base: base})
		cur = nil
		cols = 0
	}
	for _, c := range line {
		if cols+c.w > width {
			flush()
		}
		if cur == nil {
			cur = make([]cell, 0, width)
		}
		cur = append(cur, c)
		cols += c.w
	}
	flush()
	return rows
}

// clipCells keeps the leading cells of cs that fit limit cells, padding with
// spaces in style pad to exactly limit cells.
func clipCells(cs []cell, limit, pad int) []cell {
	out := make([]cell, 0, limit)
	cols := 0
	for _, c := range cs {
		if cols+c.w > limit {
			break
		}
		out = append(out, c)
		cols += c.w
	}
	for ; cols < limit; cols++ {
		out = append(out, cell{r: ' ', w: 1, style: pad})
	}
	return out
}

// prefixRows prepends a container prefix to rows laid out at innerWidth
// inside a container of width cells: first on the first row, rest on later
// rows, blank (trailing spaces trimmed) on empty rows. Prefixes are clipped
// so every row still fits width.
func prefixRows(rows []brow, first, rest, blank []cell, width, innerWidth, pad int) []brow {
	limit := max(width-innerWidth, 0)
	first = clipCells(first, limit, pad)
	rest = clipCells(rest, limit, pad)
	blank = trimTrailingSpaces(clipCells(blank, limit, pad))
	out := make([]brow, len(rows))
	for i, row := range rows {
		var p []cell
		switch {
		case i == 0:
			p = first
		case len(row.cells) == 0:
			p = blank
		default:
			p = rest
		}
		cells := make([]cell, 0, len(p)+len(row.cells))
		cells = append(cells, p...)
		cells = append(cells, row.cells...)
		if len(row.cells) == 0 {
			cells = trimTrailingSpaces(cells)
		}
		out[i] = brow{cells: cells, base: row.base}
	}
	return out
}

// repeatCells fills exactly n cells with copies of glyph, padding with
// spaces when the glyph's width does not divide n.
func repeatCells(glyph []cell, n, pad int) []cell {
	gw := cellsWidth(glyph)
	out := make([]cell, 0, n)
	cols := 0
	for gw > 0 && cols+gw <= n {
		out = append(out, glyph...)
		cols += gw
	}
	for ; cols < n; cols++ {
		out = append(out, cell{r: ' ', w: 1, style: pad})
	}
	return out
}

// spaceCells returns n spaces in style.
func spaceCells(n, style int) []cell {
	out := make([]cell, n)
	for i := range out {
		out[i] = cell{r: ' ', w: 1, style: style}
	}
	return out
}

// rowText returns the plain text of cells.
func rowText(cells []cell) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteRune(c.r)
	}
	return b.String()
}
