package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"likha/internal/model"
)

type registration struct {
	tool   Tool
	schema *compiledSchema
}

// Registry is a concurrency-safe catalog and the single policy/dispatch seam.
// Construct a registry at a safe run boundary: a Scope narrows capabilities,
// but does not snapshot subsequent registrations into a frozen run catalog.
type Registry struct {
	mu      sync.RWMutex
	tools   map[string]registration
	aliases map[string]map[string]bool
}

func New() *Registry {
	return &Registry{tools: make(map[string]registration), aliases: make(map[string]map[string]bool)}
}

// Register owns copies of all catalog data. Unsupported MCP schemas remain
// cataloged as unavailable; invalid built-in schemas are registration errors.
// No execution callback is called during registration or catalog queries.
func (r *Registry) Register(tool Tool) error {
	tool = cloneTool(tool)
	name := tool.Definition.Name
	if !modelName(name) {
		return fmt.Errorf("tool name must contain 1–64 ASCII letters, digits, underscores, or hyphens")
	}
	if tool.Source.Kind != "builtin" && tool.Source.Kind != "mcp" {
		return fmt.Errorf("tool %q requires a builtin or mcp source kind", name)
	}
	if tool.Source.Tool == "" {
		if tool.Source.Kind == "mcp" {
			return fmt.Errorf("MCP tool %q requires its original tool identity", name)
		}
		tool.Source.Tool = name
	}
	if !safeIdentity(tool.Source.Tool) || (tool.Source.Server != "" && !safeIdentity(tool.Source.Server)) {
		return fmt.Errorf("tool %q has an invalid source identity", name)
	}
	if tool.Source.Kind == "mcp" && tool.Source.Server == "" {
		return fmt.Errorf("MCP tool %q requires a server identity", name)
	}
	if tool.Source.Kind != "builtin" && reservedBuiltin(name) {
		return fmt.Errorf("tool %q would shadow a built-in name", name)
	}
	if len(tool.Effects) == 0 {
		if tool.Source.Kind == "mcp" {
			// Discovery does not prove an external tool is read-only.
			tool.Effects = []Effect{Write, Exec, Network}
		} else if tool.Target == "user" || tool.Target == "session" {
			// Harness-side surfaces: user interaction and session-owned state
			// carry no repository, provider, or network side effects to
			// disclose, and stay outside the authorization gate.
		} else {
			return fmt.Errorf("tool %q must declare its effects", name)
		}
	}
	for _, effect := range tool.Effects {
		switch effect {
		case Read, Write, Exec, Network:
		default:
			return fmt.Errorf("tool %q has an unsupported effect", name)
		}
	}
	for _, alias := range tool.Aliases {
		if !safeIdentity(alias) {
			return fmt.Errorf("tool %q has an invalid alias", name)
		}
	}
	schema, err := compileSchema(tool.Definition.Parameters, tool.Source.Kind == "builtin")
	if err != nil {
		if tool.Source.Kind != "mcp" {
			return fmt.Errorf("tool %q input schema: %w", name, err)
		}
		reason := "Input schema cannot be validated safely: " + err.Error()
		if tool.UnavailableReason != "" {
			reason = tool.UnavailableReason + "; " + reason
		}
		tool.UnavailableReason = reason
	}
	if tool.UnavailableReason == "" {
		if tool.Run == nil {
			return fmt.Errorf("tool %q requires a handler or an unavailable reason", name)
		}
		if requiresAuthorization(tool) && tool.Authorize == nil {
			return fmt.Errorf("tool %q requires an authorizer", name)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[string]registration)
		r.aliases = make(map[string]map[string]bool)
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q is already registered", name)
	}
	r.tools[name] = registration{tool: tool, schema: schema}
	for _, alias := range tool.Aliases {
		if alias == name {
			continue
		}
		if r.aliases[alias] == nil {
			r.aliases[alias] = make(map[string]bool)
		}
		r.aliases[alias][name] = true
	}
	return nil
}

// Lookup resolves canonical names first, then only unambiguous aliases.
// External aliases can never acquire a reserved built-in name, even when that
// built-in is not installed. A returned tool may still be unavailable in Scope.
func (r *Registry) Lookup(name string) (Tool, bool) {
	entry, reason := r.resolve(name)
	if reason != "" {
		return Tool{}, false
	}
	return cloneTool(entry.tool), true
}

func (r *Registry) resolve(name string) (registration, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.tools[name]; ok {
		return entry, ""
	}
	candidates := r.aliases[name]
	var found registration
	count := 0
	for canonical := range candidates {
		entry := r.tools[canonical]
		if reservedBuiltin(name) && entry.tool.Source.Kind != "builtin" {
			continue
		}
		found = entry
		count++
	}
	if count > 1 {
		return registration{}, "ambiguous_tool"
	}
	if count == 0 {
		return registration{}, "unknown_tool"
	}
	return found, ""
}

func (r *Registry) snapshot() []registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]registration, 0, len(r.tools))
	for _, entry := range r.tools {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].tool.Definition.Name < entries[j].tool.Definition.Name })
	return entries
}

func (r *Registry) Definitions(scope Scope) []model.ToolDefinition {
	definitions := make([]model.ToolDefinition, 0)
	for _, entry := range r.snapshot() {
		if availability(entry.tool, scope) == "" {
			definition := entry.tool.Definition
			definition.Parameters = append(json.RawMessage(nil), definition.Parameters...)
			definitions = append(definitions, definition)
		}
	}
	return definitions
}

// Catalog includes unavailable entries, with the same policy used by Invoke.
func (r *Registry) Catalog(scope Scope) []CatalogEntry {
	entries := make([]CatalogEntry, 0)
	for _, entry := range r.snapshot() {
		tool := entry.tool
		reason := availability(tool, scope)
		entries = append(entries, CatalogEntry{
			Name: tool.Definition.Name, Description: tool.Definition.Description,
			Target: tool.Target, Source: tool.Source, Effects: append([]Effect(nil), tool.Effects...),
			ParallelSafe: tool.ParallelSafe, Interactive: tool.Interactive,
			Available: reason == "", Reason: reason,
		})
	}
	return entries
}

// Invoke validates identity, eligibility, and arguments before authorization.
// It returns full handler content so the caller can retain its private artifact
// before calling ModelContent or BoundResult. Even on error, Result is usable.
func (r *Registry) Invoke(ctx context.Context, scope Scope, call model.ToolCall) (Result, error) {
	entry, lookupError := r.resolve(call.Name)
	source := entry.tool.Source
	if ctx == nil {
		return callFailure(call.Name, source, Refused, "invalid_call", "A call context is required.", nil)
	}
	if err := ctx.Err(); err != nil {
		return cancelledCall(call.Name, source, err)
	}
	if !validCallIdentity(call.ID) || !validCallIdentity(call.Name) {
		return callFailure(call.Name, source, Refused, "invalid_call", "A nonempty UTF-8 call ID and tool name are required.", nil)
	}
	if lookupError != "" {
		message := "This tool is not registered; choose an available canonical tool name."
		if lookupError == "ambiguous_tool" {
			message = "This legacy tool alias is ambiguous; use a server-qualified canonical name."
		}
		return callFailure(call.Name, source, Refused, lookupError, message, nil)
	}
	tool := entry.tool
	if reason := availability(tool, scope); reason != "" {
		return callFailure(call.Name, source, Refused, "tool_unavailable", reason, nil)
	}
	arguments := json.RawMessage(call.Arguments)
	value, err := decodeJSON(arguments)
	if err == nil {
		if _, ok := value.(map[string]any); !ok {
			err = errors.New("arguments must be a JSON object")
		}
	}
	if err == nil {
		err = entry.schema.validate(ctx, value)
	}
	if ctx.Err() != nil {
		return cancelledCall(call.Name, source, ctx.Err())
	}
	if err != nil {
		return callFailure(call.Name, source, Refused, "invalid_arguments", "Invalid tool arguments: "+err.Error(), err)
	}
	if tool.Authorize != nil {
		// The authorizer cannot mutate the validated payload handed to Run.
		err = authorizeTool(ctx, tool.Authorize, append(json.RawMessage(nil), arguments...))
		if ctx.Err() != nil {
			return cancelledCall(call.Name, source, ctx.Err())
		}
		if err != nil {
			if isCancellation(err) {
				return cancelledCall(call.Name, source, err)
			}
			return callFailure(call.Name, source, Refused, "authorization_refused", "Authorization was not granted; no handler was executed.", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return cancelledCall(call.Name, source, err)
	}
	result, err := runTool(ctx, tool.Run, append(json.RawMessage(nil), arguments...))
	result.Executed = true
	// Registration is authoritative provenance, including when a handler fails.
	result.Source = source
	if isCancellation(err) || (ctx.Err() != nil && err != nil) {
		cause := err
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		result.Status = Cancelled
		if result.Content == "" {
			result.Content = "cancelled: Tool execution was cancelled."
		}
		if requiresAuthorization(tool) {
			result.Warnings = append(result.Warnings, "Cancellation does not undo effects already performed.")
		}
		return result, &CallError{Code: "cancelled", Tool: call.Name, Status: Cancelled, Message: "Tool execution was cancelled.", Cause: cause}
	}
	if ctx.Err() != nil {
		result.Warnings = append(result.Warnings, "Run cancellation arrived after this handler completed; recorded effects are not undone.")
	}
	if !validStatus(result.Status) {
		if result.Status != "" {
			err = errors.New("handler returned an invalid status")
		}
		if err != nil {
			result.Status = Failed
		} else {
			result.Status = Succeeded
		}
	}
	if err != nil {
		if result.Status == Succeeded {
			result.Status = Failed
		}
		if result.Content == "" {
			result.Content = "tool_failed: Tool execution failed; inspect the operation before retrying."
		}
		return result, &CallError{Code: "tool_failed", Tool: call.Name, Status: result.Status, Message: "Tool execution failed; inspect the operation before retrying.", Cause: err}
	}
	if result.Truncated && result.Status == Succeeded {
		result.Status = Limited
	}
	return result, nil
}

// isChildAgentScope reports whether a scope belongs to a spawned child agent
// (explore, review, or a discovered profile) rather than the main run. Child
// scopes carry the repository-inspection ceiling regardless of profile name.
func isChildAgentScope(scope Scope) bool {
	agent := strings.TrimSpace(scope.Agent)
	return agent != "" && !strings.EqualFold(agent, "main")
}

func availability(tool Tool, scope Scope) string {
	name := tool.Definition.Name
	if scope.Allowed != nil && !scope.Allowed[name] {
		return "This tool is outside the run's capability allowlist."
	}
	if isChildAgentScope(scope) {
		permitted := tool.Source.Kind == "builtin" && (name == "glob" || name == "read" || name == "grep" || (name == "task" && scope.Allowed != nil && scope.Allowed[name]))
		for _, effect := range tool.Effects {
			permitted = permitted && effect == Read
		}
		if !permitted {
			return "Child agents are limited to repository inspection and explicitly permitted tasks."
		}
	}
	if strings.EqualFold(scope.Mode, "plan") {
		if tool.Source.Kind == "mcp" {
			return "MCP tools are unavailable in plan mode, including trusted servers."
		}
		for _, effect := range tool.Effects {
			if effect == Exec || (effect == Write && tool.Target != "session") {
				return "Plan mode does not permit workspace changes or command execution."
			}
		}
	}
	if tool.UnavailableReason != "" {
		return tool.UnavailableReason
	}
	if tool.Run == nil || (requiresAuthorization(tool) && tool.Authorize == nil) {
		return "Required execution or authorization backend is unavailable."
	}
	return ""
}

func requiresAuthorization(tool Tool) bool {
	if tool.Source.Kind == "mcp" {
		return true
	}
	for _, effect := range tool.Effects {
		if effect == Write || effect == Exec || effect == Network {
			return true
		}
	}
	return false
}

func modelName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func safeIdentity(s string) bool { return len(s) <= 1024 && validCallIdentity(s) }

func validCallIdentity(s string) bool {
	if strings.TrimSpace(s) == "" || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func reservedBuiltin(name string) bool {
	switch name {
	case "glob", "read", "grep", "edit_file", "edit", "run_command", "read_output", "task", "ask_user", "plan_update", "skill", "web_search", "web_fetch":
		return true
	}
	return false
}

func cloneTool(tool Tool) Tool {
	tool.Definition.Parameters = append(json.RawMessage(nil), tool.Definition.Parameters...)
	tool.Effects = append([]Effect(nil), tool.Effects...)
	tool.Aliases = append([]string(nil), tool.Aliases...)
	return tool
}

func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func cancelledCall(name string, source Source, cause error) (Result, error) {
	return callFailure(name, source, Cancelled, "cancelled", "Tool call was cancelled.", cause)
}

func authorizeTool(ctx context.Context, authorize Authorizer, arguments json.RawMessage) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("authorization backend failed")
		}
	}()
	return authorize(ctx, arguments)
}

func runTool(ctx context.Context, run Handler, arguments json.RawMessage) (result Result, err error) {
	defer func() {
		if recover() != nil {
			result = Result{Status: Failed}
			err = errors.New("tool backend failed")
		}
	}()
	return run(ctx, arguments)
}

func validStatus(status Status) bool {
	switch status {
	case Succeeded, Refused, Failed, Cancelled, Limited:
		return true
	}
	return false
}
