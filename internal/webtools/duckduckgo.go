// duckduckgo.go adds a keyless DuckDuckGo search adapter to the webtools
// package. Unlike Brave, this backend reads the unofficial
// html.duckduckgo.com HTML results page: it is not a sanctioned API, its
// markup may change without notice, and automated use may be blocked or
// rate-limited at any time. Every failure mode therefore surfaces as a
// visible named error — a changed or blocking endpoint can never masquerade
// as a successful-but-empty search, and there is no fallback to another
// vendor. The request is a single GET with only an Accept header and the
// honest minimal identity User-Agent "likha-web-search" (the same identity
// style as web_fetch; no browser impersonation, cookies, or credentials).
// Results are parsed locally from the markup with a stdlib-only token scan;
// result links wrapped in DuckDuckGo /l/?uddg= redirects are unwrapped, and
// every outcome discloses the unofficial-endpoint fragility in Notes.
package webtools

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// duckduckgoBackend, the SearchOutcome.Backend name for this adapter, is
	// declared in provider.go beside the search registry; this file only
	// references it.

	// duckduckgoEndpoint is the unofficial HTML results page. It is not an
	// API; see the fragility disclosure on SearchDuckDuckGo. A POST form
	// submission to this same path is the usual alternative when GET is
	// blocked, but this adapter deliberately fixes on GET and surfaces
	// failures instead of silently switching methods.
	duckduckgoEndpoint = "https://html.duckduckgo.com/html/"

	// duckduckgoUserAgent keeps the honest minimal identity style of
	// web_fetch. No browser spoofing: if DuckDuckGo blocks this identity the
	// failure is reported, not evaded.
	duckduckgoUserAgent = "likha-web-search"
)

// duckduckgoClient never follows redirects: ErrUseLastResponse returns the
// first response so any 3xx becomes an explicit error. DuckDuckGo's HTML
// endpoint sometimes answers with a 302 — including back to itself — when it
// throttles or blocks automated requests, so a redirect is treated as a
// failure with a note, never as a result source.
var duckduckgoClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// duckduckgoFragilityNote is appended to the Notes of every outcome, success
// or failure, so no caller can mistake unofficial-endpoint results for
// sanctioned-API results.
const duckduckgoFragilityNote = "DuckDuckGo results come from the unofficial html.duckduckgo.com HTML endpoint, not a sanctioned API: its markup may change without notice, automated use may be blocked or rate-limited at any time, and failures surface as errors instead of falling back to another vendor."

// SearchDuckDuckGo runs one web search against DuckDuckGo's unofficial HTML
// results endpoint. It is a drop-in peer of Search: same request type, same
// outcome type, and identical validation (query trimmed, nonempty, at most
// MaxQueryBytes UTF-8 bytes; limit 1..MaxSearchLimit with 0 meaning the
// default of 5), reusing the shared validator so the two backends can never
// drift apart.
//
// The key parameter is ignored and may be empty: this endpoint is keyless.
// The parameter exists only so that every search adapter shares one
// signature.
//
// FRAGILITY — read before relying on this backend. html.duckduckgo.com is an
// UNOFFICIAL HTML endpoint, not a sanctioned API. It sits outside any API
// agreement: its markup may change without notice, automated use may be
// blocked, throttled, or served a challenge page at any time, and the result
// structure parsed here is observed behavior, not a documented capability.
// The adapter fails loudly instead of quietly degrading:
//
//   - Any 3xx is an error; redirects are never followed. DuckDuckGo
//     sometimes 302s — including back to itself — when throttling or
//     blocking, so a redirect carries a note saying so.
//   - Any other non-200 is a numeric-status error.
//   - Markup that yields zero parsable results is the named error "duckduckgo
//     returned no parsable results; the HTML endpoint may have changed or
//     blocked this request", never a silent empty success.
//   - There is no fallback to another vendor, ever.
//
// Every outcome — success or failure — carries the fragility disclosure in
// Notes, plus the redirect-unwrapping counts and any limit clamping. Elapsed
// is set on every path, including validation failures.
//
// Transport: one GET to duckduckgoEndpoint with the query urlencoded; the
// same 30 second deadline cap and 4 MiB response cap as Brave; the honest
// minimal identity User-Agent duckduckgoUserAgent; no cookies, no
// credentials, no endpoint override.
//
// Parsing is a stdlib-only token scan (no x/net, no regexp) in the style of
// fetch.go's converter: anchors with class "result__a" carry the result URL
// (usually a /l/?uddg=<urlencoded target> redirect wrapper, which is decoded
// and kept only when it targets http(s)) and the link text as the title;
// anchors with class "result__snippet" carry the snippet when present.
// Snippets are never invented, DOM order is preserved, control sequences are
// stripped from every provider string, and HTML entities are decoded with
// html.UnescapeString only after tag stripping so an entity can never
// smuggle markup. Errors never include raw HTML dumps.
func SearchDuckDuckGo(ctx context.Context, key string, req SearchRequest) (SearchOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	outcome := SearchOutcome{Backend: duckduckgoBackend, Hits: []SearchHit{}}
	outcome.Notes = append(outcome.Notes, duckduckgoFragilityNote)
	started := time.Now()
	finish := func(err error) (SearchOutcome, error) {
		outcome.Elapsed = time.Since(started)
		return outcome, err
	}
	// key is deliberately unused: the endpoint is keyless. The parameter
	// exists for signature uniformity with Search.

	query, limit, err := validateSearchRequest(req)
	if err != nil {
		return finish(err)
	}
	outcome.Query = query

	values := url.Values{}
	values.Set("q", query)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, duckduckgoEndpoint+"?"+values.Encode(), nil)
	if err != nil {
		return finish(fmt.Errorf("duckduckgo web search request: %w", err))
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("User-Agent", duckduckgoUserAgent)
	// Enforce the per-request cap even when the caller's context would allow
	// longer; an existing earlier deadline keeps precedence.
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > searchTimeout {
		timeoutCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		defer cancel()
		request = request.WithContext(timeoutCtx)
	}

	response, err := duckduckgoClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("duckduckgo web search request failed: %w", err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode >= 300 && response.StatusCode < 400:
		// The redirect target is untrusted and is never named. DuckDuckGo's
		// unofficial endpoint sometimes 302s (including back to itself) when
		// it throttles or blocks automated requests.
		outcome.Notes = append(outcome.Notes, fmt.Sprintf("The unofficial DuckDuckGo HTML endpoint returned an HTTP %d redirect instead of results; redirects are never followed, and a redirect here often means the request was throttled or blocked.", response.StatusCode))
		return finish(fmt.Errorf("duckduckgo web search returned an HTTP %d redirect; search redirects are never followed", response.StatusCode))
	case response.StatusCode == http.StatusTooManyRequests:
		note := "DuckDuckGo rate-limited this search (HTTP 429)"
		if after := sanitizeText(response.Header.Get("Retry-After")); after != "" {
			note += "; Retry-After: " + truncateText(after, 64)
		}
		outcome.Notes = append(outcome.Notes, note)
		return finish(errors.New(note))
	default:
		return finish(fmt.Errorf("duckduckgo web search returned HTTP %d; retry later or verify availability", response.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("reading the duckduckgo web search response: %w", err))
	}
	if len(body) > maxSearchResponseBytes {
		return finish(fmt.Errorf("duckduckgo web search response exceeded %d bytes", maxSearchResponseBytes))
	}

	parsed := ddgParseResults(string(body), limit)
	outcome.Notes = append(outcome.Notes, fmt.Sprintf("Result anchors parsed from the HTML: %d; /l/?uddg= redirect wrappers unwrapped: %d; links dropped without an http(s) target: %d.", parsed.anchors, parsed.wrapped, parsed.dropped))

	hits := parsed.hits
	if len(hits) > limit {
		hits = hits[:limit]
		outcome.Truncated = true
		outcome.Notes = append(outcome.Notes, "DuckDuckGo returned more results than requested; the extra results were dropped.")
	}
	if len(hits) == 0 {
		// The fragility contract: an unparsable or blocked response is a
		// visible named error, never a silent empty result. No raw HTML is
		// echoed into the error.
		outcome.Notes = append(outcome.Notes, "No result anchors could be parsed from the response markup; the endpoint's structure may have changed or the request may have been blocked.")
		return finish(errors.New("duckduckgo returned no parsable results; the HTML endpoint may have changed or blocked this request"))
	}
	for _, hit := range hits {
		outcome.Hits = append(outcome.Hits, SearchHit{
			Rank:    len(outcome.Hits) + 1,
			Title:   hit.title,
			URL:     sanitizeText(hit.url),
			Snippet: hit.snippet,
		})
	}
	return finish(nil)
}

// ddgHit is one parsed result before ranking.
type ddgHit struct {
	title   string
	url     string
	snippet string
}

// ddgParse reports what one markup scan produced. anchors counts every
// "result__a" anchor encountered; wrapped counts hrefs successfully unwrapped
// from /l/?uddg= wrappers; dropped counts result anchors whose href did not
// resolve to an http(s) URL. hits holds completed results in DOM order, at
// most limit+1 of them: the extra one only proves that more results existed
// than the requested limit.
type ddgParse struct {
	hits    []ddgHit
	anchors int
	wrapped int
	dropped int
}

// ddgParseResults scans the response markup once, in DOM order, collecting
// results from "result__a" anchors (URL and title) and the following
// "result__snippet" anchors (snippet, when present). It is a stdlib-only
// token scan: no regexp, no x/net. Entities are decoded only after tag
// stripping, so encoded markup cannot be re-interpreted as structure. The
// scan stops as soon as limit+1 results are complete, which is enough to
// detect that the response had more results than the requested limit.
func ddgParseResults(body string, limit int) ddgParse {
	var parsed ddgParse
	scanCap := limit + 1
	var current *ddgHit
	for i := 0; i < len(body); {
		open := ddgIndexAnchorOpen(body, i)
		if open < 0 {
			break
		}
		tagEnd := ddgEndOfTag(body, open)
		attrs := ddgAttributes(body[open:tagEnd])
		class := html.UnescapeString(attrs["class"])
		i = tagEnd
		switch {
		case ddgHasClass(class, "result__a"):
			if current != nil {
				parsed.hits = append(parsed.hits, *current)
				current = nil
				if len(parsed.hits) >= scanCap {
					// One more result exists than the requested limit; the
					// in-progress extra is intentionally discarded.
					return parsed
				}
			}
			parsed.anchors++
			closeIdx := ddgIndexCloseAnchor(body, tagEnd)
			var text string
			if closeIdx < 0 {
				text = ddgExtractText(body[tagEnd:])
				i = len(body)
			} else {
				text = ddgExtractText(body[tagEnd:closeIdx])
				i = closeIdx + 3 // past "</a"; any trailing ">" is skipped by the next scan
			}
			target, wrapped, ok := ddgResultURL(html.UnescapeString(attrs["href"]))
			if !ok {
				parsed.dropped++
				continue
			}
			if wrapped {
				parsed.wrapped++
			}
			current = &ddgHit{title: text, url: target}
		case ddgHasClass(class, "result__snippet"):
			closeIdx := ddgIndexCloseAnchor(body, tagEnd)
			var text string
			if closeIdx < 0 {
				text = ddgExtractText(body[tagEnd:])
				i = len(body)
			} else {
				text = ddgExtractText(body[tagEnd:closeIdx])
				i = closeIdx + 3
			}
			if current != nil && current.snippet == "" && text != "" {
				current.snippet = text
			}
		default:
			// Any other anchor: skip past this tag only and keep scanning.
		}
	}
	if current != nil && len(parsed.hits) < scanCap {
		parsed.hits = append(parsed.hits, *current)
	}
	return parsed
}

// ddgResultURL normalizes one result anchor href. Most result links are
// DuckDuckGo /l/?uddg=<urlencoded target> redirect wrappers; the wrapper is
// unwrapped so hits carry the destination URL rather than DuckDuckGo's
// tracker link. It returns the target URL, whether the href was a wrapper,
// and whether an http(s) URL with a host was produced at all.
func ddgResultURL(href string) (target string, wrapped bool, ok bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false, false
	}
	// Protocol-relative and root-relative forms are anchored to https and to
	// DuckDuckGo's host before parsing.
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	} else if strings.HasPrefix(href, "/") {
		href = "https://duckduckgo.com" + href
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return "", false, false
	}
	if inner := strings.TrimSpace(parsed.Query().Get("uddg")); inner != "" &&
		(parsed.Host == "duckduckgo.com" || strings.HasSuffix(parsed.Host, ".duckduckgo.com")) &&
		(parsed.Path == "/l" || strings.HasPrefix(parsed.Path, "/l/")) {
		// parsed.Query() has already percent-decoded the uddg value once;
		// decoding again would corrupt targets that legitimately contain '%'.
		if ddgIsHTTPURL(inner) {
			return inner, true, true
		}
		return "", true, false
	}
	if ddgIsHTTPURL(href) {
		return href, false, true
	}
	return "", false, false
}

// ddgIsHTTPURL reports whether the candidate parses to an http(s) URL with a
// host. Anything else — javascript:, data:, mailto:, relative fragments — is
// rejected rather than passed through.
func ddgIsHTTPURL(candidate string) bool {
	parsed, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

// ddgIndexAnchorOpen finds the next anchor open tag at or after from,
// matching "<a" or "<A" only at a tag boundary (followed by whitespace, '>',
// or '/'). It returns the index of the '<', or -1.
func ddgIndexAnchorOpen(src string, from int) int {
	for j := from; j+2 < len(src); j++ {
		if src[j] != '<' {
			continue
		}
		if c := src[j+1]; c != 'a' && c != 'A' {
			continue
		}
		switch src[j+2] {
		case ' ', '\t', '\n', '\r', '\f', '>', '/':
			return j
		}
	}
	return -1
}

// ddgIndexCloseAnchor finds the next "</a>" (or "</A>", with optional
// whitespace before '>') at or after from, returning the index of the '<' or
// -1 when the anchor runs to EOF.
func ddgIndexCloseAnchor(src string, from int) int {
	for j := from; j+2 < len(src); j++ {
		if src[j] != '<' || src[j+1] != '/' {
			continue
		}
		if c := src[j+2]; c != 'a' && c != 'A' {
			continue
		}
		k := j + 3
		for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n' || src[k] == '\r') {
			k++
		}
		if k < len(src) && src[k] == '>' {
			return j
		}
	}
	return -1
}

// ddgEndOfTag returns the index just past the '>' closing the tag that starts
// at src[start], honoring quoted attribute values; EOF when unterminated.
func ddgEndOfTag(src string, start int) int {
	quote := byte(0)
	for j := start + 1; j < len(src); j++ {
		if quote != 0 {
			if src[j] == quote {
				quote = 0
			}
			continue
		}
		switch src[j] {
		case '"', '\'':
			quote = src[j]
		case '>':
			return j + 1
		}
	}
	return len(src)
}

// ddgAttributes extracts the attributes of one open tag (the full "<a ...>"
// text including brackets) into a lowercased-name map. First occurrence wins;
// values are returned raw and must be entity-decoded by the caller before
// use, so an entity can never split an attribute into markup.
func ddgAttributes(tag string) map[string]string {
	attrs := map[string]string{}
	i := 1 // past '<'
	set := func(name, value string) {
		if name != "" {
			if _, exists := attrs[name]; !exists {
				attrs[name] = value
			}
		}
	}
	for i < len(tag) {
		c := tag[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '/' {
			i++
			continue
		}
		if c == '>' {
			break
		}
		nameStart := i
		for i < len(tag) {
			c := tag[i]
			if c == '=' || c == '>' || c == '/' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
				break
			}
			i++
		}
		name := strings.ToLower(tag[nameStart:i])
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r' || tag[i] == '\f') {
			i++
		}
		if i < len(tag) && tag[i] == '=' {
			i++
			for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r' || tag[i] == '\f') {
				i++
			}
			var value string
			if i < len(tag) && (tag[i] == '"' || tag[i] == '\'') {
				quote := tag[i]
				i++
				valueStart := i
				for i < len(tag) && tag[i] != quote {
					i++
				}
				value = tag[valueStart:i]
				if i < len(tag) {
					i++ // closing quote
				}
			} else {
				valueStart := i
				for i < len(tag) && tag[i] != '>' && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' && tag[i] != '\f' {
					i++
				}
				value = tag[valueStart:i]
			}
			set(name, value)
			continue
		}
		set(name, "") // boolean attribute
	}
	return attrs
}

// ddgHasClass reports whether the whitespace-separated class list contains
// exactly the wanted token, so "result__a" cannot match "result__aside".
func ddgHasClass(class, want string) bool {
	for _, field := range strings.Fields(class) {
		if field == want {
			return true
		}
	}
	return false
}

// ddgExtractText reduces one anchor's inner markup to plain text: tags are
// skipped on the raw bytes first, then entities are decoded, whitespace runs
// collapse to single spaces, and control sequences are stripped — the same
// never-re tokenize ordering as fetch.go's converter, so an entity can never
// smuggle markup back into the structure scan.
func ddgExtractText(src string) string {
	var builder strings.Builder
	for i := 0; i < len(src); {
		if src[i] == '<' {
			i = ddgEndOfTag(src, i)
			continue
		}
		next := strings.IndexByte(src[i:], '<')
		if next < 0 {
			builder.WriteString(src[i:])
			break
		}
		builder.WriteString(src[i : i+next])
		i += next
	}
	text := html.UnescapeString(builder.String())
	return strings.TrimSpace(ddgCollapseSpaces(sanitizeText(text)))
}

// ddgCollapseSpaces reduces whitespace runs inside one text chunk to a single
// plain space.
func ddgCollapseSpaces(text string) string {
	if !strings.ContainsAny(text, " \t\n\r\v\f") {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	spacePending := false
	for _, r := range text {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			spacePending = true
		default:
			if spacePending {
				builder.WriteByte(' ')
				spacePending = false
			}
			builder.WriteRune(r)
		}
	}
	if spacePending {
		builder.WriteByte(' ')
	}
	return builder.String()
}
