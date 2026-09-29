package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const MaxCommandOutputBytes = 256 << 10

const truncatedOutput = "\n[command output truncated]\n"

type CommandResult struct {
	Output   string
	ExitCode int
}

// RunCommand runs the exact approved shell text in root. The working directory
// is not a sandbox: approved commands can access other paths and the network.
// A nonzero shell exit returns a result, not a launch error.
func RunCommand(ctx context.Context, root, command string) (CommandResult, error) {
	canonical, err := filepath.Abs(root)
	if err != nil {
		return CommandResult{}, fmt.Errorf("repository: %w", err)
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return CommandResult{}, fmt.Errorf("repository: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return CommandResult{}, fmt.Errorf("repository: %w", err)
	}
	if !info.IsDir() {
		return CommandResult{}, fmt.Errorf("repository %q is not a directory", root)
	}
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = canonical
	// Isolate the shell and its descendants so cancelling does not leave a
	// pipeline running after the approval's run has ended.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL); err != nil {
			if errors.Is(err, unix.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	// A child that inherits stdout/stderr must not keep Wait blocked forever
	// after its parent has been cancelled or has exited.
	cmd.WaitDelay = 2 * time.Second
	var output limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	result := CommandResult{Output: output.String()}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err == nil {
		return result, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("execute command: %w", err)
}

type limitedOutput struct {
	data      []byte
	truncated bool
}

func (o *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n > MaxCommandOutputBytes-len(o.data) {
		o.truncated = true
	}
	if remaining := MaxCommandOutputBytes - len(o.data); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		o.data = append(o.data, p...)
	}
	return n, nil
}

func (o *limitedOutput) String() string {
	if !o.truncated {
		return string(o.data)
	}
	// Keep the returned output bounded too, including its visible marker.
	return string(o.data[:MaxCommandOutputBytes-len(truncatedOutput)]) + truncatedOutput
}
