//go:build !windows

package model

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestBrowserLoginCancellationReapsHangingSystemLauncher(t *testing.T) {
	fixture := newOAuthFixture(t)
	bin := t.TempDir()
	pidPath, urlPath := filepath.Join(bin, "launcher.pid"), filepath.Join(bin, "launcher.url")
	script := "#!/bin/sh\nprintf '%s' \"$$\" > \"$LIKHA_TEST_LAUNCH_PID\"\nprintf '%s' \"$1\" > \"$LIKHA_TEST_LAUNCH_URL\"\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, openBrowserCommand()), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LIKHA_TEST_LAUNCH_PID", pidPath)
	t.Setenv("LIKHA_TEST_LAUNCH_URL", urlPath)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := BrowserLogin(ctx, BrowserLoginOptions{HostID: oauthFixtureHostID, HTTPClient: fixture.client})
		done <- err
	}()
	var rawURL, rawPID []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rawURL, _ = os.ReadFile(urlPath)
		rawPID, _ = os.ReadFile(pidPath)
		if len(rawURL) != 0 && len(rawPID) != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(rawURL) == 0 || len(rawPID) == 0 {
		t.Fatal("fake system launcher did not start")
	}
	pid, err := strconv.Atoi(string(rawPID))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("hanging system launcher cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("hanging system launcher held the callback listener alive")
	}
	admission, _ := url.Parse(string(rawURL))
	callback, _ := url.Parse(admission.Query().Get("redirect_uri"))
	requireOAuthListenerClosed(t, callback.Host)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return // CommandContext killed the process and Run reaped it.
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cancelled browser launcher process was not reaped")
}
