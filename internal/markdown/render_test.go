package markdown

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// testStyles gives every element a property the tests can read back.
func testStyles() Styles {
	return Styles{
		Text:             lipgloss.NewStyle(),
		Heading:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		HeadingUnderline: lipgloss.NewStyle().Underline(true),
		Strong:           lipgloss.NewStyle().Bold(true),
		Emphasis:         lipgloss.NewStyle().Italic(true),
		Strike:           lipgloss.NewStyle().Strikethrough(true),
		InlineCode:       lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Background(lipgloss.Color("3")),
		CodeBlock:        lipgloss.NewStyle().Background(lipgloss.Color("4")),
		Quote:            lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		Link:             lipgloss.NewStyle().Underline(true),
		LinkURL:          lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		Rule:             lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		Muted:            lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Bullet:           lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		TableBorder:      lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		Code: map[CodeKind]lipgloss.Style{
			Keyword: lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
			String:  lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		},
	}
}

func opts(width int) Options {
	return Options{Width: width, Styles: testStyles()}
}

func texts(rows []Row) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.Text
	}
	return out
}

func cells(s string) int {
	n := 0
	for _, r := range s {
		n += runewidth.RuneWidth(r)
	}
	return n
}

// styleAt returns the style painting rune i of row: its run, else Base.
func styleAt(row Row, i int) lipgloss.Style {
	for _, run := range row.Runs {
		if i >= run.Start && i < run.End {
			return run.Style
		}
	}
	return row.Base
}

// styleOf returns the style painting the first occurrence of sub in row.
func styleOf(t *testing.T, row Row, sub string) lipgloss.Style {
	t.Helper()
	at := strings.Index(row.Text, sub)
	if at < 0 {
		t.Fatalf("row %q has no %q", row.Text, sub)
	}
	return styleAt(row, utf8.RuneCountInString(row.Text[:at]))
}

func fg(s lipgloss.Style) string {
	if c, ok := s.GetForeground().(lipgloss.Color); ok {
		return string(c)
	}
	return ""
}

func bg(s lipgloss.Style) string {
	if c, ok := s.GetBackground().(lipgloss.Color); ok {
		return string(c)
	}
	return ""
}

// checkRows asserts the Render contract every caller relies on.
func checkRows(t *testing.T, src string, width int, rows []Row) {
	t.Helper()
	for i, row := range rows {
		if w := cells(row.Text); w > width {
			t.Fatalf("width %d: row %d %q is %d cells\nsource: %q", width, i, row.Text, w, src)
		}
		for _, r := range row.Text {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("row %d %q carries control rune %U\nsource: %q", i, row.Text, r, src)
			}
		}
		n := utf8.RuneCountInString(row.Text)
		prev := 0
		for _, run := range row.Runs {
			if run.Start < prev || run.Start >= run.End || run.End > n {
				t.Fatalf("row %d %q has bad runs %+v\nsource: %q", i, row.Text, row.Runs, src)
			}
			prev = run.End
		}
	}
	if len(rows) > 0 && (rows[0].Text == "" || rows[len(rows)-1].Text == "") {
		t.Fatalf("leading or trailing blank row: %q\nsource: %q", texts(rows), src)
	}
}

func render(t *testing.T, src string, opt Options) []Row {
	t.Helper()
	rows := Render(src, opt)
	checkRows(t, src, opt.Width, rows)
	return rows
}

func assertTexts(t *testing.T, got []Row, want []string) {
	t.Helper()
	if strings.Join(texts(got), "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", quoteRows(texts(got)), quoteRows(want))
	}
}

func quoteRows(rows []string) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "  %q\n", r)
	}
	return b.String()
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", width-cells(s))
}

func TestRenderEmpty(t *testing.T) {
	for _, src := range []string{"", "   ", "\n\n\n", " \t \r\n "} {
		if rows := Render(src, opts(40)); rows != nil {
			t.Fatalf("Render(%q) = %q, want no rows", src, texts(rows))
		}
	}
}

func TestRenderElements(t *testing.T) {
	for _, width := range []int{40, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			rule := strings.Repeat("─", width)
			cases := []struct {
				name string
				src  string
				want []string
			}{
				{"headings", "# One\n## Two\n### Three\nbody", []string{"One", "", "Two", "", "Three", "", "body"}},
				{"setext heading", "Title\n=====\n\nbody", []string{"Title", "", "body"}},
				{"inline styles", "**b** *i* ~~s~~ `c  d`", []string{"b i s c  d"}},
				{"soft and hard breaks", "one\ntwo  \nthree\\\nfour", []string{"one two", "three", "four"}},
				{"link", "see [docs](https://x.io/a)", []string{"see docs (https://x.io/a)"}},
				{"link text is url", "[https://x.io](https://x.io)", []string{"https://x.io"}},
				{"autolink", "<https://x.io/b>", []string{"https://x.io/b"}},
				{"image", "![a cat](cat.png)", []string{"[image: a cat]"}},
				{"entities and escapes", `a &amp; b \*c\* &#65; \&amp;`, []string{"a & b *c* A &amp;"}},
				{"bullets", "- one\n- two\n  - deep\n- three", []string{"• one", "• two", "  • deep", "• three"}},
				{"ordered start", "3. a\n4. b\n   1. x", []string{"3. a", "4. b", "  1. x"}},
				{"loose list", "- a\n\n- b", []string{"• a", "", "• b"}},
				{"task list", "- [x] done\n- [ ] todo", []string{"• [x] done", "• [ ] todo"}},
				{"quote", "> a\n>\n> b", []string{"│ a", "│", "│ b"}},
				{"nested quote", "> a\n>\n> > b", []string{"│ a", "│", "│ │ b"}},
				{"quoted list", "> - a\n>   - b", []string{"│ • a", "│   • b"}},
				{"fenced code", "```\nx := 1\n\ty\n```", []string{pad("x := 1", width), pad("    y", width)}},
				{"indented code", "    a\tb", []string{pad("a   b", width)}},
				{"empty fence", "```\n```", []string{pad("", width)}},
				{"table", "| a | bb |\n|:--|--:|\n| ccc | d |", []string{"a   │ bb", "────┼───", "ccc │  d"}},
				{"center table", "| h |\n|:-:|\n| abcde |", []string{"  h", "─────", "abcde"}},
				{"thematic break", "a\n\n---\n\nb", []string{"a", "", rule, "", "b"}},
				{"html block", "<div>\n*x*\n</div>", []string{"<div>", "*x*", "</div>"}},
				{"inline html", "a <b>bold</b> c", []string{"a <b>bold</b> c"}},
				{"prose tab", "a\tb", []string{"a b"}},
				{"blocks", "para\n\n- item\n\n> q", []string{"para", "", "• item", "", "│ q"}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					assertTexts(t, render(t, tc.src, opts(width)), tc.want)
				})
			}
		})
	}
}

func TestRenderElementStyles(t *testing.T) {
	rows := render(t, "# Big\n\n### Small `x`", opts(40))
	if s := rows[0].Base; !s.GetBold() || !s.GetUnderline() || fg(s) != "1" {
		t.Fatalf("h1 style bold=%v underline=%v fg=%q", s.GetBold(), s.GetUnderline(), fg(s))
	}
	if s := rows[2].Base; !s.GetBold() || s.GetUnderline() {
		t.Fatalf("h3 should be bold without underline")
	}
	if s := styleOf(t, rows[2], "x"); fg(s) != "2" || bg(s) != "3" || !s.GetBold() {
		t.Fatalf("code in heading: fg=%q bg=%q bold=%v", fg(s), bg(s), s.GetBold())
	}

	row := render(t, "p **b *bi*** *i* ~~s~~ `c` [l](u) <https://a.b> ![alt](p)", opts(80))[0]
	if s := styleOf(t, row, "p"); s.GetBold() || s.GetItalic() {
		t.Fatalf("plain text styled")
	}
	if s := styleOf(t, row, "b "); !s.GetBold() || s.GetItalic() {
		t.Fatalf("strong: bold=%v italic=%v", s.GetBold(), s.GetItalic())
	}
	if s := styleOf(t, row, "bi"); !s.GetBold() || !s.GetItalic() {
		t.Fatalf("nested strong+emphasis: bold=%v italic=%v", s.GetBold(), s.GetItalic())
	}
	if s := styleOf(t, row, "i "); !s.GetItalic() {
		t.Fatalf("emphasis not italic")
	}
	if s := styleOf(t, row, "s"); !s.GetStrikethrough() {
		t.Fatalf("strike not struck")
	}
	if s := styleOf(t, row, "c"); fg(s) != "2" || bg(s) != "3" {
		t.Fatalf("inline code fg=%q bg=%q", fg(s), bg(s))
	}
	if s := styleOf(t, row, "l"); !s.GetUnderline() {
		t.Fatalf("link text not underlined")
	}
	if s := styleOf(t, row, "(u)"); fg(s) != "6" || s.GetUnderline() {
		t.Fatalf("link url fg=%q underline=%v", fg(s), s.GetUnderline())
	}
	if s := styleOf(t, row, "https://a.b"); !s.GetUnderline() {
		t.Fatalf("autolink not in link style")
	}
	if s := styleOf(t, row, "[image: alt]"); fg(s) != "8" {
		t.Fatalf("image fg=%q", fg(s))
	}

	rows = render(t, "- a\n\n> q\n\n---", opts(20))
	if s := styleAt(rows[0], 0); fg(s) != "9" {
		t.Fatalf("bullet fg=%q", fg(s))
	}
	if s := styleOf(t, rows[2], "│"); fg(s) != "5" {
		t.Fatalf("quote bar fg=%q", fg(s))
	}
	if s := styleOf(t, rows[2], "q"); fg(s) != "5" {
		t.Fatalf("quote text fg=%q", fg(s))
	}
	if s := styleAt(rows[4], 0); fg(s) != "7" {
		t.Fatalf("rule fg=%q", fg(s))
	}

	rows = render(t, "| h | k |\n|---|---|\n| a | b |", opts(40))
	if s := styleOf(t, rows[0], "h"); !s.GetBold() {
		t.Fatalf("table header not strong")
	}
	if s := styleOf(t, rows[2], "a"); s.GetBold() {
		t.Fatalf("table body strong")
	}
	if s := styleOf(t, rows[0], "│"); fg(s) != "10" {
		t.Fatalf("table border fg=%q", fg(s))
	}
	if s := styleOf(t, rows[1], "┼"); fg(s) != "10" {
		t.Fatalf("table rule fg=%q", fg(s))
	}
}

func TestRenderParagraphWrap(t *testing.T) {
	src := "The quick **brown fox jumps over** the lazy dog and keeps on running"
	rows := render(t, src, opts(20))
	assertTexts(t, rows, []string{
		"The quick brown fox",
		"jumps over the lazy",
		"dog and keeps on",
		"running",
	})
	if s := styleOf(t, rows[0], "brown"); !s.GetBold() {
		t.Fatalf("bold lost on first row")
	}
	if s := styleOf(t, rows[1], "jumps"); !s.GetBold() {
		t.Fatalf("bold not carried across the wrap")
	}
	if s := styleOf(t, rows[1], "lazy"); s.GetBold() {
		t.Fatalf("bold leaked past its span")
	}

	rows = render(t, "a abcdefghijklmnop", opts(8))
	assertTexts(t, rows, []string{"a abcdef", "ghijklmn", "op"})
}

func TestRenderListContinuationHangs(t *testing.T) {
	rows := render(t, "- one two three four five six\n  - seven eight nine ten", opts(16))
	assertTexts(t, rows, []string{
		"• one two three",
		"  four five six",
		"  • seven eight",
		"    nine ten",
	})
	rows = render(t, "9. nine\n10. ten words wrap here", opts(14))
	assertTexts(t, rows, []string{" 9. nine", "10. ten words", "    wrap here"})
}

// Spec § Markdown: nested lists indent 2 cells per level, whatever the
// parent marker's width; wrapped item text still hangs under its own text.
func TestRenderNestedListIndentTwoCells(t *testing.T) {
	rows := render(t, "9. a\n10. b\n    - c\n      1. d\n         - e", opts(40))
	assertTexts(t, rows, []string{" 9. a", "10. b", "  • c", "    1. d", "      • e"})
	rows = render(t, "- a\n  - b\n    - c", opts(40))
	assertTexts(t, rows, []string{"• a", "  • b", "    • c"})
	rows = render(t, "1. one\n\n   para\n\n   - nested", opts(40))
	assertTexts(t, rows, []string{"1. one", "", "   para", "", "  • nested"})
	rows = render(t, "1. one\n   - nested item wraps here", opts(14))
	assertTexts(t, rows, []string{"1. one", "  • nested", "    item wraps", "    here"})
}

func TestRenderCodeBlockPanel(t *testing.T) {
	src := "```\n" + strings.Repeat("x", 25) + "\n```"
	rows := render(t, src, opts(10))
	assertTexts(t, rows, []string{"xxxxxxxxxx", "xxxxxxxxxx", "xxxxx     "})
	for _, row := range rows {
		if bg(row.Base) != "4" || len(row.Runs) != 0 {
			t.Fatalf("code row %q base bg=%q runs=%v", row.Text, bg(row.Base), row.Runs)
		}
	}
	// Code panels never word-wrap.
	rows = render(t, "```\naaa bbb ccc\n```", opts(6))
	assertTexts(t, rows, []string{"aaa bb", "b ccc "})

	// Tabs expand to 4-column stops from the line start.
	rows = render(t, "```\na\tb\nabcd\tc\n\td\n```", opts(12))
	assertTexts(t, rows, []string{pad("a   b", 12), pad("abcd    c", 12), pad("    d", 12)})

	// Inside containers the panel spans the text column; the prefix keeps the
	// container style, not the code background.
	rows = render(t, "> ```\n> q\n> ```", opts(10))
	assertTexts(t, rows, []string{"│ q       "})
	if s := styleAt(rows[0], 0); bg(s) != "" || fg(s) != "5" {
		t.Fatalf("quote bar on code row: fg=%q bg=%q", fg(s), bg(s))
	}
	if s := styleAt(rows[0], 2); bg(s) != "4" {
		t.Fatalf("code text bg=%q", bg(s))
	}
	rows = render(t, "- ```\n  z\n  ```", opts(8))
	assertTexts(t, rows, []string{"• z     "})
	if s := styleAt(rows[0], 1); bg(s) != "" {
		t.Fatalf("list gap painted with code background")
	}
}

func TestRenderHighlightInjection(t *testing.T) {
	var gotLang, gotCode string
	opt := opts(30)
	opt.Highlight = func(lang, code string) ([][]CodeSegment, bool) {
		gotLang, gotCode = lang, code
		var out [][]CodeSegment
		for _, line := range strings.Split(code, "\n") {
			var segs []CodeSegment
			for i, word := range strings.Split(line, " ") {
				if i > 0 {
					segs = append(segs, CodeSegment{Text: " "})
				}
				kind := Plain
				switch {
				case word == "func":
					kind = Keyword
				case strings.HasPrefix(word, `"`):
					kind = String
				case word == "//":
					kind = Comment // no Code entry: falls back to CodeBlock
				}
				segs = append(segs, CodeSegment{Text: word, Kind: kind})
			}
			out = append(out, segs)
		}
		return out, true
	}
	rows := render(t, "```go title=x\nfunc main\nx := \"s\" //\n```", opt)
	if gotLang != "go" || gotCode != "func main\nx := \"s\" //" {
		t.Fatalf("Highlight got lang=%q code=%q", gotLang, gotCode)
	}
	assertTexts(t, rows, []string{pad("func main", 30), pad(`x := "s" //`, 30)})
	if s := styleOf(t, rows[0], "func"); fg(s) != "11" || bg(s) != "4" {
		t.Fatalf("keyword fg=%q bg=%q (want token color on panel background)", fg(s), bg(s))
	}
	if s := styleOf(t, rows[0], "main"); fg(s) != "" || bg(s) != "4" {
		t.Fatalf("plain token fg=%q bg=%q", fg(s), bg(s))
	}
	if s := styleOf(t, rows[1], `"s"`); fg(s) != "12" {
		t.Fatalf("string fg=%q", fg(s))
	}
	if s := styleOf(t, rows[1], "//"); fg(s) != "" || bg(s) != "4" {
		t.Fatalf("unmapped kind fg=%q bg=%q", fg(s), bg(s))
	}

	// Indented code has no language; the highlighter still decides.
	gotLang = "unset"
	render(t, "    func", opt)
	if gotLang != "" {
		t.Fatalf("indented code lang = %q", gotLang)
	}

	plainCases := map[string]func(string, string) ([][]CodeSegment, bool){
		"declined": func(string, string) ([][]CodeSegment, bool) { return nil, false },
		"wrong text": func(string, string) ([][]CodeSegment, bool) {
			return [][]CodeSegment{{{Text: "evil", Kind: Keyword}}}, true
		},
		"wrong line count": func(_, code string) ([][]CodeSegment, bool) {
			return [][]CodeSegment{{{Text: code, Kind: Keyword}}}, true
		},
	}
	for name, hl := range plainCases {
		t.Run(name, func(t *testing.T) {
			opt := opts(20)
			opt.Highlight = hl
			rows := render(t, "```go\nfunc a\nb\n```", opt)
			assertTexts(t, rows, []string{pad("func a", 20), pad("b", 20)})
			for _, row := range rows {
				if len(row.Runs) != 0 {
					t.Fatalf("rejected highlight still styled %q: %v", row.Text, row.Runs)
				}
			}
		})
	}

	// A trailing empty line from the highlighter is tolerated.
	opt.Highlight = func(_, code string) ([][]CodeSegment, bool) {
		return [][]CodeSegment{{{Text: code, Kind: Keyword}}, nil}, true
	}
	rows = render(t, "```\nfunc\n```", opt)
	if s := styleOf(t, rows[0], "func"); fg(s) != "11" {
		t.Fatalf("trailing empty highlight line rejected")
	}
}

func TestRenderUnclosedFence(t *testing.T) {
	src := "Intro text\n\n```go\nfunc a() {\n# not a heading\n- not a list"
	rows := render(t, src, opts(24))
	assertTexts(t, rows, []string{
		"Intro text",
		"",
		pad("func a() {", 24),
		pad("# not a heading", 24),
		pad("- not a list", 24),
	})
	for _, row := range rows[2:] {
		if bg(row.Base) != "4" {
			t.Fatalf("unclosed fence row %q not in a code panel", row.Text)
		}
	}
	// Streaming: a fence that just opened, then the closed version.
	assertTexts(t, render(t, "```", opts(10)), []string{pad("", 10)})
	rows = render(t, src+"\n```\n\nAfter **bold**", opts(24))
	last := rows[len(rows)-1]
	if last.Text != "After bold" || bg(last.Base) != "" {
		t.Fatalf("text after the closed fence = %q (bg %q)", last.Text, bg(last.Base))
	}
	if s := styleOf(t, last, "bold"); !s.GetBold() {
		t.Fatalf("markdown after the fence not rendered")
	}
}

func TestRenderWideTableFallsBack(t *testing.T) {
	src := "| name | description |\n|---|:-:|\n| alpha | the first letter |\n| beta | second |"
	rows := render(t, src, opts(80))
	assertTexts(t, rows, []string{
		"name  │   description",
		"──────┼─────────────────",
		"alpha │ the first letter",
		"beta  │      second",
	})
	rows = render(t, src, opts(20))
	assertTexts(t, rows, []string{
		"| name | description",
		"|",
		"| --- | :-: |",
		"| alpha | the first",
		"letter |",
		"| beta | second |",
	})
	for _, row := range rows {
		if strings.ContainsAny(row.Text, "│─┼") || len(row.Runs) != 0 {
			t.Fatalf("fallback row %q drew borders or styles", row.Text)
		}
	}
	// The fallback reads source rows, not the container markers around them.
	rows = render(t, "> | a | b |\n> |---|---|\n> | xxxxxxxx | yyyyyyyy |", opts(14))
	assertTexts(t, rows, []string{"│ | a | b |", "│ | --- | ---", "│ |", "│ | xxxxxxxx |", "│ yyyyyyyy |"})
}

func TestRenderCJKAndEmojiWidths(t *testing.T) {
	rows := render(t, "日本語のテキスト です 🎉🎉 終わり", opts(10))
	assertTexts(t, rows, []string{"日本語のテ", "キスト", "です 🎉🎉", "終わり"})

	// A wide rune never splits; at width 1 it shows escaped.
	rows = render(t, "a日", opts(2))
	assertTexts(t, rows, []string{"a", "日"})
	rows = render(t, "日", opts(1))
	assertTexts(t, rows, []string{"\\", "u", "6", "5", "E", "5"})

	rows = render(t, "```\n日本語日本語\n```", opts(5))
	assertTexts(t, rows, []string{"日本 ", "語日 ", "本語 "})

	rows = render(t, "| 名前 | 🎉 |\n|---|---|\n| a | b |", opts(20))
	assertTexts(t, rows, []string{"名前 │ 🎉", "─────┼───", "a    │ b"})
}

func TestRenderLongURL(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("segment/", 12) + "end"
	for _, width := range []int{20, 40} {
		rows := render(t, "see <"+url+"> now", opts(width))
		joined := strings.Join(texts(rows), "")
		if !strings.Contains(joined, url) {
			t.Fatalf("width %d: url not intact across rows: %q", width, texts(rows))
		}
		if rows[0].Text != "see "+url[:width-4] {
			t.Fatalf("width %d: long url should start on the first row: %q", width, rows[0].Text)
		}
		rows = render(t, "[docs]("+url+")", opts(width))
		if !strings.HasPrefix(rows[0].Text, "docs (https://") || cells(rows[0].Text) != width {
			t.Fatalf("width %d: link url should start after the text and fill the row: %q", width, texts(rows))
		}
		if strings.Join(texts(rows), "") != "docs ("+url+")" {
			t.Fatalf("width %d: link url not intact: %q", width, texts(rows))
		}
	}
}

func TestRenderNeverEmitsEscapes(t *testing.T) {
	payloads := []string{
		"\x1b[31mred\x1b[0m",
		"\x9b31mcsi",
		"\u009b31mcsi",
		"\x1b]0;title\x07",
		"\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\",
		"\x1bP+q\x1b\\",
		"&#27;[31m &#x9b;",
		"bell\x07 nul\x00 del\x7f zw\u200b bidi\u202e",
	}
	templates := []string{
		"%s",
		"# %s",
		"**%s** *%s*",
		"`%s`",
		"[%s](https://x.io/%s)",
		"<https://x.io/%s>",
		"![%s](p)",
		"- %s\n  - %s",
		"> %s",
		"```%s\n%s\n```",
		"    %s",
		"| %s | h |\n|---|---|\n| %s | c |",
		"<div>%s</div>",
		"a <span %s>",
		"```\n%s",
	}
	for _, p := range payloads {
		for _, tmpl := range templates {
			src := strings.ReplaceAll(tmpl, "%s", p)
			for _, width := range []int{12, 40} {
				opt := opts(width)
				opt.Highlight = func(_, code string) ([][]CodeSegment, bool) {
					var out [][]CodeSegment
					for _, line := range strings.Split(code, "\n") {
						out = append(out, []CodeSegment{{Text: line, Kind: Keyword}})
					}
					return out, true
				}
				rows := render(t, src, opt)
				for _, row := range rows {
					for _, bad := range []string{"\x1b", "\x9b", "\u009b", "\x07", "\x00", "\x7f", "\u200b", "\u202e"} {
						if strings.Contains(row.Text, bad) {
							t.Fatalf("%q reached output: %q from %q", bad, row.Text, src)
						}
					}
				}
			}
		}
	}
	rows := render(t, "a\x1b[31mb", opts(40))
	assertTexts(t, rows, []string{`a\u001B[31mb`})
	rows = render(t, "```\n\u009b\n```", opts(10))
	assertTexts(t, rows, []string{`\u009B    `})
}

func TestRenderASCIIGlyphs(t *testing.T) {
	opt := opts(20)
	opt.Glyphs = Glyphs{Bullet: "-", QuoteBar: "|", Rule: "-", Ellipsis: "...", TableV: "|", TableH: "-", TableCross: "+"}
	rows := render(t, "- a\n\n> b\n\n---\n\n| x | y |\n|---|---|\n| 1 | 2 |", opt)
	assertTexts(t, rows, []string{"- a", "", "| b", "", strings.Repeat("-", 20), "", "x | y", "--+--", "1 | 2"})
	for _, row := range rows {
		for _, r := range row.Text {
			if r > unicode.MaxASCII {
				t.Fatalf("non-ASCII rune %q in %q", r, row.Text)
			}
		}
	}
}

func TestRenderNarrowWidthsClipPrefixes(t *testing.T) {
	src := "> > > - - - deep **nested** text here\n\n```\ncode\n```"
	for width := 1; width <= 8; width++ {
		rows := render(t, src, opts(width))
		if len(rows) == 0 {
			t.Fatalf("width %d: no rows", width)
		}
	}
	// Width below 1 counts as 1.
	rows := Render("ab", Options{Width: 0})
	checkRows(t, "ab", 1, rows)
	assertTexts(t, rows, []string{"a", "b"})
}

// TestRenderRandomCorpusFits renders random markdown built from every
// element (and some hostile bytes) at widths 10..120 and checks the row
// contract each time.
func TestRenderRandomCorpusFits(t *testing.T) {
	pieces := []string{
		"# ", "## ", "### ", "- ", "* ", "1. ", "12. ", "> ", "> > ", "  ", "    ", "\t",
		"```", "```go", "~~~", "---", "***", "|", " | ", "|---|---|", ":-:",
		"**", "*", "_", "~~", "`", "[", "](", ")", "![", "<", ">", "<https://example.com/a/very/long/path/that/keeps/going>",
		"word", "another", "supercalifragilisticexpialidocious", "日本語", "テキスト", "🎉", "👨\u200d👩\u200d👧", "é", "e\u0301",
		"&amp;", "&#27;", "\\", "\\*", "<b>", "</b>", "<div>", "[ ] ", "[x] ",
		"\x1b[31m", "\x9b", "\x1b]0;t\x07", "\x00", "\r", "\u200b",
		" ", " ", " ", "\n", "\n", "\n\n", "\n\n",
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 400; i++ {
		var b strings.Builder
		for n := rng.Intn(60); n >= 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		src := b.String()
		for width := 10; width <= 120; width += 1 + rng.Intn(13) {
			opt := opts(width)
			if i%2 == 0 {
				opt.Glyphs = Glyphs{Bullet: "-", QuoteBar: "|", Rule: "-", Ellipsis: "...", TableV: "|", TableH: "-", TableCross: "+"}
			}
			render(t, src, opt)
		}
	}
}

func TestDecode(t *testing.T) {
	cases := map[string]string{
		`a\*b`:      "a*b",
		`\\`:        `\`,
		`&amp;lt;`:  "&lt;",
		`\&amp;`:    "&amp;",
		`&#92;*`:    `\*`,
		`&copy; \a`: `© \a`,
	}
	for in, want := range cases {
		if got := decode([]byte(in)); got != want {
			t.Errorf("decode(%q) = %q, want %q", in, got, want)
		}
	}
}

// PlainOpenFence leaves only an unclosed fence unhighlighted; closed fences,
// including ones in containers, still reach Highlight.
func TestRenderPlainOpenFence(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string // langs Highlight sees
	}{
		{"closed", "```go\nx\n```", []string{"go"}},
		{"closed tilde", "~~~go\nx\n~~~\n\nafter", []string{"go"}},
		{"open at end", "```go\nx", []string{""}},
		{"open trailing newline", "```go\nx\n", []string{""}},
		{"closed then open", "```go\nx\n```\n\n```py\ny\n", []string{"go", ""}},
		{"closed in quote", "> ```go\n> x\n> ```", []string{"go"}},
		{"open in list", "- ```go\n  x\nfoo", []string{""}},
	}
	for _, c := range cases {
		var got []string
		opt := opts(30)
		opt.PlainOpenFence = true
		opt.Highlight = func(lang, code string) ([][]CodeSegment, bool) {
			got = append(got, lang)
			return nil, false
		}
		render(t, c.src, opt)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: Highlight saw langs %q, want %q", c.name, got, c.want)
		}
		got = nil
		opt.PlainOpenFence = false
		render(t, c.src, opt)
		for _, lang := range got {
			if lang == "" {
				t.Errorf("%s: without PlainOpenFence a fence lost its language", c.name)
			}
		}
	}
}
