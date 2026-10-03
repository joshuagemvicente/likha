package explore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"likha/internal/repository"
	"likha/internal/tools"
)

// Manager owns one main run's task tree, historical spawn budget, and scheduler.
// Runners acquire execution permits through Node; awaiting a child holds none.
type Manager struct {
	mu         sync.Mutex
	changed    *sync.Cond
	ctx        context.Context
	cancel     context.CancelFunc
	config     Config
	scheduler  *Scheduler
	nodes      map[string]*Node
	order      []*Node
	accepted   int
	active     int
	closed     bool
	updates    []recordUpdate
	publishing bool
}

// New freezes the run identity, canonical root, and inherited capability scope.
func New(ctx context.Context, config Config) (*Manager, error) {
	if ctx == nil {
		return nil, errors.New("explore run requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Runner == nil {
		return nil, errors.New("explore run requires a runner")
	}
	repo, err := repository.New(config.Root)
	if err != nil {
		return nil, err
	}
	config.Root = repo.Root()
	if config.RunID == "" {
		config.RunID, err = opaqueID("run_")
		if err != nil {
			return nil, err
		}
	}
	config.Scope = cloneScope(config.Scope)
	runCtx, cancel := context.WithCancel(ctx)
	m := &Manager{
		ctx: runCtx, cancel: cancel, config: config,
		scheduler: NewScheduler(MaxExecuting), nodes: make(map[string]*Node),
	}
	m.changed = sync.NewCond(&m.mu)
	context.AfterFunc(runCtx, func() { m.stopRun(context.Cause(runCtx)) })
	return m, nil
}

// Scope returns a copy of the inherited scope, not a mutable capability grant.
func (m *Manager) Scope() tools.Scope {
	return cloneScope(m.config.Scope)
}

// Spawn awaits one accepted child's terminal outcome. Validation/refusal errors
// create no record and consume no budget. Accepted failures are attributed in
// the outcome, rather than becoming a failure of the parent run.
func (m *Manager) Spawn(ctx context.Context, parentID, parentCallID string, spec Spec) (Outcome, error) {
	if ctx == nil {
		return Outcome{}, errors.New("explore task requires a context")
	}
	if err := m.validateSpec(spec); err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(parentCallID) == "" || len(parentCallID) > 1024 || !utf8.ValidString(parentCallID) || strings.IndexFunc(parentCallID, unicode.IsControl) >= 0 {
		return Outcome{}, errors.New("explore task requires a valid, bounded parent tool-call ID")
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	id, err := opaqueID("task_")
	if err != nil {
		return Outcome{}, err
	}

	m.mu.Lock()
	if m.closed || m.ctx.Err() != nil {
		m.mu.Unlock()
		return Outcome{}, errors.New("explore run has stopped")
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return Outcome{}, err
	}
	depth, parentCtx := 1, m.ctx
	var parent *Node
	if parentID != "" {
		parent = m.nodes[parentID]
		if parent == nil {
			m.mu.Unlock()
			return Outcome{}, fmt.Errorf("unknown explore parent %q", parentID)
		}
		if terminal(parent.record.Status) || parent.ctx.Err() != nil {
			m.mu.Unlock()
			return Outcome{}, fmt.Errorf("explore parent %q has stopped", parentID)
		}
		if parent.hasPermit || parent.acquiring {
			m.mu.Unlock()
			return Outcome{}, errors.New("release the parent's execution permit before awaiting an explore task")
		}
		depth, parentCtx = parent.record.Depth+1, parent.ctx
	}
	if depth > MaxDepth {
		m.mu.Unlock()
		return Outcome{}, fmt.Errorf("explore nesting is limited to depth %d", MaxDepth)
	}
	if m.accepted >= MaxChildren {
		m.mu.Unlock()
		return Outcome{}, fmt.Errorf("explore run has exhausted its %d accepted-task budget", MaxChildren)
	}
	acceptedAt := time.Now().UTC()
	deadline := acceptedAt.Add(ChildTimeout)
	for _, inherited := range []context.Context{parentCtx, ctx} {
		if d, ok := inherited.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
	}
	if !deadline.After(acceptedAt) {
		m.mu.Unlock()
		return Outcome{}, context.DeadlineExceeded
	}
	deadlineCtx, deadlineCancel := context.WithDeadline(parentCtx, deadline)
	nodeCtx, causeCancel := context.WithCancelCause(deadlineCtx)
	n := &Node{
		manager: m, ctx: nodeCtx, done: make(chan struct{}),
		cancel: func() { causeCancel(context.Canceled); deadlineCancel() },
		record: Record{
			ID: id, RunID: m.config.RunID, SessionID: m.config.SessionID,
			ParentID: parentID, ParentCallID: parentCallID, Agent: spec.Agent,
			Description: spec.Description, Prompt: spec.Prompt, Root: m.config.Root,
			Provider: m.config.Provider, Model: m.config.Model, Depth: depth,
			Status: Queued, AcceptedAt: acceptedAt, Deadline: deadline.UTC(),
		},
	}
	n.stopCaller = context.AfterFunc(ctx, func() {
		causeCancel(context.Cause(ctx))
		deadlineCancel()
	})
	n.stopContext = context.AfterFunc(nodeCtx, func() { m.finishContext(n) })
	m.accepted++
	if parent != nil {
		parent.spawned++
	}
	m.nodes[id] = n
	m.order = append(m.order, n)
	m.active++
	persisted := make(chan error, 1)
	m.updateLocked(n, persisted)
	m.mu.Unlock()
	go m.execute(n, persisted)

	select {
	case <-n.done:
	case <-n.ctx.Done():
		// Do not depend on AfterFunc scheduling or a cooperative runner to settle
		// the parent tool call promptly at its deadline.
		m.finishContext(n)
		<-n.done
	}
	m.mu.Lock()
	record, ownSpawns := n.record, n.spawned
	m.mu.Unlock()
	return outcomeFromRecord(record, ownSpawns), nil
}

// maxAgentRunes bounds a task's agent/profile name, mirroring the frozen
// catalog's name rule.
const maxAgentRunes = 64

// validateSpec reads the frozen Config lock-free: New freezes it and nothing
// mutates it afterward. With an empty Runners map only the built-in explore
// agent is admitted, preserving the Phase 2 contract exactly.
func (m *Manager) validateSpec(spec Spec) error {
	if !utf8.ValidString(spec.Agent) || spec.Agent == "" || utf8.RuneCountInString(spec.Agent) > maxAgentRunes || strings.IndexFunc(spec.Agent, unicode.IsControl) >= 0 {
		return errors.New("explore task requires a nonempty agent name of at most 64 UTF-8 characters without control characters")
	}
	if spec.Agent != "explore" {
		if _, ok := m.config.Runners[spec.Agent]; !ok {
			return fmt.Errorf("unknown agent %q; the task tool advertises the available profiles", spec.Agent)
		}
	}
	if !utf8.ValidString(spec.Description) || strings.TrimSpace(spec.Description) == "" || utf8.RuneCountInString(spec.Description) > MaxDescriptionRunes {
		return fmt.Errorf("explore description must contain 1 to %d UTF-8 characters", MaxDescriptionRunes)
	}
	if !utf8.ValidString(spec.Prompt) || strings.TrimSpace(spec.Prompt) == "" || len(spec.Prompt) > MaxBriefBytes {
		return fmt.Errorf("explore prompt must be nonempty valid UTF-8 and at most %d bytes", MaxBriefBytes)
	}
	return nil
}

func opaqueID(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("create explore identity: %w", err)
	}
	return prefix + hex.EncodeToString(data[:]), nil
}

func (m *Manager) execute(n *Node, persisted <-chan error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.mu.Lock()
			m.finishLocked(n, Failed, fmt.Sprintf("explore runner panicked: %v", recovered))
			m.mu.Unlock()
		}
		n.releasePermit()
		m.mu.Lock()
		m.active--
		m.changed.Broadcast()
		m.mu.Unlock()
	}()
	select {
	case err := <-persisted:
		if err != nil {
			return // The publisher has already attributed the persistence failure.
		}
	case <-n.done:
		return
	case <-n.ctx.Done():
		m.finishContext(n)
		return
	}
	m.mu.Lock()
	stopped := terminal(n.record.Status)
	m.mu.Unlock()
	if stopped || n.ctx.Err() != nil {
		m.finishContext(n)
		return
	}
	runner := m.runnerFor(n.record.Agent)
	if runner == nil {
		m.mu.Lock()
		m.finishLocked(n, Failed, fmt.Sprintf("No runner is available for agent %q", n.record.Agent))
		m.mu.Unlock()
		return
	}
	outcome := runner(n.ctx, n)
	m.mu.Lock()
	defer m.mu.Unlock()
	if terminal(n.record.Status) {
		return // Late provider responses cannot overwrite cancellation or limits.
	}
	if n.ctx.Err() != nil {
		m.finishContextLocked(n)
		return
	}
	if outcome.Findings != "" {
		n.record.Findings = outcome.Findings
	}
	// The normal runner returns Record's warnings. Do not append that saved
	// prefix a second time, while still accepting additional outcome warnings.
	prefix := 0
	for prefix < len(n.record.Warnings) && prefix < len(outcome.Warnings) && n.record.Warnings[prefix] == outcome.Warnings[prefix] {
		prefix++
	}
	n.record.Warnings = append(n.record.Warnings, outcome.Warnings[prefix:]...)
	status := outcome.Status
	if status == "" {
		status = Completed
	} else if !terminal(status) {
		status = Failed
		outcome.Reason = "explore runner returned a nonterminal outcome"
	}
	m.finishLocked(n, status, outcome.Reason)
}

// CancelBranch stops a task and its descendants, leaving siblings untouched.
func (m *Manager) CancelBranch(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.nodes[id]
	if n == nil {
		return fmt.Errorf("unknown explore task %q", id)
	}
	if terminal(n.record.Status) {
		return fmt.Errorf("explore task %q is already terminal", id)
	}
	for _, candidate := range m.order {
		if candidate == n || m.descendantLocked(candidate, id) {
			m.finishLocked(candidate, Cancelled, "explore branch cancelled")
		}
	}
	return nil
}

func (m *Manager) descendantLocked(n *Node, ancestor string) bool {
	for parent := n.record.ParentID; parent != ""; {
		if parent == ancestor {
			return true
		}
		p := m.nodes[parent]
		if p == nil {
			return false
		}
		parent = p.record.ParentID
	}
	return false
}

// CancelAll closes admission and settles every outstanding task result. Wait
// separately drains runner goroutines and outstanding persistence callbacks.
func (m *Manager) CancelAll() {
	m.stopRun(context.Canceled)
}

func (m *Manager) stopRun(cause error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	status, reason := Cancelled, "explore run cancelled"
	if errors.Is(cause, context.DeadlineExceeded) {
		status, reason = Limited, "explore run deadline exceeded"
	}
	for _, n := range m.order {
		m.finishLocked(n, status, reason)
	}
	m.cancel()
	m.changed.Broadcast()
}

// Wait drains currently accepted work, including a late cancellation-resistant
// runner. CancelAll must precede Wait when no further admission is wanted.
func (m *Manager) Wait() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.active != 0 || m.publishing || len(m.updates) != 0 {
		m.changed.Wait()
	}
}

func (m *Manager) finishContext(n *Node) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishContextLocked(n)
}

func (m *Manager) finishContextLocked(n *Node) {
	if n.ctx.Err() == nil || terminal(n.record.Status) {
		return
	}
	if errors.Is(context.Cause(n.ctx), context.DeadlineExceeded) {
		m.finishLocked(n, Limited, "explore task deadline exceeded")
	} else {
		m.finishLocked(n, Cancelled, "explore task cancelled")
	}
}

// finishLocked is the only terminal transition and result-delivery point.
func (m *Manager) finishLocked(n *Node, status State, reason string) {
	if terminal(n.record.Status) {
		return
	}
	if status == Completed {
		if errors.Is(context.Cause(n.ctx), context.DeadlineExceeded) || !time.Now().Before(n.record.Deadline) {
			status, reason = Limited, "explore task deadline exceeded"
		} else if n.ctx.Err() != nil {
			status, reason = Cancelled, "explore task cancelled"
		}
	}
	if reason == "" {
		switch status {
		case Limited:
			reason = "explore task reached a runtime limit; findings may be partial"
		case Failed:
			reason = "explore task failed; findings may be partial"
		case Cancelled:
			reason = "explore task cancelled"
		case Interrupted:
			reason = "explore task interrupted; no work was resumed"
		}
	}
	n.record.Status = status
	n.record.Reason = reason
	n.record.FinishedAt = time.Now().UTC()
	if n.stopCaller != nil {
		n.stopCaller()
	}
	if n.stopContext != nil {
		n.stopContext()
	}
	n.cancel()
	m.updateLocked(n, nil)
	close(n.done)
	m.changed.Broadcast()
}

func terminal(status State) bool {
	switch status {
	case Completed, Limited, Failed, Cancelled, Interrupted:
		return true
	default:
		return false
	}
}
