package tui

import (
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
)

// initNoProposalNote closes a /init run that never showed a review for the
// root AGENTS.md (specs/repo-init): nothing was written and nothing retries.
const initNoProposalNote = "/init finished without proposing AGENTS.md; nothing was written. Re-run /init or ask for the draft."

// startInit runs /init [guidance]: one agent turn in the current
// conversation, limited by agent.RunOptions.InitMode, whose transcript line
// is the short command while the model and saved history receive the full
// survey prompt. Refusals are visible entries and never reach the network.
func (m *ui) startInit(guidance string) tea.Cmd {
	if m.client == nil {
		m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
		return nil
	}
	if m.pending != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "A review is pending; resolve it before running /init."})
		return nil
	}
	if m.working {
		m.entries = append(m.entries, entry{role: "Error", content: "A run is active; wait for it to finish or press Esc before running /init."})
		return nil
	}
	if m.planMode {
		m.entries = append(m.entries, entry{role: "Error", content: "Plan mode is on; turn off /plan to run /init."})
		return nil
	}
	display := "/init"
	if guidance != "" {
		display += " " + guidance
	}
	// toolRunOptions reads the flag while the turn starts; finishRun clears
	// it, so only this run is limited.
	m.initRun, m.initProposed = true, false
	cmd := m.startTurnDisplay(display, agent.InitPrompt(guidance), nil)
	if !m.working {
		m.initRun = false
	}
	return cmd
}

// isRootAgentsEdit reports whether a review proposes an edit to the
// repository-root AGENTS.md. edit_file titles its review "Edit: <path>" with
// the repository-relative path PrepareEdit cleaned.
func isRootAgentsEdit(request *agent.ApprovalRequest) bool {
	if request == nil || request.Kind != "edit" {
		return false
	}
	target, ok := strings.CutPrefix(request.Title, "Edit: ")
	if !ok {
		return false
	}
	return path.Clean(strings.ReplaceAll(strings.TrimSpace(target), "\\", "/")) == "AGENTS.md"
}
