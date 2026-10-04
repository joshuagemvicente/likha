package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tools"
	likhaui "likha/internal/ui"
)

// Tool items (transcript-redesign § Tool items, § Persistence): one item per
// call from tool_start through its result, a status dot, a ⎿ summary in
// words, bounded previews, and one focus stop per call.

func toolItemTestUI(t *testing.T, width int) *ui {
	t.Helper()
	m := blocksTestUI(t, false, width)
	m.theme = likhaui.Resolve("catppuccin", true)
	m.working, m.runID = true, 1
	m.reasoningStream = -1
	m.events = make(chan agent.TurnEvent, 64)
	return m
}

func startCall(m *ui, id, name, arguments string) model.ToolCall {
	call := model.ToolCall{ID: id, Name: name, Arguments: arguments}
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "tool_start", Text: "Request: " + name + " " + arguments, ToolCall: &call})
	return call
}

func finishCall(m *ui, call model.ToolCall, result tools.Result) {
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "tool_result", Text: call.Name + ": " + result.Content, ToolCall: &call, ToolResult: &result})
}

// endRun closes the run the way a done event does, without a provider.
func endRun(m *ui) {
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "done", History: m.history})
}

// itemRows returns the laid-out rows of the tool item at entry index.
func itemRows(m *ui, index int) []string {
	m.layoutWidth = 0
	m.rebuild()
	start := m.entryLines[index]
	end := len(m.lines)
	for i := index + 1; i < len(m.entries); i++ {
		if m.entryLines[i] > start {
			end = m.entryLines[i] - 1 // minus the blank separator
			break
		}
	}
	return m.lines[start:end]
}

func toolEntryIndices(m *ui) []int {
	var indices []int
	for i, e := range m.entries {
		if e.role == "Tool" {
			indices = append(indices, i)
		}
	}
	return indices
}

func TestToolCallIsOneItemFromStartToResult(t *testing.T) {
	cases := []struct {
		name    string
		result  tools.Result
		summary string
		dot     func(likhaui.Theme) lipgloss.Style
	}{
		{"succeeded", tools.Result{Status: tools.Succeeded, Content: "one\ntwo\n"}, "  ⎿  Read 2 lines", func(th likhaui.Theme) lipgloss.Style { return th.Success }},
		{"failed", tools.Result{Status: tools.Failed, Content: "tool_failed: Tool execution failed\nError: open a.go: no such file"}, "  ⎿  Failed: open a.go: no such file", func(th likhaui.Theme) lipgloss.Style { return th.Error }},
		{"refused", tools.Result{Status: tools.Refused, Content: "tool_unavailable: Plan mode blocks reads.\nError: Plan mode blocks reads."}, "  ⎿  Refused: Plan mode blocks reads.", func(th likhaui.Theme) lipgloss.Style { return th.Error }},
		{"cancelled", tools.Result{Status: tools.Cancelled, Content: "Error: context canceled"}, "  ⎿  Cancelled", func(th likhaui.Theme) lipgloss.Style { return th.Error }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := toolItemTestUI(t, 80)
			before := len(m.entries)
			call := startCall(m, "call_1", "read", `{"path":"a.go"}`)
			index := len(m.entries) - 1
			if got := len(m.entries) - before; got != 1 {
				t.Fatalf("tool_start added %d entries, want 1", got)
			}
			record, ok := m.toolRecordAt(index)
			if !ok || record.Status != "running" || record.CallID != "call_1" {
				t.Fatalf("tool_start record = %+v, %t; want running call_1", record, ok)
			}
			if rows := itemRows(m, index); len(rows) != 2 || rows[0] != "⏺ Read(a.go)" || rows[1] != "  ⎿  Running" {
				t.Fatalf("running item rows %q", rows)
			}
			finishCall(m, call, tc.result)
			if tools := toolEntryIndices(m); len(tools) != 1 || tools[0] != index {
				t.Fatalf("result produced tool entries %v, want only %d", tools, index)
			}
			if len(m.toolRecords) != 1 {
				t.Fatalf("result appended a record: %+v", m.toolRecords)
			}
			record, _ = m.toolRecordAt(index)
			if record.Status != string(tc.result.Status) || record.Content != tc.result.Content || record.EntryIndex != index {
				t.Fatalf("record not updated in place: %+v", record)
			}
			if m.entries[index].content != "read: "+tc.result.Content {
				t.Fatalf("entry keeps start text: %q", m.entries[index].content)
			}
			rows := itemRows(m, index)
			if rows[0] != "⏺ Read(a.go)" || rows[1] != tc.summary {
				t.Fatalf("settled rows %q, want header and %q", rows, tc.summary)
			}
			// The dot carries the status color; the name is bold.
			runs := m.lineRuns[m.entryLines[index]]
			if len(runs) != 3 || runs[0].blink {
				t.Fatalf("header runs %+v", runs)
			}
			if got, want := colorName(runs[0].style.GetForeground()), colorName(tc.dot(m.theme).GetForeground()); got != want {
				t.Fatalf("dot fg %v, want %v", got, want)
			}
			if !runs[1].style.GetBold() || string([]rune(rows[0])[runs[1].start:runs[1].end]) != "Read" {
				t.Fatalf("name run %+v not the bold display name", runs[1])
			}
			if strings.Contains(strings.Join(m.lines, "\n"), `{"path"`) || strings.Contains(strings.Join(m.lines, "\n"), "Likha built-in") {
				t.Fatalf("raw arguments or provenance reached the transcript: %q", m.lines)
			}
		})
	}
}

func TestNewRunPersistsOneEntryPerCall(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID, m.reasoningStream = true, 1, -1
	m.events = make(chan agent.TurnEvent, 64)
	m.entries = append(m.entries, entry{role: "You", content: "look"})
	grep := startCall(m, "g", "grep", `{"pattern":"x"}`)
	read := startCall(m, "r", "read", `{"path":"a.go"}`)
	finishCall(m, grep, tools.Result{Status: tools.Succeeded, Content: "grep: x; returned=1; y\na.go:1: x\n"})
	finishCall(m, read, tools.Result{Status: tools.Succeeded, Content: "package a\n"})
	endRun(m)
	saved, err := store.Load(m.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	tool := 0
	for _, e := range saved.Entries {
		if e.Role == "Tool" {
			tool++
		}
	}
	if tool != 2 || len(saved.ToolRecords) != 2 {
		t.Fatalf("saved %d tool entries and %d records, want 2 each: %+v", tool, len(saved.ToolRecords), saved)
	}
	for _, record := range saved.ToolRecords {
		if e := saved.Entries[record.EntryIndex]; e.Role != "Tool" || record.Status != "succeeded" || !strings.HasPrefix(e.Content, record.Name+": ") {
			t.Fatalf("record %+v points at %+v", record, e)
		}
	}
	// Resuming renders the same single items.
	resumed := NewUI(root, nil, nil, "local", providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}, "", store, saved)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := len(resumed.inspectableToolEntries()); got != 2 {
		t.Fatalf("resumed session has %d tool items, want 2", got)
	}
	view := stripANSI(resumed.View())
	for _, want := range []string{"⏺ Grep(x)", "⎿  Found 1 match in 1 file", "⏺ Read(a.go)", "⎿  Read 1 line"} {
		if !strings.Contains(view, want) {
			t.Fatalf("resumed view lacks %q: %s", want, view)
		}
	}
}

// A call saved while still running (a parallel sibling persisted at the
// other's result, then the process died) is interrupted after resume, and
// stays interrupted while a later turn works: only this run's live calls run.
func TestStaleRunningCallStaysInterruptedInLaterRuns(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	conn := providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}
	m := NewUI(root, nil, nil, "local", conn, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.working, m.runID, m.reasoningStream = true, 1, -1
	m.events = make(chan agent.TurnEvent, 64)
	m.entries = append(m.entries, entry{role: "You", content: "look"})
	a := startCall(m, "a", "read", `{"path":"a.go"}`)
	startCall(m, "b", "read", `{"path":"b.go"}`)
	finishCall(m, a, tools.Result{Status: tools.Succeeded, Content: "package a\n"})
	saved, err := store.Load(m.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.ToolRecords[len(saved.ToolRecords)-1]; got.CallID != "b" || got.Status != "running" {
		t.Fatalf("mid-run save kept %+v, want b running", got)
	}
	resumed := NewUI(root, nil, nil, "local", conn, "", store, saved)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	indices := toolEntryIndices(resumed)
	stale := indices[len(indices)-1]
	check := func(when string) {
		t.Helper()
		rows := itemRows(resumed, stale)
		if len(rows) < 2 || rows[0] != "⏺ Read(b.go)" || rows[1] != "  ⎿  Interrupted" || resumed.lineRuns[resumed.entryLines[stale]][0].blink {
			t.Fatalf("%s: stale call rows %q", when, rows)
		}
	}
	check("idle")
	// A new turn starts and runs a call of its own; the stale one must not
	// come back to life, and the new one is the only live item.
	resumed.working, resumed.runID, resumed.reasoningStream = true, 2, -1
	resumed.events = make(chan agent.TurnEvent, 64)
	resumed.entries = append(resumed.entries, entry{role: "You", content: "again"})
	startCall(resumed, "c", "read", `{"path":"c.go"}`)
	check("next turn")
	fresh := len(resumed.entries) - 1
	if rows := itemRows(resumed, fresh); len(rows) < 2 || rows[1] != "  ⎿  Running" || !resumed.lineRuns[resumed.entryLines[fresh]][0].blink {
		t.Fatalf("live call rows %q", rows)
	}
	// A provider reusing the call ID starts a new item; the stale one stays.
	startCall(resumed, "b", "read", `{"path":"b.go"}`)
	check("reused id")
	if rows := itemRows(resumed, len(resumed.entries)-1); len(rows) < 2 || rows[1] != "  ⎿  Running" {
		t.Fatalf("reused-id call rows %q", rows)
	}
}

func TestToolPreviewCaps(t *testing.T) {
	long := strings.Repeat("line\n", 9)
	cases := []struct {
		name, arguments, content string
		preview                  int
		hint                     string
	}{
		{"grep", `{"pattern":"line"}`, "grep: line; returned=9; x\n" + strings.Repeat("a.go:1: line\n", 9), 0, ""},
		{"glob", `{"pattern":"*.go"}`, "glob: *.go; returned=9; x\n" + long, 0, ""},
		{"read", `{"path":"a.go"}`, long, 3, "     … +6 lines (ctrl+o to expand)"},
		{"run_command", `{"command":"ls"}`, "Exit status: 0\n" + long, 3, "     … +6 lines (ctrl+o to expand)"},
		{"mcp__gh__search__1", `{"q":"x"}`, long, 3, "     … +6 lines (ctrl+o to expand)"},
	}
	for _, tc := range cases {
		m := toolItemTestUI(t, 80)
		call := startCall(m, "c", tc.name, tc.arguments)
		finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: tc.content})
		rows := itemRows(m, len(m.entries)-2) // a Working row follows the result
		previews := 0
		for _, row := range rows[2:] {
			if strings.HasPrefix(row, "     line") {
				previews++
			}
		}
		if previews != tc.preview {
			t.Fatalf("%s: %d preview rows, want %d: %q", tc.name, previews, tc.preview, rows)
		}
		if tc.hint == "" && len(rows) != 2 || tc.hint != "" && rows[len(rows)-1] != tc.hint {
			t.Fatalf("%s: rows %q, want expand hint %q", tc.name, rows, tc.hint)
		}
	}
	// The ASCII set spells the truncation glyph out.
	m := blocksTestUI(t, true, 80)
	m.working, m.runID, m.reasoningStream = true, 1, -1
	m.events = make(chan agent.TurnEvent, 8)
	call := startCall(m, "c", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: long})
	rows := itemRows(m, len(m.entries)-2)
	if rows[0] != "* Read(a.go)" || rows[1] != "  L  Read 9 lines" || rows[len(rows)-1] != "     ... +6 lines (ctrl+o to expand)" {
		t.Fatalf("ascii rows %q", rows)
	}
}

func TestLegacyRequestResultMerge(t *testing.T) {
	m := blocksTestUI(t, false, 80)
	m.entries = []entry{
		{role: "You", content: "look"},
		// Parallel group: both requests, then both results in call order.
		{role: "Tool", content: `Request: grep {"pattern":"a"}`},
		{role: "Tool", content: `Request: read {"path":"b.go"}`},
		{role: "Tool", content: "grep: grep: a; returned=1; x\nb.go:1: a\n"},
		{role: "Tool", content: "read: package b\n"},
		// A record-less legacy pair: no status was recorded.
		{role: "Tool", content: `Request: glob {"pattern":"*.md"}`},
		{role: "Tool", content: "glob: README.md\n"},
		// A request whose result never arrived.
		{role: "Tool", content: `Request: run_command {"command":"make"}`},
		{role: "Assistant", content: "done"},
		// A legacy task: records at both the request and the result.
		{role: "Tool", content: `Request: task {"description":"Map routes"}`},
		{role: "Agent", content: "t1 · parent main · depth 1 · completed · Map routes"},
		{role: "Tool", content: `task: {"task_id":"t1","status":"completed","findings":"Routes in app/"}`},
	}
	m.toolRecords = []session.ToolRecord{
		{EntryIndex: 3, CallID: "g", Name: "grep", Arguments: `{"pattern":"a"}`, SourceKind: "builtin", Status: "succeeded", Content: "grep: a; returned=1; x\nb.go:1: a\n"},
		{EntryIndex: 4, CallID: "r", Name: "read", Arguments: `{"path":"b.go"}`, SourceKind: "builtin", Status: "succeeded", Content: "package b\n"},
		{EntryIndex: 9, CallID: "t", Name: "task", Arguments: `{"description":"Map routes"}`, Status: "completed", TaskID: "t1"},
		{EntryIndex: 11, CallID: "t", Name: "task", Arguments: `{"description":"Map routes"}`, Status: "succeeded", TaskID: "t1", Content: `{"task_id":"t1","status":"completed","findings":"Routes in app/"}`},
	}
	plan := m.toolItemPlan()
	wantPartner := map[int]int{1: 3, 2: 4, 5: 6, 9: 11}
	for request, result := range wantPartner {
		if plan.partner[request] != result || !plan.absorbed[result] || plan.owner[result] != request {
			t.Fatalf("request %d paired with %d (absorbed=%t owner=%d), want %d", request, plan.partner[request], plan.absorbed[result], plan.owner[result], result)
		}
	}
	if plan.partner[7] != -1 {
		t.Fatalf("unmatched request paired with %d", plan.partner[7])
	}
	if got, want := m.inspectableToolEntries(), []int{1, 2, 5, 7, 9}; !slices.Equal(got, want) {
		t.Fatalf("tool items %v, want %v", got, want)
	}
	want := map[int][]string{
		1: {"⏺ Grep(a)", "  ⎿  Found 1 match in 1 file"},
		2: {"⏺ Read(b.go)", "  ⎿  Read 1 line", "     package b"},
		5: {"⏺ Glob(*.md)", "  ⎿  Status not recorded"},
		7: {"⏺ Run(make)"},
		9: {"⏺ Task(Map routes)", "  ⎿  Done", "     Routes in app/"},
	}
	for index, rows := range want {
		got := itemRows(m, index)
		if strings.Join(got, "\n") != strings.Join(rows, "\n") {
			t.Fatalf("item %d rows %q, want %q", index, got, rows)
		}
	}
	// The unmatched request's dot is the unknown-status Muted.
	if runs := m.lineRuns[m.entryLines[7]]; colorName(runs[0].style.GetForeground()) != colorName(m.theme.Muted.GetForeground()) {
		t.Fatalf("unmatched request dot %v, want Muted", runs[0].style)
	}
	// Folded results lay out nothing of their own.
	for _, result := range []int{3, 4, 6, 11} {
		if m.entryLines[result] != m.entryLines[plan.owner[result]] {
			t.Fatalf("folded result %d starts at %d, want its item's %d", result, m.entryLines[result], m.entryLines[plan.owner[result]])
		}
	}
	for _, line := range m.lines {
		if strings.HasPrefix(line, "Tool:") || strings.Contains(line, "Request:") {
			t.Fatalf("legacy label text reached the layout: %q", line)
		}
	}
}

func TestToolFocusOneStopPerCallAndInspector(t *testing.T) {
	m := blocksTestUI(t, false, 80)
	m.theme = likhaui.Resolve("catppuccin", true)
	if m.theme.Selected.GetBackground() == nil {
		t.Fatal("catppuccin Selected has no selection tint to assert")
	}
	m.entries = []entry{
		{role: "You", content: "look"},
		{role: "Tool", content: `Request: read {"path":"a.go"}`},
		{role: "Tool", content: "read: legacy body\n"},
		{role: "Tool", content: "grep: settled"},
		{role: "Tool", content: `Request: read {"path":"c.go"}`},
	}
	m.toolRecords = []session.ToolRecord{
		{EntryIndex: 3, CallID: "g2", Name: "grep", Arguments: `{"pattern":"q"}`, SourceKind: "builtin", Status: "succeeded", Content: "grep: q; returned=0; x\n"},
		{EntryIndex: 4, CallID: "r3", Name: "read", Arguments: `{"path":"c.go"}`, Status: "running"},
	}
	m.layoutWidth = 0
	items := m.inspectableToolEntries()
	if len(items) != 3 {
		t.Fatalf("items %v, want 3 calls", items)
	}
	seen := map[int]bool{}
	var order []int
	for range items {
		m.Update(tea.KeyMsg{Type: tea.KeyTab})
		index, ok := m.focusedToolEntry()
		if !ok || seen[index] {
			t.Fatalf("tab focused %d (ok=%t) twice or not at all: %v", index, ok, seen)
		}
		seen[index] = true
		order = append(order, index)
		rows := itemRows(m, index)
		if !strings.HasPrefix(rows[0], m.blocks.Focus+" ") {
			t.Fatalf("focused item %d header %q lacks the focus glyph", index, rows[0])
		}
		for i := m.entryLines[index]; i < m.entryLines[index]+len(rows); i++ {
			if got, want := colorName(m.lineStyles[i].GetBackground()), colorName(m.theme.Selected.GetBackground()); got != want {
				t.Fatalf("focused row %q bg %v, want selection %v", m.lines[i], got, want)
			}
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if index, _ := m.focusedToolEntry(); index != order[0] {
		t.Fatalf("tab after the last item focused %d, want the first visited %d", index, order[0])
	}
	if help := m.toolInspectionHelp(); !strings.Contains(help, "/3 focused") {
		t.Fatalf("focus help counts calls, not entries: %q", help)
	}
	// Focus the settled grep call and inspect it: the existing inspector.
	m.setToolEntryFocus(3)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.toolInspectionVisible() {
		t.Fatal("enter on a focused item did not open the inspector")
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Call: g2", "Source: Likha built-in", "Result status: succeeded", `"pattern": "q"`} {
		if !strings.Contains(view, want) {
			t.Fatalf("inspector lacks %q: %s", want, view)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	// A legacy merged pair inspects both halves.
	m.setToolEntryFocus(1)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	view = stripANSI(m.View())
	if !m.toolInspectionVisible() || !strings.Contains(view, `Request: read {"path":"a.go"}`) || !strings.Contains(view, "read: legacy body") {
		t.Fatalf("legacy pair inspector: %s", view)
	}
}

func TestRunningDotBlinksOnlyWhileLive(t *testing.T) {
	m := toolItemTestUI(t, 80)
	m.showActivity()
	call := model.ToolCall{ID: "c", Name: "run_command", Arguments: `{"command":"sleep 5"}`}
	_, cmd := m.Update(agent.TurnEvent{RunID: 1, Kind: "tool_start", Text: "Request: run_command", ToolCall: &call})
	if cmd == nil || m.activityArmed != m.activityGeneration+1 {
		t.Fatalf("tool_start did not arm the activity tick: armed=%d generation=%d", m.activityArmed, m.activityGeneration)
	}
	index := len(m.entries) - 1
	// Ticks keep running for the dot after the Working row handed over.
	for i := 0; i < 2*toolBlinkFrames; i++ {
		if _, cmd := m.Update(activityTickMsg{runID: 1, generation: m.activityGeneration}); cmd == nil {
			t.Fatalf("tick %d stopped while the call runs", i)
		}
	}
	itemRows(m, index)
	header := m.entryLines[index]
	if runs := m.lineRuns[header]; !runs[0].blink {
		t.Fatalf("running dot does not blink: %+v", runs)
	}
	row := func() string {
		m.scroll, m.following = 0, false
		for _, line := range strings.Split(stripANSI(m.mainView()), "\n") {
			if strings.Contains(line, "Run(sleep 5)") {
				return line
			}
		}
		t.Fatal("header row not on screen")
		return ""
	}
	forceANSI(t)
	m.activityFrame = 0
	on := row()
	m.activityFrame = toolBlinkFrames
	off := row()
	if !strings.HasPrefix(on, "⏺ Run") || !strings.HasPrefix(off, "  Run") || runewidth.StringWidth(on) != runewidth.StringWidth(off) {
		t.Fatalf("blink phases %q / %q", on, off)
	}
	// Without color the glyph is the only signal: it stays.
	lipgloss.SetColorProfile(termenv.Ascii)
	if solid := row(); !strings.HasPrefix(solid, "⏺ Run") {
		t.Fatalf("no-color off phase blanked the dot: %q", solid)
	}
	// Approval holds the call as awaiting approval; answering resumes it.
	reply := make(chan bool, 1)
	m.Update(agent.TurnEvent{RunID: 1, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "sleep 5", Reply: reply}})
	if record, _ := m.toolRecordAt(index); record.Status != "awaiting approval" {
		t.Fatalf("approval left status %q", record.Status)
	}
	if summary, _, _ := toolSummary(m.toolRecords[0], "…"); summary != "Awaiting approval" {
		t.Fatalf("approval summary %q", summary)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if record, _ := m.toolRecordAt(index); record.Status != "running" || !<-reply {
		t.Fatalf("approved call status %q", record.Status)
	}
	// Between runs nothing is running: a lost result reads interrupted, solid.
	m.working = false
	m.liveToolCalls = nil
	rows := itemRows(m, index)
	if rows[1] != "  ⎿  Interrupted" || m.lineRuns[header][0].blink || m.toolBlinkOff() {
		t.Fatalf("stale running call rows %q", rows)
	}
	if got := colorName(m.lineRuns[header][0].style.GetForeground()); got != colorName(m.theme.Error.GetForeground()) {
		t.Fatalf("interrupted dot %v, want Error", got)
	}
}

func TestToolOutcomeWordsSurviveNoColor(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := toolItemTestUI(t, 80)
	// default: the status canvas paints its own hex background regardless
	// of the profile in themed families, which is not under test here.
	m.theme = likhaui.Resolve("default", true)
	ok := startCall(m, "a", "read", `{"path":"a.go"}`)
	bad := startCall(m, "b", "run_command", `{"command":"false"}`)
	finishCall(m, ok, tools.Result{Status: tools.Succeeded, Content: "x\n"})
	finishCall(m, bad, tools.Result{Status: tools.Failed, Content: "Exit status: 1\nError: command failed with exit status 1"})
	endRun(m)
	m.scroll, m.following = 0, false
	view := m.View()
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("no-color profile emitted escapes: %q", view)
	}
	for _, want := range []string{"⏺ Read(a.go)", "⎿  Read 1 line", "⏺ Run(false)", "⎿  Failed: exit status 1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("no-color view lacks %q: %s", want, view)
		}
	}
}

func TestCancelledRunRecordsUnexecutedCallsOnce(t *testing.T) {
	m := toolItemTestUI(t, 80)
	m.cancel = func() {}
	m.entries = append(m.entries, entry{role: "You", content: "go"})
	started := startCall(m, "s", "read", `{"path":"a.go"}`)
	interrupted := tools.Result{Status: tools.Cancelled, Content: unexecutedToolContent}
	finishCall(m, started, interrupted)
	m.cancelling = true
	history := []model.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []model.ToolCall{started, {ID: "u", Name: "grep", Arguments: `{"pattern":"z"}`}}},
		{Role: "tool", ToolCallID: "s", Content: unexecutedToolContent},
		{Role: "tool", ToolCallID: "u", Content: unexecutedToolContent},
	}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "done", History: history})
	items := m.inspectableToolEntries()
	if len(items) != 2 {
		t.Fatalf("cancelled run shows %d tool items, want one per call: %+v", len(items), m.entries)
	}
	if rows := itemRows(m, items[1]); rows[0] != "⏺ Grep(z)" || !strings.HasPrefix(rows[1], "  ⎿  Cancelled") {
		t.Fatalf("unexecuted call rows %q", rows)
	}
	if m.entries[items[1]].content != unexecutedToolContent {
		t.Fatalf("unexecuted entry content %q", m.entries[items[1]].content)
	}
}

// A compaction cancelled right after a cancelled turn adopts the same
// history; its run starts there, so the turn's unexecuted calls are not
// recorded a second time.
func TestCancelledCompactionAddsNoUnexecutedItems(t *testing.T) {
	client, err := model.New("http://127.0.0.1:1/v1", "test-model", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	m := toolItemTestUI(t, 80)
	m.client = client
	m.cancel = func() {}
	m.entries = append(m.entries, entry{role: "You", content: "go"})
	started := startCall(m, "s", "read", `{"path":"a.go"}`)
	finishCall(m, started, tools.Result{Status: tools.Cancelled, Content: unexecutedToolContent})
	m.cancelling = true
	m.history = []model.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []model.ToolCall{started, {ID: "u", Name: "grep", Arguments: `{"pattern":"z"}`}}},
		{Role: "tool", ToolCallID: "s", Content: unexecutedToolContent},
		{Role: "tool", ToolCallID: "u", Content: unexecutedToolContent},
	}
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "done", History: m.history})
	before := len(m.inspectableToolEntries())
	if before != 2 {
		t.Fatalf("cancelled turn shows %d items, want 2", before)
	}
	m.startCompaction("")
	m.cancel()
	close(m.abandon)
	m.cancelling = true
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "error", Text: "context canceled", History: m.history})
	if got := len(m.inspectableToolEntries()); got != before || len(m.toolRecords) != 2 {
		t.Fatalf("cancelled compaction left %d items and %d records, want %d each", got, len(m.toolRecords), before)
	}
}

func TestReusedCallIDUpdatesOnlyTheLiveItem(t *testing.T) {
	m := toolItemTestUI(t, 80)
	first := startCall(m, "call_0", "read", `{"path":"a.go"}`)
	finishCall(m, first, tools.Result{Status: tools.Succeeded, Content: "a\n"})
	m.entries = append(m.entries, entry{role: "Assistant", content: "next round"})
	second := startCall(m, "call_0", "read", `{"path":"b.go"}`)
	finishCall(m, second, tools.Result{Status: tools.Failed, Content: "Error: missing"})
	if len(m.toolRecords) != 2 || m.toolRecords[0].Status != "succeeded" || m.toolRecords[1].Status != "failed" {
		t.Fatalf("reused call ID rewrote the earlier item: %+v", m.toolRecords)
	}
	if got := len(m.inspectableToolEntries()); got != 2 {
		t.Fatalf("%d items, want 2", got)
	}
}

func TestToolItemsNeverOverflow(t *testing.T) {
	wide := strings.Repeat("漢字", 30)
	for _, ascii := range []bool{false, true} {
		for _, width := range []int{40, 80} {
			m := blocksTestUI(t, ascii, width)
			m.working, m.runID, m.reasoningStream = true, 1, -1
			m.events = make(chan agent.TurnEvent, 64)
			long := startCall(m, "a", "run_command", `{"command":"`+strings.Repeat("x", 300)+`"}`)
			cjk := startCall(m, "b", "read", `{"path":"`+wide+`.go"}`)
			mcp := startCall(m, "c", "mcp__server__tool__1", `{"query":"\u001b[31mred","n":12345678901234567890}`)
			finishCall(m, long, tools.Result{Status: tools.Failed, Content: "Exit status: 2\n" + strings.Repeat("y", 500) + "\n" + wide + "\n\x1b[2Jclear\tTab\nmore\nmore\n"})
			finishCall(m, cjk, tools.Result{Status: tools.Refused, Content: "Error: " + strings.Repeat("reason ", 80)})
			finishCall(m, mcp, tools.Result{Status: tools.Succeeded, Content: wide + "\n" + wide + "\n" + wide + "\n" + wide})
			startCall(m, "d", "web_search", `{"query":"`+wide+`"}`)
			m.entries = append(m.entries, entry{role: "Tool", content: `Request: grep {"pattern":"` + wide + `"}`})
			m.layoutWidth = 0
			m.rebuild()
			for i, line := range m.lines {
				if w := runewidth.StringWidth(line); w > contentWidth(width) {
					t.Fatalf("ascii=%t width %d: row %d is %d cells: %q", ascii, width, i, w, line)
				}
				if strings.ContainsRune(line, '\x1b') {
					t.Fatalf("raw escape reached the layout: %q", line)
				}
			}
			for page := 0; page*m.bodyHeight() < len(m.lines); page++ {
				m.scroll, m.following = page*m.bodyHeight(), false
				assertViewport(t, m.View(), width, 24)
			}
			m.setToolEntryFocus(m.inspectableToolEntries()[0])
			m.layoutWidth = 0
			assertViewport(t, m.View(), width, 24)
		}
	}
}
