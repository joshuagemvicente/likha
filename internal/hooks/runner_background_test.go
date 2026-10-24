package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFireBackgroundChildDoesNotFailHook(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	// The background sleep inherits stdout, so the hook's pipe stays open
	// after it exits 0 and WaitDelay expires.
	hook := script(t, `cat >/dev/null; sleep 30 & echo $! > `+pidFile)
	out := runner(Hook{Event: PreTool, Name: "bg", Command: hook}).Fire(context.Background(), toolEvent("read", `{}`))
	if out.Block != nil || len(out.Warnings) != 0 {
		t.Fatalf("a hook that exited 0 was treated as failed: %+v", out)
	}
	pid := readHookPID(t, pidFile)
	t.Cleanup(func() { _ = unix.Kill(pid, unix.SIGKILL) })
	// A killed child is reparented to init and reaped well within this
	// window, so a lingering zombie does not pass for a running job.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := unix.Kill(pid, 0); err != nil {
			t.Fatalf("background job %d was stopped with its hook: %v", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFireBackgroundJobFinishesItsWork(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "played")
	// The shape of `afplay done.aiff &`: detached work that outlasts the hook.
	hook := script(t, `cat >/dev/null; (sleep 0.3; touch "`+marker+`") >/dev/null 2>&1 &`)
	out := runner(Hook{Event: RunEnd, Name: "sound", Command: hook}).Fire(context.Background(), Event{Event: RunEnd, Outcome: "completed"})
	if len(out.Warnings) != 0 {
		t.Fatalf("warnings = %v", out.Warnings)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the hook's background job never finished its work")
}

func TestFireTimeoutStopsBackgroundChildren(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"while the hook runs", `sleep 30 >/dev/null 2>&1 & echo $! > PID; sleep 30`},
		{"while the output is held open", `sleep 30 & echo $! > PID`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			// Inline shell, not a script file: macOS may scan a fresh
			// executable for longer than the short timeout.
			hook := "cat >/dev/null; " + strings.ReplaceAll(tc.body, "PID", pidFile)
			out := runner(Hook{Event: PreTool, Name: "slow", Command: hook, Timeout: 300 * time.Millisecond, FailOpen: true}).Fire(context.Background(), toolEvent("read", `{}`))
			if len(out.Warnings) == 0 || !strings.Contains(out.Warnings[0], "timed out") {
				t.Fatalf("warnings = %v, want a timeout", out.Warnings)
			}
			pid := readHookPID(t, pidFile)
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if err := unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			_ = unix.Kill(pid, unix.SIGKILL)
			t.Fatalf("background process %d outlived its timed-out hook", pid)
		})
	}
}

func readHookPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid %q: %v", data, err)
	}
	return pid
}
