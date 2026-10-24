package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
)

// MergeOverlay recursively merges JSON objects and replaces arrays/scalars.
// Header names are case-insensitive. The result shares no maps with its inputs.
func MergeOverlay(base, override Overlay) Overlay {
	headers := make(map[string]string)
	for _, source := range []map[string]string{base.Headers, override.Headers} {
		keys := make([]string, 0, len(source))
		for key := range source {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			headers[strings.ToLower(key)] = source[key]
		}
	}
	return Overlay{Settings: mergeObjects(base.Settings, override.Settings), Body: mergeObjects(base.Body, override.Body), Headers: headers}
}

func mergeObjects(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))
	for key, value := range base {
		merged[key] = cloneJSON(value)
	}
	for key, value := range override {
		left, lok := merged[key].(map[string]any)
		right, rok := value.(map[string]any)
		if lok && rok {
			merged[key] = mergeObjects(left, right)
		} else {
			merged[key] = cloneJSON(value)
		}
	}
	return merged
}

func cloneJSON(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return mergeObjects(nil, value)
	case []any:
		copy := make([]any, len(value))
		for i, item := range value {
			copy[i] = cloneJSON(item)
		}
		return copy
	case []string:
		return append([]string(nil), value...)
	case json.RawMessage:
		return append(json.RawMessage(nil), value...)
	case map[string]string:
		copy := make(map[string]string, len(value))
		for key, item := range value {
			copy[key] = item
		}
		return copy
	default:
		return value
	}
}

// ValidateOverlay rejects controls that bypass authentication, routing, tool
// policy or conversation ownership. Diagnostics name fields, never their values.
func ValidateOverlay(overlay Overlay) error {
	seen := make(map[string]bool)
	for key, value := range overlay.Headers {
		name := strings.ToLower(key)
		if seen[name] {
			return errors.New("overlay contains duplicate case-insensitive headers")
		}
		seen[name] = true
		if key == "" || strings.ContainsAny(key, " \t\r\n:") || strings.ContainsAny(value, "\r\n") {
			return errors.New("overlay contains an invalid HTTP header")
		}
		switch name {
		case "authorization", "proxy-authorization", "x-api-key", "api-key", "x-goog-api-key", "x-amz-security-token", "cookie", "host", "content-type", "content-length", "accept", "x-opencode-session", "openai-organization", "openai-project", "chatgpt-account-id", "anthropic-version", "anthropic-beta":
			return fmt.Errorf("overlay header %s is managed by Likha", key)
		}
	}
	for key := range overlay.Body {
		switch strings.ToLower(key) {
		case "model", "messages", "input", "instructions", "system", "tools", "tool_choice", "stream", "stream_options", "store", "previous_response_id", "conversation", "conversation_id", "background", "session_id", "authorization", "api_key", "endpoint", "base_url", "headers":
			return fmt.Errorf("overlay body field %s is managed by Likha", key)
		}
	}
	for key := range overlay.Settings {
		switch key {
		case "reasoningEffort", "effort", "thinking", "thinkingConfig", "reasoning", "reasoningSummary", "include", "maxOutputTokens", "verbosity":
		default:
			return fmt.Errorf("unsupported variant setting %s", key)
		}
	}
	if _, err := json.Marshal(overlay); err != nil {
		return errors.New("overlay values must be JSON-compatible")
	}
	return nil
}

func integerSetting(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if !math.IsNaN(value) && !math.IsInf(value, 0) && value >= -1 && value < float64(math.MaxInt64) && value == math.Trunc(value) {
			return int64(value), true
		}
	case json.Number:
		n, err := value.Int64()
		return n, err == nil
	}
	return 0, false
}

func setOverlayHeaders(header http.Header, additions map[string]string) {
	for key, value := range additions {
		header.Set(key, value)
	}
}
