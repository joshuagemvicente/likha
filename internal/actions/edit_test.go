package actions

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEditPreviewRejectionAndApply(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("before\nsecond"), 0600); err != nil {
		t.Fatal(err)
	}
	edit, err := PrepareEdit(root, "notes.txt", "after\nsecond\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "--- a/notes.txt\n+++ b/notes.txt\n@@ -1,2 +1,2 @@\n-before\n-second\n\\ No newline at end of file\n+after\n+second\n"
	if edit.Path != "notes.txt" || edit.Diff != want {
		t.Fatalf("unexpected preview path/diff: %q %q", edit.Path, edit.Diff)
	}
	// A rejected proposal has no write operation; preparation alone must not
	// affect the file or create a temporary file.
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before\nsecond" {
		t.Fatalf("preparation changed file: %q, %v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("preparation created files: %v, %v", entries, err)
	}
	if err := edit.Apply(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "after\nsecond\n" {
		t.Fatalf("wrong replacement: %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions not preserved: %v, %v", info, err)
	}
	if err := edit.Apply(); !errors.Is(err, ErrStaleEdit) {
		t.Fatalf("repeated approval must not reapply a stale proposal: %v", err)
	}
}

func TestEditPreviewBoundsContextInLargeFile(t *testing.T) {
	root := t.TempDir()
	var original strings.Builder
	for i := range 200 {
		original.WriteString("line ")
		original.WriteString(strconv.Itoa(i))
		original.WriteByte('\n')
	}
	old := original.String()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	replacement := strings.Replace(old, "line 100\n", "replacement\n", 1)
	edit, err := PrepareEdit(root, "large.txt", replacement)
	if err != nil {
		t.Fatal(err)
	}
	want := "--- a/large.txt\n+++ b/large.txt\n@@ -98,7 +98,7 @@\n line 97\n line 98\n line 99\n-line 100\n+replacement\n line 101\n line 102\n line 103\n"
	if edit.Diff != want {
		t.Fatalf("one-line change should have only bounded context:\n%s", edit.Diff)
	}
}

func TestEditPreviewSeparatesDistantChanges(t *testing.T) {
	old := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve\nthirteen\nfourteen\nfifteen\nsixteen\nseventeen\neighteen\nnineteen\ntwenty\n"
	next := strings.Replace(strings.Replace(old, "three\n", "THREE\n", 1), "seventeen\n", "SEVENTEEN\n", 1)
	diff := unified("list.txt", old, next, false)
	want := "--- a/list.txt\n+++ b/list.txt\n" +
		"@@ -1,6 +1,6 @@\n one\n two\n-three\n+THREE\n four\n five\n six\n" +
		"@@ -14,7 +14,7 @@\n fourteen\n fifteen\n sixteen\n-seventeen\n+SEVENTEEN\n eighteen\n nineteen\n twenty\n"
	if diff != want {
		t.Fatalf("distant changes must appear in separate hunks:\n%s", diff)
	}
}

func TestEditPreviewNewFileAndNewlineBoundaries(t *testing.T) {
	tests := []struct {
		name, old, next, want string
		created               bool
	}{
		{"new file", "", "alpha\nbeta", "--- /dev/null\n+++ b/file.txt\n@@ -0,0 +1,2 @@\n+alpha\n+beta\n\\ No newline at end of file\n", true},
		{"remove final newline", "alpha\nbeta\n", "alpha\nbeta", "--- a/file.txt\n+++ b/file.txt\n@@ -1,2 +1,2 @@\n alpha\n-beta\n+beta\n\\ No newline at end of file\n", false},
		{"add final newline", "alpha\nbeta", "alpha\nbeta\n", "--- a/file.txt\n+++ b/file.txt\n@@ -1,2 +1,2 @@\n alpha\n-beta\n\\ No newline at end of file\n+beta\n", false},
		{"unchanged final line without newline", "alpha\nbeta", "before\nalpha\nbeta", "--- a/file.txt\n+++ b/file.txt\n@@ -1,2 +1,3 @@\n+before\n alpha\n beta\n\\ No newline at end of file\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := unified("file.txt", tc.old, tc.next, tc.created); got != tc.want {
				t.Fatalf("wrong preview:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestEditRejectsStaleContentAndIdentity(t *testing.T) {
	for _, change := range []string{"content", "identity"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "file.txt")
			if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
			edit, err := PrepareEdit(root, "file.txt", "new")
			if err != nil {
				t.Fatal(err)
			}
			if change == "content" {
				err = os.WriteFile(path, []byte("someone else's change"), 0644)
			} else {
				err = os.Rename(path, filepath.Join(root, "moved.txt"))
				if err == nil {
					err = os.WriteFile(path, []byte("old"), 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := edit.Apply(); !errors.Is(err, ErrStaleEdit) {
				t.Fatalf("expected stale edit: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) == "new" {
				t.Fatalf("stale approval overwrote newer file: %q, %v", data, err)
			}
		})
	}
}

func TestEditNewFileAndNoOverwrite(t *testing.T) {
	root := t.TempDir()
	edit, err := PrepareEdit(root, "created.txt", "created without newline")
	if err != nil {
		t.Fatal(err)
	}
	want := "--- /dev/null\n+++ b/created.txt\n@@ -0,0 +1,1 @@\n+created without newline\n\\ No newline at end of file\n"
	if edit.Diff != want {
		t.Fatalf("new-file preview: %q", edit.Diff)
	}
	path := filepath.Join(root, "created.txt")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation created file: %v", err)
	}
	if err := os.WriteFile(path, []byte("other user"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := edit.Apply(); !errors.Is(err, ErrStaleEdit) {
		t.Fatalf("new file must not overwrite existing one: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "other user" {
		t.Fatalf("unexpected content: %q, %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := edit.Apply(); !errors.Is(err, ErrStaleEdit) {
		t.Fatalf("proposal must stay stale after target appeared and was removed: %v", err)
	}
	edit, err = PrepareEdit(root, "created.txt", "created without newline")
	if err != nil {
		t.Fatal(err)
	}
	if err := edit.Apply(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "created without newline" {
		t.Fatalf("new file missing content: %q, %v", data, err)
	}
}

func TestEditRejectsEscapesSymlinkSwapsAndMissingParents(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", secret, "a/../../escape", "."} {
		if _, err := PrepareEdit(root, path, "unsafe"); err == nil {
			t.Errorf("accepted invalid path %q", path)
		}
	}
	if _, err := PrepareEdit(root, "missing/file.txt", "new"); err == nil {
		t.Fatal("missing parent silently accepted")
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareEdit(root, "link/secret.txt", "unsafe"); err == nil {
		t.Fatal("traversed directory symlink")
	}
	if err := os.Symlink(secret, filepath.Join(root, "target")); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareEdit(root, "target", "unsafe"); err == nil {
		t.Fatal("read target symlink")
	}
	if err := os.WriteFile(filepath.Join(root, "real"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	edit, err := PrepareEdit(root, "real", "new")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "real")); err != nil {
		t.Fatal(err)
	}
	if err := edit.Apply(); err == nil {
		t.Fatal("applied after a symlink swap")
	}
	if data, err := os.ReadFile(secret); err != nil || string(data) != "secret" {
		t.Fatalf("wrote outside root: %q, %v", data, err)
	}
	if err := os.Mkdir(filepath.Join(root, "parent"), 0700); err != nil {
		t.Fatal(err)
	}
	edit, err = PrepareEdit(root, "parent/new.txt", "new")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "parent"), filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "parent")); err != nil {
		t.Fatal(err)
	}
	if err := edit.Apply(); err == nil {
		t.Fatal("applied after parent symlink swap")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created file outside root: %v", err)
	}
}

func TestEditLimitsAndText(t *testing.T) {
	root := t.TempDir()
	for _, data := range []string{strings.Repeat("a", MaxEditBytes+1), "binary\x00data", "\xff"} {
		if _, err := PrepareEdit(root, "new", data); err == nil {
			t.Fatal("accepted oversize or non-text replacement")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "large"), []byte(strings.Repeat("x", MaxEditBytes+1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareEdit(root, "large", "small"); err == nil {
		t.Fatal("accepted oversized original")
	}
}

func TestEditRejectsHiddenExecutableText(t *testing.T) {
	tests := []struct {
		name, source string
	}{
		{"carriage return in Python comment", "# comment\rprint('hidden')\n"},
		{"escape", "# comment\x1b[1Gprint('hidden')\n"},
		{"backspace", "# comment\bprint('hidden')\n"},
		{"vertical tab", "# comment\vprint('hidden')\n"},
		{"delete", "# comment\x7fprint('hidden')\n"},
		{"C1 control", "# comment\u009bprint('hidden')\n"},
		{"bidi override", "# comment\u202eprint('hidden')\n"},
		{"bidi isolate", "# comment\u2066print('hidden')\n"},
		{"zero-width space", "# comment\u200bprint('hidden')\n"},
		{"soft hyphen", "# comment\u00adprint('hidden')\n"},
		{"line separator", "# comment\u2028print('hidden')\n"},
		{"paragraph separator", "# comment\u2029print('hidden')\n"},
		{"invalid UTF-8", "# comment\xffprint('hidden')\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "script.py")
			if err := os.WriteFile(path, []byte("print('before')\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if edit, err := PrepareEdit(root, "script.py", tc.source); err == nil || edit != nil {
				t.Fatalf("unsafe replacement received an approval preview: %v, %v", edit, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "print('before')\n" {
				t.Fatalf("unsafe replacement changed the original: %q, %v", data, err)
			}
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			if edit, err := PrepareEdit(root, "script.py", "print('safe')\n"); err == nil || edit != nil {
				t.Fatalf("unsafe source received an approval preview: %v, %v", edit, err)
			}
		})
	}
}

func TestEditRejectsEveryC0AndC1Control(t *testing.T) {
	root := t.TempDir()
	for r := rune(0); r <= 0x9f; r++ {
		if r == '\n' || r == '\t' || r >= 0x20 && r < 0x7f {
			continue
		}
		if edit, err := PrepareEdit(root, "new.py", "print('before')"+string(r)+"print('after')\n"); err == nil || edit != nil {
			t.Fatalf("control U+%04X received an approval preview: %v, %v", r, edit, err)
		}
	}
}

func TestEditPreviewSupportsTabsAndNewlines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.py")
	if err := os.WriteFile(path, []byte("def work():\n\treturn 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	edit, err := PrepareEdit(root, "script.py", "def work():\n\treturn 2\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "--- a/script.py\n+++ b/script.py\n@@ -1,2 +1,2 @@\n def work():\n-\treturn 1\n+\treturn 2\n"
	if edit.Diff != want {
		t.Fatalf("wrong preview for tabs and newlines:\n%s", edit.Diff)
	}
	if err := edit.Apply(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "def work():\n\treturn 2\n" {
		t.Fatalf("wrong applied text: %q, %v", data, err)
	}
}
