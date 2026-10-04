package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"likha/internal/explore"
	"likha/internal/session"
	"likha/internal/tools"
	"likha/internal/transcript"
)

// Tool items in the transcript (spec transcript-redesign § Tool items): one
// item per call — a header under a status dot, a ⎿ summary in words, and
// bounded preview rows. Raw arguments, provenance, and full output stay in
// the inspector.

// toolBlinkFrames is the number of activity ticks per blink phase of a
// running status dot (4 × 120ms).
const toolBlinkFrames = 4

// legacyRequestPrefix opens the tool_start event text. New sessions keep it
// on a call's entry only until the result replaces it; older sessions stored
// it as a separate entry.
const legacyRequestPrefix = "Request: "

// lineRun paints the rune range [start, end) of one layout row in its own
// style at render time; the row's style still paints the rest and the
// padding. blink marks a running status dot, blanked on the off phase of the
// activity tick. Offsets address the unfitted row: fit only pads tool rows,
// whose text is escaped before layout. style is shared: markdown rows hold
// thousands of runs over a handful of styles, and a lipgloss.Style is over
// half a kilobyte.
type lineRun struct {
	start, end int
	style      *lipgloss.Style
	blink      bool
}

// shared returns s as a run style.
func shared(s lipgloss.Style) *lipgloss.Style { return &s }

// toolRow is one laid-out row of a tool item.
type toolRow struct {
	text  string
	style lipgloss.Style
	runs  []lineRun
}

// toolItemPlan maps transcript entries to tool items. New sessions store one
// Tool entry per call, whose record the result updates in place, so each
// entry is its own item. Older sessions stored a "Request: name args" entry
// and a separate result entry; the plan folds each result into its request:
//
//   - pairing runs in transcript order within one model round: a You,
//     Assistant, or Reasoning entry closes every open request, because a
//     round's results always land before the next round's text;
//   - a result pairs with the earliest open request carrying the same call ID
//     (legacy task rows recorded one at the request), else the same tool name
//     and argument prefix, else the same tool name — results arrive in call
//     order, so first-in-first-out per name also matches parallel groups;
//   - a result without a record pairs by its "name: " prefix;
//   - a request left open renders header-only with an unknown status.
type toolItemPlan struct {
	record   []int  // record index the item at this entry renders, or -1
	partner  []int  // legacy result folded into this request item, or -1
	owner    []int  // request item a folded legacy result belongs to, or -1
	absorbed []bool // a legacy result shown inside an earlier request item
	live     []bool // per record: the item a live call of this run updates
}

func (m *ui) toolItemPlan() toolItemPlan {
	n := len(m.entries)
	plan := toolItemPlan{record: make([]int, n), partner: make([]int, n), owner: make([]int, n), absorbed: make([]bool, n),
		live: make([]bool, len(m.toolRecords))}
	for i := range n {
		plan.record[i], plan.partner[i], plan.owner[i] = -1, -1, -1
	}
	if m.working {
		for callID := range m.liveToolCalls {
			if r := m.liveToolRecord(callID); r >= 0 {
				plan.live[r] = true
			}
		}
	}
	for r, record := range m.toolRecords {
		// The last record at an entry wins, as toolRecordAt always read it.
		if record.EntryIndex >= 0 && record.EntryIndex < n && m.entries[record.EntryIndex].role == "Tool" {
			plan.record[record.EntryIndex] = r
		}
	}
	type request struct {
		entry              int
		name, args, callID string
	}
	var open []request
	for i, e := range m.entries {
		switch e.role {
		case "You", "Assistant", "Reasoning", transcript.TurnRole:
			open = open[:0]
			continue
		case "Tool":
		default:
			continue
		}
		if name, args, ok := legacyToolRequest(e.content); ok {
			pending := request{entry: i, name: name, args: args}
			if r := plan.record[i]; r >= 0 {
				pending.callID = m.toolRecords[r].CallID
			}
			open = append(open, pending)
			continue
		}
		match := -1
		if r := plan.record[i]; r >= 0 {
			record := m.toolRecords[r]
			match = slices.IndexFunc(open, func(p request) bool { return p.callID != "" && p.callID == record.CallID })
			if match < 0 {
				match = slices.IndexFunc(open, func(p request) bool {
					return p.callID == "" && p.name == record.Name && strings.HasPrefix(record.Arguments, p.args)
				})
			}
			if match < 0 {
				match = slices.IndexFunc(open, func(p request) bool { return p.callID == "" && p.name == record.Name })
			}
		} else {
			match = slices.IndexFunc(open, func(p request) bool { return p.callID == "" && strings.HasPrefix(e.content, p.name+": ") })
		}
		if match < 0 {
			continue
		}
		head := open[match].entry
		open = slices.Delete(open, match, match+1)
		plan.partner[head], plan.owner[i], plan.absorbed[i] = i, head, true
		if r := plan.record[i]; r >= 0 {
			plan.record[head] = r
		}
	}
	return plan
}

// legacyToolRequest parses tool_start event text: "Request: name args".
func legacyToolRequest(content string) (name, args string, ok bool) {
	rest, ok := strings.CutPrefix(content, legacyRequestPrefix)
	if !ok {
		return "", "", false
	}
	name, args, _ = strings.Cut(rest, " ")
	return name, args, name != ""
}

// toolItemRows lays out one tool item. The header row carries the status dot
// (or the focus glyph), the bold display name, and arguments clipped to one
// row; the ⎿ summary wraps under its text column; preview rows and the
// expand hint are clipped with the ellipsis. Every row fits width, and
// untrusted text is escaped before it reaches a row.
func (m *ui) toolItemRows(index int, plan toolItemPlan, focused bool, width int) []toolRow {
	g := m.blocks
	row := withBase(m.theme.Normal, m.theme.Base)
	if focused {
		// Selected carries the selection tint where the theme has one.
		row = withBase(m.theme.Selected, m.theme.Base)
	}
	on := func(fg lipgloss.Style) lipgloss.Style { return withBase(fg, row) }
	muted := on(m.theme.Muted)
	glyph := g.ToolHeader
	if focused {
		glyph = g.Focus
	}
	gutter := glyph + " "
	gutterCells := runewidth.StringWidth(gutter)
	e := m.entries[index]
	var record session.ToolRecord
	legacyResult := false
	if r := plan.record[index]; r >= 0 {
		record = m.toolRecords[r]
	} else if name, args, ok := legacyToolRequest(e.content); ok {
		record = session.ToolRecord{Name: name, Arguments: args}
		if partner := plan.partner[index]; partner >= 0 {
			record.Content = strings.TrimPrefix(m.entries[partner].content, name+": ")
			legacyResult = true
		}
	} else {
		// Legacy text with neither a record nor a request shape: its first
		// line on one muted row; the inspector keeps the whole text.
		style := muted
		if focused {
			style = row
		}
		return []toolRow{{text: gutter + toolClipCells(toolLegacyLine(e.content, g.Ellipsis), max(0, width-gutterCells), g.Ellipsis), style: style}}
	}

	kind := toolStatusKind(record.Status)
	live := kind == toolStatusRunning && plan.record[index] >= 0 && plan.live[plan.record[index]]
	if kind == toolStatusRunning && !live {
		// Only this run's started calls are running: any other call still
		// marked running lost its result to a cancelled, abandoned, or
		// crashed run, whether or not a new turn is working now.
		record.Status, kind = "interrupted", toolStatusFailure
	}
	sub := "  " + g.Result + "  "
	indent := runewidth.StringWidth(sub)
	room := max(0, width-indent)
	expanded := m.toolOutputExpanded(record)
	limit, diffLines := toolPreviewLines, transcript.DefaultDiffLines
	if expanded {
		limit, diffLines = toolExpandedLines, toolExpandedLines
	}
	summary, preview, more := toolSummary(record, g.Ellipsis, limit)
	if legacyResult {
		// Legacy results recorded no status; say so instead of guessing one.
		summary = "Status not recorded"
		preview, more = nil, 0
		if record.Name != "grep" && record.Name != "glob" {
			preview, more = toolPreview(record.Content, limit)
		}
	}
	var diff transcript.DiffSummary
	if task, children, ok := m.toolTaskRecord(record); ok {
		// Subagent items (§ Subagent items): the explore record, not the
		// call's coarse status, words the ⎿ line — its latest activity while
		// live, its outcome once settled.
		switch {
		case kind == toolStatusRunning && transcript.IsLive(task.Status):
			summary, preview, more = transcript.FitLiveLine(task, children, room, g.Ellipsis), nil, 0
		case !transcript.IsLive(task.Status):
			text, success := transcript.FitSettle(task, room, g.Ellipsis)
			summary, kind = text, toolStatusFailure
			if success {
				kind = toolStatusSuccess
			} else {
				preview, more = nil, 0
			}
		}
	} else if edit, ok := m.toolEditDiff(record, diffLines); ok {
		diff = edit
		summary = toolClipCells(edit.Summary+toolSummarySuffix(record), toolSummaryCells, g.Ellipsis)
		preview, more = nil, edit.More
	}
	dot := muted
	switch {
	case focused:
		dot = row
	case kind == toolStatusSuccess:
		dot = on(m.theme.Success)
	case kind == toolStatusFailure:
		dot = on(m.theme.Error)
	}
	display, args := toolRecordHeaderParts(record, g.Ellipsis)
	if record.Name == "edit_file" && display == "Update" && diff.Created {
		// edit_file's arguments cannot tell a create from an update; the
		// applied diff can (spec § Tool items: a create reads Create(path)).
		display = "Create"
	}
	header := fitToolHeader(display, args, max(0, width-gutterCells), g.Ellipsis)
	glyphEnd := utf8.RuneCountInString(glyph)
	textStart := utf8.RuneCountInString(gutter)
	nameEnd := textStart + utf8.RuneCountInString(header)
	if strings.HasPrefix(header, display) {
		nameEnd = textStart + utf8.RuneCountInString(display)
	}
	text := gutter + header
	rows := []toolRow{{text: text, style: on(m.theme.Normal), runs: []lineRun{
		{start: 0, end: glyphEnd, style: shared(dot), blink: live && kind == toolStatusRunning && !focused},
		{start: textStart, end: nameEnd, style: shared(on(m.theme.Normal).Bold(true))},
		{start: nameEnd, end: utf8.RuneCountInString(text), style: shared(on(m.theme.Normal))},
	}}}

	if summary == "" {
		return rows
	}
	for i, line := range wrapHanging(summary, width, indent, indent) {
		if i == 0 {
			line = sub + line
		}
		rows = append(rows, toolRow{text: line, style: muted})
	}
	pad := strings.Repeat(" ", indent)
	for _, line := range preview {
		rows = append(rows, toolRow{text: pad + toolPreviewRow(line, room, g.Ellipsis), style: muted})
	}
	rows = append(rows, m.diffRows(diff, pad, room, row, muted)...)
	hint := ""
	switch {
	case more > 0 && expanded:
		hint = g.Ellipsis + " +" + toolCount(more, "line", "lines") + " (ctrl+o to inspect)"
	case more > 0:
		hint = g.Ellipsis + " +" + toolCount(more, "line", "lines") + " (enter to expand · ctrl+o to inspect)"
	case expanded && (len(preview) > 0 || len(diff.Lines) > 0):
		hint = "(enter to collapse)"
	}
	if hint != "" {
		rows = append(rows, toolRow{text: pad + toolClipCells(hint, room, g.Ellipsis), style: muted})
	}
	if len(diff.OtherFiles) > 0 {
		// A multi-file edit shows the first file's lines and names the rest.
		others := "Also " + strings.Join(diff.OtherFiles, ", ")
		rows = append(rows, toolRow{text: pad + toolClipCells(others, room, g.Ellipsis), style: muted})
	}
	return rows
}

// diffRows lays out an edit item's diff lines under the ⎿ text column (spec
// transcript-redesign § Diffs): line number, sign, and text, each row's text
// column tinted BgDiffAdd or BgDiffRemove. The sign column carries the
// meaning, so a profile without color loses nothing; the tint spans the
// whole text column, as a code panel does.
func (m *ui) diffRows(diff transcript.DiffSummary, pad string, room int, row, muted lipgloss.Style) []toolRow {
	if len(diff.Lines) == 0 || room <= 0 {
		return nil
	}
	add := shared(withBase(withBase(m.theme.Normal, m.theme.BgDiffAdd), row))
	remove := shared(withBase(withBase(m.theme.Normal, m.theme.BgDiffRemove), row))
	numbers := diff.NumberWidth()
	off := utf8.RuneCountInString(pad)
	rows := make([]toolRow, 0, len(diff.Lines))
	for _, line := range diff.Lines {
		text := transcript.FormatDiffLine(line, numbers, room, m.blocks.Ellipsis)
		text += strings.Repeat(" ", max(0, room-runewidth.StringWidth(text)))
		tint := add
		if line.Sign == '-' {
			tint = remove
		}
		rows = append(rows, toolRow{text: pad + text, style: muted, runs: []lineRun{
			{start: off, end: off + utf8.RuneCountInString(text), style: tint},
		}})
	}
	return rows
}

// editDiffKey identifies one edit item's diff: the summary depends only on
// the record's name, arguments, and stored diff.
type editDiffKey struct {
	name, arguments, diff string
	lines                 int
}

// toolEditDiff is an applied edit's diff summary (spec transcript-redesign
// § Diffs, § Persistence): the reviewed diff the record stores, else one
// rebuilt from the edit arguments (older records; no line numbers). Refused,
// failed, cancelled, and running edits, and edits with neither source, keep
// the generic summary. Summaries are memoized across layouts: every event
// lays the transcript out again, and a diff can be megabytes.
func (m *ui) toolEditDiff(record session.ToolRecord, maxLines int) (transcript.DiffSummary, bool) {
	if record.SourceKind == "mcp" || record.Name != "edit" && record.Name != "edit_file" {
		return transcript.DiffSummary{}, false
	}
	switch strings.ToLower(strings.TrimSpace(record.Status)) {
	case string(tools.Succeeded), string(tools.Limited):
	default:
		return transcript.DiffSummary{}, false
	}
	key := editDiffKey{name: record.Name, arguments: record.Arguments, diff: record.Diff, lines: maxLines}
	summary, ok := m.editDiffCache[key]
	if !ok {
		var files []transcript.FileDiff
		if record.Diff != "" {
			files = transcript.ParseUnifiedDiff(record.Diff)
		}
		if len(files) == 0 {
			files, _ = transcript.EditFromArguments(record.Name, record.Arguments)
		}
		summary = transcript.SummarizeEdit(files, maxLines)
	}
	if m.editDiffNext != nil {
		m.editDiffNext[key] = summary
	}
	return summary, summary.Summary != ""
}

// toolTaskRecord finds the explore record a top-level task item shows, with
// its direct children. The item's TaskID names it: acceptTaskRecord sets it
// when the record first arrives and the result restates it.
func (m *ui) toolTaskRecord(record session.ToolRecord) (explore.Record, []explore.Record, bool) {
	if record.Name != "task" || record.SourceKind == "mcp" || record.TaskID == "" {
		return explore.Record{}, nil, false
	}
	at := slices.IndexFunc(m.taskRecords, func(r explore.Record) bool { return r.ID == record.TaskID && r.ParentID == "" })
	if at < 0 {
		return explore.Record{}, nil, false
	}
	task := m.taskRecords[at]
	var children []explore.Record
	for _, r := range m.taskRecords {
		if r.ParentID == task.ID {
			children = append(children, r)
		}
	}
	return task, children, true
}

// toolLegacyLine flattens the first line of legacy tool text onto one row,
// bounded before escaping so a huge stored result never reaches the layout.
func toolLegacyLine(content, ellipsis string) string {
	line, _, _ := strings.Cut(content, "\n")
	if len(line) > 2048 {
		line = strings.ToValidUTF8(line[:2048], "")
	}
	return toolHeaderText(line, ellipsis)
}

// toolPreviewRow escapes hidden runes the way wrap does, then clips the row
// to width with the ellipsis. Escaping stops once the row overflows, so a
// multi-megabyte output line costs no more than a full row.
func toolPreviewRow(line string, width int, ellipsis string) string {
	var b strings.Builder
	used := 0
	for _, r := range line {
		piece := string(r)
		if hiddenReviewRune(r) {
			piece = fmt.Sprintf("\\u%04X", r)
		}
		b.WriteString(piece)
		used += runewidth.StringWidth(piece)
		if used > width {
			break
		}
	}
	return toolClipCells(b.String(), width, ellipsis)
}

// renderRuns styles a fitted row with its row style, then repaints each run
// in its own style. blinkOff blanks blinking runs to equal-width spaces, so
// the blink never changes a cell width.
func renderRuns(row lipgloss.Style, runs []lineRun, fitted string, blinkOff bool) string {
	runes := []rune(fitted)
	var out strings.Builder
	pos := 0
	emit := func(end int, style lipgloss.Style) {
		if end > pos {
			out.WriteString(style.Render(string(runes[pos:end])))
			pos = end
		}
	}
	for _, run := range runs {
		if run.start < pos || run.start >= run.end || run.end > len(runes) {
			continue
		}
		emit(run.start, row)
		text := string(runes[run.start:run.end])
		if run.blink && blinkOff {
			text = strings.Repeat(" ", runewidth.StringWidth(text))
		}
		out.WriteString(run.style.Render(text))
		pos = run.end
	}
	emit(len(runes), row)
	return out.String()
}

// toolBlinkOff reports the off phase of running status dots. They blink only
// on the live activity tick; between runs, under review, and on a no-color
// profile (where the glyph is the only signal) they stay solid.
func (m *ui) toolBlinkOff() bool {
	return m.working && m.pending == nil && lipgloss.ColorProfile() != termenv.Ascii && (m.activityFrame/toolBlinkFrames)%2 == 1
}

// toolBlinking reports whether a live call still shows a running dot, which
// keeps the activity tick alive after the Working row hands over to tools.
func (m *ui) toolBlinking() bool {
	if !m.working || len(m.liveToolCalls) == 0 {
		return false
	}
	for callID := range m.liveToolCalls {
		if r := m.liveToolRecord(callID); r >= 0 && toolStatusKind(m.toolRecords[r].Status) == toolStatusRunning {
			return true
		}
	}
	return false
}

// toolOutputExpanded reports whether the user expanded this call's output
// inline. Expansion keys on the call ID, so it follows the item through
// rebuilds; it lives in this view only and is never persisted.
func (m *ui) toolOutputExpanded(record session.ToolRecord) bool {
	return record.CallID != "" && m.toolExpanded[record.CallID]
}

// toolHiddenLines counts the output lines a collapsed item leaves out: the
// lines Enter would reveal inline.
func (m *ui) toolHiddenLines(record session.ToolRecord) int {
	if toolStatusKind(record.Status) == toolStatusRunning {
		return 0
	}
	if edit, ok := m.toolEditDiff(record, transcript.DefaultDiffLines); ok {
		return edit.More
	}
	_, _, more := toolSummary(record, m.blocks.Ellipsis, toolPreviewLines)
	return more
}

// toggleToolOutput expands or collapses the tool item at index inline. An
// item with nothing hidden reports false so Enter can open the inspector.
func (m *ui) toggleToolOutput(index int) bool {
	record, ok := m.toolRecordAt(index)
	if !ok || record.CallID == "" {
		return false
	}
	if m.toolExpanded[record.CallID] {
		delete(m.toolExpanded, record.CallID)
		m.layoutWidth = 0
		return true
	}
	if m.toolHiddenLines(record) == 0 {
		return false
	}
	if m.toolExpanded == nil {
		m.toolExpanded = make(map[string]bool)
	}
	m.toolExpanded[record.CallID] = true
	m.layoutWidth = 0
	return true
}
