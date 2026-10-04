package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"likha/internal/agent"
	"likha/internal/highlight"
	"likha/internal/markdown"
	"likha/internal/providers"
	"likha/internal/session"
	likhaui "likha/internal/ui"
)

// Assistant markdown through the real rebuild (spec transcript-redesign §
// Markdown, § Assistant text, acceptance 7-8).

const markdownSample = "# Plan\n\nSome **bold** words and `inline` code.\n\n```go\nfunc main() {}\n```\n\nAfter the code."

func markdownTestUI(t *testing.T, theme string, ascii bool, width int) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true, Theme: theme, ASCII: ascii}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return m
}

// lineWith returns the first layout line at or after from containing sub.
func lineWith(t *testing.T, m *ui, from int, sub string) int {
	t.Helper()
	for i := from; i < len(m.lines); i++ {
		if strings.Contains(m.lines[i], sub) {
			return i
		}
	}
	t.Fatalf("no layout line contains %q: %q", sub, m.lines)
	return -1
}

// runOver returns the run painting rune i of layout line n.
func runOver(m *ui, n, i int) (lineRun, bool) {
	for _, run := range m.lineRuns[n] {
		if run.start <= i && i < run.end {
			return run, true
		}
	}
	return lineRun{}, false
}

// runeIndex is the rune offset of sub in s.
func runeIndex(t *testing.T, s, sub string) int {
	t.Helper()
	at := strings.Index(s, sub)
	if at < 0 {
		t.Fatalf("%q not in %q", sub, s)
	}
	return utf8.RuneCountInString(s[:at])
}

func TestAssistantMarkdownThroughRebuild(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		m := markdownTestUI(t, "catppuccin", false, width)
		starts := layoutEntries(m, entry{role: "You", content: "go"}, entry{role: "Assistant", content: markdownSample}, entry{role: "You", content: "next"})
		first := starts[1]
		if got := m.lines[first]; got != "⏺ Plan" {
			t.Fatalf("width %d: heading row %q, want marker hidden behind the gutter", width, got)
		}
		for i := first; i < starts[2]; i++ {
			for _, raw := range []string{"#", "**", "`", "```"} {
				if strings.Contains(m.lines[i], raw) {
					t.Fatalf("width %d: row %q shows markdown syntax %q", width, m.lines[i], raw)
				}
			}
		}
		if run, ok := runOver(m, first, 2); !ok || !run.style.GetBold() || colorName(run.style.GetForeground()) != colorName(m.theme.Title.GetForeground()) || !run.style.GetUnderline() {
			t.Fatalf("width %d: heading run %+v ok=%t, want bold underlined Title", width, run, ok)
		}
		bold := lineWith(t, m, first, "bold")
		if run, ok := runOver(m, bold, runeIndex(t, m.lines[bold], "bold")); !ok || !run.style.GetBold() {
			t.Fatalf("width %d: no bold run over %q", width, m.lines[bold])
		}
		inline := lineWith(t, m, first, "inline")
		run, ok := runOver(m, inline, runeIndex(t, m.lines[inline], "inline"))
		if !ok || colorName(run.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) || colorName(run.style.GetBackground()) != colorName(m.theme.BgCode.GetBackground()) {
			t.Fatalf("width %d: inline code run %+v, want Accent on BgCode", width, run)
		}

		code := lineWith(t, m, first, "func main() {}")
		text := m.lines[code]
		if !strings.HasPrefix(text, "  func") {
			t.Fatalf("width %d: code row %q does not hang under the text column", width, text)
		}
		if got := runewidth.StringWidth(text); got != contentWidth(width) {
			t.Fatalf("width %d: code panel row is %d cells, want the full text column %d", width, got, contentWidth(width))
		}
		codeBG := colorName(m.theme.BgCode.GetBackground())
		for i := range utf8.RuneCountInString(text) {
			run, ok := runOver(m, code, i)
			if i < 2 {
				if ok {
					t.Fatalf("width %d: indent cell %d painted by %+v; it belongs to the canvas", width, i, run)
				}
				continue
			}
			if !ok || colorName(run.style.GetBackground()) != codeBG {
				t.Fatalf("width %d: code cell %d not on BgCode (%+v ok=%t)", width, i, run, ok)
			}
		}
		if colorName(m.lineStyles[code].GetBackground()) != colorName(m.theme.Base.GetBackground()) {
			t.Fatalf("width %d: code row canvas %v, want Base", width, m.lineStyles[code].GetBackground())
		}
		kw, ok := runOver(m, code, runeIndex(t, text, "func"))
		if !ok || colorName(kw.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) {
			t.Fatalf("width %d: keyword run %+v, want Accent", width, kw)
		}
		if punct, ok := runOver(m, code, runeIndex(t, text, "()")); !ok || colorName(punct.style.GetForeground()) != colorName(m.theme.Normal.GetForeground()) {
			t.Fatalf("width %d: punctuation run %+v, want Normal on the panel", width, punct)
		}
		for i := first; i < len(m.lines); i++ {
			if w := runewidth.StringWidth(m.lines[i]); w > contentWidth(width) {
				t.Fatalf("width %d: row %d is %d cells: %q", width, i, w, m.lines[i])
			}
		}
		// The next block's band still starts where entryLines says.
		if m.lines[starts[2]+1] != "> next" {
			t.Fatalf("width %d: entryLines out of step: row %q", width, m.lines[starts[2]+1])
		}
	}
}

func TestAssistantMarkdownDefaultThemeUsesANSI16(t *testing.T) {
	m := markdownTestUI(t, "default", false, 80)
	layoutEntries(m, entry{role: "Assistant", content: "```go\n// note\nfunc f() string { return \"s\" }\n```"})
	code := lineWith(t, m, 0, "func f()")
	text := m.lines[code]
	normal := colorName(m.theme.Normal.GetForeground())
	want := map[string]string{"func": "Color(5)", "\"s\"": "Color(2)", "f()": normal, "string": normal}
	for sub, color := range want {
		run, ok := runOver(m, code, runeIndex(t, text, sub))
		if !ok || colorName(run.style.GetForeground()) != color {
			t.Fatalf("%s run %+v, want %s", sub, run, color)
		}
	}
	comment := lineWith(t, m, 0, "// note")
	if run, ok := runOver(m, comment, runeIndex(t, m.lines[comment], "note")); !ok || colorName(run.style.GetForeground()) != "Color(8)" || !run.style.GetItalic() {
		t.Fatalf("comment run %+v, want italic ANSI 8", run)
	}
}

// Spec § Markdown: keywords Accent, strings Warning, comments Muted, others
// Normal. Function names, types, builtins and numbers are "others".
func TestAssistantMarkdownCodeColorsFollowSpec(t *testing.T) {
	m := markdownTestUI(t, "catppuccin", false, 80)
	layoutEntries(m, entry{role: "Assistant", content: "```go\n// c\nfunc Add(a int) int { return a + 42 + len(\"s\") }\n```"})
	code := lineWith(t, m, 0, "func Add")
	text := m.lines[code]
	normal := colorName(m.theme.Normal.GetForeground())
	want := map[string]string{
		"func":   colorName(m.theme.Accent.GetForeground()),
		"return": colorName(m.theme.Accent.GetForeground()),
		"\"s\"":  colorName(m.theme.Warning.GetForeground()),
		"Add":    normal,
		"int":    normal,
		"42":     normal,
		"len":    normal,
	}
	for sub, color := range want {
		run, ok := runOver(m, code, runeIndex(t, text, sub))
		if !ok || colorName(run.style.GetForeground()) != color {
			t.Fatalf("%s run %+v, want %s", sub, run, color)
		}
		if colorName(run.style.GetBackground()) != colorName(m.theme.BgCode.GetBackground()) {
			t.Fatalf("%s run %+v not on BgCode", sub, run)
		}
	}
	comment := lineWith(t, m, 0, "// c")
	if run, ok := runOver(m, comment, runeIndex(t, m.lines[comment], "// c")+3); !ok || colorName(run.style.GetForeground()) != colorName(m.theme.Muted.GetForeground()) {
		t.Fatalf("comment run %+v, want Muted", run)
	}
}

func TestAssistantMarkdownStreamingUnclosedFence(t *testing.T) {
	m := markdownTestUI(t, "catppuccin", false, 60)
	m.working, m.runID = true, 7
	m.streaming, m.reasoningStream = -1, -1
	m.Update(agent.TurnEvent{RunID: 7, Kind: "text", Text: "Here:\n\n```go\nfunc a() {}\n"})
	m.View()
	open := lineWith(t, m, 0, "func a() {}")
	if run, ok := runOver(m, open, 2); !ok || colorName(run.style.GetBackground()) != colorName(m.theme.BgCode.GetBackground()) {
		t.Fatalf("unclosed fence is not an open code panel: %q", m.lines)
	}
	m.Update(agent.TurnEvent{RunID: 7, Kind: "text", Text: "```\n\nDone **now**."})
	view := stripANSI(m.View())
	if strings.Contains(view, "```") || strings.Contains(view, "**") {
		t.Fatalf("closed fence left raw markdown: %q", view)
	}
	done := lineWith(t, m, 0, "Done now.")
	if run, ok := runOver(m, done, 0); ok && colorName(run.style.GetBackground()) == colorName(m.theme.BgCode.GetBackground()) {
		t.Fatalf("prose after the closed fence still on the code panel: %+v", run)
	}
	if run, ok := runOver(m, done, runeIndex(t, m.lines[done], "now")); !ok || !run.style.GetBold() {
		t.Fatalf("prose after the fence lost its bold run: %q", m.lines[done])
	}
}

func TestAssistantMarkdownSwatchInProseNotCode(t *testing.T) {
	m := swatchTestUI(t, "Assistant", "the **accent** is #4493f8 here\n\n```\ncolor: #ff0000\n```")
	view := m.View()
	if !strings.Contains(view, "104m#4493f8") {
		t.Fatalf("prose hex literal painted no swatch: %q", view)
	}
	if strings.Contains(view, "101m#ff0000") {
		t.Fatalf("hex literal inside a code block painted a swatch: %q", view)
	}
	got := stripANSI(view)
	if !strings.Contains(got, "the accent is #4493f8 here") || !strings.Contains(got, "color: #ff0000") {
		t.Fatalf("swatch altered text: %q", got)
	}
	for i, row := range strings.Split(view, "\n") {
		if w := plainWidth(row); w != 80 {
			t.Fatalf("row %d width %d != 80: %q", i, w, stripANSI(row))
		}
	}
	// The bold run keeps its cells; the swatch cuts in without overlap.
	line := lineWith(t, m, 0, "accent")
	pos := -1
	for _, run := range m.lineRuns[line] {
		if run.start < pos || run.start >= run.end {
			t.Fatalf("runs overlap or are empty: %+v", m.lineRuns[line])
		}
		pos = run.end
	}
	if run, ok := runOver(m, line, runeIndex(t, m.lines[line], "accent")); !ok || !run.style.GetBold() {
		t.Fatalf("bold run lost next to a swatch: %+v", m.lineRuns[line])
	}
}

func TestOverlaySwatchesSplitsRuns(t *testing.T) {
	a, b := lipgloss.NewStyle().Bold(true), lipgloss.NewStyle().Italic(true)
	runs := []lineRun{{start: 2, end: 8, style: &a}, {start: 8, end: 12, style: &b}}
	got := overlaySwatches(runs, []likhaui.Swatch{{Start: 4, End: 8, Hex: "#ff0000"}}, 2)
	want := [][2]int{{2, 6}, {6, 10}, {10, 12}}
	if len(got) != len(want) {
		t.Fatalf("runs %+v, want spans %v", got, want)
	}
	for i, w := range want {
		if got[i].start != w[0] || got[i].end != w[1] {
			t.Fatalf("run %d = [%d,%d), want %v", i, got[i].start, got[i].end, w)
		}
	}
	if !got[0].style.GetBold() || !got[2].style.GetItalic() || got[1].style.GetBold() || got[1].style.GetItalic() {
		t.Fatalf("swatch did not override the covered cells: %+v", got)
	}
}

func TestAssistantMarkdownThemeSwitchRerenders(t *testing.T) {
	m := markdownTestUI(t, "catppuccin", false, 80)
	layoutEntries(m, entry{role: "Assistant", content: "```go\nfunc main() {}\n```"})
	code := lineWith(t, m, 0, "func")
	before, _ := runOver(m, code, 2)
	m.previewTheme("github")
	m.View()
	code = lineWith(t, m, 0, "func")
	after, _ := runOver(m, code, 2)
	if colorName(after.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) {
		t.Fatalf("keyword after switch %v, want github Accent %v", after.style.GetForeground(), m.theme.Accent.GetForeground())
	}
	if colorName(after.style.GetForeground()) == colorName(before.style.GetForeground()) {
		t.Fatalf("theme switch kept the old keyword color %v", before.style.GetForeground())
	}
	if len(m.markdownCache) != 1 {
		t.Fatalf("cache holds %d layouts after a theme switch, want only the current one", len(m.markdownCache))
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	m.View()
	if len(m.markdownCache) != 1 {
		t.Fatalf("cache holds %d layouts after a resize, want only the current one", len(m.markdownCache))
	}
	for key := range m.markdownCache {
		if key.width != contentWidth(60) {
			t.Fatalf("cached layout width %d after resize to 60", key.width)
		}
	}
}

func TestAssistantMarkdownCacheReusesUnchangedEntries(t *testing.T) {
	m := markdownTestUI(t, "catppuccin", false, 80)
	layoutEntries(m, entry{role: "Assistant", content: "**kept** answer"})
	kept := m.lineRuns[0]
	m.entries = append(m.entries, entry{role: "Assistant", content: "streamed"})
	m.layoutWidth = 0
	m.rebuild()
	if &m.lineRuns[0][0] != &kept[0] {
		t.Fatal("unchanged assistant entry re-rendered instead of reusing its cached rows")
	}
	// The growing (streaming) entry renders fresh each time its text changes.
	m.entries[1].content = "streamed **more**"
	m.layoutWidth = 0
	m.rebuild()
	last := lineWith(t, m, 1, "streamed more")
	if run, ok := runOver(m, last, runeIndex(t, m.lines[last], "more")); !ok || !run.style.GetBold() {
		t.Fatalf("grown entry not re-rendered: %q", m.lines[last])
	}
	if len(m.markdownCache) != 2 {
		t.Fatalf("cache holds %d entries, want the 2 current ones", len(m.markdownCache))
	}
}

// countHighlights counts highlight runs per code block for the test.
func countHighlights(t *testing.T) map[string]int {
	t.Helper()
	calls := map[string]int{}
	orig := runHighlight
	runHighlight = func(lang, code string) ([][]markdown.CodeSegment, bool) {
		calls[lang+"|"+code]++
		return orig(lang, code)
	}
	t.Cleanup(func() { runHighlight = orig })
	return calls
}

// Highlighting depends on neither width nor theme: a closed code block is
// lexed once, then reused across resizes, theme switches, and the streaming
// entry's re-renders; results for blocks that left the transcript are dropped.
func TestAssistantMarkdownHighlightMemoized(t *testing.T) {
	calls := countHighlights(t)
	m := markdownTestUI(t, "catppuccin", false, 80)
	layoutEntries(m, entry{role: "Assistant", content: "```go\nfunc a() {}\n```"})
	block := "go|func a() {}"
	if calls[block] != 1 {
		t.Fatalf("first layout highlighted %d times, want 1", calls[block])
	}
	m.Update(tea.WindowSizeMsg{Width: 61, Height: 40})
	m.View()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.View()
	m.previewTheme("github")
	m.View()
	if calls[block] != 1 {
		t.Fatalf("resizes and a theme switch re-highlighted the block: %d calls", calls[block])
	}
	code := lineWith(t, m, 0, "func a()")
	if kw, ok := runOver(m, code, 2); !ok || colorName(kw.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) {
		t.Fatalf("memoized highlight lost the new theme's keyword color: %+v", kw)
	}
	layoutEntries(m, entry{role: "Assistant", content: "plain now"})
	if len(m.highlightCache) != 0 {
		t.Fatalf("highlight cache kept %d results for blocks no longer shown", len(m.highlightCache))
	}
}

// While streaming, closed blocks reuse their highlight and the open fence
// stays plain (no re-lexing of the growing code every delta); once the
// stream ends the last block is highlighted.
func TestAssistantMarkdownStreamingHighlightsClosedBlocksOnce(t *testing.T) {
	calls := countHighlights(t)
	m := markdownTestUI(t, "catppuccin", false, 80)
	m.working, m.runID = true, 7
	m.streaming, m.reasoningStream = -1, -1
	m.Update(agent.TurnEvent{RunID: 7, Kind: "text", Text: "```go\nfunc a() {}\n```\n\n```go\nfunc b() {\n"})
	m.View()
	for i := range 5 {
		m.Update(agent.TurnEvent{RunID: 7, Kind: "text", Text: fmt.Sprintf("\treturn %d\n", i)})
		m.View()
	}
	if calls["go|func a() {}"] != 1 {
		t.Fatalf("closed block highlighted %d times while streaming, want 1", calls["go|func a() {}"])
	}
	for k, n := range calls {
		if strings.HasPrefix(k, "go|func b()") {
			t.Fatalf("open fence highlighted while streaming (%d calls for %q)", n, k)
		}
	}
	open := lineWith(t, m, 0, "func b()")
	if kw, ok := runOver(m, open, 2); !ok || colorName(kw.style.GetForeground()) == colorName(m.theme.Accent.GetForeground()) || colorName(kw.style.GetBackground()) != colorName(m.theme.BgCode.GetBackground()) {
		t.Fatalf("open fence should be a plain code panel: %+v", kw)
	}
	closed := lineWith(t, m, 0, "func a()")
	if kw, ok := runOver(m, closed, 2); !ok || colorName(kw.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) {
		t.Fatalf("closed block lost its highlight while streaming: %+v", kw)
	}
	// The stream ends (a tool call starts): the last block is highlighted.
	m.streaming = -1
	m.layoutWidth = 0
	m.View()
	open = lineWith(t, m, 0, "func b()")
	if kw, ok := runOver(m, open, 2); !ok || colorName(kw.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) {
		t.Fatalf("finished answer's last block not highlighted: %+v", kw)
	}
	if calls["go|func a() {}"] != 1 {
		t.Fatalf("closed block re-highlighted after the stream ended: %d", calls["go|func a() {}"])
	}
}

func TestAssistantMarkdownASCIIGlyphs(t *testing.T) {
	m := markdownTestUI(t, "catppuccin", true, 60)
	starts := layoutEntries(m, entry{role: "Assistant", content: "- one\n- two\n\n> quoted\n\n---"})
	want := []string{"* - one", "  - two", "", "  | quoted", "", "  " + strings.Repeat("-", contentWidth(60)-2)}
	got := m.lines[starts[0]:]
	if len(got) != len(want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
	for _, line := range got {
		for _, r := range line {
			if r > 0x7e {
				t.Fatalf("non-ASCII glyph %q in ASCII mode: %q", r, line)
			}
		}
	}
}

func TestAssistantMarkdownNoRowExceedsWidth(t *testing.T) {
	doc := strings.Join([]string{
		"## " + strings.Repeat("heading words ", 12),
		"Prose with a " + strings.Repeat("verylongtoken", 20) + " and https://example.com/" + strings.Repeat("path/", 40),
		"| col a | col b | col c |\n| --- | :-: | --: |\n| " + strings.Repeat("wide ", 30) + " | 漢字の表 | 🙂🙂 |",
		"```python\n" + strings.Repeat("x = 1  # long line ", 20) + "\n\tindented\n```",
		"> quote with `code` and " + strings.Repeat("漢字", 40),
		"1. first\n2. second with [link](https://example.com/very/long/url/that/keeps/going)\n   - nested " + strings.Repeat("deep ", 30),
		"![alt text](img.png) <span>html</span>",
	}, "\n\n")
	for _, width := range []int{40, 60, 80, 120} {
		for _, ascii := range []bool{false, true} {
			m := markdownTestUI(t, "tokyonight", ascii, width)
			layoutEntries(m, entry{role: "Assistant", content: doc})
			for i, line := range m.lines {
				if w := runewidth.StringWidth(line); w > contentWidth(width) {
					t.Fatalf("width %d ascii=%t: layout row %d is %d cells: %q", width, ascii, i, w, line)
				}
				for _, run := range m.lineRuns[i] {
					if run.start < 0 || run.end > utf8.RuneCountInString(line) || run.start >= run.end {
						t.Fatalf("width %d: run %+v outside row %q", width, run, line)
					}
				}
			}
			for i, row := range strings.Split(m.View(), "\n") {
				if w := plainWidth(row); w != width {
					t.Fatalf("width %d ascii=%t: view row %d is %d cells: %q", width, ascii, i, w, stripANSI(row))
				}
			}
		}
	}
}

// sgrPattern matches the SGR sequences styling emits; anything else that
// starts an escape came from content.
var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestAssistantMarkdownNeverEmitsContentEscapes(t *testing.T) {
	payload := "a\x1b[31mred\x1b]0;title\x07 \x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\ \x9b2J \u009b \x1bP+q\x1b\\ \x00\x7f\u200b\u202e"
	doc := strings.Join([]string{
		"# " + payload,
		"plain " + payload,
		"**" + payload + "**",
		"`" + payload + "`",
		"```go\n" + payload + "\nfunc x() {}\n```",
		"> " + payload,
		"- " + payload,
		"[" + payload + "](https://e.com/" + payload + ")",
		"| h |\n| - |\n| " + payload + " |",
		"&#27;[31m entity",
	}, "\n\n")
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.TrueColor} {
		previous := lipgloss.ColorProfile()
		lipgloss.SetColorProfile(profile)
		m := markdownTestUI(t, "catppuccin", false, 80)
		m.entries = append(m.entries, entry{role: "Assistant", content: doc})
		m.layoutWidth = 0
		m.rebuild()
		for _, line := range m.lines {
			if strings.Contains(line, "\x9b") || strings.ContainsAny(line, "\x1b\u009b\x07\x00") {
				t.Fatalf("layout row carries a raw control rune: %q", line)
			}
			// Rows arrive escaped once: fitting them for the screen only
			// pads, never escapes again.
			if fitted := fit(line, m.width); !strings.HasPrefix(fitted, line) {
				t.Fatalf("row escaped twice: %q became %q", line, fitted)
			}
		}
		view := m.View()
		lipgloss.SetColorProfile(previous)
		rest := sgrPattern.ReplaceAllString(view, "")
		// A byte check for the raw C1 CSI byte, rune checks for the rest.
		if strings.Contains(rest, "\x9b") || strings.ContainsAny(rest, "\x1b\u009b\x07\x00\x7f\u200b\u202e") {
			t.Fatalf("profile %v: content escape reached the terminal: %q", profile, rest)
		}
		if !strings.Contains(rest, `\u001B[31mred`) {
			t.Fatalf("profile %v: ESC not shown escaped once: %q", profile, rest)
		}
	}
}

func TestAssistantMarkdownNoColorKeepsText(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := markdownTestUI(t, "catppuccin", false, 80)
	m.entries = append(m.entries, entry{role: "Assistant", content: markdownSample})
	m.layoutWidth = 0
	view := strings.Split(m.View(), "\n")
	for _, want := range []string{"⏺ Plan", "  Some bold words and inline code.", "  func main() {}", "  After the code."} {
		found := false
		for _, row := range view {
			if strings.HasPrefix(row, want) {
				found = true
				// Transcript rows drop every color and attribute.
				if strings.Contains(row, "\x1b") {
					t.Fatalf("no-color row carries escapes: %q", row)
				}
			}
		}
		if !found {
			t.Fatalf("no-color view lost %q: %q", want, view)
		}
	}
}

func TestAssistantMarkdownHighlightAcrossThemes(t *testing.T) {
	for _, name := range []string{"catppuccin", "github", "gruvbox", "default"} {
		m := markdownTestUI(t, name, false, 80)
		layoutEntries(m, entry{role: "Assistant", content: "```python\ndef f():\n    return 'x'\n```\n\n```nosuchlang\ndef f():\n```"})
		def := lineWith(t, m, 0, "def f():")
		kw, ok := runOver(m, def, 2)
		if !ok {
			t.Fatalf("%s: no keyword run", name)
		}
		want := colorName(m.theme.Accent.GetForeground())
		if m.theme.Terminal16 {
			want = "Color(5)"
		}
		if got := colorName(kw.style.GetForeground()); got != want {
			t.Fatalf("%s: keyword %s, want %s", name, got, want)
		}
		str := lineWith(t, m, def, "'x'")
		if s, _ := runOver(m, str, runeIndex(t, m.lines[str], "'x'")); colorName(s.style.GetForeground()) == colorName(kw.style.GetForeground()) || s.style.GetBold() {
			t.Fatalf("%s: string token %+v not told apart from keywords (or bold)", name, s)
		}
		plain := lineWith(t, m, str+1, "def f():")
		run, _ := runOver(m, plain, 2)
		if colorName(run.style.GetForeground()) == colorName(kw.style.GetForeground()) {
			t.Fatalf("%s: unknown language was highlighted", name)
		}
		if colorName(run.style.GetBackground()) != colorName(m.theme.BgCode.GetBackground()) {
			t.Fatalf("%s: unknown-language panel not on BgCode", name)
		}
	}
}

func TestHighlightKindsMapOneToOne(t *testing.T) {
	pairs := map[highlight.Kind]markdown.CodeKind{
		highlight.Plain: markdown.Plain, highlight.Keyword: markdown.Keyword, highlight.String: markdown.String,
		highlight.Comment: markdown.Comment, highlight.Number: markdown.Number, highlight.Name: markdown.Name,
		highlight.Function: markdown.Function, highlight.Type: markdown.Type, highlight.Operator: markdown.Operator,
		highlight.Punctuation: markdown.Punctuation, highlight.Builtin: markdown.Builtin, highlight.Literal: markdown.Literal,
	}
	if len(highlightKinds) != len(pairs) {
		t.Fatalf("highlightKinds has %d kinds, want %d", len(highlightKinds), len(pairs))
	}
	for hk, mk := range pairs {
		if highlightKinds[hk] != mk {
			t.Fatalf("highlight kind %d maps to %d, want %d", hk, highlightKinds[hk], mk)
		}
	}
	segs, ok := highlightCode("go", "x := 1")
	if !ok || len(segs) != 1 {
		t.Fatalf("adapter result %v ok=%t", segs, ok)
	}
	var joined strings.Builder
	for _, s := range segs[0] {
		joined.WriteString(s.Text)
	}
	if joined.String() != "x := 1" {
		t.Fatalf("adapter changed the text: %q", joined.String())
	}
	if _, ok := highlightCode("", "x"); ok {
		t.Fatal("empty language highlighted")
	}
}

// Expanded reasoning renders markdown like assistant text (spec §
// Markdown: "Assistant text (and expanded reasoning)"), with prose and
// headings in the reasoning's Muted italics and the syntax hidden; the
// collapsed marker shows no body.
func TestExpandedReasoningRendersMarkdown(t *testing.T) {
	const content = "# Plan\n**raw** `kept`\n- item\n\n```go\nfunc main() {}\n```"
	m := markdownTestUI(t, "catppuccin", false, 80)
	starts := layoutEntries(m, entry{role: "Reasoning", content: content}, entry{role: "You", content: "next"})
	if starts[1]-starts[0] != 2 || m.lines[starts[0]] != m.blocks.Thought+" Thought" {
		t.Fatalf("collapsed reasoning rows %q", m.lines[starts[0]:starts[1]])
	}
	m.thoughtExpanded = map[int]bool{0: true}
	m.layoutWidth = 0
	m.rebuild()
	starts = m.entryLines
	if m.lines[starts[0]] != m.blocks.Thought+" Thought" {
		t.Fatalf("expanded marker row %q", m.lines[starts[0]])
	}
	body := m.lines[starts[0]+1 : starts[1]-1]
	for _, row := range body {
		if row != "" && !strings.HasPrefix(row, "  ") {
			t.Fatalf("body row %q does not hang under the pad", row)
		}
		for _, raw := range []string{"#", "**", "`"} {
			if strings.Contains(row, raw) {
				t.Fatalf("expanded reasoning row %q shows markdown syntax %q", row, raw)
			}
		}
	}
	muted := colorName(m.theme.Muted.GetForeground())
	heading := lineWith(t, m, starts[0]+1, "Plan")
	if m.lines[heading] != "  Plan" {
		t.Fatalf("heading row %q", m.lines[heading])
	}
	if run, ok := runOver(m, heading, 2); !ok || !run.style.GetBold() || !run.style.GetItalic() || colorName(run.style.GetForeground()) != muted {
		t.Fatalf("heading run %+v ok=%t, want bold Muted italics", run, ok)
	}
	prose := lineWith(t, m, starts[0]+1, "raw")
	if m.lines[prose] != "  raw kept" {
		t.Fatalf("prose row %q", m.lines[prose])
	}
	if run, ok := runOver(m, prose, 2); !ok || !run.style.GetBold() || !run.style.GetItalic() || colorName(run.style.GetForeground()) != muted {
		t.Fatalf("bold run %+v ok=%t, want bold inside the Muted italics", run, ok)
	}
	if run, ok := runOver(m, prose, runeIndex(t, m.lines[prose], "kept")); !ok || colorName(run.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) || colorName(run.style.GetBackground()) != colorName(m.theme.BgCode.GetBackground()) {
		t.Fatalf("inline code run %+v ok=%t, want Accent on BgCode", run, ok)
	}
	if item := lineWith(t, m, starts[0]+1, "item"); m.lines[item] != "  • item" {
		t.Fatalf("list row %q", m.lines[item])
	}
	code := lineWith(t, m, starts[0]+1, "func main() {}")
	if kw, ok := runOver(m, code, runeIndex(t, m.lines[code], "func")); !strings.HasPrefix(m.lines[code], "  func") || !ok || colorName(kw.style.GetForeground()) != colorName(m.theme.Accent.GetForeground()) {
		t.Fatalf("code row %q keyword run %+v ok=%t", m.lines[code], kw, ok)
	}
	for i := starts[0]; i < len(m.lines); i++ {
		if w := runewidth.StringWidth(m.lines[i]); w > contentWidth(80) {
			t.Fatalf("row %d is %d cells: %q", i, w, m.lines[i])
		}
	}
	// lines, styles, and runs stay aligned: the next block starts where
	// entryLines says.
	if len(m.lineRuns) != len(m.lines) || len(m.lineStyles) != len(m.lines) || m.lines[starts[1]+1] != "> next" {
		t.Fatalf("layout out of step: row %q", m.lines[starts[1]+1])
	}
}
