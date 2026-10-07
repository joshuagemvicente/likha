package app

import (
	"io"
	"os"
	"sync"
)

// lockedOutput serializes writes to the terminal. Bubble Tea writes each
// frame with a single Write, so an attention signal (bell, OSC 9) written
// through the same lock always lands between whole frames, never inside one
// (specs/attention-notifications T3).
type lockedOutput struct {
	mu sync.Mutex
	w  io.Writer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.w.Write(p)
}

// lockedTerminal is lockedOutput over the process's terminal file. It keeps
// the Read/Close/Fd methods Bubble Tea looks for (its term.File check), so
// wrapping stdout does not lose terminal-size detection or resize handling.
type lockedTerminal struct {
	*lockedOutput
	file *os.File
}

func (t lockedTerminal) Read(p []byte) (int, error) { return t.file.Read(p) }

// Close is a no-op: stdout belongs to the process and must outlive the
// program, and Bubble Tea never needs to close its output.
func (t lockedTerminal) Close() error { return nil }

func (t lockedTerminal) Fd() uintptr { return t.file.Fd() }

// newTerminalOutput wraps w in a write lock shared by the renderer and the
// attention signals. A terminal file keeps its file methods.
func newTerminalOutput(w io.Writer) io.Writer {
	locked := &lockedOutput{w: w}
	if f, ok := w.(*os.File); ok {
		return lockedTerminal{lockedOutput: locked, file: f}
	}
	return locked
}
