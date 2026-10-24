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
// whether more entries existed or some could not be listed (a directory over
// MaxListEntries contributes a bounded sample, an unreadable subdirectory
// contributes only its own name). It is the shared traversal behind the @
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
		if rules := loadIgnore(dir, prefix); rules != nil {
			ignores = append(append([]*dirIgnore(nil), ignores...), rules)
		}
		entries0, err := dir.ReadDir(MaxListEntries + 1)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("tree directory %q: %w", prefix, err)
		}
		if len(entries0) > MaxListEntries {
			// List a bounded sample of an oversized directory instead of
			// failing the whole walk, and report the listing as incomplete.
			entries0 = entries0[:MaxListEntries]
			if base == "" || prefix == base || strings.HasPrefix(prefix, base+"/") {
				truncated = true // only a directory inside the requested subtree loses entries
			}
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
					// An unreadable subdirectory hides only its own entries,
					// never the rest of the listing.
					truncated = true
					continue
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
	// reinclude is the same rules behind a leading match-everything pattern.
	// It reports false only when the last pattern matching a path is a
	// negation, which is how a deeper "!keep.log" overrides a parent rule.
	reinclude *gitignore.GitIgnore
}

// compileIgnore scopes one .gitignore file's text to its directory.
func compileIgnore(dir, text string) *dirIgnore {
	lines := strings.Split(text, "\n")
	return &dirIgnore{
		dir:       dir,
		rule:      gitignore.CompileIgnoreLines(lines...),
		reinclude: gitignore.CompileIgnoreLines(append([]string{"*"}, lines...)...),
	}
}

// loadIgnore reads a .gitignore in the open directory, if one exists.
func loadIgnore(dir *os.File, prefix string) *dirIgnore {
	file, err := openChild(dir, ".gitignore", false)
	if err != nil {
		return nil // no ignore file in this directory
	}
	defer file.Close()
	text, _, err := readText(file, ".gitignore", 1<<16)
	if err != nil {
		return nil // unreadable ignore file: never hide the listing behind it
	}
	return compileIgnore(prefix, text)
}

// ignored reports whether the active .gitignore rules exclude the path. The
// deepest file with a pattern matching the path decides, as in git: its
// positive match ignores the path, and its negation ("!name") re-includes
// it even when a parent file ignores it.
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
		if !ig.reinclude.MatchesPath(rel) {
			return false
		}
	}
	return false
}
