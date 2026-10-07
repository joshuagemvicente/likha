package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func applyEditFile(t *testing.T, root, path, content string) ChangeSet {
	t.Helper()
	edit, err := PrepareEdit(root, path, content)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := edit.ChangeSet(); ok {
		t.Fatal("a prepared, unapplied edit must not yield a change set")
	}
	if err := edit.Apply(); err != nil {
		t.Fatal(err)
	}
	set, ok := edit.ChangeSet()
	if !ok {
		t.Fatal("applied edit yielded no change set")
	}
	return set
}

func applyEdits(t *testing.T, root string, ops []EditOperation) ChangeSet {
	t.Helper()
	proposal, err := PrepareEdits(root, ops)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proposal.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	set, ok := proposal.ChangeSet()
	if !ok {
		t.Fatal("completed edit yielded no change set")
	}
	return set
}

func undo(t *testing.T, root string, set ChangeSet) UndoResult {
	t.Helper()
	proposal, changed, err := PrepareUndo(root, set)
	if err != nil || len(changed) != 0 {
		t.Fatalf("PrepareUndo: changed %v, err %v", changed, err)
	}
	result, err := proposal.Apply(context.Background())
	if err != nil {
		t.Fatalf("undo apply: %v (%s)", err, result.Summary)
	}
	return result
}

func TestEditChangeSetRecordsOriginal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "run.sh"), "echo old\n", 0750)
	set := applyEditFile(t, root, "run.sh", "echo new\n")
	want := ChangeSetFile{Path: "run.sh", Existed: true, OldText: "echo old\n", OldMode: 0750,
		NewSHA256: contentDigest("echo new\n"), NewMode: 0750}
	if len(set.Files) != 1 || set.Files[0] != want || len(set.CreatedDirs) != 0 {
		t.Fatalf("change set = %+v, want %+v", set, want)
	}

	created := applyEditFile(t, root, "new.txt", "hello\n")
	if file := created.Files[0]; file.Existed || file.OldText != "" || file.NewMode != 0644 || file.NewSHA256 != contentDigest("hello\n") {
		t.Fatalf("created file change set = %+v", file)
	}
}

func TestFailedApplyYieldsNoChangeSet(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	writeFile(t, path, "one\n", 0644)

	edit, err := PrepareEdit(root, "a.txt", "two\n")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "changed underneath\n", 0644)
	if err := edit.Apply(); !errors.Is(err, ErrStaleEdit) {
		t.Fatalf("stale edit applied: %v", err)
	}
	if _, ok := edit.ChangeSet(); ok {
		t.Fatal("failed edit_file apply yielded a change set")
	}

	proposal, err := PrepareEdits(root, []EditOperation{{Kind: "replace", Path: "a.txt", OldText: "changed", NewText: "edited"}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "changed again\n", 0644)
	if _, err := proposal.Apply(context.Background()); err == nil {
		t.Fatal("stale proposal applied")
	}
	if _, ok := proposal.ChangeSet(); ok {
		t.Fatal("failed edit apply yielded a change set")
	}
}

func TestUndoRestoresReplacedFileContentAndMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "run.sh")
	writeFile(t, path, "echo old\n", 0750)
	set := applyEditFile(t, root, "run.sh", "echo new\n")

	proposal, changed, err := PrepareUndo(root, set)
	if err != nil || changed != nil {
		t.Fatalf("PrepareUndo: %v %v", changed, err)
	}
	wantDiff := "--- a/run.sh\n+++ b/run.sh\n@@ -1,1 +1,1 @@\n-echo new\n+echo old\n"
	if proposal.Diff != wantDiff || !reflect.DeepEqual(proposal.Restored, []string{"run.sh"}) || len(proposal.Removed) != 0 {
		t.Fatalf("undo review = %+v", proposal)
	}
	if readFile(t, path) != "echo new\n" {
		t.Fatal("PrepareUndo wrote to the repository")
	}
	if _, err := proposal.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0750 || readFile(t, path) != "echo old\n" {
		t.Fatalf("restored file: %q mode %v err %v", readFile(t, path), info.Mode(), err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("undo left artifacts behind: %v %v", entries, err)
	}
}

func TestUndoMultiFileSetRemovesCreatedFilesAndEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n", 0644)
	writeFile(t, filepath.Join(root, "b.go"), "package b\n", 0600)
	set := applyEdits(t, root, []EditOperation{
		{Kind: "replace", Path: "a.go", OldText: "package a", NewText: "package aa"},
		{Kind: "replace", Path: "b.go", OldText: "package b", NewText: "package bb"},
		{Kind: "create", Path: filepath.Join("docs", "notes", "new.md"), Content: "# new\n"},
	})
	if want := []string{"docs", filepath.Join("docs", "notes")}; !reflect.DeepEqual(set.CreatedDirs, want) {
		t.Fatalf("created dirs = %v, want %v", set.CreatedDirs, want)
	}

	proposal, _, err := PrepareUndo(root, set)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join("docs", "notes"), "docs"}; !reflect.DeepEqual(proposal.Directories, want) {
		t.Fatalf("directories to remove = %v, want deepest first %v", proposal.Directories, want)
	}
	if want := "--- a/docs/notes/new.md\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-# new\n"; !strings.Contains(proposal.Diff, want) {
		t.Fatalf("removal diff missing from %q", proposal.Diff)
	}
	result, err := proposal.Apply(context.Background())
	if err != nil {
		t.Fatalf("%v: %s", err, result.Summary)
	}
	if readFile(t, filepath.Join(root, "a.go")) != "package a\n" || readFile(t, filepath.Join(root, "b.go")) != "package b\n" {
		t.Fatal("replaced files were not restored")
	}
	if _, err := os.Lstat(filepath.Join(root, "docs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created directory not removed: %v", err)
	}
	if len(result.RemovedDirectories) != 2 || len(result.KeptDirectories) != 0 {
		t.Fatalf("directory result = %+v", result)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("undo left artifacts behind: %v %v", entries, err)
	}
}

func TestUndoKeepsNonEmptyCreatedDirectory(t *testing.T) {
	root := t.TempDir()
	set := applyEdits(t, root, []EditOperation{{Kind: "create", Path: filepath.Join("pkg", "new.go"), Content: "package pkg\n"}})
	writeFile(t, filepath.Join(root, "pkg", "user.go"), "package pkg // mine\n", 0644)

	result := undo(t, root, set)
	if _, err := os.Lstat(filepath.Join(root, "pkg", "new.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created file not removed: %v", err)
	}
	if readFile(t, filepath.Join(root, "pkg", "user.go")) != "package pkg // mine\n" {
		t.Fatal("user file touched")
	}
	if !reflect.DeepEqual(result.KeptDirectories, []string{"pkg"}) || len(result.RemovedDirectories) != 0 {
		t.Fatalf("directory result = %+v", result)
	}
}

func TestPrepareUndoReportsChangedFiles(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(t *testing.T, root string)
		wantChanged bool
	}{
		{"unchanged", func(*testing.T, string) {}, false},
		{"content changed", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "dir", "a.txt"), "user work\n", 0644)
		}, true},
		{"mode changed", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "dir", "a.txt"), 0600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"deleted", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "dir", "a.txt")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"saved again by rename with the same content", func(t *testing.T, root string) {
			tmp := filepath.Join(root, "dir", "a.txt.tmp")
			writeFile(t, tmp, "new\n", 0644)
			if err := os.Rename(tmp, filepath.Join(root, "dir", "a.txt")); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"parent replaced by a symlink", func(t *testing.T, root string) {
			outside := t.TempDir()
			writeFile(t, filepath.Join(outside, "a.txt"), "new\n", 0644)
			if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"file replaced by a symlink", func(t *testing.T, root string) {
			outside := filepath.Join(t.TempDir(), "a.txt")
			writeFile(t, outside, "new\n", 0644)
			if err := os.Remove(filepath.Join(root, "dir", "a.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "dir", "a.txt")); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "dir", "a.txt"), "old\n", 0644)
			set := applyEditFile(t, root, filepath.Join("dir", "a.txt"), "new\n")
			tc.mutate(t, root)
			proposal, changed, err := PrepareUndo(root, set)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantChanged {
				if proposal != nil || !reflect.DeepEqual(changed, []string{filepath.Join("dir", "a.txt")}) {
					t.Fatalf("changed = %v, proposal %v", changed, proposal)
				}
				return
			}
			if proposal == nil || len(changed) != 0 {
				t.Fatalf("unchanged file reported as changed: %v", changed)
			}
		})
	}
}

func TestPrepareUndoRefusesWholeSetWhenOneFileChanged(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a\n", 0644)
	writeFile(t, filepath.Join(root, "b.txt"), "b\n", 0644)
	set := applyEdits(t, root, []EditOperation{
		{Kind: "replace", Path: "a.txt", OldText: "a", NewText: "A"},
		{Kind: "replace", Path: "b.txt", OldText: "b", NewText: "B"},
	})
	writeFile(t, filepath.Join(root, "b.txt"), "B and more\n", 0644)
	proposal, changed, err := PrepareUndo(root, set)
	if err != nil || proposal != nil || !reflect.DeepEqual(changed, []string{"b.txt"}) {
		t.Fatalf("PrepareUndo = %v %v %v", proposal, changed, err)
	}
	if readFile(t, filepath.Join(root, "a.txt")) != "A\n" {
		t.Fatal("an unchanged file of a refused set was reverted")
	}
}

func TestUndoRefusesFileChangedDuringReview(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a\n", 0644)
	writeFile(t, filepath.Join(root, "b.txt"), "b\n", 0644)
	set := applyEdits(t, root, []EditOperation{
		{Kind: "replace", Path: "a.txt", OldText: "a", NewText: "A"},
		{Kind: "replace", Path: "b.txt", OldText: "b", NewText: "B"},
		{Kind: "create", Path: "c.txt", Content: "c\n"},
	})
	proposal, _, err := PrepareUndo(root, set)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "b.txt"), "edited during review\n", 0644)
	result, err := proposal.Apply(context.Background())
	if !errors.Is(err, ErrStaleEdit) {
		t.Fatalf("undo over a changed file: %v", err)
	}
	if result.Status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", result.Status)
	}
	if readFile(t, filepath.Join(root, "a.txt")) != "A\n" || readFile(t, filepath.Join(root, "c.txt")) != "c\n" ||
		readFile(t, filepath.Join(root, "b.txt")) != "edited during review\n" {
		t.Fatal("a refused undo wrote to the repository")
	}
	if _, err := proposal.Apply(context.Background()); err == nil {
		t.Fatal("an undo proposal applied twice")
	}
}

func TestUndoJournalsProgressToCompletion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a\n", 0644)
	set := applyEdits(t, root, []EditOperation{
		{Kind: "replace", Path: "a.txt", OldText: "a", NewText: "A"},
		{Kind: "create", Path: "b.txt", Content: "b\n"},
	})
	proposal, _, err := PrepareUndo(root, set)
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	var last EditProgress
	if err := proposal.SetJournal(func(progress EditProgress) error {
		statuses = append(statuses, progress.Status)
		last = progress
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := proposal.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(statuses) < 3 || statuses[0] != "preparing" || last.Status != "completed" {
		t.Fatalf("journal statuses = %v", statuses)
	}
	if !reflect.DeepEqual(last.Applied, []string{"a.txt", "b.txt"}) || len(last.RetainedFiles) != 0 {
		t.Fatalf("final journal record = %+v", last)
	}
}

func TestUndoRemovalRefusesWhenJournalFails(t *testing.T) {
	root := t.TempDir()
	set := applyEdits(t, root, []EditOperation{{Kind: "create", Path: "b.txt", Content: "b\n"}})
	proposal, _, err := PrepareUndo(root, set)
	if err != nil {
		t.Fatal(err)
	}
	if err := proposal.SetJournal(func(EditProgress) error { return errors.New("disk full") }); err != nil {
		t.Fatal(err)
	}
	if _, err := proposal.Apply(context.Background()); err == nil {
		t.Fatal("undo proceeded without its private journal")
	}
	if readFile(t, filepath.Join(root, "b.txt")) != "b\n" {
		t.Fatal("undo removed a file without journaling the intent")
	}
}

func TestUndoRecoversRemovalWhenJournalFailsMidPublication(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a\n", 0644)
	set := applyEdits(t, root, []EditOperation{
		{Kind: "replace", Path: "a.txt", OldText: "a", NewText: "A"},
		{Kind: "create", Path: "b.txt", Content: "b\n"},
	})
	proposal, _, err := PrepareUndo(root, set)
	if err != nil {
		t.Fatal(err)
	}
	if err := proposal.SetJournal(func(progress EditProgress) error {
		if progress.Detail == "File removal recorded." {
			return errors.New("disk full")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := proposal.Apply(context.Background())
	if err == nil {
		t.Fatal("undo reported success after its journal failed")
	}
	if result.Status != "recovered" {
		t.Fatalf("status = %q, want recovered (%s)", result.Status, result.Summary)
	}
	if readFile(t, filepath.Join(root, "a.txt")) != "A\n" || readFile(t, filepath.Join(root, "b.txt")) != "b\n" {
		t.Fatal("recovery did not put the change set back as Likha wrote it")
	}
}
