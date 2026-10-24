package transcript

import (
	"testing"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// FuzzTextBounds checks that every width-bounded transcript helper keeps its
// result within width and free of control runes, on arbitrary text.
func FuzzTextBounds(f *testing.F) {
	for _, s := range []string{"", "a b", "日本語", "🎉\u200d", "e\u0301", "\x1b]0;t\x07", "\t\r\n", "\xff", "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-a\x1b[2J\n+b\t\n c"} {
		f.Add(s, 1, "…")
		f.Add(s, 7, "...")
		f.Add(s, 40, "")
	}
	f.Fuzz(func(t *testing.T, text string, width int, ellipsis string) {
		width %= 300
		check := func(name, got string, limit int, escaped bool) {
			t.Helper()
			if w := runewidth.StringWidth(got); w > max(limit, 0) {
				t.Fatalf("%s(%q, %d, %q) = %q is %d cells", name, text, width, ellipsis, got, w)
			}
			if !escaped {
				return
			}
			for _, r := range got {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					t.Fatalf("%s(%q, %d) = %q carries %U", name, text, width, got, r)
				}
			}
		}
		flat := flattenRow(text)
		check("clipRight", clipRight(flat, width, ellipsis), width, false)
		check("clipLeft", clipLeft(flat, width, ellipsis), width, false)
		check("FormatDiffLine", FormatDiffLine(DiffLine{Number: width, Sign: '+', Text: text}, 3, width, flattenRow(ellipsis)), width, true)
		check("FooterText", FooterText(TurnInfo{Model: text, Effort: text, Duration: time.Duration(width) * time.Second, Tools: width}, width, flattenRow(ellipsis)), width, true)
		rows, _ := Tail(text, width, 3)
		for _, row := range rows {
			check("Tail", row, max(width, 1), true)
		}
		summary := SummarizeEdit(ParseUnifiedDiff(text), DefaultDiffLines)
		for _, line := range summary.Lines {
			check("FormatDiffLine(parsed)", FormatDiffLine(line, summary.NumberWidth(), width, "…"), width, true)
		}
	})
}
