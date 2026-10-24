package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/hooks"
)

// Attention kinds for the attention hook event (specs/hooks D1): the moments
// FR-41 (attention-notifications) signals.
const (
	hookAttentionReview   = "review"
	hookAttentionQuestion = "question"
	hookAttentionRunEnd   = "run_end"
)

// hookWarningsMsg carries warnings from hooks fired off the UI goroutine.
type hookWarningsMsg struct{ warnings []string }

// loadHooks reads <stateDir>/hooks.json once at launch (D9). A file Likha
// cannot use starts no hooks; the error is shown once and /hooks repeats it.
func (m *ui) loadHooks() {
	if m.stateDir == "" {
		return
	}
	m.hooksPath = hooks.Path(m.stateDir)
	cfg, err := hooks.Load(m.stateDir)
	if err != nil {
		m.hooksErr = err
		m.entries = append(m.entries, entry{role: "Error", content: "Hooks disabled: " + err.Error()})
		return
	}
	m.hooks = hooks.NewRunner(cfg)
}

// hookEvent fills the fields every event carries.
func (m *ui) hookEvent(name hooks.EventName) hooks.Event {
	return hooks.Event{Event: name, SessionID: m.snapshot.ID, Repository: m.root, Agent: "main"}
}

// fireObserveHook runs an observe-only event off the UI goroutine, so a slow
// hook never delays the next request; its warnings come back as a message.
func (m *ui) fireObserveHook(ev hooks.Event) tea.Cmd {
	runner := m.hooks
	if !runner.Has(ev.Event) {
		return nil
	}
	return func() tea.Msg {
		outcome := runner.Fire(context.Background(), ev)
		if len(outcome.Warnings) == 0 {
			return nil
		}
		return hookWarningsMsg{warnings: outcome.Warnings}
	}
}

func (m *ui) applyHookWarnings(warnings []string) {
	for _, warning := range warnings {
		m.entries = append(m.entries, entry{role: "Likha", content: warning})
	}
	m.layoutWidth = 0
}

// fireSessionStart fires session_start for a new or resumed session.
func (m *ui) fireSessionStart(resumed bool) tea.Cmd {
	ev := m.hookEvent(hooks.SessionStart)
	ev.Resumed = &resumed
	return m.fireObserveHook(ev)
}

// attentionMoment fires the attention hook for a moment that needs the user:
// an approval review or a question opened, or a run finished. FR-41's bell
// and desktop notification fire at the same moments through signalAttention
// and signalRunEnd (attention.go), which add their own focus and run-length
// rules.
func (m *ui) attentionMoment(kind string) tea.Cmd {
	ev := m.hookEvent(hooks.Attention)
	ev.Attention = kind
	return m.fireObserveHook(ev)
}

// runEndHooks fires run_end, and attention for a run that finished on its own
// (a cancelled run needs no attention). It must run before finishRun clears
// the cancelling state.
func (m *ui) runEndHooks(v agent.TurnEvent) tea.Cmd {
	ev := m.hookEvent(hooks.RunEnd)
	switch {
	case m.cancelling:
		ev.Outcome = "cancelled"
	case v.Kind == "error":
		ev.Outcome = "failed"
		ev.Error = v.Text
	default:
		ev.Outcome = "completed"
	}
	cmd := m.fireObserveHook(ev)
	if ev.Outcome == "cancelled" {
		return cmd
	}
	return tea.Batch(cmd, m.attentionMoment(hookAttentionRunEnd))
}

// promptHook returns the prompt_submit check a new run performs on its own
// goroutine before anything is sent, or nil when no prompt hook is
// configured. Warnings travel through emit.
func (m *ui) promptHook(origin string) func(ctx context.Context, prompt string, emit func(agent.TurnEvent)) *hooks.BlockError {
	runner := m.hooks
	if !runner.Has(hooks.PromptSubmit) {
		return nil
	}
	base := m.hookEvent(hooks.PromptSubmit)
	return func(ctx context.Context, prompt string, emit func(agent.TurnEvent)) *hooks.BlockError {
		ev := base
		ev.Prompt, ev.Origin = prompt, origin
		outcome := runner.Fire(ctx, ev)
		for _, warning := range outcome.Warnings {
			emit(agent.TurnEvent{Kind: "hook", Text: warning})
		}
		return outcome.Block
	}
}

// applyPromptBlocked undoes a run whose prompt a hook blocked before it was
// sent: the You row and the history entry go, the prompt returns to the
// composer, and nothing was persisted or requested (specs/hooks point 10).
func (m *ui) applyPromptBlocked(v agent.TurnEvent) {
	m.hideActivity()
	if m.promptRow >= 0 && m.promptRow < len(m.entries) && m.entries[m.promptRow].role == "You" {
		m.entries = append(m.entries[:m.promptRow], m.entries[m.promptRow+1:]...)
		m.adjustToolRecordsAfterRemoval(m.promptRow)
	}
	m.promptRow = -1
	if m.priorLen <= len(m.history) {
		m.history = m.history[:m.priorLen]
	}
	message := "Prompt blocked by a hook."
	var block *hooks.BlockError
	if errors.As(v.Err, &block) {
		message = block.PromptMessage()
	}
	m.entries = append(m.entries, entry{role: "Error", content: message})
	if len(m.input) == 0 {
		m.input = []rune(v.Text)
		m.edit.endCaret(m.input)
	} else {
		m.entries = append(m.entries, entry{role: "Likha", content: "The blocked prompt was not put back because the composer holds a draft; Up recalls it from prompt history."})
	}
	m.restoreQueuedRows()
	m.working = false
	// A blocked prompt started no work, so it never recorded a run marker.
	m.runMarker = nil
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	m.cancelling = false
	m.initRun, m.initProposed, m.installRun = false, false, false
	m.steer, m.steerNow, m.steering = nil, nil, false
	m.status = "Prompt blocked"
	m.persist()
	m.refreshStatusSessionTitle()
	m.layoutWidth = 0
}

// restoreQueuedRows gives every held queue message a Queued row again; a
// flushed queue's rows were removed when its head started the blocked run.
func (m *ui) restoreQueuedRows() {
	rows := make(map[string]int)
	for _, e := range m.entries {
		if e.role == "Queued" {
			rows[e.content]++
		}
	}
	for _, text := range m.queue {
		if rows[text] > 0 {
			rows[text]--
			continue
		}
		m.entries = append(m.entries, entry{role: "Queued", content: text})
	}
}

// applySteerBlocked drops one steering prompt a hook blocked at delivery;
// the other queued prompts stay.
func (m *ui) applySteerBlocked(v agent.TurnEvent) {
	for i := range m.entries {
		if m.entries[i].role == "Queued" && m.entries[i].content == v.Text {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			m.adjustToolRecordsAfterRemoval(i)
			break
		}
	}
	for i, text := range m.queue {
		if text == v.Text {
			m.queue = append(m.queue[:i:i], m.queue[i+1:]...)
			if i < len(m.queueSelections) {
				m.queueSelections = append(m.queueSelections[:i:i], m.queueSelections[i+1:]...)
			}
			break
		}
	}
	message := "Prompt blocked by a hook."
	var block *hooks.BlockError
	if errors.As(v.Err, &block) {
		message = block.PromptMessage()
	}
	m.entries = append(m.entries, entry{role: "Error", content: message})
}

// hooksView is the /hooks report: the loaded hooks with their last outcome,
// the load error, or the path to create.
func (m *ui) hooksView() string {
	path := m.hooksPath
	if path == "" {
		path = hooks.FileName + " in the Likha state directory"
	}
	if m.hooksErr != nil {
		return "Hooks disabled: " + m.hooksErr.Error() + "\nNo hook runs until the file is fixed and Likha is relaunched."
	}
	statuses := m.hooks.Status()
	if len(statuses) == 0 {
		return "No hooks configured. Add them to " + path + " and relaunch Likha."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Hooks from %s (read at launch; edits apply after a relaunch):", path)
	for _, status := range statuses {
		fmt.Fprintf(&b, "\n- %s %q: %s", status.Event, status.Name, status.Command)
		if len(status.Match) > 0 {
			fmt.Fprintf(&b, " · match %s", strings.Join(status.Match, ", "))
		}
		fmt.Fprintf(&b, " · timeout %s", status.Timeout)
		if status.Event.Blocking() {
			onError := "block"
			if status.FailOpen {
				onError = "allow"
			}
			fmt.Fprintf(&b, " · on_error %s", onError)
		}
		last := "not run yet"
		if status.Last != "" {
			last = status.Last + " at " + status.LastAt.Format("15:04:05")
		}
		fmt.Fprintf(&b, "\n  last: %s", last)
	}
	return b.String()
}
