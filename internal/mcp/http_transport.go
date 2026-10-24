package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	// maxHTTPResponseBytes caps one response body, matching the model
	// stream cap.
	maxHTTPResponseBytes = 16 << 20
	// maxServerErrorText bounds server-supplied text shown to the user.
	maxServerErrorText = 2 << 10
	// redactedText replaces a header value wherever it would be shown.
	redactedText = "[redacted]"
)

// closeTimeout bounds the session DELETE and the cancel notification so an
// unreachable server never holds up exit. A variable so tests can shorten it.
var closeTimeout = 2 * time.Second

var errResponseTooLarge = errors.New("response exceeded 16 MiB")

// sessionExpiredError reports a 404 for a request that carried a session
// ID. restart is true for exactly one caller per expired session: that
// caller runs the new handshake.
type sessionExpiredError struct {
	restart bool
}

func (e *sessionExpiredError) Error() string {
	return "the server session expired; the call was not retried"
}

// httpTransport speaks the MCP Streamable HTTP transport: every message is
// a POST to one endpoint, answered by a JSON object or an SSE stream.
type httpTransport struct {
	endpoint  string
	headers   map[string]string
	userAgent string
	client    *http.Client
	redact    redactor

	mu         sync.Mutex
	sessionID  string
	negotiated bool // initialize succeeded: later requests carry the protocol header

	background sync.WaitGroup // cancel notifications still being sent
}

// startHTTP prepares the transport: headers are expanded here, not at load,
// so an unset variable fails this one server.
func startHTTP(name string, cfg ServerConfig, userAgent string) (*httpTransport, error) {
	headers, secrets, err := expandHeaders(cfg.Headers, os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", name, err)
	}
	base := http.DefaultTransport.(*http.Transport).Clone() // keeps proxy settings
	return &httpTransport{
		endpoint:  strings.TrimSpace(cfg.URL),
		headers:   headers,
		userAgent: userAgent,
		client: &http.Client{
			Transport: base,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		redact: newRedactor(secrets),
	}, nil
}

func (t *httpTransport) call(ctx context.Context, id int, payload []byte) (rpcResponse, error) {
	method := messageMethod(payload)
	reply, err := t.exchange(ctx, id, method, payload)
	if err != nil && ctx.Err() != nil && method != "initialize" {
		t.sendCancel(id)
	}
	if err != nil {
		return rpcResponse{}, t.redact.wrap(err)
	}
	return reply, nil
}

func (t *httpTransport) exchange(ctx context.Context, id int, method string, payload []byte) (rpcResponse, error) {
	resp, session, err := t.post(ctx, payload)
	if err != nil {
		return rpcResponse{}, err
	}
	defer resp.Body.Close()
	if err := t.checkStatus(resp, session, true); err != nil {
		return rpcResponse{}, err
	}
	if resp.StatusCode == http.StatusAccepted {
		return rpcResponse{}, errors.New("server accepted the request without answering it")
	}
	body := &cappedReader{r: resp.Body, left: maxHTTPResponseBytes}
	var reply rpcResponse
	switch contentType(resp) {
	case "application/json":
		reply, err = t.readJSON(body, id)
	case "text/event-stream":
		reply, err = t.readStream(ctx, body, id)
	case "text/html":
		return rpcResponse{}, errors.New("not an MCP endpoint (the server returned an HTML page)")
	default:
		return rpcResponse{}, fmt.Errorf("not an MCP endpoint (unexpected response type %q)", contentType(resp))
	}
	if err != nil {
		if ctx.Err() != nil {
			return rpcResponse{}, fmt.Errorf("request timed out: %w", ctx.Err())
		}
		return rpcResponse{}, err
	}
	if method == "initialize" && reply.Error == nil {
		t.mu.Lock()
		if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
			t.sessionID = session
		}
		t.negotiated = true
		t.mu.Unlock()
	}
	return reply, nil
}

func (t *httpTransport) notify(ctx context.Context, payload []byte) error {
	resp, session, err := t.post(ctx, payload)
	if err != nil {
		return t.redact.wrap(err)
	}
	defer resp.Body.Close()
	if err := t.checkStatus(resp, session, false); err != nil {
		return t.redact.wrap(err)
	}
	return nil
}

// post sends one message and returns the session ID it carried.
func (t *httpTransport) post(ctx context.Context, payload []byte) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	session := t.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", fmt.Errorf("request timed out: %w", ctx.Err())
		}
		return nil, "", connectError(err)
	}
	return resp, session, nil
}

// setHeaders applies the user's headers first so the protocol headers that
// follow always win.
func (t *httpTransport) setHeaders(req *http.Request) string {
	for name, value := range t.headers {
		req.Header.Set(name, value)
	}
	if t.userAgent != "" {
		req.Header.Set("User-Agent", t.userAgent)
	}
	t.mu.Lock()
	session, negotiated := t.sessionID, t.negotiated
	t.mu.Unlock()
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if negotiated {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	return session
}

// checkStatus turns a non-success answer into a plain-words error. A 404 on
// a request that carried a session ID expires that session; only one caller
// per session (and only from a request) is told to restart it.
func (t *httpTransport) checkStatus(resp *http.Response, session string, request bool) error {
	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		return nil
	case status >= 300 && status < 400:
		return fmt.Errorf("server redirected to %s; update the url", t.redirectOrigin(resp))
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return t.authError(resp)
	case status == http.StatusNotFound && session != "":
		restart := false
		if request {
			t.mu.Lock()
			if t.sessionID == session {
				t.sessionID, t.negotiated, restart = "", false, true
			}
			t.mu.Unlock()
		}
		return &sessionExpiredError{restart: restart}
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return fmt.Errorf("not an MCP endpoint (HTTP %d)", status)
	}
	detail := t.serverText(resp.Body)
	if detail == "" {
		return fmt.Errorf("server answered HTTP %d", status)
	}
	return fmt.Errorf("server answered HTTP %d: %s", status, detail)
}

func (t *httpTransport) authError(resp *http.Response) error {
	challenge := strings.ToLower(resp.Header.Get("WWW-Authenticate"))
	oauth := strings.Contains(challenge, "resource_metadata")
	if !oauth && strings.HasPrefix(strings.TrimSpace(challenge), "bearer") && !t.hasHeader("Authorization") {
		oauth = true
	}
	msg := fmt.Sprintf("rejected the credentials (HTTP %d)", resp.StatusCode)
	if oauth {
		msg += "; OAuth sign-in is not supported yet"
	}
	return errors.New(msg)
}

func (t *httpTransport) hasHeader(name string) bool {
	for header := range t.headers {
		if strings.EqualFold(header, name) {
			return true
		}
	}
	return false
}

func (t *httpTransport) redirectOrigin(resp *http.Response) string {
	location, err := resp.Location()
	if err != nil {
		return "another address"
	}
	return origin(location.String())
}

// serverText reads a bounded, redacted excerpt of an error body.
func (t *httpTransport) serverText(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, 64<<10))
	return t.redact.bounded(strings.TrimSpace(string(raw)))
}

func (t *httpTransport) readJSON(body io.Reader, id int) (rpcResponse, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return rpcResponse{}, fmt.Errorf("read response: %w", err)
	}
	var message rpcMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return rpcResponse{}, fmt.Errorf("server sent malformed JSON: %w", err)
	}
	reply, ok, err := t.response(message, id)
	if err != nil {
		return rpcResponse{}, err
	}
	if !ok {
		return rpcResponse{}, errors.New("server answered a different request")
	}
	return reply, nil
}

// readStream reads SSE events until the response for id arrives. Server
// requests on the stream are refused; notifications are ignored.
func (t *httpTransport) readStream(ctx context.Context, body io.Reader, id int) (rpcResponse, error) {
	reader := bufio.NewReader(body)
	var data strings.Builder
	hasData := false
	dispatch := func() (rpcResponse, bool, error) {
		if !hasData {
			return rpcResponse{}, false, nil
		}
		raw := data.String()
		data.Reset()
		hasData = false
		var message rpcMessage
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			return rpcResponse{}, false, nil // unparseable event: ignored, as on stdio
		}
		if message.Method != "" {
			if message.isRequest() {
				t.answerServerRequest(ctx, message)
			}
			return rpcResponse{}, false, nil
		}
		return t.response(message, id)
	}
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if reply, ok, err := dispatch(); err != nil || ok {
				return reply, err
			}
		case strings.HasPrefix(line, "data:"):
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			if reply, ok, err := dispatch(); err != nil || ok {
				return reply, err
			}
			return rpcResponse{}, errors.New("the server closed the response stream before answering")
		}
		if errors.Is(readErr, errResponseTooLarge) {
			return rpcResponse{}, readErr
		}
		return rpcResponse{}, fmt.Errorf("the connection dropped: %w", readErr)
	}
}

// response converts a JSON-RPC response for id, redacting header values from
// its error text and result. ok is false for a response to another request.
func (t *httpTransport) response(message rpcMessage, id int) (rpcResponse, bool, error) {
	got, err := strconv.Atoi(strings.TrimSpace(string(message.ID)))
	if err != nil || got != id {
		return rpcResponse{}, false, nil
	}
	reply := rpcResponse{JSONRPC: "2.0", ID: id, Result: message.Result, Error: message.Error}
	if reply.Error != nil {
		reply.Error = &rpcError{Code: reply.Error.Code, Message: t.redact.bounded(reply.Error.Message)}
	}
	if len(reply.Result) > 0 {
		cleaned := []byte(t.redact.apply(string(reply.Result)))
		if !json.Valid(cleaned) {
			return rpcResponse{}, false, errors.New("the server's answer contained a configured header value and could not be cleaned")
		}
		reply.Result = cleaned
	}
	return reply, true, nil
}

// answerServerRequest answers a request arriving on a response stream (see
// serverRequestReply) so the server is not left waiting.
func (t *httpTransport) answerServerRequest(ctx context.Context, request rpcMessage) {
	payload, err := serverRequestReply(request)
	if err != nil {
		return // best effort: the response we wait for still bounds the call
	}
	resp, _, err := t.post(ctx, payload)
	if err != nil {
		return // best effort: the response we wait for still bounds the call
	}
	resp.Body.Close()
}

// sendCancel tells the server a request was abandoned. It runs in the
// background, bounded by closeTimeout; close waits for it.
func (t *httpTransport) sendCancel(id int) {
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: "notifications/cancelled", Params: map[string]any{"requestId": id, "reason": "cancelled by the user"}})
	if err != nil {
		return
	}
	t.background.Add(1)
	go func() {
		defer t.background.Done()
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		_ = t.notify(ctx, payload) // best effort, as the MCP spec allows
	}()
}

// close ends the server session with a bounded DELETE and releases idle
// connections. A 405 (session end not supported) or any failure is ignored.
func (t *httpTransport) close() error {
	t.mu.Lock()
	session := t.sessionID
	t.sessionID = ""
	t.mu.Unlock()
	if session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.endpoint, nil)
		if err == nil {
			for name, value := range t.headers {
				req.Header.Set(name, value)
			}
			req.Header.Set("Mcp-Session-Id", session)
			req.Header.Set("MCP-Protocol-Version", protocolVersion)
			if resp, err := t.client.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	t.background.Wait()
	t.client.CloseIdleConnections()
	return nil
}

// rpcMessage is any JSON-RPC message a server may send on HTTP: a response,
// a request (method and id) or a notification (method only).
type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (m rpcMessage) isRequest() bool {
	id := strings.TrimSpace(string(m.ID))
	return id != "" && id != "null"
}

func messageMethod(payload []byte) string {
	var message struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(payload, &message)
	return message.Method
}

func contentType(resp *http.Response) string {
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	}
	return strings.ToLower(media)
}

// connectError names a failure to reach the server. The URL is dropped from
// the message so its path and query never reach the screen.
func connectError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &verify) || errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return fmt.Errorf("certificate not trusted: %w", err)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return fmt.Errorf("the connection dropped: %w", err)
	}
	return fmt.Errorf("could not connect: %w", err)
}

// origin reduces a URL to scheme://host[:port]: never a path, query or user.
func origin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "an unknown address"
	}
	return u.Scheme + "://" + u.Host
}

// cappedReader fails with errResponseTooLarge once more than left bytes
// have been read.
type cappedReader struct {
	r    io.Reader
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var probe [1]byte
		if n, _ := c.r.Read(probe[:]); n > 0 {
			return 0, errResponseTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// redactor removes configured header values from text shown to the user or
// returned to the model. Values shorter than minSecretLength are not
// secret-shaped and would mangle ordinary text, so they are left alone.
type redactor struct {
	secrets []string
}

const minSecretLength = 4

func newRedactor(values []string) redactor {
	seen := map[string]bool{}
	var secrets []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) < minSecretLength || seen[value] {
			return
		}
		seen[value] = true
		secrets = append(secrets, value)
	}
	for _, value := range values {
		add(value)
		// "Bearer <token>": a server may echo the token without the scheme.
		for _, field := range strings.Fields(value) {
			if len(field) >= 8 {
				add(field)
			}
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return redactor{secrets: secrets}
}

func (r redactor) apply(text string) string {
	for _, secret := range r.secrets {
		text = strings.ReplaceAll(text, secret, redactedText)
	}
	return text
}

// bounded redacts first, so a cut never leaves half a secret behind, then
// limits the text to maxServerErrorText.
func (r redactor) bounded(text string) string {
	text = r.apply(text)
	if len(text) <= maxServerErrorText {
		return text
	}
	cut := maxServerErrorText
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

func (r redactor) wrap(err error) error {
	if err == nil || len(r.secrets) == 0 {
		return err
	}
	return &redactedError{err: err, r: r}
}

// redactedError keeps the cause for errors.Is/As while its message never
// shows a header value.
type redactedError struct {
	err error
	r   redactor
}

func (e *redactedError) Error() string { return e.r.apply(e.err.Error()) }

func (e *redactedError) Unwrap() error { return e.err }
