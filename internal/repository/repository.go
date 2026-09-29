package repository

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	MaxReadBytes     = 1 << 20
	MaxSearchBytes   = 8 << 20
	MaxSearchFiles   = 5000
	MaxSearchDirs    = 5000
	MaxSearchDepth   = 64
	MaxSearchResults = 100
	MaxListEntries   = 1000
	MaxQueryBytes    = 4096
)

var (
	ErrInvalidPath = errors.New("path must be repository-relative and contain no parent traversal")
	ErrSymlink     = errors.New("symlink paths are not allowed")
	ErrTooLarge    = errors.New("file exceeds size limit")
	ErrLimit       = errors.New("repository result limit exceeded")
	ErrBinary      = errors.New("binary file is not readable as text")
)

type Repository struct {
	root     string
	identity os.FileInfo
}

type Match struct {
	Path string
	Line int
	Text string
}

// New pins the identity of the canonical repository directory. Each operation
// opens it afresh and refuses a replaced root, then walks beneath it by file
// descriptor so a concurrent symlink substitution cannot escape the directory.
func New(root string) (*Repository, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("repository path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("repository path: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, fmt.Errorf("repository path: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("repository path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repository %q is not a directory", root)
	}
	return &Repository{root: canonical, identity: info}, nil
}

func components(path string) ([]string, error) {
	if path == "" || filepath.IsAbs(path) || strings.IndexByte(path, 0) >= 0 {
		return nil, fmt.Errorf("%q: %w", path, ErrInvalidPath)
	}
	parts := strings.Split(path, string(os.PathSeparator))
	for _, part := range parts {
		if part == ".." {
			return nil, fmt.Errorf("%q: %w", path, ErrInvalidPath)
		}
	}
	clean := filepath.Clean(path)
	if clean == "." {
		return nil, nil
	}
	return strings.Split(clean, string(os.PathSeparator)), nil
}

func (r *Repository) openRoot() (*os.File, error) {
	fd, err := unix.Open(r.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	root := os.NewFile(uintptr(fd), r.root)
	info, err := root.Stat()
	if err != nil || !os.SameFile(info, r.identity) {
		root.Close()
		if err != nil {
			return nil, fmt.Errorf("stat repository: %w", err)
		}
		return nil, errors.New("repository directory was replaced")
	}
	return root, nil
}

func openChild(parent *os.File, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			var info unix.Stat_t
			if unix.Fstatat(int(parent.Fd()), name, &info, unix.AT_SYMLINK_NOFOLLOW) == nil && info.Mode&unix.S_IFMT == unix.S_IFLNK {
				return nil, fmt.Errorf("open %q: %w", name, ErrSymlink)
			}
		}
		return nil, fmt.Errorf("open %q: %w", name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (r *Repository) open(path string, directory bool) (*os.File, error) {
	parts, err := components(path)
	if err != nil {
		return nil, err
	}
	current, err := r.openRoot()
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		if directory {
			return current, nil
		}
		current.Close()
		return nil, fmt.Errorf("%q is a directory, not a file", path)
	}
	for i, part := range parts {
		next, err := openChild(current, part, directory || i != len(parts)-1)
		current.Close()
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		current = next
	}
	return current, nil
}

// List returns the immediate children's repository-relative paths in sorted order.
func (r *Repository) List(path string) ([]string, error) {
	dir, err := r.open(path, true)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(MaxListEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("list %q: %w", path, err)
	}
	if len(names) > MaxListEntries {
		return nil, fmt.Errorf("list %q: more than %d entries: %w", path, MaxListEntries, ErrLimit)
	}
	sort.Strings(names)
	base := filepath.Clean(path)
	for i, name := range names {
		if base == "." {
			names[i] = name
		} else {
			names[i] = filepath.Join(base, name)
		}
	}
	return names, nil
}

func readText(file *os.File, path string, max int64) (string, int64, error) {
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
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return "", int64(len(data)), fmt.Errorf("read %q: %w", path, err)
	}
	if int64(len(data)) > max {
		return "", int64(len(data)), fmt.Errorf("%q: %w (maximum %d bytes)", path, ErrTooLarge, max)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", int64(len(data)), fmt.Errorf("%q: %w", path, ErrBinary)
	}
	return string(data), int64(len(data)), nil
}

// Read returns at most MaxReadBytes of UTF-8 text from a regular file.
func (r *Repository) Read(path string) (string, error) {
	file, err := r.open(path, false)
	if err != nil {
		return "", err
	}
	defer file.Close()
	text, _, err := readText(file, path, MaxReadBytes)
	return text, err
}

// Search performs a literal, case-sensitive search over regular UTF-8 files.
// Symlinks and .git entries are not traversed. Limits fail explicitly rather
// than returning an incomplete result set.
func (r *Repository) Search(query string) ([]Match, error) {
	if query == "" || len(query) > MaxQueryBytes {
		return nil, fmt.Errorf("search query must contain 1 to %d bytes", MaxQueryBytes)
	}
	root, err := r.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var matches []Match
	var files, dirs int
	var bytesRead int64
	var walk func(*os.File, string, int) error
	walk = func(dir *os.File, prefix string, depth int) error {
		dirs++
		if dirs > MaxSearchDirs || depth > MaxSearchDepth {
			return fmt.Errorf("search directory %q: directory count or depth limit exceeded: %w", prefix, ErrLimit)
		}
		entries, err := dir.ReadDir(MaxListEntries + 1)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("search directory %q: %w", prefix, err)
		}
		if len(entries) > MaxListEntries {
			return fmt.Errorf("search directory %q: more than %d entries: %w", prefix, MaxListEntries, ErrLimit)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			name := entry.Name()
			if name == ".git" || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(prefix, name)
			if entry.IsDir() {
				child, err := openChild(dir, name, true)
				if err != nil {
					return fmt.Errorf("search %q: %w", path, err)
				}
				err = walk(child, path, depth+1)
				child.Close()
				if err != nil {
					return err
				}
				continue
			}
			if !entry.Type().IsRegular() && entry.Type() != 0 {
				continue
			}
			files++
			if files > MaxSearchFiles {
				return fmt.Errorf("search: more than %d files: %w", MaxSearchFiles, ErrLimit)
			}
			file, err := openChild(dir, name, false)
			if err != nil {
				return fmt.Errorf("search %q: %w", path, err)
			}
			text, size, err := readText(file, path, MaxReadBytes)
			file.Close()
			bytesRead += size
			if bytesRead > MaxSearchBytes {
				return fmt.Errorf("search: more than %d bytes: %w", MaxSearchBytes, ErrLimit)
			}
			if errors.Is(err, ErrBinary) {
				continue
			}
			if err != nil {
				return err
			}
			for line, remaining := 1, text; len(remaining) > 0; line++ {
				content, rest, hasNext := strings.Cut(remaining, "\n")
				if strings.Contains(content, query) {
					if len(matches) == MaxSearchResults {
						return fmt.Errorf("search: more than %d matches: %w", MaxSearchResults, ErrLimit)
					}
					matches = append(matches, Match{Path: path, Line: line, Text: strings.TrimSuffix(content, "\r")})
				}
				if !hasNext {
					break
				}
				remaining = rest
			}
		}
		return nil
	}
	if err := walk(root, "", 0); err != nil {
		return nil, err
	}
	return matches, nil
}
