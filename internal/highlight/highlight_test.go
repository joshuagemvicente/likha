package highlight

import (
	"strings"
	"testing"
	"time"
)

// mustLines tokenizes code and checks the invariants every result must hold:
// one line per source line, exact text reconstruction, no empty segments and
// no adjacent segments of the same kind.
func mustLines(t *testing.T, lang, code string) [][]Segment {
	t.Helper()
	lines, ok := Lines(lang, code)
	if !ok {
		t.Fatalf("Lines(%q) not ok", lang)
	}
	want := strings.Split(strings.TrimSuffix(code, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("Lines(%q): %d lines, want %d: %#v", lang, len(lines), len(want), lines)
	}
	for i, line := range lines {
		var b strings.Builder
		for j, seg := range line {
			if seg.Text == "" {
				t.Errorf("line %d segment %d is empty", i, j)
			}
			if strings.Contains(seg.Text, "\n") {
				t.Errorf("line %d segment %d contains a newline: %q", i, j, seg.Text)
			}
			if j > 0 && line[j-1].Kind == seg.Kind {
				t.Errorf("line %d segments %d and %d share kind %d", i, j-1, j, seg.Kind)
			}
			b.WriteString(seg.Text)
		}
		if b.String() != want[i] {
			t.Errorf("line %d = %q, want %q", i, b.String(), want[i])
		}
	}
	return lines
}

// kindOfText returns the kind of the first segment whose text contains s.
func kindOfText(t *testing.T, lines [][]Segment, s string) Kind {
	t.Helper()
	for _, line := range lines {
		for _, seg := range line {
			if strings.Contains(seg.Text, s) {
				return seg.Kind
			}
		}
	}
	t.Fatalf("no segment contains %q in %#v", s, lines)
	return Plain
}

func TestLinesGo(t *testing.T) {
	code := "package main\n\n// greet says hi\nfunc greet(n int) string {\n\treturn \"hi\" + strconv.Itoa(n*42)\n}\n"
	lines := mustLines(t, "go", code)
	for s, want := range map[string]Kind{
		"package":  Keyword,
		"func":     Keyword,
		"return":   Keyword,
		"// greet": Comment,
		`"hi"`:     String,
		"42":       Number,
		"greet":    Comment, // first occurrence is in the comment
		"int":      Type,
		"*":        Operator,
	} {
		if got := kindOfText(t, lines, s); got != want {
			t.Errorf("go %q kind = %d, want %d", s, got, want)
		}
	}
	// The tab indent survives as text.
	if !strings.HasPrefix(lines[4][0].Text, "\t") {
		t.Errorf("tab lost: %#v", lines[4])
	}
	// Aliases resolve.
	mustLines(t, "golang", code)
}

func TestLinesFenceInfoString(t *testing.T) {
	code := "x := 1"
	for _, lang := range []string{"go", "Go", "GO", "  go  ", "go title=main.go", "golang {.numbered}"} {
		if _, ok := Lines(lang, code); !ok {
			t.Errorf("Lines(%q) not ok", lang)
		}
	}
}

func TestLinesJavaScript(t *testing.T) {
	code := "const n = 3; // count\nfunction add(a, b) { return a + b; }\nconsole.log('ok', n);"
	for _, lang := range []string{"js", "javascript", "JavaScript"} {
		lines := mustLines(t, lang, code)
		for s, want := range map[string]Kind{
			"const":    Keyword,
			"// count": Comment,
			"'ok'":     String,
			"3":        Number,
			"+":        Operator,
		} {
			if got := kindOfText(t, lines, s); got != want {
				t.Errorf("%s %q kind = %d, want %d", lang, s, got, want)
			}
		}
	}
	mustLines(t, "ts", "let x: number = 1\n")
}

func TestLinesPython(t *testing.T) {
	code := "def area(r):\n    # circle\n    return 3.14 * r ** 2\n\nprint(len(\"abc\"))\n"
	for _, lang := range []string{"python", "py"} {
		lines := mustLines(t, lang, code)
		for s, want := range map[string]Kind{
			"def":      Keyword,
			"area":     Function,
			"# circle": Comment,
			"3.14":     Number,
			`"abc"`:    String,
			"print":    Builtin,
		} {
			if got := kindOfText(t, lines, s); got != want {
				t.Errorf("%s %q kind = %d, want %d", lang, s, got, want)
			}
		}
	}
}

func TestLinesBash(t *testing.T) {
	code := "#!/bin/sh\n# build it\nif [ -n \"$X\" ]; then\n\techo 'done'\nfi\n"
	for _, lang := range []string{"bash", "sh", "shell", "zsh"} {
		lines := mustLines(t, lang, code)
		if got := kindOfText(t, lines, "# build it"); got != Comment {
			t.Errorf("%s comment kind = %d", lang, got)
		}
		if got := kindOfText(t, lines, "if"); got != Keyword {
			t.Errorf("%s if kind = %d", lang, got)
		}
		if got := kindOfText(t, lines, "'done'"); got != String {
			t.Errorf("%s string kind = %d", lang, got)
		}
	}
}

func TestLinesJSON(t *testing.T) {
	code := "{\n  \"name\": \"likha\",\n  \"n\": 12,\n  \"ok\": true\n}"
	lines := mustLines(t, "json", code)
	if got := kindOfText(t, lines, `"likha"`); got != String {
		t.Errorf("json value kind = %d, want String", got)
	}
	if got := kindOfText(t, lines, "12"); got != Number {
		t.Errorf("json number kind = %d, want Number", got)
	}
	if got := kindOfText(t, lines, "{"); got != Punctuation {
		t.Errorf("json brace kind = %d, want Punctuation", got)
	}
	if got := kindOfText(t, lines, "true"); got != Keyword {
		t.Errorf("json true kind = %d, want Keyword", got)
	}
}

func TestLinesYAML(t *testing.T) {
	code := "# config\nname: likha\nitems:\n  - 1\n  - \"two\"\n"
	for _, lang := range []string{"yaml", "yml"} {
		lines := mustLines(t, lang, code)
		if got := kindOfText(t, lines, "# config"); got != Comment {
			t.Errorf("%s comment kind = %d", lang, got)
		}
		if got := kindOfText(t, lines, `"two"`); got != String {
			t.Errorf("%s string kind = %d", lang, got)
		}
	}
}

func TestLinesUnknownOrEmptyLanguage(t *testing.T) {
	for _, lang := range []string{"", "   ", "not-a-language-xyz", "text", "plaintext", "txt"} {
		if lines, ok := Lines(lang, "package main\nfunc main() {}\n"); ok || lines != nil {
			t.Errorf("Lines(%q) = %#v, %v; want nil, false", lang, lines, ok)
		}
	}
}

func TestLinesEmptyCode(t *testing.T) {
	for _, code := range []string{"", "\n"} {
		lines := mustLines(t, "go", code)
		if len(lines) != 1 || len(lines[0]) != 0 {
			t.Errorf("Lines(go, %q) = %#v, want one empty line", code, lines)
		}
	}
}

func TestLinesTrailingNewline(t *testing.T) {
	with := mustLines(t, "go", "x := 1\ny := 2\n")
	without := mustLines(t, "go", "x := 1\ny := 2")
	if len(with) != 2 || len(without) != 2 {
		t.Fatalf("lines with/without trailing newline = %d/%d, want 2/2", len(with), len(without))
	}
	// Only one trailing newline is absorbed; blank lines before it remain.
	if got := mustLines(t, "go", "x := 1\n\n"); len(got) != 2 {
		t.Errorf("x := 1\\n\\n: %d lines, want 2", len(got))
	}
}

func TestLinesReconstructsExactly(t *testing.T) {
	cases := []struct{ lang, code string }{
		{"go", "func f() {\n\t\tif x {\n\t\t\treturn \"héllo, 世界 🌍\"\n\t\t}\n}\n"},
		{"python", "s = 'naïve café'  # ünïcode\n\tx = 1\n"},
		{"js", "const s = `tab\there`;\r\nlet y = 2;\r\n"},
		{"bash", "echo \"a\tb\"   \n\n\n"},
		{"json", "{\"k\": \"\\u00e9\\t\"}"},
		{"yaml", "a:\n\t- b\n"},
		{"rust", "fn main() { let s = \"日本\"; }"},
		{"go", "\x1b[31mred\x1b[0m := \"\x07\"\n"},
	}
	for _, c := range cases {
		mustLines(t, c.lang, c.code)
	}
}

func TestLinesSizeBound(t *testing.T) {
	line := "x := 1 // padding padding padding\n"
	huge := strings.Repeat(line, maxCodeBytes/len(line)+1)
	if len(huge) <= maxCodeBytes {
		t.Fatalf("test input too small: %d", len(huge))
	}
	if lines, ok := Lines("go", huge); ok || lines != nil {
		t.Errorf("huge input = %d lines, %v; want nil, false", len(lines), ok)
	}
	// Just under the bound still works (the time budget is lifted so a slow
	// or race-instrumented run cannot turn this into a budget test).
	defer func(b time.Duration) { budget = b }(budget)
	budget = time.Minute
	fits := huge[:maxCodeBytes]
	if _, ok := Lines("go", fits); !ok {
		t.Error("input at the bound was rejected")
	}
}

// Superlinear lexing is cut off by the time budget: chroma's markdown lexer
// is quadratic on unclosed emphasis runs, and the block renders plain.
func TestLinesTimeBudget(t *testing.T) {
	defer func(b time.Duration) { budget = b }(budget)
	budget = 20 * time.Millisecond
	code := strings.Repeat("*a **b _c `d", 2600) // ~30 KiB, under the size bound; ~0.7 s unbudgeted
	if len(code) > maxCodeBytes {
		t.Fatalf("test input over the size bound: %d", len(code))
	}
	start := time.Now()
	lines, ok := Lines("md", code)
	elapsed := time.Since(start)
	if ok || lines != nil {
		t.Fatalf("pathological markdown highlighted in %v; want nil, false past the budget", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("budgeted call took %v", elapsed)
	}
	// Ordinary code well inside the budget still highlights.
	budget = time.Minute
	if _, ok := Lines("go", "package x\n"); !ok {
		t.Fatal("small go block not highlighted")
	}
}

// The budget holds for lexers that emit long runs of same-type tokens:
// each raw token is checked, so a coalescing read-ahead cannot hide seconds
// of lexing between two deadline checks. Unbudgeted, these took 0.3-2.5 s on
// a fast machine (apache's lexer is quadratic on a run of name characters).
func TestLinesTimeBudgetSameTypeRuns(t *testing.T) {
	defer func(b time.Duration) { budget = b }(budget)
	budget = 20 * time.Millisecond
	cases := []struct{ lang, code string }{
		{"apache", strings.Repeat("a", 8000)},
		{"ng2", strings.Repeat("(", 2000)},
		{"makefile", strings.Repeat("${", 3000)},
	}
	for _, c := range cases {
		start := time.Now()
		lines, ok := Lines(c.lang, c.code)
		elapsed := time.Since(start)
		if ok || lines != nil {
			t.Errorf("%s: highlighted in %v; want nil, false past the budget", c.lang, elapsed)
		}
		// One raw token may overrun the budget by chroma's 250 ms per-match
		// cap; allow that plus scheduling slack under -race.
		if elapsed > 750*time.Millisecond {
			t.Errorf("%s: budgeted call took %v", c.lang, elapsed)
		}
	}
}

// Dropping the coalescer keeps segments merged by Kind: adjacent raw tokens
// of one kind form one segment.
func TestLinesMergesSameKindTokens(t *testing.T) {
	lines := mustLines(t, "go", "x := a + b\n")
	for i, line := range lines {
		for j := 1; j < len(line); j++ {
			if line[j-1].Kind == line[j].Kind {
				t.Fatalf("line %d: adjacent segments %q and %q share kind %d", i, line[j-1].Text, line[j].Text, line[j].Kind)
			}
		}
	}
}

func TestKindOfCategories(t *testing.T) {
	lines := mustLines(t, "python", "class Foo(Exception):\n    @dec\n    def m(self): return None\n")
	for s, want := range map[string]Kind{
		"class":     Keyword,
		"Foo":       Type,
		"Exception": Type,
		"self":      Builtin,
		"None":      Keyword,
	} {
		if got := kindOfText(t, lines, s); got != want {
			t.Errorf("python %q kind = %d, want %d", s, got, want)
		}
	}
}
