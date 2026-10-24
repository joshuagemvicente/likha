package actions

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

func TestRunCommandBackgroundChildDoesNotFailSuccessfulCommand(t *testing.T) {
	root := t.TempDir()
	// The background sleep inherits stdout, so the pipe stays open after the
	// shell exits 0 and WaitDelay expires.
	result, err := RunCommandCaptured(context.Background(), root, "echo hi; sleep 30 & echo $! > pid", 1<<20)
	if err != nil {
		t.Fatalf("RunCommandCaptured: %v", err)
	}
	pid := readPID(t, filepath.Join(root, "pid"))
	t.Cleanup(func() { _ = unix.Kill(pid, unix.SIGKILL) })
	if result.ExitCode != 0 || !strings.Contains(result.Captured, "hi\n") {
		t.Fatalf("result = %+v", result)
	}
	if !result.BackgroundLeftRunning {
		t.Fatal("result does not report that background output was cut off")
	}
}

func TestRunCommandLeavesBackgroundJobsRunning(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"holding the output pipe", "sleep 30 & echo $! > pid"},
		{"detached from the output pipe", "sleep 30 >/dev/null 2>&1 & echo $! > pid"},
		{"after a failing exit", "sleep 30 >/dev/null 2>&1 & echo $! > pid; exit 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := RunCommandCaptured(context.Background(), root, tc.command, 1<<20); err != nil {
				t.Fatalf("RunCommandCaptured: %v", err)
			}
			pid := readPID(t, filepath.Join(root, "pid"))
			t.Cleanup(func() { _ = unix.Kill(pid, unix.SIGKILL) })
			assertStillRunning(t, pid)
		})
	}
}

func TestRunCommandBackgroundJobFinishesItsWork(t *testing.T) {
	root := t.TempDir()
	if _, err := RunCommandCaptured(context.Background(), root, "(sleep 0.3; touch marker) >/dev/null 2>&1 &", 1<<20); err != nil {
		t.Fatalf("RunCommandCaptured: %v", err)
	}
	marker := filepath.Join(root, "marker")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background job never created its marker")
}

func TestRunCommandCancelStopsBackgroundChildren(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"while the shell runs", "sleep 30 >/dev/null 2>&1 & echo $! > pid; sleep 30"},
		{"while the output is held open", "sleep 30 & echo $! > pid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			if _, err := RunCommandCaptured(ctx, root, tc.command, 1<<20); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("RunCommandCaptured error = %v, want deadline exceeded", err)
			}
			waitForExit(t, readPID(t, filepath.Join(root, "pid")))
		})
	}
}

// assertStillRunning fails if pid exits within half a second. A killed
// child is reparented to init and reaped well within that window, so a
// process that merely lingers as a zombie does not pass for a running one.
func assertStillRunning(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := unix.Kill(pid, 0); err != nil {
			t.Fatalf("background job %d was stopped with its command: %v", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("bad pid %q: %v", data, err)
	}
	return pid
}

// waitForExit fails unless pid is gone shortly after the command returned.
// The killed child is reparented to init, which reaps it promptly.
func waitForExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background process %d outlived its cancelled command", pid)
}
