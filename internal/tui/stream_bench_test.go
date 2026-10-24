package tui

import (
	"fmt"
	"strings"
	"testing"

	"likha/internal/agent"
	"likha/internal/session"
)

// streamBenchOutput is ~58KB of command output: long enough that splitting
// and clipping it per layout dominates a frame when nothing caches it.
func streamBenchOutput() string {
	var b strings.Builder
	b.WriteString("Exit status: 0\n")
	for i := 0; b.Len() < 58*1024; i++ {
		fmt.Fprintf(&b, "ok  \tlikha/internal/pkg%03d\t0.%03ds\tcoverage: %d.%d%% of statements in the package under test\n", i, i%1000, i%100, i%10)
	}
	return b.String()
}

// streamBenchUI is a working session holding calls tool items, each with a
// settled run_command record of ~58KB output, followed by a live assistant
// answer that streams through the real TurnEvent path.
func streamBenchUI(calls int) *ui {
	m := benchUI(0, 120, 40)
	output := streamBenchOutput()
	for i := range calls {
		m.entries = append(m.entries, entry{role: "You", content: fmt.Sprintf("run the tests, round %d", i)})
		m.entries = append(m.entries, entry{role: "Tool", content: "run_command"})
		m.toolRecords = append(m.toolRecords, session.ToolRecord{
			EntryIndex: len(m.entries) - 1,
			CallID:     fmt.Sprintf("call-%d", i),
			Name:       "run_command",
			Arguments:  `{"command":"go test ./..."}`,
			Status:     "succeeded",
			Content:    output,
		})
	}
	m.working = true
	m.runID = 7
	m.events = make(chan agent.TurnEvent, 1)
	return m
}

// streamToken drives one streaming text delta through Update and renders
// the frame Bubble Tea would draw for it.
func streamToken(m *ui, text string) {
	m.Update(agent.TurnEvent{Kind: "text", Text: text, RunID: m.runID})
	m.View()
}

// BenchmarkStreamingTokenLongSession is one streaming token in a session
// that already holds many tool calls with large outputs: every delta lays
// the transcript out again, so unchanged tool items should cost nothing.
func BenchmarkStreamingTokenLongSession(b *testing.B) {
	for _, calls := range []int{50, 200} {
		b.Run(fmt.Sprintf("calls=%d", calls), func(b *testing.B) {
			m := streamBenchUI(calls)
			streamToken(m, "The tests ")
			b.ResetTimer()
			for range b.N {
				streamToken(m, "pass ")
			}
		})
	}
}

// BenchmarkStreamingTokenLongAnswer is one streaming token appended to a
// ~100KB markdown answer: the answer itself is what re-renders per delta.
func BenchmarkStreamingTokenLongAnswer(b *testing.B) {
	m := streamBenchUI(0)
	var answer strings.Builder
	for i := 0; answer.Len() < 100*1024; i++ {
		fmt.Fprintf(&answer, "## Step %d\n\nThe walker opens each **path component** by descriptor, so a `symlink` swap cannot escape the root; see item %d.\n\n- first point %d\n- second point with `code`\n\n```go\nfunc step%d() error { return nil }\n```\n\n", i, i, i, i)
	}
	streamToken(m, answer.String())
	b.ResetTimer()
	for range b.N {
		streamToken(m, "more ")
	}
}
