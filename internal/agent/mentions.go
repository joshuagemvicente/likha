package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"lisa/internal/repository"
)

// File references: `@path` tokens in a prompt resolve to repository files.
// The model sees the referenced content appended to the user message; the
// TUI shows a completion popup while the user types.

// mentionToken matches @path tokens: @ followed by any non-space run. A
// trailing punctuation run (.,;:) is stripped so "see @main.go." does not
// resolve a file named "main.go.".
var mentionToken = regexp.MustCompile(`@[^\s]+`)

const (
	// mentionMaxTotal caps the bytes of file content added per turn so one
	// prompt cannot balloon a request past provider limits.
	mentionMaxTotal = 64 << 10
	// mentionMaxFiles caps how many references expand per turn.
	mentionMaxFiles = 16
	// mentionMaxIndex caps the file index the popup offers.
	mentionMaxIndex = 2000
	// mentionMaxListing caps how many paths a folder reference inlines.
	mentionMaxListing = 200
)

// expandFileReferences returns prompt with each @path token expanded: a file
// reference inlines the file's content as a fenced block; a folder reference
// inlines its tree listing (paths only, capped). Unresolvable tokens stay
// literal: the model can still use read, and a wrong token must not
// fail the turn. Content is capped at mentionMaxTotal; later references are
// left literal once the cap is reached.
func ExpandFileReferences(prompt string, repo *repository.Repository) string {
	if repo == nil || !strings.Contains(prompt, "@") {
		return prompt
	}
	var blocks strings.Builder
	total, files := 0, 0
	out := mentionToken.ReplaceAllStringFunc(prompt, func(tok string) string {
		path := strings.TrimRight(tok[1:], ".,;:)!?,")
		if path == "" {
			return tok
		}
		if files >= mentionMaxFiles || total >= mentionMaxTotal {
			return tok
		}
		// A folder reference expands to its tree listing — structure first;
		// the model reads individual files with its tools when needed.
		isDir, err := repo.IsDir(path)
		if err == nil && isDir {
			listing, truncated, err := repo.Tree(path, mentionMaxListing)
			if err != nil || len(listing) == 0 {
				return tok
			}
			body := strings.Join(listing, "\n")
			if truncated {
				body += fmt.Sprintf("\n… more entries exist under %s; reference a file with @<path> for its content", path)
			}
			files++
			blocks.WriteString("\n\n[Referenced folder @" + path + "]\n" + body)
			return tok
		}
		content, err := repo.Read(path)
		if err != nil {
			// Unknown paths stay literal so typos are visible, not silent.
			return tok
		}
		files++
		total += len(content)
		if total > mentionMaxTotal {
			files--
			total -= len(content)
			return tok
		}
		blocks.WriteString("\n\n[Referenced file @" + path + "]\n```")
		blocks.WriteString(filepath.Ext(path))
		blocks.WriteString("\n")
		blocks.WriteString(content)
		blocks.WriteString("\n```")
		return tok
	})
	return out + blocks.String()
}

// buildFileIndex returns the repository's visible, non-ignored entries for
// the mention popup via the confined Tree walk: files plain, directories
// with a trailing "/" (folder references). Hidden directories like .git,
// node_modules, and .gitignore matches never appear.
func BuildFileIndex(repo *repository.Repository) []string {
	if repo == nil {
		return nil
	}
	files, _, err := repo.Tree(".", mentionMaxIndex)
	if err != nil {
		return nil
	}
	return files
}

// mentionQuery extracts the @token currently being typed: the text after the
// last @ in the draft. ok reports whether an @ is active; the query may be
// empty (immediately after @) which matches every file.
func MentionQuery(input string) (string, bool) {
	at := strings.LastIndexByte(input, '@')
	if at < 0 {
		return "", false
	}
	tail := input[at+1:]
	if strings.ContainsAny(tail, " \t\n") {
		return "", false
	}
	return tail, true
}

// mentionMatches returns the file index entries visible for the query:
// case-insensitive substring match on the relative path, capped small.
func MentionMatches(files []string, query string) []string {
	if len(files) == 0 {
		return nil
	}
	q := strings.ToLower(query)
	var out []string
	for _, f := range files {
		if strings.Contains(strings.ToLower(f), q) {
			out = append(out, f)
			if len(out) >= 8 {
				break
			}
		}
	}
	return out
}

// completeMention replaces the active @query tail in the draft with the
// chosen file path. The replacement keeps the @ prefix.
func CompleteMention(input, choice string) string {
	at := strings.LastIndexByte(input, '@')
	if at < 0 {
		return input
	}
	return input[:at] + "@" + choice + " "
}
