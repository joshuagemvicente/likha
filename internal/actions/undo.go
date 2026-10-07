package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// UndoProposal is a read-only review of reverting one change set. The
// coordinator must show Diff and obtain the user's confirmation before Apply.
// Restored files get their original content back, Removed files were created
// by Likha and are deleted, and Directories (deepest first) are removed only
// when they are empty after the files are reverted.
type UndoProposal struct {
	Paths       []string
	Restored    []string
	Removed     []string
	Directories []string
	Diff        string

	edit *EditProposal
}

// UndoResult describes a finished undo. RemovedDirectories and
// KeptDirectories are only set after the files were reverted.
type UndoResult struct {
	// Status is the underlying apply report: complete, unchanged (nothing
	// was written), recovered (written then put back) or partial.
	Status             string
	Summary            string
	RemovedDirectories []string
	KeptDirectories    []string
}

// PrepareUndo only reads. It checks every file of set against what Likha
// wrote (content digest and mode; a file saved again by rename with the same
// content still matches). When any file changed, is missing, or can no
// longer be reached without following a symlink, it returns those paths and
// no proposal: undo never overwrites newer work.
func PrepareUndo(root string, set ChangeSet) (*UndoProposal, []string, error) {
	if len(set.Files) == 0 {
		return nil, nil, errors.New("change set has no files")
	}
	canonical, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, fmt.Errorf("repository: %w", err)
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return nil, nil, fmt.Errorf("repository: %w", err)
	}
	files := append([]ChangeSetFile(nil), set.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	state := &targetedProposal{root: canonical}
	dirs := make(map[string]*targetedDirectory)
	undo := &UndoProposal{Paths: []string{}, Restored: []string{}, Removed: []string{}, Directories: []string{}}
	var changed []string
	var diff strings.Builder
	for i, file := range files {
		if i > 0 && file.Path == files[i-1].Path {
			return nil, nil, fmt.Errorf("change set lists %q twice", file.Path)
		}
		parts, err := editPath(file.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("change set: %w", err)
		}
		path := filepath.Join(parts...)
		current, ok := currentWritten(canonical, parts, file, dirs)
		if !ok {
			changed = append(changed, path)
			continue
		}
		plan := &targetedFile{path: path, parts: parts, old: current.text, before: current.info}
		if file.Existed {
			plan.next = file.OldText
			undo.Restored = append(undo.Restored, path)
			diff.WriteString(unified(path, current.text, file.OldText, false))
		} else {
			plan.remove = true
			undo.Removed = append(undo.Removed, path)
			diff.WriteString(removalDiff(path, current.text))
		}
		state.files = append(state.files, plan)
		undo.Paths = append(undo.Paths, path)
	}
	if len(changed) > 0 {
		return nil, changed, nil
	}
	for _, dir := range dirs {
		state.dirs = append(state.dirs, dir)
	}
	sort.Slice(state.dirs, func(i, j int) bool {
		a, b := state.dirs[i], state.dirs[j]
		if len(a.parts) != len(b.parts) {
			return len(a.parts) < len(b.parts)
		}
		return a.path < b.path
	})
	for i := len(set.CreatedDirs) - 1; i >= 0; i-- {
		parts, err := editPath(set.CreatedDirs[i])
		if err != nil {
			return nil, nil, fmt.Errorf("change set directory: %w", err)
		}
		undo.Directories = append(undo.Directories, filepath.Join(parts...))
	}
	sort.SliceStable(undo.Directories, func(i, j int) bool {
		return strings.Count(undo.Directories[i], string(filepath.Separator)) > strings.Count(undo.Directories[j], string(filepath.Separator))
	})
	undo.Diff = diff.String()
	state.diff = undo.Diff
	undo.edit = &EditProposal{Paths: undo.Paths, Directories: []string{}, Diff: undo.Diff, state: state}
	return undo, nil, nil
}

// writtenTarget is a file's current content and metadata, confirmed to be
// exactly what Likha wrote.
type writtenTarget struct {
	text string
	info os.FileInfo
}

// currentWritten reads path without following symlinks and reports whether
// it still holds exactly what Likha wrote.
func currentWritten(root string, parts []string, file ChangeSetFile, dirs map[string]*targetedDirectory) (writtenTarget, bool) {
	parent, err := targetedSnapshotParent(root, parts, dirs)
	if err != nil || parent == nil {
		return writtenTarget{}, false
	}
	defer parent.Close()
	text, info, err := readTarget(parent, parts[len(parts)-1])
	if err != nil || info == nil {
		return writtenTarget{}, false
	}
	if contentDigest(text) != file.NewSHA256 || info.Mode() != file.NewMode {
		return writtenTarget{}, false
	}
	return writtenTarget{text: text, info: info}, true
}

// SetJournal installs private synchronous persistence for the revert, the
// same journal an edit apply uses, so an interrupted undo is reported on
// resume and never replayed.
func (u *UndoProposal) SetJournal(journal func(EditProgress) error) error {
	if u == nil || u.edit == nil {
		return errors.New("invalid undo proposal")
	}
	return u.edit.SetJournal(journal)
}

// Apply reverts the reviewed change set with the edit tool's publication
// machinery: every file is rechecked immediately before any write, and a
// file that changed during the review makes the whole undo refuse. Created
// directories are removed afterwards, only when empty.
func (u *UndoProposal) Apply(ctx context.Context) (UndoResult, error) {
	if u == nil || u.edit == nil {
		return UndoResult{}, errors.New("invalid undo proposal")
	}
	summary, err := u.edit.Apply(ctx)
	result := UndoResult{Summary: summary, Status: u.applyStatus()}
	if err != nil {
		return result, err
	}
	result.RemovedDirectories, result.KeptDirectories = []string{}, []string{}
	for _, dir := range u.Directories {
		removed, err := removeEmptyDirectory(u.edit.state.root, dir)
		if err != nil {
			return result, fmt.Errorf("remove directory %q: %w", dir, err)
		}
		if removed {
			result.RemovedDirectories = append(result.RemovedDirectories, dir)
		} else {
			result.KeptDirectories = append(result.KeptDirectories, dir)
		}
	}
	return result, nil
}

func (u *UndoProposal) applyStatus() string {
	s := u.edit.state
	s.mu.Lock()
	defer s.mu.Unlock()
	// A refusal during preflight returns before any report is written; it
	// touched nothing.
	if s.recovery == nil || s.recovery.report.Status == "" {
		return "unchanged"
	}
	return s.recovery.report.Status
}

// removeEmptyDirectory removes a directory Likha created when nothing else
// lives in it. A missing, non-empty or replaced (for example symlinked)
// entry is kept; parents are opened without following symlinks.
func removeEmptyDirectory(root, path string) (bool, error) {
	parts, err := editPath(path)
	if err != nil {
		return false, err
	}
	parent, _, err := openParent(root, parts, nil)
	if err != nil {
		return false, nil // the parent moved or became a symlink: keep it
	}
	defer parent.Close()
	err = unix.Unlinkat(int(parent.Fd()), parts[len(parts)-1], unix.AT_REMOVEDIR)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTEMPTY), errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTDIR):
		return false, nil
	default:
		return false, err
	}
}

// removalDiff renders a file deletion: every line removed, target /dev/null.
func removalDiff(path, old string) string {
	diff := unified(path, old, "", false)
	return strings.Replace(diff, "+++ b/"+path+"\n", "+++ /dev/null\n", 1)
}
