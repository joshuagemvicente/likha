// Package tools defines the model-callable tool catalog and its dispatch contract.
package tools

import (
	"context"
	"encoding/json"

	"likha/internal/model"
)

const MaxInlineBytes = 64 << 10

type Effect string

const (
	Read    Effect = "read"
	Write   Effect = "write"
	Exec    Effect = "exec"
	Network Effect = "network"
)

type Status string

const (
	Succeeded Status = "succeeded"
	Refused   Status = "refused"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
	Limited   Status = "limited"
)

type Source struct {
	Kind   string `json:"kind"`
	Server string `json:"server,omitempty"`
	Tool   string `json:"tool,omitempty"`
}

// Scope is frozen for one run. A nil allowlist permits the registered main
// catalog; a non-nil allowlist can only narrow available capabilities.
type Scope struct {
	Mode    string
	Agent   string
	Allowed map[string]bool
}

type Result struct {
	// Executed distinguishes a cancelled approval/queued call from a handler
	// that ran and may already have performed effects. It is not model content.
	Executed     bool     `json:"-"`
	Status       Status   `json:"status"`
	Content      string   `json:"content"`
	Source       Source   `json:"source"`
	SourceTaskID string   `json:"source_task_id,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	ArtifactID   string   `json:"artifact_id,omitempty"`
	NextOffset   int      `json:"next_offset,omitempty"`
	Cursor       string   `json:"cursor,omitempty"`
}

type Handler func(context.Context, json.RawMessage) (Result, error)
type Authorizer func(context.Context, json.RawMessage) error

type Tool struct {
	Definition        model.ToolDefinition
	Source            Source
	Effects           []Effect
	Target            string
	ParallelSafe      bool
	Interactive       bool
	UnavailableReason string
	Aliases           []string
	Authorize         Authorizer
	Run               Handler
}

type CatalogEntry struct {
	Name, Description, Target string
	Source                    Source
	Effects                   []Effect
	ParallelSafe, Interactive bool
	Available                 bool
	Reason                    string
}
