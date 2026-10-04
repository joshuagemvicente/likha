// Package model connects Likha to an OpenAI-compatible chat endpoint, local or
// hosted from the predefined accepted provider list.
package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxResponseBytes  = 16 << 20
	maxEventLineBytes = 1 << 20
	maxErrorBytes     = 64 << 10
)

type Message struct {
	Role       string
	Content    string
	Reasoning  string // thinking-model reasoning; never sent back to the provider
	ToolCalls  []ToolCall
	ToolCallID string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// UserAgent identifies Likha to providers that inspect client identity
// (OpenCode Go requires an agent user agent instead of a generic HTTP-library
// name). Run overwrites the version from the build tag.
var UserAgent = "likha/dev"

type Client struct {
	url     string // chat completions URL
	base    string // endpoint base URL for connection checks
	model   string
	apiKey  string
	checked bool
	mu      sync.Mutex
	http    *http.Client

	sessionHeader string // provider-required per-conversation session header
	sessionID     string // stable per-conversation session identifier

	oauth     *oauthSession // non-nil for OAuth providers (ChatGPT/Codex)
	oauthSave func(OAuthCredentials) error

	rateMu              sync.Mutex // guards the last-seen Codex rate-limit headers and captured token usage
	rateUsedPercent     string
	rateWindowMinutes   string
	rateResetAt         string
	tokenPrompt         int64
	tokenCompletion     int64
	tokenSeen           bool
	tokenPromptSeen     bool
	tokenCompletionSeen bool
	tokenRequest        uint64
}

// New accepts a local loopback HTTP /v1 base URL or an HTTPS endpoint from the
// predefined provider list. Plain HTTP is confined to loopback hosts so an
// endpoint cannot receive API keys or conversation content in the clear.
// Redirects are not followed: otherwise an endpoint could redirect
// conversation data to a remote host.
func New(endpoint, name, apiKey string) (*Client, error) {
	base, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("model name is required")
	}
	return &Client{
		url:    base + "/chat/completions",
		base:   base,
		model:  name,
		apiKey: apiKey,
		http:   noRedirectHTTPClient(),
	}, nil
}

// parseEndpoint validates an OpenAI-compatible base URL and returns it without
// a trailing slash.
func parseEndpoint(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid model endpoint: %w", err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	switch {
	case u.Scheme == "http":
		if !loopback || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			(u.Path != "/v1" && u.Path != "/v1/") {
			return "", errors.New("local model endpoint must be a loopback HTTP /v1 URL")
		}
	case u.Scheme == "https":
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || u.Path == "/" {
			return "", errors.New("hosted model endpoint must be an HTTPS /v1-style base URL without credentials, query, or fragment")
		}
	default:
		return "", errors.New("model endpoint must use http (loopback) or https")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func noRedirectHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// sharedListClient backs the read-only /models fetch so repeated model-list
// opens reuse keep-alive connections instead of a fresh TLS handshake.
var sharedListClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

// SetSessionHeader names the header a provider requires for per-conversation
// routing (e.g. OpenCode's x-opencode-session).
func (c *Client) SetSessionHeader(name string) {
	c.sessionHeader = name
}

// SetSession records the stable per-conversation session identifier sent in
// the provider's session header. Likha uses its own SQLite session ID.
func (c *Client) SetSession(id string) {
	c.sessionID = id
}

// SetModel switches the live client to another model on the same provider
// for subsequent turns. Stored configuration is untouched.
func (c *Client) SetModel(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("model name is required")
	}
	c.model = strings.TrimSpace(name)
	return nil
}

// Base returns the endpoint base URL the client talks to.
func (c *Client) Base() string {
	if c == nil {
		return ""
	}
	return c.base
}

// Model returns the model ID used by subsequent requests.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// APIKey returns the credential the client sends, or "" when none is set.
// It exists so the TUI can rebuild an equivalent client; it is never written
// anywhere by the UI. OAuth clients hold no static key and report "".
func (c *Client) APIKey() string {
	if c == nil || c.oauth != nil {
		return ""
	}
	return c.apiKey
}

// NewOAuth builds a client for an OAuth provider (ChatGPT/Codex). The
// endpoint follows the same validation rules as New; chat requests go to
// base + "/responses" (OpenAI Responses wire) instead of /chat/completions.
// creds is the stored login; it is refreshed transparently when expired.
func NewOAuth(baseURL, modelName, issuer, clientID string, creds OAuthCredentials) (*Client, error) {
	base, err := parseEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(modelName) == "" {
		return nil, errors.New("model name is required")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(clientID) == "" {
		return nil, errors.New("oauth issuer and client ID are required")
	}
	c := &Client{
		url:   base + "/responses",
		base:  base,
		model: modelName,
		http:  noRedirectHTTPClient(),
	}
	c.oauth = &oauthSession{issuer: issuer, clientID: clientID, creds: creds}
	c.oauth.client = c
	return c, nil
}

// SetOAuthSaver registers the callback invoked with the fresh credential
// set after every successful token refresh, so the app layer can persist
// it. Refreshes still work when unset.
func (c *Client) SetOAuthSaver(save func(OAuthCredentials) error) {
	if c == nil || c.oauth == nil {
		return
	}
	c.oauth.mu.Lock()
	c.oauthSave = save
	c.oauth.mu.Unlock()
}

// errChatgptLoginExpired marks an unusable refresh: only a new browser or
// device login fixes it.
var errChatgptLoginExpired = errors.New("chatgpt login expired; sign in again")

// oauthSession holds one OAuth provider login and mints access tokens,
// refreshing the stored credential when it is stale. A refresh is
// single-flight: concurrent callers wait for the in-flight one.
type oauthSession struct {
	client   *Client // back-reference for the shared HTTP client and token saver
	issuer   string
	clientID string

	mu    sync.Mutex
	creds OAuthCredentials

	// In-flight refresh state, guarded by mu. refreshDone is non-nil while
	// a refresh runs and is closed with the outcome.
	refreshing  bool
	refreshDone chan struct{}
	refreshTok  string
	refreshErr  error
}

// accessTokenGrace is how long before expiry a cached access token is
// considered stale, so requests never start with a token about to lapse.
const accessTokenGrace = 30 * time.Second

// refreshRequestTimeout bounds each token refresh request.
const refreshRequestTimeout = 30 * time.Second

// access returns a usable access token, refreshing the login when it is
// expired (or when a rejected request forces it). The returned error for a
// failed refresh always mentions signing in again.
func (s *oauthSession) access(ctx context.Context, force bool) (string, error) {
	s.mu.Lock()
	if !force && s.creds.Access != "" && time.Now().UnixMilli() < s.creds.Expires-accessTokenGrace.Milliseconds() {
		token := s.creds.Access
		s.mu.Unlock()
		return token, nil
	}
	if s.creds.Access == "" && s.creds.Expires == 0 {
		s.mu.Unlock()
		return "", errors.New("no stored access token; sign in again")
	}
	if s.refreshing {
		done := s.refreshDone
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-done:
		}
		s.mu.Lock()
		token, err := s.refreshTok, s.refreshErr
		s.mu.Unlock()
		return token, err
	}
	s.refreshing = true
	s.refreshDone = make(chan struct{})
	refreshToken := s.creds.Refresh
	save := s.client.oauthSave
	s.mu.Unlock()

	refreshCtx, cancel := context.WithTimeout(ctx, refreshRequestTimeout)
	defer cancel()
	ts, err := RefreshTokens(refreshCtx, s.client.http, s.issuer, s.clientID, refreshToken)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.refreshTok = ""
		s.refreshErr = fmt.Errorf("%w: %v", errChatgptLoginExpired, err)
	} else {
		creds := ts.Credentials()
		if creds.AccountID == "" {
			creds.AccountID = s.creds.AccountID // the new token set carries no account claim
		}
		s.creds = creds
		s.refreshTok = creds.Access
		s.refreshErr = nil
		if save != nil {
			_ = save(creds) // a persistence failure never fails the request
		}
	}
	s.refreshing = false
	done := s.refreshDone
	s.refreshDone = nil
	close(done)
	return s.refreshTok, s.refreshErr
}

// accountID returns the ChatGPT account ID sent with codex requests.
func (s *oauthSession) accountID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creds.AccountID
}

type requestMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []requestToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type requestToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type requestTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// Stream returns the complete assistant turn after [DONE], streaming text to
// onText and thinking-model reasoning to onReasoning. A partial turn is never
// returned as a successful answer. Reasoning is never sent back on a later
// request.
func (c *Client) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onText func(string), onReasoning func(string)) (Message, error) {
	var result Message
	if c == nil {
		return result, errors.New("model client is nil")
	}
	requestID := c.beginTokenUsage()
	if c.oauth != nil {
		return c.streamCodex(ctx, messages, tools, onText, onReasoning, requestID)
	}
	type streamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	body := struct {
		Model         string           `json:"model"`
		Messages      []requestMessage `json:"messages"`
		Tools         []requestTool    `json:"tools,omitempty"`
		Stream        bool             `json:"stream"`
		StreamOptions *streamOptions   `json:"stream_options,omitempty"`
	}{Model: c.model, Messages: make([]requestMessage, 0, len(messages)), Stream: true}
	body.StreamOptions = &streamOptions{IncludeUsage: true}
	for _, m := range messages {
		if m.Role != "system" && m.Role != "developer" && m.Role != "user" && m.Role != "assistant" && m.Role != "tool" {
			return result, fmt.Errorf("invalid message role %q", m.Role)
		}
		if m.Role == "tool" && m.ToolCallID == "" {
			return result, errors.New("tool result requires a tool call ID")
		}
		rm := requestMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, call := range m.ToolCalls {
			if m.Role != "assistant" || call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
				return result, errors.New("invalid assistant tool call")
			}
			rc := requestToolCall{ID: call.ID, Type: "function"}
			rc.Function.Name, rc.Function.Arguments = call.Name, call.Arguments
			rm.ToolCalls = append(rm.ToolCalls, rc)
		}
		body.Messages = append(body.Messages, rm)
	}
	for _, t := range tools {
		if t.Name == "" || len(t.Parameters) == 0 || !json.Valid(t.Parameters) {
			return result, fmt.Errorf("invalid tool definition %q", t.Name)
		}
		rt := requestTool{Type: "function"}
		rt.Function.Name, rt.Function.Description, rt.Function.Parameters = t.Name, t.Description, t.Parameters
		body.Tools = append(body.Tools, rt)
	}
	newRequest := func(payload []byte) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create model request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("User-Agent", UserAgent)
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		if c.sessionHeader != "" && c.sessionID != "" {
			req.Header.Set(c.sessionHeader, c.sessionID)
		}
		return req, nil
	}
	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		payload, err := json.Marshal(body)
		if err != nil {
			return result, fmt.Errorf("encode model request: %w", err)
		}
		req, err := newRequest(payload)
		if err != nil {
			return result, err
		}
		resp, err = c.http.Do(req)
		if err != nil {
			return result, fmt.Errorf("model request: %w", err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			break
		}
		detail, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return result, fmt.Errorf("model HTTP %s (reading error: %v)", resp.Status, readErr)
		}
		if len(detail) > maxErrorBytes {
			detail = detail[:maxErrorBytes]
		}
		if attempt == 0 && streamUsageOptionRejected(resp.StatusCode, detail) {
			// Some OpenAI-compatible providers reject optional stream_options.
			// The failed request did not generate a response, so retry once
			// without optional telemetry rather than breaking the user's turn.
			body.StreamOptions = nil
			continue
		}
		message := fmt.Sprintf("model HTTP %s: %s", resp.Status, strings.TrimSpace(string(detail)))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return result, fmt.Errorf("%w: %s", ErrUnauthorized, message)
		}
		return result, errors.New(message)
	}
	defer resp.Body.Close()
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return result, fmt.Errorf("model returned incompatible content type %q (expected SSE)", resp.Header.Get("Content-Type"))
	}
	message, report, err := consumeStream(ctx, io.LimitReader(resp.Body, maxResponseBytes+1), onText, onReasoning)
	if err == nil {
		c.setTokenUsage(requestID, report)
		c.markConnected()
	}
	return message, err
}

func streamUsageOptionRejected(status int, detail []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return false
	}
	message := strings.ToLower(string(detail))
	return strings.Contains(message, "stream_options") || strings.Contains(message, "include_usage")
}

// parseFinalUsage extracts prompt/completion token totals from a final
// chat-completions usage object. Both the OpenAI naming (prompt_tokens /
// completion_tokens) and the input/output aliases are accepted. ok is false
// when usage is absent or not an object; zero totals are valid usage.
func parseFinalUsage(raw json.RawMessage) (TokenUsage, bool) {
	report := parseFinalUsageDetailed(raw)
	return report.usage, report.ok
}

// parseFinalUsageDetailed is parseFinalUsage plus which of the two counts the
// usage object actually named, so a prompt-only report is not mistaken for a
// completion total of zero.
func parseFinalUsageDetailed(raw json.RawMessage) usageReport {
	if len(raw) == 0 {
		return usageReport{}
	}
	var usage map[string]json.RawMessage
	if err := json.Unmarshal(raw, &usage); err != nil || usage == nil {
		return usageReport{}
	}
	readCount := func(key string) (int64, bool) {
		value, ok := usage[key]
		if !ok {
			return 0, false
		}
		var count int64
		if json.Unmarshal(value, &count) != nil || count < 0 {
			return 0, false
		}
		return count, true
	}
	var result TokenUsage
	prompt, hasPrompt := readCount("prompt_tokens")
	if !hasPrompt {
		prompt, hasPrompt = readCount("input_tokens")
	}
	completion, hasCompletion := readCount("completion_tokens")
	if !hasCompletion {
		completion, hasCompletion = readCount("output_tokens")
	}
	result.Prompt, result.Completion = prompt, completion
	result.PromptSeen = hasPrompt
	if hasPrompt || hasCompletion {
		return usageReport{usage: result, seen: usageSeen{prompt: hasPrompt, completion: hasCompletion}, ok: true}
	}
	// Preserve the historical signal for a valid usage object with no known
	// token fields; malformed recognized fields, by contrast, are ignored.
	_, promptRecognized := usage["prompt_tokens"]
	_, inputRecognized := usage["input_tokens"]
	_, completionRecognized := usage["completion_tokens"]
	_, outputRecognized := usage["output_tokens"]
	if promptRecognized || inputRecognized || completionRecognized || outputRecognized {
		return usageReport{}
	}
	return usageReport{usage: result, ok: true}
}

// streamCodex runs the ChatGPT/Codex (OpenAI Responses wire) turn: it
// refreshes the login when needed, posts to base+"/responses", retries once
// after a forced refresh on HTTP 401, and parses the SSE stream. The three
// x-codex-primary-* rate-limit headers of the last response are captured for
// Usage, and the terminal event's token usage for LastTokenUsage.
func (c *Client) streamCodex(ctx context.Context, messages []Message, tools []ToolDefinition, onText func(string), onReasoning func(string), requestID uint64) (Message, error) {
	var result Message
	token, err := c.oauth.access(ctx, false)
	if err != nil {
		return result, err
	}
	payload, err := BuildCodexRequest(c.model, messages, tools)
	if err != nil {
		return result, err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
		if err != nil {
			return result, fmt.Errorf("create model request: %w", err)
		}
		c.setCodexHeaders(req, token)
		resp, err := c.http.Do(req)
		if err != nil {
			return result, fmt.Errorf("model request: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			detail, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes+1))
			resp.Body.Close()
			if readErr != nil {
				return result, fmt.Errorf("model HTTP %s (reading error: %v)", resp.Status, readErr)
			}
			if len(detail) > maxErrorBytes {
				detail = detail[:maxErrorBytes]
			}
			if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
				// The access token may have been revoked mid-flight: force
				// one refresh and retry the request exactly once.
				token, err = c.oauth.access(ctx, true)
				if err != nil {
					return result, err
				}
				continue
			}
			message := fmt.Sprintf("model HTTP %s: %s", resp.Status, strings.TrimSpace(string(detail)))
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return result, fmt.Errorf("%w: %s", ErrUnauthorized, message)
			}
			return result, errors.New(message)
		}
		c.captureRateLimit(resp.Header)
		mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil || mediaType != "text/event-stream" {
			resp.Body.Close()
			return result, fmt.Errorf("model returned incompatible content type %q (expected SSE)", resp.Header.Get("Content-Type"))
		}
		message, report, err := consumeCodexStreamDetailed(ctx, io.LimitReader(resp.Body, maxResponseBytes+1), onText, onReasoning)
		resp.Body.Close()
		if err == nil {
			c.setTokenUsage(requestID, report)
			c.markConnected()
		}
		return message, err
	}
}

// setCodexHeaders applies the ChatGPT/Codex request headers.
func (c *Client) setCodexHeaders(req *http.Request, token string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("originator", ChatGPTOriginator)
	if id := c.oauth.accountID(); id != "" {
		req.Header.Set("ChatGPT-Account-Id", id)
	}
	if c.sessionHeader != "" && c.sessionID != "" {
		req.Header.Set(c.sessionHeader, c.sessionID)
	}
}

// captureRateLimit records the last-seen Codex rate-limit response headers.
func (c *Client) captureRateLimit(header http.Header) {
	c.rateMu.Lock()
	c.rateUsedPercent = strings.TrimSpace(header.Get("x-codex-primary-used-percent"))
	c.rateWindowMinutes = strings.TrimSpace(header.Get("x-codex-primary-window-minutes"))
	c.rateResetAt = strings.TrimSpace(header.Get("x-codex-primary-reset-at"))
	c.rateMu.Unlock()
}

// Usage returns a short rate-limit summary from the last Codex response's
// x-codex-primary-* headers, or "" when the backend reported none.
func (c *Client) Usage() string {
	c.rateMu.Lock()
	percent, minutes := c.rateUsedPercent, c.rateWindowMinutes
	c.rateMu.Unlock()
	if _, err := strconv.ParseFloat(percent, 64); err != nil {
		percent = ""
	}
	window, err := strconv.Atoi(minutes)
	hasWindow := err == nil && window > 0
	switch {
	case percent != "" && hasWindow:
		return fmt.Sprintf("%s%% of %s window used", percent, formatWindowHours(window))
	case percent != "":
		return percent + "% used"
	default:
		return ""
	}
}

// beginTokenUsage clears the previous request's usage and returns an ID that
// prevents an older concurrent response from replacing this request's state.
func (c *Client) beginTokenUsage() uint64 {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	c.tokenRequest++
	c.tokenPrompt, c.tokenCompletion, c.tokenSeen, c.tokenPromptSeen, c.tokenCompletionSeen = 0, 0, false, false, false
	return c.tokenRequest
}

// setTokenUsage records the token usage of the last completed response:
// report.ok=true with the reported totals, or report.ok=false when the
// response carried no usage (the last response alone is reported, never a
// running total). Zero totals are valid usage (ok = true).
func (c *Client) setTokenUsage(requestID uint64, report usageReport) {
	c.rateMu.Lock()
	if requestID != c.tokenRequest {
		c.rateMu.Unlock()
		return
	}
	c.tokenPrompt, c.tokenCompletion, c.tokenSeen = report.usage.Prompt, report.usage.Completion, report.ok
	c.tokenPromptSeen, c.tokenCompletionSeen = report.usage.PromptSeen, report.ok && report.seen.completion
	c.rateMu.Unlock()
}

// LastTokenUsage reports the token usage of the last response: the final
// usage-only event of a chat-completions stream, or the usage of a Codex
// Responses terminal event. usage=false means the last response carried no
// usage (or there has been no response at all); it is never cumulative
// across turns.
func (c *Client) LastTokenUsage() (TokenUsage, bool) {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if !c.tokenSeen {
		return TokenUsage{}, false
	}
	return TokenUsage{Prompt: c.tokenPrompt, Completion: c.tokenCompletion, PromptSeen: c.tokenPromptSeen}, true
}

// formatWindowHours renders a window length in minutes as hours ("5h",
// "1.5h").
func formatWindowHours(minutes int) string {
	if minutes%60 == 0 {
		return fmt.Sprintf("%dh", minutes/60)
	}
	return strconv.FormatFloat(float64(minutes)/60, 'f', -1, 64) + "h"
}

// Connection-check failure classes. Errors.Is distinguishes an unreachable
// endpoint, rejected credentials, and an unexpected response shape.
var (
	ErrUnreachable        = errors.New("model endpoint unreachable")
	ErrUnauthorized       = errors.New("model endpoint rejected the credentials")
	ErrUnexpectedResponse = errors.New("model endpoint returned an unexpected response")
)

// Check performs one cheap, read-only call (the model list) to verify the
// endpoint answers and the credentials are valid. It never sends conversation
// content.
func (c *Client) Check(ctx context.Context) error {
	if c == nil {
		return errors.New("model client is nil")
	}
	if c.oauth != nil {
		// The Codex backend has no OpenAI-shaped model list, so the check is
		// only that the login is valid (refreshing it when stale). No
		// conversation content is ever sent.
		_, err := c.oauth.access(ctx, false)
		return err
	}
	raw, err := fetchModelList(ctx, c.base, c.apiKey)
	if err != nil {
		return err
	}
	if _, err := decodeModelIDs(raw); err != nil {
		return fmt.Errorf("%w: response is not a model list", ErrUnexpectedResponse)
	}
	return nil
}

// ListModels returns the model IDs the endpoint reports on its /models route.
// It accepts both the OpenAI data envelope and a bare array of model objects;
// Likha uses the result for setup, connection checks, and model selection.
func ListModels(ctx context.Context, endpoint, apiKey string) ([]string, error) {
	base, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedResponse, err)
	}
	raw, err := fetchModelList(ctx, base, apiKey)
	if err != nil {
		return nil, err
	}
	ids, err := decodeModelIDs(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: response is not a model list", ErrUnexpectedResponse)
	}
	return ids, nil
}

// ModelDetails describes a model reported by an endpoint. ContextWindow is
// zero when the model list has no supported positive context-window metadata.
type ModelDetails struct {
	ID                  string
	ContextWindow       int64
	ContextWindowSource string // response field that supplied ContextWindow; empty when unknown
}

// ListModelsWithDetails returns model IDs and any positive context-window
// metadata exposed by the endpoint's /models route. It accepts both the
// OpenAI data envelope and a bare array of model objects.
func ListModelsWithDetails(ctx context.Context, endpoint, apiKey string) ([]ModelDetails, error) {
	base, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedResponse, err)
	}
	raw, err := fetchModelList(ctx, base, apiKey)
	if err != nil {
		return nil, err
	}
	details, err := decodeModelDetails(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: response is not a model list", ErrUnexpectedResponse)
	}
	return details, nil
}

// decodeModelDetails accepts the same model-list envelopes as decodeModelIDs.
// It reads documented context metadata used by compatible model registries:
// OpenRouter's context_length, OMP's contextWindow, and OpenCode's limit.context.
func decodeModelDetails(raw []byte) ([]ModelDetails, error) {
	type item struct {
		ID                 string          `json:"id"`
		ContextWindow      json.RawMessage `json:"context_window"`
		ContextLength      json.RawMessage `json:"context_length"`
		ContextWindowCamel json.RawMessage `json:"contextWindow"`
		Limit              json.RawMessage `json:"limit"`
	}
	var items []item
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty model list")
	}
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, err
		}
	case '{':
		var envelope struct {
			Object string          `json:"object"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(trimmed, &envelope); err != nil {
			return nil, err
		}
		if len(envelope.Data) == 0 {
			if envelope.Object == "" {
				return nil, errors.New("missing model-list data")
			}
		} else if err := json.Unmarshal(envelope.Data, &items); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unexpected model-list JSON type")
	}
	details := make([]ModelDetails, 0, len(items))
	for _, entry := range items {
		if entry.ID == "" {
			continue
		}
		window, source := positiveWindow(entry.ContextWindow), ""
		if window > 0 {
			source = "context_window"
		} else if window = positiveWindow(entry.ContextLength); window > 0 {
			source = "context_length"
		} else if window = positiveWindow(entry.ContextWindowCamel); window > 0 {
			source = "contextWindow"
		}
		if window == 0 && len(entry.Limit) > 0 {
			var limit struct {
				Context json.RawMessage `json:"context"`
			}
			if json.Unmarshal(entry.Limit, &limit) == nil {
				window = positiveWindow(limit.Context)
				if window > 0 {
					source = "limit.context"
				}
			}
		}
		details = append(details, ModelDetails{ID: entry.ID, ContextWindow: window, ContextWindowSource: source})
	}
	return details, nil
}

func positiveWindow(candidates ...json.RawMessage) int64 {
	for _, candidate := range candidates {
		if len(candidate) == 0 {
			continue
		}
		var window int64
		if json.Unmarshal(candidate, &window) == nil && window > 0 {
			return window
		}
	}
	return 0
}

// decodeModelIDs accepts the OpenAI model-list envelope and a bare array of
// model objects. Some documented OpenAI-compatible providers return the latter
// even though their SDK schema describes the former.
func decodeModelIDs(raw []byte) ([]string, error) {
	type item struct {
		ID string `json:"id"`
	}
	var items []item
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty model list")
	}
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, err
		}
	case '{':
		var envelope struct {
			Object string          `json:"object"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(trimmed, &envelope); err != nil {
			return nil, err
		}
		if len(envelope.Data) == 0 {
			if envelope.Object == "" {
				return nil, errors.New("missing model-list data")
			}
		} else if err := json.Unmarshal(envelope.Data, &items); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unexpected model-list JSON type")
	}
	ids := make([]string, 0, len(items))
	for _, entry := range items {
		if entry.ID != "" {
			ids = append(ids, entry.ID)
		}
	}
	return ids, nil
}

// fetchModelList performs the read-only model-list request and classifies
// transport, authorization, and response-shape failures.
func fetchModelList(ctx context.Context, base, apiKey string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := sharedListClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: HTTP %s", ErrUnauthorized, resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: HTTP %s", ErrUnexpectedResponse, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: reading model list: %v", ErrUnexpectedResponse, err)
	}
	return body, nil
}

// EnsureConnected reports the connection state, probing once with a short
// deadline when this client has not yet answered a check or stream. A failed
// check is not remembered, so a later run probes again.
func (c *Client) EnsureConnected(ctx context.Context) error {
	if c == nil {
		return errors.New("model client is nil")
	}
	c.mu.Lock()
	checked := c.checked
	c.mu.Unlock()
	if checked {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Check(checkCtx); err != nil {
		return err
	}
	c.markConnected()
	return nil
}

func (c *Client) markConnected() {
	c.mu.Lock()
	c.checked = true
	c.mu.Unlock()
}

type streamChunk struct {
	Choices []struct {
		Delta *struct {
			Content   *string `json:"content"`
			Reasoning *string `json:"reasoning"`
			// DeepSeek-style reasoning field used by some providers.
			ReasoningContent *string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function *struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
	Error json.RawMessage `json:"error"`
}
type streamedToolCall struct {
	id, name, arguments strings.Builder
}

// appendPiece adds one streamed id or name piece, skipping a piece that
// repeats the whole value accumulated so far.
func appendPiece(value *strings.Builder, piece string) {
	if piece == "" || piece == value.String() {
		return
	}
	value.WriteString(piece)
}

func consumeStream(ctx context.Context, reader io.Reader, onText func(string), onReasoning func(string)) (Message, usageReport, error) {
	result := Message{Role: "assistant"}
	var lastUsage usageReport
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxEventLineBytes)
	calls := make(map[int]*streamedToolCall)
	lastCall, nextCall := -1, 0
	var text, reasoning, data strings.Builder
	var seenChoice, done bool
	process := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := data.String()
		data.Reset()
		if payload == "[DONE]" {
			done = true
			return nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("malformed model stream event: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return fmt.Errorf("model stream error: %s", chunk.Error)
		}
		if report := parseFinalUsageDetailed(chunk.Usage); report.ok {
			lastUsage = report
		}
		if len(chunk.Choices) == 0 {
			// OpenAI-compatible servers may send a final usage-only event. Usage
			// is optional telemetry: even malformed usage must not fail a turn.
			if len(chunk.Usage) == 0 {
				return errors.New("model stream event has no choices")
			}
			return nil
		}
		if len(chunk.Choices) != 1 {
			return errors.New("model stream returned multiple choices")
		}
		seenChoice = true
		delta := chunk.Choices[0].Delta
		if delta == nil {
			return nil // finish_reason-only event
		}
		if delta.Content != nil {
			text.WriteString(*delta.Content)
			if onText != nil && *delta.Content != "" {
				onText(*delta.Content)
			}
		}
		var reasoningPiece string
		if delta.Reasoning != nil {
			reasoningPiece = *delta.Reasoning
		} else if delta.ReasoningContent != nil {
			reasoningPiece = *delta.ReasoningContent
		}
		if reasoningPiece != "" {
			reasoning.WriteString(reasoningPiece)
			if onReasoning != nil {
				onReasoning(reasoningPiece)
			}
		}
		for _, fragment := range delta.ToolCalls {
			// The reason names which rule the provider broke so a router's
			// upstream quirk is diagnosable from the transcript alone.
			if fragment.Type != "" && fragment.Type != "function" {
				kind := fragment.Type
				if len(kind) > 32 {
					kind = kind[:32]
				}
				return fmt.Errorf("invalid indexed function tool-call fragment: type %q is not \"function\"", kind)
			}
			var index int
			switch {
			case fragment.Index != nil && *fragment.Index < 0:
				return fmt.Errorf("invalid indexed function tool-call fragment: negative index %d", *fragment.Index)
			case fragment.Index != nil:
				index = *fragment.Index
			case fragment.ID != "" && (lastCall < 0 || calls[lastCall].id.String() != fragment.ID):
				// Some OpenAI-compatible gateways (and routers relaying such
				// upstreams) omit the index; a new call id opens the next call.
				index = nextCall
			case lastCall >= 0:
				// No index and no new id: a continuation of the latest call.
				index = lastCall
			default:
				return fmt.Errorf("invalid indexed function tool-call fragment: the provider omitted both the index and the call id (name present: %t)", fragment.Function != nil && fragment.Function.Name != "")
			}
			call := calls[index]
			if call == nil {
				call = &streamedToolCall{}
				calls[index] = call
			}
			lastCall = index
			nextCall = max(nextCall, index+1)
			// Gateways that repeat the full id or name on every chunk must not
			// double it; genuinely split pieces still concatenate.
			appendPiece(&call.id, fragment.ID)
			if fragment.Function != nil {
				appendPiece(&call.name, fragment.Function.Name)
				call.arguments.WriteString(fragment.Function.Arguments)
			}
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return Message{}, usageReport{}, err
		}
		line := scanner.Text()
		if line == "" {
			if err := process(); err != nil {
				return Message{}, usageReport{}, err
			}
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			part := strings.TrimPrefix(line, "data:")
			part = strings.TrimPrefix(part, " ")
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(part)
		} else if strings.HasPrefix(line, "event: error") {
			// Errors with a data payload are surfaced by process; otherwise fail below.
			if data.Len() == 0 {
				return Message{}, usageReport{}, errors.New("model stream reported an error")
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Message{}, usageReport{}, err
	}
	if err := scanner.Err(); err != nil {
		return Message{}, usageReport{}, fmt.Errorf("reading model stream (limit %d bytes, line limit %d bytes): %w", maxResponseBytes, maxEventLineBytes, err)
	}
	if !done && data.Len() > 0 {
		if err := process(); err != nil {
			return Message{}, usageReport{}, err
		}
	}
	if !done {
		return Message{}, usageReport{}, errors.New("model stream ended before [DONE] or exceeded response limit")
	}
	if !seenChoice {
		return Message{}, usageReport{}, errors.New("model stream contained no assistant response")
	}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		fragment := calls[index]
		call := ToolCall{ID: fragment.id.String(), Name: fragment.name.String(), Arguments: fragment.arguments.String()}
		if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
			return Message{}, usageReport{}, fmt.Errorf("incomplete structured tool call at index %d", index)
		}
		result.ToolCalls = append(result.ToolCalls, call)
	}
	result.Content = text.String()
	result.Reasoning = reasoning.String()
	return result, lastUsage, nil
}
