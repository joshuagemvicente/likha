package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
)

// installUsage is the refusal shown for /install without a request
// (specs/install-command).
const installUsage = "Usage: /install <what to install>"

// startInstall runs /install <request>: one agent turn in the current
// conversation, marked by agent.RunOptions.InstallMode, whose transcript
// line is the short command while the model and saved history receive the
// full install prompt. Refusals are visible entries and never reach the
// network.
func (m *ui) startInstall(request string) tea.Cmd {
	request = strings.TrimSpace(request)
	if request == "" {
		m.entries = append(m.entries, entry{role: "Error", content: installUsage})
		return nil
	}
	if m.client == nil {
		m.entries = append(m.entries, entry{role: "Error", content: "No provider configured; complete first-run setup first."})
		return nil
	}
	if m.pending != nil {
		m.entries = append(m.entries, entry{role: "Error", content: "A review is pending; resolve it before running /install."})
		return nil
	}
	if m.ask.pending {
		m.entries = append(m.entries, entry{role: "Error", content: "A question is pending; answer or skip it before running /install."})
		return nil
	}
	if m.working {
		m.entries = append(m.entries, entry{role: "Error", content: "A run is active; wait for it to finish or press Esc before running /install."})
		return nil
	}
	if m.planMode {
		m.entries = append(m.entries, entry{role: "Error", content: "Plan mode is on; turn off /plan to run /install."})
		return nil
	}
	// toolRunOptions reads the flag while the turn starts; finishRun clears
	// it, so only this run carries the plan gate.
	m.installRun = true
	cmd := m.startTurnDisplay("/install "+request, agent.InstallPrompt(request), nil)
	if !m.working {
		m.installRun = false
	}
	return cmd
}
