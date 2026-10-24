package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"likha/internal/model/catalog"
)

// maxAnthropicModelPages bounds model-list pagination. With 1,000 models per
// page a longer list means a misbehaving server, not a real catalog.
const maxAnthropicModelPages = 10

// anthropicEffortOrder is the documented effort levels, lowest first, the
// order the catalog and the effort step use.
var anthropicEffortOrder = []string{"low", "medium", "high", "xhigh", "max"}

// anthropicModelPage is one page of GET /v1/models.
type anthropicModelPage struct {
	Data    []anthropicModelEntry `json:"data"`
	HasMore bool                  `json:"has_more"`
	LastID  string                `json:"last_id"`
}

type anthropicModelEntry struct {
	ID             string          `json:"id"`
	DisplayName    string          `json:"display_name"`
	MaxInputTokens json.RawMessage `json:"max_input_tokens"`
	MaxTokens      json.RawMessage `json:"max_tokens"`
	Capabilities   *struct {
		Thinking *struct {
			Types *struct {
				Disabled *struct {
					Supported bool `json:"supported"`
				} `json:"disabled"`
				Adaptive *struct {
					Supported bool `json:"supported"`
				} `json:"adaptive"`
			} `json:"types"`
		} `json:"thinking"`
		// Effort holds a "supported" flag plus one {"supported": bool}
		// object (or null) per level, so values are decoded per key.
		Effort map[string]json.RawMessage `json:"effort"`
	} `json:"capabilities"`
}

// fetchAnthropicModels lists every model the key can use, following
// has_more/last_id pages (spec points 23–25), and remembers each model's
// output limit and thinking support for request building. An empty list is
// an error; Likha never substitutes a bundled list.
func fetchAnthropicModels(ctx context.Context, client *http.Client, base, apiKey string) ([]ModelDetails, error) {
	var details []ModelDetails
	infos := make(map[string]anthropicModelInfo)
	afterID := ""
	for page := 0; page < maxAnthropicModelPages; page++ {
		current, err := fetchAnthropicModelPage(ctx, client, base, apiKey, afterID)
		if err != nil {
			return nil, err
		}
		for _, entry := range current.Data {
			if entry.ID == "" {
				continue
			}
			details = append(details, entry.details())
			infos[entry.ID] = entry.info()
		}
		if !current.HasMore {
			if len(details) == 0 {
				return nil, fmt.Errorf("%w: the model list is empty", ErrUnexpectedResponse)
			}
			rememberAnthropicModels(infos)
			return details, nil
		}
		if current.LastID == "" {
			return nil, fmt.Errorf("%w: model list page has more results but no last_id", ErrUnexpectedResponse)
		}
		afterID = current.LastID
	}
	return nil, fmt.Errorf("%w: model list did not end within %d pages", ErrUnexpectedResponse, maxAnthropicModelPages)
}

// anthropicModelIDs is ListModels for the native wire.
func anthropicModelIDs(ctx context.Context, base, apiKey string) ([]string, error) {
	details, err := fetchAnthropicModels(ctx, sharedListClient, base, apiKey)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(details))
	for _, detail := range details {
		ids = append(ids, detail.ID)
	}
	return ids, nil
}

func fetchAnthropicModelPage(ctx context.Context, client *http.Client, base, apiKey, afterID string) (anthropicModelPage, error) {
	query := url.Values{"limit": {"1000"}}
	if afterID != "" {
		query.Set("after_id", afterID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models?"+query.Encode(), nil)
	if err != nil {
		return anthropicModelPage{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	setAnthropicHeaders(req.Header, apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return anthropicModelPage{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := readAnthropicHTTPError(resp)
		if errors.Is(apiErr, ErrUnauthorized) {
			return anthropicModelPage{}, apiErr
		}
		return anthropicModelPage{}, fmt.Errorf("%w: %w", ErrUnexpectedResponse, apiErr)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return anthropicModelPage{}, fmt.Errorf("%w: reading model list: %w", ErrUnexpectedResponse, err)
	}
	var page anthropicModelPage
	if err := json.Unmarshal(body, &page); err != nil || page.Data == nil {
		return anthropicModelPage{}, fmt.Errorf("%w: response is not a model list", ErrUnexpectedResponse)
	}
	return page, nil
}

// details maps one entry to the shared model metadata. Anthropic publishes
// no default effort level.
func (e anthropicModelEntry) details() ModelDetails {
	detail := ModelDetails{ID: e.ID, DisplayName: e.DisplayName, Efforts: e.efforts()}
	if e.Capabilities != nil {
		info := e.info()
		detail.Reasoning = &ReasoningCapabilities{EffortsKnown: e.Capabilities.Effort != nil, Efforts: detail.Efforts, Output: info.maxTokens}
		if info.adaptiveKnown {
			detail.Reasoning.Adaptive = &info.adaptive
		}
		if thinking := e.Capabilities.Thinking; thinking != nil && thinking.Types != nil && thinking.Types.Disabled != nil {
			detail.Reasoning.Toggle = &thinking.Types.Disabled.Supported
		}
	}
	if window := positiveWindow(e.MaxInputTokens); window > 0 {
		detail.ContextWindow, detail.ContextWindowSource = window, "max_input_tokens"
	}
	return detail
}

func (e anthropicModelEntry) info() anthropicModelInfo {
	info := anthropicModelInfo{maxTokens: positiveWindow(e.MaxTokens)}
	if e.Capabilities != nil {
		thinking := e.Capabilities.Thinking
		info.adaptiveKnown = thinking != nil && thinking.Types != nil && thinking.Types.Adaptive != nil
		info.adaptive = thinking != nil && thinking.Types != nil && thinking.Types.Adaptive != nil && thinking.Types.Adaptive.Supported
	}
	return info
}

// efforts returns the supported levels lowest first, or nil when the model
// reports none or the list fails the catalog's level validation.
func (e anthropicModelEntry) efforts() []string {
	if e.Capabilities == nil {
		return nil
	}
	var levels []string
	for _, level := range anthropicEffortOrder {
		var flag struct {
			Supported bool `json:"supported"`
		}
		raw := e.Capabilities.Effort[level]
		if len(raw) == 0 || json.Unmarshal(raw, &flag) != nil || !flag.Supported {
			continue
		}
		levels = append(levels, level)
	}
	if len(levels) == 0 || catalog.ValidateEfforts(levels) != nil {
		return nil
	}
	return levels
}
