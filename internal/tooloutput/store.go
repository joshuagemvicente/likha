// Package tooloutput retains bounded tool results in private, session-owned
// files. Source output can contain secrets: private storage is not redaction.
// Retention never contacts a provider or injects output into model context;
// callers must disclose storage and send only explicitly requested pages.
package tooloutput

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	callBytes      int64 = 5 << 20
	sessionBytes   int64 = 50 << 20
	headerBytes          = 4096
	headerMagic          = "LIKHAO1\n"
	artifactSuffix       = ".output"
)

// Reference describes the bytes actually retained, even when Retain also
// returns an error. An empty ID means no readable artifact was created.
type Reference struct {
	ID        string
	Truncated bool
	Bytes     int64
	Warning   string
}

// Page contains numbered UTF-8 lines. NextOffset is one-based, or zero at the
// end of retained output. Truncated also covers incomplete source retention.
type Page struct {
	Content    string
	NextOffset int
	Truncated  bool
	Warnings   []string
}

// Store owns one session's output. Operations open and close their own file
// descriptors and serialize with other stores/processes using a private lock.
// No Close method or session-database integration is required.
type Store struct {
	base      string
	sessionID string
	dirs      []os.FileInfo
	lockInfo  os.FileInfo
}

// Open creates <base>/tool-output/<sessionID> with mode 0700. Session IDs use
// the existing session package's 32-character lowercase hexadecimal format.
// Existing artifacts are accounted from disk, not an in-memory allowance.
func Open(base string, sessionID string) (_ *Store, err error) {
	if !validID(sessionID) {
		return nil, errors.New("invalid tool-output session ID")
	}
	canonical, err := resolveBase(base)
	if err != nil {
		return nil, err
	}
	s := &Store{base: canonical, sessionID: sessionID}
	dir, identities, err := s.openDirectory(true)
	if err != nil {
		return nil, err
	}
	s.dirs = identities
	lock, err := openPrivateFile(dir, ".lock", unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		dir.Close()
		return nil, fmt.Errorf("open tool-output lock: %w", err)
	}
	locked := &lockedDirectory{dir: dir, lock: lock}
	defer func() {
		if closeErr := locked.close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	s.lockInfo, err = lock.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat tool-output lock: %w", err)
	}
	if s.lockInfo.Size() != 0 {
		return nil, errors.New("tool-output lock is not empty")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock tool-output session: %w", err)
	}
	locked.held = true
	if _, _, err := s.account(dir, ""); err != nil {
		return nil, err
	}
	if err := dir.Sync(); err != nil {
		return nil, fmt.Errorf("persist tool-output directory: %w", err)
	}
	return s, nil
}

// Retain captures a UTF-8 prefix within the five MiB call and 50 MiB session
// allowances. Reusing a call ID shares that call's allowance across artifacts.
// A cap or disk failure returns both a partial Reference and an error. Older
// output is never evicted. This API cannot infer an upstream interruption;
// the caller must preserve execution/interruption metadata with the reference.
func (s *Store) Retain(callID, content string) (ref Reference, err error) {
	locked, err := s.lock(true)
	if err != nil {
		return failedReference(ref, err)
	}
	defer func() {
		if closeErr := locked.close(); closeErr != nil {
			ref, err = failedReference(ref, errors.Join(err, closeErr))
		}
	}()
	digest := sha256.Sum256([]byte(callID))
	callHash := hex.EncodeToString(digest[:])
	used, usedByCall, err := s.account(locked.dir, callHash)
	if err != nil {
		return failedReference(ref, err)
	}
	allowance := max(int64(0), min(callBytes-usedByCall, sessionBytes-used))
	text, consumed, replaced := boundedText(content, allowance)
	ref.Truncated = consumed < int64(len(content))
	if ref.Truncated {
		ref.Warning = fmt.Sprintf("retention limited to 5 MiB per call and 50 MiB per session; %d source bytes discarded; clear output explicitly to reclaim space", int64(len(content))-consumed)
		err = errors.New(ref.Warning)
	}
	if replaced {
		ref.Warning = joinWarning(ref.Warning, "invalid UTF-8 was replaced in retained output")
	}
	if text == "" && content != "" {
		return ref, err
	}
	id, file, createErr := createArtifact(locked.dir)
	if createErr != nil {
		return failedReference(ref, errors.Join(err, createErr))
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			ref, err = failedReference(ref, errors.Join(err, fmt.Errorf("close retained output: %w", closeErr)))
		}
	}()
	meta := artifactMetadata{
		Version: 1, ID: id, SessionID: s.sessionID, CallHash: callHash,
		SourceBytes: int64(len(content)), PlannedBytes: int64(len(text)),
		Truncated: ref.Truncated, Warning: ref.Warning,
	}
	// Publish an incomplete header before writing any content. An interrupted
	// write remains identifiable and charged against the allowance on reopen.
	if writeErr := writeHeader(file, meta); writeErr != nil {
		removeErr := unix.Unlinkat(int(locked.dir.Fd()), id+artifactSuffix, 0)
		if removeErr != nil {
			ref.ID = id
		}
		return failedReference(ref, errors.Join(err, writeErr, removeErr))
	}
	ref.ID = id
	if syncErr := errors.Join(file.Sync(), locked.dir.Sync()); syncErr != nil {
		return failedReference(ref, errors.Join(err, fmt.Errorf("persist incomplete output reference: %w", syncErr)))
	}
	n, writeErr := file.WriteAt([]byte(text), headerBytes)
	if n != len(text) && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	// A partial write can stop inside a rune. Shrink only this new artifact;
	// never alter an older result to make room.
	safe := n
	for safe > 0 && safe < len(text) && !utf8.RuneStart(text[safe]) {
		safe--
	}
	if safe != n {
		if truncateErr := file.Truncate(int64(headerBytes + safe)); truncateErr != nil {
			writeErr = errors.Join(writeErr, fmt.Errorf("trim incomplete UTF-8 output: %w", truncateErr))
		} else {
			n = safe
		}
	}
	ref.Bytes = int64(n)
	if syncErr := file.Sync(); syncErr != nil {
		writeErr = errors.Join(writeErr, fmt.Errorf("persist retained output: %w", syncErr))
	}
	if writeErr != nil {
		return failedReference(ref, errors.Join(err, fmt.Errorf("retain output (%d bytes captured): %w", ref.Bytes, writeErr)))
	}
	meta.Bytes = ref.Bytes
	meta.Complete = true
	if finishErr := writeHeader(file, meta); finishErr != nil {
		return failedReference(ref, errors.Join(err, finishErr))
	}
	if syncErr := file.Sync(); syncErr != nil {
		// Best effort to leave the persistent completeness marker conservative.
		meta.Complete = false
		restoreErr := errors.Join(writeHeader(file, meta), file.Sync())
		return failedReference(ref, errors.Join(err, fmt.Errorf("persist completed output reference: %w", syncErr), restoreErr))
	}
	return ref, err
}

// Clear removes only artifacts in this store's session, leaving the private
// directory and lock reusable. It reports partial clearing/durability failures
// instead of pretending that all references are gone. It never follows links.
func (s *Store) Clear() (err error) {
	locked, err := s.lock(true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, locked.close()) }()
	entries, err := locked.dir.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("list retained output for clearing: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".lock" {
			continue
		}
		if _, ok := artifactID(name); !ok {
			err = errors.Join(err, fmt.Errorf("unexpected file %q in tool-output session; left untouched", name))
			continue
		}
		// Unlinkat removes the entry, not its target, even if a damaged artifact
		// was replaced with a symlink. Directories are refused, not recursed.
		if removeErr := unix.Unlinkat(int(locked.dir.Fd()), name, 0); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("clear output %s: %w", name, removeErr))
		}
	}
	if syncErr := locked.dir.Sync(); syncErr != nil {
		err = errors.Join(err, fmt.Errorf("persist cleared output: %w", syncErr))
	}
	return err
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func artifactID(name string) (string, bool) {
	id := strings.TrimSuffix(name, artifactSuffix)
	return id, strings.HasSuffix(name, artifactSuffix) && validID(id)
}

func createArtifact(dir *os.File) (string, *os.File, error) {
	for range 8 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, fmt.Errorf("generate output ID: %w", err)
		}
		id := hex.EncodeToString(random[:])
		file, err := openPrivateFile(dir, id+artifactSuffix, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("create retained output: %w", err)
		}
		return id, file, nil
	}
	return "", nil, errors.New("could not allocate a unique output ID")
}

func boundedText(content string, limit int64) (string, int64, bool) {
	var text strings.Builder
	text.Grow(int(min(int64(len(content)), limit)))
	consumed := 0
	replaced := false
	for consumed < len(content) {
		r, size := utf8.DecodeRuneInString(content[consumed:])
		width := size
		invalid := r == utf8.RuneError && size == 1
		if invalid {
			width = utf8.RuneLen(utf8.RuneError)
		}
		if int64(text.Len()+width) > limit {
			break
		}
		if invalid {
			text.WriteRune(utf8.RuneError)
			replaced = true
		} else {
			text.WriteString(content[consumed : consumed+size])
		}
		consumed += size
	}
	return text.String(), int64(consumed), replaced
}

func failedReference(ref Reference, err error) (Reference, error) {
	ref.Truncated = true
	if err != nil {
		ref.Warning = joinWarning(ref.Warning, err.Error())
	}
	return ref, err
}

func joinWarning(existing, warning string) string {
	if existing == "" {
		return warning
	}
	if warning == "" || existing == warning {
		return existing
	}
	return existing + "; " + warning
}
