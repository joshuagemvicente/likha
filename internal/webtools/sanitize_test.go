package webtools

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSanitizeTextKeepsWordBoundaries(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Go is fast", "Go is fast"},
		{"newline separates words", "Go is\nfast", "Go is fast"},
		{"crlf separates words", "Go is\r\nfast", "Go is  fast"},
		{"tab separates words", "Go\tis", "Go is"},
		{"escape stripped", "red\x1b[31m", "red[31m"},
		{"bell stripped", "a\x07b", "ab"},
		{"invalid utf8 replaced", "a\xffb", "a�b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeText(tc.in); got != tc.want {
				t.Fatalf("sanitizeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDDGParseResultsMultiLineMarkupKeepsSpaces(t *testing.T) {
	page := `<div class="result results_links">
<a class="result__a" href="https://go.dev/">The Go
	Programming Language</a>
<a class="result__snippet" href="https://go.dev/">Go is an open source
programming language.</a>
</div>`
	parsed := ddgParseResults(page, 5)
	if len(parsed.hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(parsed.hits))
	}
	hit := parsed.hits[0]
	if hit.title != "The Go Programming Language" {
		t.Fatalf("title = %q, want words kept apart", hit.title)
	}
	if hit.snippet != "Go is an open source programming language." {
		t.Fatalf("snippet = %q, want words kept apart", hit.snippet)
	}
}

// rewriteTransport sends every request to a local test server, so a backend
// with a fixed public endpoint can be exercised without network access.
type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = t.target.Scheme, t.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func TestSearchTavilyMultiLineContentKeepsSpaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev/","content":"Build simple,\nsecure systems."}]}`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	previous := tavilyClient.Transport
	tavilyClient.Transport = rewriteTransport{target: target}
	t.Cleanup(func() { tavilyClient.Transport = previous })

	outcome, err := SearchTavily(t.Context(), "test-key", SearchRequest{Query: "go"})
	if err != nil {
		t.Fatalf("SearchTavily: %v", err)
	}
	if len(outcome.Hits) != 1 || outcome.Hits[0].Snippet != "Build simple, secure systems." {
		t.Fatalf("hits = %+v, want the snippet's words kept apart", outcome.Hits)
	}
}
