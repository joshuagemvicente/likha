package app

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
// literal: the model can still use read_file, and a wrong token must not
// fail the turn. Content is capped at mentionMaxTotal; later references are
// left literal once the cap is reached.
func expandFileReferences(prompt string, repo *repository.Repository) string {
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
func buildFileIndex(repo *repository.Repository) []string {
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
func mentionQuery(input string) (string, bool) {
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
func mentionMatches(files []string, query string) []string {
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
func completeMention(input, choice string) string {
	at := strings.LastIndexByte(input, '@')
	if at < 0 {
		return input
	}
	return input[:at] + "@" + choice + " "
}

// mentionState is the @ completion popup: fileIndex is the cached walk,
// matches the visible rows for the current query, cursor the selection.
type mentionState struct {
	open    bool
	index   []string
	matches []string
	cursor  int
	err     string
}

// fileIndexMsg delivers the built file index to the UI.
type fileIndexMsg struct {
	files []string
	err   error
}

// startMention opens the popup and builds the index in the background; an
// already-built index is reused immediately.
func (m *ui) startMention() tea.Cmd {
	if m.mention.index != nil {
		m.mention.open = true
		m.syncMention()
		return nil
	}
	m.mention.open = true
	repo := m.repo
	return func() tea.Msg {
		return fileIndexMsg{files: buildFileIndex(repo)}
	}
}

// syncMention re-filters matches from the current draft. The cursor resets to
// the first visible row on any query change.
func (m *ui) syncMention() {
	query, _ := mentionQuery(string(m.input))
	m.mention.matches = mentionMatches(m.mention.index, query)
	if m.mention.cursor >= len(m.mention.matches) {
		m.mention.cursor = 0
	}
}

// mentionActive reports whether the popup is open with rows to show.
func (m *ui) mentionActive() bool {
	return m.mention.open && len(m.mention.matches) > 0
}

// updateMention handles keys while the @ popup is open. It reports whether
// the key was consumed; callers fall through to normal input handling when
// the popup does not claim the key.
func (m *ui) updateMention(v tea.KeyMsg) (bool, tea.Cmd) {
	switch v.String() {
	case "up":
		if m.mentionActive() {
			m.mention.cursor = max(0, m.mention.cursor-1)
			return true, nil
		}
	case "down":
		if m.mentionActive() {
			m.mention.cursor = min(len(m.mention.matches)-1, m.mention.cursor+1)
			return true, nil
		}
	case "tab":
		if m.mentionActive() {
			m.input = []rune(completeMention(string(m.input), m.mention.matches[m.mention.cursor]))
			m.edit.endCaret(m.input)
			m.mention.open = false
			m.layoutWidth = 0
			return true, nil
		}
	case "esc":
		m.mention.open = false
		m.mention.err = ""
		m.layoutWidth = 0
		return true, nil
	case "enter":
		if m.mentionActive() {
			m.input = []rune(completeMention(string(m.input), m.mention.matches[m.mention.cursor]))
			m.edit.endCaret(m.input)
			m.mention.open = false
			m.layoutWidth = 0
			return true, nil
		}
	}
	return false, nil
}

// mentionLines renders as many matches as fit without hiding the transcript,
// editable draft, or fixed status rows.
func (m *ui) mentionLines() []string {
	if !m.mention.open {
		return nil
	}
	style := m.composerStyle
	if m.width <= minWidth && (style == "bordered" || style == "chatter") {
		style = "minimal"
	}
	fixed := 1 // one blank line above every composer
	if style == "minimal" {
		fixed = 2
	} else if style == "bordered" {
		fixed = 3
	}
	available := max(0, m.height-len(m.header())-m.statusLineHeight()-fixed-1-1)
	if available == 0 {
		return nil
	}
	if m.mention.err != "" {
		return []string{m.theme.Warning.Render(fit("@ "+m.mention.err, m.width))}
	}
	if len(m.mention.matches) == 0 {
		return []string{m.theme.Help.Render(fit("@ no matching files", m.width))}
	}
	visible := min(5, len(m.mention.matches), available)
	start := max(0, min(m.mention.cursor-visible+1, len(m.mention.matches)-visible))
	rows := make([]string, 0, visible+1)
	for i := start; i < start+visible; i++ {
		match := m.mention.matches[i]
		if i == m.mention.cursor {
			rows = append(rows, m.theme.Selected.Render(fit("> "+match, m.width)))
		} else {
			rows = append(rows, m.theme.Help.Render(fit("  "+match, m.width)))
		}
	}
	// Shared popup hint, only while the popup budget has a spare row: the
	// same keys drive the / command popup.
	if len(rows) < available {
		rows = append(rows, m.theme.Help.Render(fit("  ↑/↓ select  Tab complete  Esc dismiss", m.width)))
	}
	return rows
}
