package transcript

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"likha/internal/explore"
)

func TestFlattenRow(t *testing.T) {
	cases := []struct{ in, want string }{
		{" a\n\tb\r\n c ", "a b c"},
		{"nb\u00a0sp", "nb sp"},
		{"esc\x1b[31m", `esc\u001B[31m`},
		{"v\vf\fnel\u0085", `v\u000Bf\u000Cnel\u0085`},
		{"ls\u2028ps\u2029", `ls\u2028ps\u2029`},
		{"bidi\u202ezw\u200b", `bidi\u202Ezw\u200B`},
	}
	for _, c := range cases {
		if got := flattenRow(c.in); got != c.want {
			t.Errorf("flattenRow(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSurfacesNeutralizeAlike pins one escaping rule for every surface: the
// footer model, a task target, a diff path, and a reasoning row all show the
// same hidden runes as the same \uXXXX text.
func TestSurfacesNeutralizeAlike(t *testing.T) {
	raw := "a\x1b\vb\u2028c\u200bd"
	want := `a\u001B\u000Bb\u2028c\u200Bd`
	if got := FooterText(TurnInfo{Model: raw}, 80, "…"); got != want+" · 0.0s" {
		t.Errorf("footer = %q", got)
	}
	rec := explore.Record{Status: explore.Running, Tools: []explore.ToolRecord{{Name: "run_command", Arguments: `{"command":"a\u001b\u000bb\u2028c\u200Bd"}`}}}
	if got := LiveLine(rec, nil); got != "Run "+want {
		t.Errorf("task = %q", got)
	}
	if got := SummarizeEdit([]FileDiff{{Path: raw}}, 10).Summary; got != "Updated "+want {
		t.Errorf("diff path = %q", got)
	}
	if rows, _ := Tail(raw, 80, 3); len(rows) != 1 || rows[0] != want {
		t.Errorf("reasoning = %q", rows)
	}
	if got := FormatDiffLine(DiffLine{Sign: '+', Text: raw}, 0, 80, "…"); got != "+ "+want {
		t.Errorf("diff line = %q", got)
	}
}

func TestClipEllipsisRule(t *testing.T) {
	cases := []struct {
		text     string
		width    int
		ellipsis string
		right    string
		left     string
	}{
		{"abcdef", 6, "…", "abcdef", "abcdef"},
		{"abcdef", 4, "…", "abc…", "…def"},
		{"abcdef", 4, "...", "a...", "...f"},
		{"abcdef", 1, "…", "…", "…"},
		{"abcdef", 2, "...", "ab", "ef"},
		{"ab cdef", 4, "…", "ab…", "…def"},
		{"模型模型", 5, "…", "模型…", "…模型"},
		{"abc", 0, "…", "", ""},
	}
	for _, c := range cases {
		if got := clipRight(c.text, c.width, c.ellipsis); got != c.right {
			t.Errorf("clipRight(%q, %d, %q) = %q, want %q", c.text, c.width, c.ellipsis, got, c.right)
		}
		if got := clipLeft(c.text, c.width, c.ellipsis); got != c.left {
			t.Errorf("clipLeft(%q, %d, %q) = %q, want %q", c.text, c.width, c.ellipsis, got, c.left)
		}
	}
}

// TestDurationShapesAgree pins the shared minute/hour shape across the
// footer, the thought marker, and the task settle line.
func TestDurationShapesAgree(t *testing.T) {
	d := 64 * time.Second
	if got := TurnDuration(d); got != "1m 4s" {
		t.Errorf("footer = %q", got)
	}
	if got := ThoughtMarker(d, true); got != "Thought for 1m 4s" {
		t.Errorf("marker = %q", got)
	}
	rec := explore.Record{Status: explore.Completed, WaitMs: d.Milliseconds(), ActiveMs: (time.Hour + 2*time.Minute + 3*time.Second).Milliseconds()}
	if got, _ := Settle(rec); got != "Done · 0 tool uses · wait 1m 4s · active 1h 2m" {
		t.Errorf("settle = %q", got)
	}
}

// TestSurfacesFitEveryWidthFuzz feeds random untrusted text through every
// width-bounded entry point and checks the overflow and no-hidden-rune
// invariants.
func TestSurfacesFitEveryWidthFuzz(t *testing.T) {
	pool := []rune("aZ /.-_:\\\"\t\n\r\x1b\x00\x7f\v\u200b\u200d\u202e\u2028\u00a0\u0301\ufe0f世界🙂👍🏽…·─é\U000E0001")
	r := rand.New(rand.NewSource(1))
	random := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteRune(pool[r.Intn(len(pool))])
		}
		return b.String()
	}
	check := func(label, s string, width int) {
		t.Helper()
		if w := runewidth.StringWidth(s); w > width {
			t.Fatalf("%s: %q is %d cells, width %d", label, s, w, width)
		}
		for _, c := range s {
			if hiddenRune(c) {
				t.Fatalf("%s: hidden rune %U in %q", label, c, s)
			}
		}
	}
	for range 2000 {
		text := random(r.Intn(40))
		width := 1 + r.Intn(40)
		for _, ellipsis := range []string{"…", "..."} {
			check("footer", FooterText(TurnInfo{Model: text, Duration: time.Duration(r.Int63n(int64(5 * time.Hour))), Tools: r.Intn(3), Cancelled: r.Intn(2) == 0}, width, ellipsis), width)
			check("diff", FormatDiffLine(DiffLine{Number: r.Intn(2000), Sign: "+- "[r.Intn(3)], Text: text}, r.Intn(5), width, ellipsis), width)
			rec := explore.Record{Status: explore.Running, Tools: []explore.ToolRecord{{Name: text, Arguments: `{"k":` + strconvQuote(text) + `}`}}}
			check("live", FitLiveLine(rec, nil, width, ellipsis), width)
			rec.Status, rec.Reason = explore.Failed, text
			settled, _ := FitSettle(rec, width, ellipsis)
			check("settle", settled, width)
			rows, _ := Tail(text, width, 3)
			for _, row := range rows {
				check("tail", row, width)
			}
		}
	}
}

func strconvQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		default:
			if c < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte("0123456789abcdef"[c>>4])
				b.WriteByte("0123456789abcdef"[c&15])
				continue
			}
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
