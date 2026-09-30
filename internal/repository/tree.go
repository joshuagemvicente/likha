package repository

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

// Tree walks the repository (or a subfolder of it) and returns the
// repository-relative paths of every regular file and directory it contains,
// sorted, with directories carrying a trailing "/". It never returns hidden
// entries (dot-prefixed, including .git), node_modules, symlinked entries, or
// anything matched by a .gitignore file in the repository root or any
// traversed subdirectory. The walk stops at maxEntries; truncated reports
// whether more entries existed. It is the shared traversal behind the @
// completion popup and folder-reference expansion.
func (r *Repository) Tree(path string, maxEntries int) (entries []string, truncated bool, err error) {
	if maxEntries < 1 {
		return nil, false, fmt.Errorf("tree %q: maxEntries must be positive", path)
	}
	// The walk always starts at the repository root so the root .gitignore
	// (and every ancestor's) applies to the requested subtree too.
	root, err := r.openRoot()
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	base := filepath.Clean(path)
	if base == "." {
		base = ""
	}
	var out []string
	var walk func(dir *os.File, prefix string, ignores []*dirIgnore, depth int) error
	walk = func(dir *os.File, prefix string, ignores []*dirIgnore, depth int) error {
		if depth > MaxTreeDepth {
			return nil // too deep: skip the subtree rather than fail the whole listing
		}
		if rule := loadIgnore(dir); rule != nil {
			ignores = append(append([]*dirIgnore(nil), ignores...), &dirIgnore{dir: prefix, rule: rule})
		}
		entries0, err := dir.ReadDir(MaxListEntries + 1)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("tree directory %q: %w", prefix, err)
		}
		if len(entries0) > MaxListEntries {
			return fmt.Errorf("tree directory %q: %w", prefix, ErrLimit)
		}
		sort.Slice(entries0, func(i, j int) bool { return entries0[i].Name() < entries0[j].Name() })
		for _, entry := range entries0 {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			candidate := filepath.Join(prefix, name)
			// Dir-only .gitignore patterns (build/) match a path with a
			// trailing slash, so directories are matched with one.
			ignoredPath := candidate
			if entry.IsDir() {
				ignoredPath = candidate + "/"
			}
			if ignored(ignores, ignoredPath) {
				continue
			}
			if base != "" {
				// Outside the requested subtree: descend only along the
				// base's path components, never emit those levels.
				if candidate == base && entry.IsDir() {
					child, err := openChild(dir, name, true)
					if err != nil {
						return fmt.Errorf("tree %q: %w", candidate, err)
					}
					err = walk(child, candidate, ignores, depth+1)
					child.Close()
					return err
				}
				if strings.HasPrefix(base+"/", candidate+"/") {
					if !entry.IsDir() {
						continue
					}
					child, err := openChild(dir, name, true)
					if err != nil {
						return fmt.Errorf("tree %q: %w", candidate, err)
					}
					err = walk(child, candidate, ignores, depth+1)
					child.Close()
					if err != nil {
						return err
					}
					continue
				}
				if !strings.HasPrefix(candidate+"/", base+"/") {
					continue
				}
			}
			if entry.IsDir() {
				if len(out) >= maxEntries {
					truncated = true
					return nil
				}
				out = append(out, candidate+"/")
				child, err := openChild(dir, name, true)
				if err != nil {
					return fmt.Errorf("tree %q: %w", candidate, err)
				}
				err = walk(child, candidate, ignores, depth+1)
				child.Close()
				if err != nil {
					return err
				}
				continue
			}
			if !entry.Type().IsRegular() && entry.Type() != 0 {
				continue
			}
			if len(out) >= maxEntries {
				truncated = true
				return nil
			}
			out = append(out, candidate)
		}
		return nil
	}
	if err := walk(root, "", nil, 0); err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}

// MaxTreeDepth bounds how deep a tree walk descends before it silently stops.
const MaxTreeDepth = 24

// dirIgnore is one .gitignore file's rules scoped to the directory it sits in.
type dirIgnore struct {
	dir  string // repository-relative directory containing the file, "" for root
	rule *gitignore.GitIgnore
}

// loadIgnore reads a .gitignore in the open directory, if one exists.
func loadIgnore(dir *os.File) *gitignore.GitIgnore {
	file, err := openChild(dir, ".gitignore", false)
	if err != nil {
		return nil // no ignore file in this directory
	}
	defer file.Close()
	text, _, err := readText(file, ".gitignore", 1<<16)
	if err != nil {
		return nil // unreadable ignore file: never hide the listing behind it
	}
	return gitignore.CompileIgnoreLines(strings.Split(text, "\n")...)
}

// ignored reports whether any active .gitignore rules match the path; a
// deeper rule set wins, matching git's nested-file semantics closely enough
// for listing purposes.
func ignored(ignores []*dirIgnore, path string) bool {
	for i := len(ignores) - 1; i >= 0; i-- {
		ig := ignores[i]
		rel := path
		if ig.dir != "" {
			if !strings.HasPrefix(path, ig.dir+"/") {
				continue
			}
			rel = path[len(ig.dir)+1:]
		}
		if ig.rule.MatchesPath(rel) {
			return true
		}
	}
	return false
}
