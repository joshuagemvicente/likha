package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// checkHangingWidths asserts the wrapHanging width contract: the first row
// fits beside the caller's firstIndent cells, and every row fits width.
func checkHangingWidths(t *testing.T, rows []string, width, firstIndent int) {
	t.Helper()
	limit := max(width, 1)
	if len(rows) == 0 {
		t.Fatalf("no rows returned")
	}
	for i, row := range rows {
		got := runewidth.StringWidth(row)
		if got > limit {
			t.Fatalf("row %d %q is %d cells > width %d (rows %q)", i, row, got, limit, rows)
		}
		if i == 0 && firstIndent < limit && got+max(firstIndent, 0) > limit {
			t.Fatalf("first row %q + indent %d exceeds width %d", row, firstIndent, limit)
		}
	}
}

func TestWrapHangingASCII(t *testing.T) {
	got := wrapHanging("the quick brown fox jumps", 12, 2, 2)
	want := []string{"the quick", "  brown fox", "  jumps"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	checkHangingWidths(t, got, 12, 2)
}

func TestWrapHangingFitsOnOneRow(t *testing.T) {
	got := wrapHanging("short", 40, 2, 2)
	if want := []string{"short"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := wrapHanging("", 40, 2, 2); !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("empty text: got %q, want one empty row", got)
	}
}

// The tool-result hang (5 cells under ⎿) differs from the first-row gutter.
func TestWrapHangingDistinctIndents(t *testing.T) {
	got := wrapHanging("alpha beta gamma delta", 15, 5, 5)
	want := []string{"alpha beta", "     gamma", "     delta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = wrapHanging("alpha beta gamma delta", 16, 0, 2)
	want = []string{"alpha beta gamma", "  delta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWrapHangingCJK(t *testing.T) {
	// Six double-width runes: 12 cells, 6 per row at width 8 with 2 indent.
	got := wrapHanging("中文字符测试", 8, 2, 2)
	want := []string{"中文字", "  符测试"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	// An odd width never splits a wide rune: 7 cells hold three runes.
	got = wrapHanging("中文字符测试", 7, 0, 0)
	want = []string{"中文字", "符测试"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("odd width: got %q, want %q", got, want)
	}
	checkHangingWidths(t, got, 7, 0)
	// CJK after a word fills the current row rather than leaving it short.
	got = wrapHanging("see 中文字符测试", 10, 0, 0)
	want = []string{"see 中文字", "符测试"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed: got %q, want %q", got, want)
	}
}

func TestWrapHangingEmoji(t *testing.T) {
	got := wrapHanging("ok 🎉🎉🎉 done", 8, 2, 2)
	want := []string{"ok", "  🎉🎉🎉", "  done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	checkHangingWidths(t, got, 8, 2)
}

func TestWrapHangingLongURL(t *testing.T) {
	url := "https://example.com/a/very/long/path/that/exceeds/the/row"
	text := "see " + url + " now"
	got := wrapHanging(text, 20, 2, 2)
	checkHangingWidths(t, got, 20, 2)
	if got[0] != "see https://exampl" {
		t.Fatalf("over-wide token should fill the first row: %q", got)
	}
	var joined strings.Builder
	for i, row := range got {
		if i > 0 {
			if !strings.HasPrefix(row, "  ") {
				t.Fatalf("row %d lacks hang indent: %q", i, row)
			}
			row = row[2:]
		}
		joined.WriteString(row)
	}
	if joined.String() != text {
		t.Fatalf("URL text not preserved across rows: %q", got)
	}
	if last := got[len(got)-1]; !strings.HasSuffix(last, "now") {
		t.Fatalf("trailing word lost: %q", got)
	}
}

func TestWrapHangingMultipleSpaces(t *testing.T) {
	if got := wrapHanging("a   b    c", 20, 0, 0); !reflect.DeepEqual(got, []string{"a   b    c"}) {
		t.Fatalf("inner spaces collapsed: %q", got)
	}
	// Spaces at a break are dropped, not carried to either row.
	got := wrapHanging("aaaa    bbbb", 6, 0, 2)
	if want := []string{"aaaa", "  bbbb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("break spaces: got %q, want %q", got, want)
	}
	// A paragraph's leading spaces stay as indentation.
	got = wrapHanging("  indented text", 40, 2, 2)
	if want := []string{"  indented text"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("leading spaces: got %q, want %q", got, want)
	}
	// Trailing spaces and space-only paragraphs never spill onto new rows.
	got = wrapHanging("abc"+strings.Repeat(" ", 30), 8, 0, 2)
	if want := []string{"abc     "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trailing spaces: got %q, want %q", got, want)
	}
	got = wrapHanging(strings.Repeat(" ", 30), 8, 2, 2)
	if want := []string{"      "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("space-only paragraph: got %q, want %q", got, want)
	}
}

func TestWrapHangingExplicitNewlines(t *testing.T) {
	got := wrapHanging("one\ntwo\n\n  three four", 40, 2, 2)
	want := []string{"one", "  two", "", "    three four"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := wrapHanging("end\n", 40, 2, 2); !reflect.DeepEqual(got, []string{"end", ""}) {
		t.Fatalf("trailing newline: got %q", got)
	}
}

func TestWrapHangingTinyWidths(t *testing.T) {
	texts := []string{
		"hello world",
		"中文 字符",
		"🎉 x\ty",
		"a\u200bb https://example.com/x",
		"",
		"\n\n",
	}
	for _, text := range texts {
		for _, width := range []int{-3, 0, 1, 2, 3, 4, 5} {
			for _, first := range []int{-1, 0, 2, 5, 9} {
				for _, hang := range []int{-1, 0, 2, 5, 9} {
					rows := wrapHanging(text, width, first, hang)
					checkHangingWidths(t, rows, width, first)
				}
			}
		}
	}
	// Width 1 cannot hold a wide rune: it shows escaped, one cell per row.
	got := wrapHanging("中", 1, 0, 2)
	if want := []string{"\\", "u", "4", "E", "2", "D"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("width 1 wide rune: got %q, want %q", got, want)
	}
	// A first-row indent that fills the width moves text to row two.
	got = wrapHanging("word", 10, 10, 2)
	if want := []string{"", "  word"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("full first indent: got %q, want %q", got, want)
	}
	// A hang wider than the row shrinks to leave two text cells.
	got = wrapHanging("ab cd", 4, 0, 9)
	if want := []string{"ab", "  cd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("oversized hang: got %q, want %q", got, want)
	}
}

func TestWrapHangingControlRunes(t *testing.T) {
	got := wrapHanging("a\tb\u200bc\x1b[31m", 80, 2, 2)
	want := []string{`a\u0009b\u200Bc\u001B[31m`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, row := range got {
		if strings.ContainsAny(row, "\t\x1b\u200b") {
			t.Fatalf("raw hidden rune leaked: %q", row)
		}
	}
	// Escapes are cells of their word: they move to a fresh row whole when
	// they fit one, and break per cell like wrap() when they do not.
	got = wrapHanging("x \u200b", 7, 0, 1)
	if want := []string{"x", ` \u200B`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("escape wrap: got %q, want %q", got, want)
	}
	got = wrapHanging("\u200b", 4, 0, 0)
	if want := []string{`\u20`, "0B"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("escape hard break: got %q, want %q", got, want)
	}
}

// Without spaces or indents, wrapHanging hard-breaks exactly like wrap().
func TestWrapHangingMatchesWrapWithoutSpaces(t *testing.T) {
	texts := []string{
		"abcdefghijklmnopqrstuvwxyz",
		"中文字符测试中文字符测试",
		"🎉🎉x🎉\u200b🎉",
		"tab\there\nnext\x07line",
	}
	for _, text := range texts {
		for width := 2; width <= 13; width++ {
			got := wrapHanging(text, width, 0, 0)
			want := wrap(text, width)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("width %d %q: got %q, want wrap() %q", width, text, got, want)
			}
		}
	}
}

// Across widths and indents, no row overflows and no text is lost: only
// spaces dropped at breaks and the hang indents differ from the input.
func TestWrapHangingPreservesText(t *testing.T) {
	text := "Likha wraps prose at word boundaries — 中文字符 and 🎉 emoji, " +
		"a https://example.com/very/long/url/that/must/break here,\n" +
		"  an indented line\twith a tab, and #4493f8 swatches."
	squash := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r == ' ' || r == '\n':
			case hiddenReviewRune(r):
				fmt.Fprintf(&b, "\\u%04X", r)
			default:
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	want := squash(text)
	for width := 2; width <= 120; width++ {
		for _, first := range []int{0, 2, 5} {
			for _, hang := range []int{0, 2, 5} {
				rows := wrapHanging(text, width, first, hang)
				checkHangingWidths(t, rows, width, first)
				if got := squash(strings.Join(rows, "")); got != want {
					t.Fatalf("width %d first %d hang %d lost text:\n got %q\nwant %q", width, first, hang, got, want)
				}
			}
		}
	}
}
