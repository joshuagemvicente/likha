package explore

import (
	"container/list"
	"context"
	"sync"
)

// Scheduler is a FIFO execution-permit pool shared by a run's callers. It has
// no knowledge of task ancestry. Callers must release their permit before
// awaiting children and acquire another permit before resuming execution.
// Construct a Scheduler with NewScheduler and do not copy it after use.
type Scheduler struct {
	mu        sync.Mutex
	limit     int
	executing int
	ready     list.List
}

type schedulerWaiter struct {
	ctx     context.Context
	ready   chan struct{}
	entry   *list.Element
	granted bool
}

// NewScheduler returns a scheduler with at most MaxExecuting execution permits.
// Nonpositive limits default to MaxExecuting; larger limits are clamped to it.
func NewScheduler(limit int) *Scheduler {
	if limit <= 0 || limit > MaxExecuting {
		limit = MaxExecuting
	}
	return &Scheduler{limit: limit}
}

// Acquire waits for an execution permit in FIFO order. Cancellation or expiry
// while waiting removes the caller without retaining a permit. After a
// successful acquisition, the caller owns the permit until it calls release,
// even if ctx is subsequently cancelled. The returned release is idempotent.
func (s *Scheduler) Acquire(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if s.executing < s.limit && s.ready.Len() == 0 {
		s.executing++
		s.mu.Unlock()
		release = s.releaseFunc()
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	}

	waiter := &schedulerWaiter{ctx: ctx, ready: make(chan struct{})}
	waiter.entry = s.ready.PushBack(waiter)
	s.grantReadyLocked()
	s.mu.Unlock()

	select {
	case <-waiter.ready:
		release = s.releaseFunc()
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	case <-ctx.Done():
		s.mu.Lock()
		if waiter.entry != nil {
			s.ready.Remove(waiter.entry)
			waiter.entry = nil
		} else if waiter.granted {
			// A grant can race cancellation. Return its reserved permit even
			// when select chose ctx.Done instead of the ready notification.
			s.executing--
			waiter.granted = false
		}
		s.grantReadyLocked()
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Stats returns a consistent snapshot of reserved execution permits and callers
// still queued for a permit. A granted caller counts as executing before it wakes.
func (s *Scheduler) Stats() (executing, queued int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executing, s.ready.Len()
}

func (s *Scheduler) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.executing--
			s.grantReadyLocked()
			s.mu.Unlock()
		})
	}
}

// grantReadyLocked reserves available permits in queue order. The caller holds
// s.mu. Expired entries are removed without a grant; their contexts wake Acquire.
func (s *Scheduler) grantReadyLocked() {
	for s.executing < s.limit && s.ready.Len() > 0 {
		entry := s.ready.Front()
		waiter := entry.Value.(*schedulerWaiter)
		s.ready.Remove(entry)
		waiter.entry = nil
		if waiter.ctx.Err() != nil {
			continue
		}
		waiter.granted = true
		s.executing++
		close(waiter.ready)
	}
}
