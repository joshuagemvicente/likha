// duckduckgo.go adds a keyless DuckDuckGo search adapter to the webtools
// package. Unlike Brave, this backend reads the unofficial
// html.duckduckgo.com HTML results page: it is not a sanctioned API, its
// markup may change without notice, and automated use may be blocked or
// rate-limited at any time. Every failure mode therefore surfaces as a
// visible named error — a changed or blocking endpoint can never masquerade
// as a successful-but-empty search, and there is no fallback to another
// vendor. The request is the same form POST the page's own search box
// submits, with the honest minimal identity User-Agent "likha-web-search"
// (the same identity style as web_fetch; no browser impersonation, cookies,
// or credentials). Requests are serialized and spaced at least
// duckduckgoMinGap apart, because parallel bursts are what trigger
// DuckDuckGo's bot challenge. Results are parsed locally per result
// container with a stdlib-only token scan; sponsored results and links back
// into duckduckgo.com are dropped, /l/?uddg= redirect wrappers are unwrapped,
// and every outcome discloses the unofficial-endpoint fragility in Notes.
// Observed endpoint behavior is recorded in specs/web-tools/research.md.
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

	// duckduckgoReferer matches what the page's own search form sends.
	duckduckgoReferer = "https://html.duckduckgo.com/"

	// duckduckgoUserAgent keeps the honest minimal identity style of
	// web_fetch. No browser spoofing: if DuckDuckGo blocks this identity the
	// failure is reported, not evaded.
	duckduckgoUserAgent = "likha-web-search"
)

var (
	// duckduckgoEndpoint is the unofficial HTML results page. It is not an
	// API; see the fragility disclosure on SearchDuckDuckGo. It is a
	// variable only so tests can point it at a local server; there is no
	// user-facing endpoint override.
	duckduckgoEndpoint = "https://html.duckduckgo.com/html/"

	// duckduckgoMinGap is the minimum spacing between the start of one
	// DuckDuckGo request and the end of the previous one in this process.
	duckduckgoMinGap = time.Second

	// duckduckgoTurn serializes DuckDuckGo requests; holding its single slot
	// also guards duckduckgoLast.
	duckduckgoTurn = make(chan struct{}, 1)
	duckduckgoLast time.Time
)

// ErrDuckDuckGoBlocked reports that DuckDuckGo answered with its bot
// challenge page instead of results. Retrying immediately does not help and
// tends to extend the block.
var ErrDuckDuckGoBlocked = errors.New("duckduckgo blocked this automated search with a bot challenge")

// duckduckgoClient never follows redirects: ErrUseLastResponse returns the
// first response so any 3xx becomes an explicit error. Probes on 2026-10-05
// saw blocks only as HTTP 202 challenge pages, but a redirect would still be
// a failure, never a result source.
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
// default of 5), reusing the shared validator so the backends can never
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
//   - HTTP 202, or a challenge page under any status, is ErrDuckDuckGoBlocked
//     with guidance not to retry right away.
//   - Any 3xx is an error; redirects are never followed.
//   - Any other non-200 is a numeric-status error.
//   - DuckDuckGo's own "No results found" page is a success with zero hits
//     and a note saying so.
//   - Any other markup that yields zero organic results is the named error
//     "duckduckgo returned no parsable results; the HTML endpoint's markup may
//     have changed", never a silent empty success.
//   - There is no fallback to another vendor or request method, ever.
//
// Every outcome — success or failure — carries the fragility disclosure in
// Notes, plus parse counts and any limit clamping. Elapsed is set on every
// path, including validation failures.
//
// Transport: one form POST (q, b) to duckduckgoEndpoint, serialized with
// every other DuckDuckGo search in the process and spaced duckduckgoMinGap
// after the previous one; the same 30 second deadline cap (queueing
// included) and 4 MiB response cap as Brave; the honest minimal identity
// User-Agent duckduckgoUserAgent; no cookies, no credentials, no endpoint
// override, and no ad- or branding-stripping parameters.
//
// Parsing is a stdlib-only token scan (no x/net, no regexp) in the style of
// fetch.go's converter, scoped to each div.result container: containers
// marked result--ad are skipped, the first "result__a" anchor gives the URL
// and title, and the container's "result__snippet" element gives the snippet
// when present. A /l/?uddg= redirect wrapper is decoded, and the target is
// kept only when it is http(s) and not a duckduckgo.com page (which catches
// y.js ad links even if the container class changes). Snippets are never
// invented, DOM order is preserved, control sequences are stripped from
// every provider string, and HTML entities are decoded with
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

	// Enforce the per-request cap even when the caller's context would allow
	// longer; an existing earlier deadline keeps precedence. Queueing behind
	// another DuckDuckGo search counts against the same cap.
	reqCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > searchTimeout {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, searchTimeout)
		defer cancel()
	}
	// contextFailure names why the request context ended: the caller's own
	// cancellation or deadline wins, otherwise the internal cap fired.
	contextFailure := func() error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if reqCtx.Err() != nil {
			return fmt.Errorf("duckduckgo web search timed out after %s", searchTimeout)
		}
		return nil
	}

	waited, release, err := duckduckgoAcquire(reqCtx)
	if err != nil {
		if ctxErr := contextFailure(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(err)
	}
	defer release()
	if waited >= 100*time.Millisecond {
		outcome.Notes = append(outcome.Notes, fmt.Sprintf("Waited %s for an earlier DuckDuckGo search; DuckDuckGo searches run one at a time, at least %s apart.", waited.Truncate(time.Millisecond), duckduckgoMinGap))
	}

	form := url.Values{}
	form.Set("q", query)
	form.Set("b", "")
	request, err := http.NewRequestWithContext(reqCtx, http.MethodPost, duckduckgoEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return finish(fmt.Errorf("duckduckgo web search request: %w", err))
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/html")
	request.Header.Set("User-Agent", duckduckgoUserAgent)
	request.Header.Set("Referer", duckduckgoReferer)

	response, err := duckduckgoClient.Do(request)
	if err != nil {
		if ctxErr := contextFailure(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("duckduckgo web search request failed: %w", err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode == http.StatusAccepted:
		return finish(duckduckgoBlocked(&outcome, response.StatusCode))
	case response.StatusCode >= 300 && response.StatusCode < 400:
		// The redirect target is untrusted and is never named.
		outcome.Notes = append(outcome.Notes, fmt.Sprintf("The unofficial DuckDuckGo HTML endpoint returned an HTTP %d redirect instead of results; redirects are never followed.", response.StatusCode))
		return finish(fmt.Errorf("duckduckgo web search returned an HTTP %d redirect; search redirects are never followed", response.StatusCode))
	case response.StatusCode == http.StatusTooManyRequests:
		note := "DuckDuckGo rate-limited this search (HTTP 429)"
		if after := sanitizeText(response.Header.Get("Retry-After")); after != "" {
			note += "; Retry-After: " + truncateText(after, 64)
		}
		outcome.Notes = append(outcome.Notes, note)
		return finish(errors.New(note))
	case response.StatusCode == http.StatusForbidden:
		return finish(errors.New("duckduckgo refused this search (HTTP 403); retry later or configure a keyed backend (brave, tavily, or exa)"))
	default:
		return finish(fmt.Errorf("duckduckgo web search returned HTTP %d; retry later or verify availability", response.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes+1))
	if err != nil {
		if ctxErr := contextFailure(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(fmt.Errorf("reading the duckduckgo web search response: %w", err))
	}
	if len(body) > maxSearchResponseBytes {
		return finish(fmt.Errorf("duckduckgo web search response exceeded %d bytes", maxSearchResponseBytes))
	}
	page := string(body)
	if ddgIsChallenge(page) {
		return finish(duckduckgoBlocked(&outcome, response.StatusCode))
	}

	parsed := ddgParseResults(page, limit)
	outcome.Notes = append(outcome.Notes, fmt.Sprintf("Result containers parsed from the HTML: %d; sponsored or duckduckgo.com links skipped: %d; links dropped without an http(s) target: %d; /l/?uddg= redirect wrappers unwrapped: %d.", parsed.containers, parsed.skipped, parsed.dropped, parsed.wrapped))

	hits := parsed.hits
	if len(hits) > limit {
		hits = hits[:limit]
		outcome.Truncated = true
		outcome.Notes = append(outcome.Notes, "DuckDuckGo returned more results than requested; the extra results were dropped.")
	}
	if len(hits) == 0 {
		if parsed.noResults {
			outcome.Notes = append(outcome.Notes, "DuckDuckGo found no results for this query.")
			return finish(nil)
		}
		// The fragility contract: unparsable markup is a visible named
		// error, never a silent empty result. No raw HTML is echoed.
		outcome.Notes = append(outcome.Notes, "No organic results could be parsed from the response markup, and it was neither a bot challenge nor DuckDuckGo's no-results page; the endpoint's structure may have changed.")
		return finish(errors.New("duckduckgo returned no parsable results; the HTML endpoint's markup may have changed"))
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

// duckduckgoBlocked records the challenge note and returns the blocked error
// for status.
func duckduckgoBlocked(outcome *SearchOutcome, status int) error {
	outcome.Notes = append(outcome.Notes, "DuckDuckGo answered with its bot challenge page (\"Unfortunately, bots use DuckDuckGo too\") instead of results. Likha does not solve or evade the challenge, and retrying right away tends to extend the block.")
	return fmt.Errorf("%w (HTTP %d); do not retry or rephrase the search now — try again in a few minutes, or configure a keyed backend (brave, tavily, or exa)", ErrDuckDuckGoBlocked, status)
}

// duckduckgoAcquire waits for this process's single DuckDuckGo request slot
// and then until duckduckgoMinGap has passed since the previous request
// finished. It returns how long the caller waited and a release func that
// records the finish time and frees the slot. A context that ends while
// waiting returns its error and holds nothing.
func duckduckgoAcquire(ctx context.Context) (time.Duration, func(), error) {
	started := time.Now()
	select {
	case duckduckgoTurn <- struct{}{}:
	case <-ctx.Done():
		return time.Since(started), nil, ctx.Err()
	}
	if !duckduckgoLast.IsZero() {
		if gap := duckduckgoMinGap - time.Since(duckduckgoLast); gap > 0 {
			timer := time.NewTimer(gap)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				<-duckduckgoTurn
				return time.Since(started), nil, ctx.Err()
			}
		}
	}
	release := func() {
		duckduckgoLast = time.Now()
		<-duckduckgoTurn
	}
	return time.Since(started), release, nil
}

// ddgIsChallenge reports whether the page is DuckDuckGo's anomaly/bot
// challenge rather than a results page. The markers are the challenge
// modal's class prefix, its form id, its script path, and its headline.
func ddgIsChallenge(page string) bool {
	for _, marker := range []string{"anomaly-modal", `id="challenge-form"`, "anomaly.js", "bots use DuckDuckGo too"} {
		if strings.Contains(page, marker) {
			return true
		}
	}
	return false
}

// ddgHit is one parsed result before ranking.
type ddgHit struct {
	title   string
	url     string
	snippet string
}

// ddgParse reports what one markup scan produced. containers counts every
// div.result container; skipped counts sponsored containers and results
// whose target is a duckduckgo.com page; dropped counts result links that did
// not resolve to an http(s) URL; wrapped counts hrefs unwrapped from
// /l/?uddg= wrappers; noResults records DuckDuckGo's own no-results
// container. hits holds organic results in DOM order, at most limit+1 of
// them: the extra one only proves that more results existed than the
// requested limit.
type ddgParse struct {
	hits       []ddgHit
	containers int
	skipped    int
	dropped    int
	wrapped    int
	noResults  bool
}

// ddgParseResults scans the response markup in DOM order. Each div whose
// class list holds the token "result" starts a container that runs to the
// next such div (results are siblings, so this is the container's content
// plus trailing whitespace or page chrome). Inside a container the first
// "result__a" anchor gives URL and title and the first "result__snippet"
// element gives the snippet. Entities are decoded only after tag stripping,
// so encoded markup cannot be re-interpreted as structure. The scan stops as
// soon as limit+1 organic results are collected.
func ddgParseResults(body string, limit int) ddgParse {
	var parsed ddgParse
	var starts []ddgTag
	for at := 0; ; {
		tag, ok := ddgNextOpenTag(body, at)
		if !ok {
			break
		}
		at = tag.end
		if tag.name == "div" && ddgHasClass(html.UnescapeString(tag.attrs["class"]), "result") {
			starts = append(starts, tag)
		}
	}
	for i, start := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1].start
		}
		parsed.containers++
		class := html.UnescapeString(start.attrs["class"])
		if ddgHasClass(class, "result--no-result") {
			parsed.noResults = true
			continue
		}
		if ddgHasClass(class, "result--ad") {
			parsed.skipped++
			continue
		}
		hit, ok := ddgContainerHit(body[start.end:end], &parsed)
		if !ok {
			continue
		}
		parsed.hits = append(parsed.hits, hit)
		if len(parsed.hits) > limit {
			break
		}
	}
	return parsed
}

// ddgContainerHit extracts one organic result from a container's markup,
// updating the skip/drop/unwrap counters. It reports false when the
// container has no usable result link.
func ddgContainerHit(container string, parsed *ddgParse) (ddgHit, bool) {
	link, ok := ddgFindClass(container, "result__a")
	if !ok {
		return ddgHit{}, false
	}
	target, wrapped, ok := ddgResultURL(html.UnescapeString(link.attrs["href"]))
	if wrapped {
		parsed.wrapped++
	}
	if !ok {
		parsed.dropped++
		return ddgHit{}, false
	}
	if ddgIsDuckDuckGoURL(target) {
		// y.js ad clicks, help pages, and internal topic links.
		parsed.skipped++
		return ddgHit{}, false
	}
	hit := ddgHit{title: ddgElementText(container, link), url: target}
	if snippet, ok := ddgFindClass(container, "result__snippet"); ok {
		hit.snippet = ddgElementText(container, snippet)
	}
	return hit, true
}

// ddgTag is one parsed open tag: its byte span in the source, lowercased
// name, and raw (entity-encoded) attributes.
type ddgTag struct {
	start, end int
	name       string
	attrs      map[string]string
}

// ddgNextOpenTag returns the next open tag at or after from. Comments,
// closing tags, doctypes, and stray '<' are skipped, as is the content of
// script and style elements.
func ddgNextOpenTag(src string, from int) (ddgTag, bool) {
	for j := from; j < len(src); j++ {
		if src[j] != '<' {
			continue
		}
		if strings.HasPrefix(src[j:], "<!--") {
			closeIdx := strings.Index(src[j+4:], "-->")
			if closeIdx < 0 {
				return ddgTag{}, false
			}
			j += 4 + closeIdx + 2
			continue
		}
		k := j + 1
		if k >= len(src) || !ddgIsASCIILetter(src[k]) {
			continue
		}
		for k < len(src) && (ddgIsASCIILetter(src[k]) || (src[k] >= '0' && src[k] <= '9')) {
			k++
		}
		name := strings.ToLower(src[j+1 : k])
		end := ddgEndOfTag(src, j)
		if name == "script" || name == "style" {
			if closeIdx := ddgIndexClose(src, end, name); closeIdx >= 0 {
				j = closeIdx
				continue
			}
			return ddgTag{}, false
		}
		return ddgTag{start: j, end: end, name: name, attrs: ddgAttributes(src[j:end])}, true
	}
	return ddgTag{}, false
}

// ddgFindClass returns the first open tag in src whose class list contains
// want.
func ddgFindClass(src, want string) (ddgTag, bool) {
	for at := 0; ; {
		tag, ok := ddgNextOpenTag(src, at)
		if !ok {
			return ddgTag{}, false
		}
		if ddgHasClass(html.UnescapeString(tag.attrs["class"]), want) {
			return tag, true
		}
		at = tag.end
	}
}

// ddgElementText returns the plain text between tag and its closing tag in
// src, or to the end of src when the element is unterminated. It assumes the
// element does not nest another element of the same name, which holds for
// the anchors and snippet elements read here.
func ddgElementText(src string, tag ddgTag) string {
	closeIdx := ddgIndexClose(src, tag.end, tag.name)
	if closeIdx < 0 {
		return ddgExtractText(src[tag.end:])
	}
	return ddgExtractText(src[tag.end:closeIdx])
}

func ddgIsASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ddgResultURL normalizes one result anchor href. Links may be DuckDuckGo
// /l/?uddg=<urlencoded target> redirect wrappers (seen on GET responses;
// POST responses carried direct links when probed); a wrapper is unwrapped
// so hits carry the destination URL rather than DuckDuckGo's tracker link.
// It returns the target URL, whether the href was a wrapper, and whether an
// http(s) URL with a host was produced at all.
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
		ddgIsDuckDuckGoHost(parsed.Hostname()) &&
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

// ddgIsDuckDuckGoURL reports whether an http(s) URL points at duckduckgo.com
// or a subdomain. No organic result points there; ad clicks (y.js) do.
func ddgIsDuckDuckGoURL(candidate string) bool {
	parsed, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	return ddgIsDuckDuckGoHost(parsed.Hostname())
}

func ddgIsDuckDuckGoHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com")
}

// ddgIndexClose finds the next "</name>" (case-insensitive, with optional
// whitespace before '>') at or after from, returning the index of the '<' or
// -1 when the element runs to EOF.
func ddgIndexClose(src string, from int, name string) int {
	for j := from; j+2+len(name) <= len(src); j++ {
		if src[j] != '<' || src[j+1] != '/' {
			continue
		}
		if !strings.EqualFold(src[j+2:j+2+len(name)], name) {
			continue
		}
		k := j + 2 + len(name)
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
	// Skip the tag name.
	for i < len(tag) && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' && tag[i] != '\f' && tag[i] != '>' && tag[i] != '/' {
		i++
	}
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
// exactly the wanted token, so "result" cannot match "results_links" and
// "result__a" cannot match "result__aside".
func ddgHasClass(class, want string) bool {
	for _, field := range strings.Fields(class) {
		if field == want {
			return true
		}
	}
	return false
}

// ddgExtractText reduces one element's inner markup to plain text: tags are
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
