package transcript

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// TurnRole is the session entry role that carries a turn footer (spec
// transcript-redesign § Persistence). Its content is EncodeTurn's JSON.
const TurnRole = "Turn"

// turnVersion is the schema version EncodeTurn writes. DecodeTurn accepts
// this and any later version, reading only the fields it knows.
const turnVersion = 1

// footerSeparator joins footer segments. The spec writes it as `·` for both
// glyph sets; tool headers already use it in ASCII mode.
const footerSeparator = " · "

// TurnInfo is what one finished assistant turn reports in its footer
// (spec transcript-redesign § Turn footer).
type TurnInfo struct {
	Model     string        // the model the turn ran on
	Duration  time.Duration // wall time from prompt submit to run end
	Tools     int           // top-level tool calls the turn showed as items
	Cancelled bool          // the user cancelled the run
	// Thoughts holds the measured duration of each reasoning block of the
	// turn, in transcript order (spec § Persistence: reasoning durations
	// persist inside the Turn entry). Nil when the turn had no reasoning or
	// a stored list was invalid.
	Thoughts []time.Duration
}

// turnJSON is the persisted shape. Field order is fixed by the struct, so
// EncodeTurn output is stable.
type turnJSON struct {
	V         *int   `json:"v"`
	Model     string `json:"model"`
	MS        int64  `json:"ms"`
	Tools     int    `json:"tools"`
	Cancelled bool   `json:"cancelled"`
	// ThoughtsMS is omitted when empty, so a turn without reasoning encodes
	// exactly as version 1 did before the field existed.
	ThoughtsMS []int64 `json:"thoughts_ms,omitempty"`
}

// EncodeTurn renders t as the content of a TurnRole entry:
// {"v":1,"model":"…","ms":12300,"tools":3,"cancelled":false}, plus
// "thoughts_ms":[…] when the turn had reasoning blocks. Durations round to
// whole milliseconds; negative durations and tool counts store as 0.
func EncodeTurn(t TurnInfo) string {
	v := turnVersion
	var thoughts []int64
	for _, d := range t.Thoughts {
		thoughts = append(thoughts, turnMS(d))
	}
	data, err := json.Marshal(turnJSON{V: &v, Model: t.Model, MS: turnMS(t.Duration), Tools: max(t.Tools, 0), Cancelled: t.Cancelled, ThoughtsMS: thoughts})
	if err != nil { // unreachable: every field marshals
		return fmt.Sprintf(`{"v":%d}`, turnVersion)
	}
	return string(data)
}

// turnMS is d in whole milliseconds, clamped at 0.
func turnMS(d time.Duration) int64 {
	return max(d.Round(time.Millisecond).Milliseconds(), 0)
}

// DecodeTurn parses a TurnRole entry's content. Unknown fields are ignored so
// later versions still load. ok is false for anything that is not a single
// JSON object with a version of at least 1 and non-negative, in-range counts.
// An invalid thoughts_ms list (a negative or out-of-range value) is dropped
// rather than failing the footer: Thoughts is then nil and the turn's
// reasoning reads as having no recorded duration.
func DecodeTurn(s string) (TurnInfo, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return TurnInfo{}, false
	}
	var raw turnJSON
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return TurnInfo{}, false
	}
	if raw.V == nil || *raw.V < 1 || raw.MS < 0 || raw.Tools < 0 || raw.MS > math.MaxInt64/int64(time.Millisecond) {
		return TurnInfo{}, false
	}
	info := TurnInfo{Model: raw.Model, Duration: time.Duration(raw.MS) * time.Millisecond, Tools: raw.Tools, Cancelled: raw.Cancelled}
	for _, ms := range raw.ThoughtsMS {
		if ms < 0 || ms > math.MaxInt64/int64(time.Millisecond) {
			info.Thoughts = nil
			break
		}
		info.Thoughts = append(info.Thoughts, time.Duration(ms)*time.Millisecond)
	}
	return info, true
}

// TurnDuration formats a turn's wall time: under a minute with one decimal
// ("8.2s", "12.3s" as in the spec's footer example), under an hour as
// minutes and seconds ("1m 4s"), and beyond that as hours and minutes
// ("1h 2m"). Values round to the shown precision before the band is chosen,
// so 59.96s reads "1m 0s", not "60.0s". Negative durations read "0.0s".
// The minute and hour shapes are wholeDuration's, shared with the thought
// marker and the task settle line.
func TurnDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	tenths := int64(d.Round(100*time.Millisecond) / (100 * time.Millisecond))
	if tenths < 600 {
		return fmt.Sprintf("%d.%ds", tenths/10, tenths%10)
	}
	secs := int64(d.Round(time.Second) / time.Second)
	if secs >= 3600 {
		secs = int64(d.Round(time.Minute)/time.Minute) * 60
	}
	return wholeDuration(secs)
}

// FooterText is the footer line for a finished turn, without its glyph or
// indent (the caller adds the gutter and Muted style):
//
//	nexum-router · 12.3s · 3 tools · cancelled
//
// The tools segment reads "1 tool" for one call and is omitted at zero;
// " · cancelled" appears only for cancelled turns; an empty model omits the
// model segment. When the line is wider than width cells, segments drop
// right-to-left (cancelled, then tools, then duration) and the last
// remaining segment truncates with ellipsis (clipRight: when ellipsis itself
// does not fit, the segment is cut to width without it). The model name is
// flattened to one row with hidden runes shown as \uXXXX (flattenRow), so
// content never emits escapes.
// The result never exceeds width cells; width <= 0 returns "".
func FooterText(t TurnInfo, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	var segments []string
	if model := flattenRow(t.Model); model != "" {
		segments = append(segments, model)
	}
	segments = append(segments, TurnDuration(t.Duration))
	switch {
	case t.Tools == 1:
		segments = append(segments, "1 tool")
	case t.Tools > 1:
		segments = append(segments, fmt.Sprintf("%d tools", t.Tools))
	}
	if t.Cancelled {
		segments = append(segments, "cancelled")
	}
	for n := len(segments); n > 1; n-- {
		if line := strings.Join(segments[:n], footerSeparator); runewidth.StringWidth(line) <= width {
			return line
		}
	}
	return clipRight(segments[0], width, ellipsis)
}
