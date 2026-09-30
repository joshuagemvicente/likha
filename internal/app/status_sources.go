package app

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"lisa/internal/session"
)

// statusFolder displays the selected root relative to the user's home when
// possible, leaving other paths unchanged.
func statusFolder(root string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return root
	}
	if label, ok := homeRelative(home, root); ok {
		return label
	}
	// The repository root is canonicalized at launch; HOME may still use a
	// symlink (for example /var -> /private/var on macOS).
	canonicalHome, homeErr := filepath.EvalSymlinks(home)
	canonicalRoot, rootErr := filepath.EvalSymlinks(root)
	if homeErr == nil && rootErr == nil {
		if label, ok := homeRelative(canonicalHome, canonicalRoot); ok {
			return label
		}
	}
	return root
}

func homeRelative(home, root string) (string, bool) {
	rel, err := filepath.Rel(home, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	if rel == "." {
		return "~", true
	}
	return filepath.Join("~", rel), true
}

// gitState is everything the status bar shows about the repository, from a
// single bounded `git status --porcelain -b` call.
type gitState struct {
	Branch    string // current branch; "" when detached
	Detached  bool
	Ahead     int
	Behind    int
	Staged    int
	Dirty     int // worktree-modified, excluding untracked
	Untracked int
}

// gitStatus runs the bounded status call; ok=false (git missing, timeout,
// non-repository) means every segment hides. Detached HEAD reports the
// short SHA (7 chars) in Branch with Detached=true, resolved from
// .git/HEAD directly — never a second git call.
func gitStatus(root string) (gitState, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", root, "status", "--porcelain", "-b")
	out, err := cmd.Output()
	if err != nil {
		return gitState{}, false
	}
	state := parseGitStatus(out)
	if state.Detached {
		state.Branch = detachedHead(root)
	}
	return state, true
}

// detachedHead resolves a detached HEAD's short SHA (7 chars) from .git/HEAD
// directly — no git subprocess. Empty on any parse failure. It walks the
// same repository chain as a branch read (regular repositories and linked
// worktrees) but reads the raw detached-HEAD contents instead of a ref.
func detachedHead(root string) string {
	for dir := filepath.Clean(root); ; dir = filepath.Dir(dir) {
		git := filepath.Join(dir, ".git")
		info, err := os.Stat(git)
		if err == nil {
			if !info.IsDir() {
				pointer := strings.TrimSpace(readSmallFile(git))
				if !strings.HasPrefix(pointer, "gitdir: ") {
					return ""
				}
				git = strings.TrimSpace(strings.TrimPrefix(pointer, "gitdir: "))
				if git == "" {
					return ""
				}
				if !filepath.IsAbs(git) {
					git = filepath.Join(dir, git)
				}
			}
			head := strings.TrimSpace(readSmallFile(filepath.Join(git, "HEAD")))
			if head == "" || strings.HasPrefix(head, "ref: ") {
				return "" // not detached, or invalid
			}
			if len(head) < 7 || strings.ContainsAny(head[:7], "\r\n\t\x00") {
				return ""
			}
			return head[:7]
		}
		if !os.IsNotExist(err) || dir == filepath.Dir(dir) {
			return ""
		}
	}
}

// parseGitStatus parses one `git status --porcelain -b` output. The first
// line is the branch header; remaining lines are porcelain v1 records
// "XY " + path, where X is the index column and Y the worktree column.
// Header forms: `## <branch>...[upstream] [ahead N[, behind M]]` (either
// count may be absent → 0) and `## HEAD (no branch)` → detached. A record
// is staged when X holds anything but ' ' or '?', worktree-dirty when Y
// holds anything but ' ' or '?' (untracked "??", where both columns are
// '?', is counted separately), and untracked when X and Y are both '?'.
func parseGitStatus(out []byte) gitState {
	var state gitState
	for i, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue // trailing newline or blank separator
		}
		if i == 0 && strings.HasPrefix(line, "## ") {
			parseGitStatusHeader(line[3:], &state)
			continue
		}
		if len(line) < 3 || line[2] != ' ' {
			continue // blank or malformed record
		}
		x, y := line[0], line[1]
		if x == '?' && y == '?' {
			state.Untracked++
			continue
		}
		if x != ' ' && x != '?' {
			state.Staged++
		}
		if y != ' ' && y != '?' {
			state.Dirty++
		}
	}
	return state
}

// parseGitStatusHeader fills Branch, Detached, Ahead and Behind from the
// content of the `##` header line, without its prefix.
func parseGitStatusHeader(header string, state *gitState) {
	if header == "HEAD (no branch)" {
		state.Detached = true
		return
	}
	// Drop any "...[upstream]" tracking suffix before extracting the name.
	branch := header
	if i := strings.Index(header, "..."); i >= 0 {
		branch = header[:i]
	}
	state.Branch = branch
	// Tracking info: "ahead N", "behind M" or "ahead N, behind M".
	const ahead = "ahead "
	const behind = "behind "
	tracking := header
	if i := strings.Index(header, "["); i >= 0 {
		if j := strings.LastIndex(header, "]"); j > i {
			tracking = header[i+1 : j]
		}
	}
	for _, part := range strings.Split(tracking, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, ahead):
			state.Ahead = parseGitNumber(part[len(ahead):])
		case strings.HasPrefix(part, behind):
			state.Behind = parseGitNumber(part[len(behind):])
		}
	}
}

// parseGitNumber reads a non-negative decimal count, 0 on anything else.
func parseGitNumber(s string) int {
	n := 0
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// readSmallFile bounds reads even if a malformed Git metadata file is huge.
func readSmallFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const maxGitMetadata = 4096
	data, err := io.ReadAll(io.LimitReader(f, maxGitMetadata+1))
	if err != nil || len(data) > maxGitMetadata {
		return ""
	}
	return string(data)
}

// statusSessionTitle mirrors the session store's preference for generated
// names, then user entries, then history. New sessions use their short ID
// until the first prompt exists. Prompts are made safe for a single
// terminal row before display.
func statusSessionTitle(snapshot session.Snapshot) string {
	if snapshot.NamedTitle != "" {
		return snapshot.NamedTitle
	}
	for _, entry := range snapshot.Entries {
		if entry.Role == "user" {
			if title := singleLineTitle(entry.Content); title != "" {
				return title
			}
		}
	}
	for _, message := range snapshot.History {
		if message.Role == "user" {
			if title := singleLineTitle(message.Content); title != "" {
				return title
			}
		}
	}
	if len(snapshot.ID) > 8 {
		return snapshot.ID[:8]
	}
	return snapshot.ID
}

func singleLineTitle(prompt string) string {
	return strings.Join(strings.FieldsFunc(prompt, func(r rune) bool {
		return unicode.IsSpace(r) || !unicode.IsPrint(r)
	}), " ")
}
