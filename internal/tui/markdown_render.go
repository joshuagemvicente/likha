package tui

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"likha/internal/highlight"
	"likha/internal/markdown"
	likhaui "likha/internal/ui"
)

// Assistant markdown in the transcript (spec transcript-redesign § Markdown,
// § Assistant text): assistant entries, the streaming one included, render
// through internal/markdown into the text column after the ⏺ gutter, with
// code blocks highlighted by internal/highlight in theme colors. Expanded
// reasoning renders through the same path in Muted italics (§ Reasoning);
// the live reasoning tail and every other role keep plain wrapped prose.

// markdownCacheKey identifies one rendered assistant entry: the rows depend
// only on the text, the text column, the theme styles, the glyph set, and
// whether an open fence stays plain (the streaming entry).
type markdownCacheKey struct {
	content   string
	width     int
	theme     string
	ascii     bool
	plainOpen bool
	reasoning bool // expanded reasoning: the Muted italic styles
}

// markdownEntry is one cached assistant layout and the code blocks it
// highlighted, so a cache hit keeps their highlight results alive too.
// deferred marks rows laid out with at least one code block left plain
// because the layout's highlight budget ran out; a later layout with budget
// to spare renders the entry again.
type markdownEntry struct {
	rows     []toolRow
	code     []highlightKey
	deferred bool
}

// markdownHighlightBudget bounds the time one layout spends highlighting
// code blocks no earlier layout has seen. A resumed session can hold
// thousands of answers with code: highlighting them all in the first layout
// froze the UI goroutine for seconds. Blocks past the budget render as plain
// code panels (the same cells, so no row moves) and are highlighted by
// later layouts, newest entries first (see rebuild), each caret tick
// spending one more budget until none is left. Tests may change it.
var markdownHighlightBudget = 40 * time.Millisecond

// highlightKey identifies one highlighted code block. Highlighting depends
// on neither width nor theme, so its results outlive resizes, theme
// previews, and the streaming entry's re-renders.
type highlightKey struct {
	lang, code string
}

// highlightResult is a memoized highlightCode result; failures (unknown
// language, over the size or time budget) are kept too, so they are not
// retried every frame.
type highlightResult struct {
	segs [][]markdown.CodeSegment
	ok   bool
}

// markdownStyles maps the active theme onto markdown elements. Inline
// attribute styles (bold, italic, strike, underline) set no color, so they
// inherit the color of their container: Normal in prose, Title in headings,
// Muted in quotes. Code styles carry the BgCode panel background.
func markdownStyles(t likhaui.Theme) markdown.Styles {
	fg := func(s lipgloss.Style) lipgloss.Style { return lipgloss.NewStyle().Foreground(s.GetForeground()) }
	codeBG := t.BgCode.GetBackground()
	onCode := func(s lipgloss.Style) lipgloss.Style { return s.Background(codeBG) }
	s := markdown.Styles{
		Text:             t.Normal,
		Heading:          t.Title,
		HeadingUnderline: t.Title.Underline(true),
		Strong:           lipgloss.NewStyle().Bold(true),
		Emphasis:         lipgloss.NewStyle().Italic(true),
		Strike:           lipgloss.NewStyle().Strikethrough(true),
		InlineCode:       onCode(fg(t.Accent)),
		CodeBlock:        onCode(fg(t.Normal)),
		Quote:            t.Muted,
		Link:             lipgloss.NewStyle().Underline(true),
		LinkURL:          t.Muted,
		Rule:             t.Border,
		Muted:            t.Muted,
		Bullet:           t.Muted,
		TableBorder:      t.Border,
	}
	// Code colors per § Markdown: keywords Accent, strings Warning, comments
	// Muted; every other kind falls back to CodeBlock (Normal on BgCode).
	if t.Terminal16 {
		// The default family stays on the terminal's own 16 colors.
		ansi := func(c string) lipgloss.Style { return onCode(lipgloss.NewStyle().Foreground(lipgloss.Color(c))) }
		s.Code = map[markdown.CodeKind]lipgloss.Style{
			markdown.Keyword: ansi("5"),
			markdown.String:  ansi("2"),
			markdown.Comment: ansi("8").Italic(true),
		}
		return s
	}
	s.Code = map[markdown.CodeKind]lipgloss.Style{
		markdown.Keyword: onCode(fg(t.Accent)),
		markdown.String:  onCode(fg(t.Warning)),
		markdown.Comment: onCode(fg(t.Muted)).Italic(true),
	}
	return s
}

// reasoningMarkdownStyles restyles the assistant styles for expanded
// reasoning (spec § Reasoning, § Markdown): prose and headings keep the
// reasoning's Muted italics, so the block stays quieter than the answer,
// while bold, strike, underline, inline code, code panels, quotes, rules,
// and tables render as they do in assistant text. Inline attribute styles
// inherit the Muted italic container.
func reasoningMarkdownStyles(s markdown.Styles, t likhaui.Theme) markdown.Styles {
	body := t.Muted.Italic(true)
	s.Text = body
	s.Heading = body.Bold(true)
	s.HeadingUnderline = s.Heading.Underline(true)
	return s
}

// markdownGlyphs is the structural glyph set for the block glyph choice.
func markdownGlyphs(ascii bool) markdown.Glyphs {
	if ascii {
		return markdown.Glyphs{Bullet: "-", QuoteBar: "|", Rule: "-", Ellipsis: "...", TableV: "|", TableH: "-", TableCross: "+"}
	}
	return markdown.Glyphs{} // the renderer's Unicode defaults
}

// highlightKinds maps highlight kinds onto markdown code kinds one to one.
var highlightKinds = [...]markdown.CodeKind{
	highlight.Plain:       markdown.Plain,
	highlight.Keyword:     markdown.Keyword,
	highlight.String:      markdown.String,
	highlight.Comment:     markdown.Comment,
	highlight.Number:      markdown.Number,
	highlight.Name:        markdown.Name,
	highlight.Function:    markdown.Function,
	highlight.Type:        markdown.Type,
	highlight.Operator:    markdown.Operator,
	highlight.Punctuation: markdown.Punctuation,
	highlight.Builtin:     markdown.Builtin,
	highlight.Literal:     markdown.Literal,
}

// highlightCode adapts highlight.Lines to markdown.Options.Highlight.
func highlightCode(lang, code string) ([][]markdown.CodeSegment, bool) {
	lines, ok := highlight.Lines(lang, code)
	if !ok {
		return nil, false
	}
	out := make([][]markdown.CodeSegment, len(lines))
	for i, line := range lines {
		segs := make([]markdown.CodeSegment, len(line))
		for j, seg := range line {
			kind := markdown.Plain
			if int(seg.Kind) >= 0 && int(seg.Kind) < len(highlightKinds) {
				kind = highlightKinds[seg.Kind]
			}
			segs[j] = markdown.CodeSegment{Text: seg.Text, Kind: kind}
		}
		out[i] = segs
	}
	return out, true
}

// runHighlight is highlightCode; tests count calls through it.
var runHighlight = highlightCode

// markdownLayout is the per-rebuild rendering context for assistant entries.
// next and code collect what this layout used; rebuild swaps them in as the
// caches, so nothing outlives the entries that need it.
type markdownLayout struct {
	styles   markdown.Styles
	thoughts markdown.Styles // expanded reasoning (reasoningMarkdownStyles)
	glyphs   markdown.Glyphs
	themeKey string
	ascii    bool
	next     map[markdownCacheKey]markdownEntry
	code     map[highlightKey]highlightResult
	// deadline ends this layout's highlight budget; fresh counts the blocks
	// this layout highlighted, and deferred records that a block was left
	// plain for want of budget. The first fresh block always runs, so every
	// layout makes progress however long the rest of it took.
	deadline time.Time
	fresh    int
	deferred bool
}

func (m *ui) newMarkdownLayout() *markdownLayout {
	styles := markdownStyles(m.theme)
	return &markdownLayout{
		styles:   styles,
		thoughts: reasoningMarkdownStyles(styles, m.theme),
		glyphs:   markdownGlyphs(m.conn.ASCII),
		themeKey: markdownThemeKey(styles, m.theme),
		ascii:    m.conn.ASCII,
		next:     map[markdownCacheKey]markdownEntry{},
		code:     map[highlightKey]highlightResult{},
		deadline: time.Now().Add(markdownHighlightBudget),
	}
}

// commit replaces the caches with what this layout used, and records
// whether code is still waiting for a highlight (the caret tick then lays
// the transcript out again).
func (m *ui) commitMarkdownLayout(md *markdownLayout) {
	m.markdownCache = md.next
	m.highlightCache = md.code
	m.highlightPending = md.deferred
}

// overBudget reports that this layout may highlight no more new blocks.
func (md *markdownLayout) overBudget() bool {
	return md.fresh > 0 && time.Now().After(md.deadline)
}

// highlight returns the memoized highlight of one code block, running
// highlightCode only for a block no recent layout has seen. With the
// layout's budget spent it returns deferred instead and memoizes nothing,
// so a later layout highlights the block.
func (m *ui) highlight(md *markdownLayout, k highlightKey) (r highlightResult, deferred bool) {
	if r, ok := md.code[k]; ok {
		return r, false
	}
	r, ok := m.highlightCache[k]
	if !ok {
		if md.overBudget() {
			md.deferred = true
			return highlightResult{}, true
		}
		r.segs, r.ok = runHighlight(k.lang, k.code)
		md.fresh++
	}
	md.code[k] = r
	return r, false
}

// markdownThemeKey fingerprints everything theme-dependent in the rendered
// rows: the element styles and the canvas they are layered on. A preview,
// switch, or direct theme change yields a new key, so stale rows never paint.
func markdownThemeKey(s markdown.Styles, t likhaui.Theme) string {
	var b strings.Builder
	add := func(st lipgloss.Style) {
		fmt.Fprintf(&b, "%v/%v/%t%t%t%t;", st.GetForeground(), st.GetBackground(), st.GetBold(), st.GetItalic(), st.GetUnderline(), st.GetStrikethrough())
	}
	for _, st := range []lipgloss.Style{t.Base, s.Text, s.Heading, s.HeadingUnderline, s.Strong, s.Emphasis, s.Strike, s.InlineCode, s.CodeBlock, s.Quote, s.Link, s.LinkURL, s.Rule, s.Muted, s.Bullet, s.TableBorder} {
		add(st)
	}
	for kind := markdown.Plain; kind <= markdown.Literal; kind++ {
		if st, ok := s.Code[kind]; ok {
			fmt.Fprintf(&b, "%d:", kind)
			add(st)
		}
	}
	return b.String()
}

// assistantRows lays out one assistant entry: markdown rows in the text
// column, the glyph gutter before the first row and the hanging indent
// before the rest. Rows come from the previous rebuild's cache when the
// text, width, theme, and glyph set are unchanged; the streaming entry's
// text changes with every delta, so it re-renders as it grows, reusing the
// highlight of every closed code block and leaving its open fence plain.
func (m *ui) assistantRows(md *markdownLayout, content, glyph string, width int, streaming bool) []toolRow {
	return m.markdownRows(md, content, glyph+" ", width, streaming, false)
}

// reasoningBodyRows lays out an expanded reasoning block's text as markdown
// in Muted italics, hanging under the two-cell pad after the marker row.
// The block is closed, so open fences render like any other.
func (m *ui) reasoningBodyRows(md *markdownLayout, content string, width int) []toolRow {
	return m.markdownRows(md, content, "  ", width, false, true)
}

// markdownRows renders content into the text column after gutter, through
// the layout caches, in the assistant or the expanded reasoning styles.
func (m *ui) markdownRows(md *markdownLayout, content, gutter string, width int, streaming, reasoning bool) []toolRow {
	key := markdownCacheKey{content: content, width: width, theme: md.themeKey, ascii: md.ascii, plainOpen: streaming, reasoning: reasoning}
	if e, ok := md.next[key]; ok {
		return e.rows
	}
	e, ok := m.markdownCache[key]
	if ok && e.deferred && !md.overBudget() {
		ok = false // budget to spare: highlight what this entry left plain
	}
	if ok {
		for _, k := range e.code {
			m.highlight(md, k)
		}
		md.deferred = md.deferred || e.deferred
	} else {
		e = markdownEntry{}
		indent := runewidth.StringWidth(gutter)
		styles := md.styles
		if reasoning {
			styles = md.thoughts
		}
		rendered := markdown.Render(content, markdown.Options{
			Width:  max(1, width-indent),
			Styles: styles,
			Glyphs: md.glyphs,
			Highlight: func(lang, code string) ([][]markdown.CodeSegment, bool) {
				k := highlightKey{lang: lang, code: code}
				r, deferred := m.highlight(md, k)
				if deferred {
					e.deferred = true
					return nil, false
				}
				e.code = append(e.code, k)
				return r.segs, r.ok
			},
			PlainOpenFence: streaming,
		})
		e.rows = m.markdownToRows(rendered, gutter, md.styles.CodeBlock.GetBackground())
	}
	md.next[key] = e
	return e.rows
}

// markdownToRows converts rendered markdown rows into transcript rows. The
// row style is the assistant canvas, which paints the gutter, the hanging
// indent, and the padding past the text column; runs cover every text cell
// in its markdown style (Row.Base outside the markdown runs) shifted past
// the gutter. Prose rows also carry inline color swatches, which override
// the cell style; code panel rows never do.
func (m *ui) markdownToRows(rendered []markdown.Row, gutter string, codeBG lipgloss.TerminalColor) []toolRow {
	canvas := withBase(m.theme.Normal, m.theme.Base)
	on := func(s lipgloss.Style) lipgloss.Style { return withBase(s, m.theme.Base) }
	if len(rendered) == 0 {
		return []toolRow{{text: gutter, style: canvas}}
	}
	pad := strings.Repeat(" ", runewidth.StringWidth(gutter))
	rows := make([]toolRow, 0, len(rendered))
	// One shared copy per distinct markdown style (by StyleID).
	styles := map[int]*lipgloss.Style{}
	style := func(id int, s lipgloss.Style) *lipgloss.Style {
		p, ok := styles[id]
		if !ok {
			p = shared(on(s))
			styles[id] = p
		}
		return p
	}
	for i, r := range rendered {
		prefix := pad
		if i == 0 {
			prefix = gutter
		}
		if r.Text == "" {
			text := ""
			if i == 0 {
				text = gutter
			}
			rows = append(rows, toolRow{text: text, style: canvas})
			continue
		}
		off := utf8.RuneCountInString(prefix)
		n := utf8.RuneCountInString(r.Text)
		base := style(r.BaseID, r.Base)
		var runs []lineRun
		pos := 0
		for _, run := range r.Runs {
			if run.Start < pos || run.Start >= run.End || run.End > n {
				continue
			}
			if run.Start > pos {
				runs = append(runs, lineRun{start: off + pos, end: off + run.Start, style: base})
			}
			runs = append(runs, lineRun{start: off + run.Start, end: off + run.End, style: style(run.StyleID, run.Style)})
			pos = run.End
		}
		if pos < n {
			runs = append(runs, lineRun{start: off + pos, end: off + n, style: base})
		}
		if !isCodePanel(r.Base, codeBG) {
			runs = overlaySwatches(runs, likhaui.FindSwatches(r.Text), off)
		}
		rows = append(rows, toolRow{text: prefix + r.Text, style: canvas, runs: runs})
	}
	return rows
}

// isCodePanel reports a code panel row: its base carries the code panel
// background. Without a code background nothing reads as a panel, so
// swatches stay on.
func isCodePanel(base lipgloss.Style, codeBG lipgloss.TerminalColor) bool {
	if codeBG == nil {
		return false
	}
	if _, none := codeBG.(lipgloss.NoColor); none {
		return false
	}
	return reflect.DeepEqual(base.GetBackground(), codeBG)
}

// overlaySwatches paints each swatch span (rune offsets into the row text
// after off) in its swatch style, cutting the runs it covers so runs stay
// sorted and disjoint. Text and widths are untouched.
func overlaySwatches(runs []lineRun, spans []likhaui.Swatch, off int) []lineRun {
	if len(spans) == 0 {
		return runs
	}
	out := make([]lineRun, 0, len(runs)+2*len(spans))
	for _, run := range runs {
		start := run.start
		for _, sp := range spans {
			s, e := sp.Start+off, sp.End+off
			if e <= start || s >= run.end {
				continue
			}
			if s > start {
				out = append(out, lineRun{start: start, end: s, style: run.style, blink: run.blink})
			}
			start = max(start, e)
		}
		if start < run.end {
			out = append(out, lineRun{start: start, end: run.end, style: run.style, blink: run.blink})
		}
	}
	for _, sp := range spans {
		if sp.Start < sp.End {
			out = append(out, lineRun{start: sp.Start + off, end: sp.End + off, style: shared(likhaui.SwatchStyle(sp.Hex))})
		}
	}
	slices.SortFunc(out, func(a, b lineRun) int { return a.start - b.start })
	return out
}
