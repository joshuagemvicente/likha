package app

import (
	"os"
	"syscall"
	"testing"
	"time"

	"likha/internal/mcp"
)

// fakeProgram records Quit calls on a channel a test can wait on.
type fakeProgram struct{ quits chan struct{} }

func newFakeProgram() *fakeProgram { return &fakeProgram{quits: make(chan struct{}, 4)} }

func (p *fakeProgram) Quit() { p.quits <- struct{}{} }

// exitRecorder stands in for os.Exit.
type exitRecorder struct{ codes chan int }

func newExitRecorder() *exitRecorder { return &exitRecorder{codes: make(chan int, 4)} }

func (e *exitRecorder) exit(code int) { e.codes <- code }

const waitLimit = 2 * time.Second

func waitQuit(t *testing.T, p *fakeProgram) {
	t.Helper()
	select {
	case <-p.quits:
	case <-time.After(waitLimit):
		t.Fatal("program was not asked to quit")
	}
}

func waitCaught(t *testing.T, w *shutdownWatch) int {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for time.Now().Before(deadline) {
		if code, ok := w.caught(); ok {
			return code
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("signal was not caught")
	return 0
}

func TestSignalExitCode(t *testing.T) {
	cases := []struct {
		sig  os.Signal
		want int
	}{
		{syscall.SIGHUP, 129},
		{syscall.SIGINT, 130},
		{syscall.SIGTERM, 143},
		{syscall.SIGQUIT, 1}, // not a shutdown signal
	}
	for _, tc := range cases {
		t.Run(tc.sig.String(), func(t *testing.T) {
			if got := signalExitCode(tc.sig); got != tc.want {
				t.Fatalf("signalExitCode(%v) = %d, want %d", tc.sig, got, tc.want)
			}
		})
	}
}

func TestShutdownWatchQuitsTheProgramOnSignal(t *testing.T) {
	for _, sig := range interactiveShutdownSignals {
		t.Run(sig.String(), func(t *testing.T) {
			signals := make(chan os.Signal, 1)
			exits := newExitRecorder()
			w := newShutdownWatch(signals, func() {}, time.Hour, exits.exit)
			program := newFakeProgram()
			w.attach(program)
			signals <- sig
			waitQuit(t, program)
			if code := waitCaught(t, w); code != signalExitCode(sig) {
				t.Fatalf("caught() = %d, want %d", code, signalExitCode(sig))
			}
			w.stop()
			select {
			case code := <-exits.codes:
				t.Fatalf("exit(%d) called during an in-time shutdown", code)
			default:
			}
		})
	}
}

func TestShutdownWatchSignalBeforeAttachQuitsOnAttach(t *testing.T) {
	signals := make(chan os.Signal, 1)
	w := newShutdownWatch(signals, func() {}, time.Hour, newExitRecorder().exit)
	defer w.stop()
	signals <- syscall.SIGTERM
	if code := waitCaught(t, w); code != exitTerminated {
		t.Fatalf("caught() = %d, want %d", code, exitTerminated)
	}
	program := newFakeProgram()
	w.attach(program)
	waitQuit(t, program)
}

func TestShutdownWatchExitsWhenShutdownStalls(t *testing.T) {
	signals := make(chan os.Signal, 1)
	exits := newExitRecorder()
	w := newShutdownWatch(signals, func() {}, 20*time.Millisecond, exits.exit)
	defer w.stop()
	w.attach(newFakeProgram())
	signals <- syscall.SIGHUP
	select {
	case code := <-exits.codes:
		if code != exitHangup {
			t.Fatalf("exit(%d), want exit(%d)", code, exitHangup)
		}
	case <-time.After(waitLimit):
		t.Fatal("grace expired without exit")
	}
}

func TestShutdownWatchRepeatedSignalsDoNotCutCleanupShort(t *testing.T) {
	signals := make(chan os.Signal, 3)
	exits := newExitRecorder()
	w := newShutdownWatch(signals, func() {}, time.Hour, exits.exit)
	program := newFakeProgram()
	w.attach(program)
	signals <- syscall.SIGHUP
	waitQuit(t, program)
	signals <- syscall.SIGHUP // a closing terminal can hang up twice
	signals <- syscall.SIGTERM
	w.stop()
	select {
	case code := <-exits.codes:
		t.Fatalf("exit(%d) called before the grace", code)
	case <-program.quits:
		t.Fatal("program asked to quit twice")
	default:
	}
	if code, _ := w.caught(); code != exitHangup {
		t.Fatalf("caught() = %d, want the first signal's %d", code, exitHangup)
	}
}

func TestShutdownWatchStopWithoutSignal(t *testing.T) {
	released := false
	w := newShutdownWatch(make(chan os.Signal), func() { released = true }, time.Hour, newExitRecorder().exit)
	program := newFakeProgram()
	w.attach(program)
	w.stop()
	if !released {
		t.Fatal("stop did not release the signals")
	}
	if _, ok := w.caught(); ok {
		t.Fatal("caught() reports a signal that never arrived")
	}
	select {
	case <-program.quits:
		t.Fatal("program asked to quit without a signal")
	default:
	}
}

// TestShutdownWatchSignalCancelsStartupContext covers Ctrl+C during startup's
// network calls: the terminal is still cooked, so it arrives as SIGINT before
// any program is attached, and those calls must end at once with the
// signal's status already recorded.
func TestShutdownWatchSignalCancelsStartupContext(t *testing.T) {
	signals := make(chan os.Signal, 1)
	exits := newExitRecorder()
	w := newShutdownWatch(signals, func() {}, time.Hour, exits.exit)
	defer w.stop()
	ctx := w.startupContext()
	if ctx.Err() != nil {
		t.Fatalf("startup context cancelled before any signal: %v", ctx.Err())
	}
	signals <- syscall.SIGINT
	select {
	case <-ctx.Done():
	case <-time.After(waitLimit):
		t.Fatal("SIGINT did not cancel the startup context")
	}
	if code, ok := w.caught(); !ok || code != exitInterrupted {
		t.Fatalf("caught() = %d, %v once the context ended, want %d, true", code, ok, exitInterrupted)
	}
	select {
	case code := <-exits.codes:
		t.Fatalf("exit(%d) called before the grace", code)
	default:
	}
}

// The signalled-exit grace covers stopping the run and then stopping every
// MCP server, with room to spare; otherwise the forced exit could cut the
// server cleanup short and leave processes behind.
func TestShutdownGraceCoversRunAndMcpStop(t *testing.T) {
	const margin = time.Second
	needed := runStopTimeout + mcp.StopBound() + margin
	if interactiveShutdownGrace < needed {
		t.Fatalf("interactiveShutdownGrace = %s, want at least %s (run stop %s + MCP stop %s + margin %s)",
			interactiveShutdownGrace, needed, runStopTimeout, mcp.StopBound(), margin)
	}
}
