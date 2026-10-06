// Context windows (specs/model-metadata): a model's prompt budget comes
// from the user's override, then the provider's live /models metadata, then
// the bundled catalog's row for the exact (provider, model) pair (its input
// limit when known, else its context window). Unknown pairs resolve to
// ok=false so the UI renders "ctx —" instead of a fabricated size. The shared
// TokenUsage totals type is also defined here.
package model

import (
	"strings"

	"likha/internal/model/catalog"
)

// TokenUsage holds the final prompt/completion token totals of one
// response, as reported by the provider in the stream's usage event.
type TokenUsage struct {
	Prompt     int64
	Completion int64
	PromptSeen bool // true only when the response reported an input/prompt count
}

// ResolveContextWindow resolves a context limit for the selected provider and
// model. A positive user override takes precedence over positive provider
// metadata, followed by the bundled catalog's row for the exact pair.
func ResolveContextWindow(providerID, modelID string, override, metadata int64) (int64, bool) {
	if override > 0 {
		return override, true
	}
	if metadata > 0 {
		return metadata, true
	}
	entry, ok := catalog.Lookup(strings.TrimSpace(providerID), strings.TrimSpace(modelID))
	if !ok || entry.PromptLimit() <= 0 {
		return 0, false
	}
	return entry.PromptLimit(), true
}
