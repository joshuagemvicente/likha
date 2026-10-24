package integration_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A consumer that stops reading (`likha -p … --json | head -1`) closes
// stdout under the run. The write fails, the run stops and still saves its
// session, and Likha exits 1 for output not written instead of dying of
// SIGPIPE with the prompt lost.
func TestHeadlessClosedStdoutSavesSessionAndExitsOne(t *testing.T) {
	fake := newScriptedProvider(t, "text:nobody reads this")
	state, repo := t.TempDir(), t.TempDir()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// No reader: the run's first write to stdout fails.
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := likhaCommand(state, append(fake.headlessArgs("--json", "-p", "closed pipe question"), repo)...)
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = write, &errOut
	err = cmd.Run()
	if closeErr := write.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("exit = %v, want exit status 1\n%s", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "likha: write output: ") {
		t.Fatalf("stderr does not report the failed write:\n%s", errOut.String())
	}
	// Nobody reads the run, so it stops before its first request.
	if posts := fake.postCount(); posts != 0 {
		t.Fatalf("the run sent %d model requests after its output failed", posts)
	}
	code, list, listErr := runLikha(t, likhaCommand(state, "--sessions", repo))
	if code != 0 || !strings.Contains(list, `"closed pipe question"`) {
		t.Fatalf("--sessions = %d %q (%s); the run was not saved", code, list, listErr)
	}
}
