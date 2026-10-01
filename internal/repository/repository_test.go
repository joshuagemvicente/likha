package repository

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T, root string) *Repository {
	t.Helper()
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// globRepository builds a repository with the same layout for every glob
// and grep test: a top-level text file, a nested one, an ignored .git entry,
// a binary file, and a symlink.
func globRepository(t *testing.T) (*Repository, string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "notes.txt"), "needle first\nother\nneedle third\n")
	writeFixture(t, filepath.Join(root, "sub", "story.txt"), "another needle\n")
	writeFixture(t, filepath.Join(root, ".git", "config"), "needle hidden\n")
	writeFixture(t, filepath.Join(root, "binary.dat"), "needle\x00binary")
	if err := os.Symlink(filepath.Join(root, "notes.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	return newFixture(t, root), root
}

func TestGlobPatterns(t *testing.T) {
	repo, _ := globRepository(t)
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"*.txt", []string{"notes.txt"}},
		{"**/*.txt", []string{"notes.txt", filepath.Join("sub", "story.txt")}},
		{"sub/*", []string{filepath.Join("sub", "story.txt")}},
		{"notes.txt", []string{"notes.txt"}},
		{"no?.txt", nil},
		{"*.dat", []string{"binary.dat"}},
		{"link.txt", nil}, // symlinks never match or traverse
		{"**", []string{"binary.dat", "notes.txt", filepath.Join("sub", "story.txt")}},
		{"sub/**", []string{filepath.Join("sub", "story.txt")}},
		{"note[sS].txt", []string{"notes.txt"}},
		{"note[^s].txt", nil},
	} {
		got, err := repo.Glob(tc.pattern)
		if err != nil {
			t.Fatalf("Glob(%q): %v", tc.pattern, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Glob(%q) = %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

func TestGlobRejectsBadPatterns(t *testing.T) {
	repo, _ := globRepository(t)
	for _, pattern := range []string{
		"../secret.txt",
		"sub/../sub/allowed.txt",
		"/etc/*",
	} {
		if _, err := repo.Glob(pattern); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Glob(%q): want invalid path, got %v", pattern, err)
		}
	}
	for _, pattern := range []string{
		"",                                   // empty: its own validation error
		"[a-",                                // malformed class
		strings.Repeat("q", MaxQueryBytes+1), // oversized
	} {
		if _, err := repo.Glob(pattern); err == nil {
			t.Errorf("Glob(%q) succeeded, want a validation error", pattern)
		}
	}
}

func TestGlobSilentOnAbsentPathsAndSkipsHidden(t *testing.T) {
	repo, _ := globRepository(t)
	// A glob never errors on directories or patterns that match nothing —
	// "no matches" is a valid answer (conventional glob behavior).
	got, err := repo.Glob("absent/*.txt")
	if err != nil || len(got) != 0 {
		t.Fatalf("absent directory glob = %q, %v", got, err)
	}
	// .git content never matches.
	if got, _ := repo.Glob(".git/*"); len(got) != 0 {
		t.Fatalf("glob traversed .git: %q", got)
	}
}

func TestGrepAndRead(t *testing.T) {
	repo, _ := globRepository(t)
	text, err := repo.Read("notes.txt")
	if err != nil || text != "needle first\nother\nneedle third\n" {
		t.Fatalf("read = %q, %v", text, err)
	}
	matches, err := repo.Grep("n(e+)dle")
	wantMatches := []Match{
		{Path: "notes.txt", Line: 1, Text: "needle first"},
		{Path: "notes.txt", Line: 3, Text: "needle third"},
		{Path: filepath.Join("sub", "story.txt"), Line: 1, Text: "another needle"},
	}
	if err != nil || !reflect.DeepEqual(matches, wantMatches) {
		t.Fatalf("grep = %#v, %v; want %#v", matches, err, wantMatches)
	}
	// Case-sensitive by default; the in-pattern flag opts out.
	if matches, _ := repo.Grep("Needle"); len(matches) != 0 {
		t.Fatalf("case-sensitive grep matched: %#v", matches)
	}
	if matches, _ := repo.Grep("(?i)needle"); len(matches) != 3 {
		t.Fatalf("insensitive grep = %#v", matches)
	}
	// Binary and .git files are skipped, not matched or surfaced.
	if matches, _ := repo.Grep("needle"); len(matches) != len(wantMatches) {
		t.Fatalf("grep picked up skipped files: %#v", matches)
	}
	if _, err := repo.Grep("[unclosed"); err == nil {
		t.Fatal("invalid regex accepted")
	}
}

func TestRejectPathsAndMissingFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFixture(t, filepath.Join(outside, "secret.txt"), "outside secret")
	writeFixture(t, filepath.Join(root, "sub", "allowed.txt"), "allowed")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "secret-link")); err != nil {
		t.Fatal(err)
	}
	repo := newFixture(t, root)
	for _, path := range []string{"../secret.txt", "sub/../sub/allowed.txt", filepath.Join(outside, "secret.txt")} {
		if _, err := repo.Read(path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Read(%q): want invalid path, got %v", path, err)
		}
		if _, err := repo.Glob(path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Glob(%q): want invalid path, got %v", path, err)
		}
	}
	for _, path := range []string{"escape/secret.txt", "secret-link"} {
		if data, err := repo.Read(path); !errors.Is(err, ErrSymlink) {
			t.Errorf("Read(%q): %q, %v; want symlink error", path, data, err)
		}
	}
	if _, err := repo.Read("absent.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read missing file: %v", err)
	}
	if _, err := repo.Read("sub"); err == nil {
		t.Error("Read directory succeeded")
	}
}

func TestRootCanonicalizedAndReplacementRejected(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "file.txt"), "original")
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	repo := newFixture(t, alias)
	if text, err := repo.Read("file.txt"); err != nil || text != "original" {
		t.Fatalf("read through canonical root = %q, %v", text, err)
	}
	if err := os.Rename(root, filepath.Join(parent, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Glob("*"); err == nil {
		t.Error("replaced repository root was accepted")
	}
}

func TestBinaryAndSizeBoundaries(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "maximum.txt"), strings.Repeat("x", MaxReadBytes))
	writeFixture(t, filepath.Join(root, "oversized.txt"), strings.Repeat("y", MaxReadBytes+1))
	writeFixture(t, filepath.Join(root, "invalid.dat"), string([]byte{0xff, 0xfe}))
	writeFixture(t, filepath.Join(root, "nul.dat"), "a\x00b")
	repo := newFixture(t, root)
	if text, err := repo.Read("maximum.txt"); err != nil || len(text) != MaxReadBytes {
		t.Fatalf("maximum read length %d: %v", len(text), err)
	}
	if _, err := repo.Read("oversized.txt"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized read: %v", err)
	}
	if _, err := repo.Grep("no match"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized grep: %v", err)
	}
	for _, path := range []string{"invalid.dat", "nul.dat"} {
		if _, err := repo.Read(path); !errors.Is(err, ErrBinary) {
			t.Errorf("binary read %q: %v", path, err)
		}
	}
}

func TestGrepAndGlobLimits(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "matches.txt"), strings.Repeat("match\n", MaxSearchResults+1))
	repo := newFixture(t, root)
	if _, err := repo.Grep("match"); !errors.Is(err, ErrLimit) {
		t.Errorf("grep result cap: %v", err)
	}
	if _, err := repo.Grep(""); err == nil {
		t.Error("empty grep succeeded")
	}
	if _, err := repo.Grep(strings.Repeat("q", MaxQueryBytes+1)); err == nil {
		t.Error("oversized grep succeeded")
	}
	// Glob's result cap: more matching files than the limit.
	for i := range MaxGlobResults + 1 {
		writeFixture(t, filepath.Join(root, "many", strconv.Itoa(i)+".dat"), "")
	}
	if _, err := repo.Glob("many/*.dat"); !errors.Is(err, ErrLimit) {
		t.Errorf("glob result cap: %v", err)
	}
}

func TestGrepBudgetIncludesBinaryFiles(t *testing.T) {
	root := t.TempDir()
	binary := strings.Repeat("x", MaxReadBytes-1) + "\x00"
	for i := range MaxSearchBytes/MaxReadBytes + 1 {
		writeFixture(t, filepath.Join(root, "binary"+string(rune('a'+i))), binary)
	}
	repo := newFixture(t, root)
	if _, err := repo.Grep("unmatched"); !errors.Is(err, ErrLimit) {
		t.Errorf("grep byte budget: %v", err)
	}
}

func TestGrepAndGlobRejectExcessiveDepth(t *testing.T) {
	root := t.TempDir()
	path := root
	for range MaxSearchDepth + 1 {
		path = filepath.Join(path, "d")
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	repo := newFixture(t, root)
	if _, err := repo.Grep("anything"); !errors.Is(err, ErrLimit) {
		t.Errorf("deep grep: %v", err)
	}
	if _, err := repo.Glob("**"); !errors.Is(err, ErrLimit) {
		t.Errorf("deep glob: %v", err)
	}
}

func TestNewRejectsMissingAndFileRoot(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "file"), "text")
	for _, path := range []string{filepath.Join(root, "absent"), filepath.Join(root, "file")} {
		if _, err := New(path); err == nil {
			t.Errorf("New(%q) succeeded", path)
		}
	}
}
