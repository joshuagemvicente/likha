package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// stdioStopGrace is how long a server may take to exit after its stdin closes
// and it receives SIGTERM, before its process group is killed. It is a
// variable so tests can shorten it.
var stdioStopGrace = time.Second

// stdioKill signals or probes a process group. It is a variable so tests can
// record which signals reach the group.
var stdioKill = unix.Kill

// stdioWriteQueue bounds the messages waiting for the server to read its
// stdin. Callers block (honoring their context) when it is full; replies to
// server requests are dropped instead, so the reader never stalls.
const stdioWriteQueue = 16

// stdioTransport runs the server as a child process and exchanges
// newline-delimited JSON-RPC over its stdin and stdout.
//
// Goroutine ownership: writeLoop owns stdin writes and exits when the
// transport stops; readLoop owns stdout reads and is the only caller of
// cmd.Wait, which it calls once stdout ends, and then stops whatever is
// left of the server's process group. close stops both and waits for that
// group cleanup.
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Reader

	writes  chan stdioWrite
	stopped chan struct{} // closed by close: no more writes are accepted
	exited  chan struct{} // closed by readLoop once the process is reaped
	// groupDone is closed by readLoop once the reaped server's process group
	// is empty or has been killed; groupErr is then final.
	groupDone chan struct{}
	groupErr  error
	once      sync.Once
	stopErr   error

	mu      sync.Mutex
	pending map[int]chan rpcResponse
	readErr error // set when readLoop ends; later calls fail at once

	// kill sends every signal and liveness probe to the server's process
	// group: stdioKill as it was when the transport started.
	kill func(pid int, signal unix.Signal) error
	// groupMu guards groupEmpty, which is set once the group is observed
	// with no live member. From then on its ID may belong to an unrelated
	// group, so no signal is ever sent to it again.
	groupMu    sync.Mutex
	groupEmpty bool
}

// stdioWrite is one whole newline-terminated message for writeLoop. done is
// nil for best-effort replies nobody waits on.
type stdioWrite struct {
	ctx  context.Context
	line []byte
	done chan error
}

// startStdio launches the configured command in its own process group and
// starts the reader and writer goroutines.
func startStdio(name string, cfg ServerConfig, userAgent string) (*stdioTransport, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = append(cmd.Environ(), "LIKHA_USER_AGENT="+userAgent)
	for key, value := range cfg.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	// Its own group lets close reach the real server behind wrappers such as
	// npx, uvx or sh -c, which do not forward signals to their children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdin pipe: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdout pipe: %w", name, err)
	}
	cmd.Stderr = nil // the server's logs must not corrupt the protocol stream
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: start %q: %w", name, cfg.Command, err)
	}
	t := &stdioTransport{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		reader:    bufio.NewReaderSize(stdout, 1<<20),
		writes:    make(chan stdioWrite, stdioWriteQueue),
		stopped:   make(chan struct{}),
		exited:    make(chan struct{}),
		groupDone: make(chan struct{}),
		pending:   make(map[int]chan rpcResponse),
		kill:      stdioKill,
	}
	go t.readLoop()
	go t.writeLoop()
	return t, nil
}

func (t *stdioTransport) call(ctx context.Context, id int, payload []byte) (rpcResponse, error) {
	reply := make(chan rpcResponse, 1)
	t.mu.Lock()
	if t.readErr != nil {
		err := t.readErr
		t.mu.Unlock()
		return rpcResponse{}, err
	}
	t.pending[id] = reply
	t.mu.Unlock()
	defer t.forget(id)

	if err := t.write(ctx, payload); err != nil {
		return rpcResponse{}, err
	}
	// A stopping server's reply is not awaited: close may take
	// stdioStopGrace to end the process, and its caller is done with it.
	select {
	case response := <-reply:
		return response, nil
	case <-t.stopped:
		return rpcResponse{}, errStdioStopped
	case <-ctx.Done():
		return rpcResponse{}, fmt.Errorf("request timed out: %w", ctx.Err())
	}
}

// errStdioStopped fails messages sent to, or awaiting, a closed transport.
var errStdioStopped = errors.New("server is stopped")

func (t *stdioTransport) notify(ctx context.Context, payload []byte) error {
	return t.write(ctx, payload)
}

// forget drops a pending request; a response arriving later is discarded.
func (t *stdioTransport) forget(id int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, id)
}

// write hands one whole message to writeLoop and waits until it is written.
// A server that stops reading its stdin fills the pipe; the wait honors ctx
// so a timeout or Esc still returns. An abandoned message that writeLoop has
// not started is skipped; one already being written completes whole, so the
// stream is never left with a partial line. Once the transport stops,
// writeLoop takes nothing more from the queue, so a message still queued
// fails at once instead of waiting out ctx.
func (t *stdioTransport) write(ctx context.Context, payload []byte) error {
	message := stdioWrite{ctx: ctx, line: append(payload, '\n'), done: make(chan error, 1)}
	select {
	case t.writes <- message:
	case <-t.stopped:
		return errStdioStopped
	case <-ctx.Done():
		return fmt.Errorf("request timed out: the server is not reading its input: %w", ctx.Err())
	}
	select {
	case err := <-message.done:
		if err != nil {
			return fmt.Errorf("write to server: %w", err)
		}
		return nil
	case <-t.stopped:
		return errStdioStopped
	case <-ctx.Done():
		return fmt.Errorf("request timed out: the server is not reading its input: %w", ctx.Err())
	}
}

// writeLoop writes queued messages in order, one at a time, so concurrent
// requests never interleave on the stream. close unblocks a stuck write by
// closing stdin.
func (t *stdioTransport) writeLoop() {
	for {
		select {
		case <-t.stopped:
			return
		case message := <-t.writes:
			if message.ctx != nil && message.ctx.Err() != nil {
				message.done <- message.ctx.Err()
				continue
			}
			_, err := t.stdin.Write(message.line)
			if message.done != nil {
				message.done <- err
			}
		}
	}
}

// readLoop matches responses to pending requests by ID. A line carrying a
// method is a server request or notification, never a response, even when
// its ID equals a pending request's: requests are answered, notifications
// ignored. When stdout ends it fails pending calls, reaps the process, and
// stops what is left of its process group.
func (t *stdioTransport) readLoop() {
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			t.failPending(fmt.Errorf("server stopped: %w", err))
			// The process is gone or about to be: reap it so no zombie
			// remains. Its exit status is not an error Likha reports.
			_ = t.cmd.Wait()
			close(t.exited)
			t.groupErr = t.stopLeftoverGroup()
			close(t.groupDone)
			return
		}
		var message rpcMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			continue // unparseable line: ignored, as on HTTP
		}
		if message.Method != "" {
			if message.isRequest() {
				t.answerServerRequest(message)
			}
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(string(message.ID)))
		if err != nil || id == 0 {
			continue // a response to no request Likha sent
		}
		t.deliver(rpcResponse{JSONRPC: "2.0", ID: id, Result: message.Result, Error: message.Error})
	}
}

// deliver hands a response to its waiter and removes it, so a duplicate
// response finds no waiter instead of blocking the reader on a full channel.
func (t *stdioTransport) deliver(reply rpcResponse) {
	t.mu.Lock()
	waiter := t.pending[reply.ID]
	delete(t.pending, reply.ID)
	t.mu.Unlock()
	if waiter != nil {
		waiter <- reply
	}
}

// answerServerRequest queues the reply without waiting: the reader must keep
// draining stdout, or a server blocked writing to it would never read stdin.
// When the queue is full the reply is dropped, which leaves that server
// request unanswered, as a server stuck on its stdin could not read it anyway.
func (t *stdioTransport) answerServerRequest(message rpcMessage) {
	payload, err := serverRequestReply(message)
	if err != nil {
		return // the ID came from valid JSON, so this cannot fail in practice
	}
	select {
	case t.writes <- stdioWrite{line: append(payload, '\n')}:
	default:
	}
}

func (t *stdioTransport) failPending(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.readErr = err
	for id, waiter := range t.pending {
		waiter <- rpcResponse{ID: id, Error: &rpcError{Message: err.Error()}}
		delete(t.pending, id)
	}
}

// close stops the server: stdin is closed and the process group gets SIGTERM;
// whatever remains of the group after stdioStopGrace is killed. close returns
// once readLoop has reaped the process and finished its group cleanup, so
// neither a zombie nor a wrapped child outlives the transport. A group seen
// empty is never signalled again. Safe to call more than once.
func (t *stdioTransport) close() error {
	t.once.Do(func() { t.stopErr = t.stop() })
	return t.stopErr
}

func (t *stdioTransport) stop() error {
	close(t.stopped)
	// Closing stdin is the MCP stdio shutdown signal and unblocks a write
	// stuck on a full pipe. An error only means it was already closed.
	_ = t.stdin.Close()
	select {
	case <-t.exited:
		// Reaped already: readLoop's group cleanup owns whatever is left.
	default:
		if err := t.signalGroup(unix.SIGTERM); err != nil {
			return err
		}
	}
	grace := time.NewTimer(stdioStopGrace)
	defer grace.Stop()
	select {
	case <-t.groupDone:
		return t.groupErr
	case <-grace.C:
	}
	// The server or a descendant in its group ignored SIGTERM. The group is
	// still unreaped or was seen alive within stdioGroupPoll, so it is still
	// the server's.
	if err := t.signalGroup(unix.SIGKILL); err != nil {
		return err
	}
	// A descendant that left the group could still hold stdout open; closing
	// our end lets readLoop finish and reap the killed process.
	_ = t.stdout.Close()
	<-t.groupDone
	return t.groupErr
}

// stdioGroupPoll is how often stopLeftoverGroup checks whether the server's
// process group has emptied.
const stdioGroupPoll = 10 * time.Millisecond

// stopLeftoverGroup runs in readLoop right after the server is reaped: a
// descendant may still run in its group, so it gets SIGTERM and, after
// stdioStopGrace, SIGKILL. Doing this at reap time rather than at close
// matters: the group ID stays reserved only while a member exists, so a
// group that emptied long before close may since belong to an unrelated job.
// The probe-then-signal gap is at most stdioGroupPoll, not the session's
// lifetime.
func (t *stdioTransport) stopLeftoverGroup() error {
	alive, err := t.groupAlive()
	if err != nil || !alive {
		return err
	}
	if err := t.signalGroup(unix.SIGTERM); err != nil {
		return err
	}
	grace := time.NewTimer(stdioStopGrace)
	defer grace.Stop()
	poll := time.NewTicker(stdioGroupPoll)
	defer poll.Stop()
	for {
		select {
		case <-grace.C:
			return t.signalGroup(unix.SIGKILL)
		case <-poll.C:
		}
		alive, err := t.groupAlive()
		if err != nil || !alive {
			return err
		}
	}
}

// groupAlive reports whether the server's process group still has a member
// that a signal can reach.
func (t *stdioTransport) groupAlive() (bool, error) {
	t.groupMu.Lock()
	defer t.groupMu.Unlock()
	if t.groupEmpty {
		return false, nil
	}
	err := t.kill(-t.cmd.Process.Pid, 0)
	if err == nil {
		return true, nil
	}
	if groupGone(err) {
		t.groupEmpty = true
		return false, nil
	}
	return false, fmt.Errorf("check server process group: %w", err)
}

// signalGroup signals every process in the server's process group, unless
// the group was already seen empty. A group with nothing left to signal is
// not an error.
func (t *stdioTransport) signalGroup(signal unix.Signal) error {
	t.groupMu.Lock()
	defer t.groupMu.Unlock()
	if t.groupEmpty {
		return nil
	}
	err := t.kill(-t.cmd.Process.Pid, signal)
	if err == nil {
		return nil
	}
	if groupGone(err) {
		t.groupEmpty = true
		return nil
	}
	return fmt.Errorf("stop server process group: %w", err)
}

// groupGone reports whether a group signal failed because the group has no
// live member. Darwin answers EPERM, not ESRCH, for a group left with only
// zombies, which no signal can affect.
func groupGone(err error) bool {
	if errors.Is(err, unix.ESRCH) {
		return true
	}
	return runtime.GOOS == "darwin" && errors.Is(err, unix.EPERM)
}
