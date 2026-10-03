package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"

	"likha/internal/actions"
	"likha/internal/mcp"
	"likha/internal/model"
	"likha/internal/repository"
)

type ApprovalRequest struct {
	Kind, Title, Body string
	Reply             chan bool
}

type TurnEvent struct {
	Kind             string
	Text             string
	History          []model.Message
	Approval         *ApprovalRequest
	RunID            uint64
	ContextTokens    int64
	ContextKnown     bool
	ContextEstimated bool
}

var agentTools = []model.ToolDefinition{
	{Name: "glob", Description: "Match repository file paths by glob pattern: `*`, `?`, and `[...]` match within one path segment (never across `/`), a bare `**` segment spans zero or more directories, and only regular files match. Pattern is repository-relative. Returns matching paths sorted, one per line.", Parameters: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`)},
	{Name: "read", Description: "Read a text file inside the repository, using a repository-relative path (UTF-8 text, up to 1 MiB).", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	{Name: "grep", Description: "Search repository text files with a regular expression (Go regexp syntax, per line; case-sensitive unless the pattern uses (?i)). Returns `path:line: text` for each match. Binary files and symlinks are skipped.", Parameters: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`)},
	{Name: "edit_file", Description: "Propose replacing the full content of a repository text file (or creating a new file). The user reviews the complete diff before any write.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)},
	{Name: "run_command", Description: "Request a shell command in the repository. The exact command and working directory require explicit user approval.", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)},
}

// runTurn streams one user request and its tools. An edit or command can only
// execute after the UI sends an explicit decision for that specific proposal.
// mcp may be nil when no MCP servers are configured.
func RunTurn(ctx context.Context, client *model.Client, repo *repository.Repository, root string, prior []model.Message, prompt string, mcpServers *mcp.McpManager, steer <-chan string, emit func(TurnEvent)) {
	// @file references inline repository content so the model reads context
	// directly; unresolved tokens stay literal.
	prompt = ExpandFileReferences(prompt, repo)
	history := make([]model.Message, 0, len(prior)+4)
	history = append(history, prior...)
	history = append(history, model.Message{Role: "user", Content: prompt})
	fail := func(err error) { emit(TurnEvent{Kind: "error", Text: err.Error(), History: history}) }
	// applySteer folds drained steering prompts into the private history and
	// reports each one to the UI, oldest first.
	applySteer := func(drained []string) {
		for _, text := range drained {
			history = append(history, model.Message{Role: "user", Content: ExpandFileReferences(text, repo)})
			emit(TurnEvent{Kind: "steer", Text: text, History: append([]model.Message(nil), history...)})
		}
	}
	// Pre-run connection check: a dead endpoint or revoked key fails before any
	// prompt round-trip. A successful earlier check or stream is remembered.
	if err := client.EnsureConnected(ctx); err != nil {
		fail(err)
		return
	}
	tools := agentTools
	if extra := mcpServers.Tools(model.UserAgent); len(extra) > 0 {
		tools = append(append([]model.ToolDefinition(nil), agentTools...), extra...)
	}

	for range 32 {
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		// Drain point (a): prompts queued while the previous round ran join the
		// history before the next provider call. Never drains on cancel, above.
		applySteer(drainSteer(steer))
		// Refresh the visible context estimate for this exact outbound request.
		contextTokens, contextKnown := model.EstimateInputTokens(client.Model(), history, tools)
		emit(TurnEvent{Kind: "context", ContextTokens: contextTokens, ContextKnown: contextKnown, ContextEstimated: true})
		assistant, err := client.Stream(ctx, history, tools, func(text string) {
			emit(TurnEvent{Kind: "text", Text: text})
		}, func(reasoning string) {
			emit(TurnEvent{Kind: "reasoning", Text: reasoning})
		})
		if err != nil {
			fail(err)
			return
		}
		if usage, ok := client.LastTokenUsage(); ok && usage.PromptSeen {
			emit(TurnEvent{Kind: "context", ContextTokens: usage.Prompt, ContextKnown: true, ContextEstimated: false})
		}
		history = append(history, assistant)
		if len(assistant.ToolCalls) == 0 {
			// Drain point (b): a prompt queued during this final stream keeps the
			// run alive for another provider round instead of ending it. A run
			// cancelled after the response completed must not consume the queue
			// (FR-21: cancellation holds queued prompts for the user).
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
		for index, call := range assistant.ToolCalls {
			if err := ctx.Err(); err != nil {
				appendUnexecuted(&history, assistant.ToolCalls[index:])
				fail(err)
				return
			}
			if call.ID == "" {
				fail(fmt.Errorf("model returned a tool call without an ID"))
				return
			}
			emit(TurnEvent{Kind: "tool_start", Text: "Request: " + call.Name + " " + call.Arguments})
			result, toolErr := dispatchTool(ctx, repo, root, call, mcpServers, emit)
			if ctx.Err() != nil && (errors.Is(toolErr, context.Canceled) || errors.Is(toolErr, context.DeadlineExceeded)) {
				appendUnexecuted(&history, assistant.ToolCalls[index:])
				fail(ctx.Err())
				return
			}
			if toolErr != nil {
				result = "Error: " + toolErr.Error()
			}
			history = append(history, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
			completed := append([]model.Message(nil), history...)
			appendUnexecuted(&completed, assistant.ToolCalls[index+1:])
			emit(TurnEvent{Kind: "tool_result", Text: call.Name + ": " + result, History: completed})
			if err := ctx.Err(); err != nil {
				appendUnexecuted(&history, assistant.ToolCalls[index+1:])
				fail(err)
				return
			}
		}
	}
	fail(fmt.Errorf("model exceeded 32 consecutive tool rounds"))
}

// drainSteer performs a non-blocking FIFO drain of prompts queued while a run
// is active. A nil steer channel (steering disabled) is safe: the receive case
// is never ready, so the default branch returns immediately without blocking.
func drainSteer(steer <-chan string) []string {
	var drained []string
	for {
		select {
		case text, ok := <-steer:
			if !ok {
				return drained
			}
			drained = append(drained, text)
		default:
			return drained
		}
	}
}

func appendUnexecuted(history *[]model.Message, calls []model.ToolCall) {
	for _, call := range calls {
		if call.ID != "" {
			*history = append(*history, model.Message{Role: "tool", ToolCallID: call.ID, Content: "Error: action not executed; run interrupted"})
		}
	}
}

func dispatchTool(ctx context.Context, repo *repository.Repository, root string, call model.ToolCall, mcpServers *mcp.McpManager, emit func(TurnEvent)) (string, error) {
	var args struct {
		Path    string  `json:"path"`
		Pattern string  `json:"pattern"`
		Content *string `json:"content"`
		Command *string `json:"command"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return "", fmt.Errorf("invalid tool arguments: %w", err)
	}
	switch call.Name {
	case "glob":
		paths, err := repo.Glob(args.Pattern)
		return strings.Join(paths, "\n"), err
	case "read":
		return repo.Read(args.Path)
	case "grep":
		matches, err := repo.Grep(args.Pattern)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		for _, match := range matches {
			fmt.Fprintf(&out, "%s:%d: %s\n", match.Path, match.Line, match.Text)
		}
		return out.String(), nil
	case "edit_file":
		if args.Content == nil {
			return "", fmt.Errorf("edit_file requires content")
		}
		edit, err := actions.PrepareEdit(root, args.Path, *args.Content)
		if err != nil {
			return "", err
		}
		approved, err := requestApproval(ctx, "edit", "Edit: "+edit.Path, edit.Diff, emit)
		if err != nil {
			return "", err
		}
		if !approved {
			return "", fmt.Errorf("edit rejected by user")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := edit.Apply(); err != nil {
			return "", err
		}
		return "Applied edit to " + edit.Path, nil
	case "run_command":
		if args.Command == nil || strings.TrimSpace(*args.Command) == "" {
			return "", fmt.Errorf("run_command requires a command")
		}
		for _, r := range *args.Command {
			if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) || runewidth.RuneWidth(r) == 0 || r == '\u2028' || r == '\u2029' || r == '\ufeff' {
				return "", fmt.Errorf("run_command rejects invisible or control characters; use printable shell text")
			}
		}
		body := "Working directory: " + root + "\nCommand:\n" + *args.Command + "\n\nApproved commands can access files outside this repository and use the network. Detached processes may outlive cancellation."
		approved, err := requestApproval(ctx, "command", "Shell command", body, emit)
		if err != nil {
			return "", err
		}
		if !approved {
			return "", fmt.Errorf("command rejected by user")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		result, err := actions.RunCommand(ctx, root, *args.Command)
		if err != nil {
			return "", err
		}
		output := fmt.Sprintf("Exit status: %d\n%s", result.ExitCode, result.Output)
		if result.ExitCode != 0 {
			return "", fmt.Errorf("command failed: %s", output)
		}
		return output, nil
	default:
		// Unknown to the built-in switch: an MCP tool, if any server exposes
		// one. Trust-on-first-use: the first call from a server is approved
		// explicitly; afterwards that server's tools run for the session.
		result, isError, err := mcpServers.Call(ctx, call.Name, json.RawMessage(call.Arguments), func(server, tool, arguments string) bool {
			body := fmt.Sprintf("MCP server: %s\nTool: %s\nArguments: %s\n\nApproving trusts this server's tools for the rest of this session. MCP servers run on your machine with your permissions.", server, tool, arguments)
			approved, approveErr := requestApproval(ctx, "mcp", "MCP tool", body, emit)
			return approveErr == nil && approved
		})
		if err != nil {
			return "", err
		}
		if isError {
			return "", fmt.Errorf("MCP tool %s failed: %s", call.Name, result)
		}
		return result, nil
	}
}

func requestApproval(ctx context.Context, kind, title, body string, emit func(TurnEvent)) (bool, error) {
	request := &ApprovalRequest{Kind: kind, Title: title, Body: body, Reply: make(chan bool, 1)}
	emit(TurnEvent{Kind: "approval", Approval: request})
	select {
	case approved := <-request.Reply:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
