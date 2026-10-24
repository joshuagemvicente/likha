package model

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"likha/internal/model/catalog"
)

// anthropicVersion is the Messages API version header value (spec decision 2).
const anthropicVersion = "2023-06-01"

// defaultAnthropicMaxTokens is the output cap for a model neither the live
// list nor the catalog describes (spec decision 9).
const defaultAnthropicMaxTokens = 64000

// claudeAdaptiveModels lists the exact model IDs that accept adaptive
// thinking, from Anthropic's "Configuring thinking" table. It is the
// fallback when the live model list has not described a model; anything
// else, including Haiku 4.5, gets no thinking field.
var claudeAdaptiveModels = map[string]bool{
	"claude-opus-5-5":   true,
	"claude-fable-5-1":  true,
	"claude-fable-5":    true,
	"claude-sonnet-5-5": true,
	"claude-opus-4-7":   true,
	"claude-opus-5":     true,
	"claude-opus-4-8":   true,
	"claude-opus-4-6":   true,
	"claude-sonnet-5":   true,
	"claude-sonnet-4-6": true,
}

// anthropicModelInfo is what Anthropic's live model list says about one
// model that request building needs.
type anthropicModelInfo struct {
	maxTokens     int64 // published output limit; 0 when not reported
	adaptive      bool  // capabilities.thinking.types.adaptive.supported
	adaptiveKnown bool  // capabilities were reported
}

// anthropicModels caches the last live model list. Only the claude row's
// base reaches the native wire, so model IDs identify the same models for
// every client; any list fetch (startup check, setup, /models) refreshes it.
var anthropicModels = struct {
	mu   sync.Mutex
	byID map[string]anthropicModelInfo
}{byID: make(map[string]anthropicModelInfo)}

func rememberAnthropicModels(infos map[string]anthropicModelInfo) {
	anthropicModels.mu.Lock()
	defer anthropicModels.mu.Unlock()
	for id, info := range infos {
		anthropicModels.byID[id] = info
	}
}

func lookupAnthropicModel(id string) (anthropicModelInfo, bool) {
	anthropicModels.mu.Lock()
	defer anthropicModels.mu.Unlock()
	info, ok := anthropicModels.byID[id]
	return info, ok
}

// anthropicThinking picks the thinking mode for model: the live list's
// capability when known, else the bundled exact-ID table (spec decision 4).
func anthropicThinking(model string) anthropicThinkingMode {
	if info, ok := lookupAnthropicModel(model); ok && info.adaptiveKnown {
		if info.adaptive {
			return thinkingAdaptive
		}
		return thinkingNone
	}
	if claudeAdaptiveModels[model] {
		return thinkingAdaptive
	}
	return thinkingNone
}

// anthropicMaxTokens is the model's published output limit: the live list,
// else the bundled catalog, else 64,000 (spec decision 9).
func anthropicMaxTokens(model string) int64 {
	if info, ok := lookupAnthropicModel(model); ok && info.maxTokens > 0 {
		return info.maxTokens
	}
	if row, ok := catalog.Lookup("claude", model); ok && row.Output > 0 {
		return row.Output
	}
	return defaultAnthropicMaxTokens
}

// nativeAnthropic reports whether c speaks the Messages API.
func (c *Client) nativeAnthropic() bool {
	return c.oauth == nil && wireFor(c.base) == WireAnthropic
}

// setAnthropicHeaders sets the native authentication pair. No Bearer header
// is ever sent on this wire (spec decision 2).
func setAnthropicHeaders(header http.Header, apiKey string) {
	header.Set("anthropic-version", anthropicVersion)
	header.Set("User-Agent", UserAgent)
	if apiKey != "" {
		header.Set("x-api-key", apiKey)
	}
}

// streamAnthropic runs one Messages API request (specs/claude-native).
func (c *Client) streamAnthropic(ctx context.Context, messages []Message, tools []ToolDefinition, onText, onReasoning func(string), requestID uint64) (Message, usageReport, error) {
	source := c.ReplaySource()
	effort := ""
	if c.effortWire == EffortAnthropic {
		effort = c.Effort()
	}
	thinking, output := anthropicThinking(c.model), anthropicMaxTokens(c.model)
	if c.nativeSnapshot {
		thinking, output = c.nativeThinking, c.nativeOutput
	}
	request, err := buildAnthropicRequest(anthropicInput{
		Model:     c.model,
		Messages:  messages,
		Tools:     tools,
		Thinking:  thinking,
		MaxTokens: output,
		Effort:    effort,
		Replay:    c.replayFor,
	})
	if err != nil {
		return Message{}, usageReport{}, err
	}
	request.Body, err = c.variantPayload(request.Body)
	if err != nil {
		return Message{}, usageReport{}, err
	}
	sendCtx, watchBody, stopWatch := withStreamWatchdog(ctx, streamIdleTimeout)
	defer stopWatch()
	resp, err := c.postAnthropic(sendCtx, request)
	if err != nil {
		return Message{}, usageReport{}, err
	}
	resp.Body = watchBody(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Message{}, usageReport{}, c.rejectedVariant(resp.StatusCode, rejectedAnthropicRequest(readAnthropicHTTPError(resp), request))
	}
	responseID := strings.TrimSpace(resp.Header.Get("request-id"))
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return Message{}, usageReport{}, withRequestID(fmt.Errorf("model returned incompatible content type %q (expected SSE)", resp.Header.Get("Content-Type")), responseID)
	}
	response, err := consumeAnthropicStream(ctx, io.LimitReader(resp.Body, maxResponseBytes+1), onText, onReasoning)
	if err != nil {
		return Message{}, usageReport{}, withRequestID(err, responseID)
	}
	message := response.message
	message.Replay, err = NewReasoningReplay(source, nil, nil, response.blocks)
	if err != nil {
		return Message{}, usageReport{}, fmt.Errorf("record Claude replay state: %w", err)
	}
	c.setTokenUsage(requestID, response.usage)
	c.markConnected()
	return message, response.usage, nil
}

func (c *Client) postAnthropic(ctx context.Context, request anthropicRequest) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(request.Body))
	if err != nil {
		return nil, fmt.Errorf("create model request: %w", err)
	}
	setAnthropicHeaders(req.Header, c.apiKey)
	setOverlayHeaders(req.Header, c.variantHeaders)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if request.Beta != "" {
		req.Header.Set("anthropic-beta", request.Beta)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model request: %w", err)
	}
	return resp, nil
}

// rejectedAnthropicRequest adds the hints a 400 may need (spec point 27): a
// request that carried replayed thinking may have been rejected for it
// (/compact hint), and one that carried effort for that (effort hint). When
// both apply, the effort error wraps the replay error so errors.As finds
// both.
func rejectedAnthropicRequest(apiErr *anthropicAPIError, request anthropicRequest) error {
	if !effortRejectedStatus(apiErr.Status) {
		return apiErr
	}
	var err error = apiErr
	if request.Replayed > 0 {
		err = &ReplayRejectedError{Sent: request.Replayed, Err: err}
	}
	if request.Effort != "" {
		err = &EffortRejectedError{Level: request.Effort, Err: err}
	}
	return err
}
