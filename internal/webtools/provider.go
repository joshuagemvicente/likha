// provider.go adds the multi-provider search registry to the webtools
// package. Brave remains the reference client in search.go; the other
// backends are thin adapters (tavily.go, exa.go, duckduckgo.go) sharing the
// same SearchRequest/SearchOutcome contract, validation, sanitization, and
// consent vocabulary. Adding a provider is additive: one name in
// SupportedBackends, one adapter file exposing the SearchFunc signature, and
// one key source (environment variable plus tool-keys.json entry) in
// config.go. No existing backend changes, and there is never a vendor
// fallback: a failing backend is surfaced, never silently retried elsewhere.
package webtools

import (
	"context"
	"fmt"
	"strings"
)

// Backend names for the search registry. braveBackend is defined in
// config.go beside the key-source constants it shares; tavilyBackend and
// exaBackend are defined in their adapter files. duckduckgoBackend lives here
// so duckduckgo.go can reference it instead of redeclaring it.
const (
	duckduckgoBackend = "duckduckgo"
)

// SupportedBackends lists every search backend name accepted in
// web.search.backend, sorted. Adding a provider is additive: append the name
// here, add an adapter file exposing the SearchFunc signature, and add a key
// source (environment variable plus tool-keys.json entry) in config.go; no
// existing backend changes and no vendor fallback is introduced.
var SupportedBackends = []string{braveBackend, tavilyBackend, exaBackend, duckduckgoBackend}

// supportedBackend reports whether the named search backend is listed in
// SupportedBackends.
func supportedBackend(backend string) bool {
	for _, name := range SupportedBackends {
		if name == backend {
			return true
		}
	}
	return false
}

// unsupportedBackendError builds the shared error for a backend name outside
// SupportedBackends, naming the value and the supported list.
func unsupportedBackendError(backend string) error {
	return fmt.Errorf("unsupported search backend %q; supported backends are: %s",
		backend, strings.Join(SupportedBackends, ", "))
}

// SearchFunc performs one search against a single backend. key is the
// backend-specific credential as resolved by LoadSearchKey (empty for
// DuckDuckGo, which needs none); req is the shared bounded request shape.
// Implementations validate and sanitize provider strings and never echo the
// key in an error or log.
type SearchFunc func(ctx context.Context, key string, req SearchRequest) (SearchOutcome, error)

// SearchWithBackend dispatches one search to the named backend: "brave" runs
// the existing Search client and requires a key; "tavily", "exa", and
// "duckduckgo" run the SearchTavily, SearchExa, and SearchDuckDuckGo adapters
// in tavily.go, exa.go, and duckduckgo.go respectively, with the DuckDuckGo
// key ignored (LoadSearchKey returns "" for it). An unknown backend fails
// with an error naming it and the supported list. There is no fallback: a
// failing backend is never silently retried on another vendor.
func SearchWithBackend(ctx context.Context, backend, key string, req SearchRequest) (SearchOutcome, error) {
	switch backend {
	case braveBackend:
		return Search(ctx, key, req)
	case tavilyBackend:
		return SearchTavily(ctx, key, req)
	case exaBackend:
		return SearchExa(ctx, key, req)
	case duckduckgoBackend:
		return SearchDuckDuckGo(ctx, key, req)
	default:
		return SearchOutcome{}, unsupportedBackendError(backend)
	}
}

// BackendPrivacyCopy returns the first-use consent privacy copy for one
// backend. Every copy states that the provider receives the query and that
// model-written queries can contain project details and need user inspection
// before allowing. Brave keeps the existing BraveDefaultRetention framing;
// Tavily and Exa defer retention and use to each provider's privacy policy;
// DuckDuckGo discloses the unofficial HTML endpoint and makes no retention or
// handling promises. An unknown backend returns "".
func BackendPrivacyCopy(backend string) string {
	switch backend {
	case braveBackend:
		return BraveDefaultRetention +
			". Brave receives the query and request metadata; selected results reach the configured model provider as untrusted excerpts. " +
			"Model-written queries can contain project details — inspect the query before allowing."
	case tavilyBackend:
		return "Tavily receives the query and request metadata; retention and use follow Tavily's privacy policy. " +
			"Selected results reach the configured model provider as untrusted excerpts. " +
			"Model-written queries can contain project details — inspect the query before allowing."
	case exaBackend:
		return "Exa receives the query and request metadata; retention and use follow Exa's privacy policy. " +
			"Selected results reach the configured model provider as untrusted excerpts. " +
			"Model-written queries can contain project details — inspect the query before allowing."
	case duckduckgoBackend:
		return "This adapter uses DuckDuckGo's unofficial HTML endpoint: it receives the query, may change markup or block automated use at any time, and its automated use sits outside a sanctioned API agreement. " +
			"No retention or handling promises are made for that endpoint. " +
			"Selected results reach the configured model provider as untrusted excerpts. " +
			"Model-written queries can contain project details — inspect the query before allowing."
	default:
		return ""
	}
}
