package transcript

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/tools"
)

var taskTestAccepted = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func taskTestTool(name, args string) explore.ToolRecord {
	return explore.ToolRecord{CallID: "c-" + name, Name: name, Arguments: args, Result: tools.Result{Status: tools.Succeeded}}
}

func taskTestRunning(toolRecords ...explore.ToolRecord) explore.Record {
	return explore.Record{ID: "t1", Status: explore.Running, AcceptedAt: taskTestAccepted, Tools: toolRecords}
}

func TestIsLive(t *testing.T) {
	for status, want := range map[explore.State]bool{
		explore.Queued: true, explore.Running: true, explore.Waiting: true,
		explore.Completed: false, explore.Failed: false, explore.Cancelled: false,
		explore.Limited: false, explore.Interrupted: false, "": false, "bogus": false,
	} {
		if got := IsLive(status); got != want {
			t.Errorf("IsLive(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestSettleCompleted(t *testing.T) {
	finished := taskTestAccepted.Add(2 * time.Minute)
	three := []explore.ToolRecord{taskTestTool("read", `{}`), taskTestTool("grep", `{}`), taskTestTool("glob", `{}`)}
	cases := []struct {
		name string
		rec  explore.Record
		want string
	}{
		{"plural with timing", explore.Record{Status: explore.Completed, Tools: three, WaitMs: 64_400, ActiveMs: 3_999, AcceptedAt: taskTestAccepted, FinishedAt: finished},
			"Done · 3 tool uses · wait 1m 4s · active 3s"},
		{"singular", explore.Record{Status: explore.Completed, Tools: three[:1], WaitMs: 1_000, ActiveMs: 3_723_000, AcceptedAt: taskTestAccepted, FinishedAt: finished},
			"Done · 1 tool use · wait 1s · active 1h 2m"},
		{"zero tools", explore.Record{Status: explore.Completed, WaitMs: 500, ActiveMs: 2_000, AcceptedAt: taskTestAccepted, FinishedAt: finished},
			"Done · 0 tool uses · wait 0s · active 2s"},
		// Saved before wait/active accounting: zero totals over a 2m span.
		{"legacy totals omitted", explore.Record{Status: explore.Completed, Tools: three, AcceptedAt: taskTestAccepted, FinishedAt: finished},
			"Done · 3 tool uses"},
		{"no timestamps omitted", explore.Record{Status: explore.Completed, Tools: three[:1]},
			"Done · 1 tool use"},
		{"settled without finish time omitted", explore.Record{Status: explore.Completed, Tools: three[:1], AcceptedAt: taskTestAccepted},
			"Done · 1 tool use"},
		// Genuinely zero: settled within a second of acceptance.
		{"sub-second task keeps zeros", explore.Record{Status: explore.Completed, AcceptedAt: taskTestAccepted, FinishedAt: taskTestAccepted.Add(400 * time.Millisecond)},
			"Done · 0 tool uses · wait 0s · active 0s"},
		{"negative totals clamp", explore.Record{Status: explore.Completed, WaitMs: -5, ActiveMs: 1_500},
			"Done · 0 tool uses · wait 0s · active 1s"},
	}
	for _, c := range cases {
		text, ok := Settle(c.rec)
		if text != c.want || !ok {
			t.Errorf("%s: Settle = (%q, %v), want (%q, true)", c.name, text, ok, c.want)
		}
	}
}

func TestSettleFailureOutcomes(t *testing.T) {
	cases := []struct {
		status explore.State
		reason string
		want   string
	}{
		{explore.Failed, "provider returned 500", "Failed: provider returned 500"},
		{explore.Failed, "", "Failed"},
		{explore.Failed, "line one\n\tline two  ", "Failed: line one line two"},
		{explore.Failed, "bad\x1b[31mred", `Failed: bad\u001B[31mred`},
		{explore.Cancelled, "", "Cancelled"},
		{explore.Cancelled, "explore task cancelled", "Cancelled"},
		{explore.Cancelled, "explore run cancelled", "Cancelled"},
		{explore.Cancelled, "context canceled", "Cancelled"},
		{explore.Cancelled, "branch cancelled from /agents", "Cancelled: branch cancelled from /agents"},
		{explore.Limited, "Explore model-request limit reached (32); available findings are partial. No additional summarization request was made.", "Limited: model-request limit"},
		{explore.Limited, "explore task reached its 32-model-request limit", "Limited: model-request limit"},
		{explore.Limited, "Model-request limit reached (32): stream reset. Available findings are partial.", "Limited: model-request limit"},
		{explore.Limited, "explore task deadline exceeded", "Limited: deadline exceeded"},
		{explore.Limited, "context deadline exceeded", "Limited: deadline exceeded"},
		{explore.Limited, "explore task reached a runtime limit; findings may be partial", "Limited: runtime limit"},
		{explore.Limited, "", "Limited"},
		{explore.Limited, "spawn budget spent", "Limited: spawn budget spent"},
		{explore.Interrupted, "explore task interrupted; no work was resumed", "Interrupted"},
		{explore.Interrupted, "", "Interrupted"},
	}
	for _, c := range cases {
		text, ok := Settle(explore.Record{Status: c.status, Reason: c.reason, Tools: []explore.ToolRecord{taskTestTool("read", `{}`)}})
		if text != c.want || ok {
			t.Errorf("Settle(%s, %q) = (%q, %v), want (%q, false)", c.status, c.reason, text, ok, c.want)
		}
	}
}

func TestSettleHasNoOutcomeForLiveOrUnknownStates(t *testing.T) {
	for _, status := range []explore.State{explore.Queued, explore.Running, explore.Waiting, "", "paused"} {
		if text, ok := Settle(explore.Record{Status: status, Reason: "x"}); text != "" || ok {
			t.Errorf("Settle(%q) = (%q, %v), want empty and false", status, text, ok)
		}
	}
}

func TestLiveLineToolVocabulary(t *testing.T) {
	cases := []struct {
		name string
		tool explore.ToolRecord
		want string
	}{
		{"read file", taskTestTool("read", `{"path":"src/app/page.tsx"}`), "Read src/app/page.tsx"},
		{"read range", taskTestTool("read", `{"path":"main.go","offset":120,"limit":61}`), "Read main.go:120-180"},
		{"read default limit", taskTestTool("read", `{"path":"main.go","offset":10}`), "Read main.go:10-209"},
		{"read dot", taskTestTool("read", `{"path":"."}`), "List ."},
		{"read no path", taskTestTool("read", `{}`), "List ."},
		{"read trailing slash", taskTestTool("read", `{"path":"src/"}`), "List src/"},
		{"read directory result", explore.ToolRecord{Name: "read", Arguments: `{"path":"internal"}`, Result: tools.Result{Content: "read directory internal\nexplore/\ntools/"}}, "List internal"},
		{"grep with path", taskTestTool("grep", `{"pattern":"LAYOUT","path":"src","literal":true}`), "Grep LAYOUT in src"},
		{"grep without path", taskTestTool("grep", `{"pattern":"func main"}`), "Grep func main"},
		{"glob", taskTestTool("glob", `{"pattern":"**/*.go","path":"internal"}`), "Glob **/*.go in internal"},
		{"edit one file", taskTestTool("edit", `{"operations":[{"kind":"replace","path":"a.go","old_text":"x","new_text":"y"},{"kind":"replace","path":"a.go","old_text":"p","new_text":"q"}]}`), "Update a.go"},
		{"edit many files", taskTestTool("edit", `{"operations":[{"kind":"replace","path":"a.go"},{"kind":"create","path":"b.go"},{"kind":"replace","path":"c.go"}]}`), "Update 3 files"},
		{"edit create", taskTestTool("edit", `{"operations":[{"kind":"create","path":"new.go","content":"package x"}]}`), "Create new.go"},
		{"edit no operations", taskTestTool("edit", `{"operations":[]}`), "Update"},
		{"edit_file", taskTestTool("edit_file", `{"path":"x.md"}`), "Update x.md"},
		{"run", taskTestTool("run_command", `{"command":"go test ./..."}`), "Run go test ./..."},
		{"fetch", taskTestTool("web_fetch", `{"url":"https://example.com/a"}`), "Fetch https://example.com/a"},
		{"search", taskTestTool("web_search", `{"query":"go 1.24 release","limit":3}`), `Search "go 1.24 release"`},
		{"task", taskTestTool("task", `{"agent":"explore","description":"Map routes","prompt":"..."}`), "Task Map routes"},
		{"task agent fallback", taskTestTool("task", `{"agent":"explore"}`), "Task explore"},
		{"ask", taskTestTool("ask_user", `{"question":"Which DB?"}`), "Ask Which DB?"},
		{"ask questionnaire", taskTestTool("ask_user", `{"questions":[{"question":"Which DB?"},{"question":"Which port?"}]}`), "Ask 2 questions"},
		{"ask questionnaire one", taskTestTool("ask_user", `{"questions":[{"question":"Which DB?","options":[{"label":"Postgres","recommended":true}]}]}`), "Ask Which DB?"},
		{"plan", taskTestTool("plan_update", `{"steps":[{"text":"a"},{"text":"b"}]}`), "Plan 2 steps"},
		{"plan one", taskTestTool("plan_update", `{"steps":[{"text":"a"}]}`), "Plan 1 step"},
		{"skill", taskTestTool("skill", `{"name":"deploy"}`), "Skill deploy"},
		{"mcp source", explore.ToolRecord{Name: "mcp__gh__search__ab12", Arguments: `{"q":"bug"}`, Result: tools.Result{Source: tools.Source{Kind: "mcp", Server: "github", Tool: "search_issues"}}}, "github.search_issues q: bug"},
		{"mcp qualified name only", taskTestTool("mcp__linear__list_issues__9f", `{}`), "linear.list_issues"},
		{"other tool", taskTestTool("frobnicate", `{"z":1,"a":"two"}`), "frobnicate a: two, z: 1"},
		{"other tool no args", taskTestTool("frobnicate", ``), "frobnicate"},
	}
	for _, c := range cases {
		if got := LiveLine(taskTestRunning(taskTestTool("glob", `{"pattern":"old"}`), c.tool), nil); got != c.want {
			t.Errorf("%s: LiveLine = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLiveLineMalformedArguments(t *testing.T) {
	cases := []struct {
		tool explore.ToolRecord
		want string
	}{
		{taskTestTool("read", `{"path":`), "Read"},
		{taskTestTool("read", `["src/app.go"]`), "Read"},
		{taskTestTool("grep", `not json`), "Grep"},
		{taskTestTool("read", `{"path":42}`), "Read"},
		{taskTestTool("edit", `{"operations":"a.go"}`), "Update"},
		{taskTestTool("plan_update", `{"steps":3}`), "Plan"},
		{taskTestTool("run_command", `{"command":null}`), "Run"},
		{taskTestTool("frobnicate", `{oops`), "frobnicate"},
		{taskTestTool("", `{}`), "tool"},
	}
	for _, c := range cases {
		if got := LiveLine(taskTestRunning(c.tool), nil); got != c.want {
			t.Errorf("LiveLine(%s %s) = %q, want %q", c.tool.Name, c.tool.Arguments, got, c.want)
		}
	}
}

func TestLiveLineFlattensUntrustedText(t *testing.T) {
	got := LiveLine(taskTestRunning(taskTestTool("run_command", `{"command":"echo a\n\tb\u001b[2J​c"}`)), nil)
	if want := "Run echo a b\\u001B[2J\\u200Bc"; got != want {
		t.Fatalf("LiveLine = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\n\t\x1b") {
		t.Fatalf("LiveLine kept a control rune: %q", got)
	}
}

func TestLiveLineQueuedAndThinking(t *testing.T) {
	if got := LiveLine(explore.Record{ID: "t1", Status: explore.Queued}, nil); got != "Queued" {
		t.Errorf("queued before activity = %q, want Queued", got)
	}
	if got := LiveLine(explore.Record{ID: "t1", Status: explore.Running}, nil); got != "Thinking" {
		t.Errorf("running with no tool yet = %q, want Thinking", got)
	}
	// A streaming partial assistant message carries no calls.
	streaming := explore.Record{ID: "t1", Status: explore.Running, History: []model.Message{
		{Role: "user", Content: "brief"}, {Role: "assistant", Content: "Let me look"},
	}}
	if got := LiveLine(streaming, nil); got != "Thinking" {
		t.Errorf("streaming first answer = %q, want Thinking", got)
	}
	// The loop re-queues for its permit every round; the latest activity
	// stays on the line instead of flickering to Queued.
	requeued := taskTestRunning(taskTestTool("read", `{"path":"go.mod"}`))
	requeued.Status = explore.Queued
	if got := LiveLine(requeued, nil); got != "Read go.mod" {
		t.Errorf("re-queued after a tool = %q, want Read go.mod", got)
	}
}

func TestLiveLinePrefersCallInFlight(t *testing.T) {
	rec := taskTestRunning(explore.ToolRecord{CallID: "c1", Name: "read", Arguments: `{"path":"a.go"}`})
	rec.History = []model.Message{
		{Role: "user", Content: "brief"},
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "read", Arguments: `{"path":"a.go"}`},
			{ID: "c2", Name: "grep", Arguments: `{"pattern":"TODO","path":"internal"}`},
			{ID: "c3", Name: "glob", Arguments: `{"pattern":"*.md"}`},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "..."},
	}
	if got := LiveLine(rec, nil); got != "Grep TODO in internal" {
		t.Fatalf("in-flight call = %q, want Grep TODO in internal", got)
	}
	// Every call answered: the last finished call names the activity.
	rec.History = append(rec.History, model.Message{Role: "tool", ToolCallID: "c2"}, model.Message{Role: "tool", ToolCallID: "c3"})
	rec.Tools = append(rec.Tools, explore.ToolRecord{CallID: "c2", Name: "grep", Arguments: `{"pattern":"TODO"}`}, explore.ToolRecord{CallID: "c3", Name: "glob", Arguments: `{"pattern":"*.md"}`})
	if got := LiveLine(rec, nil); got != "Glob *.md" {
		t.Fatalf("after the batch = %q, want Glob *.md", got)
	}
	// An earlier round's call IDs do not mark a reused ID as answered.
	rec.History = append(rec.History, model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"b.go"}`}}})
	if got := LiveLine(rec, nil); got != "Read b.go" {
		t.Fatalf("reused call ID in a new round = %q, want Read b.go", got)
	}
}

func TestLiveLineWaitingForChildren(t *testing.T) {
	parent := explore.Record{ID: "t1", Status: explore.Waiting}
	child := func(id, callID string, status explore.State, version uint64) explore.Record {
		return explore.Record{ID: id, ParentID: "t1", ParentCallID: callID, Status: status, Version: version}
	}
	cases := []struct {
		name     string
		children []explore.Record
		want     string
	}{
		{"two live", []explore.Record{child("a", "k1", explore.Running, 1), child("b", "k2", explore.Queued, 1), child("c", "k0", explore.Completed, 3)}, "Waiting for 2 subtasks"},
		{"one live", []explore.Record{child("a", "k1", explore.Waiting, 1), child("b", "k2", explore.Failed, 2)}, "Waiting for 1 subtask"},
		{"stale live version ignored", []explore.Record{child("a", "k1", explore.Completed, 5), child("a", "k1", explore.Running, 4), child("b", "k2", explore.Running, 2)}, "Waiting for 1 subtask"},
		{"other parents ignored", []explore.Record{{ID: "x", ParentID: "t9", Status: explore.Running}, {ID: "t1", Status: explore.Running}, child("a", "k1", explore.Running, 1)}, "Waiting for 1 subtask"},
		{"none known", nil, "Waiting for subtasks"},
		{"all settled", []explore.Record{child("a", "k1", explore.Completed, 2)}, "Waiting for subtasks"},
	}
	for _, c := range cases {
		if got := LiveLine(parent, c.children); got != c.want {
			t.Errorf("%s: LiveLine = %q, want %q", c.name, got, c.want)
		}
	}

	// Dispatched task calls count until their child records appear, so the
	// line is right before the runtime marks the parent waiting.
	dispatched := explore.Record{ID: "t1", Status: explore.Running, History: []model.Message{
		{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "k1", Name: "task", Arguments: `{"description":"a"}`},
			{ID: "k2", Name: "task", Arguments: `{"description":"b"}`},
			{ID: "k3", Name: "task", Arguments: `{"description":"c"}`},
		}},
	}}
	if got := LiveLine(dispatched, nil); got != "Waiting for 3 subtasks" {
		t.Errorf("dispatched without records = %q, want 3", got)
	}
	children := []explore.Record{child("a", "k1", explore.Running, 1), child("b", "k2", explore.Completed, 2)}
	if got := LiveLine(dispatched, children); got != "Waiting for 2 subtasks" {
		t.Errorf("dispatched with records = %q, want 2 (a live, k3 unspawned)", got)
	}
}

func TestLiveLineSettledRecordUsesSettleText(t *testing.T) {
	rec := explore.Record{Status: explore.Failed, Reason: "boom", Tools: []explore.ToolRecord{taskTestTool("read", `{"path":"a"}`)}}
	if got := LiveLine(rec, nil); got != "Failed: boom" {
		t.Fatalf("LiveLine(failed) = %q, want Failed: boom", got)
	}
}

// TestTaskLifecycleLines drives one record through the states the runtime
// publishes and checks the single ⎿ line at each step.
func TestTaskLifecycleLines(t *testing.T) {
	rec := explore.Record{ID: "t1", ParentCallID: "call-1", Status: explore.Queued, AcceptedAt: taskTestAccepted}
	steps := []struct {
		mutate   func()
		children []explore.Record
		want     string
		success  bool
	}{
		{func() {}, nil, "Queued", false},
		{func() { rec.Status = explore.Running }, nil, "Thinking", false},
		{func() {
			rec.History = []model.Message{{Role: "user"}, {Role: "assistant", ToolCalls: []model.ToolCall{{ID: "r1", Name: "read", Arguments: `{"path":"src/app/(admin)/layout.tsx"}`}}}}
		}, nil, "Read src/app/(admin)/layout.tsx", false},
		{func() {
			rec.History = append(rec.History, model.Message{Role: "tool", ToolCallID: "r1"})
			rec.Tools = append(rec.Tools, explore.ToolRecord{CallID: "r1", Name: "read", Arguments: `{"path":"src/app/(admin)/layout.tsx"}`})
			rec.Status = explore.Queued
		}, nil, "Read src/app/(admin)/layout.tsx", false},
		{func() {
			rec.Status = explore.Waiting
			rec.History = append(rec.History, model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "s1", Name: "task"}, {ID: "s2", Name: "task"}}})
		}, []explore.Record{{ID: "c1", ParentID: "t1", ParentCallID: "s1", Status: explore.Running}}, "Waiting for 2 subtasks", false},
		{func() {
			rec.History = append(rec.History, model.Message{Role: "tool", ToolCallID: "s1"}, model.Message{Role: "tool", ToolCallID: "s2"})
			rec.Tools = append(rec.Tools, explore.ToolRecord{CallID: "s1", Name: "task"}, explore.ToolRecord{CallID: "s2", Name: "task"})
			rec.Status = explore.Completed
			rec.WaitMs, rec.ActiveMs = 12_000, 4_200
			rec.FinishedAt = taskTestAccepted.Add(17 * time.Second)
		}, nil, "Done · 3 tool uses · wait 12s · active 4s", true},
	}
	for i, step := range steps {
		step.mutate()
		got := LiveLine(rec, step.children)
		if got != step.want {
			t.Fatalf("step %d (%s): LiveLine = %q, want %q", i, rec.Status, got, step.want)
		}
		settled, success := Settle(rec)
		if IsLive(rec.Status) {
			if settled != "" || success {
				t.Fatalf("step %d: live record settled as (%q, %v)", i, settled, success)
			}
		} else if settled != step.want || success != step.success {
			t.Fatalf("step %d: Settle = (%q, %v), want (%q, %v)", i, settled, success, step.want, step.success)
		}
	}
}

func TestFitLiveLineKeepsVerbAndPathTail(t *testing.T) {
	rec := taskTestRunning(taskTestTool("read", `{"path":"src/app/(admin)/dashboard/layout.tsx"}`))
	if got := FitLiveLine(rec, nil, 80, "…"); got != "Read src/app/(admin)/dashboard/layout.tsx" {
		t.Errorf("wide = %q", got)
	}
	if got, want := FitLiveLine(rec, nil, 20, "…"), "Read …ard/layout.tsx"; got != want {
		t.Errorf("narrow unicode = %q, want %q", got, want)
	}
	if got, want := FitLiveLine(rec, nil, 20, "..."), "Read ...d/layout.tsx"; got != want {
		t.Errorf("narrow ascii = %q, want %q", got, want)
	}
	run := taskTestRunning(taskTestTool("run_command", `{"command":"go test ./internal/... -run TestX"}`))
	if got, want := FitLiveLine(run, nil, 16, "…"), "Run go test ./i…"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	// Too narrow for the verb plus a clipped target: the whole line clips.
	if got, want := FitLiveLine(run, nil, 5, "…"), "Run…"; got != want {
		t.Errorf("tiny = %q, want %q", got, want)
	}
	if got := FitLiveLine(run, nil, 0, "…"); got != "" {
		t.Errorf("zero width = %q, want empty", got)
	}
}

func TestFitSettleDropsSegmentsRightToLeft(t *testing.T) {
	rec := explore.Record{Status: explore.Completed, Tools: make([]explore.ToolRecord, 3), WaitMs: 64_000, ActiveMs: 3_000}
	cases := []struct {
		width int
		want  string
	}{
		{80, "Done · 3 tool uses · wait 1m 4s · active 3s"},
		{43, "Done · 3 tool uses · wait 1m 4s · active 3s"},
		{42, "Done · 3 tool uses · active 3s"},
		{30, "Done · 3 tool uses · active 3s"},
		{29, "Done · 3 tool uses"},
		{18, "Done · 3 tool uses"},
		{17, "Done"},
		{4, "Done"},
		{3, "Do…"},
	}
	for _, c := range cases {
		got, ok := FitSettle(rec, c.width, "…")
		if got != c.want || !ok {
			t.Errorf("FitSettle(width %d) = (%q, %v), want (%q, true)", c.width, got, ok, c.want)
		}
	}
	failed := explore.Record{Status: explore.Failed, Reason: "provider stream reset by peer"}
	if got, ok := FitSettle(failed, 20, "..."); got != "Failed: provider..." || ok {
		t.Errorf("failed ascii = (%q, %v)", got, ok)
	}
	if got, ok := FitSettle(rec, 0, "…"); got != "" || !ok {
		t.Errorf("zero width = (%q, %v), want empty and success kept", got, ok)
	}
}

// TestTaskLinesFitEveryWidth checks the overflow invariant for every line
// kind, both ellipsis styles, and wide (CJK/emoji) targets.
func TestTaskLinesFitEveryWidth(t *testing.T) {
	children := []explore.Record{{ID: "c", ParentID: "t1", Status: explore.Running}}
	records := []explore.Record{
		taskTestRunning(taskTestTool("read", `{"path":"文档/设计/页面布局.tsx"}`)),
		taskTestRunning(taskTestTool("grep", `{"pattern":"🙂🙂🙂 emoji","path":"src"}`)),
		taskTestRunning(taskTestTool("edit", `{"operations":[{"kind":"create","path":"a/very/long/path/to/a/new/file.go"}]}`)),
		{ID: "t1", Status: explore.Waiting},
		{ID: "t1", Status: explore.Queued},
		{Status: explore.Completed, Tools: make([]explore.ToolRecord, 12), WaitMs: 3_723_000, ActiveMs: 61_000},
		{Status: explore.Failed, Reason: "失败：模型请求被拒绝 because the provider said no"},
		{Status: explore.Limited, Reason: "explore task deadline exceeded"},
		{Status: explore.Cancelled, Reason: "user stopped the branch"},
		{Status: explore.Interrupted},
	}
	for _, ellipsis := range []string{"…", "..."} {
		for _, rec := range records {
			for width := 1; width <= 60; width++ {
				live := FitLiveLine(rec, children, width, ellipsis)
				settled, _ := FitSettle(rec, width, ellipsis)
				for _, line := range []string{live, settled} {
					if cells := runewidth.StringWidth(line); cells > width {
						t.Fatalf("%s width %d ellipsis %q: %q is %d cells", rec.Status, width, ellipsis, line, cells)
					}
					if strings.Contains(line, "\n") {
						t.Fatalf("multi-row line %q", line)
					}
				}
			}
		}
	}
}
