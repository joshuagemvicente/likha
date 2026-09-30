package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withEndpoint(t *testing.T, url string) {
	t.Setenv("LISA_UPDATE_API", url)
}

func serveTag(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", got)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q", r.Method)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestParsesReleaseTag(t *testing.T) {
	srv := serveTag(t, http.StatusOK, `{"tag_name":"v0.2.0"}`+"\n")
	withEndpoint(t, srv.URL)
	got, err := Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != "v0.2.0" {
		t.Fatalf("Latest = %q", got)
	}
}

func TestLatestTruncatesOversizedBody(t *testing.T) {
	// A valid tag followed by far more than the 64KiB limit of garbage; the
	// decoder must stop reading once it has the tag payload.
	huge := `{"tag_name":"v9.9.9"}` + strings.Repeat("0", 200<<10)
	srv := serveTag(t, http.StatusOK, huge)
	withEndpoint(t, srv.URL)
	got, err := Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != "v9.9.9" {
		t.Fatalf("Latest = %q", got)
	}
}

func TestLatestNon200(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusNotFound} {
		srv := serveTag(t, status, `{}`)
		withEndpoint(t, srv.URL)
		if _, err := Latest(context.Background()); err == nil {
			t.Fatalf("status %d: want error", status)
		}
	}
}

func TestLatestUnparseableTag(t *testing.T) {
	for _, body := range []string{`{"tag_name":"notaversion"}`, `{"tag_name":"0.1.0"}`, `{"tag_name":""}`, `{}`, `garbage, not json`} {
		srv := serveTag(t, http.StatusOK, body)
		withEndpoint(t, srv.URL)
		if _, err := Latest(context.Background()); err == nil {
			t.Fatalf("body %q: want error", body)
		}
	}
}

// TestLatestNoNetworkFastFailure pins the offline contract: a refused
// connection must surface as an error quickly, so the caller's silent-on-
// failure path never stalls the TUI. Skipped under WSL1 where loopback
// connections may hang instead of refusing.
func TestLatestNoNetworkFastFailure(t *testing.T) {
	addr, free := freePort(t)
	if !free {
		t.Skipf("port %d busy; refusal verdict unreliable", addr)
	}
	withEndpoint(t, fmt.Sprintf("http://127.0.0.1:%d/releases/latest", addr))

	start := time.Now()
	_, err := Latest(context.Background())
	if err == nil {
		t.Fatal("want error for unreachable endpoint")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("refused connection took %s to fail", elapsed)
	}
}

func freePort(t *testing.T) (int, bool) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, false
	}
	addr := l.Addr().(*net.TCPAddr).Port
	// Close the listener without lingering: accepted a closed port below.
	_ = l.Close()
	http.DefaultClient.CloseIdleConnections()
	return addr, true
}

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.1.0", "v0.1.1", true},
		{"v0.1.0", "v1.0.0", true},
		{"v0.9.0", "v0.10.0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.1.0", "v1.0.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"dev", "v999.0.0", false},
		{"", "v1.0.0", false},
		{"v0.1.0", "banana", false},
		{"v0.1.0", "v0.1", false},
		{"v0.1.0", "v0.1.x", false},
		{"v0.1.0", "v0.1.0-rc1", false},
		{"v0.1.0-rc1", "v0.1.0-rc2", true},
		{"v0.1.0-rc2", "v0.1.0-rc1", false},
		{"v0.1.0-rc1", "v0.1.0", true},
		{"v0.1.0", "v0.2.0-rc1", true},
		{"v0.1.0", "v0.2.0-alpha.2", true},
		{"v0.1.0", "v1.0.0-beta", true},
		{"dev", "v1.0.0", false},
	}
	for _, tc := range cases {
		if got := Newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestNewerNumericNotLexicographic(t *testing.T) {
	// Two-digit identifiers must compare as integers.
	if Newer("v0.3.9", "v0.10.0") != true {
		t.Error("v0.10.0 should beat v0.3.9 numerically")
	}
	if Newer("v0.10.0", "v0.3.9") != false {
		t.Error("v0.3.9 should not beat v0.10.0")
	}
}

func TestPreparedWithinWindowThrottled(t *testing.T) {
	dir := t.TempDir()
	if err := writeState(dir, time.Now().UnixNano()); err != nil {
		t.Fatalf("seed throttle: %v", err)
	}
	ok, mark, err := Prepared(dir)
	if err != nil {
		t.Fatalf("Prepared: %v", err)
	}
	if ok || mark != nil {
		t.Fatalf("inside window: ok = %v, mark != nil: %t", ok, mark != nil)
	}
}

func TestPreparedOutsideWindowAllowsCheck(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-25 * time.Hour).UnixNano()
	if err := writeState(dir, old); err != nil {
		t.Fatalf("seed throttle: %v", err)
	}
	ok, mark, err := Prepared(dir)
	if err != nil || !ok {
		t.Fatalf("Prepared = (%v, %v)", ok, err)
	}
	if mark == nil {
		t.Fatal("mark = nil, want non-nil")
	}
}

func TestPreparedFreshDirNoState(t *testing.T) {
	dir := t.TempDir()
	ok, mark, err := Prepared(dir)
	if err != nil || !ok {
		t.Fatalf("Prepared = (%v, %v)", ok, err)
	}
	if mark == nil {
		t.Fatal("mark = nil, want non-nil")
	}
}

func TestPreparedNoDirStillAllows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "nested")
	ok, mark, err := Prepared(dir)
	if err != nil || !ok {
		t.Fatalf("Prepared = (%v, %v)", ok, err)
	}
	if mark == nil {
		t.Fatal("mark = nil, want non-nil")
	}
}

func TestMarkPersistsTimestamp(t *testing.T) {
	dir := t.TempDir()
	_, mark, err := Prepared(dir)
	if err != nil {
		t.Fatalf("Prepared: %v", err)
	}
	before := time.Now().Add(-time.Second).UnixNano()
	if err = mark(); err != nil {
		t.Fatalf("mark: %v", err)
	}
	after := time.Now().Add(time.Second).UnixNano()

	data, err := os.ReadFile(filepath.Join(dir, throttleFile))
	if err != nil {
		t.Fatalf("reading persisted file: %v", err)
	}
	var st state
	if json.Unmarshal(data, &st) != nil {
		t.Fatalf("persisted file %q is not valid state JSON", data)
	}
	if st.CheckedNS < before || st.CheckedNS > after {
		t.Fatalf("checked_ns = %d, want within [%d, %d]", st.CheckedNS, before, after)
	}
	info, err := os.Stat(filepath.Join(dir, throttleFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("file perms = %#o, want no group/world access", perm)
	}
}

// TestThrottleRoundTripAfterMark closes the loop: mark() must persist a
// timestamp that immediately throttles the next Prepared call.
func TestThrottleRoundTripAfterMark(t *testing.T) {
	dir := t.TempDir()
	ok, mark, err := Prepared(dir)
	if err != nil || !ok {
		t.Fatalf("Prepared = (%v, %v)", ok, err)
	}
	if err = mark(); err != nil {
		t.Fatalf("mark: %v", err)
	}
	ok, _, err = Prepared(dir)
	if err != nil {
		t.Fatalf("Prepared after mark: %v", err)
	}
	if ok {
		t.Fatal("Prepared = true immediately after mark, want throttled")
	}
}

func TestPreparedOptOut(t *testing.T) {
	dir := t.TempDir()
	// No state at all — only the opt-out env decides this.
	t.Setenv("LISA_UPDATE_CHECK", "0")
	ok, mark, err := Prepared(dir)
	if err != nil {
		t.Fatalf("Prepared: %v", err)
	}
	if ok || mark != nil {
		t.Fatalf("opt-out: ok = %v, mark != nil: %t", ok, mark != nil)
	}

	// And even a stale throttle cannot override the opt-out.
	if err = writeState(dir, time.Now().Add(-48*time.Hour).UnixNano()); err != nil {
		t.Fatalf("seed throttle: %v", err)
	}
	ok, mark, err = Prepared(dir)
	if err != nil || ok || mark != nil {
		t.Fatalf("opt-out with stale throttle: got (ok=%v, mark!=nil: %t, err=%v)", ok, mark != nil, err)
	}
}

func TestWriteStateDanglingTempFilesCleaned(t *testing.T) {
	dir := t.TempDir()
	if err := writeState(dir, time.Now().UnixNano()); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != throttleFile {
		for _, e := range entries {
			t.Errorf("unexpected entry %q", e.Name())
		}
	}
}
