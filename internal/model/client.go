// Package model connects Lisa to an OpenAI-compatible chat endpoint, local or
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

// UserAgent identifies Lisa to providers that inspect client identity
// (OpenCode Go requires an agent user agent instead of a generic HTTP-library
// name). Run overwrites the version from the build tag.
var UserAgent = "lisa/dev"

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

// SetSessionHeader names the header a provider requires for per-conversation
// routing (e.g. OpenCode's x-opencode-session).
func (c *Client) SetSessionHeader(name string) {
	c.sessionHeader = name
}

// SetSession records the stable per-conversation session identifier sent in
// the provider's session header. Lisa uses its own SQLite session ID.
func (c *Client) SetSession(id string) {
	c.sessionID = id
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

// Stream returns the complete assistant turn after [DONE], streaming only text to
// onText. A partial turn is never returned as a successful answer.
func (c *Client) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onText func(string)) (Message, error) {
	var result Message
	if c == nil {
		return result, errors.New("model client is nil")
	}
	body := struct {
		Model    string           `json:"model"`
		Messages []requestMessage `json:"messages"`
		Tools    []requestTool    `json:"tools,omitempty"`
		Stream   bool             `json:"stream"`
	}{Model: c.model, Messages: make([]requestMessage, 0, len(messages)), Stream: true}
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
	payload, err := json.Marshal(body)
	if err != nil {
		return result, fmt.Errorf("encode model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return result, fmt.Errorf("create model request: %w", err)
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
	resp, err := c.http.Do(req)
	if err != nil {
		return result, fmt.Errorf("model request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes+1))
		if readErr != nil {
			return result, fmt.Errorf("model HTTP %s (reading error: %v)", resp.Status, readErr)
		}
		if len(detail) > maxErrorBytes {
			detail = detail[:maxErrorBytes]
		}
		message := fmt.Sprintf("model HTTP %s: %s", resp.Status, strings.TrimSpace(string(detail)))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return result, fmt.Errorf("%w: %s", ErrUnauthorized, message)
		}
		return result, errors.New(message)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return result, fmt.Errorf("model returned incompatible content type %q (expected SSE)", resp.Header.Get("Content-Type"))
	}
	message, err := consumeStream(ctx, io.LimitReader(resp.Body, maxResponseBytes+1), onText)
	if err == nil {
		c.markConnected()
	}
	return message, err
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
	raw, err := fetchModelList(ctx, c.base, c.apiKey)
	if err != nil {
		return err
	}
	var list struct {
		Data   json.RawMessage `json:"data"`
		Object string          `json:"object"`
	}
	if json.Unmarshal(raw, &list) != nil || len(list.Data) == 0 && list.Object == "" {
		return fmt.Errorf("%w: response is not a model list", ErrUnexpectedResponse)
	}
	return nil
}

// ListModels returns the model IDs the endpoint reports on its /models route.
// OpenAI-compatible local servers and hosted providers expose this list; Lisa
// uses it to run without an explicit model name.
func ListModels(ctx context.Context, endpoint, apiKey string) ([]string, error) {
	base, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedResponse, err)
	}
	raw, err := fetchModelList(ctx, base, apiKey)
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return nil, fmt.Errorf("%w: response is not a model list", ErrUnexpectedResponse)
	}
	ids := make([]string, 0, len(list.Data))
	for _, item := range list.Data {
		if item.ID != "" {
			ids = append(ids, item.ID)
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
	resp, err := noRedirectHTTPClient().Do(req)
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
			ToolCalls []struct {
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
	Error json.RawMessage `json:"error"`
}
type streamedToolCall struct {
	id, name, arguments strings.Builder
}

func consumeStream(ctx context.Context, reader io.Reader, onText func(string)) (Message, error) {
	result := Message{Role: "assistant"}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxEventLineBytes)
	calls := make(map[int]*streamedToolCall)
	var text, data strings.Builder
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
		if len(chunk.Choices) == 0 {
			// OpenAI-compatible servers may send a final usage-only event.
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
				return fmt.Errorf("malformed model stream event: %w", err)
			}
			if _, ok := envelope["usage"]; !ok || !seenChoice {
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
		for _, fragment := range delta.ToolCalls {
			if fragment.Index == nil || *fragment.Index < 0 || fragment.Type != "" && fragment.Type != "function" {
				return errors.New("invalid indexed function tool-call fragment")
			}
			call := calls[*fragment.Index]
			if call == nil {
				call = &streamedToolCall{}
				calls[*fragment.Index] = call
			}
			call.id.WriteString(fragment.ID)
			if fragment.Function != nil {
				call.name.WriteString(fragment.Function.Name)
				call.arguments.WriteString(fragment.Function.Arguments)
			}
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return Message{}, err
		}
		line := scanner.Text()
		if line == "" {
			if err := process(); err != nil {
				return Message{}, err
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
				return Message{}, errors.New("model stream reported an error")
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	if err := scanner.Err(); err != nil {
		return Message{}, fmt.Errorf("reading model stream (limit %d bytes, line limit %d bytes): %w", maxResponseBytes, maxEventLineBytes, err)
	}
	if !done && data.Len() > 0 {
		if err := process(); err != nil {
			return Message{}, err
		}
	}
	if !done {
		return Message{}, errors.New("model stream ended before [DONE] or exceeded response limit")
	}
	if !seenChoice {
		return Message{}, errors.New("model stream contained no assistant response")
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
			return Message{}, fmt.Errorf("incomplete structured tool call at index %d", index)
		}
		result.ToolCalls = append(result.ToolCalls, call)
	}
	result.Content = text.String()
	return result, nil
}
