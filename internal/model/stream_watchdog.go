package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Stall limits for model HTTP traffic. Hosted providers send response
// headers as soon as they accept a streaming request, so a short header
// limit is safe for them. Local servers (Ollama, llama.cpp) may send none
// until the model is loaded and the prompt evaluated, which takes minutes on
// a CPU, so loopback requests get no header limit; their bodies still have
// the idle limit. Reasoning models can think for minutes before emitting a
// token, and some providers send nothing (not even keep-alives) while they
// do, so the body limit only catches a connection that has truly gone quiet.
const (
	modelResponseHeaderTimeout = 2 * time.Minute
	modelStreamIdleTimeout     = 5 * time.Minute
)

// ErrStreamStalled marks a model stream that delivered no bytes within the
// idle limit; the request was cancelled rather than left hanging.
var ErrStreamStalled = errors.New("model stream stalled")

// streamIdleTimeout is the idle limit applied to stream bodies. It is a
// variable only so tests can shorten it.
var streamIdleTimeout = modelStreamIdleTimeout

// errResponseHeaderTimeout marks a server that accepted a model request but
// sent no response headers within the limit.
var errResponseHeaderTimeout = errors.New("model server sent no response headers")

// modelTransport is shared by every model client so a server that accepts a
// connection but never answers cannot block a turn forever.
var modelTransport = newModelTransport(modelResponseHeaderTimeout)

func newModelTransport(headerTimeout time.Duration) *headerTimeoutTransport {
	return &headerTimeoutTransport{timeout: headerTimeout}
}

// headerTimeoutTransport bounds the wait for response headers from a
// non-loopback host. It resolves
// http.DefaultTransport on every request instead of cloning it once, so
// connection pooling and any process-wide routing installed there (proxies,
// test reroutes) still apply to model traffic.
type headerTimeoutTransport struct {
	timeout time.Duration
}

func (t *headerTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if isLoopbackHost(req.URL.Hostname()) {
		return http.DefaultTransport.RoundTrip(req)
	}
	ctx, cancel := context.WithCancel(req.Context())
	timer := time.AfterFunc(t.timeout, cancel)
	resp, err := http.DefaultTransport.RoundTrip(req.WithContext(ctx))
	fired := !timer.Stop()
	if err == nil && !fired {
		// The derived context also governs the body, so it lives until Close.
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
	if err == nil {
		resp.Body.Close() // Headers raced the timer; the cancelled body is unusable.
	}
	cancel()
	if !fired {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("%w within %s: %w", errResponseHeaderTimeout, t.timeout, err)
	}
	return nil, fmt.Errorf("%w within %s", errResponseHeaderTimeout, t.timeout)
}

// isLoopbackHost reports whether host names this machine: localhost,
// 127.0.0.0/8 or ::1.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// cancelOnClose releases a request context once its response body is done.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// withStreamWatchdog derives the context a streaming request must be sent
// with. watch wraps the response body so that limit of silence cancels the
// request; the caller must defer stop. The consumer should keep reading with
// the parent context so a stall is reported as ErrStreamStalled, not as a
// user cancellation.
func withStreamWatchdog(ctx context.Context, limit time.Duration) (requestCtx context.Context, watch func(io.ReadCloser) io.ReadCloser, stop func()) {
	requestCtx, cancel := context.WithCancel(ctx)
	watch = func(body io.ReadCloser) io.ReadCloser {
		return newIdleWatchdog(body, limit, cancel)
	}
	return requestCtx, watch, cancel
}

// idleWatchdog cancels its request when Read delivers no bytes for limit.
// Cancelling the request context is the documented way to unblock a pending
// body Read; the read error it causes is then replaced by ErrStreamStalled.
type idleWatchdog struct {
	body    io.ReadCloser
	limit   time.Duration
	timer   *time.Timer
	stalled atomic.Bool
}

func newIdleWatchdog(body io.ReadCloser, limit time.Duration, cancel context.CancelFunc) *idleWatchdog {
	w := &idleWatchdog{body: body, limit: limit}
	w.timer = time.AfterFunc(limit, func() {
		w.stalled.Store(true)
		cancel()
	})
	return w
}

func (w *idleWatchdog) Read(p []byte) (int, error) {
	n, err := w.body.Read(p)
	if n > 0 && !w.stalled.Load() {
		w.timer.Reset(w.limit)
	}
	if err != nil && w.stalled.Load() {
		return n, fmt.Errorf("%w: no data for %s", ErrStreamStalled, w.limit)
	}
	return n, err
}

func (w *idleWatchdog) Close() error {
	w.timer.Stop()
	return w.body.Close()
}
