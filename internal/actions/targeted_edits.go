package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	MaxEditOperations    = 64
	MaxEditProposalBytes = 4 << 20
)

type EditOperation struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	OldText string `json:"old_text,omitempty"`
	NewText string `json:"new_text,omitempty"`
	Content string `json:"content,omitempty"`
}

// EditProposal is a read-only review of one original snapshot. The coordinator
// must show all Paths, Directories and Diff and obtain approval before Apply.
// Changing these public review fields invalidates, rather than changes, the plan.
type EditProposal struct {
	Paths       []string
	Directories []string
	Diff        string

	state *targetedProposal
}

type targetedProposal struct {
	mu       sync.Mutex
	used     bool
	root     string
	diff     string
	files    []*targetedFile
	dirs     []*targetedDirectory
	recovery *targetedApply
	result   string
	journal  func(EditProgress) error
}

type targetedFile struct {
	path   string
	parts  []string
	old    string
	next   string
	before os.FileInfo
}

type targetedDirectory struct {
	path   string // "." denotes the canonical repository root
	parts  []string
	before os.FileInfo // nil denotes an absent directory, not permission to adopt one
}

type targetedMatch struct {
	start, end int
	text       string
}

// PrepareEdits only reads. Replacements all match the original file, including
// overlapping occurrences of old_text; no operation sees another's output.
func PrepareEdits(root string, ops []EditOperation) (*EditProposal, error) {
	if len(ops) == 0 || len(ops) > MaxEditOperations {
		return nil, fmt.Errorf("edit requires between 1 and %d operations", MaxEditOperations)
	}
	groups := make(map[string][]EditOperation)
	partsByPath := make(map[string][]string)
	payloadBytes := 0
	for i, op := range ops {
		parts, err := editPath(op.Path)
		if err != nil {
			return nil, fmt.Errorf("operation %d: %w", i+1, err)
		}
		if !previewSafeText(op.Path) {
			return nil, fmt.Errorf("operation %d: path must be preview-safe UTF-8 text", i+1)
		}
		switch op.Kind {
		case "create":
			if op.OldText != "" || op.NewText != "" {
				return nil, fmt.Errorf("operation %d: create accepts content, not old_text/new_text", i+1)
			}
			if len(op.Content) > MaxEditBytes || !previewSafeText(op.Content) {
				return nil, fmt.Errorf("operation %d: content must be preview-safe UTF-8 text of at most %d bytes", i+1, MaxEditBytes)
			}
			payloadBytes += len(op.Content)
		case "replace":
			if op.Content != "" || op.OldText == "" {
				return nil, fmt.Errorf("operation %d: replace requires nonempty old_text and accepts no content", i+1)
			}
			if len(op.OldText) > MaxEditBytes || len(op.NewText) > MaxEditBytes ||
				!previewSafeText(op.OldText) || !previewSafeText(op.NewText) {
				return nil, fmt.Errorf("operation %d: old_text/new_text must be preview-safe UTF-8 text of at most %d bytes", i+1, MaxEditBytes)
			}
			if op.OldText == op.NewText {
				return nil, fmt.Errorf("operation %d: replacement does not change text", i+1)
			}
			payloadBytes += len(op.NewText)
		default:
			return nil, fmt.Errorf("operation %d: unsupported edit kind %q (only create and replace are supported)", i+1, op.Kind)
		}
		if payloadBytes > MaxEditProposalBytes {
			return nil, fmt.Errorf("proposed content exceeds %d bytes", MaxEditProposalBytes)
		}
		path := filepath.Join(parts...)
		groups[path] = append(groups[path], op)
		partsByPath[path] = parts
	}
	paths := make([]string, 0, len(groups))
	for path, group := range groups {
		for _, op := range group {
			if op.Kind == "create" && len(group) != 1 {
				return nil, fmt.Errorf("%q: create cannot be repeated or combined with replacement", path)
			}
		}
		parts := partsByPath[path]
		for n := 1; n < len(parts); n++ {
			if _, exists := groups[filepath.Join(parts[:n]...)]; exists {
				return nil, fmt.Errorf("%q: another edit targets a required parent directory", path)
			}
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	canonical, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}
	state := &targetedProposal{root: canonical}
	dirs := make(map[string]*targetedDirectory)
	proposal := &EditProposal{Paths: paths, Directories: []string{}, state: state}
	var diff strings.Builder
	totalBytes := 0
	for _, path := range paths {
		parts := partsByPath[path]
		parent, err := targetedSnapshotParent(canonical, parts, dirs)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		file := &targetedFile{path: path, parts: parts}
		if parent != nil {
			file.old, file.before, err = readTarget(parent, parts[len(parts)-1])
			parent.Close()
			if err != nil {
				return nil, fmt.Errorf("%q: %w", path, err)
			}
		}
		group := groups[path]
		if group[0].Kind == "create" {
			if file.before != nil {
				return nil, fmt.Errorf("%q: create requires an absent file", path)
			}
			file.next = group[0].Content
		} else {
			if file.before == nil {
				return nil, fmt.Errorf("%q: replacement requires an existing file", path)
			}
			matches := make([]targetedMatch, 0, len(group))
			length := len(file.old)
			for _, op := range group {
				at := strings.Index(file.old, op.OldText)
				if at < 0 {
					return nil, fmt.Errorf("%q: old_text does not match the original file", path)
				}
				// Starting at the next byte detects overlapping occurrences too.
				if strings.Contains(file.old[at+1:], op.OldText) {
					return nil, fmt.Errorf("%q: old_text is ambiguous in the original file", path)
				}
				matches = append(matches, targetedMatch{at, at + len(op.OldText), op.NewText})
				length += len(op.NewText) - len(op.OldText)
			}
			sort.Slice(matches, func(i, j int) bool { return matches[i].start < matches[j].start })
			for i := 1; i < len(matches); i++ {
				if matches[i].start < matches[i-1].end {
					return nil, fmt.Errorf("%q: replacements overlap in the original file", path)
				}
			}
			if length > MaxEditBytes {
				return nil, fmt.Errorf("%q: resulting content exceeds %d bytes", path, MaxEditBytes)
			}
			var next strings.Builder
			next.Grow(length)
			from := 0
			for _, match := range matches {
				next.WriteString(file.old[from:match.start])
				next.WriteString(match.text)
				from = match.end
			}
			next.WriteString(file.old[from:])
			file.next = next.String()
			if file.next == file.old {
				return nil, fmt.Errorf("%q: combined replacements do not change the file", path)
			}
		}
		if !previewSafeText(file.next) {
			return nil, fmt.Errorf("%q: resulting content is not preview-safe UTF-8 text", path)
		}
		totalBytes += len(file.next)
		if totalBytes > MaxEditProposalBytes {
			return nil, fmt.Errorf("proposed content exceeds %d bytes", MaxEditProposalBytes)
		}
		state.files = append(state.files, file)
		diff.WriteString(unified(path, file.old, file.next, file.before == nil))
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
	for _, dir := range state.dirs {
		if dir.before == nil {
			proposal.Directories = append(proposal.Directories, dir.path)
		}
	}
	proposal.Diff = diff.String()
	state.diff = proposal.Diff
	return proposal, nil
}

func targetedSnapshotParent(root string, parts []string, dirs map[string]*targetedDirectory) (*os.File, error) {
	current, _, err := openParent(root, []string{"."}, nil)
	if err != nil {
		return nil, err
	}
	for depth := 0; depth < len(parts); depth++ {
		path := "."
		if depth > 0 {
			path = filepath.Join(parts[:depth]...)
		}
		var info os.FileInfo
		if current != nil {
			info, err = current.Stat()
			if err != nil {
				current.Close()
				return nil, err
			}
		}
		if previous, exists := dirs[path]; exists {
			if (previous.before == nil) != (info == nil) || info != nil && !sameState(info, previous.before) {
				if current != nil {
					current.Close()
				}
				return nil, fmt.Errorf("directory %q: %w", path, ErrStaleEdit)
			}
		} else {
			dirs[path] = &targetedDirectory{path: path, parts: append([]string(nil), parts[:depth]...), before: info}
		}
		if depth == len(parts)-1 {
			return current, nil
		}
		if current != nil {
			fd, openErr := unix.Openat(int(current.Fd()), parts[depth], unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			current.Close()
			current = nil
			if openErr == nil {
				current = os.NewFile(uintptr(fd), parts[depth])
			} else if !errors.Is(openErr, unix.ENOENT) {
				return nil, fmt.Errorf("open parent %q (symlinks not allowed): %w", parts[depth], openErr)
			}
		}
	}
	panic("empty edit path")
}

type targetedOpenDirectory struct {
	plan        *targetedDirectory
	parent      *targetedOpenDirectory
	file        *os.File
	info        os.FileInfo
	name        string // may temporarily be a staging name
	owned       bool
	stagePlan   string
	cleanupPlan string
}

type targetedStagedFile struct {
	plan              *targetedFile
	parent            *targetedOpenDirectory
	stage             string
	stageInfo         os.FileInfo
	backup            string
	backupID          targetedIdentity
	quarantine        string
	published         bool
	restored          bool
	unresolved        bool
	stagePlan         string
	backupPlan        string
	quarantinePlan    string
	stageCleanup      string
	backupCleanup     string
	quarantineCleanup string
}

type targetedIdentity struct {
	device, inode uint64
	mode          uint32
}

type targetedApply struct {
	proposal        *targetedProposal
	dirs            map[string]*targetedOpenDirectory
	files           []*targetedStagedFile
	report          targetedApplyReport
	started         bool
	phase           string
	journalFailed   bool
	journalFailures []error
}

type targetedApplyReport struct {
	// Applied records every output published, even if subsequently restored.
	// Restored records displaced entries returned without overwriting newer
	// work (including user writes to their saved original inode). Unresolved
	// records effects that could not be recovered, not a grant to retry them.
	Status              string   `json:"status"`
	Applied             []string `json:"applied"`
	Restored            []string `json:"restored"`
	Unresolved          []string `json:"unresolved"`
	LeftoverDirectories []string `json:"leftover_directories"`
	RetainedFiles       []string `json:"retained_files"`
	Detail              string   `json:"detail,omitempty"`
}

// Apply consumes one approved proposal. No target is touched until every
// original target/ancestor has passed preflight and every output is staged.
// Before the first target publication, cancellation cleans up staged work and
// newly created parents. After that boundary cancellation does not interrupt
// publication/recovery; the caller must retain the returned summary even on error.
//
// Publishing a replacement moves its original inode aside with an exclusive
// rename, then exclusively links the staged output. Unlike rename-over-existing,
// this never overwrites a concurrently created target. A target can briefly be
// absent, and a crash can leave originals/staged files beside a partial group.
// This is deliberately NOT a filesystem transaction or crash-atomic operation.
//
// SetJournal supplies synchronous private persistence, independent of approval.
// The coordinator owns session/call identity, durable storage, and read-only
// interruption reporting; these actions never replay saved records on resume or
// write a hidden repository manifest. Journal and filesystem effects are not one
// atomic transaction: pending intents and reserved names require manual inspection.
func (p *EditProposal) Apply(ctx context.Context) (string, error) {
	if p == nil || p.state == nil || ctx == nil {
		return "", errors.New("invalid edit proposal or context")
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used {
		return "", errors.New("edit proposal already consumed; prepare and approve a new proposal")
	}
	s.used = true
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !s.matchesReview(p) {
		return "", errors.New("edit review changed; prepare and approve a new proposal")
	}
	if !targetedExclusiveRenameSupported {
		return "", errors.New("targeted edits require exclusive rename support on this platform")
	}
	a := &targetedApply{proposal: s, dirs: make(map[string]*targetedOpenDirectory), phase: "preparing"}
	a.report = targetedApplyReport{Applied: []string{}, Restored: []string{}, Unresolved: []string{},
		LeftoverDirectories: []string{}, RetainedFiles: []string{}}
	s.recovery = a
	defer a.close()
	if err := a.preflight(); err != nil {
		return "", err
	}
	err := a.reserveNames()
	if err == nil {
		// The first callback follows read-only preflight and precedes EVERY
		// repository effect. Failure here cannot create even a staging entry.
		err = a.checkpoint("preparing", EditProgressStep{}, "Private recovery snapshot; never replay or inject original text into provider context.")
	}
	if err == nil {
		err = a.prepare(ctx)
	}
	if err == nil {
		// Staging/parent creation legitimately changes directory timestamps.
		// Recheck every directory's identity/mode and every original target,
		// including untouched files late in the group, before any publication.
		err = a.checkAll(false)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = a.publish(ctx)
	}
	if err == nil {
		err = a.checkAll(true)
	}
	if err == nil {
		// Keep originals until the complete group has been verified. An old
		// inode changed through an open descriptor is user work, not garbage.
		for _, f := range a.files {
			if f.backup != "" {
				old, info, readErr := readTarget(f.parent.file, f.backup)
				if readErr != nil || !sameState(info, f.plan.before) || old != f.plan.old {
					err = fmt.Errorf("original of %q changed during publication: %w", f.plan.path, ErrStaleEdit)
					break
				}
			}
		}
	}
	if err == nil {
		err = a.checkpoint("publishing", EditProgressStep{Action: "cleanup"}, "All published targets verified; cleanup is still pending.")
	}
	if err != nil && a.started {
		err = errors.Join(err, a.recover())
	}
	complete := err == nil
	cleanupErr := a.cleanup(complete)
	err = errors.Join(err, cleanupErr)
	if cleanupErr != nil && complete && a.started {
		// A cleanup journal failure is still a failure after publication.
		// Originals already discarded cannot be fabricated/replayed; recovery
		// reports those paths unresolved and preserves the published file.
		err = errors.Join(err, a.recover(), a.cleanup(false))
	}
	err = errors.Join(err, a.takeJournalFailures())
	if err == nil {
		if verifyErr := a.checkAll(true); verifyErr != nil {
			err = errors.Join(verifyErr, a.recover(), a.cleanup(false), a.takeJournalFailures())
		}
	}
	a.finishReport(err)
	finalErr := a.checkpoint(a.finalJournalStatus(err), EditProgressStep{}, a.report.Detail)
	if finalErr != nil {
		wasComplete := err == nil
		err = errors.Join(err, finalErr)
		if wasComplete && a.started {
			err = errors.Join(err, a.recover(), a.cleanup(false), a.takeJournalFailures())
		}
		a.finishReport(err)
		// One bounded retry, never a "completed" record after failed or
		// incomplete recovery. Keep the real result even if this also fails.
		err = errors.Join(err, a.checkpoint(a.finalJournalStatus(err), EditProgressStep{}, a.report.Detail))
		a.finishReport(err)
	}
	s.result = a.summary()
	return s.result, err
}

func (s *targetedProposal) matchesReview(p *EditProposal) bool {
	if p.Diff != s.diff || len(p.Paths) != len(s.files) {
		return false
	}
	for i, file := range s.files {
		if p.Paths[i] != file.path {
			return false
		}
	}
	at := 0
	for _, dir := range s.dirs {
		if dir.before == nil {
			if at >= len(p.Directories) || p.Directories[at] != dir.path {
				return false
			}
			at++
		}
	}
	return at == len(p.Directories)
}

func (a *targetedApply) preflight() error {
	for _, plan := range a.proposal.dirs {
		d := &targetedOpenDirectory{plan: plan}
		a.dirs[plan.path] = d
		var fd int
		var err error
		if len(plan.parts) == 0 {
			fd, err = unix.Open(a.proposal.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		} else {
			d.name = plan.parts[len(plan.parts)-1]
			d.parent = a.dirs[targetedParentPath(plan.parts)]
			if d.parent.file == nil {
				if plan.before != nil {
					return fmt.Errorf("directory %q: %w", plan.path, ErrStaleEdit)
				}
				continue
			}
			fd, err = unix.Openat(int(d.parent.file.Fd()), d.name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if err != nil {
			if plan.before == nil && errors.Is(err, unix.ENOENT) {
				continue
			}
			return fmt.Errorf("directory %q: %w: %v", plan.path, ErrStaleEdit, err)
		}
		d.file = os.NewFile(uintptr(fd), plan.path)
		d.info, err = d.file.Stat()
		if err != nil {
			return err
		}
		if !sameState(d.info, plan.before) {
			return fmt.Errorf("directory %q: %w", plan.path, ErrStaleEdit)
		}
	}
	for _, plan := range a.proposal.files {
		parentPath := targetedParentPath(plan.parts)
		f := &targetedStagedFile{plan: plan, parent: a.dirs[parentPath]}
		a.files = append(a.files, f)
		if f.parent.file != nil {
			if err := f.checkOriginal(); err != nil {
				return err
			}
		} else if plan.before != nil {
			return fmt.Errorf("%q: %w", plan.path, ErrStaleEdit)
		}
	}
	return nil
}

func targetedParentPath(parts []string) string {
	if len(parts) <= 1 {
		return "."
	}
	return filepath.Join(parts[:len(parts)-1]...)
}

func (a *targetedApply) prepare(ctx context.Context) error {
	for _, plan := range a.proposal.dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		d := a.dirs[plan.path]
		if d.file != nil {
			continue
		}
		if err := a.checkDirectory(d.parent); err != nil {
			return err
		}
		// Open our unpredictable staging directory before making it a public
		// parent. Do not adopt a directory that another writer created.
		tmp := d.stagePlan
		if err := a.checkpoint("preparing", EditProgressStep{Action: "stage-parent", Path: plan.path,
			To: targetedArtifact(plan.parts, tmp)}, ""); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.checkDirectory(d.parent); err != nil {
			return err
		}
		if err := unix.Mkdirat(int(d.parent.file.Fd()), tmp, 0755); err != nil {
			return fmt.Errorf("stage parent %q: %w", plan.path, err)
		}
		d.name, d.owned = tmp, true
		fd, err := unix.Openat(int(d.parent.file.Fd()), tmp, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open staged parent %q: %w", plan.path, err)
		}
		d.file = os.NewFile(uintptr(fd), tmp)
		d.info, err = d.file.Stat()
		if err != nil {
			return err
		}
		if err := a.checkpoint("preparing", EditProgressStep{}, "Staged parent identity recorded."); err != nil {
			return err
		}
		if err := a.checkDirectory(d.parent); err != nil {
			return err
		}
		name := plan.parts[len(plan.parts)-1]
		if err := a.checkpoint("preparing", EditProgressStep{Action: "create-parent", Path: plan.path,
			From: targetedArtifact(plan.parts, tmp), To: plan.path}, ""); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.checkDirectory(d.parent); err != nil {
			return err
		}
		if err := targetedRenameExclusive(int(d.parent.file.Fd()), tmp, name); err != nil {
			return fmt.Errorf("create parent %q: %w", plan.path, targetedConflict(err))
		}
		d.name = name
		if err := a.checkpoint("preparing", EditProgressStep{}, "New parent published."); err != nil {
			return err
		}
		if err := a.checkDirectory(d); err != nil {
			return err
		}
	}
	for _, f := range a.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.checkDirectory(f.parent); err != nil {
			return err
		}
		if err := a.stageFile(f); err != nil {
			return err
		}
	}
	return nil
}

func targetedTempName() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("temporary name: %w", err)
	}
	return ".likha-edit-" + hex.EncodeToString(nonce[:]), nil
}

func (a *targetedApply) stageFile(f *targetedStagedFile) error {
	name := f.stagePlan
	if err := a.checkpoint("preparing", EditProgressStep{Action: "stage-file", Path: f.plan.path,
		To: targetedArtifact(f.plan.parts, name)}, ""); err != nil {
		return err
	}
	if err := a.checkDirectory(f.parent); err != nil {
		return err
	}
	fd, err := unix.Openat(int(f.parent.file.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("stage %q: %w", f.plan.path, err)
	}
	f.stage = name
	tmp := os.NewFile(uintptr(fd), name)
	defer tmp.Close()
	// Record identity even if a later write/chmod/sync fails, so cleanup never
	// blindly unlinks a staging name replaced by somebody else.
	f.stageInfo, err = tmp.Stat()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(tmp, f.plan.next); err != nil {
		return fmt.Errorf("stage %q: %w", f.plan.path, err)
	}
	mode := os.FileMode(0644)
	if f.plan.before != nil {
		mode = f.plan.before.Mode()
	}
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= 04000
	}
	if mode&os.ModeSetgid != 0 {
		bits |= 02000
	}
	if mode&os.ModeSticky != 0 {
		bits |= 01000
	}
	if err := unix.Fchmod(fd, bits); err != nil {
		return fmt.Errorf("preserve mode of %q: %w", f.plan.path, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync staged %q: %w", f.plan.path, err)
	}
	f.stageInfo, err = tmp.Stat()
	if err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return a.checkpoint("preparing", EditProgressStep{}, "Staged file synced and identity recorded.")
}

func (a *targetedApply) checkDirectory(d *targetedOpenDirectory) error {
	if d == nil || d.file == nil || d.info == nil {
		return errors.New("missing prepared parent descriptor")
	}
	parts := append(append([]string(nil), d.plan.parts...), ".")
	parent, infos, err := openParent(a.proposal.root, parts, nil)
	if err != nil {
		return fmt.Errorf("directory %q: %w: %v", d.plan.path, ErrStaleEdit, err)
	}
	defer parent.Close()
	for i, info := range infos {
		path := "."
		if i > 0 {
			path = filepath.Join(d.plan.parts[:i]...)
		}
		expected := a.dirs[path]
		if expected == nil || expected.info == nil || !os.SameFile(info, expected.info) || info.Mode() != expected.info.Mode() {
			return fmt.Errorf("directory %q: %w", path, ErrStaleEdit)
		}
	}
	return nil
}

func (f *targetedStagedFile) checkOriginal() error {
	old, info, err := readTarget(f.parent.file, f.plan.parts[len(f.plan.parts)-1])
	if err != nil {
		return fmt.Errorf("%q: %w: %v", f.plan.path, ErrStaleEdit, err)
	}
	if (info == nil) != (f.plan.before == nil) || info != nil && (!sameState(info, f.plan.before) || old != f.plan.old) {
		return fmt.Errorf("%q: %w", f.plan.path, ErrStaleEdit)
	}
	return nil
}

func (f *targetedStagedFile) checkWritten(name string) error {
	text, info, err := readTarget(f.parent.file, name)
	if err != nil || !sameState(info, f.stageInfo) || text != f.plan.next {
		return fmt.Errorf("proposal-written %q changed: %w", f.plan.path, ErrStaleEdit)
	}
	return nil
}

func (a *targetedApply) checkAll(written bool) error {
	for _, plan := range a.proposal.dirs {
		if err := a.checkDirectory(a.dirs[plan.path]); err != nil {
			return err
		}
	}
	for _, f := range a.files {
		if written {
			if err := f.checkWritten(f.plan.parts[len(f.plan.parts)-1]); err != nil {
				return err
			}
		} else {
			if err := f.checkOriginal(); err != nil {
				return err
			}
			if err := f.checkWritten(f.stage); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *targetedApply) publish(ctx context.Context) error {
	for _, f := range a.files {
		if !a.started {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := a.checkDirectory(f.parent); err != nil {
			return err
		}
		if err := f.checkOriginal(); err != nil {
			return err
		}
		if err := f.checkWritten(f.stage); err != nil {
			return err
		}
		name := f.plan.parts[len(f.plan.parts)-1]
		fd := int(f.parent.file.Fd())
		if f.plan.before != nil {
			backup := f.backupPlan
			if err := a.checkpoint("publishing", EditProgressStep{Action: "save-original", Path: f.plan.path,
				From: f.plan.path, To: targetedArtifact(f.plan.parts, backup)}, ""); err != nil {
				return err
			}
			if err := a.checkDirectory(f.parent); err != nil {
				return err
			}
			if err := f.checkOriginal(); err != nil {
				return err
			}
			if !a.started {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if err := targetedRenameExclusive(fd, name, backup); err != nil {
				return fmt.Errorf("save original %q: %w", f.plan.path, targetedConflict(err))
			}
			a.started, f.backup = true, backup
			identity, err := targetedIdentityAt(f.parent.file, backup)
			f.backupID = identity
			if err != nil {
				f.unresolved = true
				return fmt.Errorf("identify saved original %q: %w", f.plan.path, err)
			}
			if err := a.checkpoint("publishing", EditProgressStep{}, "Original moved to its recorded backup."); err != nil {
				return err
			}
			old, info, err := readTarget(f.parent.file, backup)
			if err != nil || !sameState(info, f.plan.before) || old != f.plan.old {
				return fmt.Errorf("original %q changed at publication: %w", f.plan.path, ErrStaleEdit)
			}
		}
		if !a.started {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := a.checkpoint("publishing", EditProgressStep{Action: "publish-file", Path: f.plan.path,
			From: targetedArtifact(f.plan.parts, f.stage), To: f.plan.path}, ""); err != nil {
			return err
		}
		if err := a.checkDirectory(f.parent); err != nil {
			return err
		}
		if err := f.checkWritten(f.stage); err != nil {
			return err
		}
		if f.plan.before == nil {
			if err := f.checkOriginal(); err != nil {
				return err
			}
		}
		if !a.started {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := unix.Linkat(fd, f.stage, fd, name, 0); err != nil {
			return fmt.Errorf("publish %q: %w", f.plan.path, targetedConflict(err))
		}
		a.started, f.published = true, true
		a.report.Applied = append(a.report.Applied, f.plan.path)
		if err := a.checkpoint("publishing", EditProgressStep{}, "File publication recorded."); err != nil {
			return err
		}
	}
	return nil
}

func targetedConflict(err error) error {
	if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%w: %v", ErrStaleEdit, err)
	}
	return err
}

// Exclusive moves during recovery avoid a check-then-rename overwrite. If the
// entry moved after our read was actually concurrent user work, move THAT entry
// back exclusively instead of replacing it with the old snapshot. If its public
// name was concurrently occupied, retain it at the reported quarantine path.
func (a *targetedApply) recover() error {
	var failures []error
	a.phase = "recovering"
	a.checkpointBestEffort(EditProgressStep{})
	for i := len(a.files) - 1; i >= 0; i-- {
		f := a.files[i]
		if f.restored || !f.published && f.backup == "" {
			continue
		}
		if err := a.recoverFile(f); err != nil {
			f.unresolved = true
			failures = append(failures, fmt.Errorf("recover %q: %w", f.plan.path, err))
		}
		a.checkpointBestEffort(EditProgressStep{})
	}
	return errors.Join(failures...)
}

func (a *targetedApply) recoverFile(f *targetedStagedFile) error {
	if err := a.checkDirectory(f.parent); err != nil {
		return err
	}
	fd := int(f.parent.file.Fd())
	name := f.plan.parts[len(f.plan.parts)-1]
	if f.plan.before != nil && f.backup == "" {
		return errors.New("saved original already discarded; preserve the published file for manual inspection, never replay old_text")
	}
	if f.published {
		if err := f.checkWritten(name); err != nil {
			return err // changed/removed/replaced files belong to the user now
		}
		quarantine := f.quarantinePlan
		a.checkpointBestEffort(EditProgressStep{Action: "quarantine-written", Path: f.plan.path,
			From: f.plan.path, To: targetedArtifact(f.plan.parts, quarantine)})
		if err := a.checkDirectory(f.parent); err != nil {
			return err
		}
		if err := f.checkWritten(name); err != nil {
			return err
		}
		if err := targetedRenameExclusive(fd, name, quarantine); err != nil {
			return err
		}
		f.quarantine = quarantine
		a.checkpointBestEffort(EditProgressStep{})
		if err := f.checkWritten(quarantine); err != nil {
			a.checkpointBestEffort(EditProgressStep{Action: "return-concurrent-entry", Path: f.plan.path,
				From: targetedArtifact(f.plan.parts, quarantine), To: f.plan.path})
			if parentErr := a.checkDirectory(f.parent); parentErr != nil {
				return errors.Join(err, parentErr)
			}
			putBackErr := targetedRenameExclusive(fd, quarantine, name)
			if putBackErr == nil {
				f.quarantine = ""
			}
			a.checkpointBestEffort(EditProgressStep{})
			return errors.Join(err, putBackErr)
		}
	}
	if f.backup != "" {
		identity, err := targetedIdentityAt(f.parent.file, f.backup)
		if err == nil && identity != f.backupID {
			err = errors.New("saved original identity changed; retained without replay")
		}
		if err == nil {
			// Restore the saved inode, not a rewrite of the snapshot. Any user
			// writes through an old open descriptor are preserved as well.
			a.checkpointBestEffort(EditProgressStep{Action: "restore-original", Path: f.plan.path,
				From: targetedArtifact(f.plan.parts, f.backup), To: f.plan.path})
			err = a.checkDirectory(f.parent)
			if err == nil {
				err = targetedRenameExclusive(fd, f.backup, name)
			}
		}
		if err != nil {
			if f.quarantine != "" {
				a.checkpointBestEffort(EditProgressStep{Action: "return-written-entry", Path: f.plan.path,
					From: targetedArtifact(f.plan.parts, f.quarantine), To: f.plan.path})
				if parentErr := a.checkDirectory(f.parent); parentErr != nil {
					return errors.Join(err, parentErr)
				}
				putBackErr := targetedRenameExclusive(fd, f.quarantine, name)
				if putBackErr == nil {
					f.quarantine = ""
				}
				a.checkpointBestEffort(EditProgressStep{})
				err = errors.Join(err, putBackErr)
			}
			return err
		}
		f.backup = ""
	}
	f.restored, f.unresolved = true, false
	a.checkpointBestEffort(EditProgressStep{})
	return nil
}

func targetedIdentityAt(parent *os.File, name string) (targetedIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return targetedIdentity{}, err
	}
	return targetedIdentity{uint64(stat.Dev), uint64(stat.Ino), uint32(stat.Mode)}, nil
}

func (a *targetedApply) cleanup(complete bool) error {
	var failures []error
	for _, f := range a.files {
		if err := a.checkDirectory(f.parent); err != nil {
			if f.stage != "" || f.backup != "" || f.quarantine != "" {
				failures = append(failures, err)
			}
			continue
		}
		if f.stage != "" {
			if err := a.removeFileArtifact(f, &f.stage, f.stageInfo, f.stageCleanup, complete); err != nil {
				if complete {
					return err
				}
				failures = append(failures, fmt.Errorf("remove staging file for %q: %w", f.plan.path, err))
			}
		}
		if f.quarantine != "" && f.restored {
			if err := f.checkWritten(f.quarantine); err != nil {
				failures = append(failures, err)
			} else {
				if err := a.removeFileArtifact(f, &f.quarantine, f.stageInfo, f.quarantineCleanup, complete); err != nil {
					if complete {
						return err
					}
					failures = append(failures, err)
				}
			}
		}
		if complete && f.backup != "" {
			old, info, err := readTarget(f.parent.file, f.backup)
			if err != nil || !sameState(info, f.plan.before) || old != f.plan.old {
				return fmt.Errorf("saved original for %q changed; retained", f.plan.path)
			} else {
				if err := a.removeFileArtifact(f, &f.backup, f.plan.before, f.backupCleanup, complete); err != nil {
					return err
				}
			}
		}
		if complete {
			if err := a.checkpoint(a.phase, EditProgressStep{}, "File artifact cleanup recorded."); err != nil {
				return err
			}
		} else {
			a.checkpointBestEffort(EditProgressStep{})
		}
	}
	if !complete {
		for i := len(a.proposal.dirs) - 1; i >= 0; i-- {
			d := a.dirs[a.proposal.dirs[i].path]
			if d.owned {
				if err := a.removeDirectory(d); err != nil {
					failures = append(failures, fmt.Errorf("remove new parent %q: %w", d.plan.path, err))
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (a *targetedApply) removeFileArtifact(f *targetedStagedFile, slot *string, expected os.FileInfo, destination string, complete bool) error {
	remaining, err := targetedRemoveStaged(f.parent.file, *slot, expected, destination, func(current string, pending EditProgressStep) error {
		*slot = current
		pending.Path = f.plan.path
		pending.From = targetedArtifact(f.plan.parts, pending.From)
		pending.To = targetedArtifact(f.plan.parts, pending.To)
		if complete {
			if err := a.checkpoint(a.phase, pending, "Publication cleanup; do not infer completion before the final record."); err != nil {
				return err
			}
		} else {
			a.checkpointBestEffort(pending)
		}
		if pending.Action != "" {
			return a.checkDirectory(f.parent)
		}
		return nil
	})
	*slot = remaining
	return err
}

func targetedRemoveStaged(parent *os.File, name string, expected os.FileInfo, quarantine string, update func(string, EditProgressStep) error) (string, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", nil
	}
	if err != nil {
		return name, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return name, err
	}
	if expected == nil || !os.SameFile(info, expected) {
		return name, errors.New("temporary entry identity changed; retained")
	}
	parentFD := int(parent.Fd())
	if name != quarantine {
		if err := update(name, EditProgressStep{Action: "quarantine-artifact", From: name, To: quarantine}); err != nil {
			return name, err
		}
		if err := targetedRenameExclusive(parentFD, name, quarantine); err != nil {
			return name, err
		}
	}
	if err := update(quarantine, EditProgressStep{}); err != nil {
		return quarantine, err
	}
	// Verify the entry actually moved, not only the descriptor read before
	// the move. A substituted entry is put back without overwriting anything.
	checkFD, err := unix.Openat(parentFD, quarantine, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		check := os.NewFile(uintptr(checkFD), quarantine)
		moved, statErr := check.Stat()
		check.Close()
		err = statErr
		if err == nil && !os.SameFile(moved, expected) {
			err = errors.New("temporary entry changed during cleanup; retained")
		}
	}
	if err != nil {
		if name == quarantine {
			return quarantine, err
		}
		if journalErr := update(quarantine, EditProgressStep{Action: "return-concurrent-artifact", From: quarantine, To: name}); journalErr != nil {
			return quarantine, errors.Join(err, journalErr)
		}
		putBackErr := targetedRenameExclusive(parentFD, quarantine, name)
		if putBackErr == nil {
			return name, errors.Join(err, update(name, EditProgressStep{}))
		}
		return quarantine, errors.Join(err, putBackErr)
	}
	// Only an owned, unpredictable temporary name is unlinked, never a public
	// target. A hostile writer with an already-open inode still cannot be made
	// transactional by these filesystem primitives.
	if err := update(quarantine, EditProgressStep{Action: "remove-artifact", From: quarantine}); err != nil {
		return quarantine, err
	}
	if err := unix.Unlinkat(parentFD, quarantine, 0); err != nil {
		return quarantine, err
	}
	return "", update("", EditProgressStep{})
}

func (a *targetedApply) removeDirectory(d *targetedOpenDirectory) error {
	if d.file == nil || d.info == nil {
		return errors.New("new directory identity unavailable; retained")
	}
	if err := a.checkDirectory(d.parent); err != nil {
		return err
	}
	// Never temporarily displace a new parent that now contains user work.
	readFD, err := unix.Openat(int(d.file.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	reader := os.NewFile(uintptr(readFD), d.name)
	entries, readErr := reader.ReadDir(1)
	reader.Close()
	if len(entries) != 0 {
		return errors.New("new parent is not empty; retained")
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	fd := int(d.parent.file.Fd())
	quarantine := d.cleanupPlan
	name := d.name
	a.checkpointBestEffort(EditProgressStep{Action: "quarantine-parent", Path: d.plan.path,
		From: targetedArtifact(d.plan.parts, name), To: targetedArtifact(d.plan.parts, quarantine)})
	if err := a.checkDirectory(d.parent); err != nil {
		return err
	}
	if err := targetedRenameExclusive(fd, name, quarantine); err != nil {
		return err
	}
	d.name = quarantine
	a.checkpointBestEffort(EditProgressStep{})
	checkFD, err := unix.Openat(fd, quarantine, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		file := os.NewFile(uintptr(checkFD), quarantine)
		info, statErr := file.Stat()
		file.Close()
		err = statErr
		if err == nil && !os.SameFile(info, d.info) {
			err = errors.New("new parent identity changed; retained")
		}
	}
	if err == nil {
		a.checkpointBestEffort(EditProgressStep{Action: "remove-parent", Path: d.plan.path,
			From: targetedArtifact(d.plan.parts, quarantine)})
		if err := a.checkDirectory(d.parent); err != nil {
			return err
		}
		err = unix.Unlinkat(fd, quarantine, unix.AT_REMOVEDIR)
		if err == nil {
			d.owned = false
			a.checkpointBestEffort(EditProgressStep{})
			return nil
		}
	}
	a.checkpointBestEffort(EditProgressStep{Action: "return-parent", Path: d.plan.path,
		From: targetedArtifact(d.plan.parts, quarantine), To: targetedArtifact(d.plan.parts, name)})
	if parentErr := a.checkDirectory(d.parent); parentErr != nil {
		return errors.Join(err, parentErr)
	}
	putBackErr := targetedRenameExclusive(fd, quarantine, name)
	if putBackErr == nil {
		d.name = name
	}
	a.checkpointBestEffort(EditProgressStep{})
	return errors.Join(err, putBackErr)
}

func (a *targetedApply) finishReport(err error) {
	// Final journaling can fail after a previous summary was assembled. Rebuild
	// the effect lists rather than duplicate or retain obsolete recovery results.
	a.report.Restored = []string{}
	a.report.Unresolved = []string{}
	a.report.LeftoverDirectories = []string{}
	a.report.RetainedFiles = []string{}
	a.report.Detail = ""
	for _, f := range a.files {
		if f.restored {
			a.report.Restored = append(a.report.Restored, f.plan.path)
		}
		if f.unresolved || err != nil && f.published && !f.restored {
			a.report.Unresolved = append(a.report.Unresolved, f.plan.path)
		}
		for _, name := range []string{f.stage, f.backup, f.quarantine} {
			if name != "" {
				a.report.RetainedFiles = append(a.report.RetainedFiles, filepath.Join(targetedParentPath(f.plan.parts), name))
			}
		}
	}
	for _, plan := range a.proposal.dirs {
		d := a.dirs[plan.path]
		if d.owned && err != nil {
			a.report.LeftoverDirectories = append(a.report.LeftoverDirectories, filepath.Join(targetedParentPath(plan.parts), d.name))
		}
	}
	switch {
	case err == nil:
		a.report.Status = "complete"
	case len(a.report.Unresolved) != 0 || len(a.report.RetainedFiles) != 0 || len(a.report.LeftoverDirectories) != 0:
		a.report.Status = "partial"
	case a.started:
		a.report.Status = "recovered"
	default:
		a.report.Status = "unchanged"
	}
	if err != nil {
		a.report.Detail = err.Error() + "; retained paths are relative to their original parents; a moved ancestor can make their current location unknown. Do not replay this proposal."
	}
}

func (a *targetedApply) summary() string {
	data, _ := json.MarshalIndent(a.report, "", "  ")
	return "Edit apply result (not crash-atomic):\n" + string(data)
}

func (a *targetedApply) close() {
	for _, dir := range a.dirs {
		if dir.file != nil {
			dir.file.Close()
		}
	}
}
