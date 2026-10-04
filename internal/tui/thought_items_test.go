package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tools"
	"likha/internal/transcript"
)

// Reasoning blocks and turn footers (transcript-redesign § Reasoning, §
// Turn footer, § Persistence): a live block shows its last three rows,
// collapses to a timed marker that Tab can focus and Enter expands, and
// every finished user turn ends with a persisted footer.

// testClock is a hand-advanced UI clock.
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// thoughtTestUI is a working run inside a user turn that started at the
// pinned clock, on model nexum-router.
func thoughtTestUI(t *testing.T, width int) (*ui, *testClock) {
	t.Helper()
	m := toolItemTestUI(t, width)
	clock := &testClock{t: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	m.clock = clock.now
	m.modelName = "nexum-router"
	m.entries = append(m.entries, entry{role: "You", content: "think"})
	m.beginTurnFooter()
	return m, clock
}

func reasoningEvent(m *ui, text string) {
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "reasoning", Text: text})
}

func textEvent(m *ui, text string) {
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "text", Text: text})
}

func entriesWithRole(m *ui, role string) []int {
	var indices []int
	for i, e := range m.entries {
		if e.role == role {
			indices = append(indices, i)
		}
	}
	return indices
}

// lastFooter decodes the transcript's last Turn entry.
func lastFooter(t *testing.T, m *ui) (transcript.TurnInfo, int) {
	t.Helper()
	turns := entriesWithRole(m, transcript.TurnRole)
	if len(turns) == 0 {
		t.Fatalf("no Turn entry in %+v", m.entries)
	}
	at := turns[len(turns)-1]
	info, ok := transcript.DecodeTurn(m.entries[at].content)
	if !ok {
		t.Fatalf("Turn entry %q does not decode", m.entries[at].content)
	}
	return info, at
}

// Regression: a tool call closes the reasoning stream, so reasoning after
// the tool opens a new block below the tool item instead of appending to
// the block above it.
func TestReasoningStreamClosesOnToolCall(t *testing.T) {
	m, _ := thoughtTestUI(t, 80)
	reasoningEvent(m, "before the tool")
	call := startCall(m, "r", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "package a"})
	reasoningEvent(m, "after the tool")
	blocks := entriesWithRole(m, "Reasoning")
	if len(blocks) != 2 {
		t.Fatalf("reasoning blocks %v in %+v, want two", blocks, m.entries)
	}
	tool := toolEntryIndices(m)[0]
	if m.entries[blocks[0]].content != "before the tool" || m.entries[blocks[1]].content != "after the tool" || blocks[0] > tool || blocks[1] < tool {
		t.Fatalf("blocks %v around tool %d: %+v", blocks, tool, m.entries)
	}
	// The same holds when the tool starts but its result is still out.
	m, _ = thoughtTestUI(t, 80)
	reasoningEvent(m, "first")
	startCall(m, "r", "read", `{"path":"a.go"}`)
	reasoningEvent(m, "second")
	if blocks := entriesWithRole(m, "Reasoning"); len(blocks) != 2 || m.entries[blocks[1]].content != "second" {
		t.Fatalf("reasoning after tool_start appended to the old block: %+v", m.entries)
	}
}

// Regression: answer text closes the stream and empties its buffer, so the
// next round's reasoning does not start with the previous round's text.
func TestReasoningBufferResetsWhenTextCloses(t *testing.T) {
	m, _ := thoughtTestUI(t, 80)
	reasoningEvent(m, "round one")
	textEvent(m, "Let me read it.")
	call := startCall(m, "r", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "package a"})
	reasoningEvent(m, "round two")
	blocks := entriesWithRole(m, "Reasoning")
	if len(blocks) != 2 || m.entries[blocks[1]].content != "round two" {
		t.Fatalf("second block %q, want only this round's reasoning: %+v", m.entries[blocks[len(blocks)-1]].content, m.entries)
	}
}

// While live, the block shows `Thinking…` and the last three rows in Muted
// italics, with an ellipsis row once earlier rows are hidden; when it ends
// it collapses to the measured duration.
func TestLiveReasoningTailThenCollapse(t *testing.T) {
	m, clock := thoughtTestUI(t, 80)
	reasoningEvent(m, "line one\nline two")
	block := entriesWithRole(m, "Reasoning")[0]
	if got := itemRows(m, block); strings.Join(got, "|") != "✻ Thinking…|  line one|  line two" {
		t.Fatalf("short live block %q", got)
	}
	reasoningEvent(m, "\nline three\nline four\nline five")
	got := itemRows(m, block)
	// Exactly the last three rows (spec § Reasoning), no omission row.
	if strings.Join(got, "|") != "✻ Thinking…|  line three|  line four|  line five" {
		t.Fatalf("live tail %q, want header and the last 3 rows", got)
	}
	start := m.entryLines[block]
	if m.lineStyles[start].GetItalic() || !m.lineStyles[start+2].GetItalic() || colorName(m.lineStyles[start+2].GetForeground()) != colorName(m.theme.Muted.GetForeground()) {
		t.Fatalf("live rows not Muted italics under a plain header: %v", m.lineStyles[start:start+4])
	}
	if transcriptFocusHas(m, block) {
		t.Fatal("a live block is a focus stop")
	}
	if m.entries[block].content != "line one\nline two\nline three\nline four\nline five" {
		t.Fatalf("the entry lost reasoning text: %q", m.entries[block].content)
	}
	clock.advance(4200 * time.Millisecond)
	textEvent(m, "Answer.")
	if got := itemRows(m, block); strings.Join(got, "|") != "✻ Thought for 4s" {
		t.Fatalf("collapsed block %q", got)
	}
	// A block shorter than a second still says so.
	reasoningEvent(m, "ignored after the answer started")
	if len(entriesWithRole(m, "Reasoning")) != 1 {
		t.Fatal("reasoning after the answer opened a block")
	}
	call := startCall(m, "r", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "ok"})
	reasoningEvent(m, "quick")
	clock.advance(300 * time.Millisecond)
	endRun(m)
	second := entriesWithRole(m, "Reasoning")[1]
	if got := itemRows(m, second); strings.Join(got, "|") != "✻ Thought for <1s" {
		t.Fatalf("run end left the block %q", got)
	}
}

// Every event that ends a block stops its timer: steer delivery and an
// approval request included.
func TestReasoningTimerStopsAtSteerAndApproval(t *testing.T) {
	m, clock := thoughtTestUI(t, 80)
	reasoningEvent(m, "weighing")
	clock.advance(2 * time.Second)
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "steer", Text: "also check b.go", History: m.history})
	clock.advance(time.Minute) // later time never reaches the closed block
	reasoningEvent(m, "steered")
	clock.advance(65 * time.Second)
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "approval", Approval: &agent.ApprovalRequest{Kind: "command", Title: "Shell command", Body: "ls", Reply: make(chan bool, 1)}})
	m.pending = nil
	times := m.thoughtTimes()
	if len(times) != 2 || times[0] != (thoughtTime{2 * time.Second, true}) || times[1] != (thoughtTime{65 * time.Second, true}) {
		t.Fatalf("durations %+v, want 2s and 1m 5s", times)
	}
	if blocks := entriesWithRole(m, "Reasoning"); len(blocks) != 2 || m.entries[blocks[1]].content != "steered" {
		t.Fatalf("steer did not start a new block: %+v", m.entries)
	}
}

func transcriptFocusHas(m *ui, index int) bool {
	for _, stop := range m.transcriptFocusStops() {
		if stop == index {
			return true
		}
	}
	return false
}

// focusedThoughtTranscript builds a finished turn: reasoning, a tool item,
// reasoning, an answer, and the footer.
func focusedThoughtTranscript(t *testing.T) (*ui, []int, int) {
	t.Helper()
	m, clock := thoughtTestUI(t, 80)
	reasoningEvent(m, "first thought, long enough to wrap")
	clock.advance(3 * time.Second)
	call := startCall(m, "r", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "package a"})
	reasoningEvent(m, "second thought")
	clock.advance(9 * time.Second)
	textEvent(m, "Done.")
	endRun(m)
	return m, entriesWithRole(m, "Reasoning"), toolEntryIndices(m)[0]
}

// Tab visits tool items and collapsed markers in transcript order; Enter
// on a marker toggles it inline and leaves the composer alone; Enter on a
// tool still opens the inspector.
func TestThoughtMarkersTakeFocusAndToggle(t *testing.T) {
	m, blocks, tool := focusedThoughtTranscript(t)
	m.input = []rune("draft")
	if stops := m.transcriptFocusStops(); fmt.Sprint(stops) != fmt.Sprint([]int{blocks[0], tool, blocks[1]}) {
		t.Fatalf("focus stops %v, want %v", stops, []int{blocks[0], tool, blocks[1]})
	}
	tab := func(key tea.KeyType) {
		m.Update(tea.KeyMsg{Type: key})
	}
	tab(tea.KeyTab)
	if index, ok := m.focusedThoughtEntry(); !ok || index != blocks[0] {
		t.Fatalf("first Tab focused %d (ok %t), want marker %d", index, ok, blocks[0])
	}
	if got := itemRows(m, blocks[0]); got[0] != "› Thought for 3s" {
		t.Fatalf("focused marker row %q", got)
	}
	if help := m.toolInspectionHelp(); !strings.Contains(help, "Enter expand/collapse") {
		t.Fatalf("help %q", help)
	}
	tab(tea.KeyTab)
	if index, ok := m.focusedToolEntry(); !ok || index != tool {
		t.Fatalf("second Tab focused %d, want tool %d", index, tool)
	}
	if _, ok := m.focusedThoughtEntry(); ok {
		t.Fatal("tool focus left a marker focused")
	}
	tab(tea.KeyTab)
	if index, ok := m.focusedThoughtEntry(); !ok || index != blocks[1] {
		t.Fatalf("third Tab focused %d, want marker %d", index, blocks[1])
	}
	tab(tea.KeyShiftTab)
	tab(tea.KeyShiftTab)
	if index, ok := m.focusedThoughtEntry(); !ok || index != blocks[0] {
		t.Fatalf("Shift+Tab back focused %d, want marker %d", index, blocks[0])
	}

	entries := len(m.entries)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expanded := itemRows(m, blocks[0])
	if strings.Join(expanded, "|") != "› Thought for 3s|  first thought, long enough to wrap" {
		t.Fatalf("expanded rows %q", expanded)
	}
	// The body renders as markdown (§ Markdown) whose prose runs are
	// Muted italics.
	start := m.entryLines[blocks[0]]
	if run, ok := runOver(m, start+1, 2); !ok || !run.style.GetItalic() || colorName(run.style.GetForeground()) != colorName(m.theme.Muted.GetForeground()) {
		t.Fatalf("expanded body not Muted italics: %+v ok=%t", run, ok)
	}
	if string(m.input) != "draft" || len(m.entries) != entries || m.working || m.toolInspector.open {
		t.Fatalf("Enter on a marker reached the composer or inspector: input %q entries %d working %t", string(m.input), len(m.entries), m.working)
	}
	// Expansion survives resizes and rebuilds; only the toggled block opens.
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	if got := itemRows(m, blocks[0]); len(got) != 3 || got[1] != "  first thought, long enough" || got[2] != "  to wrap" {
		t.Fatalf("expanded rows after resize %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := itemRows(m, blocks[1]); len(got) != 1 {
		t.Fatalf("untoggled block expanded: %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := itemRows(m, blocks[0]); len(got) != 1 {
		t.Fatalf("second Enter left the block expanded: %q", got)
	}
	// Enter on a focused tool is unchanged: it opens the inspector.
	tab(tea.KeyTab)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.toolInspector.open || m.toolInspector.kind != toolInspectionResult {
		t.Fatal("Enter on a focused tool did not open the inspector")
	}
}

// Expansion follows the block, not its entry index, when per-run rows
// before it disappear, and a session switch drops it.
func TestThoughtExpansionFollowsBlockAndResetsOnSessionSwitch(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	other.Entries = []session.Entry{{Role: "You", Content: "hi"}, {Role: "Reasoning", Content: "old"}}
	if err := store.Save(other); err != nil {
		t.Fatal(err)
	}
	current, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, nil, nil, "", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), store, current)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.entries = append(m.entries, entry{role: "Queued", content: "held"}, entry{role: "Reasoning", content: "kept open"})
	m.queue = []string{"held"}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	before := entriesWithRole(m, "Reasoning")[0]
	// The first Esc releases focus; a bare Esc then drops the held queue
	// and its row, shifting the block up.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(escDecayMsg{})
	block := entriesWithRole(m, "Reasoning")[0]
	if block != before-1 || len(entriesWithRole(m, "Queued")) != 0 {
		t.Fatalf("queue row not dropped: block %d (was %d), entries %+v", block, before, m.entries)
	}
	if got := itemRows(m, block); len(got) != 2 || got[1] != "  kept open" {
		t.Fatalf("expansion lost when the queue row went: %q (entries %+v)", got, m.entries)
	}
	m.resumeSession(other.ID)
	if m.thoughtExpanded != nil || m.toolInspector.focused {
		t.Fatalf("session switch kept expansion %v / focus", m.thoughtExpanded)
	}
	if got := itemRows(m, entriesWithRole(m, "Reasoning")[0]); strings.Join(got, "|") != "✻ Thought" {
		t.Fatalf("resumed legacy block %q, want the unknown-duration marker", got)
	}
}

// Every finished user turn ends with a footer: done, cancelled, and
// failed; the run's notice comes first.
func TestTurnFooterAfterDoneCancelledAndError(t *testing.T) {
	m, clock := thoughtTestUI(t, 80)
	reasoningEvent(m, "plan")
	clock.advance(4200 * time.Millisecond)
	for _, id := range []string{"a", "b"} {
		call := startCall(m, id, "read", `{"path":"a.go"}`)
		finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "ok"})
	}
	textEvent(m, "Done.")
	clock.advance(8100 * time.Millisecond)
	endRun(m)
	info, at := lastFooter(t, m)
	if at != len(m.entries)-1 || info.Model != "nexum-router" || info.Duration != 12300*time.Millisecond || info.Tools != 2 || info.Cancelled || len(info.Thoughts) != 1 || info.Thoughts[0] != 4200*time.Millisecond {
		t.Fatalf("done footer %+v at %d of %d", info, at, len(m.entries))
	}
	rows := itemRows(m, at)
	if len(rows) != 1 || rows[0] != "  ✻ nexum-router · 12.3s · 2 tools" {
		t.Fatalf("footer rows %q", rows)
	}
	if style := m.lineStyles[m.entryLines[at]]; colorName(style.GetForeground()) != colorName(m.theme.Muted.GetForeground()) {
		t.Fatalf("footer not Muted: %v", style)
	}

	// Cancelled: the notice, then a footer that says so.
	m, clock = thoughtTestUI(t, 80)
	startCall(m, "c", "read", `{"path":"a.go"}`)
	m.cancelling = true
	clock.advance(time.Second)
	endRun(m)
	info, at = lastFooter(t, m)
	if !info.Cancelled || info.Tools != 1 || m.entries[at-1].role != "Likha" {
		t.Fatalf("cancelled footer %+v after %+v", info, m.entries[at-1])
	}
	if rows := itemRows(m, at); rows[0] != "  ✻ nexum-router · 1.0s · 1 tool · cancelled" {
		t.Fatalf("cancelled footer row %q", rows)
	}

	// Failed: the error, then a footer (spec: each finished turn).
	m, clock = thoughtTestUI(t, 80)
	call := startCall(m, "e", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "ok"})
	clock.advance(500 * time.Millisecond)
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "error", Text: "provider unavailable", History: m.history})
	info, at = lastFooter(t, m)
	if info.Tools != 1 || info.Cancelled || m.entries[at-1].role != "Error" {
		t.Fatalf("error footer %+v after %+v", info, m.entries[at-1])
	}
	if rows := itemRows(m, at); rows[0] != "  ✻ nexum-router · 0.5s · 1 tool" {
		t.Fatalf("error footer row %q", rows)
	}
	// One turn, one footer: a second terminal event changes nothing.
	m.working = true
	endRun(m)
	if n := len(entriesWithRole(m, transcript.TurnRole)); n != 1 {
		t.Fatalf("%d footers for one turn", n)
	}
}

// N tools counts the turn's top-level tool items, not distinct call IDs: a
// provider may reuse an ID across rounds (only one round rejects
// duplicates), and every round's call is its own item. A result whose
// start never arrived and a call a cancelled run never executed are items
// too; the next turn starts from zero.
func TestTurnFooterCountsToolItemsNotCallIDs(t *testing.T) {
	m, _ := thoughtTestUI(t, 80)
	for round := 0; round < 3; round++ {
		call := startCall(m, "call_0", "read", `{"path":"a.go"}`)
		finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "ok"})
	}
	finishCall(m, model.ToolCall{ID: "call_0", Name: "grep", Arguments: `{"pattern":"x"}`}, tools.Result{Status: tools.Succeeded, Content: "no matches"})
	m.cancelling = true
	unexecuted := model.ToolCall{ID: "call_9", Name: "read", Arguments: `{"path":"b.go"}`}
	history := append(append([]model.Message(nil), m.history...),
		model.Message{Role: "assistant", ToolCalls: []model.ToolCall{unexecuted}},
		model.Message{Role: "tool", ToolCallID: "call_9", Content: unexecutedToolContent})
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "done", History: history})
	items := len(toolEntryIndices(m))
	info, at := lastFooter(t, m)
	if items != 5 || info.Tools != items {
		t.Fatalf("footer counts %d tools for %d items", info.Tools, items)
	}
	if rows := itemRows(m, at); !strings.HasSuffix(rows[0], " · 5 tools · cancelled") {
		t.Fatalf("footer row %q", rows)
	}

	// The next turn counts only its own items.
	m.cancelling = false
	m.working = true
	m.beginTurnFooter()
	call := startCall(m, "call_0", "read", `{"path":"a.go"}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: "ok"})
	endRun(m)
	if info, _ := lastFooter(t, m); info.Tools != 1 {
		t.Fatalf("second turn footer counts %d tools, want 1", info.Tools)
	}
}

// At narrow widths the footer drops segments right to left and never
// overflows; ASCII mode uses its own glyphs; nothing escapes the viewport.
func TestTurnFooterAndThoughtsNarrow(t *testing.T) {
	info := transcript.TurnInfo{Model: "a-very-long-provider-model-name-that-will-not-fit", Duration: 64 * time.Second, Tools: 12, Cancelled: true}
	for _, ascii := range []bool{false, true} {
		m := blocksTestUI(t, ascii, 40)
		m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
		m.thoughtExpanded = map[int]bool{1: true}
		starts := layoutEntries(m,
			entry{role: "Reasoning", content: strings.Repeat("词", 50)},
			entry{role: "Reasoning", content: strings.Repeat("x", 120) + " tab\there esc\x1b[31m"},
			entry{role: transcript.TurnRole, content: transcript.EncodeTurn(info)},
			entry{role: transcript.TurnRole, content: transcript.EncodeTurn(transcript.TurnInfo{Model: "m", Duration: 3 * time.Second, Tools: 4})},
		)
		g := m.blocks
		for i, line := range m.lines {
			if w := runewidth.StringWidth(line); w > contentWidth(40) {
				t.Fatalf("ascii=%t row %d is %d cells: %q", ascii, i, w, line)
			}
			if strings.ContainsRune(line, '\x1b') {
				t.Fatalf("raw escape reached row %q", line)
			}
		}
		if got := m.lines[starts[2]]; !strings.HasPrefix(got, "  "+g.Thought+" a-very-long") || !strings.HasSuffix(got, g.Ellipsis) {
			t.Fatalf("ascii=%t narrow footer %q, want the model clipped alone", ascii, got)
		}
		if got := m.lines[starts[3]]; got != "  "+g.Thought+" m · 3.0s · 4 tools" {
			t.Fatalf("ascii=%t short footer %q", ascii, got)
		}
		if got := m.lines[starts[0]]; got != g.Thought+" Thought" {
			t.Fatalf("ascii=%t marker %q", ascii, got)
		}
		for page := 0; page*m.bodyHeight() < len(m.lines); page++ {
			m.scroll, m.following = page*m.bodyHeight(), false
			assertViewport(t, m.View(), 40, 12)
		}
	}
	// The live tail fits narrow terminals too.
	m, _ := thoughtTestUI(t, 40)
	m.blocks.Ellipsis = "..."
	reasoningEvent(m, strings.Repeat("漢字 ", 80)+"\n"+strings.Repeat("y", 200))
	rows := itemRows(m, entriesWithRole(m, "Reasoning")[0])
	if len(rows) != 4 || rows[0] != "✻ Thinking..." || rows[1] == "  ..." || !strings.HasPrefix(rows[3], "  yyy") {
		t.Fatalf("narrow live rows %q", rows)
	}
	for _, row := range rows {
		if runewidth.StringWidth(row) > contentWidth(40) {
			t.Fatalf("live row %q overflows", row)
		}
	}
}

// Footers and reasoning durations survive resume; a footer that does not
// decode renders nothing; reasoning from a run without a footer reads
// "Thought"; tool records keep their entries around the footers.
func TestTurnFooterAndThoughtsSurviveResume(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	conn := providers.Connection{Provider: "OpenAI", Verified: true}
	m := NewUI(root, nil, nil, "nexum-router", conn, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	clock := &testClock{t: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	m.clock = clock.now
	m.events = make(chan agent.TurnEvent, 64)
	turn := func(prompt string, thoughts []time.Duration, callID string) {
		m.working, m.runID, m.reasoningStream = true, m.runID+1, -1
		m.entries = append(m.entries, entry{role: "You", content: prompt})
		m.beginTurnFooter()
		for i, d := range thoughts {
			reasoningEvent(m, fmt.Sprintf("%s thought %d", prompt, i))
			clock.advance(d)
			call := startCall(m, fmt.Sprintf("%s-%d", callID, i), "read", `{"path":"a.go"}`)
			finishCall(m, call, tools.Result{Status: tools.Succeeded, Content: prompt + " result"})
		}
		textEvent(m, prompt+" answer")
		clock.advance(time.Second)
		endRun(m)
	}
	turn("one", []time.Duration{3 * time.Second, 75 * time.Second}, "a")
	// A run that ended without a footer (as a crash would leave it).
	m.entries = append(m.entries, entry{role: "You", content: "crashed"}, entry{role: "Reasoning", content: "lost timing"})
	turn("two", []time.Duration{1500 * time.Millisecond}, "b")
	m.entries = append(m.entries, entry{role: transcript.TurnRole, content: "{not json"})
	m.persist()

	saved, err := store.Load(m.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(fmt.Sprint(saved.Entries), "thoughts_ms"); n != 2 {
		t.Fatalf("saved %d footers with durations, want 2: %+v", n, saved.Entries)
	}
	resumed := NewUI(root, nil, nil, "other-model", conn, "", store, saved)
	resumed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var markers []string
	for _, index := range entriesWithRole(resumed, "Reasoning") {
		markers = append(markers, itemRows(resumed, index)[0])
	}
	if strings.Join(markers, "|") != "✻ Thought for 3s|✻ Thought for 1m 15s|✻ Thought|✻ Thought for 1s" {
		t.Fatalf("resumed markers %q", markers)
	}
	var footers []string
	turns := entriesWithRole(resumed, transcript.TurnRole)
	resumed.layoutWidth = 0
	resumed.rebuild()
	for _, index := range turns[:2] {
		footers = append(footers, resumed.lines[resumed.entryLines[index]])
	}
	if strings.Join(footers, "|") != "  ✻ nexum-router · 1m 19s · 2 tools|  ✻ nexum-router · 2.5s · 1 tool" {
		t.Fatalf("resumed footers %q", footers)
	}
	// The undecodable footer is last and renders nothing, separator included.
	if last := turns[2]; resumed.entryLines[last] != len(resumed.lines) {
		t.Fatalf("undecodable footer laid out rows: line %d of %d", resumed.entryLines[last], len(resumed.lines))
	}
	// Tool records still find their items around the footers.
	for _, index := range toolEntryIndices(resumed) {
		record, ok := resumed.toolRecordAt(index)
		if !ok || record.Status != string(tools.Succeeded) {
			t.Fatalf("tool item %d lost its record after resume: %+v", index, record)
		}
	}
	if got := len(toolEntryIndices(resumed)); got != 3 {
		t.Fatalf("%d tool items after resume, want 3", got)
	}
}

// A footer closes open legacy requests: a result after it is not folded
// into a request from before it.
func TestTurnFooterClosesLegacyRequests(t *testing.T) {
	m := blocksTestUI(t, false, 80)
	layoutEntries(m,
		entry{role: "Tool", content: "Request: read a.go"},
		entry{role: transcript.TurnRole, content: transcript.EncodeTurn(transcript.TurnInfo{Model: "m"})},
		entry{role: "Tool", content: "read: package a"},
	)
	if plan := m.toolItemPlan(); plan.absorbed[2] {
		t.Fatal("a result after a footer folded into the request before it")
	}
}

// End to end: a real turn gets a footer and its reasoning a duration, the
// footer never reaches the model on the next turn, and compaction (ending
// well or failing) adds no footer.
func TestTurnFooterEndToEndNeverReachesTheModel(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		mu.Lock()
		bodies = append(bodies, string(raw))
		failing := fail
		mu.Unlock()
		if failing {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"weighing it\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "nexum-router", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "nexum-router", providers.Connection{Provider: "OpenAI", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	submit := func(text string) {
		sendRunes(m, text)
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		permE2Epump(t, m, func() bool { return !m.working })
	}
	submit("first question")
	info, at := lastFooter(t, m)
	if info.Model != "nexum-router" || info.Tools != 0 || len(info.Thoughts) != 1 || at != len(m.entries)-1 {
		t.Fatalf("e2e footer %+v at %d of %d", info, at, len(m.entries))
	}
	if got := itemRows(m, entriesWithRole(m, "Reasoning")[0]); len(got) != 1 || !strings.HasPrefix(got[0], "✻ Thought for ") {
		t.Fatalf("e2e reasoning rows %q", got)
	}
	submit("second question")
	if n := len(entriesWithRole(m, transcript.TurnRole)); n != 2 {
		t.Fatalf("%d footers after two turns", n)
	}
	mu.Lock()
	second := bodies[len(bodies)-1]
	mu.Unlock()
	if !strings.Contains(second, "first question") || !strings.Contains(second, "Answer.") {
		t.Fatalf("second request lacks the first turn: %s", second)
	}
	for _, leak := range []string{"thoughts_ms", `\"v\":1`, transcript.TurnRole + `"`} {
		if strings.Contains(second, leak) {
			t.Fatalf("footer content %q reached the model: %s", leak, second)
		}
	}
	for _, msg := range m.history {
		if strings.Contains(msg.Content, "thoughts_ms") || msg.Role == transcript.TurnRole {
			t.Fatalf("footer reached history: %+v", msg)
		}
	}

	footers := len(entriesWithRole(m, transcript.TurnRole))
	submit("/compact")
	if last := m.entries[len(m.entries)-1]; last.role != "Likha" || !strings.Contains(last.content, "Conversation compacted") {
		t.Fatalf("compaction did not finish: %+v", last)
	}
	mu.Lock()
	fail = true
	mu.Unlock()
	submit("/compact")
	if last := m.entries[len(m.entries)-1]; last.role != "Error" {
		t.Fatalf("failed compaction ended with %+v", last)
	}
	if n := len(entriesWithRole(m, transcript.TurnRole)); n != footers {
		t.Fatalf("compaction added footers: %d, want %d", n, footers)
	}
}
