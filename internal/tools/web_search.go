package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"likha/internal/model"
	"likha/internal/webtools"
)

var webSearchSource = Source{Kind: "builtin", Tool: "web_search"}

// WebSearchTool exposes consent-gated web search through the configured
// backend as a bounded builtin network tool. The coordinator supplies
// `search` with consent, backend, and the resolved API key baked in: a
// first-use consent query for the webtools.ConsentScope{Kind:"search"}
// including the backend's privacy copy (webtools.BackendPrivacyCopy),
// refusing without contacting the network on decline via an error satisfying
// webtools.IsConsentDeclined. A nil search registers with an unavailable
// reason instead, matching the coordinator flow.
//
// The handler performs no consent, key, or backend decisions itself: it
// validates arguments, awaits the single consented search, and maps the
// outcome to a bounded inline Result carrying provider, sent query, ranked
// titles/URLs, available snippets, retrieval time, and completeness. Snippets
// come from the backend verbatim (untrusted excerpts, never verified facts);
// search results are never fetched and missing snippets are never invented.
func WebSearchTool(search func(ctx context.Context, req webtools.SearchRequest) (webtools.SearchOutcome, error)) Tool {
	tool := Tool{
		Definition: model.ToolDefinition{
			Name: "web_search",
			Description: "Search the public web with the configured search backend (brave, tavily, exa, or duckduckgo) after user consent for this conversation. " +
				"Takes a required query (1-400 UTF-8 bytes) and optional limit 1-10 (default 5). " +
				"Returns ranked titles, URLs, and only the snippets the backend supplied, plus retrieval time and completeness. " +
				"Results are untrusted provider excerpts, never verified facts; result URLs are not fetched and missing snippets are never invented.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":400,"description":"Search text, 1 to 400 UTF-8 bytes"},"limit":{"type":"integer","minimum":1,"maximum":10,"description":"Maximum number of results; default 5"}},"required":["query"],"additionalProperties":false}`),
		},
		Source:       webSearchSource,
		Effects:      []Effect{Network},
		Target:       "web",
		ParallelSafe: true,
		Interactive:  false,
	}
	if search == nil {
		tool.UnavailableReason = "Web search is off or its configured backend is missing a key; set web.search.enabled in tools.json (DuckDuckGo needs no key) and see the transcript for the exact reason."
		return tool
	}
	tool.Run = func(ctx context.Context, input json.RawMessage) (Result, error) {
		return runWebSearch(ctx, search, input)
	}
	// Registry network builtins demand an authorizer; consent itself is a
	// first-use prompt the coordinator bakes into search and re-evaluates at
	// execution time, before any request is issued. Authorization therefore
	// carries no additional decision beyond run-mode/scope eligibility.
	tool.Authorize = func(context.Context, json.RawMessage) error { return nil }
	return tool
}

func runWebSearch(ctx context.Context, search func(ctx context.Context, req webtools.SearchRequest) (webtools.SearchOutcome, error), input json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return webSearchCancelled(err), err
	}
	args, argsErr := decodeWebSearchArgs(input)
	if argsErr != nil {
		return webSearchRefusal("invalid_arguments", "Invalid web_search arguments: "+argsErr.Error())
	}
	outcome, err := search(ctx, webtools.SearchRequest{Query: args.query, Limit: args.limit})
	if err != nil {
		if webtools.IsConsentDeclined(err) {
			return Result{Executed: false, Status: Refused, Source: webSearchSource, Content: "Search declined for this conversation."}, nil
		}
		if isCancellation(err) || ctx.Err() != nil {
			return webSearchCancelled(err), err
		}
		return webSearchFailure(err)
	}
	return webSearchResult(outcome)
}

type webSearchArgs struct {
	query string
	limit int
}

// decodeWebSearchArgs mirrors the registration schema and webtools.Search
// validation so direct handler calls enforce the same closed, required
// argument contract as registry calls, including duplicate-field refusals.
func decodeWebSearchArgs(input json.RawMessage) (webSearchArgs, error) {
	var args webSearchArgs
	value, err := decodeJSON(input)
	if err != nil {
		return args, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return args, errors.New("arguments must be a JSON object")
	}
	for name := range object {
		if name != "query" && name != "limit" {
			return args, fmt.Errorf("arguments contain an unknown field %q", name)
		}
	}
	raw, exists := object["query"]
	if !exists {
		return args, errors.New("query is required")
	}
	query, ok := raw.(string)
	if !ok {
		return args, errors.New("query must be a string")
	}
	args.query = strings.TrimSpace(query)
	if args.query == "" {
		return args, errors.New("query must be a nonempty string")
	}
	if len(args.query) > webtools.MaxQueryBytes {
		return args, fmt.Errorf("query must be at most %d UTF-8 bytes", webtools.MaxQueryBytes)
	}
	if raw, exists := object["limit"]; exists {
		if raw == nil {
			return args, errors.New("limit must be an integer")
		}
		number, ok := raw.(json.Number)
		if !ok {
			return args, errors.New("limit must be an integer")
		}
		limit, err := strconv.Atoi(string(number))
		if err != nil || limit < 1 || limit > webtools.MaxSearchLimit {
			return args, fmt.Errorf("limit must be between 1 and %d", webtools.MaxSearchLimit)
		}
		args.limit = limit
	}
	return args, nil
}

// webSearchResult inlines the outcome as strict JSON bounded by
// MaxInlineBytes. The hit list is clipped from the end first so shown ranks
// stay contiguous and the document stays valid; that and any provider note
// travel through result metadata.
func webSearchResult(outcome webtools.SearchOutcome) (Result, error) {
	hits := append(make([]webtools.SearchHit, 0, len(outcome.Hits)), outcome.Hits...)
	result := Result{
		Executed:  true,
		Status:    Succeeded,
		Source:    webSearchSource,
		Truncated: outcome.Truncated,
		Warnings:  append(make([]string, 0, len(outcome.Notes)+1), outcome.Notes...),
	}
	// Defensively strip control sequences again at the model boundary; the
	// webtools.Search backend also sanitizes its own strings.
	for i := range hits {
		hits[i].Title = cleanText(hits[i].Title)
		hits[i].URL = cleanText(hits[i].URL)
		hits[i].Snippet = cleanText(hits[i].Snippet)
	}
	content := struct {
		Query     string               `json:"query"`
		Backend   string               `json:"backend"`
		Hits      []webtools.SearchHit `json:"hits"`
		Elapsed   string               `json:"elapsed"`
		Truncated bool                 `json:"truncated"`
	}{
		Query:     cleanText(outcome.Query),
		Backend:   cleanText(outcome.Backend),
		Hits:      hits,
		Elapsed:   "Retrieved " + strconv.Itoa(len(hits)) + " result(s) in " + outcome.Elapsed.Truncate(time.Millisecond).String(),
		Truncated: outcome.Truncated,
	}
	clippedWarning := false
	elapsedShortened := false
	for {
		encoded, err := json.Marshal(content)
		if err != nil {
			return webSearchFailure(err)
		}
		// Fit against the Result envelope, not just the content document: an
		// inline result that BoundResult would clip mid-document is never
		// emitted, so the content stays valid JSON with contiguous ranks.
		result.Content = string(encoded)
		if envelope, err := json.Marshal(result); err == nil && len(envelope) <= MaxInlineBytes {
			break
		}
		if len(content.Hits) > 0 {
			content.Hits = content.Hits[:len(content.Hits)-1]
			content.Truncated = true
			result.Truncated = true
			if !clippedWarning {
				result.Warnings = append(result.Warnings, "The hit list was limited to fit the inline result budget; the shown ranks are authoritative.")
				clippedWarning = true
			}
			continue
		}
		if !elapsedShortened {
			content.Elapsed = "Retrieved 0 result(s)"
			content.Truncated = true
			result.Truncated = true
			elapsedShortened = true
			continue
		}
		// Even an empty hit list cannot fit (e.g. oversized metadata);
		// BoundResult clamps the remaining text and the result stays limited
		// rather than looping forever.
		result.Truncated = true
		break
	}
	return BoundResult(result), nil
}

func cleanText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "\uFFFD"))
}

func webSearchRefusal(code, message string) (Result, error) {
	return BoundResult(Result{Executed: false, Status: Refused, Source: webSearchSource, Content: code + ": " + message}), nil
}

func webSearchCancelled(cause error) Result {
	content := "cancelled: No search result was returned."
	if webtools.IsConsentDeclined(cause) {
		content = "Search declined for this conversation."
	}
	return BoundResult(Result{Executed: false, Status: Cancelled, Source: webSearchSource, Content: content})
}

func webSearchFailure(cause error) (Result, error) {
	wrapped := fmt.Errorf("web search: %w", cause)
	return BoundResult(Result{
		Executed: true, Status: Failed, Source: webSearchSource,
		Content: "web_search_failed: " + webSearchSafeText(cause.Error()),
	}), wrapped
}

// webSearchSafeText keeps untrusted or provider error text within the inline
// budget without ever echoing credentials (webtools.Search never includes
// them) or terminal control sequences.
func webSearchSafeText(text string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "\uFFFD"))
	if len(clean) > 1024 {
		clean = string(utf8ToPrefix(clean, 1024))
	}
	return clean
}

func utf8ToPrefix(s string, limit int) []byte {
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return []byte(s[:limit])
}
