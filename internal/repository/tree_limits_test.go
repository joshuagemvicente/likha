package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeTreeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A deeper .gitignore negation re-includes a path its parent ignores, the
// "deeper rules winning" contract of specs/file-references.
func TestDeeperGitignoreNegationReincludes(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, ".gitignore", "*.log\n")
	writeTreeFile(t, root, "root.log", "x")
	writeTreeFile(t, root, "sub/.gitignore", "!keep.log\n")
	writeTreeFile(t, root, "sub/keep.log", "x")
	writeTreeFile(t, root, "sub/other.log", "x")
	writeTreeFile(t, root, "sub/same/.gitignore", "*.tmp\n!wanted.tmp\n")
	writeTreeFile(t, root, "sub/same/wanted.tmp", "x")
	writeTreeFile(t, root, "sub/same/junk.tmp", "x")
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	entries, _, err := repo.Tree(".", 1000)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	for _, want := range []string{"sub/keep.log", "sub/same/wanted.tmp"} {
		if !slices.Contains(entries, want) {
			t.Fatalf("Tree is missing re-included %q: %v", want, entries)
		}
	}
	for _, hidden := range []string{"root.log", "sub/other.log", "sub/same/junk.tmp"} {
		if slices.Contains(entries, hidden) {
			t.Fatalf("Tree listed ignored %q: %v", hidden, entries)
		}
	}

	page, err := repo.GlobPage(context.Background(), "**/*.log", QueryOptions{})
	if err != nil {
		t.Fatalf("GlobPage: %v", err)
	}
	if !strings.Contains(page.Content, "sub/keep.log\n") || strings.Contains(page.Content, "other.log") || strings.Contains(page.Content, "root.log") {
		t.Fatalf("GlobPage content = %q, want only sub/keep.log", page.Content)
	}
}

// One unreadable subdirectory must not empty the whole @ completion index.
func TestTreeSkipsUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	writeTreeFile(t, root, "keep.txt", "x")
	writeTreeFile(t, root, "locked/secret.txt", "x")
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	entries, truncated, err := repo.Tree(".", 1000)
	if err != nil {
		t.Fatalf("Tree failed on an unreadable subdirectory: %v", err)
	}
	if !slices.Contains(entries, "keep.txt") || !slices.Contains(entries, "locked/") {
		t.Fatalf("entries = %v, want keep.txt and locked/", entries)
	}
	if !truncated {
		t.Fatal("truncated = false, want the unlisted subdirectory reported")
	}
}

// A directory over MaxListEntries contributes a bounded sample instead of
// failing every other entry with it.
func TestTreeSamplesOversizedDirectory(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "keep.txt", "x")
	for i := 0; i <= MaxListEntries; i++ {
		writeTreeFile(t, root, fmt.Sprintf("big/f%04d.txt", i), "x")
	}
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	entries, truncated, err := repo.Tree(".", 5000)
	if err != nil {
		t.Fatalf("Tree failed on an oversized directory: %v", err)
	}
	if !slices.Contains(entries, "keep.txt") {
		t.Fatalf("entries are missing keep.txt: %d entries", len(entries))
	}
	big := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry, "big/") && entry != "big/" {
			big++
		}
	}
	if big != MaxListEntries {
		t.Fatalf("big/ contributed %d entries, want a %d-entry sample", big, MaxListEntries)
	}
	if !truncated {
		t.Fatal("truncated = false, want the oversized directory reported")
	}
}

// An explicitly requested hidden or ignored scope that discovery filters
// exclude must not come back as an apparently complete empty result.
func TestExcludedScopeIsReportedIncomplete(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, ".gitignore", "build/\n")
	writeTreeFile(t, root, ".github/workflows/ci.yml", "run: go test\n")
	writeTreeFile(t, root, "build/out.txt", "run: go test\n")
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := []struct {
		name  string
		query func(QueryOptions) (Page, error)
		scope string
		flag  string
	}{
		{"grep hidden", func(o QueryOptions) (Page, error) { return repo.GrepPage(ctx, "go test", o) }, ".github", "include_hidden"},
		{"grep ignored", func(o QueryOptions) (Page, error) { return repo.GrepPage(ctx, "go test", o) }, "build", "include_ignored"},
		{"glob hidden", func(o QueryOptions) (Page, error) { return repo.GlobPage(ctx, "**", o) }, ".github", "include_hidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := tc.query(QueryOptions{Path: tc.scope})
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if !page.Truncated || !strings.Contains(page.Content, "complete=false") {
				t.Fatalf("page = %+v, want an incomplete result", page)
			}
			if !strings.Contains(strings.Join(page.Warnings, "\n"), tc.flag) {
				t.Fatalf("warnings = %q, want guidance naming %s", page.Warnings, tc.flag)
			}
		})
	}

	page, err := repo.GrepPage(ctx, "go test", QueryOptions{Path: ".github", IncludeHidden: true})
	if err != nil {
		t.Fatalf("GrepPage with include_hidden: %v", err)
	}
	if page.Truncated || !strings.Contains(page.Content, ".github/workflows/ci.yml:1: run: go test") {
		t.Fatalf("page = %+v, want the hidden match found and complete", page)
	}
}
