package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	gitignore "github.com/sabhiram/go-gitignore"
)

var errInspectionLimit = errors.New("inspection scan limited")

// All state is per operation; independent readers share no mutable walk state.
type inspectionState struct {
	warnings        []string
	suppressed      int
	incomplete      bool
	warnUnsupported bool
	dirs            int
	files           int
	bytesRead       int64
}

func (s *inspectionState) warn(format string, args ...any) {
	if len(s.warnings) == 8 {
		s.suppressed++
		return
	}
	text := strings.ToValidUTF8(fmt.Sprintf(format, args...), "�")
	clipped := inspectionClip(text, 240)
	if len(clipped) != len(text) {
		clipped += "…"
	}
	s.warnings = append(s.warnings, clipped)
}

func (s *inspectionState) skip(format string, args ...any) {
	s.incomplete = true
	s.warn(format, args...)
}

func (s *inspectionState) limit(format string, args ...any) error {
	s.skip(format, args...)
	s.warn("scan limited; narrow path/include or lower result volume; no continuation cursor is available")
	return errInspectionLimit
}

func inspectionPath(path string, defaultRoot bool) (string, error) {
	if path == "" && defaultRoot {
		path = "."
	}
	if len(path) > MaxQueryBytes || strings.Contains(path, "://") || !utf8.ValidString(path) {
		return "", ErrInvalidPath
	}
	parts, err := components(path)
	if err != nil {
		return "", err
	}
	for _, part := range parts {
		if strings.EqualFold(part, ".git") {
			return "", errors.New(".git internals are excluded from repository inspection")
		}
	}
	if len(parts) > MaxSearchDepth {
		return "", fmt.Errorf("scope exceeds %d path components: %w", MaxSearchDepth, ErrLimit)
	}
	return filepath.Clean(path), nil
}

func inspectionPrefix(path string) string {
	if path == "." {
		return ""
	}
	return path
}

// inspectionScope descends only the requested path, carrying ancestor ignores.
// Unlike discovery, a missing/unsafe requested scope is always a hard error.
func (r *Repository) inspectionScope(ctx context.Context, path string, opts QueryOptions, state *inspectionState) (*os.File, []*dirIgnore, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	parts, err := components(path)
	if err != nil {
		return nil, nil, false, err
	}
	current, err := r.openRoot()
	if err != nil {
		return nil, nil, false, err
	}
	var ignores []*dirIgnore
	prefix, visible := "", true
	for i, part := range parts {
		if err := ctx.Err(); err != nil {
			current.Close()
			return nil, nil, false, err
		}
		if !opts.IncludeIgnored {
			ignores, err = state.loadIgnore(ctx, current, prefix, ignores)
			if err != nil {
				current.Close()
				return nil, nil, false, err
			}
		}
		next, err := openChild(current, part, i != len(parts)-1)
		current.Close()
		if err != nil {
			return nil, nil, false, fmt.Errorf("scope %q: %w", path, err)
		}
		current = next
		info, err := current.Stat()
		if err != nil {
			current.Close()
			return nil, nil, false, fmt.Errorf("scope %q: %w", path, err)
		}
		prefix = filepath.Join(prefix, part)
		visible = visible && inspectionVisible(part, prefix, info.IsDir(), ignores, opts)
	}
	return current, ignores, visible, nil
}

func (s *inspectionState) loadIgnore(ctx context.Context, dir *os.File, prefix string, ignores []*dirIgnore) ([]*dirIgnore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openChild(dir, ".gitignore", false)
	if err != nil {
		if inspectionSafetyError(err) {
			return nil, err
		}
		if !errors.Is(err, os.ErrNotExist) {
			s.skip("could not load ignore rules in %q: %v", prefix, err)
		}
		return ignores, nil
	}
	defer file.Close()
	text, _, err := inspectionReadText(ctx, file, filepath.Join(prefix, ".gitignore"), 1<<16)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.skip("could not load ignore rules in %q: %v", prefix, err)
		return ignores, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Reuse Tree's scoped rule representation and matching semantics.
	rule := gitignore.CompileIgnoreLines(strings.Split(text, "\n")...)
	return append(append([]*dirIgnore(nil), ignores...), &dirIgnore{dir: prefix, rule: rule}), nil
}

func inspectionVisible(name, path string, directory bool, ignores []*dirIgnore, opts QueryOptions) bool {
	if strings.EqualFold(name, ".git") {
		return false
	}
	if !opts.IncludeHidden && strings.HasPrefix(name, ".") {
		return false
	}
	if !opts.IncludeIgnored {
		if name == "node_modules" {
			return false
		}
		if directory {
			path += "/"
		}
		if ignored(ignores, path) {
			return false
		}
	}
	return true
}

func inspectionIncluded(include []string, path string) bool {
	return len(include) == 0 || globMatch(include, strings.Split(path, string(os.PathSeparator)))
}

func inspectionSafetyError(err error) bool {
	return errors.Is(err, ErrSymlink) || errors.Is(err, ErrInvalidPath)
}

// inspectionEntries reads in cancellable batches and never retains more than
// the legacy per-directory cap plus one entry used to detect incompleteness.
func inspectionEntries(ctx context.Context, dir *os.File) ([]os.DirEntry, bool, error) {
	var entries []os.DirEntry
	for len(entries) <= MaxListEntries {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		count := MaxListEntries + 1 - len(entries)
		if count > 64 {
			count = 64
		}
		batch, err := dir.ReadDir(count)
		entries = append(entries, batch...)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, false, err
			}
			break
		}
	}
	bounded := len(entries) > MaxListEntries
	if bounded {
		entries = entries[:MaxListEntries]
	}
	// Include the directory separator in ordering so depth-first file visitation
	// is globally path-sorted, e.g. a.go is visited before a/child.go.
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i].Name(), entries[j].Name()
		if entries[i].IsDir() {
			left += "/"
		}
		if entries[j].IsDir() {
			right += "/"
		}
		return left < right
	})
	return entries, bounded, nil
}

func (s *inspectionState) walk(ctx context.Context, dir *os.File, prefix string, ignores []*dirIgnore, opts QueryOptions, include []string, visit func(*os.File, string, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.dirs++
	depth := 0
	if prefix != "" {
		depth = len(strings.Split(prefix, string(os.PathSeparator)))
	}
	if s.dirs > MaxSearchDirs || depth > MaxSearchDepth {
		return s.limit("walk reached its %d-directory or %d-depth cap", MaxSearchDirs, MaxSearchDepth)
	}
	var err error
	if !opts.IncludeIgnored {
		ignores, err = s.loadIgnore(ctx, dir, prefix, ignores)
		if err != nil {
			return err
		}
	}
	entries, bounded, err := inspectionEntries(ctx, dir)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.skip("cannot list directory %q: %v", prefix, err)
		return nil
	}
	if bounded {
		s.skip("directory %q exceeds %d entries; only a bounded observed subset was scanned", prefix, MaxListEntries)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(prefix, name)
		if !inspectionVisible(name, path, entry.IsDir(), ignores, opts) {
			continue
		}
		if !utf8.ValidString(path) {
			s.skip("skipped an entry with a non-UTF-8 path in %q", prefix)
			continue
		}
		if entry.IsDir() {
			child, err := openChild(dir, name, true)
			if err != nil {
				if inspectionSafetyError(err) {
					return err
				}
				s.skip("cannot open directory %q: %v", path, err)
				continue
			}
			err = s.walk(ctx, child, path, ignores, opts, include, visit)
			child.Close()
			if err != nil {
				return err
			}
			continue
		}
		if !inspectionIncluded(include, path) {
			continue
		}
		if !entry.Type().IsRegular() {
			if s.warnUnsupported {
				s.skip("skipped unsupported non-regular entry %q", path)
			}
			continue
		}
		s.files++
		if s.files > MaxSearchFiles {
			return s.limit("walk reached its %d-file cap", MaxSearchFiles)
		}
		if err := visit(dir, name, path); err != nil {
			return err
		}
	}
	if bounded {
		return s.limit("walk reached a %d-entry directory cap", MaxListEntries)
	}
	return nil
}

// inspectionReadText retains the legacy regular-file, UTF-8, NUL and size
// checks, adding cancellation between bounded reads. O_NONBLOCK from openChild
// plus the regular-file check prevents a special file from blocking the scan.
func inspectionReadText(ctx context.Context, file *os.File, path string, max int64) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%q is not a regular file", path)
	}
	if info.Size() > max {
		return "", 0, fmt.Errorf("%q: %w (maximum %d bytes)", path, ErrTooLarge, max)
	}
	var data []byte
	reader := io.LimitReader(file, max+1)
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", int64(len(data)), err
		}
		n, err := reader.Read(buffer)
		data = append(data, buffer[:n]...)
		if int64(len(data)) > max {
			return "", int64(len(data)), fmt.Errorf("%q: %w (maximum %d bytes)", path, ErrTooLarge, max)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return "", int64(len(data)), fmt.Errorf("read %q: %w", path, err)
			}
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return "", int64(len(data)), err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", int64(len(data)), fmt.Errorf("%q: %w", path, ErrBinary)
	}
	return string(data), int64(len(data)), nil
}

// Reserve enough space for Page metadata and warnings, and charge JSON escape
// bytes as well as raw bytes so control-heavy text cannot defeat the inline cap.
type inspectionBuffer struct {
	strings.Builder
	used, budget int
}

func newInspectionBuffer(budget int) *inspectionBuffer {
	return &inspectionBuffer{budget: budget}
}

func (b *inspectionBuffer) remaining() int { return b.budget - b.used }

func (b *inspectionBuffer) add(text string) bool {
	cost := inspectionEncodedBytes(text)
	if cost > b.remaining() {
		return false
	}
	b.WriteString(text)
	b.used += cost
	return true
}

func inspectionEncodedBytes(text string) int {
	cost := 0
	for _, char := range text {
		cost += inspectionRuneBytes(char)
	}
	return cost
}

func inspectionRuneBytes(char rune) int {
	switch char {
	case '\\', '"', '\n', '\r', '\t', '\b', '\f':
		return 2
	case '<', '>', '&', '\u2028', '\u2029':
		return 6
	}
	if char < 0x20 {
		return 6
	}
	return utf8.RuneLen(char)
}

func inspectionClip(text string, budget int) string {
	used := 0
	for i, char := range text {
		used += inspectionRuneBytes(char)
		if used > budget {
			return text[:i]
		}
	}
	return text
}

func inspectionLabel(text string) string {
	clipped := inspectionClip(text, 160)
	if len(clipped) != len(text) {
		clipped += "…"
	}
	return fmt.Sprintf("%q", clipped)
}
