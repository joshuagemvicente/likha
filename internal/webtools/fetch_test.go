package webtools

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestFetchTimesOutOnStalledServer connects web_fetch to a peer that never
// answers the TLS handshake. Without a fetch budget the call hangs forever;
// with it the fetch fails with a named timeout. The consent prompt takes
// longer than the budget to show that deciding is not counted against it.
func TestFetchTimesOutOnStalledServer(t *testing.T) {
	oldTimeout, oldDial := fetchTimeout, fetchDial
	t.Cleanup(func() { fetchTimeout, fetchDial = oldTimeout, oldDial })
	fetchTimeout = 200 * time.Millisecond

	var dials atomic.Int32
	var peers []net.Conn
	t.Cleanup(func() {
		for _, peer := range peers {
			peer.Close()
		}
	})
	fetchDial = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		local, peer := net.Pipe() // the peer end is never read: a stalled server
		peers = append(peers, peer)
		return local, nil
	}
	consent := func(context.Context, ConsentScope) (bool, error) {
		time.Sleep(2 * fetchTimeout)
		return true, nil
	}

	done := make(chan error, 1)
	go func() {
		_, err := Fetch(context.Background(), consent, FetchRequest{URL: "https://example.com/"})
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("web_fetch did not return from a stalled server")
	}
	if !errors.Is(err, errFetchTimedOut) || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("Fetch error = %v, want a timeout after 200ms", err)
	}
	if dials.Load() == 0 {
		t.Fatal("the budget expired during the consent prompt; no connection was attempted")
	}
}

// TestFetchKeepsCallerCancellation checks the caller's own deadline is
// reported as such, not as the fetch budget.
func TestFetchKeepsCallerCancellation(t *testing.T) {
	oldDial := fetchDial
	t.Cleanup(func() { fetchDial = oldDial })
	fetchDial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	consent := func(context.Context, ConsentScope) (bool, error) { return true, nil }
	_, err := Fetch(ctx, consent, FetchRequest{URL: "https://example.com/"})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errFetchTimedOut) {
		t.Fatalf("Fetch error = %v, want the caller's deadline", err)
	}
}

// TestFetchRedirectConsentDoesNotRefillBudget spends most of the budget on a
// first hop that redirects to a new origin, takes longer than the whole
// budget to consent, and then stalls. The prompt must neither count against
// the budget nor restart it: the fetch times out once the time left over
// from the first hop is spent.
func TestFetchRedirectConsentDoesNotRefillBudget(t *testing.T) {
	oldTimeout, oldDial, oldRoots := fetchTimeout, fetchDial, fetchRootCAs
	t.Cleanup(func() { fetchTimeout, fetchDial, fetchRootCAs = oldTimeout, oldDial, oldRoots })
	fetchTimeout = time.Second
	const firstHop = 600 * time.Millisecond

	// httptest's certificate is valid for example.com, served on loopback.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(firstHop)
		http.Redirect(w, r, "https://example.org/", http.StatusFound)
	}))
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	fetchRootCAs = roots

	var peers []net.Conn
	t.Cleanup(func() {
		for _, peer := range peers {
			peer.Close()
		}
	})
	fetchDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "example.com:443" {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", server.Listener.Addr().String())
		}
		local, peer := net.Pipe() // example.org never answers the handshake
		peers = append(peers, peer)
		return local, nil
	}
	var consented atomic.Int64
	consent := func(_ context.Context, scope ConsentScope) (bool, error) {
		if scope.Origin != "https://example.com" {
			time.Sleep(2 * fetchTimeout)
			consented.Store(time.Now().UnixNano())
		}
		return true, nil
	}

	done := make(chan error, 1)
	go func() {
		_, err := Fetch(context.Background(), consent, FetchRequest{URL: "https://example.com/"})
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("web_fetch did not return from a stalled redirect target")
	}
	if !errors.Is(err, errFetchTimedOut) {
		t.Fatalf("Fetch error = %v, want the fetch budget", err)
	}
	if consented.Load() == 0 {
		t.Fatal("the redirect's consent prompt was never reached")
	}
	// About 400ms were left; a refilled budget would allow the full second.
	if after := time.Since(time.Unix(0, consented.Load())); after > fetchTimeout-firstHop/2 {
		t.Fatalf("the fetch ran %s after the redirect consent; the budget was refilled", after)
	}
}

func TestFetchBudgetPauseKeepsRemainingTime(t *testing.T) {
	expired := make(chan time.Time, 1)
	budget := startFetchBudget(300*time.Millisecond, func() { expired <- time.Now() })
	defer budget.stop()
	time.Sleep(200 * time.Millisecond)
	if !budget.pause() {
		t.Fatal("pause reported a spent budget with time left")
	}
	time.Sleep(400 * time.Millisecond) // longer than the whole budget
	select {
	case <-expired:
		t.Fatal("the budget ran out while paused")
	default:
	}
	resumed := time.Now()
	budget.resume()
	select {
	case at := <-expired:
		if left := at.Sub(resumed); left > 250*time.Millisecond {
			t.Fatalf("budget expired %s after resume, want about 100ms", left)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the resumed budget never expired")
	}
}

func TestFetchBudgetPauseReportsSpentBudget(t *testing.T) {
	expired := make(chan struct{})
	budget := startFetchBudget(time.Millisecond, func() { close(expired) })
	<-expired
	if budget.pause() {
		t.Fatal("pause reported time left on a spent budget")
	}
}
