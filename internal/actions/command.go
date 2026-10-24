package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const MaxCommandOutputBytes = 256 << 10

const truncatedOutput = "\n[command output truncated]\n"

type CommandResult struct {
	Output   string
	ExitCode int

	// RunCommandCaptured fills these fields with bounded, marker-free UTF-8
	// output and completeness metadata. RunCommand keeps its legacy preview.
	Captured  string
	Truncated bool
	Discarded int64

	// BackgroundLeftRunning reports that a background process still held the
	// output open after the shell exited. It was left running, as background
	// jobs are, but output it wrote after the capture closed was not captured,
	// and a further write to that closed output may end it (SIGPIPE).
	BackgroundLeftRunning bool
}

// RunCommand runs the exact approved shell text in root. The working directory
// is not a sandbox: approved commands can access other paths and the network.
// A nonzero shell exit returns a result, not a launch error.
func RunCommand(ctx context.Context, root, command string) (CommandResult, error) {
	var output limitedOutput
	exit, err := executeCommand(ctx, root, command, &output)
	if err != nil {
		// Preserve the legacy result on cancellation or an execution error.
		exit.code = 0
	}
	return CommandResult{Output: output.String(), ExitCode: exit.code, BackgroundLeftRunning: exit.backgroundLeftRunning}, err
}

// commandExit is how one shell run ended.
type commandExit struct {
	code int
	// backgroundLeftRunning reports that a background process kept the
	// output open past the shell's exit; see CommandResult.BackgroundLeftRunning.
	backgroundLeftRunning bool
}

// executeCommand shares the exact-shell execution and cancellation behavior.
// Callers must validate and authorize the command before reaching this helper.
func executeCommand(ctx context.Context, root, command string, output io.Writer) (commandExit, error) {
	canonical, err := filepath.Abs(root)
	if err != nil {
		return commandExit{}, fmt.Errorf("repository: %w", err)
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return commandExit{}, fmt.Errorf("repository: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return commandExit{}, fmt.Errorf("repository: %w", err)
	}
	if !info.IsDir() {
		return commandExit{}, fmt.Errorf("repository %q is not a directory", root)
	}
	if err := ctx.Err(); err != nil {
		return commandExit{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = canonical
	cmd.Env = CommandEnv(os.Environ())
	// Isolate the shell and its descendants so cancelling does not leave a
	// pipeline running after the approval's run has ended.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return KillProcessGroup(cmd.Process.Pid)
	}
	// A child that inherits stdout/stderr must not keep Wait blocked forever
	// after its parent has been cancelled or has exited.
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	exit := commandExit{}
	if cmd.ProcessState != nil {
		exit.code = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		// Cancel only fires while the shell runs; a shell that exited just
		// before the cancel can still leave its pipeline behind. A command
		// that finished normally keeps its background jobs (`server &`).
		if cmd.Process != nil {
			if killErr := KillLeftoverProcessGroup(cmd.Process.Pid); killErr != nil {
				return exit, fmt.Errorf("stop cancelled command: %w", killErr)
			}
		}
		return exit, ctx.Err()
	}
	if err == nil {
		return exit, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exit.code = exitErr.ExitCode()
		return exit, nil
	}
	// Wait reports ErrWaitDelay only when the shell itself exited 0 but a
	// background child still held the output pipes: the command succeeded,
	// and the child keeps running with its output no longer captured.
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		exit.backgroundLeftRunning = true
		return exit, nil
	}
	return exit, fmt.Errorf("execute command: %w", err)
}

// KillProcessGroup sends SIGKILL to every process in the group led by pid.
// An already empty group reports os.ErrProcessDone.
func KillProcessGroup(pid int) error {
	if err := unix.Kill(-pid, unix.SIGKILL); err != nil {
		if errors.Is(err, unix.ESRCH) {
			return os.ErrProcessDone
		}
		return fmt.Errorf("kill process group %d: %w", pid, err)
	}
	return nil
}

// KillLeftoverProcessGroup kills what remains of the group led by pid after
// its leader has been reaped. Nothing left is not an error, and neither is
// EPERM: Darwin reports it for a group holding only zombies that init has
// yet to reap, and a live member this user may not signal could not be
// stopped by a retry either.
func KillLeftoverProcessGroup(pid int) error {
	err := KillProcessGroup(pid)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, unix.EPERM) {
		return nil
	}
	return err
}

// CommandEnv returns env without Likha's provider credentials: commands the
// model runs (tests, scripts, installs) never inherit LIKHA_API_KEY or any
// LIKHA_<NAME>_API_KEY, whether or not the user reviewed them.
func CommandEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name == "LIKHA_API_KEY" || strings.HasPrefix(name, "LIKHA_") && strings.HasSuffix(name, "_API_KEY") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
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
