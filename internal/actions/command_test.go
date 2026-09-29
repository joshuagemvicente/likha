package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCommandExactTextCWDAndExit(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	// The caller merely holding a command string (or rejecting it) does nothing.
	command := "printf 'standard\\n'; printf 'error\\n' >&2; pwd; printf 'ran' > marker; exit 13"
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran without approval: %v", err)
	}
	result, err := RunCommand(context.Background(), root, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 13 || !strings.Contains(result.Output, "standard\n") || !strings.Contains(result.Output, "error\n") || !strings.Contains(result.Output, root+"\n") {
		t.Fatalf("command result omitted output, cwd, or exit status: %+v", result)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "ran" {
		t.Fatalf("approved command did not execute in repository: %q, %v", data, err)
	}
}

func TestRunCommandCancellation(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := RunCommand(ctx, root, "sleep 10; printf 'should-not-run' > marker")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation must not become a successful exit: %+v, %v", result, err)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("cancellation did not promptly stop the command")
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled command continued: %v", err)
	}
	cancelled, cancelImmediately := context.WithCancel(context.Background())
	cancelImmediately()
	if _, err := RunCommand(cancelled, root, "touch marker"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context ran command: %v", err)
	}
}

func TestRunCommandOutputIsBounded(t *testing.T) {
	root := t.TempDir()
	result, err := RunCommand(context.Background(), root, "yes x | dd bs=8192 count=128 2>/dev/null")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("large-output command failed: %+v, %v", result, err)
	}
	if len(result.Output) != MaxCommandOutputBytes || !strings.HasSuffix(result.Output, truncatedOutput) || !strings.HasPrefix(result.Output, "x\n") {
		t.Fatalf("output bound/marker incorrect: bytes=%d, suffix=%q", len(result.Output), result.Output[len(result.Output)-len(truncatedOutput):])
	}
}
