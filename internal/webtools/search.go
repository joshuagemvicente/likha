package webtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// BraveSearchEndpoint issues ordinary web results only, never Brave
	// Answers or summary-key generation. No endpoint override is accepted.
	BraveSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"

	SearchBackend      = braveBackend
	DefaultSearchLimit = 5
	MaxSearchLimit     = 10
	MaxQueryBytes      = 400

	searchTimeout          = 30 * time.Second
	maxSearchResponseBytes = 4 << 20

	// BraveDefaultRetention is used by the coordinator's consent copy. Do not
	// promise consumer-search or zero-retention privacy.
	BraveDefaultRetention = "Brave may record queries and request metadata for up to 90 days under its API privacy notice"
)

// ConsentScope is the disclosed span of one consented web operation. For a
// search call it carries Kind="search", the Backend, and the exact Query;
// Origin/URL stay empty. A fetch call per canonical origin carries Kind,
// Origin, and URL. ConsentQuery returns whether the user granted this
// conversation-scoped grant; ErrConsentDeclined (or any error mentioning
// "consent-declined") means the request must not be issued.
type ConsentScope struct {
	Kind, Backend, Query, Origin, URL string
}

// ConsentQuery asks the user once per scope for a renew-on-every-call-visible,
// conversation-lifetime grant. It is implemented by the coordinator, not here.
type ConsentQuery func(ctx context.Context, scope ConsentScope) (bool, error)

// ErrConsentDeclined reports a declined consent prompt.
var ErrConsentDeclined = errors.New("consent-declined: search was declined for this conversation")

// IsConsentDeclined matches declined-consent errors, including wrapped ones
// that lost errors.Is identity but kept the safe "consent-declined" marker.
func IsConsentDeclined(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrConsentDeclined) {
		return true
	}
	return strings.Contains(err.Error(), "consent-declined")
}

type SearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type SearchHit struct {
	Rank    int    `json:"rank"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type SearchOutcome struct {
	Query     string        `json:"query"`
	Backend   string        `json:"backend"`
	Hits      []SearchHit   `json:"hits"`
	Elapsed   time.Duration `json:"-"`
	Truncated bool          `json:"truncated"`
	Notes     []string      `json:"notes,omitempty"`
}

// braveClient never follows redirects: ErrUseLastResponse returns the first
// response and no authentication header is ever replayed onto a successor.
var braveClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Search runs one Brave web search with the given API key. The key is sent
// only in the X-Subscription-Token header of the single, non-redirected
// request and never appears in an error or log. Query is trimmed and bounded
// to MaxQueryBytes; limit is 1..MaxSearchLimit, defaulting to
// DefaultSearchLimit. Backend order is preserved, snippets are returned only
// when Brave supplies them (never invented), all returned strings have
// control characters stripped, and result URLs are not fetched. Failures are
// returned as safe errors: 401 → the key was rejected, 429 → rate limited
// with a retry-after note, redirects and other statuses → descriptive errors.
func Search(ctx context.Context, key string, req SearchRequest) (SearchOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	outcome := SearchOutcome{Backend: SearchBackend, Hits: []SearchHit{}}
	started := time.Now()
	finish := func(err error) (SearchOutcome, error) {
		outcome.Elapsed = time.Since(started)
		return outcome, err
	}

	query, limit, err := validateSearchRequest(req)
	if err != nil {
		return outcome, err
	}
	outcome.Query = query

	if strings.TrimSpace(key) == "" {
		return finish(errors.New("brave web search requires an API key from " + SearchKeyEnv + " or " + keyFileName))
	}

	values := url.Values{}
	values.Set("q", query)
	values.Set("count", strconv.Itoa(limit))
	values.Set("result_filter", "web")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, BraveSearchEndpoint+"?"+values.Encode(), nil)
	if err != nil {
		return finish(fmt.Errorf("brave web search request: %w", err))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Subscription-Token", key)
	request.Header.Set("User-Agent", "likha")
	// Enforce the per-request cap even when the caller's context would allow
	// longer; an existing earlier deadline keeps precedence.
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > searchTimeout {
		timeoutCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		defer cancel()
		request = request.WithContext(timeoutCtx)
	}

	response, err := braveClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("brave web search request failed: %w", err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode >= 300 && response.StatusCode < 400:
		// The redirect response's location text is untrusted; naming only the
		// status keeps it out of the message.
		return finish(fmt.Errorf("brave web search returned an HTTP %d redirect; search redirects are never followed", response.StatusCode))
	case response.StatusCode == http.StatusUnauthorized:
		return finish(errors.New("brave web search returned HTTP 401 unauthorized; the configured " + SearchKeyEnv + " credential was rejected"))
	case response.StatusCode == http.StatusTooManyRequests:
		note := "Brave rate-limited this search (HTTP 429)"
		if after := sanitizeText(response.Header.Get("Retry-After")); after != "" {
			note += "; Retry-After: " + truncateText(after, 64)
		}
		outcome.Notes = append(outcome.Notes, note)
		return finish(errors.New(note))
	default:
		return finish(fmt.Errorf("brave web search returned HTTP %s; retry later or verify availability", response.Status))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("reading the brave web search response: %w", err))
	}
	if len(body) > maxSearchResponseBytes {
		return finish(fmt.Errorf("brave web search response exceeded %d bytes", maxSearchResponseBytes))
	}

	var parsed struct {
		Web *struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return finish(fmt.Errorf("brave web search response was not the expected JSON: %w", err))
	}
	if parsed.Web == nil || len(parsed.Web.Results) == 0 {
		outcome.Notes = append(outcome.Notes, "Brave returned no web results for this query; treat the search as empty.")
		return finish(nil)
	}
	for _, result := range parsed.Web.Results {
		if len(outcome.Hits) == limit {
			outcome.Truncated = true
			outcome.Notes = append(outcome.Notes, "Brave returned more results than requested; the extra results were dropped.")
			break
		}
		outcome.Hits = append(outcome.Hits, SearchHit{
			Rank:    len(outcome.Hits) + 1,
			Title:   sanitizeText(result.Title),
			URL:     sanitizeText(result.URL),
			Snippet: sanitizeText(result.Description),
		})
	}
	return finish(nil)
}

func validateSearchRequest(req SearchRequest) (string, int, error) {
	if !utf8.ValidString(req.Query) {
		return "", 0, errors.New("web_search query must be valid UTF-8")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return "", 0, errors.New("web_search requires a nonempty query")
	}
	if len(query) > MaxQueryBytes {
		return "", 0, fmt.Errorf("the web_search query must be at most %d UTF-8 bytes", MaxQueryBytes)
	}
	limit := req.Limit
	switch {
	case limit == 0:
		limit = DefaultSearchLimit
	case limit < 1 || limit > MaxSearchLimit:
		return "", 0, fmt.Errorf("the web_search limit must be between 1 and %d", MaxSearchLimit)
	}
	return query, limit, nil
}

// sanitizeText strips terminal/presentation control sequences from provider
// strings without executing anything.
func sanitizeText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "\uFFFD"))
}

func truncateText(text string, limit int) string {
	if limit >= len(text) {
		return text
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
