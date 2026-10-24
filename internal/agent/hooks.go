package agent

import (
	"context"
	"strconv"
	"strings"

	"likha/internal/hooks"
	"likha/internal/tools"
)

// hookGate adapts the user's hooks to the registry gate (specs/hooks). A
// pre_tool block becomes the call's refusal; warnings and post_tool events
// leave through emit, so a slow observe-only hook never runs on the tool path.
type hookGate struct {
	runner    *hooks.Runner
	sessionID string
	root      string
	emit      func(TurnEvent)
}

// newHookGate returns nil when no tool hooks are configured, so the registry
// keeps its exact hook-free dispatch.
func newHookGate(runner *hooks.Runner, sessionID, root string, emit func(TurnEvent)) tools.Gate {
	if !runner.Has(hooks.PreTool) && !runner.Has(hooks.PostTool) {
		return nil
	}
	return &hookGate{runner: runner, sessionID: sessionID, root: root, emit: emit}
}

func (g *hookGate) BeforeTool(ctx context.Context, scope tools.Scope, call tools.GateCall) error {
	if !g.runner.Has(hooks.PreTool) {
		return nil
	}
	ev := g.event(hooks.PreTool, scope, call)
	outcome := g.runner.Fire(ctx, ev)
	for _, warning := range outcome.Warnings {
		g.emit(TurnEvent{Kind: "hook", Text: warning})
	}
	if outcome.Block != nil {
		return outcome.Block
	}
	return nil
}

func (g *hookGate) AfterTool(_ context.Context, scope tools.Scope, call tools.GateCall, result tools.Result) {
	if !g.runner.Has(hooks.PostTool) {
		return
	}
	ev := g.event(hooks.PostTool, scope, call)
	content, cut := hooks.BoundContent(result.Content)
	ev.Result = &hooks.ToolResult{Status: string(result.Status), Content: content, ContentTruncated: cut}
	if call.Name == "run_command" {
		ev.Result.ExitCode = commandExitCode(result.Content)
	}
	g.emit(TurnEvent{Kind: "hook_observe", Hook: &ev})
}

func (g *hookGate) event(name hooks.EventName, scope tools.Scope, call tools.GateCall) hooks.Event {
	effects := make([]string, len(call.Effects))
	for i, effect := range call.Effects {
		effects[i] = string(effect)
	}
	tool := &hooks.ToolCall{Name: call.Name, Source: call.Source.Kind, Effects: effects, Arguments: call.Arguments}
	if call.Source.Kind == "mcp" {
		tool.Server = call.Source.Server
	}
	return hooks.Event{Event: name, SessionID: g.sessionID, Repository: g.root, Agent: hookAgent(scope), Tool: tool}
}

// hookAgent names the agent a tool event came from: main, or the child's
// profile (explore, review, a user profile).
func hookAgent(scope tools.Scope) string {
	if agent := strings.TrimSpace(scope.Agent); agent != "" {
		return agent
	}
	return "main"
}

// commandExitCode reads the "Exit status: N" line run_command results start
// with; nil when the command never ran.
func commandExitCode(content string) *int {
	line, _, _ := strings.Cut(content, "\n")
	value, ok := strings.CutPrefix(line, "Exit status: ")
	if !ok {
		return nil
	}
	code, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return &code
}

// promptBlocked fires prompt_submit for a steering prompt at delivery. A
// block returns the hook's refusal; warnings are emitted either way.
func promptBlocked(ctx context.Context, runner *hooks.Runner, sessionID, root, text string, emit func(TurnEvent)) *hooks.BlockError {
	if !runner.Has(hooks.PromptSubmit) {
		return nil
	}
	outcome := runner.Fire(ctx, hooks.Event{Event: hooks.PromptSubmit, SessionID: sessionID, Repository: root, Agent: "main", Prompt: text, Origin: hooks.OriginSteering})
	for _, warning := range outcome.Warnings {
		emit(TurnEvent{Kind: "hook", Text: warning})
	}
	return outcome.Block
}
