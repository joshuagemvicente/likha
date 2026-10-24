package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// gitRepo returns a repository root with a .git/config to aim edits at.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "config"), "[core]\n\tbare = false\n", 0644)
	writeFile(t, filepath.Join(root, "sub", ".git", "HEAD"), "ref: refs/heads/main\n", 0644)
	return root
}

func TestEditsRefuseGitMetadata(t *testing.T) {
	root := gitRepo(t)
	cases := []string{
		".git/config",
		"./.git/config",
		"sub/.git/HEAD",
		".git/hooks/pre-commit",
		".g\u200cit/config", // HFS+ ignores zero-width joiners in names
	}
	if runtime.GOOS == "darwin" {
		// The default macOS filesystem is case-insensitive: .GIT is .git.
		cases = append(cases, ".GIT/config", ".Git/config")
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			if _, err := PrepareEdit(root, path, "[core]\n\tfsmonitor = touch pwned\n"); !errors.Is(err, ErrGitMetadata) {
				t.Fatalf("PrepareEdit(%q) error = %v, want ErrGitMetadata", path, err)
			}
			ops := []EditOperation{{Kind: "create", Path: path, Content: "x\n"}}
			if _, err := PrepareEdits(root, ops); !errors.Is(err, ErrGitMetadata) {
				t.Fatalf("PrepareEdits(%q) error = %v, want ErrGitMetadata", path, err)
			}
		})
	}
	if got := readFile(t, filepath.Join(root, ".git", "config")); got != "[core]\n\tbare = false\n" {
		t.Fatalf(".git/config changed: %q", got)
	}
}

func TestEditsAllowGitLookalikeNames(t *testing.T) {
	root := gitRepo(t)
	for _, path := range []string{".gitignore", ".github/workflows/ci.yml", "docs/.git-notes", "a.git/file"} {
		t.Run(path, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0755); err != nil {
				t.Fatal(err)
			}
			if _, err := PrepareEdit(root, path, "ok\n"); err != nil {
				t.Fatalf("PrepareEdit(%q): %v", path, err)
			}
		})
	}
}

// touchDir moves a directory's modification time well away from now, the
// way an unrelated sibling create/delete would.
func touchDir(t *testing.T, dir string) {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir, past, past); err != nil {
		t.Fatal(err)
	}
}

func TestEditFileAppliesAfterUnrelatedDirectoryChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "notes.txt"), "before\n", 0644)
	edit, err := PrepareEdit(root, "sub/notes.txt", "after\n")
	if err != nil {
		t.Fatal(err)
	}
	// A build writing a sibling file changes both ancestors' timestamps.
	writeFile(t, filepath.Join(root, "sub", "sibling.o"), "obj", 0644)
	touchDir(t, root)
	touchDir(t, filepath.Join(root, "sub"))
	if err := edit.Apply(); err != nil {
		t.Fatalf("Apply after unrelated directory change: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "sub", "notes.txt")); got != "after\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestEditFileStillRejectsReplacedAncestor(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "notes.txt"), "before\n", 0644)
	edit, err := PrepareEdit(root, "sub/notes.txt", "after\n")
	if err != nil {
		t.Fatal(err)
	}
	// Same name, same content, different directory: identity must still count.
	if err := os.Rename(filepath.Join(root, "sub"), filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sub", "notes.txt"), "before\n", 0644)
	if err := edit.Apply(); !errors.Is(err, ErrStaleEdit) {
		t.Fatalf("Apply into a replaced directory: %v, want ErrStaleEdit", err)
	}
}

func TestEditBatchAppliesAfterUnrelatedDirectoryChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "a.txt"), "one\n", 0644)
	proposal, err := PrepareEdits(root, []EditOperation{
		{Kind: "replace", Path: "sub/a.txt", OldText: "one", NewText: "two"},
		{Kind: "create", Path: "sub/b.txt", Content: "new\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sub", "sibling.o"), "obj", 0644)
	touchDir(t, root)
	touchDir(t, filepath.Join(root, "sub"))
	if summary, err := proposal.Apply(context.Background()); err != nil {
		t.Fatalf("Apply after unrelated directory change: %v (%s)", err, summary)
	}
	if got := readFile(t, filepath.Join(root, "sub", "a.txt")); got != "two\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(root, "sub", "b.txt")); got != "new\n" {
		t.Fatalf("b.txt = %q", got)
	}
}
