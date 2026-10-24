package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// likhaBinary is a build of ./cmd/likha shared by tests that need real exit
// codes or signals; `go run` reports every child failure as exit status 1.
var likhaBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "likha-integration-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create build directory: %v\n", err)
		os.Exit(1)
	}
	likhaBinary = filepath.Join(dir, "likha")
	build := exec.Command("go", "build", "-o", likhaBinary, "./cmd/likha")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build likha: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// likhaCommand runs the built binary with a private state directory and no
// inherited Likha provider settings.
func likhaCommand(state string, args ...string) *exec.Cmd {
	cmd := exec.Command(likhaBinary, args...)
	cmd.Env = append(os.Environ(), "LIKHA_PROVIDER=", "LIKHA_MODEL=", "LIKHA_API_KEY=", "LIKHA_ENDPOINT=",
		"LIKHA_OPENAI_API_KEY=", "LIKHA_STATE_DIR="+state)
	return cmd
}
