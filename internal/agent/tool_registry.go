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
	"likha/internal/explore"
	"likha/internal/mcp"
	"likha/internal/model"
	"likha/internal/profiles"
	"likha/internal/repository"
	"likha/internal/tooloutput"
	"likha/internal/tools"
	"likha/internal/webtools"
)

type RunOptions struct {
	Outputs             *tooloutput.Store
	EditJournal         func(*actions.EditProposal) error
	TasksEnabled        bool
	SessionID, Provider string
	Scope               tools.Scope
	SaveTask            func(explore.Record) error
	TaskSpawn           func(context.Context, string, explore.Spec) (explore.Outcome, error)
	// PlanMode is a live per-session read-only state, never persisted. It
	// gates mutation dispatch at turn boundaries, not mid-run.
	PlanMode bool
	// Ask brokers one interactive question; PlanApply persists a checklist
	// replacement; SkillLoad loads one discovered skill; WebSearch runs a
	// consent-gated configured search. Nil hooks register the tool with its
	// own unavailable reason or handler refusal.
	SkillAdvert string
	Ask         func(context.Context, string, tools.AskRequest) (tools.AskAnswer, error)
	PlanApply   func(context.Context, []tools.PlanStep) error
	SkillLoad   func(context.Context, string) (tools.SkillBody, error)
	WebSearch   func(context.Context, webtools.SearchRequest) (webtools.SearchOutcome, error)
	WebFetch    func(context.Context, webtools.FetchRequest) (webtools.FetchOutcome, error)
	// TaskAgents is the frozen run catalog advertised by the task tool
	// (explore, review, and discovered profile names, sorted). TaskProfiles
	// carries the accepted profiles with their drift-checked instructions;
	// the runtime builds one runner per profile.
	TaskAgents   []string
	TaskProfiles []TaskProfile
}

// TaskProfile is one accepted, drift-checked profile ready to run.
type TaskProfile struct {
	Profile      profiles.Profile
	Instructions string
}

func ToolCatalog(repo *repository.Repository, root string, servers *mcp.McpManager, options RunOptions) []tools.CatalogEntry {
	if options.TasksEnabled && options.SaveTask != nil {
		// Catalog inspection installs a non-executing adapter, not a runtime.
		options.TaskSpawn = func(context.Context, string, explore.Spec) (explore.Outcome, error) {
			return explore.Outcome{}, errors.New("catalog inspection cannot launch tasks")
		}
	}
	registry, _ := newToolRegistry(repo, root, servers, options, func(TurnEvent) {})
	return registry.Catalog(mainScope(options.Scope))
}

// RegistryForOptions exposes the composed registry for the walkthrough probe
// and future inspection surfaces; servers may be nil.
func RegistryForOptions(repo *repository.Repository, root string, options RunOptions) (*tools.Registry, []string) {
	return newToolRegistry(repo, root, nil, options, func(TurnEvent) {})
}

func newToolRegistry(repo *repository.Repository, root string, servers *mcp.McpManager, options RunOptions, emit func(TurnEvent)) (*tools.Registry, []string) {
	outputs := options.Outputs
	registry := tools.New()
	var warnings []string
	register := func(tool tools.Tool) {
		if options.PlanMode && planModeGates(tool) {
			// Registration with an unavailable reason hides the definition
			// from model requests while every attempt still resolves to a
			// refusal naming the mode; no approval flow can start (Register
			// skips the handler/authorizer requirement for unavailable
			// tools), and the /tools catalog shows the same reason.
			tool.Run = nil
			tool.Authorize = nil
			tool.Aliases = nil
			tool.UnavailableReason = "refused (plan mode): " + tool.Definition.Name +
				" is blocked while plan mode is active; toggle /plan off to restore the approval flow"
		}
		if err := registry.Register(tool); err != nil {
			warnings = append(warnings, "Tool "+tool.Definition.Name+" unavailable: "+err.Error())
		}
	}
	readSchema := `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`
	globSchema := `{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"include_hidden":{"type":"boolean"},"include_ignored":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":1000},"cursor":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`
	grepSchema := `{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"include":{"type":"string"},"literal":{"type":"boolean"},"case_sensitive":{"type":"boolean"},"include_hidden":{"type":"boolean"},"include_ignored":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`
	for _, name := range []string{"glob", "read", "grep"} {
		name := name
		description, schema := "", ""
		switch name {
		case "read":
			description, schema = "Read a repository-relative UTF-8 file or list a directory. Use 1-based offset and limit for numbered file ranges; omitted range fields preserve whole-file text. Output is bounded with explicit continuation information.", readSchema
		case "glob":
			description, schema = "Find sorted repository-relative file paths with glob syntax (*, ?, [...], **). Narrow with path; discovery respects ignores and hidden defaults unless explicitly included. Bounded results identify incomplete scans.", globSchema
		case "grep":
			description, schema = "Search repository files by Go regexp or literal text, with optional path/include/case filters. Returns path:line: text. Discovery respects ignores/hidden defaults; skipped files and limits are disclosed.", grepSchema
		}
		tool := tools.Tool{Definition: model.ToolDefinition{Name: name, Description: description, Parameters: json.RawMessage(schema)}, Source: tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Read}, Target: "repository", ParallelSafe: true}
		if repo == nil {
			tool.UnavailableReason = "No repository configured"
		}
		tool.Run = func(ctx context.Context, input json.RawMessage) (tools.Result, error) {
			var args struct {
				Path, Pattern, Include, Cursor string
				Offset, Limit                  int
				Literal                        bool
				CaseSensitive                  *bool `json:"case_sensitive"`
				IncludeHidden                  bool  `json:"include_hidden"`
				IncludeIgnored                 bool  `json:"include_ignored"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return tools.Result{}, err
			}
			var page repository.Page
			var err error
			if name == "read" {
				page, err = repo.ReadPage(ctx, args.Path, repository.ReadOptions{Offset: args.Offset, Limit: args.Limit})
			} else {
				options := repository.QueryOptions{Path: args.Path, Include: args.Include, Literal: args.Literal, CaseSensitive: args.CaseSensitive, IncludeHidden: args.IncludeHidden, IncludeIgnored: args.IncludeIgnored, Limit: args.Limit, Cursor: args.Cursor}
				if name == "glob" {
					page, err = repo.GlobPage(ctx, args.Pattern, options)
				} else {
					page, err = repo.GrepPage(ctx, args.Pattern, options)
				}
			}
			status := tools.Succeeded
			if page.Truncated || len(page.Warnings) > 0 {
				status = tools.Limited
			}
			return tools.Result{Status: status, Content: page.Content, Truncated: page.Truncated, Warnings: page.Warnings, NextOffset: page.NextOffset, Cursor: page.Cursor}, err
		}
		register(tool)
	}

	var prepared *actions.Edit
	register(tools.Tool{
		Definition: model.ToolDefinition{Name: "edit_file", Description: "Propose replacing the full content of a repository text file (or creating a new file). The user reviews the complete diff before any write.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)},
		Source:     tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Write}, Target: "repository", Interactive: true,
		Authorize: func(ctx context.Context, input json.RawMessage) error {
			prepared = nil
			var args struct{ Path, Content string }
			if err := json.Unmarshal(input, &args); err != nil {
				return err
			}
			proposal, err := actions.PrepareEdit(root, args.Path, args.Content)
			if err != nil {
				return err
			}
			approved, err := requestApproval(ctx, "edit", "Edit: "+proposal.Path, proposal.Diff, emit)
			if err != nil {
				return err
			}
			if !approved {
				return errors.New("edit rejected by user")
			}
			prepared = proposal
			return nil
		},
		Run: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			if err := ctx.Err(); err != nil {
				return tools.Result{}, err
			}
			if prepared == nil {
				return tools.Result{}, errors.New("edit has no approved proposal")
			}
			if err := prepared.Apply(); err != nil {
				return tools.Result{}, err
			}
			return tools.Result{Content: "Applied edit to " + prepared.Path}, nil
		},
	})
	var batch *actions.EditProposal
	editUnavailable := ""
	if options.EditJournal == nil {
		editUnavailable = "Private session recovery journaling is unavailable; exact multi-file edits are disabled"
	}
	register(tools.Tool{
		Definition: model.ToolDefinition{Name: "edit", Description: "Propose a bounded list of exact-text replacements and new files. Replacements must uniquely match original content without overlap. All paths and required directories appear in one complete diff review; nothing writes before explicit approval.", Parameters: json.RawMessage(`{"type":"object","properties":{"operations":{"type":"array","minItems":1,"maxItems":64,"items":{"oneOf":[{"type":"object","properties":{"kind":{"type":"string","enum":["replace"]},"path":{"type":"string"},"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"}},"required":["kind","path","old_text","new_text"],"additionalProperties":false},{"type":"object","properties":{"kind":{"type":"string","enum":["create"]},"path":{"type":"string"},"content":{"type":"string"}},"required":["kind","path","content"],"additionalProperties":false}]}}},"required":["operations"],"additionalProperties":false}`)},
		Source:     tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Write}, Target: "repository", Interactive: true,
		UnavailableReason: editUnavailable,
		Authorize: func(ctx context.Context, input json.RawMessage) error {
			batch = nil
			if len(input) > 4<<20 {
				return errors.New("edit arguments exceed four MiB")
			}
			var args struct {
				Operations []actions.EditOperation `json:"operations"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return err
			}
			proposal, err := actions.PrepareEdits(root, args.Operations)
			if err != nil {
				return err
			}
			body := "Affected files:\n" + strings.Join(proposal.Paths, "\n") + "\n"
			if len(proposal.Directories) > 0 {
				body += "\nRequired new directories:\n" + strings.Join(proposal.Directories, "\n") + "\n"
			}
			body += "\n" + proposal.Diff
			approved, err := requestApproval(ctx, "edit", "Review complete file change", body, emit)
			if err != nil {
				return err
			}
			if !approved {
				return errors.New("edit rejected by user")
			}
			batch = proposal
			return nil
		},
		Run: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			if batch == nil {
				return tools.Result{}, errors.New("edit has no approved proposal")
			}
			if err := options.EditJournal(batch); err != nil {
				return tools.Result{}, errors.New("private edit recovery journal unavailable; no file changes applied")
			}
			content, err := batch.Apply(ctx)
			return tools.Result{Content: content}, err
		},
	})
	register(tools.Tool{
		Definition: model.ToolDefinition{Name: "run_command", Description: "Request a shell command in the repository. The exact command and working directory require explicit user approval.", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","minLength":1}},"required":["command"],"additionalProperties":false}`)},
		Source:     tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Exec}, Target: "host", Interactive: true,
		Authorize: func(ctx context.Context, input json.RawMessage) error {
			var args struct{ Command string }
			if err := json.Unmarshal(input, &args); err != nil {
				return err
			}
			if err := validateCommand(args.Command); err != nil {
				return err
			}
			body := "Working directory: " + root + "\nCommand:\n" + args.Command + "\n\nApproved commands can access files outside this repository and use the network. Detached processes may outlive cancellation."
			approved, err := requestApproval(ctx, "command", "Shell command", body, emit)
			if err != nil {
				return err
			}
			if !approved {
				return errors.New("command rejected by user")
			}
			return nil
		},
		Run: func(ctx context.Context, input json.RawMessage) (tools.Result, error) {
			var args struct{ Command string }
			if err := json.Unmarshal(input, &args); err != nil {
				return tools.Result{}, err
			}
			command, err := actions.RunCommandCaptured(ctx, root, args.Command, 5<<20)
			content := command.Captured
			if content == "" {
				content = command.Output
			}
			result := tools.Result{Content: fmt.Sprintf("Exit status: %d\n%s", command.ExitCode, strings.ToValidUTF8(content, "�")), Truncated: command.Truncated}
			if command.Truncated {
				result.Status = tools.Limited
				result.Warnings = []string{fmt.Sprintf("Command output capture reached its limit; discarded %d bytes. Exit status and payload completeness are separate.", command.Discarded)}
			}
			if err != nil {
				return result, err
			}
			if command.ExitCode != 0 {
				return result, fmt.Errorf("command failed with exit status %d", command.ExitCode)
			}
			return result, nil
		},
	})
	outputTool := tools.Tool{Definition: model.ToolDefinition{Name: "read_output", Description: "Read numbered pages of a retained output artifact belonging to this session. artifact_id is an opaque reference, not a file path or URL.", Parameters: json.RawMessage(`{"type":"object","properties":{"artifact_id":{"type":"string","minLength":1},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1}},"required":["artifact_id"],"additionalProperties":false}`)}, Source: tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Read}, Target: "session", ParallelSafe: true}
	if outputs == nil {
		outputTool.UnavailableReason = "No private session output store is available"
	}
	outputTool.Run = func(ctx context.Context, input json.RawMessage) (tools.Result, error) {
		var args struct {
			ArtifactID string `json:"artifact_id"`
			Offset     int    `json:"offset"`
			Limit      int    `json:"limit"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return tools.Result{}, err
		}
		if err := ctx.Err(); err != nil {
			return tools.Result{}, err
		}
		page, err := outputs.Read(args.ArtifactID, args.Offset, args.Limit)
		return tools.Result{Content: page.Content, NextOffset: page.NextOffset, Truncated: page.Truncated, Warnings: page.Warnings}, err
	}
	register(outputTool)
	taskUnavailable := ""
	if options.TaskSpawn == nil || !options.TasksEnabled || options.SaveTask == nil {
		taskUnavailable = "Nested exploration requires an active model and private session task persistence"
	}
	register(TaskTool(options.TaskSpawn, taskUnavailable, options.TaskAgents...))
	register(tools.AskUserTool(options.Ask))
	planTool := tools.PlanUpdateTool(options.PlanApply)
	if options.PlanApply == nil {
		planTool.UnavailableReason = "Plan checklist persistence is unavailable"
	}
	register(planTool)
	skillTool := tools.SkillTool(options.SkillLoad)
	if options.SkillLoad == nil {
		skillTool.UnavailableReason = "Skills are unavailable"
	}
	register(skillTool)
	for _, item := range servers.CatalogTools(model.UserAgent) {
		item := item
		register(tools.Tool{
			Definition: item.Definition, Source: tools.Source{Kind: "mcp", Server: item.SourceServer, Tool: item.OriginalName}, Effects: []tools.Effect{tools.Write, tools.Exec, tools.Network}, Target: "external", Interactive: true, Aliases: item.Aliases,
			Authorize: func(ctx context.Context, input json.RawMessage) error {
				return servers.AuthorizeTool(ctx, item.SourceServer, item.OriginalName, string(input), func(server, tool, arguments string) bool {
					body := fmt.Sprintf("MCP server: %s\nTool: %s\nArguments: %s\n\nApproving trusts this server's tools until app relaunch, not just this call. MCP servers run with your permissions; local cancellation may not stop server work.", server, tool, arguments)
					approved, err := requestApproval(ctx, "mcp", "MCP tool", body, emit)
					return err == nil && approved
				})
			},
			Run: func(ctx context.Context, input json.RawMessage) (tools.Result, error) {
				content, isError, err := servers.CallTool(ctx, item.SourceServer, item.OriginalName, input)
				if err == nil && isError {
					err = fmt.Errorf("MCP tool %s from %s failed", item.OriginalName, item.SourceServer)
				}
				return tools.Result{Content: content}, err
			},
		})
	}
	register(tools.WebSearchTool(options.WebSearch))
	register(tools.WebFetchTool(options.WebFetch))
	return registry, warnings
}

func validateCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return errors.New("run_command requires a command")
	}
	for _, r := range command {
		if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) || runewidth.RuneWidth(r) == 0 || r == '\u2028' || r == '\u2029' || r == '\ufeff' {
			return errors.New("run_command rejects invisible or control characters; use printable shell text")
		}
	}
	return nil
}
