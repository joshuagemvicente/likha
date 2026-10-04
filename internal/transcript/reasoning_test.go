package transcript

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

func TestThoughtMarker(t *testing.T) {
	cases := []struct {
		d     time.Duration
		known bool
		want  string
	}{
		{0, false, "Thought"},
		{42 * time.Second, false, "Thought"},
		{0, true, "Thought for <1s"},
		{-3 * time.Second, true, "Thought for <1s"},
		{999 * time.Millisecond, true, "Thought for <1s"},
		{time.Second, true, "Thought for 1s"},
		{4*time.Second + 900*time.Millisecond, true, "Thought for 4s"},
		{59 * time.Second, true, "Thought for 59s"},
		{60 * time.Second, true, "Thought for 1m 0s"},
		{72 * time.Second, true, "Thought for 1m 12s"},
		{59*time.Minute + 59*time.Second, true, "Thought for 59m 59s"},
		{time.Hour + 30*time.Second, true, "Thought for 1h 0m"},
		{2*time.Hour + 5*time.Minute + 9*time.Second, true, "Thought for 2h 5m"},
	}
	for _, c := range cases {
		if got := ThoughtMarker(c.d, c.known); got != c.want {
			t.Errorf("ThoughtMarker(%v, %v) = %q, want %q", c.d, c.known, got, c.want)
		}
	}
}

func checkRows(t *testing.T, rows []string, width int) {
	t.Helper()
	for i, row := range rows {
		if row == "" {
			t.Errorf("row %d is empty", i)
		}
		if w := runewidth.StringWidth(row); w > width {
			t.Errorf("row %d %q is %d cells, wider than %d", i, row, w, width)
		}
		if strings.HasPrefix(row, " ") || strings.HasSuffix(row, " ") {
			t.Errorf("row %d %q has leading or trailing space", i, row)
		}
	}
}

func TestTailFewerRowsThanN(t *testing.T) {
	rows, omitted := Tail("first idea\nsecond idea", 40, 3)
	if want := []string{"first idea", "second idea"}; !slices.Equal(rows, want) || omitted {
		t.Fatalf("Tail = %q, %v; want %q, false", rows, omitted, want)
	}
}

func TestTailExactlyNRows(t *testing.T) {
	rows, omitted := Tail("a\nb\nc", 10, 3)
	if want := []string{"a", "b", "c"}; !slices.Equal(rows, want) || omitted {
		t.Fatalf("Tail = %q, %v; want %q, false", rows, omitted, want)
	}
}

func TestTailMoreRowsThanN(t *testing.T) {
	rows, omitted := Tail("one\ntwo\nthree\nfour\nfive", 10, 3)
	if want := []string{"three", "four", "five"}; !slices.Equal(rows, want) || !omitted {
		t.Fatalf("Tail = %q, %v; want %q, true", rows, omitted, want)
	}
}

func TestTailOmittedWhenEarlierLineHoldsContent(t *testing.T) {
	// The last three lines give exactly three rows; the earlier line still
	// counts as omitted content, while earlier blank lines do not.
	rows, omitted := Tail("intro\n\nx\ny\nz", 10, 3)
	if want := []string{"x", "y", "z"}; !slices.Equal(rows, want) || !omitted {
		t.Fatalf("Tail = %q, %v; want %q, true", rows, omitted, want)
	}
	rows, omitted = Tail("\n \t\n\nx\ny\nz", 10, 3)
	if want := []string{"x", "y", "z"}; !slices.Equal(rows, want) || omitted {
		t.Fatalf("blank prefix: Tail = %q, %v; want %q, false", rows, omitted, want)
	}
}

func TestTailSkipsBlankRows(t *testing.T) {
	rows, omitted := Tail("alpha\n\n   \n\t\nbeta\n\n", 20, 3)
	if want := []string{"alpha", "beta"}; !slices.Equal(rows, want) || omitted {
		t.Fatalf("Tail = %q, %v; want %q, false", rows, omitted, want)
	}
}

func TestTailWordWrap(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	rows, omitted := Tail(text, 10, 10)
	want := []string{"The quick", "brown fox", "jumps over", "the lazy", "dog"}
	if !slices.Equal(rows, want) || omitted {
		t.Fatalf("Tail = %q, %v; want %q, false", rows, omitted, want)
	}
	rows, omitted = Tail(text, 10, 2)
	if want := []string{"the lazy", "dog"}; !slices.Equal(rows, want) || !omitted {
		t.Fatalf("tail of wrapped line = %q, %v; want %q, true", rows, omitted, want)
	}
	checkRows(t, rows, 10)
}

func TestTailCollapsesWhitespace(t *testing.T) {
	rows, _ := Tail("  lead\tand   inner    trail  ", 40, 3)
	if want := []string{"lead and inner trail"}; !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
}

func TestTailCarriageReturns(t *testing.T) {
	rows, _ := Tail("one\r\ntwo\rthree", 20, 3)
	if want := []string{"one", "two", "three"}; !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
}

func TestTailHardBreaksLongToken(t *testing.T) {
	rows, _ := Tail("see abcdefghijklmnop", 8, 5)
	// The long word starts after "see" because its first cell fits, then
	// breaks by cell width.
	want := []string{"see abcd", "efghijkl", "mnop"}
	if !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
	checkRows(t, rows, 8)
}

func TestTailWideRunes(t *testing.T) {
	// Each CJK rune is two cells; at width 5 only two fit per row, and a
	// wide rune is never split across rows.
	rows, _ := Tail("漢字かなカナ", 5, 5)
	want := []string{"漢字", "かな", "カナ"}
	if !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
	checkRows(t, rows, 5)

	// A word that fits a fresh row but not the current one moves on whole.
	rows, _ = Tail("ab 漢字", 4, 5)
	if want := []string{"ab", "漢字"}; !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
	// An over-wide word fills the current row when its first wide rune fits
	// after the space...
	rows, _ = Tail("abc 漢字漢字漢", 6, 5)
	if want := []string{"abc 漢", "字漢字", "漢"}; !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
	checkRows(t, rows, 6)
	// ...and starts a fresh row when only one cell would be left for it.
	rows, _ = Tail("abc 漢字漢字漢", 5, 5)
	if want := []string{"abc", "漢字", "漢字", "漢"}; !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
	checkRows(t, rows, 5)

	// Emoji are wide too.
	rows, _ = Tail("ok 🙂🙂🙂", 4, 5)
	checkRows(t, rows, 4)
	if got := strings.Join(rows, ""); got != "ok🙂🙂🙂" {
		t.Fatalf("emoji rows %q lost content", rows)
	}
}

func TestTailWideRuneAtWidthOne(t *testing.T) {
	rows, _ := Tail("漢", 1, 10)
	if got := strings.Join(rows, ""); got != "\\u6F22" {
		t.Fatalf("rows %q, want the escaped rune split one cell per row", rows)
	}
	checkRows(t, rows, 1)
}

func TestTailControlRunes(t *testing.T) {
	text := "clear\x1b[2J screen\a bell\u200bzw e\u0301 del\x7f"
	rows, _ := Tail(text, 80, 3)
	if len(rows) != 1 {
		t.Fatalf("Tail = %q, want one row", rows)
	}
	row := rows[0]
	for _, r := range row {
		if hiddenRune(r) {
			t.Fatalf("row %q still holds hidden rune %U", row, r)
		}
	}
	want := "clear\\u001B[2J screen\\u0007 bell\\u200Bzw e\\u0301 del\\u007F"
	if row != want {
		t.Fatalf("row = %q, want %q", row, want)
	}
}

func TestTailControlRunesWrapAsCells(t *testing.T) {
	// An escaped rune counts its escaped text's cells, so rows still fit.
	rows, _ := Tail("ab\x1bcd", 5, 5)
	checkRows(t, rows, 5)
	if got := strings.Join(rows, ""); got != `ab\u001Bcd` {
		t.Fatalf("rows %q, want escaped text broken by cell width", rows)
	}
}

func TestTailEmpty(t *testing.T) {
	for _, text := range []string{"", "\n\n", "   ", "\t \r\n "} {
		rows, omitted := Tail(text, 40, 3)
		if len(rows) != 0 || omitted {
			t.Errorf("Tail(%q) = %q, %v; want no rows, false", text, rows, omitted)
		}
	}
}

func TestTailNonPositiveN(t *testing.T) {
	if rows, omitted := Tail("something", 40, 0); rows != nil || !omitted {
		t.Fatalf("n=0 with content = %q, %v; want nil, true", rows, omitted)
	}
	if rows, omitted := Tail("  \n", 40, -1); rows != nil || omitted {
		t.Fatalf("n<0 without content = %q, %v; want nil, false", rows, omitted)
	}
}

func TestTailNonPositiveWidth(t *testing.T) {
	rows, _ := Tail("ab", 0, 5)
	if want := []string{"a", "b"}; !slices.Equal(rows, want) {
		t.Fatalf("Tail = %q, want %q", rows, want)
	}
}

func TestTailRowsFitAcrossWidths(t *testing.T) {
	text := strings.Repeat("Considering whether 漢字 and 🙂 fit; path/to/a/very/long/file_name_without_spaces.go \x1b[31m\n", 20)
	for _, width := range []int{1, 2, 3, 7, 40, 60, 80, 120} {
		for _, n := range []int{1, 3, 50} {
			rows, _ := Tail(text, width, n)
			if len(rows) == 0 || len(rows) > n {
				t.Fatalf("width %d n %d: got %d rows", width, n, len(rows))
			}
			checkRows(t, rows, width)
		}
	}
}

func TestTailMatchesFullWrapSuffix(t *testing.T) {
	// The partial wrap of trailing lines equals the suffix of a full wrap.
	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("step %d: inspect the module boundary and its callers", i))
		if i%4 == 0 {
			lines = append(lines, "")
		}
	}
	text := strings.Join(lines, "\n")
	full, fullOmitted := Tail(text, 23, 1000)
	if fullOmitted {
		t.Fatal("full wrap reports omitted rows")
	}
	for _, n := range []int{1, 3, 7} {
		rows, omitted := Tail(text, 23, n)
		if !slices.Equal(rows, full[len(full)-n:]) || !omitted {
			t.Fatalf("n=%d: Tail = %q, %v; want %q, true", n, rows, omitted, full[len(full)-n:])
		}
	}
}
