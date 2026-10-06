package explore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"likha/internal/model"
)

// Node is a runner's handle to its own record and the shared execution budget.
// Its mutable fields are protected by manager.mu, never a second record lock.
type Node struct {
	manager     *Manager
	ctx         context.Context
	cancel      context.CancelFunc
	stopCaller  func() bool
	stopContext func() bool
	done        chan struct{}
	record      Record
	acquiring   bool
	hasPermit   bool
	release     func()
	requests    []requestUsage
	// spawned counts the descendants this task itself had accepted. The
	// durable record exposes the shared run-wide budget for inspection; the
	// model-facing outcome reports this per-task count instead.
	spawned int
	// phaseSince starts the open timing segment, which accountLocked charges
	// to active time while hasPermit is set and to wait time otherwise.
	phaseSince   time.Time
	waitTime     time.Duration
	activeTime   time.Duration
	timingClosed bool
}

type requestUsage struct {
	accounted bool
	reported  bool
	usage     model.RequestUsage
}

// Record returns a detached versioned snapshot. SpawnUsed is the shared budget
// use when that version was captured, not a separately mutated live counter.
func (n *Node) Record() Record {
	m := n.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneRecord(n.record)
}

// Acquire joins the run-wide FIFO queue. The returned release is idempotent;
// the runner must call it before an awaited Spawn, and acquire again to resume.
func (n *Node) Acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, errors.New("explore execution requires a context")
	}
	m := n.manager
	m.mu.Lock()
	if err := n.liveLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if n.acquiring || n.hasPermit {
		m.mu.Unlock()
		return nil, errors.New("explore task already holds or awaits an execution permit")
	}
	n.acquiring = true
	if n.record.Status != Queued {
		n.record.Status = Queued
		m.updateLocked(n, nil)
	}
	m.mu.Unlock()

	phaseCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(n.ctx, cancel)
	if n.ctx.Err() != nil {
		cancel()
	}
	release, err := m.scheduler.Acquire(phaseCtx)
	stop()
	cancel()
	m.mu.Lock()
	n.acquiring = false
	if err == nil {
		err = n.liveLocked()
		if err == nil {
			err = ctx.Err()
		}
	}
	if err != nil {
		m.mu.Unlock()
		if release != nil {
			release()
		}
		return nil, err
	}
	n.accountLocked(time.Now())
	n.hasPermit = true
	var once sync.Once
	wrappedRelease := func() {
		once.Do(func() {
			m.mu.Lock()
			n.accountLocked(time.Now())
			n.hasPermit = false
			n.release = nil
			m.mu.Unlock()
			release()
		})
	}
	n.release = wrappedRelease
	n.record.Status = Running
	if n.record.StartedAt.IsZero() {
		n.record.StartedAt = time.Now().UTC()
	}
	m.updateLocked(n, nil)
	m.mu.Unlock()
	return wrappedRelease, nil
}

// accountLocked folds the open timing segment into wait or active time and
// starts a new one at now. Terminal records close at FinishedAt so a permit
// released after settlement does not extend either total.
func (n *Node) accountLocked(now time.Time) {
	if n.timingClosed {
		return
	}
	if n.phaseSince.IsZero() {
		n.phaseSince = n.record.AcceptedAt
	}
	if terminal(n.record.Status) && !n.record.FinishedAt.IsZero() {
		now = n.record.FinishedAt
		n.timingClosed = true
	}
	if elapsed := now.Sub(n.phaseSince); elapsed > 0 {
		if n.hasPermit {
			n.activeTime += elapsed
		} else {
			n.waitTime += elapsed
		}
		n.phaseSince = now
	}
	n.record.WaitMs = n.waitTime.Milliseconds()
	n.record.ActiveMs = n.activeTime.Milliseconds()
}

func (n *Node) releasePermit() {
	n.manager.mu.Lock()
	release := n.release
	n.manager.mu.Unlock()
	if release != nil {
		release()
	}
}

// SetStatus lets the loop label a child join without moving execution permits.
// Terminal states settle the outcome once; all subsequent runner updates stop.
func (n *Node) SetStatus(status State) {
	m := n.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.liveLocked() != nil {
		return
	}
	if terminal(status) {
		m.finishLocked(n, status, n.record.Reason)
		return
	}
	if status != Queued && status != Running && status != Waiting {
		return
	}
	if n.record.Status != status {
		n.record.Status = status
		m.updateLocked(n, nil)
	}
}

// BeginRequest reserves exactly one of the 32 provider requests. Exhaustion is
// terminal, preserving partial findings without a hidden summarization request.
func (n *Node) BeginRequest() error {
	m := n.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := n.liveLocked(); err != nil {
		return err
	}
	if n.record.Rounds >= MaxRequests {
		reason := fmt.Sprintf("explore task reached its %d-model-request limit", MaxRequests)
		m.finishLocked(n, Limited, reason)
		return errors.New(reason)
	}
	n.record.Rounds++
	n.requests = append(n.requests, requestUsage{})
	n.summarizeUsageLocked()
	m.updateLocked(n, nil)
	return nil
}

// RecordRequestUsage consumes the independent child client's per-request
// report once for the current request. Its parameters mirror
// model.Client.LastRequestUsage, so a prompt-only report leaves completion
// unknown instead of known zero.
func (n *Node) RecordRequestUsage(prompt, completion int64, promptSeen, completionSeen, reported bool) {
	n.RecordRequest(model.RequestUsage{Prompt: prompt, Completion: completion, PromptSeen: promptSeen, CompletionSeen: completionSeen}, reported)
}

// RecordUsage consumes a report that carries a prompt-seen flag only; such a
// report is taken as complete. Prefer RecordRequest.
func (n *Node) RecordUsage(usage model.TokenUsage, reported bool) {
	n.RecordRequest(model.RequestUsage{Prompt: usage.Prompt, Completion: usage.Completion, PromptSeen: usage.PromptSeen, CompletionSeen: usage.PromptSeen}, reported)
}

// RecordRequest consumes the child client's full per-request breakdown
// (model.Client.LastRequest) once for the current request.
func (n *Node) RecordRequest(usage model.RequestUsage, reported bool) {
	m := n.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.liveLocked() != nil || len(n.requests) == 0 {
		return
	}
	request := &n.requests[len(n.requests)-1]
	if request.accounted {
		return
	}
	request.accounted = true
	request.reported = reported
	request.usage = usage
	n.summarizeUsageLocked()
	m.updateLocked(n, nil)
}

func (n *Node) summarizeUsageLocked() {
	usage := Usage{
		PromptKnown:     len(n.requests) > 0,
		CompletionKnown: len(n.requests) > 0,
		CostKnown:       len(n.requests) > 0,
	}
	for _, request := range n.requests {
		promptKnown := request.reported && request.usage.PromptSeen && request.usage.Prompt >= 0
		completionKnown := request.reported && request.usage.CompletionSeen && request.usage.Completion >= 0
		if promptKnown {
			usage.PromptTokens += request.usage.Prompt
		}
		if completionKnown {
			usage.CompletionTokens += request.usage.Completion
		}
		if request.reported && (promptKnown || completionKnown) {
			usage.ReportedRequests++
		}
		if !promptKnown || !completionKnown {
			// A partial report counts once as reported and once as incomplete;
			// UnknownRequests is not an exclusive complement of ReportedRequests.
			usage.UnknownRequests++
		}
		usage.PromptKnown = usage.PromptKnown && promptKnown
		usage.CompletionKnown = usage.CompletionKnown && completionKnown
		if request.reported {
			usage.CacheReadTokens += max(request.usage.CacheRead, 0)
			usage.CacheWriteTokens += max(request.usage.CacheWrite, 0)
			usage.ReasoningTokens += max(request.usage.Reasoning, 0)
		}
		// An unreported request carries no usable usage; its cost is unknown
		// (a subscription identity does not establish a charge).
		priced := request.usage
		if !request.reported {
			priced = model.RequestUsage{}
		}
		cost, source := model.RequestCost(n.record.Provider, n.record.Model, priced)
		if source.Known() {
			usage.Cost += cost
			usage.CostEstimated = usage.CostEstimated || source == model.CostEstimated
		} else {
			usage.CostKnown = false
		}
	}
	n.record.Usage = usage
}

// Update saves only task data: no client, credentials, approvals, or handles.
// Nil history and empty findings leave existing partial data intact.
func (n *Node) Update(history []model.Message, findings string, tool *ToolRecord) {
	m := n.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.liveLocked() != nil {
		return
	}
	if history != nil {
		n.record.History = cloneMessages(history)
	}
	if findings != "" {
		n.record.Findings = findings
	}
	if tool != nil {
		n.record.Tools = append(n.record.Tools, cloneToolRecord(*tool))
	}
	m.updateLocked(n, nil)
}

func (n *Node) Warn(warning string) {
	if warning == "" {
		return
	}
	m := n.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.liveLocked() != nil {
		return
	}
	n.record.Warnings = append(n.record.Warnings, warning)
	m.updateLocked(n, nil)
}

// Spawn joins a nested child. The loop owns the Waiting label and must release
// its execution permit before this call; depth is checked again at admission.
func (n *Node) Spawn(ctx context.Context, callID string, spec Spec) (Outcome, error) {
	return n.manager.Spawn(ctx, n.record.ID, callID, spec)
}

func (n *Node) liveLocked() error {
	if terminal(n.record.Status) {
		return fmt.Errorf("explore task %q is %s", n.record.ID, n.record.Status)
	}
	if n.ctx.Err() != nil {
		n.manager.finishContextLocked(n)
		return context.Cause(n.ctx)
	}
	return nil
}
