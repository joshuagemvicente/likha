package markdown

import (
	"strings"
	"testing"
)

// FuzzRender checks the Render row contract (width bound, no control runes,
// well-formed runs) on arbitrary sources at narrow and ordinary widths.
func FuzzRender(f *testing.F) {
	seeds := []string{
		"", "a", "# h\n\ntext", "- a\n  - b\n    - c", "> q\n> > qq", "```go\nx\t\ty\n```", "```\nunclosed",
		"| a | b |\n|---|:-:|\n| 1 | 2 | 3 |\n| x |", "日本語 🎉 e\u0301 \u200b", "\x1b]52;c;aGk=\x07", "\x1b[2J\r\n",
		"[link](http://example.com/a/very/long/url/that/goes/on)", "1. a\n10. b", "***\n---", "<div>\n\x1b[31m\n</div>",
		"\xff\xfe invalid", "\t\tindented code", "a  \nb\\\nc", "~~s~~ **b** *i* `c`",
	}
	for _, s := range seeds {
		f.Add(s, uint8(1))
		f.Add(s, uint8(7))
		f.Add(s, uint8(40))
	}
	f.Fuzz(func(t *testing.T, src string, w uint8) {
		width := int(w%120) + 1
		for _, glyphs := range []Glyphs{{}, {Bullet: "-", QuoteBar: "|", Rule: "-", Ellipsis: "...", TableV: "|", TableH: "-", TableCross: "+"}} {
			opt := opts(width)
			opt.Glyphs = glyphs
			opt.Highlight = func(_, code string) ([][]CodeSegment, bool) {
				var out [][]CodeSegment
				for _, line := range strings.Split(code, "\n") {
					out = append(out, []CodeSegment{{Text: line, Kind: Keyword}})
				}
				return out, true
			}
			checkRows(t, src, width, Render(src, opt))
		}
	})
}
