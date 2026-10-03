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

// ExaSearchEndpoint issues ordinary web results only. No endpoint override is
// accepted.
const ExaSearchEndpoint = "https://api.exa.ai/search"

// exaBackend labels Exa outcomes in SearchOutcome.Backend.
const exaBackend = "exa"

// exaClient never follows redirects: ErrUseLastResponse returns the first
// response and no authentication header is ever replayed onto a successor.
var exaClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// exaSearchPayload is the request body sent to Exa. Type "auto" lets Exa pick
// between keyword and neural matching for ordinary web results.
// contents.text is explicitly false (never omitted) so the search call pins
// results without paying for contents extraction; page-text retrieval stays
// the job of web_fetch, and the search outcome never carries extracted
// document contents.
type exaSearchPayload struct {
	Query      string             `json:"query"`
	NumResults int                `json:"numResults"`
	Type       string             `json:"type"`
	Contents   exaContentsRequest `json:"contents"`
}

// exaContentsRequest is the contents object of exaSearchPayload. Text stays a
// plain bool so the false value is always serialized.
type exaContentsRequest struct {
	Text bool `json:"text"`
}

// SearchExa runs one Exa web search with the given API key. The key is sent
// only in the x-api-1 header of the single, non-redirected POST request and
// never appears in an error or log. Query is trimmed and bounded to
// MaxQueryBytes; limit is 1..MaxSearchLimit, defaulting to DefaultSearchLimit.
// Backend order is preserved, snippets come from Exa's text or summary fields
// only when Exa supplies them (never invented), all returned strings have
// control characters stripped, and result URLs are not fetched.
//
// Wire-behavior caveat: the endpoint, header name, request fields, and
// response shape below follow Exa's published API documentation and are NOT
// yet verified against the live service; a live probe must confirm them
// before this caveat is removed. Failures are returned as safe errors:
// 401/403 → the key was rejected, 429 → rate limited with a retry-after note,
// redirects and other statuses → descriptive errors, and a malformed body → a
// parse error.
func SearchExa(ctx context.Context, key string, req SearchRequest) (SearchOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	outcome := SearchOutcome{Backend: exaBackend, Hits: []SearchHit{}}
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
		return finish(errors.New("exa search requires an Exa API key"))
	}

	payload, err := json.Marshal(exaSearchPayload{
		Query:      query,
		NumResults: limit,
		Type:       "auto",
		Contents:   exaContentsRequest{Text: false},
	})
	if err != nil {
		return finish(fmt.Errorf("encoding the exa search request: %w", err))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, ExaSearchEndpoint, bytes.NewReader(payload))
	if err != nil {
		return finish(fmt.Errorf("exa search request: %w", err))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-1", key)
	request.Header.Set("User-Agent", "likha")
	// Enforce the per-request cap even when the caller's context would allow
	// longer; an existing earlier deadline keeps precedence.
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > searchTimeout {
		timeoutCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		defer cancel()
		request = request.WithContext(timeoutCtx)
	}

	response, err := exaClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("exa search request failed: %w", err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode >= 300 && response.StatusCode < 400:
		// The redirect response's location text is untrusted; naming only the
		// status keeps it out of the message.
		return finish(fmt.Errorf("exa search returned an HTTP %d redirect; search redirects are never followed", response.StatusCode))
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return finish(fmt.Errorf("exa search returned HTTP %d; the provided Exa API key was rejected", response.StatusCode))
	case response.StatusCode == http.StatusTooManyRequests:
		note := "Exa rate-limited this search (HTTP 429)"
		if after := sanitizeText(response.Header.Get("Retry-After")); after != "" {
			note += "; Retry-After: " + truncateText(after, 64)
		}
		outcome.Notes = append(outcome.Notes, note)
		return finish(errors.New(note))
	default:
		return finish(fmt.Errorf("exa search returned HTTP %s; retry later or verify availability", response.Status))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("reading the exa search response: %w", err))
	}
	if len(body) > maxSearchResponseBytes {
		return finish(fmt.Errorf("exa search response exceeded %d bytes", maxSearchResponseBytes))
	}

	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Text    string `json:"text"`
			Summary string `json:"summary"`
		} `json:"results"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return finish(fmt.Errorf("exa search response was not the expected JSON: %w", err))
	}
	if len(parsed.Results) == 0 {
		outcome.Notes = append(outcome.Notes, "Exa returned no results for this query; treat the search as empty.")
		return finish(nil)
	}
	for _, result := range parsed.Results {
		if len(outcome.Hits) == limit {
			outcome.Truncated = true
			outcome.Notes = append(outcome.Notes, "Exa returned more results than requested; the extra results were dropped.")
			break
		}
		snippetSource := result.Text
		if strings.TrimSpace(snippetSource) == "" {
			snippetSource = result.Summary
		}
		outcome.Hits = append(outcome.Hits, SearchHit{
			Rank:    len(outcome.Hits) + 1,
			Title:   sanitizeText(result.Title),
			URL:     sanitizeText(result.URL),
			Snippet: sanitizeText(snippetSource),
		})
	}
	return finish(nil)
}
