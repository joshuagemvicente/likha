package webtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// tavilySearchEndpoint issues ordinary web results only, never Tavily's
	// generated answers or deeper search modes. No endpoint override is
	// accepted.
	tavilySearchEndpoint = "https://api.tavily.com/search"

	tavilyBackend = "tavily"
)

// tavilyClient never follows redirects: ErrUseLastResponse returns the first
// response and no authentication header is ever replayed onto a successor.
// It mirrors braveClient.
var tavilyClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// SearchTavily runs one Tavily web search with the given API key, mirroring
// the Brave Search adapter's validation, sanitization, and error handling. The
// key is sent only in the Authorization: Bearer header of the single,
// non-redirected POST and never appears in an error or log. Query is trimmed
// and bounded to MaxQueryBytes; limit is 1..MaxSearchLimit, defaulting to
// DefaultSearchLimit. The request body pins "search_depth": "basic" and
// "include_answer": false so Tavily returns ordinary web results only — never
// a generated answer or a deeper search mode — matching the Brave adapter's
// results-only contract. Backend order is preserved, the "content" field is
// returned as the snippet only when Tavily supplies it (never invented), all
// returned strings have control characters stripped, and result URLs are not
// fetched. Failures are returned as safe errors: 401/403 → the key was
// rejected, 429 → rate limited with a retry-after note, redirects and other
// statuses → descriptive errors, malformed JSON → a parse error. Elapsed is
// set on every return path, including validation and transport failures.
//
// The request and response shapes follow Tavily's published API reference but
// are unverified against the live service; adjust this adapter only after a
// live probe confirms the wire behavior.
func SearchTavily(ctx context.Context, key string, req SearchRequest) (SearchOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	outcome := SearchOutcome{Backend: tavilyBackend, Hits: []SearchHit{}}
	started := time.Now()
	finish := func(err error) (SearchOutcome, error) {
		outcome.Elapsed = time.Since(started)
		return outcome, err
	}

	query, limit, err := validateSearchRequest(req)
	if err != nil {
		return finish(err)
	}
	outcome.Query = query

	if strings.TrimSpace(key) == "" {
		return finish(errors.New("tavily web search requires an API key"))
	}

	payload, err := json.Marshal(struct {
		Query         string `json:"query"`
		MaxResults    int    `json:"max_results"`
		SearchDepth   string `json:"search_depth"`
		IncludeAnswer bool   `json:"include_answer"`
	}{
		Query:         query,
		MaxResults:    limit,
		SearchDepth:   "basic",
		IncludeAnswer: false,
	})
	if err != nil {
		return finish(fmt.Errorf("tavily web search request: %w", err))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilySearchEndpoint, bytes.NewReader(payload))
	if err != nil {
		return finish(fmt.Errorf("tavily web search request: %w", err))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "likha")
	// Enforce the per-request cap even when the caller's context would allow
	// longer; an existing earlier deadline keeps precedence.
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > searchTimeout {
		timeoutCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		defer cancel()
		request = request.WithContext(timeoutCtx)
	}

	response, err := tavilyClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("tavily web search request failed: %w", err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode >= 300 && response.StatusCode < 400:
		// The redirect response's location text is untrusted; naming only the
		// status keeps it out of the message.
		return finish(fmt.Errorf("tavily web search returned an HTTP %d redirect; search redirects are never followed", response.StatusCode))
	case response.StatusCode == http.StatusUnauthorized:
		return finish(errors.New("tavily web search returned HTTP 401 unauthorized; the configured API key was rejected"))
	case response.StatusCode == http.StatusForbidden:
		return finish(errors.New("tavily web search returned HTTP 403 forbidden; the configured API key was rejected or lacks access"))
	case response.StatusCode == http.StatusTooManyRequests:
		note := "Tavily rate-limited this search (HTTP 429)"
		if after := sanitizeText(response.Header.Get("Retry-After")); after != "" {
			note += "; Retry-After: " + truncateText(after, 64)
		}
		outcome.Notes = append(outcome.Notes, note)
		return finish(errors.New(note))
	default:
		return finish(fmt.Errorf("tavily web search returned HTTP %d; retry later or verify availability", response.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("reading the tavily web search response: %w", err))
	}
	if len(body) > maxSearchResponseBytes {
		return finish(fmt.Errorf("tavily web search response exceeded %d bytes", maxSearchResponseBytes))
	}

	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
		// ResponseTime is seconds and may be absent; a pointer distinguishes
		// "absent" from an explicit zero so the note never invents a value.
		ResponseTime *float64 `json:"response_time"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return finish(fmt.Errorf("tavily web search response was not the expected JSON: %w", err))
	}
	if parsed.ResponseTime != nil {
		outcome.Notes = append(outcome.Notes, fmt.Sprintf("Tavily reported a response time of %.2f seconds.", *parsed.ResponseTime))
	}
	if len(parsed.Results) == 0 {
		outcome.Notes = append(outcome.Notes, "Tavily returned no results for this query; treat the search as empty.")
		return finish(nil)
	}
	for _, result := range parsed.Results {
		if len(outcome.Hits) == limit {
			outcome.Truncated = true
			outcome.Notes = append(outcome.Notes, "Tavily returned more results than requested; the extra results were dropped.")
			break
		}
		outcome.Hits = append(outcome.Hits, SearchHit{
			Rank:    len(outcome.Hits) + 1,
			Title:   sanitizeText(result.Title),
			URL:     sanitizeText(result.URL),
			Snippet: sanitizeText(result.Content),
		})
	}
	return finish(nil)
}
