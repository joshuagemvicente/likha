package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"likha/internal/model"
	"likha/internal/session"
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
	t.Setenv("LIKHA_MODEL", "")
	t.Setenv("LIKHA_ENDPOINT", "")
	t.Setenv("LIKHA_STATE_DIR", t.TempDir())
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
	for _, key := range []string{"LIKHA_MODEL", "LIKHA_PROVIDER", "LIKHA_API_KEY", "LIKHA_ENDPOINT"} {
		t.Setenv(key, "")
	}
	t.Setenv("LIKHA_STATE_DIR", t.TempDir())
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
	if !strings.Contains(stdout.String(), "Usage: likha") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	for _, expected := range []string{"--provider", "--api-key", "--model", "--endpoint", "--sessions", "--resume", "--debug-models", "LIKHA_DEBUG_MODELS", "--ascii", "LIKHA_ASCII", "--version", "LIKHA_STATE_DIR", "Providers", "BYOK"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("help is missing %q: %q", expected, stdout.String())
		}
	}
}

// A malformed flag must keep stdout clean so a `-p --json` consumer never
// parses help text as events; only an explicit --help prints to stdout.
func TestRunFlagParseErrorWritesUsageToStderr(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--no-such-flag"}},
		{"unknown flag in a json pipeline", []string{"-p", "hi", "--json", "--no-such-flag"}},
		{"missing flag value", []string{"--model"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr); code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), "Usage: likha") {
				t.Fatalf("stderr = %q, want the usage text", stderr.String())
			}
		})
	}
}

func TestASCIIFlagAndEnvironment(t *testing.T) {
	t.Setenv("LIKHA_ASCII", "")
	if asciiGlyphs(false) {
		t.Fatal("Unicode glyphs must be the default")
	}
	if !asciiGlyphs(true) {
		t.Fatal("--ascii must select the ASCII glyph set")
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--ascii", "--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--ascii must parse: exit code = %d, stderr = %q", code, stderr.String())
	}
	t.Setenv("LIKHA_ASCII", "1")
	if !asciiGlyphs(false) {
		t.Fatal("LIKHA_ASCII=1 must select the ASCII glyph set")
	}
	t.Setenv("LIKHA_ASCII", "0")
	if asciiGlyphs(false) {
		t.Fatal("LIKHA_ASCII other than 1 must keep Unicode")
	}
}

func TestSessionListWorksWithoutModelOrTerminal(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	t.Setenv("LIKHA_STATE_DIR", base)
	t.Setenv("LIKHA_MODEL", "")
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

// resumeWithSelection saves a session that ran on selection and returns its
// repository and ID.
func resumeWithSelection(t *testing.T, state string, selection model.Selection) (string, string) {
	t.Helper()
	repo := t.TempDir()
	store, err := session.Open(state, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Selection = &selection
	snapshot.Entries = []session.Entry{{Role: "user", Content: "earlier"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	return repo, snapshot.ID
}

func TestResumeSavedProviderNotice(t *testing.T) {
	cases := []struct {
		name      string
		selection model.Selection
		want      string // "" means no notice at all
	}{
		{"custom endpoint saves no provider", model.Selection{Model: "test-model"}, ""},
		{"unknown provider", model.Selection{Provider: "nonesuch", Model: "m"}, "saved session provider nonesuch is unavailable (not a predefined provider); using launch preferences"},
		{"provider without a key names why", model.Selection{Provider: "openrouter", Model: "m"}, "saved session provider openrouter is unavailable ("},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := headlessEnv(t)
			t.Setenv("OPENROUTER_API_KEY", "")
			t.Setenv("LIKHA_OPENROUTER_API_KEY", "")
			repo, id := resumeWithSelection(t, state, tc.selection)
			_, _, stderr := runApp("-p", "hi", "--resume", id, repo)
			if tc.want == "" {
				if strings.Contains(stderr, "saved session provider") {
					t.Fatalf("stderr = %q, want no saved-provider notice", stderr)
				}
				return
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tc.want)
			}
		})
	}
}

func TestResumeUnknownSessionNamesIt(t *testing.T) {
	headlessEnv(t)
	id := strings.Repeat("b", 32)
	code, _, stderr := runApp("-p", "hi", "--resume", id, t.TempDir())
	if code != 2 || !strings.Contains(stderr, "no session "+id+" in this repository") || strings.Contains(stderr, "sql:") {
		t.Fatalf("exit = %d stderr = %q", code, stderr)
	}
}
