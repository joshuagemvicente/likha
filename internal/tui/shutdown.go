package tui

import (
	"context"
	"fmt"
)

// StopRun ends the active run the way Ctrl+D and /quit do on the way out:
// it cancels any OAuth login and the run's context, which kills the run's
// tool and hook process groups, and releases the run goroutine's undroppable
// event sends. It then waits, until ctx is done, for the run goroutine to
// return, so the kills land before the process exits. With no run it returns
// at once.
//
// Call it only after the program's event loop has exited: it reads and
// changes model state that Update otherwise owns.
func (m *ui) StopRun(ctx context.Context) error {
	m.cancelOAuthLogin()
	if m.cancel != nil {
		m.cancel()
	}
	m.abandonRun()
	if m.events == nil {
		return nil
	}
	for {
		select {
		case _, open := <-m.events:
			if !open {
				return nil // the run goroutine has returned
			}
		case <-ctx.Done():
			return fmt.Errorf("stop run: the run did not return in time: %w", context.Cause(ctx))
		}
	}
}
