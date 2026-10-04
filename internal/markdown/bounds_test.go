package markdown

import (
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// Nested containers past maxBlockDepth render their text plain instead of
// recursing once per level: the layout keeps exactly maxBlockDepth quote
// bars, and the innermost text still shows.
func TestRenderDeepQuotesStopAtDepthLimit(t *testing.T) {
	src := strings.Repeat(">", 5000) + " deep *text*"
	rows := render(t, src, opts(200))
	want := strings.Repeat("│ ", maxBlockDepth) + "deep *text*"
	assertTexts(t, rows, []string{want})

	list := strings.Repeat("- ", 3000) + "item"
	rows = render(t, list, opts(200))
	if len(rows) != 1 || !strings.HasSuffix(rows[0].Text, "item") || strings.Count(rows[0].Text, "•") != maxBlockDepth {
		t.Fatalf("deep list rows = %q, want one row with %d bullets ending in item", texts(rows), maxBlockDepth)
	}
}

// Deep nesting must not grow the stack per level: 32 KiB of ">" used to
// recurse 32768 quotes deep and kill the process with a fatal stack
// overflow (not a recoverable panic). Under a 16 MiB stack cap the old
// layout dies on the 8000-deep quote (one chunk) and the inline cases; the
// bounded layout needs a tiny fraction of it.
func TestRenderDeepNestingStackBounded(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	cases := map[string]string{
		"quotes in one chunk": strings.Repeat(">", maxChunkBytes-8) + "a",
		"quote list":          strings.Repeat("> - ", maxChunkBytes/4-2) + "a",
		"emphasis":            strings.Repeat("*", maxChunkBytes/2-2) + "a" + strings.Repeat("*", maxChunkBytes/2-2),
		"links":               strings.Repeat("[*a ", maxChunkBytes/9) + "x" + strings.Repeat("*](u)", maxChunkBytes/9),
		"32 KiB of quotes":    strings.Repeat(">", 32<<10) + "a",
	}
	for name, src := range cases {
		rows := render(t, src, opts(80))
		if len(rows) == 0 || !strings.Contains(strings.Join(texts(rows), ""), "a") {
			t.Fatalf("%s: rows %q lost the text", name, texts(rows))
		}
	}
}

// goldmark's parse is quadratic on some inputs; chunked parsing bounds every
// parse to maxChunkBytes and the parse budget bounds the total. Unbounded,
// 200 KiB of "[a](" took over 30 s and of ">" several seconds.
func TestRenderPathologicalInputIsBounded(t *testing.T) {
	for _, pattern := range []string{"[a](", ">", "![a](", "> - ", "[a](\n", ">\n"} {
		src := strings.Repeat(pattern, (200<<10)/len(pattern))
		start := time.Now()
		rows := render(t, src, opts(80))
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("%q x %d: Render took %v", pattern, len(src)/len(pattern), elapsed)
		}
		if len(rows) == 0 {
			t.Errorf("%q: no rows", pattern)
		}
	}
}

// Chunking is invisible on ordinary long answers: a document over
// maxChunkBytes renders exactly as its top-level sections do one by one,
// joined by the usual blank row.
func TestRenderChunkedMatchesWhole(t *testing.T) {
	defer func(b time.Duration) { parseBudget = b }(parseBudget)
	parseBudget = time.Hour // a loaded -race run must not turn this into a budget test
	section := "## Heading\n\nSome **bold** prose with `code` and a [link](http://x).\n\n- one\n- two\n  - nested\n\n> quoted\n\n```go\nfunc main() {\n\n\treturn\n}\n```\n\n| a | b |\n|---|--:|\n| 1 | 2 |\n\n"
	src := strings.Repeat(section, 2*maxChunkBytes/len(section)+1)
	if len(src) <= maxChunkBytes || len(chunks(src)) < 2 {
		t.Fatalf("test source not chunked: %d bytes, %d chunks", len(src), len(chunks(src)))
	}
	got := render(t, src, opts(60))
	one := render(t, section, opts(60))
	var want []string
	for i := 0; i < strings.Count(src, "## Heading"); i++ {
		if i > 0 {
			want = append(want, "")
		}
		want = append(want, texts(one)...)
	}
	assertTexts(t, got, want)
}

func TestChunks(t *testing.T) {
	if c := chunks("a\n\nb\n"); len(c) != 1 || c[0].text != "a\n\nb\n" {
		t.Fatalf("small source = %+v, want one chunk", c)
	}
	para := strings.Repeat("word ", 300) + "\n\n" // 1.5 KiB
	// A fence holding blank lines and column-0 text is never split.
	fence := "```\n" + strings.Repeat("x\n\ny\n", 3000) + "```\n\n"
	src := para + fence + para + "    indented\n\n" + para
	got := chunks(src)
	var joined string
	for _, c := range got {
		joined += c.text
		if len(c.text) > maxChunkBytes && !c.fence {
			t.Errorf("oversized non-fence chunk %q...", c.text[:20])
		}
	}
	if joined != src {
		t.Fatal("chunks do not reproduce the source")
	}
	if len(got) != 3 || got[1].text != fence || !got[1].fence {
		t.Fatalf("chunks = %d, middle fence chunk %v; want para | fence | rest", len(got), len(got) > 1 && got[1].fence)
	}
	// An unclosed fence runs to the end and stays one fence chunk.
	open := para + "~~~py\n" + strings.Repeat("a\n\nb\n", 3000)
	got = chunks(open)
	if last := got[len(got)-1]; !last.fence || !strings.HasPrefix(last.text, "~~~py") {
		t.Fatalf("unclosed fence chunk = %q..., fence %v", last.text[:10], last.fence)
	}
	// Text after the closing fence in the same segment is not a fence chunk.
	mixed := "```\n" + strings.Repeat("x\n", 5000) + "```\ntail\n"
	if got := chunks(mixed); len(got) != 1 || got[0].fence {
		t.Fatalf("fence with trailing text = %d chunks, fence %v", len(got), got[0].fence)
	}
}

// An oversized fenced block is one chunk and still renders as a highlighted
// panel; an oversized block of any other kind renders as plain text.
func TestRenderOversizedChunks(t *testing.T) {
	defer func(b time.Duration) { parseBudget = b }(parseBudget)
	parseBudget = time.Hour // a loaded -race run must not turn this into a budget test
	code := strings.Repeat("func x\n", maxChunkBytes/7+10)
	opt := opts(20)
	calls := 0
	opt.Highlight = func(_, code string) ([][]CodeSegment, bool) {
		calls++
		var out [][]CodeSegment
		for _, line := range strings.Split(code, "\n") {
			out = append(out, []CodeSegment{{Text: line, Kind: Keyword}})
		}
		return out, true
	}
	rows := render(t, "Intro\n\n```go\n"+code+"```\n\nOutro", opt)
	if calls != 1 || len(rows) != strings.Count(code, "\n")+4 {
		t.Fatalf("fenced block: %d highlight calls, %d rows", calls, len(rows))
	}
	if s := styleOf(t, rows[2], "func"); fg(s) != "11" || bg(s) != "4" {
		t.Fatalf("oversized fence not a highlighted panel: fg=%q bg=%q", fg(s), bg(s))
	}

	para := strings.Repeat("**bold** ", maxChunkBytes/9+10)
	rows = render(t, para, opts(40))
	if !strings.Contains(rows[0].Text, "**bold**") {
		t.Fatalf("oversized paragraph parsed as markdown: %q", rows[0].Text)
	}
}

// Once the parse budget is spent, the chunks left render as plain text.
func TestRenderParseBudget(t *testing.T) {
	defer func(b time.Duration) { parseBudget = b }(parseBudget)
	section := "Some **bold** text.\n\n"
	src := strings.Repeat(section, 2*maxChunkBytes/len(section))
	parseBudget = time.Hour
	rows := render(t, src, opts(40))
	if last := rows[len(rows)-1].Text; last != "Some bold text." {
		t.Fatalf("within budget, last row = %q", last)
	}
	parseBudget = -time.Hour
	rows = render(t, src, opts(40))
	if first, last := rows[0].Text, rows[len(rows)-1].Text; first != "Some bold text." || last != "Some **bold** text." {
		t.Fatalf("over budget: first %q (want parsed), last %q (want plain)", first, last)
	}
}
