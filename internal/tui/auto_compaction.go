package tui

import (
	"fmt"
	"math"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/providers"
)

// autoCompactEnabled reads the opt-out from config.json at run start
// (specs/auto-compaction D2). A missing state directory or an unreadable
// file leaves the default, on: the run start must not fail over a
// preference, and a broken config.json is reported where it is saved.
func (m *ui) autoCompactEnabled() bool {
	if m.stateDir == "" {
		return true
	}
	cfg, err := providers.LoadStoredConfig(m.stateDir)
	if err != nil {
		return true
	}
	return cfg.AutoCompactEnabled()
}

// beginAutoCompaction shows that the active run is compacting. The run
// stays active: commands stay refused and Esc cancels it (spec point 4).
func (m *ui) beginAutoCompaction() tea.Cmd {
	m.closeReasoning()
	m.streamBuf.Reset()
	m.streaming = -1
	if m.cancelling {
		return nil
	}
	m.status = "Compacting…"
	if m.working && m.pending == nil {
		m.showActivity()
		return m.activityTickCmd()
	}
	return nil
}

// applyAutoCompaction adopts the history an automatic compaction produced
// and keeps the run going. Unlike a finished /compact it ends nothing: the
// steering channel stays open (D8) and the turn footer later reads
// "compacted" (D10).
func (m *ui) applyAutoCompaction(v agent.TurnEvent) tea.Cmd {
	m.history = v.History
	// The agent keeps the run's own prompt and delivered steering prompts
	// right after the one summary message, so the turn's user messages now
	// start at index 1; reconcileQueue counts from there.
	m.priorLen = 1
	m.recalculateContext(m.history)
	m.entries = append(m.entries, entry{role: "Likha", content: autoCompactDivider(v.ContextTokens, v.ContextWindow) + "\n\n" + v.Text})
	m.turnCompacted = true
	m.persist()
	m.layoutWidth = 0
	if m.cancelling {
		return nil
	}
	m.status = "Waiting for model"
	if m.working && m.pending == nil {
		m.showActivity()
		return m.activityTickCmd()
	}
	return nil
}

// autoCompactDivider is the conversation line that marks an automatic
// compaction, naming the trigger (spec point 5):
//
//	Conversation compacted automatically at ~86% of the 200k context window. Summary of earlier turns:
func autoCompactDivider(tokens, window int64) string {
	if window <= 0 {
		return "Conversation compacted automatically. Summary of earlier turns:"
	}
	percent := int(math.Round(float64(tokens) / float64(window) * 100))
	return fmt.Sprintf("Conversation compacted automatically at ~%d%% of the %s context window. Summary of earlier turns:", percent, humanTokens(window))
}
