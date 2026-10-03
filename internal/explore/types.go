// Package explore owns bounded, awaited, repository-read-only task trees.
package explore

import (
	"context"
	"time"

	"likha/internal/model"
	"likha/internal/tools"
)

const (
	MaxDepth            = 2
	MaxExecuting        = 4
	MaxChildren         = 16
	MaxRequests         = 32
	MaxDescriptionRunes = 120
	MaxBriefBytes       = 32 << 10
	ChildTimeout        = 5 * time.Minute
)

type State string

const (
	Queued      State = "queued"
	Running     State = "running"
	Waiting     State = "waiting-for-child"
	Completed   State = "completed"
	Limited     State = "limited"
	Failed      State = "failed"
	Cancelled   State = "cancelled"
	Interrupted State = "interrupted"
)

type Spec struct {
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

type Usage struct {
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	PromptKnown      bool    `json:"prompt_known"`
	CompletionKnown  bool    `json:"completion_known"`
	ReportedRequests int     `json:"reported_requests"`
	UnknownRequests  int     `json:"unknown_requests"`
	Cost             float64 `json:"cost"`
	CostKnown        bool    `json:"cost_known"`
}

type ToolRecord struct {
	CallID    string       `json:"call_id"`
	Name      string       `json:"name"`
	Arguments string       `json:"arguments"`
	Result    tools.Result `json:"result"`
}

type Record struct {
	ID           string          `json:"id"`
	RunID        string          `json:"run_id"`
	SessionID    string          `json:"session_id"`
	ParentID     string          `json:"parent_id,omitempty"`
	ParentCallID string          `json:"parent_call_id"`
	Agent        string          `json:"agent"`
	Description  string          `json:"description"`
	Prompt       string          `json:"prompt"`
	Root         string          `json:"root"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Depth        int             `json:"depth"`
	Status       State           `json:"status"`
	Version      uint64          `json:"version"`
	AcceptedAt   time.Time       `json:"accepted_at"`
	StartedAt    time.Time       `json:"started_at,omitempty"`
	FinishedAt   time.Time       `json:"finished_at,omitempty"`
	Deadline     time.Time       `json:"deadline"`
	Rounds       int             `json:"rounds"`
	SpawnUsed    int             `json:"spawn_used"`
	History      []model.Message `json:"history,omitempty"`
	Tools        []ToolRecord    `json:"tools,omitempty"`
	Findings     string          `json:"findings,omitempty"`
	Warnings     []string        `json:"warnings,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Usage        Usage           `json:"usage"`
	// WaitMs is cumulative live time without an execution permit: queued in
	// the scheduler, awaiting nested children, or between phases. ActiveMs is
	// cumulative time holding a permit. Both cover AcceptedAt through
	// FinishedAt and are refreshed whenever the record version advances.
	WaitMs   int64 `json:"wait_ms,omitempty"`
	ActiveMs int64 `json:"active_ms,omitempty"`
}

type Outcome struct {
	TaskID    string    `json:"task_id"`
	Agent     string    `json:"agent"`
	Depth     int       `json:"depth"`
	Status    State     `json:"status"`
	Findings  string    `json:"findings"`
	Reason    string    `json:"reason,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
	Rounds    int       `json:"model_requests"`
	SpawnUsed int       `json:"accepted_children"`
	Deadline  time.Time `json:"deadline,omitempty"`
	Usage     Usage     `json:"usage"`
}

type Runner func(context.Context, *Node) Outcome

type Config struct {
	RunID, SessionID, Root, Provider, Model string
	Scope                                   tools.Scope
	Runner                                  Runner
	// Runners maps a named agent profile to its Runner. A nil or empty map
	// keeps the single-runner behavior: every accepted task runs on Runner.
	// Keys are profile names from the frozen run catalog; "explore" may also
	// appear and overrides the legacy Runner for explore tasks. A named key
	// holding a nil Runner makes those tasks fail fast rather than panic.
	Runners  map[string]Runner
	OnUpdate func(Record) error
}
