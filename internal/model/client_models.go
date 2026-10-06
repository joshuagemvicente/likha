package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ModelDetail is a singular alias for the existing metadata type.
type ModelDetail = ModelDetails

// Models returns the active provider's model inventory. For ChatGPT it always
// fetches the selected account's authenticated public catalog; an empty or
// incompatible response is an error, never a reason to offer a bundled list.
func (c *Client) Models(ctx context.Context) ([]ModelDetails, error) {
	if c == nil {
		return nil, errors.New("model client is nil")
	}
	if c.oauth == nil {
		return ListModelsWithDetails(ctx, c.base, c.apiKey)
	}
	requestCtx, cancel := c.oauth.requestContext(ctx)
	defer cancel()
	resp, err := c.doOAuthRequest(requestCtx, http.MethodGet, "/models", nil, "application/json")
	if err != nil {
		return nil, fmt.Errorf("ChatGPT model discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := oauthHTTPError(resp)
		if errors.Is(err, ErrUnauthorized) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrUnexpectedResponse, err)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading ChatGPT model catalog: %w", ErrUnexpectedResponse, err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("%w: ChatGPT model catalog exceeded the response limit", ErrUnexpectedResponse)
	}
	details, err := decodeChatGPTModelDetails(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: ChatGPT model discovery failed: %w", ErrUnexpectedResponse, err)
	}
	return details, nil
}

// SIWC documents only slug, display_name, visibility, and server ordering.
// Context metadata is deliberately unknown rather than guessed from a field
// belonging to a different provider's model-list protocol.
func decodeChatGPTModelDetails(raw []byte) ([]ModelDetails, error) {
	var envelope struct {
		Models json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("invalid public model catalog; retry model discovery: %w", err)
	}
	models := bytes.TrimSpace(envelope.Models)
	if len(models) == 0 || models[0] != '[' {
		return nil, errors.New("expected the public ChatGPT models[] catalog; retry model discovery for the selected account")
	}
	var items []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Visibility  string `json:"visibility"`
	}
	if err := json.Unmarshal(models, &items); err != nil {
		return nil, fmt.Errorf("invalid ChatGPT models[] catalog; retry model discovery: %w", err)
	}
	details := make([]ModelDetails, 0, len(items))
	for _, item := range items {
		if item.Visibility != "list" {
			continue
		}
		if strings.TrimSpace(item.Slug) == "" || strings.TrimSpace(item.DisplayName) == "" {
			return nil, errors.New("a listed ChatGPT model is missing its slug or display_name; retry model discovery")
		}
		details = append(details, ModelDetails{ID: item.Slug, DisplayName: item.DisplayName})
	}
	if len(details) == 0 {
		return nil, errors.New("no models are available to list for the selected ChatGPT account; check its plan permission or choose another account")
	}
	return details, nil
}
