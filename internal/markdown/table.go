package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// table lays out a GFM table as aligned columns separated by TableV and a
// TableH/TableCross rule under the header, when its natural width fits;
// otherwise it falls back to the table's pipe source as plain text.
func (r *renderer) table(t *east.Table, width int, ctx blockCtx) []brow {
	ncols := len(t.Alignments)
	if ncols == 0 {
		return r.literal(r.tableSource(t), width, ctx.text)
	}
	header := r.derive(r.opt.Styles.Strong, ctx.text)
	var grid [][][]cell
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		style := ctx.text
		if row.Kind() == east.KindTableHeader {
			style = header
		}
		cols := make([][]cell, ncols)
		i := 0
		for c := row.FirstChild(); c != nil && i < ncols; c = c.NextSibling() {
			cells := r.inlines(c, style)
			for j := range cells {
				if cells[j].brk {
					cells[j] = cell{r: ' ', w: 1, style: style}
				}
			}
			cols[i] = cells
			i++
		}
		grid = append(grid, cols)
	}

	border := r.derive(r.opt.Styles.TableBorder, ctx.text)
	v := proseCells(nil, r.opt.Glyphs.TableV, border)
	h := proseCells(nil, r.opt.Glyphs.TableH, border)
	cross := proseCells(nil, r.opt.Glyphs.TableCross, border)
	sep := append(append([]cell{{r: ' ', w: 1, style: ctx.text}}, v...), cell{r: ' ', w: 1, style: ctx.text})
	sepW := cellsWidth(sep)

	widths := make([]int, ncols)
	for _, cols := range grid {
		for i, cells := range cols {
			widths[i] = max(widths[i], cellsWidth(cells), 1)
		}
	}
	total := sepW * (ncols - 1)
	for _, w := range widths {
		total += w
	}
	if total > width || cellsWidth(cross) > sepW {
		return r.literal(r.tableSource(t), width, ctx.text)
	}

	// The rule's crossing spans the separator's width, centered on its bar.
	left := (sepW - cellsWidth(cross)) / 2
	crossing := append(append(repeatCells(h, left, border), cross...), repeatCells(h, sepW-left-cellsWidth(cross), border)...)

	var rows []brow
	for ri, cols := range grid {
		var line []cell
		for i, cells := range cols {
			if i > 0 {
				line = append(line, sep...)
			}
			line = append(line, align(cells, widths[i], t.Alignments[i], ctx.text)...)
		}
		rows = append(rows, brow{cells: trimTrailingSpaces(line), base: ctx.text})
		if ri == 0 {
			var rule []cell
			for i, w := range widths {
				if i > 0 {
					rule = append(rule, crossing...)
				}
				rule = append(rule, repeatCells(h, w, border)...)
			}
			rows = append(rows, brow{cells: trimTrailingSpaces(rule), base: ctx.text})
		}
	}
	return rows
}

// align pads cells to width per the column alignment.
func align(cells []cell, width int, a east.Alignment, pad int) []cell {
	gap := width - cellsWidth(cells)
	left := 0
	switch a {
	case east.AlignRight:
		left = gap
	case east.AlignCenter:
		left = gap / 2
	}
	out := spaceCells(left, pad)
	out = append(out, cells...)
	return append(out, spaceCells(gap-left, pad)...)
}

// tableSource recovers the table's pipe lines: each row from its source
// line, the delimiter row rebuilt from the column alignments.
func (r *renderer) tableSource(t *east.Table) []string {
	var lines []string
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		lines = append(lines, r.rowSource(row, len(t.Alignments)))
		if row.Kind() == east.KindTableHeader {
			lines = append(lines, delimiterRow(t.Alignments))
		}
	}
	return lines
}

// rowSource returns the source line a table row was parsed from, or the row
// rebuilt from its cells' text when its position is unknown.
func (r *renderer) rowSource(row ast.Node, ncols int) string {
	if pos := row.Pos(); pos >= 0 && pos < len(r.src) {
		line := r.src[pos:]
		if end := bytes.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		return strings.TrimSpace(string(line))
	}
	parts := make([]string, 0, ncols)
	for c := row.FirstChild(); c != nil; c = c.NextSibling() {
		parts = append(parts, r.plain(c))
	}
	return "| " + strings.Join(parts, " | ") + " |"
}

func delimiterRow(alignments []east.Alignment) string {
	var b strings.Builder
	b.WriteString("|")
	for _, a := range alignments {
		switch a {
		case east.AlignLeft:
			b.WriteString(" :-- |")
		case east.AlignRight:
			b.WriteString(" --: |")
		case east.AlignCenter:
			b.WriteString(" :-: |")
		default:
			b.WriteString(" --- |")
		}
	}
	return b.String()
}
