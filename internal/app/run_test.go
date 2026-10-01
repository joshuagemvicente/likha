package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lisa/internal/session"
)

func TestResolveRootFollowsSelectedRepository(t *testing.T) {
	repo := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-repository")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	root, err := resolveRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Fatalf("selected repository = %q, want %q", root, want)
	}
}

func TestRunRejectsInvalidRepository(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"regular file", []string{file}},
		{"missing directory", []string{filepath.Join(t.TempDir(), "absent")}},
		{"extra path", []string{t.TempDir(), t.TempDir()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr); code == 0 {
				t.Fatalf("accepted invalid repository: %v", tc.args)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunReportsMissingModel(t *testing.T) {
	t.Setenv("LISA_MODEL", "")
	t.Setenv("LISA_ENDPOINT", "")
	t.Setenv("LISA_STATE_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	// A closed port keeps the test independent of any real model server; with
	// no provider named, the custom endpoint's discovery fails and names the
	// missing configuration.
	if code := Run([]string{"--endpoint", "http://127.0.0.1:9/v1", t.TempDir()}, &stdout, &stderr); code == 0 {
		t.Fatal("accepted missing model configuration")
	}
	if !strings.Contains(stderr.String(), "model is required") || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunWithoutConfigurationExplainsSetup(t *testing.T) {
	for _, key := range []string{"LISA_MODEL", "LISA_PROVIDER", "LISA_API_KEY", "LISA_ENDPOINT"} {
		t.Setenv(key, "")
	}
	t.Setenv("LISA_STATE_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := Run([]string{t.TempDir()}, &stdout, &stderr); code == 0 {
		t.Fatal("started without provider configuration in a non-interactive terminal")
	}
	if !strings.Contains(stderr.String(), "no provider configured") || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunHelpDoesNotRequireRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help failed: exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: lisa") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	for _, expected := range []string{"--provider", "--api-key", "--model", "--endpoint", "--sessions", "--resume", "--version", "LISA_STATE_DIR", "Providers", "BYOK"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("help is missing %q: %q", expected, stdout.String())
		}
	}
}

func TestSessionListWorksWithoutModelOrTerminal(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	t.Setenv("LISA_STATE_DIR", base)
	t.Setenv("LISA_MODEL", "")
	store, err := session.Open(base, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Entries = []session.Entry{{Role: "user", Content: "Inspect repository"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--sessions", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("listing failed: exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), snapshot.ID) || !strings.Contains(stdout.String(), "Inspect repository") {
		t.Fatalf("session was not selectable: %q", stdout.String())
	}
}
