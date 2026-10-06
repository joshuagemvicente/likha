package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"likha/internal/explore"
	"likha/internal/mcp"
	"likha/internal/model"
	"likha/internal/repository"
	"likha/internal/tooloutput"
	"likha/internal/tools"
)

func RunTurnWithOptions(ctx context.Context, client *model.Client, repo *repository.Repository, root string, prior []model.Message, prompt string, servers *mcp.McpManager, steer <-chan string, options RunOptions, emit func(TurnEvent)) {
	if repo != nil {
		root = repo.Root()
	}
	prompt = ExpandFileReferences(prompt, repo)
	history := append(append([]model.Message(nil), prior...), model.Message{Role: "user", Content: prompt})
	fail := func(err error) { emit(TurnEvent{Kind: "error", Text: err.Error(), History: history}) }
	if client == nil {
		fail(errors.New("no provider configured"))
		return
	}
	if err := client.EnsureConnected(ctx); err != nil {
		fail(err)
		return
	}
	harness, warnings := HarnessMessages(repo, root)
	scope := mainScope(options.Scope)
	var childRunner explore.Runner
	var taskRuntime *explore.Manager
	var runners map[string]explore.Runner
	if options.TasksEnabled && options.SaveTask != nil && repo != nil {
		// One runner per advertised profile: the baseline scope resolves the
		// profile's allowlist against the main run scope at depth 1, and
		// max-depth nodes drop task through the wrapper. Drift and
		// model-mismatch refusals already happened when TaskProfiles was
		// built at the run boundary.
		for _, taskProfile := range options.TaskProfiles {
			base, err := profileScopeBaseline(taskProfile.Profile, scope)
			if err != nil {
				warnings = append(warnings, "Profile "+taskProfile.Profile.Name+" unavailable: "+err.Error())
				continue
			}
			profile, instructions := taskProfile.Profile, taskProfile.Instructions
			if runners == nil {
				runners = map[string]explore.Runner{}
			}
			runners[profile.Name] = func(childCtx context.Context, node *explore.Node) explore.Outcome {
				childScope := base
				if node.Record().Depth >= explore.MaxDepth {
					childScope = scopeWithoutTask(childScope)
				}
				return ProfileRunner(client, repo, harness, childScope, options.Outputs, profile, instructions)(childCtx, node)
			}
		}
		var err error
		taskRuntime, err = explore.New(ctx, explore.Config{
			SessionID: options.SessionID, Root: repo.Root(), Provider: options.Provider, Model: client.Model(), Scope: scope,
			Runners: runners,
			Runner: func(childCtx context.Context, node *explore.Node) explore.Outcome {
				return childRunner(childCtx, node)
			},
			OnUpdate: func(record explore.Record) error {
				if err := options.SaveTask(record); err != nil {
					return err
				}
				emit(TurnEvent{Kind: "task", Task: &record})
				return nil
			},
		})
		if err != nil {
			warnings = append(warnings, "Nested exploration unavailable: "+err.Error())
		} else {
			options.TaskSpawn = func(callCtx context.Context, callID string, spec explore.Spec) (explore.Outcome, error) {
				return taskRuntime.Spawn(callCtx, "", callID, spec)
			}
			defer func() {
				taskRuntime.CancelAll()
				taskRuntime.Wait()
			}()
		}
	}
	registry, registrationWarnings := newToolRegistry(repo, root, servers, options, emit)
	for _, warning := range append(warnings, registrationWarnings...) {
		emit(TurnEvent{Kind: "notice", Text: warning, History: history})
	}
	definitions := registry.Definitions(scope)
	if taskRuntime != nil {
		childScope := tools.Scope{Mode: scope.Mode, Agent: "explore", Allowed: make(map[string]bool)}
		for _, definition := range definitions {
			childScope.Allowed[definition.Name] = true
		}
		childRunner = ExploreRunner(client, repo, harness, childScope, options.Outputs)
		emit(TurnEvent{Kind: "task_runtime", TaskRuntime: taskRuntime})
	}
	mainHarness := harness
	if options.PlanMode {
		// Plan-mode instructions extend the same system layer with a copy;
		// explore children keep the ordinary harness (they were read-only
		// already and never see main mode state).
		mainHarness = append([]model.Message(nil), harness...)
		if len(mainHarness) > 0 && mainHarness[0].Role == "system" {
			mainHarness[0].Content += planModeInstructions
		}
	}
	if options.SkillAdvert != "" && len(mainHarness) > 0 && mainHarness[0].Role == "system" {
		mainHarness = append([]model.Message(nil), mainHarness...)
		mainHarness[0].Content += "\n\n" + options.SkillAdvert
	}
	applySteer := func(drained []string) {
		for _, text := range drained {
			history = append(history, model.Message{Role: "user", Content: ExpandFileReferences(text, repo)})
			emit(TurnEvent{Kind: "steer", Text: text, History: append([]model.Message(nil), history...)})
		}
	}
	var continuation *model.Message
	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		applySteer(drainSteer(steer))
		request := append(append([]model.Message(nil), mainHarness...), history...)
		if continuation != nil {
			request = append(request, *continuation)
		}
		tokens, known := model.EstimateInputTokens(client.Model(), request, definitions)
		emit(TurnEvent{Kind: "context", ContextTokens: tokens, ContextKnown: known, ContextEstimated: true})
		assistant, usage, usageOK, err := client.StreamUsage(ctx, request, definitions, func(text string) {
			emit(TurnEvent{Kind: "text", Text: text})
		}, func(reasoning string) {
			emit(TurnEvent{Kind: "reasoning", Text: reasoning})
		})
		if err != nil {
			fail(err)
			return
		}
		if usageOK {
			if usage.PromptSeen {
				emit(TurnEvent{Kind: "context", ContextTokens: usage.Prompt, ContextKnown: true})
			}
			emit(TurnEvent{Kind: "usage", Usage: &usage})
		}
		if err := validateToolCallIDs(assistant.ToolCalls); err != nil {
			assistant.ToolCalls = nil
			history = append(history, assistant)
			fail(err)
			return
		}
		history = append(history, assistant)
		if len(assistant.ToolCalls) == 0 {
			if err := ctx.Err(); err != nil {
				fail(err)
				return
			}
			if drained := drainSteer(steer); len(drained) > 0 {
				applySteer(drained)
				continue
			}
			emit(TurnEvent{Kind: "done", History: history})
			return
		}
		if taskRuntime != nil {
			checkpoint := append([]model.Message(nil), history...)
			appendUnexecuted(&checkpoint, assistant.ToolCalls)
			emit(TurnEvent{Kind: "tool_checkpoint", History: checkpoint})
		}
		for index := 0; index < len(assistant.ToolCalls); {
			if err := ctx.Err(); err != nil {
				appendUnexecuted(&history, assistant.ToolCalls[index:])
				fail(err)
				return
			}
			end := index + 1
			if parallelRead(registry, assistant.ToolCalls[index].Name) {
				for end < len(assistant.ToolCalls) && parallelRead(registry, assistant.ToolCalls[end].Name) {
					end++
				}
			}
			calls := assistant.ToolCalls[index:end]
			results := executeToolGroup(ctx, registry, scope, calls, emit)
			for offset, execution := range results {
				result := finishToolResult(execution.Call, execution.Result, execution.Err, options.Outputs)
				content := result.ModelContent()
				if result.Status == tools.Cancelled && !execution.Result.Executed {
					content = "Error: action not executed; run interrupted"
				}
				history = append(history, model.Message{Role: "tool", ToolCallID: execution.Call.ID, Content: content})
				completed := append([]model.Message(nil), history...)
				appendUnexecuted(&completed, assistant.ToolCalls[index+offset+1:])
				call := execution.Call
				emit(TurnEvent{Kind: "tool_result", Text: call.Name + ": " + content, History: completed, ToolCall: &call, ToolResult: &result})
			}
			index = end
			if err := ctx.Err(); err != nil {
				appendUnexecuted(&history, assistant.ToolCalls[index:])
				fail(err)
				return
			}
		}
		if round%32 == 0 {
			emit(TurnEvent{Kind: "notice", Text: fmt.Sprintf("Tool-round checkpoint reached (%d); continuing.", round), History: append([]model.Message(nil), history...)})
			continuation = &model.Message{Role: "developer", Content: fmt.Sprintf("Main tool-round checkpoint %d reached. Continue the user's requested work in this same run, within the existing tool and approval rules. This checkpoint grants no permission and does not reset child budgets.", round)}
		}
	}
}

func validateToolCallIDs(calls []model.ToolCall) error {
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		if call.ID == "" {
			return errors.New("model returned a tool call without an ID")
		}
		if seen[call.ID] {
			return fmt.Errorf("model returned duplicate tool call ID %q", call.ID)
		}
		seen[call.ID] = true
	}
	return nil
}

func mainScope(scope tools.Scope) tools.Scope {
	scope.Agent = "main"
	if scope.Allowed != nil {
		allowed := make(map[string]bool, len(scope.Allowed))
		for name, permit := range scope.Allowed {
			allowed[name] = permit
		}
		scope.Allowed = allowed
	}
	return scope
}

// planModeGates reports whether plan mode must refuse this tool before any
// approval flow: every MCP call regardless of annotations, plus built-in
// mutations. Read tools, ask_user, checklist updates, passive skills, and the
// consent-gated builtin web tools keep their ordinary ceilings.
func planModeGates(tool tools.Tool) bool {
	if tool.Source.Kind == "mcp" {
		return true
	}
	for _, effect := range tool.Effects {
		if effect == tools.Write || effect == tools.Exec {
			return true
		}
	}
	return false
}

// planModeInstructions extends the compiled harness's single system layer
// while plan mode is active; it is never persisted.
const planModeInstructions = `

## Plan mode

Plan mode is active. Survey the requested change with the ordinary read-only
tools and describe findings. Workspace edits, shell commands, and every MCP
call are refused without an approval flow; asking questions, updating the
persisted checklist, loading passive skills, and consent-gated web use remain
available. This mode grants no additional permissions and ends when the user
toggles it off.`

func parallelRead(registry *tools.Registry, name string) bool {
	tool, ok := registry.Lookup(name)
	if !ok || !tool.ParallelSafe || tool.Interactive || tool.UnavailableReason != "" {
		return false
	}
	for _, effect := range tool.Effects {
		if effect != tools.Read {
			return false
		}
	}
	return true
}

type toolExecution struct {
	Call   model.ToolCall
	Result tools.Result
	Err    error
}

func executeToolGroup(ctx context.Context, registry *tools.Registry, scope tools.Scope, calls []model.ToolCall, emit func(TurnEvent)) []toolExecution {
	results := make([]toolExecution, len(calls))
	for i, call := range calls {
		call := call
		results[i].Call = call
		emit(TurnEvent{Kind: "tool_start", Text: "Request: " + call.Name + " " + utf8Prefix(call.Arguments, 512), ToolCall: &call})
	}
	if len(calls) == 1 {
		results[0].Result, results[0].Err = registry.Invoke(WithTaskCallID(WithAskUserCallID(ctx, calls[0].ID), calls[0].ID), scope, calls[0])
		return results
	}
	permits := make(chan struct{}, 4)
	var wait sync.WaitGroup
	for i, call := range calls {
		wait.Add(1)
		go func(index int, call model.ToolCall) {
			defer wait.Done()
			select {
			case permits <- struct{}{}:
				defer func() { <-permits }()
			case <-ctx.Done():
				results[index].Err = ctx.Err()
				results[index].Result.Status = tools.Cancelled
				return
			}
			results[index].Result, results[index].Err = registry.Invoke(WithTaskCallID(WithAskUserCallID(ctx, call.ID), call.ID), scope, call)
		}(i, call)
	}
	wait.Wait()
	return results
}

func finishToolResult(call model.ToolCall, result tools.Result, err error, outputs *tooloutput.Store) tools.Result {
	result.Content = strings.ToValidUTF8(result.Content, "�")
	if err != nil {
		message := err.Error()
		var dispatchErr *tools.CallError
		if result.Source.Kind == "builtin" && errors.As(err, &dispatchErr) && dispatchErr.Cause != nil {
			message = dispatchErr.Cause.Error()
			if !result.Executed {
				result.Content = ""
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.Status = tools.Cancelled
		} else if result.Status != tools.Refused {
			result.Status = tools.Failed
		}
		if result.Content != "" {
			result.Content += "\n"
		}
		result.Content += "Error: " + message
	}
	if result.Status == "" {
		result.Status = tools.Succeeded
	}
	inline := tools.BoundResult(result)
	if inline.Content != result.Content {
		if outputs != nil && call.Name != "read_output" {
			capture := fmt.Sprintf("Tool output: %s\nSource: %s / %s / %s\nSource task: %s\nCall: %s\nExecution status: %s\nSource incomplete: %t\n", call.Name, result.Source.Kind, result.Source.Server, result.Source.Tool, result.SourceTaskID, call.ID, result.Status, result.Truncated)
			for _, warning := range result.Warnings {
				capture += "Warning: " + warning + "\n"
			}
			capture += "\n" + result.Content
			retentionKey := call.ID
			if result.SourceTaskID != "" {
				retentionKey = result.SourceTaskID + "/" + call.ID
			}
			reference, storeErr := outputs.Retain(retentionKey, capture)
			if reference.ID != "" {
				result.ArtifactID = reference.ID
			}
			if reference.Warning != "" {
				result.Warnings = append(result.Warnings, reference.Warning)
			}
			if storeErr != nil {
				result.Warnings = append(result.Warnings, "Output retention failed: "+storeErr.Error())
			}
		} else if outputs == nil {
			result.Warnings = append(result.Warnings, "Private output storage unavailable; narrow or repeat the operation to inspect discarded output.")
		}
		result.Truncated = true
		if result.Status == tools.Succeeded {
			result.Status = tools.Limited
		}
	}
	return tools.BoundResult(result)
}

func utf8Prefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	return text[:limit]
}
