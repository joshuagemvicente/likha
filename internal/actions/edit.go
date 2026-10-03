package actions

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const MaxEditBytes = 1 << 20

var ErrStaleEdit = errors.New("edit is stale: file or directory changed since preview")

type Edit struct {
	Path string
	Diff string

	root    string
	parts   []string
	parents []os.FileInfo
	old     string
	new     string
	before  os.FileInfo // nil for a file that did not exist
}

// PrepareEdit only reads the repository. Parent directories must already exist;
// neither this method nor Apply creates them. The diff describes the entire
// replacement (including a final newline's presence or absence).
func PrepareEdit(root, path, content string) (*Edit, error) {
	parts, err := editPath(path)
	if err != nil {
		return nil, err
	}
	if len(content) > MaxEditBytes || !previewSafeText(content) {
		return nil, fmt.Errorf("replacement must be UTF-8 text without invisible controls and at most %d bytes", MaxEditBytes)
	}
	canonical, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}
	parent, parents, err := openParent(canonical, parts, nil)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	old, before, err := readTarget(parent, parts[len(parts)-1])
	if err != nil {
		return nil, err
	}
	if before != nil && old == content {
		return nil, errors.New("replacement does not change the file")
	}
	path = filepath.Join(parts...)
	return &Edit{Path: path, Diff: unified(path, old, content, before == nil), root: canonical,
		parts: parts, parents: parents, old: old, new: content, before: before}, nil
}

// Apply checks that the prepared file and its parent directories are unchanged,
// writes a temporary file in the same directory, then atomically publishes it.
// Like any filesystem check followed by a rename, concurrent adversarial writes
// during that final interval cannot be made transactional without filesystem help.
func (e *Edit) Apply() error {
	if e == nil || len(e.parts) == 0 || len(e.parents) != len(e.parts) {
		return errors.New("invalid edit")
	}
	parent, _, err := openParent(e.root, e.parts, e.parents)
	if err != nil {
		return err
	}
	defer parent.Close()
	name := e.parts[len(e.parts)-1]
	// Check before doing any work, then again immediately before publishing.
	if err := e.checkTarget(parent, name); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("temporary file name: %w", err)
	}
	tmpName := ".likha-edit-" + hex.EncodeToString(nonce[:])
	fd, err := unix.Openat(int(parent.Fd()), tmpName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", e.Path, err)
	}
	defer unix.Unlinkat(int(parent.Fd()), tmpName, 0)
	tmp := os.NewFile(uintptr(fd), tmpName)
	if _, err := io.WriteString(tmp, e.new); err != nil {
		tmp.Close()
		return fmt.Errorf("write %q: %w", e.Path, err)
	}
	mode := os.FileMode(0644)
	if e.before != nil {
		mode = e.before.Mode()
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
		tmp.Close()
		return fmt.Errorf("set permissions for %q: %w", e.Path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %q: %w", e.Path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %q: %w", e.Path, err)
	}
	if err := e.checkTarget(parent, name); err != nil {
		return err
	}
	// A directory can be renamed while the temporary file is being written.
	// Confirm the still-open parent is reachable beneath the original root.
	reopened, ancestors, err := openParent(e.root, e.parts, nil)
	if err != nil {
		return fmt.Errorf("recheck parent of %q: %w", e.Path, err)
	}
	parentInfo, statErr := parent.Stat()
	closeErr := reopened.Close()
	if statErr != nil {
		return fmt.Errorf("stat parent of %q: %w", e.Path, statErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close parent of %q: %w", e.Path, closeErr)
	}
	if !os.SameFile(ancestors[0], e.parents[0]) || !os.SameFile(ancestors[len(ancestors)-1], parentInfo) {
		return fmt.Errorf("parent of %q: %w", e.Path, ErrStaleEdit)
	}
	if e.before == nil {
		// Link fails with EEXIST if the new path appeared after approval.
		if err := unix.Linkat(int(parent.Fd()), tmpName, int(parent.Fd()), name, 0); err != nil {
			if errors.Is(err, unix.EEXIST) {
				return fmt.Errorf("%q: %w", e.Path, ErrStaleEdit)
			}
			return fmt.Errorf("publish %q: %w", e.Path, err)
		}
		return nil
	}
	if err := unix.Renameat(int(parent.Fd()), tmpName, int(parent.Fd()), name); err != nil {
		return fmt.Errorf("publish %q: %w", e.Path, err)
	}
	return nil
}

func (e *Edit) checkTarget(parent *os.File, name string) error {
	current, info, err := readTarget(parent, name)
	if err != nil {
		return err
	}
	if (e.before == nil) != (info == nil) || info != nil && (!sameState(info, e.before) || current != e.old) {
		return fmt.Errorf("%q: %w", e.Path, ErrStaleEdit)
	}
	return nil
}

func editPath(path string) ([]string, error) {
	if path == "" || filepath.IsAbs(path) || strings.IndexFunc(path, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return nil, fmt.Errorf("invalid repository-relative file path %q", path)
	}
	parts := strings.Split(path, string(os.PathSeparator))
	for _, part := range parts {
		if part == ".." {
			return nil, fmt.Errorf("parent traversal in %q", path)
		}
	}
	clean := filepath.Clean(path)
	if clean == "." {
		return nil, fmt.Errorf("invalid file path %q", path)
	}
	return strings.Split(clean, string(os.PathSeparator)), nil
}

func openParent(root string, parts []string, expected []os.FileInfo) (*os.File, []os.FileInfo, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open repository: %w", err)
	}
	current := os.NewFile(uintptr(fd), root)
	parents := make([]os.FileInfo, 0, len(parts))
	for i := range parts {
		info, err := current.Stat()
		if err != nil {
			current.Close()
			return nil, nil, fmt.Errorf("stat parent of %q: %w", filepath.Join(parts...), err)
		}
		if expected != nil && !sameState(info, expected[i]) {
			current.Close()
			return nil, nil, fmt.Errorf("parent of %q: %w", filepath.Join(parts...), ErrStaleEdit)
		}
		parents = append(parents, info)
		if i == len(parts)-1 {
			return current, parents, nil
		}
		fd, err := unix.Openat(int(current.Fd()), parts[i], unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		current.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("open parent %q (must exist and not be a symlink): %w", parts[i], err)
		}
		current = os.NewFile(uintptr(fd), parts[i])
	}
	panic("empty edit path")
}

func readTarget(parent *os.File, name string) (string, os.FileInfo, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("open %q (symlinks not allowed): %w", name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", nil, fmt.Errorf("stat %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%q is not a regular file", name)
	}
	if info.Size() > MaxEditBytes {
		return "", nil, fmt.Errorf("%q exceeds %d-byte edit limit", name, MaxEditBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxEditBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("read %q: %w", name, err)
	}
	if len(data) > MaxEditBytes {
		return "", nil, fmt.Errorf("%q exceeds %d-byte edit limit", name, MaxEditBytes)
	}
	after, err := file.Stat()
	if err != nil {
		return "", nil, fmt.Errorf("stat %q: %w", name, err)
	}
	if !sameState(info, after) {
		return "", nil, fmt.Errorf("%q: %w", name, ErrStaleEdit)
	}
	old := string(data)
	if !previewSafeText(old) {
		return "", nil, fmt.Errorf("%q is not UTF-8 text without invisible controls", name)
	}
	return old, info, nil
}

func sameState(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

// A unified diff uses LF to delimit lines. Other controls can move, erase, or
// hide displayed text while leaving the file's executable bytes unchanged.
func previewSafeText(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool {
		return r != '\n' && r != '\t' &&
			(unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) ||
				unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r))
	}) < 0
}

// diffLine carries the terminator state as well as the text: changing only the
// final newline is still a removal and an addition, not an unchanged line.
type diffLine struct {
	kind      byte
	text      string
	noNewline bool
}

func unified(path, old, next string, created bool) string {
	var diff strings.Builder
	if created {
		diff.WriteString("--- /dev/null\n")
	} else {
		diff.WriteString("--- a/")
		diff.WriteString(path)
		diff.WriteByte('\n')
	}
	diff.WriteString("+++ b/")
	diff.WriteString(path)
	diff.WriteByte('\n')
	oldLines, newLines := lines(old), lines(next)
	changes := make([]diffLine, 0)
	oldFinalNL, newFinalNL := strings.HasSuffix(old, "\n"), strings.HasSuffix(next, "\n")
	equal := func(i, j int) bool {
		return oldLines[i] == newLines[j] &&
			(i != len(oldLines)-1 || oldFinalNL) == (j != len(newLines)-1 || newFinalNL)
	}
	addOld := func(i int) {
		changes = append(changes, diffLine{'-', oldLines[i], i == len(oldLines)-1 && !oldFinalNL})
	}
	addNew := func(j int) {
		changes = append(changes, diffLine{'+', newLines[j], j == len(newLines)-1 && !newFinalNL})
	}
	addEqual := func(i int) {
		changes = append(changes, diffLine{' ', oldLines[i], i == len(oldLines)-1 && !oldFinalNL})
	}

	// Shared edges cost linear time even for files with hundreds of thousands
	// of short lines. Unique common lines anchor separate changes without
	// requiring a quadratic edit-distance search across the entire file.
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && equal(prefix, prefix) {
		addEqual(prefix)
		prefix++
	}
	oldEnd, newEnd := len(oldLines), len(newLines)
	for oldEnd > prefix && newEnd > prefix && equal(oldEnd-1, newEnd-1) {
		oldEnd--
		newEnd--
	}
	oldPositions := make(map[string]int, oldEnd-prefix)
	for i := prefix; i < oldEnd; i++ {
		if _, found := oldPositions[oldLines[i]]; found {
			oldPositions[oldLines[i]] = -1
		} else {
			oldPositions[oldLines[i]] = i
		}
	}
	newCounts := make(map[string]int, newEnd-prefix)
	for j := prefix; j < newEnd; j++ {
		newCounts[newLines[j]]++
	}
	type anchor struct{ old, new int }
	candidates := make([]anchor, 0)
	for j := prefix; j < newEnd; j++ {
		i, found := oldPositions[newLines[j]]
		if found && i >= 0 && newCounts[newLines[j]] == 1 && equal(i, j) {
			candidates = append(candidates, anchor{i, j})
		}
	}
	// Longest increasing subsequence of unique matching lines.
	tails := make([]int, 0, len(candidates))
	previous := make([]int, len(candidates))
	for k, candidate := range candidates {
		at := sort.Search(len(tails), func(t int) bool { return candidates[tails[t]].old >= candidate.old })
		previous[k] = -1
		if at > 0 {
			previous[k] = tails[at-1]
		}
		if at == len(tails) {
			tails = append(tails, k)
		} else {
			tails[at] = k
		}
	}
	anchors := make([]anchor, len(tails))
	if len(tails) > 0 {
		for k, at := len(tails)-1, tails[len(tails)-1]; k >= 0; k-- {
			anchors[k] = candidates[at]
			at = previous[at]
		}
	}
	fromOld, fromNew := prefix, prefix
	for _, a := range anchors {
		appendDiffGap(fromOld, a.old, fromNew, a.new, equal, addOld, addNew, addEqual)
		addEqual(a.old)
		fromOld, fromNew = a.old+1, a.new+1
	}
	appendDiffGap(fromOld, oldEnd, fromNew, newEnd, equal, addOld, addNew, addEqual)
	for i := oldEnd; i < len(oldLines); i++ {
		addEqual(i)
	}

	oldPos, newPos := 1, 1
	for i := 0; i < len(changes); {
		for i < len(changes) && changes[i].kind == ' ' {
			oldPos++
			newPos++
			i++
		}
		if i == len(changes) {
			break
		}
		start := max(0, i-3)
		oldPos -= i - start
		newPos -= i - start
		end := min(len(changes), i+4)
		for j := i + 1; j < len(changes) && j <= end+3; j++ {
			if changes[j].kind != ' ' {
				end = min(len(changes), j+4)
			}
		}
		oldCount, newCount := 0, 0
		for _, line := range changes[start:end] {
			if line.kind != '+' {
				oldCount++
			}
			if line.kind != '-' {
				newCount++
			}
		}
		writeHunkHeader(&diff, oldPos, oldCount, newPos, newCount)
		// Within each changed run, emit removals before additions. Neither
		// the matched context nor the ordering on either side is altered.
		for k := start; k < end; {
			if changes[k].kind == ' ' {
				writeDiffLine(&diff, ' ', changes[k].text, changes[k].noNewline)
				oldPos++
				newPos++
				k++
				continue
			}
			next := k
			for next < end && changes[next].kind != ' ' {
				next++
			}
			for _, kind := range []byte{'-', '+'} {
				for _, line := range changes[k:next] {
					if line.kind != kind {
						continue
					}
					writeDiffLine(&diff, line.kind, line.text, line.noNewline)
					if kind == '-' {
						oldPos++
					} else {
						newPos++
					}
				}
			}
			k = next
		}
		i = end
	}
	if created && len(changes) == 0 {
		writeHunkHeader(&diff, 1, 0, 1, 0)
	}
	return diff.String()
}

func writeHunkHeader(b *strings.Builder, oldPos, oldCount, newPos, newCount int) {
	if oldCount == 0 {
		oldPos--
	}
	if newCount == 0 {
		newPos--
	}
	b.WriteString("@@ -" + strconv.Itoa(oldPos) + "," + strconv.Itoa(oldCount) +
		" +" + strconv.Itoa(newPos) + "," + strconv.Itoa(newCount) + " @@\n")
}

// appendDiffGap uses Myers for nearby edits. Limiting the number of diagonals
// bounds both time and trace memory for an adversarial megabyte of text.
func appendDiffGap(a, b, c, d int, equal func(int, int) bool,
	addOld, addNew, addEqual func(int)) {
	for a < b && c < d && equal(a, c) {
		addEqual(a)
		a++
		c++
	}
	suffix := 0
	for b > a && d > c && equal(b-1, d-1) {
		b--
		d--
		suffix++
	}
	if a == b || c == d {
		for i := a; i < b; i++ {
			addOld(i)
		}
		for j := c; j < d; j++ {
			addNew(j)
		}
	} else if !appendMyersGap(a, b, c, d, equal, addOld, addNew, addEqual) {
		// A large edit distance cannot justify an unbounded search. A bounded
		// lookahead retains nearby common lines; everything else is explicitly
		// shown as removed and added, never omitted from the review.
		for a < b && c < d {
			if equal(a, c) {
				addEqual(a)
				a++
				c++
				continue
			}
			oldSkip, newSkip := 33, 33
			for n := 1; n <= 32 && a+n < b; n++ {
				if equal(a+n, c) {
					oldSkip = n
					break
				}
			}
			for n := 1; n <= 32 && c+n < d; n++ {
				if equal(a, c+n) {
					newSkip = n
					break
				}
			}
			if oldSkip < newSkip {
				for n := 0; n < oldSkip; n++ {
					addOld(a + n)
				}
				a += oldSkip
			} else if newSkip < 33 {
				for n := 0; n < newSkip; n++ {
					addNew(c + n)
				}
				c += newSkip
			} else {
				addOld(a)
				addNew(c)
				a++
				c++
			}
		}
		for ; a < b; a++ {
			addOld(a)
		}
		for ; c < d; c++ {
			addNew(c)
		}
	}
	for i := 0; i < suffix; i++ {
		addEqual(b + i)
	}
}

func appendMyersGap(a, b, c, d int, equal func(int, int) bool,
	addOld, addNew, addEqual func(int)) bool {
	n, m := b-a, d-c
	limit := min(n+m, 512)
	offset := limit + 1
	v := make([]int, 2*offset+1)
	trace := make([][]int, 0, limit+1)
	operations := 0
	for depth := 0; depth <= limit; depth++ {
		for k := -depth; k <= depth; k += 2 {
			operations++
			if operations > 4_000_000 {
				return false
			}
			x := 0
			if k == -depth || (k != depth && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && equal(a+x, c+y) {
				operations++
				if operations > 4_000_000 {
					return false
				}
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				// Store index/kind pairs until the reverse path is known.
				// This avoids copying any of the input line strings.
				type step struct {
					kind  byte
					index int
				}
				steps := make([]step, 0, n+m)
				px, py := n, m
				for level := depth; level > 0; level-- {
					previous := trace[level-1]
					diagonal := px - py
					prevDiagonal := diagonal - 1
					if diagonal == -level || (diagonal != level && previous[offset+diagonal-1] < previous[offset+diagonal+1]) {
						prevDiagonal = diagonal + 1
					}
					prevX := previous[offset+prevDiagonal]
					prevY := prevX - prevDiagonal
					for px > prevX && py > prevY {
						px--
						py--
						steps = append(steps, step{' ', a + px})
					}
					if px == prevX {
						py--
						steps = append(steps, step{'+', c + py})
					} else {
						px--
						steps = append(steps, step{'-', a + px})
					}
				}
				for px > 0 && py > 0 {
					px--
					py--
					steps = append(steps, step{' ', a + px})
				}
				for i := len(steps) - 1; i >= 0; i-- {
					switch steps[i].kind {
					case ' ':
						addEqual(steps[i].index)
					case '-':
						addOld(steps[i].index)
					case '+':
						addNew(steps[i].index)
					}
				}
				return true
			}
		}
		trace = append(trace, append([]int(nil), v...))
	}
	return false
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		return parts[:len(parts)-1]
	}
	return parts
}

func writeDiffLine(b *strings.Builder, prefix byte, line string, noNewline bool) {
	b.WriteByte(prefix)
	b.WriteString(line)
	b.WriteByte('\n')
	if noNewline {
		b.WriteString("\\ No newline at end of file\n")
	}
}
