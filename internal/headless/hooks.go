package headless

import (
	"context"
	"sync"

	"likha/internal/agent"
	"likha/internal/hooks"
)

// hookSet runs the user's hooks.json for one headless run (specs/hooks D11).
// Observe-only events run on their own goroutines so a slow hook never
// delays the run; wait collects them before the run reports its result.
// attention never fires: nobody is watching a headless run.
type hookSet struct {
	// ctx parents observe-only hooks; it is not the run's context.
	ctx       context.Context
	runner    *hooks.Runner
	sessionID string
	root      string
	// warn reports a hook warning; it must be safe for concurrent use.
	warn    func(text string)
	pending sync.WaitGroup
}

// loadHooks reads <stateDir>/hooks.json as an interactive launch does. A
// file Likha cannot use starts no hooks and returns the notice to report;
// a missing file returns a nil runner and no notice, so the run is exactly
// as without hooks.
func loadHooks(stateDir string) (*hooks.Runner, string) {
	if stateDir == "" {
		return nil, ""
	}
	cfg, err := hooks.Load(stateDir)
	if err != nil {
		return nil, "Hooks disabled: " + err.Error()
	}
	return hooks.NewRunner(cfg), ""
}

func (h *hookSet) event(name hooks.EventName) hooks.Event {
	return hooks.Event{Event: name, SessionID: h.sessionID, Repository: h.root, Agent: "main"}
}

// observe fires an observe-only event in the background. Hooks get their
// own timeouts under h.ctx, not the run's context, so run_end still runs
// after a cancelled or timed-out run.
func (h *hookSet) observe(ev hooks.Event) {
	if !h.runner.Has(ev.Event) {
		return
	}
	h.pending.Add(1)
	go func() {
		defer h.pending.Done()
		for _, warning := range h.runner.Fire(h.ctx, ev).Warnings {
			h.warn(warning)
		}
	}()
}

// sessionStart fires session_start for a new or resumed session.
func (h *hookSet) sessionStart(resumed bool) {
	ev := h.event(hooks.SessionStart)
	ev.Resumed = &resumed
	h.observe(ev)
}

// promptBlocked fires prompt_submit for the run's prompt before anything is
// sent and returns the hook's block, if any.
func (h *hookSet) promptBlocked(ctx context.Context, prompt string) *hooks.BlockError {
	if !h.runner.Has(hooks.PromptSubmit) {
		return nil
	}
	ev := h.event(hooks.PromptSubmit)
	ev.Prompt, ev.Origin = prompt, hooks.OriginTyped
	outcome := h.runner.Fire(ctx, ev)
	for _, warning := range outcome.Warnings {
		h.warn(warning)
	}
	return outcome.Block
}

// runEnd fires run_end with the run's outcome. A time limit is a failure,
// matching the exit code.
func (h *hookSet) runEnd(result runResult) {
	ev := h.event(hooks.RunEnd)
	switch result.Status {
	case statusCompleted:
		ev.Outcome = "completed"
	case statusCancelled:
		ev.Outcome = "cancelled"
	default:
		ev.Outcome, ev.Error = "failed", result.Err
	}
	h.observe(ev)
}

// turnEvent handles the agent's hook events: a warning becomes a notice and
// a post_tool event fires in the background. It reports whether ev was a
// hook event.
func (h *hookSet) turnEvent(ev agent.TurnEvent) bool {
	switch ev.Kind {
	case "hook":
		h.warn(ev.Text)
		return true
	case "hook_observe":
		if ev.Hook != nil {
			h.observe(*ev.Hook)
		}
		return true
	}
	return false
}

func (h *hookSet) wait() { h.pending.Wait() }
