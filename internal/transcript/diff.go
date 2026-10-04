package transcript

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// DefaultDiffLines is the inline diff-line cap for an edit item (spec
// transcript-redesign § Diffs).
const DefaultDiffLines = 10

// DiffLine is one line of an edit's change. Number is the new-file line
// number for '+' and ' ' lines and the old-file line number for '-' lines;
// 0 means the number is unknown (an argument-derived diff, see
// EditFromArguments). Text is raw file text: FormatDiffLine neutralizes it.
type DiffLine struct {
	Number int
	Sign   byte // '+', '-', or ' '
	Text   string
}

// FileDiff is one file's change in the order the review showed it. Lines may
// include context lines (Sign ' '); SummarizeEdit shows only '+'/'-' lines.
type FileDiff struct {
	Path    string
	Created bool
	Lines   []DiffLine
}

// DiffSummary is the ⎿ content of an edit item.
//
//   - Summary is the outcome sentence ("Updated a.go with 2 additions and 1
//     removal", "Updated 3 files with …", "Created a.go (4 lines)"). Paths in
//     it are flattened to one row with hidden runes escaped; the caller clips
//     it to the row width.
//   - Lines are up to maxLines changed lines of the first file.
//   - More counts the first file's changed lines beyond Lines, for
//     "… +N lines (ctrl+o to expand)".
//   - OtherFiles names the remaining files of a multi-file edit (flattened
//     like Summary), in review order.
//   - Created reports a single-file edit that created its file (the
//     "Created p" sentence), so the item header can read Create(p).
type DiffSummary struct {
	Summary    string
	Lines      []DiffLine
	More       int
	OtherFiles []string
	Created    bool
}

// NumberWidth is the digit count of the widest line number in s.Lines, the
// numberWidth to pass to FormatDiffLine so the sign column aligns. It is 0
// when no line carries a known number.
func (s DiffSummary) NumberWidth() int {
	width := 0
	for _, line := range s.Lines {
		if line.Number > 0 {
			width = max(width, len(strconv.Itoa(line.Number)))
		}
	}
	return width
}

// SummarizeEdit builds the edit item's summary from the files of one applied
// edit (ParseUnifiedDiff or EditFromArguments). It returns the zero
// DiffSummary for no files; the caller keeps its generic result line then.
// maxLines bounds Lines (DefaultDiffLines per the spec; negative counts as 0).
func SummarizeEdit(files []FileDiff, maxLines int) DiffSummary {
	if len(files) == 0 {
		return DiffSummary{}
	}
	additions, removals := 0, 0
	for _, file := range files {
		for _, line := range file.Lines {
			switch line.Sign {
			case '+':
				additions++
			case '-':
				removals++
			}
		}
	}
	first := files[0]
	var changed []DiffLine
	for _, line := range first.Lines {
		if line.Sign == '+' || line.Sign == '-' {
			changed = append(changed, line)
		}
	}
	shown := min(max(maxLines, 0), len(changed))
	summary := DiffSummary{More: len(changed) - shown}
	if shown > 0 {
		summary.Lines = append([]DiffLine(nil), changed[:shown]...)
	}
	for _, file := range files[1:] {
		summary.OtherFiles = append(summary.OtherFiles, diffPathText(file.Path))
	}
	switch {
	case len(files) > 1:
		summary.Summary = "Updated " + strconv.Itoa(len(files)) + " files" + diffWith(additions, removals)
	case first.Created:
		summary.Created = true
		summary.Summary = "Created " + diffPathText(first.Path) + " (" + diffCount(additions, "line", "lines") + ")"
	default:
		summary.Summary = "Updated " + diffPathText(first.Path) + diffWith(additions, removals)
	}
	return summary
}

// diffWith is " with N additions and M removals", omitting a zero side and
// returning "" when both are zero.
func diffWith(additions, removals int) string {
	var parts []string
	if additions > 0 {
		parts = append(parts, diffCount(additions, "addition", "additions"))
	}
	if removals > 0 {
		parts = append(parts, diffCount(removals, "removal", "removals"))
	}
	if len(parts) == 0 {
		return ""
	}
	return " with " + strings.Join(parts, " and ")
}

func diffCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// diffPathText flattens a path onto one row like a tool header shows it
// (flattenRow): white space collapses and hidden runes are escaped.
func diffPathText(path string) string {
	return flattenRow(path)
}

// ParseUnifiedDiff reads the unified diff that internal/actions produces for
// an edit review (one "--- a/p" / "+++ b/p" / "@@" section per file; "---
// /dev/null" marks a created file). Text before the first section is skipped,
// so the whole approval body of the edit tool ("Affected files: …" then the
// diff) can be passed as is. Hunk bodies are consumed by their header counts,
// so a removed line that itself starts with "-- " is never taken for a file
// header. A malformed hunk ends that file's lines; earlier lines are kept.
func ParseUnifiedDiff(text string) []FileDiff {
	lines := strings.Split(text, "\n")
	var files []FileDiff
	for i := 0; i < len(lines); {
		if i+2 >= len(lines) || !strings.HasPrefix(lines[i], "--- ") || !strings.HasPrefix(lines[i+1], "+++ ") {
			i++
			continue
		}
		if _, _, _, _, ok := diffHunkHeader(lines[i+2]); !ok {
			i++
			continue
		}
		oldName := strings.TrimPrefix(lines[i], "--- ")
		newName := strings.TrimPrefix(lines[i+1], "+++ ")
		file := FileDiff{Created: oldName == "/dev/null", Path: strings.TrimPrefix(newName, "b/")}
		if newName == "/dev/null" {
			file.Path = strings.TrimPrefix(oldName, "a/")
		}
		i += 2
	hunks:
		for i < len(lines) {
			oldAt, oldLeft, newAt, newLeft, ok := diffHunkHeader(lines[i])
			if !ok {
				break
			}
			i++
			for i < len(lines) && (oldLeft > 0 || newLeft > 0) {
				line := lines[i]
				if line == "" {
					break hunks
				}
				switch line[0] {
				case ' ':
					if oldLeft == 0 || newLeft == 0 {
						break hunks
					}
					file.Lines = append(file.Lines, DiffLine{Number: newAt, Sign: ' ', Text: line[1:]})
					oldAt, newAt, oldLeft, newLeft = oldAt+1, newAt+1, oldLeft-1, newLeft-1
				case '-':
					if oldLeft == 0 {
						break hunks
					}
					file.Lines = append(file.Lines, DiffLine{Number: oldAt, Sign: '-', Text: line[1:]})
					oldAt, oldLeft = oldAt+1, oldLeft-1
				case '+':
					if newLeft == 0 {
						break hunks
					}
					file.Lines = append(file.Lines, DiffLine{Number: newAt, Sign: '+', Text: line[1:]})
					newAt, newLeft = newAt+1, newLeft-1
				case '\\':
					// "\ No newline at end of file" annotates the previous line.
				default:
					break hunks
				}
				i++
			}
			for i < len(lines) && strings.HasPrefix(lines[i], "\\") {
				i++
			}
		}
		files = append(files, file)
	}
	return files
}

// diffHunkHeader parses "@@ -a,b +c,d @@" (a count may be omitted, meaning 1).
func diffHunkHeader(line string) (oldAt, oldCount, newAt, newCount int, ok bool) {
	rest, found := strings.CutPrefix(line, "@@ -")
	if !found {
		return 0, 0, 0, 0, false
	}
	ranges, _, found := strings.Cut(rest, " @@")
	if !found {
		return 0, 0, 0, 0, false
	}
	oldRange, newRange, found := strings.Cut(ranges, " +")
	if !found {
		return 0, 0, 0, 0, false
	}
	oldAt, oldCount, ok = diffRange(oldRange)
	if !ok {
		return 0, 0, 0, 0, false
	}
	newAt, newCount, ok = diffRange(newRange)
	if !ok {
		return 0, 0, 0, 0, false
	}
	return oldAt, oldCount, newAt, newCount, true
}

func diffRange(text string) (at, count int, ok bool) {
	start, length, hasCount := strings.Cut(text, ",")
	at, err := strconv.Atoi(start)
	if err != nil || at < 0 {
		return 0, 0, false
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(length); err != nil || count < 0 {
			return 0, 0, false
		}
	}
	return at, count, true
}

// EditFromArguments reconstructs an edit's files from the persisted tool-call
// arguments when the reviewed diff is not available (resumed sessions). Only
// the "edit" tool qualifies: its operations carry both sides of every change.
// Created files are exact (numbered 1…N). Replacements are line diffs of
// old_text against new_text with Number 0, because the original file is not
// retained; when old_text or new_text covers part of a line the counts can
// differ from the reviewed diff. Paths are cleaned and sorted the way the
// review orders them. "edit_file" arguments lack the original content, so it
// and every other tool return ok=false.
func EditFromArguments(name, arguments string) (files []FileDiff, ok bool) {
	if name != "edit" {
		return nil, false
	}
	var args struct {
		Operations []struct {
			Kind    string `json:"kind"`
			Path    string `json:"path"`
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
			Content string `json:"content"`
		} `json:"operations"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil || len(args.Operations) == 0 {
		return nil, false
	}
	byPath := make(map[string]*FileDiff)
	var paths []string
	for _, op := range args.Operations {
		if op.Path == "" {
			return nil, false
		}
		path := filepath.Clean(op.Path)
		file := byPath[path]
		if file == nil {
			file = &FileDiff{Path: path}
			byPath[path] = file
			paths = append(paths, path)
		}
		switch op.Kind {
		case "create":
			file.Created = true
			for n, text := range diffSplitLines(op.Content) {
				file.Lines = append(file.Lines, DiffLine{Number: n + 1, Sign: '+', Text: text})
			}
		case "replace":
			file.Lines = append(file.Lines, diffTextLines(diffSplitLines(op.OldText), diffSplitLines(op.NewText))...)
		default:
			return nil, false
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		files = append(files, *byPath[path])
	}
	return files, true
}

// diffSplitLines splits like a unified diff: a final newline ends the last
// line rather than starting an empty one.
func diffSplitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLCSCells bounds the quadratic line matching for argument-derived diffs;
// larger changes report every line of the differing middle as changed.
const diffLCSCells = 1 << 18

// diffTextLines is a line diff with unknown numbers: common leading and
// trailing lines are dropped, the middle is matched by longest common
// subsequence, and each changed run lists removals before additions (the
// order internal/actions writes hunks in).
func diffTextLines(old, next []string) []DiffLine {
	prefix := 0
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(next)-prefix && old[len(old)-1-suffix] == next[len(next)-1-suffix] {
		suffix++
	}
	a, b := old[prefix:len(old)-suffix], next[prefix:len(next)-suffix]
	var out, removed, added []DiffLine
	flush := func() {
		out = append(out, removed...)
		out = append(out, added...)
		removed, added = removed[:0], added[:0]
	}
	if len(a)*len(b) > diffLCSCells {
		for _, text := range a {
			removed = append(removed, DiffLine{Sign: '-', Text: text})
		}
		for _, text := range b {
			added = append(added, DiffLine{Sign: '+', Text: text})
		}
		flush()
		return out
	}
	// common[i*(m+1)+j] is the LCS length of a[i:] and b[j:].
	n, m := len(a), len(b)
	common := make([]int32, (n+1)*(m+1))
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				common[i*(m+1)+j] = common[(i+1)*(m+1)+j+1] + 1
			} else {
				common[i*(m+1)+j] = max(common[(i+1)*(m+1)+j], common[i*(m+1)+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			flush()
			i, j = i+1, j+1
		case common[(i+1)*(m+1)+j] >= common[i*(m+1)+j+1]:
			removed = append(removed, DiffLine{Sign: '-', Text: a[i]})
			i++
		default:
			added = append(added, DiffLine{Sign: '+', Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		removed = append(removed, DiffLine{Sign: '-', Text: a[i]})
	}
	for ; j < m; j++ {
		added = append(added, DiffLine{Sign: '+', Text: b[j]})
	}
	flush()
	return out
}

// FormatDiffLine renders one diff line as "NN - text" / "NN + text" (a
// context line has a blank sign). The number is right-aligned to numberWidth
// (widened if the number needs more digits); an unknown number (0) leaves
// the column blank, or omits it when numberWidth is 0. Tabs become 4 spaces
// and hidden runes (controls, format and zero-width runes) are escaped as
// \uXXXX. The row is cut on whole runes/escapes and ends with ellipsis when
// anything was cut; it never exceeds width cells (an ellipsis wider than the
// width is dropped). width <= 0 returns "".
func FormatDiffLine(l DiffLine, numberWidth, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	sign := l.Sign
	if sign != '+' && sign != '-' {
		sign = ' '
	}
	var prefix string
	switch {
	case l.Number > 0:
		number := strconv.Itoa(l.Number)
		prefix = strings.Repeat(" ", max(numberWidth-len(number), 0)) + number + " "
	case numberWidth > 0:
		prefix = strings.Repeat(" ", numberWidth) + " "
	}
	prefix += string(sign) + " "

	units := make([]string, 0, len(prefix)+len(l.Text))
	for _, r := range prefix {
		units = append(units, string(r))
	}
	for _, r := range l.Text {
		switch {
		case r == '\t':
			units = append(units, "    ")
		case hiddenRune(r):
			units = append(units, escapeRune(r))
		default:
			units = append(units, string(r))
		}
	}
	total := 0
	for _, unit := range units {
		total += runewidth.StringWidth(unit)
	}
	if total <= width {
		return strings.Join(units, "")
	}
	tail := clipEllipsis(width, ellipsis)
	room := width - runewidth.StringWidth(tail)
	var b strings.Builder
	used := 0
	for _, unit := range units {
		cells := runewidth.StringWidth(unit)
		if used+cells > room {
			break
		}
		b.WriteString(unit)
		used += cells
	}
	return b.String() + tail
}
