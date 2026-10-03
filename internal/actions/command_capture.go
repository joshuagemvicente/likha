package actions

import (
	"context"
	"strings"
	"sync"
)

const MaxCommandCaptureBytes = 5 << 20

// RunCommandCaptured runs the exact approved shell text with the same cwd,
// process-group cancellation, and WaitDelay as RunCommand. The caller remains
// responsible for command validation and approval; the cwd is not a sandbox.
//
// Captured retains a UTF-8 prefix within limit, capped at five MiB. Nonpositive
// limits retain nothing. Invalid UTF-8 bytes (including a rune cut at the limit)
// are omitted and counted in Discarded along with output beyond the limit.
// Output equals Captured without a truncation marker; callers must bound their
// own inline previews. Both stdout and stderr continue draining after the cap.
//
// A nonzero shell exit returns a result, not an error. Cancellation and other
// execution errors return partial output marked Truncated and the exit code
// when available (-1 for a signalled process). Discarded counts only observed
// bytes, not output that might have been produced after interruption.
func RunCommandCaptured(ctx context.Context, root, command string, limit int) (CommandResult, error) {
	if limit < 0 {
		limit = 0
	}
	if limit > MaxCommandCaptureBytes {
		limit = MaxCommandCaptureBytes
	}
	output := capturedOutput{limit: limit}
	exitCode, err := executeCommand(ctx, root, command, &output)
	result := output.result()
	result.ExitCode = exitCode
	if err != nil {
		result.Truncated = true
	}
	return result, err
}

// capturedOutput accepts every write in full even after retention is exhausted.
// One mutex protects the shared stdout/stderr sink and result snapshots.
type capturedOutput struct {
	mu       sync.Mutex
	limit    int
	data     []byte
	observed int64
}

func (o *capturedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	o.observed += int64(n)
	if remaining := o.limit - len(o.data); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		o.data = append(o.data, p...)
	}
	return n, nil
}

func (o *capturedOutput) result() CommandResult {
	o.mu.Lock()
	defer o.mu.Unlock()
	// Decode only after capture so runes split across writes remain intact.
	captured := strings.ToValidUTF8(string(o.data), "")
	discarded := o.observed - int64(len(captured))
	return CommandResult{
		Output:    captured,
		Captured:  captured,
		Truncated: discarded > 0,
		Discarded: discarded,
	}
}
