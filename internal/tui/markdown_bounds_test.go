package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"likha/internal/markdown"
)

// A layout spends at most markdownHighlightBudget highlighting new code
// blocks (past the first, which always runs), newest answers first; the
// rest render as plain panels of the same rows and are highlighted on later
// caret ticks. Before the budget, resuming
// a session highlighted every block in one layout (seconds for a long one).
func TestAssistantMarkdownHighlightBudgetNewestFirst(t *testing.T) {
	defer func(b time.Duration) { markdownHighlightBudget = b }(markdownHighlightBudget)
	markdownHighlightBudget = time.Millisecond
	calls := map[string]int{}
	orig := runHighlight
	runHighlight = func(lang, code string) ([][]markdown.CodeSegment, bool) {
		calls[code]++
		time.Sleep(5 * time.Millisecond) // one block spends the whole budget
		return orig(lang, code)
	}
	t.Cleanup(func() { runHighlight = orig })

	const n = 5
	m := markdownTestUI(t, "catppuccin", false, 80)
	var entries []entry
	for i := range n {
		entries = append(entries, entry{role: "Assistant", content: fmt.Sprintf("Answer %d\n\n```go\nfunc f%d() {}\n```", i, i)})
	}
	layoutEntries(m, entries...)
	accent := colorName(m.theme.Accent.GetForeground())
	keyword := func(i int) string {
		line := lineWith(t, m, 0, fmt.Sprintf("func f%d()", i))
		run, ok := runOver(m, line, 2)
		if !ok {
			t.Fatalf("block %d has no run over its keyword", i)
		}
		if colorName(run.style.GetBackground()) != colorName(m.theme.BgCode.GetBackground()) {
			t.Fatalf("block %d is not a code panel", i)
		}
		return colorName(run.style.GetForeground())
	}
	rows := len(m.lines)
	if len(calls) != 1 || keyword(n-1) != accent {
		t.Fatalf("first layout highlighted %d blocks (want only the newest): %v", len(calls), calls)
	}
	if keyword(0) == accent || !m.highlightPending {
		t.Fatalf("oldest block highlighted in the first layout, or nothing pending (%v)", m.highlightPending)
	}
	for tick := 0; m.highlightPending; tick++ {
		if tick > 3*n {
			t.Fatalf("highlighting never completed: %v", calls)
		}
		m.Update(caretTickMsg{})
		m.View()
	}
	for i := range n {
		if got := keyword(i); got != accent {
			t.Fatalf("block %d keyword %s after the ticks, want accent %s", i, got, accent)
		}
		if c := calls[fmt.Sprintf("func f%d() {}", i)]; c != 1 {
			t.Fatalf("block %d highlighted %d times, want 1", i, c)
		}
	}
	if len(m.lines) != rows {
		t.Fatalf("highlighting moved rows: %d lines, first layout had %d", len(m.lines), rows)
	}
}

// Markdown runs share one style per distinct markdown style instead of
// carrying a half-kilobyte lipgloss.Style each: a highlighted 60-line block
// holds hundreds of runs over a handful of styles.
func TestAssistantMarkdownRunsShareStyles(t *testing.T) {
	m := markdownTestUI(t, "catppuccin", false, 80)
	var code strings.Builder
	for i := range 60 {
		fmt.Fprintf(&code, "\tx%d := fmt.Sprintf(\"%%d\", y) // note %d\n", i, i)
	}
	layoutEntries(m, entry{role: "Assistant", content: "Intro **bold**\n\n```go\n" + code.String() + "```"})
	runs := 0
	distinct := map[*lipgloss.Style]bool{}
	for _, line := range m.lineRuns {
		for _, run := range line {
			runs++
			distinct[run.style] = true
		}
	}
	if runs < 200 || len(distinct) > 12 {
		t.Fatalf("%d runs over %d style copies; want them shared", runs, len(distinct))
	}
}
