package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"likha/internal/session"
	likhaui "likha/internal/ui"
)

// Tool item rows are memoized across layouts. Every streaming delta lays the
// whole transcript out again, and laying out one settled call splits and
// clips its full stored output: a long session with large outputs spent tens
// of milliseconds per token re-deriving rows that had not changed. The cache
// holds the rows of each item the last layout drew, keyed on every input
// toolItemRows reads, so an item re-lays out only when one of them changes.

// toolRowsEnv is the layout-wide input to every tool item: the width, the
// theme the row styles come from, and the glyph set. A change drops the
// whole cache, so a resize, theme preview, or glyph switch never paints a
// stale row.
type toolRowsEnv struct {
	width  int
	theme  string
	glyphs likhaui.BlockGlyphs
}

// toolRowsInput is everything one tool item's rows depend on beyond the
// layout environment. record is a snapshot: Warnings is cloned, so a later
// in-place change to the live record still misses.
type toolRowsInput struct {
	content   string // the entry's text (legacy request or legacy line)
	partner   string // a folded legacy result's text
	hasRecord bool
	record    session.ToolRecord
	live      bool
	focused   bool
	expanded  bool
}

// equal compares inputs field by field; ToolRecord holds a slice, so the
// struct is not comparable with ==. Unchanged strings share their bytes, so
// the comparison of a megabyte output costs a pointer check.
func (a toolRowsInput) equal(b toolRowsInput) bool {
	if a.content != b.content || a.partner != b.partner || a.hasRecord != b.hasRecord {
		return false
	}
	if a.live != b.live || a.focused != b.focused || a.expanded != b.expanded {
		return false
	}
	x, y := a.record, b.record
	if x.EntryIndex != y.EntryIndex || x.CallID != y.CallID || x.TaskID != y.TaskID || x.Name != y.Name || x.Arguments != y.Arguments {
		return false
	}
	if x.SourceKind != y.SourceKind || x.Server != y.Server || x.SourceTool != y.SourceTool || x.Status != y.Status {
		return false
	}
	if x.Content != y.Content || x.ArtifactID != y.ArtifactID || x.Cursor != y.Cursor || x.Truncated != y.Truncated || x.NextOffset != y.NextOffset || x.Diff != y.Diff {
		return false
	}
	return slices.Equal(x.Warnings, y.Warnings)
}

// toolRowsEntry is one memoized tool item layout.
type toolRowsEntry struct {
	in   toolRowsInput
	rows []toolRow
}

// toolRowsLayout is the per-rebuild state of the tool row cache: next
// collects the items this layout drew and becomes the cache on commit, so
// nothing outlives the entries that need it.
type toolRowsLayout struct {
	env  toolRowsEnv
	next map[int]toolRowsEntry
}

func (m *ui) newToolRowsLayout(width int) *toolRowsLayout {
	env := toolRowsEnv{width: width, theme: themeLayoutKey(m.theme), glyphs: m.blocks}
	if env != m.toolRowsEnv {
		m.toolRowsCache = nil
	}
	return &toolRowsLayout{env: env, next: map[int]toolRowsEntry{}}
}

func (m *ui) commitToolRowsLayout(layout *toolRowsLayout) {
	m.toolRowsCache = layout.next
	m.toolRowsEnv = layout.env
}

// cachedToolItemRows is toolItemRows through the cache. Subagent items are
// never cached: their summary reads the live explore records, not the call.
func (m *ui) cachedToolItemRows(layout *toolRowsLayout, index int, plan toolItemPlan, focused bool, width int) []toolRow {
	in := m.toolRowsInput(index, plan, focused)
	if in.hasRecord && in.record.Name == "task" {
		return m.toolItemRows(index, plan, focused, width)
	}
	if cached, ok := m.toolRowsCache[index]; ok && cached.in.equal(in) {
		m.keepEditDiffs(in.record)
		layout.next[index] = cached
		return cached.rows
	}
	rows := m.toolItemRows(index, plan, focused, width)
	in.record.Warnings = slices.Clone(in.record.Warnings)
	layout.next[index] = toolRowsEntry{in: in, rows: rows}
	return rows
}

// toolRowsInput gathers what toolItemRows reads for the item at index.
func (m *ui) toolRowsInput(index int, plan toolItemPlan, focused bool) toolRowsInput {
	in := toolRowsInput{content: m.entries[index].content, focused: focused}
	if partner := plan.partner[index]; partner >= 0 {
		in.partner = m.entries[partner].content
	}
	if r := plan.record[index]; r >= 0 {
		in.hasRecord = true
		in.record = m.toolRecords[r]
		in.live = plan.live[r]
		in.expanded = m.toolOutputExpanded(in.record)
	}
	return in
}

// keepEditDiffs carries an edit item's memoized diff summaries into the
// layout in progress, as toolEditDiff would have on a cache miss, so the
// summary survives for the inspector and the next miss.
func (m *ui) keepEditDiffs(record session.ToolRecord) {
	if m.editDiffNext == nil || record.Name != "edit" && record.Name != "edit_file" {
		return
	}
	key := editDiffKey{name: record.Name, arguments: record.Arguments, diff: record.Diff}
	for _, lines := range []int{0, toolExpandedLines} {
		key.lines = lines
		if summary, ok := m.editDiffCache[key]; ok {
			m.editDiffNext[key] = summary
		}
	}
}

// themeLayoutKey fingerprints every theme role a laid-out row can carry, so
// a theme preview or switch yields a new key.
func themeLayoutKey(t likhaui.Theme) string {
	var b strings.Builder
	for _, st := range []lipgloss.Style{t.Base, t.BgUser, t.BgTool, t.BgModel, t.Title, t.Selected, t.Normal, t.Help, t.Border, t.Warning, t.Error, t.Muted, t.Accent, t.Success, t.BgCode, t.BgDiffAdd, t.BgDiffRemove} {
		fmt.Fprintf(&b, "%v/%v/%t%t%t%t%t%t%t;", st.GetForeground(), st.GetBackground(), st.GetBold(), st.GetItalic(), st.GetUnderline(), st.GetStrikethrough(), st.GetFaint(), st.GetReverse(), st.GetBlink())
	}
	fmt.Fprintf(&b, "%t", t.Terminal16)
	return b.String()
}
