package markdown

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// tabStop is the code-block tab width.
const tabStop = 4

// codeBlock lays out a code panel: every row is padded to width in the
// CodeBlock style, lines never word-wrap (long ones break by cell width), and
// tokens take Styles.Code colors when Options.Highlight succeeds.
func (r *renderer) codeBlock(lines []string, lang string, width int, ctx blockCtx) []brow {
	if len(lines) == 0 {
		lines = []string{""}
	}
	segs := r.highlight(lang, lines)
	kinds := map[CodeKind]int{}
	styleOf := func(k CodeKind) int {
		if idx, ok := kinds[k]; ok {
			return idx
		}
		idx := r.code
		if s, ok := r.opt.Styles.Code[k]; ok {
			idx = r.derive(s, r.code)
		}
		kinds[k] = idx
		return idx
	}
	var rows []brow
	for i, line := range lines {
		var cells []cell
		col := 0
		if segs != nil {
			for _, seg := range segs[i] {
				cells, col = codeCells(cells, col, seg.Text, styleOf(seg.Kind))
			}
		} else {
			cells, _ = codeCells(cells, col, line, r.code)
		}
		rows = append(rows, hardBreak(cells, width, r.code, r.code)...)
	}
	return rows
}

// highlight runs Options.Highlight and keeps its result only when it covers
// the code line for line with identical text; nil means render plain.
func (r *renderer) highlight(lang string, lines []string) [][]CodeSegment {
	if r.opt.Highlight == nil {
		return nil
	}
	segs, ok := r.opt.Highlight(lang, strings.Join(lines, "\n"))
	if !ok {
		return nil
	}
	if len(segs) == len(lines)+1 && joinSegments(segs[len(segs)-1]) == "" {
		segs = segs[:len(lines)]
	}
	if len(segs) != len(lines) {
		return nil
	}
	for i, line := range lines {
		if joinSegments(segs[i]) != line {
			return nil
		}
	}
	return segs
}

func joinSegments(segs []CodeSegment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// codeCells appends code text starting at column col: tabs expand to the
// next tabStop column and hidden runes are escaped. It returns the new
// column.
func codeCells(dst []cell, col int, s string, style int) ([]cell, int) {
	for _, r := range s {
		switch {
		case r == '\t':
			n := tabStop - col%tabStop
			for range n {
				dst = append(dst, cell{r: ' ', w: 1, style: style})
			}
			col += n
		case hidden(r):
			before := len(dst)
			dst = escapeCells(dst, r, style)
			col += len(dst) - before
		default:
			w := runewidth.RuneWidth(r)
			dst = append(dst, cell{r: r, w: w, style: style})
			col += w
		}
	}
	return dst, col
}
