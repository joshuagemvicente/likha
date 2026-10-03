package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ReadOptions selects a 1-based range. The zero value preserves raw file reads;
// setting either field requests numbered lines, with defaults of 1 and 200.
// Directories always return a page, with the same defaults.
type ReadOptions struct {
	Offset, Limit int
}

// QueryOptions narrows discovery. Path and Include are repository-relative;
// Include uses the same glob syntax as Glob. Discovery excludes hidden entries,
// ignored entries (including node_modules), symlinks and .git by default.
// IncludeHidden and IncludeIgnored never enable symlink or .git traversal.
// CaseSensitive selects the initial Go regexp mode; inline regexp flags can
// override it. Literal bypasses regexp syntax, including inline flags.
type QueryOptions struct {
	Path, Include                 string
	Literal                       bool
	CaseSensitive                 *bool
	IncludeHidden, IncludeIgnored bool
	Limit                         int
	Cursor                        string
}

// Page is bounded inspection output. Truncated also denotes an incomplete scan
// with skipped files, not only a result limit. Warnings explain the distinction.
// NextOffset is a 1-based read continuation; discovery currently issues no
// cursors and instead requires narrowing a limited query.
type Page struct {
	Content    string
	Truncated  bool
	Warnings   []string
	NextOffset int
	Cursor     string
}

const (
	inspectionInlineBytes  = 64 << 10
	inspectionContentBytes = inspectionInlineBytes - 4096
	inspectionDefaultRead  = 200
	inspectionDefaultQuery = 100
)

// Root returns the canonical root pinned by New, not an additional path grant.
func (r *Repository) Root() string { return r.root }

// ReadPage reads a regular UTF-8 file or lists the immediate children of a
// directory. Explicit file reads bypass discovery filters, but not confinement,
// .git exclusion, text validation, or the one-MiB file cap.
func (r *Repository) ReadPage(ctx context.Context, path string, opts ReadOptions) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	path, err := inspectionPath(path, false)
	if err != nil {
		return Page{}, err
	}
	if opts.Offset < 0 || opts.Limit < 0 {
		return Page{}, errors.New("read offset and limit must be positive when supplied")
	}
	file, err := r.open(path, path == ".")
	if err != nil {
		return Page{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Page{}, fmt.Errorf("stat %q: %w", path, err)
	}
	state := &inspectionState{}
	if info.IsDir() {
		// Reopen along only the scope's ancestors to collect their ignore rules.
		// This never walks unrelated repository branches.
		dir, ignores, _, err := r.inspectionScope(ctx, path, QueryOptions{}, state)
		if err != nil {
			return Page{}, err
		}
		defer dir.Close()
		page, err := inspectionReadDirectory(ctx, dir, path, ignores, opts, state)
		if err != nil {
			return Page{}, err
		}
		return r.inspectionFinish(ctx, page, state)
	}
	text, _, err := inspectionReadText(ctx, file, path, MaxReadBytes)
	if err != nil {
		return Page{}, err
	}
	if opts.Offset == 0 && opts.Limit == 0 {
		content := inspectionClip(text, inspectionContentBytes)
		page := Page{Content: content}
		if len(content) != len(text) {
			page.Truncated = true
			page.NextOffset = 1
			state.warn("raw read reached the 64-KiB inline budget; restart with offset=1 and limit=200 for numbered ranges")
		}
		return r.inspectionFinish(ctx, page, state)
	}
	page, err := inspectionReadRange(ctx, path, text, opts, state)
	if err != nil {
		return Page{}, err
	}
	return r.inspectionFinish(ctx, page, state)
}

// GlobPage matches regular repository-relative file paths. A bare ** segment
// spans directories; *, ?, and [...] match within one segment. Patterns remain
// relative to the repository root even when Path narrows the walked subtree.
func (r *Repository) GlobPage(ctx context.Context, pattern string, opts QueryOptions) (Page, error) {
	if opts.Literal || opts.CaseSensitive != nil {
		return Page{}, errors.New("glob uses case-sensitive glob syntax, not literal or regexp case options")
	}
	if strings.IndexByte(pattern, 0) >= 0 {
		return Page{}, ErrInvalidPath
	}
	segments, err := compileGlob(pattern)
	if err != nil {
		return Page{}, err
	}
	state := &inspectionState{}
	scope, include, limit, err := inspectionQuery(opts, MaxGlobResults, state)
	if err != nil {
		return Page{}, err
	}
	dir, ignores, visible, err := r.inspectionScope(ctx, scope, opts, state)
	if err != nil {
		return Page{}, err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return Page{}, err
	}
	if !info.IsDir() {
		return Page{}, fmt.Errorf("glob scope %q is not a directory", scope)
	}
	var paths []string
	body := newInspectionBuffer(inspectionContentBytes - 1024)
	if visible {
		err = state.walk(ctx, dir, inspectionPrefix(scope), ignores, opts, include, func(parent *os.File, name, path string) error {
			if !globMatch(segments, strings.Split(path, string(os.PathSeparator))) {
				return nil
			}
			// Confirm the matched entry is still regular under descriptor confinement.
			file, err := openChild(parent, name, false)
			if err != nil {
				if inspectionSafetyError(err) {
					return err
				}
				state.skip("cannot inspect %q: %v", path, err)
				return nil
			}
			info, err := file.Stat()
			file.Close()
			if err != nil {
				state.skip("cannot stat %q: %v", path, err)
				return nil
			}
			if !info.Mode().IsRegular() {
				state.skip("entry %q changed or is not a regular file", path)
				return nil
			}
			if len(paths) == limit {
				return state.limit("glob reached its effective %d-path limit", limit)
			}
			if !body.add(path + "\n") {
				return state.limit("glob reached the 64-KiB inline budget")
			}
			paths = append(paths, path)
			return nil
		})
	}
	if err != nil && !errors.Is(err, errInspectionLimit) {
		return Page{}, err
	}
	sort.Strings(paths)
	page := Page{Content: inspectionQueryHeader("glob", scope, opts, limit, len(paths), state.incomplete) + strings.Join(paths, "\n")}
	if len(paths) != 0 {
		page.Content += "\n"
	}
	return r.inspectionFinish(ctx, page, state)
}

// GrepPage applies Go regexp syntax per line. With Literal, all pattern bytes
// are text; case-insensitive literal searches use Go's Unicode simple folding.
// Include filters repository-relative paths before eligible files are opened.
func (r *Repository) GrepPage(ctx context.Context, pattern string, opts QueryOptions) (Page, error) {
	if pattern == "" || len(pattern) > MaxQueryBytes {
		return Page{}, fmt.Errorf("grep pattern must contain 1 to %d bytes", MaxQueryBytes)
	}
	query := pattern
	if opts.Literal {
		query = regexp.QuoteMeta(query)
	}
	caseSensitive := opts.CaseSensitive == nil || *opts.CaseSensitive
	if !caseSensitive {
		query = "(?i:" + query + ")"
	} else if opts.CaseSensitive != nil {
		query = "(?-i:" + query + ")"
	}
	re, err := regexp.Compile(query)
	if err != nil {
		return Page{}, fmt.Errorf("invalid grep pattern: %w", err)
	}
	state := &inspectionState{warnUnsupported: true}
	scope, include, limit, err := inspectionQuery(opts, MaxSearchResults, state)
	if err != nil {
		return Page{}, err
	}
	file, ignores, visible, err := r.inspectionScope(ctx, scope, opts, state)
	if err != nil {
		return Page{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Page{}, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return Page{}, fmt.Errorf("grep scope %q is not a regular file or directory", scope)
	}
	var matches []Match
	body := newInspectionBuffer(inspectionContentBytes - 1024)
	scan := func(file *os.File, path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			state.skip("cannot stat %q: %v", path, err)
			return nil
		}
		if !info.Mode().IsRegular() {
			state.skip("entry %q changed or is not a regular text file", path)
			return nil
		}
		if info.Size() > MaxReadBytes {
			state.skip("skipped oversized file %q (maximum %d bytes)", path, MaxReadBytes)
			return nil
		}
		remaining := int64(MaxSearchBytes) - state.bytesRead
		if remaining <= 0 || info.Size() > remaining {
			return state.limit("grep reached its %d-byte scan budget", MaxSearchBytes)
		}
		max := int64(MaxReadBytes)
		if remaining < max {
			max = remaining
		}
		text, size, err := inspectionReadText(ctx, file, path, max)
		state.bytesRead += size
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if state.bytesRead > MaxSearchBytes {
				return state.limit("grep reached its %d-byte scan budget", MaxSearchBytes)
			}
			state.skip("skipped file %q: %v", path, err)
			return nil
		}
		for line, rest := 1, text; len(rest) != 0; line++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			content, next, _ := strings.Cut(rest, "\n")
			if re.MatchString(content) {
				if len(matches) == limit {
					return state.limit("grep reached its effective %d-match limit", limit)
				}
				match := Match{Path: path, Line: line, Text: strings.TrimSuffix(content, "\r")}
				formatted := fmt.Sprintf("%s:%d: %s\n", match.Path, match.Line, match.Text)
				if !body.add(formatted) {
					// A single very long matching line must not erase all useful output.
					if len(matches) == 0 {
						prefix := fmt.Sprintf("%s:%d: ", path, line)
						match.Text = inspectionClip(match.Text, body.remaining()-inspectionEncodedBytes(prefix+"\n"))
						body.add(prefix + match.Text + "\n")
						matches = append(matches, match)
						state.warn("matching line %d in %q exceeds the inline budget; its tail was omitted", line, path)
					}
					return state.limit("grep reached the 64-KiB inline budget")
				}
				matches = append(matches, match)
			}
			rest = next
		}
		return nil
	}
	if visible {
		if info.IsDir() {
			err = state.walk(ctx, file, inspectionPrefix(scope), ignores, opts, include, func(parent *os.File, name, path string) error {
				child, err := openChild(parent, name, false)
				if err != nil {
					if inspectionSafetyError(err) {
						return err
					}
					state.skip("cannot read %q: %v", path, err)
					return nil
				}
				defer child.Close()
				return scan(child, path)
			})
		} else if inspectionIncluded(include, scope) {
			err = scan(file, scope)
		}
	}
	if err != nil && !errors.Is(err, errInspectionLimit) {
		return Page{}, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})
	mode := "Go regexp (inline flags may override initial case mode)"
	if opts.Literal {
		mode = "literal (Unicode simple folding when case-insensitive)"
	}
	var output strings.Builder
	output.WriteString(inspectionQueryHeader("grep", scope, opts, limit, len(matches), state.incomplete))
	fmt.Fprintf(&output, "syntax=%s; case_sensitive=%t; scanned_bytes=%d\n", mode, caseSensitive, state.bytesRead)
	for _, match := range matches {
		fmt.Fprintf(&output, "%s:%d: %s\n", match.Path, match.Line, match.Text)
	}
	return r.inspectionFinish(ctx, Page{Content: output.String()}, state)
}

func inspectionReadRange(ctx context.Context, path, text string, opts ReadOptions, state *inspectionState) (Page, error) {
	offset, limit := inspectionReadDefaults(opts)
	total := 0
	for rest := text; len(rest) != 0; total++ {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		_, rest, _ = strings.Cut(rest, "\n")
	}
	body := newInspectionBuffer(inspectionContentBytes - 1024)
	page := Page{}
	selected, last := 0, 0
	for line, rest := 1, text; len(rest) != 0; line++ {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		content, next, _ := strings.Cut(rest, "\n")
		rest = next
		if line < offset {
			continue
		}
		if selected == limit {
			page.NextOffset = line
			break
		}
		prefix := fmt.Sprintf("%d: ", line)
		content = strings.TrimSuffix(content, "\r")
		if !body.add(prefix + content + "\n") {
			page.Truncated = true
			state.warn("numbered read reached the 64-KiB inline budget")
			if selected != 0 {
				page.NextOffset = line
				break
			}
			body.add(prefix + inspectionClip(content, body.remaining()-inspectionEncodedBytes(prefix+"\n")) + "\n")
			state.warn("line %d exceeds the inline budget; its tail was omitted, and next_offset advances to the following line", line)
			selected, last = 1, line
			if line < total {
				page.NextOffset = line + 1
			}
			break
		}
		selected++
		last = line
	}
	page.Truncated = page.Truncated || page.NextOffset != 0
	start := offset
	if selected == 0 {
		start = 0
	}
	page.Content = fmt.Sprintf("read file %s: total_lines=%d; selected=%d-%d; offset=%d; limit=%d; more=%t; end_of_file=%t\n",
		inspectionLabel(path), total, start, last, offset, limit, page.NextOffset != 0, offset > total || last == total) + body.String()
	return page, nil
}

func inspectionReadDirectory(ctx context.Context, dir *os.File, path string, ignores []*dirIgnore, opts ReadOptions, state *inspectionState) (Page, error) {
	offset, limit := inspectionReadDefaults(opts)
	if limit > MaxListEntries {
		state.warn("directory read limit capped at %d children", MaxListEntries)
		limit = MaxListEntries
	}
	ignores, err := state.loadIgnore(ctx, dir, inspectionPrefix(path), ignores)
	if err != nil {
		return Page{}, err
	}
	entries, bounded, err := inspectionEntries(ctx, dir)
	if err != nil {
		return Page{}, err
	}
	if bounded {
		state.skip("directory %q exceeds %d entries; only a bounded observed subset is listed; narrow the path (no safe continuation)", path, MaxListEntries)
	}
	var children []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		name := entry.Name()
		candidate := filepath.Join(inspectionPrefix(path), name)
		if !inspectionVisible(name, candidate, entry.IsDir(), ignores, QueryOptions{}) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !utf8.ValidString(candidate) {
			state.skip("skipped an entry with a non-UTF-8 path in %q", path)
			continue
		}
		if entry.IsDir() {
			children = append(children, candidate+"/")
		} else if entry.Type().IsRegular() {
			children = append(children, candidate)
		}
	}
	sort.Strings(children)
	body := newInspectionBuffer(inspectionContentBytes - 1024)
	page := Page{}
	selected, last := 0, 0
	for i, child := range children {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		if i+1 < offset {
			continue
		}
		if selected == limit || !body.add(child+"\n") {
			page.NextOffset = i + 1
			page.Truncated = true
			if selected < limit {
				state.warn("directory read reached the 64-KiB inline budget")
			}
			break
		}
		selected++
		last = i + 1
	}
	if bounded {
		// Sorting an arbitrary bounded sample cannot authorize a next page.
		page.NextOffset = 0
	}
	total := fmt.Sprint(len(children))
	if bounded {
		total = "unknown (observed " + total + ")"
	}
	start := offset
	if selected == 0 {
		start = 0
	}
	page.Content = fmt.Sprintf("read directory %s: total_children=%s; selected=%d-%d; offset=%d; limit=%d; more=%t; end_of_directory=%t; hidden=false; ignored=false; .git/symlinks excluded\n",
		inspectionLabel(path), total, start, last, offset, limit, bounded || page.NextOffset != 0, !bounded && (offset > len(children) || last == len(children))) + body.String()
	return page, nil
}

func inspectionReadDefaults(opts ReadOptions) (int, int) {
	offset, limit := opts.Offset, opts.Limit
	if offset == 0 {
		offset = 1
	}
	if limit == 0 {
		limit = inspectionDefaultRead
	}
	return offset, limit
}

func inspectionQuery(opts QueryOptions, cap int, state *inspectionState) (string, []string, int, error) {
	if opts.Cursor != "" {
		return "", nil, 0, errors.New("discovery cursors are not supported; restart with an empty cursor and narrower path/include, or raise limit within the hard cap")
	}
	if opts.Limit < 0 {
		return "", nil, 0, errors.New("query limit must be positive when supplied")
	}
	path, err := inspectionPath(opts.Path, true)
	if err != nil {
		return "", nil, 0, err
	}
	var include []string
	if opts.Include != "" {
		if strings.IndexByte(opts.Include, 0) >= 0 {
			return "", nil, 0, ErrInvalidPath
		}
		include, err = compileGlob(opts.Include)
		if err != nil {
			return "", nil, 0, fmt.Errorf("invalid include filter: %w", err)
		}
	}
	limit := opts.Limit
	if limit == 0 {
		limit = inspectionDefaultQuery
	}
	if limit > cap {
		state.warn("requested query limit capped at %d", cap)
		limit = cap
	}
	return path, include, limit, nil
}

func inspectionQueryHeader(tool, scope string, opts QueryOptions, limit, count int, incomplete bool) string {
	return fmt.Sprintf("%s: path=%s; include=%s; hidden=%t; ignored=%t (.gitignore/node_modules); .git/symlinks excluded; limit=%d; returned=%d; complete=%t\n",
		tool, inspectionLabel(scope), inspectionLabel(opts.Include), opts.IncludeHidden, opts.IncludeIgnored, limit, count, !incomplete)
}

func (r *Repository) inspectionFinish(ctx context.Context, page Page, state *inspectionState) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	// A replacement detected after the scan invalidates all already-read payload.
	root, err := r.openRoot()
	if err != nil {
		return Page{}, err
	}
	root.Close()
	page.Truncated = page.Truncated || state.incomplete
	page.Warnings = append(page.Warnings, state.warnings...)
	if state.suppressed != 0 {
		page.Warnings = append(page.Warnings, fmt.Sprintf("%d additional inspection warnings omitted", state.suppressed))
	}
	return page, nil
}
