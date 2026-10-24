package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// startFake starts the fake stdio server with extra environment and closes
// it when the test ends.
func startFake(t *testing.T, env map[string]string) *Client {
	t.Helper()
	cfg := fakeServerCommand(t)
	for key, value := range env {
		cfg.Env[key] = value
	}
	client, err := Start(context.Background(), "fake", cfg, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// processState returns ps's state letters for pid ("Z" for a zombie), or ""
// once the process no longer exists.
func processState(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("ps: %v", err)
	}
	return strings.TrimSpace(string(out)) // ps exits 1 with no output when pid is gone
}

// readPIDs waits for the fake server to record at least one process ID.
func readPIDs(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && strings.HasSuffix(string(raw), "\n") {
			var pids []int
			for _, field := range strings.Fields(string(raw)) {
				pid, err := strconv.Atoi(field)
				if err != nil {
					t.Fatalf("pid file %s: %v", path, err)
				}
				pids = append(pids, pid)
			}
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid recorded in %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStdioServerRequestSharingAPendingIDIsAnswered(t *testing.T) {
	client := startFake(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, isError, err := client.Call(ctx, "ask", json.RawMessage(`{}`))
	if err != nil || isError {
		t.Fatalf("call: result=%q isError=%t err=%v", result, isError, err)
	}
	answers := strings.Split(result, "\n")
	if len(answers) != 2 {
		t.Fatalf("a server request was taken as the call's response: result = %q", result)
	}
	cases := []struct {
		name   string
		answer string
		want   rpcResponse
	}{
		{"ping gets an empty result", answers[0], rpcResponse{JSONRPC: "2.0", ID: 3, Result: json.RawMessage(`{}`)}},
		{"sampling is refused", answers[1], rpcResponse{JSONRPC: "2.0", ID: 3, Error: &rpcError{Code: -32601, Message: "method not found"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got rpcResponse
			if err := json.Unmarshal([]byte(tc.answer), &got); err != nil {
				t.Fatalf("answer %q: %v", tc.answer, err)
			}
			if got.ID != tc.want.ID || string(got.Result) != string(tc.want.Result) || (got.Error == nil) != (tc.want.Error == nil) || got.Error != nil && *got.Error != *tc.want.Error {
				t.Fatalf("answer = %s, want id %d result %s error %+v", tc.answer, tc.want.ID, tc.want.Result, tc.want.Error)
			}
		})
	}
}

func TestStdioWriteToDeafServerHonorsContext(t *testing.T) {
	client := startFake(t, map[string]string{"LIKHA_FAKE_MCP_MODE": "deaf"})
	// Far larger than a pipe buffer: the write cannot complete while the
	// server does not read.
	large, err := json.Marshal(map[string]string{"message": strings.Repeat("x", 4<<20)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, args := range []json.RawMessage{large, json.RawMessage(`{}`)} {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			_, _, err := client.Call(ctx, "echo", args)
			done <- err
		}()
	}
	for range 2 {
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("call to a deaf server: err = %v, want the deadline", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a call blocked on the server's full stdin ignored its deadline")
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("close blocked behind the stuck write")
	}
}

func TestStdioCloseReapsServerAndItsChildren(t *testing.T) {
	childFile := filepath.Join(t.TempDir(), "children")
	client := startFake(t, map[string]string{"LIKHA_FAKE_MCP_CHILDFILE": childFile})
	server := client.conn.(*stdioTransport).cmd.Process.Pid
	child := readPIDs(t, childFile)[0]
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if state := processState(t, server); state != "" {
		t.Fatalf("server process %d left behind in state %q after close", server, state)
	}
	// The orphaned child is reaped by init, not by Likha: wait for it to go.
	deadline := time.Now().Add(5 * time.Second)
	for {
		state := processState(t, child)
		if state == "" || strings.HasPrefix(state, "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server's child %d still running (state %q) after close", child, state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStdioCloseKillsServerIgnoringSIGTERM(t *testing.T) {
	old := stdioStopGrace
	stdioStopGrace = 100 * time.Millisecond
	t.Cleanup(func() { stdioStopGrace = old })
	ready := filepath.Join(t.TempDir(), "ready")
	cfg := ServerConfig{Command: "sh", Args: []string{"-c", `trap '' TERM; echo $$ > "$0"; cat > /dev/null; sleep 300`, ready}}
	conn, err := startStdio("stubborn", cfg, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	pid := readPIDs(t, ready)[0] // SIGTERM is ignored from here on
	closed := make(chan error, 1)
	go func() { closed <- conn.close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("close did not kill a server that ignores SIGTERM")
	}
	if state := processState(t, pid); state != "" {
		t.Fatalf("server process %d left behind in state %q after close", pid, state)
	}
	if err := conn.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// waitForFile waits until a server under test has created path.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagerRendersWhileAServerStarts(t *testing.T) {
	old := CallTimeout
	CallTimeout = 3 * time.Second
	t.Cleanup(func() { CallTimeout = old })
	started := filepath.Join(t.TempDir(), "started")
	// A server that never answers the handshake keeps Start busy until
	// CallTimeout.
	silent := ServerConfig{Command: "sh", Args: []string{"-c", `touch "$0"; cat > /dev/null`, started}}
	manager := NewMcpManagerForTest(map[string]ServerConfig{"silent": silent}, nil, nil, nil)
	defer manager.Stop()
	catalog := make(chan []CatalogTool, 1)
	go func() { catalog <- manager.CatalogTools("likha/test") }()
	waitForFile(t, started)

	rendered := make(chan string, 1)
	go func() { rendered <- manager.Summary() + "\n" + manager.Status() }()
	select {
	case got := <-rendered:
		if !strings.HasPrefix(got, "mcp 0/1\n") {
			t.Fatalf("render during start = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Summary/Status blocked while a server handshake was in flight")
	}
	select {
	case tools := <-catalog:
		if len(tools) != 0 {
			t.Fatalf("silent server produced tools: %+v", tools)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CatalogTools did not return after the handshake deadline")
	}
	if got := manager.Summary(); got != "mcp 0/1 crashed" {
		t.Fatalf("summary after failed start = %q", got)
	}
}

func TestManagerConcurrentCatalogStartsServerOnce(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	cfg := fakeServerCommand(t)
	cfg.Env["LIKHA_FAKE_MCP_MODE"] = "slow-init"
	cfg.Env["LIKHA_FAKE_MCP_PIDFILE"] = pidFile
	manager := NewMcpManagerForTest(map[string]ServerConfig{"fake": cfg}, nil, nil, nil)
	defer manager.Stop()
	results := make(chan []CatalogTool, 2)
	for range 2 {
		go func() { results <- manager.CatalogTools("likha/test") }()
	}
	for range 2 {
		tools := <-results
		if len(tools) != 1 || tools[0].OriginalName != "echo" {
			t.Fatalf("catalog = %+v, want the echo tool for every caller", tools)
		}
	}
	if pids := readPIDs(t, pidFile); len(pids) != 1 {
		t.Fatalf("server started %d times, want once", len(pids))
	}
}

func TestManagerStopDuringStartClosesLateClient(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	cfg := fakeServerCommand(t)
	cfg.Env["LIKHA_FAKE_MCP_MODE"] = "slow-init"
	cfg.Env["LIKHA_FAKE_MCP_PIDFILE"] = pidFile
	manager := NewMcpManagerForTest(map[string]ServerConfig{"fake": cfg}, nil, nil, nil)
	catalog := make(chan []CatalogTool, 1)
	go func() { catalog <- manager.CatalogTools("likha/test") }()
	pid := readPIDs(t, pidFile)[0]
	manager.Stop()
	<-catalog
	if got := manager.Summary(); got != "mcp 0/1" {
		t.Fatalf("summary after stop = %q, want no running server", got)
	}
	if state := processState(t, pid); state != "" {
		t.Fatalf("server %d started during Stop is still around in state %q", pid, state)
	}
	if tools := manager.CatalogTools("likha/test"); len(tools) != 0 {
		t.Fatalf("a stopped manager started servers again: %+v", tools)
	}
}

func TestStdioWriteAfterCloseFailsAtOnce(t *testing.T) {
	conn, err := startStdio("reader", ServerConfig{Command: "sh", Args: []string{"-c", "cat > /dev/null"}}, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Each send may land in the queue writeLoop no longer drains; more sends
	// than the queue holds make sure some do.
	for i := range stdioWriteQueue + 4 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		start := time.Now()
		err := conn.notify(ctx, []byte(`{"jsonrpc":"2.0","method":"ping"}`))
		cancel()
		if !errors.Is(err, errStdioStopped) {
			t.Fatalf("send %d after close: err = %v, want %v", i, err, errStdioStopped)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("send %d after close waited %s for its deadline", i, elapsed)
		}
	}
}

func TestStdioPendingCallFailsWhenClosed(t *testing.T) {
	old := stdioStopGrace
	stdioStopGrace = 3 * time.Second
	t.Cleanup(func() { stdioStopGrace = old })
	ready := filepath.Join(t.TempDir(), "ready")
	// Reads every request, answers none, and ignores SIGTERM, so only the
	// kill after stdioStopGrace would end it.
	cfg := ServerConfig{Command: "sh", Args: []string{"-c", `trap '' TERM; echo $$ > "$0"; cat > /dev/null; sleep 300`, ready}}
	conn, err := startStdio("silent", cfg, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	readPIDs(t, ready)
	called := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := conn.call(ctx, 1, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		called <- err
	}()
	time.Sleep(100 * time.Millisecond) // let the call write and start waiting
	closed := make(chan error, 1)
	go func() { closed <- conn.close() }()
	select {
	case err := <-called:
		if !errors.Is(err, errStdioStopped) {
			t.Fatalf("pending call: err = %v, want %v", err, errStdioStopped)
		}
	case <-time.After(time.Second):
		t.Fatal("a pending call kept waiting after close began")
	}
	if err := <-closed; err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestStdioCloseKillsDescendantIgnoringSIGTERM(t *testing.T) {
	old := stdioStopGrace
	stdioStopGrace = 200 * time.Millisecond
	t.Cleanup(func() { stdioStopGrace = old })
	ready := filepath.Join(t.TempDir(), "ready")
	// The server exits on SIGTERM; its child stays in the group, ignores
	// SIGTERM, and holds neither pipe, so readLoop reaps the server at once.
	script := `sh -c 'trap "" TERM; echo $$ > "$0"; exec sleep 300' "$0" </dev/null >/dev/null 2>&1 &
cat > /dev/null`
	conn, err := startStdio("wrapper", ServerConfig{Command: "sh", Args: []string{"-c", script, ready}}, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	child := readPIDs(t, ready)[0] // SIGTERM is ignored from here on
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if err := conn.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// The orphaned child is reaped by init, not by Likha: wait for it to go.
	deadline := time.Now().Add(5 * time.Second)
	for {
		state := processState(t, child)
		if state == "" || strings.HasPrefix(state, "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server's SIGTERM-ignoring child %d still running (state %q) after close", child, state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManagerStopClosesServersConcurrently(t *testing.T) {
	old := stdioStopGrace
	stdioStopGrace = 500 * time.Millisecond
	t.Cleanup(func() { stdioStopGrace = old })
	const servers = 3
	clients := make(map[string]*Client, servers)
	var pids []int
	for i := range servers {
		name := "stubborn" + strconv.Itoa(i)
		ready := filepath.Join(t.TempDir(), "ready")
		// Ignores SIGTERM and outlives its closed stdin: only the kill after
		// stdioStopGrace ends it.
		cfg := ServerConfig{Command: "sh", Args: []string{"-c", `trap '' TERM; echo $$ > "$0"; cat > /dev/null; sleep 300`, ready}}
		conn, err := startStdio(name, cfg, "likha/test")
		if err != nil {
			t.Fatal(err)
		}
		client := &Client{name: name, conn: conn}
		t.Cleanup(func() { client.Close() })
		clients[name] = client
		pids = append(pids, readPIDs(t, ready)[0])
	}
	manager := NewMcpManagerForTest(nil, clients, nil, nil)
	start := time.Now()
	manager.Stop()
	elapsed := time.Since(start)
	// Closing in turn takes a grace per server; concurrently, about one.
	if limit := 2 * stdioStopGrace; elapsed >= limit {
		t.Fatalf("Stop took %s for %d servers ignoring SIGTERM, want under %s", elapsed, servers, limit)
	}
	for _, pid := range pids {
		if state := processState(t, pid); state != "" {
			t.Fatalf("server process %d left behind in state %q after Stop", pid, state)
		}
	}
}

func TestStdioCloseKillsGroupLeftByAnExitedServer(t *testing.T) {
	old := stdioStopGrace
	stdioStopGrace = 200 * time.Millisecond
	t.Cleanup(func() { stdioStopGrace = old })
	ready := filepath.Join(t.TempDir(), "ready")
	// The server exits once its child is ready; the child stays in the
	// group, ignores SIGTERM, and holds neither pipe, so readLoop reaps the
	// server before close runs.
	script := `sh -c 'trap "" TERM; echo $$ > "$0"; exec sleep 300' "$0" </dev/null >/dev/null 2>&1 &
while [ ! -s "$0" ]; do sleep 0.01; done
exit 0`
	conn, err := startStdio("exited", ServerConfig{Command: "sh", Args: []string{"-c", script, ready}}, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	child := readPIDs(t, ready)[0]
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	select {
	case <-conn.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit on its own")
	}
	if err := conn.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// The orphaned child is reaped by init, not by Likha: wait for it to go.
	deadline := time.Now().Add(5 * time.Second)
	for {
		state := processState(t, child)
		if state == "" || strings.HasPrefix(state, "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child %d of an exited server still running (state %q) after close", child, state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManagerStopEndsAnInFlightHandshake(t *testing.T) {
	oldTimeout, oldGrace := CallTimeout, stdioStopGrace
	CallTimeout = time.Minute // far longer than Stop may take
	stdioStopGrace = 200 * time.Millisecond
	t.Cleanup(func() { CallTimeout, stdioStopGrace = oldTimeout, oldGrace })
	ready := filepath.Join(t.TempDir(), "ready")
	// Never answers the handshake and ignores SIGTERM, so only the group
	// kill after stdioStopGrace ends it.
	silent := ServerConfig{Command: "sh", Args: []string{"-c", `trap '' TERM; echo $$ > "$0"; cat > /dev/null; sleep 300`, ready}}
	manager := NewMcpManagerForTest(map[string]ServerConfig{"silent": silent}, nil, nil, nil)
	catalog := make(chan []CatalogTool, 1)
	go func() { catalog <- manager.CatalogTools("likha/test") }()
	pid := readPIDs(t, ready)[0]
	// Without the fix the handshake would hold the server for CallTimeout.
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-catalog
	})

	start := time.Now()
	manager.Stop()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Stop took %s with a handshake in flight", elapsed)
	}
	if state := processState(t, pid); state != "" {
		t.Fatalf("server %d whose handshake was in flight outlived Stop in state %q", pid, state)
	}
	select {
	case tools := <-catalog:
		catalog <- tools // the cleanup waits for it too
		if len(tools) != 0 {
			t.Fatalf("a stopped handshake produced tools: %+v", tools)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CatalogTools kept waiting on a handshake after Stop")
	}
}

// killRecorder replaces stdioKill for one test and records every group
// signal it passes through (probes with signal 0 are not recorded).
type killRecorder struct {
	mu      sync.Mutex
	signals []unix.Signal
}

func recordKills(t *testing.T) *killRecorder {
	t.Helper()
	recorder := &killRecorder{}
	old := stdioKill
	stdioKill = func(pid int, signal unix.Signal) error {
		if signal != 0 {
			recorder.mu.Lock()
			recorder.signals = append(recorder.signals, signal)
			recorder.mu.Unlock()
		}
		return unix.Kill(pid, signal)
	}
	t.Cleanup(func() { stdioKill = old })
	return recorder
}

func (r *killRecorder) take() []unix.Signal {
	r.mu.Lock()
	defer r.mu.Unlock()
	signals := r.signals
	r.signals = nil
	return signals
}

// A server whose whole group ended long before close leaves a process group
// ID the kernel may hand to an unrelated job; close must not signal it.
func TestStdioCloseNeverSignalsAGroupSeenEmpty(t *testing.T) {
	recorder := recordKills(t)
	// The background child holds stdout, so readLoop reaps the server only
	// after the child, the group's last member, has exited.
	conn, err := startStdio("finished", ServerConfig{Command: "sh", Args: []string{"-c", "sleep 0.2 & exit 0"}}, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit on its own")
	}
	pgid := conn.cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for unix.Kill(-pgid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the finished server's group never emptied")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the reap-time check observe it too
	recorder.take()
	if err := conn.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if signals := recorder.take(); len(signals) != 0 {
		t.Fatalf("close signalled an empty, possibly reused, group: %v", signals)
	}
}

// A leftover group member of a server that exited on its own is stopped
// when the server is reaped, not only when close runs much later.
func TestStdioReapStopsLeftoverGroup(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	script := `sh -c 'echo $$ > "$0"; exec sleep 300' "$0" </dev/null >/dev/null 2>&1 &
while [ ! -s "$0" ]; do sleep 0.01; done
exit 0`
	conn, err := startStdio("leftover", ServerConfig{Command: "sh", Args: []string{"-c", script, ready}}, "likha/test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.close() })
	child := readPIDs(t, ready)[0]
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		state := processState(t, child)
		if state == "" || strings.HasPrefix(state, "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("leftover child %d still running (state %q) after its server was reaped", child, state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A server queued behind a start that Stop cancelled is never launched: the
// fork would only give Stop another process to wait for, or orphan it. The
// queued command does not exist, so any launch attempt surfaces as a
// recorded start failure instead of Stop's cancellation.
func TestManagerStopSkipsQueuedStarts(t *testing.T) {
	slowPIDs := filepath.Join(t.TempDir(), "slow")
	slow := fakeServerCommand(t)
	slow.Env["LIKHA_FAKE_MCP_MODE"] = "slow-init"
	slow.Env["LIKHA_FAKE_MCP_PIDFILE"] = slowPIDs
	queued := ServerConfig{Command: filepath.Join(t.TempDir(), "missing-server")}
	// Starts run in name order, so "b-queued" waits behind "a-slow".
	manager := NewMcpManagerForTest(map[string]ServerConfig{"a-slow": slow, "b-queued": queued}, nil, nil, nil)
	catalog := make(chan []CatalogTool, 1)
	go func() { catalog <- manager.CatalogTools("likha/test") }()
	readPIDs(t, slowPIDs)
	manager.Stop()
	<-catalog
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if reason, failed := manager.failed["b-queued"]; failed {
		t.Fatalf("a server queued behind a cancelled start was launched after Stop: %s", reason)
	}
}

// Start with an already cancelled context forks nothing.
func TestStartWithCancelledContextLaunchesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Start(ctx, "late", ServerConfig{Command: filepath.Join(t.TempDir(), "missing-server")}, "likha/test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start after cancel = %v, want context.Canceled before any launch", err)
	}
}
