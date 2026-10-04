package transcript

import (
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// ThoughtMarker returns the collapsed reasoning marker text (spec
// transcript-redesign § Reasoning) without its glyph; the caller places the
// `✻` / `~` gutter and the Muted style.
//
// Formats:
//   - known == false (a resumed entry saved before durations were recorded,
//     or a stream whose start was never observed): "Thought".
//   - d < 1s (including zero and negative): "Thought for <1s".
//   - d < 1m: "Thought for Ns", whole seconds truncated ("Thought for 4s").
//   - d < 1h: "Thought for Nm Ss" ("Thought for 1m 12s", "Thought for 2m 0s").
//   - otherwise: "Thought for Nh Mm", seconds dropped.
//
// The minute and hour shapes match TurnDuration's so the footer and the
// marker read alike; seconds truncate rather than round so a marker never
// claims more thinking time than was measured.
func ThoughtMarker(d time.Duration, known bool) string {
	if !known {
		return "Thought"
	}
	return "Thought for " + reasoningDuration(d)
}

// reasoningDuration formats d for ThoughtMarker: whole seconds, truncated,
// in wholeDuration's shared shape.
func reasoningDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	return wholeDuration(int64(d / time.Second))
}

// Tail returns the last n non-empty rows of reasoning text word-wrapped to
// width, for the live `✻ Thinking…` view (spec § Reasoning: the last 3
// lines), and whether earlier non-empty rows were left out.
//
// Contract:
//   - width < 1 is treated as 1. n <= 0 returns no rows; omitted then
//     reports whether the text has any visible content at all.
//   - "\r\n" and lone "\r" end a line like "\n". Within a line, runs of
//     visible white space (spaces, tabs, NBSP, …; see separatorRune)
//     collapse to one separator and leading/trailing white space is
//     dropped, so blank and whitespace-only lines produce no rows and no
//     row starts or ends with a space. Hidden spacing runes (\v, \f, NEL,
//     U+2028) are escaped like other controls, not treated as separators.
//     The tail is a glance at live progress, not a faithful copy; the
//     expanded marker renders the full text.
//   - Rows break between words. A word wider than width starts on the
//     current row when its first cell fits after a separating space,
//     otherwise on a fresh row, and breaks by cell width (go-runewidth),
//     never inside a wide rune.
//   - Control, format (Cf), and zero-width runes are shown as their escaped
//     `\uXXXX` text, matching the transcript's existing escaped rendering,
//     so no escape sequence from model output reaches the terminal. A rune
//     wider than width (a wide rune at width 1) is escaped the same way.
//   - Every returned row is non-empty and its display width is <= width.
//   - Only the trailing lines needed for n rows are wrapped, so calling Tail
//     on every streamed delta does not re-wrap the whole buffer.
func Tail(text string, width, n int) (rows []string, omitted bool) {
	width = max(width, 1)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if n <= 0 {
		for _, line := range lines {
			if reasoningVisible(line) {
				return nil, true
			}
		}
		return nil, false
	}
	i := len(lines) - 1
	for ; i >= 0 && len(rows) < n; i-- {
		wrapped := reasoningWrap(lines[i], width)
		if len(wrapped) == 0 {
			continue
		}
		rows = append(wrapped, rows...)
	}
	if len(rows) > n {
		rows = rows[len(rows)-n:]
		omitted = true
	}
	for ; i >= 0 && !omitted; i-- {
		omitted = reasoningVisible(lines[i])
	}
	return rows, omitted
}

// reasoningVisible reports whether line yields at least one row.
func reasoningVisible(line string) bool {
	return strings.IndexFunc(line, func(r rune) bool { return !separatorRune(r) }) >= 0
}

// reasoningCell is one terminal cell group: a visible rune or one character
// of an escaped hidden rune.
type reasoningCell struct {
	r     rune
	width int
}

// reasoningCells converts one word into cells, escaping hidden runes and
// runes wider than width.
func reasoningCells(word string, width int) []reasoningCell {
	cells := make([]reasoningCell, 0, len(word))
	for _, r := range word {
		size := runewidth.RuneWidth(r)
		if hiddenRune(r) || size > width {
			for _, c := range escapeRune(r) {
				cells = append(cells, reasoningCell{c, 1})
			}
			continue
		}
		cells = append(cells, reasoningCell{r, size})
	}
	return cells
}

// reasoningWrap word-wraps one '\n'-free line into rows of at most width
// cells. A line of only white space yields no rows.
func reasoningWrap(line string, width int) []string {
	var rows []string
	var b strings.Builder
	cols := 0
	flush := func() {
		if cols > 0 {
			rows = append(rows, b.String())
		}
		b.Reset()
		cols = 0
	}
	put := func(c reasoningCell) {
		b.WriteRune(c.r)
		cols += c.width
	}
	for _, word := range strings.FieldsFunc(line, separatorRune) {
		cells := reasoningCells(word, width)
		size := 0
		for _, c := range cells {
			size += c.width
		}
		switch {
		case cols == 0 && size <= width:
		case cols > 0 && cols+1+size <= width:
			put(reasoningCell{' ', 1})
		case size <= width:
			flush()
		case cols > 0 && cols+1+cells[0].width <= width:
			// Wider than any row: fill the current row first.
			put(reasoningCell{' ', 1})
		default:
			flush()
		}
		for _, c := range cells {
			if cols+c.width > width {
				flush()
			}
			put(c)
		}
	}
	flush()
	return rows
}
