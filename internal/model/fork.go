package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Fork creates an independent request client for the parent's active model.
// Endpoint, model, static key, and provider session identity are inherited only
// in memory; no configuration or credentials are resolved, loaded, or persisted.
// Connection state starts at the parent's checked snapshot, while rate limits,
// token usage, request IDs, and both client mutexes start fresh.
//
// Call at a safe run boundary, with configuration setters quiescent until the
// snapshot completes. Existing setters do not take c.mu: that lock protects the
// checked snapshot, not concurrent model/session/transport reconfiguration.
// The inherited http.Client is safe for concurrent requests provided its
// transport and callbacks remain concurrency-safe and are not reconfigured.
// The caller supplies a fresh conversation and validates its context separately.
func (c *Client) Fork() (*Client, error) {
	if c == nil {
		return nil, errors.New("fork model client: parent is nil")
	}

	c.mu.Lock()
	child := &Client{
		url:           c.url,
		base:          c.base,
		model:         c.model,
		apiKey:        c.apiKey,
		checked:       c.checked,
		http:          c.http,
		sessionHeader: c.sessionHeader,
		sessionID:     c.sessionID,
		oauth:         c.oauth,
	}
	c.mu.Unlock()

	if strings.TrimSpace(child.model) == "" {
		return nil, errors.New("fork model client: inherited model is unavailable")
	}
	if child.http == nil {
		return nil, errors.New("fork model client: inherited HTTP client is unavailable")
	}
	if _, err := parseEndpoint(child.base); err != nil {
		return nil, errors.New("fork model client: inherited endpoint is invalid")
	}
	route := "/chat/completions"
	if child.oauth != nil {
		route = "/responses"
	}
	if child.url != child.base+route {
		return nil, errors.New("fork model client: inherited request endpoint is invalid")
	}

	if s := child.oauth; s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.client == nil || s.client.http == nil || s.lifetime == nil || s.stop == nil {
			return nil, errors.New("fork model client: inherited OAuth session is unavailable")
		}
		if child.base != ChatGPTResource || s.invalidated {
			return nil, fmt.Errorf("fork model client: %w", errChatgptLoginExpired)
		}
		if err := validateClientOAuthCredentials(s.creds); err != nil {
			return nil, fmt.Errorf("fork model client: %w", err)
		}
		if s.creds.Access == "" && s.creds.Refresh == "" {
			return nil, fmt.Errorf("fork model client: %w", errChatgptLoginExpired)
		}
		// Keep the session and owner intact. Copying credentials would reuse
		// an already-rotated token; rebinding the owner would gate refreshes
		// through a task fork and race siblings. Callbacks and invalidation
		// belong to the shared session, not to this child.
	}
	return child, nil
}

// ForkForTask forks the active client and gates each generating HTTP POST to
// its inherited request endpoint with beforeRequest. The callback must reserve
// one request atomically and be safe for concurrent calls. Internal Stream
// retries, including stream_options fallback and Codex's post-refresh retry,
// each need a separate reservation; rejected responses still consume theirs.
// A callback error prevents that attempt from reaching the transport and stays
// detectable with errors.Is through Stream's error wrapping.
//
// Cancelled requests are refused before the callback and checked again before
// transport dispatch. Model-list GETs and OAuth refreshes do not reserve a
// generating request: the shared OAuth session retains its original owner.
// The cloned http.Client preserves timeout, redirects, and its shared cookie
// jar and underlying transport. A nested task fork replaces an inherited task
// gate rather than charging both task budgets. Fork's configuration-boundary
// requirements also apply here.
func (c *Client) ForkForTask(beforeRequest func(context.Context) error) (*Client, error) {
	child, err := c.Fork()
	if err != nil {
		return nil, err
	}
	if beforeRequest == nil {
		return nil, errors.New("fork task model client: request callback is required")
	}

	// http.Client contains no mutexes. Copy only its immutable configuration;
	// the Client's own locks and request telemetry were freshly allocated by Fork.
	httpClient := *child.http
	transport := httpClient.Transport
	for {
		inherited, ok := transport.(*taskRequestTransport)
		if !ok {
			break
		}
		transport = inherited.next
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpClient.Transport = &taskRequestTransport{
		next:          transport,
		url:           child.url,
		beforeRequest: beforeRequest,
	}
	child.http = &httpClient
	return child, nil
}

// taskRequestTransport is immutable after construction. Only the generating
// endpoint is gated; credentials, request bodies, and usage are not retained.
type taskRequestTransport struct {
	next          http.RoundTripper
	url           string
	beforeRequest func(context.Context) error
}

func (t *taskRequestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if err := ctx.Err(); err != nil {
		return rejectTaskRequest(req, err)
	}
	if req.Method == http.MethodPost && req.URL.String() == t.url {
		if err := t.beforeRequest(ctx); err != nil {
			return rejectTaskRequest(req, fmt.Errorf("task model request denied: %w", err))
		}
	}
	if err := ctx.Err(); err != nil {
		return rejectTaskRequest(req, err)
	}
	return t.next.RoundTrip(req)
}

func rejectTaskRequest(req *http.Request, err error) (*http.Response, error) {
	// RoundTripper must close the body even when it refuses to send the request.
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return nil, err
}
