package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
)

func TestStoredStatusLineConfigRoundTripAndLegacy(t *testing.T) {
	stateDir := t.TempDir()
	legacy := []byte(`{"provider":"openai","model":"gpt-4o-mini","theme":"default","composer":{"style":"bordered"},"future_setting":{"enabled":true}}`)
	if err := os.WriteFile(providers.ConfigFilePath(stateDir), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusLine != nil || cfg.Provider != "openai" || cfg.Model != "gpt-4o-mini" || cfg.Theme != "default" || cfg.Composer == nil || cfg.Composer.Style != "bordered" {
		t.Fatalf("legacy config changed on load: %+v", cfg)
	}
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["status_line"]; present {
		t.Fatalf("optional status fields were enabled by default: %s", data)
	}

	cfg.StatusLine = &providers.StoredStatusLineConfig{Folder: statusFlag(true), Branch: statusFlag(true), Session: true, Version: true}
	cfg.Theme = "habamax" // existing read-modify-write saves must keep the other preferences
	if err := providers.SaveStoredConfig(stateDir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := providers.LoadStoredConfig(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != cfg.Provider || loaded.Model != cfg.Model || loaded.Theme != cfg.Theme || loaded.Composer == nil || loaded.Composer.Style != cfg.Composer.Style || loaded.StatusLine == nil || !reflect.DeepEqual(loaded.StatusLine, cfg.StatusLine) {
		t.Fatalf("preferences changed after save: %+v", loaded)
	}
	data, err = os.ReadFile(providers.ConfigFilePath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status_line":{"folder":true,"branch":true,"session":true,"version":true}`) {
		t.Fatalf("status fields not saved together: %s", data)
	}
}

func TestStatusFolderHomeBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := statusFolder(home); got != "~" {
		t.Fatalf("home = %q", got)
	}
	child := filepath.Join(home, "projects", "lisa")
	if got := statusFolder(child); got != filepath.Join("~", "projects", "lisa") {
		t.Fatalf("child = %q", got)
	}
	other := home + "-other"
	if got := statusFolder(other); got != other {
		t.Fatalf("sibling should not be abbreviated: %q", got)
	}
}

func TestStatusFolderResolvesSymlinkedHome(t *testing.T) {
	home := t.TempDir()
	alias := filepath.Join(t.TempDir(), "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "repo")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", alias)
	if got := statusFolder(root); got != filepath.Join("~", "repo") {
		t.Fatalf("canonical repository beneath symlinked home = %q", got)
	}
}

func TestStatusSessionTitleEntryHistoryAndFallback(t *testing.T) {
	snapshot := session.Snapshot{ID: "0123456789abcdef", Entries: []session.Entry{
		{Role: "assistant", Content: "not the title"},
		{Role: "user", Content: "  First\n\tuser\x1b[31m prompt  "},
		{Role: "user", Content: "later prompt"},
	}, History: []model.Message{{Role: "user", Content: "history prompt"}}}
	if got := statusSessionTitle(snapshot); !strings.HasPrefix(got, "First user ") || strings.ContainsAny(got, "\x1b\r\n\t") || strings.Contains(got, "history prompt") {
		t.Fatalf("first user prompt did not produce a safe single-line title: %q", got)
	}
	// A generated name wins over the derived titles (phase 1c).
	snapshot.NamedTitle = "Feature scaffolding"
	if got := statusSessionTitle(snapshot); got != "Feature scaffolding" {
		t.Fatalf("named title = %q", got)
	}
	snapshot.NamedTitle = ""
	snapshot.Entries = nil
	if got := statusSessionTitle(snapshot); got != "history prompt" {
		t.Fatalf("history title = %q", got)
	}
	snapshot.History = nil
	if got := statusSessionTitle(snapshot); got != "01234567" {
		t.Fatalf("empty-session title = %q", got)
	}
	snapshot.ID = "short"
	if got := statusSessionTitle(snapshot); got != "short" {
		t.Fatalf("short-ID title = %q", got)
	}
}

func TestGitStatusWorktreeBranchAndDetachedSHA(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit(repo, "init", "-q")
	runGit(repo, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(repo, "-c", "user.name=Lisa Test", "-c", "user.email=lisa@example.test", "commit", "-qm", "initial", "--allow-empty")
	if state, ok := gitStatus(repo); !ok || state.Branch != "main" || state.Detached {
		t.Fatalf("regular repository branch = %+v, ok=%v", state, ok)
	}
	child := filepath.Join(repo, "subdir")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if state, ok := gitStatus(child); !ok || state.Branch != "main" {
		t.Fatalf("repository subdir branch = %+v, ok=%v", state, ok)
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	runGit(repo, "worktree", "add", "-qb", "feature/status-line", worktree)
	if state, ok := gitStatus(worktree); !ok || state.Branch != "feature/status-line" {
		t.Fatalf("linked worktree branch = %+v, ok=%v", state, ok)
	}
	runGit(worktree, "checkout", "-q", "--detach")
	sha := strings.TrimSpace(runGitOutput(t, git, os.Environ(), worktree, "rev-parse", "HEAD"))
	if state, ok := gitStatus(worktree); !ok || !state.Detached || state.Branch != sha[:7] {
		t.Fatalf("detached HEAD = %+v, ok=%v, want sha %q", state, ok, sha[:7])
	}
	// detachedHead shares the repository walk with the former currentBranch:
	// regular repositories, linked worktrees, and non-repositories.
	if got := detachedHead(repo); got != "" {
		t.Fatalf("detached HEAD on a checked-out branch = %q", got)
	}
	if got := detachedHead(t.TempDir()); got != "" {
		t.Fatalf("non-git directory has a detached HEAD %q", got)
	}
	if state, ok := gitStatus(repo); !ok || state.Branch != "main" {
		t.Fatalf("worktree changed main branch = %+v, ok=%v", state, ok)
	}
	if state, ok := gitStatus(t.TempDir()); ok || state != (gitState{}) {
		t.Fatalf("non-git directory = %+v, ok=%v", state, ok)
	}
}

func TestParseGitStatusPorcelainColumns(t *testing.T) {
	cases := []struct {
		name    string
		records []string
		want    gitState
	}{
		{name: "empty"},
		{name: "staged only", records: []string{"M  tracked.go", "A  new.go", "D  gone.go", "R  old.go -> new.go"}, want: gitState{Staged: 4}},
		{name: "worktree only", records: []string{" M tracked.go", " D gone.go"}, want: gitState{Dirty: 2}},
		{name: "both columns", records: []string{"MM tracked.go", "AM added.go", "DM gone.go"}, want: gitState{Dirty: 3, Staged: 3}},
		{name: "untracked", records: []string{"?? scratch.go", "?? notes/"}, want: gitState{Untracked: 2}},
		{name: "rename staged", records: []string{"R  a.go -> b.go"}, want: gitState{Staged: 1}},
		{name: "rename restaged", records: []string{"RM a.go -> b.go"}, want: gitState{Dirty: 1, Staged: 1}},
		{name: "malformed skipped", records: []string{"", "xx", "M tracked.go", "?? ok.txt"}, want: gitState{Untracked: 1}},
	}
	for _, tc := range cases {
		state := parseGitStatus([]byte(strings.Join(tc.records, "\n")))
		if state.Staged != tc.want.Staged || state.Dirty != tc.want.Dirty || state.Untracked != tc.want.Untracked {
			t.Fatalf("%s: staged=%d dirty=%d untracked=%d, want %+v", tc.name, state.Staged, state.Dirty, state.Untracked, tc.want)
		}
	}
}

func TestParseGitStatusHeaderShapes(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want gitState
	}{
		{name: "in sync", out: "## main\n", want: gitState{Branch: "main"}},
		{name: "with upstream, no tracking state", out: "## main...origin/main\n", want: gitState{Branch: "main"}},
		{name: "ahead only", out: "## main...origin/main [ahead 2]\n", want: gitState{Branch: "main", Ahead: 2}},
		{name: "behind only", out: "## main...origin/main [behind 3]\n", want: gitState{Branch: "main", Behind: 3}},
		{name: "ahead and behind", out: "## main...origin/main [ahead 1, behind 4]\n", want: gitState{Branch: "main", Ahead: 1, Behind: 4}},
		{name: "detached", out: "## HEAD (no branch)\n", want: gitState{Detached: true}},
		{name: "header plus records", out: "## main...origin/main [ahead 1]\n M a.go\n?? b.txt\nR  c.go -> d.go\n", want: gitState{Branch: "main", Ahead: 1, Dirty: 1, Untracked: 1, Staged: 1}},
	}
	for _, tc := range cases {
		state := parseGitStatus([]byte(tc.out))
		if state.Branch != tc.want.Branch || state.Detached != tc.want.Detached || state.Ahead != tc.want.Ahead || state.Behind != tc.want.Behind ||
			state.Staged != tc.want.Staged || state.Dirty != tc.want.Dirty || state.Untracked != tc.want.Untracked {
			t.Fatalf("%s: got %+v, want %+v", tc.name, state, tc.want)
		}
	}
}

func TestGitStatusRepositoryStates(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Lisa Test", "GIT_AUTHOR_EMAIL=lisa@example.test", "GIT_COMMITTER_NAME=Lisa Test", "GIT_COMMITTER_EMAIL=lisa@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init", "-q")
	runGit("symbolic-ref", "HEAD", "refs/heads/main")

	// Clean repository.
	if state, ok := gitStatus(repo); !ok || state.Dirty != 0 || state.Staged != 0 {
		t.Fatalf("clean repository: gitStatus = %+v, ok=%v", state, ok)
	}

	// Worktree-only modification.
	write("tracked.txt", "one\n")
	runGit("add", ".")
	runGit("commit", "-qm", "initial")
	write("tracked.txt", "one\ntwo\n")
	if state, ok := gitStatus(repo); !ok || state.Dirty != 1 || state.Staged != 0 {
		t.Fatalf("worktree-only: gitStatus = %+v, ok=%v", state, ok)
	}

	// Staged-only change on top.
	runGit("add", "tracked.txt")
	if state, ok := gitStatus(repo); !ok || state.Dirty != 0 || state.Staged != 1 {
		t.Fatalf("staged-only: gitStatus = %+v, ok=%v", state, ok)
	}

	// Both columns: further worktree edit after staging.
	write("tracked.txt", "one\ntwo\nthree\n")
	if state, ok := gitStatus(repo); !ok || state.Dirty != 1 || state.Staged != 1 {
		t.Fatalf("both columns: gitStatus = %+v, ok=%v", state, ok)
	}

	// Untracked files are their own segment.
	write("scratch.txt", "new\n")
	write("notes.txt", "more\n")
	if state, ok := gitStatus(repo); !ok || state.Dirty != 1 || state.Staged != 1 || state.Untracked != 2 {
		t.Fatalf("with untracked: gitStatus = %+v, ok=%v", state, ok)
	}
	runGit("add", "scratch.txt", "notes.txt")
	if state, ok := gitStatus(repo); !ok || state.Untracked != 0 || state.Staged != 3 {
		t.Fatalf("staged untracked: gitStatus = %+v, ok=%v", state, ok)
	}

	// Staged rename of a committed file. tracked.txt keeps its unstaged
	// worktree edit from the "both columns" step — commit never restaged it —
	// so the real porcelain is the staged rename plus one changed file.
	runGit("commit", "-qm", "second")
	runGit("mv", "scratch.txt", "moved.txt")
	if state, ok := gitStatus(repo); !ok || state.Dirty != 1 || state.Staged != 1 {
		t.Fatalf("staged rename: gitStatus = %+v, ok=%v", state, ok)
	}
}

func TestGitStatusNonRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	if state, ok := gitStatus(t.TempDir()); ok || state != (gitState{}) {
		t.Fatalf("non-repository: gitStatus = %+v, ok=%v", state, ok)
	}
}

func TestGitStatusGitAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if state, ok := gitStatus(t.TempDir()); ok || state != (gitState{}) {
		t.Fatalf("git absent: gitStatus = %+v, ok=%v", state, ok)
	}
}

func TestGitStatusBranchUpstreamAndDetachedSHA(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=Lisa Test", "GIT_AUTHOR_EMAIL=lisa@example.test", "GIT_COMMITTER_NAME=Lisa Test", "GIT_COMMITTER_EMAIL=lisa@example.test")
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(t.TempDir(), "repo")
	origin := filepath.Join(t.TempDir(), "origin.git")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(repo, "init", "-q")
	runGit(repo, "symbolic-ref", "HEAD", "refs/heads/main")
	write(filepath.Join(repo, "tracked.txt"), "one\n")
	runGit(repo, "add", ".")
	runGit(repo, "commit", "-qm", "initial")
	runGit(repo, "init", "-q", "--bare", origin)
	runGit(repo, "remote", "add", "origin", origin)
	runGit(repo, "push", "-q", "-u", "origin", "main")

	// In sync: branch header, no tracking deltas.
	state, ok := gitStatus(repo)
	if !ok || state.Branch != "main" || state.Detached || state.Ahead != 0 || state.Behind != 0 || state.Staged != 0 || state.Dirty != 0 || state.Untracked != 0 {
		t.Fatalf("in-sync repository: gitStatus = %+v, ok=%v", state, ok)
	}

	// Local commit: ahead only.
	write(filepath.Join(repo, "tracked.txt"), "one\ntwo\n")
	runGit(repo, "add", ".")
	runGit(repo, "commit", "-qm", "second")
	if state, ok = gitStatus(repo); !ok || state.Ahead != 1 || state.Behind != 0 || state.Dirty != 0 {
		t.Fatalf("ahead repository: gitStatus = %+v, ok=%v", state, ok)
	}

	// A clone pushes another commit; after a fetch the local branch has
	// diverged: ahead 1, behind 1.
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t.TempDir(), "clone", "-q", origin, clone)
	write(filepath.Join(clone, "other.txt"), "base\n")
	runGit(clone, "add", ".")
	runGit(clone, "commit", "-qm", "third")
	runGit(clone, "push", "-q", "origin", "main")
	runGit(repo, "fetch", "-q", "origin")
	if state, ok = gitStatus(repo); !ok || state.Ahead != 1 || state.Behind != 1 {
		t.Fatalf("diverged repository: gitStatus = %+v, ok=%v", state, ok)
	}

	// Worktree and untracked segments.
	write(filepath.Join(repo, "scratch.txt"), "new\n")
	if state, ok = gitStatus(repo); !ok || state.Dirty != 0 || state.Untracked != 1 || state.Staged != 0 {
		t.Fatalf("untracked file: gitStatus = %+v, ok=%v", state, ok)
	}
	runGit(repo, "add", "scratch.txt")
	if state, ok = gitStatus(repo); !ok || state.Staged != 1 || state.Untracked != 0 {
		t.Fatalf("staged untracked: gitStatus = %+v, ok=%v", state, ok)
	}

	// Detached HEAD: short SHA in Branch, resolved without a second git call.
	runGit(repo, "checkout", "-q", "--detach")
	sha := strings.TrimSpace(runGitOutput(t, git, env, repo, "rev-parse", "HEAD"))
	state, ok = gitStatus(repo)
	if !ok || !state.Detached || state.Branch != sha[:7] || state.Staged != 1 {
		t.Fatalf("detached HEAD: gitStatus = %+v, ok=%v, want sha %q", state, ok, sha[:7])
	}
}

func runGitOutput(t *testing.T, git string, env []string, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestDetachedHeadIgnoresMissingAndOversizedHead(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	if err := os.Mkdir(git, 0700); err != nil {
		t.Fatal(err)
	}
	if got := detachedHead(root); got != "" {
		t.Fatalf("missing HEAD has a detached SHA %q", got)
	}
	if err := os.WriteFile(filepath.Join(git, "HEAD"), []byte(strings.Repeat("x", 4097)), 0600); err != nil {
		t.Fatal(err)
	}
	if got := detachedHead(root); got != "" {
		t.Fatalf("oversized HEAD has a detached SHA %q", got)
	}
}
