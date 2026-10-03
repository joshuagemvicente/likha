package tooloutput

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// RemoveSession deletes <base>/tool-output/<sessionID> and everything in it,
// for session deletion. A missing directory (never opened, or already removed)
// is success. The walk reuses Open's no-symlink, owner-checked path and holds
// the session's exclusive lock while unlinking, so it never follows a link out
// of the base. Unlike Clear, unexpected entries are removed too, since the
// whole session is going away; nested directories are walked without
// following links. Failures are reported, never hidden.
func RemoveSession(base, sessionID string) (err error) {
	if !validID(sessionID) {
		return errors.New("invalid tool-output session ID")
	}
	canonical, err := resolveBase(base)
	if err != nil {
		return err
	}
	target := filepath.Join(canonical, "tool-output", sessionID)
	if rel, relErr := filepath.Rel(canonical, target); relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("tool-output session directory %q escapes its base", target)
	}
	s := &Store{base: canonical, sessionID: sessionID}
	dir, identities, err := s.openDirectory(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open tool-output session for removal: %w", err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()

	// Pin the parent: ".." of the opened directory must be the tool-output
	// directory walked above, and its sessionID entry must still be this one.
	fd, err := unix.Openat(int(dir.Fd()), "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open tool-output parent for removal: %w", err)
	}
	parent := os.NewFile(uintptr(fd), "tool-output")
	defer func() { err = errors.Join(err, parent.Close()) }()
	parentInfo, err := parent.Stat()
	if err != nil {
		return fmt.Errorf("stat tool-output parent: %w", err)
	}
	if !os.SameFile(parentInfo, identities[len(identities)-2]) {
		return errors.New("tool-output session directory was moved during removal")
	}

	lock, err := openPrivateFile(dir, ".lock", unix.O_RDWR)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("open tool-output lock for removal: %w", err)
	}
	if lock != nil {
		defer func() { err = errors.Join(err, lock.Close()) }()
		if lockErr := unix.Flock(int(lock.Fd()), unix.LOCK_EX); lockErr != nil {
			return fmt.Errorf("lock tool-output session for removal: %w", lockErr)
		}
		// The lock stays held until the deferred close, after the rmdir below.
		// A store that recreates .lock meanwhile makes the rmdir fail visibly.
	}

	if err := removeContents(dir); err != nil {
		return fmt.Errorf("remove tool-output session %s: %w", sessionID, err)
	}
	if err := sameEntry(parent, sessionID, identities[len(identities)-1]); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(parent.Fd()), sessionID, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("remove tool-output session directory %s: %w", sessionID, err)
	}
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("persist tool-output session removal: %w", err)
	}
	return nil
}

// sameEntry confirms that name in parent is still the directory identified
// by want, so the final rmdir cannot hit a swapped-in replacement.
func sameEntry(parent *os.File, name string, want os.FileInfo) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("verify tool-output session before removal: %w", err)
	}
	entry := os.NewFile(uintptr(fd), name)
	defer entry.Close()
	info, err := entry.Stat()
	if err != nil {
		return fmt.Errorf("stat tool-output session before removal: %w", err)
	}
	if !os.SameFile(info, want) {
		return errors.New("tool-output session directory was replaced during removal")
	}
	return nil
}

// removeContents unlinks every entry under dir without following links.
// Unlinkat removes a symlink itself, never its target; subdirectories are
// opened with O_NOFOLLOW, emptied, then removed. It keeps going after a
// failure so one stuck entry does not hide the rest, and joins all errors.
func removeContents(dir *os.File) error {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("list retained output for removal: %w", err)
	}
	var errs error
	for _, entry := range entries {
		name := entry.Name()
		removeErr := unix.Unlinkat(int(dir.Fd()), name, 0)
		if errors.Is(removeErr, unix.EISDIR) || errors.Is(removeErr, unix.EPERM) && entry.IsDir() {
			removeErr = removeSubdirectory(dir, name)
		}
		if removeErr != nil && !errors.Is(removeErr, unix.ENOENT) {
			errs = errors.Join(errs, fmt.Errorf("remove %q: %w", name, removeErr))
		}
	}
	if syncErr := dir.Sync(); syncErr != nil {
		errs = errors.Join(errs, fmt.Errorf("persist retained output removal: %w", syncErr))
	}
	return errs
}

func removeSubdirectory(dir *os.File, name string) error {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	child := os.NewFile(uintptr(fd), name)
	err = removeContents(child)
	err = errors.Join(err, child.Close())
	if err != nil {
		return err
	}
	return unix.Unlinkat(int(dir.Fd()), name, unix.AT_REMOVEDIR)
}
