package tooloutput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type artifactMetadata struct {
	Version      int
	ID           string
	SessionID    string
	CallHash     string
	SourceBytes  int64
	PlannedBytes int64
	Bytes        int64
	Complete     bool
	Truncated    bool
	Warning      string
}

// Resolve trusted pre-existing ancestors once (including macOS /var), but
// refuse a symlink at the harness-owned base itself. Subsequent operations
// walk the canonical path with openat/O_NOFOLLOW and pin directory identities.
func resolveBase(base string) (string, error) {
	if base == "" {
		return "", errors.New("tool-output base directory is empty")
	}
	absolute, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("tool-output base directory: %w", err)
	}
	if absolute == string(filepath.Separator) {
		return "", errors.New("tool-output base cannot be the filesystem root")
	}
	if info, err := os.Lstat(absolute); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("tool-output base is not a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect tool-output base: %w", err)
	}
	parent := filepath.Dir(absolute)
	suffix := []string{filepath.Base(absolute)}
	for {
		if _, err := os.Lstat(parent); err == nil {
			canonical, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", fmt.Errorf("resolve tool-output ancestors: %w", err)
			}
			return filepath.Join(append([]string{canonical}, suffix...)...), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect tool-output ancestor: %w", err)
		}
		suffix = append([]string{filepath.Base(parent)}, suffix...)
		parent = filepath.Dir(parent)
	}
}

func (s *Store) openDirectory(create bool) (*os.File, []os.FileInfo, error) {
	baseParts := strings.Split(strings.TrimPrefix(s.base, string(filepath.Separator)), string(filepath.Separator))
	parts := append(baseParts, "tool-output", s.sessionID)
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open tool-output ancestor: %w", err)
	}
	current := os.NewFile(uintptr(fd), "/")
	identities := make([]os.FileInfo, 0, len(parts)+1)
	for i := 0; ; i++ {
		private := i >= len(baseParts)
		info, err := current.Stat()
		if err == nil && s.dirs != nil && (i >= len(s.dirs) || !os.SameFile(info, s.dirs[i])) {
			err = errors.New("tool-output directory or ancestor was replaced")
		}
		if err == nil {
			info, err = checkDirectory(current, private)
		}
		if err != nil {
			current.Close()
			return nil, nil, err
		}
		identities = append(identities, info)
		if i == len(parts) {
			return current, identities, nil
		}
		name := parts[i]
		fd, err := unix.Openat(int(current.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && create {
			mkdirErr := unix.Mkdirat(int(current.Fd()), name, 0700)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				current.Close()
				return nil, nil, fmt.Errorf("create tool-output directory %q: %w", name, mkdirErr)
			}
			fd, err = unix.Openat(int(current.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err == nil {
				if syncErr := current.Sync(); syncErr != nil {
					unix.Close(fd)
					current.Close()
					return nil, nil, fmt.Errorf("persist tool-output ancestor: %w", syncErr)
				}
			}
		}
		current.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("open tool-output directory %q (no symlinks): %w", name, err)
		}
		current = os.NewFile(uintptr(fd), name)
	}
}

func checkDirectory(file *os.File, private bool) (os.FileInfo, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nil, fmt.Errorf("stat tool-output directory: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, errors.New("tool-output ancestor is not a directory")
	}
	owner := uint32(os.Geteuid())
	if private {
		if stat.Uid != owner {
			return nil, errors.New("private tool-output directory belongs to another user")
		}
		if err := unix.Fchmod(int(file.Fd()), 0700); err != nil {
			return nil, fmt.Errorf("make tool-output directory private: %w", err)
		}
	} else {
		if stat.Uid != 0 && stat.Uid != owner {
			return nil, errors.New("tool-output ancestor belongs to an untrusted user")
		}
		// Sticky shared temp ancestors are acceptable; writable non-sticky
		// ancestors could let another user replace a harness directory.
		if stat.Mode&0022 != 0 && stat.Mode&unix.S_ISVTX == 0 {
			return nil, errors.New("tool-output ancestor is writable by other users")
		}
	}
	return file.Stat()
}

func openPrivateFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	err = unix.Fstat(fd, &stat)
	if err == nil && (stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid())) {
		err = errors.New("tool-output file must be regular, owned by this user, and not hard-linked")
	}
	if err == nil {
		err = unix.Fchmod(fd, 0600)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

type lockedDirectory struct {
	dir  *os.File
	lock *os.File
	held bool
}

func (s *Store) lock(exclusive bool) (*lockedDirectory, error) {
	if s == nil || !validID(s.sessionID) || s.dirs == nil || s.lockInfo == nil {
		return nil, errors.New("tool-output store is not open")
	}
	dir, _, err := s.openDirectory(false)
	if err != nil {
		return nil, err
	}
	file, err := openPrivateFile(dir, ".lock", unix.O_RDWR)
	if err != nil {
		dir.Close()
		return nil, fmt.Errorf("open existing tool-output lock: %w", err)
	}
	locked := &lockedDirectory{dir: dir, lock: file}
	info, err := file.Stat()
	if err == nil && (!os.SameFile(info, s.lockInfo) || info.Size() != 0) {
		err = errors.New("tool-output lock was replaced or modified")
	}
	if err == nil {
		mode := unix.LOCK_SH
		if exclusive {
			mode = unix.LOCK_EX
		}
		err = unix.Flock(int(file.Fd()), mode)
	}
	if err != nil {
		locked.close()
		return nil, fmt.Errorf("lock tool-output session: %w", err)
	}
	locked.held = true
	return locked, nil
}

func (l *lockedDirectory) close() error {
	var err error
	if l.held {
		err = unix.Flock(int(l.lock.Fd()), unix.LOCK_UN)
	}
	err = errors.Join(err, l.lock.Close(), l.dir.Close())
	if err != nil {
		return fmt.Errorf("release tool-output session: %w", err)
	}
	return nil
}

func (s *Store) account(dir *os.File, callHash string) (total, forCall int64, err error) {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, 0, fmt.Errorf("list retained output: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		id, ok := artifactID(entry.Name())
		if !ok {
			return 0, 0, fmt.Errorf("unexpected file %q in tool-output session", entry.Name())
		}
		file, err := openPrivateFile(dir, entry.Name(), unix.O_RDONLY)
		if err != nil {
			return 0, 0, fmt.Errorf("account retained output %s: %w", id, err)
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return 0, 0, fmt.Errorf("stat retained output %s: %w", id, statErr)
		}
		count := max(int64(0), info.Size()-headerBytes)
		total = saturatedAdd(total, count)
		if callHash != "" {
			meta, metaErr := s.readHeader(file, id)
			// Damaged/unfinished headers cannot reset accounting. Charge an
			// unidentifiable artifact to every call until explicitly cleared.
			if metaErr != nil || meta.CallHash == callHash {
				forCall = saturatedAdd(forCall, count)
			}
		}
		if err := file.Close(); err != nil {
			return 0, 0, fmt.Errorf("close accounted output %s: %w", id, err)
		}
	}
	return total, forCall, nil
}

func saturatedAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

func writeHeader(file *os.File, meta artifactMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode output reference: %w", err)
	}
	start := len(headerMagic) + sha256.Size
	if len(data) > headerBytes-start {
		return errors.New("output reference exceeds its private header allowance")
	}
	header := bytes.Repeat([]byte{' '}, headerBytes)
	copy(header, headerMagic)
	copy(header[start:], data)
	digest := sha256.Sum256(header[start:])
	copy(header[len(headerMagic):start], digest[:])
	n, err := file.WriteAt(header, 0)
	if n != len(header) && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("write output reference: %w", err)
	}
	return nil
}

func (s *Store) readHeader(file *os.File, id string) (artifactMetadata, error) {
	var meta artifactMetadata
	header := make([]byte, headerBytes)
	if _, err := file.ReadAt(header, 0); err != nil {
		return meta, fmt.Errorf("read output reference %s: %w", id, err)
	}
	start := len(headerMagic) + sha256.Size
	digest := sha256.Sum256(header[start:])
	if string(header[:len(headerMagic)]) != headerMagic || !bytes.Equal(header[len(headerMagic):start], digest[:]) {
		return meta, errors.New("retained output has an incomplete or corrupt reference header")
	}
	if err := json.Unmarshal(header[start:], &meta); err != nil {
		return meta, fmt.Errorf("decode retained output reference: %w", err)
	}
	hash, hashErr := hex.DecodeString(meta.CallHash)
	if meta.Version != 1 || meta.ID != id || meta.SessionID != s.sessionID || hashErr != nil || len(hash) != sha256.Size ||
		meta.SourceBytes < 0 || meta.PlannedBytes < 0 || meta.PlannedBytes > callBytes || meta.Bytes < 0 || meta.Bytes > callBytes || len(meta.Warning) > 1024 {
		return meta, errors.New("retained output reference has invalid metadata or belongs to another session")
	}
	return meta, nil
}
