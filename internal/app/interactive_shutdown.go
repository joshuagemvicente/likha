package app

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Exit statuses of an interactive session ended by a signal, following the
// shell's 128+n convention so a parent can tell how Likha stopped.
const (
	exitHangup      = 128 + int(syscall.SIGHUP)  // 129: the terminal closed
	exitInterrupted = 128 + int(syscall.SIGINT)  // 130: kill -INT
	exitTerminated  = 128 + int(syscall.SIGTERM) // 143: kill
)

// interactiveShutdownGrace bounds the orderly shutdown a signal starts:
// quitting the program, stopping the run, and the deferred cleanup that
// stops MCP servers. Past it the process exits anyway, because a dead or
// stalled terminal must not keep Likha (and its children) alive. It exceeds
// runStopTimeout plus mcp.StopBound by a margin, so a signalled exit is not
// cut short while servers are still being reaped.
const interactiveShutdownGrace = 5 * time.Second

// runStopTimeout bounds how long quitting waits for a cancelled run to
// return after its process groups are killed.
const runStopTimeout = time.Second

// interactiveShutdownSignals end an interactive session in order. Bubble
// Tea's own handler is disabled (it would quit on SIGTERM without stopping
// the run, and never sees SIGHUP), so SIGINT, which a raw-mode terminal only
// delivers through kill, is handled here too.
var interactiveShutdownSignals = []os.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT}

// signalExitCode returns the exit status for a session ended by sig.
func signalExitCode(sig os.Signal) int {
	switch sig {
	case syscall.SIGHUP:
		return exitHangup
	case syscall.SIGINT:
		return exitInterrupted
	case syscall.SIGTERM:
		return exitTerminated
	}
	return 1
}

// quitter is the part of the Bubble Tea program a shutdown signal drives.
type quitter interface {
	Quit()
}

// shutdownWatch turns the first shutdown signal into the same orderly exit
// as a normal quit: the attached program is asked to quit, and Run's caller
// then stops the run and unwinds its deferred cleanup. The first signal also
// arms a grace timer that calls exit with the signal's status if that
// unwinding stalls. Later signals are absorbed rather than forcing an exit:
// a closing terminal can deliver SIGHUP more than once, and cutting cleanup
// short would leave MCP servers running.
//
// Before a program is attached the terminal is still cooked, so Ctrl+C
// arrives as SIGINT during startup's network calls; those calls take the
// watch's context, which the first signal cancels, so they return at once
// and startup can exit with the signal's status instead of waiting out the
// grace.
type shutdownWatch struct {
	signals         <-chan os.Signal
	programs        chan quitter
	done            chan struct{}
	release         func()
	grace           time.Duration
	exit            func(code int)
	signalled       context.Context
	cancelSignalled context.CancelFunc

	mu      sync.Mutex
	code    int // 0 until a signal arrives
	timer   *time.Timer
	stopped bool
}

// watchShutdownSignals starts a shutdownWatch on the process's SIGHUP,
// SIGTERM and SIGINT. Stop it after the deferred cleanup it guards.
func watchShutdownSignals(exit func(code int)) *shutdownWatch {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, interactiveShutdownSignals...)
	return newShutdownWatch(signals, func() { signal.Stop(signals) }, interactiveShutdownGrace, exit)
}

// newShutdownWatch starts a watch on signals; release runs once on stop.
func newShutdownWatch(signals <-chan os.Signal, release func(), grace time.Duration, exit func(code int)) *shutdownWatch {
	signalled, cancel := context.WithCancel(context.Background())
	w := &shutdownWatch{
		signals:         signals,
		programs:        make(chan quitter, 1),
		done:            make(chan struct{}),
		release:         release,
		grace:           grace,
		exit:            exit,
		signalled:       signalled,
		cancelSignalled: cancel,
	}
	go w.watch()
	return w
}

func (w *shutdownWatch) watch() {
	var sig os.Signal
	select {
	case sig = <-w.signals:
	case <-w.done:
		return
	}
	code := signalExitCode(sig)
	w.mu.Lock()
	if w.stopped {
		// The signal raced stop: everything the grace guards has run.
		w.mu.Unlock()
		return
	}
	w.code = code
	w.timer = time.AfterFunc(w.grace, func() { w.exit(code) })
	w.mu.Unlock()
	// After code is set, so a call ended by this cancellation finds caught
	// already true.
	w.cancelSignalled()
	select {
	case program := <-w.programs:
		program.Quit()
	case <-w.done:
	}
}

// attach names the program a signal quits. A signal caught before attach
// quits the program as soon as it is attached. Call it at most once.
func (w *shutdownWatch) attach(program quitter) {
	w.programs <- program
}

// startupContext returns a context cancelled by the first shutdown signal (or by
// stop). Startup network calls made before attach take it so a signal ends
// them promptly.
func (w *shutdownWatch) startupContext() context.Context {
	return w.signalled
}

// caught returns the exit status of the signal that started a shutdown, and
// false when none has arrived.
func (w *shutdownWatch) caught() (int, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.code, w.code != 0
}

// stop releases the signals and disarms the grace timer. It belongs after
// every cleanup the grace should bound.
func (w *shutdownWatch) stop() {
	w.mu.Lock()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	w.cancelSignalled()
	w.release()
	close(w.done)
}
