package highlight

import (
	"strings"
	"testing"
)

// FuzzLines checks the Lines contract on arbitrary code: it never panics, and
// a successful result reproduces the source line for line with no empty
// segment and no two adjacent segments of one Kind.
func FuzzLines(f *testing.F) {
	for _, s := range []string{"", "\n", "package main\nfunc main() {}\n", "x = \"a\\\"b\"\r\n# c", "\x1b[31m\t\x00", "<?php echo 1; ?>", "```\n# h\n"} {
		for _, lang := range []string{"go", "python", "markdown", "php", "bash", "nope", ""} {
			f.Add(lang, s)
		}
	}
	f.Fuzz(func(t *testing.T, lang, code string) {
		out, ok := Lines(lang, code)
		if !ok {
			return
		}
		want := strings.Split(strings.TrimSuffix(code, "\n"), "\n")
		if len(out) != len(want) {
			t.Fatalf("Lines(%q, %q) = %d lines, want %d", lang, code, len(out), len(want))
		}
		for i, line := range out {
			var b strings.Builder
			for j, seg := range line {
				if seg.Text == "" || (j > 0 && line[j-1].Kind == seg.Kind) {
					t.Fatalf("Lines(%q, %q) line %d has a bad segment list %+v", lang, code, i, line)
				}
				b.WriteString(seg.Text)
			}
			if b.String() != want[i] {
				t.Fatalf("Lines(%q, %q) line %d = %q, want %q", lang, code, i, b.String(), want[i])
			}
		}
	})
}
