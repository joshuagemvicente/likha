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

func TestListReadSearch(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "notes.txt"), "needle first\nother\nneedle third\n")
	writeFixture(t, filepath.Join(root, "sub", "story.txt"), "another needle\n")
	writeFixture(t, filepath.Join(root, ".git", "config"), "needle hidden\n")
	writeFixture(t, filepath.Join(root, "binary.dat"), "needle\x00binary")
	if err := os.Symlink(filepath.Join(root, "notes.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	repo := newFixture(t, root)
	listed, err := repo.List(".")
	if err != nil {
		t.Fatal(err)
	}
	wantList := []string{".git", "binary.dat", "link.txt", "notes.txt", "sub"}
	if !reflect.DeepEqual(listed, wantList) {
		t.Fatalf("list = %q, want %q", listed, wantList)
	}
	listed, err = repo.List("sub")
	if err != nil || !reflect.DeepEqual(listed, []string{filepath.Join("sub", "story.txt")}) {
		t.Fatalf("list sub = %q, %v", listed, err)
	}
	text, err := repo.Read("notes.txt")
	if err != nil || text != "needle first\nother\nneedle third\n" {
		t.Fatalf("read = %q, %v", text, err)
	}
	matches, err := repo.Search("needle")
	wantMatches := []Match{
		{Path: "notes.txt", Line: 1, Text: "needle first"},
		{Path: "notes.txt", Line: 3, Text: "needle third"},
		{Path: filepath.Join("sub", "story.txt"), Line: 1, Text: "another needle"},
	}
	if err != nil || !reflect.DeepEqual(matches, wantMatches) {
		t.Fatalf("search = %#v, %v; want %#v", matches, err, wantMatches)
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
	for _, path := range []string{"../secret.txt", "sub/../sub/allowed.txt", filepath.Join(outside, "secret.txt"), ""} {
		if _, err := repo.Read(path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Read(%q): want invalid path, got %v", path, err)
		}
		if _, err := repo.List(path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("List(%q): want invalid path, got %v", path, err)
		}
	}
	for _, path := range []string{"escape/secret.txt", "secret-link"} {
		if data, err := repo.Read(path); !errors.Is(err, ErrSymlink) {
			t.Errorf("Read(%q): %q, %v; want symlink error", path, data, err)
		}
	}
	if _, err := repo.List("escape"); !errors.Is(err, ErrSymlink) {
		t.Errorf("List symlink: %v", err)
	}
	if _, err := repo.Read("absent.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read missing file: %v", err)
	}
	if _, err := repo.List("absent"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("List missing directory: %v", err)
	}
	if _, err := repo.Read("sub"); err == nil {
		t.Error("Read directory succeeded")
	}
	if _, err := repo.List("sub/allowed.txt"); err == nil {
		t.Error("List regular file succeeded")
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
	if _, err := repo.List("."); err == nil {
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
	if _, err := repo.Search("no match"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized search: %v", err)
	}
	for _, path := range []string{"invalid.dat", "nul.dat"} {
		if _, err := repo.Read(path); !errors.Is(err, ErrBinary) {
			t.Errorf("binary read %q: %v", path, err)
		}
	}
}

func TestSearchAndListLimits(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "matches.txt"), strings.Repeat("match\n", MaxSearchResults+1))
	repo := newFixture(t, root)
	if _, err := repo.Search("match"); !errors.Is(err, ErrLimit) {
		t.Errorf("search result cap: %v", err)
	}
	if _, err := repo.Search(""); err == nil {
		t.Error("empty search succeeded")
	}
	if _, err := repo.Search(strings.Repeat("q", MaxQueryBytes+1)); err == nil {
		t.Error("oversized query succeeded")
	}
	for i := range MaxListEntries {
		writeFixture(t, filepath.Join(root, "entry", strconv.Itoa(i)), "")
	}
	if listed, err := repo.List("entry"); err != nil || len(listed) != MaxListEntries {
		t.Fatalf("maximum list length %d: %v", len(listed), err)
	}
	writeFixture(t, filepath.Join(root, "entry", "extra"), "")
	if _, err := repo.List("entry"); !errors.Is(err, ErrLimit) {
		t.Errorf("oversized list: %v", err)
	}
}

func TestSearchBudgetIncludesBinaryFiles(t *testing.T) {
	root := t.TempDir()
	binary := strings.Repeat("x", MaxReadBytes-1) + "\x00"
	for i := range MaxSearchBytes/MaxReadBytes + 1 {
		writeFixture(t, filepath.Join(root, "binary"+string(rune('a'+i))), binary)
	}
	repo := newFixture(t, root)
	if _, err := repo.Search("unmatched"); !errors.Is(err, ErrLimit) {
		t.Errorf("search byte budget: %v", err)
	}
}

func TestSearchRejectsExcessiveDepth(t *testing.T) {
	root := t.TempDir()
	path := root
	for range MaxSearchDepth + 1 {
		path = filepath.Join(path, "d")
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	repo := newFixture(t, root)
	if _, err := repo.Search("anything"); !errors.Is(err, ErrLimit) {
		t.Errorf("deep search: %v", err)
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
