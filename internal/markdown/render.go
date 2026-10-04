// Package markdown renders assistant markdown into styled, width-bounded
// transcript rows (spec transcript-redesign § Markdown). It parses CommonMark
// plus GFM tables, strikethrough, and task lists with goldmark and lays the
// AST out itself: no HTML is produced or interpreted, and every rune of
// content passes through the same escaping as internal/tui, so no escape
// sequence from model text can reach the terminal.
//
// Syntax highlighting is injected through Options.Highlight; this package
// imports neither internal/tui nor internal/highlight.
package markdown

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Run paints the rune range [Start, End) of Row.Text in Style instead of the
// row's Base style. Runs are sorted and never overlap.
type Run struct {
	Start, End int
	Style      lipgloss.Style
	// StyleID identifies Style within one Render result: runs and row bases
	// with equal IDs carry the same style, so a caller can share one copy.
	StyleID int
}

// Row is one rendered row. Its display width (go-runewidth, as internal/tui
// measures) never exceeds Options.Width. Base paints the text outside Runs
// (and, for code panels, the padding that makes the panel a rectangle).
type Row struct {
	Text   string
	Base   lipgloss.Style
	BaseID int // StyleID of Base
	Runs   []Run
}

// CodeKind classifies a highlighted code token.
type CodeKind int

// Code token kinds, in the order internal/highlight defines them.
const (
	Plain CodeKind = iota
	Keyword
	String
	Comment
	Number
	Name
	Function
	Type
	Operator
	Punctuation
	Builtin
	Literal
)

// CodeSegment is one highlighted token of a code line.
type CodeSegment struct {
	Text string
	Kind CodeKind
}

// Styles maps markdown elements to terminal styles.
type Styles struct {
	Text, Heading, HeadingUnderline, Strong, Emphasis, Strike, InlineCode, CodeBlock, Quote, Link, LinkURL, Rule, Muted, Bullet, TableBorder lipgloss.Style
	// Code holds highlight token styles; kinds without an entry use
	// CodeBlock. Token styles inherit CodeBlock's unset properties (its
	// background).
	Code map[CodeKind]lipgloss.Style
}

// Glyphs are the structural characters. Empty fields take the Unicode
// defaults (• │ ─ … │ ─ ┼); the ASCII set is - | - ... | - +.
type Glyphs struct {
	Bullet, QuoteBar, Rule, Ellipsis, TableV, TableH, TableCross string
}

// Options configures Render.
type Options struct {
	// Width is the text column width in cells; the caller places any glyph
	// gutter and hanging indent outside it. Values below 1 count as 1.
	Width  int
	Styles Styles
	Glyphs Glyphs
	// Highlight, when non-nil, highlights a fenced code block. It receives
	// the fence's language (first word of the info string) and the code
	// without its trailing newline, and returns one segment list per line.
	// The result is used only when ok and its text matches the code line for
	// line; otherwise the block renders plain.
	Highlight func(lang, code string) ([][]CodeSegment, bool)
	// PlainOpenFence skips Highlight for a fenced block without its closing
	// fence. The streaming answer sets it: its open block changes with every
	// delta, so highlighting it would re-lex the growing code each frame. The
	// block is highlighted once the fence closes.
	PlainOpenFence bool
}

var parser = sync.OnceValue(func() goldmark.Markdown {
	return goldmark.New(goldmark.WithExtensions(
		extension.Table,
		extension.Strikethrough,
		extension.TaskList,
	))
})

// Parsing bounds. goldmark's parse is superlinear on some inputs (deeply
// nested quotes, runs of unclosed link brackets): 16 KiB of ">" takes about
// 0.15 s and the cost grows quadratically, and Render runs on the UI
// goroutine for every streaming delta. A source over maxChunkBytes is parsed
// in chunks split at top-level block boundaries (see chunks), so one parse
// never sees more than maxChunkBytes except for a chunk that is a single
// fenced code block, which parses in linear time. A larger chunk, and every
// chunk left once parseBudget has elapsed, renders as plain wrapped text.
// Tests may change parseBudget.
const maxChunkBytes = 8 << 10

var parseBudget = 100 * time.Millisecond

// Nesting bounds. The layout recurses once per container (quote, list) and
// once per inline element (emphasis, link), and goldmark nests them as deep
// as the source asks: 32 KiB of ">" is 32768 nested quotes, which overflowed
// the goroutine stack (a fatal error, not a recoverable panic). A container
// nested deeper than maxBlockDepth renders its text plain, as does an inline
// element nested deeper than maxInlineDepth. Past a few levels the text
// column is a cell or two wide anyway.
const (
	maxBlockDepth  = 24
	maxInlineDepth = 32
)

// Render lays src out as rows of at most opt.Width cells. Blocks are
// separated by one blank row; there are no leading or trailing blank rows,
// and empty input yields no rows. An unclosed code fence renders as a code
// block to the end of src.
//
// Sources over 8 KiB are parsed chunk by chunk, so in them a link reference
// definition applies only within its chunk, and a loose list or an HTML
// block split by a chunk boundary renders as two blocks. A chunk over 8 KiB
// that is not one fenced code block, and every chunk after the first once
// 100 ms of parsing has passed, render as plain wrapped text.
func Render(src string, opt Options) []Row {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	if strings.TrimSpace(src) == "" {
		return nil
	}
	opt.Width = max(opt.Width, 1)
	opt.Glyphs = withDefaults(opt.Glyphs)
	r := &renderer{opt: opt}
	r.text = r.add(opt.Styles.Text)
	r.code = r.add(opt.Styles.CodeBlock)
	var rows []brow
	deadline := time.Now().Add(parseBudget)
	for i, c := range chunks(src) {
		var block []brow
		if (len(c.text) > maxChunkBytes && !c.fence) || (i > 0 && time.Now().After(deadline)) {
			block = r.literal(strings.Split(strings.Trim(c.text, "\n"), "\n"), opt.Width, r.text)
		} else {
			r.src = []byte(c.text)
			doc := parser().Parser().Parse(text.NewReader(r.src))
			block = r.children(doc, opt.Width, blockCtx{text: r.text})
		}
		block = trimBlankRows(block)
		if len(block) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, brow{base: r.text})
		}
		rows = append(rows, block...)
	}
	return r.finish(rows)
}

func trimBlankRows(rows []brow) []brow {
	for len(rows) > 0 && len(rows[0].cells) == 0 {
		rows = rows[1:]
	}
	for len(rows) > 0 && len(rows[len(rows)-1].cells) == 0 {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// chunk is a run of whole source lines parsed on its own. fence marks a
// chunk that is exactly one fenced code block (closed on its last non-blank
// line, or never closed).
type chunk struct {
	text  string
	fence bool
}

// chunks splits src into parse chunks. A source of at most maxChunkBytes is
// one chunk, so ordinary answers parse exactly as a whole document. A larger
// one is cut into segments at lines that start a top-level block for sure:
// a line that follows a blank line, starts in column 0 with a non-blank
// character, and is outside a fenced code block. CommonMark closes every
// open container there (no lazy continuation follows a blank line), so each
// segment parses as it would in place. Consecutive segments are then packed
// into chunks of at most maxChunkBytes; a longer segment is its own chunk.
func chunks(src string) []chunk {
	if len(src) <= maxChunkBytes {
		return []chunk{{text: src}}
	}
	var segs []chunk
	var fenceChar byte
	fenceLen := 0     // the open fence's run length; 0 outside a fence
	start := 0        // byte offset of the current segment
	seenText := false // the segment has a non-blank line before this one
	fenceOnly := false
	prevBlank := false
	for pos := 0; pos < len(src); {
		next := len(src)
		if end := strings.IndexByte(src[pos:], '\n'); end >= 0 {
			next = pos + end + 1
		}
		line := strings.TrimSuffix(src[pos:next], "\n")
		blank := strings.TrimSpace(line) == ""
		if fenceLen == 0 && seenText && prevBlank && !blank && line[0] != ' ' && line[0] != '\t' {
			segs = append(segs, chunk{text: src[start:pos], fence: fenceOnly})
			start, seenText, fenceOnly = pos, false, false
		}
		switch c, n := fenceRun(line); {
		case fenceLen > 0:
			if c == fenceChar && n >= fenceLen && strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), string(c))) == "" {
				fenceLen = 0
			}
		case n > 0 && (c == '~' || !strings.Contains(strings.TrimSpace(line)[n:], "`")):
			fenceChar, fenceLen = c, n
			fenceOnly = !seenText
		case !blank:
			fenceOnly = false
		}
		seenText = seenText || !blank
		prevBlank = blank
		pos = next
	}
	segs = append(segs, chunk{text: src[start:], fence: fenceOnly})

	var out []chunk
	for _, s := range segs {
		if n := len(out); n > 0 && len(out[n-1].text)+len(s.text) <= maxChunkBytes {
			out[n-1] = chunk{text: out[n-1].text + s.text}
			continue
		}
		out = append(out, s)
	}
	return out
}

// fenceRun returns the fence character and run length opening line after at
// most three spaces of indentation, or 0, 0.
func fenceRun(line string) (byte, int) {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent > 3 {
		return 0, 0
	}
	rest := line[indent:]
	if rest == "" || (rest[0] != '`' && rest[0] != '~') {
		return 0, 0
	}
	n := len(rest) - len(strings.TrimLeft(rest, rest[:1]))
	if n < 3 {
		return 0, 0
	}
	return rest[0], n
}

func withDefaults(g Glyphs) Glyphs {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&g.Bullet, "•")
	def(&g.QuoteBar, "│")
	def(&g.Rule, "─")
	def(&g.Ellipsis, "…")
	def(&g.TableV, "│")
	def(&g.TableH, "─")
	def(&g.TableCross, "┼")
	return g
}

type renderer struct {
	src    []byte
	opt    Options
	styles []lipgloss.Style
	text   int // Styles.Text
	code   int // Styles.CodeBlock
	nest   int // inline nesting depth of the element being laid out
}

// blockCtx carries container state down the block tree.
type blockCtx struct {
	text  int  // style index for prose in this container
	tight bool // children of a tight list item: no blank rows between them
	depth int  // containers (quotes, list items) enclosing this one
}

func (r *renderer) add(s lipgloss.Style) int {
	r.styles = append(r.styles, s)
	return len(r.styles) - 1
}

// derive adds s with unset properties inherited from the style at parent.
func (r *renderer) derive(s lipgloss.Style, parent int) int {
	return r.add(s.Inherit(r.styles[parent]))
}

// finish converts laid-out rows to Rows, turning cells whose style differs
// from the row base into merged Runs.
func (r *renderer) finish(rows []brow) []Row {
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		var runs []Run
		for i, c := range row.cells {
			if c.style == row.base {
				continue
			}
			if n := len(runs); n > 0 && runs[n-1].End == i && row.cells[i-1].style == c.style {
				runs[n-1].End = i + 1
				continue
			}
			runs = append(runs, Run{Start: i, End: i + 1, Style: r.styles[c.style], StyleID: c.style})
		}
		out = append(out, Row{Text: rowText(row.cells), Base: r.styles[row.base], BaseID: row.base, Runs: runs})
	}
	return out
}

// children lays out the block children of parent, one blank row between
// blocks (none inside tight list items, except after a heading).
func (r *renderer) children(parent ast.Node, width int, ctx blockCtx) []brow {
	var rows []brow
	prevHeading := false
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		block := r.block(n, width, ctx)
		if len(block) == 0 {
			continue
		}
		if len(rows) > 0 && (!ctx.tight || prevHeading) {
			rows = append(rows, brow{base: ctx.text})
		}
		rows = append(rows, block...)
		prevHeading = n.Kind() == ast.KindHeading
	}
	return rows
}

func (r *renderer) block(n ast.Node, width int, ctx blockCtx) []brow {
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return wrapCells(r.inlines(n, ctx.text), width, ctx.text)
	case *ast.Heading:
		style := r.derive(r.opt.Styles.Heading, ctx.text)
		if n.Level <= 2 {
			style = r.derive(r.opt.Styles.HeadingUnderline, style)
		}
		return wrapCells(r.inlines(n, style), width, style)
	case *ast.ThematicBreak:
		rule := r.derive(r.opt.Styles.Rule, ctx.text)
		glyph := proseCells(nil, r.opt.Glyphs.Rule, rule)
		return []brow{{cells: trimTrailingSpaces(repeatCells(glyph, width, rule)), base: rule}}
	case *ast.FencedCodeBlock:
		lang := ""
		if n.Info != nil && (!r.opt.PlainOpenFence || r.fenceClosed(n)) {
			lang = string(n.Language(r.src))
		}
		return r.codeBlock(r.lines(n.Lines()), lang, width, ctx)
	case *ast.CodeBlock:
		return r.codeBlock(r.lines(n.Lines()), "", width, ctx)
	case *ast.HTMLBlock:
		lines := r.lines(n.Lines())
		if n.HasClosure() {
			lines = append(lines, strings.TrimSuffix(string(n.ClosureLine.Value(r.src)), "\n"))
		}
		return r.literal(lines, width, ctx.text)
	case *ast.Blockquote:
		if ctx.depth >= maxBlockDepth {
			return r.literal(r.leafLines(n), width, ctx.text)
		}
		return r.blockquote(n, width, ctx)
	case *ast.List:
		if ctx.depth >= maxBlockDepth {
			return r.literal(r.leafLines(n), width, ctx.text)
		}
		return r.list(n, width, ctx)
	case *east.Table:
		return r.table(n, width, ctx)
	case *ast.LinkReferenceDefinition:
		return nil
	}
	if n.FirstChild() != nil {
		return r.children(n, width, ctx)
	}
	if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
		return r.literal(r.lines(n.Lines()), width, ctx.text)
	}
	return nil
}

// fenceClosed reports whether a fenced block has its closing fence: the
// source line after its last content line holds a fence. goldmark runs an
// unclosed fence to the end of its container, so that line is then missing
// or ordinary text.
func (r *renderer) fenceClosed(n *ast.FencedCodeBlock) bool {
	lines := n.Lines()
	if lines.Len() == 0 {
		return true // nothing to highlight
	}
	stop := lines.At(lines.Len() - 1).Stop
	if stop <= 0 || stop > len(r.src) || r.src[stop-1] != '\n' {
		return false // the last content line ends the source
	}
	next, _, _ := bytes.Cut(r.src[stop:], []byte("\n"))
	return bytes.Contains(next, []byte("```")) || bytes.Contains(next, []byte("~~~"))
}

// lines returns the source lines of a block without their newlines.
func (r *renderer) lines(segs *text.Segments) []string {
	out := make([]string, 0, segs.Len())
	for i := 0; i < segs.Len(); i++ {
		seg := segs.At(i)
		out = append(out, strings.TrimSuffix(string(seg.Value(r.src)), "\n"))
	}
	return out
}

// literal lays out lines as plain, uninterpreted text: one hard line each,
// word-wrapped.
func (r *renderer) literal(lines []string, width, style int) []brow {
	var cells []cell
	for i, line := range lines {
		if i > 0 {
			cells = append(cells, cell{brk: true})
		}
		cells = proseCells(cells, line, style)
	}
	return wrapCells(cells, width, style)
}

func (r *renderer) blockquote(n *ast.Blockquote, width int, ctx blockCtx) []brow {
	quote := r.derive(r.opt.Styles.Quote, ctx.text)
	bar := proseCells(nil, r.opt.Glyphs.QuoteBar, quote)
	first := append(append([]cell(nil), bar...), cell{r: ' ', w: 1, style: quote})
	inner := max(width-cellsWidth(first), 1)
	rows := r.children(n, inner, blockCtx{text: quote, depth: ctx.depth + 1})
	if len(rows) == 0 {
		rows = []brow{{base: quote}}
	}
	return prefixRows(rows, first, first, bar, width, inner, quote)
}

// list lays out list items. Item text hangs under the marker; ordered
// markers are right-aligned to the widest number so every item's text starts
// in the same column. A nested list after an item's first block is indented
// listIndent cells from the item's marker (spec § Markdown: nested indent 2
// cells), whatever the marker width.
func (r *renderer) list(n *ast.List, width int, ctx blockCtx) []brow {
	bullet := r.derive(r.opt.Styles.Bullet, ctx.text)
	digits := 0
	if n.IsOrdered() {
		digits = len(strconv.Itoa(n.Start + n.ChildCount() - 1))
	}
	var rows []brow
	i := 0
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		var marker []cell
		if n.IsOrdered() {
			num := strconv.Itoa(n.Start + i)
			marker = proseCells(nil, strings.Repeat(" ", max(digits-len(num), 0))+num+".", bullet)
		} else {
			marker = proseCells(nil, r.opt.Glyphs.Bullet, bullet)
		}
		i++
		first := append(marker, cell{r: ' ', w: 1, style: ctx.text})
		laid := r.listItem(item, first, width, blockCtx{text: ctx.text, tight: n.IsTight, depth: ctx.depth + 1})
		if len(rows) > 0 && !n.IsTight {
			rows = append(rows, brow{base: ctx.text})
		}
		rows = append(rows, laid...)
	}
	return rows
}

// listIndent is the indent of a nested list under its parent item's marker.
const listIndent = 2

// listItem lays out one item's blocks after marker (which includes its
// trailing space), separated like children. Blocks hang under the item text;
// a nested list that is not the item's first block hangs listIndent cells in.
func (r *renderer) listItem(item ast.Node, marker []cell, width int, ctx blockCtx) []brow {
	mw := cellsWidth(marker)
	var rows []brow
	prevHeading := false
	for c := item.FirstChild(); c != nil; c = c.NextSibling() {
		indent := mw
		if _, nested := c.(*ast.List); nested && len(rows) > 0 {
			indent = min(listIndent, mw)
		}
		inner := max(width-indent, 1)
		block := r.block(c, inner, ctx)
		if len(block) == 0 {
			continue
		}
		lead := spaceCells(indent, ctx.text)
		if len(rows) == 0 {
			lead = marker
		} else if !ctx.tight || prevHeading {
			rows = append(rows, brow{base: ctx.text})
		}
		laid := prefixRows(block, lead, spaceCells(indent, ctx.text), nil, width, inner, ctx.text)
		if len(rows) == 0 && len(block[0].cells) == 0 {
			laid[0].cells = trimTrailingSpaces(laid[0].cells)
		}
		rows = append(rows, laid...)
		prevHeading = c.Kind() == ast.KindHeading
	}
	if len(rows) == 0 {
		inner := max(width-mw, 1)
		laid := prefixRows([]brow{{base: ctx.text}}, marker, nil, nil, width, inner, ctx.text)
		laid[0].cells = trimTrailingSpaces(laid[0].cells)
		return laid
	}
	return rows
}

// inlines flattens the inline children of n into cells, styled by nesting
// from the parent style index. Hard line breaks become brk cells.
func (r *renderer) inlines(n ast.Node, parent int) []cell {
	var out []cell
	r.inlineChildren(n, parent, &out)
	return out
}

func (r *renderer) inlineChildren(n ast.Node, parent int, out *[]cell) {
	if r.nest >= maxInlineDepth {
		*out = proseCells(*out, r.plain(n), parent)
		return
	}
	r.nest++
	defer func() { r.nest-- }()
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		r.inline(c, parent, out)
	}
}

func (r *renderer) inline(n ast.Node, parent int, out *[]cell) {
	st := r.opt.Styles
	switch n := n.(type) {
	case *ast.Text:
		*out = proseCells(*out, r.textValue(n), parent)
		switch {
		case n.HardLineBreak():
			*out = append(*out, cell{brk: true})
		case n.SoftLineBreak():
			*out = append(*out, cell{r: ' ', w: 1, style: parent})
		}
	case *ast.String:
		*out = proseCells(*out, string(n.Value), parent)
	case *ast.CodeSpan:
		*out = proseCells(*out, r.plain(n), r.derive(st.InlineCode, parent))
	case *ast.Emphasis:
		style := st.Emphasis
		if n.Level >= 2 {
			style = st.Strong
		}
		r.inlineChildren(n, r.derive(style, parent), out)
	case *east.Strikethrough:
		r.inlineChildren(n, r.derive(st.Strike, parent), out)
	case *ast.Link:
		r.inlineChildren(n, r.derive(st.Link, parent), out)
		url := string(n.Destination)
		if url != "" && url != r.plain(n) {
			*out = append(*out, cell{r: ' ', w: 1, style: parent})
			*out = proseCells(*out, "("+url+")", r.derive(st.LinkURL, parent))
		}
	case *ast.AutoLink:
		*out = proseCells(*out, string(n.Label(r.src)), r.derive(st.Link, parent))
	case *ast.Image:
		*out = proseCells(*out, "[image: "+r.plain(n)+"]", r.derive(st.Muted, parent))
	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			*out = proseCells(*out, string(seg.Value(r.src)), parent)
		}
	case *east.TaskCheckBox:
		box := "[ ] "
		if n.IsChecked {
			box = "[x] "
		}
		*out = proseCells(*out, box, parent)
	default:
		r.inlineChildren(n, parent, out)
	}
}

// plain returns the unstyled text of n's inline content, newlines as spaces.
// It walks the subtree without recursion: inline nesting is as deep as the
// source makes it.
func (r *renderer) plain(n ast.Node) string {
	var b bytes.Buffer
	for c := n.FirstChild(); c != nil; {
		descend := false
		switch c := c.(type) {
		case *ast.Text:
			b.WriteString(r.textValue(c))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(c.Value)
		case *ast.AutoLink:
			b.Write(c.Label(r.src))
		case *ast.RawHTML:
			for i := 0; i < c.Segments.Len(); i++ {
				seg := c.Segments.At(i)
				b.Write(seg.Value(r.src))
			}
		default:
			descend = c.FirstChild() != nil
		}
		if descend {
			c = c.FirstChild()
			continue
		}
		c = nextInTree(c, n)
	}
	return strings.ReplaceAll(b.String(), "\n", " ")
}

// leafLines returns the source lines of every leaf block under n (paragraph,
// heading, code, HTML; a table as its pipe lines) in document order, walking
// without recursion. A container nested past maxBlockDepth renders them as
// plain text.
func (r *renderer) leafLines(n ast.Node) []string {
	var out []string
	for c := n.FirstChild(); c != nil; {
		descend := false
		switch t := c.(type) {
		case *east.Table:
			out = append(out, r.tableSource(t)...)
		default:
			if c.Type() == ast.TypeBlock && c.Lines().Len() > 0 {
				out = append(out, r.lines(c.Lines())...)
			} else {
				descend = c.Type() == ast.TypeBlock && c.FirstChild() != nil
			}
		}
		if descend {
			c = c.FirstChild()
			continue
		}
		c = nextInTree(c, n)
	}
	return out
}

// nextInTree returns the node after n's subtree in a preorder walk of root,
// or nil once the walk leaves root.
func nextInTree(n, root ast.Node) ast.Node {
	for n != nil && n != root {
		if s := n.NextSibling(); s != nil {
			return s
		}
		n = n.Parent()
	}
	return nil
}

// decode resolves backslash escapes and entity/numeric character references
// in one pass, as goldmark's HTML writer does for text: an escaped character
// never starts a reference, and a resolved reference is never unescaped.
// Resolved control characters are escaped later like any other content.
func decode(v []byte) string {
	resolve := func(b []byte) []byte {
		return util.ResolveNumericReferences(util.ResolveEntityNames(b))
	}
	var out []byte
	start := 0
	for i := 0; i+1 < len(v); i++ {
		if v[i] == '\\' && util.IsPunct(v[i+1]) {
			out = append(out, resolve(v[start:i])...)
			out = append(out, v[i+1])
			i++
			start = i + 1
		}
	}
	out = append(out, resolve(v[start:])...)
	return string(out)
}

// textValue returns the displayed text of a Text node.
func (r *renderer) textValue(n *ast.Text) string {
	if n.IsRaw() {
		return string(n.Value(r.src))
	}
	return decode(n.Value(r.src))
}
