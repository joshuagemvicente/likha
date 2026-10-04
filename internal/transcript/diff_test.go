package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"likha/internal/actions"
)

// diffFixture writes files into a fresh repository root.
func diffFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// diffReview returns the approval body the edit tool shows for ops, the same
// text the tui holds in its pending review.
func diffReview(t *testing.T, root string, ops ...actions.EditOperation) string {
	t.Helper()
	proposal, err := actions.PrepareEdits(root, ops)
	if err != nil {
		t.Fatalf("PrepareEdits: %v", err)
	}
	body := "Affected files:\n" + strings.Join(proposal.Paths, "\n") + "\n"
	if len(proposal.Directories) > 0 {
		body += "\nRequired new directories:\n" + strings.Join(proposal.Directories, "\n") + "\n"
	}
	return body + "\n" + proposal.Diff
}

func numbered(n int, format string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, format+"\n", i)
	}
	return b.String()
}

func formatAll(s DiffSummary, width int, ellipsis string) []string {
	rows := make([]string, 0, len(s.Lines))
	for _, line := range s.Lines {
		rows = append(rows, FormatDiffLine(line, s.NumberWidth(), width, ellipsis))
	}
	return rows
}

func TestSummarizeEditModifyMatchesSpecExample(t *testing.T) {
	lines := strings.Split(numbered(15, "line %d"), "\n")
	lines[11] = "const n = 25"
	root := diffFixture(t, map[string]string{"src/app.ts": strings.Join(lines, "\n")})
	body := diffReview(t, root, actions.EditOperation{Kind: "replace", Path: "src/app.ts",
		OldText: "const n = 25\n", NewText: "const n = 27\nconst e = 17\n"})

	got := SummarizeEdit(ParseUnifiedDiff(body), DefaultDiffLines)
	if got.Summary != "Updated src/app.ts with 2 additions and 1 removal" {
		t.Fatalf("summary = %q", got.Summary)
	}
	want := []DiffLine{{12, '-', "const n = 25"}, {12, '+', "const n = 27"}, {13, '+', "const e = 17"}}
	if !reflect.DeepEqual(got.Lines, want) || got.More != 0 || got.OtherFiles != nil {
		t.Fatalf("lines = %+v more = %d others = %v", got.Lines, got.More, got.OtherFiles)
	}
	rows := formatAll(got, 80, "…")
	wantRows := []string{"12 - const n = 25", "12 + const n = 27", "13 + const e = 17"}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("rows = %q", rows)
	}
}

func TestSummarizeEditAddOnlyAndRemoveOnly(t *testing.T) {
	root := diffFixture(t, map[string]string{"a.go": "one\ntwo\nthree\n", "b.go": "keep\ndrop\nkeep too\n"})

	added := SummarizeEdit(ParseUnifiedDiff(diffReview(t, root,
		actions.EditOperation{Kind: "replace", Path: "a.go", OldText: "two\n", NewText: "two\nx\ny\n"})), DefaultDiffLines)
	if added.Summary != "Updated a.go with 2 additions" {
		t.Fatalf("add-only summary = %q", added.Summary)
	}
	if want := []DiffLine{{3, '+', "x"}, {4, '+', "y"}}; !reflect.DeepEqual(added.Lines, want) {
		t.Fatalf("add-only lines = %+v", added.Lines)
	}

	removed := SummarizeEdit(ParseUnifiedDiff(diffReview(t, root,
		actions.EditOperation{Kind: "replace", Path: "b.go", OldText: "drop\n", NewText: ""})), DefaultDiffLines)
	if removed.Summary != "Updated b.go with 1 removal" {
		t.Fatalf("remove-only summary = %q", removed.Summary)
	}
	if want := []DiffLine{{2, '-', "drop"}}; !reflect.DeepEqual(removed.Lines, want) {
		t.Fatalf("remove-only lines = %+v", removed.Lines)
	}
	if rows := formatAll(removed, 40, "…"); !reflect.DeepEqual(rows, []string{"2 - drop"}) {
		t.Fatalf("remove-only rows = %q", rows)
	}
}

func TestSummarizeEditSingularAndPluralWording(t *testing.T) {
	file := func(signs string) []FileDiff {
		f := FileDiff{Path: "p"}
		for i, sign := range []byte(signs) {
			f.Lines = append(f.Lines, DiffLine{Number: i + 1, Sign: sign, Text: "t"})
		}
		return []FileDiff{f}
	}
	cases := map[string]string{
		"+-":    "Updated p with 1 addition and 1 removal",
		"++-":   "Updated p with 2 additions and 1 removal",
		"+--":   "Updated p with 1 addition and 2 removals",
		"+":     "Updated p with 1 addition",
		"--":    "Updated p with 2 removals",
		" + ":   "Updated p with 1 addition",
		"   ":   "Updated p",
		"":      "Updated p",
		"-- ++": "Updated p with 2 additions and 2 removals",
	}
	for signs, want := range cases {
		if got := SummarizeEdit(file(signs), DefaultDiffLines).Summary; got != want {
			t.Errorf("signs %q: summary = %q, want %q", signs, got, want)
		}
	}
	// Context lines are counted nowhere and never shown.
	got := SummarizeEdit(file(" + "), DefaultDiffLines)
	if len(got.Lines) != 1 || got.Lines[0] != (DiffLine{2, '+', "t"}) || got.More != 0 {
		t.Fatalf("context leaked: %+v more %d", got.Lines, got.More)
	}
	if got := SummarizeEdit([]FileDiff{{Path: "c", Created: true, Lines: []DiffLine{{1, '+', "x"}}}}, 10).Summary; got != "Created c (1 line)" {
		t.Fatalf("singular create = %q", got)
	}
	if got := SummarizeEdit([]FileDiff{{Path: "c", Created: true}}, 10).Summary; got != "Created c (0 lines)" {
		t.Fatalf("empty create = %q", got)
	}
	if got := SummarizeEdit(nil, 10); !reflect.DeepEqual(got, DiffSummary{}) {
		t.Fatalf("no files = %+v", got)
	}
	// Created marks exactly the single-file create, for the Create(p) header.
	if !SummarizeEdit([]FileDiff{{Path: "c", Created: true}}, 10).Created {
		t.Fatal("single create not flagged Created")
	}
	if SummarizeEdit(file("+"), 10).Created {
		t.Fatal("update flagged Created")
	}
	if SummarizeEdit([]FileDiff{{Path: "c", Created: true}, {Path: "d", Created: true}}, 10).Created {
		t.Fatal("multi-file edit flagged Created")
	}
}

func TestSummarizeEditMultiFileShowsFirstAndNamesRest(t *testing.T) {
	root := diffFixture(t, map[string]string{
		"z.go":     "z1\nz2\n",
		"a.go":     "a1\na2\na3\n",
		"m/mid.go": "m1\n",
	})
	body := diffReview(t, root,
		actions.EditOperation{Kind: "replace", Path: "z.go", OldText: "z2\n", NewText: "Z2\n"},
		actions.EditOperation{Kind: "replace", Path: "a.go", OldText: "a2\n", NewText: "A2\nA2b\n"},
		actions.EditOperation{Kind: "replace", Path: "m/mid.go", OldText: "m1\n", NewText: ""},
		actions.EditOperation{Kind: "create", Path: "new/n.go", Content: "n1\nn2\n"},
	)
	files := ParseUnifiedDiff(body)
	if len(files) != 4 {
		t.Fatalf("parsed %d files from %q", len(files), body)
	}
	got := SummarizeEdit(files, DefaultDiffLines)
	// a: +2 -1, m/mid: -1, new/n: +2, z: +1 -1.
	if got.Summary != "Updated 4 files with 5 additions and 3 removals" {
		t.Fatalf("summary = %q", got.Summary)
	}
	if want := []DiffLine{{2, '-', "a2"}, {2, '+', "A2"}, {3, '+', "A2b"}}; !reflect.DeepEqual(got.Lines, want) {
		t.Fatalf("first-file lines = %+v", got.Lines)
	}
	if want := []string{"m/mid.go", "new/n.go", "z.go"}; !reflect.DeepEqual(got.OtherFiles, want) {
		t.Fatalf("others = %q", got.OtherFiles)
	}
	if !files[2].Created || files[0].Created {
		t.Fatalf("create flags: %+v", files)
	}
}

func TestSummarizeEditCreate(t *testing.T) {
	root := diffFixture(t, map[string]string{"README": "x\n"})
	got := SummarizeEdit(ParseUnifiedDiff(diffReview(t, root,
		actions.EditOperation{Kind: "create", Path: "docs/new.md", Content: "# Title\n\nbody"})), DefaultDiffLines)
	if got.Summary != "Created docs/new.md (3 lines)" {
		t.Fatalf("summary = %q", got.Summary)
	}
	want := []DiffLine{{1, '+', "# Title"}, {2, '+', ""}, {3, '+', "body"}}
	if !reflect.DeepEqual(got.Lines, want) {
		t.Fatalf("lines = %+v", got.Lines)
	}
	if rows := formatAll(got, 40, "…"); !reflect.DeepEqual(rows, []string{"1 + # Title", "2 + ", "3 + body"}) {
		t.Fatalf("rows = %q", rows)
	}

	// An empty created file still parses as a created file with no lines.
	empty := SummarizeEdit(ParseUnifiedDiff(diffReview(t, root,
		actions.EditOperation{Kind: "create", Path: "empty.txt", Content: ""})), DefaultDiffLines)
	if empty.Summary != "Created empty.txt (0 lines)" || empty.Lines != nil {
		t.Fatalf("empty create = %+v", empty)
	}
}

func TestSummarizeEditFullFileEditTool(t *testing.T) {
	root := diffFixture(t, map[string]string{"f.txt": "a\nb\nc\n"})
	edit, err := actions.PrepareEdit(root, "f.txt", "a\nB\nc\n")
	if err != nil {
		t.Fatal(err)
	}
	got := SummarizeEdit(ParseUnifiedDiff(edit.Diff), DefaultDiffLines)
	if got.Summary != "Updated f.txt with 1 addition and 1 removal" ||
		!reflect.DeepEqual(got.Lines, []DiffLine{{2, '-', "b"}, {2, '+', "B"}}) {
		t.Fatalf("edit_file summary = %+v", got)
	}
	created, err := actions.PrepareEdit(root, "g.txt", "only\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := SummarizeEdit(ParseUnifiedDiff(created.Diff), DefaultDiffLines).Summary; got != "Created g.txt (1 line)" {
		t.Fatalf("edit_file create = %q", got)
	}
}

func TestSummarizeEditMoreThanMaxLines(t *testing.T) {
	root := diffFixture(t, map[string]string{"big.txt": numbered(40, "old %d")})
	var old, next strings.Builder
	for i := 5; i <= 12; i++ {
		fmt.Fprintf(&old, "old %d\n", i)
		fmt.Fprintf(&next, "new %d\n", i)
	}
	got := SummarizeEdit(ParseUnifiedDiff(diffReview(t, root,
		actions.EditOperation{Kind: "replace", Path: "big.txt", OldText: old.String(), NewText: next.String()})), DefaultDiffLines)
	if got.Summary != "Updated big.txt with 8 additions and 8 removals" {
		t.Fatalf("summary = %q", got.Summary)
	}
	if len(got.Lines) != 10 || got.More != 6 {
		t.Fatalf("lines %d more %d", len(got.Lines), got.More)
	}
	// Removals come first (old numbers 5-12), then additions (new numbers).
	if got.Lines[0] != (DiffLine{5, '-', "old 5"}) || got.Lines[7] != (DiffLine{12, '-', "old 12"}) ||
		got.Lines[8] != (DiffLine{5, '+', "new 5"}) || got.Lines[9] != (DiffLine{6, '+', "new 6"}) {
		t.Fatalf("lines = %+v", got.Lines)
	}
	if rows := formatAll(got, 80, "…"); rows[0] != " 5 - old 5" || rows[7] != "12 - old 12" {
		t.Fatalf("aligned rows = %q", rows)
	}

	created := SummarizeEdit([]FileDiff{{Path: "c", Created: true, Lines: func() []DiffLine {
		var lines []DiffLine
		for i := 1; i <= 25; i++ {
			lines = append(lines, DiffLine{i, '+', "x"})
		}
		return lines
	}()}}, 10)
	if created.Summary != "Created c (25 lines)" || len(created.Lines) != 10 || created.More != 15 {
		t.Fatalf("create cap = %q %d %d", created.Summary, len(created.Lines), created.More)
	}
	if none := SummarizeEdit([]FileDiff{{Path: "c", Lines: []DiffLine{{1, '+', "x"}}}}, -1); none.Lines != nil || none.More != 1 {
		t.Fatalf("negative cap = %+v", none)
	}
}

func TestParseUnifiedDiffConsumesHunksByCount(t *testing.T) {
	// The removed line "-- x" appears as "--- x" and the added "++ y" as
	// "+++ y": inside a hunk neither is a file header.
	diff := "--- a/s.sql\n+++ b/s.sql\n@@ -1,3 +1,3 @@\n keep\n--- x\n+++ y\n tail\n" +
		"--- a/t.txt\n+++ b/t.txt\n@@ -7 +7 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n"
	files := ParseUnifiedDiff(diff)
	want := []FileDiff{
		{Path: "s.sql", Lines: []DiffLine{{1, ' ', "keep"}, {2, '-', "-- x"}, {2, '+', "++ y"}, {3, ' ', "tail"}}},
		{Path: "t.txt", Lines: []DiffLine{{7, '-', "old"}, {7, '+', "new"}}},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %+v", files)
	}
	// Text before the first section is ignored, including look-alike lines
	// that are not followed by a hunk header.
	prefixed := "Affected files:\n--- odd\n+++ name\n\n" + diff
	if got := ParseUnifiedDiff(prefixed); !reflect.DeepEqual(got, want) {
		t.Fatalf("prefixed = %+v", got)
	}
	if got := ParseUnifiedDiff("no diff here\n"); got != nil {
		t.Fatalf("plain text = %+v", got)
	}
	// A truncated hunk keeps the lines read so far.
	cut := ParseUnifiedDiff("--- a/c\n+++ b/c\n@@ -1,3 +1,3 @@\n-a\n")
	if len(cut) != 1 || !reflect.DeepEqual(cut[0].Lines, []DiffLine{{1, '-', "a"}}) {
		t.Fatalf("truncated = %+v", cut)
	}
}

func TestEditFromArgumentsFallback(t *testing.T) {
	args := `{"operations":[
		{"kind":"replace","path":"./src/b.go","old_text":"x := 1\ny := 2\nz := 3\n","new_text":"x := 1\ny := 20\nz := 3\nw := 4\n"},
		{"kind":"create","path":"a/new.txt","content":"one\ntwo\n"}]}`
	files, ok := EditFromArguments("edit", args)
	if !ok {
		t.Fatal("edit arguments not accepted")
	}
	want := []FileDiff{
		{Path: "a/new.txt", Created: true, Lines: []DiffLine{{1, '+', "one"}, {2, '+', "two"}}},
		{Path: "src/b.go", Lines: []DiffLine{{0, '-', "y := 2"}, {0, '+', "y := 20"}, {0, '+', "w := 4"}}},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %+v", files)
	}
	got := SummarizeEdit(files, DefaultDiffLines)
	if got.Summary != "Updated 2 files with 4 additions and 1 removal" || !reflect.DeepEqual(got.OtherFiles, []string{"src/b.go"}) {
		t.Fatalf("summary = %+v", got)
	}

	single, _ := EditFromArguments("edit", `{"operations":[{"kind":"replace","path":"p","old_text":"a\nb\n","new_text":"b\nc\n"}]}`)
	s := SummarizeEdit(single, DefaultDiffLines)
	if s.Summary != "Updated p with 1 addition and 1 removal" || s.NumberWidth() != 0 {
		t.Fatalf("single = %+v width %d", s, s.NumberWidth())
	}
	if rows := formatAll(s, 40, "…"); !reflect.DeepEqual(rows, []string{"- a", "+ c"}) {
		t.Fatalf("unnumbered rows = %q", rows)
	}

	for _, bad := range []struct{ name, args string }{
		{"edit_file", `{"path":"p","content":"x"}`},
		{"read", `{"path":"p"}`},
		{"edit", `not json`},
		{"edit", `{"operations":[]}`},
		{"edit", `{"operations":[{"kind":"delete","path":"p"}]}`},
		{"edit", `{"operations":[{"kind":"create","path":"","content":"x"}]}`},
	} {
		if files, ok := EditFromArguments(bad.name, bad.args); ok || files != nil {
			t.Errorf("%s %s accepted: %+v", bad.name, bad.args, files)
		}
	}
}

func TestDiffTextLinesLargeChangeStaysBounded(t *testing.T) {
	var old, next []string
	for i := 0; i < 1000; i++ {
		old = append(old, fmt.Sprintf("o%d", i))
		next = append(next, fmt.Sprintf("n%d", i))
	}
	lines := diffTextLines(old, next)
	if len(lines) != 2000 || lines[0].Sign != '-' || lines[999].Sign != '-' || lines[1000].Sign != '+' {
		t.Fatalf("large diff: %d lines, first %+v", len(lines), lines[0])
	}
}

func TestFormatDiffLineTruncationAndNeutralizing(t *testing.T) {
	line := DiffLine{Number: 7, Sign: '+', Text: "return computeSomethingLong(argument)"}
	if got := FormatDiffLine(line, 3, 80, "…"); got != "  7 + return computeSomethingLong(argument)" {
		t.Fatalf("padded = %q", got)
	}
	if got := FormatDiffLine(line, 1, 20, "…"); got != "7 + return computeS…" || runewidth.StringWidth(got) != 20 {
		t.Fatalf("unicode ellipsis = %q", got)
	}
	if got := FormatDiffLine(line, 1, 20, "..."); got != "7 + return comput..." {
		t.Fatalf("ascii ellipsis = %q", got)
	}
	if got := FormatDiffLine(DiffLine{Number: 1234, Sign: '-', Text: "x"}, 2, 40, "…"); got != "1234 - x" {
		t.Fatalf("number wider than column = %q", got)
	}
	if got := FormatDiffLine(DiffLine{Sign: '+', Text: "x"}, 3, 40, "…"); got != "    + x" {
		t.Fatalf("unknown number = %q", got)
	}
	if got := FormatDiffLine(DiffLine{Number: 3, Sign: '?', Text: "ctx"}, 1, 40, "…"); got != "3   ctx" {
		t.Fatalf("context sign = %q", got)
	}
	if got := FormatDiffLine(DiffLine{Number: 1, Sign: '+', Text: "\tif x {\x1b[31m\u200b}\r"}, 1, 80, "…"); got != `1 +     if x {\u001B[31m\u200B}\u000D` {
		t.Fatalf("neutralized = %q", got)
	}
	// Escapes are cut whole, never split into a partial \u sequence.
	if got := FormatDiffLine(DiffLine{Number: 1, Sign: '+', Text: "ab\x00cd"}, 1, 9, "…"); got != "1 + ab…" {
		t.Fatalf("escape cut = %q", got)
	}
	if got := FormatDiffLine(line, 1, 0, "…"); got != "" {
		t.Fatalf("zero width = %q", got)
	}
	if got := FormatDiffLine(line, 1, 2, "..."); got != "7 " {
		t.Fatalf("ellipsis wider than width = %q", got)
	}
}

func TestFormatDiffLineWideRunesFitEveryWidth(t *testing.T) {
	lines := []DiffLine{
		{Number: 12, Sign: '+', Text: "名前 := \"日本語のテキスト\" // 🎉 done"},
		{Number: 9, Sign: '-', Text: "\t\tx́ := 'é' \x07 한국어"},
		{Number: 100, Sign: '+', Text: strings.Repeat("界", 50)},
	}
	for _, ellipsis := range []string{"…", "..."} {
		for _, line := range lines {
			for width := 0; width <= 60; width++ {
				got := FormatDiffLine(line, 3, width, ellipsis)
				if cells := runewidth.StringWidth(got); cells > width {
					t.Fatalf("width %d %q: %q is %d cells", width, ellipsis, got, cells)
				}
				if strings.ContainsAny(got, "\x07\t́") {
					t.Fatalf("hidden rune survived: %q", got)
				}
			}
		}
	}
	// A wide rune that does not fit is dropped whole, leaving one cell short.
	if got := FormatDiffLine(DiffLine{Number: 1, Sign: '+', Text: "日本語"}, 1, 8, "…"); got != "1 + 日…" {
		t.Fatalf("wide cut = %q", got)
	}
}

func TestSummarizeEditNeutralizesPaths(t *testing.T) {
	files := []FileDiff{
		{Path: "a\tb\u202e.go", Lines: []DiffLine{{1, '+', "x"}}},
		{Path: "c\nd.go"},
	}
	got := SummarizeEdit(files, DefaultDiffLines)
	if got.Summary != "Updated 2 files with 1 addition" || !reflect.DeepEqual(got.OtherFiles, []string{"c d.go"}) {
		t.Fatalf("multi = %+v", got)
	}
	single := SummarizeEdit(files[:1], DefaultDiffLines)
	if single.Summary != "Updated a b\\u202E.go with 1 addition" {
		t.Fatalf("single = %q", single.Summary)
	}
}
