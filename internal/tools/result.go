package tools

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const inlineLimitWarning = "Only a limited inline result is shown; do not infer exhaustive results."

// ModelContent preserves historical plain successful content when it carries
// no completeness/continuation metadata. Other results use the Result JSON
// envelope. The complete returned string, including escaped JSON and metadata,
// never exceeds MaxInlineBytes. It does not mutate the retained Result.
func (r Result) ModelContent() string {
	r = BoundResult(r)
	if plainResult(r) {
		return r.Content
	}
	encoded, _ := json.Marshal(r)
	return string(encoded)
}

// BoundResult creates the inline view, not the artifact-retention view. Retain
// the Invoke result first (subject to session artifact limits), attach its
// artifact ID/continuation, then use this helper or ModelContent. Failed,
// refused, and cancelled operation statuses survive clipping; successful
// incomplete results become limited.
func BoundResult(r Result) Result {
	r.Warnings = append([]string(nil), r.Warnings...)
	if r.Status == "" {
		r.Status = Succeeded
	} else if !validStatus(r.Status) {
		r.Status = Failed
		r.Warnings = append(r.Warnings, "The tool returned an invalid operation status.")
	}
	if !utf8.ValidString(r.Content) {
		r.Content = strings.ToValidUTF8(r.Content, "\uFFFD")
		r.Truncated = true
		r.Warnings = append(r.Warnings, "Invalid UTF-8 was replaced in the inline view.")
	}
	metadataLimited := false
	boundIdentity := func(s string) string {
		clean := strings.ToValidUTF8(s, "\uFFFD")
		if clean != s || len(clean) > 1024 {
			metadataLimited = true
			return utf8Prefix(clean, 1024)
		}
		return clean
	}
	r.Source.Kind = boundIdentity(r.Source.Kind)
	r.Source.Server = boundIdentity(r.Source.Server)
	r.Source.Tool = boundIdentity(r.Source.Tool)
	r.SourceTaskID = boundIdentity(r.SourceTaskID)
	// Never turn a clipped continuation identifier into a different valid ID.
	if !utf8.ValidString(r.ArtifactID) || len(r.ArtifactID) > 1024 {
		r.ArtifactID = ""
		metadataLimited = true
	}
	if !utf8.ValidString(r.Cursor) || len(r.Cursor) > 1024 {
		r.Cursor = ""
		metadataLimited = true
	}
	if r.NextOffset < 0 {
		r.NextOffset = 0
		metadataLimited = true
	}
	if len(r.Warnings) > 8 {
		r.Warnings = r.Warnings[:8]
		metadataLimited = true
	}
	for i, warning := range r.Warnings {
		clean := strings.ToValidUTF8(warning, "\uFFFD")
		if clean != warning || len(clean) > 512 {
			metadataLimited = true
			clean = utf8Prefix(clean, 512)
		}
		r.Warnings[i] = clean
	}
	if metadataLimited {
		r.Truncated = true
		r.Warnings = append(r.Warnings, "Inline metadata was limited; invalid or oversized continuation identifiers were omitted.")
	}
	if r.Truncated {
		markInlineLimited(&r)
	}
	if plainResult(r) && len(r.Content) <= MaxInlineBytes {
		return r
	}
	if len(r.Content) <= MaxInlineBytes {
		if encoded, _ := json.Marshal(r); len(encoded) <= MaxInlineBytes {
			return r
		}
	}
	r.Truncated = true
	markInlineLimited(&r)
	content := r.Content
	// Reserve the exact JSON overhead; escaping can expand a byte into six.
	// Binary search over source bytes, always snapping to a UTF-8 boundary.
	low, high := 0, len(content)
	if high > MaxInlineBytes {
		high = MaxInlineBytes
	}
	for low < high {
		mid := low + (high-low+1)/2
		r.Content = utf8Prefix(content, mid)
		if encoded, _ := json.Marshal(r); len(encoded) <= MaxInlineBytes {
			low = mid
		} else {
			high = mid - 1
		}
	}
	r.Content = utf8Prefix(content, low)
	// The caller owns artifact offsets/cursors: JSON escaping, handler clipping,
	// and invalid-UTF-8 replacement can all invalidate a guessed byte offset.
	return r
}

func plainResult(r Result) bool {
	return r.Status == Succeeded && !r.Truncated && len(r.Warnings) == 0 && r.ArtifactID == "" && r.NextOffset == 0 && r.Cursor == ""
}

func markInlineLimited(r *Result) {
	if r.Status == Succeeded {
		r.Status = Limited
	}
	for _, warning := range r.Warnings {
		if warning == inlineLimitWarning {
			return
		}
	}
	r.Warnings = append(r.Warnings, inlineLimitWarning)
}

func utf8Prefix(s string, limit int) string {
	if limit >= len(s) {
		return s
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
