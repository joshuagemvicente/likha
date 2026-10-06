package transcript

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/tools"
)

// Subagent items (spec transcript-redesign § Subagent items): a `task` tool
// item carries one ⎿ line. While the task record is live the line names its
// latest tool activity; once it settles the line states the outcome in words.
// Everything here is plain text: callers add the ⎿ gutter, styles, and the
// status dot, and every untrusted value is flattened to one row with hidden
// runes escaped.

// IsLive reports whether a task state still changes: queued, running, or
// waiting for its own children. Every other state is final.
func IsLive(status explore.State) bool {
	switch status {
	case explore.Queued, explore.Running, explore.Waiting:
		return true
	}
	return false
}

// LiveLine is the ⎿ text while a task is live: the latest tool activity of
// rec, the task record whose ParentCallID is the `task` call's ID. children
// are rec's own child records (ParentID == rec.ID); other records and stale
// versions of the same ID are ignored. In priority order:
//
//   - "Waiting for N subtasks" while rec waits on children (N counts live
//     children plus dispatched task calls with no child record yet; "1
//     subtask"; no number when none are known);
//   - the call rec is executing or about to execute (from the newest
//     assistant message in rec.History without a tool result), else its last
//     finished call (rec.Tools): "Read src/app.go", "Grep LAYOUT in src",
//     "Run go test", using the display names of § Tool items;
//   - "Queued" before any tool activity, "Thinking" once running with none.
//
// A settled record returns its Settle text. The result is one row with no
// width bound; FitLiveLine bounds it.
func LiveLine(rec explore.Record, children []explore.Record) string {
	return taskLiveText(rec, children, -1, "")
}

// FitLiveLine is LiveLine within width display cells. The verb survives; a
// path target is shortened from the left (ellipsis + tail, so the file name
// stays) and any other target from the right. width <= 0 returns "".
func FitLiveLine(rec explore.Record, children []explore.Record, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	return taskLiveText(rec, children, width, ellipsis)
}

// Settle returns the final ⎿ text of a settled task and whether it succeeded:
//
//   - completed:   "Done · N tool uses · wait Xs · active Ys" ("1 tool use";
//     the wait/active pair is omitted when the record did not measure it);
//   - failed:      "Failed: reason" ("Failed" without a reason);
//   - cancelled:   "Cancelled", plus ": reason" when the reason says more
//     than that the task or run was cancelled;
//   - limited:     "Limited: model-request limit", "Limited: deadline
//     exceeded", "Limited: runtime limit", else "Limited: reason";
//   - interrupted: "Interrupted".
//
// Only completed succeeds. A live or unrecognized state returns ("", false):
// there is no outcome to show yet. N counts rec's own tool calls (rec.Tools),
// including refused or cancelled ones. Durations are whole seconds in the
// transcript's shared shape ("3s", "1m 4s", "1h 2m").
func Settle(rec explore.Record) (text string, success bool) {
	return taskSettleText(rec, -1, "")
}

// FitSettle is Settle within width display cells. A completed line drops
// segments right to left (wait first, so the active figure survives as in
// /agents, then active, then the tool count) before clipping "Done"; any
// other outcome is clipped at the end with ellipsis. width <= 0 returns "".
func FitSettle(rec explore.Record, width int, ellipsis string) (text string, success bool) {
	if width <= 0 {
		_, success = taskSettleText(rec, -1, "")
		return "", success
	}
	return taskSettleText(rec, width, ellipsis)
}

// taskActivity is one live line: a verb and an optional target. pathTarget
// targets are clipped from the left so the file name stays visible.
type taskActivity struct {
	verb, target string
	pathTarget   bool
}

func (a taskActivity) text() string {
	if a.target == "" {
		return a.verb
	}
	return a.verb + " " + a.target
}

// fit keeps the verb and shortens the target first; width < 0 is unbounded.
func (a taskActivity) fit(width int, ellipsis string) string {
	full := a.text()
	if width < 0 || runewidth.StringWidth(full) <= width {
		return full
	}
	room := width - runewidth.StringWidth(a.verb) - 1
	if a.target == "" || room < runewidth.StringWidth(ellipsis)+1 {
		return clipRight(full, width, ellipsis)
	}
	if a.pathTarget {
		return a.verb + " " + clipLeft(a.target, room, ellipsis)
	}
	return a.verb + " " + clipRight(a.target, room, ellipsis)
}

func taskLiveText(rec explore.Record, children []explore.Record, width int, ellipsis string) string {
	if !IsLive(rec.Status) {
		text, _ := taskSettleText(rec, width, ellipsis)
		return text
	}
	return taskLiveActivity(rec, children).fit(width, ellipsis)
}

func taskLiveActivity(rec explore.Record, children []explore.Record) taskActivity {
	pending := taskPendingCalls(rec.History)
	// A task batch runs without the parent's permit; the runtime marks the
	// parent waiting just after it publishes the batch's assistant message.
	if rec.Status == explore.Waiting || len(pending) > 0 && pending[0].Name == "task" {
		return taskActivity{verb: taskWaitingText(rec, children, pending)}
	}
	if len(pending) > 0 {
		return taskCallActivity(pending[0].Name, pending[0].Arguments, "", tools.Source{})
	}
	if n := len(rec.Tools); n > 0 {
		last := rec.Tools[n-1]
		return taskCallActivity(last.Name, last.Arguments, last.Result.Content, last.Result.Source)
	}
	if rec.Status == explore.Queued {
		return taskActivity{verb: "Queued"}
	}
	return taskActivity{verb: "Thinking"}
}

// taskPendingCalls returns the newest assistant message's tool calls that
// have no tool result after it, in call order. The explore loop publishes
// that message before executing its calls sequentially and publishes each
// result as it lands, so the first pending call is the one in progress (or
// waiting for its permit). A streaming partial message carries no calls.
func taskPendingCalls(history []model.Message) []model.ToolCall {
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role != "assistant" {
			continue
		}
		answered := make(map[string]bool)
		for _, message := range history[index+1:] {
			if message.Role == "tool" {
				answered[message.ToolCallID] = true
			}
		}
		var pending []model.ToolCall
		for _, call := range history[index].ToolCalls {
			if !answered[call.ID] {
				pending = append(pending, call)
			}
		}
		return pending
	}
	return nil
}

// taskWaitingText counts what rec still waits on: its live children plus
// dispatched task calls that have no child record yet.
func taskWaitingText(rec explore.Record, children []explore.Record, pending []model.ToolCall) string {
	latest := make(map[string]explore.Record)
	for _, child := range children {
		if child.ID == "" || child.ParentID != rec.ID {
			continue
		}
		if prior, ok := latest[child.ID]; !ok || child.Version >= prior.Version {
			latest[child.ID] = child
		}
	}
	count := 0
	spawned := make(map[string]bool)
	for _, child := range latest {
		if child.ParentCallID != "" {
			spawned[child.ParentCallID] = true
		}
		if IsLive(child.Status) {
			count++
		}
	}
	for _, call := range pending {
		if call.Name == "task" && !spawned[call.ID] {
			count++
		}
	}
	if count == 0 {
		return "Waiting for subtasks"
	}
	return "Waiting for " + taskCount(count, "subtask", "subtasks")
}

// taskCallActivity names one call with § Tool items' display vocabulary.
// content is the stored result, the only place a directory read shows; it is
// "" for a call still in flight. Malformed arguments keep the verb alone.
func taskCallActivity(name, argumentsJSON, content string, source tools.Source) taskActivity {
	if source.Kind == "mcp" || strings.HasPrefix(name, "mcp__") {
		server, tool := source.Server, source.Tool
		if server == "" || tool == "" {
			// Qualified names are mcp__server__tool__digest.
			if parts := strings.Split(name, "__"); len(parts) >= 3 && parts[0] == "mcp" {
				server, tool = parts[1], parts[2]
			}
		}
		verb := flattenRow(name)
		if server != "" && tool != "" {
			verb = flattenRow(server) + "." + flattenRow(tool)
		}
		return taskActivity{verb: taskNonEmpty(verb, "tool"), target: taskFieldList(argumentsJSON)}
	}
	var fields map[string]json.RawMessage
	valid := strings.TrimSpace(argumentsJSON) == "" || json.Unmarshal([]byte(argumentsJSON), &fields) == nil
	text := func(key string) string {
		var value string
		raw, ok := fields[key]
		if !ok {
			return ""
		}
		if json.Unmarshal(raw, &value) != nil {
			// A wrong-typed field is as malformed as broken JSON.
			valid = false
			return ""
		}
		return flattenRow(value)
	}
	number := func(key string) int {
		var value int
		if raw, ok := fields[key]; ok && json.Unmarshal(raw, &value) == nil {
			return value
		}
		return 0
	}
	activity := func(verb, target string) taskActivity {
		if !valid {
			target = ""
		}
		return taskActivity{verb: verb, target: target}
	}
	switch name {
	case "read":
		path := text("path")
		if valid && (path == "" || path == "." || strings.HasSuffix(path, "/") || strings.HasPrefix(content, "read directory ")) {
			if path == "" {
				path = "."
			}
			return taskActivity{verb: "List", target: path, pathTarget: true}
		}
		if offset, limit := number("offset"), number("limit"); path != "" && (offset > 0 || limit > 0) {
			// Mirrors repository.ReadOptions: either field selects a numbered
			// range with defaults of 1 and 200.
			offset = max(offset, 1)
			if limit <= 0 {
				limit = 200
			}
			path += ":" + strconv.Itoa(offset) + "-" + strconv.Itoa(offset+limit-1)
		}
		a := activity("Read", path)
		a.pathTarget = true
		return a
	case "grep", "glob":
		query := text("pattern")
		if path := text("path"); query != "" && path != "" {
			query += " in " + path
		}
		verb := "Grep"
		if name == "glob" {
			verb = "Glob"
		}
		return activity(verb, query)
	case "edit":
		verb, target := taskEditActivity(fields["operations"])
		a := activity(verb, target)
		a.pathTarget = !strings.HasSuffix(target, " files")
		return a
	case "edit_file":
		a := activity("Update", text("path"))
		a.pathTarget = true
		return a
	case "run_command":
		return activity("Run", text("command"))
	case "web_fetch":
		return activity("Fetch", text("url"))
	case "web_search":
		query := text("query")
		if query != "" {
			query = strconv.Quote(query)
		}
		return activity("Search", query)
	case "task":
		return activity("Task", taskNonEmpty(text("description"), text("agent")))
	case "ask_user":
		if question := text("question"); question != "" {
			return activity("Ask", question)
		}
		// Questionnaire form: one question names itself, more a count.
		var questions []struct {
			Question string `json:"question"`
		}
		if raw, ok := fields["questions"]; ok && json.Unmarshal(raw, &questions) == nil && len(questions) > 0 {
			if len(questions) == 1 {
				return activity("Ask", flattenRow(questions[0].Question))
			}
			return activity("Ask", taskCount(len(questions), "question", "questions"))
		}
		return activity("Ask", "")
	case "plan_update":
		var steps []json.RawMessage
		if raw, ok := fields["steps"]; ok && json.Unmarshal(raw, &steps) == nil {
			return activity("Plan", taskCount(len(steps), "step", "steps"))
		}
		return activity("Plan", "")
	case "skill":
		return activity("Skill", text("name"))
	}
	return taskActivity{verb: taskNonEmpty(flattenRow(name), "tool"), target: taskFieldList(argumentsJSON)}
}

// taskEditActivity mirrors the edit header: Create for a single new file,
// "N files" for several, else Update with the one path.
func taskEditActivity(raw json.RawMessage) (verb, target string) {
	var operations []struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &operations)
	var paths []string
	creates := true
	for _, operation := range operations {
		creates = creates && strings.EqualFold(operation.Kind, "create")
		seen := false
		for _, path := range paths {
			seen = seen || path == operation.Path
		}
		if !seen {
			paths = append(paths, operation.Path)
		}
	}
	switch {
	case len(paths) == 0:
		return "Update", ""
	case len(paths) > 1:
		return "Update", strconv.Itoa(len(paths)) + " files"
	case creates:
		return "Create", flattenRow(paths[0])
	}
	return "Update", flattenRow(paths[0])
}

// taskFieldList renders unknown tools' arguments as "k: v, …" in sorted key
// order, so the line is stable across renders.
func taskFieldList(argumentsJSON string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(argumentsJSON), &fields) != nil || len(fields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		var value string
		if json.Unmarshal(fields[key], &value) != nil {
			value = string(fields[key])
		}
		parts = append(parts, flattenRow(key)+": "+flattenRow(value))
	}
	return strings.Join(parts, ", ")
}

func taskSettleText(rec explore.Record, width int, ellipsis string) (string, bool) {
	clip := func(text string) string {
		if width < 0 {
			return text
		}
		return clipRight(text, width, ellipsis)
	}
	reason := flattenRow(rec.Reason)
	switch rec.Status {
	case explore.Completed:
		segments := []string{"Done", taskCount(len(rec.Tools), "tool use", "tool uses")}
		if taskTimingRecorded(rec) {
			segments = append(segments, "wait "+taskDuration(rec.WaitMs), "active "+taskDuration(rec.ActiveMs))
		}
		text := strings.Join(segments, " · ")
		for width >= 0 && runewidth.StringWidth(text) > width && len(segments) > 1 {
			drop := len(segments) - 1
			if len(segments) == 4 {
				drop = 2 // wait goes first; active survives
			}
			segments = append(segments[:drop:drop], segments[drop+1:]...)
			text = strings.Join(segments, " · ")
		}
		return clip(text), true
	case explore.Failed:
		return clip(taskWithReason("Failed", reason)), false
	case explore.Cancelled:
		if taskRestatesCancel(reason) {
			reason = ""
		}
		return clip(taskWithReason("Cancelled", reason)), false
	case explore.Limited:
		lower := strings.ToLower(reason)
		switch {
		case strings.Contains(lower, "model-request limit"):
			reason = "model-request limit"
		case strings.Contains(lower, "deadline exceeded"):
			reason = "deadline exceeded"
		case strings.Contains(lower, "runtime limit"):
			reason = "runtime limit"
		}
		return clip(taskWithReason("Limited", reason)), false
	case explore.Interrupted:
		return clip("Interrupted"), false
	}
	return "", false
}

func taskWithReason(outcome, reason string) string {
	if reason == "" {
		return outcome
	}
	return outcome + ": " + reason
}

// taskRestatesCancel is true for reasons that only say the task or run was
// cancelled, such as the runtime's "explore task cancelled", "explore run
// cancelled", or "context canceled".
func taskRestatesCancel(reason string) bool {
	words := strings.FieldsFunc(strings.ToLower(reason), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	for _, word := range words {
		switch word {
		case "explore", "task", "run", "context", "cancelled", "canceled":
		default:
			return false
		}
	}
	return true
}

// taskTimingRecorded mirrors /agents: records persisted before wait/active
// accounting decode both totals as zero. Zero totals are genuine only when
// the task settled within a second of acceptance; a settled record missing
// either timestamp has no measurement to show.
func taskTimingRecorded(rec explore.Record) bool {
	if rec.WaitMs > 0 || rec.ActiveMs > 0 {
		return true
	}
	if rec.AcceptedAt.IsZero() || rec.FinishedAt.IsZero() {
		return false
	}
	return rec.FinishedAt.Sub(rec.AcceptedAt) < time.Second
}

// taskDuration formats a recorded total in whole seconds (truncated, never
// negative) with the transcript's shared wholeDuration shape ("3s", "1m 4s",
// "1h 2m"), so the settle line reads like the footer and thought markers.
// /agents keeps Go's compact form ("1m4s").
func taskDuration(ms int64) string {
	return wholeDuration(max(0, ms) / 1000)
}

func taskCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

func taskNonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
