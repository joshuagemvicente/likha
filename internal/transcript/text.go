package transcript

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Shared text rules for every surface in this package, so the diff, task,
// reasoning, and footer lines escape, flatten, clip, and format durations the
// same way the rest of the transcript does (internal/tui hiddenReviewRune,
// toolHeaderText).

// hiddenRune reports runes that never reach the terminal verbatim: controls,
// format (Cf, including bidi overrides), and anything with zero cell width.
// It matches internal/tui's hiddenReviewRune.
func hiddenRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || runewidth.RuneWidth(r) == 0
}

// escapeRune is the visible form of a hidden rune: `\uXXXX`.
func escapeRune(r rune) string {
	return fmt.Sprintf("\\u%04X", r)
}

// separatorRune reports white space that may become an ordinary space: line
// breaks and tabs, plus visible Unicode spaces such as NBSP. Hidden spacing
// runes (\v, \f, NEL, U+2028/U+2029) are escaped instead, like any other
// control, so they keep the transcript's existing escaped rendering.
func separatorRune(r rune) bool {
	return r == '\n' || r == '\r' || r == '\t' || unicode.IsSpace(r) && !hiddenRune(r)
}

// flattenRow puts untrusted text on one row: separator runs collapse to one
// space, leading and trailing space is dropped, and hidden runes show as
// `\uXXXX`, never stripped. It matches internal/tui's toolHeaderText.
func flattenRow(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case separatorRune(r):
			b.WriteByte(' ')
		case hiddenRune(r):
			b.WriteString(escapeRune(r))
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// clipRight bounds already-neutralized text to width cells, keeping the head
// and ending in ellipsis when anything was cut. Space before the ellipsis is
// trimmed. When the ellipsis alone is wider than width the text is cut
// without it. A wide rune that would straddle the edge is dropped whole.
// width <= 0 returns "".
func clipRight(text string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(text) <= width {
		return text
	}
	tail := clipEllipsis(width, ellipsis)
	room := width - runewidth.StringWidth(tail)
	var b strings.Builder
	used := 0
	for _, r := range text {
		size := runewidth.RuneWidth(r)
		if used+size > room {
			break
		}
		b.WriteRune(r)
		used += size
	}
	return strings.TrimRight(b.String(), " ") + tail
}

// clipLeft is clipRight keeping the tail: ellipsis + the last cells, so a
// path keeps its file name.
func clipLeft(text string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(text) <= width {
		return text
	}
	head := clipEllipsis(width, ellipsis)
	room := width - runewidth.StringWidth(head)
	runes := []rune(text)
	start, used := len(runes), 0
	for start > 0 {
		size := runewidth.RuneWidth(runes[start-1])
		if used+size > room {
			break
		}
		start--
		used += size
	}
	return head + strings.TrimLeft(string(runes[start:]), " ")
}

// clipEllipsis is the ellipsis to append when cutting to width: the ellipsis
// itself when it fits, otherwise nothing.
func clipEllipsis(width int, ellipsis string) string {
	if runewidth.StringWidth(ellipsis) > width {
		return ""
	}
	return ellipsis
}

// wholeDuration formats a non-negative whole-second count the way every
// transcript surface does: "42s", "1m 4s", "1h 2m" (seconds dropped from an
// hour on).
func wholeDuration(secs int64) string {
	secs = max(secs, 0)
	switch {
	case secs >= 3600:
		return fmt.Sprintf("%dh %dm", secs/3600, secs%3600/60)
	case secs >= 60:
		return fmt.Sprintf("%dm %ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%ds", secs)
}
