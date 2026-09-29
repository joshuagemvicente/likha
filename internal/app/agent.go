package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"

	"lisa/internal/actions"
	"lisa/internal/model"
	"lisa/internal/repository"
)

type approvalRequest struct {
	Kind, Title, Body string
	Reply             chan bool
}

type turnEvent struct {
	kind     string
	text     string
	history  []model.Message
	approval *approvalRequest
	runID    uint64
}

var agentTools = []model.ToolDefinition{
	{Name: "list_files", Description: "List immediate entries in a repository directory. Path is relative to the repository; use . for its root.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	{Name: "read_file", Description: "Read a text file inside the repository, using a repository-relative path.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	{Name: "search_files", Description: "Search repository text files for a literal string and return paths, line numbers, and matching lines.", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)},
	{Name: "edit_file", Description: "Propose replacing the full content of a repository text file (or creating a new file). The user reviews the complete diff before any write.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)},
	{Name: "run_command", Description: "Request a shell command in the repository. The exact command and working directory require explicit user approval.", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)},
}

// runTurn streams one user request and its tools. An edit or command can only
// execute after the UI sends an explicit decision for that specific proposal.
func runTurn(ctx context.Context, client *model.Client, repo *repository.Repository, root string, prior []model.Message, prompt string, emit func(turnEvent)) {
	history := make([]model.Message, 0, len(prior)+4)
	history = append(history, prior...)
	history = append(history, model.Message{Role: "user", Content: prompt})
	fail := func(err error) { emit(turnEvent{kind: "error", text: err.Error(), history: history}) }
	// Pre-run connection check: a dead endpoint or revoked key fails before any
	// prompt round-trip. A successful earlier check or stream is remembered.
	if err := client.EnsureConnected(ctx); err != nil {
		fail(err)
		return
	}

	for range 32 {
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		assistant, err := client.Stream(ctx, history, agentTools, func(text string) {
			emit(turnEvent{kind: "text", text: text})
		})
		if err != nil {
			fail(err)
			return
		}
		history = append(history, assistant)
		if len(assistant.ToolCalls) == 0 {
			emit(turnEvent{kind: "done", history: history})
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
			emit(turnEvent{kind: "tool_start", text: "Request: " + call.Name + " " + call.Arguments})
			result, toolErr := dispatchTool(ctx, repo, root, call, emit)
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
			emit(turnEvent{kind: "tool_result", text: call.Name + ": " + result, history: completed})
			if err := ctx.Err(); err != nil {
				appendUnexecuted(&history, assistant.ToolCalls[index+1:])
				fail(err)
				return
			}
		}
	}
	fail(fmt.Errorf("model exceeded 32 consecutive tool rounds"))
}

func appendUnexecuted(history *[]model.Message, calls []model.ToolCall) {
	for _, call := range calls {
		if call.ID != "" {
			*history = append(*history, model.Message{Role: "tool", ToolCallID: call.ID, Content: "Error: action not executed; run interrupted"})
		}
	}
}

func dispatchTool(ctx context.Context, repo *repository.Repository, root string, call model.ToolCall, emit func(turnEvent)) (string, error) {
	var args struct {
		Path    string  `json:"path"`
		Query   string  `json:"query"`
		Content *string `json:"content"`
		Command *string `json:"command"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return "", fmt.Errorf("invalid tool arguments: %w", err)
	}
	switch call.Name {
	case "list_files":
		paths, err := repo.List(args.Path)
		return strings.Join(paths, "\n"), err
	case "read_file":
		return repo.Read(args.Path)
	case "search_files":
		matches, err := repo.Search(args.Query)
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
		return "", fmt.Errorf("unsupported tool %q", call.Name)
	}
}

func requestApproval(ctx context.Context, kind, title, body string, emit func(turnEvent)) (bool, error) {
	request := &approvalRequest{Kind: kind, Title: title, Body: body, Reply: make(chan bool, 1)}
	emit(turnEvent{kind: "approval", approval: request})
	select {
	case approved := <-request.Reply:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
