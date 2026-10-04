package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"likha/internal/session"
	"likha/internal/tools"
)

// Tool items are pure formatting: one header and one ⎿ summary per call, built
// from the same ToolRecord the inspector reads. Nothing here touches UI state
// or styles; callers add the dot, the ⎿ gutter, and width fitting, and pass
// the glyph set's ellipsis that marks every cut made here.

const (
	toolStatusUnknown = iota
	toolStatusRunning
	toolStatusSuccess
	toolStatusFailure
)

const (
	toolPreviewLines = 3
	// toolHeaderArgCells bounds the formatted argument text before width
	// fitting, so a multi-megabyte command never reaches the renderer whole.
	toolHeaderArgCells   = 512
	toolHeaderValueCells = 80
	toolHeaderRawCells   = 72
	toolSummaryCells     = 240
)

// toolStatusKind maps a stored status to the dot class. Limited counts as a
// failure: the result is incomplete even when the handler succeeded. Task
// rows carry explore states until their result lands, so waiting-for-child,
// completed, and interrupted map too.
func toolStatusKind(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "awaiting approval", "awaiting_approval", "awaiting-approval", "running", "waiting-for-child":
		return toolStatusRunning
	case string(tools.Succeeded), "completed":
		return toolStatusSuccess
	case string(tools.Failed), string(tools.Refused), string(tools.Cancelled), string(tools.Limited), "interrupted":
		return toolStatusFailure
	}
	return toolStatusUnknown
}

// toolDisplayHeader returns `DisplayName(readable args)` from the call alone.
func toolDisplayHeader(name, argumentsJSON string, source tools.Source, ellipsis string) string {
	display, args := toolDisplayParts(name, argumentsJSON, source, ellipsis)
	return display + "(" + args + ")"
}

// toolRecordHeader also consults the stored result, which is the only place a
// directory read or the web search backend is knowable.
func toolRecordHeader(record session.ToolRecord, ellipsis string) string {
	display, args := toolRecordHeaderParts(record, ellipsis)
	return display + "(" + args + ")"
}

func toolRecordHeaderParts(record session.ToolRecord, ellipsis string) (display, args string) {
	source := tools.Source{Kind: record.SourceKind, Server: record.Server, Tool: record.SourceTool}
	display, args = toolDisplayParts(record.Name, record.Arguments, source, ellipsis)
	switch record.Name {
	case "read":
		if display == "Read" && strings.HasPrefix(record.Content, "read directory ") {
			var fields struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(record.Arguments), &fields) == nil {
				display, args = "List", toolHeaderText(toolDefaultPath(fields.Path), ellipsis)
			}
		}
	case "web_search":
		if backend := toolWebSearchContent(record.Content).Backend; backend != "" && strings.HasPrefix(args, `"`) {
			args = toolClipCells(args+" · "+toolHeaderText(backend, ellipsis), toolHeaderArgCells, ellipsis)
		}
	}
	return display, args
}

// fitToolHeader keeps the display name and shortens arguments first, then the
// name itself only when even `Name(…)` cannot fit.
func fitToolHeader(display, args string, width int, ellipsis string) string {
	full := display + "(" + args + ")"
	if runewidth.StringWidth(full) <= width {
		return full
	}
	room := width - runewidth.StringWidth(display) - 2
	if room >= runewidth.StringWidth(ellipsis) {
		return display + "(" + toolClipCells(args, room, ellipsis) + ")"
	}
	return toolClipCells(full, width, ellipsis)
}

// toolDisplayParts never fails: malformed JSON keeps the display name and
// shows the raw arguments, bounded, instead of guessing at fields.
func toolDisplayParts(name, argumentsJSON string, source tools.Source, ellipsis string) (display, args string) {
	display = toolDisplayName(name, source, ellipsis)
	fields, ok := toolOrderedFields(argumentsJSON)
	if !ok {
		return display, toolClipCells(toolHeaderText(argumentsJSON, ellipsis), toolHeaderRawCells, ellipsis)
	}
	text := func(key string) string {
		for _, field := range fields {
			if field.key == key {
				var value string
				if json.Unmarshal(field.value, &value) == nil {
					return value
				}
				return string(field.value)
			}
		}
		return ""
	}
	number := func(key string) int {
		for _, field := range fields {
			if field.key == key {
				var value int
				if json.Unmarshal(field.value, &value) == nil {
					return value
				}
			}
		}
		return 0
	}
	flag := func(key string) bool {
		for _, field := range fields {
			if field.key == key {
				var value bool
				return json.Unmarshal(field.value, &value) == nil && value
			}
		}
		return false
	}
	if source.Kind != "mcp" {
		switch name {
		case "read":
			path := text("path")
			if path == "" || path == "." || strings.HasSuffix(path, "/") {
				return "List", toolHeaderText(toolDefaultPath(path), ellipsis)
			}
			offset, limit := number("offset"), number("limit")
			if offset > 0 || limit > 0 {
				// Mirrors repository.ReadOptions: either field selects a
				// numbered range with defaults of 1 and 200.
				offset = max(offset, 1)
				if limit <= 0 {
					limit = 200
				}
				path += ":" + strconv.Itoa(offset) + "-" + strconv.Itoa(offset+limit-1)
			}
			return display, toolHeaderText(path, ellipsis)
		case "grep", "glob":
			query := text("pattern")
			if path := text("path"); path != "" {
				query += " in " + path
			}
			if name == "grep" && flag("literal") {
				query += " · literal"
			}
			return display, toolHeaderText(query, ellipsis)
		case "edit":
			return toolEditHeader(fields, ellipsis)
		case "edit_file":
			return display, toolHeaderText(text("path"), ellipsis)
		case "run_command":
			return display, toolHeaderText(text("command"), ellipsis)
		case "web_fetch":
			return display, toolHeaderText(text("url"), ellipsis)
		case "web_search":
			return display, toolHeaderText(strconv.Quote(text("query")), ellipsis)
		case "task":
			description := text("description")
			if description == "" {
				description = text("agent")
			}
			return display, toolHeaderText(description, ellipsis)
		case "ask_user":
			return display, toolHeaderText(text("question"), ellipsis)
		case "plan_update":
			for _, field := range fields {
				if field.key == "steps" {
					var steps []json.RawMessage
					if json.Unmarshal(field.value, &steps) == nil {
						return display, toolCount(len(steps), "step", "steps")
					}
				}
			}
			return display, ""
		case "skill":
			return display, toolHeaderText(text("name"), ellipsis)
		}
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		var value string
		if json.Unmarshal(field.value, &value) != nil {
			var compact bytes.Buffer
			if json.Compact(&compact, field.value) == nil {
				value = compact.String()
			} else {
				value = string(field.value)
			}
		}
		parts = append(parts, toolHeaderText(field.key, ellipsis)+": "+toolClipCells(toolHeaderText(value, ellipsis), toolHeaderValueCells, ellipsis))
	}
	return display, toolClipCells(strings.Join(parts, ", "), toolHeaderArgCells, ellipsis)
}

func toolDisplayName(name string, source tools.Source, ellipsis string) string {
	if source.Kind == "mcp" || strings.HasPrefix(name, "mcp__") {
		server, tool := source.Server, source.Tool
		if server == "" || tool == "" {
			// Qualified names are mcp__server__tool__digest; the original
			// names are lost to sanitizing, but the parts stay readable.
			if parts := strings.Split(name, "__"); len(parts) >= 3 && parts[0] == "mcp" {
				server, tool = parts[1], parts[2]
			}
		}
		if server != "" && tool != "" {
			return toolHeaderText(server, ellipsis) + "." + toolHeaderText(tool, ellipsis)
		}
	}
	switch name {
	case "read":
		return "Read"
	case "grep":
		return "Grep"
	case "glob":
		return "Glob"
	case "edit", "edit_file":
		return "Update"
	case "run_command":
		return "Run"
	case "web_fetch":
		return "Fetch"
	case "web_search":
		return "Search"
	case "task":
		return "Task"
	case "ask_user":
		return "Ask"
	case "plan_update":
		return "Plan"
	case "skill":
		return "Skill"
	}
	if name = toolHeaderText(name, ellipsis); name == "" {
		return "tool"
	}
	return name
}

func toolEditHeader(fields []toolField, ellipsis string) (display, args string) {
	var operations []struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	}
	for _, field := range fields {
		if field.key == "operations" {
			_ = json.Unmarshal(field.value, &operations)
		}
	}
	var paths []string
	creates := true
	for _, operation := range operations {
		if !strings.EqualFold(operation.Kind, "create") {
			creates = false
		}
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
		return "Create", toolHeaderText(paths[0], ellipsis)
	}
	return "Update", toolHeaderText(paths[0], ellipsis)
}

type toolField struct {
	key   string
	value json.RawMessage
}

// toolOrderedFields keeps the model's argument order so `k: v` headers read
// the way the call was written; map iteration would reorder them per render.
func toolOrderedFields(argumentsJSON string) ([]toolField, bool) {
	if strings.TrimSpace(argumentsJSON) == "" {
		return nil, true
	}
	decoder := json.NewDecoder(strings.NewReader(argumentsJSON))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	var fields []toolField
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		fields = append(fields, toolField{key: key, value: value})
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}

func toolDefaultPath(path string) string {
	if path == "" {
		return "."
	}
	return path
}

// toolHeaderText flattens untrusted text onto one row: whitespace runs become
// one space and hidden runes are escaped, never stripped, before bounding.
func toolHeaderText(text, ellipsis string) string {
	var plain strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			plain.WriteByte(' ')
		case hiddenReviewRune(r):
			fmt.Fprintf(&plain, "\\u%04X", r)
		default:
			plain.WriteRune(r)
		}
	}
	return toolClipCells(strings.Join(strings.Fields(plain.String()), " "), toolHeaderArgCells, ellipsis)
}

// toolClipCells bounds display cells, ending in ellipsis when anything was cut.
func toolClipCells(text string, width int, ellipsis string) string {
	if runewidth.StringWidth(text) <= width {
		return text
	}
	room := width - runewidth.StringWidth(ellipsis)
	if room < 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range text {
		size := runewidth.RuneWidth(r)
		if used+size > room {
			break
		}
		b.WriteRune(r)
		used += size
	}
	return strings.TrimRight(b.String(), " ") + ellipsis
}

// toolSummary is the ⎿ line plus bounded preview rows. An empty summary means
// no ⎿ line (unknown status, such as an unmatched legacy request). more counts
// output lines beyond the preview for `… +N lines (ctrl+o to expand)`.
func toolSummary(record session.ToolRecord, ellipsis string) (summary string, preview []string, more int) {
	status := strings.ToLower(strings.TrimSpace(record.Status))
	switch toolStatusKind(status) {
	case toolStatusUnknown:
		return "", nil, 0
	case toolStatusRunning:
		switch status {
		case "queued":
			return "Queued", nil, 0
		case "running":
			return "Running", nil, 0
		case "waiting-for-child":
			return "Waiting for subtasks", nil, 0
		}
		return "Awaiting approval", nil, 0
	}
	switch status {
	case "completed":
		// An accepted task settled; its result replaces this line.
		return "Done", nil, 0
	case "interrupted":
		return "Interrupted", nil, 0
	case string(tools.Refused):
		return toolClipCells("Refused: "+toolFailureReason(record, ellipsis), toolSummaryCells, ellipsis), nil, 0
	case string(tools.Cancelled):
		if reason := toolCancelReason(record, ellipsis); reason != "" {
			return toolClipCells("Cancelled: "+reason, toolSummaryCells, ellipsis), nil, 0
		}
		return "Cancelled", nil, 0
	case string(tools.Failed):
		if record.Name == "run_command" {
			if code, output, ok := toolExitStatus(record.Content); ok && code != 0 {
				preview, more = toolPreview(output)
				return "Failed: exit status " + strconv.Itoa(code), preview, more
			}
		}
		return toolClipCells("Failed: "+toolFailureReason(record, ellipsis), toolSummaryCells, ellipsis), nil, 0
	}
	summary, preview, more = toolSuccessSummary(record, ellipsis)
	if status == string(tools.Limited) || record.Truncated {
		summary += " · limited"
	}
	if count := len(record.Warnings); count > 0 {
		summary += " · " + toolCount(count, "warning", "warnings")
	}
	return toolClipCells(summary, toolSummaryCells, ellipsis), preview, more
}

// toolSuccessSummary covers succeeded and limited records. Counts come from
// the stored content only; nothing is re-read or re-run to compute them.
func toolSuccessSummary(record session.ToolRecord, ellipsis string) (string, []string, int) {
	content := record.Content
	if record.SourceKind == "mcp" {
		return toolGenericSummary(content)
	}
	switch record.Name {
	case "grep":
		matches, files, ok := toolGrepCounts(content)
		if !ok {
			// Legacy or reshaped output without the query header: lines are
			// the most honest count available.
			return "Found " + toolCount(toolLineCount(content), "line", "lines") + " of matches", nil, 0
		}
		return "Found " + toolCount(matches, "match", "matches") + " in " + toolCount(files, "file", "files"), nil, 0
	case "glob":
		header, body := toolQueryParts(content, "glob: ")
		count, ok := toolReturned(header)
		if !ok {
			count = toolLineCount(body)
		}
		return "Found " + toolCount(count, "file", "files"), nil, 0
	case "read":
		if strings.HasPrefix(content, "read directory ") {
			_, body, _ := strings.Cut(content, "\n")
			preview, more := toolPreview(body)
			return "Listed " + toolCount(toolLineCount(body), "entry", "entries"), preview, more
		}
		if first, body, _ := strings.Cut(content, "\n"); strings.HasPrefix(first, "read file ") && strings.Contains(first, "; selected=") {
			// Ranged reads open with a metadata line before the numbered
			// rows; it is neither counted nor previewed.
			content = body
		}
		preview, more := toolPreview(content)
		return "Read " + toolCount(toolLineCount(content), "line", "lines"), preview, more
	case "run_command":
		code, output, ok := toolExitStatus(content)
		preview, more := toolPreview(output)
		if !ok {
			return "Done", preview, more
		}
		if code != 0 {
			return "Failed: exit status " + strconv.Itoa(code), preview, more
		}
		return "Exit 0", preview, more
	case "web_fetch":
		return toolWebFetchSummary(content, ellipsis)
	case "web_search":
		search := toolWebSearchContent(content)
		if search.Hits == nil {
			return toolGenericSummary(content)
		}
		titles := make([]string, 0, len(search.Hits))
		for _, hit := range search.Hits {
			titles = append(titles, toolHeaderText(hit.Title, ellipsis))
		}
		preview, more := toolPreview(strings.Join(titles, "\n"))
		summary := toolCount(len(search.Hits), "result", "results")
		if search.Backend != "" {
			summary += " from " + toolHeaderText(search.Backend, ellipsis)
		}
		return summary, preview, more
	case "task":
		var outcome struct {
			Findings *string `json:"findings"`
		}
		if json.Unmarshal([]byte(content), &outcome) == nil && outcome.Findings != nil {
			preview, more := toolPreview(*outcome.Findings)
			return "Done", preview, more
		}
	case "ask_user":
		var answer struct {
			Answer *string `json:"answer"`
		}
		if json.Unmarshal([]byte(content), &answer) == nil && answer.Answer != nil {
			return "Answered: " + toolHeaderText(*answer.Answer, ellipsis), nil, 0
		}
	case "plan_update":
		var plan struct {
			Cleared   bool `json:"cleared"`
			Total     *int
			Completed int
		}
		if json.Unmarshal([]byte(content), &plan) == nil {
			if plan.Cleared {
				return "Cleared plan", nil, 0
			}
			if plan.Total != nil {
				return fmt.Sprintf("Updated plan · %d/%d completed", plan.Completed, *plan.Total), nil, 0
			}
		}
	case "skill":
		if strings.HasPrefix(content, "Loaded skill '") {
			name, _, _ := strings.Cut(strings.TrimPrefix(content, "Loaded skill '"), "'")
			_, body, _ := strings.Cut(content, "\n\n")
			preview, more := toolPreview(body)
			return "Loaded skill " + toolHeaderText(name, ellipsis), preview, more
		}
	}
	return toolGenericSummary(content)
}

func toolGenericSummary(content string) (string, []string, int) {
	preview, more := toolPreview(content)
	if len(preview) == 0 {
		return "Done · no output", nil, 0
	}
	return "Done", preview, more
}

// toolPreview takes the first lines of output with surrounding blank lines
// dropped; rows are untrusted text the renderer escapes and truncates. Tabs
// expand to spaces so they never show as escaped control runes.
func toolPreview(text string) ([]string, int) {
	lines := toolOutputLines(text)
	if len(lines) <= toolPreviewLines {
		return lines, 0
	}
	return lines[:toolPreviewLines], len(lines) - toolPreviewLines
}

func toolOutputLines(text string) []string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\t", "    ")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \r")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// toolLineCount counts content lines; a trailing newline ends the last line
// rather than starting an empty one.
func toolLineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}

func toolCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

var (
	toolReturnedPattern = regexp.MustCompile(`; returned=(\d+);`)
	toolGrepLinePattern = regexp.MustCompile(`^(.*?):\d+: `)
	toolExitPattern     = regexp.MustCompile(`^Exit status: (-?\d+)$`)
)

// toolQueryParts splits repository query output into its header line(s) and
// result rows. grep adds a second `syntax=` header line.
func toolQueryParts(content, prefix string) (header, body string) {
	if !strings.HasPrefix(content, prefix) {
		return "", content
	}
	header, body, _ = strings.Cut(content, "\n")
	if prefix == "grep: " && strings.HasPrefix(body, "syntax=") {
		_, body, _ = strings.Cut(body, "\n")
	}
	return header, body
}

func toolReturned(header string) (int, bool) {
	match := toolReturnedPattern.FindStringSubmatch(header)
	if match == nil {
		return 0, false
	}
	count, err := strconv.Atoi(match[1])
	return count, err == nil
}

func toolGrepCounts(content string) (matches, files int, ok bool) {
	header, body := toolQueryParts(content, "grep: ")
	if matches, ok = toolReturned(header); !ok {
		return 0, 0, false
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if match := toolGrepLinePattern.FindStringSubmatch(line); match != nil && !seen[match[1]] {
			seen[match[1]] = true
		}
	}
	return matches, len(seen), true
}

// toolExitStatus reads run_command's `Exit status: N` first line and returns
// the output after it, minus the trailing `Error:` line failures append.
func toolExitStatus(content string) (code int, output string, ok bool) {
	first, rest, _ := strings.Cut(content, "\n")
	match := toolExitPattern.FindStringSubmatch(strings.TrimRight(first, "\r"))
	if match == nil {
		return 0, content, false
	}
	code, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, content, false
	}
	if index := strings.LastIndex(rest, "\nError: "); index >= 0 && !strings.Contains(rest[index+1:], "\n") {
		rest = rest[:index]
	} else if strings.HasPrefix(rest, "Error: ") && !strings.Contains(rest, "\n") {
		rest = ""
	}
	return code, rest, true
}

func toolWebFetchSummary(content, ellipsis string) (string, []string, int) {
	_, body, _ := strings.Cut(content, "\n")
	var outcome struct {
		RequestedURL string  `json:"requested_url"`
		FinalURL     string  `json:"final_url"`
		Origin       string  `json:"origin"`
		Content      *string `json:"content"`
	}
	if json.Unmarshal([]byte(body), &outcome) != nil || outcome.Content == nil {
		return toolGenericSummary(content)
	}
	host := ""
	for _, raw := range []string{outcome.Origin, outcome.FinalURL, outcome.RequestedURL} {
		if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
			host = parsed.Host
			break
		}
	}
	summary := "Fetched " + toolByteSize(len(*outcome.Content))
	if host != "" {
		summary += " from " + toolHeaderText(host, ellipsis)
	}
	preview, more := toolPreview(*outcome.Content)
	return summary, preview, more
}

func toolByteSize(n int) string {
	if n < 1024 {
		return toolCount(n, "byte", "bytes")
	}
	kb := float64(n) / 1024
	if kb < 10 {
		return strconv.FormatFloat(kb, 'f', 1, 64) + " KB"
	}
	return strconv.Itoa(int(kb+0.5)) + " KB"
}

type toolWebSearch struct {
	Backend string `json:"backend"`
	Hits    []struct {
		Title string `json:"title"`
	} `json:"hits"`
}

func toolWebSearchContent(content string) toolWebSearch {
	var search toolWebSearch
	if json.Unmarshal([]byte(content), &search) != nil {
		return toolWebSearch{}
	}
	return search
}

// toolFailureReason prefers the runtime's appended `Error: message` line, then
// a task outcome's reason, then the first line with any `code: ` prefix
// removed. The text is flattened to one row.
func toolFailureReason(record session.ToolRecord, ellipsis string) string {
	content := record.Content
	if record.Name == "task" {
		var outcome struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(content), &outcome) == nil && strings.TrimSpace(outcome.Reason) != "" {
			return toolHeaderText(outcome.Reason, ellipsis)
		}
	}
	lines := toolOutputLines(content)
	for i := len(lines) - 1; i >= 0; i-- {
		if reason, ok := strings.CutPrefix(lines[i], "Error: "); ok && strings.TrimSpace(reason) != "" {
			return toolHeaderText(reason, ellipsis)
		}
	}
	if len(lines) == 0 {
		return "no reason recorded"
	}
	return toolHeaderText(toolStripCode(lines[0]), ellipsis)
}

var toolCodePattern = regexp.MustCompile(`^[a-z][a-z_]*: `)

func toolStripCode(line string) string {
	if loc := toolCodePattern.FindStringIndex(line); loc != nil {
		return line[loc[1]:]
	}
	return line
}

// toolCancelReason drops the generic context wording every cancellation
// carries; only a specific reason earns a place after `Cancelled:`.
func toolCancelReason(record session.ToolRecord, ellipsis string) string {
	if record.Name == "task" {
		var outcome struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(record.Content), &outcome) == nil {
			return toolSpecificCancel(toolHeaderText(outcome.Reason, ellipsis))
		}
	}
	for _, line := range toolOutputLines(record.Content) {
		reason := strings.TrimPrefix(line, "Error: ")
		if reason = toolSpecificCancel(toolHeaderText(toolStripCode(reason), ellipsis)); reason != "" {
			return reason
		}
	}
	return ""
}

func toolSpecificCancel(reason string) string {
	switch strings.TrimSuffix(strings.ToLower(reason), ".") {
	case "", "cancelled", "canceled", "context canceled", "tool execution was cancelled", "tool call was cancelled":
		return ""
	case "context deadline exceeded":
		return "timed out"
	}
	return reason
}
