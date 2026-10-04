package ui

import (
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestBlockGlyphSets(t *testing.T) {
	uni := UnicodeBlockGlyphs()
	want := BlockGlyphs{
		User: ">", Assistant: "⏺", ToolHeader: "⏺", Result: "⎿",
		Error: "✗", Notice: "ℹ", Thought: "✻", Focus: "›", Ellipsis: "…",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
		Horizontal: "─", Vertical: "│",
	}
	if uni != want {
		t.Fatalf("Unicode set = %+v, want %+v", uni, want)
	}
	ascii := AsciiBlockGlyphs()
	want = BlockGlyphs{
		User: ">", Assistant: "*", ToolHeader: "*", Result: "L",
		Error: "x", Notice: "i", Thought: "~", Focus: ">", Ellipsis: "...",
		TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
		Horizontal: "-", Vertical: "|",
	}
	if ascii != want {
		t.Fatalf("ASCII set = %+v, want %+v", ascii, want)
	}
	if BlockGlyphSet(false) != uni || BlockGlyphSet(true) != ascii {
		t.Fatal("BlockGlyphSet must select Unicode by default and ASCII when asked")
	}
}

func TestBlockGlyphsFitTheGutter(t *testing.T) {
	// Every glyph but the ASCII ellipsis is one cell wide (narrow, non-East
	// Asian widths, as the transcript measures them), so the two-cell gutter
	// and hanging indents line up; the ASCII set stays 7-bit.
	cond := runewidth.NewCondition()
	cond.EastAsianWidth = false
	for label, set := range map[string]BlockGlyphs{"unicode": UnicodeBlockGlyphs(), "ascii": AsciiBlockGlyphs()} {
		for field, g := range blockGlyphFields(set) {
			want := 1
			if field == "Ellipsis" && label == "ascii" {
				want = 3
			}
			if w := cond.StringWidth(g); w != want {
				t.Errorf("%s/%s %q: width %d, want %d", label, field, g, w, want)
			}
			for _, r := range g {
				if label == "ascii" && r > 0x7e {
					t.Errorf("ascii/%s %q: non-ASCII rune %U", field, g, r)
				}
			}
		}
	}
}

func TestBlockKindsDistinct(t *testing.T) {
	// The glyph alone identifies a block kind (meaning is never color-only).
	for label, set := range map[string]BlockGlyphs{"unicode": UnicodeBlockGlyphs(), "ascii": AsciiBlockGlyphs()} {
		seen := map[string]string{}
		for kind, g := range map[string]string{
			"User": set.User, "Assistant": set.Assistant, "Result": set.Result,
			"Error": set.Error, "Notice": set.Notice, "Thought": set.Thought,
		} {
			if prev, dup := seen[g]; dup {
				t.Errorf("%s: %s and %s share glyph %q", label, prev, kind, g)
			}
			seen[g] = kind
		}
	}
}

func blockGlyphFields(g BlockGlyphs) map[string]string {
	return map[string]string{
		"User": g.User, "Assistant": g.Assistant, "ToolHeader": g.ToolHeader,
		"Result": g.Result, "Error": g.Error, "Notice": g.Notice,
		"Thought": g.Thought, "Focus": g.Focus, "Ellipsis": g.Ellipsis,
		"TopLeft": g.TopLeft, "TopRight": g.TopRight, "BottomLeft": g.BottomLeft,
		"BottomRight": g.BottomRight, "Horizontal": g.Horizontal, "Vertical": g.Vertical,
	}
}
