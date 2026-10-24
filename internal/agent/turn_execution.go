package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"likha/internal/explore"
	"likha/internal/hooks"
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
	fail := func(err error) { emit(TurnEvent{Kind: "error", Text: err.Error(), Err: err, History: history}) }
	if client == nil {
		fail(errors.New("no provider configured"))
		return
	}
	if err := client.EnsureConnected(ctx); err != nil {
		fail(err)
		return
	}
	harness, warnings := HarnessMessages(repo, root)
	// Children share the run's hook gate: their pre_tool and post_tool events
	// name the child agent (specs/hooks D12).
	gate := newHookGate(options.Hooks, options.SessionID, root, emit)
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
		for _, taskProfile := range runTaskProfiles(options) {
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
				return ProfileRunner(client, repo, harness, childScope, options.Outputs, profile, instructions, gate)(childCtx, node)
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
		childRunner = ExploreRunner(client, repo, harness, childScope, options.Outputs, gate)
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
	if options.InitMode && !options.PlanMode {
		// Same copy-and-extend as plan mode; explore and profile children use
		// ExploreRegistry (glob/read/grep/task only), so they never gain write
		// or exec tools during an /init turn either.
		mainHarness = append([]model.Message(nil), mainHarness...)
		if len(mainHarness) > 0 && mainHarness[0].Role == "system" {
			mainHarness[0].Content += initModeInstructions
		}
	}
	if options.InstallMode && !options.PlanMode {
		mainHarness = append([]model.Message(nil), mainHarness...)
		if len(mainHarness) > 0 && mainHarness[0].Role == "system" {
			mainHarness[0].Content += installModeInstructions
		}
	}
	if options.SkillAdvert != "" && len(mainHarness) > 0 && mainHarness[0].Role == "system" {
		mainHarness = append([]model.Message(nil), mainHarness...)
		mainHarness[0].Content += "\n\n" + options.SkillAdvert
	}
	// applySteer delivers drained steering prompts and reports how many
	// reached the history: a prompt_submit hook may drop one at delivery
	// (specs/hooks point 10) while the others stay. A cancelled screen
	// delivers nothing more; the run is ending, and the prompts stay queued
	// in the UI like any other prompt the run never reached.
	applySteer := func(drained []string) int {
		delivered := 0
		for _, text := range drained {
			err := screenSteeringPrompt(ctx, options.Hooks, options.SessionID, root, text, emit)
			var block *hooks.BlockError
			if errors.As(err, &block) {
				emit(TurnEvent{Kind: "steer_blocked", Text: text, Err: block})
				continue
			}
			if err != nil {
				return delivered
			}
			history = append(history, model.Message{Role: "user", Content: ExpandFileReferences(text, repo)})
			emit(TurnEvent{Kind: "steer", Text: text, History: append([]model.Message(nil), history...)})
			delivered++
		}
		return delivered
	}
	var continuation *model.Message
	buildRequest := func() []model.Message {
		request := append(append([]model.Message(nil), mainHarness...), history...)
		if continuation != nil {
			request = append(request, *continuation)
		}
		return request
	}
	// runStart indexes the run's own prompt in history; automatic
	// compaction keeps the user messages from there on verbatim.
	// compactStoodDown ends automatic compaction for the rest of the run
	// once it failed or could not get under the threshold (D5, D6).
	runStart := len(prior)
	// stoppedPartial is set while history ends with the answer a steer-now
	// press cut short; it stays there only if a steering prompt follows.
	stoppedPartial := false
	compactStoodDown := !options.AutoCompact || options.ContextWindow <= 0
	// compactBeforeRequest runs one automatic compaction (specs/
	// auto-compaction). proceed is false only when the run was cancelled
	// and has already failed; every other outcome lets the request go out.
	// sent reports whether a summarize request was made.
	compactBeforeRequest := func(tokens int64) (proceed, sent bool) {
		window := options.ContextWindow
		emit(TurnEvent{Kind: "compacting"})
		replacement, summary, usage, usageOK, err := compactRun(ctx, client, history, runStart)
		if err != nil {
			if usageOK {
				emit(TurnEvent{Kind: "usage", Usage: &usage})
			}
			nothing := errors.Is(err, errNothingToCompact)
			if ctx.Err() != nil {
				fail(err)
				return false, !nothing
			}
			compactStoodDown = true
			text := fmt.Sprintf("Automatic compaction failed: %v. Continuing without compacting again in this run.", err)
			if nothing {
				text = fmt.Sprintf("Nothing earlier to summarize at ~%d%% of the context window; continuing without compacting again in this run.", contextPercent(tokens, window))
			}
			emit(TurnEvent{Kind: "notice", Text: text, History: append([]model.Message(nil), history...)})
			return true, !nothing
		}
		history = replacement
		runStart = 1 // compactRun puts the kept user prompts right after the summary
		compacted := TurnEvent{Kind: "auto_compacted", Text: summary, History: append([]model.Message(nil), history...), ContextTokens: tokens, ContextWindow: window}
		if usageOK {
			compacted.Usage = &usage
		}
		emit(compacted)
		return true, true
	}
	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		// A steer-now press that landed while tools executed is delivered
		// by the ordinary drain below; it must not stop the next request.
		discardSteerNow(options.SteerNow)
		delivered := applySteer(drainSteer(steer))
		if err := ctx.Err(); err != nil {
			// A cancel that interrupted a prompt_submit screen is not a
			// block: the cut-short answer the transcript shows stays in
			// the history the run ends with.
			fail(err)
			return
		}
		if stoppedPartial && delivered == 0 {
			// A prompt_submit hook blocked every prompt the press was for.
			// Nothing follows the cut-short answer, and a request ending
			// on an assistant turn is a prefill Claude rejects, so the
			// model answers afresh instead.
			history = history[:len(history)-1]
		}
		stoppedPartial = false
		request := buildRequest()
		tokens, known := client.EstimateInputTokens(request, definitions)
		emit(TurnEvent{Kind: "context", ContextTokens: tokens, ContextKnown: known, ContextEstimated: true})
		if !compactStoodDown && shouldAutoCompact(tokens, known, options.ContextWindow) {
			proceed, sent := compactBeforeRequest(tokens)
			if !proceed {
				return
			}
			// Compaction is a safe point too: prompts queued while it ran
			// join this request (D8), and a steer-now press is covered by
			// that same delivery.
			discardSteerNow(options.SteerNow)
			applySteer(drainSteer(steer))
			request = buildRequest()
			tokens, known = client.EstimateInputTokens(request, definitions)
			emit(TurnEvent{Kind: "context", ContextTokens: tokens, ContextKnown: known, ContextEstimated: true, ContextReestimate: !sent})
			if !compactStoodDown && shouldAutoCompact(tokens, known, options.ContextWindow) {
				compactStoodDown = true
				emit(TurnEvent{Kind: "notice", Text: fmt.Sprintf("Still at ~%d%% of the context window after compacting; continuing without compacting again in this run. /compact with a focus may help.", contextPercent(tokens, options.ContextWindow)), History: append([]model.Message(nil), history...)})
			}
		}
		roundCtx, stopRound := watchSteerNow(ctx, options.SteerNow, steer)
		var partial, partialReasoning strings.Builder
		// Only text, reasoning and notices are emitted while a response
		// streams. The headless --json redactor treats tool_start,
		// tool_result, usage and auto_compacted as the end of a model
		// response and releases the partial word it held back for
		// redaction, so emitting one of them mid-stream would let a secret
		// split across that point go out unredacted.
		assistant, usage, usageOK, err := client.StreamUsage(roundCtx, request, definitions, func(text string) {
			partial.WriteString(text)
			emit(TurnEvent{Kind: "text", Text: text})
		}, func(reasoning string) {
			partialReasoning.WriteString(reasoning)
			emit(TurnEvent{Kind: "reasoning", Text: reasoning})
		})
		steered := stopRound()
		if err != nil {
			if ctx.Err() == nil && steered {
				// Steer now (specs/steering-prompts M2): only this request
				// stopped. Visible text stays in history so the model does
				// not repeat itself, and on DeepSeek so does the reasoning
				// streamed so far, as unsigned replay text DeepSeek requires
				// on every earlier turn (specs/reasoning-replay decision 6).
				// Display reasoning, signed replay state and half-streamed
				// tool calls are dropped (a cut-short signed payload would be
				// rejected). Neither transport reports usage before its final
				// frame, so the stopped request carries none.
				if partial.Len() > 0 {
					history = append(history, client.PartialAssistant(partial.String(), partialReasoning.String()))
					stoppedPartial = true
				}
				emit(TurnEvent{Kind: "stream_interrupted", History: append([]model.Message(nil), history...)})
				continue
			}
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
			dropToolCalls(&assistant)
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
			if drained := drainSteer(steer); len(drained) > 0 && applySteer(drained) > 0 {
				continue
			}
			// A cancel that interrupted a prompt_submit screen above still
			// ends the run as cancelled, not done.
			if err := ctx.Err(); err != nil {
				fail(err)
				return
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

// discardSteerNow drops a steer-now signal still buffered from a press that
// landed while no request was in flight.
func discardSteerNow(steerNow <-chan struct{}) {
	select {
	case <-steerNow:
	default:
	}
}

// watchSteerNow derives one model request's context. A steer-now signal
// cancels only that context, and only while steering text is waiting in the
// queue: a signal with nothing to deliver never stops a response. stop ends
// the watch, waits for the watcher goroutine, and reports whether a steer
// cancelled the request. The run context's own cancellation is never
// reported as a steer.
func watchSteerNow(ctx context.Context, steerNow <-chan struct{}, steer <-chan string) (context.Context, func() bool) {
	roundCtx, cancel := context.WithCancel(ctx)
	if steerNow == nil {
		return roundCtx, func() bool { cancel(); return false }
	}
	finished := make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		for {
			select {
			case <-steerNow:
				if len(steer) == 0 {
					continue
				}
				cancel()
				result <- true
				return
			case <-roundCtx.Done():
				result <- false
				return
			case <-finished:
				result <- false
				return
			}
		}
	}()
	return roundCtx, func() bool {
		close(finished)
		steered := <-result
		cancel()
		return steered
	}
}

// dropToolCalls removes a malformed batch of tool calls from an assistant
// message that stays in history, together with its signed replay state.
func dropToolCalls(assistant *model.Message) {
	assistant.ToolCalls = nil
	dropSignedReplay(assistant)
}

// dropClippedReplay drops an assistant message's signed replay state when
// its content is about to be clipped to limit bytes. The replay text itself
// is never clipped.
func dropClippedReplay(assistant *model.Message, limit int) {
	if len(assistant.Content) > limit {
		dropSignedReplay(assistant)
	}
}

// dropSignedReplay keeps only the replay state that stays valid once the
// agent has changed an assistant message. Signed state describes the exact
// turn the issuer returned, so sending it with a different turn would
// misrepresent what the model issued. Unsigned reasoning text is plain
// context and stays: DeepSeek rejects later tool requests whose earlier
// turns lack it (specs/reasoning-replay context.md, explore and profile).
func dropSignedReplay(assistant *model.Message) {
	assistant.Replay = assistant.Replay.UnsignedOnly()
	if len(assistant.ToolCalls) == 0 {
		return
	}
	// Copy: the slice may share its backing array with an earlier history
	// value.
	calls := append([]model.ToolCall(nil), assistant.ToolCalls...)
	for i := range calls {
		calls[i].ThoughtSignature = ""
	}
	assistant.ToolCalls = calls
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

// initModeGates reports whether an /init turn must refuse this tool before
// any approval flow: every MCP call, every shell command, and every write
// except edit_file (whose Authorize further limits it to the root AGENTS.md).
func initModeGates(tool tools.Tool) bool {
	if tool.Source.Kind == "mcp" {
		return true
	}
	for _, effect := range tool.Effects {
		if effect == tools.Exec {
			return true
		}
		if effect == tools.Write && tool.Definition.Name != "edit_file" {
			return true
		}
	}
	return false
}

// initModeInstructions extends the compiled harness's single system layer for
// one /init turn; it is never persisted.
const initModeInstructions = `

## /init survey

This turn is an /init survey. Use read-only tools (read, glob, grep, task
subagents, ask_user) to learn the repository. Shell commands, MCP tools, and
every write except one edit_file proposal for the repository-root AGENTS.md
are refused. The user reviews the full diff before anything is written. This
limit applies to this turn only and grants no additional permissions.`

// installModeInstructions extends the compiled harness's single system layer
// for one /install turn; it is never persisted.
const installModeInstructions = `

## /install turn

This turn is an /install: detect with read tools and read-only commands, ask
only the unresolved choices in one ask_user questionnaire, then post the plan
with plan_update. Until a plan with at least one step is posted this turn,
commands outside the read-only tier and every edit are refused without review.
After that, every command and edit follows its normal approval. This limit
applies to this turn only and grants no additional permissions.`

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
