package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// stdioTransport runs the server as a child process and exchanges
// newline-delimited JSON-RPC over its stdin and stdout.
type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Reader
	mu      sync.Mutex
	pending map[int]chan rpcResponse
}

// startStdio launches the configured command and starts the reader that
// matches responses to pending requests.
func startStdio(name string, cfg ServerConfig, userAgent string) (*stdioTransport, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = append(cmd.Environ(), "LIKHA_USER_AGENT="+userAgent)
	for key, value := range cfg.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
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
		cmd:     cmd,
		stdin:   stdin,
		reader:  bufio.NewReaderSize(stdout, 1<<20),
		pending: make(map[int]chan rpcResponse),
	}
	go t.readLoop()
	return t, nil
}

func (t *stdioTransport) call(ctx context.Context, id int, payload []byte) (rpcResponse, error) {
	reply := make(chan rpcResponse, 1)
	t.mu.Lock()
	t.pending[id] = reply
	if _, err := t.stdin.Write(append(payload, '\n')); err != nil {
		delete(t.pending, id)
		t.mu.Unlock()
		return rpcResponse{}, err
	}
	t.mu.Unlock()

	select {
	case response := <-reply:
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return response, nil
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return rpcResponse{}, fmt.Errorf("request timed out: %w", ctx.Err())
	}
}

func (t *stdioTransport) notify(_ context.Context, payload []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.stdin.Write(append(payload, '\n'))
	return err
}

// readLoop matches responses to pending requests by ID and ignores
// server-initiated requests and notifications.
func (t *stdioTransport) readLoop() {
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			t.failPending(fmt.Errorf("server stopped: %w", err))
			return
		}
		var reply rpcResponse
		if err := json.Unmarshal([]byte(line), &reply); err != nil || reply.ID == 0 {
			continue // notification or unparseable line: ignored in v1
		}
		t.mu.Lock()
		waiter := t.pending[reply.ID]
		t.mu.Unlock()
		if waiter != nil {
			waiter <- reply
		}
	}
}

func (t *stdioTransport) failPending(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, waiter := range t.pending {
		waiter <- rpcResponse{ID: id, Error: &rpcError{Message: err.Error()}}
		delete(t.pending, id)
	}
}

// close kills the server process; the reader then fails pending calls.
func (t *stdioTransport) close() error {
	return t.cmd.Process.Kill()
}
