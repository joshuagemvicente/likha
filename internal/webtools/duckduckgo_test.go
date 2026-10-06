package webtools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The testdata/duckduckgo pages were captured from html.duckduckgo.com on
// 2026-10-05 (specs/web-tools/research.md §7): results_post.html is a POST
// results page with direct links, results_get_uddg.html a GET page whose
// links are /l/?uddg= wrapped, no_results.html the genuine no-results page,
// and challenge.html the HTTP 202 bot challenge. Each results page opens with
// a sponsored y.js result.

func readDDGFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "duckduckgo", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// useDDGServer points the adapter at a local server with no request spacing
// for the duration of the test.
func useDDGServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	endpoint, gap := duckduckgoEndpoint, duckduckgoMinGap
	duckduckgoEndpoint, duckduckgoMinGap = server.URL+"/html/", 0
	t.Cleanup(func() {
		server.Close()
		duckduckgoEndpoint, duckduckgoMinGap = endpoint, gap
		duckduckgoLast = time.Time{}
	})
}

func TestDDGParseResultsSkipsAdsAndKeepsOrganicOrder(t *testing.T) {
	for _, name := range []string{"results_post.html", "results_get_uddg.html"} {
		t.Run(name, func(t *testing.T) {
			parsed := ddgParseResults(readDDGFixture(t, name), 10)
			if len(parsed.hits) != 10 {
				t.Fatalf("got %d hits, want 10", len(parsed.hits))
			}
			if parsed.skipped < 1 {
				t.Fatalf("skipped = %d, want the sponsored result counted", parsed.skipped)
			}
			first := parsed.hits[0]
			if first.url != "https://go.dev/" || first.title != "The Go Programming Language" {
				t.Fatalf("first hit = %+v, want go.dev organic result", first)
			}
			if !strings.HasPrefix(first.snippet, "Go is an open source programming language") {
				t.Fatalf("first snippet = %q", first.snippet)
			}
			for _, hit := range parsed.hits {
				if ddgIsDuckDuckGoURL(hit.url) || !ddgIsHTTPURL(hit.url) {
					t.Fatalf("hit URL %q should be an external http(s) URL", hit.url)
				}
				if hit.title == "" {
					t.Fatalf("hit %q has no title", hit.url)
				}
			}
		})
	}
}

func TestDDGParseResultsUnwrapsRedirects(t *testing.T) {
	parsed := ddgParseResults(readDDGFixture(t, "results_get_uddg.html"), 10)
	if parsed.wrapped < 10 {
		t.Fatalf("wrapped = %d, want every organic link unwrapped", parsed.wrapped)
	}
	direct := ddgParseResults(readDDGFixture(t, "results_post.html"), 10)
	if direct.wrapped != 0 {
		t.Fatalf("POST page wrapped = %d, want direct links", direct.wrapped)
	}
}

func TestDDGParseResultsStopsAfterLimitPlusOne(t *testing.T) {
	parsed := ddgParseResults(readDDGFixture(t, "results_post.html"), 3)
	if len(parsed.hits) != 4 {
		t.Fatalf("got %d hits, want limit+1 = 4", len(parsed.hits))
	}
}

func TestDDGParseResultsNoResultsPage(t *testing.T) {
	parsed := ddgParseResults(readDDGFixture(t, "no_results.html"), 5)
	if !parsed.noResults || len(parsed.hits) != 0 {
		t.Fatalf("parsed = %+v, want noResults with no hits", parsed)
	}
}

func TestDDGParseResultsPairsSnippetWithinContainer(t *testing.T) {
	page := `<div class="result web-result"><h2><a class="result__a" href="https://a.example/">A</a></h2></div>` +
		`<div class="result result--ad"><a class="result__a" href="https://duckduckgo.com/y.js?ad_domain=x">Ad</a><a class="result__snippet">ad copy</a></div>` +
		`<div class="result web-result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fduckduckgo.com%2Fy.js%3Fad_domain%3Dy">Wrapped ad</a></div>` +
		`<div class="result web-result"><a class="result__a" href="javascript:alert(1)">bad</a></div>` +
		`<div class="result web-result"><a class="result__a" href="https://b.example/?q=100%25">B &amp; <b>bold</b></a><a class="result__snippet" href="https://b.example/">B &lt;snippet&gt;</a></div>`
	parsed := ddgParseResults(page, 5)
	if len(parsed.hits) != 2 {
		t.Fatalf("hits = %+v, want 2 organic", parsed.hits)
	}
	if parsed.hits[0].snippet != "" {
		t.Fatalf("first hit took snippet %q from another container", parsed.hits[0].snippet)
	}
	second := parsed.hits[1]
	if second.title != "B & bold" || second.snippet != "B <snippet>" || second.url != "https://b.example/?q=100%25" {
		t.Fatalf("second hit = %+v", second)
	}
	if parsed.skipped != 2 || parsed.dropped != 1 || parsed.wrapped != 1 {
		t.Fatalf("counts skipped=%d dropped=%d wrapped=%d, want 2/1/1", parsed.skipped, parsed.dropped, parsed.wrapped)
	}
}

func TestDDGIsChallenge(t *testing.T) {
	if !ddgIsChallenge(readDDGFixture(t, "challenge.html")) {
		t.Fatal("challenge page not detected")
	}
	for _, name := range []string{"results_post.html", "results_get_uddg.html", "no_results.html"} {
		if ddgIsChallenge(readDDGFixture(t, name)) {
			t.Fatalf("%s misdetected as a challenge", name)
		}
	}
}

func TestSearchDuckDuckGoPostsFormAndReturnsOrganicHits(t *testing.T) {
	page := readDDGFixture(t, "results_post.html")
	useDDGServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/html/" {
			t.Errorf("request %s %s, want POST /html/", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != duckduckgoUserAgent {
			t.Errorf("User-Agent = %q", got)
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("q") != "golang" || r.URL.RawQuery != "" {
			t.Errorf("form = %v, query = %q, err = %v", r.PostForm, r.URL.RawQuery, err)
		}
		_, _ = w.Write([]byte(page))
	})
	outcome, err := SearchDuckDuckGo(context.Background(), "", SearchRequest{Query: "  golang ", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Query != "golang" || outcome.Backend != duckduckgoBackend || len(outcome.Hits) != 5 || !outcome.Truncated {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.Hits[0].Rank != 1 || outcome.Hits[0].URL != "https://go.dev/" {
		t.Fatalf("first hit = %+v", outcome.Hits[0])
	}
	if outcome.Notes[0] != duckduckgoFragilityNote {
		t.Fatalf("notes = %q", outcome.Notes)
	}
}

func TestSearchDuckDuckGoBlocked(t *testing.T) {
	challenge := readDDGFixture(t, "challenge.html")
	for _, status := range []int{http.StatusAccepted, http.StatusOK} {
		useDDGServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(challenge))
		})
		_, err := SearchDuckDuckGo(context.Background(), "", SearchRequest{Query: "golang"})
		if !errors.Is(err, ErrDuckDuckGoBlocked) || !strings.Contains(err.Error(), "do not retry") {
			t.Fatalf("status %d: err = %v, want ErrDuckDuckGoBlocked with no-retry guidance", status, err)
		}
	}
}

func TestSearchDuckDuckGoNoResultsIsEmptySuccess(t *testing.T) {
	page := readDDGFixture(t, "no_results.html")
	useDDGServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(page)) })
	outcome, err := SearchDuckDuckGo(context.Background(), "", SearchRequest{Query: "qzxv7kplmwq9fjtz3rr8bnn"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Hits) != 0 || !strings.Contains(strings.Join(outcome.Notes, "\n"), "found no results") {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestSearchDuckDuckGoUnparsableMarkupIsNamedError(t *testing.T) {
	useDDGServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body><p>something new</p></body></html>"))
	})
	_, err := SearchDuckDuckGo(context.Background(), "", SearchRequest{Query: "golang"})
	if err == nil || !strings.Contains(err.Error(), "no parsable results") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchDuckDuckGoStatusErrors(t *testing.T) {
	cases := map[int]string{
		http.StatusFound:           "redirect",
		http.StatusTooManyRequests: "HTTP 429",
		http.StatusForbidden:       "HTTP 403",
		http.StatusBadGateway:      "HTTP 502",
	}
	for status, want := range cases {
		useDDGServer(t, func(w http.ResponseWriter, r *http.Request) {
			if status == http.StatusFound {
				w.Header().Set("Location", "/html/")
			}
			w.WriteHeader(status)
		})
		_, err := SearchDuckDuckGo(context.Background(), "", SearchRequest{Query: "golang"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("status %d: err = %v, want %q", status, err, want)
		}
	}
}

func TestSearchDuckDuckGoSpacesConcurrentRequests(t *testing.T) {
	page := readDDGFixture(t, "results_post.html")
	var mu sync.Mutex
	var active, maxActive int
	var starts []time.Time
	useDDGServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		starts = append(starts, time.Now())
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		_, _ = w.Write([]byte(page))
	})
	duckduckgoMinGap = 50 * time.Millisecond

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := SearchDuckDuckGo(context.Background(), "", SearchRequest{Query: "golang"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("max concurrent requests = %d, want 1", maxActive)
	}
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < 50*time.Millisecond {
			t.Fatalf("request %d started %s after the previous one, want >= 50ms", i, gap)
		}
	}
}

func TestSearchDuckDuckGoCancelWhileQueued(t *testing.T) {
	useDDGServer(t, func(w http.ResponseWriter, r *http.Request) {})
	duckduckgoTurn <- struct{}{} // another search holds the slot
	defer func() { <-duckduckgoTurn }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := SearchDuckDuckGo(ctx, "", SearchRequest{Query: "golang"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
}
