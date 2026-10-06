package agent

import (
	"context"
	"errors"
	"sync/atomic"

	"likha/internal/explore"
	"likha/internal/mcp"
	"likha/internal/model"
	"likha/internal/repository"
	"likha/internal/tools"
)

type ApprovalRequest struct {
	Kind, Title, Body string
	// Warning, when set, renders as a highlighted line above the body: the
	// reason a command always asks (specs/command-permissions).
	Warning string
	// Remember offers a third decision-bar button: RememberSession allows
	// the exact command for the rest of the session, RememberTrust trusts
	// the repository's checks. Empty offers only Approve and Decline.
	Remember string
	// Remembered is set by the UI before it sends true on Reply when the
	// user chose the Remember button; the channel send orders the write
	// before the agent's read.
	Remembered bool
	Reply      chan bool
}

// Remember options for ApprovalRequest.Remember.
const (
	RememberSession = "session" // command: "Approve always" (cmdpolicy grant scope)
	RememberTrust   = "trust"   // verification: "Trust repo checks"
	RememberEdits   = "edits"   // edit: "Approve always" for the session's edits
	RememberServer  = "server"  // MCP: "Approve always" trusts the server for the session
)

// AutoApprovalReason names a call that ran under an Approve always grant in
// the "Approval: auto-approved (<reason>)" result line (specs/approve-always).
const AutoApprovalReason = "approved always for this session"

// EditGrant is the session's "Approve always" for repository edits. The UI
// owns one per session (replaced on session switch); edit tools read it from
// tool goroutines. A nil *EditGrant is never granted; Allow on nil is a no-op.
type EditGrant struct{ on atomic.Bool }

// Allow grants every later repository edit in the session.
func (g *EditGrant) Allow() {
	if g != nil {
		g.on.Store(true)
	}
}

// Allowed reports whether the session's edits run without a review.
func (g *EditGrant) Allowed() bool {
	return g != nil && g.on.Load()
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
	ToolCall         *model.ToolCall
	ToolResult       *tools.Result
	Task             *explore.Record
	TaskRuntime      *explore.Manager
	Ask              *AskInteraction
	Consent          *ConsentRequest
	// Usage is one model request's breakdown (Kind "usage"), emitted after
	// every successful request of a run so spend counts each tool round.
	// A "stream_interrupted" event (steer now) carries the stopped request's
	// usage when the provider reported any, and nil when it did not.
	Usage *model.RequestUsage
}

// ConsentRequest asks for one conversation-scoped web grant. Search consents
// bind to a configured backend, fetch consents to a canonical origin; the
// reply channel has room for one send and the run context terminates the
// wait. A decline performs no request toward the pending target.
type ConsentRequest struct {
	Kind    string // "search-backend" or "fetch-origin"
	Backend string
	Query   string
	Origin  string
	URL     string
	Privacy string // retention and data-flow disclosure shown before the decision
	Reply   chan bool
}

// ScopeKey identifies the granted scope within one conversation.
func (c *ConsentRequest) ScopeKey() string {
	if c.Kind == "fetch-origin" {
		return "fetch-origin:" + c.Origin
	}
	return "search-backend:" + c.Backend
}

// AskInteraction carries one model question to the interactive UI with its
// exactly-once reply channel. The tool dispatcher blocks on Reply until the
// UI answers, skips, or the run context terminates; the reply channel has
// room for one send so a late or stale UI never blocks Update.
type AskInteraction struct {
	ID      string // delivery identity within this run
	CallID  string // exact provider tool-call attribution
	Request tools.AskRequest
	Reply   chan tools.AskAnswer
}

// runTurn streams one user request and its tools. An edit or command can only
// execute after the UI sends an explicit decision for that specific proposal.
// mcp may be nil when no MCP servers are configured.
func RunTurn(ctx context.Context, client *model.Client, repo *repository.Repository, root string, prior []model.Message, prompt string, mcpServers *mcp.McpManager, steer <-chan string, emit func(TurnEvent)) {
	RunTurnWithOptions(ctx, client, repo, root, prior, prompt, mcpServers, steer, RunOptions{}, emit)
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
	registry, _ := newToolRegistry(repo, root, mcpServers, RunOptions{}, emit)
	if call.ID == "" {
		call.ID = "direct-call"
	}
	result, err := registry.Invoke(ctx, tools.Scope{Agent: "main"}, call)
	var dispatchErr *tools.CallError
	if result.Source.Kind == "builtin" && errors.As(err, &dispatchErr) && dispatchErr.Cause != nil {
		err = dispatchErr.Cause
	}
	return result.ModelContent(), err
}

func requestApproval(ctx context.Context, kind, title, body string, emit func(TurnEvent)) (bool, error) {
	return awaitApproval(ctx, &ApprovalRequest{Kind: kind, Title: title, Body: body}, emit)
}

// awaitApproval emits a prepared request and blocks for its single decision.
func awaitApproval(ctx context.Context, request *ApprovalRequest, emit func(TurnEvent)) (bool, error) {
	request.Reply = make(chan bool, 1)
	emit(TurnEvent{Kind: "approval", Approval: request})
	select {
	case approved := <-request.Reply:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
