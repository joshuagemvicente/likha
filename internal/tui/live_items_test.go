package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"likha/internal/actions"
	"likha/internal/agent"
	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tools"
	likhaui "likha/internal/ui"
)

// Live surfaces (transcript-redesign § Diffs, § Subagent items, §
// Persistence): an applied edit shows its reviewed diff under the ⎿ summary,
// and a task item carries its subagent's live activity, then its outcome,
// with no Agent entries.

// editRepo writes a repository whose a.go holds "line 1" … "line 20".
func editRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// editReviewBody builds the edit tool's review body exactly as the agent
// does (agent/tool_registry.go): the affected-files list, then the real
// unified diff internal/actions prepares.
func editReviewBody(t *testing.T, root string, ops []actions.EditOperation) string {
	t.Helper()
	proposal, err := actions.PrepareEdits(root, ops)
	if err != nil {
		t.Fatal(err)
	}
	body := "Affected files:\n" + strings.Join(proposal.Paths, "\n") + "\n"
	if len(proposal.Directories) > 0 {
		body += "\nRequired new directories:\n" + strings.Join(proposal.Directories, "\n") + "\n"
	}
	return body + "\n" + proposal.Diff
}

func editArguments(t *testing.T, ops []actions.EditOperation) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"operations": ops})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// approveEdit opens the review for the running call and approves it with
// Enter, the way the user does.
func approveEdit(t *testing.T, m *ui, body string) {
	t.Helper()
	reply := make(chan bool, 1)
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "edit", Title: "Review complete file change", Body: body, Reply: reply}})
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.pending != nil || !<-reply {
		t.Fatal("the edit review was not approved")
	}
}

// trimmed drops the diff tint's padding, which is part of the row text.
func trimmed(rows []string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = strings.TrimRight(row, " ")
	}
	return out
}

func TestEditItemShowsReviewedDiff(t *testing.T) {
	var created []string
	for i := 1; i <= 14; i++ {
		created = append(created, fmt.Sprintf("row %d", i))
	}
	cases := []struct {
		name string
		ops  []actions.EditOperation
		want []string
	}{
		{"modify", []actions.EditOperation{{Kind: "replace", Path: "a.go", OldText: "line 12\n", NewText: "line twelve\nline 12b\n"}}, []string{
			"⏺ Update(a.go)",
			"  ⎿  Updated a.go with 2 additions and 1 removal",
			"     12 - line 12",
			"     12 + line twelve",
			"     13 + line 12b",
		}},
		{"multi-file", []actions.EditOperation{
			{Kind: "replace", Path: "a.go", OldText: "line 3\n", NewText: "line three\n"},
			{Kind: "create", Path: "b.go", Content: "x\ny\n"},
			{Kind: "create", Path: "c.go", Content: "z\n"},
		}, []string{
			"⏺ Update(3 files)",
			"  ⎿  Updated 3 files with 4 additions and 1 removal",
			"     3 - line 3",
			"     3 + line three",
			"     Also b.go, c.go",
		}},
		{"create", []actions.EditOperation{{Kind: "create", Path: "new.go", Content: "package x\n\nfunc F() {}\n"}}, []string{
			"⏺ Create(new.go)",
			"  ⎿  Created new.go (3 lines)",
			"     1 + package x",
			"     2 +",
			"     3 + func F() {}",
		}},
		{"more than ten lines", []actions.EditOperation{{Kind: "create", Path: "long.go", Content: strings.Join(created, "\n") + "\n"}}, []string{
			"⏺ Create(long.go)",
			"  ⎿  Created long.go (14 lines)",
			"      1 + row 1", "      2 + row 2", "      3 + row 3", "      4 + row 4", "      5 + row 5",
			"      6 + row 6", "      7 + row 7", "      8 + row 8", "      9 + row 9", "     10 + row 10",
			"     … +4 lines (enter to expand · ctrl+o to inspect)",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := editRepo(t)
			m := toolItemTestUI(t, 80)
			body := editReviewBody(t, root, tc.ops)
			call := startCall(m, "e", "edit", editArguments(t, tc.ops))
			index := len(m.entries) - 1
			approveEdit(t, m, body)
			finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "Applied edits"})
			record, _ := m.toolRecordAt(index)
			if !strings.HasPrefix(record.Diff, "--- ") || strings.Contains(record.Diff, "Affected files:") {
				t.Fatalf("record diff %q, want the reviewed unified diff alone", record.Diff)
			}
			if len(m.approvedEdits) != 0 {
				t.Fatalf("approved edits kept after the result: %v", m.approvedEdits)
			}
			rows := itemRows(m, index)
			if got := trimmed(rows); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			// Diff rows tint their whole text column by sign; the row
			// itself, gutter included, stays on the canvas.
			start := m.entryLines[index]
			for i, row := range rows {
				if w := runewidth.StringWidth(row); w > contentWidth(80) {
					t.Fatalf("row %d is %d cells: %q", i, w, row)
				}
				sign := ""
				if fields := strings.Fields(row); len(fields) >= 2 && (fields[1] == "+" || fields[1] == "-") {
					sign = fields[1]
				}
				runs := m.lineRuns[start+i]
				if sign == "" {
					continue
				}
				want := m.theme.BgDiffAdd
				if sign == "-" {
					want = m.theme.BgDiffRemove
				}
				if len(runs) != 1 || runs[0].start != 5 || runs[0].end != len([]rune(row)) {
					t.Fatalf("diff row %q runs %+v, want one tint over the text column", row, runs)
				}
				if got := colorName(runs[0].style.GetBackground()); got == "" || got != colorName(want.GetBackground()) {
					t.Fatalf("diff row %q background %q, want %q", row, got, colorName(want.GetBackground()))
				}
			}
			if dot := m.lineRuns[start][0]; colorName(dot.style.GetForeground()) != colorName(m.theme.Success.GetForeground()) {
				t.Fatalf("applied edit dot %v, want Success", dot.style)
			}
		})
	}
}

func TestEditDiffASCIIAndNarrow(t *testing.T) {
	root := editRepo(t)
	m := blocksTestUI(t, true, 40)
	m.working, m.runID, m.reasoningStream = true, 1, -1
	m.events = make(chan agent.TurnEvent, 64)
	long := strings.Repeat("wide ", 20) + "漢字"
	ops := []actions.EditOperation{{Kind: "replace", Path: "a.go", OldText: "line 7\n", NewText: long + "\n"}}
	call := startCall(m, "e", "edit", editArguments(t, ops))
	index := len(m.entries) - 1
	approveEdit(t, m, editReviewBody(t, root, ops))
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "Applied edits"})
	rows := itemRows(m, index)
	// The summary wraps under its text column; diff rows never wrap.
	if len(rows) != 5 || rows[0] != "* Update(a.go)" || rows[1] != "  L  Updated a.go with 1 addition and" || rows[2] != "     1 removal" || strings.TrimRight(rows[3], " ") != "     7 - line 7" {
		t.Fatalf("ascii rows %q", rows)
	}
	if !strings.HasPrefix(rows[4], "     7 + wide wide") || !strings.HasSuffix(rows[4], "...") || runewidth.StringWidth(rows[4]) != contentWidth(40) {
		t.Fatalf("long diff row %q, want clipped with the ASCII ellipsis", rows[4])
	}
	for i, row := range m.lines {
		if w := runewidth.StringWidth(row); w > contentWidth(40) {
			t.Fatalf("row %d is %d cells: %q", i, w, row)
		}
	}
	assertViewport(t, m.View(), 40, 24)
}

// Without color the tints drop and the sign column alone tells added from
// removed lines.
func TestEditDiffSignsSurviveNoColor(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	root := editRepo(t)
	m := toolItemTestUI(t, 80)
	m.theme = likhaui.Resolve("default", true)
	ops := []actions.EditOperation{{Kind: "replace", Path: "a.go", OldText: "line 12\n", NewText: "line twelve\n"}}
	call := startCall(m, "e", "edit", editArguments(t, ops))
	approveEdit(t, m, editReviewBody(t, root, ops))
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "Applied edits"})
	endRun(m)
	m.scroll, m.following = 0, false
	view := m.View()
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("no-color profile emitted escapes: %q", view)
	}
	for _, want := range []string{"⎿  Updated a.go with 1 addition and 1 removal", "     12 - line 12", "     12 + line twelve"} {
		if !strings.Contains(view, want) {
			t.Fatalf("no-color view lacks %q: %s", want, view)
		}
	}
}

// A real edit_file run: the review the agent requests is approved, the file
// is written, and the item shows the diff the user reviewed.
func TestPermE2EApprovedEditShowsDiff(t *testing.T) {
	m, _, _, _ := permE2EnewTurn(t)
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	indices := toolEntryIndices(m)
	if len(indices) != 1 {
		t.Fatalf("tool items %v, want one", indices)
	}
	record, _ := m.toolRecordAt(indices[0])
	if record.Status != string(tools.Succeeded) || !strings.HasPrefix(record.Diff, "--- a/work.txt\n+++ b/work.txt\n") {
		t.Fatalf("applied edit record %+v", record)
	}
	rows := trimmed(itemRows(m, indices[0]))
	want := []string{"⏺ Update(work.txt)", "  ⎿  Updated work.txt with 120 additions and 1 removal", "     1 - before", "     1 + new line"}
	if len(rows) != 13 || strings.Join(rows[:4], "\n") != strings.Join(want, "\n") || rows[12] != "     … +111 lines (enter to expand · ctrl+o to inspect)" {
		t.Fatalf("applied edit rows %q", rows)
	}
}

// A declined edit never claims the change: no diff is stored or drawn.
func TestPermE2EDeclinedEditHasNoDiff(t *testing.T) {
	m, _, _, _ := permE2EnewTurn(t)
	// Edit reviews offer Approve always between Approve and Decline
	// (specs/approve-always); focus stops at Decline.
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	permE2Epump(t, m, func() bool { return !m.working })
	indices := toolEntryIndices(m)
	record, _ := m.toolRecordAt(indices[0])
	if record.Status != string(tools.Refused) || record.Diff != "" || len(m.approvedEdits) != 0 {
		t.Fatalf("declined edit record %+v, approved %v", record, m.approvedEdits)
	}
	rows := itemRows(m, indices[0])
	if len(rows) != 2 || rows[1] != "  ⎿  Refused: edit rejected by user" {
		t.Fatalf("declined edit rows %q", rows)
	}
}

// An approved edit that fails to apply, or a run cancelled after approval,
// stores no diff either.
func TestApprovedEditWithoutApplyHasNoDiff(t *testing.T) {
	root := editRepo(t)
	ops := []actions.EditOperation{{Kind: "replace", Path: "a.go", OldText: "line 1\n", NewText: "line one\n"}}
	m := toolItemTestUI(t, 80)
	call := startCall(m, "e", "edit", editArguments(t, ops))
	index := len(m.entries) - 1
	approveEdit(t, m, editReviewBody(t, root, ops))
	finishCall(m, call, tools.Result{Status: tools.Failed, Content: "Error: private edit recovery journal unavailable; no file changes applied"})
	record, _ := m.toolRecordAt(index)
	if record.Diff != "" {
		t.Fatalf("failed edit stored a diff: %q", record.Diff)
	}
	if rows := itemRows(m, index); len(rows) != 3 || rows[1]+" "+strings.TrimSpace(rows[2]) != "  ⎿  Failed: private edit recovery journal unavailable; no file changes applied" {
		t.Fatalf("failed edit rows %q", rows)
	}

	m = toolItemTestUI(t, 80)
	startCall(m, "e", "edit", editArguments(t, ops))
	approveEdit(t, m, editReviewBody(t, root, ops))
	endRun(m)
	if m.approvedEdits != nil {
		t.Fatalf("a finished run kept approved edits: %v", m.approvedEdits)
	}
	for _, record := range m.toolRecords {
		if record.Diff != "" {
			t.Fatalf("an edit without a result stored a diff: %+v", record)
		}
	}
}

// The reviewed diff persists on the record and renders the same numbered
// rows after resume; records without one fall back to the arguments.
func TestEditDiffSurvivesResume(t *testing.T) {
	root := editRepo(t)
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
	m.entries = append(m.entries, entry{role: "You", content: "edit"})
	ops := []actions.EditOperation{{Kind: "replace", Path: "a.go", OldText: "line 12\n", NewText: "line twelve\nline 12b\n"}}
	call := startCall(m, "e", "edit", editArguments(t, ops))
	approveEdit(t, m, editReviewBody(t, root, ops))
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "Applied edits"})
	file := startCall(m, "f", "edit_file", `{"path":"b.go","content":"package b\n"}`)
	approveEdit(t, m, "--- /dev/null\n+++ b/b.go\n@@ -0,0 +1,1 @@\n+package b\n")
	finishCall(m, file, tools.Result{Status: tools.Succeeded, Content: "Applied edit to b.go"})
	endRun(m)
	saved, err := store.Load(m.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ToolRecords) != 2 || !strings.HasPrefix(saved.ToolRecords[0].Diff, "--- a/a.go\n") || saved.ToolRecords[1].Diff != "--- /dev/null\n+++ b/b.go\n@@ -0,0 +1,1 @@\n+package b\n" {
		t.Fatalf("saved records %+v, want both reviewed diffs", saved.ToolRecords)
	}
	resume := func(snapshot session.Snapshot) (*ui, []int) {
		resumed := NewUI(root, nil, nil, "local", conn, "", store, snapshot)
		resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		return resumed, toolEntryIndices(resumed)
	}
	resumed, indices := resume(saved)
	want := map[int][]string{
		0: {"⏺ Update(a.go)", "  ⎿  Updated a.go with 2 additions and 1 removal", "     12 - line 12", "     12 + line twelve", "     13 + line 12b"},
		// edit_file's create reads Create(path), like edit's create.
		1: {"⏺ Create(b.go)", "  ⎿  Created b.go (1 line)", "     1 + package b"},
	}
	for i, index := range indices {
		if got := trimmed(itemRows(resumed, index)); strings.Join(got, "\n") != strings.Join(want[i], "\n") {
			t.Fatalf("resumed item %d rows %q, want %q", i, got, want[i])
		}
	}
	// Records saved before the diff field: edit rebuilds its lines from
	// the arguments without numbers; edit_file keeps the generic line.
	for i := range saved.ToolRecords {
		saved.ToolRecords[i].Diff = ""
	}
	legacy, indices := resume(saved)
	if got := trimmed(itemRows(legacy, indices[0])); strings.Join(got, "\n") != "⏺ Update(a.go)\n  ⎿  Updated a.go with 2 additions and 1 removal\n     - line 12\n     + line twelve\n     + line 12b" {
		t.Fatalf("argument fallback rows %q", got)
	}
	if got := itemRows(legacy, indices[1]); len(got) != 3 || got[0] != "⏺ Update(b.go)" || got[1] != "  ⎿  Done" || got[2] != "     Applied edit to b.go" {
		t.Fatalf("edit_file without a diff rows %q", got)
	}
}

// edit_file cannot say from its arguments whether it creates a file; once
// applied, its reviewed diff does, and a create reads Create(path) to match
// its "Created" summary while an update keeps Update(path).
func TestEditFileCreateHeaderFollowsAppliedDiff(t *testing.T) {
	root := editRepo(t)
	for _, tc := range []struct {
		path, content string
		header        string
		summary       string
	}{
		{"fresh.go", "a\nb\nc\n", "⏺ Create(fresh.go)", "  ⎿  Created fresh.go (3 lines)"},
		{"a.go", "line 1\n", "⏺ Update(a.go)", "  ⎿  Updated a.go with 19 removals"},
	} {
		proposal, err := actions.PrepareEdit(root, tc.path, tc.content)
		if err != nil {
			t.Fatal(err)
		}
		m := toolItemTestUI(t, 80)
		args, _ := json.Marshal(map[string]string{"path": tc.path, "content": tc.content})
		call := startCall(m, "f", "edit_file", string(args))
		index := len(m.entries) - 1
		if got := itemRows(m, index); got[0] != "⏺ Update("+tc.path+")" {
			t.Fatalf("%s: running header %q, want Update until the diff is applied", tc.path, got[0])
		}
		approveEdit(t, m, proposal.Diff)
		finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "Applied edit to " + tc.path})
		rows := trimmed(itemRows(m, index))
		if rows[0] != tc.header || rows[1] != tc.summary {
			t.Fatalf("%s: applied rows %q, want %q / %q", tc.path, rows, tc.header, tc.summary)
		}
	}
}

// taskEvent delivers one explore record update the way the runtime does.
func taskEvent(m *ui, record explore.Record) {
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "task", Task: &record})
}

func roles(m *ui) map[string]int {
	counts := map[string]int{}
	for _, e := range m.entries {
		if e.role != "Working" { // the activity row comes and goes
			counts[e.role]++
		}
	}
	return counts
}

func TestTaskItemLiveThenSettled(t *testing.T) {
	cases := []struct {
		name    string
		status  explore.State
		reason  string
		result  tools.Status
		settle  string
		success bool
		preview []string
	}{
		{"completed", explore.Completed, "", tools.Succeeded, "  ⎿  Done · 2 tool uses · wait 1s · active 4s", true, []string{"     Routes in app/"}},
		{"failed", explore.Failed, "provider error", tools.Failed, "  ⎿  Failed: provider error", false, nil},
		{"cancelled", explore.Cancelled, "explore task cancelled", tools.Cancelled, "  ⎿  Cancelled", false, nil},
		{"limited", explore.Limited, "explore model-request limit reached", tools.Limited, "  ⎿  Limited: model-request limit", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := toolItemTestUI(t, 80)
			m.entries = append(m.entries, entry{role: "You", content: "map it"})
			call := startCall(m, "t", "task", `{"agent":"explore","description":"Map routes","prompt":"map"}`)
			index := len(m.entries) - 1
			before := roles(m)
			check := func(when, summary string, dot lipgloss.Style, blink bool) []string {
				t.Helper()
				// The update itself invalidates the layout: the next frame
				// shows the new line without any forced rebuild.
				if view := stripANSI(m.View()); !strings.Contains(view, summary) {
					t.Fatalf("%s: frame lacks %q: %s", when, summary, view)
				}
				rows := itemRows(m, index)
				if len(rows) < 2 || rows[0] != "⏺ Task(Map routes)" || rows[1] != summary {
					t.Fatalf("%s: rows %q, want summary %q", when, rows, summary)
				}
				run := m.lineRuns[m.entryLines[index]][0]
				if run.blink != blink || colorName(run.style.GetForeground()) != colorName(dot.GetForeground()) {
					t.Fatalf("%s: dot %+v blink=%t, want blink=%t fg %v", when, run.style, run.blink, blink, dot.GetForeground())
				}
				if got := roles(m); fmt.Sprint(got) != fmt.Sprint(before) {
					t.Fatalf("%s: entries changed from %v to %v", when, before, got)
				}
				return rows
			}
			muted := m.theme.Muted
			record := explore.Record{ID: "t1", SessionID: m.snapshot.ID, ParentCallID: "t", Agent: "explore", Description: "Map routes", Depth: 1, Status: explore.Queued, Version: 1}
			taskEvent(m, record)
			check("queued", "  ⎿  Queued", muted, true)

			record.Status, record.Version = explore.Running, 2
			record.History = []model.Message{{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "r1", Name: "read", Arguments: `{"path":"src/app.go"}`}}}}
			taskEvent(m, record)
			check("running", "  ⎿  Read src/app.go", muted, true)

			// A nested child: it updates the parent's line and nothing else.
			record.Status, record.Version = explore.Waiting, 3
			record.Tools = []explore.ToolRecord{{CallID: "r1", Name: "read", Arguments: `{"path":"src/app.go"}`}}
			record.History = append(record.History,
				model.Message{Role: "tool", ToolCallID: "r1", Content: "package app"},
				model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "c1", Name: "task", Arguments: `{"agent":"explore","description":"Nested","prompt":"x"}`}}})
			taskEvent(m, record)
			check("waiting, no child yet", "  ⎿  Waiting for 1 subtask", muted, true)
			child := explore.Record{ID: "t2", SessionID: m.snapshot.ID, ParentID: "t1", ParentCallID: "c1", Agent: "explore", Description: "Nested", Depth: 2, Status: explore.Running, Version: 1}
			taskEvent(m, child)
			check("waiting on a running child", "  ⎿  Waiting for 1 subtask", muted, true)
			child.Status, child.Version = explore.Completed, 2
			taskEvent(m, child)
			check("child settled", "  ⎿  Waiting for subtasks", muted, true)
			if got := len(toolEntryIndices(m)); got != 1 {
				t.Fatalf("nested child produced %d tool items, want 1", got)
			}

			record.Status, record.Version, record.Reason = tc.status, 4, tc.reason
			record.Tools = append(record.Tools, explore.ToolRecord{CallID: "c1", Name: "task"})
			record.WaitMs, record.ActiveMs = 1000, 4000
			record.Findings = "Routes in app/"
			taskEvent(m, record)
			dot := m.theme.Error
			if tc.success {
				dot = m.theme.Success
			}
			check("settled", tc.settle, dot, false)

			outcome, _ := json.Marshal(explore.Outcome{TaskID: "t1", Agent: "explore", Depth: 1, Status: tc.status, Findings: record.Findings, Reason: tc.reason})
			finishCall(m, call, tools.Result{Status: tc.result, Content: string(outcome)})
			rows := check("result", tc.settle, dot, false)
			if got := rows[2:]; strings.Join(got, "\n") != strings.Join(tc.preview, "\n") {
				t.Fatalf("result preview rows %q, want %q", got, tc.preview)
			}
			if roles(m)["Agent"] != 0 {
				t.Fatalf("Agent entries produced: %+v", m.entries)
			}
		})
	}
}

// A task whose run ended without a result reads interrupted, never live.
func TestTaskItemInterrupted(t *testing.T) {
	m := toolItemTestUI(t, 80)
	startCall(m, "t", "task", `{"agent":"explore","description":"Map routes","prompt":"map"}`)
	index := len(m.entries) - 1
	record := explore.Record{ID: "t1", SessionID: m.snapshot.ID, ParentCallID: "t", Agent: "explore", Description: "Map routes", Depth: 1, Status: explore.Running, Version: 1}
	taskEvent(m, record)
	endRun(m)
	rows := itemRows(m, index)
	run := m.lineRuns[m.entryLines[index]][0]
	if len(rows) != 2 || rows[1] != "  ⎿  Interrupted" || run.blink || colorName(run.style.GetForeground()) != colorName(m.theme.Error.GetForeground()) {
		t.Fatalf("ended-run task rows %q dot %+v", rows, run)
	}
	// Resume marks the record itself interrupted; the settle line agrees.
	m.taskRecords[0].Status = explore.Interrupted
	if rows := itemRows(m, index); len(rows) != 2 || rows[1] != "  ⎿  Interrupted" {
		t.Fatalf("interrupted record rows %q", rows)
	}
}

// A saved session with a live task, a settled task, and legacy Agent
// entries resumes as task items plus ℹ notices.
func TestTaskItemsResumeWithLegacyAgentEntries(t *testing.T) {
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
	m.entries = append(m.entries, entry{role: "You", content: "map"})
	now := time.Now().UTC()
	valid := func(id, callID string, status explore.State, version uint64) explore.Record {
		return explore.Record{ID: id, RunID: "run1", SessionID: m.snapshot.ID, Root: m.snapshot.Root, ParentCallID: callID, Agent: "explore",
			Description: "Task " + id, Depth: 1, Status: status, Version: version, AcceptedAt: now, Deadline: now.Add(time.Hour)}
	}
	done := startCall(m, "a", "task", `{"agent":"explore","description":"Task t1","prompt":"x"}`)
	startCall(m, "b", "task", `{"agent":"explore","description":"Task t2","prompt":"x"}`)
	settled := valid("t1", "a", explore.Completed, 2)
	settled.FinishedAt, settled.WaitMs, settled.ActiveMs = now, 2000, 3000
	settled.Tools = []explore.ToolRecord{{CallID: "r", Name: "read"}}
	taskEvent(m, valid("t1", "a", explore.Running, 1))
	taskEvent(m, settled)
	taskEvent(m, valid("t2", "b", explore.Running, 1))
	outcome, _ := json.Marshal(explore.Outcome{TaskID: "t1", Agent: "explore", Depth: 1, Status: explore.Completed, Findings: "found"})
	finishCall(m, done, tools.Result{Status: tools.Succeeded, Content: string(outcome)})
	// The process dies here: the second task is still running.
	saved, err := store.Load(m.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Tasks) != 2 {
		t.Fatalf("saved %d task records, want 2", len(saved.Tasks))
	}
	for _, e := range saved.Entries {
		if e.Role == "Agent" {
			t.Fatalf("an Agent entry was saved: %+v", e)
		}
	}
	// An older build saved Agent lines; they still load as notices.
	saved.Entries = append(saved.Entries, session.Entry{Role: "Agent", Content: "t0 · parent main · depth 1 · completed · Old task"})
	resumed := NewUI(root, nil, nil, "local", conn, "", store, saved)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	indices := toolEntryIndices(resumed)
	if len(indices) != 2 {
		t.Fatalf("resumed task items %v, want 2", indices)
	}
	if rows := itemRows(resumed, indices[0]); len(rows) != 3 || rows[1] != "  ⎿  Done · 1 tool use · wait 2s · active 3s" || rows[2] != "     found" {
		t.Fatalf("resumed settled task rows %q", rows)
	}
	if rows := itemRows(resumed, indices[1]); len(rows) != 2 || rows[1] != "  ⎿  Interrupted" {
		t.Fatalf("resumed live task rows %q", rows)
	}
	resumed.layoutWidth = 0
	resumed.rebuild()
	notice := false
	for _, line := range resumed.lines {
		if strings.HasPrefix(line, "Agent:") {
			t.Fatalf("legacy Agent label reached the layout: %q", line)
		}
		notice = notice || line == resumed.blocks.Notice+" t0 · parent main · depth 1 · completed · Old task"
	}
	if !notice {
		t.Fatalf("legacy Agent entry did not render as a notice: %q", resumed.lines)
	}
}
