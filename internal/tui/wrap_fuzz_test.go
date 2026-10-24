package tui

import (
	"strings"
	"testing"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// noHiddenRunes fails when text carries a rune that must render escaped.
func noHiddenRunes(t *testing.T, where, text string) {
	t.Helper()
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("%s: %q carries control rune %U", where, text, r)
		}
	}
}

// FuzzTextFitting checks the width and escaping contracts of the transcript's
// plain-text wrap and clip helpers on arbitrary text and widths.
func FuzzTextFitting(f *testing.F) {
	for _, s := range []string{"", "a b c", "日本語テキスト", "🎉👨\u200d👩", "e\u0301", "\x1b]52;c;aGk=\x07", "\t\r\n\x00", "\xff\xfe", "  lead  trail  ", strings.Repeat("x", 300)} {
		f.Add(s, 1, 0, 0)
		f.Add(s, 2, 1, 4)
		f.Add(s, 20, 2, 2)
		f.Add(s, 80, 90, -3)
	}
	f.Fuzz(func(t *testing.T, text string, width, first, hang int) {
		width %= 200
		first, hang = first%250, hang%250
		bound := max(width, 1)
		for i, row := range wrapHanging(text, width, first, hang) {
			limit := bound
			if i == 0 && first > 0 && first < bound {
				limit = bound - first
			}
			if w := runewidth.StringWidth(row); w > limit {
				t.Fatalf("wrapHanging(%q, %d, %d, %d) row %d %q is %d cells, limit %d", text, width, first, hang, i, row, w, limit)
			}
			noHiddenRunes(t, "wrapHanging", row)
		}
		if width >= 2 { // the main view is at least minWidth wide; wrap needs room for a wide rune
			for _, row := range wrap(text, width) {
				if w := runewidth.StringWidth(row); w > width {
					t.Fatalf("wrap(%q, %d) row %q is %d cells", text, width, row, w)
				}
				noHiddenRunes(t, "wrap", row)
			}
		}
		if width >= 0 {
			if got := fit(text, width); runewidth.StringWidth(got) > width {
				t.Fatalf("fit(%q, %d) = %q is %d cells", text, width, got, runewidth.StringWidth(got))
			}
			for _, clip := range []string{toolPreviewRow(text, width, "…"), clipVisible(text, width, "…"), toolHeaderText(text, "…")} {
				noHiddenRunes(t, "clip", clip)
			}
			if got := toolPreviewRow(text, width, "…"); runewidth.StringWidth(got) > width {
				t.Fatalf("toolPreviewRow(%q, %d) = %q is %d cells", text, width, got, runewidth.StringWidth(got))
			}
			if got := clipVisible(text, width, "…"); runewidth.StringWidth(got) > width {
				t.Fatalf("clipVisible(%q, %d) = %q is %d cells", text, width, got, runewidth.StringWidth(got))
			}
			if got := fitToolHeader(toolHeaderText(text, "…"), toolHeaderText(text, "…"), width, "…"); runewidth.StringWidth(got) > width {
				t.Fatalf("fitToolHeader(%q, %d) = %q is %d cells", text, width, got, runewidth.StringWidth(got))
			}
			styled := "\x1b[1m" + fit(text, width+1) + "\x1b[0m"
			if got := truncateRow(styled, width); runewidth.StringWidth(stripANSI(got)) > width {
				t.Fatalf("truncateRow(%q, %d) = %q", styled, width, got)
			}
		}
	})
}
