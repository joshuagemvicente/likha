package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// ChangeSet records what one successful edit apply wrote, so the user can
// later ask for it to be reverted (specs/edit-undo). It holds the original
// content of replaced files: private session data, never model context.
type ChangeSet struct {
	Files []ChangeSetFile `json:"files"`
	// CreatedDirs lists repository-relative directories the apply created,
	// shallowest first.
	CreatedDirs []string `json:"created_dirs"`
}

// ChangeSetFile is one path of a change set. NewSHA256 and NewMode describe
// what Likha wrote; undo refuses when the file no longer matches them.
type ChangeSetFile struct {
	Path      string      `json:"path"`
	Existed   bool        `json:"existed"`
	OldText   string      `json:"old_text,omitempty"`
	OldMode   os.FileMode `json:"old_mode,omitempty"`
	NewSHA256 string      `json:"new_sha256"`
	NewMode   os.FileMode `json:"new_mode"`
}

// ChangeSet describes the applied edit. ok is false until Apply succeeded.
func (e *Edit) ChangeSet() (set ChangeSet, ok bool) {
	if e == nil || !e.applied {
		return ChangeSet{}, false
	}
	return ChangeSet{Files: []ChangeSetFile{changeSetFile(e.Path, e.old, e.new, e.before)}, CreatedDirs: []string{}}, true
}

// ChangeSet describes the applied proposal. ok is false unless Apply
// completed: a failed, recovered or partially unresolved apply is not
// undoable, and its journal record keeps reporting it.
func (p *EditProposal) ChangeSet() (set ChangeSet, ok bool) {
	if p == nil || p.state == nil {
		return ChangeSet{}, false
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.completed {
		return ChangeSet{}, false
	}
	set = ChangeSet{Files: make([]ChangeSetFile, 0, len(s.files)), CreatedDirs: []string{}}
	for _, file := range s.files {
		set.Files = append(set.Files, changeSetFile(file.path, file.old, file.next, file.before))
	}
	for _, dir := range s.dirs {
		if dir.before == nil {
			set.CreatedDirs = append(set.CreatedDirs, dir.path)
		}
	}
	return set, true
}

// Size is the number of bytes of saved original content.
func (c ChangeSet) Size() int {
	size := 0
	for _, file := range c.Files {
		size += len(file.OldText)
	}
	return size
}

// Paths lists the change set's file paths in recorded order.
func (c ChangeSet) Paths() []string {
	paths := make([]string, 0, len(c.Files))
	for _, file := range c.Files {
		paths = append(paths, file.Path)
	}
	return paths
}

func changeSetFile(path, old, next string, before os.FileInfo) ChangeSetFile {
	file := ChangeSetFile{Path: path, NewSHA256: contentDigest(next), NewMode: writtenMode(before)}
	if before != nil {
		file.Existed, file.OldText, file.OldMode = true, old, before.Mode()
	}
	return file
}

// writtenMode is the mode both edit tools give the file they publish: the
// replaced file's mode, or 0644 for a new file.
func writtenMode(before os.FileInfo) os.FileMode {
	if before == nil {
		return 0644
	}
	return before.Mode()
}

func contentDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
