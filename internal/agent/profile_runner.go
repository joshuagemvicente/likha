package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/profiles"
	"likha/internal/repository"
	"likha/internal/tooloutput"
	"likha/internal/tools"
)

// ProfileRunner starts each accepted profile task with a private client and
// fresh conversation, sharing ExploreRunner's runtime semantics. Two inputs
// differ: the child's system layer is compiled from the profile's
// user-authored instructions, and the effective scope is the coordinator's
// frozen Resolve output, used verbatim. The captured harness and scope are
// run-boundary snapshots, never the parent's conversation, approvals, or live
// configuration.
func ProfileRunner(client *model.Client, repo *repository.Repository, harness []model.Message, scope tools.Scope, outputs *tooloutput.Store, profile profiles.Profile, instructions string) explore.Runner {
	harness = exploreLoopMessages(harness)
	return func(ctx context.Context, node *explore.Node) explore.Outcome {
		record := node.Record()
		history := []model.Message{{Role: "user", Content: record.Prompt}}
		findings := exploreLoopFindings{}
		publish := func(tool *explore.ToolRecord) {
			node.Update(exploreLoopMessages(history), findings.text(""), tool)
		}
		finish := func(status explore.State, reason string) explore.Outcome {
			publish(nil)
			latest := node.Record()
			// Budget denial also stops the node's context. Preserve the runtime's
			// limited/deadline attribution rather than calling it cancellation.
			switch latest.Status {
			case explore.Limited, explore.Cancelled, explore.Failed:
				status = latest.Status
				if latest.Reason != "" {
					reason = latest.Reason
				}
			default:
				if err := ctx.Err(); err != nil {
					status, reason = explore.Cancelled, err.Error()
					if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
						status, reason = explore.Limited, context.DeadlineExceeded.Error()
					}
				}
			}
			warnings := append([]string(nil), latest.Warnings...)
			if status != explore.Completed {
				warning := "Task ended without a complete final answer; available findings and tool observations may be partial."
				node.Warn(warning)
				warnings = append(warnings, warning)
			}
			return explore.Outcome{
				TaskID: latest.ID, Agent: latest.Agent, Depth: latest.Depth,
				Status: status, Findings: findings.text(""), Reason: reason,
				Warnings: warnings, Rounds: latest.Rounds,
				SpawnUsed: latest.SpawnUsed, Deadline: latest.Deadline, Usage: latest.Usage,
			}
		}
		// A profile without instructions cannot be compiled into a child
		// context; refuse before any request, fork, or budget consumption.
		if strings.TrimSpace(instructions) == "" {
			return finish(explore.Failed, "Profile instructions are unavailable; no request was made.")
		}
		publish(nil)
		if err := ctx.Err(); err != nil {
			return finish(explore.Cancelled, err.Error())
		}
		if client == nil {
			return finish(explore.Failed, "Inherited provider client is unavailable; no fallback was attempted.")
		}
		child, err := client.ForkForTask(func(requestCtx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := requestCtx.Err(); err != nil {
				return err
			}
			// The transport invokes this before each generating POST, including
			// Stream's internal retries, but not model-list or OAuth requests.
			return node.BeginRequest()
		})
		if err != nil {
			return finish(explore.Failed, "Cannot fork the inherited provider client: "+err.Error())
		}
		// The coordinator's Resolve output is the frozen effective scope
		// (parent capabilities ∩ user/mode policy ∩ profile allowlist); it is
		// used verbatim and never recomputed or widened here.
		childScope := scope
		registry, err := ExploreRegistry(repo, node, childScope)
		if err != nil {
			return finish(explore.Failed, "Cannot construct profile tools: "+err.Error())
		}
		compiled := ProfileMessages(exploreLoopMessages(harness), record, profile, instructions)
		// ProfileMessages includes the scoped user brief. Keep that one fresh
		// user message in the saved conversation, not a duplicate raw brief.
		if len(compiled) > 0 && compiled[len(compiled)-1].Role == "user" {
			history = exploreLoopMessages(compiled[len(compiled)-1:])
			compiled = compiled[:len(compiled)-1]
		}
		briefContent := history[0].Content
		publish(nil)
		definitions := registry.Definitions(childScope)

		var release func()
		dropPermit := func() {
			if release != nil {
				release()
				release = nil
			}
		}
		defer dropPermit()
		acquire := func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if release == nil {
				var err error
				release, err = node.Acquire(ctx)
				if err != nil {
					return err
				}
			}
			node.SetStatus(explore.Running)
			return ctx.Err()
		}
		if err := acquire(); err != nil {
			return finish(explore.Failed, err.Error())
		}
		// Unlike the main mention helper, these reads are cancellable, hold the
		// task's execution permit, and cannot bypass an inherited read denial.
		if repo != nil && (childScope.Allowed == nil || childScope.Allowed["read"]) {
			err := exploreLoopExpandBrief(ctx, repo, record.Prompt, node.Warn, func(path, content, expanded string) {
				history[0].Content = briefContent + strings.TrimPrefix(expanded, record.Prompt)
				findings.addEvidence("Explicit repository reference @"+path, content)
				publish(nil)
			})
			if err != nil {
				return finish(explore.Failed, err.Error())
			}
		} else if strings.Contains(record.Prompt, "@") {
			node.Warn("Explicit file references were not expanded because repository read is outside the inherited capability allowlist.")
		}

		saveResult := func(call model.ToolCall, raw tools.Result, callErr error) {
			// Retention derives its private key from task/call identity. The
			// provider's original call ID stays unchanged everywhere else.
			raw.SourceTaskID = record.ID
			result := finishToolResult(call, raw, callErr, outputs)
			content := result.ModelContent()
			history = append(history, model.Message{Role: "tool", ToolCallID: call.ID, Content: content})
			findings.addEvidence(fmt.Sprintf("%s call %s (%s), status=%s", call.Name, call.ID, utf8Prefix(call.Arguments, 512), result.Status), content)
			if result.Status == tools.Refused || result.Status == tools.Failed || result.Status == tools.Cancelled {
				node.Warn(fmt.Sprintf("Tool %s (%s) %s: %s", call.Name, call.ID, result.Status, utf8Prefix(content, 1024)))
			}
			for _, warning := range result.Warnings {
				node.Warn(warning)
			}
			publish(&explore.ToolRecord{CallID: call.ID, Name: call.Name, Arguments: call.Arguments, Result: result})
		}
		settleUnexecuted := func(calls []model.ToolCall, status tools.Status, reason string) {
			for _, call := range calls {
				var source tools.Source
				if tool, ok := registry.Lookup(call.Name); ok {
					source = tool.Source
				}
				saveResult(call, tools.Result{Status: status, Source: source, Content: "Error: action not executed; " + reason}, nil)
			}
		}

		logicalRound := 0
		for {
			if err := ctx.Err(); err != nil {
				return finish(explore.Cancelled, err.Error())
			}
			if node.Record().Rounds >= explore.MaxRequests || logicalRound >= explore.MaxRequests {
				return finish(explore.Limited, fmt.Sprintf("Model-request limit reached (%d); available findings are partial. No additional summarization request was made.", explore.MaxRequests))
			}
			if err := acquire(); err != nil {
				return finish(explore.Failed, err.Error())
			}
			request := append(exploreLoopMessages(compiled), history...)
			logicalRound++

			partial := model.Message{Role: "assistant"}
			var lastProgress time.Time
			lastBytes := 0
			clipped := false
			progress := func(target *string, chunk string) {
				remaining := exploreStreamBytes - len(*target)
				piece := utf8Prefix(strings.ToValidUTF8(chunk, "�"), remaining)
				clipped = clipped || len(piece) != len(chunk)
				*target += piece
				bytes := len(partial.Content) + len(partial.Reasoning)
				elapsed := time.Since(lastProgress)
				if bytes == lastBytes || elapsed < exploreProgressInterval || (bytes-lastBytes < 512 && elapsed < time.Second) {
					return
				}
				transcript := append(exploreLoopMessages(history), partial)
				node.Update(transcript, findings.text(partial.Content), nil)
				lastProgress, lastBytes = time.Now(), bytes
			}
			assistant, streamErr := child.Stream(ctx, request, definitions, func(text string) {
				progress(&partial.Content, text)
			}, func(reasoning string) {
				progress(&partial.Reasoning, reasoning)
			})
			// Read the fork's telemetry exactly once, even when Stream failed.
			usage, known := child.LastTokenUsage()
			node.RecordUsage(usage, known)
			assistant.Role = "assistant"
			if assistant.Content == "" || (streamErr != nil && len(partial.Content) > len(assistant.Content)) {
				assistant.Content = partial.Content
			}
			if assistant.Reasoning == "" || (streamErr != nil && len(partial.Reasoning) > len(assistant.Reasoning)) {
				assistant.Reasoning = partial.Reasoning
			}
			clipped = clipped || len(assistant.Content) > exploreStreamBytes || len(assistant.Reasoning) > exploreStreamBytes
			assistant.Content = utf8Prefix(strings.ToValidUTF8(assistant.Content, "�"), exploreStreamBytes)
			assistant.Reasoning = utf8Prefix(strings.ToValidUTF8(assistant.Reasoning, "�"), exploreStreamBytes)
			if clipped {
				node.Warn("Assistant text/reasoning exceeded the bounded child transcript; its tail was omitted.")
			}
			idErr := validateToolCallIDs(assistant.ToolCalls)
			if idErr != nil {
				// Never execute any part of a malformed batch, or persist an
				// unmatched/duplicate result protocol into the next request.
				assistant.ToolCalls = nil
				node.Warn(idErr.Error())
			}
			history = append(history, assistant)
			findings.addAnswer(assistant.Content)
			publish(nil)
			latest := node.Record()
			if streamErr != nil && latest.Rounds >= explore.MaxRequests && ctx.Err() == nil {
				settleUnexecuted(assistant.ToolCalls, tools.Limited, "model-request budget exhausted")
				return finish(explore.Limited, fmt.Sprintf("Model-request limit reached (%d): %v. Available findings are partial; no additional request was made.", explore.MaxRequests, streamErr))
			}
			if err := ctx.Err(); err != nil {
				reason := err.Error()
				if latest.Reason != "" {
					reason = latest.Reason
				}
				settleUnexecuted(assistant.ToolCalls, tools.Cancelled, reason)
				return finish(explore.Cancelled, err.Error())
			}
			if streamErr != nil {
				settleUnexecuted(assistant.ToolCalls, tools.Failed, "model request failed")
				return finish(explore.Failed, streamErr.Error())
			}
			if idErr != nil {
				return finish(explore.Failed, idErr.Error())
			}
			if len(assistant.ToolCalls) == 0 {
				return finish(explore.Completed, "")
			}

			for index := 0; index < len(assistant.ToolCalls); {
				if err := ctx.Err(); err != nil {
					settleUnexecuted(assistant.ToolCalls[index:], tools.Cancelled, "task interrupted")
					return finish(explore.Cancelled, err.Error())
				}
				call := assistant.ToolCalls[index]
				if call.Name == "task" {
					end := index + 1
					for end < len(assistant.ToolCalls) && assistant.ToolCalls[end].Name == "task" {
						end++
					}
					// Every task dispatch, including a forged/refused spawn,
					// runs without the parent slot. Siblings settle together.
					dropPermit()
					node.SetStatus(explore.Waiting)
					results := exploreLoopTaskBatch(ctx, registry, childScope, assistant.ToolCalls[index:end])
					for _, execution := range results {
						saveResult(execution.Call, execution.Result, execution.Err)
					}
					index = end
					continue
				}
				if err := acquire(); err != nil {
					settleUnexecuted(assistant.ToolCalls[index:], tools.Cancelled, "task interrupted before execution")
					return finish(explore.Failed, err.Error())
				}
				// Registry is authoritative for schema and permission refusals;
				// such results are returned to the child within its own budget.
				result, err := registry.Invoke(ctx, childScope, call)
				saveResult(call, result, err)
				index++
			}
			// A fresh FIFO acquisition starts the next model/read segment. The
			// main loop's checkpoints cannot extend this task's request budget.
			dropPermit()
		}
	}
}
