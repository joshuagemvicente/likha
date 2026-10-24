package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/session"
	"likha/internal/tools"
	likhaui "likha/internal/ui"
)

// rowCacheUI is a working session with settled command, edit, and legacy
// tool items ahead of a live run, tall enough that every layout row is on
// screen, so View compares the whole transcript.
func rowCacheUI() *ui {
	m := benchUI(0, 100, 400)
	// Output rows run past the narrowest width, so a resize re-clips them.
	output := strings.Repeat("ok  \tlikha/internal/pkg/with/a/long/import/path/that/runs/past/the/text/column\t0.012s\n", 40)
	m.entries = append(m.entries, entry{role: "You", content: "run the tests"})
	m.entries = append(m.entries, entry{role: "Tool", content: "run_command"})
	m.toolRecords = append(m.toolRecords, session.ToolRecord{
		EntryIndex: len(m.entries) - 1, CallID: "call-run", Name: "run_command",
		Arguments: `{"command":"go test ./..."}`, Status: "succeeded",
		Content: "Exit status: 0\n" + output, Warnings: []string{"slow"},
	})
	m.entries = append(m.entries, entry{role: "Tool", content: "edit_file"})
	m.toolRecords = append(m.toolRecords, session.ToolRecord{
		EntryIndex: len(m.entries) - 1, CallID: "call-edit", Name: "edit_file",
		Arguments: `{"path":"a.go"}`, Status: "succeeded",
		Diff: "--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,2 @@\n-old line\n+new line\n context\n",
	})
	m.entries = append(m.entries, entry{role: "Tool", content: "legacy tool line"})
	m.entries = append(m.entries, entry{role: "Tool", content: legacyRequestPrefix + "read {\"path\":\"b.go\"}"})
	m.entries = append(m.entries, entry{role: "Tool", content: "read: line one\nline two"})
	m.working = true
	m.runID = 3
	m.events = make(chan agent.TurnEvent, 1)
	return m
}

// uncachedView renders m as if no tool rows had ever been cached.
func uncachedView(m *ui) string {
	m.toolRowsCache = nil
	m.toolRowsEnv = toolRowsEnv{}
	return m.View()
}

// TestToolRowCacheMatchesUncachedLayout drives the same events through two
// sessions, one rendering through the tool row cache and one with the cache
// emptied before every frame, and requires identical frames after each
// event: streaming, a live call settling in place, in-place record
// changes, legacy text changes, focus, expand and collapse, theme, resize, glyph set, an entry
// removal shifting indices, and the run ending with a call still running.
func TestToolRowCacheMatchesUncachedLayout(t *testing.T) {
	// Frames carry color, so a stale theme shows in the comparison.
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	cached, plain := rowCacheUI(), rowCacheUI()
	call := &model.ToolCall{ID: "call-live", Name: "run_command", Arguments: `{"command":"make"}`}
	// stranded never settles: the run ending turns its running dot into an
	// interrupted item.
	stranded := &model.ToolCall{ID: "call-stranded", Name: "run_command", Arguments: `{"command":"sleep 9"}`}
	toolIndex := func(m *ui, callID string) int {
		for _, record := range m.toolRecords {
			if record.CallID == callID {
				return record.EntryIndex
			}
		}
		t.Fatalf("no tool item for %s", callID)
		return -1
	}
	steps := []struct {
		name  string
		apply func(m *ui)
	}{
		{"first token", func(m *ui) {
			m.Update(agent.TurnEvent{Kind: "text", Text: "Running ", RunID: m.runID})
		}},
		{"more tokens", func(m *ui) {
			m.Update(agent.TurnEvent{Kind: "text", Text: "the **build** now.", RunID: m.runID})
		}},
		{"live call starts", func(m *ui) {
			m.Update(agent.TurnEvent{Kind: "tool_start", Text: "run_command", ToolCall: call, RunID: m.runID})
		}},
		{"activity tick", func(m *ui) {
			m.activityFrame += toolBlinkFrames
			m.layoutWidth = 0
		}},
		{"live call settles in place", func(m *ui) {
			result := &tools.Result{Status: tools.Succeeded, Content: "Exit status: 0\n" + strings.Repeat("built target\n", 9)}
			m.Update(agent.TurnEvent{Kind: "tool_result", Text: "run_command", ToolCall: call, ToolResult: result, RunID: m.runID})
		}},
		{"tokens after the call", func(m *ui) {
			m.Update(agent.TurnEvent{Kind: "text", Text: "Done.", RunID: m.runID})
		}},
		{"warning added in place", func(m *ui) {
			m.toolRecords[0].Warnings = append(m.toolRecords[0].Warnings, "flaky")
			m.layoutWidth = 0
		}},
		{"output changes in place", func(m *ui) {
			m.toolRecords[0].Content = "Exit status: 0\n" + strings.Repeat("rewritten output\n", 12)
			m.layoutWidth = 0
		}},
		{"legacy texts change", func(m *ui) {
			for i, e := range m.entries {
				switch e.content {
				case "legacy tool line":
					m.entries[i].content = "legacy tool line, reworded"
				case "read: line one\nline two":
					m.entries[i].content = "read: line one\nline two\nline three"
				}
			}
			m.layoutWidth = 0
		}},
		{"second live call starts", func(m *ui) {
			m.Update(agent.TurnEvent{Kind: "tool_start", Text: "run_command", ToolCall: stranded, RunID: m.runID})
		}},
		{"focus a call", func(m *ui) { m.setToolEntryFocus(toolIndex(m, "call-run")) }},
		{"expand it", func(m *ui) {
			if !m.toggleToolOutput(toolIndex(m, "call-run")) {
				t.Fatal("expand: nothing to toggle")
			}
		}},
		{"focus the edit", func(m *ui) { m.setToolEntryFocus(toolIndex(m, "call-edit")) }},
		{"collapse the first", func(m *ui) {
			if !m.toggleToolOutput(toolIndex(m, "call-run")) {
				t.Fatal("collapse: nothing to toggle")
			}
		}},
		{"theme preview", func(m *ui) { m.previewTheme("github") }},
		{"resize", func(m *ui) { m.Update(tea.WindowSizeMsg{Width: 72, Height: 400}) }},
		{"ascii glyphs", func(m *ui) {
			m.blocks = likhaui.BlockGlyphSet(true)
			m.layoutWidth = 0
		}},
		{"entry removed before the calls", func(m *ui) {
			m.entries = append(m.entries[:0], m.entries[1:]...)
			m.adjustToolRecordsAfterRemoval(0)
			m.toolInspector.focused = false
			m.layoutWidth = 0
		}},
		{"run ends", func(m *ui) {
			m.Update(agent.TurnEvent{Kind: "done", RunID: m.runID, History: m.history})
		}},
	}
	for _, step := range steps {
		step.apply(cached)
		step.apply(plain)
		got, want := cached.View(), uncachedView(plain)
		if got != want {
			t.Fatalf("after %s: cached frame differs from uncached frame\ncached:\n%s\nuncached:\n%s", step.name, stripANSI(got), stripANSI(want))
		}
	}
}

// TestToolRowCacheReusesUnchangedItems pins the point of the cache: a
// streaming token re-lays out no settled tool item, while a change to one
// of its inputs lays it out again.
func TestToolRowCacheReusesUnchangedItems(t *testing.T) {
	m := rowCacheUI()
	m.Update(agent.TurnEvent{Kind: "text", Text: "Running", RunID: m.runID})
	m.View()
	index := m.toolRecords[0].EntryIndex
	before := m.toolRowsCache[index].rows
	m.Update(agent.TurnEvent{Kind: "text", Text: " still", RunID: m.runID})
	m.View()
	if after := m.toolRowsCache[index].rows; &after[0] != &before[0] {
		t.Fatal("a streaming token laid out an unchanged tool item again")
	}
	m.setToolEntryFocus(index)
	m.View()
	if after := m.toolRowsCache[index].rows; &after[0] == &before[0] {
		t.Fatal("focusing a tool item reused its unfocused rows")
	}
}

// BenchmarkToolRowCacheMiss is the layout cost of the long session with the
// cache emptied before each frame: the per-token cost before the cache.
func BenchmarkToolRowCacheMiss(b *testing.B) {
	m := streamBenchUI(200)
	streamToken(m, "The tests ")
	b.ResetTimer()
	for range b.N {
		m.Update(agent.TurnEvent{Kind: "text", Text: "pass ", RunID: m.runID})
		uncachedView(m)
	}
}

// Every session.ToolRecord field is part of the tool row cache key: a record
// that differs in any one field must miss the cache. A field added to
// ToolRecord without a comparison in toolRowsInput.equal fails here.
func TestToolRowsInputComparesEveryToolRecordField(t *testing.T) {
	recordType := reflect.TypeOf(session.ToolRecord{})
	for i := range recordType.NumField() {
		field := recordType.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			var changed session.ToolRecord
			value := reflect.ValueOf(&changed).Elem().Field(i)
			switch value.Kind() {
			case reflect.String:
				value.SetString("changed")
			case reflect.Int:
				value.SetInt(1)
			case reflect.Bool:
				value.SetBool(true)
			case reflect.Slice:
				value.Set(reflect.Append(value, reflect.ValueOf("changed")))
			default:
				t.Fatalf("session.ToolRecord.%s has kind %s: extend this test and compare the field in toolRowsInput.equal (row_cache.go)", field.Name, value.Kind())
			}
			base := toolRowsInput{hasRecord: true}
			next := toolRowsInput{hasRecord: true, record: changed}
			if base.equal(next) {
				t.Fatalf("toolRowsInput.equal ignores session.ToolRecord.%s: add it to the cache key in row_cache.go, or a changed record paints stale rows", field.Name)
			}
		})
	}
}
