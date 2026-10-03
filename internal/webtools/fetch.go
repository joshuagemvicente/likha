// fetch.go adds bounded public HTTPS fetching with a strict address policy to
// the webtools package. It enforces, in order: URL canonicalization and
// validation, per-canonical-origin consent (original URL and every redirect
// target, before any connection to that origin), connection-time validation of
// every resolved address through the dialer control hook, TLS with server-name
// verification of the full chain against the system roots, an absolute cap on
// both encoded and decompressed response bytes, a local conversion of
// supported text media into text/markdown, and sanitized, control-character
// free content. Everything that arrives from a fetched site is untrusted data;
// it is never executed and never triggers other transactions. No cookies are
// stored or sent, no proxy environment settings apply, and no headers beyond
// the three enumerated below are set.
package webtools

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// fetchContentLimit is the absolute bound for one fetch: encoded response
	// bytes, decompressed response bytes, and converted content.
	fetchContentLimit = 2 << 20
	// fetchMaxRedirects is the redirect budget per fetch (spec: at most five).
	fetchMaxRedirects = 5

	fetchUserAgent = "likha-web-fetch"

	// fetchAccept is the only content negotiation sent. No wildcard is
	// included: unsupported payloads are meant to fail clearly.
	fetchAccept = "text/html, application/xhtml+xml, text/plain, text/markdown"

	fetchMaxNote = 512
)

// fetchIDNA is deliberately local: x/text ships no public idna package and no
// new dependency is permitted, so canonicalization is case folding plus NFKC
// normalization and a direct RFC 3492 punycode encoding of each non-ASCII
// label. The resulting ASCII host must still pass the LDH hostname check and
// verify against the site's TLS certificate, so a divergent mapping fails
// closed rather than reaching an unintended host.
func fetchIDNAToASCII(host string) (string, error) {
	labels := strings.Split(host, ".")
	for i, label := range labels {
		if label == "" || utf8.RuneCountInString(label) > 63 {
			return "", errNonCanonicalHost
		}
		containsNonASCII := false
		for j := 0; j < len(label); j++ {
			if label[j] >= utf8.RuneSelf {
				containsNonASCII = true
				break
			}
		}
		if !containsNonASCII {
			continue
		}
		encoded, encodeErr := fetchEncodePunyLabel(norm.NFKC.String(strings.ToLower(label)))
		if encodeErr != nil || encoded == "" || len(encoded) > 63 {
			return "", errNonCanonicalHost
		}
		labels[i] = encoded
	}
	return strings.Join(labels, "."), nil
}

// errNonCanonicalHost marks a host that cannot be canonicalized.
var errNonCanonicalHost = errors.New("the host does not canonicalize to a valid ASCII form")

const (
	fetchPunyBase   = 36
	fetchPunyTmin   = 1
	fetchPunyTmax   = 26
	fetchPunySkew   = 38
	fetchPunyDamp   = 700
	fetchPunyBias   = 72
	fetchPunyNFirst = 0x80
)

// fetchEncodePunyLabel encodes one already-normalized, non-ASCII-permitting
// label as the punycode portion (RFC 3492, section 6.3) without the "xn--"
// prefix.
func fetchEncodePunyLabel(label string) (string, error) {
	runes := []rune(label)
	var out strings.Builder
	var basicCount int
	for _, r := range runes {
		if r < fetchPunyNFirst {
			out.WriteRune(r)
			basicCount++
		}
	}
	if basicCount < len(runes) && basicCount > 0 {
		out.WriteByte('-')
	}
	var n, delta, bias, handled = fetchPunyNFirst, 0, fetchPunyBias, basicCount
	for handled < len(runes) {
		next := int(utf8.MaxRune) + 1
		for _, r := range runes {
			if int(r) >= n && int(r) < next {
				next = int(r)
			}
		}
		if next > int(utf8.MaxRune) {
			return "", errNonCanonicalHost
		}
		delta += (next - n) * (handled + 1)
		n = next
		for _, r := range runes {
			if int(r) < n {
				delta++
				continue
			}
			if int(r) != n {
				continue
			}
			quota := delta
			for k := fetchPunyBase; ; k += fetchPunyBase {
				weight := k - bias
				if weight < fetchPunyTmin {
					weight = fetchPunyTmin
				} else if weight > fetchPunyTmax {
					weight = fetchPunyTmax
				}
				if quota < weight {
					break
				}
				out.WriteByte(fetchPunyDigit(weight + (quota-weight)%(fetchPunyBase-weight)))
				quota = (quota - weight) / (fetchPunyBase - weight)
			}
			out.WriteByte(fetchPunyDigit(quota))
			bias = fetchAdaptBias(delta, handled+1, handled == basicCount)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return out.String(), nil
}

func fetchPunyDigit(digit int) byte {
	if digit < 26 {
		return byte('a' + digit)
	}
	return byte('0' + digit - 26)
}

func fetchAdaptBias(delta, points int, first bool) int {
	if first {
		delta /= fetchPunyDamp
	} else {
		delta /= 2
	}
	delta += delta / points
	k := 0
	for delta > ((fetchPunyBase-fetchPunyTmin)*fetchPunyTmax)/2 {
		delta /= fetchPunyBase - fetchPunyTmin
		k += fetchPunyBase
	}
	return k + (fetchPunyBase-fetchPunyTmin+1)*delta/(delta+fetchPunySkew)
}

// FetchRequest requests one public HTTPS page. Format selects a target
// representation ("text" or "markdown"); empty defaults to "markdown".
type FetchRequest struct {
	URL    string `json:"url"`
	Format string `json:"format,omitempty"`
}

// FetchOutcome reports one completed fetch. Origin always names the canonical
// origin actually contacted in the final response. Notes are human-readable
// audit lines: consent events, redirect disclosures, dropped payload counts,
// and elapsed retrieval time. Content is untrusted data, sanitized.
type FetchOutcome struct {
	RequestedURL string   `json:"requested_url"`
	FinalURL     string   `json:"final_url"`
	Origin       string   `json:"origin"`
	MediaType    string   `json:"media_type,omitempty"`
	Redirects    []string `json:"redirects,omitempty"`
	Content      string   `json:"content"`
	Truncated    bool     `json:"truncated,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

// FetchDeclinedError reports a refused consent prompt. No request was ever
// sent to Origin: declined origins are never touched.
type FetchDeclinedError struct {
	Origin string
	URL    string
}

func (e *FetchDeclinedError) Error() string {
	return fmt.Sprintf("fetch declined: consent for origin %s in this conversation was refused; no request was sent", e.Origin)
}

// FetchPolicyError reports a URL that fails the address policy before or
// during the fetch. Reason never echoes raw URL bytes.
type FetchPolicyError struct {
	URL    string
	Reason string
}

func (e *FetchPolicyError) Error() string {
	return fmt.Sprintf("web_fetch address policy rejected %q: %s", e.URL, e.Reason)
}

// Fetch performs one consent-gated, policy-bounded public HTTPS fetch and
// returns the converted content. The consent callback must be provided by the
// coordinator; a nil callback fails closed without any connection. Cancellation
// is honored across parsing, DNS, connecting, reading, and conversion.
func Fetch(ctx context.Context, consent func(ctx context.Context, scope ConsentScope) (bool, error), req FetchRequest) (FetchOutcome, error) {
	outcome := FetchOutcome{}
	started := time.Now()
	finish := func(err error) (FetchOutcome, error) {
		outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Retrieval time: %d ms with a %d-byte content cap.", time.Since(started).Milliseconds(), fetchContentLimit)))
		return outcome, err
	}
	if ctx == nil {
		return outcome, errors.New("web_fetch requires a context")
	}

	// Canonicalize and validate the URL before anything else, including
	// consent and the network.
	if !utf8.ValidString(req.URL) {
		return outcome, errors.New("the web_fetch url argument must be valid UTF-8")
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	switch format {
	case "":
		format = "markdown"
	case "markdown", "text":
	default:
		return outcome, fmt.Errorf("the web_fetch format must be text or markdown; %q is unsupported", fetchSanitizeNote(req.Format))
	}
	target, targetErr := fetchParseTarget(req.URL)
	if targetErr != nil {
		return outcome, targetErr
	}
	outcome.RequestedURL = target.canonical
	outcome.FinalURL = target.canonical
	outcome.Origin = target.origin

	// Consent for the original canonical origin, before any connection.
	if consent == nil {
		return outcome, errors.New("web_fetch requires a consent callback; nothing was requested")
	}
	granted, consentErr := consent(ctx, ConsentScope{Kind: "fetch-origin", Origin: target.origin, URL: target.canonical})
	if consentErr != nil {
		return finish(fmt.Errorf("the fetch consent prompt failed before any request was sent: %w", consentErr))
	}
	if !granted {
		outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Consent declined for origin %s; no request was sent.", target.origin)))
		return finish(&FetchDeclinedError{Origin: target.origin, URL: target.canonical})
	}
	outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Consent granted for origin %s; the fetch may proceed.", target.origin)))

	// Transport: no proxy, no cookie jar, exactly the curated headers, and a
	// dialer control hook validating every resolved address at connection
	// time. The transport does its own TLS using the URL host as server name
	// with system roots and full-chain verification.
	dialer := &net.Dialer{Control: fetchDialControl}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          1,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   0, // bounded by the caller's context
		ResponseHeaderTimeout: 0, // bounded by the caller's context
	}
	permitted := map[string]bool{target.origin: true}
	var fetchState struct {
		final fetchTarget // updated per followed redirect; starts as the original target
	}
	fetchState.final = target
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(nextReq *http.Request, via []*http.Request) error {
			// Revalidate the whole address policy for the next URL, before it
			// is connected.
			next, nextErr := fetchTargetFromParsed(nextReq.URL)
			if nextErr != nil {
				return nextErr
			}
			if len(via) > fetchMaxRedirects {
				return &FetchPolicyError{URL: next.canonical, Reason: "the redirect budget of five has been exceeded"}
			}
			// A new canonical origin needs its own consent before that
			// connection is made. Already-completed hops stay recorded.
			if !permitted[next.origin] {
				okOrigin, originErr := consent(ctx, ConsentScope{Kind: "fetch-origin", Origin: next.origin, URL: next.canonical})
				if originErr != nil {
					return originErr
				}
				if !okOrigin {
					outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Consent declined for redirected origin %s; no request was sent to that target.", next.origin)))
					return &FetchDeclinedError{Origin: next.origin, URL: next.canonical}
				}
				permitted[next.origin] = true
				outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Consent granted for redirected origin %s.", next.origin)))
			}
			fetchState.final = next
			outcome.Redirects = append(outcome.Redirects, next.canonical)
			return nil
		},
	}
	defer transport.CloseIdleConnections()

	request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, target.canonical, nil)
	if requestErr != nil {
		return finish(fmt.Errorf("the web_fetch request could not be constructed: %w", requestErr))
	}
	// No cookies (there is no jar), no search credentials, no user-configured
	// headers: exactly this set is sent.
	request.Header.Set("Accept", fetchAccept)
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", fetchUserAgent)

	response, doErr := client.Do(request)
	if doErr != nil {
		if cancelErr := ctx.Err(); cancelErr != nil {
			return finish(cancelErr)
		}
		var declined *FetchDeclinedError
		var policy *FetchPolicyError
		if errors.As(doErr, &declined) || errors.As(doErr, &policy) {
			// Typed redirect/decline outcomes carry their own safe messages.
			return finish(doErr)
		}
		cause := doErr
		var urlErr *url.Error
		if errors.As(doErr, &urlErr) {
			cause = urlErr.Err
		}
		return finish(fmt.Errorf("the web_fetch request did not complete: %s", fetchSanitizeNote(cause.Error())))
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Name only the numeric status: reason phrases are server-controlled.
		return finish(fmt.Errorf("web_fetch returned HTTP %d; the body was discarded", response.StatusCode))
	}
	if len(outcome.Redirects) > 0 {
		outcome.FinalURL = fetchState.final.canonical
		outcome.Origin = fetchState.final.origin
		outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Final URL after %d redirects: %s.", len(outcome.Redirects), outcome.FinalURL)))
	}

	contentType := response.Header.Get("Content-Type")
	mediaType, params, parseErr := mime.ParseMediaType(contentType)
	if parseErr != nil {
		return finish(fmt.Errorf("web_fetch could not parse the response Content-Type header; only text/html, application/xhtml+xml, text/plain, and text/markdown are handled"))
	}
	mediaType = strings.ToLower(mediaType)
	outcome.MediaType = mediaType
	switch mediaType {
	case "text/html", "application/xhtml+xml", "text/plain", "text/markdown":
	default:
		// Binary/pdf/image payloads are never included, decoded, or guessed
		// at; naming the type is the safe disclosure.
		return finish(fmt.Errorf("web_fetch cannot handle media type %q; only text/html, application/xhtml+xml, text/plain, and text/markdown are handled and the payload was discarded", mediaType))
	}

	// Bound the response before and while reading it. Content-Length is
	// asserted up front; the stream itself is limited as well, so a lying
	// header cannot bypass the cap.
	if length := response.Header.Get("Content-Length"); length != "" {
		if parsed, intErr := strconv.ParseInt(length, 10, 64); intErr == nil && parsed > fetchContentLimit {
			dropNote := fmt.Sprintf("Discarded a response payload: Content-Length %d exceeds the %d-byte fetch cap.", parsed, fetchContentLimit)
			outcome.Notes = append(outcome.Notes, fetchNote(dropNote))
			return finish(fmt.Errorf("web_fetch discarded the response body: Content-Length %d exceeds the %d-byte fetch cap", parsed, fetchContentLimit))
		}
	}
	encoded, overCap, readErr := fetchReadCapped(ctx, response.Body, fetchContentLimit)
	if readErr != nil {
		return finish(readErr)
	}
	if overCap {
		dropNote := fmt.Sprintf("Discarded a response payload: %d encoded bytes streamed exceeds the %d-byte fetch cap.", len(encoded), fetchContentLimit)
		outcome.Notes = append(outcome.Notes, fetchNote(dropNote))
		return finish(fmt.Errorf("web_fetch discarded the response body: it exceeded the %d-byte fetch cap while streaming", fetchContentLimit))
	}

	// Decompressed bytes get their own re-cap, so an encoded-within-cap
	// decompression bomb still cannot pass.
	text := string(encoded)
	decodedBytes := len(encoded)
	switch encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding"))); encoding {
	case "", "identity":
	case "gzip", "x-gzip":
		gzipReader, gzipErr := gzip.NewReader(bytes.NewReader(encoded))
		if gzipErr != nil {
			outcome.Notes = append(outcome.Notes, fetchNote("Declared Content-Encoding gzip could not be applied; the body was treated as identity."))
		} else {
			decompressed, overCompressed, compressErr := fetchReadCapped(ctx, gzipReader, fetchContentLimit)
			_ = gzipReader.Close()
			if compressErr != nil {
				return finish(compressErr)
			}
			if overCompressed {
				dropNote := fmt.Sprintf("Discarded a response payload: %d decompressed bytes exceed the %d-byte fetch cap.", len(decompressed), fetchContentLimit)
				outcome.Notes = append(outcome.Notes, fetchNote(dropNote))
				return finish(fmt.Errorf("web_fetch discarded the response body: decompressed size exceeded the %d-byte fetch cap", fetchContentLimit))
			}
			text = string(decompressed)
			decodedBytes = len(decompressed)
		}
	default:
		outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Server sent unsupported Content-Encoding %q; the body was treated as identity.", fetchSanitizeNote(encoding))))
	}
	outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Response received: %d encoded bytes, %d bytes after decompression.", len(encoded), decodedBytes)))

	converted, convertErr := fetchConvert(ctx, text, format, mediaType, params, &outcome)
	if convertErr != nil {
		return finish(convertErr)
	}
	if len(converted) > fetchContentLimit {
		converted = fetchClipUTF8(converted, fetchContentLimit)
		outcome.Truncated = true
		outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Converted content was clipped at the %d-byte cap.", fetchContentLimit)))
	}
	outcome.Content = converted
	return finish(nil)
}

// fetchReadCapped reads at most limit+1 bytes so an over-cap stream is
// detected by the extra byte; it never buffers more than that.
func fetchReadCapped(ctx context.Context, body io.Reader, limit int64) ([]byte, bool, error) {
	data, readErr := io.ReadAll(io.LimitReader(body, limit+1))
	if readErr != nil {
		if cancelErr := ctx.Err(); cancelErr != nil {
			return nil, false, cancelErr
		}
		return nil, false, fmt.Errorf("reading the response body: %w", readErr)
	}
	return data, int64(len(data)) > limit, nil
}

// fetchDialControl validates each resolved address at connection time: only
// global public unicast on port 443 may be dialed. It rejects without echoing
// the rejected address, so DNS rebinding cannot smuggle a private target past
// the pre-connection checks.
func fetchDialControl(network, address string, _ syscall.RawConn) error {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return errors.New("connection rejected: only tcp dials are permitted")
	}
	host, port, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return errors.New("connection rejected: the target address could not be parsed")
	}
	if port != "443" {
		return errors.New("connection rejected: this fetcher only connects on port 443")
	}
	addr, parseErr := netip.ParseAddr(host)
	if parseErr != nil {
		return errors.New("connection rejected: the resolved address was not a literal IP")
	}
	if addr.Zone() != "" {
		return errors.New("connection rejected: IPv6 zone identifiers are not permitted")
	}
	if !fetchIsPublicUnicast(addr) {
		return errors.New("connection rejected: the resolved address is not global public unicast")
	}
	return nil
}

// fetchParseTarget canonicalizes a raw URL string and enforces the entire
// address policy that can be checked without connecting: HTTPS scheme only, no
// credentials, https on port 443 only, a single valid IDNA host or public IP
// literal, no zone identifiers, and no percent- or control-character tricks in
// the host.
func fetchParseTarget(raw string) (fetchTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fetchTarget{}, &FetchPolicyError{URL: "", Reason: "a URL is required"}
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return fetchTarget{}, &FetchPolicyError{URL: fetchSanitizeNote(raw), Reason: "the URL contains control characters"}
		}
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil {
		return fetchTarget{}, &FetchPolicyError{URL: fetchSanitizeNote(raw), Reason: "the URL is malformed"}
	}
	return fetchTargetFromParsed(parsed)
}

type fetchTarget struct {
	canonical string     // "https://host/path?query", port 443 always omitted
	origin    string     // "https://host"
	host      string     // canonical IDNA host or the IP literal itself
	ip        netip.Addr // valid address when the host is an IP literal
}

func fetchTargetFromParsed(parsed *url.URL) (fetchTarget, error) {
	var target fetchTarget
	if parsed == nil {
		return target, &FetchPolicyError{Reason: "no URL is present"}
	}
	if parsed.Scheme != "https" {
		return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "only the https scheme is permitted"}
	}
	if parsed.Opaque != "" {
		return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "opaque URLs are not fetchable"}
	}
	if parsed.User != nil {
		return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "embedding credentials in the URL is not permitted"}
	}
	if strings.Contains(parsed.Host, "%") {
		return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "IPv6 zone identifiers and percent-encoded hosts are not permitted"}
	}
	host := parsed.Hostname()
	if host == "" {
		return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "a host is required"}
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "only the default port 443 is permitted"}
	}
	if len(host) > 253 {
		return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "the host exceeds 253 characters"}
	}
	host = strings.TrimSuffix(host, ".")
	if literal, literalErr := netip.ParseAddr(host); literalErr == nil {
		if literal.Zone() != "" {
			return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "IPv6 zone identifiers are not permitted"}
		}
		literal = literal.Unmap()
		if !fetchIsPublicUnicast(literal) {
			return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "only global public unicast addresses are reachable"}
		}
		target.host = literal.String()
		target.ip = literal
	} else {
		canonical, idnaErr := fetchIDNAToASCII(host)
		if idnaErr != nil || canonical == "" {
			return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "the host does not resolve to a valid canonical form"}
		}
		canonical = strings.ToLower(canonical)
		if !fetchValidHostname(canonical) {
			return target, &FetchPolicyError{URL: fetchSanitizeNote(parsed.String()), Reason: "the host is not a valid canonical hostname"}
		}
		target.host = canonical
	}
	hostPart := target.host
	if target.ip.IsValid() && !target.ip.Is4() {
		hostPart = "[" + target.host + "]"
	}
	target.origin = "https://" + hostPart
	target.canonical = target.origin + parsed.RequestURI()
	return target, nil
}

// fetchValidHostname accepts classic LDH hostnames after IDNA mapping:
// lowercase letters, digits, and hyphens, with no empty, oversized, or
// hyphen-bounded labels and no underscore, space, or wildcard labels.
func fetchValidHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			switch c := label[i]; {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	return true
}

// fetchIsPublicUnicast reports whether an address is a global public unicast
// address. It covers IPv4 and IPv6 special-purpose ranges (loopback,
// RFC 1918/shared address space, link-local, multicast, reserved and
// documentation blocks, 6to4/Teredo/NAT64 and other transition ranges that
// embed raw IPv4, and the unspecified address) and rejects IPv6 zone
// identifiers. IPv4-mapped IPv6 is evaluated as its contained IPv4 address.
func fetchIsPublicUnicast(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.Is4() {
		quads := addr.As4()
		switch {
		case quads[0] == 0 || quads[0] == 10 || quads[0] == 127:
			return false
		case quads[0] == 100 && quads[1] >= 64 && quads[1] <= 127:
			return false
		case quads[0] == 169 && quads[1] == 254:
			return false
		case quads[0] == 172 && quads[1] >= 16 && quads[1] <= 31:
			return false
		case quads[0] == 192 && quads[1] == 0:
			return false
		case quads[0] == 192 && quads[1] == 88 && quads[2] == 99:
			return false
		case quads[0] == 192 && quads[1] == 168:
			return false
		case quads[0] == 198 && (quads[1] == 18 || quads[1] == 19):
			return false
		case quads[0] == 198 && quads[1] == 51 && quads[2] == 100:
			return false
		case quads[0] == 203 && quads[1] == 0 && quads[2] == 113:
			return false
		case quads[0] >= 224:
			return false
		}
		return true
	}
	octets := addr.As16()
	first := binary.BigEndian.Uint16(octets[:2])
	// Only currently allocated global unicast (2000::/3) counts as global.
	// Everything below it covers ::/8 transition ranges (unspecified,
	// loopback, IPv4-mapped, NAT64 and other embedded-IPv4 forms), discard
	// space, and unassigned blocks.
	if first < 0x2000 || first > 0x3fff {
		return false
	}
	second := binary.BigEndian.Uint16(octets[2:4])
	switch {
	case first == 0x2001 && second == 0x0000:
		return false // Teredo embeds an IPv4 address
	case first == 0x2001 && second == 0x0db8:
		return false // documentation block
	case first == 0x2001 && second >= 0x0010 && second <= 0x001f:
		return false // ORCHID
	case first == 0x2001 && second >= 0x0020 && second <= 0x002f:
		return false // ORCHIDv2
	case first == 0x2002:
		return false // 6to4 embeds an IPv4 address
	}
	return true
}

// fetchConvert turns decoded response text into the requested format.
func fetchConvert(ctx context.Context, text, format, mediaType string, params map[string]string, outcome *FetchOutcome) (string, error) {
	decoded := fetchDecodeCharset(text, params, outcome)
	switch mediaType {
	case "text/html", "application/xhtml+xml":
		return fetchHTMLToContent(ctx, decoded, format == "text")
	case "text/markdown", "text/plain":
		return fetchSanitizeText(strings.ReplaceAll(strings.ReplaceAll(decoded, "\r\n", "\n"), "\r", "\n")), nil
	default:
		return "", fmt.Errorf("web_fetch cannot convert media type %q", mediaType)
	}
}

// fetchDecodeCharset converts the response to a UTF-8 string. Latin-1 family
// charsets decode byte-for-byte; anything else that is not UTF-8 becomes
// replacement characters, always disclosed in notes.
func fetchDecodeCharset(text string, params map[string]string, outcome *FetchOutcome) string {
	charset := strings.ToLower(strings.TrimSpace(params["charset"]))
	switch charset {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		if !utf8.ValidString(text) {
			outcome.Notes = append(outcome.Notes, fetchNote("Invalid UTF-8 bytes in the response were replaced."))
			return strings.ToValidUTF8(text, "\uFFFD")
		}
		return text
	case "iso-8859-1", "iso8859-1", "iso_8859-1", "latin-1", "latin1", "l1", "cp28591", "windows-1252", "cp1252", "iso-8859-15":
		if charset == "windows-1252" || charset == "cp1252" || charset == "iso-8859-15" {
			outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Charset %q was decoded byte-for-byte as a Latin-1 approximation.", charset)))
		}
		var builder strings.Builder
		builder.Grow(len(text))
		for i := 0; i < len(text); i++ {
			builder.WriteRune(rune(text[i]))
		}
		return builder.String()
	default:
		outcome.Notes = append(outcome.Notes, fetchNote(fmt.Sprintf("Charset %q is unsupported; the response was decoded as UTF-8 with replacement characters.", charset)))
		return strings.ToValidUTF8(text, "\uFFFD")
	}
}

// fetchNote bounds and sanitizes one note line.
func fetchNote(text string) string {
	return truncateText(fetchSanitizeNote(text), fetchMaxNote)
}

// fetchSanitizeNote prepares a server-derived string for a message or note:
// valid UTF-8, no terminal control characters, bounded length.
func fetchSanitizeNote(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "�"))
	return truncateText(text, fetchMaxNote)
}

// fetchSanitizeText strips control characters while keeping newline and tab:
// decoded content is never allowed to carry terminal escape sequences.
func fetchSanitizeText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, text)
}

// fetchCollapseSpaces reduces whitespace runs inside one text chunk to a
// single plain space.
func fetchCollapseSpaces(text string) string {
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

// fetchHTMLToContent converts an HTML/XHTML document into plain text or
// markdown with a small streaming tokenizer. It is deliberately minimal: it
// strips script/style/noscript/iframe/svg/math/template/canvas blocks
// entirely, drops comments and doctypes, decodes entities with the standard
// library, preserves paragraph breaks around block elements, renders headings
// with markdown markers and list items with bullets in markdown mode, and
// keeps link text while dropping hrefs so a target is never implied as
// verified. Quoted attribute values may end tokenization early, nesting
// beyond the recognized element set falls back to plain text, and table
// structure becomes one text stream; these limits are accepted because the
// output is untrusted data either way.
func fetchHTMLToContent(ctx context.Context, src string, plain bool) (string, error) {
	converter := &fetchConverter{markdown: !plain}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for index, ticks := 0, 0; index < len(src); ticks++ {
		if ticks%0x1000 == 0 {
			if cancel := ctx.Err(); cancel != nil {
				return "", cancel
			}
		}
		switch {
		case src[index] != '<':
			next := strings.IndexByte(src[index:], '<')
			if next < 0 {
				converter.text(src[index:])
				index = len(src)
				continue
			}
			converter.text(src[index : index+next])
			index += next
		case strings.HasPrefix(src[index:], "<!--"):
			end := strings.Index(src[index+4:], "-->")
			if end < 0 {
				index = len(src)
			} else {
				index += 4 + end + 3
			}
		case strings.HasPrefix(src[index:], "<!"), strings.HasPrefix(src[index:], "<?"):
			if end := strings.IndexByte(src[index:], '>'); end < 0 {
				index = len(src)
			} else {
				index += end + 1
			}
		case strings.HasPrefix(src[index:], "</"):
			name, size := fetchScanTagName(src[index+2:])
			if name == "" {
				converter.text("<")
				index += 2
				continue
			}
			index += 2 + size
			index = fetchEndOfTag(src, index)
			converter.closeTag(strings.ToLower(name))
		default:
			name, size := fetchScanTagName(src[index+1:])
			if name == "" {
				converter.text("<")
				index++
				continue
			}
			lower := strings.ToLower(name)
			converter.openTag(lower)
			index += 1 + size
			index = fetchEndOfTag(src, index)
			if fetchSkipElements[lower] {
				if closeAt := fetchIndexCloseTag(src, index, lower); closeAt < 0 {
					index = len(src)
				} else {
					index = closeAt
				}
			}
		}
	}
	return converter.finish(), nil
}

var fetchSkipElements = map[string]bool{
	"script": true, "style": true, "noscript": true, "iframe": true,
	"template": true, "svg": true, "math": true, "canvas": true,
}

var fetchBlockElements = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"caption": true, "center": true, "colgroup": true, "dd": true,
	"details": true, "dialog": true, "dir": true, "div": true, "dl": true,
	"dt": true, "fieldset": true, "figcaption": true, "figure": true,
	"footer": true, "form": true, "header": true, "hgroup": true,
	"main": true, "menu": true, "nav": true, "ol": true, "p": true,
	"section": true, "summary": true, "table": true, "tbody": true,
	"td": true, "tfoot": true, "th": true, "thead": true, "tr": true,
	"ul": true,
}

// fetchConverter is the minimal streaming HTML extractor described above. It
// never re-tokenizes decoded text, so entities cannot smuggle markup.
type fetchConverter struct {
	out              strings.Builder
	trailingNewlines int
	pendingSpace     bool
	breakPending     int    // newlines owed before the next text (1 = soft break, 2 = paragraph)
	prefixPending    string // starts the next line: heading marker or list bullet
	prefixKind       byte   // 'h', 'l', or 0
	headingLevel     int
	insidePre        bool
	markdown         bool
	listKind         []byte
	counters         []int
}

func (c *fetchConverter) write(text string) {
	c.out.WriteString(text)
	if len(text) == 0 {
		return
	}
	if text[len(text)-1] == '\n' {
		count := 0
		for i := len(text) - 1; i >= 0 && text[i] == '\n'; i-- {
			count++
		}
		c.trailingNewlines = count
		if c.trailingNewlines > 2 {
			c.trailingNewlines = 2
		}
	} else {
		c.trailingNewlines = 0
	}
}

func (c *fetchConverter) lineBreak(count int) {
	if count > c.breakPending {
		c.breakPending = count
	}
}

// flushBreaks emits owed newlines, avoiding leading blank lines at the start
// of the document and never exceeding two consecutive newlines, then writes
// any pending line prefix (heading marker or bullet).
func (c *fetchConverter) flushBreaks() {
	if c.breakPending > 0 {
		if c.out.Len() > 0 {
			owed := c.breakPending - c.trailingNewlines
			if owed > 0 {
				c.write(strings.Repeat("\n", owed))
			}
		}
		c.breakPending = 0
		c.pendingSpace = false
	}
	if c.prefixPending != "" {
		c.write(c.prefixPending)
		c.prefixPending = ""
		c.prefixKind = 0
	}
}

func (c *fetchConverter) text(chunk string) {
	if chunk == "" {
		return
	}
	chunk = html.UnescapeString(chunk)
	if !c.insidePre {
		chunk = fetchCollapseSpaces(chunk)
	}
	chunk = fetchSanitizeText(chunk)
	if strings.TrimSpace(chunk) == "" {
		if c.out.Len() > 0 {
			c.pendingSpace = true
		}
		return
	}
	c.flushBreaks()
	if c.pendingSpace && c.trailingNewlines == 0 {
		c.write(" ")
	}
	c.pendingSpace = false
	c.write(chunk)
}

// fetchHeadingPrefix renders a heading number in markdown mode; plain text
// keeps only the surrounding breaks.
func fetchHeadingPrefix(level int, markdown bool) string {
	if !markdown {
		return ""
	}
	return strings.Repeat("#", level) + " "
}

func (c *fetchConverter) openTag(name string) {
	switch {
	case fetchSkipElements[name]:
		c.lineBreak(2)
	case name == "br":
		c.lineBreak(1)
	case name == "hr":
		c.lineBreak(2)
	case name == "pre":
		c.lineBreak(2)
		c.insidePre = true
	case name == "li":
		c.lineBreak(2)
		if n := len(c.listKind); n > 0 {
			marker := "• "
			if c.listKind[n-1] == 'o' {
				c.counters[n-1]++
				marker = fmt.Sprintf("%d. ", c.counters[n-1])
			} else if c.markdown {
				marker = "- "
			}
			c.prefixPending = marker
			c.prefixKind = 'l'
		}
	case name == "ol":
		c.listKind = append(c.listKind, 'o')
		c.counters = append(c.counters, 0)
		c.lineBreak(2)
	case name == "ul":
		c.listKind = append(c.listKind, 'u')
		c.lineBreak(2)
	case len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6':
		c.lineBreak(2)
		c.prefixPending = fetchHeadingPrefix(int(name[1]-'0'), c.markdown)
		c.prefixKind = 'h'
	case fetchBlockElements[name]:
		c.lineBreak(2)
	}
}

func (c *fetchConverter) closeTag(name string) {
	switch {
	case name == "pre":
		c.insidePre = false
		c.lineBreak(2)
	case len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6':
		if c.prefixKind == 'h' {
			c.prefixPending = ""
			c.prefixKind = 0
		}
		c.lineBreak(2)
	case name == "li":
		if c.prefixKind == 'l' {
			c.prefixPending = ""
			c.prefixKind = 0
		}
		c.lineBreak(2)
	case name == "ol" || name == "ul":
		if n := len(c.listKind); n > 0 {
			c.listKind = c.listKind[:n-1]
			if name == "ol" {
				c.counters = c.counters[:n-1]
			}
		}
		c.lineBreak(2)
	case fetchBlockElements[name]:
		c.lineBreak(2)
	}
}

// finish normalizes the extracted text: per-line trailing spaces trimmed, no
// more than two consecutive newlines, and no surrounding blank space.
func (c *fetchConverter) finish() string {
	lines := strings.Split(c.out.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	result := strings.Join(lines, "\n")
	result = strings.Trim(result, "\n \t")
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result
}

// fetchScanTagName scans an HTML element name; it returns the scanned bytes,
// stopping before whitespace, '>', or '/'.
func fetchScanTagName(src string) (string, int) {
	end := 0
	for end < len(src) {
		c := src[end]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			break
		}
		end++
	}
	return src[:end], end
}

// fetchEndOfTag returns the index just past the '>' closing the tag that
// starts at src, honoring quoted attribute values; EOF when unterminated.
func fetchEndOfTag(src string, start int) int {
	quote := byte(0)
	for j := start; j < len(src); j++ {
		switch char := src[j]; {
		case quote == 0 && (char == '"' || char == '\''):
			quote = char
		case quote != 0 && char == quote:
			quote = 0
		case char == '>':
			return j + 1
		}
	}
	return len(src)
}

// fetchIndexCloseTag finds the position of the case-insensitive closing tag
// of name at or after from, or -1 when the element runs to EOF. Only a real
// tag boundary (next byte is '>', '/', whitespace, or EOF) matches.
func fetchIndexCloseTag(src string, from int, name string) int {
	needle := "</" + name
	for j := from; j+len(needle) <= len(src); j++ {
		if !fetchNeedleEqual(src[j:j+len(needle)], needle) {
			continue
		}
		if j+len(needle) == len(src) {
			return j
		}
		switch next := src[j+len(needle)]; {
		case next == '>' || next == '/' || next == ' ' || next == '\t' || next == '\n' || next == '\r':
			return j
		}
	}
	return -1
}

// fetchNeedleEqual compares two byte strings ASCII-case-insensitively.
func fetchNeedleEqual(a, b string) bool {
	for i := 0; i < len(a); i++ {
		ca := a[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if ca != b[i] {
			return false
		}
	}
	return true
}

func fetchClipUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
